package license

import (
	"strings"
	"testing"
)

// ⭐⭐⭐ A QUESTION ANSWERED BY MEASUREMENT: can a site whose
// pages are served by a Vercel REWRITE still express per-resource terms?
//
// ⛔ A rewrite cannot set the headers. The per-resource Content-Usage and Link
// headers are derived FROM THE BYTES BEING SERVED — read out of a post's signed
// frontmatter, or out of the head the renderer generated from it — and a rewrite
// is a static URL mapping that never sees a response body. A function could set
// them, but costs an invocation per request, which is the thing the Blob
// decision exists to avoid.
//
// ⭐ The design anticipated exactly this. robots.txt carries PER-PATH
// Content-Usage rules, so a post that overrides the site default is expressible
// in a FILE — put there because "a self-hoster on S3 or GitHub Pages cannot set
// response headers, so if per-resource terms only lived in Content-Usage: they
// would be unavailable to exactly the people polis exists for."
//
// ⭐⭐ A rewrite-served blob site IS that self-hoster case. This test is the
// evidence that nothing is lost: an overriding post's terms survive in the file
// a crawler reads.
func TestAnOverridingPostsTermsSurviveWithoutHeaders(t *testing.T) {
	siteDefault := &Terms{TrainAI: "n", Search: "y"}
	openPost := &Terms{TrainAI: "y", Search: "y"}

	// The site-wide rule, path-less.
	siteRule := RobotsRule("", siteDefault)
	if siteRule == "" {
		t.Fatal("the site default produced no robots rule")
	}
	// The post's own rule, carrying its path.
	postPath := "/posts/20260101/open-post.html"
	postRule := RobotsRule(postPath, openPost)
	if postRule == "" {
		t.Fatal("an overriding post produced no robots rule")
	}

	if !strings.Contains(postRule, postPath) {
		t.Errorf("the post's rule does not name its path: %q — without the path a crawler "+
			"cannot tell which resource the override applies to", postRule)
	}
	if postRule == siteRule {
		t.Errorf("the overriding post's rule is identical to the site default (%q); the "+
			"override would be invisible in the file", postRule)
	}
	// ⛔ And the two must disagree on the term that differs, or the file is not
	// carrying the override at all.
	if strings.Contains(siteRule, "train-ai=y") {
		t.Errorf("the site rule claims train-ai=y but the site default is n: %q", siteRule)
	}
	if !strings.Contains(postRule, "train-ai=y") {
		t.Errorf("the post rule does not carry its own train-ai=y: %q", postRule)
	}
	t.Logf("site: %s", siteRule)
	t.Logf("post: %s", postRule)
}

// ⚠️ AND THE HONEST LIMIT, stated as a test so it is not discovered later: a
// term with no robots.txt expression would be lost on a rewrite-served site,
// because headers are the only other channel. ⭐ AIPREF's whole vocabulary is
// train-ai and search, and both are expressible, which is why the gap closes.
func TestEveryAiprefTermIsExpressibleInAFile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		terms *Terms
	}{
		{"train-ai=n", &Terms{TrainAI: "n"}},
		{"train-ai=y", &Terms{TrainAI: "y"}},
		{"search=n", &Terms{Search: "n"}},
		{"search=y", &Terms{Search: "y"}},
		{"both", &Terms{TrainAI: "n", Search: "y"}},
	} {
		if got := RobotsRule("/p.html", tc.terms); got == "" {
			t.Errorf("%s is not expressible in robots.txt, so it would be LOST on a site "+
				"served by a rewrite that cannot set headers", tc.name)
		}
	}
}
