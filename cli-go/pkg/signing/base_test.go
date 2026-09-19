package signing

import (
	"strings"
	"testing"
)

// legacyContentToSign is the pre-epic-08 rule, frozen here as TEST DATA.
//
// It is the canary's other half: every fixture below is run through both this
// and MarkdownSigningBase, and any disagreement has to be a case the old rule
// got WRONG. Keeping it in a test file rather than in the package is the point —
// the old behaviour is something we compare against, not something we can still
// accidentally call.
//
// Verbatim from signing.go before the typed base replaced it:
//
//	for _, line := range strings.Split(content, "\n") {
//	    if strings.HasPrefix(line, "signature:") { continue }
//	    if isComment && strings.HasPrefix(line, "author:") { continue }
//	    out = append(out, line)
//	}
//	return CanonicalizeContent(strings.Join(out, "\n"))
func legacyContentToSign(content string, isComment bool) string {
	lines := strings.Split(content, "\n")
	var out []string
	for _, line := range lines {
		if strings.HasPrefix(line, "signature:") {
			continue
		}
		if isComment && strings.HasPrefix(line, "author:") {
			continue
		}
		out = append(out, line)
	}
	return CanonicalizeContent(strings.Join(out, "\n"))
}

// ---------------------------------------------------------------------------
// The regression test the epic exists to add.
// ---------------------------------------------------------------------------

// TestMarkdownSigningBase_BodyKeywordLinesSurvive is the DELIBERATE regression
// test for the strip bug: a body line beginning `signature:` was dropped from
// the signing base, so authentic content failed verification.
//
// ⚠️ The fix falls out of the typed design as a side effect, which is exactly
// why this test has to exist on purpose: delete it and the bug can come back
// unnoticed the next time someone "simplifies" the strip.
//
// Both arms matter and they failed differently before the fix — reproduced
// 2026-09-04: an UNINDENTED body line beginning `signature:` was silently
// dropped, while an INDENTED one survived. So a test using only the indented
// form would have passed against the bug.
func TestMarkdownSigningBase_BodyKeywordLinesSurvive(t *testing.T) {
	post := "---\n" +
		"title: How polis signs things\n" +
		"published: 2026-09-04T10:00:00Z\n" +
		"signature: AAAAtheRealFrontmatterSignature\n" +
		"---\n" +
		"\n" +
		"A polis post carries its signature in frontmatter:\n" +
		"\n" +
		"signature: <base64 SSHSIG>\n" +
		"  signature: this one is indented\n" +
		"author: someone.example\n" +
		"\n" +
		"That is the whole trick.\n"

	got := MarkdownSigningBase(post, TypePost)

	if strings.Contains(got, "AAAAtheRealFrontmatterSignature") {
		t.Error("the frontmatter signature: line must be excluded from the signing base")
	}
	for _, want := range []string{
		"signature: <base64 SSHSIG>",
		"  signature: this one is indented",
		"author: someone.example",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("body line %q was dropped from the signing base — the prefix scan is back", want)
		}
	}

	// The comment base strips `author:` too, and must confine that to the
	// frontmatter block just the same.
	comment := "---\n" +
		"title: Re: signing\n" +
		"type: comment\n" +
		"author: commenter.example\n" +
		"signature: AAAAtheRealFrontmatterSignature\n" +
		"---\n" +
		"\n" +
		"Quoting your frontmatter back at you:\n" +
		"\n" +
		"author: someone.example\n" +
		"signature: <base64 SSHSIG>\n"

	got = MarkdownSigningBase(comment, TypeComment)

	if strings.Contains(got, "commenter.example") {
		t.Error("the frontmatter author: line must be excluded from the comment signing base")
	}
	if strings.Contains(got, "AAAAtheRealFrontmatterSignature") {
		t.Error("the frontmatter signature: line must be excluded from the comment signing base")
	}
	for _, want := range []string{"author: someone.example", "signature: <base64 SSHSIG>"} {
		if !strings.Contains(got, want) {
			t.Errorf("body line %q was dropped from the comment signing base", want)
		}
	}
}

// TestMarkdownSigningBase_PostKeepsItsAuthorLine locks the one difference
// between the two types: a post's `author:` is NOT excluded, because posts have
// never written one after signing. Collapsing the two rules would silently
// change every post's signing base.
func TestMarkdownSigningBase_PostKeepsItsAuthorLine(t *testing.T) {
	post := "---\ntitle: X\nauthor: me.example\nsignature: SIG\n---\n\nbody\n"
	got := MarkdownSigningBase(post, TypePost)
	if !strings.Contains(got, "author: me.example") {
		t.Error("a post's author: line is inside its signature and must not be stripped")
	}
	if strings.Contains(got, "SIG") {
		t.Error("a post's signature: line must be stripped")
	}
}

// ---------------------------------------------------------------------------
// The canary: old rule vs new rule, and every disagreement named.
// ---------------------------------------------------------------------------

