package template

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// partialTree is one site's snippets: a global one, a local-theme one, and the
// shape's snippet and root partials, plus a traversal target outside them.
var partialTree = map[string]string{
	"site/snippets/about.html":                                   "global-about",
	"site/themes/turbo/snippets/card.html":                       "theme-card",
	".polis/bundles/pub.polis.core/shapes/v4/snippets/foot.html": "base-foot",
	".polis/bundles/pub.polis.core/shapes/v4/stream-post.html":   "shape-root-post",
	".polis/keys/id_ed25519":                                     "SECRET",
}

const partialPage = `{{> about}}|{{> theme:card}}|{{> foot}}|{{> stream-post}}`

// ⭐ PARTIALS RESOLVE THE SAME ON A STORE WITH NO FILESYSTEM.
//
// The same tree is laid out on a POSIX site and an object-backed one whose
// directory does not exist, and one page pulls a partial from each source in
// the chain. Anything still reading a path would leave the object-backed
// page's includes unexpanded.
func TestPartialsResolveTheSameOnAStoreWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	obj := storage.NewObjectStore("tenants/alice")
	for key, body := range partialTree {
		p := filepath.Join(dir, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := obj.Put(ctx, key, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	const want = "global-about|theme-card|base-foot|shape-root-post"
	for name, site := range map[string]storage.Site{
		"posix":  storage.At(dir),
		"object": storage.On(filepath.Join(t.TempDir(), "no-such-dir"), obj),
	} {
		e := New(Config{Site: site, ActiveTheme: "turbo", ActiveShape: "v4"})
		got, err := e.Render(partialPage, NewRenderContext())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s: rendered %q, want %q", name, got, want)
		}

		// A partial that climbs out of its lookup prefix resolves nowhere.
		if _, _, _, err := e.loadPartial("../../.polis/keys/id_ed25519"); err == nil {
			t.Errorf("%s: a traversal resolved", name)
		}
	}
}

// ⚠️ ON A FILESYSTEM AN EMPTY DIRECTORY STILL STOPS THE THEME LOOKUP falling
// through to the CLI themes, as it always did: presence is the directory's,
// not its contents'. A partial missing from it is then looked for in the base
// sources, never in the CLI copy.
func TestAnEmptyLocalThemeDirectoryStillShadowsTheCLITheme(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "site", "themes", "turbo", "snippets"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cli, "turbo", "snippets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cli, "turbo", "snippets", "card.html"), []byte("cli-card"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(Config{Site: storage.At(dir), CLIThemesDir: cli, ActiveTheme: "turbo"})
	if _, _, _, err := e.loadPartial("theme:card"); err == nil {
		t.Error("the CLI theme was read through an empty local theme directory")
	}

	if err := os.RemoveAll(filepath.Join(dir, "site", "themes")); err != nil {
		t.Fatal(err)
	}
	got, _, source, err := e.loadPartial("theme:card")
	if err != nil || got != "cli-card" || source != "theme" {
		t.Errorf("with no local theme: %q from %q, %v; want the CLI copy", got, source, err)
	}
}
