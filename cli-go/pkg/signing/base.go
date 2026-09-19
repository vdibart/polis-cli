package signing

import "strings"

// The typed signing base — `pub.polis.signing-base.v1`.
//
// SIGNET epic 08. Full prose specification, per type and field-by-field:
// docs/signet/spec/signing-base.md. That document and this file must agree; a
// second implementation is meant to be buildable from the document alone.
//
// ⛔ v1 IS EXACTLY THE BYTES ALREADY IN PRODUCTION. Nothing here changed what
// gets signed. 1,077 signed artifacts are live across the fleet and none of
// them can be re-signed — the private keys that made most of them are on other
// people's machines — so "improving" the canonicalization is not available as a
// move. A change to these bytes is a change to whether someone's published work
// still verifies. See Signet epic 08, D1.
//
// ⚠️ v1 IS THE UNMARKED VERSION, and that is a deliberate consequence of the
// above rather than an oversight. A version marker has to live INSIDE the signed
// bytes to be worth anything (otherwise stripping it downgrades the artifact),
// and adding a field inside the signed bytes of an artifact type that already
// exists would invalidate every artifact of that type. So: absent marker ⇒ v1.
// A future v2 announces itself in a signed field, and v1 is never retired —
// every verifier carries it forever.
//
// ⭐ The version exists so canonicalization CAN change one day without breaking
// history, not because it is about to.

// BaseVersion identifies the signing-base rules this build implements. It is
// the version an artifact is verified under when it carries no marker of its
// own, which today is every artifact.
const BaseVersion = "pub.polis.signing-base.v1"

// ObjectType names a signed polis object.
//
// It replaces the boolean discriminator `ContentToSign(content, isComment bool)`
// carried until epic 08. A bool has exactly two inhabitants and no name for
// either: it cannot be extended, it cannot be logged, and passing it wrongly is
// a silent false "this content was tampered with". The rule that produced the
// original defect — a verifier applied the post rule to a comment and every
// blessed comment failed verification — is now selected by a named type.
type ObjectType string

const (
	// TypePost is a `pub.polis.post` — markdown, and every frontmatter field
	// except `signature` is inside the signature.
	TypePost ObjectType = "pub.polis.post"
	// TypeComment is a `pub.polis.comment` — markdown, and `author` is written
	// after signing alongside `signature`, so neither is inside the signature.
	TypeComment ObjectType = "pub.polis.comment"
)

// unsignedFrontmatterFields declares, per object type, the TOP-LEVEL
// frontmatter keys that are written AFTER signing and are therefore not part of
// the signed bytes.
//
// ⛔ THIS TABLE IS THE RULE. It is data rather than a branch so that a producer,
// a verifier and the spec test read the same declaration instead of three
// copies of an `if`. Adding a frontmatter key WITHOUT adding it here means the
// key is inside the signature — which is the default and usually what you want.
// Adding one here means the producer must write it only in the final,
// post-signing frontmatter.
var unsignedFrontmatterFields = map[ObjectType][]string{
	TypePost:    {"signature"},
	TypeComment: {"signature", "author"},
}

// UnsignedFrontmatterFields returns the top-level frontmatter keys excluded
// from typ's signing base, in the order they are stripped. The returned slice
// is a copy; callers may not mutate the rule.
func UnsignedFrontmatterFields(typ ObjectType) []string {
	fields := unsignedFrontmatterFields[typ]
	out := make([]string, len(fields))
	copy(out, fields)
	return out
}

// MarkdownObjectTypeFor reports which object type a markdown artifact was
// signed under, from the frontmatter discriminators every verifier already
// reads: the `type:` value and whether an `in-reply-to:` block is present.
//
// ⚠️ It matches the bare value "comment", not "pub.polis.comment", because that
// is what published comments actually carry in `type:`. Widening it would be a
// guess about artifacts that do not exist.
func MarkdownObjectTypeFor(frontmatterType string, hasInReplyTo bool) ObjectType {
	if frontmatterType == "comment" || hasInReplyTo {
		return TypeComment
	}
	return TypePost
}

// MarkdownSigningBase reconstructs the exact bytes that were signed for a
// markdown object of type typ, from its full on-disk or on-the-wire form
// (frontmatter + body).
//
// The rule, in full:
//
//  1. Locate the leading frontmatter block — line 0 is `---` and the block ends
//     at the next line that is `---`. No frontmatter ⇒ nothing is excluded.
//  2. Drop, FROM THAT BLOCK ONLY, each line beginning at column 0 with one of
//     typ's unsigned field names followed by `:`.
//  3. Canonicalize the remainder (see CanonicalizeContent).
//
// ⛔ STEP 1 IS THE FIX, and it is the whole reason this function replaced
// ContentToSign. The old rule scanned the WHOLE DOCUMENT for the prefix, so a
// body line legitimately beginning `signature:` — a YAML example in a post about
// signing, a quoted frontmatter block, a code fence — was dropped from the
// reconstruction. The bytes then differed from what was signed and AUTHENTIC
// CONTENT FAILED VERIFICATION, reported by Judge and Patrol as tampering.
// Matching a key by its shape
// anywhere in a document, instead of by its structural position, is the bug
// class; the same class appears in every frontmatter parser that trims a key
// before matching it and so cannot tell a nested key from a top-level one.
//
// ⭐ The fix can only ever make MORE artifacts verify, never fewer: it changes
// the reconstruction only for documents whose body contains such a line, and
// for those the old reconstruction was already wrong.
func MarkdownSigningBase(content string, typ ObjectType) string {
	excluded := unsignedFrontmatterFields[typ]
	lines := strings.Split(content, "\n")
	end := frontmatterEnd(lines)

	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i <= end && hasTopLevelKey(line, excluded) {
			continue
		}
		out = append(out, line)
	}
	return CanonicalizeContent(strings.Join(out, "\n"))
}

// frontmatterEnd returns the index of the line closing the leading frontmatter
// block, or -1 when the content has no frontmatter at all.
//
// ⚠️ An UNTERMINATED block (opening `---`, no closing one) extends to the end of
// the document. That is not tidiness — sitecheck.ParseFrontmatter reads a
// `signature:` out of such a file and hands it to verification, so the block
// boundary must cover everything that parser was willing to read a field from,
// or the two disagree about what was signed. It is also exactly what the old
// document-wide scan did for that shape, so the degenerate case is unchanged.
func frontmatterEnd(lines []string) int {
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return -1
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return i
		}
	}
	return len(lines) - 1
}

// hasTopLevelKey reports whether line declares one of keys at column 0.
//
// ⚠️ No trimming, deliberately. A leading space means the line is a CHILD of the
// key above it — `license:`'s block, `in-reply-to:`'s `url:` — and a child named
// `signature` is a different field from the top-level `signature`. Trimming
// first is precisely the defect this function exists not to have.
func hasTopLevelKey(line string, keys []string) bool {
	for _, k := range keys {
		if strings.HasPrefix(line, k+":") {
			return true
		}
	}
	return false
}
