package sitecheck

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔⛔ THE ONE CONSUMER OF Capabilities().Modes MUST BE ABLE TO SEE A BACKEND
// THAT HAS NONE.
//
// ⚠️ KeyPerms used to build its own store with storage.NewSite(siteDir), so it
// always got a POSIX store regardless of what the site was stored on. On a
// blob-backed site it would stat a local filesystem holding none of the site's
// data and report whatever it found — which is worse than saying it cannot
// check, because it looks like an answer.
//
// ⭐ The rule in one test: whatever reads
// Modes has an answer that does not depend on them.
func TestKeyPermsSaysSoWhenTheBackendHasNoModes(t *testing.T) {
	blob := storage.On(t.TempDir(), storage.NewObjectStore(""))
	got := KeyPerms(blob)
	if got.OK {
		t.Error("KeyPerms reported OK on a backend with no modes; it cannot have checked anything")
	}
	if got.Message != "file modes are not available on this storage backend" {
		t.Errorf("message = %q; it must say the check could not run, not that it failed",
			got.Message)
	}
}

// ⭐ And on POSIX it still checks for real.
func TestKeyPermsStillChecksOnPosix(t *testing.T) {
	site := storage.At(t.TempDir())
	// A key at 0600 is the correct case.
	if err := site.Store().Put(t.Context(), ".polis/keys/id_ed25519",
		[]byte("k"), storage.WithMode(0o600)); err != nil {
		t.Fatal(err)
	}
	if got := KeyPerms(site); !got.OK {
		t.Errorf("a 0600 key was reported not OK: %q", got.Message)
	}
	// And 0644 is the failure it exists to catch.
	other := storage.At(t.TempDir())
	if err := other.Store().Put(t.Context(), ".polis/keys/id_ed25519",
		[]byte("k"), storage.WithMode(0o644)); err != nil {
		t.Fatal(err)
	}
	if got := KeyPerms(other); got.OK {
		t.Error("a 0644 private key was reported OK")
	}
}
