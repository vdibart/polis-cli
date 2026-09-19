package site

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
)

// The core bundle's post paths. Fixed by design — the core bundle's layout is
// not user-configurable, and checking that it has not moved is a feature rather
// than a limitation.
const (
	corePostsDir   = "content/pub.polis.core/post"
	corePostsMount = "posts"
)

// The file-based licence projections, generated in ONE place for every caller.
//
// ⛔ WHY THIS FUNCTION EXISTS AT ALL — it is not tidiness, it is a bug that had
// to be prevented before Medic could compare content.
//
// robots.txt and rsl.xml have two writers: the renderer writes them on every
// render, and Medic restores them when they are missing or wrong. Those two
// used to build their own options for license.Robots, and they did not build
// the same ones — the renderer passed a per-work override rule for every work
// that diverges from the site default plus a Sitemap line; Medic passed
// neither. Medic's generation was therefore strictly POORER than the
// renderer's on every site, not just on sites with overrides.
//
// A content comparison against a poorer generation is worse than no comparison
// at all: it mismatches on every tenant on every sweep, overwrites the correct
// file with the poorer one, loses the per-path rules, and the next render
// writes them back — an hourly write war between two halves of the same
// program. So the generation is one function, both callers use it, and neither
// gets to assemble its own inputs.
//
// This is the rule about projections — a projection is regenerated from its
// source, never maintained beside it — applied to the projection machinery
// itself: one predicate, two callers.

// LicenseProjections builds robots.txt and rsl.xml from the site's own signed
// licence. Returns (nil, nil, nil) when the site has stated no terms — silence
// is a real answer and an empty robots.txt would speak for an author who did
// not.
//
// ⚠️ THE ADDRESS COMES FROM THE SIGNED LICENCE, not from the caller.
// fallbackBaseURL is used ONLY when the licence names no origin of its own.
// That ordering is what makes the output recomputable from the site alone —
// the same property `DIDDocumentNeedsWrite` relies on, and the reason Medic can
// detect tampering with no stored baseline. A caller-first ordering would put
// the answer back in operator configuration, where Medic and the renderer can
// disagree, and the write war would return by the back door.
func LicenseProjections(siteDir, fallbackBaseURL string) (robots, rsl []byte, err error) {
	terms, err := SiteTerms(siteDir)
	if err != nil || terms == nil {
		return nil, nil, err
	}

	base := LicenseProjectionBase(terms, fallbackBaseURL)

	overrides, err := licenseOverrides(siteDir, terms)
	if err != nil {
		return nil, nil, err
	}

	robotsBody := license.Robots(license.RobotsOptions{
		SiteTerms:  terms,
		Overrides:  overrides,
		RSLURL:     licenseURL(base, "rsl.xml"),
		SitemapURL: licenseURL(base, "sitemap.xml"),
	})

	rslBody, err := license.RSL(terms, licenseURL(base, ""))
	if err != nil {
		return nil, nil, err
	}

	return []byte(robotsBody), []byte(rslBody), nil
}

// LicenseProjectionBase returns the origin the projections are addressed to:
// the origin of the signed licence's own terms URL, falling back to the
// caller's when the licence names none.
//
// Exported because Medic has to know whether an address exists at all before
// it decides to act — a projection it cannot address is one it must not write.
func LicenseProjectionBase(terms *license.Terms, fallbackBaseURL string) string {
	if terms != nil {
		if o := originOf(terms.Terms); o != "" {
			return o
		}
	}
	return strings.TrimSuffix(fallbackBaseURL, "/")
}

// licenseOverrides finds works whose materialised terms differ from the site
// default and returns a per-path robots rule for each.
//
// attach-04 §3's optional path pattern is what closes the static-hosting gap: a
// post that overrides the site default is still fully expressed in a file.
// Works matching the default produce no rule, so robots.txt stays small on a
// site where nothing diverges.
//
// ⚠️ The core bundle's paths are FIXED BY DESIGN, so they are written here
// rather than read from configuration.
// The mount is fixed for the same reason, and that is a change from the
// renderer's old walk, which took the mount from PageConfig: several renderers
// are constructed without those fields set, and each of those emitted override
// rules keyed by the CONTENT path instead of the mount path. Two writers of one
// file with two ideas of the key is the same disagreement this file exists to
// end, one level down.
func licenseOverrides(siteDir string, siteTerms *license.Terms) (map[string]*license.Terms, error) {
	siteSignal := license.ContentUsage(siteTerms)

	postsDir := filepath.Join(siteDir, filepath.FromSlash(corePostsDir))
	overrides := map[string]*license.Terms{}

	err := filepath.Walk(postsDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if info.Name() == ".versions" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil // unreadable post: nothing to say about it
		}
		workTerms := license.ParseBlock(string(content))
		if license.ContentUsage(workTerms) == siteSignal {
			return nil
		}
		rel, err := filepath.Rel(postsDir, path)
		if err != nil {
			return nil
		}
		mount := filepath.ToSlash(filepath.Join(corePostsMount, rel))
		overrides["/"+strings.TrimSuffix(mount, ".md")+".html"] = workTerms
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return overrides, nil
}

// licenseURL joins an origin and a site-relative path. With no origin the path
// stands alone, which is what a site rendered without a base URL has always
// emitted.
func licenseURL(base, path string) string {
	base = strings.TrimSuffix(base, "/")
	if base == "" {
		return path
	}
	if path == "" {
		return base + "/"
	}
	return base + "/" + path
}

// originOf returns the scheme://host of a URL, or "" if it has none.
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
