package tailor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createOldSite creates a pre-bundle polis site (v0.42.0 era) for testing.
func createOldSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// .well-known/polis with old format
	wkDir := filepath.Join(dir, ".well-known")
	os.MkdirAll(wkDir, 0755)
	wk := map[string]interface{}{
		"version":    "0.42.0",
		"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest",
		"author":     "testuser",
		"created":    "2026-02-27T00:00:00Z",
		"config": map[string]interface{}{
			"directories": map[string]string{
				"posts":    "posts",
				"comments": "comments",
			},
		},
		"domain":   "test.example.com",
		"base_url": "https://test.example.com",
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(wkDir, "polis"), data, 0644)

	// .polis/keys (so it's a valid site)
	os.MkdirAll(filepath.Join(dir, ".polis", "keys"), 0700)
	os.WriteFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"), []byte("fake-private-key"), 0600)
	os.WriteFile(filepath.Join(dir, ".polis", "keys", "id_ed25519.pub"), []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest"), 0644)

	// Old-style posts in posts/ directory
	for _, dateDir := range []string{"20260227", "20260314"} {
		postDir := filepath.Join(dir, "posts", dateDir)
		os.MkdirAll(postDir, 0755)

		content := "---\ntitle: Test Post in " + dateDir + "\npublished: 2026-02-27T12:00:00Z\ncurrent-version: sha256:abc123\n---\n\nHello world\n"
		os.WriteFile(filepath.Join(postDir, "test-post.md"), []byte(content), 0644)

		// HTML file (should NOT be moved)
		os.WriteFile(filepath.Join(postDir, "test-post.html"), []byte("<html>rendered</html>"), 0644)

		// .versions dir
		versionsDir := filepath.Join(postDir, ".versions")
		os.MkdirAll(versionsDir, 0755)
		os.WriteFile(filepath.Join(versionsDir, "test-post.md"), []byte("old version"), 0644)
	}

	// Old-style metadata
	os.MkdirAll(filepath.Join(dir, "metadata"), 0755)
	following := `{"version": "0.42.0", "following": []}`
	os.WriteFile(filepath.Join(dir, "metadata", "following.json"), []byte(following), 0644)

	blessed := `{"version": "0.42.0", "comments": []}`
	os.WriteFile(filepath.Join(dir, "metadata", "blessed-comments.json"), []byte(blessed), 0644)

	manifest := `{"post_count": 2, "theme": "sols"}`
	os.WriteFile(filepath.Join(dir, "metadata", "manifest.json"), []byte(manifest), 0644)

	// Old-style webapp config
	os.MkdirAll(filepath.Join(dir, ".polis"), 0700)
	os.WriteFile(filepath.Join(dir, ".polis", "webapp-config.json"), []byte(`{"view_mode": "list"}`), 0644)

	return dir
}

