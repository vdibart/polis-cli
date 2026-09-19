package main

// L5 — the orientation line.
//
// Every public page opens, under its title, with one line of plain links that
// says who it is for, what it is about, what kind of page it is, and where to
// go next:
//
//	*For* [Developers](../../README.md#building-on-polis) — *About* [Identity](../../general/README.md#identity) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/site/keyhistory.go`](../../../cli-go/pkg/site/keyhistory.go) — *See also* [concept](../concepts/identity.md) · [recipe](../recipes/08-survive-a-key-rotation.md)
//
// ⛔ CHECKED, OR NOT BUILT. A header nobody re-reads when a page moves is the
// fastest-rotting line on the page, so every value comes from a fixed list and
// every link resolves by L1. It checks nothing about the prose.
//
// ⭐ A concept page's See also IS the documentation triad: somewhere deeper (a
// spec or a reference) and something to do (a guide or a recipe), or a
// "no … yet" token saying the page does not exist. Printing the tokens is the
// gap report (TestDocsTriadGaps, -v).

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// noOrientationLine are the pages that carry no line: the landing, indexes,
// reading paths, the vision essay, the vulnerability
// policy, and tombstones. Every other public page must carry one.
var noOrientationLine = map[string]string{
	"docs/general/vision.md":               "an essay, not a page in any door",
	"docs/general/security/SECURITY.md":    "a policy GitHub renders specially",
	"docs/ds/developer/storage-adapter.md": "tombstone",
	"docs/webapp/designer/README.md":       "tombstone",
	"docs/webapp/designer/components.md":   "tombstone",
	"docs/webapp/designer/decisions.md":    "tombstone",
	"docs/webapp/designer/theme-system.md": "tombstone",
}

func carriesNoLine(page string) bool {
	_, listed := noOrientationLine[page]
	return listed || path.Base(page) == "README.md" || strings.HasPrefix(page, "docs/paths/")
}

var (
	doors = map[string]string{
		"Writers":      "docs/README.md#writing-on-polis",
		"Developers":   "docs/README.md#building-on-polis",
		"Reviewers":    "docs/README.md#reviewing-the-security-and-identity-design",
		"Operators":    "docs/README.md#running-polis",
		"Contributors": "docs/README.md#contributing-to-polis",
	}
	primitives = map[string]string{
		"Identity":      "docs/general/README.md#identity",
		"Content":       "docs/general/README.md#content",
		"Relationships": "docs/general/README.md#relationships",
	}
	kinds = map[string]string{
		"Concept": "docs/README.md#kinds-of-page", "Spec": "docs/README.md#kinds-of-page",
		"Guide": "docs/README.md#kinds-of-page", "Recipe": "docs/README.md#kinds-of-page",
		"Reference": "docs/README.md#kinds-of-page", "Tour": "docs/README.md#kinds-of-page",
	}
	components = map[string]string{
		"CLI":               "docs/cli/README.md",
		"Webapp":            "docs/webapp/README.md",
		"Discovery service": "docs/ds/README.md",
		"Hosted service":    "docs/ops/README.md",
	}
	seeAlsoLabels = map[string]bool{"concept": true, "spec": true, "reference": true, "guide": true, "recipe": true, "tour": true}
	gapTokens     = map[string]bool{"no concept yet": true, "no spec yet": true, "no recipe yet": true}

	fieldOrder = []string{"For", "About", "Kind", "Component", "Code", "See also"}

	// fieldsByKind is design §10.2 with ruling R4: Component on everything but
	// tours; Code on specs and references only; About on concepts and specs.
	fieldsByKind = map[string]map[string]bool{
		"Concept":   {"For": true, "About": true, "Kind": true, "Component": true, "See also": true},
		"Spec":      {"For": true, "About": true, "Kind": true, "Component": true, "Code": true, "See also": true},
		"Guide":     {"For": true, "Kind": true, "Component": true, "See also": true},
		"Recipe":    {"For": true, "Kind": true, "Component": true, "See also": true},
		"Reference": {"For": true, "Kind": true, "Component": true, "Code": true, "See also": true},
		"Tour":      {"For": true, "Kind": true, "See also": true},
	}

	// folderKinds: where a folder states a kind, the line must agree.
	folderKinds = []struct{ prefix, kind string }{
		{"docs/signet/spec/", "Spec"}, {"docs/signet/recipes/", "Recipe"},
		{"docs/signet/concepts/", "Concept"}, {"docs/signet/guides/", "Guide"}, {"docs/handbook/", "Tour"},
	}

	subtitleLine = regexp.MustCompile(`^\*[^*].*\*$`)
	labelledLink = regexp.MustCompile("^\\[([^\\]]+)\\]\\(([^)\\s]+)\\)$")
	fieldSeg     = regexp.MustCompile(`^\*([A-Za-z ]+)\* (.+)$`)
)

type orientation struct {
	Fields map[string][]orientationValue
	Tokens []string
}

type orientationValue struct{ Text, Target string }

