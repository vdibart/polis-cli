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
// same way). Comments historically (mis)registered the /comments/ mount path,
// which holds rendered .html; a .md there is at most a legacy duplicate, and
// the serve path 301s it to this one.
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
	// ⛔⛔ A COMMENT URL COMES FROM ANOTHER SITE, so this is attacker input, and
	// the result is handed straight to the store. It used to take everything
	// after the first comment segment found ANYWHERE in the string — the query
	// included — and keep '..', so `.../comment/../../../.polis/...` became a
	// valid in-site key: an anonymous read of drafts, a forged event deleting a
	// post, a blessed-cache write anywhere.
	//
	// ⭐ So: only the URL's PATH is read (the host, query and fragment play no
	// part), percent-encoding is decoded first so `%2e%2e` cannot slip past, and
	// a remainder with an empty, '.' or '..' segment or a backslash yields "".
	u, err := url.Parse(commentURL)
	if err != nil {
		return ""
	}
	p := u.Path
	var rest string
	if i := strings.Index(p, commentSrcSeg); i >= 0 {
		rest = p[i+len(commentSrcSeg):]
	} else if i := strings.Index(p, commentMountSeg); i >= 0 {
		rest = p[i+len(commentMountSeg):]
	} else {
		return ""
	}
	if !SafeRelPath(rest) {
		return ""
	}
	rest = strings.TrimSuffix(rest, ".html")
	rest = strings.TrimSuffix(rest, ".md")
	if rest == "" {
		return ""
	}
	return "content/pub.polis.core/comment/" + rest + ".md"
}

// SafeRelPath reports whether p is a plain relative path: no empty, '.' or
// '..' segment, no backslash, no NUL, no leading slash. ⭐ The check every key
// built from foreign input passes before it addresses the store.
func SafeRelPath(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}
