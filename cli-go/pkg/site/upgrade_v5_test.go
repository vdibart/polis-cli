package site

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// TestCleanV3LegacyArchivesOnAV5Site: v5 writes none of v3's archive pages,
// so on a v5 site they are stale and go, as they do on v4.
func TestCleanV3LegacyArchivesOnAV5Site(t *testing.T) {
	dir := t.TempDir()
	s := storage.At(dir)
	if err := bundle.EnsureReferencePayload(s, "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SetActiveShapeName(s, "v5"); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "posts", "index.html")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("<html>old v3 archive</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !V3LegacyArchivesExist(s) {
		t.Fatal("a v3 archive page on a v5 site should be reported as stale")
	}
	if removed := CleanV3LegacyArchives(s); len(removed) != 1 {
		t.Fatalf("removed %v, want posts/index.html", removed)
	}
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Error("posts/index.html should be gone")
	}
}

// TestCleanStrayRenderedPagesRemovesOnlyRenderedCopies: a page beside its
// .md source is removed; a page with no source beside it, and every source,
// stay.
func TestCleanStrayRenderedPagesRemovesOnlyRenderedCopies(t *testing.T) {
	dir := t.TempDir()
	s := storage.At(dir)
	files := map[string]string{
		"content/pub.polis.core/post/20260401/hello.md":      "---\ntitle: Hello\n---\nHi.\n",
		"content/pub.polis.core/post/20260401/hello.html":    "<html>stray</html>",
		"content/pub.polis.core/comment/20260402/reply.md":   "---\n---\nA reply.\n",
		"content/pub.polis.core/comment/20260402/reply.html": "<html>stray</html>",
		"content/pub.polis.core/post/20260401/notes.html":    "<html>no source beside it</html>",
	}
	for p, b := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removed := CleanStrayRenderedPages(s)
	if len(removed) != 2 {
		t.Fatalf("removed %v, want the two rendered copies", removed)
	}
	for p := range files {
		_, err := os.Stat(filepath.Join(dir, p))
		gone := errors.Is(err, fs.ErrNotExist)
		wantGone := p == "content/pub.polis.core/post/20260401/hello.html" || p == "content/pub.polis.core/comment/20260402/reply.html"
		if gone != wantGone {
			t.Errorf("%s: gone=%v, want %v", p, gone, wantGone)
		}
	}
}
