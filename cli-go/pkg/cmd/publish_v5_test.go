package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// v5PublishSite is a site on the given shape with one post in its public
// index, as a publish leaves it.
func v5PublishSite(t *testing.T, shape string) string {
	t.Helper()
	dir := t.TempDir()
	s := storage.At(dir)
	if err := bundle.EnsureReferencePayload(s, "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SetActiveShapeName(s, shape); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SetActiveThemeName(s, "especial-light"); err != nil {
		t.Fatal(err)
	}
	src := "content/pub.polis.core/post/20260401/fresh.md"
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(src)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, src), []byte("---\ntitle: Fresh\npublished: 2026-04-01T12:00:00Z\n---\nJust published.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := metadata.AppendToPublicIndex(s, &metadata.IndexEntry{Type: "post", Path: src, Title: "Fresh", Published: "2026-04-01T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLIS_BASE_URL", "https://example.com")
	return dir
}

// TestRenderV5AfterChange: on a v5 site a change renders the site (in full
// the first time, with no render record yet), so the
// post has its page and is in the posts view; on any other shape it does
// nothing (v4 has its cascade, v3 never rendered on a CLI publish).
func TestRenderV5AfterChange(t *testing.T) {
	dir := v5PublishSite(t, "v5")
	renderV5AfterChange(dir, "content/pub.polis.core/post/20260401/fresh.md")
	index, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil || !strings.Contains(string(index), `data-path="/posts/20260401/fresh.html"`) {
		t.Fatalf("the posts view should carry the new post (err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "posts", "20260401", "fresh.html")); err != nil {
		t.Errorf("the post should have its own page: %v", err)
	}

	for _, shape := range []string{"v3", "v4"} {
		dir := v5PublishSite(t, shape)
		renderV5AfterChange(dir, "content/pub.polis.core/post/20260401/fresh.md")
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			t.Errorf("on %s, the v5 render must not run", shape)
		}
	}
}
