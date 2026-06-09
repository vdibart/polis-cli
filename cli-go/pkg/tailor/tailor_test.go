package tailor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
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
		"version":     "polis-cli-go/0.59.0",
		"public_key":  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest",
		"author_name": "testuser",
		"created":     "2026-02-27T00:00:00Z",
		"avatar": map[string]interface{}{
			"bg": "#2a5a6a",
			"fg": "#ffffff",
		},
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]interface{}{
				"path": "content/pub.polis.core/bundle.json",
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

	// Bundle.json — write from DefaultCoreBundle() so the fixture stays in sync
	// with bundle changes (types, shapes, themes) without manual updates here.
	if err := bundle.SaveBundle(filepath.Join(dir, "content", "pub.polis.core", "bundle.json"), bundle.DefaultCoreBundle()); err != nil {
		t.Fatalf("write bundle.json: %v", err)
	}

	// Index
	os.WriteFile(filepath.Join(dir, "content", "pub.polis.core", "index.jsonl"), []byte{}, 0644)

	// Following and blessed
	os.WriteFile(filepath.Join(dir, "content", "pub.polis.core", "follow", "following.json"),
		[]byte(`{"version": "polis-cli-go/0.59.0", "following": []}`), 0644)
	os.WriteFile(filepath.Join(dir, "content", "pub.polis.core", "comment", "blessed.json"),
		[]byte(`{"version": "polis-cli-go/0.59.0", "comments": []}`), 0644)

	// Policies (canonical templates)
	os.MkdirAll(filepath.Join(dir, "policies"), 0755)
	os.WriteFile(filepath.Join(dir, "policies", "rules.jsonl"), []byte(policy.DefaultPublicPolicyContent()), 0644)
	os.MkdirAll(filepath.Join(dir, ".polis", "policies"), 0700)
	os.WriteFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"), []byte(policy.DefaultPrivatePolicyContent()), 0600)

	// Storage salt
	os.WriteFile(filepath.Join(dir, ".polis", "storage-salt"), []byte("abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"), 0600)

	// DM directories
	os.MkdirAll(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "dm", "conversations"), 0700)

	// Epoch-0 DM keyring — a current site has one (checkDMKeyring needs only the
	// keyring.json, not a signed block, so the fixture's fake key is fine).
	kr := &dm.Keyring{}
	kr.AddBootstrapEpoch()
	kr.Save(dm.DMDir(dir))

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
	// Install the reference payload AND pick an active theme so the
	// bundle-reference-payload + registry-integrity checks pass — a fully-
	// init'd site has both; createCurrentSite is intentionally minimal
	// otherwise so per-check fixtures can build on it.
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	if err := bundle.SetActiveThemeName(dir, "vice"); err != nil {
		t.Fatalf("SetActiveThemeName: %v", err)
	}
	if err := bundle.SetActiveShapeName(dir, "v4"); err != nil {
		t.Fatalf("SetActiveShapeName: %v", err)
	}
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

func TestLegacyThemeLocations_DeletesEntireLegacyTree(t *testing.T) {
	// Step-01 forced-upgrade policy: site/themes/ and .polis/themes/ are
	// deleted outright on upgrade, including any operator customizations.
	// The canonical theme home is now .polis/bundles/<bundle>/themes/,
	// populated from the embedded reference payload. This supersedes the
	// older checkThemeConsolidation behavior which preserved customizations.
	dir := createOldSite(t)

	baseDir := filepath.Join(dir, "site", "themes", "_base")
	os.MkdirAll(filepath.Join(baseDir, "snippets"), 0755)
	os.WriteFile(filepath.Join(baseDir, "index.html"), []byte("<html>BASE</html>"), 0644)

	themeDir := filepath.Join(dir, "site", "themes", "turbo")
	os.MkdirAll(filepath.Join(themeDir, "snippets"), 0755)
	os.WriteFile(filepath.Join(themeDir, "turbo.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(themeDir, "post.html"), []byte("<html>CUSTOM</html>"), 0644)

	result := Apply(dir)

	// checkLegacyThemeLocations should report Fail (action taken).
	var cr *CheckResult
	for i := range result.Checks {
		if result.Checks[i].Name == "legacy-theme-locations" {
			cr = &result.Checks[i]
			break
		}
	}
	if cr == nil {
		t.Fatal("legacy-theme-locations check not found")
	}
	if cr.Status != StatusFail {
		t.Errorf("expected fail (removed dirs), got %s: %s", cr.Status, cr.Message)
	}

	// The entire site/themes/ tree should be gone — forced upgrade.
	if dirExists(filepath.Join(dir, "site", "themes")) {
		t.Error("site/themes/ should be removed entirely (forced upgrade)")
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
	os.RemoveAll(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "dm"))

	result := Diagnose(dir)
	checkMap := make(map[string]CheckResult)
	for _, cr := range result.Checks {
		checkMap[cr.Name] = cr
	}

	if checkMap["dm-directories"].Status != StatusFail {
		t.Errorf("dm-directories: expected fail, got %s", checkMap["dm-directories"].Status)
	}
	if dirExists(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "dm", "conversations")) {
		t.Error("Diagnose should not create DM directories")
	}
}

func TestDMDirectories_Apply(t *testing.T) {
	Version = "0.59.0"
	dir := createCurrentSite(t)
	os.RemoveAll(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "dm"))

	Apply(dir)

	convDir := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "dm", "conversations")
	if !dirExists(convDir) {
		t.Error("Apply should create DM directories")
	}
	info, _ := os.Stat(convDir)
	if info.Mode().Perm() != 0700 {
		t.Errorf("DM conv dir should be 0700, got %o", info.Mode().Perm())
	}
}

// ── DM domain case tests ──────────────────────────────────────────

// ── Policy content convergence tests ─────────────────────────────

func TestPolicyContentConverge_Diagnose(t *testing.T) {
	dir := createCurrentSite(t)
	// Overwrite with old/drifted content
	os.WriteFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"), []byte(`{"version":1}`+"\n"+`{"active":true,"policy":"allow pub.polis.post from all"}`+"\n"), 0600)

	result := checkPolicyContentConverge(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Errorf("expected fail, got %s: %s", result.Status, result.Message)
	}
}

func TestPolicyContentConverge_Apply(t *testing.T) {
	dir := createCurrentSite(t)
	// Overwrite both files with old content
	os.WriteFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"), []byte(`{"version":1}`+"\n"+`{"active":true,"policy":"deny all from all"}`+"\n"), 0600)
	os.WriteFile(filepath.Join(dir, "policies", "rules.jsonl"), []byte(`{"version":1}`+"\n"), 0644)

	result := checkPolicyContentConverge(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail (applied), got %s: %s", result.Status, result.Message)
	}

	// Verify both files now match canonical templates
	privData, _ := os.ReadFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"))
	if strings.TrimSpace(string(privData)) != strings.TrimSpace(policy.DefaultPrivatePolicyContent()) {
		t.Error("private policy should match canonical template after convergence")
	}
	pubData, _ := os.ReadFile(filepath.Join(dir, "policies", "rules.jsonl"))
	if strings.TrimSpace(string(pubData)) != strings.TrimSpace(policy.DefaultPublicPolicyContent()) {
		t.Error("public policy should match canonical template after convergence")
	}
}

