package server

import (
	"context"
	"strings"
	"testing"

	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// ⭐ The fix must not stop a site showing its OWN comment's local body.
func TestALocalCommentBodyIsStillReadForTheSitesOwnDomain(t *testing.T) {
	s := newConfiguredServer(t)
	own := polisurl.ExtractDomain(s.GetBaseURL())
	if own == "" {
		t.Skip("test server has no base URL")
	}
	_ = s.Site().Store().Put(context.Background(), "content/pub.polis.core/comment/20260101/mine.md", []byte("---\n---\nmy own comment\n"))
	if got := readLocalCommentBody(s.Site(), own, "https://"+own+"/content/pub.polis.core/comment/20260101/mine.md"); !strings.Contains(got, "my own comment") {
		t.Errorf("own comment body not read: %q", got)
	}
	if got := readLocalCommentBody(s.Site(), own, "https://other.example/content/pub.polis.core/comment/20260101/mine.md"); got != "" {
		t.Errorf("a foreign URL read a local body: %q", got)
	}
	// ⛔ A site-relative URL is treated as the site's own, so it must still be
	// unable to walk out of the comment tree.
	_ = s.Site().Store().Put(context.Background(), ".polis/x.md", []byte("PRIVATE"))
	for _, rel := range []string{"content/pub.polis.core/comment/../../../.polis/x.md", "/comments/../.polis/x.md"} {
		if got := readLocalCommentBody(s.Site(), own, rel); got != "" {
			t.Errorf("a relative traversal URL %q read %q", rel, got)
		}
	}
	if got := readLocalCommentBody(s.Site(), own, "/content/pub.polis.core/comment/20260101/mine.md"); !strings.Contains(got, "my own comment") {
		t.Errorf("a site-relative own comment was not read: %q", got)
	}
}
