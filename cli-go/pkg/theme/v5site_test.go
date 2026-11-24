package theme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// v5SiteWithTheme is a site with the reference payload and one theme whose
// definition sets the site colour and the background.
func v5SiteWithTheme(t *testing.T) (string, storage.Site) {
	t.Helper()
	dir := t.TempDir()
	site := storage.At(dir)
	if err := bundle.EnsureReferencePayload(site, "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	themeDir := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "themes", "base")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, V5TokensFile), []byte(`{"tokens":{"site":"#8a4a2e","bg":"#e0deda"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, site
}

func styles(t *testing.T, dir string, site storage.Site) string {
	t.Helper()
	if err := CopyV5CSS(site, "base"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "styles.css"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSiteTokensLayerOverTheTheme: the owner's values win over the theme's,
// the theme's other values stay, and resetting puts the theme's back.
func TestSiteTokensLayerOverTheTheme(t *testing.T) {
	dir, site := v5SiteWithTheme(t)
	if _, err := SetSiteTokens(site, "base", map[string]string{"site": "#2a6f5a", "font-heading": "Lora"}, false); err != nil {
		t.Fatal(err)
	}
	css := styles(t, dir, site)
	for _, want := range []string{"--site: #2a6f5a", "--bg: #e0deda", "Lora"} {
		if !strings.Contains(css, want) {
			t.Errorf("styles.css lacks %q", want)
		}
	}
	if strings.Contains(css, "--site: #8a4a2e") {
		t.Error("the theme's site colour was drawn over the owner's")
	}
	prev, err := ResetSiteTokens(site, "base", []string{"site"})
	if err != nil {
		t.Fatal(err)
	}
	if prev["site"] != "#2a6f5a" {
		t.Errorf("previous = %v, want the values from before", prev)
	}
	if css := styles(t, dir, site); !strings.Contains(css, "--site: #8a4a2e") || !strings.Contains(css, "Lora") {
		t.Error("resetting site did not put the theme's back, or reset more than it named")
	}
	if _, err := ResetSiteTokens(site, "base", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, SiteTokensKey)); !os.IsNotExist(err) {
		t.Error("with no value left, the site's file stays")
	}
}

// TestSiteTokensRefuseWholeAndWriteNothing: a bad value, an unknown font (set
// by someone who is there to be told, so refused, not fallen back) and a
// failing contrast each refuse the whole change, naming why.
func TestSiteTokensRefuseWholeAndWriteNothing(t *testing.T) {
	dir, site := v5SiteWithTheme(t)
	for _, bad := range []map[string]string{
		{"site": "#2a6f5a", "rollover": "spin"},
		{"font-ui": "Comic Sans MS"},
		{"text-soft": "#cccccc"},
		{"site": "red; background: url(https://x.example/a)"},
	} {
		_, err := SetSiteTokens(site, "base", bad, false)
		var refused *V5TokenError
		if !errors.As(err, &refused) || len(refused.Reasons) == 0 {
			t.Errorf("%v: err = %v, want refused with the reasons", bad, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, SiteTokensKey)); !os.IsNotExist(err) {
		t.Error("a refused change wrote the site's file")
	}
}

// TestSiteTokensReplacePutsTheOldValuesBack: Randomise, then Revert.
func TestSiteTokensReplacePutsTheOldValuesBack(t *testing.T) {
	_, site := v5SiteWithTheme(t)
	if _, err := SetSiteTokens(site, "base", map[string]string{"site": "#2a6f5a"}, false); err != nil {
		t.Fatal(err)
	}
	prev, err := SetSiteTokens(site, "base", map[string]string{"bg": "#f3efe6", "site": "#6a3d8f"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetSiteTokens(site, "base", prev, true); err != nil {
		t.Fatal(err)
	}
	own, _ := ReadSiteTokens(site)
	if len(own.Tokens) != 1 || own.Tokens["site"] != "#2a6f5a" {
		t.Errorf("after revert = %v, want exactly the values from before", own.Tokens)
	}
}

// TestSiteTokensWithheldAtRenderWhenTheyFail: a file written by hand that
// fails the contract is left out of styles.css (and said so, safely), never
// deleted, and the theme is drawn alone.
func TestSiteTokensWithheldAtRenderWhenTheyFail(t *testing.T) {
	dir, site := v5SiteWithTheme(t)
	if err := os.MkdirAll(filepath.Join(dir, "site"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SiteTokensKey), []byte(`{"tokens":{"site":"*/ body{display:none} /*"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	css := styles(t, dir, site)
	if !strings.Contains(css, SiteTokensKey+" is withheld") || !strings.Contains(css, "--site: #8a4a2e") {
		t.Error("a failing file was not withheld with the theme drawn alone")
	}
	if strings.Contains(css, "body{display:none}") && !strings.Contains(css, "* / body{display:none}") {
		t.Error("the withheld value closed the comment it was quoted in")
	}
	if _, err := os.Stat(filepath.Join(dir, SiteTokensKey)); err != nil {
		t.Error("the failing file was deleted")
	}
}