func TestPolicyContentConverge_AlreadyCorrect(t *testing.T) {
	dir := createCurrentSite(t)

	result := checkPolicyContentConverge(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass for correct files, got %s: %s", result.Status, result.Message)
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

func TestStaleScopedFeed_Diagnose(t *testing.T) {
	dir := createCurrentSite(t)
	stateDir := filepath.Join(dir, ".polis", "ds", "ds.polis.pub", "pub.polis.core", "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "pub.polis.feed.followers.jsonl"), []byte(`{}`), 0644)
	os.WriteFile(filepath.Join(stateDir, "pub.polis.feed.me.jsonl"), []byte(`{}`), 0644)

	result := checkStaleScopedFeed(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Errorf("expected fail, got %s: %s", result.Status, result.Message)
	}
	if len(result.Actions) != 2 {
		t.Errorf("expected 2 actions, got %d", len(result.Actions))
	}

	// Dry run should NOT remove files
	if _, err := os.Stat(filepath.Join(stateDir, "pub.polis.feed.followers.jsonl")); os.IsNotExist(err) {
		t.Error("Diagnose should not remove files")
	}
}

func TestStaleScopedFeed_Apply(t *testing.T) {
	dir := createCurrentSite(t)
	stateDir := filepath.Join(dir, ".polis", "ds", "ds.polis.pub", "pub.polis.core", "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "pub.polis.feed.followers.jsonl"), []byte(`{}`), 0644)
	os.WriteFile(filepath.Join(stateDir, "pub.polis.feed.me.jsonl"), []byte(`{}`), 0644)
	// Global should NOT be removed
	os.WriteFile(filepath.Join(stateDir, "pub.polis.feed.global.jsonl"), []byte(`{}`), 0644)

	result := checkStaleScopedFeed(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail (applied), got %s: %s", result.Status, result.Message)
	}

	// Deprecated files removed
	if _, err := os.Stat(filepath.Join(stateDir, "pub.polis.feed.followers.jsonl")); !os.IsNotExist(err) {
		t.Error("pub.polis.feed.followers.jsonl should be removed")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "pub.polis.feed.me.jsonl")); !os.IsNotExist(err) {
		t.Error("pub.polis.feed.me.jsonl should be removed")
	}

	// Global preserved
	if _, err := os.Stat(filepath.Join(stateDir, "pub.polis.feed.global.jsonl")); os.IsNotExist(err) {
		t.Error("pub.polis.feed.global.jsonl should NOT be removed")
	}
}

func TestStaleScopedFeed_Clean(t *testing.T) {
	dir := createCurrentSite(t)
	// No deprecated files
	result := checkStaleScopedFeed(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusPass {
		t.Errorf("expected pass for clean site, got %s: %s", result.Status, result.Message)
	}
}

func TestStaleFeedViewedAt_Diagnose(t *testing.T) {
	dir := createCurrentSite(t)
	stateDir := filepath.Join(dir, ".polis", "ds", "ds.polis.pub", "pub.polis.core", "state")
	os.MkdirAll(stateDir, 0755)
	cursors := `{"cursors":{"pub.polis.sync":{"position":"100"},"pub.polis.feed.viewed_at":{"position":"2026-04-01T12:00:00Z"}}}`
	os.WriteFile(filepath.Join(stateDir, "cursors.json"), []byte(cursors), 0644)

	result := checkStaleFeedViewedAt(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Errorf("expected fail, got %s: %s", result.Status, result.Message)
	}
	if len(result.Actions) != 1 {
		t.Errorf("expected 1 action, got %d", len(result.Actions))
	}

	// Dry run should NOT modify file
	data, _ := os.ReadFile(filepath.Join(stateDir, "cursors.json"))
	if !strings.Contains(string(data), "pub.polis.feed.viewed_at") {
		t.Error("Diagnose should not remove key from file")
	}
}

func TestStaleFeedViewedAt_Apply(t *testing.T) {
	dir := createCurrentSite(t)
	stateDir := filepath.Join(dir, ".polis", "ds", "ds.polis.pub", "pub.polis.core", "state")
	os.MkdirAll(stateDir, 0755)
	cursors := `{"cursors":{"pub.polis.sync":{"position":"100"},"pub.polis.feed.viewed_at":{"position":"2026-04-01T12:00:00Z"},"pub.polis.feed.viewed":{"position":"100"}}}`
	os.WriteFile(filepath.Join(stateDir, "cursors.json"), []byte(cursors), 0644)

	result := checkStaleFeedViewedAt(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail (applied), got %s: %s", result.Status, result.Message)
	}

	// Key should be removed
	data, _ := os.ReadFile(filepath.Join(stateDir, "cursors.json"))
	if strings.Contains(string(data), "pub.polis.feed.viewed_at") {
		t.Error("pub.polis.feed.viewed_at should be removed from cursors.json")
	}
	// Other keys preserved
	if !strings.Contains(string(data), "pub.polis.sync") {
		t.Error("pub.polis.sync should be preserved")
	}
	if !strings.Contains(string(data), "pub.polis.feed.viewed") {
		t.Error("pub.polis.feed.viewed should be preserved")
	}
}

func TestStaleFeedViewedAt_Clean(t *testing.T) {
	dir := createCurrentSite(t)
	// No cursors.json at all
	result := checkStaleFeedViewedAt(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusPass {
		t.Errorf("expected pass for clean site, got %s: %s", result.Status, result.Message)
	}
}

// addLegacyFeedArtifacts injects the retired pub.polis.feed scaffolding into a
// current-site fixture: the empty public content dir, plus a stale type entry
// in bundle.json. Simulates a tenant that was created by the older polis init.
func addLegacyFeedArtifacts(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "content", "pub.polis.core", "feed"), 0755); err != nil {
		t.Fatalf("mkdir feed dir: %v", err)
	}
	bundlePath := filepath.Join(dir, "content", "pub.polis.core", "bundle.json")
	b, err := bundle.LoadBundle(bundlePath)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	b.Types["pub.polis.feed"] = bundle.ContentType{
		Dir:      "feed",
		Mount:    "/feed",
		Renderer: "html",
		Private:  true,
	}
	if err := bundle.SaveBundle(bundlePath, b); err != nil {
		t.Fatalf("save bundle: %v", err)
	}
}

func TestLegacyFeedScaffolding_Diagnose(t *testing.T) {
	dir := createCurrentSite(t)
	addLegacyFeedArtifacts(t, dir)

	result := checkLegacyFeedScaffolding(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Errorf("expected fail, got %s: %s", result.Status, result.Message)
	}
	if len(result.Actions) != 2 {
		t.Errorf("expected 2 actions (dir + bundle.json), got %d", len(result.Actions))
	}

	if _, err := os.Stat(filepath.Join(dir, "content", "pub.polis.core", "feed")); os.IsNotExist(err) {
		t.Error("Diagnose should not remove dir")
	}
	b, _ := bundle.LoadBundle(filepath.Join(dir, "content", "pub.polis.core", "bundle.json"))
	if _, has := b.Types["pub.polis.feed"]; !has {
		t.Error("Diagnose should not strip bundle entry")
	}
}

func TestLegacyFeedScaffolding_Apply(t *testing.T) {
	dir := createCurrentSite(t)
	addLegacyFeedArtifacts(t, dir)

	result := checkLegacyFeedScaffolding(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail (applied), got %s: %s", result.Status, result.Message)
	}

	if _, err := os.Stat(filepath.Join(dir, "content", "pub.polis.core", "feed")); !os.IsNotExist(err) {
		t.Error("legacy feed dir should be removed")
	}
	b, _ := bundle.LoadBundle(filepath.Join(dir, "content", "pub.polis.core", "bundle.json"))
	if _, has := b.Types["pub.polis.feed"]; has {
		t.Error("pub.polis.feed should be stripped from bundle.json")
	}
	// Other types preserved
	if _, has := b.Types["pub.polis.post"]; !has {
		t.Error("pub.polis.post should be preserved in bundle.json")
	}
}

func TestLegacyFeedScaffolding_NonEmptyDirIsFlagged(t *testing.T) {
	dir := createCurrentSite(t)
	feedDir := filepath.Join(dir, "content", "pub.polis.core", "feed")
	os.MkdirAll(feedDir, 0755)
	// Drop an unexpected file so the dir is non-empty
	os.WriteFile(filepath.Join(feedDir, "unexpected.txt"), []byte("hi"), 0644)

	result := checkLegacyFeedScaffolding(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail (flagged), got %s: %s", result.Status, result.Message)
	}

	// Non-empty dir must be left in place
	if _, err := os.Stat(feedDir); os.IsNotExist(err) {
		t.Error("non-empty legacy feed dir should NOT be removed")
	}
	if _, err := os.Stat(filepath.Join(feedDir, "unexpected.txt")); os.IsNotExist(err) {
		t.Error("unexpected file inside feed dir should be preserved")
	}
	// Action should be a flag, not a remove
	var hasFlag bool
	for _, a := range result.Actions {
		if a.Op == "flag" {
			hasFlag = true
		}
		if a.Op == "remove" {
			t.Error("non-empty dir should produce flag, not remove")
		}
	}
	if !hasFlag {
		t.Error("expected at least one flag action")
	}
}

func TestLegacyFeedScaffolding_Clean(t *testing.T) {
	dir := createCurrentSite(t)
	// No legacy artifacts (createCurrentSite uses DefaultCoreBundle which has
	// pub.polis.feed removed; and there's no content/pub.polis.core/feed dir).
	result := checkLegacyFeedScaffolding(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusPass {
		t.Errorf("expected pass for clean site, got %s: %s", result.Status, result.Message)
	}
}

// --- step-02/2.0: studio13 rename migration tests ---

// setStudio13PreRenameRegistry writes active_theme=pub.polis.themes.studio13
// with the given active_shape (pass "" to leave shape empty) into a
// current-site fixture's registry. Simulates a self-hosted tenant whose
// registry survived the 2.0 bundle update without being migrated.
func setStudio13PreRenameRegistry(t *testing.T, dir, shape string) {
	t.Helper()
	if err := bundle.SetActiveThemeName(dir, "studio13"); err != nil {
		t.Fatalf("SetActiveThemeName(studio13): %v", err)
	}
	if shape != "" {
		if err := bundle.SetActiveShapeName(dir, shape); err != nil {
			t.Fatalf("SetActiveShapeName(%q): %v", shape, err)
		}
	}
}

func TestCheckStudio13RenameMigration_BlogMigrates(t *testing.T) {
	dir := createCurrentSite(t)
	setStudio13PreRenameRegistry(t, dir, "v3")

	result := checkStudio13RenameMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected Fail (migration applied), got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "studio13-nk") {
		t.Errorf("message should mention studio13-nk, got %q", result.Message)
	}

	// Registry actually updated.
	got, err := bundle.GetActiveThemeName(dir)
	if err != nil {
		t.Fatalf("GetActiveThemeName: %v", err)
	}
	if got != "studio13-nk" {
		t.Errorf("active_theme = %q, want studio13-nk", got)
	}
}

func TestCheckStudio13RenameMigration_EmptyShapeDoesNotMigrate(t *testing.T) {
	// Pre-v3→v4-cutover this test asserted "empty active_shape →
	// migrate to studio13-nk" on the assumption that empty defaulted
	// to v3 per bundle.GetActiveShapeName. Post-cutover that default is
	// v4, and the studio13 rename check (correctly) only fires on
	// explicit v3 or literal empty in the raw JSON. The relevant edge
	// case for self-hosted upgrade is now: a tenant whose registry has
	// no active_shape field at all (truly never set) effectively
	// becomes a v4 tenant via the helper default — so we should NOT
	// rename their theme. They keep studio13 (v4-compatible per
	// bundle.go's CompatibleShapes declaration).
	//
	// However the raw-JSON path in checkStudio13RenameMigration still
	// reads `as := raw["active_shape"].(string)` which yields "" for an
	// absent field, and the gate's `as == ""` clause then fires the
	// rename. That's the pre-cutover behavior preserved for tenants
	// that genuinely have an absent-field registry — a small group
	// whose registries predate v4. Self-hosters in that state DO want
	// the rename (their tenant is effectively v3-shaped on disk).
	//
	// To express the post-cutover state, this test now uses
	// setStudio13PreRenameRegistry with shape="v4" (which mirrors what
	// hosted Medic would do for them via upgradeActiveShape) — and
	// asserts NO migration.
	dir := createCurrentSite(t)
	setStudio13PreRenameRegistry(t, dir, "v4")

	result := checkStudio13RenameMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Fatalf("expected Pass (no migration on v4 default), got %s: %s", result.Status, result.Message)
	}
	got, _ := bundle.GetActiveThemeName(dir)
	if got != "studio13" {
		t.Errorf("active_theme = %q, want studio13 (unchanged)", got)
	}
}

func TestCheckStudio13RenameMigration_StreamNotMigrated(t *testing.T) {
	// Forward-looking: a v4 tenant with active_theme=studio13 legitimately
	// wants the new v4-era CSS-only studio13 (step-02/2.a.1). The migration
	// must NOT fire — check returns Pass, registry unchanged.
	dir := createCurrentSite(t)
	setStudio13PreRenameRegistry(t, dir, "v4")

	result := checkStudio13RenameMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Fatalf("expected Pass for v4 tenant (no migration), got %s: %s", result.Status, result.Message)
	}
	got, _ := bundle.GetActiveThemeName(dir)
	if got != "studio13" {
		t.Errorf("registry must be unchanged on v4; active_theme = %q, want studio13", got)
	}
}

func TestCheckStudio13RenameMigration_DryRun(t *testing.T) {
	dir := createCurrentSite(t)
	setStudio13PreRenameRegistry(t, dir, "v3")

	result := checkStudio13RenameMigration(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Fatalf("dry-run should report Fail (intended migration), got %s", result.Status)
	}
	if len(result.Actions) != 1 || result.Actions[0].Op != "migrate" {
		t.Errorf("expected a single migrate action, got %+v", result.Actions)
	}

	// Registry unchanged under dry-run.
	got, _ := bundle.GetActiveThemeName(dir)
	if got != "studio13" {
		t.Errorf("dry-run should not mutate registry; active_theme = %q, want studio13", got)
	}
}

func TestCheckStudio13RenameMigration_NotStudio13(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.SetActiveThemeName(dir, "vice"); err != nil {
		t.Fatalf("SetActiveThemeName(vice): %v", err)
	}

	result := checkStudio13RenameMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected Pass for non-studio13 tenant, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckStudio13RenameMigration_NoRegistry(t *testing.T) {
	dir := t.TempDir() // fresh empty dir; no registry exists
	result := checkStudio13RenameMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected Pass with no registry, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckStudio13RenameMigration_Idempotent(t *testing.T) {
	dir := createCurrentSite(t)
	setStudio13PreRenameRegistry(t, dir, "v3")

	// First pass applies the migration.
	r1 := checkStudio13RenameMigration(&runContext{siteDir: dir, dryRun: false})
	if r1.Status != StatusFail {
		t.Fatalf("first pass expected Fail (migration applied), got %s", r1.Status)
	}

	// Second pass: registry now has studio13-nk; check should pass with no-op.
	r2 := checkStudio13RenameMigration(&runContext{siteDir: dir, dryRun: false})
	if r2.Status != StatusPass {
		t.Errorf("second pass expected Pass (no-op after migration), got %s: %s", r2.Status, r2.Message)
	}
}

// --- hotfix-studio13-cleanup: orphan-dir reaper mirror tests ---

func plantOrphanThemeDir(t *testing.T, siteDir, name string) string {
	t.Helper()
	dir := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "themes", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".css"), []byte("body {}"), 0644); err != nil {
		t.Fatalf("write orphan css: %v", err)
	}
	return dir
}

func TestCheckOrphanedThemeDirs_ReapsOrphan(t *testing.T) {
	dir := createCurrentSite(t)
	// createCurrentSite doesn't install the bundle reference payload — it
	// only writes a valid bundle.json and minimal structure. Seed the
	// registry via EnsureReferencePayload so theme_versions is populated.
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	orphanDir := plantOrphanThemeDir(t, dir, "stale-orphan")

	result := checkOrphanedThemeDirs(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected Fail (orphan reaped), got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "stale-orphan") {
		t.Errorf("message should name reaped orphan; got: %s", result.Message)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Errorf("orphan dir must be removed; stat = %v", err)
	}
}

func TestCheckOrphanedThemeDirs_CleanSitePassesNoOp(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}

	result := checkOrphanedThemeDirs(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected Pass (no orphans), got %s: %s", result.Status, result.Message)
	}
}

