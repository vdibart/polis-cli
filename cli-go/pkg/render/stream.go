package render

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitemap"
	"github.com/vdibart/polis-cli/cli-go/pkg/template"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// maxStreamSiblings caps the per-page sibling list — D-SIBLINGS (step-02/2.b.3).
// Below the floor (<2) v4 still renders, but with fewer items.
//
// Bumped from 4 → 6 (2026-04-29) to address tall-monitor /index.html bug:
// 1080p+ viewports could fit focus + 4 siblings without producing a
// scrollbar, leaving the user with no scroll-trigger affordance and the
// pagination machinery (gated on userHasScrolled) inert. 6 siblings
// covers most 1080p/1440p tall monitors without a runtime fetch.
// stream.js's fillViewportIfShort() is the runtime fallback for 4K and
// oddly-tall viewports — kicks off a fetchNextPage from init when
// document.scrollHeight still < window.innerHeight after SSR.
const maxStreamSiblings = 6

// renderStreamAll is the shape-dispatched counterpart to RenderAll. Emits:
//   - <mount>/posts/YYYYMMDD/slug.html for every post (focus = that post)
//   - <site-root>/index.html (focus = most-recent post)
//
// v4 does not emit per-comment pages, tag pages, or an archive index —
// those surfaces are subsumed by the stream controller (WS-2) on the live
// client. 2.c ships only the static logged-out artifact.
func (r *PageRenderer) renderStreamAll(force bool) (*RenderStats, error) {
	stats := &RenderStats{}

	// Structural CSS first: base.css (shared) + styles.css (stream shape + theme).
	if err := theme.CopyBaseCSS(r.config.DataDir, r.config.CLIThemesDir); err != nil {
		return nil, fmt.Errorf("failed to copy base CSS: %w", err)
	}
	if err := theme.CopyStreamCSS(r.config.DataDir, r.themeName); err != nil {
		return nil, fmt.Errorf("failed to copy v4 CSS: %w", err)
	}
	// /stream.js placeholder (step-03/3.e). Quiets pageview 404s before WS-2
	// ships the real controller.
	if err := theme.CopyStreamController(r.config.DataDir); err != nil {
		return nil, fmt.Errorf("failed to copy v4 controller stub: %w", err)
	}

	posts, _, err := r.loadPublicIndex()
	if err != nil {
		return nil, fmt.Errorf("failed to load public index: %w", err)
	}

	// Empty corpus: emit a minimal index.html so first-load doesn't 404.
	// Posts directory intentionally not created — nothing to put in it.
	if len(posts) == 0 {
		if err := r.renderStreamEmptyIndex(); err != nil {
			return nil, fmt.Errorf("failed to render empty index: %w", err)
		}
		stats.IndexGenerated = true
		return stats, nil
	}

	// Body cache: shared across the full-corpus render so each post's body
	// HTML renders at most once even though it appears as a sibling on
	// multiple per-post pages. Carries the per-post TitleLinkState too,
	// so "title overlaps body" detection survives cache hits — otherwise
	// the second-and-later occurrences of a redundant-title sibling
	// would drop the redundant state.
	bodyCache := make(map[string]siblingBodyCacheEntry, len(posts))

	// Per-post pages. Focus iterates through every post; siblings split
	// into a newer-direction rail (above the focus) and an older-
	// direction rail (below). Currently 1 newer above when one exists,
	// rest of the budget below — see pickStreamSiblings.
	totalPosts := len(posts)
	for i, focus := range posts {
		above, below := pickStreamSiblings(posts, i)
		r.populateSiblingBodies(above, bodyCache)
		r.populateSiblingBodies(below, bodyCache)
		outPath := streamOutputPath(focus.URL)
		if err := r.renderStreamFile(focus, above, below, totalPosts, outPath, force); err != nil {
			return nil, fmt.Errorf("render v4 post %s: %w", focus.URL, err)
		}
		stats.PostsRendered++
	}

	// Homepage: focus = newest post; no above-focus siblings (focus IS
	// the newest); below = next-4-older.
	// NOTE: pass the relative path ("index.html") — renderStreamFile auto-joins
	// DataDir for non-absolute outPaths. Pre-joining here would cause a
	// double-join when DataDir is itself relative (real-world CLI invocations
	// like `polis --data-dir tests/harness/clone render` produced
	// tests/harness/clone/tests/harness/clone/index.html before this
	// fix; tests masked the bug by using absolute t.TempDir() paths).
	_, indexBelow := pickStreamSiblings(posts, 0)
	r.populateSiblingBodies(indexBelow, bodyCache)
	if err := r.renderStreamFile(posts[0], nil, indexBelow, totalPosts, "index.html", true); err != nil {
		return nil, fmt.Errorf("render v4 index: %w", err)
	}
	stats.IndexGenerated = true

	// Full sitemap rebuild — corresponds to the `polis render --force` recovery
	// path. Per-publish/per-unpublish increments live in PublishStream and
	// UnpublishStream. A failure here is non-fatal: posts are already on disk, and
	// the next publish will re-attempt the sitemap update.
	if err := r.rebuildSitemapFromCorpus(); err != nil {
		// Surface as a warning but don't fail the render — sitemap.xml is
		// SEO infrastructure, not user-facing content.
		fmt.Fprintf(os.Stderr, "[!] sitemap rebuild failed: %v\n", err)
	}

	return stats, nil
}

