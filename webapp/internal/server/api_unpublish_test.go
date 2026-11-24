package server

import (
	"os"
	"path/filepath"
	"testing"
)

// ⭐ /v1 can take a post down, which the v5 app needs (it has no other way),
// and only the owner can: an agent's token is refused, whatever its scopes.
func TestTheOwnerCanUnpublishAPostThroughTheAPIAndAnAgentCannot(t *testing.T) {
	s, h := queryRoutes(t)
	owner := mint(t, s)
	code, out := apiCall(t, h, "POST", "/v1/content/post", owner, `{"markdown":"# Going away\n\nSoon gone.\n","filename":"going"}`)
	if code != 200 && code != 201 {
		t.Fatalf("publish = %d %v", code, out)
	}
	path, _ := out["path"].(string)
	if path == "" {
		t.Fatalf("publish answered no path: %v", out)
	}

	agent := mint(t, s, "publish")
	if code, out := apiCall(t, h, "POST", "/v1/content/post/actions/unpublish", agent, `{"path":"`+path+`"}`); code < 400 || code >= 500 {
		t.Errorf("an agent's token unpublished: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, path)); err != nil {
		t.Fatalf("the refused unpublish removed the post: %v", err)
	}

	code, out = apiCall(t, h, "POST", "/v1/content/post/actions/unpublish", owner, `{"path":"`+path+`"}`)
	if code != 200 {
		t.Fatalf("the owner's unpublish = %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, path)); !os.IsNotExist(err) {
		t.Error("the published file is still there")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "going.md")); err != nil {
		t.Errorf("the post's text is not back in drafts: %v", err)
	}
	if code, _ := apiCall(t, h, "POST", "/v1/content/post/actions/unpublish", owner, `{"path":"content/pub.polis.core/comment/20260101/x.md"}`); code < 400 || code >= 500 {
		t.Errorf("post.unpublish took a comment path: %d", code)
	}
}
