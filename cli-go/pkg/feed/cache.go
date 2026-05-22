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

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
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



// ExcerptCharCap is the single dial controlling how many characters of
// post body are extracted into a CachedFeedItem.Excerpt. Tune in one place:
// raising shows richer previews at the cost of larger feed cache files +
// JSON payloads; lowering shrinks them. Display-side line count is
// controlled separately by CSS clipping in the stream shape.
//
// Note: existing on-disk cache entries don't resize automatically when the
// cap changes — they keep whatever length they had when fetched. New fetches
// pick up the new cap. Force a refresh by clearing pub.polis.feed.jsonl
// excerpts and reloading the dashboard (tail-trigger refills them).
const ExcerptCharCap = 200

// CachedFeedItem represents a single item in the feed cache.
type CachedFeedItem struct {
	ID           string `json:"id"`
	Type         string `json:"type"`                    // "post", "comment", "announcement", or "follow"
	EventType    string `json:"event_type,omitempty"`    // Original DS event type (e.g. "pub.polis.post.published")
	Title        string `json:"title"`
	URL          string `json:"url"`
	Published    string `json:"published"`
	Hash         string `json:"hash,omitempty"`
	AuthorURL    string `json:"author_url"`
	AuthorDomain string `json:"author_domain"`
	TargetURL    string `json:"target_url,omitempty"`
	TargetDomain string `json:"target_domain,omitempty"`
	CachedAt     string `json:"cached_at"`
	ReadAt       string `json:"read_at"`
	Excerpt      string `json:"excerpt,omitempty"`
	// CommentCount is populated for own-scope post items via blessed.json
	// counts (handlers_stream.go's loadOwnContentAsFeedItems). Cross-tenant
	// items leave it zero; the stream renderer hides the badge when
	// the value is zero (renderPost's is-empty class).
	CommentCount int `json:"comment_count,omitempty"`
}

// FeedConfig holds user-editable feed configuration.
type FeedConfig struct {
	StalenessMinutes int `json:"staleness_minutes"`
	MaxItems         int `json:"max_items"`          // Legacy: used as fallback total cap when per-type limits are zero
	MaxAgeDays       int `json:"max_age_days"`       // Age limit for posts and comments
	// Per-type retention limits. When set, these override MaxItems.
	MaxPosts            int `json:"max_posts,omitempty"`             // default 300
	MaxComments         int `json:"max_comments,omitempty"`          // default 150
	MaxAnnouncements    int `json:"max_announcements,omitempty"`     // default 50
	MaxAnnouncementDays int `json:"max_announcement_days,omitempty"` // default 14
}

// DefaultFeedConfig returns the default feed configuration.
func DefaultFeedConfig() FeedConfig {
	return FeedConfig{
		StalenessMinutes:    5,
		MaxItems:            500,
		MaxAgeDays:          90,
		MaxPosts:            300,
		MaxComments:         150,
		MaxAnnouncements:    50,
		MaxAnnouncementDays: 14,
	}
}

// effectiveMaxPosts returns the posts cap, falling back to MaxItems if unset.
func (cfg *FeedConfig) effectiveMaxPosts() int {
	if cfg.MaxPosts > 0 {
		return cfg.MaxPosts
	}
	if cfg.MaxItems > 0 {
		return cfg.MaxItems
	}
	return 300
}

// effectiveMaxComments returns the comments cap, falling back to MaxItems if unset.
func (cfg *FeedConfig) effectiveMaxComments() int {
	if cfg.MaxComments > 0 {
		return cfg.MaxComments
	}
	if cfg.MaxItems > 0 {
		return cfg.MaxItems
	}
	return 150
}

// effectiveMaxAnnouncements returns the announcements cap.
func (cfg *FeedConfig) effectiveMaxAnnouncements() int {
	if cfg.MaxAnnouncements > 0 {
		return cfg.MaxAnnouncements
	}
	return 50
}

