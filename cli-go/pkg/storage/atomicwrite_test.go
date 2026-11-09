package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ⛔⛔ A READER NEVER SEES AN EMPTY OR PARTIAL FILE while appends land. Append
// is read-concatenate-Put, so this is the Put path: before the fix it was
// os.WriteFile, which truncates then writes, and a probe saw thousands of short
// reads. Every read must be a whole prefix of complete lines.
func TestAReaderNeverSeesAPartialFileDuringAppends(t *testing.T) {
	dir := t.TempDir()
	s := NewSite(dir)
	ctx := context.Background()
	if err := s.Put(ctx, "content/index.jsonl", []byte("seed\n")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "content/index.jsonl")
	var stop atomic.Bool
	var bad atomic.Int32
	var reads atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			b, err := os.ReadFile(path)
			if err != nil {
				bad.Add(1)
				continue
			}
			reads.Add(1)
			if len(b) == 0 || !bytes.HasPrefix(b, []byte("seed\n")) || b[len(b)-1] != '\n' {
				bad.Add(1)
			}
		}
	}()
	for i := 0; i < 300; i++ {
		if err := Append(ctx, s, "content/index.jsonl", []byte(fmt.Sprintf(`{"n":%d,"pad":"%s"}`+"\n", i, strings.Repeat("x", 200)))); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if reads.Load() == 0 {
		t.Fatal("the reader never ran; the test proves nothing")
	}
	if n := bad.Load(); n > 0 {
		t.Errorf("%d of %d reads saw a missing, empty or partial file during appends", n, reads.Load())
	}
}

// ⛔ Two concurrent writers to one key never share a temp file, so the target
// is always exactly one writer's bytes.
func TestConcurrentAtomicWritersNeverTearTheTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	const n = 16
	payload := func(i int) []byte { return bytes.Repeat([]byte{byte('a' + i)}, 64*1024) }
	var wg sync.WaitGroup
	for round := 0; round < 20; round++ {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if err := WriteFileAtomic(path, payload(i), 0o644, false); err != nil {
					t.Error(err)
				}
			}(i)
		}
		wg.Wait()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) != 64*1024 || bytes.Count(b, b[:1]) != len(b) {
			t.Fatalf("round %d: the target is torn (%d bytes, mixed writers)", round, len(b))
		}
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".f.json.tmp-*"))
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// ⭐ A Put keeps an existing file's mode, as os.WriteFile did; a symlinked
// target is written through, not replaced.
func TestAPutKeepsAnExistingModeAndWritesThroughALink(t *testing.T) {
	dir := t.TempDir()
	s := NewSite(dir)
	ctx := context.Background()
	p := filepath.Join(dir, "content/a.txt")
	_ = s.Put(ctx, "content/a.txt", []byte("1"))
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "content/a.txt", []byte("2"), WithMode(0o644)); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("an existing file's mode changed to %o; a Put must keep it", fi.Mode().Perm())
	}
	target := filepath.Join(dir, "content/real.txt")
	_ = os.WriteFile(target, []byte("old"), 0o644)
	if err := os.Symlink(target, filepath.Join(dir, "content/link.txt")); err != nil {
		t.Skip("no symlinks here")
	}
	if err := s.Put(ctx, "content/link.txt", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(filepath.Join(dir, "content/link.txt")); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("a Put replaced a symlink with a regular file")
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Errorf("the link's target reads %q; a Put must write through the link", b)
	}
}

// ⚠️ In a directory that cannot be written, an existing writable file is still
// updated, as the in-place write did; the differential's read-only steps
// caught the temp-and-rename version failing there.
func TestAPutInAReadOnlyDirectoryStillUpdatesAWritableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	s := NewSite(dir)
	ctx := context.Background()
	if err := s.Put(ctx, "content/a.md", []byte("1")); err != nil {
		t.Fatal(err)
	}
	cdir := filepath.Join(dir, "content")
	if err := os.Chmod(cdir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(cdir, 0o755)
	if err := s.Put(ctx, "content/a.md", []byte("2")); err != nil {
		t.Fatalf("updating a writable file in a read-only directory failed: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(cdir, "a.md")); string(b) != "2" {
		t.Errorf("read %q, want 2", b)
	}
}
