package render

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The blog shape (v3) is supported for good on a site its owner runs: Tailor
// never switches a self-hosted site to the stream. So every shipped theme that
// declares the blog shape must render a site through the shipped v3 templates —
// a per-post page, the archive and the home page, carrying the post and the
// theme's stylesheet. This is "renders correctly", not "has every v4 feature".
func TestEveryBlogCompatibleThemeRendersTheBlogShape(t *testing.T) {
	var themes []string
	for name, th := range bundle.DefaultCoreBundle().Themes {
		if strings.HasPrefix(name, "_") {
			continue
		}
		for _, s := range th.CompatibleShapes {
			if s == "pub.polis.shapes.v3" {
				themes = append(themes, name)
			}
		}
	}
	sort.Strings(themes)
	if len(themes) < 5 {
		t.Fatalf("only %d themes declare the blog shape: %v", len(themes), themes)
	}

	for _, name := range themes {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			setupStreamTestSite(t, dir)
			if err := bundle.SetActiveShapeName(storage.At(dir), "v3"); err != nil {
				t.Fatal(err)
			}
			if err := bundle.SetActiveThemeName(storage.At(dir), name); err != nil {
				t.Fatal(err)
			}
			writeStreamPost(t, dir, "20260507", "hello", "Hello Blog", "A paragraph of the post.")

			r, err := NewPageRenderer(PageConfig{
				Site:           storage.At(dir),
				BaseURL:        "https://example.com",
				PostsSourceDir: "content/pub.polis.core/post",
				PostsMountDir:  "posts",
			})
			if err != nil {
				t.Fatalf("NewPageRenderer: %v", err)
			}
			if _, err := r.RenderAll(true); err != nil {
				t.Fatalf("RenderAll: %v", err)
			}

			page := mustReadFile(t, filepath.Join(dir, "posts", "20260507", "hello.html"))
			for _, want := range []string{"Hello Blog", "A paragraph of the post."} {
				if !strings.Contains(page, want) {
					t.Errorf("post page lacks %q", want)
				}
			}
			if strings.Contains(page, "{{") {
				t.Error("post page has an unrendered template tag")
			}
			if home := mustReadFile(t, filepath.Join(dir, "index.html")); !strings.Contains(home, "Hello Blog") {
				t.Error("home page does not list the post")
			}
			if _, err := os.Stat(filepath.Join(dir, "posts", "index.html")); err != nil {
				t.Errorf("no blog archive page: %v", err)
			}
			css, err := os.ReadFile(filepath.Join(dir, "styles.css"))
			if err != nil || !strings.Contains(string(css), "--color-") {
				t.Errorf("styles.css missing or without the theme's palette: %v", err)
			}
		})
	}
}
