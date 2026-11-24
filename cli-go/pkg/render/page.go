package render

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
	"html"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/cache"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/template"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// replyCacheMu serializes read-modify-write access to the on-disk
// reply-context cache (.polis/bundles/pub.polis.core/comments/
// reply-context-cache.json). Without serialization, concurrent
// LoadOrFetchReplyContext callers each load the same snapshot, add
// one entry, and write back — last writer wins, and the others'
// fresh entries are lost.
//
// The mutex is NOT held
// across the outbound HTTP fetch — only the cache load + write
// halves. The fetch itself is lock-free; R19-3 (singleflight, below)
// closes the duplicate-fetch race for same-URL callers separately.
// The invariant: every entry that gets written stays in the
// cache.
var replyCacheMu sync.Mutex

// ── R19-3: singleflight ─────────────────────────────────────────────
//
// replyFetchGroup de-duplicates concurrent calls to FetchReplyContextFull
// for the same URL. Without this, 5 parallel comment-thread renderers
// pointing at the same parent post each fire an independent HTTP
// request — wasting origin bandwidth and amplifying R19-1's
// write-write race (more concurrent writers = more lost entries
// pre-R19-1 fix; after R19-1 the entries survive but the duplicate
// fetches are still pure waste).
//
// In-place equivalent of golang.org/x/sync/singleflight — kept inline
// to avoid adding a dependency for one call site. The first caller
// for a key invokes fn(); subsequent callers wait on the wg and
// receive the same return values.

type replyFetchCall struct {
	wg  sync.WaitGroup
	val ReplyContextEntry
	ok  bool
}

type replyFetchGroupT struct {
	mu       sync.Mutex
	inflight map[string]*replyFetchCall
}

var replyFetchGroup = &replyFetchGroupT{inflight: make(map[string]*replyFetchCall)}

// Do executes fn() exactly once per `key` while one is in flight;
// concurrent callers with the same key receive the same result.
func (g *replyFetchGroupT) Do(key string, fn func() (ReplyContextEntry, bool)) (ReplyContextEntry, bool) {
	g.mu.Lock()
	if c, present := g.inflight[key]; present {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.ok
	}
	c := &replyFetchCall{}
	c.wg.Add(1)
	g.inflight[key] = c
	g.mu.Unlock()

	c.val, c.ok = fn()

	g.mu.Lock()
	delete(g.inflight, key)
	g.mu.Unlock()
	c.wg.Done()

	return c.val, c.ok
}

// ── R19-4: negative cache for failed fetches ────────────────────────
//
// In-memory map of URL → expiry-time for recent fetch failures. A
// comment-thread parent post whose origin is permanently down
// (deleted post, dead tenant, intermittent network) burns the full
// 3-second HTTP timeout on every render without this. The negative
// cache short-circuits subsequent fetches for the same URL until the
// entry expires.
//
// TTL is uniform at replyNegativeCacheTTL (15 min). The original
// design called for differentiated TTLs by error class (404 long,
// 5xx short, network shortest) but the FetchReplyContextFull
// signature returns only (entry, ok) — no error classification. A
// uniform 15min TTL accepts some retry overhead on flaky DNS while
// still avoiding the "every render re-fetches every dead URL" case.
// Differentiated TTLs can come later if observed retry waste justifies
// the API change.
//
// In-memory only — survives process restarts NOT. Survives long
// enough for one tenant's render bursts, which is all R19-4 was
// scoped to address.

const replyNegativeCacheTTL = 15 * time.Minute

var (
	replyNegativeCacheMu sync.Mutex
	replyNegativeCache   = make(map[string]time.Time) // URL → expiresAt
)

// replyNegativeCacheCheck reports whether the URL has a live negative
// entry. Stale entries are lazily evicted on access.
func replyNegativeCacheCheck(url string) bool {
	replyNegativeCacheMu.Lock()
	defer replyNegativeCacheMu.Unlock()
	if expiresAt, ok := replyNegativeCache[url]; ok {
		if time.Now().Before(expiresAt) {
			return true
		}
		delete(replyNegativeCache, url)
	}
	return false
}

// replyNegativeCachePut records the URL as recently-failed.
func replyNegativeCachePut(url string) {
	replyNegativeCacheMu.Lock()
	defer replyNegativeCacheMu.Unlock()
	replyNegativeCache[url] = time.Now().Add(replyNegativeCacheTTL)
}

// htmlCommentRE matches HTML comments across newlines. Used by
// StripHTMLComments to drop developer-doc comments embedded in the shape
// templates (DOM contract notes, protocol-marker explanations, design
// rationales) before the rendered page hits disk. These comments are
// useful when reading source but pure waste at runtime — on a typical
// rendered per-post page they account for ~70% of the uncompressed
// payload.
//
// Safety: this regex strips ALL HTML comments. Markdown-rendered code
// blocks emit `&lt;!-- ... --&gt;` (HTML-escaped), so user-authored
// `<!-- ... -->` in post bodies isn't matched. Bluemonday's UGC policy
// strips raw HTML comments before this stage anyway.
var htmlCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)

// StripHTMLComments removes HTML comments from rendered output for payload
// savings. Idempotent. Safe to call on any string; non-HTML content with no
// `<!--` substring is returned unchanged after the regex's fast path.
//
// EXCEPTION — developer trail markers: comments that open with the "keep" bang
// `<!--!` are preserved (the same convention minifiers use for license blocks).
// This lets the public stream template ship a small developer-facing trail
// marker into view-source — pointing inspectors at the codebase + docs — while
// every other layout comment (the ~70% payload) is still stripped. Keep
// preserved markers compact: they ship on every public page.
func StripHTMLComments(s string) string {
	if !strings.Contains(s, "<!--") {
		return s
	}
	return htmlCommentRE.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "<!--!") {
			return m
		}
		return ""
	})
}

// DefaultHTTPClient is an optional shared HTTP client for outbound requests
// (e.g., fetching reply context during rendering). Set by the calling application
// to enable connection pooling. If nil, a short-timeout client is created per call.
var DefaultHTTPClient *http.Client

// BlessedCacheFiller is the render-time read-through for a blessed-comment cache
// MISS, installed by the webapp at startup. It fetches the comment from its
// author and populates the isolated blessed cache THROUGH cache.Ingest (the same
// verify-and-gate + provenance plumbing Rosie uses), returning true when it wrote
// a cache entry (so LoadLocalCommentContent re-reads + renders it). It must NOT
// block page render unboundedly — it is single-flighted and negative-cached by
// the webapp. nil by default → CLI static render / offline returns a placeholder
// (empty string) on a miss. See LoadLocalCommentContent.
var BlessedCacheFiller func(site storage.Site, commentURL string) bool

// WidgetVersion is the single source of truth for the current polis widget version.
// Update this constant when widget.js changes. Theme snippets reference it via
// the {{widget_version}} template variable, so they never need manual version bumps.
//
// ── HANDBOOK TRAIL MARKER ─ foreign-site widget thread ─────────────────
// This constant is what every rendered tenant page references in its
// <script src="…/widget-{{widget_version}}.js"> tag (the v5 shape's
// frame.html, and the v3 and v4 shapes' templates).
// Bump this when the widget (polis.pub's widget.js) changes; polis.pub
// 302-redirects old version URLs to the current one for old HTML.
// Tour: https://github.com/vdibart/polis-cli/blob/main/docs/handbook/foreign-site-widget.md
// Map:  https://github.com/vdibart/polis-cli/blob/main/AGENTS.md
// ───────────────────────────────────────────────────────────────────────
const WidgetVersion = "1.6.2"

// PageConfig holds configuration for page rendering.
type PageConfig struct {
	// Site is the site being rendered: where it is, and what it is stored on.
	//
	// ⭐ THIS STRUCT WAS ALREADY THE RIGHT SHAPE and only the identity's type
	// was wrong — it held `DataDir string`, and five call sites inside this
	// package were already wrapping it in storage.At to reach the packages
	// that had converted.
	Site          storage.Site
	CLIThemesDir  string // CLI themes directory (fallback)
	BaseURL       string // Site base URL
	RenderMarkers bool   // Add snippet markers for editing
	// Source/output separation: when MountDir is set, rendered output goes to
	// mount dirs (e.g. "posts/") instead of alongside source files in content dirs.
	// Empty MountDir falls back to legacy behavior (write next to source).
	PostsSourceDir    string // e.g. "content/pub.polis.core/post"
	PostsMountDir     string // e.g. "posts"
	CommentsSourceDir string // e.g. "content/pub.polis.core/comment"
	CommentsMountDir  string // e.g. "comments"

	// Offline renders from what the site already holds and makes no network
	// call: a reply's context comes from the site's cache or its URL, and a
	// commenter's avatar falls back to the derived one. `polis branch` renders
	// this way, because it runs before the site has an address of its own.
	Offline bool
}

// PageRenderer renders polis pages using templates.
type PageRenderer struct {
	config     PageConfig
	engine     *template.Engine
	templates  *theme.Templates
	themeName  string
	shapeName  string            // "v3" (page) or "v4" (stream); drives RenderAll dispatch
	replyCache replyContextCache // lazily loaded cache for reply context
	v5theme    *theme.V5Theme    // the active theme's v5 definition (v5 shape only; nil: the polis default)
}

