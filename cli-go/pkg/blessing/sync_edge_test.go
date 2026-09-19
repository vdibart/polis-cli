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

// Trust posture: the CLI trusts the DS and does NOT re-verify autobless DS
// attestations (that gate lives in stream.BlessingHandler, which drops
// autobless events without a DSKeyCache). blessing sync intentionally has no
// DSKeyCache, so an auto_blessed granted event with no attestation must still
// be synced — pinning that the CLI stays on the trust-the-DS path.
func TestSyncBlessedComments_TrustsDSAutobless(t *testing.T) {
	siteDir := t.TempDir()
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	emptyBC := &metadata.BlessedComments{Version: "test", Comments: []metadata.PostComments{}}
	if err := metadata.SaveBlessedComments(siteDir, emptyBC); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	autoblessNoAttestation := discovery.StreamEvent{
		ID:        json.Number("1"),
		Type:      "pub.polis.comment.blessing.granted",
		Timestamp: "2026-05-21T00:00:00Z",
		Actor:     "bob.com",
		Payload: map[string]interface{}{
			"comment_url":   "https://a.example/comments/1.md",
			"in_reply_to":   "https://bob.com/posts/p1.md",
			"target_domain": "bob.com",
			"auto_blessed":  true, // no ds_attestation present
		},
	}
	server := newStreamServer(t, "bob.com", []discovery.StreamEvent{autoblessNoAttestation})
	defer server.Close()

	result, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
	if err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}
	if result.Synced != 1 {
		t.Errorf("expected autobless event to be trusted and synced, got synced=%d", result.Synced)
	}
}

// Sync against a non-empty starting index that has *some* overlap with the DS
// response. Existing entries should be left alone; only the new ones get added.
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

	// DS reports 4 grants: 2 already-local, 2 new.
	server := newStreamServer(t, "bob.com", []discovery.StreamEvent{
		grantedEvent("1", "https://a.example/comments/old1.md", "https://bob.com/posts/p1.md", "bob.com", "2026-04-01T00:00:00Z"),
		grantedEvent("2", "https://b.example/comments/old2.md", "https://bob.com/posts/p2.md", "bob.com", "2026-04-01T00:00:00Z"),
		grantedEvent("3", "https://c.example/comments/new3.md", "https://bob.com/posts/p1.md", "bob.com", "2026-05-01T00:00:00Z"),
		grantedEvent("4", "https://d.example/comments/new4.md", "https://bob.com/posts/p3.md", "bob.com", "2026-05-01T00:00:00Z"),
	})
	defer server.Close()

	result, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
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

// Malformed DS event (missing comment/target URL) — Sync should not panic and
// should skip it, continuing with the rest.
func TestSyncBlessedComments_MalformedRecord_Tolerated(t *testing.T) {
	siteDir := t.TempDir()
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := metadata.SaveBlessedComments(siteDir, &metadata.BlessedComments{Version: "test"}); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	malformed := discovery.StreamEvent{
		ID:        json.Number("1"),
		Type:      "pub.polis.comment.blessing.granted",
		Timestamp: "2026-05-21T00:00:00Z",
		Actor:     "bob.com",
		Payload:   map[string]interface{}{"target_domain": "bob.com"}, // no comment/target URL
	}
	server := newStreamServer(t, "bob.com", []discovery.StreamEvent{
		malformed,
		grantedEvent("2", "https://a.example/c.md", "https://bob.com/posts/p2.md", "bob.com", "2026-05-21T00:00:00Z"),
	})
	defer server.Close()

	result, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
	if err != nil {
		t.Fatalf("SyncBlessedComments shouldn't fail on malformed record: %v", err)
	}
	// The malformed event is skipped before it counts; only the valid grant
	// is processed.
	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}
	if result.Synced != 1 {
		t.Errorf("Synced = %d, want 1", result.Synced)
	}
}

// Sync must page the stream to completion: page 1 returns has_more=true with a
// cursor, page 2 finishes. Both pages' grants must land, over exactly 2 calls.
func TestSyncBlessedComments_PaginatesStream(t *testing.T) {
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
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("since") == "" {
			// Page 1
			_ = json.NewEncoder(w).Encode(discovery.StreamQueryResponse{
				Events: []discovery.StreamEvent{
					grantedEvent("1", "https://a.example/c1.md", "https://bob.com/posts/p1.md", "bob.com", "2026-05-01T00:00:00Z"),
				},
				Cursor:  "cursor-2",
				HasMore: true,
			})
			return
		}
		// Page 2 (since=cursor-2)
		_ = json.NewEncoder(w).Encode(discovery.StreamQueryResponse{
			Events: []discovery.StreamEvent{
				grantedEvent("2", "https://b.example/c2.md", "https://bob.com/posts/p2.md", "bob.com", "2026-05-02T00:00:00Z"),
			},
			Cursor:  "cursor-2",
			HasMore: false,
		})
	}))
	defer server.Close()

	result, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
	if err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 DS calls (paginated), got %d", calls)
	}
	if result.Synced != 2 {
		t.Errorf("expected synced=2 across both pages, got %d", result.Synced)
	}
}
