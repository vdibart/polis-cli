package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ ".polis MUST NOT be world-readable under any circumstances" (Vincent,
// 2026-10-01). site.Init has encoded the same split since the beginning — 0700 at
// or under .polis, 0755 elsewhere — but nothing enforced it, and Put creating a
// parent at 0755 would have broken it silently. On a shared host, 0755 on
// .polis/dm lets any local account list a user's conversations.
//
// NewSite is the constructor that carries the rule, so the rule cannot be left
// off by forgetting an option.
func TestNewSiteKeepsEveryPolisParentPrivate(t *testing.T) {
	root := t.TempDir()
	st := storage.NewSite(root)

	for _, key := range []string{
		".polis/dm/conversations/peer/messages.jsonl",
		".polis/keys/id_ed25519",
		".polis/ds/example.com/pub.polis.core/state/cursor.json",
		".polis/bundles/pub.polis.core/themes/vice/vice.css",
	} {
		if err := st.Put(context.Background(), key, []byte("x"), storage.WithMode(0o600)); err != nil {
			t.Fatalf("Put %s: %v", key, err)
		}
	}

	// Every directory created under .polis, at every depth, is 0700.
	var loose []string
	_ = filepath.Walk(filepath.Join(root, ".polis"), func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return nil
		}
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			rel, _ := filepath.Rel(root, p)
			loose = append(loose, rel+" is "+perm.String())
		}
		return nil
	})
	if len(loose) > 0 {
		t.Errorf("these directories under .polis are reachable by group or other:\n  %s",
			strings.Join(loose, "\n  "))
	}

	// And content keeps the mode that serving needs.
	if err := st.Put(context.Background(), "content/pub.polis.core/post/20261001/a.md", []byte("x")); err != nil {
		t.Fatalf("Put content: %v", err)
	}
	fi, err := os.Stat(filepath.Join(root, "content/pub.polis.core/post/20261001"))
	if err != nil {
		t.Fatalf("stat content dir: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o755 {
		t.Errorf("content directory = %v, want 0755 — served content must stay readable", got)
	}
}

// The longest-matching prefix wins, so a nested tree can be tightened further
// without restating the parent.
func TestTheLongestPrivatePrefixWins(t *testing.T) {
	root := t.TempDir()
	st := storage.NewPOSIX(root,
		storage.WithPrivatePrefix(".polis", 0o700),
		storage.WithPrivatePrefix(".polis/dm", 0o750),
	)
	if err := st.Put(context.Background(), ".polis/dm/x/y.json", []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	for dir, want := range map[string]os.FileMode{
		".polis":      0o700,
		".polis/dm":   0o750,
		".polis/dm/x": 0o750,
	} {
		fi, err := os.Stat(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s = %v, want %v", dir, got, want)
		}
	}
}

// ⚠️ A plain NewPOSIX is NOT a site store and does not know the rule. It stays
// general because it also roots at a theme directory, the installed bundle and
// the CLI themes directory, none of which is a site.
func TestPlainPosixIsNotASiteStore(t *testing.T) {
	root := t.TempDir()
	if err := storage.NewPOSIX(root).Put(context.Background(), ".polis/a/b.json", []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	fi, err := os.Stat(filepath.Join(root, ".polis"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o755 {
		t.Errorf(".polis under a plain POSIX store = %v, want 0755; if this is now 0700, the "+
			"rule moved into NewPOSIX and NewSite may be redundant", got)
	}
}

// ⛔ A private mode the store could not write into is refused loudly, not
// honoured and not ignored. 0500 on a directory the store must create children in
// makes its own Put fail with EACCES — which is how it was found, by this file.
func TestAnUnusablePrivateModeIsRefusedLoudly(t *testing.T) {
	st := storage.NewPOSIX(t.TempDir(), storage.WithPrivatePrefix(".polis/dm", 0o500))
	err := st.Put(context.Background(), ".polis/dm/x/y.json", []byte("x"))
	if err == nil {
		t.Fatal("expected a refusal; a privacy option that is silently ignored is worse than none")
	}
	if !strings.Contains(err.Error(), "owner write and execute") {
		t.Errorf("the error should explain the constraint, got: %v", err)
	}
}

// The path-compat helpers root a store at the file's own directory, so a missing
// parent is created by THAT store, not by a site store — and with the default
// mode it came out 0755. This is how half the directories under .polis on the
// hosted fleet became world-readable.
func TestWriteAtPathKeepsMissingPolisParentsPrivate(t *testing.T) {
	site := t.TempDir()
	if err := os.Mkdir(filepath.Join(site, ".polis"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(site, ".polis", "policies", "nested", "rules.jsonl")
	if err := storage.WriteAtPath(target, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := storage.AppendLineAtPath(filepath.Join(site, ".polis", "logs", "a.log"), []byte("y")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".polis/policies", ".polis/policies/nested", ".polis/logs"} {
		fi, err := os.Stat(filepath.Join(site, rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s created %04o, want 0700", rel, fi.Mode().Perm())
		}
	}

	// Outside .polis the default is unchanged: served content must stay readable.
	if err := storage.WriteAtPath(filepath.Join(site, "content", "post", "a.md"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(site, "content", "post"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("content/post created %04o, want 0755", fi.Mode().Perm())
	}
}