// findOrientationLine returns the line under the page's H1: the first
// non-empty line, or the second when the first is an italic subtitle.
func findOrientationLine(md string) (line string, n int, ok bool) {
	lines := strings.Split(md, "\n")
	i := 0
	for ; i < len(lines) && !strings.HasPrefix(lines[i], "# "); i++ {
	}
	seen := 0
	for i++; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "*For* ") {
			return l, i + 1, true
		}
		seen++
		if seen > 1 || !subtitleLine.MatchString(l) {
			return l, i + 1, false
		}
	}
	return "", 0, false
}

// checkOrientation validates one line for the page at page (repo-relative) and
// returns every problem.
// linkBase is where an orientation link resolves from: the page's directory,
// or the repo root for an absolute github.com/vdibart/polis-cli URL (which a page
// the release copies to the root must use). It returns the directory and the
// target to resolve against it.
func linkBase(page, target string) (string, string) {
	if m := githubURL.FindStringSubmatch(target); m != nil && strings.HasPrefix(target, "https://") {
		return ".", m[1]
	}
	return path.Dir(page), target
}

func checkOrientation(page, line string) (orientation, []string) {
	o := orientation{Fields: map[string][]orientationValue{}}
	var errs []string
	fail := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	last := -1
	for _, seg := range strings.Split(line, " — ") {
		m := fieldSeg.FindStringSubmatch(seg)
		if m == nil {
			fail("segment %q is not *Field* values", seg)
			continue
		}
		label, pos := m[1], -1
		for i, f := range fieldOrder {
			if f == label {
				pos = i
			}
		}
		switch {
		case pos < 0:
			fail("unknown field %q", label)
			continue
		case pos <= last:
			fail("field %q repeated or out of order (order: %s)", label, strings.Join(fieldOrder, ", "))
		}
		last = pos
		for _, v := range strings.Split(m[2], " · ") {
			v = strings.TrimSpace(v)
			if label == "See also" && gapTokens[v] {
				o.Tokens = append(o.Tokens, v)
				continue
			}
			lm := labelledLink.FindStringSubmatch(v)
			if lm == nil {
				fail("%s value %q is not a link", label, v)
				continue
			}
			o.Fields[label] = append(o.Fields[label], orientationValue{Text: lm[1], Target: lm[2]})
		}
	}

	resolves := func(label string, v orientationValue) bool {
		dir, target := linkBase(page, v.Target)
		if why := resolveTarget(dir, target, true); why != "" {
			fail("%s [%s](%s): %s", label, v.Text, v.Target, why)
			return false
		}
		return true
	}
	// wantTarget checks a fixed-list value links exactly where its list says.
	wantTarget := func(label string, list map[string]string, v orientationValue) {
		want, ok := list[v.Text]
		if !ok {
			fail("%s value %q is not one of the fixed values", label, v.Text)
			return
		}
		dir, target := linkBase(page, v.Target)
		p, frag, _ := strings.Cut(target, "#")
		got := path.Clean(path.Join(dir, p))
		if frag != "" {
			got += "#" + frag
		}
		if got != want {
			fail("%s %q links %s, want %s", label, v.Text, got, want)
			return
		}
		resolves(label, v)
	}

	if n := len(o.Fields["For"]); n < 1 || n > 3 {
		fail("For names %d doors, want 1–3", n)
	}
	for _, v := range o.Fields["For"] {
		wantTarget("For", doors, v)
	}
	for _, v := range o.Fields["About"] {
		wantTarget("About", primitives, v)
	}
	if len(o.Fields["Kind"]) != 1 {
		fail("Kind has %d values, want exactly 1", len(o.Fields["Kind"]))
		return o, errs
	}
	kind := o.Fields["Kind"][0].Text
	wantTarget("Kind", kinds, o.Fields["Kind"][0])
	for _, v := range o.Fields["Component"] {
		wantTarget("Component", components, v)
	}
	for _, v := range o.Fields["Code"] {
		code := strings.Trim(v.Text, "`")
		if v.Text != "`"+code+"`" {
			fail("Code value %q is not a code path in backticks", v.Text)
		}
		dir, target := linkBase(page, v.Target)
		if resolves("Code", v) && path.Clean(path.Join(dir, target)) != path.Clean(code) {
			fail("Code [%s] links %s, not the path it names", v.Text, path.Clean(path.Join(dir, target)))
		}
	}
	for _, v := range o.Fields["See also"] {
		if !seeAlsoLabels[v.Text] {
			fail("See also label %q is not a kind of page", v.Text)
		}
		resolves("See also", v)
	}

	allowed := fieldsByKind[kind]
	for label := range o.Fields {
		if allowed != nil && !allowed[label] {
			fail("a %s does not carry %s", kind, label)
		}
	}
	for _, fk := range folderKinds {
		if strings.HasPrefix(page, fk.prefix) && kind != fk.kind {
			fail("page in %s says Kind %s, want %s", fk.prefix, kind, fk.kind)
		}
	}
	if kind == "Concept" {
		has := map[string]bool{}
		for _, v := range o.Fields["See also"] {
			has[v.Text] = true
		}
		for _, t := range o.Tokens {
			has[t] = true
		}
		if !has["spec"] && !has["reference"] && !has["no spec yet"] {
			fail("concept names no spec (or reference) and no `no spec yet` — the triad is structural")
		}
		if !has["guide"] && !has["recipe"] && !has["no recipe yet"] {
			fail("concept names no guide or recipe and no `no recipe yet` — the triad is structural")
		}
	}
	return o, errs
}

