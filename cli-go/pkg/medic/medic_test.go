package medic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/patrol"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// setupValidTenant creates a minimal valid tenant site in a temp directory.
func setupValidTenant(t *testing.T, base, name string) string {
	t.Helper()

	siteDir := filepath.Join(base, name)

	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	for _, dir := range []string{
		".well-known",
		filepath.Join(".polis", "keys"),
		filepath.Join("content", "pub.polis.core", "post"),
	} {
		if err := os.MkdirAll(filepath.Join(siteDir, dir), 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	os.Chmod(filepath.Join(siteDir, ".polis"), 0700)

	// Create bundle.json
	bundleData, _ := json.Marshal(map[string]interface{}{
		"name":    "pub.polis.core",
		"version": "1.0.0",
	})
	os.WriteFile(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"), bundleData, 0644)

	wk := map[string]interface{}{
		"public_key": strings.TrimSpace(string(pubKey)),
	}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wkData, 0644)
	os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), privKey, 0600)
	os.WriteFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"), pubKey, 0644)

	return siteDir
}

func TestHealTenant_FixKeyPerms(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "alice")

	// Make key permissions unsafe
	privKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")
	os.Chmod(privKeyPath, 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.KeyPerms.OK {
		t.Fatal("precondition: key perms should be bad")
	}

	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify fix was applied
	info, err := os.Stat(privKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600, got %04o", info.Mode().Perm())
	}

	// Verify fix was recorded
	found := false
	for _, f := range tr.Fixed {
		if f.Action == "chmod" && strings.Contains(f.Path, "id_ed25519") {
			found = true
		}
	}
	if !found {
		t.Error("expected chmod fix for key perms")
	}
}

func TestHealTenant_FixDirExposure(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "bob")

	// Make .polis world-readable
	os.Chmod(filepath.Join(siteDir, ".polis"), 0755)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.DirExposure.OK {
		t.Fatal("precondition: dir exposure should be bad")
	}

	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify fix was applied
	info, err := os.Stat(filepath.Join(siteDir, ".polis"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("expected 0700, got %04o", info.Mode().Perm())
	}

	found := false
	for _, f := range tr.Fixed {
		if f.Action == "chmod" && f.Path == ".polis" {
			found = true
		}
	}
	if !found {
		t.Error("expected chmod fix for dir exposure")
	}
}

func TestHealTenant_StripExecuteBit(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "carol")

	// Create file with execute bit
	contentDir := filepath.Join(siteDir, "content")
	os.MkdirAll(contentDir, 0755)
	filePath := filepath.Join(contentDir, "readme.txt")
	os.WriteFile(filePath, []byte("hello"), 0755)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify execute bit stripped
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0111 != 0 {
		t.Errorf("execute bit should be stripped, got %04o", info.Mode().Perm())
	}

	found := false
	for _, f := range tr.Fixed {
		if f.Action == "chmod" && strings.Contains(f.Path, "readme.txt") {
			found = true
		}
	}
	if !found {
		t.Error("expected chmod fix for executable bit")
	}
}

func TestHealTenant_QuarantineSymlink(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "dave")

	// Create symlink
	contentDir := filepath.Join(siteDir, "content")
	os.MkdirAll(contentDir, 0755)
	symlinkPath := filepath.Join(contentDir, "sneaky")
	os.Symlink("/etc/passwd", symlinkPath)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify symlink removed from original location
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Error("symlink should be removed from original location")
	}

	// Verify quarantine recorded
	if len(tr.Quarantined) == 0 {
		t.Fatal("expected quarantined file")
	}
	found := false
	for _, q := range tr.Quarantined {
		if strings.Contains(q.Path, "sneaky") && q.Action == "quarantine" {
			found = true
		}
	}
	if !found {
		t.Error("expected quarantine action for symlink")
	}
}

func TestHealTenant_QuarantineSuspiciousExtension(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "eve")

	// Create PHP file
	contentDir := filepath.Join(siteDir, "content")
	os.MkdirAll(contentDir, 0755)
	phpPath := filepath.Join(contentDir, "hack.php")
	os.WriteFile(phpPath, []byte("<?php echo 1; ?>"), 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify file removed
	if _, err := os.Stat(phpPath); !os.IsNotExist(err) {
		t.Error("php file should be removed from original location")
	}

	// Verify quarantine recorded
	found := false
	for _, q := range tr.Quarantined {
		if strings.Contains(q.Path, "hack.php") {
			found = true
		}
	}
	if !found {
		t.Error("expected quarantine for .php file")
	}
}