// createCurrentSite creates an up-to-date polis site for testing.
func createCurrentSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// .well-known/polis with current format
	wkDir := filepath.Join(dir, ".well-known")
	os.MkdirAll(wkDir, 0755)
	wk := map[string]interface{}{
		"version":    "polis-cli-go/0.59.0",
		"public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest",
		"author":     "testuser",
		"created":    "2026-02-27T00:00:00Z",
		"avatar": map[string]interface{}{
			"bg": "#2a5a6a",
			"fg": "#ffffff",
		},
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]interface{}{
				"active": true,
				"path":   "content/pub.polis.core/bundle.json",
			},
		},
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(wkDir, "polis"), data, 0644)

	// .polis/keys
	os.MkdirAll(filepath.Join(dir, ".polis", "keys"), 0700)
	os.WriteFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"), []byte("fake-private-key"), 0600)
	os.WriteFile(filepath.Join(dir, ".polis", "keys", "id_ed25519.pub"), []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest"), 0644)

	// Bundle layout
	os.MkdirAll(filepath.Join(dir, "content", "pub.polis.core", "post"), 0755)
	os.MkdirAll(filepath.Join(dir, "content", "pub.polis.core", "comment"), 0755)
	os.MkdirAll(filepath.Join(dir, "content", "pub.polis.core", "follow"), 0755)
	os.MkdirAll(filepath.Join(dir, "content", "pub.polis.core", "tag"), 0755)

	// Bundle.json
	bundleJSON := `{"name": "pub.polis.core", "version": "1.0.0", "description": "Core polis content types", "handler": {"type": "builtin"}, "types": {"pub.polis.post": {"dir": "post"}, "pub.polis.comment": {"dir": "comment"}, "pub.polis.follow": {"dir": "follow"}, "pub.polis.feed": {"dir": "feed"}, "pub.polis.dm": {"dir": "dm"}, "pub.polis.tag": {"dir": "tag"}}}`
	os.WriteFile(filepath.Join(dir, "content", "pub.polis.core", "bundle.json"), []byte(bundleJSON), 0644)

	// Index
	os.WriteFile(filepath.Join(dir, "content", "pub.polis.core", "index.jsonl"), []byte{}, 0644)

	// Following and blessed
	os.WriteFile(filepath.Join(dir, "content", "pub.polis.core", "follow", "following.json"),
		[]byte(`{"version": "polis-cli-go/0.59.0", "following": []}`), 0644)
	os.WriteFile(filepath.Join(dir, "content", "pub.polis.core", "comment", "blessed.json"),
		[]byte(`{"version": "polis-cli-go/0.59.0", "comments": []}`), 0644)

	// Policies (with required rules)
	os.MkdirAll(filepath.Join(dir, "policies"), 0755)
	os.WriteFile(filepath.Join(dir, "policies", "rules.jsonl"), []byte(`{"version":1}`+"\n"), 0644)
	os.MkdirAll(filepath.Join(dir, ".polis", "policies"), 0700)
	privatePolicies := `{"version":1}` + "\n" +
		`{"active":true,"policy":"omit pub.polis.feed from self"}` + "\n" +
		`{"active":true,"policy":"deny all from all"}` + "\n"
	os.WriteFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"), []byte(privatePolicies), 0600)

	// Storage salt
	os.WriteFile(filepath.Join(dir, ".polis", "storage-salt"), []byte("abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"), 0600)

	// DM directories
	os.MkdirAll(filepath.Join(dir, ".polis", "content", "pub.polis.core", "dm", "conv"), 0700)

	// Webapp config (no deprecated view_mode)
	os.MkdirAll(filepath.Join(dir, ".polis", "webapp"), 0700)
	os.WriteFile(filepath.Join(dir, ".polis", "webapp", "config.json"), []byte(`{}`), 0644)

	return dir
}

