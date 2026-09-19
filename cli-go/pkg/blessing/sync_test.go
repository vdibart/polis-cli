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

// grantedEvent builds a pub.polis.comment.blessing.granted stream event as the
// DS emits it: actor = post author (the granter), payload carries the comment
// URL, the in_reply_to post URL, and target_domain (the post owner).
func grantedEvent(id, commentURL, inReplyTo, postOwnerDomain, ts string) discovery.StreamEvent {
	return discovery.StreamEvent{
		ID:        json.Number(id),
		Type:      "pub.polis.comment.blessing.granted",
		Timestamp: ts,
		Actor:     postOwnerDomain,
		Payload: map[string]interface{}{
			"comment_url":   commentURL,
			"in_reply_to":   inReplyTo,
			"target_domain": postOwnerDomain,
		},
	}
}

// newStreamServer returns a test server that serves a single page of the given
// events from GET /v1/stream (HasMore=false). It fails the test if the request
// does not carry the expected type/actor filters.
func newStreamServer(t *testing.T, wantActor string, events []discovery.StreamEvent) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("type"); got != "pub.polis.comment.blessing.granted" {
			t.Errorf("type filter = %q, want %q", got, "pub.polis.comment.blessing.granted")
		}
		if got := q.Get("actor"); got != wantActor {
			t.Errorf("actor filter = %q, want %q", got, wantActor)
		}
		resp := discovery.StreamQueryResponse{Events: events, HasMore: false}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// writeEmptyBlessed creates an empty blessed.json under siteDir.
func writeEmptyBlessed(t *testing.T, siteDir string) {
	t.Helper()
	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	empty := &metadata.BlessedComments{Version: "test", Comments: []metadata.PostComments{}}
	if err := metadata.SaveBlessedComments(siteDir, empty); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}
}

func clientFor(server *httptest.Server) *discovery.Client {
	return &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}
}

func TestSyncBlessedComments_AddsNewComments(t *testing.T) {
	siteDir := t.TempDir()
	writeEmptyBlessed(t, siteDir)

	server := newStreamServer(t, "bob.com", []discovery.StreamEvent{
		grantedEvent("1", "https://alice.com/comments/reply1.md", "https://bob.com/posts/20260127/hello.md", "bob.com", "2026-01-15T12:00:00Z"),
		grantedEvent("2", "https://charlie.com/comments/reply2.md", "https://bob.com/posts/20260128/world.md", "bob.com", "2026-01-16T12:00:00Z"),
	})
	defer server.Close()

	result, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
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

	bc, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if len(bc.Comments) != 2 {
		t.Fatalf("bc.Comments len = %d, want 2", len(bc.Comments))
	}
}

// TestSyncBlessedComments_OnlySyncsPostOwnerSide is the regression test for the
// actor-direction bug: the stream returns a mix of grants where THIS tenant is
// the post owner (target_domain == me) and grants where this tenant is instead
// the commenter (target_domain != me, i.e. my own comment blessed on someone
// else's post). Only the post-owner grants belong in my blessed.json. The old
// {actor: me} relationship query synced exactly the wrong (commenter) set.
func TestSyncBlessedComments_OnlySyncsPostOwnerSide(t *testing.T) {
	siteDir := t.TempDir()
	writeEmptyBlessed(t, siteDir)

	events := []discovery.StreamEvent{
		// Mine: someone commented on MY post and I blessed it.
		grantedEvent("1", "https://alice.com/comments/on-my-post.md", "https://bob.com/posts/20260127/hello.md", "bob.com", "2026-01-15T12:00:00Z"),
		// NOT mine: I commented on Alice's post and SHE blessed it. Its actor is
		// alice.com, but assert the defensive target_domain guard drops it even
		// if such an event reached us.
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.comment.blessing.granted",
			Timestamp: "2026-01-16T12:00:00Z",
			Actor:     "alice.com",
			Payload: map[string]interface{}{
				"comment_url":   "https://bob.com/comments/on-alice-post.md",
				"in_reply_to":   "https://alice.com/posts/20260101/foo.md",
				"target_domain": "alice.com",
			},
		},
	}
	server := newStreamServer(t, "bob.com", events)
	defer server.Close()

	result, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
	if err != nil {
		t.Fatalf("SyncBlessedComments: %v", err)
	}

	if result.Synced != 1 {
		t.Errorf("result.Synced = %d, want 1 (only the post-owner grant)", result.Synced)
	}

	bc, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if len(bc.Comments) != 1 {
		t.Fatalf("bc.Comments len = %d, want 1", len(bc.Comments))
	}
	if bc.Comments[0].Post != "posts/20260127/hello.md" {
		t.Errorf("post = %q, want the post I own", bc.Comments[0].Post)
	}
	if len(bc.Comments[0].Blessed) != 1 || bc.Comments[0].Blessed[0].URL != "https://alice.com/comments/on-my-post.md" {
		t.Errorf("synced the wrong comment: %+v", bc.Comments[0].Blessed)
	}
}

func TestSyncBlessedComments_SkipsExisting(t *testing.T) {
	siteDir := t.TempDir()

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

	server := newStreamServer(t, "bob.com", []discovery.StreamEvent{
		grantedEvent("1", "https://alice.com/comments/reply1.md", "https://bob.com/posts/20260127/hello.md", "bob.com", "2026-01-15T12:00:00Z"),   // already local
		grantedEvent("2", "https://charlie.com/comments/reply2.md", "https://bob.com/posts/20260128/world.md", "bob.com", "2026-01-16T12:00:00Z"), // new
	})
	defer server.Close()

	result, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
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
	writeEmptyBlessed(t, siteDir)

	server := newStreamServer(t, "bob.com", []discovery.StreamEvent{})
	defer server.Close()

	result, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
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
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
	}))
	defer server.Close()

	_, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
	if err == nil {
		t.Fatal("expected error on DS failure")
	}
}

func TestSyncBlessedComments_NoBlessedFileYet(t *testing.T) {
	siteDir := t.TempDir()
	// Do NOT create blessed.json - SyncBlessedComments should fail because
	// LoadBlessedComments requires the file to exist (and it is loaded before
	// any DS call, so no server is needed).
	server := newStreamServer(t, "bob.com", []discovery.StreamEvent{
		grantedEvent("1", "https://alice.com/comments/reply.md", "https://bob.com/posts/hello.md", "bob.com", "2026-01-15T12:00:00Z"),
	})
	defer server.Close()

	_, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
	if err == nil {
		t.Fatal("expected error when blessed.json does not exist")
	}
}

func TestSyncBlessedComments_ExtractsPostPathFromTargetURL(t *testing.T) {
	siteDir := t.TempDir()
	writeEmptyBlessed(t, siteDir)

	server := newStreamServer(t, "bob.com", []discovery.StreamEvent{
		grantedEvent("1", "https://alice.com/comments/reply.md", "https://bob.com/posts/20260201/deep-thought.md", "bob.com", "2026-02-01T12:00:00Z"),
	})
	defer server.Close()

	_, err := SyncBlessedComments(siteDir, "bob.com", clientFor(server))
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
