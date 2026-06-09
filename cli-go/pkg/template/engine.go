// Package template provides a Mustache-like template engine for polis themes.
package template

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var variableRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

// MarkdownRenderer is a function that converts markdown to HTML.
type MarkdownRenderer func(markdown string) (string, error)

// Config holds template engine configuration.
type Config struct {
	DataDir          string           // Site data directory (for global snippets)
	CLIThemesDir     string           // CLI themes directory (fallback for theme snippets)
	ActiveTheme      string           // Currently active theme name
	ActiveShape      string           // Active shape name (e.g. "v3", "v4"); drives base-partial lookup
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
	PublishedYear  string // Calendar year ("2026") — stream year-marker
	URL            string
	Version        string
	SignatureShort string

	// Site variables
	SiteURL     string
	SiteTitle   string
	CSSPath     string
	BaseCSSPath string
	HomePath    string
	FaviconPath string
	AuthorName string
	AuthorURL  string
	Year       string

	// Counts
	BlessedCount int
	CommentCount int
	PostCount    int

	// Derived counts
	FollowingCount int
	// FollowersCount is the count of distinct followers across DS state (sum
	// of pub.polis.follow.json entries under .polis/ds/<domain>/). Populated
	// by v4 renders for the .site-stats block; blog templates ignore it.
	FollowersCount int

	// SiteBio is the pre-rendered HTML of the global about snippet
	// (snippets/about.md), used by v4's .site-bio block in the layout-left
	// card. Pre-rendering server-side instead of via the partial resolver
	// lets the bio gracefully empty out when about.md is missing rather than
	// failing the render with a partial-not-found error.
	SiteBio string

	// Pre-rendered HTML fragments
	ViewAllPostsLink string // Pre-rendered "View all N posts" link (empty if ≤10)
	AvatarHTML       string // Pre-rendered avatar: <img> or <span> with initial

	// Widget variables
	AuthorDomain  string // Site domain (e.g. "alice.polis.pub")
	PageType      string // "post", "comment", or "index"
	WidgetVersion string // Widget JS version (e.g. "1.4.1")

	// stream shape variables (stream.html)
	// CanonicalURL is the rel=canonical href for the page being rendered, per
	// D-CANONICAL: per-post pages self-canonical to their own .html URL;
	// /index.html canonical-points at the absolute URL of the currently-newest
	// post. Populated by renderStreamFile / renderStreamEmptyIndex; left empty by v3
	// callers (blog templates don't reference {{canonical_url}}).
	CanonicalURL  string
	ControllerURL string // stream.js URL (WS-2 placeholder)
	// StreamShapeVersion is the active stream shape version (e.g. "1.7.0").
	// Threaded into the rendered HTML as the cache-bust query param on the
	// controller script URL:
	// `<script src="{{controller_url}}?v={{stream_shape_version}}">`.
	// Bumping the stream shape version (in cli-go/pkg/bundle/bundle.go) thus
	// also invalidates browser cache of the previous controller code, so users
	// pick up new behavior on next page load instead of the stale TTL-bound
	// copy.
	StreamShapeVersion string

	// Metadata head-block (step-03/3.d). Populated by renderStreamFile for v4
	// per-post pages and by the index renderer for /index.html. Left empty
	// by blog callers (blog templates don't reference these).
	//   OGDescription — plain-text excerpt of the focus post body, ≤150 chars,
	//                   word-boundary truncated. Falls back to title.
	//   OGImageURL    — absolute URL of the tenant favicon (worker decision
	//                   for v1; per-post body-image extraction is a later
	//                   refinement).
	//   JSONLD        — fully-rendered <script type="application/ld+json">…
	//                   </script> block, with </script> defensively escaped
	//                   so adversarial body content can't break out.
	//   ISOPublished  — RFC 3339 timestamp; from frontmatter `published`.
	//   ISOModified   — RFC 3339 timestamp; from frontmatter `updated` if
	//                   present, else equals ISOPublished.
	OGTitle       string // ctx.Title pre-attribute-escaped for og:/twitter:title
	OGDescription string
	OGImageURL    string
	JSONLD        string
	ISOPublished  string
	ISOModified   string
	WidgetHome     string // data-home for hosted nav widget
	WidgetHandle   string // data-handle for hosted nav widget
	WidgetHost     string // data-host for hosted nav widget
	SiteDomain     string // site domain (alias surface for stream templates)
	// FocusPathURL is the focus post's path-form canonical URL
	// (e.g. "/posts/20260429/foo.html"). Used by the stream's focus-
	// title link wrapper — clicking the focus title or body navigates
	// to this URL. Differs from CanonicalURL (which is absolute) and
	// URL (also absolute on v4 per renderStreamFile).
	FocusPathURL string

	// TitleLinkState modifies the focus's {{title_link_state}} wrapper
	// class. Set to "is-redundant" when the post body's first paragraph
	// already begins with the title text; CSS hides the explicit title
	// chrome so it doesn't read as a duplicate. Same field is mirrored
	// on PostData for siblings.
	TitleLinkState string

	// LayoutRightStateClass is appended to <main class="layout-right">
	// to flag layout state for CSS rules. Currently used to gate the
	// stream-top-fade overlay: empty on /index.html (no above-focus
	// siblings, no fade needed), "has-above-focus" on per-post pages
	// where above-focus entries extend up under the topbar.
	LayoutRightStateClass string

	// Comment-specific
	InReplyToURL     string
	InReplyToTitle   string // Human-readable title (fetched or URL-derived)
	InReplyToDomain  string // Domain of the post being replied to
	InReplyToExcerpt string // First paragraph excerpt from the parent post
	RootPostURL      string
	TargetAuthor     string
	Preview          string

	// Tag variables
	TagName     string
	TagCount    int
	TargetCount int

	// Loop data (for sections)
	Posts           []PostData
	Comments        []CommentData
	BlessedComments []BlessedCommentData
	RecentPosts     []PostData
	RecentComments  []CommentData
	Siblings        []PostData // v4: older posts shown BELOW the focus (older-direction rail)
	SiblingsAbove   []PostData // v4: newer posts shown ABOVE the focus (newer-direction rail)
	Following       []FollowingData
	Targets         []TagTargetData
	Tags            []TagData
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
	Excerpt        string // Plain-text excerpt from post body (first ~200 chars; v3 use)
	BodyHTML       string // Full HTML-rendered body (v4 use; populated on demand by render pipeline)
	Published      string
	PublishedHuman string
	CommentCount   int
	// IsAboveFocus marks v4 sibling entries rendered ABOVE the focus
	// (newer-direction rail). Drives the .is-above-focus class in
	// stream-post.html for the fade-into-topbar visual treatment. Always
	// false for below-focus siblings + index page list views.
	IsAboveFocus bool
	// TitleLinkState is appended to the {{title_link_state}} substitution
	// on the entry's <a class="entry-title-link"> wrapper. "is-redundant"
	// when the post body's first paragraph already begins with the title
	// text — CSS hides the wrapper visually so the body absorbs the
	// title without producing a duplicate. DOM stays present so
	// stream.js's setupCardClicks can still read its href as the
	// click-anywhere navigate target.
	TitleLinkState string
}

