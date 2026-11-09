// =============================================================================
// HANDBOOK TRAIL MARKER — DS-to-stream thread (cursor + state storage)
// =============================================================================
// Store is the on-disk persistence layer for the continuous sync path. It
// holds the per-projection cursors (cursors.json), the materialized state
// (followers, blessings, notifications, feed cache as JSONL), and the
// per-DS user config (notification rules, feed settings). state/ is safely
// deletable — re-syncing from the DS rebuilds it from cursor=0.
//
// Trail across files (DS-to-stream, continuous sync):
//   producer       — webapp/internal/server/sync.go: runUnifiedSync()
//   DS endpoint    — discovery-service/core/handlers/stream.ts
//   transformer    — cli-go/pkg/feed/handler.go (FeedHandler)
//   cursor + cache — this file (Store)
//
// Pull the thread:
//   github.com/vdibart/polis-cli/blob/main/docs/handbook/ds-to-stream.md  (tour)
//   github.com/vdibart/polis-cli/blob/main/AGENTS.md                      (map)
// =============================================================================

// Package stream manages per-projection cursors, state, and config on disk.
//
// Root: .polis/ds/<discovery-service-domain>/<bundle-name>/
// Directories:
//
//	config/    — user preferences (notification rules, feed settings); survives resets
//	state/     — computed/derived data (followers, blessings, cursors, notifications, feed cache)
package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Store manages per-projection cursors, state, and config on disk.
// Each store is scoped to a specific discovery service domain and bundle.
type Store struct {
	mu sync.Mutex

	// site is where these files live and what they live on.
	//
	// ⭐ THE STRUCT WAS ALREADY THE RIGHT SHAPE, carrying three derived
	// directories; only the identity's type was missing. Holding the handle is
	// what lets the writes below go through the store, so a remote instance's
	// cursors and config land in its backend rather than on a container disk
	// that the next cold start throws away.
	site storage.Site

	// ⚠️ The three KEYS, relative to the site root. Every read and write
	// below addresses its file by key under one of them.
	configKey  string // .polis/ds/<domain>/<bundle>/config/
	stateKey   string // .polis/ds/<domain>/<bundle>/state/
	bundleKey  string // .polis/ds/<domain>/<bundle>/
	bundleName string // e.g. "pub.polis.core"
}

// NewStore creates a new Store rooted at the site's .polis/ds/discoveryDomain/bundleName/.
func NewStore(site storage.Site, discoveryDomain string, bundleName string) *Store {
	bundleKey := ".polis/ds/" + discoveryDomain + "/" + bundleName
	return &Store{
		site:       site,
		configKey:  bundleKey + "/config",
		stateKey:   bundleKey + "/state",
		bundleKey:  bundleKey,
		bundleName: bundleName,
	}
}

// BundleName returns the bundle name this store is scoped to.
func (s *Store) BundleName() string {
	return s.bundleName
}

// --- Cursor operations (consolidated in state/cursors.json) ---

// CursorEntry holds a single projection's cursor position and last update time.
type CursorEntry struct {
	Position    string `json:"position"`
	LastUpdated string `json:"last_updated"`
}

// CursorsFile is the on-disk format for state/cursors.json.
type CursorsFile struct {
	Cursors map[string]CursorEntry `json:"cursors"`
}

// GetCursor returns the cursor position for a projection, or "0" if not set.
func (s *Store) GetCursor(projection string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cf, err := s.loadCursors()
	if err != nil {
		return "0", nil
	}
	entry, ok := cf.Cursors[projection]
	if !ok || entry.Position == "" {
		return "0", nil
	}
	return entry.Position, nil
}

// SetCursor stores the cursor position for a projection.
func (s *Store) SetCursor(projection string, cursor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cf, _ := s.loadCursors()
	if cf == nil {
		cf = &CursorsFile{Cursors: make(map[string]CursorEntry)}
	}
	cf.Cursors[projection] = CursorEntry{
		Position:    cursor,
		LastUpdated: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	return s.saveCursors(cf)
}

// GetCursorEntry returns the full cursor entry for a projection, or a zero entry if not set.
func (s *Store) GetCursorEntry(projection string) (CursorEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cf, err := s.loadCursors()
	if err != nil {
		return CursorEntry{}, nil
	}
	entry, ok := cf.Cursors[projection]
	if !ok {
		return CursorEntry{}, nil
	}
	return entry, nil
}

// cursorKeyMigrations maps old cursor keys to their new pub.polis.* equivalents.
var cursorKeyMigrations = map[string]string{
	"polis.sync":         "pub.polis.sync",
	"polis.notification": "pub.polis.notification",
	"polis.follow":       "pub.polis.follow",
	"polis.feed":         "pub.polis.feed",
	"polis.blessing":     "pub.polis.comment.blessing",
}

// deprecatedCursorKeys are cursor keys that should be removed on load.
var deprecatedCursorKeys = []string{
	"pub.polis.feed.followers", // replaced by runtime filtering over network cache
	"pub.polis.feed.me",        // replaced by runtime filtering over network cache
	"pub.polis.feed.viewed_at", // replaced by position-based pub.polis.feed.viewed
}

func (s *Store) loadCursors() (*CursorsFile, error) {
	data, err := s.site.Store().Get(context.Background(), s.stateKey+"/cursors.json")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &CursorsFile{Cursors: make(map[string]CursorEntry)}, nil
		}
		return nil, fmt.Errorf("read cursors: %w", err)
	}
	var cf CursorsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse cursors: %w", err)
	}
	if cf.Cursors == nil {
		cf.Cursors = make(map[string]CursorEntry)
	}

	// Migrate old polis.* cursor keys to pub.polis.* equivalents
	migrated := false
	for oldKey, newKey := range cursorKeyMigrations {
		if entry, ok := cf.Cursors[oldKey]; ok {
			if _, exists := cf.Cursors[newKey]; !exists {
				cf.Cursors[newKey] = entry
			}
			delete(cf.Cursors, oldKey)
			migrated = true
		}
	}
	// Remove deprecated scoped feed cursor keys (collapsed to runtime filters)
	for _, key := range deprecatedCursorKeys {
		if _, ok := cf.Cursors[key]; ok {
			delete(cf.Cursors, key)
			migrated = true
		}
	}

	if migrated {
		// Not fatal: the migration is re-applied on the next load. But say so,
		// or a store that can never be written looks like one that migrated.
		if err := s.saveCursorsLocked(&cf); err != nil {
			log.Printf("[!] stream: could not save migrated cursors: %v", err)
		}
	}

	return &cf, nil
}

