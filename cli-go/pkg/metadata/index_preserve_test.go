package metadata

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// index.jsonl is UNSIGNED. Its rewrites (a replace in AppendToPublicIndex,
// RemoveIndexEntry) write every line they do not change back AS ITS ORIGINAL
// BYTES — unparseable lines included, which the old struct rewrite deleted —
// and a replaced line keeps the members this build does not model.

func seedIndex(t *testing.T, lines ...string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, BundleContentDir, PublicIndexFilename)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func indexLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

const (
	lineA       = `{"type":"post","path":"p/a.md","title":"A","published":"2026-09-01T00:00:00Z","current_version":"sha256:1","future_entry":{"x":1}}`
	lineGarbage = `this is not json {`
	lineB       = `{"type":"comment","path":"c/b.md","title":"B","published":"2026-09-02T00:00:00Z","current_version":"sha256:2","in_reply_to":{"url":"https://x.example/p.md","future_reply":"r"},"future_entry":2}`
	lineOdd     = `{ "type": "tag",  "path": "t/c.json", "title": "C", "published": "2026-09-03T00:00:00Z", "current_version": "sha256:3", "future_tag": true }`
	lineNull    = `null`
)

func TestAReplaceKeepsUntouchedLinesBytesAndTheReplacedLinesMembers(t *testing.T) {
	dir, path := seedIndex(t, lineA, lineGarbage, lineB, lineOdd, lineNull)
	if err := AppendPostToIndex(dir, "p/a.md", "A renamed", "2026-09-01T00:00:00Z", "sha256:9"); err != nil {
		t.Fatal(err)
	}
	got := indexLines(t, path)
	if len(got) != 5 {
		t.Fatalf("want 5 lines, got %d:\n%s", len(got), strings.Join(got, "\n"))
	}
	for i, want := range []string{lineGarbage, lineB, lineOdd, lineNull} {
		if got[i+1] != want {
			t.Errorf("untouched line %d changed:\n want %s\n got  %s", i+2, want, got[i+1])
		}
	}
	var a map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got[0]), &a); err != nil {
		t.Fatal(err)
	}
	if string(a["title"]) != `"A renamed"` {
		t.Fatalf("the replacement itself was lost: %s", got[0])
	}
	if string(a["future_entry"]) != `{"x":1}` {
		t.Errorf("the replaced line lost an unmodelled member: %s", got[0])
	}

	// A comment re-indexed against the same parent keeps in_reply_to's members.
	if err := AppendCommentToIndex(dir, "c/b.md", "B edited", "2026-09-02T00:00:00Z", "sha256:8", "https://x.example/p.md"); err != nil {
		t.Fatal(err)
	}
	got = indexLines(t, path)
	var b struct {
		Title       string                     `json:"title"`
		FutureEntry json.RawMessage            `json:"future_entry"`
		InReplyTo   map[string]json.RawMessage `json:"in_reply_to"`
	}
	if err := json.Unmarshal([]byte(got[2]), &b); err != nil {
		t.Fatal(err)
	}
	if b.Title != "B edited" || string(b.FutureEntry) != `2` || string(b.InReplyTo["future_reply"]) != `"r"` {
		t.Errorf("the replaced comment lost a member: %s", got[2])
	}
	if got[1] != lineGarbage || got[3] != lineOdd || got[4] != lineNull {
		t.Errorf("an untouched line changed on the second replace:\n%s", strings.Join(got, "\n"))
	}
}

func TestRemoveIndexEntryKeepsEveryOtherLineVerbatim(t *testing.T) {
	dir, path := seedIndex(t, lineA, lineGarbage, lineB, lineOdd, lineNull)
	if err := RemoveIndexEntry(dir, "c/b.md"); err != nil {
		t.Fatal(err)
	}
	got := indexLines(t, path)
	want := []string{lineA, lineGarbage, lineOdd, lineNull}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("remove changed other lines:\n want\n%s\n got\n%s", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

// An index written by the pre-preservation writer, with an entry re-indexed to
// the same values, comes back byte-identical.
func TestAnUnchangedReindexIsByteIdentical(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "index.golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, BundleContentDir, PublicIndexFilename)
	post := "content/pub.polis.core/post/20260901/hello.md"
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(post)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, post), []byte("---\ntitle: Hello <&>\nlicense:\n  v: pub.polis.license.v1\n  profile: pub.polis.license.reserved/1\n  train-ai: n\n  search: y\n  terms: https://a.example/license\n---\n\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, golden, 0644); err != nil {
		t.Fatal(err)
	}
	if err := AppendPostToIndex(dir, post, "Hello <&> \"q\"", "2026-09-01T00:00:00Z", "sha256:aa"); err != nil {
		t.Fatal(err)
	}
	if err := AppendCommentToIndex(dir, "content/pub.polis.core/comment/20260902/c.md", "Re: hello", "2026-09-02T00:00:00Z", "sha256:bb", "https://b.example/p.md"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, golden) {
		t.Fatalf("an unchanged re-index changed index.jsonl:\n--- before\n%s\n--- after\n%s", golden, after)
	}
}
