package render

// ⭐ THE RENDER BASELINE: what a v3 or v4 site renders to, byte for byte.
//
// A new shape ships to every site, active or not: its files land in each
// tenant's installed bundle, and the deploy re-renders every hosted site once.
// That is harmless only if no site that has not been switched renders a single
// different byte. This test is the proof. It renders fixed sites in the shipped
// shapes and compares every output file with the hashes in
// testdata/render_baseline/, which were recorded before the v5 shape existed.
//
// ⚠️ A deliberate change to v3 or v4 output (a shape, a theme's CSS, the widget
// version) changes these hashes too. Re-record with
//
//	go test ./pkg/render/ -run TestRenderBaseline -update-render-baseline
//
// and say in the commit which change moved them. Never re-record to make a
// failure go away without knowing why it failed.

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

var updateRenderBaseline = flag.Bool("update-render-baseline", false, "re-record testdata/render_baseline")

// renderBaselineCases: each shipped shape with a theme it serves today.
var renderBaselineCases = []struct{ name, shape, theme string }{
	{"v4-studio13", "v4", "studio13"},
	{"v4-especial-light", "v4", "especial-light"},
	{"v4-vice", "v4", "vice"},
	{"v3-especial-light", "v3", "especial-light"},
	{"v3-zane", "v3", "zane"},
}

func TestRenderBaseline(t *testing.T) {
	for _, c := range renderBaselineCases {
		t.Run(c.name, func(t *testing.T) {
			got := renderBaselineSite(t, c.shape, c.theme)
			golden := filepath.Join("testdata", "render_baseline", c.name+".golden")
			if *updateRenderBaseline {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("no baseline for %s (record it with -update-render-baseline): %v", c.name, err)
			}
			if got != string(want) {
				t.Errorf("%s renders differently from its baseline:\n%s", c.name, diffBaseline(string(want), got))
			}
		})
	}
}

// TestRenderBaselineIsDeterministic: two renders of the same site agree, so a
// baseline failure always means the output changed, never that the clock moved.
func TestRenderBaselineIsDeterministic(t *testing.T) {
	for _, c := range renderBaselineCases {
		if a, b := renderBaselineSite(t, c.shape, c.theme), renderBaselineSite(t, c.shape, c.theme); a != b {
			t.Errorf("%s renders differently twice:\n%s", c.name, diffBaseline(a, b))
		}
	}
}

// renderBaselineSite builds a small fixed site, renders it, and lists every
// output file with its hash: everything the site serves, nothing it keeps
// privately (.polis/) and none of its sources (content/).
func renderBaselineSite(t *testing.T, shape, themeName string) string {
	t.Helper()
	dir := t.TempDir()
	setupStreamTestSite(t, dir)
	if err := bundle.SetActiveShapeName(storage.At(dir), shape); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SetActiveThemeName(storage.At(dir), themeName); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "site", "snippets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site", "snippets", "about.md"), []byte("I write about *signed* things.\n\n- one\n- two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeStreamPost(t, dir, "20260315", "alpha", "Alpha", "First body, with a [link](https://example.org) and `code`.")
	writeStreamPost(t, dir, "20260320", "bravo", "Bravo", "## A heading\n\nSecond body.\n\n> A quotation.")
	writeStreamPost(t, dir, "20260325", "charlie", "Charlie", "Third body text.")

	// The sitemap dates each page from its source file's modification time:
	// pinned, so the clock is not part of the output.
	pinned := time.Date(2026, 3, 26, 9, 0, 0, 0, time.UTC)
	_ = filepath.WalkDir(filepath.Join(dir, "content"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			_ = os.Chtimes(p, pinned, pinned)
		}
		return nil
	})

	r, err := NewPageRenderer(PageConfig{
		Site:              storage.At(dir),
		BaseURL:           "https://example.com",
		PostsSourceDir:    "content/pub.polis.core/post",
		PostsMountDir:     "posts",
		CommentsSourceDir: "content/pub.polis.core/comment",
		CommentsMountDir:  "comments",
		Offline:           true,
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	// POLIS_BASELINE_DUMP=<dir> keeps each case's output, to read a change
	// the hashes only report.
	if out := os.Getenv("POLIS_BASELINE_DUMP"); out != "" {
		_ = os.CopyFS(filepath.Join(out, shape+"-"+themeName), os.DirFS(dir))
	}

	var lines []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			if rel == ".polis" || rel == "content" || rel == "site" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == filepath.Join(".well-known", "polis") {
			return nil // the fixture's own input
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		lines = append(lines, hex.EncodeToString(sum[:])+"  "+filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

func diffBaseline(want, got string) string {
	w, g := map[string]bool{}, map[string]bool{}
	for _, l := range strings.Split(want, "\n") {
		w[l] = true
	}
	for _, l := range strings.Split(got, "\n") {
		g[l] = true
	}
	var out []string
	for l := range w {
		if !g[l] && l != "" {
			out = append(out, "- "+l)
		}
	}
	for l := range g {
		if !w[l] && l != "" {
			out = append(out, "+ "+l)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}
