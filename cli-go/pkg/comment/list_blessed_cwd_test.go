package comment

import (
	"context"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Blessed comments are found through the site, wherever the process runs.
//
// ⛔ Regression: the listing built each date directory as a site-relative key
// and handed it to path-addressed helpers, which resolved it against the
// working directory. From the site root it worked by accident; from anywhere
// else (`polis --data-dir`) every blessed comment vanished from the list.
func TestListBlessedCommentsDoesNotDependOnTheWorkingDirectory(t *testing.T) {
	site := storage.At(t.TempDir())
	key := "content/pub.polis.core/comment/20260101/c-1.md"
	body := "---\ntitle: One\npublished: 2026-01-01T00:00:00Z\nin_reply_to: https://a.example/posts/p.md\n---\n\nHello\n"
	if err := site.Store().Put(context.Background(), key, []byte(body)); err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir()) // anywhere but the site

	got, err := ListComments(site, StatusBlessed)
	if err != nil {
		t.Fatalf("ListComments(blessed): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListComments(blessed) found %d comments, want 1", len(got))
	}
}

// A site with no blessed comments lists none, and that is not an error.
func TestListBlessedCommentsOnAnEmptySiteIsEmpty(t *testing.T) {
	got, err := ListComments(storage.At(t.TempDir()), StatusBlessed)
	if err != nil {
		t.Fatalf("ListComments(blessed) on an empty site: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListComments(blessed) on an empty site found %d", len(got))
	}
}
