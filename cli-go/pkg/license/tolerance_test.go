package license

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// SIGNET epic 47 — tolerant verifiers, over a signed licence.
//
// ⭐ THE TYPE MOST LIKELY TO GROW A FIELD. `terms` carries AIPREF and RSL
// vocabulary, and both are live standards with their own release cycles: a
// member this build has never heard of is the EXPECTED case here, not the
// exotic one. A verifier that called those licences forged would make polis the
// thing that broke when a standard moved.
//
// ⛔ This package's verifier had NO status vocabulary before this epic — Verify
// returned a bare bool, so the package could not say "I could not check this"
// at all. SignatureStatus and VerifyStatus are epic 47's; see Q1 in the plan.

func licenceSignedOverWiderBytes(t *testing.T, canonical string, doc map[string]any, priv []byte) []byte {
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

func signedLicence(t *testing.T, priv []byte) *File {
	t.Helper()
	terms, err := ProfileTerms(ProfileReserved, "https://alice.example", "2026-08-27T14:02:00Z")
	if err != nil {
		t.Fatalf("ProfileTerms: %v", err)
	}
	f := &File{
		Type:      TypeName,
		Terms:     terms,
		Created:   "2026-08-27T14:02:00Z",
		Updated:   "2026-08-27T14:02:00Z",
		Generator: "polis-cli-go/test",
	}
	canonical, err := canonicalJSON(f)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	sig, err := signing.SignContent(canonical, priv)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}
	f.Signature = sig
	return f
}

func TestToleranceRowValidWithNothingUnrecognised(t *testing.T) {
	priv, pub := testKeys(t)
	raw, _ := json.Marshal(signedLicence(t, priv))

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := parsed.UnrecognisedFields(); len(got) != 0 {
		t.Fatalf("UnrecognisedFields = %q, want none", got)
	}
	if status, verr := VerifyStatus(parsed, pub); status != StatusValid {
		t.Fatalf("status = %q (%v), want valid", status, verr)
	}
}

func TestToleranceRowValidButTheExtraFieldsAreNotCovered(t *testing.T) {
	priv, pub := testKeys(t)
	raw, _ := json.Marshal(signedLicence(t, priv))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["jurisdiction"] = "US-NY"
	raw, _ = json.Marshal(doc)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	status, verr := VerifyStatus(parsed, pub)
	if status != StatusValid {
		t.Fatalf("status = %q (%v), want valid", status, verr)
	}
	got := parsed.UnrecognisedFields()
	if len(got) != 1 || got[0] != "jurisdiction" {
		t.Fatalf("UnrecognisedFields = %q, want [jurisdiction]", got)
	}
	if note := signing.UncoveredNote(got); !strings.Contains(note, "not covered by the signature") {
		t.Errorf("UncoveredNote = %q — the valid row must SAY the field is uncovered", note)
	}
}

func TestToleranceRowUnknownWhenSignedOverAWiderFieldSet(t *testing.T) {
	// A future AIPREF or RSL key inside `terms`, signed by a newer writer. The
	// member is two levels down, which is the D1 case for this type.
	priv, pub := testKeys(t)
	canonical := `{"type":"pub.polis.license","terms":{"v":"pub.polis.license.v1","profile":"pub.polis.license.reserved/1","train-ai":"n","search":"y","ai-input":"n","attribution":"required","terms":"https://alice.example/license","contact":"https://alice.example/license","asserted":"2026-08-27T14:02:00Z","train-genai":"n"},"created":"2026-08-27T14:02:00Z","updated":"2026-08-27T14:02:00Z","generator":"polis-cli-go/newer"}`
	raw := licenceSignedOverWiderBytes(t, canonical, map[string]any{
		"type": TypeName,
		"terms": map[string]any{
			"v": "pub.polis.license.v1", "profile": "pub.polis.license.reserved/1",
			"train-ai": "n", "search": "y", "ai-input": "n", "attribution": "required",
			"terms": "https://alice.example/license", "contact": "https://alice.example/license",
			"asserted": "2026-08-27T14:02:00Z", "train-genai": "n",
		},
		"created": "2026-08-27T14:02:00Z", "updated": "2026-08-27T14:02:00Z",
		"generator": "polis-cli-go/newer",
	}, priv)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := parsed.UnrecognisedFields()
	if len(got) != 1 || got[0] != "terms.train-genai" {
		t.Fatalf("UnrecognisedFields = %q, want [terms.train-genai]", got)
	}
	status, verr := VerifyStatus(parsed, pub)
	if status == StatusInvalid {
		t.Fatal("a licence signed over vocabulary this build does not declare reported INVALID — polis must not be what breaks when a standard moves")
	}
	if status != StatusUnknown {
		t.Fatalf("status = %q (%v), want unknown", status, verr)
	}
	if verr == nil || !strings.Contains(verr.Error(), "does not understand") {
		t.Errorf("explanation = %v, want it to name the fields", verr)
	}

	// The same answer through Status, which is what the key-history walk in
	// sitecheck uses — the walk lives outside the package, the RULE does not.
	if st, why := Status(parsed, false); st != StatusUnknown || why == "" {
		t.Errorf("Status(false) = %q / %q, want unknown with an explanation", st, why)
	}
}

func TestToleranceRowInvalidWhenNothingIsUnrecognised(t *testing.T) {
	priv, pub := testKeys(t)
	f := signedLicence(t, priv)
	f.Terms.TrainAI = Allow // the exact edit a licence signature exists to catch
	raw, _ := json.Marshal(f)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if status, _ := VerifyStatus(parsed, pub); status != StatusInvalid {
		t.Fatalf("status = %q, want invalid — altered terms with no unknown member is tampering, plainly", status)
	}
	if st, _ := Status(parsed, false); st != StatusInvalid {
		t.Fatalf("Status(false) = %q, want invalid", st)
	}
}

func TestVerifyStatusReportsTheOtherTwoFactsToo(t *testing.T) {
	// The vocabulary is four values, and the two this epic did not add still
	// have to be reachable — otherwise `unknown` quietly absorbs them.
	priv, pub := testKeys(t)

	if st, _ := VerifyStatus(&File{}, pub); st != StatusUnsigned {
		t.Errorf("an unsigned licence = %q, want unsigned — absent means unstated", st)
	}
	if st, _ := VerifyStatus(signedLicence(t, priv), nil); st != StatusUnknown {
		t.Errorf("no key = %q, want unknown — having failed to look is not having found a problem", st)
	}
}
