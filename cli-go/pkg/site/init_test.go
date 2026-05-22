package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
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
		".polis/bundles/pub.polis.core/posts/drafts",
		".polis/bundles/pub.polis.core/comments/drafts",
		".polis/bundles/pub.polis.core/comments/pending",
		".polis/bundles/pub.polis.core/comments/denied",
		".polis/bundles/pub.polis.core/dm/conv",
		"site/snippets",
		"content/pub.polis.core/post",
		"content/pub.polis.core/comment",
		"content/pub.polis.core/follow",
		"content/pub.polis.core/feed",
		"content/pub.polis.core/tag",
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
	// Listing-is-activation: presence in Bundles map means the bundle is
	// active. The pre-cleanup `Active bool` field was removed in C4.
	if core.Path != "content/pub.polis.core/bundle.json" {
		t.Errorf("Bundle path = %q, want content/pub.polis.core/bundle.json", core.Path)
	}

	// Active theme is picked at init (random selection from declared bundle
	// themes) and persisted in the registry — no half-initialized state
	// between init and first render.
	_ = wk
	activeTheme := GetActiveTheme(dir)
	if activeTheme == "" {
		t.Error("active theme should be set after init (random pick from bundle themes)")
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

	// Verify favicon.svg was generated from avatar config
	faviconData, err := os.ReadFile(filepath.Join(dir, "favicon.svg"))
	if err != nil {
		t.Error("favicon.svg should be created during init")
	} else {
		svg := string(faviconData)
		if !strings.Contains(svg, `viewBox="0 0 128 128"`) {
			t.Error("favicon.svg should be a valid SVG")
		}
		if wk.Avatar != nil && !strings.Contains(svg, wk.Avatar.BG) {
			t.Error("favicon.svg should use avatar background color")
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
	if len(types) != 7 {
		t.Errorf("types count = %d, want 7", len(types))
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

// TestInit_DefaultSiteTitleFromAuthor verifies that an empty SiteTitle
// falls back to the author name. Without this default, fresh-init tenants
// emit <title></title> + a leading-space artifact on rendered pages.
func TestInit_DefaultSiteTitleFromAuthor(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{Author: "Alice"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.SiteTitle != "Alice" {
		t.Errorf("SiteTitle = %q, want default-from-author Alice", wk.SiteTitle)
	}
}

func TestInit_CustomTheme(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{Theme: "turbo"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if _, err := LoadWellKnown(dir); err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	// Active theme now lives in the registry, not well-known.
	activeTheme := GetActiveTheme(dir)
	if activeTheme != "turbo" {
		t.Errorf("active theme = %q, want turbo", activeTheme)
	}
}

// TestInit_SelectsRandomThemeByDefault verifies that polis init with no
// theme specified picks one from the declared bundle themes and persists
// it into the registry — no half-initialized state between init and first
// render. See cleanup C2.
func TestInit_SelectsRandomThemeByDefault(t *testing.T) {
	dir := t.TempDir()

	if _, err := Init(dir, InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	activeTheme := GetActiveTheme(dir)
	if activeTheme == "" {
		t.Fatal("active theme should be picked at init, got empty")
	}
	// Selected theme must be one declared in the bundle.
	declared := bundle.DefaultCoreBundle().Themes
	if _, ok := declared[activeTheme]; !ok {
		t.Errorf("active theme %q is not declared in the bundle", activeTheme)
	}
}

// TestInit_PreservesVersionStamps verifies that polis init does not clobber
// the per-shape/per-theme version stamps that bundle.EnsureReferencePayload
// records into registry.json. Without these stamps, Patrol's NeedsRefresh
// check is broken (returns true forever; see cleanup C3 for the bug history).
func TestInit_PreservesVersionStamps(t *testing.T) {
	dir := t.TempDir()

	if _, err := Init(dir, InitOptions{}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	reg, err := bundle.LoadRegistry(dir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	entry := reg.FindInstalledBundle("pub.polis.core")
	if entry == nil {
		t.Fatal("registry should have a pub.polis.core entry after init")
	}

	defaults := bundle.DefaultCoreBundle()
	for name, sh := range defaults.Shapes {
		if got, want := entry.ShapeVersions[name], sh.Version; got != want {
			t.Errorf("shape %q version stamp = %q, want %q", name, got, want)
		}
	}
	for name, th := range defaults.Themes {
		if got, want := entry.ThemeVersions[name], th.Version; got != want {
			t.Errorf("theme %q version stamp = %q, want %q", name, got, want)
		}
	}

	// Sanity: NeedsRefresh should be false on a freshly-initialized site.
	if reg.NeedsRefresh(defaults) {
		t.Error("NeedsRefresh should be false after init (stamps just recorded)")
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
	if _, hasViewMode := obj["view_mode"]; hasViewMode {
		t.Errorf("view_mode should not be set in new configs")
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
	for _, expected := range []string{".polis/", ".env*", "/polis", "/polis-server", "/polis-full"} {
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
	dmConvDir := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "dm", "conv")
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

	// Private policies must exist (empty template — overrides only)
	privatePolicyData, err := os.ReadFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"))
	if err != nil {
		t.Fatalf("Private policies should exist: %v", err)
	}
	if !strings.Contains(string(privatePolicyData), "version") {
		t.Error("Private policies should contain version header")
	}

	// Public policies must exist and include DM rules + catch-all
	publicPolicyData, err := os.ReadFile(filepath.Join(dir, "policies", "rules.jsonl"))
	if err != nil {
		t.Fatalf("Public policies should exist: %v", err)
	}
	if !strings.Contains(string(publicPolicyData), "pub.polis.dm") {
		t.Error("Public policies should contain DM rules")
	}
	if !strings.Contains(string(publicPolicyData), "deny all from all") {
		t.Error("Public policies should contain deny-all catch-all")
	}
}

func TestInit_CreatesDefaultAvatar(t *testing.T) {
	dir := t.TempDir()

	_, err := Init(dir, InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatalf("Failed to load .well-known/polis: %v", err)
	}
	if wk.Avatar == nil {
		t.Fatal("Avatar should not be nil after init")
	}
	if len(wk.Avatar.BG) != 7 || wk.Avatar.BG[0] != '#' {
		t.Errorf("Avatar.BG should be #rrggbb, got %q", wk.Avatar.BG)
	}
	if wk.Avatar.FG != "#ffffff" {
		t.Errorf("Avatar.FG = %q, want #ffffff", wk.Avatar.FG)
	}
	if ratio := ContrastRatio(wk.Avatar.BG, wk.Avatar.FG); ratio < 4.5 {
		t.Errorf("Avatar contrast ratio %.2f < 4.5", ratio)
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
