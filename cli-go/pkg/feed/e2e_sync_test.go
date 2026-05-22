// End-to-end test for the DS-to-stream sync path.
//
// Exercises the full pipeline across packages:
//
//   mock DS (httptest) → discovery.Client.StreamQuery
//                      → feed.FeedHandler.Process
//                      → feed.CacheManager.MergeItems
//                      → cache file written + cursor advanced
//
// This is the only test today that wires every step of the continuous
// sync path together with a real cursor advance + real on-disk cache.
// If the contract between any of these packages breaks (DS response
// shape, FeedHandler event-type dispatch, cache file layout), the
// assertions here surface it.

package feed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// fakeDSStream mints an httptest server that responds to /v1/stream
// with a fixed list of events on the first call, then signals exhaustion
// (has_more=false) on subsequent calls. Useful for testing one full sync
// cycle.
func fakeDSStream(t *testing.T, events []map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stream" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		actor := r.URL.Query().Get("actor")
		typeFilter := r.URL.Query().Get("type")

		filtered := []map[string]interface{}{}
		for _, e := range events {
			if actor != "" && e["actor"] != actor {
				continue
			}
			if typeFilter != "" && e["type"] != typeFilter {
				continue
			}
			filtered = append(filtered, e)
		}

		cursor := "0"
		if len(filtered) > 0 {
			if id, ok := filtered[len(filtered)-1]["id"].(string); ok {
				cursor = id
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"events":   filtered,
			"cursor":   cursor,
			"has_more": false,
		})
	}))
}

// TestE2E_DSStreamToFeedCache documents the full DS→feed-cache sync
// contract end-to-end. A new post and a new comment from a followed
// author land on DS as stream events; the sync path consumes them and
// the local cache file ends up with both items.
func TestE2E_DSStreamToFeedCache(t *testing.T) {
	dsEvents := []map[string]interface{}{
		{
			"id":         "1",
			"type":       "pub.polis.post.published",
			"actor":      "alice.example",
			"created_at": "2026-05-21T00:00:00Z",
			"signature":  "sig1",
			"payload": map[string]interface{}{
				"url":          "https://alice.example/posts/hello.md",
				"version":      "sha256:" + strings.Repeat("a", 64),
				"title":        "Hello",
				"published_at": "2026-05-21T00:00:00Z",
			},
		},
		{
			"id":         "2",
			"type":       "pub.polis.comment.published",
			"actor":      "alice.example",
			"created_at": "2026-05-21T00:05:00Z",
			"signature":  "sig2",
			"payload": map[string]interface{}{
				"url":          "https://alice.example/comments/r1.md",
				"in_reply_to":  "https://carol.example/posts/p.md",
				"root_post":    "https://carol.example/posts/p.md",
				"published_at": "2026-05-21T00:05:00Z",
			},
		},
	}

	ds := fakeDSStream(t, dsEvents)
	defer ds.Close()

	// Configure a discovery client. DSKeyCache=nil skips response
	// envelope verification — needed because the mock server doesn't
	// produce DS-signed responses.
	client := discovery.NewClient(ds.URL, "test-key")
	client.DSKeyCache = nil

	// === Pull events from DS via the same query path syncFeed uses. ===
	resp, err := client.StreamQuery("0", 100, "", "", "")
	if err != nil {
		t.Fatalf("StreamQuery: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events from DS, got %d", len(resp.Events))
	}

	// === Run events through FeedHandler. ===
	h := &FeedHandler{
		MyDomain:        "bob.example", // we're bob, alice is in our feed
		FollowedDomains: map[string]bool{"alice.example": true},
	}
	items := h.Process(resp.Events)
	if len(items) != 2 {
		t.Fatalf("expected 2 FeedItems after Process, got %d", len(items))
	}

	// Spot-check item shapes
	var foundPost, foundComment bool
	for _, it := range items {
		if it.Type == "post" {
			foundPost = true
			if it.Title != "Hello" {
				t.Errorf("post item title = %q, want %q", it.Title, "Hello")
			}
			if it.AuthorDomain != "alice.example" {
				t.Errorf("post item author = %q, want alice.example", it.AuthorDomain)
			}
		}
		if it.Type == "comment" {
			foundComment = true
			if it.AuthorDomain != "alice.example" {
				t.Errorf("comment author = %q, want alice.example", it.AuthorDomain)
			}
		}
	}
	if !foundPost || !foundComment {
		t.Errorf("missing items: post=%v comment=%v", foundPost, foundComment)
	}

	// === Persist items through CacheManager. ===
	cm := NewCacheManager(t.TempDir(), "ds.example.com")
	added, err := cm.MergeItems(items)
	if err != nil {
		t.Fatalf("MergeItems: %v", err)
	}
	if added != 2 {
		t.Errorf("expected 2 items added to cache, got %d", added)
	}

	// === Advance cursor (the part syncFeed does post-merge). ===
	if err := cm.SetCursor(resp.Cursor); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	got, _ := cm.GetCursor()
	if got != resp.Cursor {
		t.Errorf("cursor not persisted: got %q, want %q", got, resp.Cursor)
	}

	// === Re-read the cache; both items survive. ===
	all, _ := cm.List()
	if len(all) != 2 {
		t.Errorf("expected 2 cached items after sync, got %d", len(all))
	}

	// === A second sync from the new cursor returns no events (idempotency). ===
	resp2, err := client.StreamQuery(resp.Cursor, 100, "", "", "")
	if err != nil {
		t.Fatalf("second StreamQuery: %v", err)
	}
	// Mock returns all events regardless of cursor — real DS would
	// honor since. The key contract we're asserting: items already in
	// cache are NOT re-added (MergeItems dedupes on ID).
	items2 := h.Process(resp2.Events)
	added2, _ := cm.MergeItems(items2)
	if added2 != 0 {
		t.Errorf("expected 0 new items on second pass (dedupe by ID), got %d", added2)
	}
}

