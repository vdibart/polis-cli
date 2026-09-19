package cmd

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/index"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Close-out F15: `polis index` dropped lines it could not parse and reported
// the rest as the whole index.
func TestIndexCommand_ReportsSkippedLines(t *testing.T) {
	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: "https://maya.example", SiteTitle: "maya"}); err != nil {
		t.Fatal(err)
	}
	// `null` parses, and would otherwise be listed as an empty entry (R1-5).
	body := "{\"type\":\"post\",\"path\":\"a.md\"}\ngarbage\n{\"type\":\"post\",\"path\":\"b.md\"}\nnull\n"
	if err := os.WriteFile(index.IndexPath(dir), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	prevDataDir, prevJSON := dataDir, jsonOutput
	t.Cleanup(func() { dataDir, jsonOutput = prevDataDir, prevJSON })
	dataDir, jsonOutput = dir, true

	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	handleIndex(nil)
	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	var got struct {
		Data struct {
			Count        int   `json:"count"`
			Skipped      int   `json:"skipped"`
			SkippedLines []int `json:"skipped_lines"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if got.Data.Count != 2 || got.Data.Skipped != 2 ||
		len(got.Data.SkippedLines) != 2 || got.Data.SkippedLines[0] != 2 || got.Data.SkippedLines[1] != 4 {
		t.Errorf("data = %+v, want count 2, skipped at lines 2 and 4", got.Data)
	}
}
