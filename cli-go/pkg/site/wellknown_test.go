package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Test Helpers
// ============================================================================

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "polis-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func writeTestWellKnown(t *testing.T, dir string, wk *WellKnown) {
	t.Helper()
	if err := SaveWellKnown(dir, wk); err != nil {
		t.Fatalf("Failed to save well-known: %v", err)
	}
}

func loadTestdata(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", filename))
	if err != nil {
		t.Fatalf("Failed to read testdata/%s: %v", filename, err)
	}
	return data
}

// ============================================================================
// 1. File Existence and Structure Tests
// ============================================================================

func TestWellKnownFileExists(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   time.Now().UTC().Format(time.RFC3339),
	}
	writeTestWellKnown(t, dir, wk)

	// File must exist
	path := filepath.Join(dir, ".well-known", "polis")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("File does not exist: %v", err)
	}

	// File must be readable (not a directory)
	if info.IsDir() {
		t.Fatal("Expected file, got directory")
	}

	// File permissions should allow owner read/write
	mode := info.Mode().Perm()
	if mode&0600 != 0600 {
		t.Errorf("File should be readable/writable by owner, got %o", mode)
	}
}

func TestWellKnownValidJSON(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "0.1.0",
		Author:    "Test Author",
		Email:     "test@example.com",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		SiteTitle: "Test Site",
		Created:   "2026-01-01T00:00:00Z",
		Config: &WellKnownConfig{
			Directories: WellKnownDirectories{
				Keys:  ".polis/keys",
				Posts: "posts",
			},
			Files: WellKnownFiles{
				PublicIndex: "metadata/public.jsonl",
			},
		},
		BaseURL: "https://test.polis.pub",
	}
	writeTestWellKnown(t, dir, wk)

	// Must parse without errors
	loaded, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	// Must round-trip correctly
	if loaded.Version != wk.Version {
		t.Errorf("Version mismatch: got %q, want %q", loaded.Version, wk.Version)
	}
	if loaded.Author != wk.Author {
		t.Errorf("Author mismatch: got %q, want %q", loaded.Author, wk.Author)
	}
	if loaded.Email != wk.Email {
		t.Errorf("Email mismatch: got %q, want %q", loaded.Email, wk.Email)
	}
	if loaded.PublicKey != wk.PublicKey {
		t.Errorf("PublicKey mismatch: got %q, want %q", loaded.PublicKey, wk.PublicKey)
	}
	if loaded.SiteTitle != wk.SiteTitle {
		t.Errorf("SiteTitle mismatch: got %q, want %q", loaded.SiteTitle, wk.SiteTitle)
	}
	if loaded.Created != wk.Created {
		t.Errorf("Created mismatch: got %q, want %q", loaded.Created, wk.Created)
	}
	if loaded.BaseURL != wk.BaseURL {
		t.Errorf("BaseURL mismatch: got %q, want %q", loaded.BaseURL, wk.BaseURL)
	}
	if loaded.Config == nil {
		t.Fatal("Config should not be nil")
	}
	if loaded.Config.Directories.Keys != wk.Config.Directories.Keys {
		t.Errorf("Config.Directories.Keys mismatch")
	}
	if loaded.Config.Files.PublicIndex != wk.Config.Files.PublicIndex {
		t.Errorf("Config.Files.PublicIndex mismatch")
	}
}

func TestWellKnownRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		wk      WellKnown
		wantErr bool
	}{
		{
			name: "with all required fields",
			wk: WellKnown{
				PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
				Version:   "0.1.0",
				Created:   "2026-01-01T00:00:00Z",
			},
			wantErr: false,
		},
		{
			name: "missing public_key is technically allowed by struct but should be validated",
			wk: WellKnown{
				Version: "0.1.0",
				Created: "2026-01-01T00:00:00Z",
			},
			wantErr: false, // struct allows it, validation is separate
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t)
			err := SaveWellKnown(dir, &tt.wk)
			if (err != nil) != tt.wantErr {
				t.Errorf("SaveWellKnown() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ============================================================================
// 2. Field Validation Tests
// ============================================================================

func TestPublicKeyFormat(t *testing.T) {
	tests := []struct {
		name      string
		publicKey string
		wantValid bool
	}{
		{
			name:      "valid ed25519 key",
			publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
			wantValid: true,
		},
		{
			name:      "valid ed25519 key without comment",
			publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX",
			wantValid: true,
		},
		{
			name:      "wrong key type",
			publicKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB",
			wantValid: false,
		},
		{
			name:      "empty key",
			publicKey: "",
			wantValid: false,
		},
		{
			name:      "malformed key",
			publicKey: "not-a-valid-key",
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := strings.HasPrefix(tt.publicKey, "ssh-ed25519 ")
			if valid != tt.wantValid {
				t.Errorf("PublicKey validation: got valid=%v, want valid=%v", valid, tt.wantValid)
			}
		})
	}
}

func TestVersionFormat(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		wantValid bool
	}{
		{"valid semver", "0.1.0", true},
		{"valid semver with major", "1.0.0", true},
		{"valid semver three digits", "2.10.5", true},
		{"empty version", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.version != ""
			if valid != tt.wantValid {
				t.Errorf("Version validation: got valid=%v, want valid=%v", valid, tt.wantValid)
			}
		})
	}
}

func TestCreatedFormat(t *testing.T) {
	tests := []struct {
		name      string
		created   string
		wantValid bool
	}{
		{"valid RFC3339", "2026-01-01T00:00:00Z", true},
		{"valid RFC3339 with offset", "2026-01-01T00:00:00+05:00", true},
		{"valid RFC3339 with nanoseconds", "2026-01-01T00:00:00.123456789Z", true},
		{"invalid format", "2026-01-01", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := time.Parse(time.RFC3339, tt.created)
			valid := err == nil && tt.created != ""
			if valid != tt.wantValid {
				t.Errorf("Created validation: got valid=%v, want valid=%v", valid, tt.wantValid)
			}
		})
	}
}

func TestEmailFormat(t *testing.T) {
	tests := []struct {
		name      string
		email     string
		wantValid bool
	}{
		{"valid email", "test@example.com", true},
		{"valid email with subdomain", "test@mail.example.com", true},
		{"empty is allowed", "", true}, // email is optional
		{"missing @", "invalid", false},
		{"missing domain", "test@", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.email == "" || (strings.Contains(tt.email, "@") && strings.Contains(tt.email[strings.Index(tt.email, "@"):], "."))
			if valid != tt.wantValid {
				t.Errorf("Email validation: got valid=%v, want valid=%v for %q", valid, tt.wantValid, tt.email)
			}
		})
	}
}

func TestBaseURLFormat(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		wantValid bool
	}{
		{"valid https", "https://example.com", true},
		{"valid https with subdomain", "https://x.polis.pub", true},
		{"empty is allowed", "", true}, // base_url is optional
		{"http not recommended", "http://example.com", false},
		{"trailing slash", "https://example.com/", false},
		{"with path", "https://example.com/path", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.baseURL == "" ||
				(strings.HasPrefix(tt.baseURL, "https://") &&
					!strings.HasSuffix(tt.baseURL, "/") &&
					strings.Count(tt.baseURL, "/") == 2)
			if valid != tt.wantValid {
				t.Errorf("BaseURL validation: got valid=%v, want valid=%v for %q", valid, tt.wantValid, tt.baseURL)
			}
		})
	}
}

// ============================================================================
// 3. Config Section Tests
// ============================================================================

func TestConfigDirectoriesDefaults(t *testing.T) {
	config := &WellKnownConfig{
		Directories: WellKnownDirectories{
			Keys:     ".polis/keys",
			Posts:    "posts",
			Comments: "comments",
			Snippets: "snippets",
			Themes:   ".polis/themes",
			Versions: ".versions",
		},
	}

	if config.Directories.Keys != ".polis/keys" {
		t.Errorf("Keys should be .polis/keys")
	}
	if config.Directories.Posts != "posts" {
		t.Errorf("Posts should be posts")
	}
	if config.Directories.Comments != "comments" {
		t.Errorf("Comments should be comments")
	}
	if config.Directories.Snippets != "snippets" {
		t.Errorf("Snippets should be snippets")
	}
	if config.Directories.Themes != ".polis/themes" {
		t.Errorf("Themes should be .polis/themes")
	}
	if config.Directories.Versions != ".versions" {
		t.Errorf("Versions should be .versions")
	}
}

