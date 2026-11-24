package tailor

import (
	"path/filepath"
	"sort"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ TAILOR NEVER LOSES A CUSTOMISATION.
//
// It may MOVE something a user made, never delete it, overwrite it without a
// copy in the run's backup, or switch it off. A theme polis never shipped is the
// user's own: Tailor keeps it where it is (or moves it to where themes live
// now), and keeps it active if it was. Until this, three checks deleted or
// disabled such a theme as "drift" — the themes guide even warned about it.

// shippedThemeName reports whether polis has ever shipped a theme by this name.
// Anything else in a themes directory was put there by the user.
func shippedThemeName(name string) bool {
	if name == "_shared" {
		return true
	}
	if bundle.IsRetiredTheme(name) {
		return true
	}
	_, ok := bundle.DefaultCoreBundle().Themes[name]
	return ok
}

// ownThemeDirs lists the subdirectories of a site-relative themes directory
// that are not a theme polis shipped, sorted.
func ownThemeDirs(siteDir, rootRel string) []string {
	entries, err := storage.ListDir(filepath.Join(siteDir, filepath.FromSlash(rootRel)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := filepath.Base(filepath.FromSlash(e.Key))
		if e.IsDir && !shippedThemeName(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ownThemeInstalled reports whether a theme directory by this name exists where
// render looks (the installed bundle's themes, or site/themes/) and polis never
// shipped it — the user's own theme, which render uses although the bundle does
// not declare it.
func ownThemeInstalled(siteDir, name string) bool {
	if name == "" || shippedThemeName(name) {
		return false
	}
	return dirExists(filepath.Join(sitecheck.ThemesDir(siteDir), name)) ||
		dirExists(filepath.Join(siteDir, "site", "themes", name))
}