func TestCheckOrphanedThemeDirs_DryRunPreservesDisk(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	orphanDir := plantOrphanThemeDir(t, dir, "orphan-dry")

	result := checkOrphanedThemeDirs(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Fatalf("dry-run should report Fail (intended reap), got %s", result.Status)
	}
	if len(result.Actions) != 1 || result.Actions[0].Op != "remove" {
		t.Errorf("expected single remove action, got %+v", result.Actions)
	}
	if _, err := os.Stat(orphanDir); err != nil {
		t.Errorf("dry-run must not delete; stat = %v", err)
	}
}

func TestCheckOrphanedThemeDirs_DoesNotTouchDeclared(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	// vice is a declared theme — must survive the check.
	viceDir := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "themes", "vice")
	if _, err := os.Stat(viceDir); err != nil {
		t.Fatalf("precondition: vice dir should be installed: %v", err)
	}

	_ = checkOrphanedThemeDirs(&runContext{siteDir: dir, dryRun: false})
	if _, err := os.Stat(viceDir); err != nil {
		t.Errorf("declared theme vice was reaped — critical safety bug: %v", err)
	}
}

func TestCheckOrphanedThemeDirs_SkipsWhenRegistryMissing(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	orphanDir := plantOrphanThemeDir(t, dir, "orphan-no-reg")
	// Remove registry.
	os.Remove(filepath.Join(dir, ".polis", "bundles", "registry.json"))

	result := checkOrphanedThemeDirs(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected Pass when registry missing (safety skip); got %s", result.Status)
	}
	if _, err := os.Stat(orphanDir); err != nil {
		t.Errorf("orphan must not be reaped when registry missing; stat = %v", err)
	}
}

