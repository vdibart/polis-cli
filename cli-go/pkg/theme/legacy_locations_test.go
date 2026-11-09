package theme

import (
	"context"
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyThemeLocationsExist(t *testing.T) {
	dataDir := t.TempDir()
	if LegacyThemeLocationsExist(storage.At(dataDir)) {
		t.Error("expected false on fresh dir")
	}
	os.MkdirAll(filepath.Join(dataDir, "site", "themes", "vice"), 0755)
	if !LegacyThemeLocationsExist(storage.At(dataDir)) {
		t.Error("expected true once site/themes/ exists")
	}
}

func TestLegacyThemeLocationsExist_DotPolisThemes(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, ".polis", "themes", "sols"), 0755)
	if !LegacyThemeLocationsExist(storage.At(dataDir)) {
		t.Error("expected true once .polis/themes/ exists")
	}
}

func TestRemoveLegacyThemeLocations_Removes(t *testing.T) {
	dataDir := t.TempDir()
	siteThemes := filepath.Join(dataDir, "site", "themes", "vice")
	polisThemes := filepath.Join(dataDir, ".polis", "themes", "sols")
	os.MkdirAll(siteThemes, 0755)
	os.MkdirAll(polisThemes, 0755)
	// Plant a tweak that should be lost (forced upgrade policy).
	os.WriteFile(filepath.Join(siteThemes, "vice.css"), []byte("user tweak"), 0644)

	if err := RemoveLegacyThemeLocations(storage.At(dataDir)); err != nil {
		t.Fatalf("RemoveLegacyThemeLocations: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "site", "themes")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("site/themes/ should be removed")
	}
	if _, err := os.Stat(filepath.Join(dataDir, ".polis", "themes")); !errors.Is(err, fs.ErrNotExist) {
		t.Error(".polis/themes/ should be removed")
	}
}

func TestRemoveLegacyThemeLocations_Idempotent(t *testing.T) {
	dataDir := t.TempDir()
	if err := RemoveLegacyThemeLocations(storage.At(dataDir)); err != nil {
		t.Fatalf("first call (no-op): %v", err)
	}
	if err := RemoveLegacyThemeLocations(storage.At(dataDir)); err != nil {
		t.Fatalf("second call (no-op): %v", err)
	}
}

// On a store with no filesystem there are no empty directories: a legacy
// location exists exactly when something is stored under it, and removing it
// deletes everything under the prefix.
func TestLegacyThemeLocations_OnAnObjectStore(t *testing.T) {
	site := storage.On("/nonexistent-site-root", storage.NewObjectStore(""))
	if LegacyThemeLocationsExist(site) {
		t.Error("expected false on an empty store")
	}
	ctx := context.Background()
	if err := site.Store().Put(ctx, ".polis/themes/sols/sols.css", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := site.Store().Put(ctx, "site/themes/vice/vice.css", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if !LegacyThemeLocationsExist(site) {
		t.Fatal("expected true once something is stored under a legacy location")
	}
	if err := RemoveLegacyThemeLocations(site); err != nil {
		t.Fatal(err)
	}
	if LegacyThemeLocationsExist(site) {
		t.Error("legacy locations still present after RemoveLegacyThemeLocations")
	}
	if err := RemoveLegacyThemeLocations(site); err != nil {
		t.Fatalf("second removal (no-op): %v", err)
	}
}

// ⚠️ An EMPTY legacy directory on a filesystem still means the tenant needs
// migrating, which a prefix listing cannot see.
func TestLegacyThemeLocationsExist_EmptyDirectory(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, "site", "themes"), 0755)
	if !LegacyThemeLocationsExist(storage.At(dataDir)) {
		t.Error("expected true for an empty site/themes/ directory")
	}
}
