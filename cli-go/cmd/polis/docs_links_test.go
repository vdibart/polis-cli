package main

// The docs link check.
//
// ⛔ ONE RULE: THE TARGET EXISTS IN THE PUBLIC TREE. This is not a docs linter.
// It reads no prose and has no opinion about style or content. It exists
// because a published doc path is permanent — signed posts, magic-link emails
// and polis.pub's own pages link into docs/, and none of those can be edited
// when a page moves. The last reorganisation left four live polis.pub links on
// GitHub 404s and nothing noticed.
//
// # The link forms
//
//   - L1: a relative Markdown link in any public *.md file — docs/, READMEs,
//     AGENTS.md, skills/ — resolves to a public file or directory, and its
//     #fragment to a heading in that page.
//   - L2: a repo-relative docs/… path cited outside docs/ (code, comments,
//     AGENTS.md, llms.txt) resolves. From public source it must resolve in the
//     public tree; from closed source (present only in the private repo) it may
//     also name internal docs.
//   - L3: a github.com/vdibart/polis-cli/blob/main/… URL anywhere resolves,
//     fragment included. These are the links polis.pub serves to strangers.
//   - L4: every docs path published at v0.66.0 still exists, as a page or as a
//     tombstone. Deleting a tombstone is how a signed post's link breaks.
//   - L6: no public file names a path that exists here and never ships under
//     that name — a CLAUDE.md, or a script under cli-bash/bin/ — in a link,
//     a code span, a comment or help text. L1 and L3 catch these as links;
//     L6 catches the mention a reader cannot follow either.
//   - L5: the orientation line under each page's title (see docs_orientation_test.go).
//
// # "The public tree"
//
// ⛔ THIS FILE DOES NOT DEFINE "PUBLIC". scripts/release/public.map and
// private.map do, and the release sync reads the same two files through the
// same code (scripts/release/mapping.sh). The public tree is every file the
// mapping copies, at its PUBLIC path (cli-bash/bin/polis is cli-bash/polis,
// cli-bash/migrations/ is migrations/), plus the files the public repo owns
// and the ones the release generates. A target outside it counts as missing, so
// a public page linking into plans/ fails L1 without a rule of its own.
//
// In the public repo there is no mapping and no plans/: that tree IS the public
// tree, and it is checked as it stands. Here, a missing mapping fails, so the
// fallback cannot be reached by accident.

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode"
)

const repoRoot = "../../.."

// A page the release copies to the root (public.map: `generated <root> copy-of
// <page>`) is read from two directories, so a relative link cannot resolve from
// both. Decided: such a page links with absolute github.com/vdibart/polis-cli
// URLs, which L3 checks against the public tree and which also resolve for
// anyone reading the raw file.

// closedSources are read as L2/L3 sources where present, though they never ship:
// polis.pub serves links from them, and their comments point operators at docs.
var closedSources = []string{"webapp/internal/hosted/", "webapp/cmd/", "discovery-service/"}

