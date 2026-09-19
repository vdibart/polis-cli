package dm

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ⛔ LoadKeyring tolerates members it does not model, so Save must write them
// back: a newer polis that adds a member to the keyring — possibly key
// material — must not lose it the first time an older build saves.

func seedKeyring(t *testing.T, fixture string) (dir string, data []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	if err := os.WriteFile(keyringPath(dir), data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, data
}

// A keyring written by the pre-preservation Save comes back byte-identical.
func TestAnUntouchedKeyringRoundTripIsByteIdentical(t *testing.T) {
	dir, golden := seedKeyring(t, "keyring.golden.json")
	k, err := LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Save(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(keyringPath(dir))
	if !bytes.Equal(after, golden) {
		t.Fatalf("an untouched round-trip changed keyring.json:\n--- before\n%s\n--- after\n%s", golden, after)
	}
}

// Every unmodelled member, at every nesting level, survives a mutating save
// with its value byte-identical.
func TestKeyringSavePreservesUnmodelledMembersAtEveryLevel(t *testing.T) {
	dir, before := seedKeyring(t, "keyring.future.json")
	k, err := LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	k.Revision++
	if err := k.SaveCAS(dir, k.Revision-1); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(keyringPath(dir))

	for _, path := range [][]interface{}{
		{"future_top"},
		{"epochs", 0, "future_epoch"},
		{"epochs", 1, "future_epoch"},
		{"epochs", 1, "kdf", "future_kdf"},
		{"epochs", 1, "recovery_kdf", "future_recovery_kdf"},
	} {
		want := compactAt(t, before, path)
		got := compactAt(t, after, path)
		if got == nil {
			t.Errorf("%v was dropped by Save:\n%s", path, after)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%v changed: want %s got %s", path, want, got)
		}
	}
	var rev struct{ Revision int }
	_ = json.Unmarshal(after, &rev)
	if rev.Revision != 4 {
		t.Fatalf("the mutation itself was lost: revision %d", rev.Revision)
	}
}

// ⛔ The browser gets only what this build can classify. An unmodelled member
// of a keyring may be key material a newer build keeps server-side.
func TestBrowserViewServesNoUnmodelledMember(t *testing.T) {
	dir, _ := seedKeyring(t, "keyring.future.json")
	k, err := LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(k.BrowserView())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("future_")) {
		t.Fatalf("BrowserView served an unmodelled member: %s", out)
	}
	// …and stripping them for the browser does not strip them from the keyring.
	again, _ := json.Marshal(k)
	if !bytes.Contains(again, []byte("future_recovery_kdf")) {
		t.Fatal("BrowserView mutated the keyring it was called on")
	}
}

// compactAt walks a JSON document by member names and list indices and returns
// the compacted value there, or nil when absent.
func compactAt(t *testing.T, doc []byte, path []interface{}) []byte {
	t.Helper()
	cur := json.RawMessage(doc)
	for _, step := range path {
		switch s := step.(type) {
		case string:
			var m map[string]json.RawMessage
			if json.Unmarshal(cur, &m) != nil {
				return nil
			}
			v, ok := m[s]
			if !ok {
				return nil
			}
			cur = v
		case int:
			var l []json.RawMessage
			if json.Unmarshal(cur, &l) != nil || s >= len(l) {
				return nil
			}
			cur = l[s]
		}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, cur); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