func TestHealTenant_QuarantineManifest(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "frank")

	// Create suspicious file
	contentDir := filepath.Join(siteDir, "content")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "evil.sh"), []byte("#!/bin/bash"), 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	HealTenant(siteDir, quarantineDir, cr, false)

	// Find manifest
	var manifestPath string
	filepath.WalkDir(quarantineDir, func(path string, d os.DirEntry, err error) error {
		if d != nil && d.Name() == "manifest.json" {
			manifestPath = path
			return filepath.SkipAll
		}
		return nil
	})

	if manifestPath == "" {
		t.Fatal("manifest.json not found in quarantine")
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	var entries []ManifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("manifest should have entries")
	}

	entry := entries[0]
	if !strings.Contains(entry.OriginalPath, "evil.sh") {
		t.Errorf("manifest should record original path, got: %s", entry.OriginalPath)
	}
	if !strings.Contains(entry.Reason, ".sh") {
		t.Errorf("manifest should record reason, got: %s", entry.Reason)
	}
}

func TestHealTenant_DryRun(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "grace")

	// Create multiple issues
	privKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")
	os.Chmod(privKeyPath, 0644)
	contentDir := filepath.Join(siteDir, "content")
	os.MkdirAll(contentDir, 0755)
	phpPath := filepath.Join(contentDir, "hack.php")
	os.WriteFile(phpPath, []byte("<?php"), 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, true) // dry-run

	// Fixes should be reported
	if len(tr.Fixed) == 0 {
		t.Error("expected fixes reported in dry-run")
	}
	if len(tr.Quarantined) == 0 {
		t.Error("expected quarantines reported in dry-run")
	}

	// But nothing should actually change
	info, _ := os.Stat(privKeyPath)
	if info.Mode().Perm() == 0600 {
		t.Error("dry-run should not change key permissions")
	}

	if _, err := os.Stat(phpPath); os.IsNotExist(err) {
		t.Error("dry-run should not move files to quarantine")
	}
}

func TestHealTenant_SkipsNonCorrectable(t *testing.T) {
	base := t.TempDir()
	siteDir := filepath.Join(base, "heidi")
	// Create minimal dir without .well-known — non-correctable
	os.MkdirAll(siteDir, 0755)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	if tr.Skipped == 0 {
		t.Error("expected non-correctable issues to be counted as skipped")
	}
}

func TestHealTenant_CleanTenant(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "ivan")

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	if len(tr.Fixed) != 0 {
		t.Errorf("clean tenant should have no fixes, got %d", len(tr.Fixed))
	}
	if len(tr.Quarantined) != 0 {
		t.Errorf("clean tenant should have no quarantines, got %d", len(tr.Quarantined))
	}
	if tr.Skipped != 0 {
		t.Errorf("clean tenant should have no skips, got %d", tr.Skipped)
	}
}

func TestHealAll(t *testing.T) {
	base := t.TempDir()
	tenantsDir := filepath.Join(base, "tenants")
	quarantineDir := filepath.Join(base, "quarantine")

	// Create one clean tenant
	setupValidTenant(t, tenantsDir, "alice")

	// Create one with bad key perms
	siteDir := setupValidTenant(t, tenantsDir, "bob")
	os.Chmod(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"), 0644)

	result := HealAll(tenantsDir, quarantineDir, false)

	if result.Tenants != 2 {
		t.Errorf("expected 2 tenants, got %d", result.Tenants)
	}
	if result.TotalFixed == 0 {
		t.Error("expected at least one fix")
	}
	if result.DurationMs < 0 {
		t.Error("duration should be non-negative")
	}
}

// ============ Provisioning tests ============

func TestHealTenant_ProvisionsMissingPolicies(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "prov-policies")

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify public policy file was created
	pubPolicyPath := filepath.Join(siteDir, "policies", "rules.jsonl")
	data, err := os.ReadFile(pubPolicyPath)
	if err != nil {
		t.Fatalf("public policy not created: %v", err)
	}
	if !strings.Contains(string(data), "pub.polis.comment.blessing") {
		t.Error("public policy should contain blessing rules")
	}

	// Verify private policy file was created
	privPolicyPath := filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")
	data, err = os.ReadFile(privPolicyPath)
	if err != nil {
		t.Fatalf("private policy not created: %v", err)
	}
	if !strings.Contains(string(data), "pub.polis.post") {
		t.Error("private policy should contain post rules")
	}
	info, _ := os.Stat(privPolicyPath)
	if info.Mode().Perm() != 0600 {
		t.Errorf("private policy permissions should be 0600, got %04o", info.Mode().Perm())
	}

	// Verify provisioned actions recorded
	if len(tr.Provisioned) < 2 {
		t.Errorf("expected at least 2 provisioned actions, got %d", len(tr.Provisioned))
	}
}