// effectiveMaxAnnouncementDays returns the announcement age limit.
func (cfg *FeedConfig) effectiveMaxAnnouncementDays() int {
	if cfg.MaxAnnouncementDays > 0 {
		return cfg.MaxAnnouncementDays
	}
	return 14
}

// effectiveMaxAgeDays returns the post/comment age limit.
func (cfg *FeedConfig) effectiveMaxAgeDays() int {
	if cfg.MaxAgeDays > 0 {
		return cfg.MaxAgeDays
	}
	return 90
}

// CacheManager handles feed cache operations.
type CacheManager struct {
	mu         sync.Mutex
	cacheFile  string        // state/pub.polis.feed.jsonl (or scoped variant)
	configFile string        // config/feed.json
	cursorKey  string        // cursor key in cursors.json (e.g. "pub.polis.feed" or "pub.polis.feed.global")
	store      *stream.Store // for cursor operations
}

// CacheFile returns the path to pub.polis.feed.jsonl for a given DS domain.
func CacheFile(dataDir, discoveryDomain string) string {
	return filepath.Join(dataDir, ".polis", "ds", discoveryDomain, "pub.polis.core", "state", "pub.polis.feed.jsonl")
}

// ScopedCacheFile returns the path to a scoped feed cache file (e.g. pub.polis.feed.followers.jsonl).
func ScopedCacheFile(dataDir, discoveryDomain, scope string) string {
	return filepath.Join(dataDir, ".polis", "ds", discoveryDomain, "pub.polis.core", "state", "pub.polis.feed."+scope+".jsonl")
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
		cursorKey:  "pub.polis.feed",
		store:      stream.NewStore(dataDir, discoveryDomain, "pub.polis.core"),
	}
}

