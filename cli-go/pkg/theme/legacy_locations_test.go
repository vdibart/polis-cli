package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyThemeLocationsExist(t *testing.T) {
	dataDir := t.TempDir()
	if LegacyThemeLocationsExist(dataDir) {
		t.Error("expected false on fresh dir")
	}
	os.MkdirAll(filepath.Join(dataDir, "site", "themes", "vice"), 0755)
	if !LegacyThemeLocationsExist(dataDir) {
		t.Error("expected true once site/themes/ exists")
	}
}

func TestLegacyThemeLocationsExist_DotPolisThemes(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, ".polis", "themes", "sols"), 0755)
	if !LegacyThemeLocationsExist(dataDir) {
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

	if err := RemoveLegacyThemeLocations(dataDir); err != nil {
		t.Fatalf("RemoveLegacyThemeLocations: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "site", "themes")); !os.IsNotExist(err) {
		t.Error("site/themes/ should be removed")
	}
	if _, err := os.Stat(filepath.Join(dataDir, ".polis", "themes")); !os.IsNotExist(err) {
		t.Error(".polis/themes/ should be removed")
	}
}

func TestRemoveLegacyThemeLocations_Idempotent(t *testing.T) {
	dataDir := t.TempDir()
	if err := RemoveLegacyThemeLocations(dataDir); err != nil {
		t.Fatalf("first call (no-op): %v", err)
	}
	if err := RemoveLegacyThemeLocations(dataDir); err != nil {
		t.Fatalf("second call (no-op): %v", err)
	}
}
