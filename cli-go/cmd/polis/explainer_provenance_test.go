package main

// The Signet explainer at polis.pub/for/trust shows real excerpts and states
// where each one came from. These tests hold it to the four it attributes to a
// specification.
//
// ⛔ THE CLAIM IS THE POINT. A page whose whole argument is "you can check this
// yourself" is undermining itself if its own provenance cannot be checked. An
// excerpt that says "from the delegation spec" and no longer matches the
// delegation spec is worse than one that never claimed a source, because the
// claim is what earns the reader's trust in everything around it.
//
// ⚠️ These cover only the SPEC-sourced excerpts, on purpose: they read files in
// this repo and need no network, so they run in the ordinary test run. The three
// excerpts marked *Live* quote bytes on a real site and cannot be checked
// offline; that is a separate, opt-in check.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// closedAndAbsent reports whether a repo file is missing because this is the
// public repository, where closed source does not ship (and plans/ is absent
// too). Anywhere else a missing file is the test's to fail on.
func closedAndAbsent(rel string) bool {
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		return false
	}
	_, perr := os.Stat(filepath.Join(repoRoot, "plans"))
	return os.IsNotExist(perr)
}

const explainerPage = "webapp/internal/hosted/for-trust.html"

func readExplainer(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(explainerPage)))
	if err != nil {
		// The public repository: the page is hosted-only (closed source) and simply
		// absent there, as is plans/. Anywhere else a missing page is a failure.
		if _, perr := os.Stat(filepath.Join(repoRoot, "plans")); os.IsNotExist(perr) && os.IsNotExist(err) {
			t.Skipf("%s is closed source and not in this tree", explainerPage)
		}
		t.Fatalf("reading %s: %v", explainerPage, err)
	}
	return string(data)
}

// TestExplainerSpecExcerptsMatchTheirSpecs checks that every distinctive string
// the page quotes from a specification is still present in the specification it
// names. It is deliberately token-based rather than a whole-block comparison:
// the page substitutes its own example domains (castle-alice.com for the spec's
// alice.example), so an exact match would fail for a reason that is not drift.
func TestExplainerSpecExcerptsMatchTheirSpecs(t *testing.T) {
	page := readExplainer(t)

	for _, tc := range []struct {
		chapter string
		spec    string
		tokens  []string
	}{
		{
			chapter: "four — attestation",
			spec:    "docs/signet/spec/attestation.md",
			tokens:  []string{`pub.polis.attestation.correction`, `"version"`, `"asserted"`},
		},
		{
			chapter: "five — key history",
			spec:    "docs/signet/spec/key-history.md",
			tokens:  []string{`transition_sig`, `valid_until`, `"epoch"`},
		},
		{
			chapter: "six — delegation",
			spec:    "docs/signet/spec/delegation.md",
			tokens:  []string{`"agent":"rosie"`, `pub.polis.comment.blessing`, `"grant"`},
		},
		{
			chapter: "seven — discovery service",
			spec:    "docs/ds/developer/api-reference.md",
			tokens:  []string{`ds_signature`, `ds_key_id`, `signature_verified`},
		},
	} {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(tc.spec)))
		if err != nil {
			t.Errorf("chapter %s cites %s, which could not be read: %v", tc.chapter, tc.spec, err)
			continue
		}
		spec := string(data)

		for _, tok := range tc.tokens {
			if !strings.Contains(page, tok) {
				t.Errorf("chapter %s: the page no longer quotes %q — the excerpt changed but this test did not", tc.chapter, tok)
			}
			if !strings.Contains(spec, tok) {
				t.Errorf("chapter %s: the page quotes %q and attributes it to %s, but that file no longer contains it. "+
					"Either the spec moved on and the page is now wrong, or the attribution is wrong.", tc.chapter, tok, tc.spec)
			}
		}
	}
}

var provenanceLineRe = regexp.MustCompile(`(?s)<p class="provenance">(.*?)</p>`)

