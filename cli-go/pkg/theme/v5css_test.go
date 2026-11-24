package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// TestV5StylesKeepEveryComment: a v5 site's styles.css is the shape's CSS,
// then the theme's values, then the site's own CSS, joined as written. A
// theme's note reaches readers as a comment (the Elwood themes carry one),
// and nothing strips a comment on the way. If a minifier
// is ever added to this path, this fails: decide about the notes first.
func TestV5StylesKeepEveryComment(t *testing.T) {
	dir := t.TempDir()
	site := storage.At(dir)
	if err := bundle.EnsureReferencePayload(site, "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	themeDir := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "themes", "notetheme")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	note := "A note left in a theme, for whoever reads its CSS."
	if err := os.WriteFile(filepath.Join(themeDir, V5TokensFile), []byte(`{"note":"`+note+`","tokens":{"site":"#8a4a2e"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "site"), 0o755); err != nil {
		t.Fatal(err)
	}
	own := "/* the owner's own comment */\n.item h3 { letter-spacing: .01em; }\n"
	if err := os.WriteFile(filepath.Join(dir, "site", "custom.css"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyV5CSS(site, "notetheme"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "styles.css"))
	if err != nil {
		t.Fatal(err)
	}
	styles := string(b)
	shape, _ := os.ReadFile(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "shapes", "v5", "v5.css"))
	if !strings.HasPrefix(styles, string(shape)) {
		t.Error("styles.css does not begin with the shape's stylesheet, as written")
	}
	for _, want := range []string{note, "--site: #8a4a2e", "/* the owner's own comment */"} {
		if !strings.Contains(styles, want) {
			t.Errorf("styles.css lost %q", want)
		}
	}
	if strings.Index(styles, note) > strings.Index(styles, "/* the owner's own comment */") {
		t.Error("the theme's tokens must come before the site's own CSS, so the owner's choices win")
	}
}

// TestV5StylesWithoutTokensIsThePolisDefault: a theme with no v5 definition
// draws in the polis default, the shape's own token values. Every theme in
// the bundle has one now, so the test names a theme that does not exist.
func TestV5StylesWithoutTokensIsThePolisDefault(t *testing.T) {
	dir := t.TempDir()
	site := storage.At(dir)
	if err := bundle.EnsureReferencePayload(site, "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	if err := CopyV5CSS(site, "no-v5-definition"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "styles.css"))
	shape, _ := os.ReadFile(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "shapes", "v5", "v5.css"))
	if string(b) != string(shape) {
		t.Error("with no v5 tokens and no custom CSS, styles.css should be the shape's stylesheet alone")
	}
}

// TestV5StylesWithholdARefusedTheme: a definition the contract refuses (here,
// one that fails the contrast check) never reaches the stylesheet, which says
// why it is missing.
func TestV5StylesWithholdARefusedTheme(t *testing.T) {
	dir := t.TempDir()
	site := storage.At(dir)
	if err := bundle.EnsureReferencePayload(site, "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	themeDir := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "themes", "faint")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, V5TokensFile), []byte(`{"tokens":{"text-soft":"#dddddd"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyV5CSS(site, "faint"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "styles.css"))
	if strings.Contains(string(b), "#dddddd") || !strings.Contains(string(b), "The theme faint is withheld") {
		t.Error("a refused theme must be withheld, and the stylesheet must say so")
	}
}
