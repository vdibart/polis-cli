package patrol

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Snapshot stores baseline state for a tenant, used to detect changes between patrol sweeps.
type Snapshot struct {
	// Item 1: Salt integrity
	SaltHash  string    `json:"salt_hash,omitempty"`
	SaltMtime time.Time `json:"salt_mtime,omitempty"`

	// Item 3: File modification timestamps
	FileMtimes map[string]time.Time `json:"file_mtimes,omitempty"`

	// Item 5: Directory inventories
	KeyDirFiles    []string `json:"key_dir_files,omitempty"`
	PolicyDirFiles map[string][]string `json:"policy_dir_files,omitempty"`

	// Metadata
	GeneratedAt time.Time `json:"generated_at"`
}

// LoadSnapshot reads a snapshot from disk. Returns nil if file doesn't exist.
func LoadSnapshot(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse snapshot: %w", err)
	}
	return &snap, nil
}

// SaveSnapshot writes a snapshot to disk, creating parent directories as needed.
func SaveSnapshot(path string, snap *Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create snapshot dir: %w", err)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	return nil
}

// ComputeSaltHash returns the SHA-256 hex hash of the storage-salt file contents and its mtime.
// Returns empty strings if the salt file doesn't exist.
func ComputeSaltHash(siteDir string) (hash string, mtime time.Time, err error) {
	path := filepath.Join(siteDir, ".polis", "storage-salt")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("stat salt: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read salt: %w", err)
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h), info.ModTime(), nil
}

// trackedFiles are the files whose mtimes are tracked across sweeps.
var trackedFiles = []string{
	filepath.Join(".polis", "keys", "id_ed25519"),
	filepath.Join(".polis", "keys", "id_ed25519.pub"),
	filepath.Join(".well-known", "polis"),
	filepath.Join(".polis", "storage-salt"),
	filepath.Join("policies", "rules.jsonl"),
	filepath.Join(".polis", "policies", "rules.jsonl"),
}

// ComputeFileMtimes returns the modification times of tracked files.
// Missing files are silently omitted.
func ComputeFileMtimes(siteDir string) map[string]time.Time {
	mtimes := make(map[string]time.Time)
	for _, rel := range trackedFiles {
		info, err := os.Stat(filepath.Join(siteDir, rel))
		if err != nil {
			continue
		}
		mtimes[rel] = info.ModTime()
	}
	return mtimes
}

// ComputeKeyDirInventory returns a sorted list of files in .polis/keys/.
func ComputeKeyDirInventory(siteDir string) []string {
	return listDir(filepath.Join(siteDir, ".polis", "keys"))
}

// ComputePolicyDirInventory returns sorted file lists for both policy directories.
func ComputePolicyDirInventory(siteDir string) map[string][]string {
	result := make(map[string][]string)
	pubDir := filepath.Join(siteDir, "policies")
	privDir := filepath.Join(siteDir, ".polis", "policies")
	if files := listDir(pubDir); len(files) > 0 {
		result["policies"] = files
	}
	if files := listDir(privDir); len(files) > 0 {
		result[filepath.Join(".polis", "policies")] = files
	}
	return result
}

// listDir returns a sorted list of file names in a directory.
// Returns nil if the directory doesn't exist.
func listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}
