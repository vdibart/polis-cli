package comment

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	// ⚠️ ALIASED: `site` is the parameter name throughout the site-handle
	// migration, and an unaliased import of this package shadows it.
	sitepkg "github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// commentLicenseFrontmatter resolves this site's terms for a comment being
// signed and returns the YAML fragment to splice into its frontmatter.
//
// A comment has no per-work override today: the author writes it in a compose
// box rather than a file with frontmatter, so there is nowhere to write one.
// The site default applies, which is the right answer anyway — the terms on
// your commentary are the terms on your writing.
//
// Returns "" when this site has stated no terms.
func commentLicenseFrontmatter(site storage.Site, siteURL string) (string, error) {
	siteTerms, err := sitepkg.SiteTerms(site)
	if err != nil {
		return "", err
	}
	terms, err := license.Resolve("", siteTerms, siteURL)
	if err != nil {
		return "", err
	}
	if terms == nil {
		return "", nil
	}
	return "\n" + license.Block(terms), nil
}
