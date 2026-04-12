package site

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
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
		Version:   "polis-cli-go/0.57.0",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		AuthorName:    "test",
		Created:   time.Now().UTC().Format(time.RFC3339),
	}
	writeTestWellKnown(t, dir, wk)

	path := filepath.Join(dir, ".well-known", "polis")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("File does not exist: %v", err)
	}
	if info.IsDir() {
		t.Fatal("Expected file, got directory")
	}
	mode := info.Mode().Perm()
	if mode&0600 != 0600 {
		t.Errorf("File should be readable/writable by owner, got %o", mode)
	}
}

func TestWellKnownValidJSON(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:     "polis-cli-go/0.57.0",
		AuthorName:      "Test Author",
		Email:       "test@example.com",
		PublicKey:    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		SiteTitle:   "Test Site",
		Created:     "2026-01-01T00:00:00Z",
		ActiveTheme: "sols",
		Bundles: map[string]BundleEntry{
			"pub.polis.core": {Active: true, Path: "content/pub.polis.core/bundle.json"},
		},
	}
	writeTestWellKnown(t, dir, wk)

	loaded, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	if loaded.Version != wk.Version {
		t.Errorf("Version mismatch: got %q, want %q", loaded.Version, wk.Version)
	}
	if loaded.AuthorName != wk.AuthorName {
		t.Errorf("Author mismatch: got %q, want %q", loaded.AuthorName, wk.AuthorName)
	}
	if loaded.Email != wk.Email {
		t.Errorf("Email mismatch: got %q, want %q", loaded.Email, wk.Email)
	}
	if loaded.PublicKey != wk.PublicKey {
		t.Errorf("PublicKey mismatch")
	}
	if loaded.SiteTitle != wk.SiteTitle {
		t.Errorf("SiteTitle mismatch")
	}
	if loaded.Created != wk.Created {
		t.Errorf("Created mismatch")
	}
	if loaded.ActiveTheme != wk.ActiveTheme {
		t.Errorf("ActiveTheme mismatch: got %q, want %q", loaded.ActiveTheme, wk.ActiveTheme)
	}
	if len(loaded.Bundles) != 1 {
		t.Errorf("Bundles count: got %d, want 1", len(loaded.Bundles))
	}
	if entry, ok := loaded.Bundles["pub.polis.core"]; !ok || !entry.Active {
		t.Errorf("pub.polis.core bundle should be active")
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
		{"valid ed25519 key", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local", true},
		{"valid ed25519 key without comment", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX", true},
		{"wrong key type", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB", false},
		{"empty key", "", false},
		{"malformed key", "not-a-valid-key", false},
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

// ============================================================================
// 3. Bundle Registry Tests
// ============================================================================

func TestBundleRegistryRoundTrip(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
		Bundles: map[string]BundleEntry{
			"pub.polis.core":      {Active: true, Path: "content/pub.polis.core/bundle.json"},
			"com.example.recipes": {Active: false, Path: "content/com.example.recipes/bundle.json"},
		},
	}
	writeTestWellKnown(t, dir, wk)

	loaded, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	if len(loaded.Bundles) != 2 {
		t.Fatalf("Bundles count: got %d, want 2", len(loaded.Bundles))
	}

	core := loaded.Bundles["pub.polis.core"]
	if !core.Active || core.Path != "content/pub.polis.core/bundle.json" {
		t.Errorf("pub.polis.core: active=%v, path=%q", core.Active, core.Path)
	}

	recipes := loaded.Bundles["com.example.recipes"]
	if recipes.Active || recipes.Path != "content/com.example.recipes/bundle.json" {
		t.Errorf("com.example.recipes: active=%v, path=%q", recipes.Active, recipes.Path)
	}
}

func TestNoBundlesField(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	loaded, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}
	if loaded.Bundles != nil && len(loaded.Bundles) != 0 {
		t.Errorf("Bundles should be nil or empty, got %v", loaded.Bundles)
	}
}

// ============================================================================
// 4. Active Theme Tests
// ============================================================================

func TestActiveThemeRoundTrip(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:     "polis-cli-go/0.57.0",
		AuthorName:      "alice",
		PublicKey:    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:     "2026-01-01T00:00:00Z",
		ActiveTheme: "turbo",
	}
	writeTestWellKnown(t, dir, wk)

	if got := GetActiveTheme(dir); got != "turbo" {
		t.Errorf("GetActiveTheme() = %q, want turbo", got)
	}
}

func TestSetActiveTheme(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:     "polis-cli-go/0.57.0",
		AuthorName:      "alice",
		PublicKey:    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:     "2026-01-01T00:00:00Z",
		ActiveTheme: "sols",
	}
	writeTestWellKnown(t, dir, wk)

	if err := SetActiveTheme(dir, "zane"); err != nil {
		t.Fatalf("SetActiveTheme: %v", err)
	}

	if got := GetActiveTheme(dir); got != "zane" {
		t.Errorf("After SetActiveTheme, got %q, want zane", got)
	}
}

