package metadata

import (
	"os"
	"strings"
	"testing"
)

// TestCanonicalBlessedJSON_Vector locks a blessing list's canonical bytes, in full,
// against the exact vector published in docs/signet/spec/signing-base.md —
// Signet epic 08, D2.
//
// ⭐ THE VECTOR IS THE SPEC AND THIS TEST IS WHAT KEEPS IT TRUE. The published
// field order is Go's STRUCT DECLARATION order, which is invisible from the
// artifact: an independent reimplementer writes canonical JSON by sorting keys,
// as RFC 8785 and every other canonical-JSON convention do, produces different
// bytes, and concludes our signatures are invalid. Documenting the order is the
// whole reimplementability fix; a golden vector is what stops the document from
// quietly going stale the next time a field is added.
//
// ⛔ If this test fails, the signing base changed and every existing a blessing list
// signature just stopped verifying. Do not update the golden string to match
// the code — work out what moved and put it back.
const goldenBlessed = `{"version":"polis-cli-go/0.67.0","comments":[{"post":"https://alice.example/content/pub.polis.core/post/20260827/hello.md","blessed":[{"url":"https://bob.example/content/pub.polis.core/comment/20260828/re-hello.md","version":"sha256:0000000000000000000000000000000000000000000000000000000000000000","blessed_at":"2026-08-30T08:00:00Z"}]}]}`

func TestCanonicalBlessedJSON_Vector(t *testing.T) {
	bc := &BlessedComments{
		Version: "polis-cli-go/0.67.0",
		Comments: []PostComments{{
			Post: "https://alice.example/content/pub.polis.core/post/20260827/hello.md",
			Blessed: []BlessedComment{{
				URL:       "https://bob.example/content/pub.polis.core/comment/20260828/re-hello.md",
				Version:   "sha256:0000000000000000000000000000000000000000000000000000000000000000",
				BlessedAt: "2026-08-30T08:00:00Z",
			}},
		}},
	}

	got, err := canonicalBlessedJSON(bc)
	if err != nil {
		t.Fatalf("canonicalBlessedJSON: %v", err)
	}
	if string(got) != goldenBlessed {
		t.Errorf("canonical bytes moved:\n got: %s\nwant: %s", got, goldenBlessed)
	}
}

// signingBaseSpec is the public specification of the signing base. It is a
// DELIVERABLE, not a write-up of one: the acceptance test for this format is
// that a competent stranger can produce bytes that verify against ours without
// reading any Go.
const signingBaseSpec = "../../../docs/signet/spec/signing-base.md"

// TestSpecPublishesTheVector is the mechanical check that keeps the spec honest.
//
// ⚠️ Prose and code drift the moment a human transcribes between them, and a
// spec whose worked example is subtly wrong is worse than no spec — it produces
// a second implementation that is confidently incompatible. So the documented
// bytes are compared against the SAME constant the golden test pins, on every
// test run, rather than by eye at review time.
func TestSpecPublishesTheVector(t *testing.T) {
	assertSpecPublishes(t, goldenBlessed)
}

func assertSpecPublishes(t *testing.T, vector string) {
	t.Helper()
	data, err := os.ReadFile(signingBaseSpec)
	if err != nil {
		t.Fatalf("the specification is part of this format, not an optional extra: %v", err)
	}
	if !strings.Contains(string(data), vector) {
		t.Errorf("%s does not publish these exact bytes for a blessing list:\n%s",
			signingBaseSpec, vector)
	}
}
