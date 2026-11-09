package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func appendStore(t *testing.T) Store {
	t.Helper()
	return NewSite(t.TempDir())
}

func TestAppendCreatesThenExtends(t *testing.T) {
	ctx := context.Background()
	s := appendStore(t)

	if err := AppendLine(ctx, s, "content/index.jsonl", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := AppendLine(ctx, s, "content/index.jsonl", []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "content/index.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\"a\":1}\n{\"a\":2}\n" {
		t.Errorf("got %q", got)
	}
}

// ⛔⛔ THE BUG THIS EXISTS TO CLOSE. Without the key lock, two concurrent
// read-modify-writes interleave and one is lost — on the public index that
// means a published post no reader can discover, silently.
func TestConcurrentAppendsLoseNothing(t *testing.T) {
	ctx := context.Background()
	s := appendStore(t)
	const n = 50

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := AppendLine(ctx, s, "index.jsonl", []byte(fmt.Sprintf("line-%02d", i))); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := s.Get(ctx, "index.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("%d lines survived of %d — appends are interleaving", len(lines), n)
	}
	seen := map[string]bool{}
	for _, l := range lines {
		seen[l] = true
	}
	for i := 0; i < n; i++ {
		if !seen[fmt.Sprintf("line-%02d", i)] {
			t.Errorf("line-%02d was lost", i)
		}
	}
}

// ⭐⭐⭐ THE ONE THAT MATTERS MOST, and the reason the lock is per KEY rather
// than per append: the writer that clobbers an append is a WHOLE-FILE REBUILD,
// which is what Medic does to the public index on every sweep. Serialising
// appends against each other would have fixed nothing.
func TestAWholeFileRewriteCannotClobberAConcurrentAppend(t *testing.T) {
	ctx := context.Background()
	s := appendStore(t)
	const key = "index.jsonl"

	if err := AppendLine(ctx, s, key, []byte("original")); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// A publisher appending.
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			_ = AppendLine(ctx, s, key, []byte(fmt.Sprintf("published-%02d", i)))
		}
	}()

	// Medic rebuilding the whole file — taking the SAME lock.
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			_ = WithKeyLock(s, key, func() error {
				existing, err := s.Get(ctx, key)
				if err != nil {
					return err
				}
				// A rebuild reconstructs from what it read: same content back.
				return s.Put(ctx, key, existing)
			})
		}
	}()
	wg.Wait()

	got, _ := s.Get(ctx, key)
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(lines) != 26 {
		t.Fatalf("%d lines survived of 26 — a rebuild clobbered an append", len(lines))
	}
}

// ⛔ A read failure is not an empty file. Treating it as one silently truncates:
// the append would write only the new data and everything before it would be
// gone.
func TestAppendRefusesWhenItCannotReadWhatIsThere(t *testing.T) {
	ctx := context.Background()
	if err := Append(ctx, nil, "k", []byte("x")); err == nil {
		t.Error("appending to a nil store succeeded")
	}

	s := appendStore(t)
	// A key whose parent is a FILE: the read fails with something other than
	// not-found, and the append must refuse rather than overwrite.
	if err := s.Put(ctx, "blocked", []byte("i am a file")); err != nil {
		t.Fatal(err)
	}
	if err := AppendLine(ctx, s, "blocked/child.jsonl", []byte("x")); err == nil {
		t.Error("appended through a file; a read failure was treated as absence")
	}
}

// ⭐ Appending to an absent key creates it — the O_CREATE half of what
// O_APPEND|O_CREATE did.
func TestAppendToAnAbsentKeyCreatesIt(t *testing.T) {
	ctx := context.Background()
	s := appendStore(t)
	if err := AppendLine(ctx, s, "new/file.jsonl", []byte("first")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "new/file.jsonl")
	if err != nil || string(got) != "first\n" {
		t.Errorf("got %q, %v", got, err)
	}
}

// ⚠️ Matches O_APPEND: no separator is inserted, so a file not ending in a
// newline gains a joined line. Stated as a test so nobody "fixes" it later and
// changes what existing files look like.
func TestAppendInsertsNoSeparatorOfItsOwn(t *testing.T) {
	ctx := context.Background()
	s := appendStore(t)
	if err := s.Put(ctx, "k", []byte("no-trailing-newline")); err != nil {
		t.Fatal(err)
	}
	if err := Append(ctx, s, "k", []byte("MORE")); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "k")
	if string(got) != "no-trailing-newlineMORE" {
		t.Errorf("got %q", got)
	}
}

// ⚠️ Different keys must not serialise against each other, or an append to a
// DM mailbox would block one to the index.
func TestDifferentKeysDoNotBlockEachOther(t *testing.T) {
	s := appendStore(t)
	done := make(chan struct{})
	_ = WithKeyLock(s, "a", func() error {
		go func() {
			_ = WithKeyLock(s, "b", func() error { close(done); return nil })
		}()
		<-done // ⛔ deadlocks if the lock is per store rather than per key
		return nil
	})
}

// ⛔⛔ THE TWO LOCK FORMS MUST RESOLVE TO THE SAME MUTEX FOR THE SAME FILE.
//
// ⚠️ CAUGHT BY PROBING, NOT BY REASONING: they did not agree. During the
// site-handle migration a package converts to the store-addressed form while
// its neighbours still use the path-addressed one — the public index is
// written by `metadata` and rebuilt by `index` — and two different mutexes
// would mean the lock excludes nothing while still looking present. The race
// it was built to fix would come back silently, mid-migration.
func TestTheStoreAndPathLockFormsAgree(t *testing.T) {
	dir := t.TempDir()
	s := NewSite(dir)
	const key = "content/pub.polis.core/index.jsonl"
	abs := filepath.Join(dir, key)

	// Hold the path-addressed lock; the store-addressed one must block.
	blocked := make(chan struct{})
	released := make(chan struct{})
	go func() {
		_ = WithPathLock(abs, func() error {
			close(blocked)
			<-released
			return nil
		})
	}()
	<-blocked

	got := make(chan struct{})
	go func() {
		_ = WithKeyLock(s, key, func() error { close(got); return nil })
	}()

	select {
	case <-got:
		t.Fatal("the store-addressed lock did not exclude the path-addressed one; " +
			"they resolve to different mutexes and the lock protects nothing")
	case <-time.After(100 * time.Millisecond):
		// correct: blocked
	}
	close(released)
	<-got
}
