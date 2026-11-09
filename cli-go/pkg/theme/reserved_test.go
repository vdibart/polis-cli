package theme

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"testing"
)

// A random pick — at polis init, and wherever Medic, Tailor or render repair a
// missing theme — never lands on a reserved system theme. sols is the logged-out
// landing theme and stardust is why.polis.pub's; the webapp refuses both in its
// picker, and a random pick used to hand them out anyway.

func installThemes(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		p := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "themes", n)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, n+".css"), []byte("body{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSelectRandomThemeNeverPicksAReservedTheme(t *testing.T) {
	dir := installThemes(t, "sols", "stardust", "vice", "turbo")
	if themes, _ := ListThemes(storage.At(dir), ""); len(themes) != 4 {
		t.Fatalf("fixture: ListThemes = %v, want all four themes valid", themes)
	}
	for i := 0; i < 200; i++ {
		got, err := SelectRandomTheme(storage.At(dir), "")
		if err != nil {
			t.Fatal(err)
		}
		if IsReserved(got) {
			t.Fatalf("pick %d chose the reserved theme %q", i, got)
		}
	}
}

// Reserved themes alone are still better than no theme at all.
func TestSelectRandomThemeFallsBackWhenOnlyReservedThemesExist(t *testing.T) {
	dir := installThemes(t, "sols")
	got, err := SelectRandomTheme(storage.At(dir), "")
	if err != nil || got != "sols" {
		t.Fatalf("got %q, %v; want sols as the only theme there is", got, err)
	}
}

func TestIsReservedAcceptsShortAndQualifiedNames(t *testing.T) {
	for name, want := range map[string]bool{
		"sols": true, "stardust": true, "pub.polis.themes.sols": true, "pub.polis.themes.stardust": true,
		"vice": false, "pub.polis.themes.vice": false, "": false,
	} {
		if got := IsReserved(name); got != want {
			t.Errorf("IsReserved(%q) = %v, want %v", name, got, want)
		}
	}
}
