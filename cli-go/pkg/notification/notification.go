// Package notification manages the notification state layer.
//
// Notifications are stored as JSONL in .polis/ds/<domain>/<bundle>/state/pub.polis.notification.jsonl.
// Each line is a StateEntry with lifecycle fields (created_at, read_at).
// Rules and configuration live in config/notifications.json, managed by the stream package.
package notification

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	pathpkg "path"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
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

// StateDirKey is the state directory for a DS domain, relative to the site
// root. ⭐ A KEY, so the same address works on any backend.
func StateDirKey(discoveryDomain string) string {
	return ".polis/ds/" + discoveryDomain + "/pub.polis.core/state"
}

// StateFileKey is pub.polis.notification.jsonl for a DS domain, as a key.
func StateFileKey(discoveryDomain string) string {
	return StateDirKey(discoveryDomain) + "/pub.polis.notification.jsonl"
}

// Manager handles notification state operations.
type Manager struct {
	// ⭐ The struct was already the right shape; only the identity was missing.
	site     storage.Site
	stateKey string
}

// NewManager creates a notification manager for a specific discovery service domain.
func NewManager(site storage.Site, discoveryDomain string) *Manager {
	return &Manager{
		site:     site,
		stateKey: StateFileKey(discoveryDomain),
	}
}

// List returns all notification entries from state.jsonl.
func (m *Manager) List() ([]StateEntry, error) {
	// NOT a whole Get on a filesystem: this STREAMS THE BYTES with a
	// bufio.Scanner rather than reading the file whole, which is what matters
	// for a file that grows without bound. storage.OpenRead streams wherever
	// the store has a real path, and reads whole only where there is no file.
	//
	// ⚠️ A review called this a rationalisation because the PARSED entries are
	// accumulated into a slice. That is true and is a different question: the
	// parsed form is bounded by what the caller needs, while Get would hold the
	// raw bytes as well. Converting is possible and is not free.
	file, err := storage.OpenRead(m.site, m.stateKey)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
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
	// Stays: the write below goes through atomicfile, which owns atomicity outside
	// the interface and creates no parents.

	var buf bytes.Buffer
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal entry %s: %w", e.ID, err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	return atomicfile.Write(context.Background(), m.site, m.stateKey, buf.Bytes(), 0600)
}

// ClearAll empties every discovery domain's notification state file and returns
// the number of entries removed.
//
// ⚠️ This is a DELETE, not a rebuild. It lived under `polis rebuild
// --notifications` for historical reasons and reconstructs nothing — nothing
// anywhere can put those entries back. Signet epic 25 D5 moved the verb to
// `polis notifications clear` and left the old flag accepted as a deprecated
// alias.
//
// Mode stays 0600: this is `.polis/` state, not served content.
func ClearAll(site storage.Site) (int, error) {
	count := 0

	// ⭐ Through the store. Store.List reports a real failure and treats only
	// absence as empty, which is what the not-exist arm here returned anyway.
	ctx := context.Background()
	entries, err := site.Store().List(ctx, ".polis/ds")
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir {
			continue
		}
		stateKey := StateFileKey(pathpkg.Base(entry.Key))
		data, err := site.Store().Get(ctx, stateKey)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}
		if err := atomicfile.Write(ctx, site, stateKey, []byte{}, 0600); err != nil {
			return count, err
		}
	}

	return count, nil
}
