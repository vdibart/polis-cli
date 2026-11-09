package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// ⛔ An atomicfile write (keepMode false) REPLACES a symlink and never writes
// through it: Medic and the actor key projection write with it inside tenant
// directories, where a planted link would point outside. A store Put
// (keepMode true) writes through, as os.WriteFile did.
func TestAtomicWriteReplacesALinkAndPutWritesThroughOne(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "target")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(link, []byte("new"), 0o600, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "untouched" {
		t.Error("⛔ an atomic write followed the link and wrote outside")
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("the link was not replaced by a 0600 file: %v", fi.Mode())
	}

	link2 := filepath.Join(dir, "target2")
	if err := os.Symlink(outside, link2); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(link2, []byte("through"), 0o644, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "through" {
		t.Error("a store Put no longer writes through a link, as os.WriteFile did")
	}
}