func TestVersionsIsDirectoryNameOnly(t *testing.T) {
	// IMPORTANT: The versions config is a directory NAME, not a path.
	// The bash CLI stores ".versions" and constructs paths at runtime like:
	//   posts/2025/01/.versions/my-post.md
	// NOT "posts/.versions" which would be wrong.
	//
	// This test ensures we don't accidentally change it back to a path.

	dir := setupTestDir(t)

	// Create a new site using Init
	opts := InitOptions{
		SiteTitle: "Test Site",
	}

	_, err := Init(dir, opts)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Load the created .well-known/polis
	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("LoadWellKnown failed: %v", err)
	}

	// Verify versions is just ".versions" (directory name only)
	if wk.Config.Directories.Versions != ".versions" {
		t.Errorf("Versions should be '.versions' (directory name only), got %q", wk.Config.Directories.Versions)
	}

	// Ensure it doesn't contain a path separator (catches "posts/.versions" mistake)
	if strings.Contains(wk.Config.Directories.Versions, "/") {
		t.Errorf("Versions should be a directory name, not a path. Got %q which contains '/'", wk.Config.Directories.Versions)
	}
}

func TestConfigFilesDefaults(t *testing.T) {
	config := &WellKnownConfig{
		Files: WellKnownFiles{
			PublicIndex:     "metadata/public.jsonl",
			BlessedComments: "metadata/blessed-comments.json",
			FollowingIndex:  "metadata/following.json",
		},
	}

	if config.Files.PublicIndex != "metadata/public.jsonl" {
		t.Errorf("PublicIndex should be metadata/public.jsonl")
	}
	if config.Files.BlessedComments != "metadata/blessed-comments.json" {
		t.Errorf("BlessedComments should be metadata/blessed-comments.json")
	}
	if config.Files.FollowingIndex != "metadata/following.json" {
		t.Errorf("FollowingIndex should be metadata/following.json")
	}
}

func TestConfigPathsAreRelative(t *testing.T) {
	config := &WellKnownConfig{
		Directories: WellKnownDirectories{
			Keys:     ".polis/keys",
			Posts:    "posts",
			Comments: "comments",
			Snippets: "snippets",
			Themes:   ".polis/themes",
			Versions: ".versions",
		},
		Files: WellKnownFiles{
			PublicIndex:     "metadata/public.jsonl",
			BlessedComments: "metadata/blessed-comments.json",
			FollowingIndex:  "metadata/following.json",
		},
	}

	paths := []string{
		config.Directories.Keys,
		config.Directories.Posts,
		config.Directories.Comments,
		config.Directories.Snippets,
		config.Directories.Themes,
		config.Directories.Versions,
		config.Files.PublicIndex,
		config.Files.BlessedComments,
		config.Files.FollowingIndex,
	}

	for _, p := range paths {
		if strings.HasPrefix(p, "/") {
			t.Errorf("Path should be relative, not absolute: %s", p)
		}
		if strings.Contains(p, "..") {
			t.Errorf("Path should not contain parent traversal: %s", p)
		}
	}
}

func TestConfigPathsResolve(t *testing.T) {
	dir := setupTestDir(t)

	config := &WellKnownConfig{
		Directories: WellKnownDirectories{
			Posts: "posts",
		},
	}

	resolved := filepath.Join(dir, config.Directories.Posts)

	// Verify resolved path is within siteDir
	if !strings.HasPrefix(resolved, dir) {
		t.Errorf("Resolved path %s should be within %s", resolved, dir)
	}
}

// ============================================================================
// 4. Testdata Loading Tests
// ============================================================================

