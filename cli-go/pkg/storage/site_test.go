package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// ⛔ .polis must not be world-readable, and the constructor is what carries
// that rule. At() must use NewSite, never NewPOSIX — a store built the other
// way creates parents at 0755 and exposes keys, drafts and DM state on a
// shared host.
//
// ⚠️ This is not hypothetical: converting one append site to the store dropped
// an explicit 0700 and would have shipped exactly that, with nothing at any
// layer catching it.
func TestAtMakesPolisPrivate(t *testing.T) {
	dir := t.TempDir()
	site := At(dir)

	if err := site.Store().Put(context.Background(), ".polis/keys/id_ed25519", []byte("k")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".polis"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf(".polis created with %04o; it must not be group- or world-readable", perm)
	}
}

// ⭐ A zero Site has a nil store, and the failure that produces is a nil
// dereference far from the mistake. Valid() is what a function accepting one
// from outside should check.
func TestZeroSiteIsNotValid(t *testing.T) {
	var zero Site
	if zero.Valid() {
		t.Error("a zero Site reported valid")
	}
	if zero.Store() != nil {
		t.Error("a zero Site has a store")
	}
	if !At(t.TempDir()).Valid() {
		t.Error("a constructed Site reported invalid")
	}
	// ⚠️ A root with no store is also invalid — On(dir, nil) is a mistake, not
	// a POSIX shorthand.
	if On("/tmp/x", nil).Valid() {
		t.Error("On(dir, nil) reported valid; it would nil-dereference later")
	}
}

// ⭐ On() is how a blob-backed instance is built. The handle must use the store
// it was given and nothing else — if it silently fell back to POSIX, a remote
// instance would write to an ephemeral local disk and look like it worked.
func TestOnUsesTheStoreItWasGiven(t *testing.T) {
	dir := t.TempDir()
	explicit := NewPOSIX(dir)
	site := On(dir, explicit)
	if site.Store() != explicit {
		t.Error("On() did not keep the store it was handed")
	}
}

// ⚠️ Two handles for the same site must agree on the root, or a per-key lock
// taken through one does not exclude a writer using the other.
func TestTheRootIsNormalised(t *testing.T) {
	dir := t.TempDir()
	for _, variant := range []string{dir, dir + "/", dir + "/./", dir + "/sub/.."} {
		if got := At(variant).Dir(); got != At(dir).Dir() {
			t.Errorf("At(%q).Dir() = %q, want %q — two handles on one site must agree", variant, got, At(dir).Dir())
		}
	}
}

func TestPathJoinsOntoTheRoot(t *testing.T) {
	site := At("/sites/alice")
	if got := site.Path("content", "pub.polis.core", "index.jsonl"); got != "/sites/alice/content/pub.polis.core/index.jsonl" {
		t.Errorf("Path() = %q", got)
	}
	if got := site.Path(); got != "/sites/alice" {
		t.Errorf("Path() with no elements = %q, want the root", got)
	}
}

// ⛔⛔ AN EMPTY ROOT IS A CALLER BUG, NOT "THE CURRENT DIRECTORY".
//
// ⚠️ cleanAbs("") returns ".", so At("") used to hand back a handle rooted at
// the working directory, reporting Valid. Two bundle tests existed to assert
// that an empty site dir is an error, and the site-handle conversion silently
// disarmed them — which is how this was found. A guard that a refactor can
// turn off without failing is worse than none.
func TestAtRefusesAnEmptyRoot(t *testing.T) {
	s := At("")
	if s.Valid() {
		t.Error("At(\"\") reported valid; an empty root must not mean the working directory")
	}
	if s.Dir() != "" {
		t.Errorf("At(\"\").Dir() = %q, want empty", s.Dir())
	}
	if s.Store() != nil {
		t.Error("At(\"\") carries a store")
	}
}

// ⭐ ID is what in-process bookkeeping keys on (the DM rate limiter), so two
// handles for one site must agree, two sites must not, and the value must not
// be usable as a path.
func TestIDIsStableAndIsNotAPath(t *testing.T) {
	dir := t.TempDir()
	a, b := At(dir), At(dir)
	if a.ID() != b.ID() {
		t.Errorf("two handles for one site disagree: %q vs %q", a.ID(), b.ID())
	}
	if At(dir).ID() == At(t.TempDir()).ID() {
		t.Error("two different sites share an ID")
	}
	if blob := On(dir, NewObjectStore("tenants/alice")); blob.ID() != a.ID() {
		t.Errorf("a blob-backed handle for the same root has a different ID: %q vs %q", blob.ID(), a.ID())
	}
	if _, err := os.Stat(a.ID()); err == nil {
		t.Errorf("ID %q resolves on the filesystem; it must never be usable as a path", a.ID())
	}
	if (Site{}).ID() != "" {
		t.Error("a zero handle has an ID")
	}
}
