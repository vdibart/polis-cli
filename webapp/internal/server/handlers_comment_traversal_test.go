package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// neighbourDraft plants a comment draft in a SECOND site beside the server's own,
// the way two tenants sit side by side on the hosted volume, and returns its path
// together with an id that reaches it from the server's drafts directory.
func neighbourDraft(t *testing.T, s *Server) (path, traversingID string) {
	t.Helper()
	bob := filepath.Join(filepath.Dir(s.DataDir), "bob")
	dir := filepath.Join(bob, ".polis", "bundles", "pub.polis.core", "comments", "drafts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "secret.md")
	body := "---\nin_reply_to: https://x.example/posts/20260101/p.md\n---\nbob's private words\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// <site>/.polis/bundles/pub.polis.core/comments/drafts is six levels below
	// the directory the two sites share.
	traversingID = strings.Repeat("../", 6) + "bob/.polis/bundles/pub.polis.core/comments/drafts/secret"
	return path, traversingID
}

// filesUnder lists every regular file beneath dir, relative to it.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// ⛔ /api/comments/sign takes a draft_id from the request body and loads that
// draft. With a traversing id it loaded ANOTHER SITE'S draft and published it as
// the caller's own signed comment — a cross-tenant read on the hosted volume,
// where every tenant is served this same handler.
func TestSignRefusesADraftIDThatReachesAnotherSite(t *testing.T) {
	s := newConfiguredServer(t)
	bobDraft, id := neighbourDraft(t, s)

	req := httptest.NewRequest(http.MethodPost, "/api/comments/sign", jsonBody(t, map[string]string{"draft_id": id}))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleCommentSign(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "private words") {
		t.Error("the response carries the other site's draft")
	}
	if _, err := os.Stat(bobDraft); err != nil {
		t.Errorf("the other site's draft was disturbed: %v", err)
	}
	for _, f := range filesUnder(t, s.DataDir) {
		if strings.Contains(f, filepath.Join("comments", "pending")) || strings.Contains(f, filepath.Join("pub.polis.core", "comment")+string(filepath.Separator)) {
			t.Errorf("a comment was produced from the other site's draft: %s", f)
		}
	}
}

// The save path: a traversing id must leave nothing outside the site, and what
// is saved is addressed by a base name only.
func TestSavingADraftWithATraversingIDStaysInsideTheSite(t *testing.T) {
	s := newTestServer(t)
	shared := filepath.Dir(s.DataDir)
	before := filesUnder(t, shared)

	req := httptest.NewRequest(http.MethodPost, "/api/comments/drafts", jsonBody(t, map[string]string{
		"id":          strings.Repeat("../", 6) + "bob/pwned",
		"in_reply_to": "https://example.com/posts/test.md",
		"content":     "x",
	}))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleCommentDrafts(rr, req)

	// The handler reduces the id to its base name, so the save succeeds under
	// that name. Without that step the package refuses the id and this is a 500.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if id, _ := resp["id"].(string); id == "" || strings.ContainsAny(id, `/\`) {
		t.Errorf("saved id = %q, want a bare name", resp["id"])
	}

	site := filepath.Base(s.DataDir) + string(filepath.Separator)
	for _, f := range filesUnder(t, shared) {
		if strings.HasPrefix(f, site) {
			continue
		}
		known := false
		for _, b := range before {
			if b == f {
				known = true
			}
		}
		if !known {
			t.Errorf("the save wrote outside the site: %s", f)
		}
	}
}

// Deleting a draft by a traversing id must not remove another site's file. The
// id arrives in the URL path here, so it is the sanitiser that meets it first.
func TestDeletingADraftCannotReachAnotherSite(t *testing.T) {
	s := newTestServer(t)
	bobDraft, id := neighbourDraft(t, s)

	req := httptest.NewRequest(http.MethodDelete, "/api/comments/drafts/"+id, nil)
	rr := httptest.NewRecorder()
	s.handleCommentDraft(rr, req)

	if _, err := os.Stat(bobDraft); err != nil {
		t.Errorf("the other site's draft was deleted: %v", err)
	}
}
