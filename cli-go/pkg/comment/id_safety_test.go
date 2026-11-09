package comment

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ A traversing comment id used to write into ANOTHER TENANT'S site directory on
// the shared hosted volume. Reproduced before the fix: an id of six "../"
// segments plus "bob/pwned" produced tenants/bob/pwned.md.
//
// ⚠️ The detach did not introduce the traversal — os.WriteFile could already land
// in an existing directory — but it made it materially worse, because
// storage.WriteAtPath CREATES MISSING PARENTS, so the id can build the tree it
// needs on the way out.
func TestATraversingDraftIDCannotEscapeTheSite(t *testing.T) {
	for _, id := range []string{
		strings.Repeat("../", 6) + "bob/pwned",
		strings.Repeat("../", 4) + "bob/newdir/evil",
		"../escape",
		"sub/dir/evil",
		`..\windows`,
		".hidden",
		"",
	} {
		base := t.TempDir()
		alice := filepath.Join(base, "tenants", "alice")
		if err := os.MkdirAll(alice, 0o755); err != nil {
			t.Fatal(err)
		}

		err := SaveDraft(storage.At(alice), &CommentDraft{ID: id, Content: "x", InReplyTo: "https://x/p.md"})

		// An empty id is legitimate: SaveDraft generates one.
		if id == "" {
			if err != nil {
				t.Errorf("an empty id must be generated, not refused: %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("id %q was accepted", id)
		}

		var outside []string
		_ = filepath.Walk(base, func(p string, fi os.FileInfo, e error) error {
			if e != nil || fi.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(base, p)
			if !strings.HasPrefix(rel, filepath.Join("tenants", "alice")+string(filepath.Separator)) {
				outside = append(outside, rel)
			}
			return nil
		})
		if len(outside) > 0 {
			t.Errorf("id %q wrote outside the site: %v", id, outside)
		}
	}
}

// A normal id still works, so the guard is not simply refusing everything.
func TestAnOrdinaryDraftIDStillSaves(t *testing.T) {
	dir := t.TempDir()
	if err := SaveDraft(storage.At(dir), &CommentDraft{ID: "20260101-abc123", Content: "x", InReplyTo: "https://x/p.md"}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments", "drafts", "20260101-abc123.md")); err != nil {
		t.Errorf("draft not written where expected: %v", err)
	}
}
