package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Close-out F15. A content file the rebuild could not READ was dropped with no
// count, where a file it could read but not index (no `published`) was counted.
// Both are "a file the rebuild walked and did not index", and both are said.
func TestRebuild_CountsAnUnreadableFileAsSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 000 file; the unreadable case cannot be constructed")
	}
	dataDir := seedFourTypes(t)
	core := filepath.Join(dataDir, "content", "pub.polis.core")

	locked := filepath.Join(core, "post", "20260105", "locked.md")
	write(t, locked, "---\ntitle: Locked\npublished: 2026-01-05T00:00:00Z\n---\n\nx\n")
	lockedTag := filepath.Join(core, "tag", "locked.json")
	write(t, lockedTag, `{"tag":"locked","created":"2026-01-05T00:00:00Z","current_version":"sha256:eeee"}`)
	for _, p := range []string{locked, lockedTag} {
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(p, 0644)
	}

	result, err := RebuildContentIndex(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Skipped[EntryTypePost]; got != 1 {
		t.Errorf("Skipped[post] = %d, want 1 — the unreadable post was dropped without a count", got)
	}
	if got := result.Skipped[EntryTypeTag]; got != 1 {
		t.Errorf("Skipped[tag] = %d, want 1", got)
	}
}
