package feed

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// ParsePublished parses a published timestamp that may be in ISO 8601 or
// JavaScript Date.toString() format (e.g. "Wed Mar 04 2026 22:00:01 GMT+0000 (Coordinated Universal Time)").
// Returns zero time if unparseable.
func ParsePublished(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// Try ISO 8601 first (most common in well-formed data)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04:05Z", s); err == nil {
		return t
	}
	// JS Date.toString(): "Wed Mar 04 2026 22:00:01 GMT+0000 (Coordinated Universal Time)"
	// Strip the parenthesized timezone name suffix before parsing
	cleaned := s
	if idx := strings.Index(s, " ("); idx > 0 {
		cleaned = s[:idx]
	}
	if t, err := time.Parse("Mon Jan 02 2006 15:04:05 GMT-0700", cleaned); err == nil {
		return t
	}
	return time.Time{}
}

// PublishedBefore returns true if timestamp a is before timestamp b.
func PublishedBefore(a, b string) bool {
	ta, tb := ParsePublished(a), ParsePublished(b)
	if ta.IsZero() && tb.IsZero() {
		return a < b // fallback to string comparison
	}
	if ta.IsZero() {
		return true // unknown dates sort last
	}
	if tb.IsZero() {
		return false
	}
	return ta.Before(tb)
}

// Version is set at init time by cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// CachedFeedItem represents a single item in the feed cache.
type CachedFeedItem struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	Published    string `json:"published"`
	Hash         string `json:"hash,omitempty"`
	AuthorURL    string `json:"author_url"`
	AuthorDomain string `json:"author_domain"`
	TargetURL    string `json:"target_url,omitempty"`
	TargetDomain string `json:"target_domain,omitempty"`
	CachedAt     string `json:"cached_at"`
	ReadAt       string `json:"read_at,omitempty"`
	Excerpt      string `json:"excerpt,omitempty"`
}

// FeedConfig holds user-editable feed configuration.
type FeedConfig struct {
	StalenessMinutes int `json:"staleness_minutes"`
	MaxItems         int `json:"max_items"`
	MaxAgeDays       int `json:"max_age_days"`
}

// DefaultFeedConfig returns the default feed configuration.
func DefaultFeedConfig() FeedConfig {
	return FeedConfig{
		StalenessMinutes: 15,
		MaxItems:         500,
		MaxAgeDays:       90,
	}
}

// CacheManager handles feed cache operations.
type CacheManager struct {
	mu         sync.Mutex
	cacheFile  string        // state/polis.feed.jsonl
	configFile string        // config/feed.json
	store      *stream.Store // for cursor operations
}

// CacheFile returns the path to pub.polis.feed.jsonl for a given DS domain.
func CacheFile(dataDir, discoveryDomain string) string {
	return filepath.Join(dataDir, ".polis", "ds", discoveryDomain, "pub.polis.core", "state", "pub.polis.feed.jsonl")
}

// ConfigFile returns the path to config/feed.json for a given DS domain.
func ConfigFile(dataDir, discoveryDomain string) string {
	return filepath.Join(dataDir, ".polis", "ds", discoveryDomain, "pub.polis.core", "config", "feed.json")
}

// NewCacheManager creates a new feed cache manager scoped to a discovery service domain.
func NewCacheManager(dataDir, discoveryDomain string) *CacheManager {
	return &CacheManager{
		cacheFile:  CacheFile(dataDir, discoveryDomain),
		configFile: ConfigFile(dataDir, discoveryDomain),
		store:      stream.NewStore(dataDir, discoveryDomain, "pub.polis.core"),
	}
}

// ComputeItemID generates a deterministic ID for a feed item.
// ID = first 16 hex chars of sha256(author_url + "|" + relative_path).
func ComputeItemID(authorURL, path string) string {
	h := sha256.Sum256([]byte(authorURL + "|" + path))
	return fmt.Sprintf("%x", h[:8])
}

// List returns all cached feed items, sorted by published descending.
func (cm *CacheManager) List() ([]CachedFeedItem, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.listLocked()
}

func (cm *CacheManager) listLocked() ([]CachedFeedItem, error) {
	file, err := os.Open(cm.cacheFile)
	if err != nil {
		if os.IsNotExist(err) {
			return []CachedFeedItem{}, nil
		}
		return nil, fmt.Errorf("failed to open cache file: %w", err)
	}
	defer file.Close()

	var items []CachedFeedItem
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var item CachedFeedItem
		if err := json.Unmarshal(line, &item); err != nil {
			continue // Skip malformed lines
		}
		items = append(items, item)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read cache: %w", err)
	}

	// Sort by Published descending to match the docstring contract.
	// The JSONL file order is not guaranteed (corruption, manual edits, older versions).
	sort.Slice(items, func(i, j int) bool {
		return PublishedBefore(items[j].Published, items[i].Published)
	})

	return items, nil
}

