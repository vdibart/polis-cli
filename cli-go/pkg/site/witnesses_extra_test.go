package site

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// witnesses.json is unsigned (each record carries its own DS signature), so a
// merge PRESERVES what this build does not model, at both levels: the file and
// each witness record.

func seedWitnessSite(t *testing.T, fixture string) (dir, path string, data []byte) {
	t.Helper()
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk := `{"public_key":"k","witnesses":"` + DefaultWitnessesPointer + `"}`
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), []byte(wk), 0644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "content", "witness", "witnesses.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return dir, path, data
}

// A file written by the pre-preservation RecordWitness encodes byte-identically.
func TestAnUntouchedWitnessFileEncodesByteIdentically(t *testing.T) {
	_, _, golden := seedWitnessSite(t, "witnesses.golden.json")
	f, err := WitnessesFromBytes(golden)
	if err != nil {
		t.Fatal(err)
	}
	got, err := encodeWitnessFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, golden) {
		t.Fatalf("an untouched round-trip changed witnesses.json:\n--- before\n%s\n--- after\n%s", golden, got)
	}
}

func TestRecordWitnessPreservesUnmodelledMembersAtEveryLevel(t *testing.T) {
	dir, path, before := seedWitnessSite(t, "witnesses.future.json")
	w := discovery.Witness{Action: discovery.WitnessActionContent, Type: "pub.polis.post", URL: "https://a.example/r.md",
		Version: "sha256:cc", DS: "https://ds.example", DSKeyID: "k1", WitnessedAt: "2026-09-04T00:00:00Z", Signature: "c2ln"}
	changed, err := RecordWitness(dir, w.URL, w)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	after, _ := os.ReadFile(path)
	for _, p := range [][]interface{}{
		{"future_top"},
		{"witnesses", "https://a.example/p.md", 0, "future_witness"},
		{"witnesses", "https://a.example/p.md", 1, "future_witness"},
	} {
		want, got := jsonAt(t, before, p), jsonAt(t, after, p)
		if got == nil {
			t.Errorf("%v was dropped by RecordWitness:\n%s", p, after)
		} else if !bytes.Equal(got, want) {
			t.Errorf("%v changed: want %s got %s", p, want, got)
		}
	}
	if jsonAt(t, after, []interface{}{"witnesses", "https://a.example/r.md", 0, "url"}) == nil {
		t.Fatal("the new witness was not recorded")
	}
}

// jsonAt walks a JSON document by member names and list indices and returns
// the compacted value there, or nil when absent.
func jsonAt(t *testing.T, doc []byte, path []interface{}) []byte {
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