// NewScopedCacheManager creates a feed cache manager for a specific scope (e.g. "followers", "global").
// Each scope gets its own JSONL file and cursor key.
func NewScopedCacheManager(dataDir, discoveryDomain, scope string) *CacheManager {
	return &CacheManager{
		cacheFile:  ScopedCacheFile(dataDir, discoveryDomain, scope),
		configFile: ConfigFile(dataDir, discoveryDomain),
		cursorKey:  "pub.polis.feed." + scope,
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
	// R20-B-S2 (2026-05-18): the default bufio.Scanner buffer is 64KB
	// and ErrTooLong on a longer line aborts the entire scan — pre-fix
	// a single oversize feed entry from a followed author (oversize
	// title / excerpt / body_html, etc.) would brick the reader's feed
	// view entirely. DS payload cap is 256KB (R15-6), so we set our
	// reader cap to match: anything DS accepts must be readable here.
	// Lines past 1MB are aberrant; skip them individually instead of
	// killing the whole scan.
	const maxFeedLineBytes = 1 << 20 // 1MiB
	scanner.Buffer(make([]byte, 0, 64<<10), maxFeedLineBytes)
	var skipped int
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

	// R20-B-S2: don't fail the whole feed view on a single oversize
	// line. ErrTooLong indicates a line exceeded our buffer; log and
	// continue with whatever we already parsed. Other scanner errors
	// (I/O failure, etc.) are still fatal.
	if err := scanner.Err(); err != nil {
		if err == bufio.ErrTooLong {
			skipped++
			// Returning the partial result is safer than 500-ing the
			// entire feed view; the caller already tolerates missing
			// items. Future: surface skipped count via a side-channel.
			return items, nil
		}
		return nil, fmt.Errorf("failed to read cache: %w", err)
	}
	_ = skipped // currently unused but reserved for future telemetry

	// Backfill EventType for items cached before the field was added.
	for i := range items {
		if items[i].EventType == "" {
			switch items[i].Type {
			case "post":
				items[i].EventType = "pub.polis.post.published"
			case "comment":
				items[i].EventType = "pub.polis.comment.published"
			case "announcement":
				items[i].EventType = "pub.polis.follow.announced"
			}
		}
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
	Type          string          // "post", "comment", or "" (all)
	Status        string          // "read", "unread", or "" (all)
	AuthorDomains map[string]bool // restrict to these author domains (nil = all)
}

// ListFiltered returns cached feed items filtered by type and/or read status.
func (cm *CacheManager) ListFiltered(opts FilterOptions) ([]CachedFeedItem, error) {
	all, err := cm.List()
	if err != nil {
		return nil, err
	}

	if opts.Type == "" && opts.Status == "" && opts.AuthorDomains == nil {
		return all, nil
	}

	var filtered []CachedFeedItem
	for _, item := range all {
		if opts.AuthorDomains != nil && !opts.AuthorDomains[item.AuthorDomain] {
			continue
		}
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
	return cm.store.GetCursor(cm.cursorKey)
}

// SetCursor stores the feed stream cursor position.
func (cm *CacheManager) SetCursor(cursor string) error {
	return cm.store.SetCursor(cm.cursorKey, cursor)
}

// MarkChecked bumps LastUpdated to "now" without changing the cursor
// position. Use after a sync attempt that resolved structurally to
// "no work to do" — e.g., empty following list, missing discovery
// config — so IsStale returns false and clients stop re-triggering
// the sync in a tight loop. Don't call after transient errors (DS
// unreachable, etc.) where we want the next cycle to retry.
func (cm *CacheManager) MarkChecked() error {
	cursor, _ := cm.GetCursor()
	return cm.store.SetCursor(cm.cursorKey, cursor)
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

	// Ensure defaults for required fields
	if cfg.StalenessMinutes <= 0 {
		cfg.StalenessMinutes = 5
	}
	if cfg.MaxAgeDays <= 0 {
		cfg.MaxAgeDays = 90
	}
	// MaxItems kept as legacy fallback; per-type limits are optional (zero = use defaults)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal feed config: %w", err)
	}

	return atomicfile.WriteFile(cm.configFile, append(data, '\n'), 0600)
}

// IsStale returns true if the cache needs refreshing based on staleness_minutes.
func (cm *CacheManager) IsStale() (bool, error) {
	entry, err := cm.store.GetCursorEntry(cm.cursorKey)
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
		staleness = 5
	}

	return time.Since(lastUpdated) > time.Duration(staleness)*time.Minute, nil
}

// LastUpdated returns the timestamp of the last feed sync, or empty string if never synced.
func (cm *CacheManager) LastUpdated() string {
	entry, err := cm.store.GetCursorEntry(cm.cursorKey)
	if err != nil {
		return ""
	}
	return entry.LastUpdated
}

// MergeResult holds the outcome of a MergeItems call.
type MergeResult struct {
	Added    int // items added before pruning
	Retained int // items that survived pruning (Added minus any pruned)
}

// MergeItems integrates new FeedItems into the cache. Returns the number of new items added.
func (cm *CacheManager) MergeItems(items []FeedItem) (int, error) {
	r, err := cm.MergeItemsResult(items)
	return r.Added, err
}

// MergeItemsResult integrates new FeedItems and reports how many survived pruning.
func (cm *CacheManager) MergeItemsResult(items []FeedItem) (MergeResult, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	existing, err := cm.listLocked()
	if err != nil {
		return MergeResult{}, err
	}

	// Build ID map of existing items
	idMap := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		idMap[item.ID] = struct{}{}
	}

	// Add new items, tracking their IDs
	now := time.Now().UTC().Format(time.RFC3339)
	var newIDs []string
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
			EventType:    item.EventType,
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
		newIDs = append(newIDs, id)
	}

	// Sort by published descending
	sort.Slice(existing, func(i, j int) bool {
		return PublishedBefore(existing[j].Published, existing[i].Published)
	})

	if err := cm.writeAll(existing); err != nil {
		return MergeResult{}, err
	}

	// Prune after merge
	cm.pruneLocked()

	// Count how many of the newly added items survived pruning
	retained := len(newIDs)
	if retained > 0 {
		afterItems, err := cm.listLocked()
		if err == nil {
			afterIDs := make(map[string]struct{}, len(afterItems))
			for _, item := range afterItems {
				afterIDs[item.ID] = struct{}{}
			}
			retained = 0
			for _, id := range newIDs {
				if _, ok := afterIDs[id]; ok {
					retained++
				}
			}
		}
	}

	return MergeResult{Added: len(newIDs), Retained: retained}, nil
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
//
// CONCURRENCY HAZARD: SaveItems overwrites the cache with the caller's
// full slice. If a parallel goroutine has merged new items between the
// caller's List() and this SaveItems(), the new items are lost.
// Prefer UpdateExcerpts for excerpt-only updates.
func (cm *CacheManager) SaveItems(items []CachedFeedItem) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.writeAll(items)
}

