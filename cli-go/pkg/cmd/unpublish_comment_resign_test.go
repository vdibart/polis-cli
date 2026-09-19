package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// `polis unpublish` on a comment promises the draft can be republished. Before
// close-out E2 the draft kept no reply fields, so `polis comment sign` refused
// it with "in_reply_to is required".
func TestRunUnpublish_CommentDraftCanBeSignedAgain(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	commentPath := createTestComment(t, dir, "20260201", "comment-a", "Re: Some Post")
	if err := RunUnpublish(dir, commentPath, true); err != nil {
		t.Fatalf("RunUnpublish: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments", "drafts", "comment-a.md")); err != nil {
		t.Fatalf("expected the draft: %v", err)
	}
	draft, err := comment.LoadDraft(dir, "comment-a")
	if err != nil {
		t.Fatalf("LoadDraft: %v", err)
	}
	if draft.InReplyTo != "https://target.com/posts/post.md" {
		t.Errorf("in_reply_to = %q", draft.InReplyTo)
	}
	if draft.RootPost != "https://target.com/posts/post.md" {
		t.Errorf("root_post = %q", draft.RootPost)
	}

	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := comment.SignComment(dir, draft, "test.example.com", "https://example.com", priv); err != nil {
		t.Fatalf("comment sign after unpublish: %v", err)
	}
}
