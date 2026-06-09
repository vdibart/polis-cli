// Package url provides URL utilities for polis.
package url

import (
	"net/url"
	"strings"
)

// NormalizeToHTML converts .md URLs to .html format. Inverse of
// NormalizeToMD: polis stores content URLs in .md form (the canonical
// signing target), but display surfaces (links, hrefs in the rendered
// stream) want the .html mount-path form so the link resolves to a
// browsable page rather than a raw markdown source.
func NormalizeToHTML(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		if strings.HasSuffix(rawURL, ".md") {
			return strings.TrimSuffix(rawURL, ".md") + ".html"
		}
		return rawURL
	}
	if strings.HasSuffix(parsed.Path, ".md") {
		parsed.Path = strings.TrimSuffix(parsed.Path, ".md") + ".html"
	}
	return parsed.String()
}

// NormalizeToMD converts .html URLs to .md format.
// Polis stores all content URLs with .md extension.
// This matches the bash CLI's normalize_url_to_md function.
func NormalizeToMD(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		// If we can't parse, fall back to simple string replacement
		if strings.HasSuffix(rawURL, ".html") {
			return strings.TrimSuffix(rawURL, ".html") + ".md"
		}
		return rawURL
	}

	if strings.HasSuffix(parsed.Path, ".html") {
		parsed.Path = strings.TrimSuffix(parsed.Path, ".html") + ".md"
	}

	return parsed.String()
}

// commentMountSeg / commentSrcSeg are the two URL shapes a comment can take.
// Mount (legacy / historical DS registration) vs canonical source path.
const (
	commentMountSeg = "/comments/"
	commentSrcSeg   = "/content/pub.polis.core/comment/"
)

// CommentContentURL builds the CANONICAL (source-path) URL for a comment —
// e.g. https://site/content/pub.polis.core/comment/20260302/<id>.md. This is
// the same path PublishComment writes the signed .md to, so the DS-registered
// URL dereferences the canonical artifact (posts register their source path the
// same way). Comments historically (mis)registered the /comments/ mount path —
// see plans/comment-registration-severe-bug.md, Defect 1.
func CommentContentURL(baseURL, dateDir, commentID string) string {
	return strings.TrimSuffix(baseURL, "/") + commentSrcSeg + dateDir + "/" + commentID + ".md"
}

// CommentURLToContentRel converts a comment URL — in EITHER the legacy mount
// form (.../comments/<date>/<id>.{md,html}) or the canonical source form
// (.../content/pub.polis.core/comment/<date>/<id>.{md,html}) — to the canonical
// site-relative source path "content/pub.polis.core/comment/<date>/<id>.md".
// Returns "" if the URL matches neither shape. Tolerant of both forms so the
// comment-URL canonicalization (Defect 1) can flip producers without breaking
// consumers mid-transition.
func CommentURLToContentRel(commentURL string) string {
	var rest string
	if i := strings.Index(commentURL, commentSrcSeg); i >= 0 {
		rest = commentURL[i+len(commentSrcSeg):]
	} else if i := strings.Index(commentURL, commentMountSeg); i >= 0 {
		rest = commentURL[i+len(commentMountSeg):]
	} else {
		return ""
	}
	rest = strings.TrimSuffix(rest, ".html")
	rest = strings.TrimSuffix(rest, ".md")
	return "content/pub.polis.core/comment/" + rest + ".md"
}
