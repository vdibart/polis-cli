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

func apiCall(t *testing.T, h http.Handler, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// ⭐ post.update signs a new version of one of the owner's posts at the same
// path, under the publish scope, and refuses anything that isn't a post.
func TestPostUpdateRepublishesAtTheSamePath(t *testing.T) {
	s, h := queryRoutes(t)
	publish := mint(t, s, "publish")
	code, made := apiCall(t, h, "POST", "/v1/content/post", publish, `{"markdown":"# First\n\nversion one"}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, made)
	}
	path, _ := made["path"].(string)
	if !strings.HasPrefix(path, "content/pub.polis.core/post/") {
		t.Fatalf("created path %q", path)
	}

	body, _ := json.Marshal(map[string]string{"path": path, "markdown": "# First\n\nversion two"})
	code, again := apiCall(t, h, "PUT", "/v1/content/post/x", publish, string(body))
	if code != http.StatusOK {
		t.Fatalf("update = %d %v", code, again)
	}
	if again["path"] != path || again["version"] == made["version"] {
		t.Errorf("update gave path %v version %v; want the same path and a new version than %v", again["path"], again["version"], made["version"])
	}
	src, err := os.ReadFile(filepath.Join(s.DataDir, path))
	if err != nil || !strings.Contains(string(src), "version two") || !strings.Contains(string(src), "signature:") {
		t.Errorf("source after update: %v\n%s", err, src)
	}

	read := mint(t, s, "read")
	if code, _ := apiCall(t, h, "PUT", "/v1/content/post/x", read, string(body)); code != http.StatusForbidden {
		t.Errorf("read scope updated a post: %d", code)
	}
	for _, bad := range []string{
		"content/pub.polis.core/comment/20260101/x.md",
		"content/pub.polis.core/post/../../../.polis/keys/id_ed25519",
		"index.md",
		"",
	} {
		b, _ := json.Marshal(map[string]string{"path": bad, "markdown": "pwned"})
		if code, _ := apiCall(t, h, "PUT", "/v1/content/post/x", publish, string(b)); code == http.StatusOK {
			t.Errorf("update with path %q succeeded", bad)
		}
	}
}

// ⭐ The blessing decisions reach the same functions the browser app's
// /api/blessing routes call, under the bless scope.
func TestBlessingDecisionsGoThroughTheSharedFunctions(t *testing.T) {
	s, h := queryRoutes(t)
	bless := mint(t, s, "bless")
	publish := mint(t, s, "publish")
	comment := `{"comment_url":"https://alice.example/comments/20261113/hi.md","in_reply_to":"https://test.example/posts/20261113/p.md"}`

	// No discovery service in a test site: the shared function's own answer.
	for _, action := range []string{"bless", "deny"} {
		code, out := apiCall(t, h, "POST", "/v1/content/comment/actions/"+action, bless, comment)
		if code != http.StatusBadRequest || !strings.Contains(asString(out), "Discovery service not configured") {
			t.Errorf("%s without a discovery service = %d %v", action, code, out)
		}
	}
	// Unbless needs none, and succeeds for a comment that wasn't blessed.
	if code, out := apiCall(t, h, "POST", "/v1/content/comment/actions/unbless", bless, comment); code != http.StatusOK || out["success"] != true {
		t.Errorf("unbless = %d %v", code, out)
	}
	for _, action := range []string{"bless", "deny", "unbless"} {
		if code, _ := apiCall(t, h, "POST", "/v1/content/comment/actions/"+action, publish, comment); code != http.StatusForbidden {
			t.Errorf("publish scope may %s: %d", action, code)
		}
	}
	desc := fmtDescription(t, h)
	for _, id := range []string{`"comment.bless"`, `"comment.deny"`, `"comment.unbless"`, `"post.update"`} {
		if !strings.Contains(desc, id) {
			t.Errorf("description lacks %s", id)
		}
	}
}

func asString(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

// ⭐ comment.create writes a comment from text in one step: it is signed on
// the owner's site and sent; when it can't be sent (no discovery service in a
// test site), it says so and keeps the signed comment to send later.
func TestCommentCreateWritesAndSendsInOneStep(t *testing.T) {
	s, h := queryRoutes(t)
	commentKey := mint(t, s, "comment")
	body := `{"in_reply_to":"https://alice.example/posts/20261113/hello.md","markdown":"Nice post."}`
	code, out := apiCall(t, h, "POST", "/v1/content/comment", commentKey, body)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, out)
	}
	meta, _ := out["comment"].(map[string]any)
	if meta == nil || meta["id"] == "" || meta["in_reply_to"] != "https://alice.example/posts/20261113/hello.md" {
		t.Fatalf("comment = %v", out["comment"])
	}
	if out["sent"] != false || out["send_error"] == nil {
		t.Errorf("with no discovery service: sent=%v send_error=%v, want false and a reason", out["sent"], out["send_error"])
	}

	publish := mint(t, s, "publish")
	if code, _ := apiCall(t, h, "POST", "/v1/content/comment", publish, body); code != http.StatusForbidden {
		t.Errorf("publish scope wrote a comment: %d", code)
	}
	for _, bad := range []string{
		`{"markdown":"no target"}`,
		`{"in_reply_to":"https://alice.example/posts/x.md","markdown":"   "}`,
	} {
		if code, _ := apiCall(t, h, "POST", "/v1/content/comment", commentKey, bad); code == http.StatusCreated {
			t.Errorf("%s was accepted", bad)
		}
	}
}

// ⭐ follow.create and follow.delete reach the same functions the browser
// app's /api/following calls, under the follow scope. Here it is the owner's
// own full token; an agent's follow is agent_follow_test.go's.
func TestFollowGoesThroughTheSharedFunctions(t *testing.T) {
	s, h := queryRoutes(t)
	follow := mint(t, s)
	read := mint(t, s, "read")

	// The shared function's own refusals, reached through /v1.
	if code, out := apiCall(t, h, "POST", "/v1/content/follow", follow, `{"url":"http://alice.example"}`); code != http.StatusBadRequest || !strings.Contains(asString(out), "HTTPS") {
		t.Errorf("follow over http = %d %v", code, out)
	}
	self := `{"url":"` + s.GetBaseURL() + `"}`
	if code, out := apiCall(t, h, "POST", "/v1/content/follow", follow, self); code != http.StatusBadRequest || !strings.Contains(asString(out), "own site") {
		t.Errorf("follow yourself = %d %v", code, out)
	}
	if code, out := apiCall(t, h, "POST", "/v1/content/follow/actions/delete", follow, `{"url":"http://alice.example"}`); code != http.StatusBadRequest || !strings.Contains(asString(out), "HTTPS") {
		t.Errorf("unfollow over http = %d %v", code, out)
	}
	for _, path := range []string{"/v1/content/follow", "/v1/content/follow/actions/delete"} {
		if code, _ := apiCall(t, h, "POST", path, read, `{"url":"https://alice.example"}`); code != http.StatusForbidden {
			t.Errorf("read scope on %s = %d, want 403", path, code)
		}
	}
	// Listing follows is still the engine's, under read.
	if code, _ := apiCall(t, h, "GET", "/v1/content/follow", read, ""); code != http.StatusOK {
		t.Errorf("follow.list with read = %d", code)
	}
	desc := fmtDescription(t, h)
	for _, id := range []string{`"follow.create"`, `"follow.delete"`, `"follow.list"`} {
		if !strings.Contains(desc, id) {
			t.Errorf("description lacks %s", id)
		}
	}
}
