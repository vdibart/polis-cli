package theme

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestV5ThemesAreDefinedOnlyInTheirDefinition: each v5 theme lives in one
// place, its v5.json, so it can be tuned without anything diverging. Nowhere
// in the v5 shape or the v5 design files may a theme be drawn by name (a
// data-theme selector), and no file there may carry a theme's own pair of
// colours (its site colour and its background together), which is what a
// theme copied in under another name looks like. The theme gallery's
// rendered output is generated from the definitions and is not checked.
func TestV5ThemesAreDefinedOnlyInTheirDefinition(t *testing.T) {
	themesDir := filepath.Join("..", "bundle", "fixtures", "pub.polis.core", "themes")
	roots := []string{filepath.Join("..", "bundle", "fixtures", "pub.polis.core", "shapes", "v5")}
	// The design files are private and absent from a public checkout.
	design := filepath.Join("..", "..", "..", "docs", ".internal", "design", "v5")
	if _, err := os.Stat(design); err == nil {
		roots = append(roots, design)
	}
	generated := filepath.Join(design, "gallery", "themes")
	// The dark sketch is an early mockup drawn in the v4 sols palette, which it
	// names as its source. It is a record of that sketch, not a v5 theme.
	sketch := filepath.Join(design, "mockups", "99-dark-sketch.html")

	type pair struct{ name, site, bg string }
	var pairs []pair
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(themesDir, e.Name(), V5TokensFile))
		if err != nil {
			continue
		}
		def, err := ParseV5Theme(b)
		if err != nil {
			continue // a refused definition is the theme tests' business
		}
		site, bg := def.Tokens["site"], def.Tokens["bg"]
		if site != "" && bg != "" {
			pairs = append(pairs, pair{e.Name(), strings.ToLower(site), strings.ToLower(bg)})
		}
	}
	if len(pairs) == 0 {
		t.Fatal("found no theme to look for: the themes directory moved?")
	}

	byName := regexp.MustCompile(`data-theme\s*[=~|^$*]?=?\s*["']?[a-z]`)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p == generated {
					return filepath.SkipDir
				}
				return nil
			}
			if p == sketch {
				return nil
			}
			switch filepath.Ext(p) {
			case ".html", ".css", ".js", ".json", ".md":
			default:
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			text := strings.ToLower(string(b))
			if byName.MatchString(text) {
				t.Errorf("%s draws a theme by name (data-theme): a theme is defined only in its v5.json", p)
			}
			for _, pr := range pairs {
				if strings.Contains(text, pr.site) && strings.Contains(text, pr.bg) {
					t.Errorf("%s carries %s's colours (site %s, bg %s): a theme is defined only in themes/%s/v5.json", p, pr.name, pr.site, pr.bg, pr.name)
				}
			}
			return nil
		})
	}
}
