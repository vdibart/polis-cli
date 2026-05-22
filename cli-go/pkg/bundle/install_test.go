package bundle

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReferencePayloadFS_ServesShapeAssets verifies the FS exposed for the
// webapp's /bundle-assets/ route returns the v4 stream.js + stream.css with
// the canonical content. Step-06/6.a depends on this for SPA hydration
// without duplicating files in webapp/internal/webui/www.
func TestReferencePayloadFS_ServesShapeAssets(t *testing.T) {
	fsys := ReferencePayloadFS()

	jsBytes, err := fs.ReadFile(fsys, "pub.polis.core/shapes/v4/stream.js")
	if err != nil {
		t.Fatalf("read stream.js: %v", err)
	}
	if len(jsBytes) < 100 {
		t.Errorf("stream.js too small (%d bytes); expected the full controller", len(jsBytes))
	}
	if !strings.Contains(string(jsBytes), "PolisStream") {
		t.Errorf("stream.js missing PolisStream namespace")
	}
	if !strings.Contains(string(jsBytes), "afterRender") {
		t.Errorf("stream.js missing afterRender registry (step-06/6.a)")
	}
	if !strings.Contains(string(jsBytes), "registerRenderer") {
		t.Errorf("stream.js missing registerRenderer registry (step-06/6.a)")
	}

	cssBytes, err := fs.ReadFile(fsys, "pub.polis.core/shapes/v4/stream.css")
	if err != nil {
		t.Fatalf("read stream.css: %v", err)
	}
	if len(cssBytes) < 100 {
		t.Errorf("stream.css too small (%d bytes)", len(cssBytes))
	}
	if !strings.Contains(string(cssBytes), "body.is-owner") {
		t.Errorf("stream.css missing body.is-owner gate (step-06/6.a)")
	}
}

func TestEnsureReferencePayload_CopiesShapeTemplates(t *testing.T) {
	siteDir := t.TempDir()
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}

	// blog shape templates should now exist in the tenant bundle.
	shapeDir := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "shapes", "v3")
	for _, name := range []string{
		"index.html", "post.html", "posts.html",
		"comment.html", "comment-inline.html",
		"tag.html", "tag-index.html",
	} {
		p := filepath.Join(shapeDir, name)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("missing template %s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("template %s is empty", name)
		}
	}

	// Snippets should be present.
	snippetsDir := filepath.Join(shapeDir, "snippets")
	entries, err := os.ReadDir(snippetsDir)
	if err != nil {
		t.Fatalf("snippets dir missing: %v", err)
	}
	if len(entries) < 5 {
		t.Errorf("expected ≥5 snippets, got %d", len(entries))
	}
}

func TestEnsureReferencePayload_Idempotent(t *testing.T) {
	siteDir := t.TempDir()
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Capture post.html content and mtime.
	postPath := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "shapes", "v3", "post.html")
	first, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("read post.html: %v", err)
	}

	// Second run should not error and should leave content identical.
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("second install: %v", err)
	}
	second, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("re-read post.html: %v", err)
	}
	if string(first) != string(second) {
		t.Error("post.html content changed between installs")
	}
}

// TestEnsureReferencePayload_MtimeIdempotent verifies that re-running the
// install on an already-correct site doesn't bump file mtimes — important
// because Medic now invokes this on every cycle once a refresh is needed.
// Without the content-equality short-circuit in install.go, every cycle
// would rewrite every file.
func TestEnsureReferencePayload_MtimeIdempotent(t *testing.T) {
	siteDir := t.TempDir()
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	postPath := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "shapes", "v3", "post.html")
	info1, err := os.Stat(postPath)
	if err != nil {
		t.Fatalf("stat post.html: %v", err)
	}
	mtime1 := info1.ModTime()

	// Sleep briefly so a re-write would produce a differing mtime.
	time.Sleep(20 * time.Millisecond)

	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("second install: %v", err)
	}
	info2, err := os.Stat(postPath)
	if err != nil {
		t.Fatalf("re-stat post.html: %v", err)
	}
	if !info2.ModTime().Equal(mtime1) {
		t.Errorf("post.html mtime changed across no-op install: was %v, now %v", mtime1, info2.ModTime())
	}
}

// TestEnsureReferencePayload_RecordsVersions verifies that the install stamps
// the registry with the bundle's shipped shape/theme versions. Patrol's
// checkBundleReferencePayload uses these stamps to decide when to re-install.
func TestEnsureReferencePayload_RecordsVersions(t *testing.T) {
	siteDir := t.TempDir()
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("install: %v", err)
	}

	reg, err := LoadRegistry(siteDir)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	entry := reg.FindInstalledBundle("pub.polis.core")
	if entry == nil {
		t.Fatal("registry should have a pub.polis.core entry")
	}
	bundle := DefaultCoreBundle()
	if got, want := entry.ShapeVersions["v3"], bundle.Shapes["v3"].Version; got != want {
		t.Errorf("shape v3 version = %q, want %q", got, want)
	}
	if got, want := entry.ThemeVersions["vice"], bundle.Themes["vice"].Version; got != want {
		t.Errorf("theme vice version = %q, want %q", got, want)
	}
	// Sanity: NeedsRefresh should now report false.
	if reg.NeedsRefresh(bundle) {
		t.Error("NeedsRefresh should be false after install")
	}
}

