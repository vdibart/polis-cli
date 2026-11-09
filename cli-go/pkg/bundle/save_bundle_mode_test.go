package bundle

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// ⛔ bundle.json is SERVED CONTENT: every caller writes it to
// content/pub.polis.core/bundle.json. A site whose web server runs as another
// user cannot read it out of a 0700 directory, so the directories SaveBundle
// creates must be traversable by everyone. They were briefly 0700, on the
// mistaken belief that a bundle lives under .polis.
func TestSaveBundleCreatesServableDirectories(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)

	site := t.TempDir()
	path := filepath.Join(site, "content", "pub.polis.core", "bundle.json")
	if err := SaveBundle(path, DefaultCoreBundle()); err != nil {
		t.Fatalf("SaveBundle: %v", err)
	}
	for _, dir := range []string{filepath.Join(site, "content"), filepath.Join(site, "content", "pub.polis.core")} {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o755 {
			t.Errorf("%s is %04o, want 0755", dir, got)
		}
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("bundle.json: %v, mode %v, want 0644", err, fi.Mode().Perm())
	}
}
