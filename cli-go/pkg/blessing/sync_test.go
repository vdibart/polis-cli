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
)

func TestSyncBlessedComments_AddsNewComments(t *testing.T) {
	siteDir := t.TempDir()

	// Create empty blessed.json
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	emptyBC := &metadata.BlessedComments{
		Version:  "test",
		Comments: []metadata.PostComments{},
	}
	if err := metadata.SaveBlessedComments(siteDir, emptyBC); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") != "granted" {
			t.Errorf("status filter = %q, want %q", r.URL.Query().Get("status"), "granted")
		}
		if r.URL.Query().Get("actor") != "bob.com" {
			t.Errorf("actor filter = %q, want %q", r.URL.Query().Get("actor"), "bob.com")
		}

		resp := discovery.RelationshipQueryResponse{
			Count: 2,
			Records: []discovery.RelationshipRecord{
				{
					ID:        json.Number("1"),
					SourceURL: "https://alice.com/comments/reply1.md",
					TargetURL: "https://bob.com/posts/20260127/hello.md",
					Actor:     "alice.com",
					Status:    "granted",
					UpdatedAt: "2026-01-15T12:00:00Z",
				},
				{
					ID:        json.Number("2"),
					SourceURL: "https://charlie.com/comments/reply2.md",
					TargetURL: "https://bob.com/posts/20260128/world.md",
					Actor:     "charlie.com",
					Status:    "granted",
					UpdatedAt: "2026-01-16T12:00:00Z",
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

	result, err := SyncBlessedComments(siteDir, "bob.com", client)
	if err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("result.Total = %d, want 2", result.Total)
	}
	if result.Synced != 2 {
		t.Errorf("result.Synced = %d, want 2", result.Synced)
	}
	if result.Existing != 0 {
		t.Errorf("result.Existing = %d, want 0", result.Existing)
	}

	// Verify the local file was updated
	bc, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if len(bc.Comments) != 2 {
		t.Fatalf("bc.Comments len = %d, want 2", len(bc.Comments))
	}
}

func TestSyncBlessedComments_SkipsExisting(t *testing.T) {
	siteDir := t.TempDir()

	// Pre-populate with one existing blessed comment
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	existingBC := &metadata.BlessedComments{
		Version: "test",
		Comments: []metadata.PostComments{
			{
				Post: "posts/20260127/hello.md",
				Blessed: []metadata.BlessedComment{
					{URL: "https://alice.com/comments/reply1.md", BlessedAt: "2026-01-15T12:00:00Z"},
				},
			},
		},
	}
	if err := metadata.SaveBlessedComments(siteDir, existingBC); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.RelationshipQueryResponse{
			Count: 2,
			Records: []discovery.RelationshipRecord{
				{
					ID:        json.Number("1"),
					SourceURL: "https://alice.com/comments/reply1.md", // Already exists locally
					TargetURL: "https://bob.com/posts/20260127/hello.md",
					Actor:     "alice.com",
					Status:    "granted",
					UpdatedAt: "2026-01-15T12:00:00Z",
				},
				{
					ID:        json.Number("2"),
					SourceURL: "https://charlie.com/comments/reply2.md", // New
					TargetURL: "https://bob.com/posts/20260128/world.md",
					Actor:     "charlie.com",
					Status:    "granted",
					UpdatedAt: "2026-01-16T12:00:00Z",
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

	result, err := SyncBlessedComments(siteDir, "bob.com", client)
	if err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("result.Total = %d, want 2", result.Total)
	}
	if result.Synced != 1 {
		t.Errorf("result.Synced = %d, want 1 (only new one)", result.Synced)
	}
	if result.Existing != 1 {
		t.Errorf("result.Existing = %d, want 1", result.Existing)
	}
}

func TestSyncBlessedComments_EmptyDSResponse(t *testing.T) {
	siteDir := t.TempDir()

	// Create the blessed comments directory
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	emptyBC := &metadata.BlessedComments{
		Version:  "test",
		Comments: []metadata.PostComments{},
	}
	if err := metadata.SaveBlessedComments(siteDir, emptyBC); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

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

	result, err := SyncBlessedComments(siteDir, "bob.com", client)
	if err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}

	if result.Total != 0 {
		t.Errorf("result.Total = %d, want 0", result.Total)
	}
	if result.Synced != 0 {
		t.Errorf("result.Synced = %d, want 0", result.Synced)
	}
}

func TestSyncBlessedComments_DSFailure(t *testing.T) {
	siteDir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`))
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	_, err := SyncBlessedComments(siteDir, "bob.com", client)
	if err == nil {
		t.Fatal("expected error on DS failure")
	}
}

func TestSyncBlessedComments_NoBlessedFileYet(t *testing.T) {
	siteDir := t.TempDir()
	// Do NOT create blessed.json - SyncBlessedComments should fail
	// because LoadBlessedComments requires the file to exist

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.RelationshipQueryResponse{
			Count: 1,
			Records: []discovery.RelationshipRecord{
				{
					ID:        json.Number("1"),
					SourceURL: "https://alice.com/comments/reply.md",
					TargetURL: "https://bob.com/posts/hello.md",
					Actor:     "alice.com",
					Status:    "granted",
					UpdatedAt: "2026-01-15T12:00:00Z",
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

	_, err := SyncBlessedComments(siteDir, "bob.com", client)
	if err == nil {
		t.Fatal("expected error when blessed.json does not exist")
	}
}

func TestSyncBlessedComments_ExtractsPostPathFromTargetURL(t *testing.T) {
	siteDir := t.TempDir()

	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	emptyBC := &metadata.BlessedComments{
		Version:  "test",
		Comments: []metadata.PostComments{},
	}
	if err := metadata.SaveBlessedComments(siteDir, emptyBC); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.RelationshipQueryResponse{
			Count: 1,
			Records: []discovery.RelationshipRecord{
				{
					ID:        json.Number("1"),
					SourceURL: "https://alice.com/comments/reply.md",
					TargetURL: "https://bob.com/posts/20260201/deep-thought.md",
					Actor:     "alice.com",
					Status:    "granted",
					UpdatedAt: "2026-02-01T12:00:00Z",
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

	_, err := SyncBlessedComments(siteDir, "bob.com", client)
	if err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}

	bc, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}

	if len(bc.Comments) != 1 {
		t.Fatalf("comments len = %d, want 1", len(bc.Comments))
	}
	if bc.Comments[0].Post != "posts/20260201/deep-thought.md" {
		t.Errorf("post path = %q, want %q", bc.Comments[0].Post, "posts/20260201/deep-thought.md")
	}
}

func TestGetBlessedCommentsForDomain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("actor") != "bob.com" {
			t.Errorf("actor = %q, want %q", r.URL.Query().Get("actor"), "bob.com")
		}
		if r.URL.Query().Get("status") != "granted" {
			t.Errorf("status = %q, want %q", r.URL.Query().Get("status"), "granted")
		}

		resp := discovery.RelationshipQueryResponse{
			Count: 2,
			Records: []discovery.RelationshipRecord{
				{
					ID:        json.Number("1"),
					SourceURL: "https://alice.com/comments/reply1.md",
					TargetURL: "https://bob.com/posts/hello.md",
					Status:    "granted",
				},
				{
					ID:        json.Number("2"),
					SourceURL: "https://charlie.com/comments/reply2.md",
					TargetURL: "https://bob.com/posts/world.md",
					Status:    "granted",
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

	records, err := GetBlessedCommentsForDomain("bob.com", client)
	if err != nil {
		t.Fatalf("GetBlessedCommentsForDomain: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].SourceURL != "https://alice.com/comments/reply1.md" {
		t.Errorf("records[0].SourceURL = %q", records[0].SourceURL)
	}
}

func TestGetBlessedCommentsForDomain_DSError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"service unavailable"}`))
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	_, err := GetBlessedCommentsForDomain("bob.com", client)
	if err == nil {
		t.Fatal("expected error on DS failure")
	}
}

