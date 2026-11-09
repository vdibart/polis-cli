package url

import "testing"

// ⛔⛔ A COMMENT URL FROM ANOTHER SITE MUST NEVER BECOME A KEY OUTSIDE THE
// COMMENT TREE. Both inputs below were proven by the security scan to read a
// tenant's draft through the previous version.
func TestATraversalCommentURLYieldsNoKey(t *testing.T) {
	for _, u := range []string{
		"https://evil.example/content/pub.polis.core/comment/../../../.polis/bundles/pub.polis.core/comments/drafts/secret.md",
		"https://evil.example/x.md?q=/comments/x/../../../../.polis/bundles/pub.polis.core/comments/drafts/secret.md",
		"https://evil.example/comments/%2e%2e/%2e%2e/.polis/x.md",
		"https://evil.example/comments/a//b.md",
		"https://evil.example/comments/./b.md",
		`https://evil.example/comments/a\..\b.md`,
		"https://evil.example/comments/#/../../.polis/x.md",
		"https://evil.example/comments/.md",
	} {
		if k := CommentURLToContentRel(u); k != "" {
			t.Errorf("%s → %q; want no key", u, k)
		}
	}
}

// ⭐ Ordinary comment URLs, both shapes, still map.
func TestOrdinaryCommentURLsStillMap(t *testing.T) {
	for u, want := range map[string]string{
		"https://a.example/content/pub.polis.core/comment/20260101/abc.md": "content/pub.polis.core/comment/20260101/abc.md",
		"https://a.example/comments/20260101/abc.html":                     "content/pub.polis.core/comment/20260101/abc.md",
		"https://a.example/comments/20260101/abc.md?ref=feed#top":          "content/pub.polis.core/comment/20260101/abc.md",
	} {
		if got := CommentURLToContentRel(u); got != want {
			t.Errorf("%s → %q; want %q", u, got, want)
		}
	}
}