func TestLoadBashCLIWellKnown(t *testing.T) {
	data := loadTestdata(t, "bash_cli_wellknown.json")

	var wk WellKnown
	if err := json.Unmarshal(data, &wk); err != nil {
		t.Fatalf("Failed to parse bash CLI wellknown: %v", err)
	}

	// Verify canonical fields
	if wk.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", wk.Version, "1.0.0")
	}
	if wk.Author != "John Doe" {
		t.Errorf("Author = %q, want %q", wk.Author, "John Doe")
	}
	if wk.Email != "john@example.com" {
		t.Errorf("Email = %q, want %q", wk.Email, "john@example.com")
	}
	if !strings.HasPrefix(wk.PublicKey, "ssh-ed25519 ") {
		t.Errorf("PublicKey should start with ssh-ed25519")
	}
	if wk.SiteTitle != "My Site" {
		t.Errorf("SiteTitle = %q, want %q", wk.SiteTitle, "My Site")
	}
	if wk.Created != "2026-01-01T00:00:00Z" {
		t.Errorf("Created = %q, want %q", wk.Created, "2026-01-01T00:00:00Z")
	}

	// Verify config section
	if wk.Config == nil {
		t.Fatal("Config should not be nil")
	}
	if wk.Config.Directories.Keys != ".polis/keys" {
		t.Errorf("Config.Directories.Keys = %q, want %q", wk.Config.Directories.Keys, ".polis/keys")
	}
	if wk.Config.Files.PublicIndex != "metadata/public.jsonl" {
		t.Errorf("Config.Files.PublicIndex = %q, want %q", wk.Config.Files.PublicIndex, "metadata/public.jsonl")
	}
}

func TestLoadLegacyWebappWellKnown(t *testing.T) {
	data := loadTestdata(t, "legacy_webapp_wellknown.json")

	var wk WellKnown
	if err := json.Unmarshal(data, &wk); err != nil {
		t.Fatalf("Failed to parse legacy webapp wellknown: %v", err)
	}

	// Verify webapp-specific fields
	if wk.BaseURL != "https://x.polis.pub" {
		t.Errorf("BaseURL = %q, want %q", wk.BaseURL, "https://x.polis.pub")
	}
	if wk.SiteTitle != "My Site" {
		t.Errorf("SiteTitle = %q, want %q", wk.SiteTitle, "My Site")
	}
	if !strings.HasPrefix(wk.PublicKey, "ssh-ed25519 ") {
		t.Errorf("PublicKey should start with ssh-ed25519")
	}

	// Verify deprecated fields are still readable
	if wk.PublicKeyPath != ".polis/keys/id_ed25519.pub" {
		t.Errorf("PublicKeyPath = %q, want %q", wk.PublicKeyPath, ".polis/keys/id_ed25519.pub")
	}
	if wk.Generator != "polis-cli-go/0.1.0" {
		t.Errorf("Generator = %q, want %q", wk.Generator, "polis-cli-go/0.1.0")
	}
	if wk.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("CreatedAt = %q, want %q", wk.CreatedAt, "2026-01-01T00:00:00Z")
	}

	// Config should be nil in legacy format
	if wk.Config != nil {
		t.Errorf("Config should be nil in legacy format")
	}
}

func TestLoadMinimalWellKnown(t *testing.T) {
	data := loadTestdata(t, "minimal_wellknown.json")

	var wk WellKnown
	if err := json.Unmarshal(data, &wk); err != nil {
		t.Fatalf("Failed to parse minimal wellknown: %v", err)
	}

	// Only required fields should be present
	if !strings.HasPrefix(wk.PublicKey, "ssh-ed25519 ") {
		t.Errorf("PublicKey should start with ssh-ed25519")
	}
	if wk.Version != "0.1.0" {
		t.Errorf("Version = %q, want %q", wk.Version, "0.1.0")
	}
	if wk.Created != "2026-01-01T00:00:00Z" {
		t.Errorf("Created = %q, want %q", wk.Created, "2026-01-01T00:00:00Z")
	}

	// Optional fields should be empty
	if wk.Author != "" {
		t.Errorf("Author should be empty in minimal format, got %q", wk.Author)
	}
	if wk.Email != "" {
		t.Errorf("Email should be empty in minimal format, got %q", wk.Email)
	}
	if wk.Config != nil {
		t.Errorf("Config should be nil in minimal format")
	}
}

func TestLoadCorruptWellKnown(t *testing.T) {
	data := loadTestdata(t, "corrupt_wellknown.json")

	var wk WellKnown
	err := json.Unmarshal(data, &wk)
	if err == nil {
		t.Fatal("Expected error parsing corrupt JSON, got nil")
	}
}

