package storage_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ A recursive List returns WHAT IT COULD READ, plus the first failure.
//
// Both halves matter. The callers this replaced were WalkDir callbacks that did
// `if err != nil { return nil }` — skip that entry, carry on — so the tolerance
// lived in the callback, and the first version of List aborted the whole walk and
// returned nil. One directory at mode 000 under post/ then made a damaged tenant
// look clean: validate said "the post directory holds no files" and exited 0,
// Patrol verified nothing and raised no alert, and the key-leak scan came back
// empty with a planted key beside it.
//
// ⭐ Returning the readable entries AND the error means a caller that ignores the
// error behaves exactly as it did before the detach, while one that checks it can
// say "I could not look at all of this" — which no caller could express before.
func TestRecursiveListReturnsWhatItCouldReadPlusTheError(t *testing.T) {
	root := t.TempDir()
	st := storage.NewPOSIX(root)
	for _, d := range []string{"a", "b", "c"} {
		if err := st.Put(context.Background(), "post/"+d+"/x.md", []byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	countFiles := func(es []storage.Entry) int {
		n := 0
		for _, e := range es {
			if !e.IsDir {
				n++
			}
		}
		return n
	}

	entries, err := st.List(context.Background(), "post", storage.Recursive())
	if err != nil || countFiles(entries) != 3 {
		t.Fatalf("healthy: files=%d err=%v, want 3 and nil", countFiles(entries), err)
	}

	bad := filepath.Join(root, "post", "b")
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Skipf("cannot chmod (running as root?): %v", err)
	}
	defer os.Chmod(bad, 0o755)

	entries, err = st.List(context.Background(), "post", storage.Recursive())
	if got := countFiles(entries); got != 2 {
		t.Errorf("files = %d, want 2 — the readable entries must still come back, or a caller "+
			"that ignores the error reports an empty directory for a damaged one", got)
	}
	if err == nil {
		t.Error("err = nil — a caller that CHECKS must be able to tell it did not see everything")
	}
}

// ⛔ An unreadable ROOT is not partial: there is nothing to be tolerant about, and
// reporting "no entries, no error" would be the original bug.
func TestRecursiveListOnAnUnreadableRootIsAnError(t *testing.T) {
	root := t.TempDir()
	st := storage.NewPOSIX(root)
	if err := st.Put(context.Background(), "post/a/x.md", []byte("x")); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "post")
	if err := os.Chmod(p, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	defer os.Chmod(p, 0o755)

	entries, err := st.List(context.Background(), "post", storage.Recursive())
	if err == nil {
		t.Error("err = nil for an unreadable root")
	}
	if len(entries) != 0 {
		t.Errorf("entries = %d, want 0", len(entries))
	}
	// ⚠️ And it must NOT read as absence: "I may not look" is not "nothing is there".
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("a permission error must not satisfy fs.ErrNotExist — a caller would treat a " +
			"locked directory as an empty one and write over it")
	}
}

// ⛔ PathAbsent means "genuinely not there"; PathExists means "is it usable".
// Conflating them is what made Patrol report healthy policies and a healthy
// storage salt as MISSING, and site/validate report PRIVATE_KEY_MISSING for a key
// that was present but unreadable.
func TestPathAbsentDistinguishesMissingFromUnreadable(t *testing.T) {
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(locked, "secret.json")
	if err := os.WriteFile(inside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Genuinely missing.
	if absent, err := storage.PathAbsent(filepath.Join(root, "nope.json")); !absent || err != nil {
		t.Errorf("missing file: absent=%v err=%v, want true and nil", absent, err)
	}
	// Present and readable.
	if absent, err := storage.PathAbsent(inside); absent || err != nil {
		t.Errorf("present file: absent=%v err=%v, want false and nil", absent, err)
	}

	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	defer os.Chmod(locked, 0o755)

	// ⛔ Present but unreadable is NOT absent — and a caller that discards the
	// error must still get false, which is the whole reason this helper exists.
	absent, err := storage.PathAbsent(inside)
	if absent {
		t.Error("an unreadable path reported as ABSENT; a caller would report a file that " +
			"exists as missing, or write over it")
	}
	if err == nil {
		t.Error("err = nil; the reason must be available to a caller that wants it")
	}
	// And PathExists is the one that may not be used for this question.
	if present, _ := storage.PathExists(inside); present {
		t.Error("PathExists reported an unreadable path as present; if this changed, " +
			"PathAbsent may be redundant")
	}
}

// The error from a partial listing names the ENTRY that could not be read. The
// prefix itself is present and everything else under it was returned, so naming
// the prefix would send a reader to the wrong place.
//
// ⚠️ What this does NOT pin: the same rule for an entry that VANISHES mid-walk,
// which List maps to not-found. That needs a directory to disappear between
// being listed and being read, and no fixture can stage it. A permission error
// carries its own path, so this passes whichever path List hands to the mapping;
// the vanished case is covered by reading posix.go, not by this test.
func TestAPartialListErrorNamesTheEntryNotThePrefix(t *testing.T) {
	root := t.TempDir()
	st := storage.NewPOSIX(root)
	for _, d := range []string{"a", "b"} {
		if err := st.Put(context.Background(), "post/"+d+"/x.md", []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	bad := filepath.Join(root, "post", "b")
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	defer os.Chmod(bad, 0o755)
	if _, err := os.ReadDir(bad); err == nil {
		t.Skip("mode 000 is readable here (running as root?)")
	}

	_, err := st.List(context.Background(), "post", storage.Recursive())
	if err == nil {
		t.Fatal("err = nil")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("err = %q, want it to name %s", err, bad)
	}
}
