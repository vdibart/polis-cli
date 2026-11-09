package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A temp file an atomic write leaves while it runs, or after its writer was
// killed, is not a file of the site's: no listing may show it, or a sweep
// racing a write reports a file that was never created.

const tempOfIndex = ".index.html.tmp-0123456789ab"

func TestIsAtomicTempMatchesOnlyTheTempNames(t *testing.T) {
	for name, want := range map[string]bool{
		tempOfIndex:                     true,
		".id_ed25519.tmp-a1b2c3d4e5f6":  true,
		"index.html":                    false,
		"index.html.tmp-0123456789ab":   false, // no leading dot
		".index.html.tmp-zz23456789ab":  false, // not hex
		".index.html.tmp-0123456789a":   false, // eleven characters
		".index.html.tmp-0123456789abc": false,
		".tmp-0123456789ab":             false, // no base name
	} {
		if got := IsAtomicTemp(name); got != want {
			t.Errorf("IsAtomicTemp(%q) = %v, want %v", name, got, want)
		}
	}
}

// ⭐ The name writeFileAtomic makes is one IsAtomicTemp recognises: the two
// share one marker, and this catches a change to either.
func TestTheTempNameAWriteMakesIsRecognised(t *testing.T) {
	tmp, err := atomicTempPath(filepath.Join(t.TempDir(), "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	if !IsAtomicTemp(filepath.Base(tmp)) {
		t.Fatalf("the temp name %q is not one IsAtomicTemp recognises", filepath.Base(tmp))
	}
}

func setUpTemps(t *testing.T) (Store, string) {
	t.Helper()
	dir := t.TempDir()
	s := NewSite(dir)
	ctx := context.Background()
	for _, k := range []string{"index.html", "posts/a.html"} {
		if err := s.Put(ctx, k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{tempOfIndex, "posts/.a.html.tmp-abcdefabcdef"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s, dir
}

func keysOf(es []Entry) map[string]bool {
	m := map[string]bool{}
	for _, e := range es {
		m[e.Key] = true
	}
	return m
}

func TestListingsHideAtomicTemps(t *testing.T) {
	s, dir := setUpTemps(t)
	ctx := context.Background()

	flat, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if keysOf(flat)[tempOfIndex] {
		t.Error("a flat List shows a temp file")
	}
	if !keysOf(flat)["index.html"] {
		t.Error("a flat List lost the real file")
	}

	all, err := s.List(ctx, "", Recursive())
	if err != nil {
		t.Fatal(err)
	}
	k := keysOf(all)
	if k[tempOfIndex] || k["posts/.a.html.tmp-abcdefabcdef"] {
		t.Errorf("a recursive List shows a temp file: %v", k)
	}
	if !k["posts/a.html"] {
		t.Error("a recursive List lost the real file")
	}

	viaDir, err := ListDir(filepath.Join(dir, "posts"))
	if err != nil {
		t.Fatal(err)
	}
	if keysOf(viaDir)[".a.html.tmp-abcdefabcdef"] {
		t.Error("ListDir shows a temp file")
	}

	with, err := s.List(ctx, "", Recursive(), IncludeAtomicTemps())
	if err != nil {
		t.Fatal(err)
	}
	if !keysOf(with)[tempOfIndex] {
		t.Error("IncludeAtomicTemps did not show the temp file")
	}
}

// ⭐ A killed writer's leftover is removed by a later write in its directory,
// once it is older than StaleAtomicTempAge; a younger one is left, because
// it may belong to a write still running.
func TestALaterWriteRemovesAStaleTempButNotAFreshOne(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".a.html.tmp-aaaaaaaaaaaa")
	fresh := filepath.Join(dir, ".b.html.tmp-bbbbbbbbbbbb")
	lookalike := filepath.Join(dir, ".c.html.tmp-notatemp")
	for _, p := range []string{stale, fresh, lookalike} {
		if err := os.WriteFile(p, []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * StaleAtomicTempAge)
	for _, p := range []string{stale, lookalike} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	staleTempsCleaned.Delete(dir)
	if err := WriteFileAtomic(filepath.Join(dir, "a.html"), []byte("x"), 0o644, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the stale temp survived the next write")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a fresh temp was removed; it may be a write in flight")
	}
	if _, err := os.Stat(lookalike); err != nil {
		t.Error("a name that only resembles a temp was removed")
	}
}
