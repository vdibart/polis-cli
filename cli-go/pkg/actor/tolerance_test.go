package actor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// SIGNET epic 47 — tolerant verifiers, over an operator's actor registry.
//
// ⭐ THE TYPE WHOSE WHOLE POINT IS THAT STRANGERS READ IT. A registry is
// published so OTHER operators can check what we say we run — which means the
// reader is, by construction, running a different build from the writer. A
// verifier that called a newer operator's registry forged would break the one
// property this file has.
//
// ⚠️ TWO SIGNATURES, TWO FIELD SETS, TWO ANSWERS. The file signature covers the
// whole document; an entry's countersignature covers only `v`, `operator` and
// that entry. So an unrecognised member at the top of the file makes the FILE
// uncheckable and leaves every countersignature exactly as checkable as before.

func registrySignedOverWiderBytes(t *testing.T, canonical string, doc map[string]any, priv []byte) []byte {
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

func signedRegistry(t *testing.T, priv []byte) *Registry {
	t.Helper()
	r := vectorRegistry()
	r.Signature = ""
	if err := Sign(r, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

func TestToleranceRowValidWithNothingUnrecognised(t *testing.T) {
	priv, pub := keypair(t)
	raw, _ := json.Marshal(signedRegistry(t, priv))

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
	priv, pub := keypair(t)
	raw, _ := json.Marshal(signedRegistry(t, priv))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["contact"] = "https://polis.pub/abuse"
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
	if len(got) != 1 || got[0] != "contact" {
		t.Fatalf("UnrecognisedFields = %q, want [contact]", got)
	}
	if note := signing.UncoveredNote(got); !strings.Contains(note, "not covered by the signature") {
		t.Errorf("UncoveredNote = %q — the valid row must SAY the field is uncovered", note)
	}
}

func TestToleranceRowUnknownWhenSignedOverAWiderFieldSet(t *testing.T) {
	priv, pub := keypair(t)
	canonical := `{"v":"pub.polis.actor-registry.v1","operator":"polis.polis.pub","actors":[{"domain":"judge.polis.pub","authority":"operator","expected_actions":["pub.polis.attestation.integrity"]}],"asserted":"2026-09-05T14:02:00Z","generator":"polis-cli-go/newer","contact":"https://polis.pub/abuse"}`
	raw := registrySignedOverWiderBytes(t, canonical, map[string]any{
		"v": SchemaVersion, "operator": "polis.polis.pub",
		"actors": []any{map[string]any{
			"domain": "judge.polis.pub", "authority": AuthorityOperator,
			"expected_actions": []any{"pub.polis.attestation.integrity"},
		}},
		"asserted": "2026-09-05T14:02:00Z", "generator": "polis-cli-go/newer",
		"contact": "https://polis.pub/abuse",
	}, priv)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	status, verr := Verify(parsed, pub)
	if status == StatusInvalid {
		t.Fatal("a registry signed over fields this build does not declare reported INVALID")
	}
	if status != StatusUnknown {
		t.Fatalf("status = %q (%v), want unknown", status, verr)
	}
	if verr == nil || !strings.Contains(verr.Error(), "does not understand") {
		t.Errorf("explanation = %v, want it to name the fields", verr)
	}
}

func TestToleranceRowInvalidWhenNothingIsUnrecognised(t *testing.T) {
	priv, pub := keypair(t)
	r := signedRegistry(t, priv)
	r.Actors[0].Domain = "attacker.example"
	raw, _ := json.Marshal(r)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if status, _ := Verify(parsed, pub); status != StatusInvalid {
		t.Fatalf("status = %q, want invalid", status)
	}
}

func TestToleranceSeesAMemberInsideAnActorEntry(t *testing.T) {
	priv, pub := keypair(t)
	canonical := `{"v":"pub.polis.actor-registry.v1","operator":"polis.polis.pub","actors":[{"domain":"judge.polis.pub","authority":"operator","expected_actions":["pub.polis.attestation.integrity"],"since":"2026-01-01T00:00:00Z"}],"asserted":"2026-09-05T14:02:00Z","generator":"polis-cli-go/newer"}`
	raw := registrySignedOverWiderBytes(t, canonical, map[string]any{
		"v": SchemaVersion, "operator": "polis.polis.pub",
		"actors": []any{map[string]any{
			"domain": "judge.polis.pub", "authority": AuthorityOperator,
			"expected_actions": []any{"pub.polis.attestation.integrity"},
			"since":            "2026-01-01T00:00:00Z",
		}},
		"asserted": "2026-09-05T14:02:00Z", "generator": "polis-cli-go/newer",
	}, priv)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := parsed.UnrecognisedFields()
	if len(got) != 1 || got[0] != "actors[0].since" {
		t.Fatalf("UnrecognisedFields = %q, want [actors[0].since]", got)
	}
	if status, _ := Verify(parsed, pub); status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}

func TestTheTwoSignaturesGetSeparateToleranceAnswers(t *testing.T) {
	// ⛔ The bug this pins: folding the file-level list into the entry-level one
	// would report an actor's countersignature uncheckable because of a field
	// the actor never saw. `contact` is not in EntryCanonicalJSON's field set,
	// so it cannot affect a countersignature — and `actors[0].since` is.
	opPriv, opPub := keypair(t)
	acPriv, acPub := keypair(t)

	r := vectorRegistry()
	r.Signature = ""
	cs, err := Countersign(r, &r.Actors[0], acPriv)
	if err != nil {
		t.Fatalf("Countersign: %v", err)
	}
	r.Actors[0].Countersignature = cs
	if err := Sign(r, opPriv); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	raw, _ := json.Marshal(r)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["contact"] = "https://polis.pub/abuse"
	raw, _ = json.Marshal(doc)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// A member outside both signing bases leaves BOTH signatures verifying.
	if st, verr := Verify(parsed, opPub); st != StatusValid {
		t.Fatalf("file status = %q (%v), want valid", st, verr)
	}
	if st, verr := VerifyCountersignature(parsed, &parsed.Actors[0], acPub); st != StatusValid {
		t.Fatalf("countersignature status = %q (%v), want valid", st, verr)
	}

	// Now the sharp case: a member INSIDE the file's signed bytes that the
	// countersignature's field set does not contain. The file becomes
	// uncheckable by this build; the countersignature must not.
	wider := `{"v":"pub.polis.actor-registry.v1","operator":"polis.polis.pub","actors":[{"domain":"judge.polis.pub","authority":"operator","expected_actions":["pub.polis.attestation.integrity"],"countersignature":` + mustJSON(t, cs) + `}],"asserted":"2026-09-05T14:02:00Z","generator":"polis-cli-go/0.67.0","contact":"https://polis.pub/abuse"}`
	raw2 := registrySignedOverWiderBytes(t, wider, map[string]any{
		"v": SchemaVersion, "operator": "polis.polis.pub",
		"actors": []any{map[string]any{
			"domain": "judge.polis.pub", "authority": AuthorityOperator,
			"expected_actions": []any{"pub.polis.attestation.integrity"},
			"countersignature": cs,
		}},
		"asserted": "2026-09-05T14:02:00Z", "generator": "polis-cli-go/0.67.0",
		"contact": "https://polis.pub/abuse",
	}, opPriv)

	parsed2, err := Parse(raw2)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if st, _ := Verify(parsed2, opPub); st != StatusUnknown {
		t.Fatalf("file status = %q, want unknown — the file was signed over a field set this build cannot rebuild", st)
	}
	// ⭐ The point: the entry's own subtree carries nothing unrecognised, so the
	// countersignature answer is untouched by the file-level member.
	if got := parsed2.entryUnrecognised(&parsed2.Actors[0]); len(got) != 0 {
		t.Fatalf("entryUnrecognised = %q, want none — `contact` is not in the entry's signing base", got)
	}
	if st, verr := VerifyCountersignature(parsed2, &parsed2.Actors[0], acPub); st != StatusValid {
		t.Fatalf("countersignature status = %q (%v), want valid", st, verr)
	}
}

func mustJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