// TestCanary_LegacyVsTypedBase runs both rules over the same corpus and requires
// that they agree EXCEPT where the old one was demonstrably wrong.
//
// ⭐ This is the "old and new side by side, report disagreement before cutover"
// the epic's Actors section requires. Judge, Patrol, Medic and the DS all verify
// signatures, so a silent difference here is a fleet-wide false alarm rather
// than a quiet failure — green unit tests on the new rule alone would not be
// evidence.
func TestCanary_LegacyVsTypedBase(t *testing.T) {
	// Fixtures that MUST agree byte-for-byte: these are the shapes every real
	// artifact on the fleet has.
	agree := []struct {
		name string
		typ  ObjectType
		in   string
	}{
		{"post, minimal", TypePost,
			"---\ntitle: Hello\nsignature: AAAA\n---\n\nBody text.\n"},
		{"post, full frontmatter", TypePost,
			"---\ntitle: Hello\npublished: 2026-09-04T10:00:00Z\ngenerator: polis-cli-go/0.67.0\n" +
				"current-version: sha256:abc\nversion-history:\n  - sha256:abc (2026-09-04T10:00:00Z)\n" +
				"signature: AAAA\n---\n\nBody text.\n"},
		{"post with a licence block", TypePost,
			"---\ntitle: Hello\npublished: 2026-09-04T10:00:00Z\nlicense:\n  v: pub.polis.license.v1\n" +
				"  profile: pub.polis.license.reserved/1\n  train-ai: n\n  search: y\n" +
				"  terms: https://x.example/license\n  asserted: 2026-09-04T10:00:00Z\n" +
				"signature: AAAA\n---\n\nBody text.\n"},
		{"comment", TypeComment,
			"---\ntitle: Re: Hello\ntype: comment\npublished: 2026-09-04T10:00:00Z\n" +
				"author: https://who.example\nin-reply-to:\n  url: https://x.example/p\n" +
				"  root-post: https://x.example/p\nsignature: AAAA\n---\n\nA reply.\n"},
		{"comment, no author yet (pre-signing shape)", TypeComment,
			"---\ntitle: Re: Hello\ntype: comment\nin-reply-to:\n  url: https://x.example/p\n---\n\nA reply.\n"},
		{"body mentioning signatures without a colon-prefixed line", TypePost,
			"---\ntitle: On signatures\nsignature: AAAA\n---\n\nThe signature: is inline, not at column 0.\n"},
		{"CRLF line endings", TypePost,
			"---\r\ntitle: Hello\r\nsignature: AAAA\r\n---\r\n\r\nBody.\r\n"},
		{"unterminated frontmatter", TypePost,
			"---\ntitle: Hello\nsignature: AAAA\n"},
		{"empty", TypePost, ""},
		{"no frontmatter at all", TypePost, "just a body\n"},
	}
	for _, tc := range agree {
		old := legacyContentToSign(tc.in, tc.typ == TypeComment)
		got := MarkdownSigningBase(tc.in, tc.typ)
		if old != got {
			t.Errorf("CANARY DISAGREEMENT on %q — this shape exists on the fleet:\n old=%q\n new=%q",
				tc.name, old, got)
		}
	}

	// Fixtures that MUST disagree, each one a case the old rule got wrong.
	// A silent agreement here means the fix is not in.
	disagree := []struct {
		name string
		typ  ObjectType
		in   string
	}{
		{"post whose body has a signature: line", TypePost,
			"---\ntitle: X\nsignature: AAAA\n---\n\nsignature: example\n"},
		{"comment whose body has an author: line", TypeComment,
			"---\ntitle: X\ntype: comment\nauthor: a.example\nsignature: AAAA\n---\n\nauthor: quoted\n"},
	}
	for _, tc := range disagree {
		old := legacyContentToSign(tc.in, tc.typ == TypeComment)
		got := MarkdownSigningBase(tc.in, tc.typ)
		if old == got {
			t.Errorf("%q: old and new agree, so the prefix-scan bug is still present", tc.name)
		}
		if len(got) <= len(old) {
			t.Errorf("%q: the new base must KEEP the body line the old one dropped", tc.name)
		}
	}
}

// TestMarkdownObjectTypeFor pins the discriminator to what published artifacts
// actually carry. Widening it (to "pub.polis.comment", say) would be a guess.
func TestMarkdownObjectTypeFor(t *testing.T) {
	cases := []struct {
		fmType     string
		hasInReply bool
		want       ObjectType
	}{
		{"", false, TypePost},
		{"post", false, TypePost},
		{"comment", false, TypeComment},
		{"", true, TypeComment},
		{"post", true, TypeComment},
	}
	for _, tc := range cases {
		if got := MarkdownObjectTypeFor(tc.fmType, tc.hasInReply); got != tc.want {
			t.Errorf("MarkdownObjectTypeFor(%q, %v) = %q, want %q", tc.fmType, tc.hasInReply, got, tc.want)
		}
	}
}

// TestUnsignedFrontmatterFields locks the rule table and its immutability.
func TestUnsignedFrontmatterFields(t *testing.T) {
	if got := UnsignedFrontmatterFields(TypePost); len(got) != 1 || got[0] != "signature" {
		t.Errorf("post unsigned fields = %v, want [signature]", got)
	}
	if got := UnsignedFrontmatterFields(TypeComment); len(got) != 2 || got[0] != "signature" || got[1] != "author" {
		t.Errorf("comment unsigned fields = %v, want [signature author]", got)
	}
	// A caller must not be able to edit the rule out from under the verifiers.
	UnsignedFrontmatterFields(TypePost)[0] = "clobbered"
	if got := UnsignedFrontmatterFields(TypePost); got[0] != "signature" {
		t.Error("UnsignedFrontmatterFields returned the live rule slice, not a copy")
	}
	// An unknown type excludes nothing rather than guessing.
	if got := UnsignedFrontmatterFields(ObjectType("pub.polis.nope")); len(got) != 0 {
		t.Errorf("unknown type unsigned fields = %v, want none", got)
	}
}
