package following

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// SIGNET epic 47 — tolerant verifiers, over the follow file.
//
// ⭐ THE FIXTURES ARE FAITHFUL, NOT CONVENIENT. Row 3 is not "a broken
// signature plus a junk field": it is a signature made over a WIDER field set
// than this build declares — exactly what a newer writer's output looks like to
// a verifier that shipped before the field. Reproducing that means signing
// hand-written canonical bytes, because by construction this build cannot
// produce them.

// signedOverWiderBytes returns the file JSON for a document signed over
// `canonical` — bytes that include members this build does not declare.
func signedOverWiderBytes(t *testing.T, canonical string, doc map[string]any, priv []byte) []byte {
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

func TestToleranceRowValidWithNothingUnrecognised(t *testing.T) {
	priv, pub := newTestKeys(t)
	f := sampleFile()
	if err := Sign(f, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, _ := json.Marshal(f)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := parsed.UnrecognisedFields(); len(got) != 0 {
		t.Fatalf("UnrecognisedFields = %q, want none", got)
	}
	status, verr := Verify(parsed, pub)
	if status != StatusValid {
		t.Fatalf("status = %q (%v), want valid", status, verr)
	}
}

func TestToleranceRowValidButTheExtraFieldsAreNotCovered(t *testing.T) {
	// A member OUTSIDE the signing base does not disturb the signature — the
	// base is rebuilt from named fields, never hashed off the raw file. The
	// status stays `valid`, and the verifier owes the reader the fact that the
	// extra member is not covered by it.
	priv, pub := newTestKeys(t)
	f := sampleFile()
	if err := Sign(f, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var doc map[string]any
	raw, _ := json.Marshal(f)
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["curated_by"] = "https://someone.example"
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
	if len(got) != 1 || got[0] != "curated_by" {
		t.Fatalf("UnrecognisedFields = %q, want [curated_by]", got)
	}
	if note := signing.UncoveredNote(got); !strings.Contains(note, "not covered by the signature") {
		t.Errorf("UncoveredNote = %q — the valid row must SAY the field is uncovered", note)
	}
}

func TestToleranceRowUnknownWhenSignedOverAWiderFieldSet(t *testing.T) {
	// ⛔ THE ROW THE EPIC EXISTS FOR. Before this rule, a file a newer writer
	// signed correctly read `invalid` here — and nobody could patch the
	// verifiers already in the field.
	priv, pub := newTestKeys(t)
	canonical := `{"version":"polis-cli-go/newer","following":[{"url":"https://alice.example","added_at":"2026-08-28T00:00:00Z"}],"curated_by":"https://someone.example"}`
	raw := signedOverWiderBytes(t, canonical, map[string]any{
		"version": "polis-cli-go/newer",
		"following": []any{map[string]any{
			"url": "https://alice.example", "added_at": "2026-08-28T00:00:00Z",
		}},
		"curated_by": "https://someone.example",
	}, priv)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	status, verr := Verify(parsed, pub)
	if status == StatusInvalid {
		t.Fatal("a file signed over fields this build does not declare reported INVALID — the exact failure epic 47 removes")
	}
	if status != StatusUnknown {
		t.Fatalf("status = %q (%v), want unknown", status, verr)
	}
	if verr == nil || !strings.Contains(verr.Error(), "does not understand") {
		t.Errorf("explanation = %v, want it to say which fields were not understood", verr)
	}
	if status == StatusValid {
		t.Fatal("unknown must never be valid")
	}
}

func TestToleranceRowInvalidWhenNothingIsUnrecognised(t *testing.T) {
	// ⚠️ The rule must not swallow the alarm it was built beside. A tampered
	// file that carries no unknown member is still, and must stay, `invalid`.
	priv, pub := newTestKeys(t)
	f := sampleFile()
	if err := Sign(f, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	f.Following[0].URL = "https://attacker.example"
	raw, _ := json.Marshal(f)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	status, _ := Verify(parsed, pub)
	if status != StatusInvalid {
		t.Fatalf("status = %q, want invalid — an edited file with no unknown member is tampering, plainly", status)
	}
}

func TestToleranceSeesAMemberInsideAListEntry(t *testing.T) {
	// D1: per nesting level, INCLUDING list entries. A top-level-only rule
	// would miss the shape this epic was actually raised for.
	priv, pub := newTestKeys(t)
	canonical := `{"version":"polis-cli-go/newer","following":[{"url":"https://alice.example","added_at":"2026-08-28T00:00:00Z","reason":"met at IIW"}]}`
	raw := signedOverWiderBytes(t, canonical, map[string]any{
		"version": "polis-cli-go/newer",
		"following": []any{map[string]any{
			"url": "https://alice.example", "added_at": "2026-08-28T00:00:00Z", "reason": "met at IIW",
		}},
	}, priv)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := parsed.UnrecognisedFields()
	if len(got) != 1 || got[0] != "following[0].reason" {
		t.Fatalf("UnrecognisedFields = %q, want [following[0].reason]", got)
	}
	if status, _ := Verify(parsed, pub); status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}

func TestAFileBuiltInMemoryHasNothingUnrecognised(t *testing.T) {
	// There were no foreign bytes to meet, so the answer is nil — not a gap,
	// and not something a caller has to special-case.
	if got := sampleFile().UnrecognisedFields(); got != nil {
		t.Errorf("UnrecognisedFields = %q, want nil", got)
	}
	var nilFile *FollowingFile
	if got := nilFile.UnrecognisedFields(); got != nil {
		t.Errorf("nil receiver returned %q", got)
	}
}
