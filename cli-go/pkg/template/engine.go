// Package template provides a Mustache-like template engine for polis themes.
package template

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MarkdownRenderer is a function that converts markdown to HTML.
type MarkdownRenderer func(markdown string) (string, error)

// Config holds template engine configuration.
type Config struct {
	DataDir          string           // Site data directory (for global snippets)
	CLIThemesDir     string           // CLI themes directory (fallback for theme snippets)
	ActiveTheme      string           // Currently active theme name
	RenderMarkers    bool             // Whether to add snippet markers for editing
	BaseURL          string           // Site base URL for generating URLs
	MarkdownRenderer MarkdownRenderer // Function to render markdown to HTML
}

// Engine renders polis templates with Mustache-like syntax.
type Engine struct {
	config           Config
	maxDepth         int              // Maximum recursion depth for partial includes (default 10)
	markdownRenderer MarkdownRenderer // Function to render markdown to HTML
}

// RenderContext holds all variables available during rendering.
type RenderContext struct {
	// Page variables
	Title          string
	Content        string
	Published      string // ISO 8601 format
	PublishedHuman string // Human-readable format
	URL            string
	Version        string
	SignatureShort string

	// Site variables
	SiteURL    string
	SiteTitle  string
	CSSPath    string
	HomePath   string
	AuthorName string
	AuthorURL  string
	Year       string

	// Counts
	BlessedCount int
	CommentCount int
	PostCount    int

	// Conditional HTML fragments
	ViewAllPostsLink string // Pre-rendered "View all N posts" link (empty if ≤10)

	// Widget variables
	AuthorDomain string // Site domain (e.g. "alice.polis.pub")
	PageType     string // "post", "comment", or "index"

	// Comment-specific
	InReplyToURL string
	RootPostURL  string
	TargetAuthor string
	Preview      string

	// Loop data (for sections)
	Posts           []PostData
	Comments        []CommentData
	BlessedComments []BlessedCommentData
	RecentPosts     []PostData
	RecentComments  []CommentData
	Following       []FollowingData
}

// FollowingData represents a followed author in a loop.
type FollowingData struct {
	URL        string // Full URL (e.g. "https://alice.polis.pub")
	Domain     string // Domain only (e.g. "alice.polis.pub")
	AuthorName string // Optional display name
	SiteTitle  string // Optional site title
}

// PostData represents a post in a loop.
type PostData struct {
	URL            string
	Title          string
	Excerpt        string // Plain-text excerpt from post body (first ~200 chars)
	Published      string
	PublishedHuman string
	CommentCount   int
}

// CommentData represents a comment in a loop.
type CommentData struct {
	URL            string
	TargetAuthor   string
	Published      string
	PublishedHuman string
	Preview        string
}

// BlessedCommentData represents a blessed comment on a post.
type BlessedCommentData struct {
	URL            string
	AuthorName     string
	Published      string
	PublishedHuman string
	Content        string
}

// New creates a new template engine with the given configuration.
func New(cfg Config) *Engine {
	return &Engine{
		config:           cfg,
		maxDepth:         10,
		markdownRenderer: cfg.MarkdownRenderer,
	}
}

// SetMarkdownRenderer sets the markdown renderer function.
// This allows avoiding import cycles when the render package needs to use templates.
func (e *Engine) SetMarkdownRenderer(renderer MarkdownRenderer) {
	e.markdownRenderer = renderer
}

// Render renders a template string with the given context.
// It processes variable substitution, partials ({{> path}}), and sections ({{#name}}...{{/name}}).
func (e *Engine) Render(tmpl string, ctx *RenderContext) (string, error) {
	result, err := e.renderWithDepth(tmpl, ctx, 0)
	if err != nil {
		return "", err
	}
	// Restore escaped braces from user data (see escapedOpenBrace in sections.go)
	return strings.ReplaceAll(result, escapedOpenBrace, "{{"), nil
}

// renderWithDepth renders a template with depth tracking to prevent infinite recursion.
func (e *Engine) renderWithDepth(template string, ctx *RenderContext, depth int) (string, error) {
	if depth > e.maxDepth {
		return "", fmt.Errorf("maximum template recursion depth (%d) exceeded", e.maxDepth)
	}

	var err error

	// Process sections first (they may contain partials and variables)
	template, err = e.processSections(template, ctx, depth)
	if err != nil {
		return "", err
	}

	// Process partials ({{> path}})
	template, err = e.processPartials(template, ctx, depth)
	if err != nil {
		return "", err
	}

	// Process variable substitution
	template = e.substituteVariables(template, ctx)

	return template, nil
}

