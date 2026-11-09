package version

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func TestVersionsKeySitsBesideTheCanonicalFile(t *testing.T) {
	got := VersionsKey("content/pub.polis.core/post/20260101/hello.md", ".versions")
	if want := "content/pub.polis.core/post/20260101/.versions/hello.md"; got != want {
		t.Errorf("VersionsKey = %q, want %q", got, want)
	}
}

// ⭐ A SIDE-CAR ON A STORE WITH NO FILESYSTEM IS THE SAME SIDE-CAR.
//
// A base version and an edit are recorded on a POSIX site and on an
// object-backed one whose directory does not exist. The bytes must match, and
// the POSIX side-car must be the public file it always was.
func TestHistoryIsTheSameOnAStoreWithNoFilesystem(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
	const canonical = "content/pub.polis.core/post/20260101/hello.md"
	key := VersionsKey(canonical, ".versions")

	dir := t.TempDir()
	sites := []storage.Site{
		storage.At(dir),
		storage.On(filepath.Join(t.TempDir(), "no-such-dir"), storage.NewObjectStore("tenants/alice")),
	}
	var got [][]byte
	for _, site := range sites {
		if err := InitializeHistory(site, key, canonical, "one\n", "aaaa", "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if err := AppendHistory(site, key, canonical, "aaaa", "bbbb", "2026-01-02T00:00:00Z", "one\n", "two\n"); err != nil {
			t.Fatal(err)
		}
		data, err := site.Store().Get(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, data)
	}
	// ⚠️ diff -u names its two temp files and their mtimes in the --- and +++
	// lines, so those differ between any two runs; everything else must not.
	if !bytes.Equal(withoutDiffHeaders(got[0]), withoutDiffHeaders(got[1])) {
		t.Errorf("the side-cars differ:\nposix:\n%s\nobject:\n%s", got[0], got[1])
	}
	if !strings.Contains(string(got[0]), "# CURRENT_HASH=sha256:bbbb") || !strings.Contains(string(got[0]), "PARENT=sha256:aaaa") {
		t.Errorf("the edit was not recorded:\n%s", got[0])
	}
	if _, err := os.Stat(sites[1].Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something was written to the object-backed site's directory: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(key)))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o044 != 0o044 {
		t.Errorf("side-car mode %o is not readable by others; it is public content", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(filepath.Join(dir, filepath.FromSlash(key))))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o755 {
		t.Errorf(".versions directory mode = %o, want 755", di.Mode().Perm())
	}
}

func withoutDiffHeaders(b []byte) []byte {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// failingStat is an object store that cannot say whether a key exists.
type failingStat struct{ *storage.ObjectStore }

func (failingStat) Stat(context.Context, string) (storage.Info, error) {
	return storage.Info{}, errors.New("stat refused")
}

// ⛔ THREE ARMS: absent, could-not-tell, present.
//
// An absent side-car gets a header and the entry. A side-car the store cannot
// stat is an error, never a fresh header written over a history that may be
// there.
func TestAppendHistoryTellsAbsentFromCouldNotTell(t *testing.T) {
	ctx := context.Background()
	const canonical = "content/pub.polis.core/post/20260101/hello.md"
	key := VersionsKey(canonical, ".versions")

	obj := storage.NewObjectStore("tenants/alice")
	site := storage.On(filepath.Join(t.TempDir(), "no-such-dir"), obj)
	if err := AppendHistory(site, key, canonical, "aaaa", "bbbb", "2026-01-02T00:00:00Z", "one\n", "two\n"); err != nil {
		t.Fatalf("an absent side-car: %v", err)
	}
	data, err := obj.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# VERSION_FILE_FORMAT=1.0\n# CANONICAL_FILE="+canonical+"\n") {
		t.Errorf("an absent side-car got no header:\n%s", data)
	}

	before := string(data)
	failing := storage.On(site.Dir(), failingStat{obj})
	if err := AppendHistory(failing, key, canonical, "bbbb", "cccc", "2026-01-03T00:00:00Z", "two\n", "three\n"); err == nil {
		t.Error("a side-car the store could not stat was written anyway")
	}
	after, _ := obj.Get(ctx, key)
	if string(after) != before {
		t.Errorf("the side-car changed after a failed stat:\n%s", after)
	}
}
