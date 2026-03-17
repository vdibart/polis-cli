package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_BundleLayout(t *testing.T) {
	dir := t.TempDir()

	result, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	// Verify new directory structure
	expectedDirs := []string{
		".polis/keys",
		".polis/logs",
		".polis/policies",
		".polis/webapp",
		".polis/webapp/hooks",
		".polis/content/pub.polis.core/posts/drafts",
		".polis/content/pub.polis.core/comments/drafts",
		".polis/content/pub.polis.core/comments/pending",
		".polis/content/pub.polis.core/comments/denied",
		".polis/content/pub.polis.core/dm/conv",
		"site/snippets",
		"site/themes",
		"content/pub.polis.core/post",
		"content/pub.polis.core/comment",
		"content/pub.polis.core/follow",
		"content/pub.polis.core/feed",
		"policies",
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

	// Verify .well-known/polis has bundle registry
	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if len(wk.Bundles) != 1 {
		t.Fatalf("Bundles count = %d, want 1", len(wk.Bundles))
	}
	core := wk.Bundles["pub.polis.core"]
	if !core.Active {
		t.Error("pub.polis.core should be active")
	}
	if core.Path != "content/pub.polis.core/bundle.json" {
		t.Errorf("Bundle path = %q, want content/pub.polis.core/bundle.json", core.Path)
	}

	// Verify default theme is empty (selected randomly on first render)
	if wk.ActiveTheme != "" {
		t.Errorf("ActiveTheme = %q, want empty (deferred to first render)", wk.ActiveTheme)
	}

	// Verify content files exist
	expectedFiles := []string{
		"content/pub.polis.core/bundle.json",
		"content/pub.polis.core/index.jsonl",
		"content/pub.polis.core/follow/following.json",
		"content/pub.polis.core/comment/blessed.json",
		".polis/webapp/config.json",
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
		t.Errorf("KeyPaths.Private = %q", result.KeyPaths.Private)
	}
	if result.KeyPaths.Public != ".polis/keys/id_ed25519.pub" {
		t.Errorf("KeyPaths.Public = %q", result.KeyPaths.Public)
	}
}

func TestInit_BundleJsonIsValid(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Read and validate bundle.json
	data, err := os.ReadFile(filepath.Join(dir, "content", "pub.polis.core", "bundle.json"))
	if err != nil {
		t.Fatalf("Failed to read bundle.json: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("bundle.json is not valid JSON: %v", err)
	}

	if raw["name"] != "pub.polis.core" {
		t.Errorf("name = %v, want pub.polis.core", raw["name"])
	}
	if raw["version"] != "1.0.0" {
		t.Errorf("version = %v, want 1.0.0", raw["version"])
	}

	handler, _ := raw["handler"].(map[string]interface{})
	if handler["type"] != "builtin" {
		t.Errorf("handler.type = %v, want builtin", handler["type"])
	}

	types, _ := raw["types"].(map[string]interface{})
	if len(types) != 5 {
		t.Errorf("types count = %d, want 5", len(types))
	}
}

func TestInit_SiteTitle(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{SiteTitle: "My Custom Site"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.SiteTitle != "My Custom Site" {
		t.Errorf("SiteTitle = %q, want My Custom Site", wk.SiteTitle)
	}
}

func TestInit_CustomTheme(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{Theme: "turbo"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.ActiveTheme != "turbo" {
		t.Errorf("ActiveTheme = %q, want turbo", wk.ActiveTheme)
	}
}

func TestInit_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("First init failed: %v", err)
	}

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

	oldVersion := Version
	defer func() { Version = oldVersion }()
	Version = "0.57.0"

	result, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if !result.Success {
		t.Fatal("Init should succeed")
	}

	expected := "polis-cli-go/0.57.0"

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.Version != expected {
		t.Errorf(".well-known/polis version = %q, want %q", wk.Version, expected)
	}

	// Check following.json version
	followingData, err := os.ReadFile(filepath.Join(dir, "content", "pub.polis.core", "follow", "following.json"))
	if err != nil {
		t.Fatalf("Failed to read following.json: %v", err)
	}
	var following map[string]interface{}
	json.Unmarshal(followingData, &following)
	if following["version"] != expected {
		t.Errorf("following.json version = %v, want %q", following["version"], expected)
	}

	// Check blessed.json version
	blessedData, err := os.ReadFile(filepath.Join(dir, "content", "pub.polis.core", "comment", "blessed.json"))
	if err != nil {
		t.Fatalf("Failed to read blessed.json: %v", err)
	}
	var blessed map[string]interface{}
	json.Unmarshal(blessedData, &blessed)
	if blessed["version"] != expected {
		t.Errorf("blessed.json version = %v, want %q", blessed["version"], expected)
	}
}

func TestInit_CreatesWebappConfig(t *testing.T) {
	dir := t.TempDir()

	result, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	configPath := filepath.Join(dir, ".polis", "webapp", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("webapp config should exist: %v", err)
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("webapp config should be valid JSON: %v", err)
	}
	if obj["view_mode"] != "list" {
		t.Errorf("view_mode = %v, want list", obj["view_mode"])
	}
	if obj["show_frontmatter"] != false {
		t.Errorf("show_frontmatter = %v, want false", obj["show_frontmatter"])
	}

	found := false
	for _, f := range result.FilesCreated {
		if f == ".polis/webapp/config.json" {
			found = true
			break
		}
	}
	if !found {
		t.Error("webapp config should be in FilesCreated")
	}
}

func TestInit_NoEmailByDefault(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.Email != "" {
		t.Errorf("Email should be empty by default, got %q", wk.Email)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if strings.Contains(string(data), `"email"`) {
		t.Error("Raw JSON should not contain email field when not explicitly set")
	}
}

func TestInit_ExplicitEmailIsWritten(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{Email: "alice@example.com"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", wk.Email)
	}
}

func TestInit_CreatesAboutSnippet(t *testing.T) {
	dir := t.TempDir()

	result, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	aboutPath := filepath.Join(dir, "site", "snippets", "about.md")
	if _, err := os.Stat(aboutPath); err != nil {
		t.Fatalf("site/snippets/about.md should exist: %v", err)
	}

	found := false
	for _, f := range result.FilesCreated {
		if f == "site/snippets/about.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("site/snippets/about.md should be in FilesCreated, got: %v", result.FilesCreated)
	}
}

func TestInit_GitignoreContents(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	content := string(data)
	for _, expected := range []string{".polis/", ".env*", "/site/themes/", "/polis", "/polis-server", "/polis-full"} {
		if !strings.Contains(content, expected) {
			t.Errorf(".gitignore should contain %q", expected)
		}
	}
}

func TestInit_EmptyIndexFile(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	indexPath := filepath.Join(dir, "content", "pub.polis.core", "index.jsonl")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("index.jsonl should exist: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("index.jsonl should be empty initially, got %d bytes", len(data))
	}
}

func TestInit_PassesAllPatrolChecks(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// DM directories must exist with correct permissions
	dmConvDir := filepath.Join(dir, ".polis", "content", "pub.polis.core", "dm", "conv")
	info, err := os.Stat(dmConvDir)
	if err != nil {
		t.Fatalf("DM conv directory should exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("DM conv path should be a directory")
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Errorf("DM conv directory permissions = %04o, want 0700", perm)
	}

	// Storage salt must exist
	saltPath := filepath.Join(dir, ".polis", "storage-salt")
	saltData, err := os.ReadFile(saltPath)
	if err != nil {
		t.Fatalf("Storage salt should exist: %v", err)
	}
	if len(saltData) != 64 { // 32 bytes hex-encoded
		t.Errorf("Storage salt should be 64 hex chars, got %d", len(saltData))
	}
	saltInfo, _ := os.Stat(saltPath)
	if perm := saltInfo.Mode().Perm(); perm != 0600 {
		t.Errorf("Storage salt permissions = %04o, want 0600", perm)
	}

	// Private policies must include DM rules
	privatePolicyData, err := os.ReadFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"))
	if err != nil {
		t.Fatalf("Private policies should exist: %v", err)
	}
	if !strings.Contains(string(privatePolicyData), "pub.polis.dm") {
		t.Error("Private policies should contain DM rules")
	}

	// Public policies must exist
	if _, err := os.Stat(filepath.Join(dir, "policies", "rules.jsonl")); err != nil {
		t.Fatalf("Public policies should exist: %v", err)
	}
}

func TestInit_OldDirsNotCreated(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Old layout dirs should NOT exist
	oldDirs := []string{
		"posts",
		"comments",
		"snippets",
		"metadata",
		".polis/themes",
		".polis/posts",
		".polis/comments",
	}
	for _, d := range oldDirs {
		path := filepath.Join(dir, d)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("Old directory %s should NOT exist in new layout", d)
		}
	}
}
