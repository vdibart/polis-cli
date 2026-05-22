// Package notification manages the notification state layer.
//
// Notifications are stored as JSONL in .polis/ds/<domain>/<bundle>/state/pub.polis.notification.jsonl.
// Each line is a StateEntry with lifecycle fields (created_at, read_at).
// Rules and configuration live in config/notifications.json, managed by the stream package.
package notification

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
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
// writeAll rewrites the entire state file atomically. Marshals all entries
// in memory first; if any entry fails to marshal we surface the error rather
// than silently dropping it (the prior implementation discarded write/marshal
// errors and the deferred Close swallowed close errors too).
func (m *Manager) writeAll(entries []StateEntry) error {
	if err := os.MkdirAll(filepath.Dir(m.stateFile), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	var buf bytes.Buffer
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal entry %s: %w", e.ID, err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	return atomicfile.WriteFile(m.stateFile, buf.Bytes(), 0600)
}
