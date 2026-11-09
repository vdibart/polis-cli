package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// ⛔ republish names a file it will rewrite into a signed comment, so the path
// is held to the editor's rule and must be a comment.
func TestCommentUpdateRefusesPathsOutsideComments(t *testing.T) {
	engine, siteDir := newTestEngine(t)
	victim := filepath.Join(siteDir, "index.md")
	_ = os.WriteFile(victim, []byte("---\npublished: 2026-01-01T00:00:00Z\n---\nhome"), 0o644)
	for _, p := range []string{
		"content/pub.polis.core/comment/../../../index.md",
		"content/pub.polis.core/comment/../post/x.md",
		"../outside.md",
		"index.md",
		".polis/keys/id_ed25519",
	} {
		_, err := engine.Dispatch(context.Background(), ActionRequest{
			Action: "update", ContentType: "pub.polis.comment",
			Payload: map[string]any{"path": p, "markdown": "pwned"},
		})
		if err == nil {
			t.Errorf("update with path %q succeeded", p)
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != "---\npublished: 2026-01-01T00:00:00Z\n---\nhome" {
		t.Errorf("index.md was rewritten: %q", b)
	}
}

// ⛔ Tag names are normalised; one that does not normalise is refused rather
// than read as a path.
func TestTagListAndDeleteRefuseTraversal(t *testing.T) {
	engine, _ := newTestEngine(t)
	// ⚠️ Only ".." is a refusal. "../x" and "a/../../b" normalise to the plain
	// names "x" and "ab" before anything is addressed, so they cannot traverse
	// either, but they are not refused: they name an ordinary tag.
	for _, action := range []string{"list", "delete"} {
		for _, name := range []string{".."} {
			_, err := engine.Dispatch(context.Background(), ActionRequest{
				Action: action, ContentType: "pub.polis.tag",
				Payload: map[string]any{"tag": name},
			})
			if err == nil {
				t.Errorf("tag %s %q succeeded", action, name)
			}
		}
	}
}
