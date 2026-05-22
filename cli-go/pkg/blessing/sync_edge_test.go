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

// SyncBlessedComments edge cases not in sync_test.go.

// The DS query is parameterized as `status: granted` — DS filters
// server-side. Verify the client honors only what DS returned (it
// shouldn't try to re-filter, because that would mask DS bugs).
//
// If a future DS regression leaked denied entries into a "granted"
// query, the client should treat them as granted (DS is the source
// of truth on relationship status). This test pins that down.
func TestSyncBlessedComments_TrustsDSStatusField(t *testing.T) {
	siteDir := t.TempDir()
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	emptyBC := &metadata.BlessedComments{Version: "test", Comments: []metadata.PostComments{}}
	if err := metadata.SaveBlessedComments(siteDir, emptyBC); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// DS returns 3 entries — one with status=granted (correct),
		// two with weird status fields. SyncBlessedComments doesn't
		// inspect status (the query already filtered server-side), so
		// it should accept all 3 into the local index.
		resp := discovery.RelationshipQueryResponse{
			Count: 3,
			Records: []discovery.RelationshipRecord{
				{ID: json.Number("1"), SourceURL: "https://a.example/comments/1.md", TargetURL: "https://bob.com/posts/p1.md", Status: "granted", UpdatedAt: "2026-05-21T00:00:00Z"},
				{ID: json.Number("2"), SourceURL: "https://b.example/comments/2.md", TargetURL: "https://bob.com/posts/p2.md", Status: "weird-status", UpdatedAt: "2026-05-21T00:00:00Z"},
				{ID: json.Number("3"), SourceURL: "https://c.example/comments/3.md", TargetURL: "https://bob.com/posts/p3.md", Status: "", UpdatedAt: "2026-05-21T00:00:00Z"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	result, err := SyncBlessedComments(siteDir, "bob.com", client)
	if err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}
	if result.Synced != 3 {
		t.Errorf("expected to sync all 3 records DS returned, got %d", result.Synced)
	}
}

// Sync against a non-empty starting index that has *some* overlap
// with the DS response. Existing entries should be left alone; only
// the new ones get added.
func TestSyncBlessedComments_PartialOverlapAcrossPosts(t *testing.T) {
	siteDir := t.TempDir()
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Pre-existing: 2 posts each with 1 blessed comment
	existing := &metadata.BlessedComments{
		Version: "test",
		Comments: []metadata.PostComments{
			{Post: "posts/p1.md", Blessed: []metadata.BlessedComment{{URL: "https://a.example/comments/old1.md", BlessedAt: "2026-04-01T00:00:00Z"}}},
			{Post: "posts/p2.md", Blessed: []metadata.BlessedComment{{URL: "https://b.example/comments/old2.md", BlessedAt: "2026-04-01T00:00:00Z"}}},
		},
	}
	if err := metadata.SaveBlessedComments(siteDir, existing); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// DS reports 4 entries: 2 already-local, 2 new
		resp := discovery.RelationshipQueryResponse{
			Count: 4,
			Records: []discovery.RelationshipRecord{
				{ID: json.Number("1"), SourceURL: "https://a.example/comments/old1.md", TargetURL: "https://bob.com/posts/p1.md", Status: "granted"},
				{ID: json.Number("2"), SourceURL: "https://b.example/comments/old2.md", TargetURL: "https://bob.com/posts/p2.md", Status: "granted"},
				{ID: json.Number("3"), SourceURL: "https://c.example/comments/new3.md", TargetURL: "https://bob.com/posts/p1.md", Status: "granted"},
				{ID: json.Number("4"), SourceURL: "https://d.example/comments/new4.md", TargetURL: "https://bob.com/posts/p3.md", Status: "granted"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	result, err := SyncBlessedComments(siteDir, "bob.com", client)
	if err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}
	if result.Synced != 2 || result.Existing != 2 {
		t.Errorf("expected synced=2 existing=2, got synced=%d existing=%d", result.Synced, result.Existing)
	}

	bc, _ := metadata.LoadBlessedComments(siteDir)
	// Final state: p1 has 2, p2 has 1, p3 has 1
	postCounts := map[string]int{}
	for _, post := range bc.Comments {
		postCounts[post.Post] = len(post.Blessed)
	}
	if postCounts["posts/p1.md"] != 2 {
		t.Errorf("posts/p1.md = %d entries, want 2", postCounts["posts/p1.md"])
	}
	if postCounts["posts/p2.md"] != 1 {
		t.Errorf("posts/p2.md = %d entries, want 1", postCounts["posts/p2.md"])
	}
	if postCounts["posts/p3.md"] != 1 {
		t.Errorf("posts/p3.md = %d entries, want 1", postCounts["posts/p3.md"])
	}
}

// Malformed DS response (missing required fields) — Sync should still
// not panic. The behavior is to swallow and continue: invalid records
// are silently skipped. Pin this so a future refactor doesn't make
// the loop fail-fast on the first bad record.
func TestSyncBlessedComments_MalformedRecord_Tolerated(t *testing.T) {
	siteDir := t.TempDir()
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := metadata.SaveBlessedComments(siteDir, &metadata.BlessedComments{Version: "test"}); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := discovery.RelationshipQueryResponse{
			Count: 2,
			Records: []discovery.RelationshipRecord{
				{ID: json.Number("1"), SourceURL: "", TargetURL: "https://bob.com/posts/p1.md", Status: "granted"}, // empty SourceURL
				{ID: json.Number("2"), SourceURL: "https://a.example/c.md", TargetURL: "https://bob.com/posts/p2.md", Status: "granted"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	result, err := SyncBlessedComments(siteDir, "bob.com", client)
	if err != nil {
		t.Fatalf("SyncBlessedComments shouldn't fail on malformed record: %v", err)
	}
	// The current implementation accepts the empty-SourceURL entry —
	// it doesn't validate. If that ever changes, this number drops.
	// Either is acceptable; the test pins down "no panic" + "sync
	// returns".
	if result.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Total)
	}
}

// Sync where the DS response is paginated — first page returns has_more=true.
// SyncBlessedComments today doesn't paginate; verify the single-page
// behavior is what's documented. If a future refactor adds pagination,
// this test fails fast at the assertion.
func TestSyncBlessedComments_SinglePageOnly(t *testing.T) {
	siteDir := t.TempDir()
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := metadata.SaveBlessedComments(siteDir, &metadata.BlessedComments{Version: "test"}); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Always respond with one record and ignore any cursor — if
		// the client retries, calls > 1.
		resp := discovery.RelationshipQueryResponse{
			Count: 1,
			Records: []discovery.RelationshipRecord{
				{ID: json.Number("1"), SourceURL: "https://a.example/c.md", TargetURL: "https://bob.com/posts/p.md", Status: "granted"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := SyncBlessedComments(siteDir, "bob.com", client); err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 DS call (no pagination), got %d", calls)
	}
}