func TestGetCommentsByAuthor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "pub.polis.comment" {
			t.Errorf("type = %q, want %q", r.URL.Query().Get("type"), "pub.polis.comment")
		}
		if r.URL.Query().Get("actor") != "alice.com" {
			t.Errorf("actor = %q, want %q", r.URL.Query().Get("actor"), "alice.com")
		}

		resp := discovery.ContentQueryResponse{
			Count: 1,
			Records: []discovery.ContentRecord{
				{
					ID:      json.Number("1"),
					Type:    "pub.polis.comment",
					URL:     "https://alice.com/comments/reply.md",
					Version: "v1abc",
					Actor:   "alice.com",
					Author:  "alice.com",
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

	records, err := GetCommentsByAuthor("alice.com", client)
	if err != nil {
		t.Fatalf("GetCommentsByAuthor: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].URL != "https://alice.com/comments/reply.md" {
		t.Errorf("records[0].URL = %q", records[0].URL)
	}
	if records[0].Version != "v1abc" {
		t.Errorf("records[0].Version = %q", records[0].Version)
	}
}

func TestGetCommentsByAuthor_DSError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	_, err := GetCommentsByAuthor("alice.com", client)
	if err == nil {
		t.Fatal("expected error on DS failure")
	}
}

func TestGetCommentsByAuthor_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.ContentQueryResponse{
			Count:   0,
			Records: []discovery.ContentRecord{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	records, err := GetCommentsByAuthor("alice.com", client)
	if err != nil {
		t.Fatalf("GetCommentsByAuthor: %v", err)
	}

	if len(records) != 0 {
		t.Errorf("got %d records, want 0", len(records))
	}
}