// publishedAtV0660 is every docs/ path in polis-cli at v0.66.0 (L4). Never
// remove an entry: a published path is permanent.
var publishedAtV0660 = []string{
	"docs/README.md",
	"docs/api/README.md", "docs/api/developer/dispatch-engine.md", "docs/api/developer/reference.md",
	"docs/cli/README.md", "docs/cli/developer/packages.md", "docs/cli/user/command-reference.md",
	"docs/cli/user/json-mode.md", "docs/cli/user/policies.md", "docs/cli/user/templating.md",
	"docs/ds/README.md", "docs/ds/admin/configuration.md", "docs/ds/admin/deployment.md",
	"docs/ds/developer/api-reference.md", "docs/ds/developer/pql-json-api.md", "docs/ds/developer/storage-adapter.md",
	"docs/ds/developer/stream-architecture.md", "docs/ds/developer/unpublish-lifecycle.md",
	"docs/general/README.md", "docs/general/contributing.md", "docs/general/vision.md",
	"docs/general/concepts/actors.md", "docs/general/concepts/architecture.md", "docs/general/concepts/bundles.md",
	"docs/general/concepts/content-system.md", "docs/general/concepts/content-types.md",
	"docs/general/concepts/infinity-stream.md", "docs/general/concepts/shapes.md",
	"docs/general/concepts/snap-off-architecture.md", "docs/general/concepts/themes.md",
	"docs/general/reference/glossary.md", "docs/general/reference/in-defense-of-bless.md",
	"docs/general/reference/policy-grammar.md", "docs/general/reference/pql-golden.jsonl",
	"docs/general/reference/pql.md", "docs/general/reference/pql-vocabulary.json",
	"docs/general/security/SECURITY.md", "docs/general/security/dm-encryption.md",
	"docs/general/security/registration-and-privacy.md", "docs/general/security/security-model.md",
	"docs/handbook/README.md", "docs/handbook/dm-encryption.md", "docs/handbook/ds-to-stream.md",
	"docs/handbook/foreign-site-widget.md", "docs/handbook/stream-overview.md", "docs/handbook/url-as-filter.md",
	"docs/webapp/README.md", "docs/webapp/designer/README.md", "docs/webapp/designer/brand.md",
	"docs/webapp/designer/components.md", "docs/webapp/designer/decisions.md", "docs/webapp/designer/navigation.md",
	"docs/webapp/designer/pages.md", "docs/webapp/designer/theme-system.md",
	"docs/webapp/developer/development.md", "docs/webapp/developer/feed-architecture.md",
	"docs/webapp/user/user-manual.md",
}

// ---------------------------------------------------------------- the tree

// publicTree is the public tree, read from the mapping once per test binary.
type publicTree struct {
	src        map[string]string // public path -> the file here it is copied from; "" when owned or generated
	pub        map[string]string // the inverse, for copied files
	dirs       map[string]bool   // every public directory
	rootCopies map[string]string // a page here -> the public root path the release copies it to
	fromMap    bool              // false in the public repo, where the tree is taken as it stands
}

var (
	treeOnce sync.Once
	tree     publicTree
	treeErr  error
)

// theTree returns the public tree; a mapping that cannot be read fails every test that asks.
func theTree() publicTree {
	treeOnce.Do(func() { tree, treeErr = loadPublicTree(repoRoot) })
	if treeErr != nil {
		panic(treeErr)
	}
	return tree
}

func (pt *publicTree) add(pub, src string) {
	pt.src[pub] = src
	if src != "" {
		pt.pub[src] = pub
	}
	for d := path.Dir(pub); d != "."; d = path.Dir(d) {
		pt.dirs[d] = true
	}
}

func loadPublicTree(root string) (publicTree, error) {
	pt := publicTree{src: map[string]string{}, pub: map[string]string{}, dirs: map[string]bool{}, rootCopies: map[string]string{}}
	mapFile := filepath.Join(root, "scripts", "release", "public.map")
	if _, err := os.Stat(mapFile); err != nil {
		if _, err := os.Stat(filepath.Join(root, "plans")); err == nil {
			return pt, fmt.Errorf("scripts/release/public.map is missing, and plans/ exists: in the private repo the public tree is defined only by the mapping")
		}
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && d.Name() == ".git" {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				rel, _ := filepath.Rel(root, p)
				pt.add(filepath.ToSlash(rel), filepath.ToSlash(rel))
			}
			return nil
		})
		return pt, err
	}
	pt.fromMap = true
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "release", "mapping.sh"), "resolve", "--worktree")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return pt, fmt.Errorf("scripts/release/mapping.sh resolve: %v\n%s", err, stderr.String())
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Split(line, "\t"); len(f) == 3 && f[0] == "copy" {
			pt.add(f[2], f[1])
		}
	}
	data, err := os.ReadFile(mapFile)
	if err != nil {
		return pt, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || (f[0] != "owned" && f[0] != "generated") {
			continue
		}
		if strings.HasSuffix(f[1], "/") {
			pt.dirs[strings.TrimSuffix(f[1], "/")] = true
			continue
		}
		pt.add(f[1], "")
		if f[0] == "generated" && len(f) == 4 && f[2] == "copy-of" {
			pt.rootCopies[f[3]] = f[1]
		}
	}
	return pt, nil
}

