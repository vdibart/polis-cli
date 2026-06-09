package server

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
)

// A page containing only own-author entries yields no avatar map and performs
// NO network fetch (identity is suppressed for own posts). This exercises the
// own-author skip without touching the network.
func TestBuildAuthorAvatars_OwnAuthorOnlyReturnsNil(t *testing.T) {
	s := &Server{BaseURL: "https://me.polis.pub"}
	items := []feed.CachedFeedItem{
		{URL: "https://me.polis.pub/posts/20260101/a.md", Type: "post", AuthorDomain: "me.polis.pub"},
		{URL: "https://me.polis.pub/posts/20260102/b.md", Type: "post", AuthorDomain: "me.polis.pub"},
	}
	if got := s.buildAuthorAvatars(items); got != nil {
		t.Errorf("expected nil avatar map for own-author-only page, got %v", got)
	}
}
