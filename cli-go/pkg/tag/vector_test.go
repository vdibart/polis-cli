package tag

import (
	"os"
	"strings"
	"testing"
)

// TestCanonicalJSON_Vector locks a tag file's canonical bytes, in full,
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
// ⛔ If this test fails, the signing base changed and every existing a tag file
// signature just stopped verifying. Do not update the golden string to match
// the code — work out what moved and put it back.
const goldenTag = `{"tag":"reading","targets":[{"uri":"https://alice.example/content/pub.polis.core/post/20260827/hello.md","added":"2026-08-27T14:02:00Z"}],"created":"2026-08-27T14:02:00Z","updated":"2026-08-27T14:02:00Z","generator":"polis-cli-go/0.67.0"}`

func TestCanonicalJSON_Vector(t *testing.T) {
	tf := &TagFile{
		Tag: "reading",
		Targets: []TagTarget{{
			URI:   "https://alice.example/content/pub.polis.core/post/20260827/hello.md",
			Added: "2026-08-27T14:02:00Z",
		}},
		Created:   "2026-08-27T14:02:00Z",
		Updated:   "2026-08-27T14:02:00Z",
		Generator: "polis-cli-go/0.67.0",
	}

	got, err := canonicalJSON(tf)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	if string(got) != goldenTag {
		t.Errorf("canonical bytes moved:\n got: %s\nwant: %s", got, goldenTag)
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
	assertSpecPublishes(t, goldenTag)
}

func assertSpecPublishes(t *testing.T, vector string) {
	t.Helper()
	data, err := os.ReadFile(signingBaseSpec)
	if err != nil {
		t.Fatalf("the specification is part of this format, not an optional extra: %v", err)
	}
	if !strings.Contains(string(data), vector) {
		t.Errorf("%s does not publish these exact bytes for a tag file:\n%s",
			signingBaseSpec, vector)
	}
}
