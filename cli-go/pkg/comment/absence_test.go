package comment

import (
	"context"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Republishing a comment that does not exist fails and creates nothing.
func TestRepublishCommentOnAMissingCommentIsAnError(t *testing.T) {
	site := storage.At(t.TempDir())
	key := "content/pub.polis.core/comment/20260101/missing.md"

	if _, err := RepublishComment(site, key, "hello", nil); err == nil {
		t.Fatal("RepublishComment on a missing comment succeeded")
	}
	if ok, _ := site.Store().Exists(context.Background(), key); ok {
		t.Fatal("RepublishComment created the comment it was asked to update")
	}
}

// Asking whether a missing comment was edited since blessing is an error, not
// a "no": the caller asked about a file that is not there.
func TestIsEditedSinceBlessingOnAMissingCommentIsAnError(t *testing.T) {
	site := storage.At(t.TempDir())
	if _, err := IsEditedSinceBlessing(site, "content/pub.polis.core/comment/20260101/missing.md"); err == nil {
		t.Fatal("IsEditedSinceBlessing on a missing comment returned no error")
	}
}
