package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Unit tests for DataDirStorage in isolation. Existing coverage in
// static_serve_test.go exercises this adapter indirectly through
// serve.ServeTenantPublic, but never tests the three methods directly
// — so behaviors like the silent LoadBundle fallback, error
// propagation, and concurrent access were unobserved.

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// ============================================================================
// StatPublicFile
// ============================================================================

func TestDataDirStorage_StatPublicFile_Existing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "posts", "hi.html"), "<html/>")

	s := NewDataDirStorage(dir)
	info, err := s.StatPublicFile("ignored-handle", "posts/hi.html")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.IsDir() {
		t.Error("expected file, got dir")
	}
	if info.Size() == 0 {
		t.Error("expected non-zero size")
	}
}

func TestDataDirStorage_StatPublicFile_Missing(t *testing.T) {
	s := NewDataDirStorage(t.TempDir())
	_, err := s.StatPublicFile("h", "posts/does-not-exist.html")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist, got: %v", err)
	}
}

// The handle parameter is unused — same path under any handle should
// yield identical results. This pins down the single-tenant contract.
func TestDataDirStorage_StatPublicFile_HandleIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "x")

	s := NewDataDirStorage(dir)
	_, e1 := s.StatPublicFile("alice", "a.txt")
	_, e2 := s.StatPublicFile("bob", "a.txt")
	if (e1 == nil) != (e2 == nil) {
		t.Errorf("handle should be ignored; got errs alice=%v bob=%v", e1, e2)
	}
}

// ============================================================================
// ReadPublicFile
// ============================================================================

func TestDataDirStorage_ReadPublicFile_Existing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "content.md"), "hello world")

	s := NewDataDirStorage(dir)
	got, err := s.ReadPublicFile("h", "content.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("got %q, want %q", string(got), "hello world")
	}
}

func TestDataDirStorage_ReadPublicFile_Missing(t *testing.T) {
	s := NewDataDirStorage(t.TempDir())
	_, err := s.ReadPublicFile("h", "missing.html")
	if err == nil {
		t.Fatal("expected error")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist, got %v", err)
	}
}

func TestDataDirStorage_ReadPublicFile_Empty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "empty.txt"), "")

	s := NewDataDirStorage(dir)
	got, err := s.ReadPublicFile("h", "empty.txt")
	if err != nil {
		t.Fatalf("empty file should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d bytes", len(got))
	}
}

// The adapter itself does NOT block path traversal — that's the
// responsibility of serve.ServeTenantPublic upstream. Pin this down
// so a future refactor that moves the traversal check elsewhere
// doesn't silently introduce a regression.
//
// CAUTION: if this test ever starts failing, the trusted boundary has
// shifted and the upstream handler may no longer be the right place
// for the check.
func TestDataDirStorage_ReadPublicFile_DoesNotBlockTraversal(t *testing.T) {
	dir := t.TempDir()
	// Write a file *outside* DataDir that the traversal would reach.
	outsideDir := t.TempDir() // sibling temp dir, not under `dir`
	writeFile(t, filepath.Join(outsideDir, "secret.txt"), "leaked")

	s := NewDataDirStorage(dir)
	// Compute a relative path that climbs out of DataDir into outsideDir.
	relTraversal, err := filepath.Rel(dir, filepath.Join(outsideDir, "secret.txt"))
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	got, err := s.ReadPublicFile("h", relTraversal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "leaked" {
		t.Errorf("expected adapter to read traversed file (no guard at this layer); got %q", string(got))
	}
}

// ============================================================================
// LoadBundle — the silent-fallback behavior was previously unobserved
// ============================================================================

func TestDataDirStorage_LoadBundle_ReadsBundleJSON(t *testing.T) {
	dir := t.TempDir()
	custom := map[string]interface{}{
		"name":    "pub.polis.core",
		"version": "9.9.9-test",
		"handler": map[string]interface{}{"type": "builtin"},
		"types":   map[string]interface{}{},
	}
	data, _ := json.Marshal(custom)
	writeFile(t, filepath.Join(dir, "content", "pub.polis.core", "bundle.json"), string(data))

	s := NewDataDirStorage(dir)
	b := s.LoadBundle("h")
	if b == nil {
		t.Fatal("LoadBundle returned nil")
	}
	if b.Version != "9.9.9-test" {
		t.Errorf("expected disk version 9.9.9-test, got %q (fallback to default?)", b.Version)
	}
}

// The fallback path is the riskiest behavior in this adapter — it
// silently substitutes the default bundle on any LoadBundle error.
// Lock down both cases (missing file, malformed JSON) so a future
// refactor that "helpfully" logs or fails noisily can be reviewed
// against this expectation.
func TestDataDirStorage_LoadBundle_MissingFile_FallsBackToDefault(t *testing.T) {
	s := NewDataDirStorage(t.TempDir())
	b := s.LoadBundle("h")
	if b == nil {
		t.Fatal("LoadBundle returned nil; expected default bundle")
	}
	if b.Name != "pub.polis.core" {
		t.Errorf("default bundle should be pub.polis.core, got %q", b.Name)
	}
}

func TestDataDirStorage_LoadBundle_MalformedJSON_FallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "content", "pub.polis.core", "bundle.json"), "{not valid json")

	s := NewDataDirStorage(dir)
	b := s.LoadBundle("h")
	if b == nil {
		t.Fatal("LoadBundle returned nil")
	}
	// We hit the fallback, so it should be the default bundle (not the
	// malformed disk content). The default's Name is "pub.polis.core".
	if b.Name != "pub.polis.core" {
		t.Errorf("expected fallback to default bundle, got Name=%q", b.Name)
	}
}

// ============================================================================
// Concurrency — confirm the adapter is safe under concurrent reads.
// No shared mutable state, but pin the contract so a future addition
// of e.g. an internal cache doesn't quietly break it.
// ============================================================================

func TestDataDirStorage_ConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shared.txt"), "shared content")

	s := NewDataDirStorage(dir)

	var wg sync.WaitGroup
	const goroutines = 32
	errs := make(chan error, goroutines*2)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.StatPublicFile("h", "shared.txt"); err != nil {
				errs <- err
			}
			if got, err := s.ReadPublicFile("h", "shared.txt"); err != nil {
				errs <- err
			} else if string(got) != "shared content" {
				errs <- &readMismatch{got: string(got)}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent read failure: %v", err)
	}
}

type readMismatch struct{ got string }

func (m *readMismatch) Error() string { return "unexpected read result: " + m.got }

// ============================================================================
// NewDataDirStorage — constructor sanity
// ============================================================================

func TestNewDataDirStorage_PreservesPath(t *testing.T) {
	s := NewDataDirStorage("/some/path")
	if s.DataDir != "/some/path" {
		t.Errorf("DataDir = %q, want /some/path", s.DataDir)
	}
}

func TestNewDataDirStorage_EmptyDirIsValid(t *testing.T) {
	// Constructor doesn't validate path existence; callers control
	// this. A non-existent DataDir produces errors at call time, not
	// at construction time.
	s := NewDataDirStorage("/definitely/does/not/exist/anywhere")
	_, err := s.ReadPublicFile("h", "any.txt")
	if err == nil {
		t.Fatal("expected error reading from nonexistent DataDir")
	}
	if !strings.Contains(err.Error(), "no such file") && !os.IsNotExist(err) {
		t.Errorf("expected not-found-style error, got: %v", err)
	}
}
