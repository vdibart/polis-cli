package comment

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
)

// TestTheTwoEscapersAgree pins the equivalence that lets a comment's title and a
// post's title share ONE inverse (close-out R3-1, R4-1).
//
// escapeYAMLTitle here and publish.escapeYAMLString apply the same quoting rule,
// and unquoteYAMLTitle reverses BOTH by delegating to publish.UnquoteYAMLString.
// ⛔ Nothing else holds that: publish's own property test covers its escaper, and
// this one would stay green while this package's escaper drifted away from it.
// The close-out's own standard was "hold the claim with a property test, not a
// comment" — this is that test for this half.
func TestTheTwoEscapersAgree(t *testing.T) {
	// Every family escapeYAMLString's rule reacts to, plus the two that a
	// tolerant inverse silently damaged before R4-1 ('Foo' and backslashes).
	cases := []string{
		"", " ", "  ", "plain title", "Foo: Bar", "Ends with colon:", "a: b: c",
		"'Foo'", "''", "it's", `"quoted"`, `The "quoted" one`, `"Foo: Bar"`,
		`a\b`, `a\\b: c`, `C:\path\to\thing`, `back\\slash: yes`,
		" leading", "trailing ", "\tTabbed", "tabbed\t", "\r\ncrlf", "*star", "&amp", "!bang", "|pipe", ">gt",
		"@at", "`tick", "#hash", "-dash", "?question", "%percent",
		"null", "true", "~", "{}", "[]", "Résumé 🌱: notes", "日本語のタイトル",
		strings.Repeat(`"`, 32), strings.Repeat(`\`, 32), strings.Repeat("a", 4096),
	}

	for _, s := range cases {
		if got := unquoteYAMLTitle(escapeYAMLTitle(s)); got != s {
			t.Errorf("round trip changed the title:\n  in  %q\n  out %q\n  (escaped: %q)", s, got, escapeYAMLTitle(s))
		}
	}

	// The alphabet is the one the rule cares about, so a random draw lands on
	// the interesting boundaries far more often than prose would.
	alphabet := []rune(` a:"'\*&!|>@` + "`" + `#-?%{}[]~é🌱`)
	rng := rand.New(rand.NewSource(20260917))
	lossy := 0
	for i := 0; i < 50000; i++ {
		n := rng.Intn(12)
		var b strings.Builder
		for j := 0; j < n; j++ {
			b.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		s := b.String()
		if got := unquoteYAMLTitle(escapeYAMLTitle(s)); got != s {
			if lossy < 5 {
				t.Errorf("round trip changed the title:\n  in  %q\n  out %q\n  (escaped: %q)", s, got, escapeYAMLTitle(s))
			}
			lossy++
		}
	}
	if lossy > 0 {
		t.Errorf("%d of 50000 random titles did not survive escape -> unquote", lossy)
	}

	// And the pairing is only meaningful while the shared inverse is the one
	// being exercised: a title needing quotes must actually come back through
	// publish.UnquoteYAMLString, not be passed along untouched.
	if escapeYAMLTitle("Foo: Bar") == "Foo: Bar" {
		t.Fatal("escapeYAMLTitle no longer quotes a colon-space title, so this test proves nothing")
	}
	if publish.UnquoteYAMLString(escapeYAMLTitle("Foo: Bar")) != "Foo: Bar" {
		t.Fatal("publish.UnquoteYAMLString no longer reverses this package's escaper")
	}

	// THE TAB-LED TITLE, once the one boundary this test pinned as broken. A
	// tab-led title IS reachable — `# <tab>Tabbed` survives
	// publish.ExtractTitle — and publish.ParseFrontmatter trims every value on
	// the way back, so while the escapers quoted only a leading SPACE the tab was
	// lost. Both now quote any leading or trailing whitespace, so the title
	// survives the whole frontmatter round trip. Asserted end to end, through
	// the real parser, not just escape -> unquote.
	if got := publish.ExtractTitle("# \tTabbed\n"); got != "\tTabbed" {
		t.Errorf("ExtractTitle now yields %q for a tab-led heading; the case this pins moved", got)
	}
	// (publish's own escaper is held to the same, in publish's tests.)
	fm := publish.ParseFrontmatter("---\ntitle: " + escapeYAMLTitle("\tTabbed") + "\n---\n")
	if got := publish.UnquoteYAMLString(fm["title"]); got != "\tTabbed" {
		t.Errorf("a tab-led title did not survive the frontmatter round trip: got %q", got)
	}
}
