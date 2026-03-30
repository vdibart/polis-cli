package blessing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestBeseech_Success_Pending(t *testing.T) {
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	var receivedReq discovery.ContentRegisterRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/content" {
			t.Errorf("path = %q, want /v1/content", r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&receivedReq)

		resp := discovery.ContentRegisterResponse{
			Success:            true,
			Message:            "blessing requested",
			Type:               "pub.polis.comment",
			URL:                receivedReq.URL,
			Status:             "created",
			RelationshipStatus: "pending",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	req := &BeseechRequest{
		CommentURL:       "https://alice.com/comments/reply.md",
		CommentVersion:   "v1abc",
		InReplyTo:        "https://bob.com/posts/hello.md",
		InReplyToVersion: "v2def",
		RootPost:         "https://bob.com/posts/hello.md",
		Author:           "alice.com",
	}

	result, err := Beseech(req, client, privPEM)
	if err != nil {
		t.Fatalf("Beseech: %v", err)
	}

	if !result.Success {
		t.Error("result.Success = false, want true")
	}
	if result.Status != "pending" {
		t.Errorf("result.Status = %q, want %q", result.Status, "pending")
	}

	// Verify the content register request
	if receivedReq.Type != "pub.polis.comment" {
		t.Errorf("type = %q, want %q", receivedReq.Type, "pub.polis.comment")
	}
	if receivedReq.URL != "https://alice.com/comments/reply.md" {
		t.Errorf("url = %q", receivedReq.URL)
	}
	if receivedReq.Version != "v1abc" {
		t.Errorf("version = %q", receivedReq.Version)
	}
	if receivedReq.Author != "alice.com" {
		t.Errorf("author = %q", receivedReq.Author)
	}
	if receivedReq.Signature == "" {
		t.Error("signature is empty, expected signed content")
	}

	// Verify metadata fields
	inReplyTo, _ := receivedReq.Metadata["in_reply_to"].(string)
	if inReplyTo != "https://bob.com/posts/hello.md" {
		t.Errorf("metadata.in_reply_to = %q", inReplyTo)
	}
	inReplyToVersion, _ := receivedReq.Metadata["in_reply_to_version"].(string)
	if inReplyToVersion != "v2def" {
		t.Errorf("metadata.in_reply_to_version = %q", inReplyToVersion)
	}
	rootPost, _ := receivedReq.Metadata["root_post"].(string)
	if rootPost != "https://bob.com/posts/hello.md" {
		t.Errorf("metadata.root_post = %q", rootPost)
	}
	timestamp, _ := receivedReq.Metadata["timestamp"].(string)
	if timestamp == "" {
		t.Error("metadata.timestamp is empty")
	}
}

func TestBeseech_Success_AutoBlessed(t *testing.T) {
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.ContentRegisterResponse{
			Success:            true,
			Message:            "auto-blessed",
			Status:             "created",
			RelationshipStatus: "granted",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	req := &BeseechRequest{
		CommentURL:     "https://alice.com/comments/reply.md",
		CommentVersion: "v1abc",
		InReplyTo:      "https://bob.com/posts/hello.md",
		Author:         "alice.com",
	}

	result, err := Beseech(req, client, privPEM)
	if err != nil {
		t.Fatalf("Beseech: %v", err)
	}

	if result.Status != "granted" {
		t.Errorf("result.Status = %q, want %q (auto-blessed)", result.Status, "granted")
	}
}

func TestBeseech_DSFailure(t *testing.T) {
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		resp := discovery.ContentRegisterResponse{
			Success: false,
			Error:   "invalid comment URL",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	req := &BeseechRequest{
		CommentURL:     "https://alice.com/comments/reply.md",
		CommentVersion: "v1abc",
		InReplyTo:      "https://bob.com/posts/hello.md",
		Author:         "alice.com",
	}

	_, err = Beseech(req, client, privPEM)
	if err == nil {
		t.Fatal("expected error on DS failure")
	}
}

func TestBeseech_UnknownRelationshipStatus(t *testing.T) {
	privPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.ContentRegisterResponse{
			Success:            true,
			RelationshipStatus: "something_else",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	req := &BeseechRequest{
		CommentURL:     "https://alice.com/comments/reply.md",
		CommentVersion: "v1abc",
		InReplyTo:      "https://bob.com/posts/hello.md",
		Author:         "alice.com",
	}

	result, err := Beseech(req, client, privPEM)
	if err != nil {
		t.Fatalf("Beseech: %v", err)
	}

	// Unknown status should default to "pending"
	if result.Status != "pending" {
		t.Errorf("result.Status = %q, want %q for unknown relationship_status", result.Status, "pending")
	}
}

func TestBeseech_NilPrivateKey(t *testing.T) {
	req := &BeseechRequest{
		CommentURL:     "https://alice.com/comments/reply.md",
		CommentVersion: "v1abc",
		InReplyTo:      "https://bob.com/posts/hello.md",
		Author:         "alice.com",
	}

	// Nil private key should fail at signing
	_, err := Beseech(req, nil, nil)
	if err == nil {
		t.Fatal("expected error with nil private key")
	}
}
