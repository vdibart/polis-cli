package branch

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An abandoned staging directory (it held keys) is removed by the next run;
// one young enough to belong to a run in progress is left alone.
func TestRemoveStaleStagingSparesARunInProgress(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, stagingPrefix+"old")
	young := filepath.Join(dir, stagingPrefix+"young")
	other := filepath.Join(dir, "keep-me")
	for _, d := range []string{old, young, other} {
		if err := os.MkdirAll(filepath.Join(d, "in"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * staleStagingAge)
	for _, d := range []string{old, other} {
		if err := os.Chtimes(d, past, past); err != nil {
			t.Fatal(err)
		}
	}
	removeStaleStaging(dir)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("an abandoned staging directory survived")
	}
	for _, d := range []string{young, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s was removed", filepath.Base(d))
		}
	}
}

// ⛔ The cap counts bytes written, so an archive cannot unpack past it.
func TestUnzipRefusesAnExportPastTheCap(t *testing.T) {
	defer func(v int64) { maxExportBytes = v }(maxExportBytes)
	maxExportBytes = 1000
	archive := filepath.Join(t.TempDir(), "big.zip")
	f, _ := os.Create(archive)
	zw := zip.NewWriter(f)
	for _, name := range []string{"a.md", "b.md"} {
		w, _ := zw.Create(name)
		w.Write([]byte(strings.Repeat("x", 600)))
	}
	zw.Close()
	f.Close()
	err := unzip(archive, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "more than 1000 bytes") {
		t.Fatalf("err = %v, want the cap", err)
	}
}
