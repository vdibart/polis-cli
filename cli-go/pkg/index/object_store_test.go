package index

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// copyIntoObjectStore puts every file under dir into a fresh object store, at
// the same keys, and returns a site on it whose directory does not exist.
func copyIntoObjectStore(t *testing.T, dir string) storage.Site {
	t.Helper()
	obj := storage.NewObjectStore("tenants/alice")
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return obj.Put(context.Background(), filepath.ToSlash(rel), data)
	})
	if err != nil {
		t.Fatal(err)
	}
	return storage.On(filepath.Join(t.TempDir(), "no-such-dir"), obj)
}

// ⭐ A REBUILD ON A STORE WITH NO FILESYSTEM WRITES THE SAME INDEX.
//
// Every contributor runs against a POSIX site and an object-backed copy of it.
// The object-backed site's directory does not exist, so anything still reading
// a path would index nothing, and the bytes would differ. A version snapshot
// sits in each tree to pin that both walks skip .versions.
func TestRebuildOnAStoreWithNoFilesystemWritesTheSameIndex(t *testing.T) {
	dataDir := seedFourTypes(t)
	write(t, filepath.Join(dataDir, "content", "pub.polis.core", "post", "20260101", ".versions", "hello.md"),
		"---\ntitle: Old\npublished: 2026-01-01T00:00:00Z\n---\n\nold\n")
	obj := copyIntoObjectStore(t, dataDir)

	posixResult, err := RebuildContentIndex(storage.At(dataDir), nil)
	if err != nil {
		t.Fatal(err)
	}
	objResult, err := RebuildContentIndex(obj, nil)
	if err != nil {
		t.Fatal(err)
	}

	key := IndexKey(obj)
	if key != "content/pub.polis.core/index.jsonl" {
		t.Fatalf("IndexKey = %q", key)
	}
	posixBytes, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(key)))
	if err != nil {
		t.Fatal(err)
	}
	objBytes, err := obj.Store().Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(posixBytes, objBytes) {
		t.Errorf("the indexes differ:\nposix:\n%s\nobject:\n%s", posixBytes, objBytes)
	}
	if posixResult.Total != 4 || objResult.Total != 4 {
		t.Errorf("totals: posix %d, object %d, want 4 each", posixResult.Total, objResult.Total)
	}
	if strings.Contains(string(objBytes), ".versions") {
		t.Errorf("a version snapshot was indexed:\n%s", objBytes)
	}
}

// failingList is an object store whose listings fail: the walk could not look.
type failingList struct{ *storage.ObjectStore }

func (failingList) List(context.Context, string, ...storage.ListOption) ([]storage.Entry, error) {
	return nil, errors.New("listing refused")
}

// ⛔ AN ABSENT PREFIX AND A FAILED LISTING ARE DIFFERENT ANSWERS.
//
// A site with no content lists empty, and the rebuild writes an empty index:
// "nothing there". A listing that fails is "could not look", and the rebuild
// must say so rather than write an index that drops every post.
func TestRebuildTellsAnAbsentPrefixFromAFailedListing(t *testing.T) {
	ctx := context.Background()

	empty := storage.On(filepath.Join(t.TempDir(), "no-such-dir"), storage.NewObjectStore("tenants/empty"))
	result, err := RebuildContentIndex(empty, nil)
	if err != nil {
		t.Fatalf("a site with no content is not an error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("Total = %d on an empty site", result.Total)
	}
	if ok, _ := empty.Store().Exists(ctx, IndexKey(empty)); !ok {
		t.Error("an empty site got no index")
	}

	obj := storage.NewObjectStore("tenants/alice")
	if err := obj.Put(ctx, "content/pub.polis.core/post/20260101/hello.md",
		[]byte("---\ntitle: Hello\npublished: 2026-01-01T00:00:00Z\ncurrent-version: sha256:aaaa\n---\n\nHi.\n")); err != nil {
		t.Fatal(err)
	}
	failing := storage.On(filepath.Join(t.TempDir(), "no-such-dir"), failingList{obj})
	if _, err := RebuildContentIndex(failing, nil); err == nil {
		t.Fatal("a listing that failed was read as an empty site")
	}
	if ok, _ := obj.Exists(ctx, "content/pub.polis.core/index.jsonl"); ok {
		t.Error("an index was written after a listing failed")
	}
}
