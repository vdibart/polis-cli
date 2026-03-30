package blessing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestDeny_Success(t *testing.T) {
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

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
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	result, err := Deny(
		"https://alice.com/comments/reply.md",
		"https://bob.com/posts/hello.md",
		client, privPEM,
	)
	if err != nil {
		t.Fatalf("Deny: %v", err)
	}

	if !result.Success {
		t.Error("result.Success = false, want true")
	}

	// Verify the DS request
	if receivedReq.Type != "pub.polis.comment.blessing" {
		t.Errorf("DS type = %q, want %q", receivedReq.Type, "pub.polis.comment.blessing")
	}
	if receivedReq.SourceURL != "https://alice.com/comments/reply.md" {
		t.Errorf("DS source_url = %q", receivedReq.SourceURL)
	}
	if receivedReq.TargetURL != "https://bob.com/posts/hello.md" {
		t.Errorf("DS target_url = %q", receivedReq.TargetURL)
	}
	if receivedReq.Action != "deny" {
		t.Errorf("DS action = %q, want %q", receivedReq.Action, "deny")
	}
}

func TestDeny_DSFailure(t *testing.T) {
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"server error"}`))
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	_, err = Deny(
		"https://alice.com/comments/reply.md",
		"https://bob.com/posts/hello.md",
		client, privPEM,
	)
	if err == nil {
		t.Fatal("expected error on DS failure")
	}
}

func TestDenyRequest_UsesRequestFields(t *testing.T) {
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	var receivedReq discovery.RelationshipUpdateRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedReq)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	request := &IncomingRequest{
		ID:         "req-42",
		CommentURL: "https://charlie.com/comments/deep-reply.md",
		InReplyTo:  "https://bob.com/posts/20260201/big-post.md",
		Author:     "charlie.com",
	}

	result, err := DenyRequest(request, client, privPEM)
	if err != nil {
		t.Fatalf("DenyRequest: %v", err)
	}

	if !result.Success {
		t.Error("result.Success = false")
	}

	// DenyRequest should pass request.CommentURL as sourceURL and request.InReplyTo as targetURL
	if receivedReq.SourceURL != "https://charlie.com/comments/deep-reply.md" {
		t.Errorf("source_url = %q, want comment URL from request", receivedReq.SourceURL)
	}
	if receivedReq.TargetURL != "https://bob.com/posts/20260201/big-post.md" {
		t.Errorf("target_url = %q, want in_reply_to from request", receivedReq.TargetURL)
	}
}