// RenderStats holds statistics from a render operation.
type RenderStats struct {
	PostsRendered    int
	PostsSkipped     int
	CommentsRendered int
	CommentsSkipped  int
	IndexGenerated   bool
	ArchiveGenerated bool
	TagsRendered     int
}

// NewPageRenderer creates a new page renderer.
func NewPageRenderer(cfg PageConfig) (*PageRenderer, error) {
	// Load active theme
	themeName, err := theme.GetActiveTheme(cfg.Site)
	if err != nil || themeName == "" {
		// ⚠️ A theme that could not be saved still renders: the name is the
		// stable fallback, so repeated renders agree until the registry is
		// repaired.
		themeName, err = theme.SelectRandomTheme(cfg.Site, cfg.CLIThemesDir)
		if err != nil && !errors.Is(err, theme.ErrThemeNotSaved) {
			return nil, fmt.Errorf("no theme available: %w", err)
		}
	}

	// Load active shape from registry. Defaults to v5, the shape every
	// tenant runs since the cutover: bundle.GetActiveShapeName returns "v5"
	// on a missing/empty registry field, and Medic's upgradeActiveShape
	// remediation flips any remaining v3 stamps to v5. This fallback covers the edge case of a
	// renderer constructed before Medic has touched a tenant (cold-
	// boot races, test fixtures without a registry).
	shapeName, err := bundle.GetActiveShapeName(cfg.Site)
	if err != nil || shapeName == "" {
		shapeName = "v5"
	}

	// Load templates
	templates, err := theme.LoadShape(cfg.Site, cfg.CLIThemesDir, shapeName, themeName)
	if err != nil {
		return nil, fmt.Errorf("failed to load theme: %w", err)
	}

	// Create template engine with markdown renderer
	engine := template.New(template.Config{
		Site:             cfg.Site,
		CLIThemesDir:     cfg.CLIThemesDir,
		ActiveTheme:      themeName,
		ActiveShape:      shapeName,
		RenderMarkers:    cfg.RenderMarkers,
		BaseURL:          cfg.BaseURL,
		MarkdownRenderer: MarkdownToHTML,
	})

	return &PageRenderer{
		config:    cfg,
		engine:    engine,
		templates: templates,
		themeName: themeName,
		shapeName: shapeName,
	}, nil
}

// sourceToMountPath maps a source-relative path to its mount-relative equivalent.
// If mount dirs are not configured, returns the original path (legacy behavior).
func (r *PageRenderer) sourceToMountPath(sourcePath, fileType string) string {
	var sourceDir, mountDir string
	switch fileType {
	case "post":
		sourceDir, mountDir = r.config.PostsSourceDir, r.config.PostsMountDir
	case "comment":
		sourceDir, mountDir = r.config.CommentsSourceDir, r.config.CommentsMountDir
	}
	if mountDir == "" || sourceDir == "" {
		return sourcePath // legacy: no separation
	}
	suffix := strings.TrimPrefix(sourcePath, sourceDir+"/")
	if suffix == sourcePath {
		return sourcePath // path doesn't match source prefix
	}
	return filepath.Join(mountDir, suffix)
}

// copyFile copies a file from src to dst, creating parent directories as needed.
// RenderFile renders a single file (post or comment) to HTML.
// Returns the rendered HTML, whether it was rendered (vs skipped), and any error.
func (r *PageRenderer) RenderFile(path string, fileType string, force bool) (string, bool, error) {
	// Build full paths — source is always in content dir
	mdKey := strings.TrimPrefix(filepath.ToSlash(path), "/")
	// Map source path to mount path for output
	mountPath := r.sourceToMountPath(path, fileType)
	htmlMountRel := strings.TrimSuffix(mountPath, ".md") + ".html"
	htmlKey := strings.TrimPrefix(filepath.ToSlash(htmlMountRel), "/")

	// Check if rendering is needed (unless force)
	if !force {
		mdInfo, err := r.config.Site.Store().Stat(context.Background(), mdKey)
		if err != nil {
			return "", false, fmt.Errorf("source file not found: %w", err)
		}

		htmlInfo, err := r.config.Site.Store().Stat(context.Background(), htmlKey)
		// ⚠️ Depends on ModTimes: the skip-if-unchanged check for a single
		// page. Without mtimes both are the zero time and every page
		// re-renders every time — correct, slower.
		if err == nil && htmlInfo.ModTime.After(mdInfo.ModTime) {
			// HTML is newer than MD, skip
			return "", false, nil
		}
	}

	// Read markdown file
	content, err := r.config.Site.Store().Get(context.Background(), mdKey)
	if err != nil {
		return "", false, fmt.Errorf("failed to read file: %w", err)
	}

	// Parse frontmatter
	fm := parseFrontmatter(string(content))
	body := stripFrontmatter(string(content))

	// Convert markdown to HTML
	htmlContent, err := MarkdownToHTML(body)
	if err != nil {
		return "", false, fmt.Errorf("failed to render markdown: %w", err)
	}

	// Build render context — use mount path for URLs and relative paths
	ctx := template.NewRenderContext()
	ctx.WidgetVersion = WidgetVersion
	ctx.Title = fm["title"]
	ctx.Content = htmlContent
	ctx.Published = fm["published"]
	ctx.PublishedHuman = template.FormatHumanDate(fm["published"])
	ctx.URL = r.buildURL(htmlMountRel)
	ctx.Version = fm["current-version"]
	if ctx.Version == "" {
		ctx.Version = fm["version"]
	}
	ctx.SignatureShort = template.TruncateSignature(fm["signature"], 16)

	// The work's own frozen terms, read back out of its frontmatter. Both
	// surfaces are empty strings when the work states none, so an unlicensed
	// site renders exactly as it did before.
	workTerms := license.ParseBlock(string(content))
	ctx.LicenseHead = license.HeadHTML(workTerms)
	ctx.LicenseNotice = licenseNoticeHTML(workTerms)

	// Site info — CSS/Home paths computed from mount path (shallower than source)
	ctx.SiteURL = r.config.BaseURL
	ctx.SiteTitle = r.getSiteTitle()
	ctx.CSSPath = theme.CalculateCSSPath(htmlMountRel)
	ctx.BaseCSSPath = strings.Replace(theme.CalculateCSSPath(htmlMountRel), "styles.css", "base.css", 1)
	ctx.HomePath = theme.CalculateHomePath(htmlMountRel)
	ctx.FaviconPath = strings.Replace(ctx.HomePath, "index.html", "favicon.svg", 1)
	ctx.AuthorName = r.getAuthorName()
	if ctx.AuthorName == "" {
		ctx.AuthorName = r.getAuthorDomain()
	}
	ctx.AuthorURL = r.config.BaseURL

	// Widget variables
	ctx.AuthorDomain = r.getAuthorDomain()
	// 2026-05-18: if author_name in .well-known/polis is set to the same
	// string as the domain (common for tenants that haven't set a
	// distinct display name — e.g. discover.polis.pub publishes
	// author_name="discover.polis.pub"), suppress the name field so the
	// rendered layout-left shows the handle once, not twice. CSS hides
	// .site-name:empty so the wrapper collapses cleanly. EqualFold + trim
	// guards against incidental whitespace and casing differences in the
	// stored author_name.
	if strings.EqualFold(strings.TrimSpace(ctx.AuthorName), strings.TrimSpace(ctx.AuthorDomain)) {
		ctx.AuthorName = ""
	}
	ctx.PageType = fileType // "post" or "comment"

	// Avatar for site header
	ctx.AvatarHTML = r.buildAvatarHTML()

	// Comment-specific fields
	if fileType == "comment" {
		ctx.InReplyToURL = fm["in_reply_to"]
		if ctx.InReplyToURL == "" {
			// Try nested format
			ctx.InReplyToURL = parseNestedField(string(content), "in-reply-to", "url")
		}
		ctx.RootPostURL = fm["root_post"]
		if ctx.RootPostURL == "" {
			ctx.RootPostURL = parseNestedField(string(content), "in-reply-to", "root-post")
		}
		// Convert .md URL to .html for the link target
		if strings.HasSuffix(ctx.InReplyToURL, ".md") {
			ctx.InReplyToURL = strings.TrimSuffix(ctx.InReplyToURL, ".md") + ".html"
		}
		if strings.HasSuffix(ctx.RootPostURL, ".md") {
			ctx.RootPostURL = strings.TrimSuffix(ctx.RootPostURL, ".md") + ".html"
		}
		// Resolve reply context: fetch from remote (with cache) or derive from URL
		if ctx.InReplyToURL != "" {
			if r.replyCache == nil {
				r.replyCache = loadReplyContextCache(r.config.Site)
			}
			ctx.InReplyToTitle, ctx.InReplyToExcerpt, ctx.InReplyToDomain = resolveReplyContext(
				r.config.Site, ctx.InReplyToURL, r.replyCache, r.config.Offline,
			)
		}
		// R23-1: escape the commenter-controlled / remotely-fetched reply-context
		// fields before they reach the (non-escaping) template engine. Done here,
		// after every functional use of the URLs (the .md→.html rewrite above and
		// the fetch/cache lookup both need the raw URL).
		sanitizeReplyContextForRender(ctx)
	}

	// Load blessed comments and recent posts for post pages
	if fileType == "post" {
		blessedComments, _ := r.loadBlessedCommentsForPost(path)
		ctx.BlessedComments = blessedComments
		ctx.BlessedCount = len(blessedComments)

		// Load recent posts for "More from this site" section
		if posts, _, err := r.loadPublicIndex(); err == nil {
			if len(posts) > 5 {
				ctx.RecentPosts = posts[:5]
			} else {
				ctx.RecentPosts = posts
			}
		}
	}

	// Select template
	var tmpl string
	switch fileType {
	case "post":
		tmpl = r.templates.Post
	case "comment":
		tmpl = r.templates.Comment
	default:
		tmpl = r.templates.Post
	}

	// Render template
	rendered, err := r.engine.Render(tmpl, ctx)
	if err != nil {
		return "", false, fmt.Errorf("failed to render template: %w", err)
	}
	rendered = StripHTMLComments(rendered)

	// Write HTML output to mount dir

	if err := r.config.Site.Store().Put(context.Background(), htmlKey, []byte(rendered), storage.Rebuildable()); err != nil {
		return "", false, fmt.Errorf("failed to write output: %w", err)
	}

	return rendered, true, nil
}