// CommentData represents a comment in a loop.
type CommentData struct {
	URL            string
	TargetAuthor   string
	Published      string
	PublishedHuman string
	Preview        string
}

// TagTargetData represents a tagged target in a tag page loop.
type TagTargetData struct {
	URI   string
	Added string
}

// TagData represents a tag in the tag index loop.
type TagData struct {
	TagName string
	Count   int
	Updated string
}

// BlessedCommentData represents a blessed comment on a post. The comment
// renders inside the v4 comment-thread card layout (commenter avatar floats
// right, handle reveals on rollover, date on the left, small timeline-dot on
// the rail) — so the per-comment author identity (domain + avatar markup)
// rides alongside the body. AuthorAvatarHTML is the SSR'd <span class="av">
// markup from render.AvatarHTML — emitted verbatim into the partial (the
// avatar markup is self-contained + sanitized at construction).
type BlessedCommentData struct {
	URL              string
	AuthorName       string
	AuthorDomain     string
	AuthorAvatarHTML string
	Published        string
	PublishedHuman   string
	Content          string
}

// New creates a new template engine with the given configuration.
func New(cfg Config) *Engine {
	return &Engine{
		config:           cfg,
		maxDepth:         10,
		markdownRenderer: cfg.MarkdownRenderer,
	}
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
		"published_year":  ctx.PublishedYear,
		"url":             ctx.URL,
		"version":         ctx.Version,
		"signature_short": ctx.SignatureShort,

		// Site variables
		"site_url":    ctx.SiteURL,
		"site_title":  ctx.SiteTitle,
		"css_path":      ctx.CSSPath,
		"base_css_path": ctx.BaseCSSPath,
		"home_path":     ctx.HomePath,
		"favicon_path":  ctx.FaviconPath,
		"author_name": ctx.AuthorName,
		"author_url":  ctx.AuthorURL,
		"year":        ctx.Year,

		// Counts
		"blessed_count":  fmt.Sprintf("%d", ctx.BlessedCount),
		"comment_count":  fmt.Sprintf("%d", ctx.CommentCount),
		// Mirror sections.go's per-iteration comment_count_class /
		// comment_count_display vars at the top-level scope so v4's
		// focus-entry badge in stream.html can hide-when-empty using
		// the same grammar siblings use via stream-post.html.
		// Empty count → class "is-empty" + empty display string.
		// Non-empty → class "" + stringified count.
		"comment_count_class":   commentCountClass(ctx.CommentCount),
		"comment_count_display": commentCountDisplay(ctx.CommentCount),
		// title_link_state — appended into the focus's
		// <a class="entry-title-link {{title_link_state}}"> wrapper.
		// "is-redundant" hides the chrome when the body already leads
		// with the title text (see RenderContext.TitleLinkState).
		"title_link_state":      ctx.TitleLinkState,
		"post_count":     fmt.Sprintf("%d", ctx.PostCount),
		"posts_count":    fmt.Sprintf("%d", ctx.PostCount), // stream uses "posts_count" plural in .site-stats
		"following_count": fmt.Sprintf("%d", ctx.FollowingCount),
		"followers_count": fmt.Sprintf("%d", ctx.FollowersCount),
		"site_bio":        ctx.SiteBio,

		// Pre-rendered HTML fragments
		"view_all_posts": ctx.ViewAllPostsLink,
		"avatar_html":    ctx.AvatarHTML,

		// Widget variables
		"author_domain":  ctx.AuthorDomain,
		"page_type":      ctx.PageType,
		"widget_version": ctx.WidgetVersion,

		// stream shape variables
		"canonical_url":             ctx.CanonicalURL,
		"focus_path_url":            ctx.FocusPathURL,
		"layout_right_state_class":  ctx.LayoutRightStateClass,
		"controller_url":            ctx.ControllerURL,
		"stream_shape_version":      ctx.StreamShapeVersion,
		// v4 metadata head-block (step-03/3.d). Empty for blog callers — v3
		// templates don't substitute these names.
		"og_title":       ctx.OGTitle,
		"og_description": ctx.OGDescription,
		"og_image_url":   ctx.OGImageURL,
		"json_ld":        ctx.JSONLD,
		"iso_published":  ctx.ISOPublished,
		"iso_modified":   ctx.ISOModified,
		"widget_home":      ctx.WidgetHome,
		"widget_handle":    ctx.WidgetHandle,
		"widget_host":      ctx.WidgetHost,
		"site_domain":      ctx.SiteDomain,

		// Tag variables
		"tag_name":     ctx.TagName,
		"tag_count":    fmt.Sprintf("%d", ctx.TagCount),
		"target_count": fmt.Sprintf("%d", ctx.TargetCount),

		// Comment-specific
		"in_reply_to_url":     ctx.InReplyToURL,
		"in_reply_to_title":   ctx.InReplyToTitle,
		"in_reply_to_domain":  ctx.InReplyToDomain,
		"in_reply_to_excerpt": ctx.InReplyToExcerpt,
		"root_post_url":       ctx.RootPostURL,
		"target_author":       ctx.TargetAuthor,
		"preview":             ctx.Preview,
	}

	// Replace all {{variable}} patterns.
	// Escape "{{" in substituted values to prevent template injection
	// (see escapedOpenBrace in sections.go).
	return variableRe.ReplaceAllStringFunc(template, func(match string) string {
		// Extract variable name from {{name}}
		name := match[2 : len(match)-2]
		if val, ok := vars[name]; ok {
			return strings.ReplaceAll(val, "{{", escapedOpenBrace)
		}
		// Unknown variables are left as-is
		return match
	})
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