// ⭐ None of the writes below creates its directory: atomicfile.Write has the
// site's store create the parent, under the store's own mode rule, which makes
// everything at or under .polis exactly 0700 whatever the umask. That rule is
// what keeps the stream state private, and TestStoreDirectoriesArePrivate pins
// it.
func (s *Store) saveCursors(cf *CursorsFile) error {
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cursors: %w", err)
	}
	return atomicfile.Write(context.Background(), s.site, s.stateKey+"/cursors.json", data, 0600)
}

// saveCursorsLocked writes cursors without acquiring the mutex (for use within already-locked methods).
func (s *Store) saveCursorsLocked(cf *CursorsFile) error {
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cursors: %w", err)
	}
	return atomicfile.Write(context.Background(), s.site, s.stateKey+"/cursors.json", data, 0600)
}

// --- State operations (state/<name>.json) ---

// stateFileMigrations maps old state file names to their new pub.polis.* equivalents.
var stateFileMigrations = map[string]string{
	"polis.follow":   "pub.polis.follow",
	"polis.blessing": "pub.polis.comment.blessing",
}

// StateKey returns the site-relative key of the state file for the given name
// (without extension). Used by callers that stat the file for mtime-based
// invalidation without reading its contents.
func (s *Store) StateKey(name string) string {
	return s.stateKey + "/" + name + ".json"
}

// LoadState loads state from state/<name>.json directly into target.
// On first load, it checks for old filenames and renames them.
func (s *Store) LoadState(name string, target interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	st := s.site.Store()
	stateKey := s.StateKey(name)
	data, err := st.Get(ctx, stateKey)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Check if an old state file exists that should be migrated to this name
			for oldName, newName := range stateFileMigrations {
				if newName == name {
					oldKey := s.StateKey(oldName)
					if oldData, oldErr := st.Get(ctx, oldKey); oldErr == nil {
						if s.migrateStateFile(ctx, oldKey, stateKey, oldData) {
							data = oldData
							err = nil
						}
						break
					}
				}
			}
			if err != nil {
				return nil // no state yet
			}
		} else {
			return fmt.Errorf("read state %s: %w", name, err)
		}
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse state %s: %w", name, err)
	}
	return nil
}

// migrateStateFile moves a state file from its legacy name to its current one
// and reports whether the move happened. A false leaves the caller with no
// state, exactly as a failed rename always did.
func (s *Store) migrateStateFile(ctx context.Context, oldKey, newKey string, data []byte) bool {
	st := s.site.Store()
	if st.Capabilities().RealPath {
		oldPath, oldErr := st.RealPath(oldKey)
		newPath, newErr := st.RealPath(newKey)
		if oldErr != nil || newErr != nil {
			return false
		}
		// NOT the storage interface: a one-time layout MIGRATION by rename.
		// The interface has no rename, and on a filesystem a copy-then-delete
		// would not be atomic.
		return os.Rename(oldPath, newPath) == nil
	}
	// ⭐ No filesystem, so no rename. Write the new name, then remove the old
	// one. The state under .polis/ds is rebuilt from the stream if lost, and a
	// failed delete leaves a legacy file that is never read again, because the
	// new name is found first from then on.
	if err := st.Put(ctx, newKey, data, storage.WithMode(0o600)); err != nil {
		return false
	}
	if err := st.Delete(ctx, oldKey); err != nil {
		log.Printf("[!] stream: migrated %s but could not remove it: %v", oldKey, err)
	}
	return true
}

// SaveState writes state directly to state/<name>.json.
func (s *Store) SaveState(name string, state interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state %s: %w", name, err)
	}
	return atomicfile.Write(context.Background(), s.site, s.StateKey(name), data, 0600)
}

// --- Config operations (config/<name>.json) ---

// LoadConfig loads config from config/<name>.json into target.
func (s *Store) LoadConfig(name string, target interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.site.Store().Get(context.Background(), s.configKey+"/"+name+".json")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // no config yet
		}
		return fmt.Errorf("read config %s: %w", name, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse config %s: %w", name, err)
	}
	return nil
}

// SaveConfig writes config to config/<name>.json.
func (s *Store) SaveConfig(name string, config interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config %s: %w", name, err)
	}
	return atomicfile.Write(context.Background(), s.site, s.configKey+"/"+name+".json", data, 0600)
}