func TestHealTenant_ProvisionsStorageSalt(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "prov-salt")

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	HealTenant(siteDir, quarantineDir, cr, false)

	saltPath := filepath.Join(siteDir, ".polis", "storage-salt")
	data, err := os.ReadFile(saltPath)
	if err != nil {
		t.Fatalf("salt not created: %v", err)
	}
	if len(data) != 64 { // 32 bytes hex-encoded
		t.Errorf("expected 64 hex chars, got %d", len(data))
	}
	info, _ := os.Stat(saltPath)
	if info.Mode().Perm() != 0600 {
		t.Errorf("salt permissions should be 0600, got %04o", info.Mode().Perm())
	}
}

func TestHealTenant_ProvisionsDMDirectories(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "prov-dm-dirs")

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	HealTenant(siteDir, quarantineDir, cr, false)

	dmDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	info, err := os.Stat(dmDir)
	if err != nil {
		t.Fatalf("DM directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("DM path should be a directory")
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("DM dir permissions should be 0700, got %04o", info.Mode().Perm())
	}
}

func TestHealTenant_ProvisionsBundleJSON(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "prov-bundle")

	// Remove the bundle.json that setupValidTenant creates
	os.Remove(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"))

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	HealTenant(siteDir, quarantineDir, cr, false)

	bundlePath := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("bundle.json not created: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid bundle JSON: %v", err)
	}
	if raw["name"] != "pub.polis.core" {
		t.Errorf("expected name 'pub.polis.core', got %v", raw["name"])
	}
}

func TestHealTenant_EnsuresDMPoliciesOnExisting(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "prov-dm-rules")

	// Create private policies WITHOUT DM rules
	privatePolicyDir := filepath.Join(siteDir, ".polis", "policies")
	os.MkdirAll(privatePolicyDir, 0700)
	os.WriteFile(filepath.Join(privatePolicyDir, "rules.jsonl"),
		[]byte(`{"active":true,"policy":"allow pub.polis.post from all"}`+"\n"), 0600)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify DM rules were appended
	data, _ := os.ReadFile(filepath.Join(privatePolicyDir, "rules.jsonl"))
	if !strings.Contains(string(data), "pub.polis.dm") {
		t.Error("DM rules should be appended to existing private policies")
	}
	if !strings.Contains(string(data), "pub.polis.post") {
		t.Error("existing rules should be preserved")
	}

	// Verify action recorded
	found := false
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "DM") {
			found = true
		}
	}
	if !found {
		t.Error("expected DM provision action")
	}
}

func TestHealTenant_SkipsExistingFiles(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "prov-existing")

	// Add avatar to well-known so patrol doesn't flag it
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	wkData, _ := os.ReadFile(wkPath)
	var wkMap map[string]interface{}
	json.Unmarshal(wkData, &wkMap)
	wkMap["avatar"] = map[string]interface{}{"bg": "#2a5a6a", "fg": "#ffffff"}
	wkUpdated, _ := json.MarshalIndent(wkMap, "", "  ")
	os.WriteFile(wkPath, wkUpdated, 0644)

	// Create all expected files
	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"),
		[]byte(`{"active":true,"policy":"emit pub.polis.comment.blessing from self"}`+"\n"), 0644)

	os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0700)
	os.WriteFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"),
		[]byte(`{"active":true,"policy":"allow pub.polis.dm from following"}`+"\n"+
			`{"active":true,"policy":"omit pub.polis.feed from self"}`+"\n"+
			`{"active":true,"policy":"deny all from all"}`+"\n"), 0600)

	os.MkdirAll(filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv"), 0700)
	os.WriteFile(filepath.Join(siteDir, ".polis", "storage-salt"),
		[]byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"), 0600)
	os.MkdirAll(filepath.Join(siteDir, "content", "pub.polis.core", "tag"), 0755)

	// Create _base theme directory
	baseThemeDir := filepath.Join(siteDir, "site", "themes", "_base")
	os.MkdirAll(filepath.Join(baseThemeDir, "snippets"), 0755)
	for _, f := range []string{"index.html", "post.html", "posts.html", "comment.html", "comment-inline.html", "tag.html", "tag-index.html"} {
		os.WriteFile(filepath.Join(baseThemeDir, f), []byte("<html></html>"), 0644)
	}
	for _, f := range []string{"about.html", "also-reading.html", "blessed-comment.html", "comment-item.html", "polis-widget.html", "post-item.html"} {
		os.WriteFile(filepath.Join(baseThemeDir, "snippets", f), []byte("<div></div>"), 0644)
	}

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	if len(tr.Provisioned) != 0 {
		t.Errorf("expected no provisioned actions for fully provisioned tenant, got %d: %v", len(tr.Provisioned), tr.Provisioned)
	}
}

func TestHealTenant_ProvisionDryRun(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "prov-dryrun")

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, true) // dry-run

	// Should report provisioning actions
	if len(tr.Provisioned) == 0 {
		t.Error("expected provisioned actions reported in dry-run")
	}

	// But no files should be created
	if _, err := os.Stat(filepath.Join(siteDir, "policies", "rules.jsonl")); !os.IsNotExist(err) {
		t.Error("dry-run should not create public policy file")
	}
	if _, err := os.Stat(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")); !os.IsNotExist(err) {
		t.Error("dry-run should not create private policy file")
	}
	if _, err := os.Stat(filepath.Join(siteDir, ".polis", "storage-salt")); !os.IsNotExist(err) {
		t.Error("dry-run should not create storage salt")
	}
}