// ListByType returns cached feed items filtered by type ("post" or "comment").
func (cm *CacheManager) ListByType(itemType string) ([]CachedFeedItem, error) {
	all, err := cm.List()
	if err != nil {
		return nil, err
	}

	if itemType == "" {
		return all, nil
	}

	var filtered []CachedFeedItem
	for _, item := range all {
		if item.Type == itemType {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// FilterOptions configures feed list filtering.
type FilterOptions struct {
	Type   string // "post", "comment", or "" (all)
	Status string // "read", "unread", or "" (all)
}

// ListFiltered returns cached feed items filtered by type and/or read status.
func (cm *CacheManager) ListFiltered(opts FilterOptions) ([]CachedFeedItem, error) {
	all, err := cm.List()
	if err != nil {
		return nil, err
	}

	if opts.Type == "" && opts.Status == "" {
		return all, nil
	}

	var filtered []CachedFeedItem
	for _, item := range all {
		if opts.Type != "" && item.Type != opts.Type {
			continue
		}
		if opts.Status == "unread" && item.ReadAt != "" {
			continue
		}
		if opts.Status == "read" && item.ReadAt == "" {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, nil
}

// UnreadCount returns the number of unread items in the cache.
func (cm *CacheManager) UnreadCount() (int, error) {
	items, err := cm.List()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, item := range items {
		if item.ReadAt == "" {
			count++
		}
	}
	return count, nil
}

// GetCursor returns the feed stream cursor position, or "0" if not set.
func (cm *CacheManager) GetCursor() (string, error) {
	return cm.store.GetCursor("pub.polis.feed")
}

// SetCursor stores the feed stream cursor position.
func (cm *CacheManager) SetCursor(cursor string) error {
	return cm.store.SetCursor("pub.polis.feed", cursor)
}

// LoadConfig loads the feed configuration, returning defaults if not found.
func (cm *CacheManager) LoadConfig() (*FeedConfig, error) {
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultFeedConfig()
			return &cfg, nil
		}
		return nil, fmt.Errorf("failed to read feed config: %w", err)
	}

	var cfg FeedConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse feed config: %w", err)
	}

	return &cfg, nil
}

// SaveConfig writes the feed configuration to disk.
func (cm *CacheManager) SaveConfig(cfg *FeedConfig) error {
	if err := os.MkdirAll(filepath.Dir(cm.configFile), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Ensure defaults
	if cfg.StalenessMinutes <= 0 {
		cfg.StalenessMinutes = 15
	}
	if cfg.MaxItems <= 0 {
		cfg.MaxItems = 500
	}
	if cfg.MaxAgeDays <= 0 {
		cfg.MaxAgeDays = 90
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal feed config: %w", err)
	}

	return os.WriteFile(cm.configFile, append(data, '\n'), 0644)
}

// IsStale returns true if the cache needs refreshing based on staleness_minutes.
func (cm *CacheManager) IsStale() (bool, error) {
	entry, err := cm.store.GetCursorEntry("pub.polis.feed")
	if err != nil {
		return true, nil
	}

	if entry.LastUpdated == "" {
		return true, nil
	}

	lastUpdated, err := time.Parse(time.RFC3339, entry.LastUpdated)
	if err != nil {
		// Try alternative format
		lastUpdated, err = time.Parse("2006-01-02T15:04:05Z", entry.LastUpdated)
		if err != nil {
			return true, nil
		}
	}

	cfg, _ := cm.LoadConfig()
	staleness := cfg.StalenessMinutes
	if staleness <= 0 {
		staleness = 15
	}

	return time.Since(lastUpdated) > time.Duration(staleness)*time.Minute, nil
}

// LastUpdated returns the timestamp of the last feed sync, or empty string if never synced.
func (cm *CacheManager) LastUpdated() string {
	entry, err := cm.store.GetCursorEntry("pub.polis.feed")
	if err != nil {
		return ""
	}
	return entry.LastUpdated
}

// MergeItems integrates new FeedItems into the cache. Returns the number of new items added.
func (cm *CacheManager) MergeItems(items []FeedItem) (int, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	existing, err := cm.listLocked()
	if err != nil {
		return 0, err
	}

	// Build ID map of existing items
	idMap := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		idMap[item.ID] = struct{}{}
	}

	// Add new items
	now := time.Now().UTC().Format(time.RFC3339)
	newCount := 0
	for _, item := range items {
		id := ComputeItemID(item.AuthorURL, item.URL)
		if _, exists := idMap[id]; exists {
			continue
		}
		// Normalize published timestamp to ISO 8601
		published := item.Published
		if t := ParsePublished(published); !t.IsZero() {
			published = t.UTC().Format(time.RFC3339)
		}
		existing = append(existing, CachedFeedItem{
			ID:           id,
			Type:         item.Type,
			Title:        item.Title,
			URL:          item.URL,
			Published:    published,
			Hash:         item.Hash,
			AuthorURL:    item.AuthorURL,
			AuthorDomain: item.AuthorDomain,
			TargetURL:    item.TargetURL,
			TargetDomain: item.TargetDomain,
			CachedAt:     now,
		})
		idMap[id] = struct{}{}
		newCount++
	}

	// Sort by published descending
	sort.Slice(existing, func(i, j int) bool {
		return PublishedBefore(existing[j].Published, existing[i].Published)
	})

	if err := cm.writeAll(existing); err != nil {
		return 0, err
	}

	// Prune after merge
	cm.pruneLocked()

	return newCount, nil
}

// MarkRead marks a single item as read.
func (cm *CacheManager) MarkRead(id string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	items, err := cm.listLocked()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	found := false
	for i := range items {
		if items[i].ID == id {
			items[i].ReadAt = now
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("item not found: %s", id)
	}

	return cm.writeAll(items)
}

// MarkUnread marks a single item as unread.
func (cm *CacheManager) MarkUnread(id string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	items, err := cm.listLocked()
	if err != nil {
		return err
	}

	found := false
	for i := range items {
		if items[i].ID == id {
			items[i].ReadAt = ""
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("item not found: %s", id)
	}

	return cm.writeAll(items)
}

// MarkAllRead marks all items as read.
func (cm *CacheManager) MarkAllRead() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	items, err := cm.listLocked()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for i := range items {
		if items[i].ReadAt == "" {
			items[i].ReadAt = now
		}
	}

	return cm.writeAll(items)
}

// MarkUnreadFrom marks the item with the given ID and all more recent items (by published date) as unread.
func (cm *CacheManager) MarkUnreadFrom(id string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	items, err := cm.listLocked()
	if err != nil {
		return err
	}

	// Find the target item's published date
	targetPublished := ""
	for _, item := range items {
		if item.ID == id {
			targetPublished = item.Published
			break
		}
	}

	if targetPublished == "" {
		return fmt.Errorf("item not found: %s", id)
	}

	// Mark the target and all items with same or newer published date as unread
	targetTime := ParsePublished(targetPublished)
	for i := range items {
		itemTime := ParsePublished(items[i].Published)
		if !itemTime.IsZero() && !targetTime.IsZero() && !itemTime.Before(targetTime) {
			items[i].ReadAt = ""
		} else if items[i].Published >= targetPublished {
			// Fallback for unparseable timestamps
			items[i].ReadAt = ""
		}
	}

	return cm.writeAll(items)
}

// SaveItems writes all items back to the cache file. Used to persist
// updates like excerpts without going through MergeItems.
func (cm *CacheManager) SaveItems(items []CachedFeedItem) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.writeAll(items)
}

// Prune enforces MaxItems and MaxAgeDays limits. Returns the number of items removed.
func (cm *CacheManager) Prune() (int, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.pruneLocked()
}

func (cm *CacheManager) pruneLocked() (int, error) {
	items, err := cm.listLocked()
	if err != nil {
		return 0, err
	}

	cfg, _ := cm.LoadConfig()

	maxAgeDays := cfg.MaxAgeDays
	if maxAgeDays <= 0 {
		maxAgeDays = 90
	}
	maxItems := cfg.MaxItems
	if maxItems <= 0 {
		maxItems = 500
	}

	originalLen := len(items)

	// Remove items older than MaxAgeDays
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays).UTC().Format(time.RFC3339)
	var remaining []CachedFeedItem
	for _, item := range items {
		if item.Published >= cutoff {
			remaining = append(remaining, item)
		}
	}

	// Enforce MaxItems (keep most recent)
	if len(remaining) > maxItems {
		remaining = remaining[:maxItems]
	}

	removed := originalLen - len(remaining)
	if removed > 0 {
		if err := cm.writeAll(remaining); err != nil {
			return 0, err
		}
	}

	return removed, nil
}

// SetStalenessMinutes updates the staleness threshold.
func (cm *CacheManager) SetStalenessMinutes(minutes int) error {
	if minutes < 1 {
		minutes = 1
	}

	cfg, err := cm.LoadConfig()
	if err != nil {
		return err
	}

	cfg.StalenessMinutes = minutes
	return cm.SaveConfig(cfg)
}

// writeAll rewrites all items to the cache file.
func (cm *CacheManager) writeAll(items []CachedFeedItem) error {
	if err := os.MkdirAll(filepath.Dir(cm.cacheFile), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.Create(cm.cacheFile)
	if err != nil {
		return fmt.Errorf("failed to create cache file: %w", err)
	}
	defer file.Close()

	for _, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			continue
		}
		file.WriteString(string(data) + "\n")
	}

	return nil
}