func TestDiagnoseOldSite(t *testing.T) {
	Version = "0.59.0"
	dir := createOldSite(t)
	result := Diagnose(dir)

	if result.Mode != "diagnose" {
		t.Errorf("expected mode=diagnose, got %s", result.Mode)
	}

	// Should find multiple issues
	if result.Summary.Failed == 0 {
		t.Error("expected failures on old site, got 0")
	}

	// Verify specific checks
	checkMap := make(map[string]CheckResult)
	for _, cr := range result.Checks {
		checkMap[cr.Name] = cr
	}

	// wellknown-version should fail (bare semver)
	if cr, ok := checkMap["wellknown-version"]; ok {
		if cr.Status != StatusFail {
			t.Errorf("wellknown-version: expected fail, got %s: %s", cr.Status, cr.Message)
		}
	} else {
		t.Error("wellknown-version check not found")
	}

	// wellknown-legacy-config should fail (has config, domain, base_url)
	if cr, ok := checkMap["wellknown-legacy-config"]; ok {
		if cr.Status != StatusFail {
			t.Errorf("wellknown-legacy-config: expected fail, got %s: %s", cr.Status, cr.Message)
		}
	}

	// wellknown-bundles should fail (no bundles)
	if cr, ok := checkMap["wellknown-bundles"]; ok {
		if cr.Status != StatusFail {
			t.Errorf("wellknown-bundles: expected fail, got %s: %s", cr.Status, cr.Message)
		}
	}

	// bundle-json should fail (missing)
	if cr, ok := checkMap["bundle-json"]; ok {
		if cr.Status != StatusFail {
			t.Errorf("bundle-json: expected fail, got %s: %s", cr.Status, cr.Message)
		}
	}

	// layout-posts should fail (2 posts in old location)
	if cr, ok := checkMap["layout-posts"]; ok {
		if cr.Status != StatusFail {
			t.Errorf("layout-posts: expected fail, got %s: %s", cr.Status, cr.Message)
		}
		if len(cr.Actions) == 0 {
			t.Error("layout-posts: expected actions")
		}
	}

	// layout-following should fail
	if cr, ok := checkMap["layout-following"]; ok {
		if cr.Status != StatusFail {
			t.Errorf("layout-following: expected fail, got %s: %s", cr.Status, cr.Message)
		}
	}

	// layout-blessed should fail
	if cr, ok := checkMap["layout-blessed"]; ok {
		if cr.Status != StatusFail {
			t.Errorf("layout-blessed: expected fail, got %s: %s", cr.Status, cr.Message)
		}
	}

	// manifest-obsolete should fail
	if cr, ok := checkMap["manifest-obsolete"]; ok {
		if cr.Status != StatusFail {
			t.Errorf("manifest-obsolete: expected fail, got %s: %s", cr.Status, cr.Message)
		}
	}

	// webapp-config should fail
	if cr, ok := checkMap["webapp-config"]; ok {
		if cr.Status != StatusFail {
			t.Errorf("webapp-config: expected fail, got %s: %s", cr.Status, cr.Message)
		}
	}

	// Diagnose should not modify any files
	// Check .well-known/polis is unchanged
	data, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if v, _ := raw["version"].(string); v != "0.42.0" {
		t.Errorf("Diagnose modified .well-known/polis version: got %s, want 0.42.0", v)
	}
	// Check posts are still in old location
	if !fileExists(filepath.Join(dir, "posts", "20260227", "test-post.md")) {
		t.Error("Diagnose moved posts — should be dry-run only")
	}
}

