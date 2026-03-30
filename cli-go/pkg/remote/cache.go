package remote

import (
	"strings"
	"sync"
	"time"
)

// LRUCache is a thread-safe LRU cache with per-entry TTL expiry.
// Used to cache remote content fetches and reduce redundant HTTP requests
// when multiple tenants follow the same author.
type LRUCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	head    *cacheEntry // most recently used
	tail    *cacheEntry // least recently used
	maxSize int
}

type cacheEntry struct {
	key       string
	value     string
	expiresAt time.Time
	prev      *cacheEntry
	next      *cacheEntry
}

// NewLRUCache creates an LRU cache with the given maximum number of entries.
func NewLRUCache(maxSize int) *LRUCache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &LRUCache{
		entries: make(map[string]*cacheEntry, maxSize),
		maxSize: maxSize,
	}
}

// Get retrieves a value from the cache. Returns the value and true if found
// and not expired, or empty string and false otherwise.
func (c *LRUCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		return "", false
	}

	// Check TTL expiry
	if time.Now().After(e.expiresAt) {
		c.removeEntry(e)
		return "", false
	}

	// Move to front (most recently used)
	c.moveToFront(e)
	return e.value, true
}

// Set stores a value in the cache with the given TTL.
func (c *LRUCache) Set(key, value string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing entry
	if e, ok := c.entries[key]; ok {
		e.value = value
		e.expiresAt = time.Now().Add(ttl)
		c.moveToFront(e)
		return
	}

	// Evict if at capacity
	if len(c.entries) >= c.maxSize {
		c.removeTail()
	}

	// Add new entry at front
	e := &cacheEntry{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
	c.entries[key] = e
	c.addToFront(e)
}

// Len returns the number of entries in the cache (including expired ones
// that haven't been evicted yet).
func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// TTLForURL returns the appropriate cache TTL for the given URL.
// Posts and comments are versioned (can be republished) so use short TTLs.
func TTLForURL(url string) time.Duration {
	switch {
	case strings.Contains(url, "/.well-known/polis"):
		return 5 * time.Minute // keys can rotate
	case strings.Contains(url, "/content/pub.polis.core/post/"):
		return 5 * time.Minute // versioned, republishes infrequent
	case strings.Contains(url, "/content/pub.polis.core/comment/"):
		return 5 * time.Minute // versioned, republishes infrequent
	case strings.Contains(url, "/index.jsonl") || strings.Contains(url, "/public.jsonl"):
		return 2 * time.Minute // changes on publish/republish
	default:
		return 3 * time.Minute // conservative default
	}
}

// --- internal linked list operations ---

func (c *LRUCache) addToFront(e *cacheEntry) {
	e.prev = nil
	e.next = c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}

func (c *LRUCache) moveToFront(e *cacheEntry) {
	if e == c.head {
		return
	}
	c.detach(e)
	c.addToFront(e)
}

func (c *LRUCache) removeTail() {
	if c.tail == nil {
		return
	}
	c.removeEntry(c.tail)
}

func (c *LRUCache) removeEntry(e *cacheEntry) {
	delete(c.entries, e.key)
	c.detach(e)
}

func (c *LRUCache) detach(e *cacheEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		c.tail = e.prev
	}
	e.prev = nil
	e.next = nil
}