// RenderIndex generates the index.html page.
func (r *PageRenderer) RenderIndex() error {
	// Load posts and comments from public.jsonl
	posts, comments, err := r.loadPublicIndex()
	if err != nil {
		return fmt.Errorf("failed to load public index: %w", err)
	}

	// Build render context
	ctx := template.NewRenderContext()
	ctx.WidgetVersion = WidgetVersion
	ctx.SiteURL = r.config.BaseURL
	ctx.SiteTitle = r.getSiteTitle()
	ctx.CSSPath = "styles.css"
	ctx.BaseCSSPath = "base.css"
	ctx.HomePath = "index.html"
	ctx.FaviconPath = "favicon.svg"
	ctx.AuthorName = r.getAuthorName()
	if ctx.AuthorName == "" {
		ctx.AuthorName = r.getAuthorDomain()
	}
	ctx.AuthorURL = r.config.BaseURL
	ctx.PostCount = len(posts)
	ctx.CommentCount = len(comments)
	ctx.Posts = posts
	ctx.Comments = comments
	ctx.AuthorDomain = r.getAuthorDomain()
	// Suppress duplicate name when author_name == domain (see comment on
	// the per-post render path above for the rationale).
	if strings.EqualFold(strings.TrimSpace(ctx.AuthorName), strings.TrimSpace(ctx.AuthorDomain)) {
		ctx.AuthorName = ""
	}
	ctx.PageType = "index"

	// Load following data (non-fatal if missing)
	followFile, err := following.LoadSite(r.config.Site)
	if err == nil && followFile.Count() > 0 {
		ctx.FollowingCount = followFile.Count()
		for _, entry := range followFile.All() {
			domain := strings.TrimPrefix(entry.URL, "https://")
			domain = strings.TrimPrefix(domain, "http://")
			domain = strings.TrimSuffix(domain, "/")
			ctx.Following = append(ctx.Following, template.FollowingData{
				URL:        entry.URL,
				Domain:     domain,
				AuthorName: entry.AuthorName,
				SiteTitle:  entry.SiteTitle,
			})
		}
	}

	// Build avatar HTML from .well-known/polis avatar config
	ctx.AvatarHTML = r.buildAvatarHTML()

	// Set recent posts (first 10)
	if len(posts) > 10 {
		ctx.RecentPosts = posts[:10]
		ctx.ViewAllPostsLink = fmt.Sprintf(`<a href="posts/" class="view-all">View all %d posts &rarr;</a>`, len(posts))
	} else {
		ctx.RecentPosts = posts
	}

	// Set recent comments (first 10)
	if len(comments) > 10 {
		ctx.RecentComments = comments[:10]
	} else {
		ctx.RecentComments = comments
	}

	// Render template
	rendered, err := r.engine.Render(r.templates.Index, ctx)
	if err != nil {
		return fmt.Errorf("failed to render index template: %w", err)
	}
	rendered = StripHTMLComments(rendered)

	// Write output
	indexKey := "index.html"
	if err := r.config.Site.Store().Put(context.Background(), indexKey, []byte(rendered), storage.Rebuildable()); err != nil {
		return fmt.Errorf("failed to write index.html: %w", err)
	}

	return nil
}

// RenderArchive generates the posts/index.html archive page.
// No-ops silently if the theme doesn't have a posts.html template.
func (r *PageRenderer) RenderArchive() error {
	if r.templates.Archive == "" {
		return nil
	}

	// Load posts from public.jsonl
	posts, _, err := r.loadPublicIndex()
	if err != nil {
		return fmt.Errorf("failed to load public index: %w", err)
	}

	// Build render context with all posts (unlimited)
	ctx := template.NewRenderContext()
	ctx.WidgetVersion = WidgetVersion
	ctx.SiteURL = r.config.BaseURL
	ctx.SiteTitle = r.getSiteTitle()
	ctx.CSSPath = "../styles.css"
	ctx.BaseCSSPath = "../base.css"
	ctx.HomePath = "../index.html"
	ctx.FaviconPath = "../favicon.svg"
	ctx.AuthorName = r.getAuthorName()
	if ctx.AuthorName == "" {
		ctx.AuthorName = r.getAuthorDomain()
	}
	ctx.AuthorURL = r.config.BaseURL
	ctx.PostCount = len(posts)
	ctx.Posts = posts
	ctx.AuthorDomain = r.getAuthorDomain()
	// Suppress duplicate name when author_name == domain.
	if strings.EqualFold(strings.TrimSpace(ctx.AuthorName), strings.TrimSpace(ctx.AuthorDomain)) {
		ctx.AuthorName = ""
	}
	ctx.PageType = "index"
	ctx.AvatarHTML = r.buildAvatarHTML()

	// Load following count for archive page stats
	if followFile, err := following.LoadSite(r.config.Site); err == nil {
		ctx.FollowingCount = followFile.Count()
	}

	// Render template
	rendered, err := r.engine.Render(r.templates.Archive, ctx)
	if err != nil {
		return fmt.Errorf("failed to render archive template: %w", err)
	}
	rendered = StripHTMLComments(rendered)

	// Write output to mount dir (posts/index.html) or legacy content dir
	// ⭐ A KEY, so the write below goes through the store. ⚠️ The mount dir is
	// user-configurable, so it is normalised to slashes and de-rooted rather
	// than assumed clean.
	archiveDir := strings.TrimPrefix(filepath.ToSlash(r.config.PostsMountDir), "/")
	if r.config.PostsMountDir == "" {
		archiveDir = "content/pub.polis.core/post"
	}

	archiveKey := archiveDir + "/index.html"
	if err := r.config.Site.Store().Put(context.Background(), archiveKey, []byte(rendered), storage.Rebuildable()); err != nil {
		return fmt.Errorf("failed to write posts/index.html: %w", err)
	}

	return nil
}

// tagFileData is a local type for parsing tag JSON files (avoids importing tag package).
type tagFileData struct {
	Tag     string          `json:"tag"`
	Targets []tagTargetData `json:"targets"`
	Updated string          `json:"updated"`
}

// tagTargetData represents a single target within a tag file.
type tagTargetData struct {
	URI   string `json:"uri"`
	Added string `json:"added"`
}

