// Package render provides markdown to HTML rendering using goldmark.
package render

import (
	"bytes"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldhtml "github.com/yuin/goldmark/renderer/html"
)

// md is the configured goldmark markdown renderer
var md goldmark.Markdown

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
			goldhtml.WithUnsafe(), // Allow raw HTML in markdown
		),
	)
}

// MarkdownToHTML converts markdown content to HTML.
func MarkdownToHTML(markdown string) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
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