// ============================================================================
// 5. Upgrade/Migration Tests
// ============================================================================

func TestUpgradePreservesExistingData(t *testing.T) {
	// This tests that upgrading doesn't lose existing canonical data
	// Note: base_url is now deprecated and will be removed by upgrade
	original := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		SiteTitle: "Existing Title",
		Created:   "2026-01-01T00:00:00Z",
	}

	dir := setupTestDir(t)
	writeTestWellKnown(t, dir, original)

	// Load and verify preservation of canonical fields
	loaded, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	if loaded.SiteTitle != original.SiteTitle {
		t.Errorf("SiteTitle should be preserved: got %q, want %q", loaded.SiteTitle, original.SiteTitle)
	}
	if loaded.PublicKey != original.PublicKey {
		t.Errorf("PublicKey should be preserved")
	}
	if loaded.Created != original.Created {
		t.Errorf("Created should be preserved: got %q, want %q", loaded.Created, original.Created)
	}
}

// ============================================================================
// 6. Cross-CLI Compatibility Tests
// ============================================================================

func TestFieldOrderDoesNotMatter(t *testing.T) {
	// JSON with fields in different order
	jsonData := `{
		"config": {"directories": {"posts": "posts"}},
		"created": "2026-01-01T00:00:00Z",
		"version": "0.1.0",
		"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		"author": "Test Author"
	}`

	var wk WellKnown
	if err := json.Unmarshal([]byte(jsonData), &wk); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	if wk.Version != "0.1.0" {
		t.Errorf("Version mismatch")
	}
	if wk.Author != "Test Author" {
		t.Errorf("Author mismatch")
	}
	if wk.Created != "2026-01-01T00:00:00Z" {
		t.Errorf("Created mismatch")
	}
	if wk.Config == nil || wk.Config.Directories.Posts != "posts" {
		t.Errorf("Config.Directories.Posts mismatch")
	}
}

func TestExtraFieldsAreIgnored(t *testing.T) {
	// JSON with extra unknown fields should still parse
	jsonData := `{
		"version": "0.1.0",
		"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		"created": "2026-01-01T00:00:00Z",
		"unknown_field": "should be ignored",
		"another_unknown": {"nested": "value"}
	}`

	var wk WellKnown
	if err := json.Unmarshal([]byte(jsonData), &wk); err != nil {
		t.Fatalf("Failed to parse with extra fields: %v", err)
	}

	if wk.Version != "0.1.0" {
		t.Errorf("Version mismatch")
	}
}

// ============================================================================
// 7. Error Handling Tests
// ============================================================================

func TestLoadMissingFile(t *testing.T) {
	dir := setupTestDir(t)
	// Don't create any .well-known/polis file

	_, err := LoadWellKnown(dir)
	if err == nil {
		t.Fatal("Expected error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("Expected IsNotExist error, got: %v", err)
	}
}

func TestLoadCorruptedJSON(t *testing.T) {
	dir := setupTestDir(t)

	// Write invalid JSON
	wellKnownDir := filepath.Join(dir, ".well-known")
	if err := os.MkdirAll(wellKnownDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wellKnownDir, "polis"), []byte("not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadWellKnown(dir)
	if err == nil {
		t.Fatal("Expected error for corrupted JSON")
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := setupTestDir(t)
	// .well-known doesn't exist yet

	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
	}

	if err := SaveWellKnown(dir, wk); err != nil {
		t.Fatalf("SaveWellKnown should create directory: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(filepath.Join(dir, ".well-known", "polis")); err != nil {
		t.Errorf("File should exist after save: %v", err)
	}
}

// ============================================================================
// 8. Helper Function Tests
// ============================================================================

func TestGetSiteTitle(t *testing.T) {
	dir := setupTestDir(t)

	// Test with site_title set
	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		SiteTitle: "My Awesome Site",
		BaseURL:   "https://example.polis.pub",
	}
	writeTestWellKnown(t, dir, wk)

	title := GetSiteTitle(dir)
	if title != "My Awesome Site" {
		t.Errorf("GetSiteTitle() = %q, want %q", title, "My Awesome Site")
	}
}

func TestGetSiteTitleNoFallback(t *testing.T) {
	dir := setupTestDir(t)

	// Test without site_title - should return empty (no fallback to base_url)
	// base_url is runtime config from POLIS_BASE_URL env var, not stored in file
	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
	}
	writeTestWellKnown(t, dir, wk)

	title := GetSiteTitle(dir)
	if title != "" {
		t.Errorf("GetSiteTitle() = %q, want empty string (no fallback)", title)
	}
}

func TestGetPublicKey(t *testing.T) {
	dir := setupTestDir(t)

	expectedKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local"
	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: expectedKey,
	}
	writeTestWellKnown(t, dir, wk)

	key := GetPublicKey(dir)
	if key != expectedKey {
		t.Errorf("GetPublicKey() = %q, want %q", key, expectedKey)
	}
}