// RenderTags renders tag pages and a tag index page.
// Returns the number of tags rendered, or 0 if no tag template is available.
func (r *PageRenderer) RenderTags() (int, error) {
	if r.templates.Tag == "" || r.templates.TagIndex == "" {
		return 0, nil
	}

	tagsDir := "content/pub.polis.core/tag"
	entries, err := listSiteDir(r.config.Site, tagsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read tags directory: %w", err)
	}

	var allTags []template.TagData
	count := 0

	for _, entry := range entries {
		name := pathpkg.Base(entry.Key)
		if entry.IsDir || !strings.HasSuffix(name, ".json") {
			continue
		}

		// ⛔ THROUGH THE STORE. This was ReadAtPath(filepath.Join(tagsDir, name))
		// after tagsDir became a KEY, so it resolved against the process
		// working directory instead of the site — and the `continue` below
		// swallowed the error, so every tag silently vanished from the index
		// with no test failing. ⚠️ A path and a key are both `string`: the
		// compiler cannot tell them apart, which is exactly why this needs a
		// test rather than care.
		data, err := r.config.Site.Store().Get(context.Background(), tagsDir+"/"+name)
		if err != nil {
			continue
		}

		var tf tagFileData
		if err := json.Unmarshal(data, &tf); err != nil {
			continue
		}

		// Convert targets for template
		var targets []template.TagTargetData
		for _, t := range tf.Targets {
			targets = append(targets, template.TagTargetData{
				URI:   t.URI,
				Added: t.Added,
			})
		}

		// ⛔ The page is written at tags/<tag>/index.html from the file's own
		// "tag" field, so a planted or cloned tag file naming ".." overwrote
		// the root index.html. Only a name that is already a normalised tag
		// name gets a page; anything else is skipped.
		if n, nerr := tag.NormalizeTagName(tf.Tag); nerr != nil || n != tf.Tag {
			log.Printf("[!] render: skipping tag file %s: %q is not a tag name", name, tf.Tag)
			continue
		}

		// Render individual tag page at tags/<name>/index.html
		tagPagePath := filepath.Join("tags", tf.Tag, "index.html")
		ctx := template.NewRenderContext()
		ctx.WidgetVersion = WidgetVersion
		ctx.TagName = tf.Tag
		ctx.TargetCount = len(tf.Targets)
		ctx.Targets = targets
		ctx.SiteTitle = r.getSiteTitle()
		ctx.SiteURL = r.config.BaseURL
		ctx.CSSPath = "../../styles.css"
		ctx.BaseCSSPath = "../../base.css"
		ctx.HomePath = "../../index.html"
		ctx.FaviconPath = "../../favicon.svg"
		ctx.AuthorDomain = r.getAuthorDomain()
		ctx.PageType = "tag"

		rendered, err := r.engine.Render(r.templates.Tag, ctx)
		if err != nil {
			return count, fmt.Errorf("failed to render tag page for %q: %w", tf.Tag, err)
		}
		rendered = StripHTMLComments(rendered)

		outKey := strings.TrimPrefix(filepath.ToSlash(tagPagePath), "/")
		if err := r.config.Site.Store().Put(context.Background(), outKey, []byte(rendered), storage.Rebuildable()); err != nil {
			return count, fmt.Errorf("failed to write tag page: %w", err)
		}

		count++

		// Collect for index
		allTags = append(allTags, template.TagData{
			TagName: tf.Tag,
			Count:   len(tf.Targets),
			Updated: tf.Updated,
		})
	}

	if count == 0 {
		return 0, nil
	}

	// Render tag index page at tags/index.html
	ctx := template.NewRenderContext()
	ctx.WidgetVersion = WidgetVersion
	ctx.Tags = allTags
	ctx.TagCount = len(allTags)
	ctx.SiteTitle = r.getSiteTitle()
	ctx.SiteURL = r.config.BaseURL
	ctx.CSSPath = "../styles.css"
	ctx.BaseCSSPath = "../base.css"
	ctx.HomePath = "../index.html"
	ctx.FaviconPath = "../favicon.svg"
	ctx.AuthorDomain = r.getAuthorDomain()
	ctx.PageType = "tag-index"

	rendered, err := r.engine.Render(r.templates.TagIndex, ctx)
	if err != nil {
		return count, fmt.Errorf("failed to render tag index: %w", err)
	}
	rendered = StripHTMLComments(rendered)

	indexKey := "tags/index.html"
	if err := r.config.Site.Store().Put(context.Background(), indexKey, []byte(rendered), storage.Rebuildable()); err != nil {
		return count, fmt.Errorf("failed to write tag index: %w", err)
	}

	return count, nil
}

// RenderAll renders all posts and comments, and generates the index.
func (r *PageRenderer) RenderAll(force bool) (*RenderStats, error) {
	// stream-shape tenants render through the stream shell: a per-post page with a
	// focus article + 2-4 sibling excerpts, plus an index.html that focuses
	// the most-recent post. v3 keeps the legacy per-post/archive/tag pipeline.
	if r.shapeName == "v4" {
		return r.renderStreamAll(force)
	}
	// ⚠️ v5 is named explicitly: any shape this does not name falls through
	// to the v3 pipeline below.
	if r.shapeName == "v5" {
		return r.renderV5All(force)
	}

	stats := &RenderStats{}

	// Copy CSS first
	if err := theme.CopyCSS(r.config.Site, r.config.CLIThemesDir, r.themeName); err != nil {
		return nil, fmt.Errorf("failed to copy CSS: %w", err)
	}
	// Copy base CSS (shared structural styles)
	if err := theme.CopyBaseCSS(r.config.Site, r.config.CLIThemesDir); err != nil {
		return nil, fmt.Errorf("failed to copy base CSS: %w", err)
	}

	// Find all posts
	postsDir := "content/pub.polis.core/post"
	// ⭐ The key IS the relative path, so the filepath.Rel this loop used to do
	// is gone rather than translated. A missing directory lists as empty, which
	// is what the old !os.IsNotExist guard arrived at by a longer route.
	postEntries, err := listSiteRecursive(r.config.Site, postsDir)
	if err != nil {
		return nil, err
	}
	for _, e := range postEntries {
		// SkipDir has no equivalent once everything is enumerated, so the
		// .versions skip becomes a key filter.
		if e.IsDir || !strings.HasSuffix(e.Key, ".md") || isVersionsKey(e.Key) {
			continue
		}
		_, rendered, err := r.RenderFile(e.Key, "post", force)
		if err != nil {
			return nil, fmt.Errorf("failed to render %s: %w", e.Key, err)
		}
		if rendered {
			stats.PostsRendered++
		} else {
			stats.PostsSkipped++
		}
	}

	// Find all comments
	commentsDir := "content/pub.polis.core/comment"
	// ⭐ The key IS the relative path, so the filepath.Rel this loop used to do
	// is gone rather than translated. A missing directory lists as empty, which
	// is what the old !os.IsNotExist guard arrived at by a longer route.
	commentEntries, err := listSiteRecursive(r.config.Site, commentsDir)
	if err != nil {
		return nil, err
	}
	for _, e := range commentEntries {
		// SkipDir has no equivalent once everything is enumerated, so the
		// .versions skip becomes a key filter.
		if e.IsDir || !strings.HasSuffix(e.Key, ".md") || isVersionsKey(e.Key) {
			continue
		}
		_, rendered, err := r.RenderFile(e.Key, "comment", force)
		if err != nil {
			return nil, fmt.Errorf("failed to render %s: %w", e.Key, err)
		}
		if rendered {
			stats.CommentsRendered++
		} else {
			stats.CommentsSkipped++
		}
	}

	// Copy artifacts to mount dirs
	if r.config.CommentsMountDir != "" {
		// Non-fatal, as before: no blessed list means nothing to copy, and a
		// failed read or write leaves the mount's copy as it was.
		ctx := context.Background()
		if data, err := r.config.Site.Store().Get(ctx, "content/pub.polis.core/comment/blessed.json"); err == nil {
			dst := strings.TrimPrefix(filepath.ToSlash(r.config.CommentsMountDir), "/") + "/blessed.json"
			_ = r.config.Site.Store().Put(ctx, dst, data)
		}
	}

	// Generate index
	if err := r.RenderIndex(); err != nil {
		return nil, fmt.Errorf("failed to render index: %w", err)
	}
	stats.IndexGenerated = true

	// Generate archive page
	if err := r.RenderArchive(); err != nil {
		return nil, fmt.Errorf("failed to render archive: %w", err)
	}
	if r.templates.Archive != "" {
		stats.ArchiveGenerated = true
	}

	// Generate tag pages
	tagsRendered, err := r.RenderTags()
	if err != nil {
		return nil, fmt.Errorf("failed to render tags: %w", err)
	}
	stats.TagsRendered = tagsRendered

	// Licence surfaces: the terms page, the profile explanation, robots.txt,
	// and rsl.xml — all generated from the signed licence. No-op for a site
	// that has stated no terms.
	if err := r.RenderLicenseSurfaces(); err != nil {
		return nil, fmt.Errorf("failed to render licence surfaces: %w", err)
	}

	// Persist reply context cache if it was populated during comment rendering
	if r.replyCache != nil && len(r.replyCache) > 0 {
		saveReplyContextCache(r.config.Site, r.replyCache)
	}

	return stats, nil
}

// loadPublicIndex loads posts and comments from public.jsonl.
func (r *PageRenderer) loadPublicIndex() ([]template.PostData, []template.CommentData, error) {
	entries, err := metadata.LoadPublicIndex(r.config.Site)
	if err != nil {
		return nil, nil, err
	}

	// Build a map of post path -> blessed comment count
	commentCountMap := make(map[string]int)
	if bc, err := metadata.LoadBlessedComments(r.config.Site); err == nil {
		for _, pc := range bc.Comments {
			commentCountMap[pc.Post] = len(pc.Blessed)
		}
	}

	var posts []template.PostData
	var comments []template.CommentData

	for _, entry := range entries {
		if strings.HasPrefix(entry.Path, "posts/") || entry.Type == "post" {
			// Map source path to mount path, then convert .md to .html for URL
			mountPath := r.sourceToMountPath(entry.Path, "post")
			htmlPath := strings.TrimSuffix(mountPath, ".md") + ".html"

			// Look up blessed comment count (try multiple path forms)
			count := commentCountMap[entry.Path]
			if count == 0 {
				// Try flexible matching (handles mount path vs source content path)
				for k, v := range commentCountMap {
					if metadata.MatchesPostPath(k, entry.Path) {
						count = v
						break
					}
				}
			}

			// Generate excerpt from post body — read from source path
			var excerpt string
			mdKey := strings.TrimPrefix(filepath.ToSlash(entry.Path), "/")
			if data, err := r.config.Site.Store().Get(context.Background(), mdKey); err == nil {
				body := stripFrontmatter(string(data))
				excerpt = MarkdownToPlainText(body, 200)
			}

			posts = append(posts, template.PostData{
				URL:            "/" + htmlPath,
				Title:          entry.Title,
				Excerpt:        excerpt,
				Published:      entry.Published,
				PublishedHuman: template.FormatHumanDate(entry.Published),
				CommentCount:   count,
			})
		} else if strings.HasPrefix(entry.Path, "comments/") || entry.Type == "comment" {
			mountPath := r.sourceToMountPath(entry.Path, "comment")
			htmlPath := strings.TrimSuffix(mountPath, ".md") + ".html"
			inReplyToURL := ""
			if entry.InReplyTo != nil {
				inReplyToURL = entry.InReplyTo.URL
			}
			comments = append(comments, template.CommentData{
				URL: "/" + htmlPath,
				// R23-1: TargetAuthor + Preview land unescaped in comment-item.html
				// ({{target_author}}, {{preview}}); the engine doesn't HTML-escape.
				// entry.Title can be remote/blessed content, so escape both here.
				TargetAuthor:   html.EscapeString(extractDomain(inReplyToURL)),
				Published:      entry.Published,
				PublishedHuman: template.FormatHumanDate(entry.Published),
				Preview:        html.EscapeString(truncateText(entry.Title, 100)), // Use title as preview
			})
		}
	}

	// Reverse order (newest first)
	for i, j := 0, len(posts)-1; i < j; i, j = i+1, j-1 {
		posts[i], posts[j] = posts[j], posts[i]
	}
	for i, j := 0, len(comments)-1; i < j; i, j = i+1, j-1 {
		comments[i], comments[j] = comments[j], comments[i]
	}

	return posts, comments, nil
}

