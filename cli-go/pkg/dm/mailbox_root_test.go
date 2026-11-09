package dm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔⛔ A MAILBOX TAKES THE SITE, NOT THE DM DIRECTORY.
//
// NewMailbox appends the DM path itself. Rooting it at the DM dir makes it
// look two levels too deep, and the failure is SILENT: there is simply nothing
// there, so an inbox rebuilds empty and a conversation reads as having no
// messages.
//
// ⚠️ IT SHIPPED TWICE, both times from a mechanical sweep that wrapped each
// caller's old argument when the parameter's MEANING changed from "the DM dir"
// to "the site": once in the webapp's DM handlers (caught by their tests), and
// once in `polis dm decrypt`, which no test covered — it would have reported
// an empty inbox for a full site export.
func TestAMailboxRootedAtTheSiteFindsItsConversations(t *testing.T) {
	site := storage.At(t.TempDir())
	if _, err := NewMailbox(site).AppendSentRaw("bob.example", 0, "Y2lwaGVy", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "", "sent"); err != nil {
		t.Fatal(err)
	}

	entries, err := NewMailbox(site).RebuildInbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a mailbox rooted at the site rebuilt %d inbox entries, want 1", len(entries))
	}

	// ⛔ The bug, demonstrated: the same messages are INVISIBLE to a mailbox
	// rooted at the DM directory.
	wrong, err := NewMailbox(storage.At(site.Path(DMDirKey))).RebuildInbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(wrong) != 0 {
		t.Errorf("a mailbox rooted at the DM dir found %d entries — if this starts passing, "+
			"NewMailbox's contract changed and this test should change with it", len(wrong))
	}
}

// ⛔ And a build guard, because the behavioural test only covers the code it
// calls: no production caller may root a mailbox at a DM directory.
func TestNoCallerRootsAMailboxAtTheDMDir(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	bad := regexp.MustCompile(`NewMailbox\(storage\.At\([^)]*(?:dmDir|DMDir)`)
	scanned := 0
	for _, dir := range []string{
		filepath.Join(root, "cli-go", "pkg"),
		filepath.Join(root, "webapp", "internal"),
	} {
		err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			scanned++
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			for i, line := range strings.Split(string(b), "\n") {
				if bad.MatchString(line) {
					t.Errorf("%s:%d roots a mailbox at the DM dir:\n    %s\n  ⛔ Pass the site; "+
						"NewMailbox appends the DM path itself, and this one would read an "+
						"empty tree.", p, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d files; the walk is wrong", scanned)
	}
}