// ============================================================================
// 5. Testdata Loading Tests
// ============================================================================

func TestLoadBashCLIWellKnown(t *testing.T) {
	data := loadTestdata(t, "bash_cli_wellknown.json")

	var wk WellKnown
	if err := json.Unmarshal(data, &wk); err != nil {
		t.Fatalf("Failed to parse bash CLI wellknown: %v", err)
	}

	if wk.Version != "polis-cli/0.45.0" {
		t.Errorf("Version = %q, want %q", wk.Version, "polis-cli/0.45.0")
	}
	if wk.AuthorName != "John Doe" {
		t.Errorf("Author = %q, want %q", wk.AuthorName, "John Doe")
	}
	if wk.Email != "john@example.com" {
		t.Errorf("Email = %q, want %q", wk.Email, "john@example.com")
	}
	if !strings.HasPrefix(wk.PublicKey, "ssh-ed25519 ") {
		t.Error("PublicKey should start with ssh-ed25519")
	}
	if wk.SiteTitle != "My Site" {
		t.Errorf("SiteTitle = %q, want %q", wk.SiteTitle, "My Site")
	}
	if wk.Created != "2026-01-01T00:00:00Z" {
		t.Errorf("Created = %q", wk.Created)
	}
	if wk.ActiveTheme != "sols" {
		t.Errorf("ActiveTheme = %q, want sols", wk.ActiveTheme)
	}
	if len(wk.Bundles) != 1 {
		t.Fatalf("Bundles count = %d, want 1", len(wk.Bundles))
	}
	core := wk.Bundles["pub.polis.core"]
	if !core.Active {
		t.Error("pub.polis.core should be active")
	}
}