// substituteVariables replaces {{variable}} with values from context.
func (e *Engine) substituteVariables(template string, ctx *RenderContext) string {
	// Build variable map
	vars := map[string]string{
		// Page variables
		"title":           ctx.Title,
		"content":         ctx.Content,
		"published":       ctx.Published,
		"published_human": ctx.PublishedHuman,
		"url":             ctx.URL,
		"version":         ctx.Version,
		"signature_short": ctx.SignatureShort,

		// Site variables
		"site_url":    ctx.SiteURL,
		"site_title":  ctx.SiteTitle,
		"css_path":    ctx.CSSPath,
		"home_path":   ctx.HomePath,
		"author_name": ctx.AuthorName,
		"author_url":  ctx.AuthorURL,
		"year":        ctx.Year,

		// Counts
		"blessed_count": fmt.Sprintf("%d", ctx.BlessedCount),
		"comment_count": fmt.Sprintf("%d", ctx.CommentCount),
		"post_count":    fmt.Sprintf("%d", ctx.PostCount),

		// Conditional fragments
		"view_all_posts": ctx.ViewAllPostsLink,

		// Widget variables
		"author_domain": ctx.AuthorDomain,
		"page_type":     ctx.PageType,

		// Comment-specific
		"in_reply_to_url": ctx.InReplyToURL,
		"root_post_url":   ctx.RootPostURL,
		"target_author":   ctx.TargetAuthor,
		"preview":         ctx.Preview,
	}

	// Replace all {{variable}} patterns.
	// Escape "{{" in substituted values to prevent template injection
	// (see escapedOpenBrace in sections.go).
	re := regexp.MustCompile(`\{\{(\w+)\}\}`)
	return re.ReplaceAllStringFunc(template, func(match string) string {
		// Extract variable name from {{name}}
		name := match[2 : len(match)-2]
		if val, ok := vars[name]; ok {
			return strings.ReplaceAll(val, "{{", escapedOpenBrace)
		}
		// Unknown variables are left as-is
		return match
	})
}

// GetThemeSnippetsDir returns the path to theme snippets directory.
func (e *Engine) GetThemeSnippetsDir() string {
	// First try local theme
	localTheme := filepath.Join(e.config.DataDir, "site", "themes", e.config.ActiveTheme, "snippets")
	if e.config.ActiveTheme != "" {
		return localTheme
	}
	// Fall back to CLI themes
	if e.config.CLIThemesDir != "" && e.config.ActiveTheme != "" {
		return filepath.Join(e.config.CLIThemesDir, e.config.ActiveTheme, "snippets")
	}
	return localTheme
}

// GetGlobalSnippetsDir returns the path to global snippets directory.
func (e *Engine) GetGlobalSnippetsDir() string {
	return filepath.Join(e.config.DataDir, "site", "snippets")
}

// FormatHumanDate formats an ISO 8601 date string to human-readable format.
func FormatHumanDate(isoDate string) string {
	// Try parsing ISO 8601 format
	t, err := time.Parse("2006-01-02T15:04:05Z", isoDate)
	if err != nil {
		// Try date-only format
		t, err = time.Parse("2006-01-02", isoDate)
		if err != nil {
			return isoDate // Return as-is if parsing fails
		}
	}
	return t.Format("January 2, 2006")
}

// TruncateSignature returns the first N characters of a base64 signature.
func TruncateSignature(signature string, length int) string {
	// Remove whitespace and newlines
	sig := strings.TrimSpace(signature)
	sig = strings.ReplaceAll(sig, "\n", "")

	if len(sig) <= length {
		return sig
	}
	return sig[:length] + "..."
}

// NewRenderContext creates a new context with common defaults.
func NewRenderContext() *RenderContext {
	return &RenderContext{
		Year:            time.Now().Format("2006"),
		Posts:           []PostData{},
		Comments:        []CommentData{},
		BlessedComments: []BlessedCommentData{},
		RecentPosts:     []PostData{},
		RecentComments:  []CommentData{},
	}
}