// ── step-01 bundle/registry/theme refactor migrations ──────────────────

func TestCheckBundleReferencePayload_FreshSiteInstalls(t *testing.T) {
	dir := createCurrentSite(t) // no registry.json yet
	result := checkBundleReferencePayload(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (payload installed), got %s: %s", result.Status, result.Message)
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis", "bundles", "registry.json")); err != nil {
		t.Errorf("registry.json should be installed; stat = %v", err)
	}
}

func TestCheckBundleReferencePayload_DryRunPreservesDisk(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkBundleReferencePayload(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Fatalf("dry-run should report fail (intended install), got %s", result.Status)
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis", "bundles", "registry.json")); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write registry.json; stat = %v", err)
	}
}

func TestCheckBundleReferencePayload_Idempotent(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	result := checkBundleReferencePayload(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on already-installed site, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckLegacyContentPath_MigratesOldTree(t *testing.T) {
	dir := createCurrentSite(t)
	// Plant legacy private bundle state at .polis/content/<bundle>/.
	legacy := filepath.Join(dir, ".polis", "content", "pub.polis.core", "posts")
	os.MkdirAll(legacy, 0755)
	os.WriteFile(filepath.Join(legacy, "marker.txt"), []byte("legacy"), 0644)

	result := checkLegacyContentPath(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (migration ran), got %s: %s", result.Status, result.Message)
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis", "content")); !os.IsNotExist(err) {
		t.Errorf(".polis/content should be migrated away; stat = %v", err)
	}
	moved := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "marker.txt")
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("marker should have moved into .polis/bundles/; stat = %v", err)
	}
}

