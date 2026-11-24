package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// polis render replaces an edited shipped file with polis's copy; the edit is
// kept first, privately, and nothing is kept when nothing differs.
func TestRenderKeepsAnEditedShippedFileBeforeReplacingIt(t *testing.T) {
	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{BaseURL: "https://alice.example", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	if kept, _, err := keepDriftedPayload(dir, time.Now()); err != nil || len(kept) != 0 {
		t.Fatalf("fresh site: kept %v, %v", kept, err)
	}

	rel := filepath.Join(".polis", "bundles", "pub.polis.core", "themes", "vice", "vice.css")
	edited := []byte("/* my edit */\n")
	if err := os.WriteFile(filepath.Join(dir, rel), edited, 0o644); err != nil {
		t.Fatal(err)
	}
	kept, keptDir, err := keepDriftedPayload(dir, time.Date(2026, 11, 15, 1, 2, 3, 0, time.UTC))
	if err != nil || len(kept) != 1 {
		t.Fatalf("kept %v, %v", kept, err)
	}
	copyPath := filepath.Join(dir, filepath.FromSlash(keptDir), rel)
	got, err := os.ReadFile(copyPath)
	if err != nil || !bytes.Equal(got, edited) {
		t.Fatalf("kept copy: %v %q", err, got)
	}
	if info, _ := os.Stat(copyPath); info.Mode().Perm() != 0o600 {
		t.Errorf("kept copy mode %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Join(dir, filepath.FromSlash(keptDir))); info.Mode().Perm() != 0o700 {
		t.Errorf("kept dir mode %v", info.Mode().Perm())
	}
}
