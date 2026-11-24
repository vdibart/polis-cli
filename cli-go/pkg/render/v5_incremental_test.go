package render

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// incrementalSite is a v5 site of n posts, one a day from 2026-03-01, fully
// rendered once, so it has a render record.
func incrementalSite(t *testing.T, n int) (string, *PageRenderer) {
	t.Helper()
	dir := t.TempDir()
	setupStreamTestSite(t, dir)
	if err := bundle.SetActiveShapeName(storage.At(dir), "v5"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		d := time.Date(2026, 3, i, 0, 0, 0, 0, time.UTC).Format("20060102")
		writeStreamPost(t, dir, d, "p"+d, "Post "+d, "Body of "+d+".")
	}
	r := newIncrementalRenderer(t, dir)
	if _, err := r.RenderAll(true); err != nil {
		t.Fatal(err)
	}
	return dir, r
}

func newIncrementalRenderer(t *testing.T, dir string) *PageRenderer {
	t.Helper()
	r, err := NewPageRenderer(PageConfig{
		Site: storage.At(dir), BaseURL: "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post", PostsMountDir: "posts",
		CommentsSourceDir: "content/pub.polis.core/comment", CommentsMountDir: "comments", Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// published is every file a reader can be served, by its key.
func published(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			if rel == ".polis" {
				return filepath.SkipDir
			}
			return nil
		}
		b, _ := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	return out
}

// countsOnly is a page with its toolbar's numbers blanked: the one part a
// page the change did not rewrite may keep from before.
var toolbarNumber = regexp.MustCompile(`(<b data-count="[a-z-]+">)\d+(</b>(?: <span data-noun="[a-z]+">)?)[a-z]*`)

func countsOnly(s string) string { return toolbarNumber.ReplaceAllString(s, "${1}N${2}") }

// ⭐ An incremental render writes what a full render would, file for file,
// except the toolbar's counts on pages it did not rewrite; and it rewrites a
// bounded number of pages, however many posts the site has.
func TestV5IncrementalMatchesAFullRender(t *testing.T) {
	steps := []struct {
		name   string
		change func(t *testing.T, dir string) string
	}{
		{"publish", func(t *testing.T, dir string) string {
			writeStreamPost(t, dir, "20260331", "new", "A new one", "Fresh words.")
			return "content/pub.polis.core/post/20260331/new.md"
		}},
		{"republish", func(t *testing.T, dir string) string {
			src := "content/pub.polis.core/post/20260310/p20260310.md"
			if err := os.WriteFile(filepath.Join(dir, src), []byte("---\ntitle: Post 20260310\npublished: 2026-03-10T12:00:00Z\n---\nRewritten body.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return src
		}},
		{"unpublish", func(t *testing.T, dir string) string {
			src := "content/pub.polis.core/post/20260312/p20260312.md"
			if err := metadata.RemoveIndexEntry(storage.At(dir), src); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(filepath.Join(dir, src))
			_ = os.Remove(filepath.Join(dir, "posts/20260312/p20260312.html"))
			return src
		}},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			dir, _ := incrementalSite(t, 20)
			src := s.change(t, dir)
			stats, incremental, err := newIncrementalRenderer(t, dir).RenderV5Change(src)
			if err != nil || !incremental {
				t.Fatalf("incremental = %v, err %v", incremental, err)
			}
			if stats.PostsRendered > 1+2*v5NeighbourBudget {
				t.Errorf("rewrote %d post pages for one change", stats.PostsRendered)
			}
			after := published(t, dir)
			if _, err := newIncrementalRenderer(t, dir).RenderAll(true); err != nil {
				t.Fatal(err)
			}
			full := published(t, dir)
			for key, want := range full {
				got, ok := after[key]
				if !ok {
					t.Errorf("%s: a full render writes it; the incremental one did not", key)
					continue
				}
				if got != want && countsOnly(got) != countsOnly(want) {
					t.Errorf("%s differs from a full render beyond its toolbar counts", key)
				}
				if key == "index.html" && got != want {
					t.Error("index.html differs from a full render (its counts are always current)")
				}
			}
			for key := range after {
				if _, ok := full[key]; !ok && !strings.HasPrefix(key, "content/") {
					t.Errorf("%s: the incremental render left a file a full render does not have", key)
				}
			}
		})
	}
}

// Publishing on a 31-post site rewrites the same small set as on a 20-post
// one: the new post's page and the three below it whose streams show it (a
// page shows three newer and three older; the pages further down keep the
// same neighbours, only renumbered).
func TestV5IncrementalWorkDoesNotGrowWithTheSite(t *testing.T) {
	for _, n := range []int{20, 31} {
		dir, _ := incrementalSite(t, n)
		writeStreamPost(t, dir, "20260401", "new", "New", "New words.")
		stats, incremental, err := newIncrementalRenderer(t, dir).RenderV5Change("content/pub.polis.core/post/20260401/new.md")
		if err != nil || !incremental {
			t.Fatalf("n=%d: incremental %v err %v", n, incremental, err)
		}
		if want := 1 + v5NeighbourBudget/2; stats.PostsRendered != want {
			t.Errorf("n=%d: rewrote %d post pages, want %d (the new post and the three below it)", n, stats.PostsRendered, want)
		}
	}
}

// ⛔ What the record cannot bound renders the whole site.
func TestV5IncrementalFallsBackToAFullRender(t *testing.T) {
	src := "content/pub.polis.core/post/20260305/p20260305.md"
	for name, spoil := range map[string]func(t *testing.T, dir string){
		"no record":      func(t *testing.T, dir string) { _ = os.Remove(filepath.Join(dir, v5ManifestKey)) },
		"another theme":  func(t *testing.T, dir string) { _ = bundle.SetActiveThemeName(storage.At(dir), "vice") },
		"a missing page": func(t *testing.T, dir string) { _ = os.Remove(filepath.Join(dir, "posts/20260319/p20260319.html")) },
		"no home page":   func(t *testing.T, dir string) { _ = os.Remove(filepath.Join(dir, "index.html")) },
	} {
		t.Run(name, func(t *testing.T) {
			dir, _ := incrementalSite(t, 20)
			spoil(t, dir)
			stats, incremental, err := newIncrementalRenderer(t, dir).RenderV5Change(src)
			if err != nil || incremental {
				t.Fatalf("incremental = %v, err %v; want a full render", incremental, err)
			}
			if stats.PostsRendered != 20 {
				t.Errorf("rendered %d post pages, want all 20", stats.PostsRendered)
			}
		})
	}
}

// The comments toolbar is a count of comments; "on M sites" is gone.
func TestV5CommentsToolbarIsACount(t *testing.T) {
	got := v5Toolbars(v5Counts{posts: 2, withComments: 1, comments: 1})
	if strings.Contains(got, "sites</span></span>") || strings.Contains(got, " on <b") {
		t.Errorf("the comments toolbar still counts sites: %s", got)
	}
	for _, want := range []string{`<b data-count="comments">1</b> <span data-noun="comment">comment</span>`, `<b data-count="posts">2</b>`, `<b data-count="with-comments">1</b>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}
