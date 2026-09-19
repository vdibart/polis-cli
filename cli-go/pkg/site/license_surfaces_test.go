package site

import (
	"os"
	"path/filepath"
	"testing"
)

// RemoveLicenseSurfaces used to delete EVERY top-level .html in the licence
// mount, while its comment promised "only what the renderer writes". An
// author's own page in that folder was deleted on withdrawal.
func TestRemoveLicenseSurfacesKeepsTheAuthorsOwnPages(t *testing.T) {
	dir, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://maya.example"})

	mount := filepath.Join(dir, filepath.FromSlash(LicenseMountDir(dir)))
	if err := os.MkdirAll(mount, 0755); err != nil {
		t.Fatal(err)
	}
	// What the renderer writes: the terms page and one page per profile
	// version the site has stated.
	rendered := []string{"index.html", "reserved-1.html", "open-1.html"}
	for _, name := range rendered {
		if err := os.WriteFile(filepath.Join(mount, name), []byte("<html>generated</html>"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// What the author put there.
	authored := []string{"notes.html", "reserved-2-draft.html", "unknown-9.html"}
	for _, name := range authored {
		if err := os.WriteFile(filepath.Join(mount, name), []byte("<html>mine</html>"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := RemoveLicenseSurfaces(dir); err != nil {
		t.Fatal(err)
	}

	for _, name := range rendered {
		if _, err := os.Stat(filepath.Join(mount, name)); !os.IsNotExist(err) {
			t.Errorf("generated page %s survived removal", name)
		}
	}
	for _, name := range authored {
		if _, err := os.Stat(filepath.Join(mount, name)); err != nil {
			t.Errorf("the author's own %s was deleted: %v", name, err)
		}
	}
}

// With only generated pages present, the mount itself goes too.
func TestRemoveLicenseSurfacesRemovesAnEmptiedMount(t *testing.T) {
	dir, _ := initSite(t, InitOptions{License: "reserved", BaseURL: "https://maya.example"})
	mount := filepath.Join(dir, filepath.FromSlash(LicenseMountDir(dir)))
	os.MkdirAll(mount, 0755)
	os.WriteFile(filepath.Join(mount, "index.html"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(mount, "reserved-1.html"), []byte("x"), 0644)

	if err := RemoveLicenseSurfaces(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mount); !os.IsNotExist(err) {
		t.Error("an emptied licence mount was left standing")
	}
}