// TestNeedsRefresh_DetectsShapeVersionDrift verifies the version check
// catches an advanced shape version even when files happen to be identical.
func TestNeedsRefresh_DetectsShapeVersionDrift(t *testing.T) {
	siteDir := t.TempDir()
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("install: %v", err)
	}
	reg, _ := LoadRegistry(siteDir)
	entry := reg.FindInstalledBundle("pub.polis.core")
	entry.ShapeVersions["v3"] = "0.9.0-old"
	if err := SaveRegistry(siteDir, reg); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, _ := LoadRegistry(siteDir)
	if !reloaded.NeedsRefresh(DefaultCoreBundle()) {
		t.Error("NeedsRefresh should be true when shape version differs from shipped")
	}
}

func TestEnsureReferencePayload_PreservesPrivateState(t *testing.T) {
	siteDir := t.TempDir()

	// Simulate pre-existing private state inside the bundle dir — e.g., a draft.
	draftsDir := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	if err := os.MkdirAll(draftsDir, 0755); err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(draftsDir, "my-draft.md")
	if err := os.WriteFile(draftPath, []byte("existing draft"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("EnsureReferencePayload: %v", err)
	}

	// Draft should survive — the reference payload doesn't touch posts/drafts.
	got, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("draft file missing after install: %v", err)
	}
	if string(got) != "existing draft" {
		t.Errorf("draft content changed: %q", string(got))
	}
}

func TestEnsureReferencePayload_UnknownBundle(t *testing.T) {
	siteDir := t.TempDir()
	err := EnsureReferencePayload(siteDir, "com.nonexistent.bundle")
	if err == nil {
		t.Fatal("expected error for unknown bundle")
	}
}

func TestEnsureReferencePayload_EmptyArgs(t *testing.T) {
	if err := EnsureReferencePayload("", "pub.polis.core"); err == nil {
		t.Error("expected error for empty siteDir")
	}
	if err := EnsureReferencePayload(t.TempDir(), ""); err == nil {
		t.Error("expected error for empty bundleName")
	}
}

// ============================================================================
// CompareReferencePayload Tests (F1)
// ============================================================================

func TestCompareReferencePayload_CleanMatch(t *testing.T) {
	siteDir := t.TempDir()
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("install: %v", err)
	}
	mismatches, err := CompareReferencePayload(siteDir, "pub.polis.core")
	if err != nil {
		t.Fatalf("CompareReferencePayload: %v", err)
	}
	if len(mismatches) != 0 {
		t.Errorf("fresh install should have zero mismatches, got %v", mismatches)
	}
}

func TestCompareReferencePayload_DetectsMutation(t *testing.T) {
	siteDir := t.TempDir()
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Tamper with an installed template.
	victim := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "shapes", "v3", "post.html")
	if err := os.WriteFile(victim, []byte("<!-- corrupted -->"), 0644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	mismatches, err := CompareReferencePayload(siteDir, "pub.polis.core")
	if err != nil {
		t.Fatalf("CompareReferencePayload: %v", err)
	}
	if len(mismatches) != 1 {
		t.Fatalf("expected 1 mismatch, got %d (%v)", len(mismatches), mismatches)
	}
	if !strings.Contains(mismatches[0], "post.html") {
		t.Errorf("mismatch path should include post.html, got %q", mismatches[0])
	}
}

func TestCompareReferencePayload_DetectsDeletion(t *testing.T) {
	siteDir := t.TempDir()
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("install: %v", err)
	}
	victim := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "shapes", "v3", "index.html")
	if err := os.Remove(victim); err != nil {
		t.Fatalf("delete: %v", err)
	}
	mismatches, err := CompareReferencePayload(siteDir, "pub.polis.core")
	if err != nil {
		t.Fatalf("CompareReferencePayload: %v", err)
	}
	var found bool
	for _, m := range mismatches {
		if strings.Contains(m, "index.html") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("deleted index.html should appear in mismatches, got %v", mismatches)
	}
}

func TestCompareReferencePayload_IgnoresExtraFiles(t *testing.T) {
	siteDir := t.TempDir()
	if err := EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Tenant adds a draft alongside the reference payload.
	draftsDir := filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	os.MkdirAll(draftsDir, 0755)
	if err := os.WriteFile(filepath.Join(draftsDir, "my-draft.md"), []byte("# draft"), 0644); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	mismatches, err := CompareReferencePayload(siteDir, "pub.polis.core")
	if err != nil {
		t.Fatalf("CompareReferencePayload: %v", err)
	}
	if len(mismatches) != 0 {
		t.Errorf("tenant-authored file shouldn't count as a mismatch, got %v", mismatches)
	}
}

func TestCompareReferencePayload_UnknownBundle(t *testing.T) {
	_, err := CompareReferencePayload(t.TempDir(), "com.nonexistent.bundle")
	if err == nil {
		t.Error("expected error for unknown bundle")
	}
}

func TestCompareReferencePayload_EmptyArgs(t *testing.T) {
	if _, err := CompareReferencePayload("", "pub.polis.core"); err == nil {
		t.Error("expected error for empty siteDir")
	}
	if _, err := CompareReferencePayload(t.TempDir(), ""); err == nil {
		t.Error("expected error for empty bundleName")
	}
}
