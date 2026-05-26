// Package render provides markdown to HTML rendering using goldmark.
package render

import (
	"bytes"
	"html"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldhtml "github.com/yuin/goldmark/renderer/html"
)

// md is the configured goldmark markdown renderer
var md goldmark.Markdown

// sanitizer is the HTML sanitization policy applied after markdown rendering.
// It strips dangerous elements (script, iframe, object, embed, form) and event
// handlers while preserving structural HTML that polis authors commonly use.
var sanitizer *bluemonday.Policy

// safeSrcURLPattern matches URLs safe to put in a `src` attribute on elements
// where bluemonday's built-in URL-scheme check doesn't reach (notably <source>;
// bluemonday's isURLAttribute list at sanitize.go:992 omits it). Accepts
// http(s)://, mailto:, protocol-relative //, root-relative /, and any URL with
// no scheme (relative paths). Rejects javascript:, data:, vbscript:, file:, etc.
var safeSrcURLPattern = regexp.MustCompile(`(?i)^(?:https?:|mailto:|//|/|#|\?|[^:/?#]*(?:[/?#]|$))`)

func init() {
	md = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM, // GitHub Flavored Markdown
			extension.Typographer,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			goldhtml.WithHardWraps(),
			goldhtml.WithXHTML(),
			goldhtml.WithUnsafe(), // Allow raw HTML through so bluemonday can make allow/deny decisions
		),
	)

	sanitizer = bluemonday.UGCPolicy()
	// Structural HTML elements authors use in posts
	sanitizer.AllowElements("details", "summary", "video", "audio", "source", "picture", "figure", "figcaption", "mark", "kbd", "abbr", "dfn", "ins", "del", "sub", "sup", "ruby", "rt", "rp", "time", "meter", "progress", "dialog")
	// Video/audio attributes — bluemonday URL-scheme-checks `src` on <video>/<audio>
	// via its internal isURLAttribute list, so the global AllowURLSchemes allowlist
	// (http/https/mailto, set by UGCPolicy) is enforced here automatically.
	sanitizer.AllowAttrs("src", "type", "controls", "autoplay", "loop", "muted", "poster", "preload", "width", "height").OnElements("video", "audio")
	// <source>'s `src` is NOT in bluemonday's isURLAttribute list (sanitize.go:992),
	// so the global URL-scheme allowlist does not reach it — `<source src="javascript:…">`
	// would survive sanitization. Enforce safe URLs here via .Matching.
	sanitizer.AllowAttrs("src").Matching(safeSrcURLPattern).OnElements("source")
	sanitizer.AllowAttrs("type", "width", "height").OnElements("source")
	// Data attributes for custom styling
	sanitizer.AllowDataAttributes()
	// Class attributes (themes depend on this)
	sanitizer.AllowAttrs("class").Globally()
	// ID attributes (heading anchors, in-page links)
	sanitizer.AllowAttrs("id").Globally()
}

// MarkdownToHTML converts markdown content to HTML.
// The output is sanitized to remove dangerous elements (script, iframe, etc.)
// and event handlers while preserving safe structural HTML.
func MarkdownToHTML(markdown string) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		return "", err
	}
	return sanitizer.Sanitize(buf.String()), nil
}

// htmlTagPattern matches HTML tags for stripping.
var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

// whitespacePattern matches runs of whitespace for collapsing.
var whitespacePattern = regexp.MustCompile(`\s+`)

// MarkdownToPlainText converts markdown to plain text, truncated to maxLen characters.
// It renders markdown to HTML, strips all tags and entities, collapses whitespace,
// and truncates with "..." if the result exceeds maxLen.
func MarkdownToPlainText(markdown string, maxLen int) string {
	if markdown == "" {
		return ""
	}

	// Render to HTML
	htmlStr, err := MarkdownToHTML(markdown)
	if err != nil {
		return truncatePlainText(markdown, maxLen)
	}

	// Strip HTML tags
	text := htmlTagPattern.ReplaceAllString(htmlStr, "")

	// Unescape HTML entities
	text = html.UnescapeString(text)

	// Collapse whitespace and trim
	text = whitespacePattern.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	return truncatePlainText(text, maxLen)
}

// truncatePlainText truncates text to maxLen, adding "..." if truncated.
// It tries to break at word boundaries.
func truncatePlainText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	// Find last space before maxLen-3 to break at word boundary
	cut := maxLen - 3
	if idx := strings.LastIndex(text[:cut], " "); idx > cut/2 {
		cut = idx
	}
	return text[:cut] + "..."
}