// rebuildSitemapFromCorpus reads every post in the public index, stats each
// source markdown for its mtime, builds a fresh sitemap, and writes it
// atomically. Source mtime (vs. the frontmatter "published" field) reflects
// republish updates correctly — RepublishPost rewrites the source file, so
// the on-disk mtime advances even though the published-timestamp does not.
func (r *PageRenderer) rebuildSitemapFromCorpus() error {
	entries, err := metadata.LoadPublicIndex(r.config.DataDir)
	if err != nil {
		return fmt.Errorf("load index for sitemap: %w", err)
	}
	var smEntries []sitemap.Entry
	for _, e := range entries {
		if !(strings.HasPrefix(e.Path, "posts/") || e.Type == "post") {
			continue
		}
		htmlURL := r.postSourceToURL(e.Path)
		absURL := r.buildURL(strings.TrimPrefix(htmlURL, "/"))
		info, statErr := os.Stat(filepath.Join(r.config.DataDir, e.Path))
		if statErr != nil {
			// Source missing — skip the entry; don't poison the sitemap with
			// dangling URLs.
			continue
		}
		smEntries = append(smEntries, sitemap.Entry{
			URL:     absURL,
			LastMod: info.ModTime(),
		})
	}
	body, err := sitemap.Build(smEntries)
	if err != nil {
		return fmt.Errorf("build sitemap: %w", err)
	}
	return sitemap.Write(r.config.DataDir, body)
}

// populateSiteIdentity fills the .site-bio + .site-stats blocks on the v4
// layout-left card. Caller passes the already-known total post count (from
// loadPublicIndex) to avoid a second index read; followers + following +
// bio markdown are read from disk here.
//
// Followers come from .polis/ds/<ds-domain>/pub.polis.core/state/pub.polis.follow.json
// (a JSON array of follower entries, populated by stream sync). Following
// comes from content/pub.polis.core/follow/following.json. Bio comes from
// site/snippets/about.md rendered to HTML. All three reads are best-effort:
// a missing or malformed file leaves the field at its zero value rather
// than failing the render.
//
// Multiple DS dirs are uncommon (a tenant typically registers with one DS).
// First-valid-DS-dir wins on count if multiple exist; if a tenant ever
// federates across multiple DSes, the count would need to dedupe by author
// domain across files (out of scope for this populator).
func (r *PageRenderer) populateSiteIdentity(ctx *template.RenderContext, postsCount int) {
	ctx.PostCount = postsCount

	if followFile, err := following.Load(following.DefaultPath(r.config.DataDir)); err == nil {
		ctx.FollowingCount = followFile.Count()
	}

	dsDir := filepath.Join(r.config.DataDir, ".polis", "ds")
	if entries, err := os.ReadDir(dsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			followerPath := filepath.Join(dsDir, entry.Name(), "pub.polis.core", "state", "pub.polis.follow.json")
			followerData, err := os.ReadFile(followerPath)
			if err != nil {
				continue
			}
			var followers []interface{}
			if err := json.Unmarshal(followerData, &followers); err == nil {
				ctx.FollowersCount = len(followers)
				break
			}
		}
	}

	// Bio: render site/snippets/about.md to HTML. Absent file → empty bio
	// (the .site-bio:empty CSS rule then collapses the block).
	aboutPath := filepath.Join(r.config.DataDir, "site", "snippets", "about.md")
	if data, err := os.ReadFile(aboutPath); err == nil {
		if html, err := MarkdownToHTML(string(data)); err == nil {
			ctx.SiteBio = html
		}
	}
}