func TestLoadMinimalWellKnown(t *testing.T) {
	data := loadTestdata(t, "minimal_wellknown.json")

	var wk WellKnown
	if err := json.Unmarshal(data, &wk); err != nil {
		t.Fatalf("Failed to parse minimal wellknown: %v", err)
	}

	if !strings.HasPrefix(wk.PublicKey, "ssh-ed25519 ") {
		t.Error("PublicKey should start with ssh-ed25519")
	}
	if wk.Version != "0.1.0" {
		t.Errorf("Version = %q, want %q", wk.Version, "0.1.0")
	}
	if wk.Created != "2026-01-01T00:00:00Z" {
		t.Errorf("Created = %q", wk.Created)
	}
	if wk.AuthorName != "" {
		t.Errorf("Author should be empty, got %q", wk.AuthorName)
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
// 6. Email Privacy Tests
// ============================================================================

func TestEmailOmitempty_NotSerializedWhenEmpty(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"email"`) {
		t.Error("Email field should not be present when empty (omitempty)")
	}
}

func TestEmailOmitempty_SerializedWhenSet(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		Email:     "alice@example.com",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

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

// ============================================================================
// 7. Error Handling Tests
// ============================================================================

func TestLoadMissingFile(t *testing.T) {
	dir := setupTestDir(t)
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
	wellKnownDir := filepath.Join(dir, ".well-known")
	os.MkdirAll(wellKnownDir, 0755)
	os.WriteFile(filepath.Join(wellKnownDir, "polis"), []byte("not valid json"), 0644)

	_, err := LoadWellKnown(dir)
	if err == nil {
		t.Fatal("Expected error for corrupted JSON")
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
	}

	if err := SaveWellKnown(dir, wk); err != nil {
		t.Fatalf("SaveWellKnown should create directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".well-known", "polis")); err != nil {
		t.Errorf("File should exist after save: %v", err)
	}
}

// ============================================================================
// 8. Helper Function Tests
// ============================================================================

func TestGetSiteTitle(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		SiteTitle: "My Awesome Site",
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	title := GetSiteTitle(dir)
	if title != "My Awesome Site" {
		t.Errorf("GetSiteTitle() = %q, want %q", title, "My Awesome Site")
	}
}

func TestGetSiteTitleMissing(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	title := GetSiteTitle(dir)
	if title != "" {
		t.Errorf("GetSiteTitle() = %q, want empty string", title)
	}
}

func TestGetPublicKey(t *testing.T) {
	dir := setupTestDir(t)
	expectedKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local"
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: expectedKey,
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	key := GetPublicKey(dir)
	if key != expectedKey {
		t.Errorf("GetPublicKey() = %q, want %q", key, expectedKey)
	}
}

func TestGetPublicKeyMissingFile(t *testing.T) {
	dir := setupTestDir(t)
	key := GetPublicKey(dir)
	if key != "" {
		t.Errorf("GetPublicKey() should return empty string for missing file, got %q", key)
	}
}

// ============================================================================
// 9. Extra Fields Compatibility Test
// ============================================================================

func TestExtraFieldsAreIgnored(t *testing.T) {
	jsonData := `{
		"version": "polis-cli-go/0.57.0",
		"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		"author": "alice",
		"created": "2026-01-01T00:00:00Z",
		"unknown_field": "should be ignored",
		"another_unknown": {"nested": "value"}
	}`

	var wk WellKnown
	if err := json.Unmarshal([]byte(jsonData), &wk); err != nil {
		t.Fatalf("Failed to parse with extra fields: %v", err)
	}

	if wk.Version != "polis-cli-go/0.57.0" {
		t.Errorf("Version mismatch")
	}
}

func TestFieldOrderDoesNotMatter(t *testing.T) {
	jsonData := `{
		"bundles": {"pub.polis.core": {"active": true, "path": "content/pub.polis.core/bundle.json"}},
		"created": "2026-01-01T00:00:00Z",
		"version": "polis-cli-go/0.57.0",
		"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		"author_name": "Test Author"
	}`

	var wk WellKnown
	if err := json.Unmarshal([]byte(jsonData), &wk); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	if wk.Version != "polis-cli-go/0.57.0" {
		t.Error("Version mismatch")
	}
	if wk.AuthorName != "Test Author" {
		t.Error("AuthorName mismatch")
	}
	if len(wk.Bundles) != 1 {
		t.Errorf("Bundles count: got %d, want 1", len(wk.Bundles))
	}
}

// ============================================================================
// 10. JSON Formatting Tests
// ============================================================================

func TestSaveWellKnownIndentation(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
		Bundles: map[string]BundleEntry{
			"pub.polis.core": {Active: true, Path: "content/pub.polis.core/bundle.json"},
		},
	}

	if err := SaveWellKnown(dir, wk); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "\n  ") {
		t.Error("Output should be indented with 2 spaces")
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("Output should end with newline")
	}
}

// ============================================================================
// 11. Avatar Generation Tests
// ============================================================================

func TestGenerateDefaultAvatar_ReturnsValidConfig(t *testing.T) {
	avatar := GenerateDefaultAvatar()
	if avatar == nil {
		t.Fatal("GenerateDefaultAvatar() returned nil")
	}
	if len(avatar.BG) != 7 || avatar.BG[0] != '#' {
		t.Errorf("BG should be #rrggbb, got %q", avatar.BG)
	}
	if avatar.FG != "#ffffff" {
		t.Errorf("FG should be #ffffff, got %q", avatar.FG)
	}
	if avatar.Border != "" {
		t.Errorf("Border should be empty, got %q", avatar.Border)
	}
	if avatar.BorderW != 0 {
		t.Errorf("BorderW should be 0, got %d", avatar.BorderW)
	}
	if avatar.Pattern != "" {
		t.Errorf("Pattern should be empty, got %q", avatar.Pattern)
	}
}

func TestGenerateDefaultAvatar_ContrastRatio(t *testing.T) {
	for i := 0; i < 100; i++ {
		avatar := GenerateDefaultAvatar()
		ratio := ContrastRatio(avatar.BG, avatar.FG)
		if ratio < 4.5 {
			t.Errorf("Iteration %d: contrast ratio %.2f < 4.5 for BG=%s FG=%s",
				i, ratio, avatar.BG, avatar.FG)
		}
	}
}

func TestGenerateDefaultAvatar_Randomness(t *testing.T) {
	colors := make(map[string]bool)
	for i := 0; i < 10; i++ {
		avatar := GenerateDefaultAvatar()
		colors[avatar.BG] = true
	}
	if len(colors) < 2 {
		t.Error("Expected at least 2 different colors from 10 generations")
	}
}

func TestHslToHex_KnownValues(t *testing.T) {
	tests := []struct {
		h, s, l float64
		want    string
	}{
		{0, 100, 50, "#ff0000"},
		{120, 100, 50, "#00ff00"},
		{240, 100, 50, "#0000ff"},
		{0, 0, 0, "#000000"},
		{0, 0, 100, "#ffffff"},
		{0, 0, 50, "#808080"},
	}
	for _, tt := range tests {
		got := hslToHex(tt.h, tt.s, tt.l)
		if got != tt.want {
			t.Errorf("hslToHex(%.0f, %.0f, %.0f) = %q, want %q", tt.h, tt.s, tt.l, got, tt.want)
		}
	}
}

func TestContrastRatio_KnownPairs(t *testing.T) {
	// Black on white should be ~21:1
	ratio := ContrastRatio("#ffffff", "#000000")
	if math.Abs(ratio-21.0) > 0.1 {
		t.Errorf("White/Black contrast = %.2f, want ~21.0", ratio)
	}

	// Same color should be 1:1
	ratio = ContrastRatio("#888888", "#888888")
	if math.Abs(ratio-1.0) > 0.01 {
		t.Errorf("Same color contrast = %.2f, want 1.0", ratio)
	}
}

func TestAvatarRoundTrip(t *testing.T) {
	dir := setupTestDir(t)
	avatar := &AvatarConfig{BG: "#2a5a6a", FG: "#ffffff"}
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
		Avatar:    avatar,
	}
	writeTestWellKnown(t, dir, wk)

	loaded, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}
	if loaded.Avatar == nil {
		t.Fatal("Avatar should not be nil after roundtrip")
	}
	if loaded.Avatar.BG != "#2a5a6a" {
		t.Errorf("Avatar.BG = %q, want #2a5a6a", loaded.Avatar.BG)
	}
	if loaded.Avatar.FG != "#ffffff" {
		t.Errorf("Avatar.FG = %q, want #ffffff", loaded.Avatar.FG)
	}
}

func TestAvatarOmitempty_NotSerializedWhenNil(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:   "polis-cli-go/0.57.0",
		AuthorName:    "alice",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:   "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	data, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if strings.Contains(string(data), `"avatar"`) {
		t.Error("Avatar field should not be present when nil (omitempty)")
	}
}

// ============================================================================
// MigrateAuthorField Tests
// ============================================================================

func TestMigrateAuthorField_AuthorOnly(t *testing.T) {
	dir := setupTestDir(t)
	os.MkdirAll(filepath.Join(dir, ".well-known"), 0755)
	// Write raw JSON with deprecated "author" field
	wkPath := filepath.Join(dir, ".well-known", "polis")
	os.WriteFile(wkPath, []byte(`{"version":"1","public_key":"pk","author":"Alice","created":"2026-01-01T00:00:00Z"}`), 0644)

	if err := MigrateAuthorField(dir); err != nil {
		t.Fatalf("MigrateAuthorField: %v", err)
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("LoadWellKnown: %v", err)
	}
	if wk.AuthorName != "Alice" {
		t.Errorf("AuthorName = %q, want %q", wk.AuthorName, "Alice")
	}
	// Verify "author" key is gone from raw JSON
	data, _ := os.ReadFile(wkPath)
	if strings.Contains(string(data), `"author"`) && !strings.Contains(string(data), `"author_name"`) {
		t.Error("deprecated author key should be removed from JSON")
	}
}

func TestMigrateAuthorField_BothPresent(t *testing.T) {
	dir := setupTestDir(t)
	os.MkdirAll(filepath.Join(dir, ".well-known"), 0755)
	wkPath := filepath.Join(dir, ".well-known", "polis")
	os.WriteFile(wkPath, []byte(`{"version":"1","public_key":"pk","author":"Old","author_name":"New","created":"2026-01-01T00:00:00Z"}`), 0644)

	if err := MigrateAuthorField(dir); err != nil {
		t.Fatalf("MigrateAuthorField: %v", err)
	}

	wk, _ := LoadWellKnown(dir)
	if wk.AuthorName != "New" {
		t.Errorf("AuthorName should preserve existing value, got %q", wk.AuthorName)
	}
}

func TestMigrateAuthorField_AuthorNameOnly(t *testing.T) {
	dir := setupTestDir(t)
	os.MkdirAll(filepath.Join(dir, ".well-known"), 0755)
	wkPath := filepath.Join(dir, ".well-known", "polis")
	os.WriteFile(wkPath, []byte(`{"version":"1","public_key":"pk","author_name":"Alice","created":"2026-01-01T00:00:00Z"}`), 0644)

	if err := MigrateAuthorField(dir); err != nil {
		t.Fatalf("MigrateAuthorField: %v", err)
	}

	wk, _ := LoadWellKnown(dir)
	if wk.AuthorName != "Alice" {
		t.Errorf("AuthorName = %q", wk.AuthorName)
	}
}

func TestMigrateAuthorField_NoFile(t *testing.T) {
	dir := t.TempDir()
	if err := MigrateAuthorField(dir); err != nil {
		t.Errorf("should not error on missing file: %v", err)
	}
}

// ============================================================================
// Favicon Generation Tests
// ============================================================================

func TestGenerateFaviconSVG_BasicAvatar(t *testing.T) {
	config := &AvatarConfig{BG: "#2a5a6a", FG: "#ffffff"}
	svg := GenerateFaviconSVG(config, "A")

	if !strings.Contains(svg, `fill="#2a5a6a"`) {
		t.Error("SVG should contain background color")
	}
	if !strings.Contains(svg, `fill="#ffffff">A</text>`) {
		t.Error("SVG should contain initial 'A' in foreground color")
	}
	if !strings.Contains(svg, `viewBox="0 0 128 128"`) {
		t.Error("SVG should have 128x128 viewBox")
	}
}

func TestGenerateFaviconSVG_NilConfig(t *testing.T) {
	svg := GenerateFaviconSVG(nil, "V")

	if !strings.Contains(svg, `fill="#888888"`) {
		t.Error("Nil config should use grey fallback background")
	}
	if !strings.Contains(svg, ">V</text>") {
		t.Error("SVG should contain initial 'V'")
	}
}

func TestGenerateFaviconSVG_WithBorder(t *testing.T) {
	config := &AvatarConfig{BG: "#2a5a6a", FG: "#ffffff", Border: "#d06888", BorderW: 2}
	svg := GenerateFaviconSVG(config, "B")

	if !strings.Contains(svg, `stroke="#d06888"`) {
		t.Error("SVG should contain border stroke")
	}
}

func TestGenerateFaviconSVG_WithPattern(t *testing.T) {
	config := &AvatarConfig{BG: "#2a5a6a", FG: "#ffffff", Pattern: "dots", PatternColor: "#3a7a8a"}
	svg := GenerateFaviconSVG(config, "P")

	if !strings.Contains(svg, "<defs>") {
		t.Error("SVG should contain pattern defs")
	}
	if !strings.Contains(svg, `fill="url(#p)"`) {
		t.Error("SVG should reference the pattern fill")
	}
	// Initial should be hidden when pattern is set
	if strings.Contains(svg, ">P</text>") {
		t.Error("Initial should be hidden when pattern is set")
	}
}

func TestGenerateFaviconSVG_MultiCharInitial(t *testing.T) {
	svg := GenerateFaviconSVG(nil, "VD")

	if !strings.Contains(svg, `font-size="56"`) {
		t.Error("Multi-char initial should use smaller font size 56")
	}
}

func TestWriteFavicon_CreatesFile(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:    "polis-cli-go/test",
		AuthorName: "Alice",
		PublicKey:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:    "2026-01-01T00:00:00Z",
		Avatar:     &AvatarConfig{BG: "#3a6a2a", FG: "#ffffff"},
	}
	writeTestWellKnown(t, dir, wk)

	if err := WriteFavicon(dir); err != nil {
		t.Fatalf("WriteFavicon: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "favicon.svg"))
	if err != nil {
		t.Fatalf("favicon.svg should exist: %v", err)
	}
	svg := string(data)
	if !strings.Contains(svg, `fill="#3a6a2a"`) {
		t.Error("favicon should use avatar BG color")
	}
	if !strings.Contains(svg, ">A</text>") {
		t.Error("favicon should use first letter of author name")
	}
}

func TestWriteFavicon_NoAvatar(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:    "polis-cli-go/test",
		AuthorName: "Bob",
		PublicKey:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:    "2026-01-01T00:00:00Z",
	}
	writeTestWellKnown(t, dir, wk)

	if err := WriteFavicon(dir); err != nil {
		t.Fatalf("WriteFavicon: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "favicon.svg"))
	if !strings.Contains(string(data), ">B</text>") {
		t.Error("favicon should use initial from author name even without avatar config")
	}
}

func TestWriteFavicon_MissingWellKnown(t *testing.T) {
	dir := t.TempDir()
	err := WriteFavicon(dir)
	if err == nil {
		t.Error("WriteFavicon should error when .well-known/polis is missing")
	}
}

func TestWriteFavicon_OverwritesExisting(t *testing.T) {
	dir := setupTestDir(t)
	wk := &WellKnown{
		Version:    "polis-cli-go/test",
		AuthorName: "Carol",
		PublicKey:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyXXXXXXXXXXXXXXXXXXXXXXXX polis-local",
		Created:    "2026-01-01T00:00:00Z",
		Avatar:     &AvatarConfig{BG: "#aa0000", FG: "#ffffff"},
	}
	writeTestWellKnown(t, dir, wk)

	// Write initial favicon
	WriteFavicon(dir)

	// Update avatar and rewrite
	wk.Avatar.BG = "#00aa00"
	writeTestWellKnown(t, dir, wk)
	WriteFavicon(dir)

	data, _ := os.ReadFile(filepath.Join(dir, "favicon.svg"))
	if !strings.Contains(string(data), `fill="#00aa00"`) {
		t.Error("favicon should reflect updated avatar color")
	}
	if strings.Contains(string(data), `fill="#aa0000"`) {
		t.Error("favicon should not contain old avatar color")
	}
}
