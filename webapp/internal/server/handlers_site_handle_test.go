package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
)

// The handlers that used to build absolute paths from the data directory now
// address the site by key. These pin what each one did before, so the change
// of address cannot change what a user sees.

func writePublishedComment(t *testing.T, s *Server, id string) string {
	t.Helper()
	rel := filepath.Join("content", "pub.polis.core", "comment", "20260917", id+".md")
	full := filepath.Join(s.DataDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	published := `---
title: "Re: a-post"
type: comment
published: 2026-09-17T10:00:00Z
author: owner.example
in-reply-to:
  url: https://someone.example/posts/20260901/a-post.md
current-version: sha256:abc
signature: SIG
---

My reply.
`
	if err := os.WriteFile(full, []byte(published), 0644); err != nil {
		t.Fatal(err)
	}
	return rel
}

// Unpublishing a comment by its id resolves the id to the comment's key and
// unpublishes it. ⚠️ It answered 500 for every id: the lookup returns a
// site-relative key, and the handler then tried to make that key relative to
// the data directory, which fails for a relative path.
func TestUnpublishByCommentIDFindsTheComment(t *testing.T) {
	s := newConfiguredServer(t)
	s.BaseURL = "https://owner.example"
	rel := writePublishedComment(t, s, "by-id")

	w := httptest.NewRecorder()
	s.handleUnpublish(w, httptest.NewRequest(http.MethodPost, "/api/unpublish",
		jsonBody(t, map[string]string{"comment_id": "by-id"})))
	if w.Code != http.StatusOK {
		t.Fatalf("unpublish by comment_id = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, rel)); !os.IsNotExist(err) {
		t.Errorf("published comment still present after unpublish: %v", err)
	}
	if _, err := comment.LoadDraft(s.Site(), "by-id"); err != nil {
		t.Errorf("no draft after unpublish by id: %v", err)
	}
}

// The draft an unpublish writes lands in a 0700 directory even under a
// permissive umask: drafts are private, and the store creates every parent
// under .polis at 0700, as the MkdirAll it replaced did.
func TestUnpublishCreatesThePrivateDraftsDirectory(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	s := newConfiguredServer(t)
	s.BaseURL = "https://owner.example"
	drafts := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts")
	if err := os.RemoveAll(drafts); err != nil {
		t.Fatal(err)
	}
	rel := writePublishedComment(t, s, "mode-check")

	w := httptest.NewRecorder()
	s.handleUnpublish(w, httptest.NewRequest(http.MethodPost, "/api/unpublish",
		jsonBody(t, map[string]string{"path": rel})))
	if w.Code != http.StatusOK {
		t.Fatalf("unpublish = %d: %s", w.Code, w.Body.String())
	}
	fi, err := os.Stat(drafts)
	if err != nil {
		t.Fatalf("drafts directory not created: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("drafts directory mode = %o, want 700", got)
	}
}

// Generating a hook script creates .polis/webapp/hooks at 0700 under a
// permissive umask, as the MkdirAll it replaced did.
func TestHooksGenerateCreatesThePrivateHooksDirectory(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	s := newConfiguredServer(t)
	s.EnableHooks = true
	hooks := filepath.Join(s.DataDir, ".polis", "webapp", "hooks")
	if err := os.RemoveAll(hooks); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.handleHooksGenerate(w, httptest.NewRequest(http.MethodPost, "/api/hooks/generate",
		jsonBody(t, map[string]string{"hook_type": "post-publish"})))
	if w.Code != http.StatusOK {
		t.Fatalf("hooks generate = %d: %s", w.Code, w.Body.String())
	}
	fi, err := os.Stat(hooks)
	if err != nil {
		t.Fatalf("hooks directory not created: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("hooks directory mode = %o, want 700", got)
	}
	script, err := os.Stat(filepath.Join(hooks, "post-publish.sh"))
	if err != nil {
		t.Fatalf("hook script not written: %v", err)
	}
	if got := script.Mode().Perm(); got != 0o755 {
		t.Errorf("hook script mode = %o, want 755", got)
	}

	// A second generate of the same hook is refused, not overwritten.
	w = httptest.NewRecorder()
	s.handleHooksGenerate(w, httptest.NewRequest(http.MethodPost, "/api/hooks/generate",
		jsonBody(t, map[string]string{"hook_type": "post-publish"})))
	if w.Code != http.StatusConflict {
		t.Errorf("second generate = %d, want 409", w.Code)
	}
}

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
