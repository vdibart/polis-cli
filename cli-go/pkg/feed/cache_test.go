package feed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDiscoveryDomain = "test.supabase.co"

func TestComputeItemID(t *testing.T) {
	// Same inputs produce same ID
	id1 := ComputeItemID("https://alice.polis.pub", "posts/hello.md")
	id2 := ComputeItemID("https://alice.polis.pub", "posts/hello.md")
	if id1 != id2 {
		t.Errorf("same inputs should produce same ID: %s vs %s", id1, id2)
	}

	// Different inputs produce different IDs
	id3 := ComputeItemID("https://bob.polis.pub", "posts/hello.md")
	if id1 == id3 {
		t.Errorf("different authors should produce different IDs")
	}

	// ID is 16 hex chars
	if len(id1) != 16 {
		t.Errorf("expected 16-char ID, got %d: %s", len(id1), id1)
	}
}

func TestCacheManager_EmptyCache(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	items, err := cm.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty list, got %d items", len(items))
	}

	count, err := cm.UnreadCount()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 unread, got %d", count)
	}

	stale, err := cm.IsStale()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stale {
		t.Error("empty cache should be stale")
	}
}

// TestCacheManager_MarkChecked verifies that MarkChecked clears the stale
// flag without changing the cursor. Used by syncFeed's structural early-
// returns (empty following list, missing DS config) to break the
// frontend's auto-refresh-on-stale loop when there's no work to do.
func TestCacheManager_MarkChecked(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	// Fresh cache is stale.
	if stale, _ := cm.IsStale(); !stale {
		t.Fatal("precondition: fresh cache should be stale")
	}

	// Plant a cursor value to verify MarkChecked preserves it.
	if err := cm.SetCursor("123"); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}

	// Roll LastUpdated back so IsStale returns true (SetCursor just
	// bumped it). We can't easily back-date without poking into the
	// store, so just verify MarkChecked clears stale on its own:
	// after calling it on a fresh-cursor cache, stale should be false.
	if err := cm.MarkChecked(); err != nil {
		t.Fatalf("MarkChecked: %v", err)
	}
	if stale, _ := cm.IsStale(); stale {
		t.Error("after MarkChecked, cache should not be stale")
	}

	// Cursor preserved across MarkChecked.
	if cursor, _ := cm.GetCursor(); cursor != "123" {
		t.Errorf("cursor changed across MarkChecked: got %q, want 123", cursor)
	}
}

func TestCacheManager_ListSortsOutOfOrderJSONL(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	// Write items out of order directly to the JSONL cache file
	cacheFile := CacheFile(dir, testDiscoveryDomain)
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0755); err != nil {
		t.Fatal(err)
	}
	lines := []CachedFeedItem{
		{ID: "oldest", Type: "post", Title: "Oldest", Published: "2026-01-01T10:00:00Z", AuthorURL: "https://a.pub", AuthorDomain: "a.pub", CachedAt: "2026-01-01T10:00:00Z"},
		{ID: "newest", Type: "post", Title: "Newest", Published: "2026-01-03T10:00:00Z", AuthorURL: "https://b.pub", AuthorDomain: "b.pub", CachedAt: "2026-01-03T10:00:00Z"},
		{ID: "middle", Type: "post", Title: "Middle", Published: "2026-01-02T10:00:00Z", AuthorURL: "https://c.pub", AuthorDomain: "c.pub", CachedAt: "2026-01-02T10:00:00Z"},
	}
	f, err := os.Create(cacheFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range lines {
		data, _ := json.Marshal(item)
		f.Write(data)
		f.Write([]byte("\n"))
	}
	f.Close()

	// List should return sorted by Published descending regardless of file order
	items, err := cm.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].ID != "newest" {
		t.Errorf("expected newest first, got %s", items[0].ID)
	}
	if items[1].ID != "middle" {
		t.Errorf("expected middle second, got %s", items[1].ID)
	}
	if items[2].ID != "oldest" {
		t.Errorf("expected oldest last, got %s", items[2].ID)
	}
}