// exists reports whether a repo-relative path names a file or directory. With
// public, the path is a PUBLIC path, looked up in the public tree.
func exists(rel string, public bool) bool {
	rel = path.Clean(rel)
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "../") {
		return false
	}
	if public {
		pt := theTree()
		_, file := pt.src[rel]
		return file || pt.dirs[rel]
	}
	_, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	return err == nil
}

// ---------------------------------------------------------------- markdown

var (
	fenceLine   = regexp.MustCompile("^\\s*(```|~~~)")
	codeSpan    = regexp.MustCompile("`[^`]*`")
	atxHeading  = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)
	htmlAnchor  = regexp.MustCompile(`<a\s+(?:name|id)="([^"]+)"`)
	mdLink      = regexp.MustCompile(`!?\[((?:[^\[\]]|\[[^\]]*\])*)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	refDef      = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*<?(\S+?)>?(?:\s+"[^"]*")?\s*$`)
	htmlTag     = regexp.MustCompile(`<[^>]+>`)
	emphasisRun = regexp.MustCompile(`[*~]`)
)

// proseLines returns each line outside code fences, with code spans blanked,
// numbered from 1. raw keeps the original text for headings.
type proseLine struct {
	N        int
	Raw, Txt string
}

func proseLines(md string) []proseLine {
	var out []proseLine
	in := false
	for i, l := range strings.Split(md, "\n") {
		if fenceLine.MatchString(l) {
			in = !in
			continue
		}
		if in {
			continue
		}
		out = append(out, proseLine{N: i + 1, Raw: l, Txt: codeSpan.ReplaceAllStringFunc(l, func(s string) string { return strings.Repeat(" ", len(s)) })})
	}
	return out
}

// paragraphs joins consecutive prose lines, so a link whose text wraps onto
// the next line is still seen. N is the paragraph's first line; Raw keeps the
// lines, Txt joins them with spaces.
func paragraphs(lines []proseLine) []proseLine {
	var out []proseLine
	for _, l := range lines {
		blank := strings.TrimSpace(l.Raw) == ""
		if n := len(out); n > 0 && !blank && strings.TrimSpace(out[n-1].Raw) != "" &&
			l.N == out[n-1].N+strings.Count(out[n-1].Raw, "\n")+1 {
			out[n-1].Raw += "\n" + l.Raw
			out[n-1].Txt += " " + l.Txt
			continue
		}
		out = append(out, l)
	}
	return out
}

// githubSlug is GitHub's heading anchor: rendered text, lowercased, everything
// but letters, marks, digits, connector punctuation, spaces and hyphens
// dropped, spaces to hyphens.
//
// ⚠️ GitHub ignores the `{#id}` heading-attribute syntax: it slugs the braces'
// contents into the anchor like any other text. A page that needs a short
// anchor puts <a id="…"></a> before the heading.
func githubSlug(heading string) string {
	// Code spans render their text verbatim, <url> included; outside them an
	// HTML tag and emphasis markers render as nothing.
	parts := strings.Split(mdLink.ReplaceAllString(heading, "$1"), "`")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = emphasisRun.ReplaceAllString(htmlTag.ReplaceAllString(parts[i], ""), "")
	}
	s := strings.ToLower(strings.Join(parts, ""))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '️' || r == '‍': // emoji presentation selector, joiner
		case r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Pc, r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

var anchorCache = map[string]map[string]bool{}

