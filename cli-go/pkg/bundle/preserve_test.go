package bundle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// bundle.json and registry.json are UNSIGNED, so SaveBundle and SaveRegistry
// PRESERVE what this build does not model, at every nesting level — and a file
// with nothing unmodelled saves byte-identically to what the pre-preservation
// writers produced (the *.golden.json fixtures were written by them).

func copyFixture(t *testing.T, fixture, dst string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAnUntouchedBundleRoundTripIsByteIdentical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.json")
	golden := copyFixture(t, "bundle.golden.json", path)
	b, err := LoadBundle(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBundle(path, b); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, golden) {
		t.Fatalf("an untouched round-trip changed bundle.json:\n--- before\n%s\n--- after\n%s", golden, after)
	}
}

func TestSaveBundlePreservesUnmodelledMembersAtEveryLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.json")
	before := copyFixture(t, "bundle.future.json", path)
	b, err := LoadBundle(path)
	if err != nil {
		t.Fatal(err)
	}
	b.Description = "mutated"
	if err := SaveBundle(path, b); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	assertPreserved(t, before, after, [][]interface{}{
		{"future_top"},
		{"handler", "future_handler"},
		{"ds", "future_ds"},
		{"types", "pub.polis.post", "future_type"},
		{"types", "pub.polis.post", "storage", "future_storage"},
		{"types", "pub.polis.post", "notifications", 0, "future_rule"},
		{"shapes", "v4", "future_shape"},
		{"themes", "vice", "future_theme"},
	})
}

func TestAnUntouchedRegistryRoundTripIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	golden := copyFixture(t, "registry.golden.json", RegistryPath(dir))
	reg, err := LoadRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRegistry(dir, reg); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(RegistryPath(dir))
	if !bytes.Equal(after, golden) {
		t.Fatalf("an untouched round-trip changed registry.json:\n--- before\n%s\n--- after\n%s", golden, after)
	}
}

func TestSaveRegistryPreservesUnmodelledMembersAtEveryLevel(t *testing.T) {
	dir := t.TempDir()
	before := copyFixture(t, "registry.future.json", RegistryPath(dir))
	// A real setter: load → mutate → save.
	if err := SetActiveThemeName(dir, "vice"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(RegistryPath(dir))
	if !bytes.Contains(after, []byte(`"pub.polis.themes.vice"`)) {
		t.Fatalf("the mutation itself was lost:\n%s", after)
	}
	assertPreserved(t, before, after, [][]interface{}{
		{"future_top"},
		{"installed_bundles", 0, "future_installed"},
		{"notifications", 0, "future_rule"},
	})
}

func assertPreserved(t *testing.T, before, after []byte, paths [][]interface{}) {
	t.Helper()
	for _, p := range paths {
		want, got := valueAt(t, before, p), valueAt(t, after, p)
		if want == nil {
			t.Fatalf("fixture lacks %v", p)
		}
		if got == nil {
			t.Errorf("%v was dropped", p)
		} else if !bytes.Equal(got, want) {
			t.Errorf("%v changed: want %s got %s", p, want, got)
		}
	}
}

// valueAt walks a JSON document by member names and list indices and returns
// the compacted value there, or nil when absent.
func valueAt(t *testing.T, doc []byte, path []interface{}) []byte {
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
