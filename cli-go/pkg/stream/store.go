// Package stream manages per-projection cursors, state, and config on disk.
//
// Root: .polis/ds/<discovery-service-domain>/<bundle-name>/
// Directories:
//
//	config/    — user preferences (notification rules, feed settings); survives resets
//	state/     — computed/derived data (followers, blessings, cursors, notifications, feed cache)
package stream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store manages per-projection cursors, state, and config on disk.
// Each store is scoped to a specific discovery service domain and bundle.
type Store struct {
	mu         sync.Mutex
	configDir  string // .polis/ds/<domain>/<bundle>/config/
	stateDir   string // .polis/ds/<domain>/<bundle>/state/
	bundleDir  string // .polis/ds/<domain>/<bundle>/
	bundleName string // e.g. "pub.polis.core"
}

// NewStore creates a new Store rooted at dataDir/.polis/ds/discoveryDomain/bundleName/.
func NewStore(dataDir string, discoveryDomain string, bundleName string) *Store {
	bundleDir := filepath.Join(dataDir, ".polis", "ds", discoveryDomain, bundleName)
	return &Store{
		configDir:  filepath.Join(bundleDir, "config"),
		stateDir:   filepath.Join(bundleDir, "state"),
		bundleDir:  bundleDir,
		bundleName: bundleName,
	}
}

// BundleDir returns the bundle root directory (.polis/ds/<domain>/<bundle>/).
func (s *Store) BundleDir() string {
	return s.bundleDir
}

// StateDir returns the state directory (.polis/ds/<domain>/<bundle>/state/).
func (s *Store) StateDir() string {
	return s.stateDir
}

// ConfigDir returns the config directory (.polis/ds/<domain>/<bundle>/config/).
func (s *Store) ConfigDir() string {
	return s.configDir
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

func (s *Store) loadCursors() (*CursorsFile, error) {
	data, err := os.ReadFile(filepath.Join(s.stateDir, "cursors.json"))
	if err != nil {
		if os.IsNotExist(err) {
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
	if migrated {
		_ = s.saveCursorsLocked(&cf)
	}

	return &cf, nil
}

func (s *Store) saveCursors(cf *CursorsFile) error {
	if err := os.MkdirAll(s.stateDir, 0700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cursors: %w", err)
	}
	return os.WriteFile(filepath.Join(s.stateDir, "cursors.json"), data, 0644)
}

// saveCursorsLocked writes cursors without acquiring the mutex (for use within already-locked methods).
func (s *Store) saveCursorsLocked(cf *CursorsFile) error {
	if err := os.MkdirAll(s.stateDir, 0700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cursors: %w", err)
	}
	return os.WriteFile(filepath.Join(s.stateDir, "cursors.json"), data, 0644)
}

// --- State operations (state/<name>.json) ---

// stateFileMigrations maps old state file names to their new pub.polis.* equivalents.
var stateFileMigrations = map[string]string{
	"polis.follow":   "pub.polis.follow",
	"polis.blessing": "pub.polis.comment.blessing",
}

// LoadState loads state from state/<name>.json directly into target.
// On first load, it checks for old filenames and renames them.
func (s *Store) LoadState(name string, target interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stateFile := filepath.Join(s.stateDir, name+".json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			// Check if an old state file exists that should be migrated to this name
			for oldName, newName := range stateFileMigrations {
				if newName == name {
					oldFile := filepath.Join(s.stateDir, oldName+".json")
					if oldData, oldErr := os.ReadFile(oldFile); oldErr == nil {
						// Rename old file to new name
						if renameErr := os.Rename(oldFile, stateFile); renameErr == nil {
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

// SaveState writes state directly to state/<name>.json.
func (s *Store) SaveState(name string, state interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.stateDir, 0700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state %s: %w", name, err)
	}
	return os.WriteFile(filepath.Join(s.stateDir, name+".json"), data, 0644)
}

// --- Config operations (config/<name>.json) ---

// LoadConfig loads config from config/<name>.json into target.
func (s *Store) LoadConfig(name string, target interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(s.configDir, name+".json"))
	if err != nil {
		if os.IsNotExist(err) {
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
	if err := os.MkdirAll(s.configDir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config %s: %w", name, err)
	}
	return os.WriteFile(filepath.Join(s.configDir, name+".json"), data, 0644)
}
