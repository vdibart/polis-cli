package render

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// A reply-context cache that cannot be read or decoded starts empty (and says
// so) instead of failing a render or silently keeping half-decoded entries.
func TestReplyContextCacheUnreadableStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, replyContextCachePath)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"https://a.example/p.html": {"title": "x"}, "broken`), 0644); err != nil {
		t.Fatal(err)
	}
	if c := loadReplyContextCache(storage.At(dir)); len(c) != 0 {
		t.Errorf("an undecodable cache loaded %d entries, want none", len(c))
	}
}

// A cache that cannot be written is logged, never fatal.
func TestReplyContextCacheUnwritableIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	// A FILE where the cache's parent directory should be.
	blocker := filepath.Join(dir, filepath.Dir(replyContextCachePath))
	if err := os.MkdirAll(filepath.Dir(blocker), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("not a dir"), 0644); err != nil {
		t.Fatal(err)
	}
	saveReplyContextCache(storage.At(dir), replyContextCache{})
}

// A missing cache is the ordinary first-render case: it starts empty and says
// nothing, unlike a cache that exists and cannot be read.
func TestReplyContextCacheAbsentStartsEmptyAndQuiet(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	if c := loadReplyContextCache(storage.At(t.TempDir())); len(c) != 0 {
		t.Fatalf("an absent cache loaded %d entries, want 0", len(c))
	}
	if buf.Len() != 0 {
		t.Fatalf("an absent cache logged %q; absence is not a fault", buf.String())
	}
}
