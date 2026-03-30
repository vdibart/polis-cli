package render

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/template"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// DefaultHTTPClient is an optional shared HTTP client for outbound requests
// (e.g., fetching reply context during rendering). Set by the calling application
// to enable connection pooling. If nil, a short-timeout client is created per call.
var DefaultHTTPClient *http.Client

// WidgetVersion is the single source of truth for the current polis widget version.
// Update this constant when widget.js changes. Theme snippets reference it via
// the {{widget_version}} template variable, so they never need manual version bumps.
const WidgetVersion = "1.4.4"

// PageConfig holds configuration for page rendering.
type PageConfig struct {
	DataDir       string // Site data directory
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
}

// PageRenderer renders polis pages using templates.
type PageRenderer struct {
	config     PageConfig
	engine     *template.Engine
	templates  *theme.Templates
	themeName  string
	replyCache replyContextCache // lazily loaded cache for reply context
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
	themeName, err := theme.GetActiveTheme(cfg.DataDir)
	if err != nil || themeName == "" {
		themeName, err = theme.SelectRandomTheme(cfg.DataDir, cfg.CLIThemesDir)
		if err != nil {
			return nil, fmt.Errorf("no theme available: %w", err)
		}
	}

	// Load templates
	templates, err := theme.Load(cfg.DataDir, cfg.CLIThemesDir, themeName)
	if err != nil {
		return nil, fmt.Errorf("failed to load theme: %w", err)
	}

	// Create template engine with markdown renderer
	engine := template.New(template.Config{
		DataDir:          cfg.DataDir,
		CLIThemesDir:     cfg.CLIThemesDir,
		ActiveTheme:      themeName,
		RenderMarkers:    cfg.RenderMarkers,
		BaseURL:          cfg.BaseURL,
		MarkdownRenderer: MarkdownToHTML,
	})

	return &PageRenderer{
		config:    cfg,
		engine:    engine,
		templates: templates,
		themeName: themeName,
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
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// RenderFile renders a single file (post or comment) to HTML.
// Returns the rendered HTML, whether it was rendered (vs skipped), and any error.
func (r *PageRenderer) RenderFile(path string, fileType string, force bool) (string, bool, error) {
	// Build full paths — source is always in content dir
	mdPath := filepath.Join(r.config.DataDir, path)
	// Map source path to mount path for output
	mountPath := r.sourceToMountPath(path, fileType)
	htmlMountRel := strings.TrimSuffix(mountPath, ".md") + ".html"
	htmlPath := filepath.Join(r.config.DataDir, htmlMountRel)

	// Check if rendering is needed (unless force)
	if !force {
		mdInfo, err := os.Stat(mdPath)
		if err != nil {
			return "", false, fmt.Errorf("source file not found: %w", err)
		}

		htmlInfo, err := os.Stat(htmlPath)
		if err == nil && htmlInfo.ModTime().After(mdInfo.ModTime()) {
			// HTML is newer than MD, skip
			return "", false, nil
		}
	}

	// Read markdown file
	content, err := os.ReadFile(mdPath)
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

	// Site info — CSS/Home paths computed from mount path (shallower than source)
	ctx.SiteURL = r.config.BaseURL
	ctx.SiteTitle = r.getSiteTitle()
	ctx.CSSPath = theme.CalculateCSSPath(htmlMountRel)
	ctx.BaseCSSPath = strings.Replace(theme.CalculateCSSPath(htmlMountRel), "styles.css", "base.css", 1)
	ctx.HomePath = theme.CalculateHomePath(htmlMountRel)
	ctx.AuthorName = r.getAuthorName()
	if ctx.AuthorName == "" {
		ctx.AuthorName = r.getAuthorDomain()
	}
	ctx.AuthorURL = r.config.BaseURL

	// Widget variables
	ctx.AuthorDomain = r.getAuthorDomain()
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
				r.replyCache = loadReplyContextCache(r.config.DataDir)
			}
			ctx.InReplyToTitle, ctx.InReplyToExcerpt, ctx.InReplyToDomain = resolveReplyContext(
				r.config.DataDir, ctx.InReplyToURL, r.replyCache,
			)
		}
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

	// Write HTML output to mount dir
	if err := os.MkdirAll(filepath.Dir(htmlPath), 0755); err != nil {
		return "", false, fmt.Errorf("failed to create output directory: %w", err)
	}

	if err := os.WriteFile(htmlPath, []byte(rendered), 0644); err != nil {
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
	ctx.PageType = "index"

	// Load following data (non-fatal if missing)
	followPath := following.DefaultPath(r.config.DataDir)
	followFile, err := following.Load(followPath)
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

	// Write output
	indexPath := filepath.Join(r.config.DataDir, "index.html")
	if err := os.WriteFile(indexPath, []byte(rendered), 0644); err != nil {
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
	ctx.AuthorName = r.getAuthorName()
	if ctx.AuthorName == "" {
		ctx.AuthorName = r.getAuthorDomain()
	}
	ctx.AuthorURL = r.config.BaseURL
	ctx.PostCount = len(posts)
	ctx.Posts = posts
	ctx.AuthorDomain = r.getAuthorDomain()
	ctx.PageType = "index"
	ctx.AvatarHTML = r.buildAvatarHTML()

	// Load following count for archive page stats
	followPath := following.DefaultPath(r.config.DataDir)
	if followFile, err := following.Load(followPath); err == nil {
		ctx.FollowingCount = followFile.Count()
	}

	// Render template
	rendered, err := r.engine.Render(r.templates.Archive, ctx)
	if err != nil {
		return fmt.Errorf("failed to render archive template: %w", err)
	}

	// Write output to mount dir (posts/index.html) or legacy content dir
	archiveDir := filepath.Join(r.config.DataDir, r.config.PostsMountDir)
	if r.config.PostsMountDir == "" {
		archiveDir = filepath.Join(r.config.DataDir, "content", "pub.polis.core", "post")
	}
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("failed to create posts directory: %w", err)
	}

	archivePath := filepath.Join(archiveDir, "index.html")
	if err := os.WriteFile(archivePath, []byte(rendered), 0644); err != nil {
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

	tagsDir := filepath.Join(r.config.DataDir, "content", "pub.polis.core", "tag")
	entries, err := os.ReadDir(tagsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read tags directory: %w", err)
	}

	var allTags []template.TagData
	count := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(tagsDir, entry.Name()))
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
		ctx.AuthorDomain = r.getAuthorDomain()
		ctx.PageType = "tag"

		rendered, err := r.engine.Render(r.templates.Tag, ctx)
		if err != nil {
			return count, fmt.Errorf("failed to render tag page for %q: %w", tf.Tag, err)
		}

		outPath := filepath.Join(r.config.DataDir, tagPagePath)
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return count, fmt.Errorf("failed to create tag directory: %w", err)
		}
		if err := os.WriteFile(outPath, []byte(rendered), 0644); err != nil {
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
	ctx.AuthorDomain = r.getAuthorDomain()
	ctx.PageType = "tag-index"

	rendered, err := r.engine.Render(r.templates.TagIndex, ctx)
	if err != nil {
		return count, fmt.Errorf("failed to render tag index: %w", err)
	}

	indexPath := filepath.Join(r.config.DataDir, "tags", "index.html")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		return count, fmt.Errorf("failed to create tags directory: %w", err)
	}
	if err := os.WriteFile(indexPath, []byte(rendered), 0644); err != nil {
		return count, fmt.Errorf("failed to write tag index: %w", err)
	}

	return count, nil
}