func TestHealTenant_ProvisionsTagDirectory(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "prov-tag")

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.TagDirectory.OK {
		t.Fatal("precondition: TagDirectory should fail (missing)")
	}

	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify tag directory was created
	tagDir := filepath.Join(siteDir, "content", "pub.polis.core", "tag")
	info, err := os.Stat(tagDir)
	if err != nil {
		t.Fatalf("tag directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("tag path should be a directory")
	}

	// Verify provisioned action recorded
	found := false
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "tag directory") {
			found = true
		}
	}
	if !found {
		t.Error("expected provision action for tag directory")
	}
}

func TestHealTenant_ProvisionsTagDirectory_DryRun(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "prov-tag-dry")

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, true) // dry-run

	// Should report provisioning action
	found := false
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "tag directory") {
			found = true
		}
	}
	if !found {
		t.Error("expected provision action reported in dry-run")
	}

	// But directory should NOT be created
	tagDir := filepath.Join(siteDir, "content", "pub.polis.core", "tag")
	if _, err := os.Stat(tagDir); !os.IsNotExist(err) {
		t.Error("dry-run should not create tag directory")
	}
}

func TestHealTenant_UpgradesBundleTypes(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "alice")
	quarantineDir := filepath.Join(base, "quarantine")

	// Write a valid bundle missing pub.polis.dm (simulates pre-DM provisioned site)
	oldBundle := bundle.DefaultCoreBundle()
	delete(oldBundle.Types, "pub.polis.dm")
	bundle.SaveBundle(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"), oldBundle)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.BundleTypes.OK {
		t.Fatal("precondition: BundleTypes should fail before healing")
	}

	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Should have provisioned the bundle upgrade
	found := false
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "missing types") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected provision action for bundle types upgrade")
	}

	// Verify the on-disk bundle now has all default types
	bundlePath := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	b, err := bundle.LoadBundle(bundlePath)
	if err != nil {
		t.Fatalf("LoadBundle after heal: %v", err)
	}
	if _, ok := b.Types["pub.polis.dm"]; !ok {
		t.Error("expected pub.polis.dm in upgraded bundle")
	}
	if _, ok := b.Types["pub.polis.post"]; !ok {
		t.Error("expected pub.polis.post in upgraded bundle")
	}

	// Re-run patrol — should pass now
	cr2 := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if !cr2.BundleTypes.OK {
		t.Errorf("BundleTypes should be OK after healing: %s", cr2.BundleTypes.Message)
	}
}

func TestHealTenant_UpgradesBundleTypes_DryRun(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "alice")
	quarantineDir := filepath.Join(base, "quarantine")

	// Write a valid bundle missing pub.polis.dm
	oldBundle := bundle.DefaultCoreBundle()
	delete(oldBundle.Types, "pub.polis.dm")
	bundle.SaveBundle(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"), oldBundle)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	tr := HealTenant(siteDir, quarantineDir, cr, true)

	// Should report the action
	found := false
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "missing types") {
			found = true
		}
	}
	if !found {
		t.Error("expected provision action reported in dry-run")
	}

	// But should NOT have modified the file
	cr2 := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr2.BundleTypes.OK {
		t.Error("dry-run should not modify bundle.json")
	}
}

func TestHealAll_IncludesProvisioning(t *testing.T) {
	base := t.TempDir()
	tenantsDir := filepath.Join(base, "tenants")
	quarantineDir := filepath.Join(base, "quarantine")

	// Create tenant without provisioning files
	setupValidTenant(t, tenantsDir, "alice")

	result := HealAll(tenantsDir, quarantineDir, false)

	if result.TotalProvisioned == 0 {
		t.Error("expected TotalProvisioned > 0 for unprovisioned tenant")
	}
}

// ============ DM directory permissions tests (Item 4) ============

