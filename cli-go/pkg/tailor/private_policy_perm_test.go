package tailor

import (
	"os"
	"path/filepath"
	"testing"
)

// ⛔ ".polis can not be world-readable under any circumstances" (Vincent,
// 2026-10-01). pkg/site guards that for a fresh init and pkg/medic for a heal.
// Tailor needs its own, because its policy converge WRITES under .polis and is a
// third way a directory there comes into existence.
//
// It matters specifically because of the detach: storage.WriteAtPath roots a plain
// POSIX store at the file's parent, which creates parents at 0755. That is right
// for served content and wrong here, so this path uses a SITE store instead. This
// test is what makes that choice checkable rather than merely commented.
func TestTheConvergedPrivatePolicyStaysPrivate(t *testing.T) {
	siteDir := t.TempDir()

	path := filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")
	if err := writeSiteKey(siteDir, path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("writeSiteKey: %v", err)
	}

	// The file keeps the mode it asked for.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("private policy file = %v, want 0600", got)
	}

	// ⛔ And every parent it created is owner-only, which is the part a plain
	// WriteAtPath would have got wrong.
	for _, dir := range []string{".polis", ".polis/policies"} {
		di, err := os.Stat(filepath.Join(siteDir, dir))
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if perm := di.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s = %v — reachable by group or other; this is the WriteAtPath "+
				"mistake, and the fix is a site store", dir, perm)
		}
	}
}

// The public tree must NOT be tightened by the same change: a post's directory
// has to stay readable or the site stops serving.
func TestTheConvergedPublicPolicyStaysServable(t *testing.T) {
	siteDir := t.TempDir()
	path := filepath.Join(siteDir, "policies", "rules.jsonl")
	if err := writeSiteKey(siteDir, path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("writeSiteKey: %v", err)
	}
	di, err := os.Stat(filepath.Join(siteDir, "policies"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := di.Mode().Perm(); got != 0o755 {
		t.Errorf("public policies dir = %v, want 0755 — served content must stay readable", got)
	}
}

// ⛔ THE CONVERGE PATH ITSELF, not just the helper underneath it.
//
// The two tests above call writeSiteKey directly, so bypassing it — writing with
// storage.WriteAtPath at checks.go:723 or :1278 — would leave this package green.
// A reviewer made exactly that point. This test runs the real check and inspects
// the disk, so it holds whichever way the write is made.
func TestTheConvergeCheckLeavesPolisPrivate(t *testing.T) {
	siteDir := t.TempDir()

	// A site with nothing in it: the converge check has to create both trees.
	res := checkPoliciesPrivate(&runContext{siteDir: siteDir})
	if res.Status == StatusPass {
		t.Fatalf("expected the check to do work on an empty site, got pass: %+v", res)
	}

	priv := filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")
	if _, err := os.Stat(priv); err != nil {
		t.Fatalf("the private policy was not written: %v", err)
	}
	for _, dir := range []string{".polis", ".polis/policies"} {
		di, err := os.Stat(filepath.Join(siteDir, dir))
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if perm := di.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s = %v after the real converge path — reachable by group or other", dir, perm)
		}
	}
	if fi, err := os.Stat(priv); err == nil && fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("the private policy FILE is %v", fi.Mode().Perm())
	}
}