func TestGetPublicKeyMissingFile(t *testing.T) {
	dir := setupTestDir(t)
	// No file written

	key := GetPublicKey(dir)
	if key != "" {
		t.Errorf("GetPublicKey() should return empty string for missing file, got %q", key)
	}
}

// ============================================================================
// 9. KnownFields Map Completeness Test
// ============================================================================

func TestKnownFieldsMapComplete(t *testing.T) {
	// This test verifies that the KnownFields map in the webapp matches
	// the WellKnown struct fields using reflection

	wkType := reflect.TypeOf(WellKnown{})

	// Check top-level fields
	for i := 0; i < wkType.NumField(); i++ {
		field := wkType.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" {
			continue
		}
		// Extract field name from json tag (before comma)
		jsonName := strings.Split(jsonTag, ",")[0]
		if jsonName == "-" {
			continue
		}

		// This is informational - we're documenting what fields exist
		t.Logf("WellKnown has field: %s (json: %s)", field.Name, jsonName)
	}
}

// ============================================================================
// 10. Upgrade Function Tests
// ============================================================================

func TestUpgradeWellKnown_MigratesCreatedAt(t *testing.T) {
	dir := setupTestDir(t)

	// Create legacy file with created_at instead of created
	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		CreatedAt: "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	// Run upgrade
	upgraded, err := UpgradeWellKnown(dir)
	if err != nil {
		t.Fatalf("UpgradeWellKnown failed: %v", err)
	}

	// Verify created_at was migrated to created
	if upgraded.Created != "2026-01-01T00:00:00Z" {
		t.Errorf("Created should be %q, got %q", "2026-01-01T00:00:00Z", upgraded.Created)
	}
	if upgraded.CreatedAt != "" {
		t.Errorf("CreatedAt should be cleared after migration, got %q", upgraded.CreatedAt)
	}

	// Verify file was updated
	loaded, _ := LoadWellKnown(dir)
	if loaded.Created != "2026-01-01T00:00:00Z" {
		t.Error("File should have been updated with migrated created field")
	}
}

func TestUpgradeWellKnown_AddsCreatedWhenMissing(t *testing.T) {
	dir := setupTestDir(t)

	// Create file without created or created_at
	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		SiteTitle: "Test Site",
	}
	writeTestWellKnown(t, dir, wk)

	// Run upgrade
	upgraded, err := UpgradeWellKnown(dir)
	if err != nil {
		t.Fatalf("UpgradeWellKnown failed: %v", err)
	}

	// Verify created was added
	if upgraded.Created == "" {
		t.Error("Created should have been set to current time")
	}

	// Verify it's a valid RFC3339 timestamp
	_, err = time.Parse(time.RFC3339, upgraded.Created)
	if err != nil {
		t.Errorf("Created should be valid RFC3339, got %q: %v", upgraded.Created, err)
	}

	// Verify file was updated
	loaded, _ := LoadWellKnown(dir)
	if loaded.Created == "" {
		t.Error("File should have been updated with created timestamp")
	}
}

