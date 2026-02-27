package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_DefaultPaths(t *testing.T) {
	dir := t.TempDir()

	result, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	// Verify standard directories exist
	expectedDirs := []string{
		".polis/keys",
		".polis/themes",
		"posts",
		"comments",
		"snippets",
		"metadata",
		".well-known",
	}
	for _, d := range expectedDirs {
		path := filepath.Join(dir, d)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("Expected directory %s to exist: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Expected %s to be a directory", d)
		}
	}

	// Verify .well-known/polis has default paths in config
	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.Config == nil {
		t.Fatal("Config should not be nil")
	}
	if wk.Config.Directories.Keys != ".polis/keys" {
		t.Errorf("Keys dir = %q, want %q", wk.Config.Directories.Keys, ".polis/keys")
	}
	if wk.Config.Directories.Posts != "posts" {
		t.Errorf("Posts dir = %q, want %q", wk.Config.Directories.Posts, "posts")
	}
	if wk.Config.Directories.Comments != "comments" {
		t.Errorf("Comments dir = %q, want %q", wk.Config.Directories.Comments, "comments")
	}
	if wk.Config.Directories.Snippets != "snippets" {
		t.Errorf("Snippets dir = %q, want %q", wk.Config.Directories.Snippets, "snippets")
	}
	if wk.Config.Directories.Themes != ".polis/themes" {
		t.Errorf("Themes dir = %q, want %q", wk.Config.Directories.Themes, ".polis/themes")
	}
	if wk.Config.Directories.Versions != ".versions" {
		t.Errorf("Versions dir = %q, want %q", wk.Config.Directories.Versions, ".versions")
	}
	if wk.Config.Files.PublicIndex != "metadata/public.jsonl" {
		t.Errorf("PublicIndex = %q, want %q", wk.Config.Files.PublicIndex, "metadata/public.jsonl")
	}
	if wk.Config.Files.BlessedComments != "metadata/blessed-comments.json" {
		t.Errorf("BlessedComments = %q, want %q", wk.Config.Files.BlessedComments, "metadata/blessed-comments.json")
	}
	if wk.Config.Files.FollowingIndex != "metadata/following.json" {
		t.Errorf("FollowingIndex = %q, want %q", wk.Config.Files.FollowingIndex, "metadata/following.json")
	}

	// Verify metadata files exist
	expectedFiles := []string{
		"metadata/public.jsonl",
		"metadata/blessed-comments.json",
		"metadata/following.json",
		"metadata/manifest.json",
		".polis/webapp-config.json",
	}
	for _, f := range expectedFiles {
		path := filepath.Join(dir, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Expected file %s to exist: %v", f, err)
		}
	}

	// Verify keys exist
	if _, err := os.Stat(filepath.Join(dir, ".polis/keys/id_ed25519")); err != nil {
		t.Error("Private key should exist")
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis/keys/id_ed25519.pub")); err != nil {
		t.Error("Public key should exist")
	}

	// Verify result fields
	if result.KeyPaths.Private != ".polis/keys/id_ed25519" {
		t.Errorf("KeyPaths.Private = %q, want %q", result.KeyPaths.Private, ".polis/keys/id_ed25519")
	}
	if result.KeyPaths.Public != ".polis/keys/id_ed25519.pub" {
		t.Errorf("KeyPaths.Public = %q, want %q", result.KeyPaths.Public, ".polis/keys/id_ed25519.pub")
	}
	if len(result.DirsCreated) == 0 {
		t.Error("DirsCreated should not be empty")
	}
	if len(result.FilesCreated) == 0 {
		t.Error("FilesCreated should not be empty")
	}
}