// pickStreamSiblings selects up to maxStreamSiblings posts to display alongside
// the focus, split into two rails:
//   - above: newer posts shown ABOVE the focus (newer-direction rail).
//     One immediate-newer post when available; padded with more newer
//     ones only when there aren't enough older posts to fill the budget.
//   - below: older posts shown BELOW the focus (older-direction rail).
//     Filled to (maxStreamSiblings - len(above)) — the natural "keep
//     reading" flow.
//
// Both slices are returned in DOM order (newest-first within each rail).
// Each post in `above` is marked IsAboveFocus=true so stream-post.html
// can apply the .is-above-focus class for the fade-into-topbar treatment.
//
// `posts` is expected in newest-first order (as returned by
// loadPublicIndex). Total siblings (len(above)+len(below)) is capped at
// maxStreamSiblings; in the common case len(above)=1 so the focus stays the
// visual hero. Edge cases push len(above) higher only when the focus is
// at or near the corpus's oldest end (older rail can't fill the budget).
func pickStreamSiblings(posts []template.PostData, focusIdx int) (above, below []template.PostData) {
	n := len(posts)
	if n <= 1 {
		return nil, nil
	}
	// Above rail: take 1 immediately-newer post when one exists. The
	// minimum-viable hint that the focus sits in the middle of a stream;
	// more newer is padded in below if the older rail can't fill the
	// budget. (See post-budget block.)
	above = make([]template.PostData, 0, maxStreamSiblings)
	if focusIdx > 0 {
		entry := posts[focusIdx-1]
		entry.IsAboveFocus = true
		above = append(above, entry)
	}
	// Below rail: fill remaining budget with older posts.
	below = make([]template.PostData, 0, maxStreamSiblings)
	belowBudget := maxStreamSiblings - len(above)
	for i := focusIdx + 1; i < n && len(below) < belowBudget; i++ {
		below = append(below, posts[i])
	}
	// Padding: when older rail came up short (focus is at/near the
	// oldest end of the corpus), pull more newer posts up into the
	// above rail so the page still has a full sibling context. New
	// entries land at the TOP of the above rail (older-of-newer stays
	// closest to the focus).
	if len(above)+len(below) < maxStreamSiblings {
		nextNewerIdx := focusIdx - 2
		for nextNewerIdx >= 0 && len(above)+len(below) < maxStreamSiblings {
			entry := posts[nextNewerIdx]
			entry.IsAboveFocus = true
			above = append([]template.PostData{entry}, above...)
			nextNewerIdx--
		}
	}
	return above, below
}

// populateSiblingBodies fills in BodyHTML on each sibling so the v4 SHAPE
// can SSR the full body of each sibling inline (rather than just an
// excerpt). The provided cache is consulted first — when the same post
// appears as a sibling on multiple per-post pages (the common case in a
// full-corpus render), its body renders at most once. Callers should pass
// a cache scoped to the lifetime of their render batch (renderStreamAll
// builds one for the full corpus; PublishStream / UnpublishStream build per-call
// caches).
//
// Best-effort: if the source markdown is missing or fails to render, the
// sibling's BodyHTML stays empty and the entry-content slot in the partial
// renders empty. Failure is non-fatal — the sibling still appears with
// title + meta, just no body.
//
// TRUST CHAIN. The HTML written into BodyHTML is unescaped at template-
// substitution time (`{{body_html}}` in stream-post.html) — the same trust
// model the stream controller's innerHTML sites depend on. The chain:
//
//  1. Bodies are sanitized with bluemonday at publish time, before the
//     content-hashed source markdown is signed.
//  2. The signature covers the bytes that produced this HTML: any in-
//     transit tampering breaks signature verification at the consumer.
//  3. The read-side is intentionally NOT re-sanitizing — re-sanitization
//     would mask publish-time policy by silently stripping content the
//     signer authorized.
//  4. DM decryption is the only trust boundary that crosses out of this
//     chain; DMs never ride this code path (owner-private, not SSR'd).
//
// See cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.js
// "TRUST CHAIN" block above the first innerHTML site for the canonical
// statement of the chain — keep this comment + that one in sync.
// siblingBodyCacheEntry stores everything we compute per sibling once
// from its markdown source. Caching only the HTML body would lose the
// TitleLinkState on cache hits, dropping the redundant-title hide on
// subsequent occurrences of the same post.
type siblingBodyCacheEntry struct {
	BodyHTML       string
	TitleLinkState string
}

