package publish

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// UpdateIndexEntry re-encodes only the matched line, which keeps the members
// this build does not model; every other line is written back as its original
// bytes, unparseable ones included.
func TestUpdateIndexEntryKeepsOtherLinesBytesAndTheMatchedLinesMembers(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "content", "pub.polis.core", "index.jsonl")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		t.Fatal(err)
	}
	matched := `{"type":"post","path":"p/a.md","title":"A","published":"2026-09-01T00:00:00Z","current_version":"sha256:1","future_entry":{"x":1}}`
	others := []string{
		`not json at all {`,
		"{ \"type\": \"post\", \"path\": \"p/b.md\", \"title\": \"B\" }  ",
		"{\"type\":\"post\",\"path\":\"p/c.md\",\"title\":\"C\"}\r",
	}
	content := matched + "\n" + strings.Join(others, "\n") + "\n"
	if err := os.WriteFile(indexPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := UpdateIndexEntry(dir, "p/a.md", "A renamed", "sha256:2"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(indexPath)
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(got) != 4 {
		t.Fatalf("want 4 lines, got %d:\n%s", len(got), data)
	}
	for i, want := range others {
		if got[i+1] != want {
			t.Errorf("untouched line %d changed:\n want %q\n got  %q", i+2, want, got[i+1])
		}
	}
	var a map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got[0]), &a); err != nil {
		t.Fatal(err)
	}
	if string(a["title"]) != `"A renamed"` || string(a["current_version"]) != `"sha256:2"` {
		t.Fatalf("the update itself was lost: %s", got[0])
	}
	if string(a["future_entry"]) != `{"x":1}` {
		t.Errorf("the matched line lost an unmodelled member: %s", got[0])
	}
}
