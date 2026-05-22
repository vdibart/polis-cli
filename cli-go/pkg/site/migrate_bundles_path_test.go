package site

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyPrivateContentExists(t *testing.T) {
	siteDir := t.TempDir()
	if LegacyPrivateContentExists(siteDir) {
		t.Error("expected false on fresh site")
	}
	os.MkdirAll(filepath.Join(siteDir, ".polis", "content"), 0700)
	if !LegacyPrivateContentExists(siteDir) {
		t.Error("expected true once .polis/content/ exists")
	}
}

func TestLegacyActiveThemeInWellKnown(t *testing.T) {
	siteDir := t.TempDir()
	os.MkdirAll(filepath.Join(siteDir, ".well-known"), 0755)

	if LegacyActiveThemeInWellKnown(siteDir) {
		t.Error("missing well-known should report false")
	}

	os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), []byte(`{"public_key":"x"}`), 0644)
	if LegacyActiveThemeInWellKnown(siteDir) {
		t.Error("well-known without active_theme should report false")
	}

	os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), []byte(`{"active_theme":"vice"}`), 0644)
	if !LegacyActiveThemeInWellKnown(siteDir) {
		t.Error("well-known with active_theme should report true")
	}
}

func TestMigratePrivateBundlesPath_HappyPath(t *testing.T) {
	siteDir := t.TempDir()
	// Set up a legacy tree with a draft post + a pending comment.
	postDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "posts", "drafts")
	commentDir := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "comments", "pending")
	os.MkdirAll(postDir, 0755)
	os.MkdirAll(commentDir, 0755)
	os.WriteFile(filepath.Join(postDir, "my-draft.md"), []byte("draft body"), 0644)
	os.WriteFile(filepath.Join(commentDir, "my-comment.md"), []byte("comment body"), 0644)

	if err := MigratePrivateBundlesPath(siteDir); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// New tree should have the data.
	newDraft := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "my-draft.md")
	if data, err := os.ReadFile(newDraft); err != nil || string(data) != "draft body" {
		t.Errorf("draft not migrated: err=%v data=%q", err, string(data))
	}
	newComment := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "comments", "pending", "my-comment.md")
	if data, err := os.ReadFile(newComment); err != nil || string(data) != "comment body" {
		t.Errorf("comment not migrated: err=%v data=%q", err, string(data))
	}

	// Legacy tree should be gone.
	if _, err := os.Stat(filepath.Join(siteDir, ".polis", "content")); !os.IsNotExist(err) {
		t.Error("legacy .polis/content/ should be removed")
	}
}

func TestMigratePrivateBundlesPath_PreservesExistingShapes(t *testing.T) {
	siteDir := t.TempDir()
	// Pre-existing shapes/ dir (from EnsureReferencePayload).
	shapesDir := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "shapes", "v3")
	os.MkdirAll(shapesDir, 0755)
	os.WriteFile(filepath.Join(shapesDir, "index.html"), []byte("<html>SHAPE</html>"), 0644)

	// Legacy data alongside.
	legacyPosts := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "posts", "drafts")
	os.MkdirAll(legacyPosts, 0755)
	os.WriteFile(filepath.Join(legacyPosts, "draft.md"), []byte("body"), 0644)

	if err := MigratePrivateBundlesPath(siteDir); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Shapes preserved.
	if _, err := os.Stat(filepath.Join(shapesDir, "index.html")); err != nil {
		t.Error("shapes/v3/index.html should be preserved")
	}
	// Legacy migrated.
	if _, err := os.Stat(filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "draft.md")); err != nil {
		t.Error("legacy draft should be migrated")
	}
}

func TestMigratePrivateBundlesPath_Idempotent(t *testing.T) {
	siteDir := t.TempDir()
	// No legacy tree — should be no-op.
	if err := MigratePrivateBundlesPath(siteDir); err != nil {
		t.Fatalf("first migrate (no-op): %v", err)
	}
	if err := MigratePrivateBundlesPath(siteDir); err != nil {
		t.Fatalf("second migrate (no-op): %v", err)
	}
}

func TestMigratePrivateBundlesPath_RefusesAmbiguousMerge(t *testing.T) {
	siteDir := t.TempDir()
	// Both old and new have a posts/ subtree with conflicting content.
	legacyPosts := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "posts")
	newPosts := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "posts")
	os.MkdirAll(legacyPosts, 0755)
	os.MkdirAll(newPosts, 0755)
	os.WriteFile(filepath.Join(legacyPosts, "old.md"), []byte("old"), 0644)
	os.WriteFile(filepath.Join(newPosts, "new.md"), []byte("new"), 0644)

	if err := MigratePrivateBundlesPath(siteDir); err == nil {
		t.Error("expected error for ambiguous merge")
	}
}