func TestApplyOldSite(t *testing.T) {
	Version = "0.59.0"
	dir := createOldSite(t)
	result := Apply(dir)

	if result.Mode != "apply" {
		t.Errorf("expected mode=apply, got %s", result.Mode)
	}

	if result.BackupDir == "" {
		t.Error("expected backup directory to be set")
	}

	// Verify .well-known/polis was updated
	data, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	// Version should be updated
	if v, _ := raw["version"].(string); v != "polis-cli-go/0.59.0" {
		t.Errorf("version not updated: got %s", v)
	}

	// Legacy keys should be removed
	for _, key := range []string{"config", "domain", "base_url"} {
		if _, exists := raw[key]; exists {
			t.Errorf("legacy key %q should have been removed", key)
		}
	}

	// Bundles should be present
	if bundles, ok := raw["bundles"].(map[string]interface{}); !ok || len(bundles) == 0 {
		t.Error("bundles registry should have been added")
	}

	// bundle.json should exist
	if !fileExists(filepath.Join(dir, "content", "pub.polis.core", "bundle.json")) {
		t.Error("bundle.json should have been created")
	}

	// Posts should be moved to new location
	if !fileExists(filepath.Join(dir, "content", "pub.polis.core", "post", "20260227", "test-post.md")) {
		t.Error("post should have been moved to content/pub.polis.core/post/")
	}
	if !fileExists(filepath.Join(dir, "content", "pub.polis.core", "post", "20260314", "test-post.md")) {
		t.Error("post should have been moved to content/pub.polis.core/post/")
	}

	// .versions should be moved
	if !fileExists(filepath.Join(dir, "content", "pub.polis.core", "post", "20260227", ".versions", "test-post.md")) {
		t.Error(".versions should have been moved")
	}

	// HTML should still be at mount point
	if !fileExists(filepath.Join(dir, "posts", "20260227", "test-post.html")) {
		t.Error("HTML should remain at posts/ mount point")
	}

	// following.json should be in new location
	if !fileExists(filepath.Join(dir, "content", "pub.polis.core", "follow", "following.json")) {
		t.Error("following.json should have been moved")
	}

	// blessed.json should be in new location
	if !fileExists(filepath.Join(dir, "content", "pub.polis.core", "comment", "blessed.json")) {
		t.Error("blessed.json should have been moved")
	}

	// manifest.json should be removed
	if fileExists(filepath.Join(dir, "metadata", "manifest.json")) {
		t.Error("manifest.json should have been removed")
	}

	// webapp-config.json should be moved
	if fileExists(filepath.Join(dir, ".polis", "webapp-config.json")) {
		t.Error("webapp-config.json should have been moved")
	}
	if !fileExists(filepath.Join(dir, ".polis", "webapp", "config.json")) {
		t.Error("webapp config should be at .polis/webapp/config.json")
	}

	// Policies should be created
	if !fileExists(filepath.Join(dir, "policies", "rules.jsonl")) {
		t.Error("public policy should have been created")
	}
	if !fileExists(filepath.Join(dir, ".polis", "policies", "rules.jsonl")) {
		t.Error("private policy should have been created")
	}

	// Index should be rebuilt
	if !fileExists(filepath.Join(dir, "content", "pub.polis.core", "index.jsonl")) {
		t.Error("index.jsonl should have been created")
	}
	indexData, _ := os.ReadFile(filepath.Join(dir, "content", "pub.polis.core", "index.jsonl"))
	indexLines := strings.Split(strings.TrimSpace(string(indexData)), "\n")
	if len(indexLines) != 2 {
		t.Errorf("index should have 2 entries, got %d", len(indexLines))
	}
}

func TestApplyIdempotent(t *testing.T) {
	Version = "0.59.0"
	dir := createOldSite(t)

	// First apply
	Apply(dir)

	// Second apply — should be all pass (except cli-update which requires network)
	result := Diagnose(dir)

	for _, cr := range result.Checks {
		// cli-update requires network; render-site requires a theme (not in test fixtures)
		if cr.Status == StatusFail && cr.Name != "cli-update" && cr.Name != "render-site" {
			t.Errorf("second run: unexpected failure: %s: %s", cr.Name, cr.Message)
		}
	}
}

func TestDiagnoseCurrentSite(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)
	result := Diagnose(dir)

	// Current site should pass everything (cli-update requires network; render-site requires a theme)
	for _, cr := range result.Checks {
		if cr.Status == StatusFail && cr.Name != "cli-update" && cr.Name != "render-site" {
			t.Errorf("unexpected failure on current site: %s: %s", cr.Name, cr.Message)
		}
	}
}

func TestMissingWellKnown(t *testing.T) {
	dir := t.TempDir()
	result := Diagnose(dir)

	if result.Summary.Total != 1 {
		t.Errorf("expected 1 check, got %d", result.Summary.Total)
	}
	if result.Summary.Skipped != 1 {
		t.Errorf("expected 1 skip, got %d", result.Summary.Skipped)
	}
}

func TestCheckPathSeparators(t *testing.T) {
	dir := t.TempDir()

	// Create index.jsonl with backslash paths
	indexDir := filepath.Join(dir, "content", "pub.polis.core")
	os.MkdirAll(indexDir, 0755)
	content := `{"type":"post","path":"content\\pub.polis.core\\post\\20260227\\test.md","title":"Test"}` + "\n"
	os.WriteFile(filepath.Join(indexDir, "index.jsonl"), []byte(content), 0644)

	result := checkPathSeparators(&runContext{siteDir: dir, dryRun: false})

	if result.Status != StatusFail {
		t.Errorf("expected fail for backslash paths, got %s: %s", result.Status, result.Message)
	}

	// Verify fix
	fixed, _ := os.ReadFile(filepath.Join(indexDir, "index.jsonl"))
	if strings.Contains(string(fixed), "\\\\") {
		t.Error("backslash paths should have been fixed")
	}
}