// loadBlessedCommentsForPost loads blessed comments for a specific post.
func (r *PageRenderer) loadBlessedCommentsForPost(postPath string) ([]template.BlessedCommentData, error) {
	// Load blessed comments for this specific post
	comments, err := metadata.GetBlessedCommentsForPost(r.config.Site, postPath)
	if err != nil {
		return nil, err
	}

	var results []template.BlessedCommentData

	for _, comment := range comments {
		// Try to load local comment content
		content := r.loadLocalCommentContent(comment.URL)

		// Convert .md to .html in the URL for browser-facing links
		commentURL := comment.URL
		if strings.HasSuffix(commentURL, ".md") {
			commentURL = strings.TrimSuffix(commentURL, ".md") + ".html"
		}

		// Commenter avatar — fetched per author with hue fallback, matching
		// the v4 comment-thread card design (mockup 16). Renders at 28px to
		// match the stream's commenter-avatar size; floated right inside the
		// card via the blessed-comment.html template + the .comment-attached
		// scoping in stream.css.
		authorDomain := extractDomain(comment.URL)
		var avatarCfg AvatarConfig
		ok := false
		if !r.config.Offline {
			avatarCfg, ok = LoadOrFetchAuthorAvatar(authorDomain)
		}
		if !ok || avatarCfg.BG == "" {
			avatarCfg = FallbackAvatarConfig(authorDomain)
		}
		initial := ""
		if authorDomain != "" {
			initial = strings.ToUpper(authorDomain[:1])
		}
		avatarHTML := AvatarHTML(avatarCfg, initial, 28)

		results = append(results, template.BlessedCommentData{
			URL:              commentURL,
			AuthorName:       authorDomain,
			AuthorDomain:     authorDomain,
			AuthorAvatarHTML: avatarHTML,
			Published:        comment.BlessedAt,
			// v4 canonical comment cards share the stream meta-line date
			// shape ("May 25 · 10:00pm" / relative for recent) — NOT v3's
			// year-bearing "May 25, 2026". StripYear matches the post
			// meta-line above it; FormatHumanDateTime adds the time + the
			// recent-relative form so the comment date reads like every
			// other v4 timestamp on the page.
			PublishedHuman: template.StripYear(template.FormatHumanDateTime(comment.BlessedAt)),
			Content:        content,
		})
	}

	return results, nil
}

// loadLocalCommentContent resolves a blessed comment URL to rendered HTML from
// the isolated blessed-comment cache. Returns empty string if not available.
func (r *PageRenderer) loadLocalCommentContent(commentURL string) string {
	return LoadLocalCommentContent(r.config.Site, commentURL)
}

// LoadLocalCommentContent renders a foreign blessed comment for inline display,
// reading it ONLY from the isolated blessed-comment cache
// (.polis/ds/<ds>/pub.polis.core/cache/blessed/<author>/…) through the same
// goldmark + bluemonday pipeline as per-post rendering. It NEVER reads a public
// path (comments/ or content/pub.polis.core/comment/): serving one tenant's
// comment from another tenant's public tree is exactly the Defect-3 violation
// the cache exists to prevent.
//
// On a cache MISS it read-throughs via BlessedCacheFiller (installed by the
// webapp), which fetches the comment from its author and populates the cache via
// the SAME cache.Ingest plumbing Rosie uses — so render-created entries carry a
// proper provenance sidecar and are never rogue to Rosie's integrity/GC. When
// the filler is unset (CLI static render / offline), a miss is a graceful empty
// string. Callers MUST treat "" as "no inline content available" and degrade.
func LoadLocalCommentContent(site storage.Site, commentURL string) string {
	data, _, ok := cache.FindBlessed(site, commentURL)
	if !ok && BlessedCacheFiller != nil && BlessedCacheFiller(site, commentURL) {
		data, _, ok = cache.FindBlessed(site, commentURL)
	}
	if !ok {
		return ""
	}
	return renderCommentMarkdownBody(data)
}

// LoadOwnCommentContent renders the SITE OWNER's OWN comment for inline display,
// reading it from the owner's local public path (the comments/ mount or the
// content/pub.polis.core/comment/ source). This is the owner's canonical content
// living in the owner's own tree — reading it here is NOT a foreign-content
// violation. Callers MUST use this only for comments the owner authored (author
// domain == the owner's domain); FOREIGN blessed comments go through
// LoadLocalCommentContent (isolated cache only, never a public path). Returns ""
// when not found, rendered through the same goldmark + bluemonday pipeline.
func LoadOwnCommentContent(site storage.Site, commentURL string) string {
	rel := polisurl.CommentURLToContentRel(commentURL)
	if rel == "" {
		return ""
	}
	suffix := strings.TrimPrefix(rel, "content/pub.polis.core/comment/") // "<date>/<id>.md"
	for _, key := range []string{
		"comments/" + suffix,
		"content/pub.polis.core/comment/" + suffix,
	} {
		// ⛔ Proven inside the comment trees before any read.
		if !storage.Within(key, "comments") && !storage.Within(key, "content/pub.polis.core/comment") {
			continue
		}
		if data, err := site.Store().Get(context.Background(), key); err == nil {
			return renderCommentMarkdownBody(data)
		}
	}
	return ""
}

// renderCommentMarkdownBody strips frontmatter and renders a comment .md body to
// sanitized HTML, returning the raw body on render error (best-effort display).
func renderCommentMarkdownBody(data []byte) string {
	body := stripFrontmatter(string(data))
	html, err := MarkdownToHTML(body)
	if err != nil {
		return body
	}
	return html
}

// getSiteTitle returns the site title from .well-known/polis.
func (r *PageRenderer) getSiteTitle() string {
	wkKey := ".well-known/polis"
	data, err := r.config.Site.Store().Get(context.Background(), wkKey)
	if err != nil {
		return r.config.BaseURL
	}

	var wk struct {
		SiteTitle string `json:"site_title"`
		BaseURL   string `json:"base_url"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return r.config.BaseURL
	}

	if wk.SiteTitle != "" {
		return wk.SiteTitle
	}
	return wk.BaseURL
}

// getAuthorName returns the author name from .well-known/polis.
func (r *PageRenderer) getAuthorName() string {
	wkKey := ".well-known/polis"
	data, err := r.config.Site.Store().Get(context.Background(), wkKey)
	if err != nil {
		return ""
	}

	var wk struct {
		AuthorName string `json:"author_name"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return ""
	}

	return wk.AuthorName
}

// getAuthorDomain returns the site domain, extracted from the BaseURL config.
func (r *PageRenderer) getAuthorDomain() string {
	return extractDomain(r.config.BaseURL)
}

