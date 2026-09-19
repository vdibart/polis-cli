package attestation

import (
	"os"
	"strings"
	"testing"
)

// TestCanonicalJSON_Vector locks an attestation record's canonical bytes, in full,
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
// ⛔ If this test fails, the signing base changed and every existing an attestation record
// signature just stopped verifying. Do not update the golden string to match
// the code — work out what moved and put it back.
const goldenAttestation = `{"type":"pub.polis.attestation","issuer":"https://judge.example","predicate":"pub.polis.attestation.integrity","subject":{"type":"uri","id":"https://alice.example/content/pub.polis.core/post/20260827/hello.md","version":"sha256:0000000000000000000000000000000000000000000000000000000000000000"},"payload":{"checks":"14","result":"pass"},"asserted":"2026-08-29T11:00:00Z","generator":"polis-cli-go/0.67.0"}`

const goldenAttestationMinimal = `{"type":"pub.polis.attestation","issuer":"https://judge.example","predicate":"pub.polis.attestation.integrity","subject":{"type":"identity","id":"https://alice.example"},"asserted":"2026-08-29T11:00:00Z","generator":"polis-cli-go/0.67.0"}`

func TestCanonicalJSON_Vector(t *testing.T) {
	r := &Record{
		Type:      TypeName,
		Issuer:    "https://judge.example",
		Predicate: "pub.polis.attestation.integrity",
		Subject: Subject{
			Type:    SubjectURI,
			ID:      "https://alice.example/content/pub.polis.core/post/20260827/hello.md",
			Version: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		},
		// Declared out of order on purpose: map keys serialise
		// LEXICOGRAPHICALLY, so "checks" precedes "result" in the bytes below
		// regardless of how the map was built. That is specification, not an
		// implementation detail — a reimplementer that preserves insertion
		// order produces a different digest.
		Payload:   map[string]string{"result": "pass", "checks": "14"},
		Asserted:  "2026-08-29T11:00:00Z",
		Generator: "polis-cli-go/0.67.0",
	}

	got, err := CanonicalJSON(r)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if string(got) != goldenAttestation {
		t.Errorf("canonical bytes moved:\n got: %s\nwant: %s", got, goldenAttestation)
	}

	// The minimal record: no payload and no subject version. Both are OMITTED
	// ENTIRELY rather than written empty, so "absent" has one byte sequence.
	min := &Record{
		Type:      TypeName,
		Issuer:    "https://judge.example",
		Predicate: "pub.polis.attestation.integrity",
		Subject:   Subject{Type: SubjectIdentity, ID: "https://alice.example"},
		Asserted:  "2026-08-29T11:00:00Z",
		Generator: "polis-cli-go/0.67.0",
	}
	got, err = CanonicalJSON(min)
	if err != nil {
		t.Fatalf("CanonicalJSON (minimal): %v", err)
	}
	if string(got) != goldenAttestationMinimal {
		t.Errorf("minimal-record canonical bytes moved:\n got: %s\nwant: %s", got, goldenAttestationMinimal)
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
	assertSpecPublishes(t, goldenAttestation)
	assertSpecPublishes(t, goldenAttestationMinimal)
}

func assertSpecPublishes(t *testing.T, vector string) {
	t.Helper()
	data, err := os.ReadFile(signingBaseSpec)
	if err != nil {
		t.Fatalf("the specification is part of this format, not an optional extra: %v", err)
	}
	if !strings.Contains(string(data), vector) {
		t.Errorf("%s does not publish these exact bytes for an attestation record:\n%s",
			signingBaseSpec, vector)
	}
}