func (r *PageRenderer) populateSiblingBodies(siblings []template.PostData, cache map[string]siblingBodyCacheEntry) {
	for j := range siblings {
		if siblings[j].BodyHTML != "" {
			continue
		}
		url := siblings[j].URL
		if cached, ok := cache[url]; ok {
			siblings[j].BodyHTML = cached.BodyHTML
			siblings[j].TitleLinkState = cached.TitleLinkState
			continue
		}
		srcRel, err := streamSourceMDPath(url, r.config.PostsSourceDir, r.config.PostsMountDir)
		if err != nil {
			continue
		}
		srcPath := filepath.Join(r.config.DataDir, srcRel)
		data, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		body := stripFrontmatter(string(data))
		body = StripLeadingTitleHeading(body, siblings[j].Title)
		var titleLinkState string
		if TitleStartsFirstSentence(siblings[j].Title, body) {
			titleLinkState = "is-redundant"
		}
		html, err := MarkdownToHTML(body)
		if err != nil {
			continue
		}
		cache[url] = siblingBodyCacheEntry{BodyHTML: html, TitleLinkState: titleLinkState}
		siblings[j].BodyHTML = html
		siblings[j].TitleLinkState = titleLinkState
	}
}

// TitleStartsFirstSentence reports whether the post body's first
// non-blank line begins with the title text. Powers the stream templates'
// "hide redundant title chrome" rule (see RenderContext.TitleLinkState):
// when a post's body leads with a sentence whose opening matches the
// frontmatter title — common when titles are auto-derived as a
// truncation of the body's first sentence — the explicit title <h1>/<h3>
// reads as a duplicate of the body. Hiding the title chrome lets the
// body absorb the title naturally.
//
// Normalization is deliberately lenient so the check survives small
// punctuation differences between title and body — curly vs. straight
// quotes, em-dash vs. hyphen, smart apostrophes — that frontmatter
// quoting and markdown rendering routinely introduce.
//
// No-op when title is empty or no first text line is found.
//
// CALLERS — every render or response path that emits a (title, body|excerpt)
// pair to a renderer is responsible for calling this function and propagating
// the result. The renderer (template, SPA component, etc.) reads the boolean
// and suppresses the title chrome accordingly. If you are adding a new render
// path that ships title + body content, add it here:
//
//   v3 per-post pages          — cli-go/pkg/render/page.go (RenderContext.TitleLinkState)
//   v4 focus-entry SSR         — cli-go/pkg/render/stream.go (RenderContext.TitleLinkState)
//   v4 sibling SSR             — cli-go/pkg/render/stream.go (siblings_above/below loop)
//   /api/v1/stream/items
//     own-handle scope         — buildPostTitleRedundancy (reads markdown body)
//     cross-tenant scope       — handleStreamItems inline (uses cached excerpt)
//   /api/feed/grouped          — handleFeedGrouped inline (uses cached excerpt)
//
// Surfaces that should consume the flag (renderer-side):
//
//   v3/v4 SSR templates        — .is-redundant CSS class on title-link wrapper
//   v4 stream.js renderPost    — meta.title_redundant → entry-title.is-redundant
//   legacy app.js renderConversationsTabbed — group.post_title_redundant gate
//
// When the title is derived from the body via excerpt-fetch (no markdown
// source on disk), use the cached excerpt as the body argument — the
// detector matches on the first non-blank line, which the excerpt
// preserves. Lower fidelity than reading the full markdown but
// catches the common auto-titled-post case.
func TitleStartsFirstSentence(title, body string) bool {
	titleNorm := normalizeForTitleCompare(title)
	// Strip trailing truncation markers — auto-derived titles commonly
	// end with "..." (literal three dots) or a hyphen (em-dash truncated
	// mid-word, folded by normalizeForTitleCompare). The body doesn't
	// have those at the corresponding position; without trimming,
	// HasPrefix fails even when title is genuinely a prefix of the
	// body's first sentence. Trim from the right to leave the
	// substantive content intact.
	titleNorm = strings.TrimRight(titleNorm, ". -")
	if titleNorm == "" {
		return false
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Skip leading markdown headings — StripLeadingTitleHeading runs
		// before this and pulls the title-matching ATX line out of the
		// body, but a body that starts with an unrelated heading
		// shouldn't drive the prefix check on the heading text. Move on
		// to the first prose line.
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		firstNorm := normalizeForTitleCompare(trimmed)
		return strings.HasPrefix(firstNorm, titleNorm)
	}
	return false
}