func TestParseFrontmatter(t *testing.T) {
	content := `---
title: Hello World
published: 2026-02-27T12:00:00Z
current-version: sha256:abc123
version-history:
  - sha256:abc123 (2026-02-27T12:00:00Z)
signature: base64data==
---

Body content here.
`
	fm := parseFrontmatter(content)

	if fm["title"] != "Hello World" {
		t.Errorf("title: got %q", fm["title"])
	}
	if fm["published"] != "2026-02-27T12:00:00Z" {
		t.Errorf("published: got %q", fm["published"])
	}
	if fm["current-version"] != "sha256:abc123" {
		t.Errorf("current-version: got %q", fm["current-version"])
	}
	// version-history entries (indented) should be skipped
	if _, ok := fm["- sha256"]; ok {
		t.Error("indented version-history entries should be skipped")
	}
}

func TestStripFrontmatter(t *testing.T) {
	content := "---\ntitle: Test\n---\n\nBody here.\n"
	body := stripFrontmatter(content)
	if !strings.HasPrefix(body, "\nBody here.") {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestPartialMigration(t *testing.T) {
	Version = "0.59.0"
	dir := createOldSite(t)

	// Manually move one post to simulate partial migration
	newPostDir := filepath.Join(dir, "content", "pub.polis.core", "post", "20260227")
	os.MkdirAll(newPostDir, 0755)
	os.Rename(
		filepath.Join(dir, "posts", "20260227", "test-post.md"),
		filepath.Join(newPostDir, "test-post.md"),
	)

	result := Diagnose(dir)

	checkMap := make(map[string]CheckResult)
	for _, cr := range result.Checks {
		checkMap[cr.Name] = cr
	}

	// layout-posts should still fail for the remaining post
	cr := checkMap["layout-posts"]
	if cr.Status != StatusFail {
		t.Errorf("layout-posts: expected fail (partial migration), got %s", cr.Status)
	}

	// Count of .md move actions should be 1 (only the remaining post)
	mdMoves := 0
	for _, a := range cr.Actions {
		if a.Op == "move" && strings.HasSuffix(a.Path, ".md") {
			mdMoves++
		}
	}
	if mdMoves != 1 {
		t.Errorf("expected 1 md move action, got %d", mdMoves)
	}
}

func TestJSONOutput(t *testing.T) {
	Version = "0.59.0"
	dir := createOldSite(t)
	result := Diagnose(dir)

	// Verify result serializes to valid JSON
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	// Verify it round-trips
	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if decoded.Mode != "diagnose" {
		t.Errorf("mode: got %s", decoded.Mode)
	}
	if len(decoded.Checks) == 0 {
		t.Error("expected checks in decoded result")
	}
}

func TestBackupCreated(t *testing.T) {
	Version = "0.59.0"
	dir := createOldSite(t)
	result := Apply(dir)

	if result.BackupDir == "" {
		t.Fatal("expected backup directory")
	}

	if !dirExists(result.BackupDir) {
		t.Error("backup directory should exist")
	}
}

func TestTagDirectory_Diagnose(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)

	// Remove the tag directory to trigger the check
	os.Remove(filepath.Join(dir, "content", "pub.polis.core", "tag"))

	result := Diagnose(dir)

	checkMap := make(map[string]CheckResult)
	for _, cr := range result.Checks {
		checkMap[cr.Name] = cr
	}

	cr, ok := checkMap["tag-directory"]
	if !ok {
		t.Fatal("tag-directory check not found")
	}
	if cr.Status != StatusFail {
		t.Errorf("tag-directory: expected fail, got %s: %s", cr.Status, cr.Message)
	}

	// Diagnose should not create the directory
	if dirExists(filepath.Join(dir, "content", "pub.polis.core", "tag")) {
		t.Error("Diagnose should not create tag directory")
	}
}

func TestTagDirectory_Apply(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)

	// Remove the tag directory to trigger the check
	os.Remove(filepath.Join(dir, "content", "pub.polis.core", "tag"))

	result := Apply(dir)

	checkMap := make(map[string]CheckResult)
	for _, cr := range result.Checks {
		checkMap[cr.Name] = cr
	}

	cr, ok := checkMap["tag-directory"]
	if !ok {
		t.Fatal("tag-directory check not found")
	}
	if cr.Status != StatusFail {
		t.Errorf("tag-directory: expected fail (created), got %s: %s", cr.Status, cr.Message)
	}

	// Apply should create the directory
	if !dirExists(filepath.Join(dir, "content", "pub.polis.core", "tag")) {
		t.Error("Apply should create tag directory")
	}
}

