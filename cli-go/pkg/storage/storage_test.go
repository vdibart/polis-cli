package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func newStore(t *testing.T) (*storage.POSIX, string) {
	t.Helper()
	dir := t.TempDir()
	return storage.NewPOSIX(dir), dir
}

func TestCleanKeyRejectsEscapesAndAbsolutes(t *testing.T) {
	// An absolute key is refused rather than normalised: trimming the slash
	// would turn a caller's mistaken filesystem path into a plausible-looking
	// key and write it somewhere, instead of failing.
	bad := []string{"/etc/passwd", "/a/b", "../outside", "a/../../outside", "\x00"}
	for _, k := range bad {
		if _, err := storage.CleanKey(k); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("CleanKey(%q) = %v, want ErrInvalidKey", k, err)
		}
	}
	// A trailing slash is not significant.
	for _, pair := range [][2]string{
		{"a/b", "a/b"}, {"a/b/", "a/b"}, {"", ""}, {".", ""}, {"a//b", "a/b"},
	} {
		got, err := storage.CleanKey(pair[0])
		if err != nil {
			t.Fatalf("CleanKey(%q): %v", pair[0], err)
		}
		if got != pair[1] {
			t.Errorf("CleanKey(%q) = %q, want %q", pair[0], got, pair[1])
		}
	}
}