// normalizeForTitleCompare lowercases and folds typographic variants so
// titles and body prose can be prefix-compared even when their
// punctuation diverges (frontmatter often stores titles with straight
// quotes while the markdown body renders curly ones, etc.).
//
// Also strips backslashes — defense-in-depth against frontmatter parsers
// that don't unescape YAML double-quoted strings (yamlUnquote in page.go
// handles this at parse time, but legacy reply-context-cache entries
// written before that fix carry escaped titles like `\"Just use RSS\"…`).
// Markdown bodies don't have legitimate backslashes after rendering so
// stripping them here is safe.
func normalizeForTitleCompare(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer(
		"“", `"`, "”", `"`, // curly double quotes
		"‘", "'", "’", "'", // curly single quotes / smart apostrophe
		"—", "-", "–", "-", // em-dash, en-dash
		"…", "...", // horizontal ellipsis
		`\`, "", // strip backslash escape leakage
	).Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// StripLeadingTitleHeading removes a leading ATX heading (# / ## / ...) from
// the markdown body when its text matches the post's frontmatter title
// (whitespace-trimmed, case-insensitive). v4's focus + sibling templates
// always render the frontmatter title as an explicit <h1>; without this strip,
// posts whose markdown leads with the same title produce a visible duplicate.
//
// No-op when title is empty, when the body doesn't lead with a heading, or
// when the heading text doesn't match — so posts whose first heading is an
// intentional, distinct subhead are untouched.
func StripLeadingTitleHeading(body, title string) string {
	titleNorm := strings.TrimSpace(strings.ToLower(title))
	if titleNorm == "" {
		return body
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			return body
		}
		rest := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if strings.ToLower(rest) != titleNorm {
			return body
		}
		// Drop the heading line plus a single trailing blank line if present.
		next := i + 1
		if next < len(lines) && strings.TrimSpace(lines[next]) == "" {
			next++
		}
		return strings.Join(lines[next:], "\n")
	}
	return body
}

// streamOutputPath converts a focus URL ("/posts/20260323/hello.html") into a
// DataDir-relative filesystem path. loadPublicIndex prefixes URLs with "/"
// so this is a straight strip.
func streamOutputPath(focusURL string) string {
	return strings.TrimPrefix(focusURL, "/")
}

// renderStreamFile renders a single stream page with the given focus and siblings.
// outPath is either site-root/index.html or a mount-relative posts/.../*.html
// (relative paths are resolved against DataDir here). totalPosts feeds the
// .site-stats block; callers pass the known full-corpus count from their
// loadPublicIndex result (passing 0 is also valid for empty-corpus paths).
// siblingsAbove + siblingsBelow split the unified-grammar sibling rail into
// the newer-direction (above the focus) and older-direction (below) lists.
// Either may be nil — callers thread nil for siblingsAbove on /index.html
// (focus IS the newest, nothing newer to show).
func (r *PageRenderer) renderStreamFile(focus template.PostData, siblingsAbove, siblingsBelow []template.PostData, totalPosts int, outPath string, force bool) error {
	// If outPath is relative (mount-path case), resolve against DataDir.
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(r.config.DataDir, outPath)
	}

	// Derive source path for the focus post from its URL. URL looks like
	// "/posts/20260323/slug.html" — strip the mount prefix and re-map to
	// the content source path so we can read the markdown body.
	srcRel, err := streamSourceMDPath(focus.URL, r.config.PostsSourceDir, r.config.PostsMountDir)
	if err != nil {
		return fmt.Errorf("map focus URL to source: %w", err)
	}
	srcPath := filepath.Join(r.config.DataDir, srcRel)

	// Skip-if-newer unless forced.
	if !force {
		if srcInfo, err := os.Stat(srcPath); err == nil {
			if outInfo, err := os.Stat(outPath); err == nil && outInfo.ModTime().After(srcInfo.ModTime()) {
				return nil
			}
		}
	}

	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read focus source: %w", err)
	}

	fm := parseFrontmatter(string(content))
	body := stripFrontmatter(string(content))
	body = StripLeadingTitleHeading(body, fm["title"])
	titleRedundant := TitleStartsFirstSentence(fm["title"], body)
	htmlContent, err := MarkdownToHTML(body)
	if err != nil {
		return fmt.Errorf("render focus markdown: %w", err)
	}

	ctx := template.NewRenderContext()
	ctx.WidgetVersion = WidgetVersion
	ctx.Title = fm["title"]
	if ctx.Title == "" {
		ctx.Title = focus.Title
	}
	ctx.Content = htmlContent
	if titleRedundant {
		ctx.TitleLinkState = "is-redundant"
	}
	ctx.Published = focus.Published
	// stream meta-line wants full date + time ("April 23, 2026 · 4:12pm")
	// per the build-target mockup. focus.PublishedHuman comes from
	// loadPostsAndComments via FormatHumanDate ("April 23, 2026" — no time);
	// re-format here so v3 paths stay date-only while v4 gets date+time.
	// PublishedYear feeds the year-marker above the focus entry (5.g layout).
	ctx.PublishedHuman = template.FormatHumanDateTime(focus.Published)
	ctx.PublishedYear = template.FormatYear(focus.Published)
	ctx.URL = r.buildURL(strings.TrimPrefix(focus.URL, "/"))
	// FocusPathURL is the path-form canonical URL (no scheme/host) used
	// by the focus title-link's href. Path-form so the link works on
	// both hosted (where browser resolves against the tenant subdomain)
	// and self-hosted deployments without any deployment-specific
	// rewriting.
	ctx.FocusPathURL = focus.URL
	// LayoutRightStateClass flags pages that have above-focus siblings
	// rendered (= per-post pages, not /index.html). Drives the
	// stream-top-fade overlay's visibility AND the auto-scroll-on-init
	// path in stream.js (which hides the bottom-most above-focus entry
	// behind the fade and gives the user a scroll-bar position
	// suggesting "scroll up for more").
	if len(siblingsAbove) > 0 {
		// Leading space so the template concatenates cleanly when the
		// value is empty (no above-focus on /index.html).
		// Result on per-post pages: `<main class="layout-right show-top-fade">`.
		// Result on /index.html:    `<main class="layout-right">`.
		// JS-side updateTopFadeVisibility() takes over from init()
		// onward — the SSR-time class is for first-paint correctness
		// before JS runs.
		ctx.LayoutRightStateClass = " show-top-fade"
	}
	ctx.Version = fm["current-version"]
	if ctx.Version == "" {
		ctx.Version = fm["version"]
	}
	ctx.SignatureShort = template.TruncateSignature(fm["signature"], 16)

	// Site identity.
	ctx.SiteURL = r.config.BaseURL
	ctx.SiteTitle = r.getSiteTitle()
	ctx.CSSPath = streamRelativePath(outPath, r.config.DataDir, "styles.css")
	ctx.BaseCSSPath = streamRelativePath(outPath, r.config.DataDir, "base.css")
	// Absolute paths for HomePath + FaviconPath so they survive the stream
	// controller's pushState/replaceState URL-bar updates on scroll. Browsers
	// resolve relative <a href> against document.baseURI at click/hover time;
	// once the controller has stamped a /posts/YYYYMMDD/foo.html URL into the
	// bar (stream.js scroll-focus handler), a relative "index.html" or
	// "../../index.html" resolves wrong (e.g. yields /posts/20260429/index.html
	// instead of /index.html). Same hazard hit the favicon — fixed there
	// originally; widening to HomePath now since the bio card + wordmark links
	// were dropping users into 404'd post-dated index.html paths.
	ctx.HomePath = "/index.html"
	ctx.FaviconPath = "/favicon.svg"
	ctx.AuthorName = r.getAuthorName()
	if ctx.AuthorName == "" {
		ctx.AuthorName = r.getAuthorDomain()
	}
	ctx.AuthorURL = r.config.BaseURL
	ctx.AuthorDomain = r.getAuthorDomain()
	ctx.SiteDomain = r.getAuthorDomain()
	// Suppress duplicate name when author_name == domain. Without this,
	// tenants that publish author_name="<domain>" (e.g. discover.polis
	// .pub) render the handle twice in layout-left's site-identity
	// block — once as .site-name (bold) and once as .site-handle
	// (mono). page.go's per-post / homepage / archive paths apply the
	// same suppression; this is the v4 stream-shape equivalent.
	if strings.EqualFold(strings.TrimSpace(ctx.AuthorName), strings.TrimSpace(ctx.AuthorDomain)) {
		ctx.AuthorName = ""
	}
	ctx.PageType = "post"
	ctx.AvatarHTML = r.buildAvatarHTML()

	// .site-bio + .site-stats blocks on the layout-left card.
	r.populateSiteIdentity(ctx, totalPosts)

	// Blessed-comment thread for the focus post (step-05/5.i Phase B).
	// Loaded from the same source as v3 (`metadata.GetBlessedCommentsForPost`)
	// keyed off the focus's source-relative path. Matches v3's wiring at
	// page.go:248-252 — we set CommentCount alongside so the focus's
	// meta-line badge can substitute {{comment_count_class}} and
	// {{comment_count_display}} via engine.go's top-level mappings,
	// mirroring the per-iteration vars siblings get from sections.go.
	if blessedComments, err := r.loadBlessedCommentsForPost(srcRel); err == nil {
		ctx.BlessedComments = blessedComments
		ctx.BlessedCount = len(blessedComments)
		ctx.CommentCount = len(blessedComments)
	}

	// v4-specific vars.
	// CanonicalURL per D-CANONICAL: the focus post's own .html URL serves as
	// canonical for both per-post pages (self-canonical) AND /index.html
	// (focus = newest post = canonical target). Single formula handles both
	// because the renderStreamAll caller passes posts[0] as focus when emitting
	// /index.html, which is exactly the URL that /index.html should
	// canonical-point at.
	ctx.CanonicalURL = r.buildURL(strings.TrimPrefix(focus.URL, "/"))
	ctx.ControllerURL = "/stream.js"
	// Cache-bust the controller URL on each shape bump. Template emits
	// `<script src="{{controller_url}}?v={{stream_shape_version}}" defer>` so
	// browsers re-fetch /stream.js whenever StreamShapeVersion advances,
	// instead of holding onto a stale cached copy past its TTL.
	ctx.StreamShapeVersion = bundle.StreamShapeVersion

	// Metadata head-block (step-03/3.d). Per-post and index pages both go
	// through this code path; the index uses focus = newest post, so OG
	// fields naturally describe the newest post. JSON-LD type DIFFERS by
	// page kind: per-post = BlogPosting, index = WebSite (per schema.org
	// conventions; an index isn't a single article).
	//
	// All template-substituted values are HTML-attribute-escaped here because
	// the template engine itself does not escape variables (see engine.go's
	// substituteVariables — substitutions are raw). Escaping at the producer
	// keeps the template author from having to think about it.
	desc := buildOGDescription(body)
	if desc == "" {
		desc = ctx.Title // fallback so og:description is never empty
	}
	ctx.OGTitle = attrEscape(ctx.Title)
	ctx.OGDescription = attrEscape(desc)
	ctx.OGImageURL = attrEscape(r.buildURL("favicon.svg")) // worker decision (a) per plan §3.d
	ctx.ISOPublished = formatISO(focus.Published)
	ctx.ISOModified = formatISO(fm["updated"])
	if ctx.ISOModified == "" {
		ctx.ISOModified = ctx.ISOPublished
	}

	isIndex := outPath == filepath.Join(r.config.DataDir, "index.html")
	var jsonLD string
	if isIndex {
		// Index gets a WebSite JSON-LD describing the tenant as a whole. The
		// canonical URL here is the index's canonical (= newest post per
		// D-CANONICAL); we use the tenant root as the WebSite URL to match
		// schema.org WebSite conventions.
		jsonLD, err = buildWebSiteJSONLD(ctx.SiteTitle, r.buildURL(""), desc)
	} else {
		jsonLD, err = buildBlogPostingJSONLD(
			ctx.Title,
			ctx.ISOPublished,
			ctx.ISOModified,
			ctx.AuthorName,
			ctx.AuthorURL,
			ctx.CanonicalURL,
		)
	}
	if err != nil {
		return fmt.Errorf("build JSON-LD: %w", err)
	}
	ctx.JSONLD = jsonLD
	ctx.WidgetHome = r.config.BaseURL
	ctx.WidgetHandle = streamHandleFromDomain(ctx.AuthorDomain)
	ctx.WidgetHost = streamHostFromDomain(ctx.AuthorDomain)

	// Re-format sibling PublishedHuman with the v4 date+time shape so
	// SSR'd siblings match the focus + the JS-paginated entries (which
	// pull published_human from /api/v1/stream/items, also FormatHumanDateTime).
	// Slice mutation is local to this function — caller's siblings slice
	// isn't shared across pages because pickStreamSiblings returns a fresh
	// slice per focus. Both rails get the same treatment.
	v4SiblingsAbove := make([]template.PostData, len(siblingsAbove))
	copy(v4SiblingsAbove, siblingsAbove)
	for i := range v4SiblingsAbove {
		v4SiblingsAbove[i].PublishedHuman = template.FormatHumanDateTime(v4SiblingsAbove[i].Published)
	}
	ctx.SiblingsAbove = v4SiblingsAbove

	v4SiblingsBelow := make([]template.PostData, len(siblingsBelow))
	copy(v4SiblingsBelow, siblingsBelow)
	for i := range v4SiblingsBelow {
		v4SiblingsBelow[i].PublishedHuman = template.FormatHumanDateTime(v4SiblingsBelow[i].Published)
	}
	ctx.Siblings = v4SiblingsBelow

	rendered, err := r.engine.Render(r.templates.Stream, ctx)
	if err != nil {
		return fmt.Errorf("render stream template: %w", err)
	}
	rendered = StripHTMLComments(rendered)

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(outPath, []byte(rendered), 0644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

// renderStreamEmptyIndex writes a minimal index.html for tenants with zero posts.
// Kept intentionally thin — the site still needs an entry page so the domain
// resolves to something sensible, but there's no stream content to render.
func (r *PageRenderer) renderStreamEmptyIndex() error {
	if err := os.MkdirAll(r.config.DataDir, 0755); err != nil {
		return err
	}
	siteTitle := r.getSiteTitle()
	author := r.getAuthorName()
	if author == "" {
		author = r.getAuthorDomain()
	}
	// Empty-corpus canonical: self-canonical to the tenant's index URL. There
	// are no posts to point at, but emitting a canonical (vs. omitting one)
	// keeps crawlers from picking some other URL as canonical via header
	// heuristics. Decision recorded in step-03 Execution Report (3.b specific).
	canonical := r.buildURL("index.html")
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en"><head>
<meta charset="UTF-8">
<title>%s</title>
<link rel="canonical" href="%s">
<link rel="stylesheet" href="base.css">
<link rel="stylesheet" href="styles.css">
</head><body>
<main class="focus-content" data-polis-focus="true">
  <h1>%s</h1>
  <p>No posts yet.</p>
</main>
</body></html>
`, attrEscape(siteTitle), attrEscape(canonical), attrEscape(author))
	return os.WriteFile(filepath.Join(r.config.DataDir, "index.html"), []byte(body), 0644)
}