// TestDocsOrientationLines is L5.
func TestDocsOrientationLines(t *testing.T) {
	for page := range noOrientationLine {
		if !exists(page, true) {
			t.Errorf("noOrientationLine lists %s, which does not exist", page)
		}
	}
	checked := 0
	for _, page := range publicDocs(t) {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(page)))
		if err != nil {
			t.Fatal(err)
		}
		line, n, ok := findOrientationLine(string(data))
		if carriesNoLine(page) {
			if ok {
				t.Errorf("%s:%d: carries an orientation line, but is a page that carries none", page, n)
			}
			continue
		}
		if !ok {
			t.Errorf("%s:%d: no orientation line under the title (found %q)", page, n, line)
			continue
		}
		checked++
		_, errs := checkOrientation(page, line)
		for _, e := range errs {
			t.Errorf("%s:%d: %s", page, n, e)
		}
	}
	if checked < 50 {
		t.Errorf("only %d orientation lines checked", checked)
	}
}

// TestDocsTriadGaps prints the gap report: every "no … yet" token.
func TestDocsTriadGaps(t *testing.T) {
	var gaps []string
	for _, page := range publicDocs(t) {
		if carriesNoLine(page) {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(page)))
		if line, _, ok := findOrientationLine(string(data)); ok {
			o, _ := checkOrientation(page, line)
			for _, tok := range o.Tokens {
				gaps = append(gaps, page+": "+tok)
			}
		}
	}
	sort.Strings(gaps)
	for _, g := range gaps {
		t.Log(g)
	}
}

// TestOrientationCheckCatchesBreaks is the mutation check: each bad line fails.
func TestOrientationCheckCatchesBreaks(t *testing.T) {
	const page = "docs/signet/spec/key-history.md"
	const good = "*For* [Developers](../../README.md#building-on-polis) — *About* [Identity](../../general/README.md#identity) — *Kind* [Spec](../../README.md#kinds-of-page) — *Code* [`cli-go/pkg/site/keyhistory.go`](../../../cli-go/pkg/site/keyhistory.go) — *See also* [concept](../concepts/identity.md) · [recipe](../recipes/08-survive-a-key-rotation.md)"
	if _, errs := checkOrientation(page, good); len(errs) != 0 {
		t.Fatalf("the good line fails: %v", errs)
	}
	bad := map[string]string{
		"a value off the list":      strings.Replace(good, "[Developers]", "[Engineers]", 1),
		"a door linking elsewhere":  strings.Replace(good, "#building-on-polis", "#writing-on-polis", 1),
		"a missing code path":       strings.Replace(good, "keyhistory.go`](../../../cli-go/pkg/site/keyhistory.go)", "gone.go`](../../../cli-go/pkg/site/gone.go)", 1),
		"a broken see-also link":    strings.Replace(good, "../concepts/identity.md", "../concepts/nope.md", 1),
		"a kind against its folder": strings.Replace(good, "[Spec](", "[Guide](", 1),
		"fields out of order":       strings.Replace(good, "*About* [Identity](../../general/README.md#identity) — *Kind* [Spec](../../README.md#kinds-of-page)", "*Kind* [Spec](../../README.md#kinds-of-page) — *About* [Identity](../../general/README.md#identity)", 1),
		"a private code path":       strings.Replace(good, "`cli-go/pkg/site/keyhistory.go`](../../../cli-go/pkg/site/keyhistory.go)", "`dns/polis.pub.txt`](../../../dns/polis.pub.txt)", 1),
	}
	for name, line := range bad {
		if _, errs := checkOrientation(page, line); len(errs) == 0 {
			t.Errorf("%s: passed, want a failure\n%s", name, line)
		}
	}
	const concept = "docs/signet/concepts/trust.md"
	triadBroken := "*For* [Reviewers](../../README.md#reviewing-the-security-and-identity-design) — *About* [Relationships](../../general/README.md#relationships) — *Kind* [Concept](../../README.md#kinds-of-page) — *See also* [spec](../spec/attestation.md#the-signed-follow-file)"
	if _, errs := checkOrientation(concept, triadBroken); len(errs) == 0 {
		t.Error("a concept with no guide, recipe or token passed the triad rule")
	}
	if _, errs := checkOrientation(concept, triadBroken+" · no recipe yet"); len(errs) != 0 {
		t.Errorf("the token should satisfy the triad: %v", errs)
	}
}
