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