// avatarPatterns maps pattern names to SVG generator functions.
// These match the patterns in nav.js and app.js.
var avatarPatterns = map[string]func(color string) string{
	"rings": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='14' cy='14' r='10' fill='none' stroke='%s' stroke-width='1.5'/><circle cx='14' cy='14' r='5' fill='none' stroke='%s' stroke-width='1'/></svg>`, c, c)
	},
	"cross": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='4' y1='4' x2='24' y2='24' stroke='%s' stroke-width='1.5'/><line x1='24' y1='4' x2='4' y2='24' stroke='%s' stroke-width='1.5'/></svg>`, c, c)
	},
	"grid": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='9' y1='0' x2='9' y2='28' stroke='%s' stroke-width='0.8'/><line x1='19' y1='0' x2='19' y2='28' stroke='%s' stroke-width='0.8'/><line x1='0' y1='9' x2='28' y2='9' stroke='%s' stroke-width='0.8'/><line x1='0' y1='19' x2='28' y2='19' stroke='%s' stroke-width='0.8'/></svg>`, c, c, c, c)
	},
	"dots": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='7' cy='7' r='2' fill='%s'/><circle cx='21' cy='7' r='2' fill='%s'/><circle cx='14' cy='14' r='2' fill='%s'/><circle cx='7' cy='21' r='2' fill='%s'/><circle cx='21' cy='21' r='2' fill='%s'/></svg>`, c, c, c, c, c)
	},
	"stripes": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='-2' y1='6' x2='6' y2='-2' stroke='%s' stroke-width='1.5'/><line x1='5' y1='13' x2='13' y2='5' stroke='%s' stroke-width='1.5'/><line x1='12' y1='20' x2='20' y2='12' stroke='%s' stroke-width='1.5'/><line x1='19' y1='27' x2='27' y2='19' stroke='%s' stroke-width='1.5'/><line x1='26' y1='34' x2='34' y2='26' stroke='%s' stroke-width='1.5'/></svg>`, c, c, c, c, c)
	},
	"diamond": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><polygon points='14,4 24,14 14,24 4,14' fill='none' stroke='%s' stroke-width='1.5'/></svg>`, c)
	},
	"halves": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><rect x='0' y='14' width='28' height='14' fill='%s' opacity='0.4'/></svg>`, c)
	},
}

// AvatarConfig is a site's avatar customization. Sourced from the author's
// .well-known/polis "avatar" block today (moving into the content bundle
// later) — callers needing a REMOTE author's avatar should go through the
// LoadOrFetchAuthorAvatar seam rather than reading .well-known directly. The
// zero value (empty BG) means "no custom avatar" → default initial.
type AvatarConfig struct {
	BG           string `json:"bg"`
	FG           string `json:"fg"`
	Border       string `json:"border"`
	BorderW      int    `json:"border_w"`
	Pattern      string `json:"pattern"`
	PatternColor string `json:"pattern_color"`
}

// AvatarHTML renders a <span class="avatar-initial"> for the given avatar
// config. When cfg.BG is set it applies bg/fg/border and (if present) the SVG
// pattern as a base64 data-URI background; a pattern blanks the initial
// (matching nav.js/app.js). When cfg.BG is empty it returns the default
// unstyled initial (theme CSS styles it). sizePx > 0 sets width/height/
// line-height inline so one config renders at any size (stream chip vs
// settings preview); sizePx == 0 leaves sizing to CSS (the local nav avatar).
// initial is the already-derived display glyph — the caller chooses its source.
//
// This is the single source of avatar markup, shared by the local site avatar
// (buildAvatarHTML) and per-author stream enrichment. The JS renderer
// (shapes/v4/stream.js buildAvatar) mirrors this output; keep them in step.
func AvatarHTML(cfg AvatarConfig, initial string, sizePx int) string {
	sizeStyle := ""
	if sizePx > 0 {
		sizeStyle = fmt.Sprintf("width:%dpx;height:%dpx;line-height:%dpx;", sizePx, sizePx, sizePx)
	}

	// No custom avatar: default unstyled initial (+ optional inline size).
	if cfg.BG == "" {
		if sizeStyle != "" {
			return fmt.Sprintf(`<span class="avatar-initial" style="%s">%s</span>`,
				sizeStyle, html.EscapeString(initial))
		}
		return fmt.Sprintf(`<span class="avatar-initial">%s</span>`, html.EscapeString(initial))
	}

	// Build inline style matching nav.js buildAvatarStyle.
	style := fmt.Sprintf("background-color:%s;color:%s;",
		html.EscapeString(cfg.BG), html.EscapeString(cfg.FG))

	if cfg.Border != "" && cfg.BorderW > 0 {
		style += fmt.Sprintf("border:%dpx solid %s;", cfg.BorderW, html.EscapeString(cfg.Border))
	}

	// Pattern support.
	hasPattern := false
	if cfg.Pattern != "" && cfg.Pattern != "none" && cfg.PatternColor != "" {
		if gen, ok := avatarPatterns[cfg.Pattern]; ok {
			svg := gen(cfg.PatternColor)
			b64 := base64.StdEncoding.EncodeToString([]byte(svg))
			style += fmt.Sprintf("background-image:url(data:image/svg+xml;base64,%s);background-size:cover;", b64)
			hasPattern = true
		}
	}

	// Hide the initial when a pattern is set (matching nav.js behavior).
	displayInitial := html.EscapeString(initial)
	if hasPattern {
		displayInitial = ""
	}

	return fmt.Sprintf(`<span class="avatar-initial" style="%s%s">%s</span>`,
		sizeStyle, style, displayInitial)
}

// buildAvatarHTML returns pre-rendered HTML for the LOCAL site's avatar, from
// the .well-known/polis avatar config (or a default initial-letter avatar).
// Thin wrapper over AvatarHTML; sizePx 0 leaves sizing to theme CSS.
func (r *PageRenderer) buildAvatarHTML() string {
	authorName := r.getAuthorName()
	if authorName == "" {
		authorName = r.getAuthorDomain()
	}

	initial := "?"
	if runes := []rune(authorName); len(runes) > 0 {
		initial = string(runes[0])
	}

	return AvatarHTML(r.loadLocalAvatarConfig(), initial, 0)
}

// loadLocalAvatarConfig reads the serving site's avatar block from
// .well-known/polis. Returns the zero AvatarConfig when absent or unparseable.
func (r *PageRenderer) loadLocalAvatarConfig() AvatarConfig {
	wkKey := ".well-known/polis"
	data, err := r.config.Site.Store().Get(context.Background(), wkKey)
	if err != nil {
		return AvatarConfig{}
	}
	var wk struct {
		Avatar *AvatarConfig `json:"avatar"`
	}
	if err := json.Unmarshal(data, &wk); err != nil || wk.Avatar == nil {
		return AvatarConfig{}
	}
	return *wk.Avatar
}

// buildURL builds a full URL for a file path.
func (r *PageRenderer) buildURL(path string) string {
	if r.config.BaseURL == "" {
		return path
	}
	return strings.TrimSuffix(r.config.BaseURL, "/") + "/" + path
}

// parseFrontmatter extracts frontmatter fields from content.
func parseFrontmatter(content string) map[string]string {
	result := make(map[string]string)
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return result
	}

	// Find end of frontmatter
	end := strings.Index(content[3:], "\n---")
	if end == -1 {
		return result
	}

	fm := content[4 : end+3]
	lines := strings.Split(fm, "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
			continue // Skip nested items
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := yamlUnquote(strings.TrimSpace(parts[1]))
			result[key] = value
		}
	}

	return result
}

// yamlUnquote strips outer YAML quote characters (double or single) and
// unescapes the inner content per YAML quoting rules. Handles the
// double-quoted form (`"\"foo\""` → `"foo"`) and single-quoted form
// (`'it”s'` → `it's`); plain (unquoted) values pass through unchanged.
//
// This is a deliberately small subset of the full YAML escape grammar —
// polis frontmatter only uses titles and a few other simple string
// fields, and the auto-derived title path commonly produces
// `title: "\"X\" Y..."` when the post's first sentence opens with a
// quoted phrase. Without unescaping, the in-memory title carries the
// literal backslashes, which then mismatch the rendered body's
// straight quotes during the title-dedup prefix check.
func yamlUnquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		inner := v[1 : len(v)-1]
		// Replace `\\` first so a literal backslash before a quote
		// (e.g. `\\"`) doesn't get its backslash consumed by the
		// `\"` rule below.
		return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(inner)
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

// stripFrontmatter removes frontmatter from content.
func stripFrontmatter(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return content
	}

	end := strings.Index(content[3:], "\n---")
	if end == -1 {
		return content
	}

	return strings.TrimSpace(content[end+7:])
}

// parseNestedField extracts a nested field value from frontmatter.
// For example: parseNestedField(content, "in-reply-to", "url")
func parseNestedField(content, section, field string) string {
	lines := strings.Split(content, "\n")
	inSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for section start
		if strings.HasPrefix(line, section+":") && !strings.HasPrefix(line, "  ") {
			inSection = true
			continue
		}

		// Check for section end (new non-indented field)
		if inSection && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			break
		}

		// Look for field in section
		if inSection && strings.HasPrefix(trimmed, field+":") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, field+":"))
		}
	}

	return ""
}

// parseInReplyToDisplay derives a human-readable title and domain from an in_reply_to URL.
// For example, "https://discover.polis.pub/posts/20260307/domain-names-cost-10-year.html"
// returns ("Domain Names Cost 10 Year", "discover.polis.pub").
func parseInReplyToDisplay(rawURL string) (title, domain string) {
	if rawURL == "" {
		return "", ""
	}
	domain = extractDomain(rawURL)

	// Extract filename from the URL path, strip extension, and convert to title case
	stripped := strings.TrimPrefix(rawURL, "https://")
	stripped = strings.TrimPrefix(stripped, "http://")
	parts := strings.Split(stripped, "/")
	if len(parts) < 2 {
		return domain, domain
	}
	filename := parts[len(parts)-1]
	filename = strings.TrimSuffix(filename, ".html")
	filename = strings.TrimSuffix(filename, ".md")

	// Convert kebab-case to title case
	words := strings.Split(filename, "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	title = strings.Join(words, " ")
	return title, domain
}

// extractDomain extracts the domain from a URL.
func extractDomain(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")

	if idx := strings.Index(url, "/"); idx > 0 {
		return url[:idx]
	}
	return url
}

// truncateText truncates text to a maximum length, adding "..." if truncated.
func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}

