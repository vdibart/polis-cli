package patrol

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeSaltHash(t *testing.T) {
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700)

	// No salt file — should return empty
	hash, _, err := ComputeSaltHash(siteDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "" {
		t.Errorf("expected empty hash for missing salt, got %q", hash)
	}

	// Create salt file
	saltPath := filepath.Join(siteDir, ".polis", "storage-salt")
	saltContent := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	os.WriteFile(saltPath, []byte(saltContent), 0600)

	hash, mtime, err := ComputeSaltHash(siteDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Error("expected non-empty hash")
	}
	if mtime.IsZero() {
		t.Error("expected non-zero mtime")
	}

	// Same content should produce same hash
	hash2, _, _ := ComputeSaltHash(siteDir)
	if hash != hash2 {
		t.Errorf("hash should be deterministic: %q != %q", hash, hash2)
	}
}

func TestLoadSaveSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handle", "snapshot.json")

	// Load non-existent — should return nil, nil
	snap, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap != nil {
		t.Error("expected nil for non-existent snapshot")
	}

	// Save and reload
	now := time.Now().UTC().Truncate(time.Second)
	original := &Snapshot{
		SaltHash:    "abc123",
		SaltMtime:   now,
		FileMtimes:  map[string]time.Time{".polis/keys/id_ed25519": now},
		KeyDirFiles: []string{"id_ed25519", "id_ed25519.pub"},
		PolicyDirFiles: map[string][]string{
			"policies": {"rules.jsonl"},
		},
		GeneratedAt: now,
	}
	if err := SaveSnapshot(path, original); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	loaded, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if loaded.SaltHash != original.SaltHash {
		t.Errorf("SaltHash: got %q, want %q", loaded.SaltHash, original.SaltHash)
	}
	if len(loaded.KeyDirFiles) != 2 {
		t.Errorf("KeyDirFiles: got %d, want 2", len(loaded.KeyDirFiles))
	}
	if len(loaded.FileMtimes) != 1 {
		t.Errorf("FileMtimes: got %d, want 1", len(loaded.FileMtimes))
	}
}

func TestSnapshotDir_CreatedAutomatically(t *testing.T) {
	dir := t.TempDir()
	// Nested path that doesn't exist yet
	path := filepath.Join(dir, "patrol", "myhandle", "snapshot.json")

	snap := &Snapshot{GeneratedAt: time.Now().UTC()}
	if err := SaveSnapshot(path, snap); err != nil {
		t.Fatalf("SaveSnapshot should create dirs: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("snapshot file should exist after save")
	}
}

func TestComputeFileMtimes(t *testing.T) {
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".polis", "keys"), 0700)
	os.MkdirAll(filepath.Join(siteDir, ".well-known"), 0755)

	// Create some tracked files
	os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), []byte("key"), 0600)
	os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), []byte("{}"), 0644)

	mtimes := ComputeFileMtimes(siteDir)

	if _, ok := mtimes[filepath.Join(".polis", "keys", "id_ed25519")]; !ok {
		t.Error("should include private key mtime")
	}
	if _, ok := mtimes[filepath.Join(".well-known", "polis")]; !ok {
		t.Error("should include .well-known mtime")
	}
	// Missing files should be omitted
	if _, ok := mtimes[filepath.Join(".polis", "storage-salt")]; ok {
		t.Error("should not include non-existent files")
	}
}

func TestComputeKeyDirInventory(t *testing.T) {
	siteDir := t.TempDir()
	keysDir := filepath.Join(siteDir, ".polis", "keys")
	os.MkdirAll(keysDir, 0700)

	os.WriteFile(filepath.Join(keysDir, "id_ed25519"), []byte("priv"), 0600)
	os.WriteFile(filepath.Join(keysDir, "id_ed25519.pub"), []byte("pub"), 0644)

	files := ComputeKeyDirInventory(siteDir)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	// Should be sorted
	if files[0] != "id_ed25519" || files[1] != "id_ed25519.pub" {
		t.Errorf("unexpected files: %v", files)
	}
}

func TestComputeKeyDirInventory_Missing(t *testing.T) {
	siteDir := t.TempDir()
	files := ComputeKeyDirInventory(siteDir)
	if files != nil {
		t.Errorf("expected nil for missing dir, got %v", files)
	}
}

func TestComputePolicyDirInventory(t *testing.T) {
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"), []byte("{}"), 0644)

	result := ComputePolicyDirInventory(siteDir)
	if files, ok := result["policies"]; !ok || len(files) != 1 {
		t.Errorf("expected 1 file in policies, got %v", result)
	}
}
