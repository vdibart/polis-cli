package metadata

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Unblessing on a site that has never blessed anything is nothing to do, not
// a failure: there is no list for the comment to be in.
func TestUnblessWithNoBlessedListIsNothingToDo(t *testing.T) {
	site := storage.At(t.TempDir())
	if err := RemoveBlessedCommentSigned(site, "https://alice.example/comments/20261113/hi.md", nil); err != nil {
		t.Errorf("unbless with no list: %v", err)
	}
}