// anchors returns every fragment a page answers to.
func anchors(rel string) map[string]bool {
	if a, ok := anchorCache[rel]; ok {
		return a
	}
	a := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err == nil {
		seen := map[string]int{}
		lines := proseLines(string(data))
		for i, l := range lines {
			for _, m := range htmlAnchor.FindAllStringSubmatch(l.Raw, -1) {
				a[m[1]] = true
			}
			text := ""
			if m := atxHeading.FindStringSubmatch(l.Raw); m != nil && strings.HasPrefix(strings.TrimSpace(l.Raw), "#") {
				text = m[1]
			} else if i > 0 && lines[i-1].N == l.N-1 && strings.TrimSpace(lines[i-1].Raw) != "" &&
				regexp.MustCompile(`^=+\s*$`).MatchString(l.Raw) {
				text = lines[i-1].Raw
			}
			if text == "" {
				continue
			}
			s := githubSlug(text)
			if n := seen[s]; n > 0 {
				a[fmt.Sprintf("%s-%d", s, n)] = true
			} else {
				a[s] = true
			}
			seen[s]++
		}
	}
	anchorCache[rel] = a
	return a
}

// resolveTarget checks one link target, relative to the repo-relative dir it
// was written in. It returns "" when the target resolves.
func resolveTarget(fromDir, target string, public bool) string {
	target = strings.TrimSpace(target)
	if u, err := url.PathUnescape(target); err == nil {
		target = u
	}
	p, frag, _ := strings.Cut(target, "#")
	rel := path.Clean(path.Join(fromDir, p))
	if p == "" {
		rel = fromDir // an in-page anchor; fromDir is the page itself
	}
	if !exists(rel, public) {
		if exists(rel, false) {
			return "not in the public tree"
		}
		return "no such file"
	}
	page := rel
	if public {
		page = theTree().src[rel] // "" when the public repo owns the page: its headings are not here
	}
	if frag != "" && strings.HasSuffix(rel, ".md") && page != "" {
		if !anchors(page)[frag] {
			return "no heading #" + frag
		}
	}
	return ""
}

