package snippet

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"testing"
)

// The CLI's copy of a theme's snippets stands in only when the site has NO local
// copy. A local theme directory that is there but cannot be read is not replaced.
func TestThemeSnippetsFallBackOnlyWhenTheLocalDirectoryIsAbsent(t *testing.T) {
	cli := t.TempDir()
	cliSnippet := filepath.Join(cli, "sols", "snippets", "from-cli.html")
	if err := os.MkdirAll(filepath.Dir(cliSnippet), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cliSnippet, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	listed := func(site string) bool {
		tree, err := ListSnippets(storage.At(site), cli, "sols", "", "theme")
		if err != nil {
			t.Fatalf("ListSnippets: %v", err)
		}
		for _, e := range tree.Entries {
			if e.Name == "from-cli.html" {
				return true
			}
		}
		return false
	}

	absent := t.TempDir()
	if !listed(absent) {
		t.Error("no local theme directory: the CLI's snippet was not listed")
	}

	// A FILE where the theme directory belongs: the stat fails, but not with
	// not-exist, so this is not an absence.
	blocked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(blocked, "site", "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "site", "themes", "sols"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if listed(blocked) {
		t.Error("an unreachable local theme directory was replaced by the CLI's copy")
	}
}
