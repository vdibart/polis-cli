package tailor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⚠️ Tailor reads index lines up to 1 MiB; Patrol keeps bufio's 64 KiB default
// (the difference is recorded rather than aligned, because aligning would
// change what one of them reports). This pins Tailor's side.
// Tailor reads index.jsonl lines with the SAME bound as Patrol,
// sitecheck.IndexMaxLine (64 KiB), so the two actors agree about every file.
// It used to allow 1 MiB, so a 100 KiB line passed Tailor while Patrol failed it.
func TestCheckIndexEntriesSharesPatrolsLineBound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "content", "pub.polis.core", "index.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"pub.polis.post","path":"p.md","published":"2026-01-15T10:00:00Z","current_version":"sha256:abc","title":"` + strings.Repeat("x", 100*1024) + `"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := checkIndexEntries(&runContext{siteDir: dir, dryRun: true})
	if got.Status != StatusSkip || got.Message != "scan index.jsonl: bufio.Scanner: token too long" {
		t.Errorf("a 100 KiB line, over the shared 64 KiB bound: %+v", got)
	}

	short := `{"type":"pub.polis.post","path":"p.md","published":"2026-01-15T10:00:00Z","current_version":"sha256:abc","title":"` + strings.Repeat("x", 60*1024) + `"}`
	if err := os.WriteFile(path, []byte(short+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got = checkIndexEntries(&runContext{siteDir: dir, dryRun: true})
	if got.Status != StatusPass || got.Message != "all index entries valid" {
		t.Errorf("a valid 60 KiB line, under the bound: %+v", got)
	}
}
