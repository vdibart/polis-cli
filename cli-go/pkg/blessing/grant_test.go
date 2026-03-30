package blessing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestExtractPostPath(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{
			"https://alice.polis.site/posts/20260127/hello-world.md",
			"posts/20260127/hello-world.md",
		},
		{
			"https://alice.polis.site/posts/deep/nested/path.md",
			"posts/deep/nested/path.md",
		},
		{
			"https://alice.polis.site/posts/simple.md",
			"posts/simple.md",
		},
		{
			// No /posts/ in URL - should return as-is
			"https://alice.polis.site/other/path.md",
			"https://alice.polis.site/other/path.md",
		},
		{
			// Edge case: /posts/ at the very start of path
			"https://example.com/posts/hello.md",
			"posts/hello.md",
		},
	}

	for _, tt := range tests {
		got := extractPostPath(tt.url)
		if got != tt.want {
			t.Errorf("extractPostPath(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestGrant_Success(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	// Create the blessed comments directory structure
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Track what the DS received
	var receivedReq discovery.RelationshipUpdateRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/relationships" {
			t.Errorf("path = %q, want /v1/relationships", r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&receivedReq)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	request := &IncomingRequest{
		ID:             "req-1",
		CommentURL:     "https://alice.com/comments/reply.md",
		CommentVersion: "v1abc",
		InReplyTo:      "https://bob.com/posts/20260127/hello.md",
		Author:         "alice.com",
	}

	result, err := Grant(siteDir, request, client, nil, privPEM)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}

	if !result.Success {
		t.Error("result.Success = false, want true")
	}
	if result.CommentURL != "https://alice.com/comments/reply.md" {
		t.Errorf("result.CommentURL = %q", result.CommentURL)
	}
	if result.CommentVersion != "v1abc" {
		t.Errorf("result.CommentVersion = %q", result.CommentVersion)
	}
	if result.PostPath != "posts/20260127/hello.md" {
		t.Errorf("result.PostPath = %q, want %q", result.PostPath, "posts/20260127/hello.md")
	}

	// Verify the DS request was well-formed
	if receivedReq.Type != "pub.polis.comment.blessing" {
		t.Errorf("DS request type = %q, want %q", receivedReq.Type, "pub.polis.comment.blessing")
	}
	if receivedReq.SourceURL != "https://alice.com/comments/reply.md" {
		t.Errorf("DS request source_url = %q", receivedReq.SourceURL)
	}
	if receivedReq.TargetURL != "https://bob.com/posts/20260127/hello.md" {
		t.Errorf("DS request target_url = %q", receivedReq.TargetURL)
	}
	if receivedReq.Action != "grant" {
		t.Errorf("DS request action = %q, want %q", receivedReq.Action, "grant")
	}

	// Verify blessed-comments.json was created with the comment
	bc, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if len(bc.Comments) != 1 {
		t.Fatalf("bc.Comments len = %d, want 1", len(bc.Comments))
	}
	if bc.Comments[0].Post != "posts/20260127/hello.md" {
		t.Errorf("bc.Comments[0].Post = %q", bc.Comments[0].Post)
	}
	if len(bc.Comments[0].Blessed) != 1 {
		t.Fatalf("blessed count = %d, want 1", len(bc.Comments[0].Blessed))
	}
	if bc.Comments[0].Blessed[0].URL != "https://alice.com/comments/reply.md" {
		t.Errorf("blessed URL = %q", bc.Comments[0].Blessed[0].URL)
	}
}

func TestGrant_DSFailure(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	request := &IncomingRequest{
		CommentURL: "https://alice.com/comments/reply.md",
		InReplyTo:  "https://bob.com/posts/hello.md",
	}

	_, err = Grant(siteDir, request, client, nil, privPEM)
	if err == nil {
		t.Fatal("expected error on DS failure")
	}
}

func TestGrantByVersion(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	result, err := GrantByVersion(
		siteDir,
		"v1abc",
		"https://alice.com/comments/reply.md",
		"https://bob.com/posts/20260127/hello.md",
		client, nil, privPEM,
	)
	if err != nil {
		t.Fatalf("GrantByVersion: %v", err)
	}

	if !result.Success {
		t.Error("result.Success = false, want true")
	}
	if result.CommentVersion != "v1abc" {
		t.Errorf("result.CommentVersion = %q, want %q", result.CommentVersion, "v1abc")
	}
	if result.CommentURL != "https://alice.com/comments/reply.md" {
		t.Errorf("result.CommentURL = %q", result.CommentURL)
	}
	if result.PostPath != "posts/20260127/hello.md" {
		t.Errorf("result.PostPath = %q", result.PostPath)
	}
}

func TestGrant_LocalMetadataFailureDoesNotBlockGrant(t *testing.T) {
	// Use a malformed blessed.json to force metadata parse failure.
	// The Grant should still succeed because the DS call succeeded.
	siteDir := t.TempDir()
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	// Make the content directory read-only so metadata.AddBlessedComment fails
	contentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(contentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write a malformed blessed.json to cause parse failure
	blessedPath := filepath.Join(contentDir, metadata.BlessedCommentsFilename)
	if err := os.WriteFile(blessedPath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	request := &IncomingRequest{
		CommentURL: "https://alice.com/comments/reply.md",
		InReplyTo:  "https://bob.com/posts/hello.md",
	}

	// Should succeed despite local metadata failure
	result, err := Grant(siteDir, request, client, nil, privPEM)
	if err != nil {
		t.Fatalf("Grant should succeed even when local metadata fails: %v", err)
	}
	if !result.Success {
		t.Error("result.Success = false")
	}
}