func isExternal(t string) bool {
	return regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`).MatchString(t) || strings.HasPrefix(t, "//")
}

// ---------------------------------------------------------------- walking

func walk(t *testing.T, root string, fn func(rel string)) {
	t.Helper()
	base := filepath.Join(repoRoot, filepath.FromSlash(root))
	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if rel != "." && (strings.HasPrefix(name, ".") && name != ".well-known" || name == "node_modules" || name == "testdata" && strings.HasPrefix(rel, "discovery-service")) {
				return filepath.SkipDir
			}
			return nil
		}
		fn(rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// publicDocs lists every public page under docs/ (docs/ maps to itself).
func publicDocs(t *testing.T) []string {
	var pages []string
	for _, p := range publicMarkdown(t) {
		if strings.HasPrefix(p.Pub, "docs/") {
			pages = append(pages, p.Src)
		}
	}
	if len(pages) == 0 {
		t.Fatal("no docs found — the check must not pass by reading nothing")
	}
	return pages
}

// page is a public Markdown file: its public path, and the file here it is read from.
type page struct{ Pub, Src string }

// publicMarkdown lists every public Markdown file, docs/ and everything else
// that ships. A changelog is historical and never rewritten.
func publicMarkdown(t *testing.T) []page {
	var pages []page
	docs := 0
	for pub, src := range theTree().src {
		if src != "" && strings.HasSuffix(pub, ".md") && !historicalSource(src) {
			pages = append(pages, page{pub, src})
			if strings.HasPrefix(pub, "docs/") {
				docs++
			}
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Pub < pages[j].Pub })
	if docs == 0 || len(pages) == docs {
		t.Fatalf("%d pages, %d under docs/ — the check must not pass by reading nothing", len(pages), docs)
	}
	return pages
}

var sourceExt = map[string]bool{
	".go": true, ".js": true, ".ts": true, ".sh": true, ".md": true, ".txt": true, ".html": true,
	".css": true, ".json": true, ".jsonl": true, ".yaml": true, ".yml": true, ".toml": true, ".py": true, ".tmpl": true,
}

func isSource(rel string) bool {
	base := path.Base(rel)
	if base == "Makefile" || strings.HasPrefix(rel, "cli-bash/bin/") {
		return true
	}
	return sourceExt[path.Ext(rel)]
}

// sources calls fn for every file read as an L2/L3 source, with its public
// path, or "" for closed source that never ships.
func sources(t *testing.T, fn func(rel, pub string)) {
	pt := theTree()
	walk(t, ".", func(rel string) {
		if !isSource(rel) || historicalSource(rel) {
			return
		}
		if pub, ok := pt.pub[rel]; ok {
			fn(rel, pub)
			return
		}
		if path.Base(rel) == "CLAUDE.md" {
			fn(rel, "") // never ships, and still cites docs
			return
		}
		for _, c := range closedSources {
			if strings.HasPrefix(rel, c) {
				fn(rel, "")
				return
			}
		}
	})
}

func readLines(t *testing.T, rel string) []string {
	f, err := os.Open(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// ---------------------------------------------------------------- the tests

// relativeLinks returns every relative link target in a Markdown document,
// with the line of the paragraph it is in.
func relativeLinks(md string) (targets []string, lines []int) {
	for _, l := range paragraphs(proseLines(md)) {
		for _, m := range mdLink.FindAllStringSubmatch(l.Txt, -1) {
			if !isExternal(m[2]) {
				targets, lines = append(targets, m[2]), append(lines, l.N)
			}
		}
		for _, raw := range strings.Split(l.Raw, "\n") {
			if m := refDef.FindStringSubmatch(raw); m != nil && !isExternal(m[1]) {
				targets, lines = append(targets, m[1]), append(lines, l.N)
			}
		}
	}
	return targets, lines
}

// TestDocsRelativeLinksResolve is L1.
func TestDocsRelativeLinksResolve(t *testing.T) {
	checked := 0
	rootCopies := theTree().rootCopies
	for _, pg := range publicMarkdown(t) {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(pg.Src)))
		if err != nil {
			t.Fatal(err)
		}
		targets, lines := relativeLinks(string(data))
		dirs := []string{path.Dir(pg.Pub)}
		if copy := rootCopies[pg.Src]; copy != "" {
			dirs = append(dirs, path.Dir(copy))
		}
		for i, target := range targets {
			for j, dir := range dirs {
				checked++
				if strings.HasPrefix(target, "#") {
					dir = pg.Pub
				}
				why := resolveTarget(dir, target, true)
				switch {
				case why == "":
				case j > 0:
					t.Errorf("%s:%d: link (%s) does not resolve from the root copy %s: %s — a page copied to the root links with absolute github.com/vdibart/polis-cli URLs",
						pg.Src, lines[i], target, rootCopies[pg.Src], why)
				default:
					t.Errorf("%s:%d: link (%s), read from %s/ in the public tree: %s", pg.Src, lines[i], target, dir, why)
				}
			}
		}
	}
	if checked < 100 {
		t.Fatalf("only %d relative links checked — the extractor is broken, not the docs clean", checked)
	}
}

var (
	docPathMention = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./-])((?:\.\./)*docs/[A-Za-z0-9_.#/-]*)`)
	githubURL      = regexp.MustCompile(`github\.com/vdibart/polis-cli/(?:blob|tree)/main/([A-Za-z0-9_.#/%-]+)`)
)

// cleanMention trims the punctuation prose leaves on the end of a path.
func cleanMention(m string) string {
	return strings.TrimRight(m, ".,:;)#-")
}

// looksLikePath keeps prose ("docs/comments") out of L2: a cited path names a
// file (an extension), a directory (a trailing slash), or is relative (../).
func looksLikePath(m string) bool {
	p, _, _ := strings.Cut(m, "#")
	return strings.HasPrefix(p, "../") || strings.HasSuffix(p, "/") || path.Ext(p) != ""
}