func TestInit_CustomDirectories(t *testing.T) {
	dir := t.TempDir()

	opts := InitOptions{
		KeysDir:     "custom/keys",
		PostsDir:    "custom-posts",
		CommentsDir: "custom-comments",
		SnippetsDir: "custom-snippets",
		ThemesDir:   "custom/themes",
		VersionsDir: ".custom-versions",
	}

	result, err := Init(dir, opts)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	// Verify custom directories were created
	customDirs := []string{
		"custom/keys",
		"custom-posts",
		"custom-comments",
		"custom-snippets",
		"custom/themes",
	}
	for _, d := range customDirs {
		path := filepath.Join(dir, d)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("Expected directory %s to exist: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Expected %s to be a directory", d)
		}
	}

	// Verify keys are at custom location
	if _, err := os.Stat(filepath.Join(dir, "custom/keys/id_ed25519")); err != nil {
		t.Error("Private key should exist at custom path")
	}
	if _, err := os.Stat(filepath.Join(dir, "custom/keys/id_ed25519.pub")); err != nil {
		t.Error("Public key should exist at custom path")
	}

	// Verify .well-known/polis stores custom paths
	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.Config.Directories.Keys != "custom/keys" {
		t.Errorf("Keys dir = %q, want %q", wk.Config.Directories.Keys, "custom/keys")
	}
	if wk.Config.Directories.Posts != "custom-posts" {
		t.Errorf("Posts dir = %q, want %q", wk.Config.Directories.Posts, "custom-posts")
	}
	if wk.Config.Directories.Comments != "custom-comments" {
		t.Errorf("Comments dir = %q, want %q", wk.Config.Directories.Comments, "custom-comments")
	}
	if wk.Config.Directories.Snippets != "custom-snippets" {
		t.Errorf("Snippets dir = %q, want %q", wk.Config.Directories.Snippets, "custom-snippets")
	}
	if wk.Config.Directories.Themes != "custom/themes" {
		t.Errorf("Themes dir = %q, want %q", wk.Config.Directories.Themes, "custom/themes")
	}
	if wk.Config.Directories.Versions != ".custom-versions" {
		t.Errorf("Versions dir = %q, want %q", wk.Config.Directories.Versions, ".custom-versions")
	}

	// Verify result key paths
	if result.KeyPaths.Private != "custom/keys/id_ed25519" {
		t.Errorf("KeyPaths.Private = %q, want %q", result.KeyPaths.Private, "custom/keys/id_ed25519")
	}
}

func TestInit_CustomFilePaths(t *testing.T) {
	dir := t.TempDir()

	opts := InitOptions{
		PublicIndex:     "custom/public.jsonl",
		BlessedComments: "custom/blessed.json",
		FollowingIndex:  "custom/following.json",
	}

	result, err := Init(dir, opts)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	// Verify files exist at custom locations
	expectedFiles := []string{
		"custom/public.jsonl",
		"custom/blessed.json",
		"custom/following.json",
	}
	for _, f := range expectedFiles {
		path := filepath.Join(dir, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Expected file %s to exist: %v", f, err)
		}
	}

	// Verify .well-known/polis stores custom paths
	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.Config.Files.PublicIndex != "custom/public.jsonl" {
		t.Errorf("PublicIndex = %q, want %q", wk.Config.Files.PublicIndex, "custom/public.jsonl")
	}
	if wk.Config.Files.BlessedComments != "custom/blessed.json" {
		t.Errorf("BlessedComments = %q, want %q", wk.Config.Files.BlessedComments, "custom/blessed.json")
	}
	if wk.Config.Files.FollowingIndex != "custom/following.json" {
		t.Errorf("FollowingIndex = %q, want %q", wk.Config.Files.FollowingIndex, "custom/following.json")
	}

	// Verify following.json content is valid
	data, err := os.ReadFile(filepath.Join(dir, "custom/following.json"))
	if err != nil {
		t.Fatalf("Failed to read following.json: %v", err)
	}
	var following map[string]interface{}
	if err := json.Unmarshal(data, &following); err != nil {
		t.Fatalf("following.json is not valid JSON: %v", err)
	}
	if following["version"] != "polis-cli-go/dev" {
		t.Errorf("following.json version = %v, want %q", following["version"], "polis-cli-go/dev")
	}
}