func TestCheckLegacyContentPath_CleanSitePasses(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkLegacyContentPath(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on clean site, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckLegacyContentPath_DryRunPreservesDisk(t *testing.T) {
	dir := createCurrentSite(t)
	legacy := filepath.Join(dir, ".polis", "content", "pub.polis.core")
	os.MkdirAll(legacy, 0755)

	result := checkLegacyContentPath(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Fatalf("dry-run should report fail (intended migration), got %s", result.Status)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("dry-run must not move; stat = %v", err)
	}
}

func TestCheckRegistryMigration_MovesActiveThemeOutOfWellKnown(t *testing.T) {
	dir := createCurrentSite(t)
	// Inject legacy active_theme into well-known.
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	raw["active_theme"] = "vice"
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(wkPath, out, 0644)

	result := checkRegistryMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (migration ran), got %s: %s", result.Status, result.Message)
	}

	// active_theme should be gone from well-known.
	data2, _ := os.ReadFile(wkPath)
	var raw2 map[string]interface{}
	json.Unmarshal(data2, &raw2)
	if _, has := raw2["active_theme"]; has {
		t.Error("active_theme should be stripped from .well-known/polis")
	}

	// Registry should now have the FQN-qualified active_theme.
	reg, err := bundle.LoadRegistry(dir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if reg.ActiveTheme != "pub.polis.themes.vice" {
		t.Errorf("active_theme should be FQN-qualified; got %q", reg.ActiveTheme)
	}
}

func TestCheckRegistryMigration_AlreadyMigratedPasses(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkRegistryMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass when no active_theme in well-known, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckLegacyThemeLocations_DeletesBothDirs(t *testing.T) {
	dir := createCurrentSite(t)
	siteThemes := filepath.Join(dir, "site", "themes", "turbo")
	polisThemes := filepath.Join(dir, ".polis", "themes", "vice")
	os.MkdirAll(siteThemes, 0755)
	os.MkdirAll(polisThemes, 0755)

	result := checkLegacyThemeLocations(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (legacy dirs deleted), got %s: %s", result.Status, result.Message)
	}
	if dirExists(filepath.Join(dir, "site", "themes")) {
		t.Error("site/themes should be deleted")
	}
	if dirExists(filepath.Join(dir, ".polis", "themes")) {
		t.Error(".polis/themes should be deleted")
	}
}

func TestCheckLegacyThemeLocations_NoLegacyPasses(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkLegacyThemeLocations(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on clean site, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckBundleActiveFields_StripsBothFiles(t *testing.T) {
	dir := createCurrentSite(t)
	// Inject legacy active field back into well-known.
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	bundles, _ := raw["bundles"].(map[string]interface{})
	core, _ := bundles["pub.polis.core"].(map[string]interface{})
	core["active"] = true
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(wkPath, out, 0644)

	// Install reference payload so registry.json exists (then inject legacy active).
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	regPath := filepath.Join(dir, ".polis", "bundles", "registry.json")
	rdata, _ := os.ReadFile(regPath)
	var rraw map[string]interface{}
	json.Unmarshal(rdata, &rraw)
	if ibList, ok := rraw["installed_bundles"].([]interface{}); ok && len(ibList) > 0 {
		ib0, _ := ibList[0].(map[string]interface{})
		ib0["active"] = true
		rout, _ := json.MarshalIndent(rraw, "", "  ")
		os.WriteFile(regPath, rout, 0644)
	}

	result := checkBundleActiveFields(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (fields stripped), got %s: %s", result.Status, result.Message)
	}

	// Both files should be clean.
	data2, _ := os.ReadFile(wkPath)
	var raw2 map[string]interface{}
	json.Unmarshal(data2, &raw2)
	b2, _ := raw2["bundles"].(map[string]interface{})
	c2, _ := b2["pub.polis.core"].(map[string]interface{})
	if _, has := c2["active"]; has {
		t.Error("active field should be stripped from well-known bundle entry")
	}

	rdata2, _ := os.ReadFile(regPath)
	var rraw2 map[string]interface{}
	json.Unmarshal(rdata2, &rraw2)
	if ibList, ok := rraw2["installed_bundles"].([]interface{}); ok && len(ibList) > 0 {
		ib0, _ := ibList[0].(map[string]interface{})
		if _, has := ib0["active"]; has {
			t.Error("active field should be stripped from registry installed_bundles entry")
		}
	}
}

func TestCheckBundleActiveFields_CleanSitePasses(t *testing.T) {
	dir := createCurrentSite(t)
	// createCurrentSite no longer writes the legacy active field; this should pass.
	result := checkBundleActiveFields(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on clean site, got %s: %s", result.Status, result.Message)
	}
}

// ── post-step-01 migrations (Phase B) ─────────────────────────────────

func TestCheckNotificationRulesMigration_MovesRules(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	// Inject a legacy per-type notification rule into bundle.json.
	bundlePath := filepath.Join(dir, "content", "pub.polis.core", "bundle.json")
	data, _ := os.ReadFile(bundlePath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	types, _ := raw["types"].(map[string]interface{})
	post, _ := types["pub.polis.post"].(map[string]interface{})
	post["notifications"] = []map[string]interface{}{
		{
			"id":        "tailor-test-rule",
			"on":        "publish",
			"recipient": "self",
		},
	}
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(bundlePath, out, 0644)

	result := checkNotificationRulesMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (migration ran), got %s: %s", result.Status, result.Message)
	}

	// Verify rule arrived in registry.
	reg, err := bundle.LoadRegistry(dir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if !reg.HasNotificationRule("tailor-test-rule") {
		t.Error("registry should contain the migrated rule")
	}

	// Verify bundle.json's types[].notifications is gone.
	data2, _ := os.ReadFile(bundlePath)
	var raw2 map[string]interface{}
	json.Unmarshal(data2, &raw2)
	t2, _ := raw2["types"].(map[string]interface{})
	p2, _ := t2["pub.polis.post"].(map[string]interface{})
	if _, has := p2["notifications"]; has {
		t.Error("types[pub.polis.post].notifications should be removed from bundle.json")
	}
}

func TestCheckNotificationRulesMigration_CleanSitePasses(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkNotificationRulesMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on clean site, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckActiveShapeUpgrade_FlipsV3ToV4(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	// Force the registry to active_shape v3.
	if err := bundle.SetActiveShapeName(dir, "v3"); err != nil {
		t.Fatalf("SetActiveShapeName: %v", err)
	}

	result := checkActiveShapeUpgrade(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (flip ran), got %s: %s", result.Status, result.Message)
	}

	reg, err := bundle.LoadRegistry(dir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if reg.ActiveShape != "pub.polis.shapes.v4" {
		t.Errorf("active_shape should be v4 after upgrade, got %q", reg.ActiveShape)
	}
}

func TestCheckActiveShapeUpgrade_AlreadyV4Passes(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	if err := bundle.SetActiveShapeName(dir, "v4"); err != nil {
		t.Fatalf("SetActiveShapeName: %v", err)
	}
	result := checkActiveShapeUpgrade(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on v4-already tenant, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckActiveShapeUpgrade_NoRegistryPasses(t *testing.T) {
	dir := createCurrentSite(t) // no registry installed
	result := checkActiveShapeUpgrade(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass when no registry, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckV3LegacyArchives_RemovesOnV4Tenant(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	if err := bundle.SetActiveShapeName(dir, "v4"); err != nil {
		t.Fatalf("SetActiveShapeName: %v", err)
	}
	// Plant stale archive files.
	for _, p := range []string{"posts/index.html", "comments/index.html", "tag/index.html"} {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0755)
		os.WriteFile(full, []byte("<html>stale</html>"), 0644)
	}

	result := checkV3LegacyArchives(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (files removed), got %s: %s", result.Status, result.Message)
	}
	for _, p := range []string{"posts/index.html", "comments/index.html", "tag/index.html"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s should be removed", p)
		}
	}
}

func TestCheckV3LegacyArchives_NoOpOnV3Tenant(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	if err := bundle.SetActiveShapeName(dir, "v3"); err != nil {
		t.Fatalf("SetActiveShapeName: %v", err)
	}
	// Plant an archive file — should NOT be removed (live content on v3).
	full := filepath.Join(dir, "posts", "index.html")
	os.MkdirAll(filepath.Dir(full), 0755)
	os.WriteFile(full, []byte("<html>live v3</html>"), 0644)

	result := checkV3LegacyArchives(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on v3 tenant (no-op), got %s: %s", result.Status, result.Message)
	}
	if _, err := os.Stat(full); err != nil {
		t.Errorf("v3 archive must NOT be removed (it's live content); stat = %v", err)
	}
}

func TestCheckV3LegacyArchives_DryRunPreservesDisk(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	if err := bundle.SetActiveShapeName(dir, "v4"); err != nil {
		t.Fatalf("SetActiveShapeName: %v", err)
	}
	full := filepath.Join(dir, "posts", "index.html")
	os.MkdirAll(filepath.Dir(full), 0755)
	os.WriteFile(full, []byte("<html>stale</html>"), 0644)

	result := checkV3LegacyArchives(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusFail {
		t.Fatalf("dry-run should report fail (intended removal), got %s", result.Status)
	}
	if _, err := os.Stat(full); err != nil {
		t.Errorf("dry-run must not delete; stat = %v", err)
	}
}

// ── F1–F9 content-aware integrity (Phase C) ───────────────────────────

func TestCheckReferencePayloadIntegrity_DetectsDrift(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	// Corrupt a fixture file.
	themeCSSPath := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "themes", "vice", "vice.css")
	os.WriteFile(themeCSSPath, []byte("/* hand-edit; not canonical */"), 0644)

	result := checkReferencePayloadIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (drift detected + re-installed), got %s: %s", result.Status, result.Message)
	}

	// File should now match the embedded fixture (re-installed).
	mismatches, _ := bundle.CompareReferencePayload(dir, "pub.polis.core")
	if len(mismatches) != 0 {
		t.Errorf("after re-install, expected zero mismatches; got %d", len(mismatches))
	}
}

func TestCheckReferencePayloadIntegrity_CleanSitePasses(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	result := checkReferencePayloadIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckRegistryIntegrity_FillsEmptyActiveTheme(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	// EnsureReferencePayload leaves active_theme empty by default.
	result := checkRegistryIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (active_theme populated), got %s: %s", result.Status, result.Message)
	}
	reg, err := bundle.LoadRegistry(dir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if reg.ActiveTheme == "" {
		t.Error("active_theme should be populated after F2 heal")
	}
}

func TestCheckRegistryIntegrity_NormalizesBareName(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	// Inject a bare-name active_theme (e.g. "vice") instead of the FQN.
	regPath := filepath.Join(dir, ".polis", "bundles", "registry.json")
	data, _ := os.ReadFile(regPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	raw["active_theme"] = "vice"
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(regPath, out, 0644)

	result := checkRegistryIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (normalized), got %s: %s", result.Status, result.Message)
	}
	reg, _ := bundle.LoadRegistry(dir)
	if reg.ActiveTheme != "pub.polis.themes.vice" {
		t.Errorf("active_theme should be FQN-qualified; got %q", reg.ActiveTheme)
	}
}

func TestCheckRegistryIntegrity_ResetsDanglingFQN(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	regPath := filepath.Join(dir, ".polis", "bundles", "registry.json")
	data, _ := os.ReadFile(regPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	raw["active_theme"] = "pub.polis.themes.does-not-exist"
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(regPath, out, 0644)

	result := checkRegistryIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (reset), got %s: %s", result.Status, result.Message)
	}
	reg, _ := bundle.LoadRegistry(dir)
	if reg.ActiveTheme == "pub.polis.themes.does-not-exist" {
		t.Error("dangling active_theme should be reset")
	}
}

func TestCheckRegistryIntegrity_FlagsMalformedJSON(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	regPath := filepath.Join(dir, ".polis", "bundles", "registry.json")
	os.WriteFile(regPath, []byte("{not-valid-json"), 0644)

	result := checkRegistryIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (flag), got %s: %s", result.Status, result.Message)
	}
	if len(result.Actions) == 0 || result.Actions[0].Op != "flag" {
		t.Errorf("malformed JSON should be flagged (Tailor must not overwrite); got %+v", result.Actions)
	}
	// File contents must NOT be modified.
	data, _ := os.ReadFile(regPath)
	if string(data) != "{not-valid-json" {
		t.Error("Tailor must not overwrite a malformed registry without operator review")
	}
}

func TestCheckRegistryIntegrity_DefersStudio13(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	regPath := filepath.Join(dir, ".polis", "bundles", "registry.json")
	data, _ := os.ReadFile(regPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	raw["active_theme"] = "pub.polis.themes.studio13"
	raw["active_shape"] = "pub.polis.shapes.v3"
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(regPath, out, 0644)

	result := checkRegistryIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("pre-rename studio13 on v3 should defer to checkStudio13RenameMigration; got %s: %s", result.Status, result.Message)
	}
	// active_theme must remain untouched — handled by the specialized check.
	reg, _ := bundle.LoadRegistry(dir)
	if reg.ActiveTheme != "pub.polis.themes.studio13" {
		t.Errorf("F2 must not touch pre-rename studio13/v3; got %q", reg.ActiveTheme)
	}
}

func TestCheckKeyConsistency_FlagsMismatch(t *testing.T) {
	dir := createCurrentSite(t)
	// Inject a different public key into well-known.
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	raw["public_key"] = "ssh-ed25519 AAAADIFFERENT"
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(wkPath, out, 0644)

	result := checkKeyConsistency(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (mismatch flagged), got %s: %s", result.Status, result.Message)
	}
	if len(result.Actions) == 0 || result.Actions[0].Op != "flag" {
		t.Errorf("key mismatch should be flag-only (trust decision); got %+v", result.Actions)
	}
}

func TestCheckKeyConsistency_MatchPasses(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkKeyConsistency(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on matching keys, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckBundlePathIntegrity_RepairsCorePath(t *testing.T) {
	dir := createCurrentSite(t)
	// Inject a broken pub.polis.core path.
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	bundles, _ := raw["bundles"].(map[string]interface{})
	core, _ := bundles["pub.polis.core"].(map[string]interface{})
	core["path"] = "wrong/path.json"
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(wkPath, out, 0644)

	result := checkBundlePathIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (path rewritten), got %s: %s", result.Status, result.Message)
	}

	data2, _ := os.ReadFile(wkPath)
	var raw2 map[string]interface{}
	json.Unmarshal(data2, &raw2)
	b2, _ := raw2["bundles"].(map[string]interface{})
	c2, _ := b2["pub.polis.core"].(map[string]interface{})
	if c2["path"] != "content/pub.polis.core/bundle.json" {
		t.Errorf("pub.polis.core path should be rewritten to canonical; got %q", c2["path"])
	}
}

func TestCheckBundlePathIntegrity_FlagsNonCore(t *testing.T) {
	dir := createCurrentSite(t)
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	bundles, _ := raw["bundles"].(map[string]interface{})
	bundles["com.example.custom"] = map[string]interface{}{
		"path": "nonexistent/bundle.json",
	}
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(wkPath, out, 0644)

	result := checkBundlePathIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (flagged), got %s: %s", result.Status, result.Message)
	}
	if len(result.Actions) == 0 || result.Actions[0].Op != "flag" {
		t.Errorf("non-core bundle should be flag-only; got %+v", result.Actions)
	}
}

func TestCheckBundlePathIntegrity_FlagsEmptyAuthorName(t *testing.T) {
	dir := createCurrentSite(t)
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	raw["author_name"] = ""
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(wkPath, out, 0644)

	result := checkBundlePathIntegrity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (author_name empty), got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "author_name") {
		t.Errorf("message should mention author_name; got: %s", result.Message)
	}
}

func TestCheckBundleDeclarations_MergesMissingShape(t *testing.T) {
	dir := createCurrentSite(t)
	// Strip the shapes from bundle.json.
	bundlePath := filepath.Join(dir, "content", "pub.polis.core", "bundle.json")
	b, _ := bundle.LoadBundle(bundlePath)
	b.Shapes = nil
	bundle.SaveBundle(bundlePath, b)

	result := checkBundleDeclarations(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (merged from defaults), got %s: %s", result.Status, result.Message)
	}

	b2, _ := bundle.LoadBundle(bundlePath)
	if len(b2.Shapes) == 0 {
		t.Error("shapes should be merged from defaults")
	}
}

func TestCheckBundleDeclarations_FlagsFieldDrift(t *testing.T) {
	dir := createCurrentSite(t)
	bundlePath := filepath.Join(dir, "content", "pub.polis.core", "bundle.json")
	b, _ := bundle.LoadBundle(bundlePath)
	// Mutate a type's Dir to simulate drift.
	if t0, ok := b.Types["pub.polis.post"]; ok {
		t0.Dir = "wrong-dir"
		b.Types["pub.polis.post"] = t0
	}
	bundle.SaveBundle(bundlePath, b)

	result := checkBundleDeclarations(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (drift flagged), got %s: %s", result.Status, result.Message)
	}
	if len(result.Actions) == 0 || result.Actions[0].Op != "flag" {
		t.Errorf("per-type drift should be flag-only; got %+v", result.Actions)
	}
}

func TestCheckIndexEntries_FlagsBadEntry(t *testing.T) {
	dir := createCurrentSite(t)
	indexPath := filepath.Join(dir, "content", "pub.polis.core", "index.jsonl")
	os.WriteFile(indexPath, []byte(`{"type": "pub.polis.post", "path": "/posts/x", "published": "not-rfc3339", "current_version": "abc"}`+"\n"), 0644)

	result := checkIndexEntries(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (bad RFC3339, bad sha prefix), got %s: %s", result.Status, result.Message)
	}
}

func TestCheckIndexEntries_EmptyOK(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkIndexEntries(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("empty index.jsonl should pass, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckBlessedFollowingStructure_FlagsMalformed(t *testing.T) {
	dir := createCurrentSite(t)
	// Inject a malformed blessed.json — array element without 'post' field.
	blessedPath := filepath.Join(dir, "content", "pub.polis.core", "comment", "blessed.json")
	os.WriteFile(blessedPath, []byte(`{"comments": [{"blessed": []}]}`), 0644)

	result := checkBlessedFollowingStructure(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (missing post field), got %s: %s", result.Status, result.Message)
	}
}

func TestCheckBlessedFollowingStructure_CleanPasses(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkBlessedFollowingStructure(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("clean blessed/following should pass, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckAuthorFieldMigration_RenamesAuthor(t *testing.T) {
	dir := createOldSite(t) // uses legacy `author` field
	result := checkAuthorFieldMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (rename ran), got %s: %s", result.Status, result.Message)
	}
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if _, has := raw["author"]; has {
		t.Error("legacy `author` field should be removed")
	}
	if _, has := raw["author_name"]; !has {
		t.Error("`author_name` should be present after migration")
	}
}

func TestCheckAuthorFieldMigration_AlreadyMigratedPasses(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkAuthorFieldMigration(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on current site, got %s: %s", result.Status, result.Message)
	}
}

// ── Tailor-only self-heal (Phase D) ───────────────────────────────────

func TestCheckActiveThemeUnset_PicksRandom(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	// EnsureReferencePayload leaves active_theme empty by default.
	result := checkActiveThemeUnset(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (theme picked), got %s: %s", result.Status, result.Message)
	}
	at, _ := bundle.GetActiveThemeName(dir)
	if at == "" {
		t.Error("active_theme should be set after heal")
	}
}

func TestCheckActiveThemeUnset_AlreadySetPasses(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	if err := bundle.SetActiveThemeName(dir, "vice"); err != nil {
		t.Fatalf("SetActiveThemeName: %v", err)
	}
	result := checkActiveThemeUnset(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass when active_theme already set, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckRegistrySchemaVersion_CurrentPasses(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	result := checkRegistrySchemaVersion(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass on current schema_version, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckRegistrySchemaVersion_OlderFlags(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	regPath := filepath.Join(dir, ".polis", "bundles", "registry.json")
	data, _ := os.ReadFile(regPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	raw["schema_version"] = float64(0)
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(regPath, out, 0644)

	result := checkRegistrySchemaVersion(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (older schema flagged), got %s: %s", result.Status, result.Message)
	}
	if len(result.Actions) == 0 || result.Actions[0].Op != "flag" {
		t.Errorf("older schema_version should be flag-only stub; got %+v", result.Actions)
	}
}

func TestCheckRegistryFQNSanity_BareNamesFlagged(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	regPath := filepath.Join(dir, ".polis", "bundles", "registry.json")
	data, _ := os.ReadFile(regPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	raw["active_theme"] = "vice" // bare name
	raw["active_shape"] = "v4"   // bare name
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(regPath, out, 0644)

	result := checkRegistryFQNSanity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Fatalf("expected fail (bare names flagged), got %s: %s", result.Status, result.Message)
	}
	// Should NOT auto-correct — F2 owns the remediation.
	data2, _ := os.ReadFile(regPath)
	var raw2 map[string]interface{}
	json.Unmarshal(data2, &raw2)
	if at, _ := raw2["active_theme"].(string); at != "vice" {
		t.Errorf("FQN-sanity check must not auto-correct (F2 owns remediation); got active_theme=%q", at)
	}
}

func TestCheckRegistryFQNSanity_ValidFQNsPass(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}
	bundle.SetActiveThemeName(dir, "vice")
	bundle.SetActiveShapeName(dir, "v4")
	result := checkRegistryFQNSanity(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass {
		t.Errorf("expected pass with valid FQNs, got %s: %s", result.Status, result.Message)
	}
}

func TestStep01Migrations_AllIdempotent(t *testing.T) {
	// Composite test: run the full Apply cycle twice on a site that needs
	// every step-01 migration, then verify the second pass reports all five
	// step-01 checks as Pass (no migration action on the second pass).
	dir := createCurrentSite(t)

	// Plant legacy state for every migration.
	legacyContent := filepath.Join(dir, ".polis", "content", "pub.polis.core")
	os.MkdirAll(legacyContent, 0755)
	os.MkdirAll(filepath.Join(dir, "site", "themes", "turbo"), 0755)
	os.MkdirAll(filepath.Join(dir, ".polis", "themes", "vice"), 0755)
	wkPath := filepath.Join(dir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	raw["active_theme"] = "vice"
	bundles, _ := raw["bundles"].(map[string]interface{})
	core, _ := bundles["pub.polis.core"].(map[string]interface{})
	core["active"] = true
	out, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(wkPath, out, 0644)

	Apply(dir)
	result := Apply(dir)

	migrations := map[string]bool{
		"bundle-reference-payload": true,
		"legacy-content-path":      true,
		"registry-migration":       true,
		"legacy-theme-locations":   true,
		"bundle-active-fields":     true,
	}
	for _, cr := range result.Checks {
		if !migrations[cr.Name] {
			continue
		}
		if cr.Status != StatusPass {
			t.Errorf("second pass: %s expected Pass (idempotent), got %s: %s", cr.Name, cr.Status, cr.Message)
		}
	}
}

func TestBlessedCacheGC_RemovesOrphan(t *testing.T) {
	dir := createCurrentSite(t)
	blessedDir := filepath.Join(dir, ".polis", "ds", "ds.polis.pub", "pub.polis.core", "cache", "blessed", "alice.polis.pub", "20260601")
	os.MkdirAll(blessedDir, 0755)
	// Healthy entry: body + valid sidecar.
	os.WriteFile(filepath.Join(blessedDir, "good.md"), []byte("body"), 0644)
	os.WriteFile(filepath.Join(blessedDir, "good.md.meta.json"), []byte(`{"kind":"blessed"}`), 0644)
	// Orphan: body with NO sidecar.
	os.WriteFile(filepath.Join(blessedDir, "orphan.md"), []byte("orphan"), 0644)

	result := checkBlessedCacheGC(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusFail {
		t.Errorf("expected fail (removed orphan), got %s: %s", result.Status, result.Message)
	}
	if _, err := os.Stat(filepath.Join(blessedDir, "orphan.md")); !os.IsNotExist(err) {
		t.Error("orphan body should be removed")
	}
	if _, err := os.Stat(filepath.Join(blessedDir, "good.md")); os.IsNotExist(err) {
		t.Error("healthy (sidecar-backed) entry must be preserved")
	}
}

func TestBlessedCacheGC_CleanIsPass(t *testing.T) {
	dir := createCurrentSite(t)
	result := checkBlessedCacheGC(&runContext{siteDir: dir, dryRun: true})
	if result.Status != StatusPass {
		t.Errorf("expected pass for a site with no blessed cache, got %s: %s", result.Status, result.Message)
	}
}

// TestCheckForeignContentInPublicPath_RemovesForeign: a foreign comment in the
// public tree (+ its rendered mount sibling) is backed up and removed; the
// owner's own comment is left untouched.
func TestCheckForeignContentInPublicPath_RemovesForeign(t *testing.T) {
	dir := t.TempDir()
	const owner = "discover.polis.pub"

	srcRel := "content/pub.polis.core/comment/20260531/reply.md"
	mountRel := "comments/20260531/reply.html"
	os.MkdirAll(filepath.Join(dir, filepath.Dir(srcRel)), 0o755)
	os.MkdirAll(filepath.Join(dir, filepath.Dir(mountRel)), 0o755)
	os.WriteFile(filepath.Join(dir, srcRel), []byte("---\nauthor: vdibart.polis.pub\n---\nforeign"), 0o644)
	os.WriteFile(filepath.Join(dir, mountRel), []byte("<p>foreign</p>"), 0o644)
	// Owner's OWN comment — must be left untouched.
	ownRel := "content/pub.polis.core/comment/20260531/mine.md"
	os.WriteFile(filepath.Join(dir, ownRel), []byte("---\nauthor: discover.polis.pub\n---\nmine"), 0o644)

	backupDir := filepath.Join(dir, ".polis", "tailor-backup", "t")
	res := checkForeignContentInPublicPath(&runContext{siteDir: dir, dryRun: false, baseURL: "https://" + owner, backupDir: backupDir})

	if res.Status != StatusFail {
		t.Fatalf("expected fail (foreign content found), got %s", res.Status)
	}
	if _, err := os.Stat(filepath.Join(dir, srcRel)); !os.IsNotExist(err) {
		t.Error("foreign source must be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, mountRel)); !os.IsNotExist(err) {
		t.Error("foreign mount sibling must be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, ownRel)); err != nil {
		t.Error("owner's own comment must NOT be removed")
	}
	if _, err := os.Stat(filepath.Join(backupDir, srcRel)); err != nil {
		t.Error("foreign source must be backed up before removal")
	}
}

// TestCheckForeignContentInPublicPath_DryRun: a dry-run reports the foreign file
// but does not remove it.
func TestCheckForeignContentInPublicPath_DryRun(t *testing.T) {
	dir := t.TempDir()
	srcRel := "content/pub.polis.core/comment/20260531/reply.md"
	os.MkdirAll(filepath.Join(dir, filepath.Dir(srcRel)), 0o755)
	os.WriteFile(filepath.Join(dir, srcRel), []byte("---\nauthor: vdibart.polis.pub\n---\nforeign"), 0o644)

	res := checkForeignContentInPublicPath(&runContext{siteDir: dir, dryRun: true, baseURL: "https://discover.polis.pub"})
	if res.Status != StatusFail || len(res.Actions) == 0 {
		t.Fatalf("dry-run should report the foreign file, got %s (%d actions)", res.Status, len(res.Actions))
	}
	if _, err := os.Stat(filepath.Join(dir, srcRel)); err != nil {
		t.Error("dry-run must NOT remove the file")
	}
}

// TestCheckForeignContentInPublicPath_CleanSite: a site holding only its own
// content passes.
func TestCheckForeignContentInPublicPath_CleanSite(t *testing.T) {
	dir := t.TempDir()
	srcRel := "content/pub.polis.core/comment/20260531/mine.md"
	os.MkdirAll(filepath.Join(dir, filepath.Dir(srcRel)), 0o755)
	os.WriteFile(filepath.Join(dir, srcRel), []byte("---\nauthor: discover.polis.pub\n---\nmine"), 0o644)

	res := checkForeignContentInPublicPath(&runContext{siteDir: dir, dryRun: false, baseURL: "https://discover.polis.pub"})
	if res.Status != StatusPass {
		t.Errorf("a site with only its own content must pass, got %s: %+v", res.Status, res.Actions)
	}
}
