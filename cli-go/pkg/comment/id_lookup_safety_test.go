package comment

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoTenants lays out alice and bob side by side on one volume, with a draft, a
// pending comment and a published comment in bob's site, and returns both site
// directories plus the three files that belong to bob.
func twoTenants(t *testing.T) (alice, bob string, bobFiles []string) {
	t.Helper()
	base := t.TempDir()
	alice = filepath.Join(base, "tenants", "alice")
	bob = filepath.Join(base, "tenants", "bob")
	for _, site := range []string{alice, bob} {
		for _, status := range []string{StatusDrafts, StatusPending, StatusDenied} {
			if err := os.MkdirAll(storage.At(site).Path(commentDirKey(status)), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(site, "content", "pub.polis.core", "comment", "20260101"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body := "---\nin_reply_to: https://x.example/posts/20260101/p.md\ncreated_at: 2026-01-01T00:00:00Z\n---\nbob's private words\n"
	bobFiles = []string{
		filepath.Join(storage.At(bob).Path(commentDirKey(StatusDrafts)), "secret.md"),
		filepath.Join(storage.At(bob).Path(commentDirKey(StatusPending)), "secret.md"),
		filepath.Join(bob, "content", "pub.polis.core", "comment", "20260101", "secret.md"),
	}
	for _, f := range bobFiles {
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return alice, bob, bobFiles
}

// traversalTo returns an id that, joined under alice's <status> directory with
// ".md" appended, lands on bob's file of the same status.
func traversalTo(status string) string {
	// alice/.polis/bundles/pub.polis.core/comments/<status>/ is six levels below
	// the tenants directory.
	return strings.Repeat("../", 6) + "bob/.polis/bundles/pub.polis.core/comments/" + status + "/secret"
}

// ⛔ A comment id becomes a path in every function that takes one, not only in
// the two that write. The first fix for the traversing id guarded SaveDraft and
// the pending write and left the lookups open: LoadDraft read another tenant's
// draft, DeleteDraft removed it, and the sign handler turned what LoadDraft read
// into the caller's own published comment.
func TestATraversingIDCannotReadAnotherTenantsFiles(t *testing.T) {
	alice, _, bobFiles := twoTenants(t)

	if d, err := LoadDraft(storage.At(alice), traversalTo(StatusDrafts)); err == nil {
		t.Errorf("LoadDraft read another tenant's draft: %q", d.Content)
	}
	if c, err := GetComment(storage.At(alice), traversalTo(StatusPending), StatusPending); err == nil {
		t.Errorf("GetComment read another tenant's pending comment: %q", c.Content)
	}
	if found, p := FindBlessedComment(storage.At(alice), "../../../../../bob/content/pub.polis.core/comment/20260101/secret"); found {
		t.Errorf("FindBlessedComment resolved outside the site: %s", p)
	}
	for _, f := range bobFiles {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("a lookup disturbed %s: %v", f, err)
		}
	}
}

func TestATraversingIDCannotDeleteOrMoveAnotherTenantsFiles(t *testing.T) {
	alice, _, bobFiles := twoTenants(t)

	if err := DeleteDraft(storage.At(alice), traversalTo(StatusDrafts)); err == nil {
		t.Error("DeleteDraft accepted a traversing id")
	}
	// MoveComment reads the source, writes the destination and REMOVES the source.
	if err := MoveComment(storage.At(alice), traversalTo(StatusPending), StatusPending, StatusDenied); err == nil {
		t.Error("MoveComment accepted a traversing id")
	}
	if err := MoveComment(storage.At(alice), traversalTo(StatusPending), StatusPending, StatusBlessed); err == nil {
		t.Error("MoveComment to blessed accepted a traversing id")
	}
	if err := PublishComment(storage.At(alice), traversalTo(StatusPending)); err == nil {
		t.Error("PublishComment accepted a traversing id")
	}
	if _, err := BeseechComment(storage.At(alice), traversalTo(StatusPending), nil); err == nil {
		t.Error("BeseechComment accepted a traversing id")
	}

	for _, f := range bobFiles {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("another tenant's file is gone or unreadable: %s: %v", f, err)
		}
	}
	// And nothing of bob's was copied into alice's site.
	_ = filepath.Walk(alice, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			t.Errorf("a traversing id produced a file in the caller's site: %s", p)
		}
		return nil
	})
}

// A status is a path segment too. Only the four the package defines are ones.
func TestAnUnknownStatusIsRefused(t *testing.T) {
	alice, _, bobFiles := twoTenants(t)
	// comments/ is five levels below the tenants directory.
	evil := "../../../../../bob/.polis/bundles/pub.polis.core/comments/pending"

	if got, err := ListComments(storage.At(alice), evil); err == nil {
		t.Errorf("ListComments listed another site's comments through a traversing status: %d", len(got))
	}
	if _, err := GetComment(storage.At(alice), "secret", evil); err == nil {
		t.Error("GetComment accepted a traversing status")
	}
	if err := MoveComment(storage.At(alice), "secret", evil, StatusDenied); err == nil {
		t.Error("MoveComment accepted a traversing source status")
	}
	if err := MoveComment(storage.At(alice), "secret", StatusPending, evil); err == nil {
		t.Error("MoveComment accepted a traversing destination status")
	}
	for _, f := range bobFiles {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("another tenant's file is gone: %s: %v", f, err)
		}
	}
}

// ⭐ The lookup rule is deliberately NARROWER than the one SaveDraft applies. A
// comment already on disk must stay reachable: an id holding ".." between other
// characters, or starting with a dot, names a file inside the directory and
// nothing else. Refusing it here would strand a comment that exists.
func TestAnExistingCommentWithAnUnusualIDStaysReachable(t *testing.T) {
	alice, _, _ := twoTenants(t)
	body := "---\nin_reply_to: https://x.example/posts/20260101/p.md\n---\nhello\n"

	for _, id := range []string{"plain-id", "x..y", ".hidden", "bob-post-20260101-2"} {
		p := filepath.Join(storage.At(alice).Path(commentDirKey(StatusDrafts)), id+".md")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDraft(storage.At(alice), id); err != nil {
			t.Errorf("LoadDraft(%q): %v", id, err)
		}
		if err := DeleteDraft(storage.At(alice), id); err != nil {
			t.Errorf("DeleteDraft(%q): %v", id, err)
		}
		if _, err := os.Stat(p); err == nil {
			t.Errorf("DeleteDraft(%q) left the file", id)
		}
	}
}