func TestInit_SiteTitle(t *testing.T) {
	dir := t.TempDir()

	opts := InitOptions{
		SiteTitle: "My Custom Site",
	}

	result, err := Init(dir, opts)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.SiteTitle != "My Custom Site" {
		t.Errorf("SiteTitle = %q, want %q", wk.SiteTitle, "My Custom Site")
	}
}

func TestInit_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()

	// First init should succeed
	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("First init failed: %v", err)
	}

	// Second init should fail
	_, err = Init(dir, InitOptions{})
	if err == nil {
		t.Fatal("Second init should fail (refusing to overwrite)")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("Error should mention 'refusing to overwrite', got: %v", err)
	}
}

func TestInit_VersionPropagation(t *testing.T) {
	dir := t.TempDir()

	// Set the package Version so GetGenerator() returns the expected value
	oldVersion := Version
	defer func() { Version = oldVersion }()
	Version = "0.47.0"

	opts := InitOptions{
		Version: "0.47.0",
	}

	result, err := Init(dir, opts)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	expected := "polis-cli-go/0.47.0"

	// Check .well-known/polis version
	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.Version != expected {
		t.Errorf(".well-known/polis version = %q, want %q", wk.Version, expected)
	}

	// Check manifest.json version
	manifestData, err := os.ReadFile(filepath.Join(dir, "metadata", "manifest.json"))
	if err != nil {
		t.Fatalf("Failed to read manifest.json: %v", err)
	}
	var manifest map[string]interface{}
	json.Unmarshal(manifestData, &manifest)
	if manifest["version"] != expected {
		t.Errorf("manifest.json version = %v, want %q", manifest["version"], expected)
	}

	// Check following.json version
	followingData, err := os.ReadFile(filepath.Join(dir, "metadata", "following.json"))
	if err != nil {
		t.Fatalf("Failed to read following.json: %v", err)
	}
	var following map[string]interface{}
	json.Unmarshal(followingData, &following)
	if following["version"] != expected {
		t.Errorf("following.json version = %v, want %q", following["version"], expected)
	}

	// Check blessed-comments.json version
	blessedData, err := os.ReadFile(filepath.Join(dir, "metadata", "blessed-comments.json"))
	if err != nil {
		t.Fatalf("Failed to read blessed-comments.json: %v", err)
	}
	var blessed map[string]interface{}
	json.Unmarshal(blessedData, &blessed)
	if blessed["version"] != expected {
		t.Errorf("blessed-comments.json version = %v, want %q", blessed["version"], expected)
	}
}

func TestInit_DefaultVersion(t *testing.T) {
	dir := t.TempDir()

	// No Version specified → uses GetGenerator() with default "dev"
	oldVersion := Version
	defer func() { Version = oldVersion }()
	Version = "dev"

	result, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	expected := "polis-cli-go/dev"
	if wk.Version != expected {
		t.Errorf(".well-known/polis version = %q, want %q", wk.Version, expected)
	}
}

func TestInit_CreatesWebappConfig(t *testing.T) {
	dir := t.TempDir()

	result, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify file exists
	configPath := filepath.Join(dir, ".polis", "webapp-config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("webapp-config.json should exist: %v", err)
	}

	// Verify valid JSON with webapp-specific defaults (no discovery credentials)
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("webapp-config.json should be valid JSON: %v", err)
	}
	if obj["setup_at"] == nil || obj["setup_at"] == "" {
		t.Error("webapp-config.json should have setup_at")
	}
	if obj["view_mode"] != "list" {
		t.Errorf("view_mode = %v, want %q", obj["view_mode"], "list")
	}
	if obj["show_frontmatter"] != false {
		t.Errorf("show_frontmatter = %v, want false", obj["show_frontmatter"])
	}
	if _, ok := obj["discovery_url"]; ok {
		t.Error("discovery_url should not be in webapp-config.json (belongs in .env)")
	}
	if _, ok := obj["discovery_key"]; ok {
		t.Error("discovery_key should not be in webapp-config.json (belongs in .env)")
	}

	// Verify it's in FilesCreated
	found := false
	for _, f := range result.FilesCreated {
		if f == ".polis/webapp-config.json" {
			found = true
			break
		}
	}
	if !found {
		t.Error("webapp-config.json should be in FilesCreated")
	}
}