// awaitingApproval holds broken citations in files this project may not edit
// without its owner's approval (CLAUDE.md is the maintainer's). Each entry is
// a proposal on record; the test fails the moment the citation is fixed, so an
// entry cannot outlive its reason.
var awaitingApproval = map[string]string{}

var stillCited = map[string]bool{}

// historicalSources record paths as they were when written; a changelog entry
// is never rewritten. This file names private paths on purpose.
func historicalSource(rel string) bool {
	return path.Base(rel) == "CHANGELOG.md" || rel == "cli-go/cmd/polis/docs_links_test.go"
}

// TestDocPathsCitedOutsideDocsResolve is L2.
func TestDocPathsCitedOutsideDocsResolve(t *testing.T) {
	checked := 0
	sources(t, func(rel, pub string) {
		public := pub != ""
		if strings.HasPrefix(rel, "docs/") {
			return
		}
		for i, line := range readLines(t, rel) {
			for _, m := range docPathMention.FindAllStringSubmatch(line, -1) {
				mention := cleanMention(m[1])
				if !looksLikePath(mention) {
					continue
				}
				dir, here := "", path.Dir(rel)
				if public {
					here = path.Dir(pub) // a relative path is read where the file is published
				}
				if strings.HasPrefix(mention, "../") {
					dir = here
				}
				checked++
				why := resolveTarget(dir, mention, public)
				if why != "" && dir == "" && strings.HasSuffix(rel, ".md") {
					// A Markdown file's own relative link (discovery-service/README.md → docs/…).
					if resolveTarget(here, mention, public) == "" {
						why = ""
					}
				}
				if why != "" && awaitingApproval[rel+" → "+mention] != "" {
					stillCited[rel+" → "+mention] = true
					why = ""
				}
				if why != "" {
					t.Errorf("%s:%d: %s: %s", rel, i+1, mention, why)
				}
			}
		}
	})
	for k := range awaitingApproval {
		if !stillCited[k] {
			t.Errorf("awaitingApproval lists %q, which is no longer cited broken — remove the entry", k)
		}
	}
	if checked < 100 {
		t.Fatalf("only %d doc paths checked outside docs/ — the scan is broken, not the tree clean", checked)
	}
}

// TestPublishedGitHubLinksResolve is L3.
func TestPublishedGitHubLinksResolve(t *testing.T) {
	checked := 0
	sources(t, func(rel, _ string) {
		for i, line := range readLines(t, rel) {
			for _, m := range githubURL.FindAllStringSubmatch(line, -1) {
				target := cleanMention(m[1])
				checked++
				if why := resolveTarget("", target, true); why != "" {
					t.Errorf("%s:%d: github.com/…/main/%s: %s", rel, i+1, target, why)
				}
			}
		}
	})
	if checked < 5 {
		t.Fatalf("only %d GitHub links checked — the scan is broken", checked)
	}
}