func TestThemeConsolidation_RemovesStaleFiles(t *testing.T) {
	dir := createOldSite(t)

	// Create _base with a template
	baseDir := filepath.Join(dir, "site", "themes", "_base")
	os.MkdirAll(filepath.Join(baseDir, "snippets"), 0755)
	baseContent := "<html>BASE</html>"
	os.WriteFile(filepath.Join(baseDir, "index.html"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(baseDir, "snippets", "about.html"), []byte("<div>BASE</div>"), 0644)

	// Create a theme with stale + custom files
	themeDir := filepath.Join(dir, "site", "themes", "turbo")
	os.MkdirAll(filepath.Join(themeDir, "snippets"), 0755)
	os.WriteFile(filepath.Join(themeDir, "turbo.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(themeDir, "index.html"), []byte(baseContent), 0644)     // stale
	os.WriteFile(filepath.Join(themeDir, "snippets", "about.html"), []byte("<div>BASE</div>"), 0644) // stale
	os.WriteFile(filepath.Join(themeDir, "post.html"), []byte("<html>CUSTOM</html>"), 0644)          // custom

	result := Apply(dir)

	// Find the theme-consolidation check
	var cr *CheckResult
	for i := range result.Checks {
		if result.Checks[i].Name == "theme-consolidation" {
			cr = &result.Checks[i]
			break
		}
	}
	if cr == nil {
		t.Fatal("theme-consolidation check not found")
	}
	if cr.Status != StatusFail {
		t.Errorf("expected fail (removed files), got %s: %s", cr.Status, cr.Message)
	}
	if len(cr.Actions) != 2 {
		t.Errorf("expected 2 actions (index.html + about.html), got %d", len(cr.Actions))
	}

	// Stale files should be removed
	if fileExists(filepath.Join(themeDir, "index.html")) {
		t.Error("stale index.html should be removed")
	}
	// Custom file should be preserved
	if !fileExists(filepath.Join(themeDir, "post.html")) {
		t.Error("custom post.html should be preserved")
	}
	// CSS should be preserved
	if !fileExists(filepath.Join(themeDir, "turbo.css")) {
		t.Error("CSS file should be preserved")
	}
}

// ── Avatar config tests ────────────────────────────────────────────

func TestAvatarConfig_Diagnose(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)

	// Remove avatar to trigger check
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	delete(raw, "avatar")
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(wkPath, append(out, '\n'), 0644)

	result := Diagnose(dir)
	checkMap := make(map[string]CheckResult)
	for _, cr := range result.Checks {
		checkMap[cr.Name] = cr
	}

	cr := checkMap["avatar-config"]
	if cr.Status != StatusFail {
		t.Errorf("avatar-config: expected fail, got %s: %s", cr.Status, cr.Message)
	}

	// Diagnose should not modify the file
	data, _ = os.ReadFile(wkPath)
	json.Unmarshal(data, &raw)
	if _, exists := raw["avatar"]; exists {
		t.Error("Diagnose should not add avatar")
	}
}

func TestAvatarConfig_Apply(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)

	// Remove avatar to trigger check
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	delete(raw, "avatar")
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(wkPath, append(out, '\n'), 0644)

	Apply(dir)

	data, _ = os.ReadFile(wkPath)
	json.Unmarshal(data, &raw)
	avatar, ok := raw["avatar"].(map[string]interface{})
	if !ok {
		t.Fatal("avatar should have been added")
	}
	if _, hasBG := avatar["bg"]; !hasBG {
		t.Error("avatar should have bg field")
	}
	if _, hasFG := avatar["fg"]; !hasFG {
		t.Error("avatar should have fg field")
	}
}

// ── Storage salt tests ─────────────────────────────────────────────

func TestStorageSalt_Diagnose(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)
	os.Remove(filepath.Join(dir, ".polis", "storage-salt"))

	result := Diagnose(dir)
	checkMap := make(map[string]CheckResult)
	for _, cr := range result.Checks {
		checkMap[cr.Name] = cr
	}

	if checkMap["storage-salt"].Status != StatusFail {
		t.Errorf("storage-salt: expected fail, got %s", checkMap["storage-salt"].Status)
	}
	if fileExists(filepath.Join(dir, ".polis", "storage-salt")) {
		t.Error("Diagnose should not create storage salt")
	}
}

