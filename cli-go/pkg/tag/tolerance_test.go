package tag

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// SIGNET epic 47 — tolerant verifiers, over a tag file.

func tagSignedOverWiderBytes(t *testing.T, canonical string, doc map[string]any, priv []byte) []byte {
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
	_, priv, pub := tagTestSite(t)
	tf := signedTagFile(t, priv, "reading")
	raw, _ := json.Marshal(tf)

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
	_, priv, pub := tagTestSite(t)
	tf := signedTagFile(t, priv, "reading")
	raw, _ := json.Marshal(tf)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["colour"] = "amber"
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
	if len(got) != 1 || got[0] != "colour" {
		t.Fatalf("UnrecognisedFields = %q, want [colour]", got)
	}
	if note := signing.UncoveredNote(got); !strings.Contains(note, "not covered by the signature") {
		t.Errorf("UncoveredNote = %q — the valid row must SAY the field is uncovered", note)
	}
}

func TestToleranceRowUnknownWhenSignedOverAWiderFieldSet(t *testing.T) {
	_, priv, pub := tagTestSite(t)
	canonical := `{"tag":"reading","targets":[{"uri":"https://alice.example/posts/x.md","added":"2026-01-01T00:00:00Z"}],"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z","generator":"newer","colour":"amber"}`
	raw := tagSignedOverWiderBytes(t, canonical, map[string]any{
		"tag": "reading",
		"targets": []any{map[string]any{
			"uri": "https://alice.example/posts/x.md", "added": "2026-01-01T00:00:00Z",
		}},
		"created": "2026-01-01T00:00:00Z", "updated": "2026-01-01T00:00:00Z",
		"generator": "newer", "colour": "amber",
	}, priv)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	status, verr := Verify(parsed, pub)
	if status == StatusInvalid {
		t.Fatal("a tag file signed over fields this build does not declare reported INVALID")
	}
	if status != StatusUnknown {
		t.Fatalf("status = %q (%v), want unknown", status, verr)
	}
	if verr == nil || !strings.Contains(verr.Error(), "does not understand") {
		t.Errorf("explanation = %v, want it to name the fields", verr)
	}

	// ⭐ And the same answer through the history-aware path, which applies the
	// rule AFTER the chain walk: a nil chain is current-key-only, so this is
	// the same question asked of the other entry point.
	st, _, herr := VerifyWithHistory(parsed, pub, nil)
	if st != StatusUnknown {
		t.Fatalf("VerifyWithHistory status = %q (%v), want unknown — both entry points apply the rule", st, herr)
	}
}

func TestToleranceRowInvalidWhenNothingIsUnrecognised(t *testing.T) {
	_, priv, pub := tagTestSite(t)
	tf := signedTagFile(t, priv, "reading")
	tf.Targets[0].URI = "https://attacker.example/x.md"
	raw, _ := json.Marshal(tf)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if status, _ := Verify(parsed, pub); status != StatusInvalid {
		t.Fatalf("status = %q, want invalid", status)
	}
	if st, _, _ := VerifyWithHistory(parsed, pub, nil); st != StatusInvalid {
		t.Fatalf("VerifyWithHistory status = %q, want invalid", st)
	}
}

func TestToleranceSeesAMemberInsideATarget(t *testing.T) {
	_, priv, pub := tagTestSite(t)
	canonical := `{"tag":"reading","targets":[{"uri":"https://alice.example/posts/x.md","added":"2026-01-01T00:00:00Z","note":"re-read"}],"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z","generator":"newer"}`
	raw := tagSignedOverWiderBytes(t, canonical, map[string]any{
		"tag": "reading",
		"targets": []any{map[string]any{
			"uri": "https://alice.example/posts/x.md", "added": "2026-01-01T00:00:00Z", "note": "re-read",
		}},
		"created": "2026-01-01T00:00:00Z", "updated": "2026-01-01T00:00:00Z", "generator": "newer",
	}, priv)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := parsed.UnrecognisedFields()
	if len(got) != 1 || got[0] != "targets[0].note" {
		t.Fatalf("UnrecognisedFields = %q, want [targets[0].note]", got)
	}
	if status, _ := Verify(parsed, pub); status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}