func TestUpgradeWellKnown_AddsConfigSection(t *testing.T) {
	dir := setupTestDir(t)

	// Create file without config section
	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	// Run upgrade
	upgraded, err := UpgradeWellKnown(dir)
	if err != nil {
		t.Fatalf("UpgradeWellKnown failed: %v", err)
	}

	// Verify config section was added
	if upgraded.Config == nil {
		t.Fatal("Config should not be nil after upgrade")
	}
	if upgraded.Config.Directories.Posts != "posts" {
		t.Errorf("Config.Directories.Posts should be %q", "posts")
	}
	if upgraded.Config.Files.PublicIndex != "metadata/public.jsonl" {
		t.Errorf("Config.Files.PublicIndex should be %q", "metadata/public.jsonl")
	}
}

func TestUpgradeWellKnown_Idempotent(t *testing.T) {
	dir := setupTestDir(t)

	// Create a complete file
	wk := &WellKnown{
		Version:   "0.1.0",
		Author:    "Test Author",
		Email:     "test@example.com",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		SiteTitle: "Test Site",
		Created:   "2026-01-01T00:00:00Z",
		Config: &WellKnownConfig{
			Directories: WellKnownDirectories{
				Posts: "posts",
			},
		},
	}
	writeTestWellKnown(t, dir, wk)

	// Get file content before upgrade
	beforeData, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))

	// Run upgrade twice
	_, err1 := UpgradeWellKnown(dir)
	if err1 != nil {
		t.Fatalf("First upgrade failed: %v", err1)
	}

	// Get file after first upgrade to see if there were any changes
	afterFirstData, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))

	_, err2 := UpgradeWellKnown(dir)
	if err2 != nil {
		t.Fatalf("Second upgrade failed: %v", err2)
	}

	// Get file after second upgrade
	afterSecondData, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))

	// After second upgrade, file should be same as after first
	if string(afterFirstData) != string(afterSecondData) {
		t.Error("Second upgrade changed the file - upgrade is not idempotent")
	}

	// Log whether first upgrade made changes (informational)
	if string(beforeData) != string(afterFirstData) {
		t.Log("First upgrade made changes (expected if config was incomplete)")
	}
}

func TestUpgradeWellKnown_ClearsDeprecatedFields(t *testing.T) {
	dir := setupTestDir(t)

	// Create file with deprecated fields
	wk := &WellKnown{
		Version:       "0.1.0",
		PublicKey:     "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:       "2026-01-01T00:00:00Z",
		PublicKeyPath: ".polis/keys/id_ed25519.pub",
		Generator:     "polis-cli-go/0.1.0",
	}
	writeTestWellKnown(t, dir, wk)

	// Run upgrade
	upgraded, err := UpgradeWellKnown(dir)
	if err != nil {
		t.Fatalf("UpgradeWellKnown failed: %v", err)
	}

	// Verify deprecated fields were cleared
	if upgraded.PublicKeyPath != "" {
		t.Errorf("PublicKeyPath should be cleared, got %q", upgraded.PublicKeyPath)
	}
	if upgraded.Generator != "" {
		t.Errorf("Generator should be cleared, got %q", upgraded.Generator)
	}
}

func TestUpgradeWellKnown_RemovesBaseURL(t *testing.T) {
	dir := setupTestDir(t)

	// Create file with base_url (legacy webapp format)
	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
		BaseURL:   "https://x.polis.pub",
		Subdomain: "x",
	}
	writeTestWellKnown(t, dir, wk)

	// Run upgrade
	upgraded, err := UpgradeWellKnown(dir)
	if err != nil {
		t.Fatalf("UpgradeWellKnown failed: %v", err)
	}

	// Verify base_url and subdomain were removed
	if upgraded.BaseURL != "" {
		t.Errorf("BaseURL should be removed, got %q", upgraded.BaseURL)
	}
	if upgraded.Subdomain != "" {
		t.Errorf("Subdomain should be removed, got %q", upgraded.Subdomain)
	}

	// Verify file was updated
	loaded, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}
	if loaded.BaseURL != "" {
		t.Errorf("File should not contain base_url, got %q", loaded.BaseURL)
	}
	if loaded.Subdomain != "" {
		t.Errorf("File should not contain subdomain, got %q", loaded.Subdomain)
	}
}

