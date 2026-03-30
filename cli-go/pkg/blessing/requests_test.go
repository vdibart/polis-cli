package blessing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

func TestDomainMatches(t *testing.T) {
	tests := []struct {
		url    string
		domain string
		want   bool
	}{
		{"https://testpilot.polis.pub/posts/20260101/hello.md", "testpilot.polis.pub", true},
		{"https://follower1.polis.pub/comments/20260101/reply.md", "testpilot.polis.pub", false},
		{"https://follower1.polis.pub/comments/20260101/reply.md", "follower1.polis.pub", true},
		{"https://TESTPILOT.POLIS.PUB/posts/test.md", "testpilot.polis.pub", true}, // case-insensitive
		{"not-a-url", "testpilot.polis.pub", false},
		{"", "testpilot.polis.pub", false},
	}

	for _, tt := range tests {
		got := domainMatches(tt.url, tt.domain)
		if got != tt.want {
			t.Errorf("domainMatches(%q, %q) = %v, want %v", tt.url, tt.domain, got, tt.want)
		}
	}
}

func TestFetchPendingRequests_FiltersByTargetDomain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the query parameters
		if r.URL.Query().Get("type") != "pub.polis.comment.blessing" {
			t.Errorf("type = %q, want %q", r.URL.Query().Get("type"), "pub.polis.comment.blessing")
		}
		if r.URL.Query().Get("status") != "pending" {
			t.Errorf("status = %q, want %q", r.URL.Query().Get("status"), "pending")
		}

		resp := discovery.RelationshipQueryResponse{
			Count: 3,
			Records: []discovery.RelationshipRecord{
				{
					ID:        json.Number("1"),
					Type:      "pub.polis.comment.blessing",
					SourceURL: "https://alice.com/comments/reply1.md",
					TargetURL: "https://bob.com/posts/hello.md",
					Actor:     "alice.com",
					Status:    "pending",
					Metadata:  map[string]interface{}{"comment_version": "v1abc"},
					CreatedAt: "2026-01-15T10:00:00Z",
				},
				{
					ID:        json.Number("2"),
					Type:      "pub.polis.comment.blessing",
					SourceURL: "https://charlie.com/comments/reply2.md",
					TargetURL: "https://bob.com/posts/world.md",
					Actor:     "charlie.com",
					Status:    "pending",
					Metadata:  map[string]interface{}{"comment_version": "v2def"},
					CreatedAt: "2026-01-16T10:00:00Z",
				},
				{
					// This record targets a different domain; should be filtered out
					ID:        json.Number("3"),
					Type:      "pub.polis.comment.blessing",
					SourceURL: "https://alice.com/comments/reply3.md",
					TargetURL: "https://dave.com/posts/other.md",
					Actor:     "alice.com",
					Status:    "pending",
					Metadata:  map[string]interface{}{"comment_version": "v3ghi"},
					CreatedAt: "2026-01-17T10:00:00Z",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	requests, err := FetchPendingRequests(client, "bob.com")
	if err != nil {
		t.Fatalf("FetchPendingRequests: %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(requests))
	}

	// Verify first request
	if requests[0].ID != "1" {
		t.Errorf("requests[0].ID = %q, want %q", requests[0].ID, "1")
	}
	if requests[0].CommentURL != "https://alice.com/comments/reply1.md" {
		t.Errorf("requests[0].CommentURL = %q", requests[0].CommentURL)
	}
	if requests[0].CommentVersion != "v1abc" {
		t.Errorf("requests[0].CommentVersion = %q, want %q", requests[0].CommentVersion, "v1abc")
	}
	if requests[0].InReplyTo != "https://bob.com/posts/hello.md" {
		t.Errorf("requests[0].InReplyTo = %q", requests[0].InReplyTo)
	}
	if requests[0].Author != "alice.com" {
		t.Errorf("requests[0].Author = %q, want %q", requests[0].Author, "alice.com")
	}
	if requests[0].CreatedAt != "2026-01-15T10:00:00Z" {
		t.Errorf("requests[0].CreatedAt = %q", requests[0].CreatedAt)
	}

	// Verify second request
	if requests[1].Author != "charlie.com" {
		t.Errorf("requests[1].Author = %q, want %q", requests[1].Author, "charlie.com")
	}
}

func TestFetchPendingRequests_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.RelationshipQueryResponse{
			Count:   0,
			Records: []discovery.RelationshipRecord{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	requests, err := FetchPendingRequests(client, "bob.com")
	if err != nil {
		t.Fatalf("FetchPendingRequests: %v", err)
	}

	// Should return empty slice, not nil
	if requests == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(requests) != 0 {
		t.Errorf("got %d requests, want 0", len(requests))
	}
}

func TestFetchPendingRequests_AllFilteredOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.RelationshipQueryResponse{
			Count: 1,
			Records: []discovery.RelationshipRecord{
				{
					ID:        json.Number("1"),
					SourceURL: "https://alice.com/comments/reply.md",
					TargetURL: "https://other.com/posts/hello.md",
					Actor:     "alice.com",
					Status:    "pending",
					Metadata:  map[string]interface{}{},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	requests, err := FetchPendingRequests(client, "bob.com")
	if err != nil {
		t.Fatalf("FetchPendingRequests: %v", err)
	}

	if len(requests) != 0 {
		t.Errorf("got %d requests, want 0 (all filtered out)", len(requests))
	}
}

func TestFetchPendingRequests_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`))
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	_, err := FetchPendingRequests(client, "bob.com")
	if err == nil {
		t.Fatal("expected error on server failure")
	}
}

func TestFetchPendingRequests_MissingCommentVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.RelationshipQueryResponse{
			Count: 1,
			Records: []discovery.RelationshipRecord{
				{
					ID:        json.Number("1"),
					SourceURL: "https://alice.com/comments/reply.md",
					TargetURL: "https://bob.com/posts/hello.md",
					Actor:     "alice.com",
					Status:    "pending",
					Metadata:  map[string]interface{}{},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	requests, err := FetchPendingRequests(client, "bob.com")
	if err != nil {
		t.Fatalf("FetchPendingRequests: %v", err)
	}

	if len(requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(requests))
	}
	// comment_version should be empty string when not in metadata
	if requests[0].CommentVersion != "" {
		t.Errorf("CommentVersion = %q, want empty", requests[0].CommentVersion)
	}
}

func TestFetchPendingRequests_CaseInsensitiveDomainMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.RelationshipQueryResponse{
			Count: 1,
			Records: []discovery.RelationshipRecord{
				{
					ID:        json.Number("1"),
					SourceURL: "https://alice.com/comments/reply.md",
					TargetURL: "https://BOB.COM/posts/hello.md",
					Actor:     "alice.com",
					Status:    "pending",
					Metadata:  map[string]interface{}{},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	requests, err := FetchPendingRequests(client, "bob.com")
	if err != nil {
		t.Fatalf("FetchPendingRequests: %v", err)
	}

	if len(requests) != 1 {
		t.Fatalf("got %d requests, want 1 (case-insensitive match)", len(requests))
	}
}