// TestE2E_DSStreamToFeedCache_SelfEventsSkipped verifies the filter:
// events authored by MyDomain are skipped from the feed. Without this
// filter, you'd see your own posts in your feed.
func TestE2E_DSStreamToFeedCache_SelfEventsSkipped(t *testing.T) {
	dsEvents := []map[string]interface{}{
		{
			"id":         "1",
			"type":       "pub.polis.post.published",
			"actor":      "bob.example", // SELF
			"created_at": "2026-05-21T00:00:00Z",
			"signature":  "sig1",
			"payload": map[string]interface{}{
				"url":          "https://bob.example/posts/me.md",
				"title":        "My Own Post",
				"published_at": "2026-05-21T00:00:00Z",
			},
		},
		{
			"id":         "2",
			"type":       "pub.polis.post.published",
			"actor":      "alice.example", // not self
			"created_at": "2026-05-21T00:05:00Z",
			"signature":  "sig2",
			"payload": map[string]interface{}{
				"url":          "https://alice.example/posts/hi.md",
				"title":        "Alice's Post",
				"published_at": "2026-05-21T00:05:00Z",
			},
		},
	}

	ds := fakeDSStream(t, dsEvents)
	defer ds.Close()

	client := discovery.NewClient(ds.URL, "test-key")
	client.DSKeyCache = nil

	resp, _ := client.StreamQuery("0", 100, "", "", "")

	h := &FeedHandler{MyDomain: "bob.example"} // IncludeSelf = false (default)
	items := h.Process(resp.Events)

	if len(items) != 1 {
		t.Fatalf("expected 1 item (self filtered), got %d", len(items))
	}
	if items[0].AuthorDomain != "alice.example" {
		t.Errorf("expected only alice's post, got author=%q", items[0].AuthorDomain)
	}
}

// TestE2E_DSStreamToFeedCache_IncludeSelf verifies the "me" scope path:
// when IncludeSelf is true, self-authored events flow through to the
// feed. This is how the "Posts" tab on your own dashboard works.
func TestE2E_DSStreamToFeedCache_IncludeSelf(t *testing.T) {
	dsEvents := []map[string]interface{}{
		{
			"id":         "1",
			"type":       "pub.polis.post.published",
			"actor":      "bob.example", // self
			"created_at": "2026-05-21T00:00:00Z",
			"signature":  "sig1",
			"payload": map[string]interface{}{
				"url":          "https://bob.example/posts/me.md",
				"title":        "My Own Post",
				"published_at": "2026-05-21T00:00:00Z",
			},
		},
	}

	ds := fakeDSStream(t, dsEvents)
	defer ds.Close()

	client := discovery.NewClient(ds.URL, "test-key")
	client.DSKeyCache = nil

	resp, _ := client.StreamQuery("0", 100, "", "", "")

	h := &FeedHandler{MyDomain: "bob.example", IncludeSelf: true}
	items := h.Process(resp.Events)
	if len(items) != 1 {
		t.Fatalf("expected 1 self item with IncludeSelf=true, got %d", len(items))
	}
}

// TestE2E_DSStreamToFeedCache_UnknownEventTypeIgnored verifies that
// unknown event types are dropped silently. This protects the feed
// from future DS event types that this client doesn't understand yet.
func TestE2E_DSStreamToFeedCache_UnknownEventTypeIgnored(t *testing.T) {
	dsEvents := []map[string]interface{}{
		{
			"id":         "1",
			"type":       "pub.polis.future.eventtype.fromtomorrow",
			"actor":      "alice.example",
			"created_at": "2026-05-21T00:00:00Z",
			"signature":  "sig1",
			"payload":    map[string]interface{}{"url": "https://alice.example/x.md"},
		},
		{
			"id":         "2",
			"type":       "pub.polis.post.published",
			"actor":      "alice.example",
			"created_at": "2026-05-21T00:05:00Z",
			"signature":  "sig2",
			"payload": map[string]interface{}{
				"url":          "https://alice.example/posts/hi.md",
				"title":        "Hi",
				"published_at": "2026-05-21T00:05:00Z",
			},
		},
	}

	ds := fakeDSStream(t, dsEvents)
	defer ds.Close()

	client := discovery.NewClient(ds.URL, "test-key")
	client.DSKeyCache = nil

	resp, _ := client.StreamQuery("0", 100, "", "", "")

	h := &FeedHandler{MyDomain: "bob.example"}
	items := h.Process(resp.Events)

	if len(items) != 1 {
		t.Fatalf("expected 1 item (unknown type ignored), got %d", len(items))
	}
	if items[0].Title != "Hi" {
		t.Errorf("expected the known event to be processed, got title=%q", items[0].Title)
	}
}