func TestPutCreatesParentPrefixesImplicitly(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	key := "content/pub.polis.core/post/20261001/hello.md"

	if err := s.Put(ctx, key, []byte("body")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// No caller created a directory, and yet the whole chain exists. This is
	// what removes the MkdirAll call sites.
	if _, err := os.Stat(filepath.Join(dir, "content", "pub.polis.core", "post", "20261001")); err != nil {
		t.Fatalf("parent prefixes were not created: %v", err)
	}
	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "body" {
		t.Errorf("Get = %q, want %q", got, "body")
	}
}

func TestNotFoundIsCheckableWithErrorsIs(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()

	if _, err := s.Get(ctx, "nope.md"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
	if _, err := s.Stat(ctx, "nope.md"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Stat missing = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, "nope.md"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Delete missing = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, "nope.md", storage.IgnoreMissing()); err != nil {
		t.Errorf("Delete missing with IgnoreMissing = %v, want nil", err)
	}
}

// The reason ErrNotFound exists rather than reusing os.IsNotExist: the moment
// an error is wrapped, os.IsNotExist stops seeing it and reports "exists" for
// something that is absent. Every caller that branches on not-found would take
// the wrong branch silently.
func TestWrappedNotFoundStillMatchesButDefeatsOsIsNotExist(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.Get(context.Background(), "nope.md")
	wrapped := errors.Join(errors.New("while loading the index"), err)

	if !errors.Is(wrapped, storage.ErrNotFound) {
		t.Error("errors.Is must see through a wrap")
	}
	if os.IsNotExist(wrapped) {
		t.Error("os.IsNotExist unexpectedly saw through a wrap; the premise of ErrNotFound has changed")
	}
}

func TestExistsDoesNotReportMissingAsAnError(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	ok, err := s.Exists(ctx, "nope.md")
	if err != nil {
		t.Fatalf("Exists on a missing key returned an error: %v", err)
	}
	if ok {
		t.Error("Exists = true for a missing key")
	}
	if err := s.Put(ctx, "there.md", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, "there.md"); !ok {
		t.Error("Exists = false for a present key")
	}
}

func TestListDirectChildrenVersusRecursive(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"a/one.md", "a/b/two.md", "a/b/c/three.md", "top.md"} {
		if err := s.Put(ctx, k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	direct, err := s.List(ctx, "a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(direct) != 2 {
		t.Fatalf("List(a) returned %d entries, want 2: %+v", len(direct), direct)
	}
	// Keys are full, not basenames, so a caller can pass one back to Get.
	var sawDir bool
	for _, e := range direct {
		if e.IsDir {
			sawDir = true
			if e.Key != "a/b" {
				t.Errorf("directory entry key = %q, want a/b", e.Key)
			}
		} else if e.Key != "a/one.md" {
			t.Errorf("file entry key = %q, want a/one.md", e.Key)
		}
	}
	if !sawDir {
		t.Error("List did not report a/b as a directory")
	}

	rec, err := s.List(ctx, "a", storage.Recursive())
	if err != nil {
		t.Fatalf("List recursive: %v", err)
	}
	var files int
	for _, e := range rec {
		if !e.IsDir {
			files++
		}
	}
	if files != 3 {
		t.Errorf("recursive List found %d files, want 3: %+v", files, rec)
	}
}

func TestListMissingPrefixIsEmptyNotAnError(t *testing.T) {
	s, _ := newStore(t)
	got, err := s.List(context.Background(), "never/existed")
	if err != nil {
		t.Fatalf("List on a missing prefix returned %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("want no entries, got %+v", got)
	}
}

func TestPutWithModeIsHonouredWhereModesExist(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	if !s.Capabilities().Modes {
		t.Skip("backend does not claim Modes")
	}
	if err := s.Put(ctx, ".polis/keys/id_ed25519", []byte("secret"), storage.WithMode(0o600)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
	info, err := s.Stat(ctx, ".polis/keys/id_ed25519")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != 0o600 {
		t.Errorf("Stat reported mode %o, want 600", info.Mode)
	}
}

func TestDeleteRecursiveAndRootRefusal(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"d/one.md", "d/sub/two.md"} {
		if err := s.Put(ctx, k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete(ctx, "d"); err == nil {
		t.Error("non-recursive Delete of a populated prefix should fail")
	}
	if err := s.Delete(ctx, "d", storage.DeleteRecursive()); err != nil {
		t.Fatalf("recursive Delete: %v", err)
	}
	if ok, _ := s.Exists(ctx, "d/sub/two.md"); ok {
		t.Error("recursive Delete left a key behind")
	}
	for _, root := range []string{"", "/", "."} {
		if err := s.Delete(ctx, root, storage.DeleteRecursive()); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("Delete(%q) = %v, want ErrInvalidKey", root, err)
		}
	}
}

// A21: an export is a copy, not a reconstruction. The key a post is published
// at, the key it is stored at, and the key it is restored at are one string.
// A backend that hashes, flattens or prefixes keys fails this by construction.
func TestExportIsACopyNotAReconstruction(t *testing.T) {
	src, _ := newStore(t)
	dst, _ := newStore(t)
	ctx := context.Background()

	original := map[string][]byte{
		"content/pub.polis.core/post/20261001/hello.md":           []byte("signed post"),
		"content/pub.polis.core/post/20261001/.versions/hello.md": []byte("history"),
		"content/pub.polis.core/index.jsonl":                      []byte(`{"path":"x"}`),
		".well-known/polis":                                       []byte(`{"public_key":"k"}`),
		"posts/20261001/hello.html":                               []byte("<html>"),
	}
	for k, v := range original {
		if err := src.Put(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}

	// Export is List + Get. Import is Put at the same key. No mapping step.
	entries, err := src.List(ctx, "", storage.Recursive())
	if err != nil {
		t.Fatal(err)
	}
	copied := 0
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		b, err := src.Get(ctx, e.Key)
		if err != nil {
			t.Fatalf("Get(%q): %v", e.Key, err)
		}
		if err := dst.Put(ctx, e.Key, b); err != nil {
			t.Fatalf("Put(%q): %v", e.Key, err)
		}
		copied++
	}
	if copied != len(original) {
		t.Fatalf("copied %d keys, want %d", copied, len(original))
	}
	for k, want := range original {
		got, err := dst.Get(ctx, k)
		if err != nil {
			t.Fatalf("destination is missing %q: %v", k, err)
		}
		if string(got) != string(want) {
			t.Errorf("%q: bytes differ after round trip", k)
		}
	}
}

func TestTraversalIsRefusedAtEveryEntryPoint(t *testing.T) {
	s, dir := newStore(t)
	ctx := context.Background()
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	if err := os.WriteFile(outside, []byte("do not read me"), 0o644); err != nil {
		t.Fatal(err)
	}
	escape := "../" + filepath.Base(outside)

	if _, err := s.Get(ctx, escape); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("Get(%q) = %v, want ErrInvalidKey", escape, err)
	}
	if err := s.Put(ctx, escape, []byte("x")); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("Put(%q) = %v, want ErrInvalidKey", escape, err)
	}
	if _, err := s.Exists(ctx, escape); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("Exists(%q) = %v, want ErrInvalidKey", escape, err)
	}
	if err := s.Delete(ctx, escape); !errors.Is(err, storage.ErrInvalidKey) {
		t.Errorf("Delete(%q) = %v, want ErrInvalidKey", escape, err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "do not read me" {
		t.Error("the file outside the root was modified")
	}
}

func TestRelKeyConvertsHelperPathsAndRefusesEscapes(t *testing.T) {
	root := "/srv/site"
	for _, pair := range [][2]string{
		{"/srv/site/content/pub.polis.core/index.jsonl", "content/pub.polis.core/index.jsonl"},
		{"/srv/site/.well-known/polis", ".well-known/polis"},
		{"/srv/site", ""},
	} {
		got, err := storage.RelKey(root, pair[0])
		if err != nil {
			t.Fatalf("RelKey(%q): %v", pair[0], err)
		}
		if got != pair[1] {
			t.Errorf("RelKey(%q) = %q, want %q", pair[0], got, pair[1])
		}
	}
	for _, bad := range []string{"/etc/passwd", "/srv/other/x.md", "/srv/site/../outside"} {
		if _, err := storage.RelKey(root, bad); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("RelKey(%q) = %v, want ErrInvalidKey", bad, err)
		}
	}
}
