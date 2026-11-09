//go:build windows

package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ⭐ On Windows a rename over a file another process holds open fails, where
// the in-place write this replaced succeeded. The write retries briefly, so a
// reader that closes the file within the backoff does not turn a publish into
// an error.
func TestARenameOverAnOpenFileIsRetried(t *testing.T) {
	target := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.Close()
	}()
	if err := WriteFileAtomic(target, []byte("new"), 0o644, true); err != nil {
		t.Fatalf("the write failed although the reader let go within the backoff: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new" {
		t.Fatalf("got %q, %v; want the new contents", got, err)
	}
}
