package render

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"
	"unicode"
)

// maxOGDescriptionLen caps the og:description / twitter:description value at
// the threshold most unfurlers truncate to anyway. Word-boundary truncation
// keeps the snippet readable.
const maxOGDescriptionLen = 150

// buildOGDescription extracts a plain-text excerpt from a markdown body for
// use in og:description / twitter:description. Strategy:
//   1. Reuse the existing MarkdownToPlainText helper to strip markdown syntax.
//   2. Collapse whitespace to single spaces.
//   3. Truncate at the last word boundary ≤ maxOGDescriptionLen, falling back
//      to a hard cut if no word boundary exists.
//   4. Append an ellipsis when truncation occurred.
//
// Empty body → empty string (caller should fall back to title).
func buildOGDescription(markdownBody string) string {
	plain := MarkdownToPlainText(markdownBody, maxOGDescriptionLen+50) // pull a bit more so we have room to find a word boundary
	plain = collapseWhitespace(plain)
	if plain == "" {
		return ""
	}
	if len(plain) <= maxOGDescriptionLen {
		return plain
	}
	// Truncate at the last whitespace within the window.
	cut := plain[:maxOGDescriptionLen]
	if idx := strings.LastIndexFunc(cut, unicode.IsSpace); idx > 0 {
		cut = cut[:idx]
	}
	return strings.TrimRight(cut, " \t\n") + "…"
}

// collapseWhitespace flattens runs of whitespace to single spaces and trims.
// MarkdownToPlainText emits text with embedded newlines from list items /
// paragraph breaks; for an OG description we want a single-line summary.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true // leading whitespace dropped
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimRight(b.String(), " ")
}

// attrEscape HTML-attribute-escapes a value destined for content="..." in a
// <meta> tag. html.EscapeString covers &, <, >, ", '. Crucial for OG/Twitter
// fields where user-controlled titles + descriptions land in attribute values.
func attrEscape(s string) string {
	return html.EscapeString(s)
}

// blogPostingJSONLD struct marshals to schema.org BlogPosting JSON-LD per
// step-03/3.d. Field order in the final JSON follows struct order; that
// ordering is irrelevant to crawlers but kept stable for readable output.
type blogPostingJSONLD struct {
	Context          string                 `json:"@context"`
	Type             string                 `json:"@type"`
	Headline         string                 `json:"headline"`
	DatePublished    string                 `json:"datePublished,omitempty"`
	DateModified     string                 `json:"dateModified,omitempty"`
	Author           jsonLDAuthor           `json:"author"`
	URL              string                 `json:"url"`
	MainEntityOfPage string                 `json:"mainEntityOfPage"`
}

type webSiteJSONLD struct {
	Context     string `json:"@context"`
	Type        string `json:"@type"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

type jsonLDAuthor struct {
	Type string `json:"@type"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// buildBlogPostingJSONLD renders a per-post BlogPosting JSON-LD as a fully-
// formed <script type="application/ld+json">…</script> block. The serialized
// JSON has its `</` sequences escaped as `<\/` (JSON-spec-valid; backslash-
// escaped solidus parses identically) so adversarial body content containing
// "</script>" can't break out of the script tag.
func buildBlogPostingJSONLD(title, isoPublished, isoModified, authorName, authorURL, canonicalURL string) (string, error) {
	v := blogPostingJSONLD{
		Context:          "https://schema.org",
		Type:             "BlogPosting",
		Headline:         title,
		DatePublished:    isoPublished,
		DateModified:     isoModified,
		URL:              canonicalURL,
		MainEntityOfPage: canonicalURL,
		Author: jsonLDAuthor{
			Type: "Person",
			Name: authorName,
			URL:  authorURL,
		},
	}
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal BlogPosting JSON-LD: %w", err)
	}
	return wrapJSONLDScript(body), nil
}

// buildWebSiteJSONLD renders a homepage WebSite JSON-LD block. Used for
// /index.html. Description is optional; pass empty if no tenant blurb is
// available.
func buildWebSiteJSONLD(name, canonicalURL, description string) (string, error) {
	v := webSiteJSONLD{
		Context:     "https://schema.org",
		Type:        "WebSite",
		Name:        name,
		URL:         canonicalURL,
		Description: description,
	}
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal WebSite JSON-LD: %w", err)
	}
	return wrapJSONLDScript(body), nil
}

// wrapJSONLDScript wraps a JSON byte slice in a <script type="application/ld+json">
// element with defensive </script> escaping.
func wrapJSONLDScript(body []byte) string {
	// Defensive: turn `</` into `<\/`. JSON doesn't require backslash-escaping
	// solidus but accepts it; this prevents a literal "</script>" from ever
	// appearing inside the script-element text content (which an HTML parser
	// would treat as the closing tag).
	safe := strings.ReplaceAll(string(body), "</", "<\\/")
	return `<script type="application/ld+json">` + "\n" + safe + "\n" + `</script>`
}

// formatISO normalizes a frontmatter timestamp string to RFC 3339. Polis
// frontmatter always emits "2006-01-02T15:04:05Z" (publish.go), so this is
// usually a passthrough. Returns empty string on parse failure rather than a
// malformed value (better to omit datePublished than to write garbage).
func formatISO(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	if err != nil {
		// Try other common forms before giving up.
		if t2, err2 := time.Parse(time.RFC3339, s); err2 == nil {
			return t2.UTC().Format(time.RFC3339)
		}
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
