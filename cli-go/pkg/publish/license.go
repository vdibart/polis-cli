package publish

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// licenseFrontmatter resolves the terms for a work being published and returns
// the YAML fragment to splice into its frontmatter.
//
// ⚠️ THE ONE RULE FOR CALLERS. The publish path builds frontmatter TWICE — once
// unsigned to form the signing base, and once with the signature attached for
// disk. Those two are hand-written format strings that must stay byte-identical
// apart from the signature line, because signing.MarkdownSigningBase reconstructs the
// signed bytes by deleting `signature:` from the file. Splice the SAME returned
// string into both. Building the block twice, or in different positions, breaks
// every signature silently — the file still parses, it just never verifies.
//
// Returns "" when nobody has said anything, which leaves the frontmatter
// byte-identical to what it was before this feature existed. Absent means
// unstated: not permitted, not denied.
func licenseFrontmatter(dataDir, authoredProfile, baseURL string) (string, *license.Terms, error) {
	siteTerms, err := site.SiteTerms(dataDir)
	if err != nil {
		// A site whose licence file is unreadable or invalid must not silently
		// publish under NO terms — that would quietly widen the grant. Fail the
		// publish instead and let the author fix the licence.
		return "", nil, err
	}

	terms, err := license.Resolve(authoredProfile, siteTerms, baseURL)
	if err != nil {
		return "", nil, err
	}
	if terms == nil {
		return "", nil, nil
	}
	return "\n" + license.Block(terms), terms, nil
}
