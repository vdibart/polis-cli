package main

import (
	"path"
	"regexp"
	"strings"
	"testing"
)

// ⭐ TRAIL MARKERS ARE A ROUTE, AND A ROUTE HAS NO DEAD ENDS.
//
// A trail marker is the comment at the top of a source file that says what the
// file does and where to read on ("Pull the thread"). The ones in the pages and
// scripts a site serves are what a reader meets in view-source, and from there
// the public docs carry them on: HTML comment → doc → doc. So every path a
// marker names must be one a reader can open: a file or directory in the
// PUBLIC repo, never one that does not ship (the discovery service, polis.pub's
// hosted service, this repo's plans). And every marker must lead into the docs.
//
// servedClosedSources are files that do not ship in the public repo but ARE
// served to every reader of a polis.pub site, so their markers are read in
// view-source and are held to the same rule.
var servedClosedSources = []string{
	"webapp/internal/hosted/widget/widget.js",
	"webapp/internal/hosted/nav/nav.js",
}

var (
	markerPhrase = regexp.MustCompile(`Pull the thread|TRAIL MARKER|· trail marker`)
	// A link to the public repo names a repo path after its prefix.
	githubPrefix = regexp.MustCompile(`(?:https?://)?github\.com/vdibart/polis-cli/(?:blob|tree)/main/`)
	// A repo path a marker names: a top-level directory of this repo, then
	// path segments. Prose like "the webapp" has no slash and is not a path.
	markerPath = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./-])((?:AGENTS\.md)|(?:cli-go|cli-bash|webapp|discovery-service|docs|skills|completions|plans|scripts)/[A-Za-z0-9_./-]*[A-Za-z0-9_/])`)
)

// markerBlock returns the comment block holding a file's trail marker (the
// lines of the comment the marker phrase is in), or nil when it has none.
func markerBlock(lines []string, ext string) (block []string, first int) {
	at := -1
	for i, l := range lines {
		if markerPhrase.MatchString(l) {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, 0
	}
	if ext == ".html" {
		lo, hi := at, at
		for lo > 0 && !strings.Contains(lines[lo], "<!--") {
			lo--
		}
		for hi < len(lines)-1 && !strings.Contains(lines[hi], "-->") {
			hi++
		}
		return lines[lo : hi+1], lo
	}
	isComment := func(l string) bool {
		t := strings.TrimSpace(l)
		return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*")
	}
	lo, hi := at, at
	for lo > 0 && isComment(lines[lo-1]) {
		lo--
	}
	for hi < len(lines)-1 && isComment(lines[hi+1]) {
		hi++
	}
	return lines[lo : hi+1], lo
}

// publicPath reports whether a repo path a marker names reaches a reader: a
// file the public repo carries, or one of its directories.
func publicPath(rel string) bool {
	rel = strings.TrimSuffix(path.Clean(rel), "/")
	pt := theTree()
	if _, ok := pt.pub[rel]; ok {
		return true
	}
	return pt.dirs[rel]
}

func TestTrailMarkersLeadOnlyIntoThePublicTree(t *testing.T) {
	served := map[string]bool{}
	for _, s := range servedClosedSources {
		served[s] = true
	}
	markers := 0
	check := func(rel string) {
		lines := readLines(t, rel)
		block, first := markerBlock(lines, path.Ext(rel))
		if block == nil {
			return
		}
		markers++
		docs := 0
		for i, l := range block {
			l = githubPrefix.ReplaceAllString(l, " ")
			for _, m := range markerPath.FindAllStringSubmatch(l, -1) {
				p := strings.TrimRight(m[1], ".")
				if strings.HasPrefix(p, "docs/") {
					docs++
				}
				if !publicPath(p) {
					t.Errorf("%s:%d: the trail marker names %s, which a reader cannot open (it is not in the public repo); name a public doc instead", rel, first+i+1, p)
				}
			}
		}
		if docs == 0 {
			t.Errorf("%s:%d: the trail marker names no doc under docs/, so the route stops here", rel, first+1)
		}
	}
	sources(t, func(rel, pub string) {
		// Source files only: docs and AGENTS.md are the route's next step, and
		// a test that names the phrase is not a marker.
		if pub != "" && path.Ext(rel) != ".md" && !strings.HasSuffix(rel, "_test.go") {
			check(rel)
		}
	})
	for s := range served {
		if closedAndAbsent(s) {
			continue // the public repository: served by polis.pub, not shipped here
		}
		check(s)
	}
	if markers < 10 {
		t.Fatalf("only %d trail markers found — the scan is broken, not the tree clean", markers)
	}
}

// The check can fail: a marker naming closed source, and one naming no doc.
func TestTrailMarkerCheckCatchesBreaks(t *testing.T) {
	block, _ := markerBlock([]string{"package x", "", "// HANDBOOK TRAIL MARKER: see", "// discovery-service/core/pql.ts", "func f() {}"}, ".go")
	if len(block) != 2 {
		t.Fatalf("block = %q", block)
	}
	m := markerPath.FindStringSubmatch(block[1])
	if m == nil || publicPath(m[1]) {
		t.Errorf("discovery-service/core/pql.ts should be found and refused, got %v", m)
	}
	if !publicPath("docs/general/reference/pql.md") || !publicPath("cli-go/pkg/pql/") {
		t.Error("a public doc and a public directory should be accepted")
	}
}