// ReplyContextEntry holds cached metadata about a remote post referenced
// by a comment. Lower-case fields (Title/Excerpt/Domain) have always been
// part of the on-disk cache; BodyMD/Published/FetchedAt were added with
// the stream's `type=comments` thread-entry shape (2026-04-29) and
// stay omitempty so old cache files keep loading without migration.
type ReplyContextEntry struct {
	Title     string `json:"title"`
	Excerpt   string `json:"excerpt"`
	Domain    string `json:"domain"`
	BodyMD    string `json:"body_md,omitempty"`
	Published string `json:"published,omitempty"`
	FetchedAt string `json:"fetched_at,omitempty"` // RFC3339; empty on legacy entries
}

// replyContextEntry kept as a private alias for the existing in-package
// callers (post-render reply-context injection). New external callers
// use ReplyContextEntry directly.
type replyContextEntry = ReplyContextEntry

// replyContextCache is the on-disk cache mapping in_reply_to URLs to metadata.
type replyContextCache map[string]replyContextEntry

const replyContextCachePath = ".polis/bundles/pub.polis.core/comments/reply-context-cache.json"

// loadReplyContextCache loads the cache from disk.
func loadReplyContextCache(site storage.Site) replyContextCache {
	cache := make(replyContextCache)
	data, err := site.Store().Get(context.Background(), replyContextCachePath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("[!] render: could not read the reply-context cache, starting empty: %v", err)
		}
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		log.Printf("[!] render: reply-context cache is unreadable, starting empty: %v", err)
		return make(replyContextCache)
	}
	return cache
}

// saveReplyContextCache persists the cache to disk.
func saveReplyContextCache(site storage.Site, cache replyContextCache) {
	pruneReplyContextCache(cache, maxReplyContextEntries)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		log.Printf("[!] render: could not encode the reply-context cache: %v", err)
		return
	}
	if err := site.Store().Put(context.Background(), replyContextCachePath, data); err != nil {
		log.Printf("[!] render: could not save the reply-context cache: %v", err)
	}
}

// maxReplyContextEntries bounds the reply-context cache per site. Var so a
// test can lower it.
//
// ⛔ The stream's body endpoint is anonymous on hosted and adds an entry per
// distinct URL, so without a bound anyone could grow a tenant's .polis file
// without limit (only a per-IP hourly rate stood in the way). A site's real
// working set is its own comments' targets, far below this.
var maxReplyContextEntries = 2000

// pruneReplyContextCache drops the oldest entries (by fetched_at; legacy
// entries with none count as oldest) until at most max remain. An evicted
// entry is only refetched when next needed.
func pruneReplyContextCache(cache replyContextCache, max int) {
	if max <= 0 || len(cache) <= max {
		return
	}
	keys := make([]string, 0, len(cache))
	for k := range cache {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := cache[keys[i]].FetchedAt, cache[keys[j]].FetchedAt
		if a != b {
			return a < b // RFC3339 UTC sorts as time; "" first
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys[:len(keys)-max] {
		delete(cache, k)
	}
}

// fetchReplyContext fetches a remote post's .md source and extracts title + excerpt.
// Wrapper for back-compat — the v3 reply-context-injection callers don't
// need body/published. New callers should use FetchReplyContextFull.
func fetchReplyContext(htmlURL string) (title, excerpt string, ok bool) {
	r, ok := FetchReplyContextFull(htmlURL)
	return r.Title, r.Excerpt, ok
}

// replyURLAllowed validates that an `in_reply_to` URL is safe to fetch.
// Defends FetchReplyContextFull against SSRF: the URL originates in
// remote comment frontmatter, and the render path runs server-side on
// hosted polis. Without this gate, an attacker-published comment with
// in_reply_to=`http://localhost:6379/` (Redis), `http://[::1]/`, or
// `http://169.254.169.254/...` (cloud metadata) would coerce the server
// into making the request and caching the reply.
//
// Allowed: https URLs whose host passes dm.ValidateDomain (rejects bare
// IPs, localhost, .local/.internal, single-label hostnames, port suffix,
// brackets). http is allowed only because some self-hosted dev sites
// don't have TLS — the host validator still rejects all IP literals and
// internal-network sentinel suffixes.
func replyURLAllowed(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	host := u.Hostname() // strips port + IPv6 brackets
	return dm.ValidateDomain(host) == nil
}

// sanitizeReplyContextForRender escapes the commenter-controlled reply-context
// fields that land in HTML text + href/attribute contexts in the v3 comment
// shape (comment.html). The template engine substitutes {{var}} WITHOUT
// HTML-escaping (see template.Engine.substituteVariables), so these fields —
// the `in_reply_to` frontmatter (URL) and the Title/Excerpt/Domain fetched
// verbatim from a remote origin — would otherwise be a stored-XSS vector on
// the host tenant's origin (R23-1). Title/Excerpt/Domain are HTML-escaped;
// the URLs are scheme-checked (so `javascript:`/`data:`/etc. can't produce a
// clickable XSS href) and then HTML-escaped (so a quote can't break out of the
// href/meta attribute). v4 (the stream shape) is unaffected — its client-side
// renderer assigns metadata via textContent, never innerHTML.
func sanitizeReplyContextForRender(ctx *template.RenderContext) {
	ctx.InReplyToTitle = html.EscapeString(ctx.InReplyToTitle)
	ctx.InReplyToExcerpt = html.EscapeString(ctx.InReplyToExcerpt)
	ctx.InReplyToDomain = html.EscapeString(ctx.InReplyToDomain)
	ctx.InReplyToURL = html.EscapeString(safeRenderURL(ctx.InReplyToURL))
	ctx.RootPostURL = html.EscapeString(safeRenderURL(ctx.RootPostURL))
}

// safeRenderURL gates a commenter-controlled URL before it is rendered into an
// href/attribute context (R23-1). It is intentionally MORE permissive than
// replyURLAllowed — that gate guards the server-side fetch (SSRF) and requires
// http/https + a real public host; this gate only needs to keep dangerous URI
// schemes (javascript:, data:, vbscript:, file:) out of the rendered markup,
// so it reuses the same allowlist as the bluemonday <source src> guard
// (http/https/mailto/protocol-relative/root-relative/relative). Unsafe inputs
// collapse to "" so the href renders empty rather than executable. The caller
// still html.EscapeString's the result to neutralize attribute breakout.
func safeRenderURL(u string) string {
	if safeSrcURLPattern.MatchString(u) {
		return u
	}
	return ""
}

// FetchReplyContextFull fetches a remote post's .md source and extracts
// title, excerpt, body markdown, and published timestamp from the
// frontmatter. Same fetch semantics as fetchReplyContext (3s timeout via
// DefaultHTTPClient, 32KB body cap, fallback to /content/ source path).
// Failed fetches return ok=false and a zero entry; callers should
// degrade gracefully (e.g. render comment without inline post body).
//
// SSRF defense: the URL host is validated through dm.ValidateDomain so
// attacker-supplied `in_reply_to` values can't be used to reach internal
// services (Redis, Postgres, cloud metadata at 169.254.169.254, Fly's
// internal DNS, etc.). The render path runs server-side on hosted polis
// where one poisoned remote comment would otherwise persist in the reply
// cache.
func FetchReplyContextFull(htmlURL string) (entry ReplyContextEntry, ok bool) {
	if !replyURLAllowed(htmlURL) {
		return ReplyContextEntry{}, false
	}

	mdURL := htmlURL
	if strings.HasSuffix(mdURL, ".html") {
		mdURL = strings.TrimSuffix(mdURL, ".html") + ".md"
	}

	client := DefaultHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	resp, err := client.Get(mdURL) //nolint:gosec // G704: replyURLAllowed above admits only http(s) to a public domain (dm.ValidateDomain), and the .md swap changes only the path
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			_ = resp.Body.Close() // read side, unread
		}
		// Fallback: try content source path (hosted sites serve .md at /content/...)
		sourceURL := mountToSourceURL(mdURL)
		if sourceURL != mdURL && replyURLAllowed(sourceURL) {
			resp, err = client.Get(sourceURL) //nolint:gosec // G704: checked by replyURLAllowed on the line above
			if err != nil || resp.StatusCode != 200 {
				if resp != nil {
					_ = resp.Body.Close() // read side, unread
				}
				return ReplyContextEntry{}, false
			}
		} else {
			return ReplyContextEntry{}, false
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024)) // 32KB max
	if err != nil {
		return ReplyContextEntry{}, false
	}

	content := string(body)

	// Find frontmatter bounds. Frontmatter is the markdown bracketed by
	// `---` on its own line at top-of-file and a closing `---`. Anything
	// before/after that is body.
	var fmStart, fmEnd int = -1, -1
	if idx := strings.Index(content, "---"); idx >= 0 {
		if end := strings.Index(content[idx+3:], "---"); end >= 0 {
			fmStart = idx
			fmEnd = idx + 3 + end
		}
	}

	// Extract title + published from frontmatter
	if fmStart >= 0 {
		fm := content[fmStart+3 : fmEnd]
		for _, line := range strings.Split(fm, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "title:") {
				v := strings.TrimSpace(strings.TrimPrefix(line, "title:"))
				entry.Title = yamlUnquote(v)
			} else if strings.HasPrefix(line, "published:") {
				v := strings.TrimSpace(strings.TrimPrefix(line, "published:"))
				entry.Published = yamlUnquote(v)
			}
		}
	}

	// Body markdown is everything after the frontmatter close.
	bodyStart := content
	if fmEnd >= 0 {
		bodyStart = content[fmEnd+3:]
	}
	bodyStart = strings.TrimLeft(bodyStart, "\n")
	entry.BodyMD = bodyStart

	// Extract excerpt: first non-empty paragraph after frontmatter
	var excerptParts []string
	excerptLen := 0
	for _, line := range strings.Split(bodyStart, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(excerptParts) > 0 {
				break // stop at first blank line after collecting text
			}
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "![") {
			continue
		}
		excerptParts = append(excerptParts, line)
		excerptLen += len(line)
		if excerptLen >= 400 {
			break
		}
	}
	if len(excerptParts) > 0 {
		entry.Excerpt = truncateText(strings.Join(excerptParts, " "), 400)
	}

	entry.Domain = extractDomain(htmlURL)
	entry.FetchedAt = time.Now().UTC().Format(time.RFC3339)

	ok = entry.Title != "" || entry.Excerpt != "" || entry.BodyMD != ""
	return entry, ok
}

