package metadata

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Signet epic 44 C1 / close-out F15. LoadPublicIndex skips a line it cannot
// parse, so a reader of a half-garbage index sees the parseable half and
// nothing tells it the rest exists. ReadPublicIndex returns the same entries
// AND says which lines it could not read.
func TestReadPublicIndex_CountsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, BundleContentDir, PublicIndexFilename)
	os.MkdirAll(filepath.Dir(path), 0755)
	// ⚠️ `null` is VALID JSON and unmarshals into the struct without error,
	// leaving the zero entry — an index line that becomes an entry with no
	// type and no path. It is not an entry; it is a line we could not use
	// (round 1, R1-5).
	body := `{"type":"post","path":"a.md","published":"2026-01-01T00:00:00Z"}
not json at all

[1,2,3]
{"type":"post","path":"b.md","published":"2026-01-02T00:00:00Z"}
{"type":"post","path":"c.md"
null
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	entries, report, err := ReadPublicIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("entries = %d, want the 2 readable ones", len(entries))
	}
	if report.Skipped != 4 {
		t.Errorf("Skipped = %d, want 4", report.Skipped)
	}
	// 1-based line numbers; the blank line 3 is not a malformed entry.
	if want := []int{2, 4, 6, 7}; !reflect.DeepEqual(report.SkippedLines, want) {
		t.Errorf("SkippedLines = %v, want %v", report.SkippedLines, want)
	}

	// LoadPublicIndex is unchanged for its existing callers.
	legacy, err := LoadPublicIndex(dir)
	if err != nil || len(legacy) != 2 {
		t.Errorf("LoadPublicIndex = %d entries, %v; want 2, nil", len(legacy), err)
	}
}

func TestReadPublicIndex_MissingFileIsEmptyAndClean(t *testing.T) {
	entries, report, err := ReadPublicIndex(t.TempDir())
	if err != nil || len(entries) != 0 || report.Skipped != 0 {
		t.Errorf("missing index: %d entries, report %+v, err %v; want empty, clean, nil", len(entries), report, err)
	}
}
