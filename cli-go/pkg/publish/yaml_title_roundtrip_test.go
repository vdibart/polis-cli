package publish

import (
	"math/rand"
	"strings"
	"testing"
)

// ⛔ THE CLAIM IS A PROPERTY, NOT A COMMENT: for every title this package can
// write, unquoting what was stored returns exactly the title.
//
// UnquoteYAMLString exists to undo escapeYAMLString and nothing else. The first
// version was copied in spirit from render.yamlUnquote — a TOLERANT parser,
// correct where it lives because it reads frontmatter anyone may have written —
// and it was lossy on two families our own writer produces: it stripped the
// author's apostrophes from `'Foo'` (which the escaper stores bare, since
// apostrophes are legal unquoted YAML) and collapsed `\\` to `\` inside a
// quoted value (which the escaper never escapes). Both renamed the post on
// republish, the R3-1 symptom (close-out R4-1).
func TestUnquoteIsTheExactInverseOfEscape(t *testing.T) {
	handPicked := []string{
		// The families that regressed.
		`'Foo'`, `''`, `'`, `it's`, `'quoted': yes`,
		`a\\b: c`, `back\slash`, `\`, `\\`, `x: "q" \\ y`, `C:\path\to: here`,
		// The cases that drove the escaper's rule.
		`Foo: Bar`, `Re: a-post`, `ends with colon:`, "has\nnewline", `The "quoted" one`,
		` leading space`, `trailing space `, `*star`, `&amp`, `!bang`, `|pipe`, `>gt`, `@at`, "`tick`", `#hash`,
		// Ordinary ones.
		``, `plain`, `Hello, world`, `dash - dash`, `emoji 🌱`, `a: b: c`, `"`, `""`, `"""`,
	}
	for _, title := range handPicked {
		assertRoundTrip(t, title)
	}

	// Random strings over the alphabet the rule reacts to.
	const alphabet = `ab: "'\|>*&!@#` + "`\n" + `  :`
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		n := r.Intn(12)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteByte(alphabet[r.Intn(len(alphabet))])
		}
		assertRoundTrip(t, sb.String())
	}
}

func assertRoundTrip(t *testing.T, title string) {
	t.Helper()
	stored := escapeYAMLString(title)
	if got := UnquoteYAMLString(stored); got != title {
		t.Errorf("escape→unquote(%q) = %q, want %q (stored as %q)", title, got, stored, stored)
	}
}

// ⚠️ WHAT IT CANNOT DO, stated as a test so the limit is not mistaken for a
// bug later — and so the *"no-op on bash-published content"* claim cannot come
// back (close-out R4-2).
//
// The bash CLI writes `title: $title` with no escaping. So a bash post whose
// author typed quotes stores them bare on the line, and our inverse — which
// can only assume OUR writer — reads them as this package's quoting and
// strips them. This package would have written the same title differently
// (escaping the inner quotes), which is exactly why the loss is confined to
// content bash wrote.
func TestAForeignWritersQuotesAreReadAsOurEscaping(t *testing.T) {
	const authorTyped = `"The Great Gatsby"`

	// What bash puts on the line: the title, raw.
	const asBashStoresIt = authorTyped
	if got := UnquoteYAMLString(asBashStoresIt); got != `The Great Gatsby` {
		t.Errorf("UnquoteYAMLString(%q) = %q, want the quotes read as ours — this is the documented limit", asBashStoresIt, got)
	}

	// What this package puts on the line for the SAME title, which is why the
	// limit does not reach Go-published content.
	asGoStoresIt := escapeYAMLString(authorTyped)
	if asGoStoresIt == asBashStoresIt {
		t.Fatalf("this package now stores %q the way bash does (%q) — the two are no longer distinguishable, so this reasoning needs redoing", authorTyped, asGoStoresIt)
	}
	if got := UnquoteYAMLString(asGoStoresIt); got != authorTyped {
		t.Errorf("our own bytes must round-trip: UnquoteYAMLString(%q) = %q, want %q", asGoStoresIt, got, authorTyped)
	}
}
