package atomicfile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔⛔ AN ATOMIC WRITE UNDER .polis MUST NOT CREATE A WORLD-READABLE PARENT.
//
// ⚠️ This is the private-tree permission failure by a different route. The POSIX branch writes a
// temp file with os.WriteFile, which does not create directories, so the parent
// has to be made first — and if this helper picked its own mode it would make
// `.polis` 0755 while the store's own rule says 0700. The parent is therefore
// created BY THE STORE, at the store's rule.
func TestAtomicWriteKeepsPolisPrivate(t *testing.T) {
	dir := t.TempDir()
	site := storage.At(dir)
	if err := Write(context.Background(), site, ".polis/bundles/registry.json",
		[]byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".polis"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf(".polis created with %04o by an atomic write; it must not be group- or "+
			"world-readable", perm)
	}
}

// ⭐ On a backend with no filesystem the Put IS the atomic write, and nothing
// touches a disk. ⛔ On Vercel the filesystem is read-only outside /tmp, so a
// helper that insisted on temp-and-rename would fail every write.
func TestAtomicWriteOnABackendWithNoFilesystem(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	site := storage.On(root, storage.NewObjectStore(""))

	if err := Write(ctx, site, ".polis/bundles/registry.json", []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("atomic write to an object store: %v", err)
	}
	got, err := site.Store().Get(ctx, ".polis/bundles/registry.json")
	if err != nil || string(got) != "{}\n" {
		t.Errorf("read back %q, %v", got, err)
	}
	// ⛔ and nothing on disk
	if entries, err := storage.ListDir(root); err == nil && len(entries) > 0 {
		t.Errorf("an atomic write to an object store touched the filesystem: %d entries",
			len(entries))
	}
}

// ⛔ No temp key may survive on an object store. Reaching for temp-and-rename
// there would double the requests and leave litter behind a failure, which is
// why the Put is used directly.
func TestAtomicWriteLeavesNoTempKeyOnAnObjectStore(t *testing.T) {
	ctx := context.Background()
	os_ := storage.NewObjectStore("")
	site := storage.On(t.TempDir(), os_)
	if err := Write(ctx, site, "a/b.json", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := os_.List(ctx, "", storage.Recursive())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Key != "a/b.json" {
		var keys []string
		for _, e := range entries {
			keys = append(keys, e.Key)
		}
		t.Errorf("store holds %v, want exactly [a/b.json]", keys)
	}
}

// ⛔ A zero handle must be refused rather than nil-dereferenced somewhere else.
func TestAtomicWriteRefusesAnUnusableSite(t *testing.T) {
	if err := Write(context.Background(), storage.Site{}, "k", []byte("x"), 0o644); err == nil {
		t.Error("wrote through a zero site handle")
	}
}

// ⚠️ A key at the store ROOT must still work when the root does not exist yet.
// Put creates the root; EnsureParent used to return early for a root-level key
// and leave it missing, so the .tmp write failed with ENOENT — the failure
// EnsureParent exists to prevent, one level up.
func TestAtomicWriteCreatesAMissingStoreRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not", "yet", "here")
	site := storage.At(root)
	if err := Write(context.Background(), site, "license.json", []byte("{}"), 0o644); err != nil {
		t.Fatalf("atomic write at the root of an uncreated store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "license.json")); err != nil {
		t.Errorf("file not written: %v", err)
	}
}