// FormatHumanDateTime formats an ISO 8601 timestamp as the stream's
// per-entry meta-line shape: "April 23, 2026 · 4:12pm". Used by both the
// SSR'd stream artifact (focus + siblings) and the structured-query API
// (handlers_stream.go) so the SSR'd entries and the JS-appended ones share
// one canonical date format. v3 paths keep using FormatHumanDate (no time
// component) — v3 layouts have no per-entry meta line that needs it.
func FormatHumanDateTime(iso string) string {
	t, ok := parseISOTime(iso)
	if !ok {
		return iso
	}
	// R23 #17: recent timestamps render as relative ("just now",
	// "X minutes ago", "X hours ago") up to 10 hours back; older
	// stamps fall through to the absolute "April 23, 2026 · 4:12pm"
	// format. Mirrors the v3 owner-only dashboard's relative-time
	// treatment without diverging the absolute format.
	delta := time.Since(t)
	if delta >= 0 && delta < 10*time.Hour {
		switch {
		case delta < 60*time.Second:
			return "just now"
		case delta < 2*time.Minute:
			return "1 minute ago"
		case delta < time.Hour:
			return fmt.Sprintf("%d minutes ago", int(delta.Minutes()))
		case delta < 2*time.Hour:
			return "1 hour ago"
		default:
			return fmt.Sprintf("%d hours ago", int(delta.Hours()))
		}
	}
	return t.Format("January 2, 2006 · 3:04pm")
}