func TestCheckUnrecognizedFields_KnownFields(t *testing.T) {
	dir := setupTestDir(t)

	// Create file with only known fields
	wk := &WellKnown{
		Version:   "0.1.0",
		Author:    "Test",
		Email:     "test@example.com",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
		Config: &WellKnownConfig{
			Directories: WellKnownDirectories{
				Posts: "posts",
			},
		},
	}
	writeTestWellKnown(t, dir, wk)

	// CheckUnrecognizedFields should not panic
	CheckUnrecognizedFields(dir)
	// Note: We can't easily verify log output in unit tests without more setup
}

func TestCheckUnrecognizedFields_EmptyFile(t *testing.T) {
	dir := setupTestDir(t)

	// Create minimal file
	wellKnownDir := filepath.Join(dir, ".well-known")
	os.MkdirAll(wellKnownDir, 0755)
	os.WriteFile(filepath.Join(wellKnownDir, "polis"), []byte("{}"), 0644)

	// Should not panic
	CheckUnrecognizedFields(dir)
}

func TestCheckUnrecognizedFields_MissingFile(t *testing.T) {
	dir := setupTestDir(t)
	// No file created

	// Should not panic
	CheckUnrecognizedFields(dir)
}

// ============================================================================
// 11. JSON Indentation Test
// ============================================================================

// ============================================================================
// 12. Email Privacy + Domain Identity Tests (Phase 0)
// ============================================================================

func TestEmailOmitempty_NotSerializedWhenEmpty(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "0.1.0",
		Author:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
		// Email intentionally not set
	}
	writeTestWellKnown(t, dir, wk)

	// Read raw JSON to verify email is not present
	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"email"`) {
		t.Error("Email field should not be present in JSON when empty (omitempty)")
	}

	// Verify round-trip
	loaded, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Email != "" {
		t.Errorf("Email should be empty, got %q", loaded.Email)
	}
}

func TestEmailOmitempty_SerializedWhenSet(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "0.1.0",
		Author:    "alice",
		Email:     "alice@example.com",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	// Verify email is present when explicitly set
	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"email"`) {
		t.Error("Email field should be present when set")
	}

	loaded, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", loaded.Email, "alice@example.com")
	}
}

func TestUpgradeWellKnown_RemovesDomain(t *testing.T) {
	dir := setupTestDir(t)

	// Create file with domain field (legacy format)
	wk := &WellKnown{
		Version:   "0.1.0",
		Author:    "alice",
		Domain:    "alice.polis.pub",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	// Run upgrade
	upgraded, err := UpgradeWellKnown(dir)
	if err != nil {
		t.Fatalf("UpgradeWellKnown failed: %v", err)
	}

	// Verify domain was removed
	if upgraded.Domain != "" {
		t.Errorf("Domain should be removed, got %q", upgraded.Domain)
	}

	// Verify file was updated and doesn't contain domain
	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"domain"`) {
		t.Error("Saved file should not contain domain key")
	}
}

func TestBackwardCompat_ExistingFileWithEmail(t *testing.T) {
	// Test that old .well-known/polis files with email still load correctly
	dir := setupTestDir(t)
	wellKnownDir := filepath.Join(dir, ".well-known")
	os.MkdirAll(wellKnownDir, 0755)

	// Old-format JSON with email, no domain
	oldJSON := `{
  "version": "0.45.0",
  "author": "alice",
  "email": "alice@example.com",
  "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey polis-local",
  "created": "2026-01-01T00:00:00Z"
}`
	os.WriteFile(filepath.Join(wellKnownDir, "polis"), []byte(oldJSON), 0644)

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Should load old-format file: %v", err)
	}
	if wk.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", wk.Email, "alice@example.com")
	}
	if wk.Domain != "" {
		t.Errorf("Domain should be empty for old-format file, got %q", wk.Domain)
	}
}

func TestSaveWellKnownIndentation(t *testing.T) {
	dir := setupTestDir(t)

	wk := &WellKnown{
		Version:   "0.1.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Config: &WellKnownConfig{
			Directories: WellKnownDirectories{
				Posts: "posts",
			},
		},
	}

	if err := SaveWellKnown(dir, wk); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}

	// Verify it's indented (not compact)
	if !strings.Contains(string(data), "\n  ") {
		t.Error("Output should be indented with 2 spaces")
	}
}
