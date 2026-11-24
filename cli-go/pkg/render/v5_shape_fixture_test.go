package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestWriteV5ShapeFixtures renders the v5 fixture site once per set of token
// values, for the shape tokens' screenshot check (a development script, not
// shipped), which measures the style guide's alignment rules from pixels. It
// is a generator, not a check: skipped unless POLIS_V5_SHAPE_FIXTURES names a
// directory holding combos.json ({"<name>": {"<token>": "<value>"}}); each
// set is written as the site's own values (site/v5.json) and rendered into
// <dir>/<name> ("_theme" in a set names the theme; daryl otherwise). The
// fixture is renderV5Site's, with an untitled post added so every kind of
// second line is on the page.
func TestWriteV5ShapeFixtures(t *testing.T) {
	out := os.Getenv("POLIS_V5_SHAPE_FIXTURES")
	if out == "" {
		t.Skip("POLIS_V5_SHAPE_FIXTURES is not set")
	}
	b, err := os.ReadFile(filepath.Join(out, "combos.json"))
	if err != nil {
		t.Fatal(err)
	}
	var combos map[string]map[string]string
	if err := json.Unmarshal(b, &combos); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(combos))
	for n := range combos {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		dir := filepath.Join(out, name)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		tokens := combos[name]
		themeName := "daryl" // it sets nothing: the polis default
		if th, ok := tokens["_theme"]; ok {
			themeName = th
			delete(tokens, "_theme")
		}
		renderV5SiteAt(t, dir, themeName, func(dir string) {
			writeStreamPost(t, dir, "20260322", "untitled", "", "An untitled post's opening words, which lead its card in place of a title.")
			if len(tokens) == 0 {
				return
			}
			v, err := json.Marshal(map[string]any{"tokens": tokens})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "site", "v5.json"), v, 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