func TestStorageSalt_Apply(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)
	os.Remove(filepath.Join(dir, ".polis", "storage-salt"))

	Apply(dir)

	if !fileExists(filepath.Join(dir, ".polis", "storage-salt")) {
		t.Error("Apply should create storage salt")
	}
}

// ── DM directories tests ──────────────────────────────────────────

func TestDMDirectories_Diagnose(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)
	os.RemoveAll(filepath.Join(dir, ".polis", "content", "pub.polis.core", "dm"))

	result := Diagnose(dir)
	checkMap := make(map[string]CheckResult)
	for _, cr := range result.Checks {
		checkMap[cr.Name] = cr
	}

	if checkMap["dm-directories"].Status != StatusFail {
		t.Errorf("dm-directories: expected fail, got %s", checkMap["dm-directories"].Status)
	}
	if dirExists(filepath.Join(dir, ".polis", "content", "pub.polis.core", "dm", "conv")) {
		t.Error("Diagnose should not create DM directories")
	}
}

func TestDMDirectories_Apply(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)
	os.RemoveAll(filepath.Join(dir, ".polis", "content", "pub.polis.core", "dm"))

	Apply(dir)

	convDir := filepath.Join(dir, ".polis", "content", "pub.polis.core", "dm", "conv")
	if !dirExists(convDir) {
		t.Error("Apply should create DM directories")
	}
	info, _ := os.Stat(convDir)
	if info.Mode().Perm() != 0700 {
		t.Errorf("DM conv dir should be 0700, got %o", info.Mode().Perm())
	}
}

// ── DM domain case tests ──────────────────────────────────────────

func TestDMDomainCase_PassesWhenClean(t *testing.T) {
	dir := createCurrentSite(t)

	result := checkDMDomainCase(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass for empty DM dir, got %s: %s", result.Status, result.Message)
	}
}

func TestDMDomainCase_FixesMixedCase(t *testing.T) {
	dir := createCurrentSite(t)
	convDir := filepath.Join(dir, ".polis", "content", "pub.polis.core", "dm", "conv")

	// Create a conversation with mixed-case domain
	conv := `{"peer_domain":"Example.COM","peer_url":"https://Example.COM","messages":[]}`
	os.WriteFile(filepath.Join(convDir, "abc123.json"), []byte(conv), 0600)

	result := checkDMDomainCase(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail, got %s: %s", result.Status, result.Message)
	}

	// Verify fix
	data, _ := os.ReadFile(filepath.Join(convDir, "abc123.json"))
	var fixed struct {
		PeerDomain string `json:"peer_domain"`
	}
	json.Unmarshal(data, &fixed)
	if fixed.PeerDomain != "example.com" {
		t.Errorf("expected lowercase domain, got %s", fixed.PeerDomain)
	}
}

// ── Policy feed self-omit tests ────────────────────────────────────