func TestHealTenant_DMDirPerms(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "dm-perms")

	// Create DM directory with wrong permissions
	dmDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	os.MkdirAll(dmDir, 0755)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.DMDirectories.OK {
		t.Fatal("precondition: DM dir perms should be bad")
	}

	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify permissions fixed
	info, err := os.Stat(dmDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("expected 0700, got %04o", info.Mode().Perm())
	}

	// Verify fix was recorded
	found := false
	for _, f := range tr.Fixed {
		if f.Action == "chmod" && strings.Contains(f.Path, "dm") {
			found = true
		}
	}
	if !found {
		t.Error("expected chmod fix for DM dir perms")
	}
}

func TestHealTenant_DMDomainCase(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "alice")

	// Create DM conversation with mixed-case domains
	dmBase := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm")
	convDir := filepath.Join(dmBase, "conv")
	os.MkdirAll(convDir, 0700)

	conv := `{
  "peer_domain": "David.Polis.Pub",
  "peer_url": "https://David.Polis.Pub/",
  "messages": [
    {
      "id": "abc123",
      "from": "David.Polis.Pub",
      "to": "Alice.Polis.Pub",
      "encrypted_content": "test",
      "storage_nonce": "test",
      "timestamp": "2026-03-10T19:28:46Z",
      "status": "received"
    }
  ]
}
`
	os.WriteFile(filepath.Join(convDir, "test123.json"), []byte(conv), 0600)

	// Create conversations index with mixed-case
	idx := `{
  "conversations": [
    {
      "id": "test123",
      "peer_domain": "David.Polis.Pub",
      "peer_url": "https://David.Polis.Pub/",
      "last_message_at": "2026-03-10T19:28:46Z",
      "unread_count": 1
    }
  ]
}
`
	os.WriteFile(filepath.Join(dmBase, "conversations.json"), []byte(idx), 0600)

	// Run patrol to detect the issue
	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.DMDomainCase.OK {
		t.Fatal("precondition: DMDomainCase should fail")
	}

	// Heal
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify fix was recorded
	found := false
	for _, f := range tr.Fixed {
		if f.Action == "normalize" && strings.Contains(f.Detail, "lowercased") {
			found = true
		}
	}
	if !found {
		t.Error("expected normalize fix for DM domain case")
	}

	// Verify conversation file was fixed
	data, err := os.ReadFile(filepath.Join(convDir, "test123.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixedConv struct {
		PeerDomain string `json:"peer_domain"`
		Messages   []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &fixedConv); err != nil {
		t.Fatal(err)
	}
	if fixedConv.PeerDomain != "david.polis.pub" {
		t.Errorf("peer_domain = %q, want 'david.polis.pub'", fixedConv.PeerDomain)
	}
	if fixedConv.Messages[0].From != "david.polis.pub" {
		t.Errorf("from = %q, want 'david.polis.pub'", fixedConv.Messages[0].From)
	}
	if fixedConv.Messages[0].To != "alice.polis.pub" {
		t.Errorf("to = %q, want 'alice.polis.pub'", fixedConv.Messages[0].To)
	}

	// Verify index was fixed
	idxData, err := os.ReadFile(filepath.Join(dmBase, "conversations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixedIdx struct {
		Conversations []struct {
			PeerDomain string `json:"peer_domain"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(idxData, &fixedIdx); err != nil {
		t.Fatal(err)
	}
	if len(fixedIdx.Conversations) != 1 || fixedIdx.Conversations[0].PeerDomain != "david.polis.pub" {
		t.Errorf("index peer_domain not fixed: %+v", fixedIdx)
	}

	// Re-run patrol — should now pass
	cr2 := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if !cr2.DMDomainCase.OK {
		t.Errorf("DMDomainCase should pass after heal: %s", cr2.DMDomainCase.Message)
	}
}

func TestHealTenant_DMDomainCase_DryRun(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "bob")

	// Create DM conversation with mixed-case
	convDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm", "conv")
	os.MkdirAll(convDir, 0700)
	conv := `{"peer_domain":"Alice.COM","peer_url":"https://Alice.COM/","messages":[]}`
	os.WriteFile(filepath.Join(convDir, "test.json"), []byte(conv), 0600)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, true) // dry run

	// Should report the fix
	found := false
	for _, f := range tr.Fixed {
		if f.Action == "normalize" {
			found = true
		}
	}
	if !found {
		t.Error("dry run should still report the normalize fix")
	}

	// But file should NOT be modified
	data, _ := os.ReadFile(filepath.Join(convDir, "test.json"))
	if !strings.Contains(string(data), "Alice.COM") {
		t.Error("dry run should not modify files")
	}
}

// ============ DM preview encryption ============

func TestHealTenant_DMPreviewEncryption(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "alice")

	// Create DM conversations with plaintext previews
	dmBase := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm")
	os.MkdirAll(filepath.Join(dmBase, "conv"), 0700)

	// Also need storage salt for dm.NewStore
	dm.EnsureSalt(siteDir)

	idx := `{
  "conversations": [
    {
      "id": "conv1",
      "peer_domain": "bob.example.com",
      "peer_url": "https://bob.example.com",
      "last_message_at": "2026-03-10T19:28:46Z",
      "unread_count": 0,
      "last_preview": "Hello this is plaintext"
    },
    {
      "id": "conv2",
      "peer_domain": "carol.example.com",
      "peer_url": "https://carol.example.com",
      "last_message_at": "2026-03-10T20:00:00Z",
      "unread_count": 1,
      "last_preview": "Another plaintext preview"
    }
  ]
}
`
	os.WriteFile(filepath.Join(dmBase, "conversations.json"), []byte(idx), 0600)

	// Run patrol to detect the issue
	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.DMPreviewEncryption.OK {
		t.Fatal("precondition: DMPreviewEncryption should fail")
	}

	// Heal
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify fix was recorded
	found := false
	for _, f := range tr.Fixed {
		if f.Action == "encrypt" && strings.Contains(f.Detail, "plaintext DM previews") {
			found = true
		}
	}
	if !found {
		t.Error("expected encrypt fix for DM preview encryption")
	}

	// Verify previews are now encrypted in the file
	data, err := os.ReadFile(filepath.Join(dmBase, "conversations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Hello this is plaintext") {
		t.Error("plaintext preview should no longer appear in raw file")
	}
	if strings.Contains(string(data), "Another plaintext preview") {
		t.Error("plaintext preview should no longer appear in raw file")
	}

	// Re-run patrol — should now pass
	cr2 := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if !cr2.DMPreviewEncryption.OK {
		t.Errorf("DMPreviewEncryption should pass after heal: %s", cr2.DMPreviewEncryption.Message)
	}
}

func TestHealTenant_DMPreviewEncryption_DryRun(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "bob")

	dmBase := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm")
	os.MkdirAll(filepath.Join(dmBase, "conv"), 0700)
	dm.EnsureSalt(siteDir)

	idx := `{"conversations":[{"id":"conv1","peer_domain":"alice.com","last_preview":"secret plaintext"}]}`
	os.WriteFile(filepath.Join(dmBase, "conversations.json"), []byte(idx), 0600)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, true) // dry run

	// Should report the fix
	found := false
	for _, f := range tr.Fixed {
		if f.Action == "encrypt" {
			found = true
		}
	}
	if !found {
		t.Error("dry run should still report the encrypt fix")
	}

	// But file should NOT be modified
	data, _ := os.ReadFile(filepath.Join(dmBase, "conversations.json"))
	if !strings.Contains(string(data), "secret plaintext") {
		t.Error("dry run should not modify files")
	}
}

// ============ Policy consolidation checks (deny-all, feed-self-omit) ============

func TestHealTenant_AppendsDenyAllCatchAll(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "alice")
	quarantineDir := filepath.Join(base, "quarantine")

	// Create private policies WITHOUT deny-all catch-all
	os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0700)
	os.WriteFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"),
		[]byte(`{"version":1,"generator":"polis-cli-go/0.57.0"}`+"\n"+
			`{"active":true,"policy":"allow pub.polis.post from all"}`+"\n"+
			`{"active":true,"policy":"omit pub.polis.feed from self"}`+"\n"), 0600)

	// Also create public policies so that check passes
	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"),
		[]byte(`{"active":true,"policy":"emit pub.polis.comment.blessing from self"}`+"\n"), 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.PolicyDenyAll.OK {
		t.Fatal("precondition: PolicyDenyAll should fail")
	}
	if !cr.PolicyFeedSelfOmit.OK {
		t.Fatal("precondition: PolicyFeedSelfOmit should pass (it's present)")
	}

	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Should have provisioned the deny-all rule
	found := false
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "default-deny catch-all") {
			found = true
		}
	}
	if !found {
		t.Error("expected provision action for deny-all catch-all")
	}

	// Verify the rule was appended
	data, _ := os.ReadFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"))
	if !strings.Contains(string(data), `"deny all from all"`) {
		t.Error("expected deny all from all to be appended")
	}

	// Verify it's the LAST rule
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, `"deny all from all"`) {
		t.Errorf("deny all from all should be last rule, got: %s", lastLine)
	}

	// Re-run patrol — should pass now
	cr2 := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if !cr2.PolicyDenyAll.OK {
		t.Errorf("PolicyDenyAll should pass after healing: %s", cr2.PolicyDenyAll.Message)
	}
}

func TestHealTenant_AppendsFeedSelfOmit(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "bob")
	quarantineDir := filepath.Join(base, "quarantine")

	// Create private policies WITHOUT feed self-omit but WITH deny-all
	os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0700)
	os.WriteFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"),
		[]byte(`{"version":1,"generator":"polis-cli-go/0.57.0"}`+"\n"+
			`{"active":true,"policy":"allow pub.polis.post from all"}`+"\n"+
			`{"active":true,"policy":"deny all from all"}`+"\n"), 0600)

	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"),
		[]byte(`{"active":true,"policy":"emit pub.polis.comment.blessing from self"}`+"\n"), 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if !cr.PolicyDenyAll.OK {
		t.Fatal("precondition: PolicyDenyAll should pass (it's present)")
	}
	if cr.PolicyFeedSelfOmit.OK {
		t.Fatal("precondition: PolicyFeedSelfOmit should fail")
	}

	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Should have provisioned the feed self-omit rule
	found := false
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "feed self-omit") {
			found = true
		}
	}
	if !found {
		t.Error("expected provision action for feed self-omit")
	}

	// Verify the rule was appended
	data, _ := os.ReadFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"))
	if !strings.Contains(string(data), `"omit pub.polis.feed from self"`) {
		t.Error("expected omit pub.polis.feed from self to be appended")
	}

	// Re-run patrol — should pass now
	cr2 := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if !cr2.PolicyFeedSelfOmit.OK {
		t.Errorf("PolicyFeedSelfOmit should pass after healing: %s", cr2.PolicyFeedSelfOmit.Message)
	}
}

func TestHealTenant_AppendsBothMissingRules(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "carol")
	quarantineDir := filepath.Join(base, "quarantine")

	// Create private policies missing BOTH rules
	os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0700)
	os.WriteFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"),
		[]byte(`{"version":1,"generator":"polis-cli-go/0.57.0"}`+"\n"+
			`{"active":true,"policy":"allow pub.polis.post from all"}`+"\n"+
			`{"active":true,"policy":"allow pub.polis.dm from following"}`+"\n"), 0600)

	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"),
		[]byte(`{"active":true,"policy":"emit pub.polis.comment.blessing from self"}`+"\n"), 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Should have 2 provisioning actions for the rules
	ruleProvisions := 0
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "feed self-omit") || strings.Contains(p.Detail, "default-deny") {
			ruleProvisions++
		}
	}
	if ruleProvisions != 2 {
		t.Errorf("expected 2 rule provisions, got %d", ruleProvisions)
	}

	// Verify order: feed-omit should come before deny-all in the file
	data, _ := os.ReadFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"))
	feedIdx := strings.Index(string(data), "omit pub.polis.feed from self")
	denyIdx := strings.Index(string(data), "deny all from all")
	if feedIdx >= denyIdx {
		t.Error("feed self-omit should appear before deny-all catch-all")
	}

	// Re-run patrol — both should pass
	cr2 := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if !cr2.PolicyFeedSelfOmit.OK {
		t.Errorf("PolicyFeedSelfOmit should pass: %s", cr2.PolicyFeedSelfOmit.Message)
	}
	if !cr2.PolicyDenyAll.OK {
		t.Errorf("PolicyDenyAll should pass: %s", cr2.PolicyDenyAll.Message)
	}
}

func TestHealTenant_PolicyRules_DryRun(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "dave")
	quarantineDir := filepath.Join(base, "quarantine")

	// Create private policies missing both rules
	os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0700)
	os.WriteFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"),
		[]byte(`{"active":true,"policy":"allow pub.polis.post from all"}`+"\n"), 0600)

	os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
	os.WriteFile(filepath.Join(siteDir, "policies", "rules.jsonl"),
		[]byte(`{"active":true,"policy":"emit pub.polis.comment.blessing from self"}`+"\n"), 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	tr := HealTenant(siteDir, quarantineDir, cr, true) // dry-run

	// Should report provisions
	ruleProvisions := 0
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "feed self-omit") || strings.Contains(p.Detail, "default-deny") {
			ruleProvisions++
		}
	}
	if ruleProvisions != 2 {
		t.Errorf("expected 2 rule provisions reported in dry-run, got %d", ruleProvisions)
	}

	// But file should NOT be modified
	data, _ := os.ReadFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"))
	if strings.Contains(string(data), "deny all from all") {
		t.Error("dry-run should not modify policy file")
	}
}

func TestHealTenant_ProvisionsBaseTheme(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "base-theme-test")

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.BaseTheme.OK {
		t.Fatal("expected BaseTheme check to fail for tenant without _base")
	}

	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	found := false
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "_base theme") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected _base theme provisioning action")
	}

	// Verify _base was created
	if _, err := os.Stat(filepath.Join(siteDir, "site", "themes", "_base", "index.html")); err != nil {
		t.Error("expected _base/index.html to exist after provisioning")
	}
}

func TestHealTenant_CleansStaleThemeFiles(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "stale-theme-test")

	// Create _base
	baseDir := filepath.Join(siteDir, "site", "themes", "_base")
	os.MkdirAll(filepath.Join(baseDir, "snippets"), 0755)
	baseContent := "<html>BASE</html>"
	os.WriteFile(filepath.Join(baseDir, "index.html"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(baseDir, "post.html"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(baseDir, "snippets", "about.html"), []byte("<div>ABOUT</div>"), 0644)

	// Create a theme with stale duplicates
	themeDir := filepath.Join(siteDir, "site", "themes", "turbo")
	os.MkdirAll(filepath.Join(themeDir, "snippets"), 0755)
	os.WriteFile(filepath.Join(themeDir, "turbo.css"), []byte("body{}"), 0644)
	// These match _base — should be removed
	os.WriteFile(filepath.Join(themeDir, "index.html"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(themeDir, "post.html"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(themeDir, "snippets", "about.html"), []byte("<div>ABOUT</div>"), 0644)
	// This differs — should be preserved
	os.WriteFile(filepath.Join(themeDir, "comment.html"), []byte("<html>CUSTOM</html>"), 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Should have removed 3 stale files
	staleRemoved := 0
	for _, f := range tr.Fixed {
		if f.Action == "remove_stale_theme_file" {
			staleRemoved++
		}
	}
	if staleRemoved != 3 {
		t.Errorf("expected 3 stale files removed, got %d", staleRemoved)
	}

	// index.html and post.html should be gone
	if _, err := os.Stat(filepath.Join(themeDir, "index.html")); !os.IsNotExist(err) {
		t.Error("stale index.html should have been removed")
	}
	if _, err := os.Stat(filepath.Join(themeDir, "post.html")); !os.IsNotExist(err) {
		t.Error("stale post.html should have been removed")
	}

	// Custom comment.html should be preserved
	if _, err := os.Stat(filepath.Join(themeDir, "comment.html")); err != nil {
		t.Error("custom comment.html should be preserved")
	}

	// CSS file should be preserved
	if _, err := os.Stat(filepath.Join(themeDir, "turbo.css")); err != nil {
		t.Error("CSS file should be preserved")
	}
}

func TestHealTenant_ProvisionAvatar(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "eve")

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if cr.Avatar.OK {
		t.Fatal("precondition: avatar should be missing")
	}

	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify avatar was provisioned
	found := false
	for _, p := range tr.Provisioned {
		if p.Detail == "generated default avatar" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'generated default avatar' in provisioned actions")
	}

	// Verify well-known now has avatar
	data, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	var wk map[string]interface{}
	json.Unmarshal(data, &wk)
	avatar, ok := wk["avatar"].(map[string]interface{})
	if !ok {
		t.Fatal("avatar should be present in well-known after heal")
	}
	bg, _ := avatar["bg"].(string)
	fg, _ := avatar["fg"].(string)
	if len(bg) != 7 || bg[0] != '#' {
		t.Errorf("avatar bg should be #rrggbb, got %q", bg)
	}
	if fg != "#ffffff" {
		t.Errorf("avatar fg = %q, want #ffffff", fg)
	}
}

func TestHealTenant_AvatarNotOverwritten(t *testing.T) {
	base := t.TempDir()
	siteDir := setupValidTenant(t, base, "frank")

	// Add custom avatar
	wkPath := filepath.Join(siteDir, ".well-known", "polis")
	data, _ := os.ReadFile(wkPath)
	var wk map[string]interface{}
	json.Unmarshal(data, &wk)
	wk["avatar"] = map[string]interface{}{"bg": "#112233", "fg": "#ffffff"}
	updated, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(wkPath, updated, 0644)

	cr := patrol.CheckTenant(siteDir, patrol.CheckOptions{})
	if !cr.Avatar.OK {
		t.Fatal("precondition: avatar should be OK")
	}

	quarantineDir := filepath.Join(base, "quarantine")
	tr := HealTenant(siteDir, quarantineDir, cr, false)

	// Verify no avatar provisioning action
	for _, p := range tr.Provisioned {
		if strings.Contains(p.Detail, "avatar") {
			t.Error("should not provision avatar when one already exists")
		}
	}

	// Verify original avatar preserved
	data, _ = os.ReadFile(wkPath)
	json.Unmarshal(data, &wk)
	avatar, _ := wk["avatar"].(map[string]interface{})
	if avatar["bg"] != "#112233" {
		t.Errorf("avatar bg should be preserved as #112233, got %v", avatar["bg"])
	}
}