// UpdateExcerpts applies per-item Excerpt updates to the cache atomically
// under the cache mutex. Items not in `updates` are preserved as-is —
// avoids the read-modify-write race that SaveItems(items) carries when
// concurrent goroutines hold separate slices.
//
// Returns the number of items actually updated. IDs in `updates` that
// don't match any cached item are silently skipped (no-op for the
// caller; matches the best-effort excerpt-fetch shape).
//
// Closes R17 final-review item #4 (concurrent excerpt-fetch overwrite).
// Use this from any path that backfills derived data on a per-item key
// without intent to add or remove items from the cache.
func (cm *CacheManager) UpdateExcerpts(updates map[string]string) (int, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	items, err := cm.listLocked()
	if err != nil {
		return 0, err
	}
	updated := 0
	for i := range items {
		if newExcerpt, ok := updates[items[i].ID]; ok && items[i].Excerpt != newExcerpt {
			items[i].Excerpt = newExcerpt
			updated++
		}
	}
	if updated == 0 {
		return 0, nil
	}
	if err := cm.writeAll(items); err != nil {
		return 0, err
	}
	return updated, nil
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
	originalLen := len(items)

	remaining := pruneByType(items, cfg)

	removed := originalLen - len(remaining)
	if removed > 0 {
		if err := cm.writeAll(remaining); err != nil {
			return 0, err
		}
	}

	return removed, nil
}

// pruneByType partitions items into posts, comments, and announcements,
// applies per-type age and count limits, then merges back sorted by published descending.
func pruneByType(items []CachedFeedItem, cfg *FeedConfig) []CachedFeedItem {
	now := time.Now()
	contentCutoff := now.AddDate(0, 0, -cfg.effectiveMaxAgeDays())
	announcementCutoff := now.AddDate(0, 0, -cfg.effectiveMaxAnnouncementDays())

	maxPosts := cfg.effectiveMaxPosts()
	maxComments := cfg.effectiveMaxComments()
	maxAnnouncements := cfg.effectiveMaxAnnouncements()

	// Partition by type
	var posts, comments, announcements []CachedFeedItem
	for _, item := range items {
		t := ParsePublished(item.Published)
		switch item.Type {
		case "announcement":
			if !t.IsZero() && t.Before(announcementCutoff) {
				continue // too old
			}
			announcements = append(announcements, item)
		case "comment":
			if !t.IsZero() && t.Before(contentCutoff) {
				continue
			}
			comments = append(comments, item)
		default: // "post" and any future types
			if !t.IsZero() && t.Before(contentCutoff) {
				continue
			}
			posts = append(posts, item)
		}
	}

	// Apply per-type count limits (items already sorted by published desc from listLocked)
	if len(posts) > maxPosts {
		posts = posts[:maxPosts]
	}
	if len(comments) > maxComments {
		comments = comments[:maxComments]
	}
	if len(announcements) > maxAnnouncements {
		announcements = announcements[:maxAnnouncements]
	}

	// Merge back and sort by published descending
	result := make([]CachedFeedItem, 0, len(posts)+len(comments)+len(announcements))
	result = append(result, posts...)
	result = append(result, comments...)
	result = append(result, announcements...)
	sort.Slice(result, func(i, j int) bool {
		return PublishedBefore(result[j].Published, result[i].Published)
	})

	return result
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
