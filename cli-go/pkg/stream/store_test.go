package stream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewStore(t *testing.T) {
	s := NewStore("/tmp/testsite", "ds.polis.pub", "pub.polis.core")
	expectedState := filepath.Join("/tmp/testsite", ".polis", "ds", "ds.polis.pub", "pub.polis.core", "state")
	if s.StateDir() != expectedState {
		t.Errorf("StateDir() = %q, want %q", s.StateDir(), expectedState)
	}
	expectedConfig := filepath.Join("/tmp/testsite", ".polis", "ds", "ds.polis.pub", "pub.polis.core", "config")
	if s.ConfigDir() != expectedConfig {
		t.Errorf("ConfigDir() = %q, want %q", s.ConfigDir(), expectedConfig)
	}
	expectedBundle := filepath.Join("/tmp/testsite", ".polis", "ds", "ds.polis.pub", "pub.polis.core")
	if s.BundleDir() != expectedBundle {
		t.Errorf("BundleDir() = %q, want %q", s.BundleDir(), expectedBundle)
	}
	if s.BundleName() != "pub.polis.core" {
		t.Errorf("BundleName() = %q, want pub.polis.core", s.BundleName())
	}
}

func TestCursors(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "ds.polis.pub", "pub.polis.core")

	// Default cursor is "0"
	cursor, err := s.GetCursor("pub.polis.follow")
	if err != nil {
		t.Fatalf("GetCursor: %v", err)
	}
	if cursor != "0" {
		t.Errorf("default cursor = %q, want %q", cursor, "0")
	}

	// Set and get cursor
	if err := s.SetCursor("pub.polis.follow", "4521"); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	cursor, err = s.GetCursor("pub.polis.follow")
	if err != nil {
		t.Fatalf("GetCursor after set: %v", err)
	}
	if cursor != "4521" {
		t.Errorf("cursor = %q, want %q", cursor, "4521")
	}

	// Different projection has independent cursor
	cursor, err = s.GetCursor("pub.polis.post")
	if err != nil {
		t.Fatalf("GetCursor other: %v", err)
	}
	if cursor != "0" {
		t.Errorf("other cursor = %q, want %q", cursor, "0")
	}

	// Set second cursor
	if err := s.SetCursor("pub.polis.post", "4600"); err != nil {
		t.Fatalf("SetCursor post: %v", err)
	}

	// Verify both cursors persist independently
	cursor, _ = s.GetCursor("pub.polis.follow")
	if cursor != "4521" {
		t.Errorf("follow cursor = %q, want %q", cursor, "4521")
	}
	cursor, _ = s.GetCursor("pub.polis.post")
	if cursor != "4600" {
		t.Errorf("post cursor = %q, want %q", cursor, "4600")
	}

	// All cursors are in a single file
	data, err := os.ReadFile(filepath.Join(s.StateDir(), "cursors.json"))
	if err != nil {
		t.Fatalf("read cursors.json: %v", err)
	}
	var cf CursorsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		t.Fatalf("parse cursors.json: %v", err)
	}
	if len(cf.Cursors) != 2 {
		t.Errorf("expected 2 cursor entries, got %d", len(cf.Cursors))
	}
}

func TestCursorEntry(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "ds.polis.pub", "pub.polis.core")

	// Non-existent entry returns zero value
	entry, err := s.GetCursorEntry("pub.polis.feed")
	if err != nil {
		t.Fatalf("GetCursorEntry: %v", err)
	}
	if entry.Position != "" {
		t.Errorf("expected empty position, got %q", entry.Position)
	}

	// Set cursor and verify entry
	s.SetCursor("pub.polis.feed", "100")
	entry, err = s.GetCursorEntry("pub.polis.feed")
	if err != nil {
		t.Fatalf("GetCursorEntry after set: %v", err)
	}
	if entry.Position != "100" {
		t.Errorf("position = %q, want %q", entry.Position, "100")
	}
	if entry.LastUpdated == "" {
		t.Error("last_updated should be set")
	}
}

func TestState(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "ds.polis.pub", "pub.polis.core")

	// Loading non-existent state should not error
	var state FollowerState
	if err := s.LoadState("follow", &state); err != nil {
		t.Fatalf("LoadState non-existent: %v", err)
	}
	if state.Count != 0 {
		t.Errorf("empty state count = %d, want 0", state.Count)
	}

	// Save and load state
	saved := &FollowerState{
		Followers: []string{"alice.com", "bob.com"},
		Count:     2,
	}
	if err := s.SaveState("follow", saved); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	var loaded FollowerState
	if err := s.LoadState("follow", &loaded); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.Count != 2 {
		t.Errorf("loaded count = %d, want 2", loaded.Count)
	}

	// Verify state file exists at state/<name>.json
	stateFile := filepath.Join(s.StateDir(), "follow.json")
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		t.Errorf("state file %q does not exist", stateFile)
	}
}