var unshippedMention = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./-])((?:[A-Za-z0-9_.-]+/)*CLAUDE\.md|(?:[A-Za-z0-9_.-]+/)*cli-bash/bin(?:/[A-Za-z0-9_.-]*)?)`)

// namesItsOwnModel are the files that implement the public-tree model, and
// name the unshipped paths as data.
func namesItsOwnModel(rel string) bool {
	return rel == "cli-go/cmd/polis/docs_links_test.go" || rel == "cli-go/cmd/polis/source_cites_test.go"
}

// unshippedMentions returns every CLAUDE.md or cli-bash/bin/ path a line names.
func unshippedMentions(line string) []string {
	var out []string
	for _, m := range unshippedMention.FindAllStringSubmatch(line, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestPublishedSourceNamesNoUnshippedPath is L6.
func TestPublishedSourceNamesNoUnshippedPath(t *testing.T) {
	scanned := 0
	sources(t, func(rel, pub string) {
		if pub == "" || namesItsOwnModel(rel) {
			return
		}
		scanned++
		for i, line := range readLines(t, rel) {
			for _, m := range unshippedMentions(line) {
				t.Errorf("%s:%d: names %s, which the public repo does not have", rel, i+1, m)
			}
		}
	})
	if scanned < 500 {
		t.Fatalf("only %d public sources scanned — the walk is broken", scanned)
	}
}

// TestPublishedDocPathsStillExist is L4.
func TestPublishedDocPathsStillExist(t *testing.T) {
	for _, p := range publishedAtV0660 {
		if !exists(p, true) {
			t.Errorf("%s was published at v0.66.0 and no longer exists — a published path keeps a page or a tombstone, forever", p)
		}
	}
}

// TestDocsLinkCheckCatchesBreaks proves the check can fail.
func TestDocsLinkCheckCatchesBreaks(t *testing.T) {
	cases := []struct {
		from, target string
		public, ok   bool
	}{
		{"docs", "README.md", true, true},
		{"docs", "no-such-page.md", true, false},
		{"docs/general/security", "security-model.md#4-signature-model", true, true},
		{"docs/general/security", "security-model.md#no-such-heading", true, false},
		{"docs", "../archive/manifesto.md", true, false},
		{"docs", ".internal/information-architecture.md", true, false},
		{"docs", ".internal/information-architecture.md", false, true},
		{"docs", "../LICENSE", true, true},
		{"", "webapp/internal/hosted/llms.txt", true, false},
		// Every CLAUDE.md stays here, at any depth.
		{"docs/webapp", "../../webapp/CLAUDE.md", true, false},
		{"", "CLAUDE.md", true, false},
		{"", "CLAUDE.local.md", true, false},
		{"docs/webapp", "../../webapp/CLAUDE.md", false, true},
		// A file that ships under another name is public only under that name.
		{"docs/cli", "../../cli-bash/bin/polis", true, false},
		{"docs/cli", "../../cli-bash/polis", true, true},
		{"", "cli-bash/bin/", true, false},
		{"", "cli-bash/", true, true},
		{"", "cli-bash/migrations/cli/manifest.json", true, false},
		{"", "migrations/cli/manifest.json", true, true},
		{"", "release/github/CODEOWNERS", true, false},
		{"", ".github/CODEOWNERS", true, true},
		// Private by the mapping, and the hosted service.
		{"", "scripts/release/mapping.sh", true, false},
		{"", "scripts/install.sh", true, true},
		{"", "cli-bash/README.md", true, false},
		{"", "webapp/cmd/hosted/", true, false},
		{"", "webapp/cmd/tailor/", true, true},
		// Outside docs/: a README's own link.
		{"webapp", "../docs/api/user/reference.md", true, false},
		{"webapp", "../docs/api/developer/reference.md", true, true},
	}
	private := theTree().fromMap // the private repo: cases about its own files apply only here
	if pub := theTree().pub["cli-bash/bin/polis"]; private && pub != "cli-bash/polis" {
		t.Errorf("cli-bash/bin/polis is published as %q, want cli-bash/polis", pub)
	}
	pages := map[string]bool{}
	for _, p := range publicMarkdown(t) {
		pages[p.Pub] = true
	}
	for p, want := range map[string]bool{"webapp/README.md": true, "skills/polis/SKILL.md": true, "AGENTS.md": true,
		"docs/README.md": true, "webapp/CLAUDE.md": false, "CLAUDE.md": false, "CLAUDE.local.md": false, "cli-go/CHANGELOG.md": false} {
		if pages[p] != want {
			t.Errorf("publicMarkdown includes %s = %v, want %v", p, pages[p], want)
		}
	}
	for line, want := range map[string]string{
		"see `webapp/CLAUDE.md` for the rules":                     "webapp/CLAUDE.md",
		"// the rule (CLAUDE.md, Signing base)":                    "CLAUDE.md",
		"run `cli-bash/bin/polis init`":                            "cli-bash/bin/polis",
		"[bash](../../cli-bash/bin/polis-upgrade)":                 "../../cli-bash/bin/polis-upgrade",
		"the bash CLI (`polis`, in `cli-bash/`)":                   "",
		"see docs/cli/implementation-parity.md, not X":             "",
		"https://github.com/vdibart/polis-cli/blob/main/CLAUDE.md": "", // L3's
	} {
		if got := strings.Join(unshippedMentions(line), " "); got != want {
			t.Errorf("unshippedMentions(%q) = %q, want %q", line, got, want)
		}
	}
	targets, _ := relativeLinks("See [the API](../docs/api/user/reference.md) and [x](https://e.com).\n\n[r]: ../README.md\n")
	if strings.Join(targets, " ") != "../docs/api/user/reference.md ../README.md" {
		t.Errorf("relativeLinks = %q", targets)
	}
	for _, c := range cases {
		if !c.public && c.ok && !private {
			continue // a private file that resolves privately: absent from the public repo
		}
		if got := resolveTarget(c.from, c.target, c.public) == ""; got != c.ok {
			t.Errorf("resolveTarget(%q, %q, public=%v) resolved=%v, want %v", c.from, c.target, c.public, got, c.ok)
		}
	}
	slugs := map[string]string{
		"4. Signature Model":                         "4-signature-model",
		"⛔ The `Status:` lines (D8)":                 "-the-status-lines-d8",
		"Reviewing the security and identity design": "reviewing-the-security-and-identity-design",
		"`polis clone <url> [target-dir]`":           "polis-clone-url-target-dir",
		"7 ⚠️ Tolerance: what a reader MUST do":      "7--tolerance-what-a-reader-must-do",
		"Glossary {#glossary}":                       "glossary-glossary",
		"[Link](x.md) and `public_key`":              "link-and-public_key",
	}
	for in, want := range slugs {
		if got := githubSlug(in); got != want {
			t.Errorf("githubSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLinkModelIsTheMapping: the tree these tests check is exactly the tree the
// release produces, read from the mapping — never a list of its own [AC1].
func TestLinkModelIsTheMapping(t *testing.T) {
	pt := theTree()
	if !pt.fromMap {
		t.Skip("the public repo: no mapping, and the tree is checked as it stands")
	}
	out, err := exec.Command("bash", filepath.Join(repoRoot, "scripts", "release", "mapping.sh"), "publist", "--worktree").Output()
	if err != nil {
		t.Fatalf("mapping.sh publist: %v", err)
	}
	want := map[string]bool{}
	for _, p := range strings.Fields(string(out)) {
		want[p] = true
		if _, ok := pt.src[p]; !ok {
			t.Errorf("the mapping publishes %s; the link model does not have it", p)
		}
	}
	if len(want) < 500 {
		t.Fatalf("mapping.sh publist named %d files — the mapping is not being read", len(want))
	}
	for p, src := range pt.src {
		if src != "" && !want[p] {
			t.Errorf("the link model has %s (from %s); the mapping does not publish it", p, src)
		}
	}
	if pt.rootCopies["docs/general/security/SECURITY.md"] != "SECURITY.md" {
		t.Errorf("root copies are not read from public.map's copy-of entries: %v", pt.rootCopies)
	}
}

// TestLinkModelFallback: without a mapping, the public repo's tree is checked as
// it stands; the private repo (it has plans/) fails instead of falling back.
func TestLinkModelFallback(t *testing.T) {
	pub := t.TempDir()
	os.MkdirAll(filepath.Join(pub, "docs"), 0o755)
	os.WriteFile(filepath.Join(pub, "docs", "README.md"), []byte("# x\n"), 0o644)
	pt, err := loadPublicTree(pub)
	if err != nil || pt.fromMap || pt.src["docs/README.md"] != "docs/README.md" || !pt.dirs["docs"] {
		t.Errorf("the public repo's tree: %+v, %v", pt, err)
	}
	os.MkdirAll(filepath.Join(pub, "plans"), 0o755)
	if _, err := loadPublicTree(pub); err == nil {
		t.Error("plans/ exists and the mapping does not, and the tree loaded anyway")
	}
}