func TestPolicyFeedSelfOmit_Diagnose(t *testing.T) {
	dir := createCurrentSite(t)
	// Overwrite with policies missing the feed self-omit rule
	os.WriteFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"), []byte(`{"version":1}`+"\n"), 0600)

	result := checkPolicyFeedSelfOmit(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Errorf("expected fail, got %s: %s", result.Status, result.Message)
	}
}

func TestPolicyFeedSelfOmit_Apply(t *testing.T) {
	dir := createCurrentSite(t)
	os.WriteFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"), []byte(`{"version":1}`+"\n"), 0600)

	result := checkPolicyFeedSelfOmit(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail (applied), got %s: %s", result.Status, result.Message)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"))
	if !strings.Contains(string(data), "omit pub.polis.feed from self") {
		t.Error("feed self-omit rule should have been appended")
	}
}

// ── Policy deny-all tests ──────────────────────────────────────────

func TestPolicyDenyAll_Diagnose(t *testing.T) {
	dir := createCurrentSite(t)
	os.WriteFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"), []byte(`{"version":1}`+"\n"), 0600)

	result := checkPolicyDenyAll(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Errorf("expected fail, got %s: %s", result.Status, result.Message)
	}
}

func TestPolicyDenyAll_Apply(t *testing.T) {
	dir := createCurrentSite(t)
	os.WriteFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"), []byte(`{"version":1}`+"\n"), 0600)

	result := checkPolicyDenyAll(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail (applied), got %s: %s", result.Status, result.Message)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"))
	if !strings.Contains(string(data), "deny all from all") {
		t.Error("deny-all rule should have been appended")
	}
}

// ── Webapp view_mode tests ─────────────────────────────────────────

func TestWebappViewMode_Diagnose(t *testing.T) {
	dir := createCurrentSite(t)
	os.WriteFile(filepath.Join(dir, ".polis", "webapp", "config.json"), []byte(`{"view_mode":"list","theme":"sols"}`), 0644)

	result := checkWebappViewMode(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Errorf("expected fail, got %s: %s", result.Status, result.Message)
	}

	// Should not modify in dry run
	data, _ := os.ReadFile(filepath.Join(dir, ".polis", "webapp", "config.json"))
	if !strings.Contains(string(data), "view_mode") {
		t.Error("Diagnose should not remove view_mode")
	}
}

func TestWebappViewMode_Apply(t *testing.T) {
	dir := createCurrentSite(t)
	os.WriteFile(filepath.Join(dir, ".polis", "webapp", "config.json"), []byte(`{"view_mode":"list","theme":"sols"}`), 0644)

	result := checkWebappViewMode(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail (applied), got %s: %s", result.Status, result.Message)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".polis", "webapp", "config.json"))
	if strings.Contains(string(data), "view_mode") {
		t.Error("view_mode should have been removed")
	}
	if !strings.Contains(string(data), "theme") {
		t.Error("other keys should be preserved")
	}
}

func TestThemeConsolidation_PassesWhenClean(t *testing.T) {
	dir := createOldSite(t)

	// Create _base
	baseDir := filepath.Join(dir, "site", "themes", "_base")
	os.MkdirAll(baseDir, 0755)
	os.WriteFile(filepath.Join(baseDir, "index.html"), []byte("<html>BASE</html>"), 0644)

	// Create CSS-only theme (no stale files)
	themeDir := filepath.Join(dir, "site", "themes", "turbo")
	os.MkdirAll(themeDir, 0755)
	os.WriteFile(filepath.Join(themeDir, "turbo.css"), []byte("body{}"), 0644)

	result := Diagnose(dir)

	var cr *CheckResult
	for i := range result.Checks {
		if result.Checks[i].Name == "theme-consolidation" {
			cr = &result.Checks[i]
			break
		}
	}
	if cr == nil {
		t.Fatal("theme-consolidation check not found")
	}
	if cr.Status != StatusPass {
		t.Errorf("expected pass for clean site, got %s: %s", cr.Status, cr.Message)
	}
}