func TestConfig(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "ds.polis.pub", "pub.polis.core")

	type TestConfig struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	// Loading non-existent config should not error
	var cfg TestConfig
	if err := s.LoadConfig("test", &cfg); err != nil {
		t.Fatalf("LoadConfig non-existent: %v", err)
	}

	// Save and load config
	saved := &TestConfig{Name: "test", Value: 42}
	if err := s.SaveConfig("test", saved); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	var loaded TestConfig
	if err := s.LoadConfig("test", &loaded); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Name != "test" {
		t.Errorf("loaded name = %q, want test", loaded.Name)
	}
	if loaded.Value != 42 {
		t.Errorf("loaded value = %d, want 42", loaded.Value)
	}
}

func TestStateAndCursorsIndependent(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "ds.polis.pub", "pub.polis.core")

	// Set cursor first
	s.SetCursor("pub.polis.follow", "100")

	// Save state — should NOT affect cursor
	saved := &FollowerState{
		Followers: []string{"alice.com"},
		Count:     1,
	}
	s.SaveState("follow", saved)

	// Verify cursor is still readable
	cursor, _ := s.GetCursor("pub.polis.follow")
	if cursor != "100" {
		t.Errorf("GetCursor after SaveState = %q, want %q", cursor, "100")
	}

	// Verify state is in its own file
	var loaded FollowerState
	s.LoadState("follow", &loaded)
	if loaded.Count != 1 {
		t.Errorf("loaded count = %d, want 1", loaded.Count)
	}
}

func TestDeprecatedCursorKeyCleanup(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "ds.polis.pub", "pub.polis.core")

	// Pre-populate cursors.json with deprecated keys alongside valid ones
	os.MkdirAll(s.StateDir(), 0755)
	cf := CursorsFile{
		Cursors: map[string]CursorEntry{
			"pub.polis.sync":           {Position: "500", LastUpdated: "2026-04-01T00:00:00Z"},
			"pub.polis.feed":           {Position: "500", LastUpdated: "2026-04-01T00:00:00Z"},
			"pub.polis.feed.followers":  {Position: "300", LastUpdated: "2026-03-01T00:00:00Z"},
			"pub.polis.feed.me":         {Position: "400", LastUpdated: "2026-03-15T00:00:00Z"},
			"pub.polis.feed.viewed_at":  {Position: "2026-04-01T12:00:00Z", LastUpdated: "2026-04-01T00:00:00Z"},
			"pub.polis.feed.global":     {Position: "450", LastUpdated: "2026-03-20T00:00:00Z"},
		},
	}
	data, _ := json.MarshalIndent(cf, "", "  ")
	os.WriteFile(filepath.Join(s.StateDir(), "cursors.json"), data, 0644)

	// Reading any cursor triggers loadCursors which cleans up deprecated keys
	cursor, err := s.GetCursor("pub.polis.sync")
	if err != nil {
		t.Fatalf("GetCursor: %v", err)
	}
	if cursor != "500" {
		t.Errorf("sync cursor = %q, want %q", cursor, "500")
	}

	// Verify deprecated keys are gone
	_, err = s.GetCursorEntry("pub.polis.feed.followers")
	if err != nil {
		t.Fatalf("GetCursorEntry: %v", err)
	}
	followersEntry, _ := s.GetCursorEntry("pub.polis.feed.followers")
	if followersEntry.Position != "" {
		t.Errorf("deprecated pub.polis.feed.followers should be removed, got position %q", followersEntry.Position)
	}

	meEntry, _ := s.GetCursorEntry("pub.polis.feed.me")
	if meEntry.Position != "" {
		t.Errorf("deprecated pub.polis.feed.me should be removed, got position %q", meEntry.Position)
	}

	viewedAtEntry, _ := s.GetCursorEntry("pub.polis.feed.viewed_at")
	if viewedAtEntry.Position != "" {
		t.Errorf("deprecated pub.polis.feed.viewed_at should be removed, got position %q", viewedAtEntry.Position)
	}

	// Verify non-deprecated keys are preserved
	globalEntry, _ := s.GetCursorEntry("pub.polis.feed.global")
	if globalEntry.Position != "450" {
		t.Errorf("pub.polis.feed.global should be preserved, got position %q", globalEntry.Position)
	}

	feedEntry, _ := s.GetCursorEntry("pub.polis.feed")
	if feedEntry.Position != "500" {
		t.Errorf("pub.polis.feed should be preserved, got position %q", feedEntry.Position)
	}

	// Verify the file on disk no longer has deprecated keys
	rawData, _ := os.ReadFile(filepath.Join(s.StateDir(), "cursors.json"))
	var persisted CursorsFile
	json.Unmarshal(rawData, &persisted)
	if _, ok := persisted.Cursors["pub.polis.feed.followers"]; ok {
		t.Error("pub.polis.feed.followers should be removed from cursors.json on disk")
	}
	if _, ok := persisted.Cursors["pub.polis.feed.me"]; ok {
		t.Error("pub.polis.feed.me should be removed from cursors.json on disk")
	}
	if _, ok := persisted.Cursors["pub.polis.feed.viewed_at"]; ok {
		t.Error("pub.polis.feed.viewed_at should be removed from cursors.json on disk")
	}
	if len(persisted.Cursors) != 3 {
		t.Errorf("expected 3 cursor entries on disk, got %d", len(persisted.Cursors))
	}
}
