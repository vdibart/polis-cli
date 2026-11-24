package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The handlers that used to build absolute paths from the data directory now
// address the site by key. These pin what each one did before, so the change
// of address cannot change what a user sees.

// A missing drafts directory is zero drafts and serves "items": [] — never
// null — without the separate absence check that used to guard it.
func TestStreamDraftsWithNoDraftsDirectoryServeAnEmptyList(t *testing.T) {
	s := newConfiguredServer(t)
	if err := os.RemoveAll(filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.handleStreamItemsDrafts(w, "", 10)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if string(got["items"]) != "[]" {
		t.Errorf(`items = %s, want []`, got["items"])
	}
}

// A draft's title in the drafts read is the title publishing would give it
// (publish.ExtractTitle), its excerpt starts after the line used as the
// title, and only an empty draft gets the placeholder.
func TestStreamDraftsTitleIsThePublishedTitle(t *testing.T) {
	s := newConfiguredServer(t)
	dir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		content, title, excerpt string
	}{
		"heading":  {"# A heading\n\nThe body.\n", "A heading", "The body."},
		"plain":    {"Things to write about next\nA paragraph.\n", "Things to write about next", "A paragraph."},
		"level2":   {"\n## Notes\n\nSome notes.\n", "Notes", "Some notes."},
		"empty":    {"\n  \n", "(untitled draft)", ""},
		"headonly": {"# Only a heading\n", "Only a heading", ""},
	}
	for id, c := range cases {
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	w := httptest.NewRecorder()
	s.handleStreamItemsDrafts(w, "", 0)
	var got struct {
		Items []struct {
			ID, Title, Excerpt string
			BodyHTML           string `json:"body_html"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if len(got.Items) != len(cases) {
		t.Fatalf("got %d drafts, want %d", len(got.Items), len(cases))
	}
	for _, it := range got.Items {
		c := cases[it.ID]
		if it.Title != c.title {
			t.Errorf("%s: title = %q, want %q", it.ID, it.Title, c.title)
		}
		if it.Excerpt != c.excerpt {
			t.Errorf("%s: excerpt = %q, want %q", it.ID, it.Excerpt, c.excerpt)
		}
		if c.title != "(untitled draft)" && strings.Contains(it.BodyHTML, c.title) {
			t.Errorf("%s: body repeats the title line: %q", it.ID, it.BodyHTML)
		}
	}
}
