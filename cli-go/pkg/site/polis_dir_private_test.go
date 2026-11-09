package site_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ ".polis can not be world-readable under any circumstances" (Vincent,
// 2026-10-01). This is the end-to-end guard for that, and it is deliberately
// NOT a test of the storage interface: it initialises a real site and inspects
// what is on disk, so it holds no matter which code path created a directory or
// which constructor that path happened to choose.
//
// It exists because the detach made this breakable. storage.Put creates parent
// directories, and before NewSite declared .polis private at 0700 a write into
// .polis/dm would have left the tree at 0755 — on a shared host, enough for any
// local account to list a user's conversations. It compiled and every test passed.
func TestNothingUnderPolisIsReadableByGroupOrOther(t *testing.T) {
	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{
		Author:    "Test Author",
		SiteTitle: "test",
		BaseURL:   "https://example.com",
	}); err != nil {
		t.Fatalf("site.Init: %v", err)
	}

	polis := filepath.Join(dir, ".polis")
	if _, err := os.Stat(polis); err != nil {
		t.Fatalf("no .polis after init: %v", err)
	}

	// ⛔⛔ A PUT MUST CREATE A DIRECTORY HERE, or this guard proves nothing.
	// The first version only inspected what site.Init had already created, and
	// Init pre-creates every directory at the right mode — so removing the private
	// prefix from storage.NewSite left this test GREEN. A reviewer found that by
	// injecting exactly that change.
	//
	// Writing to a key whose parents do NOT exist is what forces the store to
	// choose a directory mode, which is the property under test.
	st := storage.NewSite(dir)
	for _, key := range []string{
		".polis/guard-probe/nested/deeper/x.json",
		".polis/ds/example.com/pub.polis.core/state/cursor.json",
	} {
		if err := st.Put(context.Background(), key, []byte("{}"), storage.WithMode(0o600)); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
	}

	var loose []string
	seen := 0
	if err := filepath.Walk(polis, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		seen++
		// ⭐ DIRECTORIES are the assertion, because traversal is the gate: a
		// 0700 directory makes everything beneath it unreachable whatever its own
		// mode says. That is also why this matters beyond today — the inner modes
		// become live the moment the tree is copied somewhere else.
		if fi.IsDir() && fi.Mode().Perm()&0o077 != 0 {
			rel, _ := filepath.Rel(dir, p)
			loose = append(loose, "dir  "+rel+" is "+fi.Mode().Perm().String())
		}
		// ⚠️ File modes are NOT asserted wholesale: .polis/keys/id_ed25519.pub is
		// 0644 and is a PUBLIC key, published in .well-known/polis. The one file
		// whose mode is load-bearing is checked separately below.
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}

	// ⛔ A guard that inspects nothing passes forever.
	if seen < 2 {
		t.Fatalf("walked only %d entries under .polis; this check would pass on an empty tree", seen)
	}

	if fi, err := os.Stat(filepath.Join(dir, ".polis", "keys", "id_ed25519")); err == nil {
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("the PRIVATE key is %v — it must be owner-only", perm)
		}
	} else {
		t.Errorf("no private key after init: %v", err)
	}

	if len(loose) > 0 {
		sort.Strings(loose)
		t.Errorf("these entries under .polis are reachable by group or other, which is never "+
			"allowed — on a shared host this exposes keys, drafts, DM state and feed cursors:\n  %s",
			strings.Join(loose, "\n  "))
	}
}
