package license

import (
	"os"
	"strings"
	"testing"
)

// TestCanonicalJSON_Vector locks a licence's canonical bytes, in full,
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
// ⛔ If this test fails, the signing base changed and every existing a licence
// signature just stopped verifying. Do not update the golden string to match
// the code — work out what moved and put it back.
const goldenLicense = `{"type":"pub.polis.license","terms":{"v":"pub.polis.license.v1","profile":"pub.polis.license.reserved/1","train-ai":"n","search":"y","ai-input":"n","attribution":"required","terms":"https://alice.example/license","contact":"https://alice.example/license","asserted":"2026-08-27T14:02:00Z"},"created":"2026-08-27T14:02:00Z","updated":"2026-08-27T14:02:00Z","generator":"polis-cli-go/0.67.0"}`

func TestCanonicalJSON_Vector(t *testing.T) {
	f := &File{
		Type: TypeName,
		Terms: &Terms{
			V:           SchemaVersion,
			Profile:     ProfileReserved,
			TrainAI:     Disallow,
			Search:      Allow,
			AIInput:     Disallow,
			Attribution: AttributionRequired,
			Terms:       "https://alice.example/license",
			Contact:     "https://alice.example/license",
			Asserted:    "2026-08-27T14:02:00Z",
		},
		Created:   "2026-08-27T14:02:00Z",
		Updated:   "2026-08-27T14:02:00Z",
		Generator: "polis-cli-go/0.67.0",
	}

	got, err := canonicalJSON(f)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	if string(got) != goldenLicense {
		t.Errorf("canonical bytes moved:\n got: %s\nwant: %s", got, goldenLicense)
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
	assertSpecPublishes(t, goldenLicense)
}

func assertSpecPublishes(t *testing.T, vector string) {
	t.Helper()
	data, err := os.ReadFile(signingBaseSpec)
	if err != nil {
		t.Fatalf("the specification is part of this format, not an optional extra: %v", err)
	}
	if !strings.Contains(string(data), vector) {
		t.Errorf("%s does not publish these exact bytes for a licence:\n%s",
			signingBaseSpec, vector)
	}
}
