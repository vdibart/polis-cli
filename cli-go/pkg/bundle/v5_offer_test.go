package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNoThemeIsOfferedForV5WithoutItsTokens: a theme is offered for the v5
// shape only once it supplies the v5 token contract (its own v5.json, checked by the token contract). One
// offered without it would draw in the polis default under the theme's name.
func TestNoThemeIsOfferedForV5WithoutItsTokens(t *testing.T) {
	for name, th := range DefaultCoreBundle().Themes {
		for _, s := range th.CompatibleShapes {
			if s != "pub.polis.shapes.v5" {
				continue
			}
			if _, err := os.Stat(filepath.Join("fixtures", "pub.polis.core", "themes", name, "v5.json")); err != nil {
				t.Errorf("theme %q is offered for v5 but has no v5.json: %v", name, err)
			}
		}
	}
}

// TestTheConvertedThemesAreOfferedForV5: every theme whose v5 definition is
// in the bundle is declared in it and offered for v5 (the Elwood pair is
// declared too, and reserved from the picker in the theme package). A theme
// dir the manifest does not declare would be quarantined by Medic as an
// orphan on every tenant.
func TestTheConvertedThemesAreOfferedForV5(t *testing.T) {
	notOffered := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join("fixtures", "pub.polis.core", "themes"))
	if err != nil {
		t.Fatal(err)
	}
	themes := DefaultCoreBundle().Themes
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join("fixtures", "pub.polis.core", "themes", e.Name(), "v5.json")); err != nil || notOffered[e.Name()] {
			continue
		}
		th, ok := themes[e.Name()]
		if !ok {
			t.Errorf("%s has a v5 definition but no entry in the bundle", e.Name())
			continue
		}
		if !containsString(th.CompatibleShapes, "pub.polis.shapes.v5") {
			t.Errorf("%s has a v5 definition but is not offered for v5", e.Name())
		}
	}
}