var dateYearRe = regexp.MustCompile(`,\s+\d{4}`)

// StripYear removes the ", YYYY" from a FormatHumanDateTime absolute date
// ("January 2, 2026 · 3:04pm" → "January 2 · 3:04pm"). Relative strings
// ("3 hours ago", "just now") pass through unchanged. The v4 stream drops
// the per-entry year — year-markers segment the stream, so repeating it on
// every row is redundant — and a shorter date keeps the comment-count column
// closer to the left. Display-only; the underlying datetime attr is intact.
func StripYear(human string) string {
	return dateYearRe.ReplaceAllString(human, "")
}

// FormatYear extracts the calendar year from an ISO 8601 timestamp. Used
// by the stream's year-marker rendering. Returns "" if the input is
// unparseable so callers can omit the year-marker rather than render a
// broken one.
func FormatYear(iso string) string {
	t, ok := parseISOTime(iso)
	if !ok {
		return ""
	}
	return t.Format("2006")
}

// parseISOTime accepts the timestamp shapes the rest of the codebase emits
// (RFC3339Nano, RFC3339, plain "2006-01-02T15:04:05Z", and date-only
// "2006-01-02"). Centralized here so FormatHumanDate / FormatHumanDateTime /
// FormatYear share one tolerance band.
func parseISOTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
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
		Siblings:        []PostData{},
	}
}
