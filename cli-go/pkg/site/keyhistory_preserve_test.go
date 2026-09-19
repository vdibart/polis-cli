package site

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ⛔ A CHAIN IS APPEND-ONLY AND IS NEVER REBUILT — so a rotation must not drop
// what this build does not model, at the block level, at the entry level, or
// inside a witness. transition_sig covers only the five-field rotation
// message, so this is preservation, not signing: carrying a member through
// changes nothing any signature covers.
func TestARotationKeepsUnmodelledMembersAtEveryLevelOfTheChain(t *testing.T) {
	dir := t.TempDir()
	wkPath := filepath.Join(dir, ".well-known", "polis")
	if err := os.MkdirAll(filepath.Dir(wkPath), 0755); err != nil {
		t.Fatal(err)
	}
	before := loadTestdata(t, "future_keyhistory_wellknown.json")
	if err := os.WriteFile(wkPath, before, 0644); err != nil {
		t.Fatal(err)
	}
	rotateOnce(t, dir)
	after, _ := os.ReadFile(wkPath)

	var b, a struct {
		H struct {
			FutureBlock json.RawMessage              `json:"future_block"`
			Current     map[string]json.RawMessage   `json:"current"`
			History     []map[string]json.RawMessage `json:"history"`
		} `json:"public_key_history"`
	}
	if err := json.Unmarshal(before, &b); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &a); err != nil {
		t.Fatal(err)
	}
	if len(a.H.History) != len(b.H.History)+1 {
		t.Fatalf("history went from %d to %d entries, want +1", len(b.H.History), len(a.H.History))
	}
	retired := a.H.History[len(a.H.History)-1]
	var witnessesBefore, witnessesAfter []map[string]json.RawMessage
	_ = json.Unmarshal(b.H.History[1]["witnesses"], &witnessesBefore)
	_ = json.Unmarshal(a.H.History[1]["witnesses"], &witnessesAfter)

	for _, c := range []struct {
		name      string
		want, got json.RawMessage
	}{
		{"block-level future_block", b.H.FutureBlock, a.H.FutureBlock},
		{"entry-level future_current (now retired)", b.H.Current["future_current"], retired["future_current"]},
		{"entry-level future_entry (history[0])", b.H.History[0]["future_entry"], a.H.History[0]["future_entry"]},
		{"witness-level future_witness (history[1])", member(witnessesBefore, "future_witness"), member(witnessesAfter, "future_witness")},
	} {
		if c.want == nil {
			t.Fatalf("fixture lacks %s", c.name)
		}
		if c.got == nil {
			t.Errorf("%s was dropped by the rotation", c.name)
		} else if cj(t, c.got) != cj(t, c.want) {
			t.Errorf("%s changed: want %s got %s", c.name, c.want, c.got)
		}
	}
	// And the rotation itself happened, with the frozen shape intact.
	if string(retired["valid_until"]) != `"`+goldenValidFrom+`"` || string(a.H.Current["valid_from"]) != `"`+goldenValidFrom+`"` {
		t.Fatalf("the rotation itself was lost:\n%s", after)
	}
	if string(a.H.History[0]["transition_sig"]) != "null" {
		t.Fatalf("genesis transition_sig must stay an explicit null: %s", a.H.History[0]["transition_sig"])
	}
}

func member(list []map[string]json.RawMessage, name string) json.RawMessage {
	if len(list) == 0 {
		return nil
	}
	return list[0][name]
}

func cj(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