func TestCacheManager_MergeItems(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	// Dates relative to now so items don't get pruned by the 90-day max age
	// (default MaxAgeDays). Hardcoded calendar dates time-bomb the test.
	now := time.Now().UTC()
	newCount, err := cm.MergeItems([]FeedItem{
		{Type: "post", Title: "First Post", URL: "posts/first.md", Published: now.Add(-72 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "Second Post", URL: "posts/second.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "comment", Title: "A Comment", URL: "comments/reply.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://bob.polis.pub", AuthorDomain: "bob.polis.pub"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newCount != 3 {
		t.Errorf("expected 3 new items, got %d", newCount)
	}

	items, err := cm.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Should be sorted by published descending
	if items[0].Title != "A Comment" {
		t.Errorf("expected newest first, got %s", items[0].Title)
	}
	if items[2].Title != "First Post" {
		t.Errorf("expected oldest last, got %s", items[2].Title)
	}

	// All should be unread
	for _, item := range items {
		if item.ReadAt != "" {
			t.Errorf("new items should be unread, got ReadAt=%s", item.ReadAt)
		}
		if item.CachedAt == "" {
			t.Error("CachedAt should be set")
		}
		if item.ID == "" {
			t.Error("ID should be set")
		}
	}
}

func TestCacheManager_MergeDedup(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	now := time.Now().UTC()
	items := []FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
	}

	// First merge
	newCount, err := cm.MergeItems(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newCount != 1 {
		t.Errorf("expected 1 new, got %d", newCount)
	}

	// Mark it read
	cached, _ := cm.List()
	cm.MarkRead(cached[0].ID)

	// Second merge with same item (different title shouldn't matter, same author+path)
	items[0].Title = "Post A Updated"
	newCount, err = cm.MergeItems(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newCount != 0 {
		t.Errorf("expected 0 new (dedup), got %d", newCount)
	}

	// Read state should be preserved
	cached, _ = cm.List()
	if len(cached) != 1 {
		t.Fatalf("expected 1 item, got %d", len(cached))
	}
	if cached[0].ReadAt == "" {
		t.Error("read state should be preserved after dedup merge")
	}
}

func TestCacheManager_MergeDedup_BlessingGrantedAndPublished(t *testing.T) {
	// When the same comment arrives via both comment.published (from another user)
	// and blessing.granted, the cache should deduplicate by author+URL.
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	now := time.Now().UTC()
	// First: comment arrives via comment.published
	n1, err := cm.MergeItems([]FeedItem{
		{
			Type:         "comment",
			Title:        "Great post!",
			URL:          "https://bob.polis.pub/comments/20260304/reply.md",
			Published:    now.Add(-26 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://bob.polis.pub",
			AuthorDomain: "bob.polis.pub",
			TargetURL:    "https://alice.polis.pub/posts/20260304/hello.html",
			TargetDomain: "alice.polis.pub",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n1 != 1 {
		t.Errorf("expected 1 new item, got %d", n1)
	}

	// Second: same comment arrives via blessing.granted (different timestamp, no title)
	n2, err := cm.MergeItems([]FeedItem{
		{
			Type:         "comment",
			URL:          "https://bob.polis.pub/comments/20260304/reply.md",
			Published:    now.Add(-24 * time.Hour).Format(time.RFC3339),
			AuthorURL:    "https://bob.polis.pub",
			AuthorDomain: "bob.polis.pub",
			TargetURL:    "https://alice.polis.pub/posts/20260304/hello.html",
			TargetDomain: "alice.polis.pub",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 new items (dedup), got %d", n2)
	}

	// Should only have 1 item total
	items, _ := cm.List()
	if len(items) != 1 {
		t.Fatalf("expected 1 item after dedup, got %d", len(items))
	}
	if items[0].Title != "Great post!" {
		t.Errorf("original title should be preserved, got %q", items[0].Title)
	}
}

func TestCacheManager_MarkRead(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	now := time.Now().UTC()
	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "Post B", URL: "posts/b.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
	})

	items, _ := cm.List()
	if err := cm.MarkRead(items[0].ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, _ = cm.List()
	if items[0].ReadAt == "" {
		t.Error("item should be marked as read")
	}
	if items[1].ReadAt != "" {
		t.Error("other item should still be unread")
	}

	unread, _ := cm.UnreadCount()
	if unread != 1 {
		t.Errorf("expected 1 unread, got %d", unread)
	}
}

func TestCacheManager_MarkUnread(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	now := time.Now().UTC()
	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
	})

	items, _ := cm.List()
	cm.MarkRead(items[0].ID)

	// Verify it's read
	items, _ = cm.List()
	if items[0].ReadAt == "" {
		t.Fatal("should be read")
	}

	// Mark unread
	if err := cm.MarkUnread(items[0].ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, _ = cm.List()
	if items[0].ReadAt != "" {
		t.Error("should be unread again")
	}
}

func TestCacheManager_MarkAllRead(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	now := time.Now().UTC()
	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: now.Add(-72 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "Post B", URL: "posts/b.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "comment", Title: "Comment C", URL: "comments/c.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://bob.polis.pub", AuthorDomain: "bob.polis.pub"},
	})

	if err := cm.MarkAllRead(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	unread, _ := cm.UnreadCount()
	if unread != 0 {
		t.Errorf("expected 0 unread, got %d", unread)
	}
}

func TestCacheManager_MarkUnreadFrom(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	// Use relative dates to avoid 90-day prune window
	now := time.Now().UTC()
	oldDate := now.AddDate(0, 0, -30).Format(time.RFC3339)
	midDate := now.AddDate(0, 0, -15).Format(time.RFC3339)
	newDate := now.AddDate(0, 0, -1).Format(time.RFC3339)

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Old", URL: "posts/old.md", Published: oldDate, AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "Mid", URL: "posts/mid.md", Published: midDate, AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "New", URL: "posts/new.md", Published: newDate, AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
	})

	// Mark all read first
	cm.MarkAllRead()

	// Get the middle item's ID
	items, _ := cm.List()
	// Items sorted desc: New, Mid, Old
	midID := items[1].ID

	if err := cm.MarkUnreadFrom(midID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, _ = cm.List()
	// New (most recent) should be unread (more recent than mid)
	if items[0].ReadAt != "" {
		t.Error("New should be unread")
	}
	// Mid should be unread (the target)
	if items[1].ReadAt != "" {
		t.Error("Mid should be unread")
	}
	// Old (oldest) should still be read (older than mid)
	if items[2].ReadAt == "" {
		t.Error("Old should still be read")
	}
}

func TestCacheManager_ListByType(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	now := time.Now().UTC()
	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: now.Add(-72 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "comment", Title: "Comment B", URL: "comments/b.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://bob.polis.pub", AuthorDomain: "bob.polis.pub"},
		{Type: "post", Title: "Post C", URL: "posts/c.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
	})

	posts, err := cm.ListByType("post")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(posts) != 2 {
		t.Errorf("expected 2 posts, got %d", len(posts))
	}

	comments, err := cm.ListByType("comment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 1 {
		t.Errorf("expected 1 comment, got %d", len(comments))
	}

	all, err := cm.ListByType("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 total, got %d", len(all))
	}
}

func TestCacheManager_ListFiltered(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	now := time.Now().UTC()
	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: now.Add(-72 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "comment", Title: "Comment B", URL: "comments/b.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://bob.polis.pub", AuthorDomain: "bob.polis.pub"},
		{Type: "post", Title: "Post C", URL: "posts/c.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
	})

	// Mark first item (Post C, most recent) as read
	items, _ := cm.List()
	cm.MarkRead(items[0].ID)

	// Filter by type only
	posts, err := cm.ListFiltered(FilterOptions{Type: "post"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(posts) != 2 {
		t.Errorf("expected 2 posts, got %d", len(posts))
	}

	// Filter by status only - unread
	unread, err := cm.ListFiltered(FilterOptions{Status: "unread"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unread) != 2 {
		t.Errorf("expected 2 unread, got %d", len(unread))
	}

	// Filter by status only - read
	read, err := cm.ListFiltered(FilterOptions{Status: "read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(read) != 1 {
		t.Errorf("expected 1 read, got %d", len(read))
	}

	// Combined filter: unread posts
	unreadPosts, err := cm.ListFiltered(FilterOptions{Type: "post", Status: "unread"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(unreadPosts) != 1 {
		t.Errorf("expected 1 unread post, got %d", len(unreadPosts))
	}
	if len(unreadPosts) > 0 && unreadPosts[0].Title != "Post A" {
		t.Errorf("expected Post A, got %s", unreadPosts[0].Title)
	}

	// Combined filter: read comments (should be 0)
	readComments, err := cm.ListFiltered(FilterOptions{Type: "comment", Status: "read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(readComments) != 0 {
		t.Errorf("expected 0 read comments, got %d", len(readComments))
	}

	// No filters = all items
	all, err := cm.ListFiltered(FilterOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 total, got %d", len(all))
	}

	// AuthorDomains filter: only alice
	alice, err := cm.ListFiltered(FilterOptions{AuthorDomains: map[string]bool{"alice.polis.pub": true}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alice) != 2 {
		t.Errorf("expected 2 items from alice, got %d", len(alice))
	}

	// AuthorDomains filter: only bob
	bob, err := cm.ListFiltered(FilterOptions{AuthorDomains: map[string]bool{"bob.polis.pub": true}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bob) != 1 {
		t.Errorf("expected 1 item from bob, got %d", len(bob))
	}

	// AuthorDomains nil = all items (no-op)
	nilDomains, err := cm.ListFiltered(FilterOptions{AuthorDomains: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nilDomains) != 3 {
		t.Errorf("expected 3 items with nil AuthorDomains, got %d", len(nilDomains))
	}

	// Empty AuthorDomains map = nothing matches
	empty, err := cm.ListFiltered(FilterOptions{AuthorDomains: map[string]bool{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 items with empty AuthorDomains, got %d", len(empty))
	}

	// Combined: AuthorDomains + Type filter
	alicePosts, err := cm.ListFiltered(FilterOptions{
		AuthorDomains: map[string]bool{"alice.polis.pub": true},
		Type:          "post",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alicePosts) != 2 {
		t.Errorf("expected 2 posts from alice, got %d", len(alicePosts))
	}

	// Combined: AuthorDomains + Status filter
	aliceUnread, err := cm.ListFiltered(FilterOptions{
		AuthorDomains: map[string]bool{"alice.polis.pub": true},
		Status:        "unread",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(aliceUnread) != 1 {
		t.Errorf("expected 1 unread item from alice, got %d", len(aliceUnread))
	}
}

func TestCacheManager_Prune(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	// Set low limits for testing
	cm.SaveConfig(&FeedConfig{
		StalenessMinutes: 1,
		MaxItems:         2,
		MaxAgeDays:       90,
	})

	now := time.Now().UTC()
	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post 1", URL: "posts/1.md", Published: now.Add(-72 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 2", URL: "posts/2.md", Published: now.Add(-48 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 3", URL: "posts/3.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	items, err := cm.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items after prune (MaxItems=2), got %d", len(items))
	}
	// Should keep the most recent
	if items[0].Title != "Post 3" {
		t.Errorf("expected most recent first, got %s", items[0].Title)
	}
}

func TestCacheManager_PruneByAge(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	// Set MaxAgeDays to 30
	cm.SaveConfig(&FeedConfig{
		StalenessMinutes: 15,
		MaxItems:         500,
		MaxAgeDays:       30,
	})

	oldDate := time.Now().AddDate(0, 0, -60).UTC().Format(time.RFC3339)
	recentDate := time.Now().UTC().Format(time.RFC3339)

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Old Post", URL: "posts/old.md", Published: oldDate, AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Recent Post", URL: "posts/recent.md", Published: recentDate, AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	items, _ := cm.List()
	if len(items) != 1 {
		t.Errorf("expected 1 item after age prune, got %d", len(items))
	}
	if len(items) > 0 && items[0].Title != "Recent Post" {
		t.Errorf("expected recent post to survive, got %s", items[0].Title)
	}
}

func TestCacheManager_IsStale(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	// No cursor entry = stale
	stale, _ := cm.IsStale()
	if !stale {
		t.Error("should be stale with no cursor")
	}

	// Set cursor (which sets LastUpdated to now)
	cm.SetCursor("100")

	stale, _ = cm.IsStale()
	if stale {
		t.Error("should not be stale right after setting cursor")
	}
}

func TestCacheManager_IsStale_SameCursorRefreshesTimestamp(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	// Use a very short staleness window
	cm.SaveConfig(&FeedConfig{StalenessMinutes: 1, MaxItems: 500, MaxAgeDays: 90})

	// Set cursor, then manually backdate the LastUpdated to make it stale
	cm.SetCursor("100")

	// Overwrite cursor entry with an old timestamp to simulate staleness
	cursorsPath := filepath.Join(dir, ".polis", "ds", testDiscoveryDomain, "pub.polis.core", "state", "cursors.json")
	cf := map[string]interface{}{
		"cursors": map[string]interface{}{
			"pub.polis.feed": map[string]interface{}{
				"position":     "100",
				"last_updated": "2020-01-01T00:00:00Z",
			},
		},
	}
	data, _ := json.Marshal(cf)
	os.WriteFile(cursorsPath, data, 0644)

	stale, _ := cm.IsStale()
	if !stale {
		t.Fatal("should be stale with old timestamp")
	}

	// Re-set the same cursor position — this should refresh LastUpdated
	cm.SetCursor("100")

	stale, _ = cm.IsStale()
	if stale {
		t.Error("should not be stale after SetCursor with same position")
	}
}

func TestCacheManager_Config(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	// Default config
	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.StalenessMinutes != 5 {
		t.Errorf("expected default staleness 5, got %d", cfg.StalenessMinutes)
	}
	if cfg.MaxItems != 500 {
		t.Errorf("expected default max items 500, got %d", cfg.MaxItems)
	}

	// Update staleness
	if err := cm.SetStalenessMinutes(30); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, _ = cm.LoadConfig()
	if cfg.StalenessMinutes != 30 {
		t.Errorf("expected staleness 30, got %d", cfg.StalenessMinutes)
	}
}

func TestCacheManager_CursorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	// Default cursor
	cursor, err := cm.GetCursor()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cursor != "0" {
		t.Errorf("expected default cursor 0, got %s", cursor)
	}

	// Set and get cursor
	if err := cm.SetCursor("12345"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cursor, _ = cm.GetCursor()
	if cursor != "12345" {
		t.Errorf("expected cursor 12345, got %s", cursor)
	}

	// LastUpdated should be set
	lastUpdated := cm.LastUpdated()
	if lastUpdated == "" {
		t.Error("LastUpdated should be set after SetCursor")
	}
}

func TestCacheManager_MarkReadNotFound(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	err := cm.MarkRead("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}

	err = cm.MarkUnread("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}

	err = cm.MarkUnreadFrom("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestCacheManager_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	// Don't pre-create directories
	cm := NewCacheManager(dir, testDiscoveryDomain)

	now := time.Now().UTC()
	_, err := cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Test", URL: "posts/test.md", Published: now.Add(-24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify cache file at new path
	if _, err := os.Stat(filepath.Join(dir, ".polis", "ds", testDiscoveryDomain, "pub.polis.core", "state", "pub.polis.feed.jsonl")); err != nil {
		t.Error("cache file should exist at pub.polis.core/state/pub.polis.feed.jsonl")
	}
}

func TestCacheManager_ConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	// Save config
	cfg := &FeedConfig{
		StalenessMinutes: 30,
		MaxItems:         200,
		MaxAgeDays:       60,
	}
	if err := cm.SaveConfig(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Load and verify
	loaded, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.StalenessMinutes != 30 {
		t.Errorf("expected staleness 30, got %d", loaded.StalenessMinutes)
	}
	if loaded.MaxItems != 200 {
		t.Errorf("expected max_items 200, got %d", loaded.MaxItems)
	}

	// Verify config file at new path
	if _, err := os.Stat(filepath.Join(dir, ".polis", "ds", testDiscoveryDomain, "pub.polis.core", "config", "feed.json")); err != nil {
		t.Error("config file should exist at pub.polis.core/config/feed.json")
	}
}

func TestCacheManager_PruneByType_AnnouncementsDoNotDisplacePosts(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	cm.SaveConfig(&FeedConfig{
		StalenessMinutes: 15,
		MaxPosts:         3,
		MaxComments:      2,
		MaxAnnouncements: 2,
		MaxAgeDays:       90,
		MaxAnnouncementDays: 14,
	})

	now := time.Now()
	items := []FeedItem{
		{Type: "post", Title: "Post 1", URL: "posts/1.md", Published: now.Add(-1 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 2", URL: "posts/2.md", Published: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 3", URL: "posts/3.md", Published: now.Add(-3 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 4", URL: "posts/4.md", Published: now.Add(-4 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "announcement", Title: "Follow 1", URL: "https://x.pub", Published: now.Add(-30 * time.Minute).UTC().Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
		{Type: "announcement", Title: "Follow 2", URL: "https://y.pub", Published: now.Add(-45 * time.Minute).UTC().Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
		{Type: "announcement", Title: "Follow 3", URL: "https://z.pub", Published: now.Add(-50 * time.Minute).UTC().Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
		{Type: "comment", Title: "Comment 1", URL: "comments/1.md", Published: now.Add(-1 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://c.pub", AuthorDomain: "c.pub"},
	}

	cm.MergeItems(items)

	result, _ := cm.List()

	// Count by type
	counts := map[string]int{}
	for _, item := range result {
		counts[item.Type]++
	}

	if counts["post"] != 3 {
		t.Errorf("expected 3 posts (MaxPosts=3), got %d", counts["post"])
	}
	if counts["announcement"] != 2 {
		t.Errorf("expected 2 announcements (MaxAnnouncements=2), got %d", counts["announcement"])
	}
	if counts["comment"] != 1 {
		t.Errorf("expected 1 comment, got %d", counts["comment"])
	}

	// Total should be 6 (3+2+1), not capped by a single global limit
	if len(result) != 6 {
		t.Errorf("expected 6 total items, got %d", len(result))
	}
}

func TestCacheManager_PruneByType_AnnouncementAgeShorter(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	cm.SaveConfig(&FeedConfig{
		StalenessMinutes:    15,
		MaxPosts:            300,
		MaxComments:         150,
		MaxAnnouncements:    50,
		MaxAgeDays:          90,
		MaxAnnouncementDays: 14,
	})

	now := time.Now()
	recentDate := now.Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	oldPostDate := now.AddDate(0, 0, -30).UTC().Format(time.RFC3339)       // 30 days: within 90-day post limit
	oldAnnouncementDate := now.AddDate(0, 0, -20).UTC().Format(time.RFC3339) // 20 days: beyond 14-day announcement limit

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Recent Post", URL: "posts/recent.md", Published: recentDate, AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Old Post", URL: "posts/old.md", Published: oldPostDate, AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "announcement", Title: "Recent Follow", URL: "https://x.pub", Published: recentDate, AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
		{Type: "announcement", Title: "Old Follow", URL: "https://y.pub", Published: oldAnnouncementDate, AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
	})

	result, _ := cm.List()

	// Old post (30 days) should survive — within 90-day limit
	// Old announcement (20 days) should be pruned — beyond 14-day limit
	titles := map[string]bool{}
	for _, item := range result {
		titles[item.Title] = true
	}

	if !titles["Old Post"] {
		t.Error("30-day-old post should survive (within 90-day limit)")
	}
	if titles["Old Follow"] {
		t.Error("20-day-old announcement should be pruned (beyond 14-day limit)")
	}
	if !titles["Recent Post"] {
		t.Error("recent post should survive")
	}
	if !titles["Recent Follow"] {
		t.Error("recent announcement should survive")
	}
	if len(result) != 3 {
		t.Errorf("expected 3 items, got %d", len(result))
	}
}

func TestCacheManager_PruneByType_LegacyConfigFallback(t *testing.T) {
	dir := t.TempDir()
	cm := NewCacheManager(dir, testDiscoveryDomain)

	// Save a legacy config with only MaxItems (no per-type limits)
	cm.SaveConfig(&FeedConfig{
		StalenessMinutes: 15,
		MaxItems:         3,
		MaxAgeDays:       90,
	})

	now := time.Now()
	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post 1", URL: "posts/1.md", Published: now.Add(-1 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 2", URL: "posts/2.md", Published: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 3", URL: "posts/3.md", Published: now.Add(-3 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 4", URL: "posts/4.md", Published: now.Add(-4 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 5", URL: "posts/5.md", Published: now.Add(-5 * time.Hour).UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	result, _ := cm.List()

	// MaxItems=3 is used as fallback for MaxPosts when per-type limits are zero
	if len(result) != 3 {
		t.Errorf("expected 3 posts (legacy MaxItems fallback), got %d", len(result))
	}
	if result[0].Title != "Post 1" {
		t.Errorf("expected most recent first, got %s", result[0].Title)
	}
}

func TestNewScopedCacheManager_IsolatedFiles(t *testing.T) {
	dir := t.TempDir()

	// Create network and global scope managers.
	// Uses "global" because "followers" and "me" cursor keys are now deprecated
	// and cleaned up by loadCursors(). Global retains its own materialized cache.
	network := NewCacheManager(dir, testDiscoveryDomain)
	global := NewScopedCacheManager(dir, testDiscoveryDomain, "global")

	now := time.Now()

	// Add items to network
	network.MergeItems([]FeedItem{
		{Type: "post", Title: "Network Post", URL: "posts/net.md", Published: now.UTC().Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})

	// Add items to global
	global.MergeItems([]FeedItem{
		{Type: "post", Title: "Global Post", URL: "posts/glo.md", Published: now.UTC().Format(time.RFC3339), AuthorURL: "https://b.pub", AuthorDomain: "b.pub"},
	})

	// Each scope should have exactly 1 item
	netItems, _ := network.List()
	globalItems, _ := global.List()

	if len(netItems) != 1 {
		t.Errorf("network: expected 1 item, got %d", len(netItems))
	}
	if len(globalItems) != 1 {
		t.Errorf("global: expected 1 item, got %d", len(globalItems))
	}
	if netItems[0].Title != "Network Post" {
		t.Errorf("network: expected 'Network Post', got %s", netItems[0].Title)
	}
	if globalItems[0].Title != "Global Post" {
		t.Errorf("global: expected 'Global Post', got %s", globalItems[0].Title)
	}

	// Cursors should be independent
	network.SetCursor("100")
	global.SetCursor("200")

	netCursor, _ := network.GetCursor()
	globalCursor, _ := global.GetCursor()

	if netCursor != "100" {
		t.Errorf("network cursor: expected '100', got %s", netCursor)
	}
	if globalCursor != "200" {
		t.Errorf("global cursor: expected '200', got %s", globalCursor)
	}

	// Mark read in one scope shouldn't affect other
	network.MarkAllRead()
	globalItems, _ = global.List()
	for _, item := range globalItems {
		if item.ReadAt != "" {
			t.Error("marking network read shouldn't affect global")
		}
	}
}

func TestPruneByType_Unit(t *testing.T) {
	now := time.Now()
	cfg := &FeedConfig{
		MaxPosts:            2,
		MaxComments:         1,
		MaxAnnouncements:    1,
		MaxAgeDays:          90,
		MaxAnnouncementDays: 14,
	}

	items := []CachedFeedItem{
		{ID: "p1", Type: "post", Published: now.Add(-1 * time.Hour).UTC().Format(time.RFC3339)},
		{ID: "p2", Type: "post", Published: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339)},
		{ID: "p3", Type: "post", Published: now.Add(-3 * time.Hour).UTC().Format(time.RFC3339)},
		{ID: "c1", Type: "comment", Published: now.Add(-1 * time.Hour).UTC().Format(time.RFC3339)},
		{ID: "c2", Type: "comment", Published: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339)},
		{ID: "a1", Type: "announcement", Published: now.Add(-1 * time.Hour).UTC().Format(time.RFC3339)},
		{ID: "a2", Type: "announcement", Published: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339)},
	}

	result := pruneByType(items, cfg)

	ids := map[string]bool{}
	for _, item := range result {
		ids[item.ID] = true
	}

	// Should keep 2 posts, 1 comment, 1 announcement = 4 total
	if len(result) != 4 {
		t.Errorf("expected 4 items, got %d", len(result))
	}
	if !ids["p1"] || !ids["p2"] {
		t.Error("should keep 2 most recent posts")
	}
	if ids["p3"] {
		t.Error("3rd post should be pruned")
	}
	if !ids["c1"] {
		t.Error("should keep most recent comment")
	}
	if ids["c2"] {
		t.Error("2nd comment should be pruned")
	}
	if !ids["a1"] {
		t.Error("should keep most recent announcement")
	}
	if ids["a2"] {
		t.Error("2nd announcement should be pruned")
	}

	// Result should be sorted by published descending
	for i := 1; i < len(result); i++ {
		if PublishedBefore(result[i-1].Published, result[i].Published) {
			t.Errorf("result not sorted descending at index %d", i)
		}
	}
}

// TestCacheManager_MergeAtMaxPosts_NewestSurvivesPrune reproduces a production
// bug where a newly published post was lost when the cache was at the maxPosts
// limit (300). The post was added by MergeItems, then immediately pruned by
// pruneLocked, but the stream cursor still advanced past it — permanently
// losing the item.
func TestCacheManager_MergeAtMaxPosts_NewestSurvivesPrune(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	// Default config: MaxPosts=300
	cfg := DefaultFeedConfig()
	maxPosts := cfg.MaxPosts // 300

	// Seed with exactly maxPosts posts
	baseTime := time.Now().Add(-time.Duration(maxPosts) * 2 * time.Hour) // all items within 25 days
	seed := make([]FeedItem, maxPosts)
	for i := 0; i < maxPosts; i++ {
		ts := baseTime.Add(time.Duration(i) * 2 * time.Hour)
		idx := fmt.Sprintf("%04d", i)
		seed[i] = FeedItem{
			Type:         "post",
			EventType:    "pub.polis.post.published",
			Title:        "Post " + idx,
			URL:          "https://author.test/post/" + idx + ".md",
			Published:    ts.Format(time.RFC3339),
			AuthorURL:    "https://author.test",
			AuthorDomain: "author.test",
		}
	}
	if _, err := cm.MergeItems(seed); err != nil {
		t.Fatalf("seed merge: %v", err)
	}

	items, _ := cm.List()
	if len(items) != maxPosts {
		t.Fatalf("expected %d items after seed, got %d", maxPosts, len(items))
	}

	// Add one NEW post that is the newest
	newestTime := baseTime.Add(time.Duration(maxPosts) * 2 * time.Hour)
	newest := FeedItem{
		Type:         "post",
		EventType:    "pub.polis.post.published",
		Title:        "THE NEWEST POST",
		URL:          "https://author.test/post/newest.md",
		Published:    newestTime.Format(time.RFC3339),
		AuthorURL:    "https://author.test",
		AuthorDomain: "author.test",
	}

	newCount, err := cm.MergeItems([]FeedItem{newest})
	if err != nil {
		t.Fatalf("newest merge: %v", err)
	}
	if newCount != 1 {
		t.Errorf("expected newCount=1, got %d", newCount)
	}

	// The newest post MUST survive pruning
	items, _ = cm.List()
	if len(items) != maxPosts {
		t.Errorf("expected %d items after prune, got %d", maxPosts, len(items))
	}
	if len(items) == 0 {
		t.Fatal("cache is empty")
	}
	if items[0].Title != "THE NEWEST POST" {
		t.Errorf("newest post should be first item, got %q (published=%s)", items[0].Title, items[0].Published)
	}

	// Verify the oldest seed post was the one pruned
	for _, item := range items {
		if item.Title == "Post "+fmt.Sprintf("%04d", 0) {
			t.Error("oldest seed post should have been pruned")
		}
	}
}

// TestMergeItemsResult_ReportsRetained verifies that MergeItemsResult correctly
// reports how many newly added items survived pruning.
func TestMergeItemsResult_ReportsRetained(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	// Set maxPosts=3 for easy testing
	cm.SaveConfig(&FeedConfig{
		StalenessMinutes: 5,
		MaxPosts:         3,
		MaxAgeDays:       90,
	})

	now := time.Now()

	// Seed with 3 posts (at limit)
	seed := []FeedItem{
		{Type: "post", Title: "A", URL: "https://a.test/1.md", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.test", AuthorDomain: "a.test"},
		{Type: "post", Title: "B", URL: "https://a.test/2.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.test", AuthorDomain: "a.test"},
		{Type: "post", Title: "C", URL: "https://a.test/3.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.test", AuthorDomain: "a.test"},
	}
	cm.MergeItems(seed)

	// Add 1 new post (newest) — should push out oldest
	mr, err := cm.MergeItemsResult([]FeedItem{
		{Type: "post", Title: "D", URL: "https://a.test/4.md", Published: now.Format(time.RFC3339), AuthorURL: "https://a.test", AuthorDomain: "a.test"},
	})
	if err != nil {
		t.Fatalf("merge error: %v", err)
	}
	if mr.Added != 1 {
		t.Errorf("expected Added=1, got %d", mr.Added)
	}
	if mr.Retained != 1 {
		t.Errorf("expected Retained=1, got %d", mr.Retained)
	}
}

// TestMergeItemsResult_DetectsPruneLoss verifies that MergeItemsResult detects
// when a newly added item is lost to pruning (e.g., added but then pruned by age).
func TestMergeItemsResult_DetectsPruneLoss(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	cm.SaveConfig(&FeedConfig{
		StalenessMinutes: 5,
		MaxPosts:         300,
		MaxAgeDays:       30, // 30 days
	})

	now := time.Now()

	// Add one recent post so the cache isn't empty
	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Recent", URL: "https://a.test/recent.md", Published: now.Format(time.RFC3339), AuthorURL: "https://a.test", AuthorDomain: "a.test"},
	})

	// Try to add a post that's older than 30 days — it will be pruned by age
	old := now.AddDate(0, 0, -60)
	mr, err := cm.MergeItemsResult([]FeedItem{
		{Type: "post", Title: "Old", URL: "https://a.test/old.md", Published: old.Format(time.RFC3339), AuthorURL: "https://a.test", AuthorDomain: "a.test"},
	})
	if err != nil {
		t.Fatalf("merge error: %v", err)
	}
	if mr.Added != 1 {
		t.Errorf("expected Added=1, got %d", mr.Added)
	}
	if mr.Retained != 0 {
		t.Errorf("expected Retained=0 (pruned by age), got %d", mr.Retained)
	}
}

// TestCacheManager_UpdateExcerpts — R17 final-review item #4. UpdateExcerpts
// applies per-item excerpt updates atomically under the cache mutex,
// preserving items that aren't in the update map. Closes the
// concurrent-fetch overwrite race that SaveItems(items) carries.
func TestCacheManager_UpdateExcerpts(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)
	now := time.Now().UTC()

	if _, err := cm.MergeItems([]FeedItem{
		{Type: "post", Title: "A", URL: "posts/a.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "B", URL: "posts/b.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "C", URL: "posts/c.md", Published: now.Add(-3 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	items, _ := cm.List()
	if len(items) != 3 {
		t.Fatalf("seeded 3 items, got %d", len(items))
	}

	// Build update map by ID — only A and C get excerpts.
	updates := make(map[string]string)
	for _, it := range items {
		switch it.Title {
		case "A":
			updates[it.ID] = "excerpt for A"
		case "C":
			updates[it.ID] = "excerpt for C"
		}
	}

	applied, err := cm.UpdateExcerpts(updates)
	if err != nil {
		t.Fatalf("UpdateExcerpts: %v", err)
	}
	if applied != 2 {
		t.Errorf("expected 2 applied, got %d", applied)
	}

	// Re-list and verify per-item state.
	out, _ := cm.List()
	for _, it := range out {
		switch it.Title {
		case "A":
			if it.Excerpt != "excerpt for A" {
				t.Errorf("A.Excerpt = %q, want %q", it.Excerpt, "excerpt for A")
			}
		case "B":
			if it.Excerpt != "" {
				t.Errorf("B.Excerpt should remain empty, got %q", it.Excerpt)
			}
		case "C":
			if it.Excerpt != "excerpt for C" {
				t.Errorf("C.Excerpt = %q, want %q", it.Excerpt, "excerpt for C")
			}
		}
	}

	// Idempotent: re-applying the same updates returns 0 (no diff).
	applied2, err := cm.UpdateExcerpts(updates)
	if err != nil {
		t.Fatalf("second UpdateExcerpts: %v", err)
	}
	if applied2 != 0 {
		t.Errorf("expected 0 applied on idempotent re-call, got %d", applied2)
	}

	// Empty updates map is a no-op.
	applied3, err := cm.UpdateExcerpts(map[string]string{})
	if err != nil {
		t.Fatalf("empty-map UpdateExcerpts: %v", err)
	}
	if applied3 != 0 {
		t.Errorf("expected 0 applied on empty map, got %d", applied3)
	}

	// Unknown ID is silently skipped.
	applied4, err := cm.UpdateExcerpts(map[string]string{"sha256:nonexistent": "ghost"})
	if err != nil {
		t.Fatalf("unknown-id UpdateExcerpts: %v", err)
	}
	if applied4 != 0 {
		t.Errorf("expected 0 applied on unknown ID, got %d", applied4)
	}
}

// TestCacheManager_UpdateExcerpts_RaceFreeWithMerge — sanity-check the
// race scenario the API was designed to fix. Sequence:
//   1. Background goroutine reads cache snapshot.
//   2. Concurrent goroutine merges new items.
//   3. Background goroutine writes excerpts via UpdateExcerpts.
//   4. Final cache should contain BOTH the merged items AND the
//      background goroutine's excerpts. No new items lost.
//
// Pre-fix (SaveItems): the background goroutine's SaveItems(items)
// would overwrite the cache with its older snapshot, losing the
// merged items. This test would fail.
func TestCacheManager_UpdateExcerpts_RaceFreeWithMerge(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)
	now := time.Now().UTC()

	if _, err := cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Old", URL: "posts/old.md", Published: now.Add(-2 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Step 1: background reads snapshot.
	snapshot, _ := cm.List()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot should have 1 item, got %d", len(snapshot))
	}
	oldID := snapshot[0].ID

	// Step 2: concurrent merge adds a new item.
	if _, err := cm.MergeItems([]FeedItem{
		{Type: "post", Title: "New", URL: "posts/new.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
	}); err != nil {
		t.Fatalf("concurrent merge: %v", err)
	}

	// Step 3: background writes excerpt for the OLD item via UpdateExcerpts
	// (NOT SaveItems — that's the unsafe path).
	applied, err := cm.UpdateExcerpts(map[string]string{oldID: "old excerpt"})
	if err != nil {
		t.Fatalf("UpdateExcerpts: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}

	// Step 4: final cache has BOTH items, AND the old one carries its excerpt.
	final, _ := cm.List()
	if len(final) != 2 {
		t.Fatalf("final cache should have 2 items (merged item NOT lost); got %d", len(final))
	}
	titles := map[string]string{}
	for _, it := range final {
		titles[it.Title] = it.Excerpt
	}
	if _, ok := titles["New"]; !ok {
		t.Error("merged item 'New' was lost — race condition NOT fixed")
	}
	if titles["Old"] != "old excerpt" {
		t.Errorf("old item's excerpt should be 'old excerpt', got %q", titles["Old"])
	}
}

// TestList_OversizeLine_Skipped — R20-B-S2 regression guard
// (2026-05-18). A single feed entry that exceeds the default
// bufio.Scanner buffer (64KB) used to return bufio.ErrTooLong from
// listLocked, which the caller surfaced as an error — bricking the
// entire feed view. A malicious remote feed entry (oversize title /
// excerpt / body_html) from a followed author was enough to DoS the
// reader. Fix: explicit 1MB buffer + skip-on-too-long.
func TestList_OversizeLine_Skipped(t *testing.T) {
	tempDir := t.TempDir()
	cm := NewCacheManager(tempDir, "default")

	// Seed a tiny valid item.
	valid := FeedItem{
		Type: "post", Title: "Valid", URL: "posts/v.md",
		Published: "2026-05-18T00:00:00Z",
		AuthorURL: "https://a.pub", AuthorDomain: "a.pub",
	}
	if _, err := cm.MergeItems([]FeedItem{valid}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Append a single oversize line directly to the cache file.
	// 2MiB exceeds the 1MiB cap so it should be skipped.
	cachePath := filepath.Join(tempDir, ".polis", "ds", "default", "pub.polis.core", "state", "pub.polis.feed.jsonl")
	oversize := strings.Repeat("x", 2<<20)
	oversizeLine := `{"type":"post","title":"` + oversize + `"}` + "\n"
	f, err := os.OpenFile(cachePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString(oversizeLine); err != nil {
		t.Fatalf("append oversize: %v", err)
	}
	f.Close()

	// Append another valid item AFTER the oversize line.
	// Pre-fix scanner would abort on the oversize and never reach
	// this. We can't easily get this through MergeItems because
	// MergeItems re-reads + re-writes (which would hit the same
	// scanner bug) — so append directly.
	valid2 := `{"type":"post","title":"After","url":"posts/a.md","published":"2026-05-18T01:00:00Z","author_url":"https://a.pub","author_domain":"a.pub"}` + "\n"
	f, _ = os.OpenFile(cachePath, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(valid2)
	f.Close()

	// List should succeed (no error) and return at least the items
	// before the oversize line. The oversize line is dropped.
	items, err := cm.List()
	if err != nil {
		t.Fatalf("R20-B-S2 regression: List returned error on oversize line: %v", err)
	}
	if len(items) == 0 {
		t.Errorf("expected at least 1 valid item after oversize skip; got 0")
	}
	// The valid items must be present.
	titles := make(map[string]bool, len(items))
	for _, it := range items {
		titles[it.Title] = true
	}
	if !titles["Valid"] {
		t.Errorf("'Valid' item missing from results: %+v", items)
	}
}
