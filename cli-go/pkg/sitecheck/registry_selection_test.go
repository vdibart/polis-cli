package sitecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withInstalled installs a CSS-only theme and a shape the way polis init does,
// under .polis/bundles/pub.polis.core/.
func (b *siteBuilder) withInstalled(theme, shape string) *siteBuilder {
	core := filepath.Join(".polis", "bundles", "pub.polis.core")
	b.write(filepath.Join(core, "themes", theme, theme+".css"), []byte("body{}"))
	b.write(filepath.Join(core, "themes", "_shared", "base.css"), []byte("body{}")) // not a theme
	b.write(filepath.Join(core, "shapes", shape, "index.html"), []byte("<html></html>"))
	return b
}

func (b *siteBuilder) withRegistry(theme, shape string) *siteBuilder {
	b.write(filepath.Join(".polis", "bundles", "registry.json"), mustJSON(map[string]interface{}{
		"schema_version": 1,
		"active_theme":   theme,
		"active_shape":   shape,
	}))
	return b
}

// A registry naming a theme or shape that is not installed used to pass —
// validate only asked whether the file parsed — and failed later, at render.
func TestRunLocal_RegistryNamingAnUninstalledThemeOrShapeFails(t *testing.T) {
	cases := []struct {
		name, theme, shape string
		wantInFinding      []string
	}{
		{"misspelled theme", "pub.polis.themes.vise", "pub.polis.shapes.v4", []string{`"vise"`, "installed: vice"}},
		{"misspelled shape", "pub.polis.themes.vice", "pub.polis.shapes.v5", []string{`"v5"`, "installed: v4"}},
		{"unqualified theme", "vice", "pub.polis.shapes.v4", []string{"active_theme", "fully-qualified"}},
		{"a shape named as the theme", "pub.polis.shapes.v4", "pub.polis.shapes.v4", []string{"active_theme", "fully-qualified"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newSite(t).withPrivateState(t).withInstalled("vice", "v4").withRegistry(c.theme, c.shape)
			got := checkByID(t, RunLocal(b.dir), "bundle.registry")
			if got.Outcome != OutcomeFailed {
				t.Fatalf("outcome = %s (%s), want failed", got.Outcome, got.Detail)
			}
			all := strings.Join(got.Findings, "\n")
			for _, want := range c.wantInFinding {
				if !strings.Contains(all, want) {
					t.Errorf("findings do not mention %q:\n%s", want, all)
				}
			}
			if strings.Contains(all, "_shared") {
				t.Errorf("_shared is listed as an installed theme:\n%s", all)
			}
		})
	}
}

func TestRunLocal_RegistryNamingInstalledThemeAndShapePasses(t *testing.T) {
	b := newSite(t).withPrivateState(t).withInstalled("vice", "v4").withRegistry("pub.polis.themes.vice", "pub.polis.shapes.v4")
	if got := checkByID(t, RunLocal(b.dir), "bundle.registry"); got.Outcome != OutcomePassed {
		t.Fatalf("outcome = %s (%s %v), want passed", got.Outcome, got.Detail, got.Findings)
	}

	// Unset fields name nothing, so there is nothing to be missing.
	b = newSite(t).withPrivateState(t).withRegistry("", "")
	if got := checkByID(t, RunLocal(b.dir), "bundle.registry"); got.Outcome != OutcomePassed {
		t.Fatalf("empty selections: outcome = %s (%s %v), want passed", got.Outcome, got.Detail, got.Findings)
	}
}

// A legacy, unmigrated theme can exist only in the CLI themes directory, and
// the renderer falls back to that directory, so the site renders. Validation
// must find it there too, and still fail a theme found nowhere.
func TestRunLocal_RegistryThemeOnlyInCLIThemesDir(t *testing.T) {
	cliThemes := t.TempDir()
	legacy := filepath.Join(cliThemes, "legacy")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "legacy.css"), []byte("body{}"), 0644); err != nil {
		t.Fatal(err)
	}

	b := newSite(t).withPrivateState(t).withInstalled("vice", "v4").withRegistry("pub.polis.themes.legacy", "pub.polis.shapes.v4")
	if got := checkByID(t, RunLocalWithThemes(b.dir, nil, cliThemes), "bundle.registry"); got.Outcome != OutcomePassed {
		t.Fatalf("theme only in the CLI themes directory: outcome = %s (%s %v), want passed", got.Outcome, got.Detail, got.Findings)
	}

	b = newSite(t).withPrivateState(t).withInstalled("vice", "v4").withRegistry("pub.polis.themes.nowhere", "pub.polis.shapes.v4")
	got := checkByID(t, RunLocalWithThemes(b.dir, nil, cliThemes), "bundle.registry")
	if got.Outcome != OutcomeFailed {
		t.Fatalf("theme found nowhere: outcome = %s (%s), want failed", got.Outcome, got.Detail)
	}
	if all := strings.Join(got.Findings, "\n"); !strings.Contains(all, `"nowhere"`) || !strings.Contains(all, "installed: legacy, vice") {
		t.Errorf("findings do not name the missing theme and every installed one:\n%s", all)
	}
}

func TestRunLocal_AbsentRegistryIsStillNotApplicable(t *testing.T) {
	b := newSite(t).withPrivateState(t).withInstalled("vice", "v4")
	if got := checkByID(t, RunLocal(b.dir), "bundle.registry"); got.Outcome != OutcomeNotApplicable {
		t.Fatalf("outcome = %s, want not_applicable", got.Outcome)
	}
}
