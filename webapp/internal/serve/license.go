package serve

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
)

// Licence response headers, per resource.
//
// ⚠️ PER-RESOURCE, NEVER PER-SITE. If a site's default is `reserved` but one
// post is `open`, that post's response has to carry THAT post's terms —
// otherwise the header contradicts the signed frontmatter of the very document
// it is attached to, which breaks one-source-of-truth in the most visible place
// possible. AIPREF agrees: draft-ietf-aipref-attach-04 §2 defines Content-Usage
// as representation metadata that "applies to the content of a message, not the
// resource."
//
// The terms are therefore read out of the bytes actually being served rather
// than looked up from the site licence. For markdown that is the signed
// frontmatter block; for rendered HTML it is the head elements the renderer
// generated from that same block. Either way the header cannot disagree with
// the body, because it is derived from it.
//
// Headers are a convenience, never the only home for a term. A self-hoster on
// S3 or GitHub Pages sets no headers at all, and every term is also expressed
// in robots.txt, rsl.xml, and the signed frontmatter for exactly that reason.

var (
	metaContentUsageRe = regexp.MustCompile(`(?i)<meta\s+name="content-usage"\s+content="([^"]*)"`)
	linkLicenseRe      = regexp.MustCompile(`(?i)<link\s+rel="license"\s+href="([^"]*)"`)
)

// setLicenseHeaders attaches Content-Usage and Link: rel="license" to a
// response, derived from the bytes being served. No-ops for anything that
// carries no terms.
func setLicenseHeaders(w http.ResponseWriter, ext string, data []byte) {
	switch ext {
	case ".md":
		terms := license.ParseBlock(string(data))
		if cu := license.ContentUsage(terms); cu != "" {
			w.Header().Set("Content-Usage", cu)
		}
		if lh := license.LinkHeader(terms); lh != "" {
			w.Header().Add("Link", lh)
		}
	case ".html":
		// Read back what the renderer emitted for THIS page rather than
		// re-resolving the terms. Re-resolving would consult the site's
		// CURRENT licence, which is exactly the contradiction to avoid: the
		// page in hand was signed under the terms it displays.
		head := headSection(data)
		if m := metaContentUsageRe.FindStringSubmatch(head); m != nil && m[1] != "" {
			w.Header().Set("Content-Usage", unescapeAttr(m[1]))
		}
		if m := linkLicenseRe.FindStringSubmatch(head); m != nil && m[1] != "" {
			w.Header().Add("Link", "<"+unescapeAttr(m[1])+`>; rel="license"`)
		}
	}
}

// headSection returns the document head, so a licence-shaped string appearing
// in body copy cannot be promoted into a response header.
func headSection(data []byte) string {
	s := string(data)
	if end := strings.Index(strings.ToLower(s), "</head>"); end != -1 {
		return s[:end]
	}
	// No head found: scan a bounded prefix rather than the whole document.
	if len(s) > 8192 {
		return s[:8192]
	}
	return s
}

// unescapeAttr reverses the HTML attribute escaping the renderer applied.
// Header values are ASCII tokens and URLs, so this handles the entities
// html.EscapeString can produce and nothing more.
func unescapeAttr(s string) string {
	return strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&#34;", `"`,
		"&#39;", "'",
	).Replace(s)
}