// LoadOrFetchReplyContext returns a cached ReplyContextEntry for the
// given target URL, fetching + persisting on cache miss or expiry.
// `ttl` controls how long body data stays valid; pass 0 for "never
// expire" (only refetch on missing entries). Cache lives at
// .polis/bundles/pub.polis.core/comments/reply-context-cache.json.
//
// Used by the stream's `type=comments` enrichment path. Failed
// fetches return ok=false; the cache is NOT written for failures so
// retries get a fresh attempt next call.
//
// Concurrency: load + save halves are serialized via replyCacheMu
// (R19-1). The outbound HTTP fetch is NOT held under the mutex —
// concurrent same-URL fetches still race to write (last write wins
// on the duplicate), but R19-3 (singleflight) will close that gap.
// The invariant this mutex preserves: every entry that gets written
// stays in the cache.
//
// Cache-hit predicate (R19-2): any present entry counts as a hit,
// regardless of whether BodyMD is populated. The remaining live
// caller (buildCommentThreadEnrichments in handlers_stream.go) only
// consumes Title + Excerpt, which partial entries already have. The
// old `present && entry.BodyMD != ""` predicate was a vestige of
// buildPostBodies (deleted in commit 174bab8e), which needed full
// bodies; it now just forces re-fetches that don't help anyone.
func LoadOrFetchReplyContext(site storage.Site, htmlURL string, ttl time.Duration) (ReplyContextEntry, bool) {
	// Read-side: lock long enough to read the cache and snapshot the
	// hit entry. We deliberately copy the value out so we don't hold
	// the mutex during the TTL parse + fetch.
	replyCacheMu.Lock()
	cache := loadReplyContextCache(site)
	entry, present := cache[htmlURL]
	replyCacheMu.Unlock()

	if present {
		// Hit — check TTL. R19-2: dropped the BodyMD!="" gate. Any
		// present entry is good enough for the remaining caller's
		// Title + Excerpt consumption.
		if ttl <= 0 {
			return entry, true
		}
		if entry.FetchedAt != "" {
			if t, err := time.Parse(time.RFC3339, entry.FetchedAt); err == nil {
				if time.Since(t) < ttl {
					return entry, true
				}
			}
		}
	}

	// R19-4: short-circuit if a recent fetch for this URL failed.
	// Avoids burning 3s per render on every dead origin (deleted post,
	// dead tenant, intermittent network). Returns whatever cached
	// entry we have (possibly partial) along with ok=false to signal
	// the negative-cache short-circuit.
	if replyNegativeCacheCheck(htmlURL) {
		return entry, false
	}

	// R19-3: singleflight — concurrent same-URL callers share one
	// outbound HTTP request. The first caller fetches and writes the
	// result; subsequent callers receive the same return values from
	// replyFetchGroup.Do.
	fetched, ok := replyFetchGroup.Do(htmlURL, func() (ReplyContextEntry, bool) {
		got, fetchOK := FetchReplyContextFull(htmlURL)
		if !fetchOK {
			// R19-4: record the failure so subsequent calls short-
			// circuit. Uniform 15min TTL — see replyNegativeCacheTTL
			// for the rationale on not differentiating by error class.
			replyNegativeCachePut(htmlURL)
			return got, false
		}
		// Write-side: re-load fresh under the lock so concurrent
		// fetches' writes don't clobber each other (R19-1). Without
		// this re-load, an old cache snapshot from another call site
		// could overwrite entries written by parallel callers between
		// our read and write.
		replyCacheMu.Lock()
		fresh := loadReplyContextCache(site)
		fresh[htmlURL] = got
		saveReplyContextCache(site, fresh)
		replyCacheMu.Unlock()
		return got, true
	})
	if !ok {
		// Return whatever cached entry we had (possibly partial / legacy)
		// along with ok=false to signal the fetch miss to callers that
		// want to fall back to URL-derived defaults.
		return entry, false
	}
	return fetched, true
}

// LoadOrFetchCommentHTML fetches a remote comment's markdown body and renders
// it to sanitized HTML, reusing the reply-context cache / SSRF gate (replyURLAllowed)
// / singleflight / 24h disk cache / negative cache via LoadOrFetchReplyContext.
//
// Used by the stream's read-focus mode to show exactly one comment fetched
// from the commenter's origin site. Returns ("", false) when the comment can't
// be fetched or carries no body — callers degrade gracefully (render the
// comment header with author + date, empty body). MarkdownToHTML
// (goldmark + bluemonday) sanitizes the body server-side before it reaches the
// client, which is the load-bearing guarantee for rendering unblessed remote
// comments inline.
func LoadOrFetchCommentHTML(site storage.Site, commentURL string, ttl time.Duration) (string, bool) {
	entry, ok := LoadOrFetchReplyContext(site, commentURL, ttl)
	if !ok || entry.BodyMD == "" {
		return "", false
	}
	html, err := MarkdownToHTML(entry.BodyMD)
	if err != nil {
		return "", false
	}
	return html, true
}

// mountToSourceURL converts a mount-path URL to a content source path URL.
// e.g. "https://alice.polis.pub/posts/20260323/hello.md" → "https://alice.polis.pub/content/pub.polis.core/post/20260323/hello.md"
// Returns the original URL unchanged if it doesn't contain a known mount path.
func mountToSourceURL(u string) string {
	if idx := strings.Index(u, "/posts/"); idx >= 0 {
		return u[:idx] + "/content/pub.polis.core/post/" + u[idx+len("/posts/"):]
	}
	if idx := strings.Index(u, "/comments/"); idx >= 0 {
		return u[:idx] + "/content/pub.polis.core/comment/" + u[idx+len("/comments/"):]
	}
	return u
}

// resolveReplyContext looks up or fetches metadata for the post a comment replies to.
// Returns title, excerpt, domain — using cache, fetch, or URL-derived fallback.
func resolveReplyContext(site storage.Site, inReplyToURL string, cache replyContextCache, offline bool) (title, excerpt, domain string) {
	domain = extractDomain(inReplyToURL)

	// Check cache first
	if entry, ok := cache[inReplyToURL]; ok {
		return entry.Title, entry.Excerpt, entry.Domain
	}
	if offline {
		fallbackTitle, _ := parseInReplyToDisplay(inReplyToURL)
		return fallbackTitle, "", domain
	}

	// Try fetching from remote
	fetchedTitle, fetchedExcerpt, ok := fetchReplyContext(inReplyToURL)
	if ok {
		if fetchedTitle == "" {
			// Fallback: derive title from URL
			fetchedTitle, _ = parseInReplyToDisplay(inReplyToURL)
		}
		cache[inReplyToURL] = replyContextEntry{
			Title:   fetchedTitle,
			Excerpt: fetchedExcerpt,
			Domain:  domain,
		}
		return fetchedTitle, fetchedExcerpt, domain
	}

	// Fallback: URL-derived title, no excerpt
	fallbackTitle, _ := parseInReplyToDisplay(inReplyToURL)
	return fallbackTitle, "", domain
}

// listSiteRecursive lists everything beneath absDir, which is under root. Keys
// come back relative to root, which is what the render path wants: it addresses
// content by site-relative path already.
func listSiteRecursive(site storage.Site, key string) ([]storage.Entry, error) {
	// ⭐⭐ IT TAKES A KEY NOW, and two layers of round trip came off. This was
	// storage.NewSite(root) — a store rebuilt on every call because the
	// function had only a root string — and then RelKey to turn the absolute
	// path its caller had just BUILT from that same root back into a key.
	// ⭐ The caller has the key; nothing needs to recover it.
	return site.Store().List(context.Background(), key, storage.Recursive())
}

// listSiteDir lists what is directly under absDir, which is under root.
func listSiteDir(site storage.Site, key string) ([]storage.Entry, error) {
	// ⭐ See listSiteRecursive: a key in, no round trip.
	return site.Store().List(context.Background(), key)
}

// isVersionsKey reports whether key sits inside a .versions directory, which
// the render walks skip. It replaces a filepath.SkipDir, which has no meaning
// once a recursive List has already enumerated the tree.
func isVersionsKey(key string) bool {
	for _, seg := range strings.Split(key, "/") {
		if seg == ".versions" {
			return true
		}
	}
	return false
}
