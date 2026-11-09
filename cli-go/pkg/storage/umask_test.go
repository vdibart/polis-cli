//go:build !windows

package storage

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// ⛔⛔ TWO MECHANISMS, TWO UMASK BEHAVIOURS, AND THE DIFFERENCE IS OBSERVABLE.
//
// A directory created through the STORE is EXACT — it chmods after Mkdir,
// because a `.polis` left at 0755 by a permissive umask is the bug
// WithPrivatePrefix exists to prevent, and `polis clone` depends on the same
// exactness for its tree.
//
// ⚠️ A directory created by a hand-rolled os.MkdirAll RESPECTS THE UMASK, and
// that is what every caller atomicfile.Write replaces used to do.
//
// ⭐⭐⭐ I GOT THIS WRONG TWICE AND THE DIFFERENTIAL CAUGHT BOTH. Making the
// store's chmod conditional broke clone (0755 → 0700 under umask 077); leaving
// it unconditional while routing parent creation through it broke tag content
// the other way (0700 → 0755). One rule in the store cannot satisfy both,
// because the inconsistency is in the CALLERS. So EnsureParent — the one new
// path — splits it: a declared private prefix takes the store's exact rule, and
// anything else takes os.MkdirAll, which is what its callers did.
func TestStoreCreatedDirectoriesAreExactUnderAnyUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	s := NewSite(dir)
	ctx := context.Background()

	// ⭐ Through Put: the store's rule, so exact. `polis clone` depends on this.
	if err := s.Put(ctx, "content/pub.polis.core/tag/reading.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	content, err := os.Stat(filepath.Join(dir, "content", "pub.polis.core", "tag"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := content.Mode().Perm(); perm != 0o755 {
		t.Errorf("a store-created content directory is %04o under umask 077; the store's "+
			"mode rule is exact, and clone depends on it", perm)
	}

	// ⛔ And .polis is exact too, which is the guarantee that must never depend
	// on a process setting.
	if err := s.Put(ctx, ".polis/keys/id_ed25519", []byte("k")); err != nil {
		t.Fatal(err)
	}
	priv, err := os.Stat(filepath.Join(dir, ".polis"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := priv.Mode().Perm(); perm != 0o700 {
		t.Errorf(".polis is %04o; a declared private prefix must be exactly 0700", perm)
	}
}

// ⚠️ EnsureParent is the one new path, and it must match what its callers did:
// os.MkdirAll for ordinary content, the store's exact rule for `.polis`.
func TestEnsureParentMatchesWhatItsCallersDid(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	p := NewSite(dir)

	// ordinary content: umask applies, as os.MkdirAll(dir, 0755) did
	if err := p.EnsureParent("content/pub.polis.core/tag/x.json"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "content", "pub.polis.core", "tag"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("EnsureParent made a content directory %04o under umask 077; its callers "+
			"used os.MkdirAll(dir, 0755), which the umask masks to 0700", perm)
	}

	// .polis: exact, umask or no umask
	if err := p.EnsureParent(".polis/bundles/registry.json"); err != nil {
		t.Fatal(err)
	}
	priv, err := os.Stat(filepath.Join(dir, ".polis"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := priv.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf(".polis made %04o by EnsureParent; a declared private prefix must be exact "+
			"so no umask can expose keys", perm)
	}
}

// ⭐ And the case that matters most: a PERMISSIVE umask must not loosen a
// declared private prefix. This is the direction that leaks a private key.
func TestAPermissiveUmaskCannotLoosenPolis(t *testing.T) {
	old := syscall.Umask(0o000)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	s := NewSite(dir)
	if err := s.Put(context.Background(), ".polis/keys/id_ed25519", []byte("k")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".polis"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf(".polis created %04o under umask 000; a declared private prefix must be "+
			"exact so a permissive umask cannot expose keys", perm)
	}
}

// ⛔ A NEW file gets the umask, exactly as os.WriteFile gave it. The first
// version of the atomic write chmodded new files too, so under umask 077 posts
// were created 0644 where they had been 0600 — the differential caught it.
func TestANewFileGetsTheUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	dir := t.TempDir()
	s := NewSite(dir)
	if err := s.Put(context.Background(), "content/new.md", []byte("x"), WithMode(0o644)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "content/new.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("a new file under umask 077 is %o; want 600", fi.Mode().Perm())
	}
}