// TestExplainerProvenanceLinesLinkTheirSources exists because the page's footer
// says "the linked files are the record". That sentence is only true if the
// provenance lines actually link, and for a while it was not: the page invited
// the reader to check a source it gave them no path to.
//
// ⛔ AND IT GUARDS THE EDIT, NOT JUST THE IDEA. These links were once added and
// then silently lost — the tests of the day checked quoted tokens and field
// order, both of which survive the links being stripped, so nothing failed.
// A page's claim about itself needs a test on the claim.
func TestExplainerProvenanceLinesLinkTheirSources(t *testing.T) {
	page := readExplainer(t)

	lines := provenanceLineRe.FindAllStringSubmatch(page, -1)
	if len(lines) != 7 {
		t.Fatalf("expected 7 provenance lines, one per chapter, found %d", len(lines))
	}
	for i, m := range lines {
		if !strings.Contains(m[1], "<a href=") {
			snippet := m[1]
			if len(snippet) > 80 {
				snippet = snippet[:80] + "…"
			}
			t.Errorf("provenance line %d names a source but does not link it: %q", i+1, snippet)
		}
	}
}

// TestExplainerDatesItsLiveExcerpts guards the other half of the footer's claim.
// Three excerpts assert live bytes on a real site; without a date the reader
// cannot tell whether "live" means today or a year ago.
func TestExplainerDatesItsLiveExcerpts(t *testing.T) {
	page := readExplainer(t)
	if !regexp.MustCompile(`fetched from live sites on \d{1,2} \w+ \d{4}`).MatchString(page) {
		t.Error(`the page no longer states when its live excerpts were fetched — ` +
			`docs/signet/overview.md's wording is "fetched from live sites on <date>"`)
	}
}

var jsonKeyRe = regexp.MustCompile(`"([A-Za-z_]+)"\s*:`)

// jsonKeyOrder pulls the JSON object keys out of a fragment, in the order they
// appear. It is safe against URL values: the pattern needs a closing quote
// before the colon, which "https://… never has.
func jsonKeyOrder(fragment string) []string {
	var keys []string
	for _, m := range jsonKeyRe.FindAllStringSubmatch(fragment, -1) {
		keys = append(keys, m[1])
	}
	return keys
}

// TestExplainerBlessingFieldOrderMatchesSpec is the sharpest of these checks.
//
// ⛔ Chapter six quotes CANONICAL SIGNED BYTES, and field order is part of what
// is signed — `signing-base.md` states the order is Go struct declaration order
// and explicitly not alphabetical, because changing it would invalidate every
// existing signature. A reader could reasonably copy this excerpt. So it is not
// enough that the right field NAMES appear; they must appear in the right ORDER.
func TestExplainerBlessingFieldOrderMatchesSpec(t *testing.T) {
	page := readExplainer(t)

	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash("docs/signet/spec/delegation.md")))
	if err != nil {
		t.Fatalf("reading the delegation spec: %v", err)
	}

	specLine := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, `{"type":"pub.polis.comment.blessing"`) {
			specLine = line
			break
		}
	}
	if specLine == "" {
		t.Fatal("the delegation spec no longer contains a canonical blessing payload — chapter six's excerpt has nothing to be checked against")
	}

	// The page pretty-prints the same payload across several lines inside a
	// <pre>; take the block that carries it.
	start := strings.Index(page, `{"type":"pub.polis.comment.blessing"`)
	if start < 0 {
		t.Fatal("the page no longer shows the canonical blessing payload in chapter six")
	}
	end := strings.Index(page[start:], "</pre>")
	if end < 0 {
		t.Fatal("chapter six's excerpt is not inside a <pre> block")
	}
	pageBlock := page[start : start+end]

	specKeys := jsonKeyOrder(specLine)
	pageKeys := jsonKeyOrder(pageBlock)

	if len(specKeys) != len(pageKeys) {
		t.Fatalf("field count differs: the page shows %v, the spec shows %v", pageKeys, specKeys)
	}
	for i := range specKeys {
		if specKeys[i] != pageKeys[i] {
			t.Fatalf("field ORDER differs at position %d: the page shows %v, the spec shows %v. "+
				"Order is inside the signature, so this is a correctness bug, not a formatting one.", i, pageKeys, specKeys)
		}
	}
}

// TestExplainerKeyHistoryExcerptStaysMarkedIllustrative guards a caveat that is
// easy to lose in an edit. No site on the network has rotated a key, so chapter
// five's excerpt is constructed from the spec rather than fetched. If that
// sentence ever disappears while the excerpt stays, the page silently starts
// claiming something it cannot support.
func TestExplainerKeyHistoryExcerptStaysMarkedIllustrative(t *testing.T) {
	page := readExplainer(t)
	if !strings.Contains(page, "illustrative, because no site on the network has rotated yet") {
		t.Error("chapter five's excerpt is no longer marked illustrative — either a site has rotated and it should now " +
			"quote real bytes, or the caveat was dropped and the page now overclaims")
	}
}
