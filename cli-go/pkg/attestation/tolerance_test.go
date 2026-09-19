package attestation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// SIGNET epic 47 — tolerant verifiers, over an attestation record.
//
// ⭐ M1: this package ALREADY took the tolerant posture when READING — Load's
// doc says an unrecognised predicate, an unrecognised subject type and an
// unknown top-level key all load, because a reader that rejected them would
// make the format unextendable by anyone but us. What this epic adds is the
// same posture when CHECKING, which is where the rejection had moved to.

func recSignedOverWiderBytes(t *testing.T, canonical string, doc map[string]any, priv []byte) []byte {
	t.Helper()
	sig, err := signing.SignContent([]byte(canonical), priv)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}
	doc["signature"] = sig
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func signedSampleRecord(t *testing.T, priv []byte) *Record {
	t.Helper()
	r := sampleRecord()
	canonical, err := CanonicalJSON(r)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	sig, err := signing.SignContent(canonical, priv)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}
	r.Signature = sig
	r.Version = ContentVersion(canonical)
	return r
}

func TestToleranceRowValidWithNothingUnrecognised(t *testing.T) {
	priv, pub := newTestKeys(t)
	raw, _ := json.Marshal(signedSampleRecord(t, priv))

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := parsed.UnrecognisedFields(); len(got) != 0 {
		t.Fatalf("UnrecognisedFields = %q, want none", got)
	}
	if status, verr := Verify(parsed, pub); status != StatusValid {
		t.Fatalf("status = %q (%v), want valid", status, verr)
	}
}

func TestToleranceRowValidButTheExtraFieldsAreNotCovered(t *testing.T) {
	priv, pub := newTestKeys(t)
	raw, _ := json.Marshal(signedSampleRecord(t, priv))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["expires"] = "2027-01-01T00:00:00Z"
	raw, _ = json.Marshal(doc)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	status, verr := Verify(parsed, pub)
	if status != StatusValid {
		t.Fatalf("status = %q (%v), want valid", status, verr)
	}
	got := parsed.UnrecognisedFields()
	if len(got) != 1 || got[0] != "expires" {
		t.Fatalf("UnrecognisedFields = %q, want [expires]", got)
	}
	if note := signing.UncoveredNote(got); !strings.Contains(note, "not covered by the signature") {
		t.Errorf("UncoveredNote = %q — the valid row must SAY the field is uncovered", note)
	}
}

func TestToleranceRowUnknownWhenSignedOverAWiderFieldSet(t *testing.T) {
	// `expires` is RESERVED in this type's doc as a future signed field. When it
	// ships, every verifier already in the field meets exactly this document —
	// which, before this rule, it called forged.
	priv, pub := newTestKeys(t)
	canonical := `{"type":"pub.polis.attestation","issuer":"https://vdibart.polis.pub","predicate":"pub.polis.attestation.same-as","subject":{"type":"identity","id":"https://vincent.example.com"},"asserted":"2026-08-28T00:00:00Z","expires":"2027-01-01T00:00:00Z","generator":"polis-cli-go/newer"}`
	raw := recSignedOverWiderBytes(t, canonical, map[string]any{
		"type":      TypeName,
		"issuer":    "https://vdibart.polis.pub",
		"predicate": PredicateSameAs,
		"subject":   map[string]any{"type": "identity", "id": "https://vincent.example.com"},
		"asserted":  "2026-08-28T00:00:00Z",
		"expires":   "2027-01-01T00:00:00Z",
		"generator": "polis-cli-go/newer",
	}, priv)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	status, verr := Verify(parsed, pub)
	if status == StatusInvalid {
		t.Fatal("a record signed over fields this build does not declare reported INVALID")
	}
	if status != StatusUnknown {
		t.Fatalf("status = %q (%v), want unknown", status, verr)
	}
	if verr == nil || !strings.Contains(verr.Error(), "does not understand") {
		t.Errorf("explanation = %v, want it to name the fields", verr)
	}

	// Both other entry points answer the same. VerifyAgainst must not collapse
	// an unknown to invalid just because its loop ran out of keys.
	if st, _, _ := VerifyWithHistory(parsed, pub, nil); st != StatusUnknown {
		t.Errorf("VerifyWithHistory status = %q, want unknown", st)
	}
	if st, _ := VerifyAgainst(parsed, [][]byte{pub}); st != StatusUnknown {
		t.Errorf("VerifyAgainst status = %q, want unknown", st)
	}
}

func TestToleranceRowInvalidWhenNothingIsUnrecognised(t *testing.T) {
	priv, pub := newTestKeys(t)
	r := signedSampleRecord(t, priv)
	r.Subject.ID = "https://attacker.example"
	raw, _ := json.Marshal(r)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if status, _ := Verify(parsed, pub); status != StatusInvalid {
		t.Fatalf("status = %q, want invalid", status)
	}
	if st, _ := VerifyAgainst(parsed, [][]byte{pub}); st != StatusInvalid {
		t.Fatalf("VerifyAgainst status = %q, want invalid", st)
	}
}

func TestToleranceSeesAMemberInsideTheSubject(t *testing.T) {
	priv, pub := newTestKeys(t)
	canonical := `{"type":"pub.polis.attestation","issuer":"https://vdibart.polis.pub","predicate":"pub.polis.attestation.same-as","subject":{"type":"identity","id":"https://vincent.example.com","fragment":"key-1"},"asserted":"2026-08-28T00:00:00Z","generator":"polis-cli-go/newer"}`
	raw := recSignedOverWiderBytes(t, canonical, map[string]any{
		"type":      TypeName,
		"issuer":    "https://vdibart.polis.pub",
		"predicate": PredicateSameAs,
		"subject":   map[string]any{"type": "identity", "id": "https://vincent.example.com", "fragment": "key-1"},
		"asserted":  "2026-08-28T00:00:00Z",
		"generator": "polis-cli-go/newer",
	}, priv)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := parsed.UnrecognisedFields()
	if len(got) != 1 || got[0] != "subject.fragment" {
		t.Fatalf("UnrecognisedFields = %q, want [subject.fragment]", got)
	}
	if status, _ := Verify(parsed, pub); status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}

func TestAnUnknownPayloadKeyIsContentNotAWiderFieldSet(t *testing.T) {
	// ⚠️ The payload is an OPEN MAP by design, and its keys are data. Treating
	// a new payload key as an unrecognised field would make every record
	// carrying one uncheckable — and the signature covers the payload, so a
	// record with an unseen key still verifies perfectly well.
	priv, pub := newTestKeys(t)
	r := sampleRecord()
	r.Payload = map[string]string{"never_seen_before": "1", "result": "pass"}
	canonical, err := CanonicalJSON(r)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	sig, err := signing.SignContent(canonical, priv)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}
	r.Signature = sig
	raw, _ := json.Marshal(r)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := parsed.UnrecognisedFields(); len(got) != 0 {
		t.Fatalf("payload keys reported as unrecognised: %q", got)
	}
	if status, verr := Verify(parsed, pub); status != StatusValid {
		t.Fatalf("status = %q (%v), want valid", status, verr)
	}
}
