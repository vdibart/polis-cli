package cmd

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

// The bug this guards: `polis post` prints a content path
// (content/pub.polis.core/post/…/x.md) and `polis republish` rejected anything
// not prefixed "posts/" — while posts/ holds only rendered .html. Both forms
// failed, so a published post could not be republished at all. The round trip
// below — publish prints a path, republish accepts it — is what nobody tested.
func TestRepublishAcceptsBothPathForms(t *testing.T) {
	b := bundle.DefaultCoreBundle()

	const contentPath = "content/pub.polis.core/post/20260906/for-example.md"
	const mountPath = "posts/20260906/for-example.md"

	if got := b.MountToSourcePath(mountPath); got != contentPath {
		t.Errorf("mount form does not normalise: got %q, want %q", got, contentPath)
	}
	// Already-normalised input must survive untouched, or normalising twice
	// (handleRepublish then handleRepublishComment) would corrupt the path.
	if got := b.MountToSourcePath(contentPath); got != contentPath {
		t.Errorf("content form is not idempotent: got %q, want %q", got, contentPath)
	}
}

func TestContentTypeForPath(t *testing.T) {
	b := bundle.DefaultCoreBundle()

	tests := []struct {
		path string
		want string
	}{
		{"content/pub.polis.core/post/20260906/x.md", "pub.polis.post"},
		{"content/pub.polis.core/comment/20260906/x.md", "pub.polis.comment"},
		// A mount-form path is NOT a content path; dispatch must run after
		// normalisation, never before it.
		{"posts/20260906/x.md", ""},
		{"comments/20260906/x.md", ""},
		{"", ""},
		// Must not match a sibling type whose dir merely shares a prefix.
		{"content/pub.polis.core/posts-archive/x.md", ""},
	}
	// ⚠️ Keys are FULLY QUALIFIED ("pub.polis.comment", not "comment"). The
	// first cut of the dispatch compared against the bare word and so never
	// matched — which would have routed every comment republish into the post
	// path. This test is the only thing that caught it.
	for _, tt := range tests {
		if got := contentTypeForPath(b, tt.path); got != tt.want {
			t.Errorf("contentTypeForPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
