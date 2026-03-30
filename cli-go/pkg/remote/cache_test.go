package remote

import (
	"sync"
	"testing"
	"time"
)

func TestLRUCache_BasicSetGet(t *testing.T) {
	c := NewLRUCache(10)
	c.Set("key1", "val1", time.Minute)

	v, ok := c.Get("key1")
	if !ok || v != "val1" {
		t.Errorf("expected val1, got %q (ok=%v)", v, ok)
	}
}

func TestLRUCache_Miss(t *testing.T) {
	c := NewLRUCache(10)
	_, ok := c.Get("nonexistent")
	if ok {
		t.Error("expected miss for nonexistent key")
	}
}

func TestLRUCache_TTLExpiry(t *testing.T) {
	c := NewLRUCache(10)
	c.Set("key1", "val1", time.Millisecond)

	time.Sleep(5 * time.Millisecond)

	_, ok := c.Get("key1")
	if ok {
		t.Error("expected miss after TTL expiry")
	}
	if c.Len() != 0 {
		t.Errorf("expected 0 entries after expiry eviction, got %d", c.Len())
	}
}

func TestLRUCache_Eviction(t *testing.T) {
	c := NewLRUCache(3)
	c.Set("a", "1", time.Minute)
	c.Set("b", "2", time.Minute)
	c.Set("c", "3", time.Minute)

	if c.Len() != 3 {
		t.Errorf("expected 3 entries, got %d", c.Len())
	}

	// Adding a 4th should evict "a" (least recently used)
	c.Set("d", "4", time.Minute)
	if c.Len() != 3 {
		t.Errorf("expected 3 entries after eviction, got %d", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Error("expected 'a' to be evicted")
	}
	if v, ok := c.Get("d"); !ok || v != "4" {
		t.Error("expected 'd' to exist")
	}
}

func TestLRUCache_AccessUpdatesOrder(t *testing.T) {
	c := NewLRUCache(3)
	c.Set("a", "1", time.Minute)
	c.Set("b", "2", time.Minute)
	c.Set("c", "3", time.Minute)

	// Access "a" to move it to front
	c.Get("a")

	// Add "d" — should evict "b" (now least recently used)
	c.Set("d", "4", time.Minute)
	if _, ok := c.Get("b"); ok {
		t.Error("expected 'b' to be evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("expected 'a' to still exist after access")
	}
}

func TestLRUCache_UpdateExisting(t *testing.T) {
	c := NewLRUCache(10)
	c.Set("key1", "old", time.Minute)
	c.Set("key1", "new", time.Minute)

	v, ok := c.Get("key1")
	if !ok || v != "new" {
		t.Errorf("expected 'new', got %q", v)
	}
	if c.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", c.Len())
	}
}

func TestLRUCache_Concurrent(t *testing.T) {
	c := NewLRUCache(100)
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('A' + i%26))
			c.Set(key, "val", time.Minute)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('A' + i%26))
			c.Get(key)
		}(i)
	}

	wg.Wait()
	// No panics = pass
}

func TestTTLForURL(t *testing.T) {
	tests := []struct {
		url string
		ttl time.Duration
	}{
		{"https://alice.polis.pub/.well-known/polis", 5 * time.Minute},
		{"https://alice.polis.pub/content/pub.polis.core/post/20250101/hello.md", 5 * time.Minute},
		{"https://alice.polis.pub/content/pub.polis.core/comment/20250101/reply.md", 5 * time.Minute},
		{"https://alice.polis.pub/content/pub.polis.core/index.jsonl", 2 * time.Minute},
		{"https://alice.polis.pub/content/pub.polis.core/public.jsonl", 2 * time.Minute},
		{"https://alice.polis.pub/posts/hello.html", 3 * time.Minute},
	}

	for _, tt := range tests {
		got := TTLForURL(tt.url)
		if got != tt.ttl {
			t.Errorf("TTLForURL(%q) = %v, want %v", tt.url, got, tt.ttl)
		}
	}
}