// streamSourceMDPath maps a post's output URL ("/posts/YYYYMMDD/slug.html") to its
// content-source relative path ("content/pub.polis.core/post/YYYYMMDD/slug.md").
// Falls back to treating the URL suffix as already-source if mount dirs aren't
// configured (legacy / test paths).
func streamSourceMDPath(focusURL, sourceDir, mountDir string) (string, error) {
	trimmed := strings.TrimPrefix(focusURL, "/")
	trimmed = strings.TrimSuffix(trimmed, ".html") + ".md"

	if mountDir != "" && sourceDir != "" && strings.HasPrefix(trimmed, mountDir+"/") {
		return filepath.Join(sourceDir, strings.TrimPrefix(trimmed, mountDir+"/")), nil
	}
	// Legacy: URL already refers to source path.
	return trimmed, nil
}

// streamRelativePath computes a relative href from outPath back to targetRel, both
// expressed relative to dataDir. For posts/YYYYMMDD/slug.html → styles.css,
// the result is "../../styles.css".
func streamRelativePath(outPath, dataDir, targetRel string) string {
	rel, err := filepath.Rel(filepath.Dir(outPath), filepath.Join(dataDir, targetRel))
	if err != nil {
		return targetRel
	}
	return filepath.ToSlash(rel)
}

// streamHandleFromDomain extracts a local-handle component from a domain: "alice"
// from "alice.polis.pub". Returns the full domain unchanged if no landlord
// suffix is present. Purely cosmetic for the hosted nav widget's data-handle
// attribute; the widget falls back to the full domain when absent.
func streamHandleFromDomain(domain string) string {
	if idx := strings.Index(domain, "."); idx > 0 {
		return domain[:idx]
	}
	return domain
}

// streamHostFromDomain extracts the landlord component: "polis.pub" from
// "alice.polis.pub". Inverse of streamHandleFromDomain.
func streamHostFromDomain(domain string) string {
	if idx := strings.Index(domain, "."); idx > 0 {
		return domain[idx+1:]
	}
	return ""
}