// RenderAll renders all posts and comments, and generates the index.
func (r *PageRenderer) RenderAll(force bool) (*RenderStats, error) {
	stats := &RenderStats{}

	// Copy CSS first
	if err := theme.CopyCSS(r.config.DataDir, r.config.CLIThemesDir, r.themeName); err != nil {
		return nil, fmt.Errorf("failed to copy CSS: %w", err)
	}
	// Copy base CSS (shared structural styles)
	if err := theme.CopyBaseCSS(r.config.DataDir, r.config.CLIThemesDir); err != nil {
		return nil, fmt.Errorf("failed to copy base CSS: %w", err)
	}

	// Find all posts
	postsDir := filepath.Join(r.config.DataDir, "content", "pub.polis.core", "post")
	if err := filepath.Walk(postsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".versions" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		relPath, _ := filepath.Rel(r.config.DataDir, path)
		_, rendered, err := r.RenderFile(relPath, "post", force)
		if err != nil {
			return fmt.Errorf("failed to render %s: %w", relPath, err)
		}

		if rendered {
			stats.PostsRendered++
		} else {
			stats.PostsSkipped++
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Find all comments
	commentsDir := filepath.Join(r.config.DataDir, "content", "pub.polis.core", "comment")
	if err := filepath.Walk(commentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".versions" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		relPath, _ := filepath.Rel(r.config.DataDir, path)
		_, rendered, err := r.RenderFile(relPath, "comment", force)
		if err != nil {
			return fmt.Errorf("failed to render %s: %w", relPath, err)
		}

		if rendered {
			stats.CommentsRendered++
		} else {
			stats.CommentsSkipped++
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Copy artifacts to mount dirs
	if r.config.CommentsMountDir != "" {
		srcBlessed := filepath.Join(r.config.DataDir, "content", "pub.polis.core", "comment", "blessed.json")
		if _, err := os.Stat(srcBlessed); err == nil {
			dstBlessed := filepath.Join(r.config.DataDir, r.config.CommentsMountDir, "blessed.json")
			copyFile(srcBlessed, dstBlessed) // non-fatal
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

	// Persist reply context cache if it was populated during comment rendering
	if r.replyCache != nil && len(r.replyCache) > 0 {
		saveReplyContextCache(r.config.DataDir, r.replyCache)
	}

	return stats, nil
}

// loadPublicIndex loads posts and comments from public.jsonl.
func (r *PageRenderer) loadPublicIndex() ([]template.PostData, []template.CommentData, error) {
	entries, err := metadata.LoadPublicIndex(r.config.DataDir)
	if err != nil {
		return nil, nil, err
	}

	// Build a map of post path -> blessed comment count
	commentCountMap := make(map[string]int)
	if bc, err := metadata.LoadBlessedComments(r.config.DataDir); err == nil {
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
			mdPath := filepath.Join(r.config.DataDir, entry.Path)
			if data, err := os.ReadFile(mdPath); err == nil {
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
				URL:            "/" + htmlPath,
				TargetAuthor:   extractDomain(inReplyToURL),
				Published:      entry.Published,
				PublishedHuman: template.FormatHumanDate(entry.Published),
				Preview:        truncateText(entry.Title, 100), // Use title as preview
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
	comments, err := metadata.GetBlessedCommentsForPost(r.config.DataDir, postPath)
	if err != nil {
		return nil, err
	}

	var results []template.BlessedCommentData

	for _, comment := range comments {
		// Try to load local comment content
		content := r.loadLocalCommentContent(comment.URL)

		results = append(results, template.BlessedCommentData{
			URL:            comment.URL,
			AuthorName:     extractDomain(comment.URL),
			Published:      comment.BlessedAt,
			PublishedHuman: template.FormatHumanDate(comment.BlessedAt),
			Content:        content,
		})
	}

	return results, nil
}

// loadLocalCommentContent tries to resolve a comment URL to a local file and load its content.
// Returns rendered HTML content if found, empty string otherwise.
// Checks both mount path (comments/) and source path (content/pub.polis.core/comment/).
func (r *PageRenderer) loadLocalCommentContent(commentURL string) string {
	// Try to extract relative path from URL (e.g., comments/20260101/id.md)
	suffix := ""
	if idx := strings.Index(commentURL, "/comments/"); idx >= 0 {
		suffix = commentURL[idx+len("/comments/"):] // "20260101/id.md"
	} else if strings.HasPrefix(commentURL, "comments/") {
		suffix = strings.TrimPrefix(commentURL, "comments/")
	}

	if suffix == "" {
		return ""
	}

	// Ensure .md extension
	if !strings.HasSuffix(suffix, ".md") {
		suffix = strings.TrimSuffix(suffix, ".html") + ".md"
	}

	// Try multiple candidate paths: mount path first, then source content path
	candidates := []string{
		filepath.Join(r.config.DataDir, "comments", suffix),
	}
	if r.config.CommentsSourceDir != "" {
		candidates = append(candidates, filepath.Join(r.config.DataDir, r.config.CommentsSourceDir, suffix))
	}

	for _, fullPath := range candidates {
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		// Strip frontmatter and render markdown
		body := stripFrontmatter(string(data))
		html, err := MarkdownToHTML(body)
		if err != nil {
			return body
		}
		return html
	}

	return ""
}

// getSiteTitle returns the site title from .well-known/polis.
func (r *PageRenderer) getSiteTitle() string {
	wkPath := filepath.Join(r.config.DataDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
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
	wkPath := filepath.Join(r.config.DataDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
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

// buildAvatarHTML returns pre-rendered HTML for the site avatar.
// Uses avatar config from .well-known/polis if available, otherwise generates
// a default initial-letter avatar. Supports bg, fg, border, and pattern rendering
// matching the nav.js/app.js avatar implementation.
func (r *PageRenderer) buildAvatarHTML() string {
	authorName := r.getAuthorName()
	if authorName == "" {
		authorName = r.getAuthorDomain()
	}

	initial := "?"
	if runes := []rune(authorName); len(runes) > 0 {
		initial = string(runes[0])
	}

	// Try to load avatar config from .well-known/polis
	wkPath := filepath.Join(r.config.DataDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
	if err == nil {
		var wk struct {
			Avatar *struct {
				BG           string `json:"bg"`
				FG           string `json:"fg"`
				Border       string `json:"border"`
				BorderW      int    `json:"border_w"`
				Pattern      string `json:"pattern"`
				PatternColor string `json:"pattern_color"`
			} `json:"avatar"`
		}
		if err := json.Unmarshal(data, &wk); err == nil && wk.Avatar != nil && wk.Avatar.BG != "" {
			av := wk.Avatar

			// Build inline style matching nav.js buildAvatarStyle
			style := fmt.Sprintf("background-color:%s;color:%s;",
				html.EscapeString(av.BG),
				html.EscapeString(av.FG))

			if av.Border != "" && av.BorderW > 0 {
				style += fmt.Sprintf("border:%dpx solid %s;",
					av.BorderW, html.EscapeString(av.Border))
			}

			// Pattern support
			hasPattern := false
			if av.Pattern != "" && av.Pattern != "none" && av.PatternColor != "" {
				if gen, ok := avatarPatterns[av.Pattern]; ok {
					svg := gen(av.PatternColor)
					b64 := base64.StdEncoding.EncodeToString([]byte(svg))
					style += fmt.Sprintf("background-image:url(data:image/svg+xml;base64,%s);background-size:cover;", b64)
					hasPattern = true
				}
			}

			// Hide initial when custom pattern is set (matching nav.js behavior)
			displayInitial := html.EscapeString(initial)
			if hasPattern {
				displayInitial = ""
			}

			return fmt.Sprintf(`<span class="avatar-initial" style="%s">%s</span>`,
				style, displayInitial)
		}
	}

	// Default: unstyled initial (theme CSS provides default avatar styling)
	return fmt.Sprintf(`<span class="avatar-initial">%s</span>`, html.EscapeString(initial))
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
			value := strings.TrimSpace(parts[1])
			result[key] = value
		}
	}

	return result
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

// replyContextEntry holds cached metadata about a remote post referenced by a comment.
type replyContextEntry struct {
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
	Domain  string `json:"domain"`
}

// replyContextCache is the on-disk cache mapping in_reply_to URLs to metadata.
type replyContextCache map[string]replyContextEntry

const replyContextCachePath = ".polis/content/pub.polis.core/comments/reply-context-cache.json"

// loadReplyContextCache loads the cache from disk.
func loadReplyContextCache(dataDir string) replyContextCache {
	cache := make(replyContextCache)
	data, err := os.ReadFile(filepath.Join(dataDir, replyContextCachePath))
	if err != nil {
		return cache
	}
	_ = json.Unmarshal(data, &cache)
	return cache
}

// saveReplyContextCache persists the cache to disk.
func saveReplyContextCache(dataDir string, cache replyContextCache) {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	fullPath := filepath.Join(dataDir, replyContextCachePath)
	_ = os.MkdirAll(filepath.Dir(fullPath), 0755)
	_ = os.WriteFile(filepath.Join(dataDir, replyContextCachePath), data, 0644)
}

// fetchReplyContext fetches a remote post's .md source and extracts title + excerpt.
// Uses a short timeout so render doesn't hang if the source is unreachable.
func fetchReplyContext(htmlURL string) (title, excerpt string, ok bool) {
	// Try fetching the .md source (has frontmatter with title)
	mdURL := htmlURL
	if strings.HasSuffix(mdURL, ".html") {
		mdURL = strings.TrimSuffix(mdURL, ".html") + ".md"
	}

	client := DefaultHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	resp, err := client.Get(mdURL)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		// Fallback: try content source path (hosted sites serve .md at /content/...)
		sourceURL := mountToSourceURL(mdURL)
		if sourceURL != mdURL {
			resp, err = client.Get(sourceURL)
			if err != nil || resp.StatusCode != 200 {
				if resp != nil {
					resp.Body.Close()
				}
				return "", "", false
			}
		} else {
			return "", "", false
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024)) // 32KB max
	if err != nil {
		return "", "", false
	}

	content := string(body)

	// Extract title from frontmatter
	if idx := strings.Index(content, "---"); idx >= 0 {
		if end := strings.Index(content[idx+3:], "---"); end >= 0 {
			fm := content[idx+3 : idx+3+end]
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "title:") {
					title = strings.TrimSpace(strings.TrimPrefix(line, "title:"))
					title = strings.Trim(title, "\"'")
					break
				}
			}
		}
	}

	// Extract excerpt: first non-empty paragraph after frontmatter
	bodyStart := content
	if idx := strings.Index(content, "---"); idx >= 0 {
		if end := strings.Index(content[idx+3:], "---"); end >= 0 {
			bodyStart = content[idx+3+end+3:]
		}
	}
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
		excerpt = truncateText(strings.Join(excerptParts, " "), 400)
	}

	return title, excerpt, title != "" || excerpt != ""
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
func resolveReplyContext(dataDir, inReplyToURL string, cache replyContextCache) (title, excerpt, domain string) {
	domain = extractDomain(inReplyToURL)

	// Check cache first
	if entry, ok := cache[inReplyToURL]; ok {
		return entry.Title, entry.Excerpt, entry.Domain
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
