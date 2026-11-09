package publish

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// A missing index is an error, never a creation: UpdateIndexEntry edits an
// entry that must already exist, and writing a fresh index holding only the
// edited line would erase every other entry the site publishes.
func TestUpdateIndexEntryOnAMissingIndexIsAnErrorAndWritesNothing(t *testing.T) {
	site := storage.At(t.TempDir())

	err := UpdateIndexEntry(site, "content/pub.polis.core/post/20260101/a.md", "A", "sha256:x")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("UpdateIndexEntry with no index = %v, want an absence error", err)
	}
	if ok, _ := site.Store().Exists(context.Background(), "content/pub.polis.core/index.jsonl"); ok {
		t.Fatal("UpdateIndexEntry created an index; a missing index must stay missing")
	}
}

// Republishing a post that does not exist fails before anything is signed or
// written.
func TestRepublishPostOnAMissingPostIsAnError(t *testing.T) {
	site := storage.At(t.TempDir())
	key := "content/pub.polis.core/post/20260101/missing.md"

	if _, err := RepublishPost(site, key, "# Missing\n\nbody", nil); err == nil {
		t.Fatal("RepublishPost on a missing post succeeded")
	}
	if ok, _ := site.Store().Exists(context.Background(), key); ok {
		t.Fatal("RepublishPost created the post it was asked to update")
	}
}

// Rewriting an index entry keeps the index's existing mode. Under a strict
// umask a newly created file would come back 0600, and the public index must
// stay readable to whatever serves the site.
func TestUpdateIndexEntryKeepsTheIndexMode(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	site := storage.At(dir)
	key := "content/pub.polis.core/post/20260101/a.md"
	idx := filepath.Join(dir, "content", "pub.polis.core", "index.jsonl")
	if err := os.MkdirAll(filepath.Dir(idx), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"post","path":"` + key + `","title":"A","current_version":"sha256:x"}` + "\n"
	if err := os.WriteFile(idx, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(idx, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := UpdateIndexEntry(site, key, "B", "sha256:y"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(idx)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Fatalf("index mode after UpdateIndexEntry = %o, want 644", got)
	}
}