// ============================================================================
// Email Privacy Tests (Phase 0)
// ============================================================================

func TestInit_NoEmailByDefault(t *testing.T) {
	dir := t.TempDir()

	result, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}

	// Email should NOT be set by default
	if wk.Email != "" {
		t.Errorf("Email should be empty by default, got %q", wk.Email)
	}

	// Verify raw JSON doesn't contain email field
	data, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if strings.Contains(string(data), `"email"`) {
		t.Error("Raw JSON should not contain email field when not explicitly set")
	}
}

func TestInit_ExplicitEmailIsWritten(t *testing.T) {
	dir := t.TempDir()

	result, err := Init(dir, InitOptions{
		Email: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}

	if wk.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", wk.Email, "alice@example.com")
	}
}

func TestInit_DomainNotWritten(t *testing.T) {
	dir := t.TempDir()

	result, err := Init(dir, InitOptions{
		Author: "alice",
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}

	// Domain should not be written — it's derived from POLIS_BASE_URL at runtime
	if wk.Domain != "" {
		t.Errorf("Domain should be empty, got %q", wk.Domain)
	}
}

func TestInit_MetadataDirDerived(t *testing.T) {
	dir := t.TempDir()

	opts := InitOptions{
		PublicIndex: "custom/data/public.jsonl",
	}

	result, err := Init(dir, opts)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	// The metadata directory should be derived from the public index path
	metadataDir := filepath.Join(dir, "custom", "data")
	info, err := os.Stat(metadataDir)
	if err != nil {
		t.Fatalf("Metadata dir %s should exist: %v", metadataDir, err)
	}
	if !info.IsDir() {
		t.Errorf("Expected %s to be a directory", metadataDir)
	}

	// manifest.json should be in the derived metadata dir
	manifestPath := filepath.Join(metadataDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest.json should exist in derived metadata dir: %v", err)
	}
}

// ============================================================================
// About Snippet Tests
// ============================================================================

func TestInit_CreatesAboutSnippet(t *testing.T) {
	dir := t.TempDir()

	result, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	aboutPath := filepath.Join(dir, "snippets", "about.md")
	if _, err := os.Stat(aboutPath); err != nil {
		t.Fatalf("snippets/about.md should exist: %v", err)
	}

	// Verify it's in FilesCreated
	found := false
	for _, f := range result.FilesCreated {
		if f == "snippets/about.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("snippets/about.md should be in FilesCreated, got: %v", result.FilesCreated)
	}
}

func TestInit_DoesNotOverwriteAbout(t *testing.T) {
	dir := t.TempDir()

	// Pre-create the snippets dir and about.md with custom content
	snippetsDir := filepath.Join(dir, "snippets")
	os.MkdirAll(snippetsDir, 0755)
	customContent := "My custom about page\n"
	os.WriteFile(filepath.Join(snippetsDir, "about.md"), []byte(customContent), 0644)

	// Init will fail because keys don't exist yet — but we test the re-init scenario
	// by checking that our pre-created about.md survives init
	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(snippetsDir, "about.md"))
	if err != nil {
		t.Fatalf("about.md should still exist: %v", err)
	}
	if string(data) != customContent {
		t.Errorf("about.md content = %q, want %q (should not overwrite)", string(data), customContent)
	}
}

func TestInit_AboutSnippetContent(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "snippets", "about.md"))
	if err != nil {
		t.Fatalf("about.md should exist: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "polis") {
		t.Error("about.md should mention 'polis'")
	}
	if content == "" {
		t.Error("about.md should not be empty")
	}
}
