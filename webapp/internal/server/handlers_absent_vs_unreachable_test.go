package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A 404 is for content that is genuinely absent. A path that cannot be reached
// at all is a server-side failure and stays a 500, as it was when the check
// asked os.IsNotExist.
func TestUnpublishAnswers404OnlyForAGenuineAbsence(t *testing.T) {
	s := newConfiguredServer(t)
	post := filepath.Join(s.DataDir, "content", "pub.polis.core", "post")
	// A FILE where the date directory belongs: stat fails with "not a directory".
	if err := os.WriteFile(filepath.Join(post, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	status := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/unpublish", jsonBody(t, map[string]string{"path": path}))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.handleUnpublish(rr, req)
		return rr.Code
	}

	if got := status("content/pub.polis.core/post/20260101/missing.md"); got != http.StatusNotFound {
		t.Errorf("missing post: status = %d, want 404", got)
	}
	if got := status("content/pub.polis.core/post/afile/x.md"); got != http.StatusInternalServerError {
		t.Errorf("unreachable post: status = %d, want 500", got)
	}
}
