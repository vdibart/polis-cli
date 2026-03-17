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
	// Video/audio attributes
	sanitizer.AllowAttrs("src", "type", "controls", "autoplay", "loop", "muted", "poster", "preload", "width", "height").OnElements("video", "audio", "source")
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
