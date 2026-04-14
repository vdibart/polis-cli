// Package notification manages the notification state layer.
//
// Notifications are stored as JSONL in .polis/ds/<domain>/<bundle>/state/pub.polis.notification.jsonl.
// Each line is a StateEntry with lifecycle fields (created_at, read_at).
// Rules and configuration live in config/notifications.json, managed by the stream package.
package notification

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateEntry represents a single notification in state.jsonl.
type StateEntry struct {
	ID        string                 `json:"id"`
	RuleID    string                 `json:"rule_id"`
	Actor     string                 `json:"actor"`
	Icon      string                 `json:"icon"`
	Message   string                 `json:"message"`
	Link      string                 `json:"link,omitempty"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
	EventIDs  []int                  `json:"event_ids"`
	CreatedAt string                 `json:"created_at"`
	ReadAt    string                 `json:"read_at,omitempty"`
}

// StateDir returns the state directory for a given DS domain (bundle-scoped).
func StateDir(dataDir, discoveryDomain string) string {
	return filepath.Join(dataDir, ".polis", "ds", discoveryDomain, "pub.polis.core", "state")
}

// StateFile returns the path to pub.polis.notification.jsonl for a given DS domain.
func StateFile(dataDir, discoveryDomain string) string {
	return filepath.Join(StateDir(dataDir, discoveryDomain), "pub.polis.notification.jsonl")
}

// Manager handles notification state operations.
type Manager struct {
	stateFile string
}

// NewManager creates a notification manager for a specific discovery service domain.
func NewManager(dataDir, discoveryDomain string) *Manager {
	return &Manager{
		stateFile: StateFile(dataDir, discoveryDomain),
	}
}

// List returns all notification entries from state.jsonl.
func (m *Manager) List() ([]StateEntry, error) {
	file, err := os.Open(m.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return []StateEntry{}, nil
		}
		return nil, fmt.Errorf("failed to open state file: %w", err)
	}
	defer file.Close()

	var entries []StateEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e StateEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // Skip malformed lines
		}
		entries = append(entries, e)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	return entries, nil
}

// Append adds new entries to state.jsonl, skipping duplicates by ID.
// Returns the number of entries actually written.
func (m *Manager) Append(entries []StateEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	// Load existing IDs for dedup
	existing, err := m.List()
	if err != nil {
		return 0, err
	}
	existingIDs := make(map[string]bool, len(existing))
	for _, e := range existing {
		existingIDs[e.ID] = true
	}

	// Filter out duplicates
	var toWrite []StateEntry
	for _, e := range entries {
		if !existingIDs[e.ID] {
			toWrite = append(toWrite, e)
			existingIDs[e.ID] = true // Prevent duplicates within the batch
		}
	}

	if len(toWrite) == 0 {
		return 0, nil
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(m.stateFile), 0700); err != nil {
		return 0, fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.OpenFile(m.stateFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to open state file: %w", err)
	}
	defer file.Close()

	for _, e := range toWrite {
		data, err := json.Marshal(e)
		if err != nil {
			continue
		}
		if _, err := file.WriteString(string(data) + "\n"); err != nil {
			return 0, fmt.Errorf("failed to write entry: %w", err)
		}
	}

	return len(toWrite), nil
}

// CountUnread returns the number of unread notifications.
func (m *Manager) CountUnread() (int, error) {
	entries, err := m.List()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.ReadAt == "" {
			count++
		}
	}
	return count, nil
}

// MarkRead sets read_at on matching entry IDs.
// If markAll is true, all unread entries are marked as read.
// Returns the number of entries marked.
func (m *Manager) MarkRead(ids []string, markAll bool) (int, error) {
	entries, err := m.List()
	if err != nil {
		return 0, err
	}

	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	marked := 0
	for i := range entries {
		if entries[i].ReadAt != "" {
			continue
		}
		if markAll || idSet[entries[i].ID] {
			entries[i].ReadAt = now
			marked++
		}
	}

	if marked > 0 {
		if err := m.writeAll(entries); err != nil {
			return 0, err
		}
	}
	return marked, nil
}

// ListPaginated returns a page of entries sorted newest-first.
// If includeRead is false, only unread entries are returned.
func (m *Manager) ListPaginated(offset, limit int, includeRead bool) ([]StateEntry, int, error) {
	all, err := m.List()
	if err != nil {
		return nil, 0, err
	}

	// Filter
	var filtered []StateEntry
	for _, e := range all {
		if includeRead || e.ReadAt == "" {
			filtered = append(filtered, e)
		}
	}

	// Sort newest-first (JSONL appends newest last, so reverse)
	for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}

	total := len(filtered)

	// Paginate
	if offset >= len(filtered) {
		return []StateEntry{}, total, nil
	}
	filtered = filtered[offset:]
	if limit > 0 && limit < len(filtered) {
		filtered = filtered[:limit]
	}

	return filtered, total, nil
}

// PruneConfig controls how aggressively to prune old notifications.
type PruneConfig struct {
	MaxItems   int // Maximum entries to keep (0 = unlimited)
	MaxAgeDays int // Maximum age in days (0 = unlimited)
}

// DefaultPruneConfig returns sensible defaults for notification pruning.
func DefaultPruneConfig() PruneConfig {
	return PruneConfig{
		MaxItems:   500,
		MaxAgeDays: 90,
	}
}

// Prune removes old notifications that exceed the configured limits.
// Returns the number of entries removed.
func (m *Manager) Prune(cfg PruneConfig) (int, error) {
	entries, err := m.List()
	if err != nil {
		return 0, err
	}

	if len(entries) == 0 {
		return 0, nil
	}

	original := len(entries)

	// Remove entries older than MaxAgeDays
	if cfg.MaxAgeDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -cfg.MaxAgeDays)
		var kept []StateEntry
		for _, e := range entries {
			t, err := time.Parse("2006-01-02T15:04:05Z", e.CreatedAt)
			if err != nil {
				kept = append(kept, e) // Keep entries with unparseable timestamps
				continue
			}
			if !t.Before(cutoff) {
				kept = append(kept, e)
			}
		}
		entries = kept
	}

	// Trim to MaxItems (keep newest — JSONL appends chronologically)
	if cfg.MaxItems > 0 && len(entries) > cfg.MaxItems {
		entries = entries[len(entries)-cfg.MaxItems:]
	}

	removed := original - len(entries)
	if removed > 0 {
		if err := m.writeAll(entries); err != nil {
			return 0, err
		}
	}

	return removed, nil
}

// writeAll rewrites the entire state file.
func (m *Manager) writeAll(entries []StateEntry) error {
	if err := os.MkdirAll(filepath.Dir(m.stateFile), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.Create(m.stateFile)
	if err != nil {
		return fmt.Errorf("failed to create state file: %w", err)
	}
	defer file.Close()

	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			continue
		}
		file.WriteString(string(data) + "\n")
	}

	return nil
}
