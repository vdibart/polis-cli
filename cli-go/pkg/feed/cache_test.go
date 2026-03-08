package feed

import (
	"encoding/json"
	"os"
	"path/filepath"
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

	newCount, err := cm.MergeItems([]FeedItem{
		{Type: "post", Title: "First Post", URL: "posts/first.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "Second Post", URL: "posts/second.md", Published: "2026-02-02T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "comment", Title: "A Comment", URL: "comments/reply.md", Published: "2026-02-03T10:00:00Z", AuthorURL: "https://bob.polis.pub", AuthorDomain: "bob.polis.pub"},
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

	items := []FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
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

	// First: comment arrives via comment.published
	n1, err := cm.MergeItems([]FeedItem{
		{
			Type:         "comment",
			Title:        "Great post!",
			URL:          "https://bob.polis.pub/comments/20260304/reply.md",
			Published:    "2026-02-04T10:00:00Z",
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
			Published:    "2026-02-04T12:00:00Z",
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

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "Post B", URL: "posts/b.md", Published: "2026-02-02T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
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

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
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

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "Post B", URL: "posts/b.md", Published: "2026-02-02T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "comment", Title: "Comment C", URL: "comments/c.md", Published: "2026-02-03T10:00:00Z", AuthorURL: "https://bob.polis.pub", AuthorDomain: "bob.polis.pub"},
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

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Old", URL: "posts/old.md", Published: "2026-01-01T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "Mid", URL: "posts/mid.md", Published: "2026-01-15T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "post", Title: "New", URL: "posts/new.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
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
	// New (2026-02-01) should be unread (more recent than mid)
	if items[0].ReadAt != "" {
		t.Error("New should be unread")
	}
	// Mid (2026-01-15) should be unread (the target)
	if items[1].ReadAt != "" {
		t.Error("Mid should be unread")
	}
	// Old (2026-01-01) should still be read (older than mid)
	if items[2].ReadAt == "" {
		t.Error("Old should still be read")
	}
}

func TestCacheManager_ListByType(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "comment", Title: "Comment B", URL: "comments/b.md", Published: "2026-02-02T10:00:00Z", AuthorURL: "https://bob.polis.pub", AuthorDomain: "bob.polis.pub"},
		{Type: "post", Title: "Post C", URL: "posts/c.md", Published: "2026-02-03T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
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

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post A", URL: "posts/a.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
		{Type: "comment", Title: "Comment B", URL: "comments/b.md", Published: "2026-02-02T10:00:00Z", AuthorURL: "https://bob.polis.pub", AuthorDomain: "bob.polis.pub"},
		{Type: "post", Title: "Post C", URL: "posts/c.md", Published: "2026-02-03T10:00:00Z", AuthorURL: "https://alice.polis.pub", AuthorDomain: "alice.polis.pub"},
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

	cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Post 1", URL: "posts/1.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 2", URL: "posts/2.md", Published: "2026-02-02T10:00:00Z", AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "Post 3", URL: "posts/3.md", Published: "2026-02-03T10:00:00Z", AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
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
	if cfg.StalenessMinutes != 15 {
		t.Errorf("expected default staleness 15, got %d", cfg.StalenessMinutes)
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

	_, err := cm.MergeItems([]FeedItem{
		{Type: "post", Title: "Test", URL: "posts/test.md", Published: "2026-02-01T10:00:00Z", AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
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
