package metadata

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// SIGNET epic 47 — tolerant verifiers, over the blessing list.
//
// ⭐ THIS IS THE TYPE THAT RAISED THE EPIC. Epic 11 added `agent` and `grant` to
// a blessed entry, INSIDE the signed bytes, to a type that was already being
// signed. Any verifier built before those fields would have reported every
// marked list `invalid` — a correct file, called forged, on machines nobody can
// reach. That one field was sequenced by hand (epic 46 R9.1); the rule below is
// so the next one needs no sequencing.
//
// ⚠️ On THIS tree the marker is declared, so a marked list reads `valid` — the
// fixtures below use a different, genuinely unknown member.

func blessedSignedOverWiderBytes(t *testing.T, canonical string, doc map[string]any, priv []byte) []byte {
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
	bc := sampleBlessed()
	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}
	raw, _ := json.Marshal(bc)

	parsed, err := ParseBlessed(raw)
	if err != nil {
		t.Fatalf("ParseBlessed: %v", err)
	}
	if got := parsed.UnrecognisedFields(); len(got) != 0 {
		t.Fatalf("UnrecognisedFields = %q, want none", got)
	}
	if status, verr := VerifyBlessed(parsed, pub); status != StatusValid {
		t.Fatalf("status = %q (%v), want valid", status, verr)
	}
}

func TestToleranceRowValidButTheExtraFieldsAreNotCovered(t *testing.T) {
	priv, pub := newTestKeys(t)
	bc := sampleBlessed()
	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}
	raw, _ := json.Marshal(bc)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["policy_version"] = "2"
	raw, _ = json.Marshal(doc)

	parsed, err := ParseBlessed(raw)
	if err != nil {
		t.Fatalf("ParseBlessed: %v", err)
	}
	status, verr := VerifyBlessed(parsed, pub)
	if status != StatusValid {
		t.Fatalf("status = %q (%v), want valid", status, verr)
	}
	got := parsed.UnrecognisedFields()
	if len(got) != 1 || got[0] != "policy_version" {
		t.Fatalf("UnrecognisedFields = %q, want [policy_version]", got)
	}
	if note := signing.UncoveredNote(got); !strings.Contains(note, "not covered by the signature") {
		t.Errorf("UncoveredNote = %q — the valid row must SAY the field is uncovered", note)
	}
}

func TestToleranceRowUnknownWhenSignedOverAWiderFieldSet(t *testing.T) {
	priv, pub := newTestKeys(t)
	canonical := `{"version":"polis-cli-go/newer","comments":[{"post":"https://a.example/p","blessed":[]}],"policy_version":"2"}`
	raw := blessedSignedOverWiderBytes(t, canonical, map[string]any{
		"version":        "polis-cli-go/newer",
		"comments":       []any{map[string]any{"post": "https://a.example/p", "blessed": []any{}}},
		"policy_version": "2",
	}, priv)

	parsed, err := ParseBlessed(raw)
	if err != nil {
		t.Fatalf("ParseBlessed: %v", err)
	}
	status, verr := VerifyBlessed(parsed, pub)
	if status == StatusInvalid {
		t.Fatal("a list signed over fields this build does not declare reported INVALID — the exact failure epic 47 removes")
	}
	if status != StatusUnknown {
		t.Fatalf("status = %q (%v), want unknown", status, verr)
	}
	if verr == nil || !strings.Contains(verr.Error(), "does not understand") {
		t.Errorf("explanation = %v, want it to name the fields", verr)
	}
}

func TestToleranceRowInvalidWhenNothingIsUnrecognised(t *testing.T) {
	priv, pub := newTestKeys(t)
	bc := sampleBlessed()
	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}
	bc.Comments[0].Blessed[0].URL = "https://attacker.example/c.md"
	raw, _ := json.Marshal(bc)

	parsed, err := ParseBlessed(raw)
	if err != nil {
		t.Fatalf("ParseBlessed: %v", err)
	}
	if status, _ := VerifyBlessed(parsed, pub); status != StatusInvalid {
		t.Fatalf("status = %q, want invalid — an edited list with no unknown member is tampering, plainly", status)
	}
}

func TestToleranceSeesAMemberTwoLevelsDownInsideABlessedEntry(t *testing.T) {
	// ⭐ D1, at the exact depth epic 11's marker sits: a per-post entry's
	// `blessed` list. A top-level-only rule would have reported this `invalid`.
	priv, pub := newTestKeys(t)
	canonical := `{"version":"polis-cli-go/newer","comments":[{"post":"https://a.example/p","blessed":[{"url":"https://b.example/c.md","version":"sha256:aaa","blessed_at":"2026-08-30T00:00:00Z","reason":"on topic"}]}]}`
	raw := blessedSignedOverWiderBytes(t, canonical, map[string]any{
		"version": "polis-cli-go/newer",
		"comments": []any{map[string]any{
			"post": "https://a.example/p",
			"blessed": []any{map[string]any{
				"url": "https://b.example/c.md", "version": "sha256:aaa",
				"blessed_at": "2026-08-30T00:00:00Z", "reason": "on topic",
			}},
		}},
	}, priv)

	parsed, err := ParseBlessed(raw)
	if err != nil {
		t.Fatalf("ParseBlessed: %v", err)
	}
	got := parsed.UnrecognisedFields()
	if len(got) != 1 || got[0] != "comments[0].blessed[0].reason" {
		t.Fatalf("UnrecognisedFields = %q, want [comments[0].blessed[0].reason]", got)
	}
	if status, _ := VerifyBlessed(parsed, pub); status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}

func TestTheAgentMarkerIsRecognisedOnThisBuildAndReadsValid(t *testing.T) {
	// ⛔ THE STALE-FIXTURE TRAP THE PLAN NAMES. Epic 11 merged to main before
	// this epic started, so `agent` and `grant` ARE declared here. A fixture
	// asserting `unknown` for the marker would be pinning the pre-merge world
	// and would go green for the wrong reason.
	priv, pub := newTestKeys(t)
	bc := sampleBlessed()
	bc.Comments[0].Blessed[0].Agent = "rosie"
	bc.Comments[0].Blessed[0].Grant = "https://a.example/content/pub.polis.core/attestation/g.json"
	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}
	raw, _ := json.Marshal(bc)

	parsed, err := ParseBlessed(raw)
	if err != nil {
		t.Fatalf("ParseBlessed: %v", err)
	}
	if got := parsed.UnrecognisedFields(); len(got) != 0 {
		t.Fatalf("the agent marker read as unrecognised (%q) — it is declared on this build", got)
	}
	if status, verr := VerifyBlessed(parsed, pub); status != StatusValid {
		t.Fatalf("status = %q (%v), want valid", status, verr)
	}
}
