// =============================================================================
// HANDBOOK TRAIL MARKER — DS-to-stream thread (webapp stream surface)
// =============================================================================
// This file exposes the webapp's stream HTTP endpoints to the SPA, including
// the comment-count stamping that backs Path B (on-demand aggregation).
// populateCrossTenantCommentCounts batches every post on the rendered page
// into one synchronous DS counts call (bounded by a strict timeout) so the
// badge is deterministic each render, with a short-lived local cache to skip
// the roundtrip across requests.
//
// Trail across files (DS-to-stream, aggregation):
//   webapp surface — this file (populateCrossTenantCommentCounts)
//   DS endpoint    — discovery-service/core/handlers/counts.ts (getCommentCounts)
//
// Companion to the continuous sync path:
//   producer       — webapp/internal/server/sync.go: runUnifiedSync()
//
// Pull the thread:
//   github.com/vdibart/polis-cli/blob/main/docs/handbook/ds-to-stream.md      (tour)
//   github.com/vdibart/polis-cli/blob/main/docs/ds/developer/api-reference.md
//   github.com/vdibart/polis-cli/blob/main/AGENTS.md                          (map)
// =============================================================================

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	"github.com/vdibart/polis-cli/cli-go/pkg/template"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// Comment-count stamping constants. The whole page's uncached counts are
// fetched in ONE batched DS call (the counts endpoint accepts up to
// streamCountBatchMax URLs per request) synchronously, so every post on the
// page is stamped deterministically each render. A prior visibleHorizon
// heuristic (sync the first N, background the rest) left below-the-fold posts
// — and any post on a cold cache — showing 0 until a later render warmed the
// cache, then dropping back to 0 when the TTL expired: the badge flipped
// 0↔1↔0 across navigations. Overflow beyond the batch cap (only on an
// explicit large `limit`) falls back to a background fill.
const (
	streamCountBatchMax         = 50 // matches DS COMMENT_COUNTS_MAX_URLS
	visibleCountFetchTimeout    = 500 * time.Millisecond
	backgroundCountFetchTimeout = 5 * time.Second
)

// streamItemResponse wraps a CachedFeedItem with a server-formatted
// published_human field. The stream's SSR'd entries (focus + siblings)
// format dates via template.FormatHumanDateTime; this endpoint emits the
// same format so JS-appended pagination entries match the SSR'd ones
// without a client-side formatter (SC-3 close — step-05/5.g). Embedding
// the source struct preserves the full feed-item JSON shape; only the
// extra published_human key is added.
type streamItemResponse struct {
	feed.CachedFeedItem
	PublishedHuman string `json:"published_human"`

	// Enrichment fields populated only for type=comments thread-entry
	// items (own-handle scope). The stream's renderCommentThread
	// reads these to render the full target post inline + the local
	// comment body underneath. All omitempty so non-thread items
	// don't carry empty fields.
	TargetPostTitle    string `json:"target_post_title,omitempty"`
	TargetPostBodyHTML string `json:"target_post_body_html,omitempty"`
	// TargetPostTitleRedundant flags posts whose body's first prose
	// sentence already begins with the title text (rendered as
	// **bold-prefixed** markdown is common). The stream's renderCommentThread
	// hides the explicit title chrome (.is-redundant) when this is true so
	// the body doesn't visually duplicate the title. Computed via
	// render.TitleStartsFirstSentence on the body markdown after
	// render.StripLeadingTitleHeading has dropped any matching leading
	// ATX heading.
	TargetPostTitleRedundant bool   `json:"target_post_title_redundant,omitempty"`
	TargetPostPublished      string `json:"target_post_published,omitempty"`
	TargetPostPublishedHuman string `json:"target_post_published_human,omitempty"`
	TargetPostAuthorDomain   string `json:"target_post_author_domain,omitempty"`
	// Avatar config for the PARENT POST's author. The activity-view comment-
	// thread floats the post author's avatar in the post region (the comments
	// view instead puts the avatar on the commenter, via author_avatar above).
	// Cross-author only; hue fallback when the author has no custom avatar.
	TargetPostAuthorAvatar *render.AvatarConfig `json:"target_post_author_avatar,omitempty"`
	CommentBodyHTML        string               `json:"comment_body_html,omitempty"`

	// BlessedComments populates only for type=posts items on own-handle
	// scope when the post has blessed comments (with=comments narrows the
	// page to such posts). Lets the stream's renderPost emit an inline
	// .entry-comments-panel without a follow-up fetch — same data shape
	// the SSR'd per-post focus entry already renders via
	// snippets/blessed-comment.html.
	BlessedComments []blessedCommentResponse `json:"blessed_comments,omitempty"`

	// TitleRedundant flags posts whose body's first sentence already begins
	// with the title text. Same detection (StripLeadingTitleHeading +
	// TitleStartsFirstSentence on raw markdown) the SSR sibling-render
	// path uses for static stream output (render/stream.go), brought to
	// the dynamic API path so JS-rendered posts get the same title-chrome
	// suppression. The stream's renderPost (excerpt form) and injectBody
	// (expanded form) both honor this — the dataset attribute carries it
	// across the lazy-fetch body swap.
	TitleRedundant bool `json:"title_redundant,omitempty"`

	// BodyHTML carries the full post body rendered to HTML for cross-tenant
	// post entries. v4/public renders bodies inline (SSR'd, same-origin
	// reads); the SPA's lazy-fetch fallback skips cross-origin URLs due
	// to CORS, leaving cross-tenant entries stuck on the truncated excerpt.
	// This field ships the rendered body alongside the API response so
	// renderPost's existing meta.body_html branch (already wired for
	// drafts) injects the expanded body without a follow-up fetch. Own-
	// tenant entries keep the lazy-fetch path (same-origin works).
	BodyHTML string `json:"body_html,omitempty"`

	// SourcePath populates only for own-handle content (posts + comments).
	// It carries the storage-form path (`content/pub.polis.core/post/...md`
	// or `content/pub.polis.core/comment/...md`) so owner-side decorators
	// can drive operations that key off the source path — most importantly
	// `/api/unpublish` which validates the prefix and reads the on-disk
	// file directly. Without this field, owner-extras would have to derive
	// the source path from URL (the rendered .html mount-path), which is
	// brittle and duplicates `loadOwnContentAsFeedItems`'s forward
	// mapping. Empty for cross-tenant items (the unpublish action doesn't
	// apply to them).
	SourcePath string `json:"source_path,omitempty"`

	// AuthorAvatar carries the entry author's avatar config (the post author
	// for posts, the commenter for comments) for CROSS-AUTHOR entries — the
	// stream's renderPost / renderCommentThread build the floated 28px avatar
	// from it. Omitted for own-author entries (identity suppressed, "I know
	// who I am"). Populated by buildAuthorAvatars; a deterministic hue
	// fallback is synthesized when the author has no custom avatar, so this
	// is always present for cross-author items. cfg field names are
	// snake_case (bg/fg/border/border_w/pattern/pattern_color) to match the
	// JS buildAvatar consumer.
	AuthorAvatar *render.AvatarConfig `json:"author_avatar,omitempty"`
}

// blessedCommentResponse is the JSON shape the stream emits per blessed
// comment on a post item. Mirrors template.BlessedCommentData (the SSR
// struct that drives snippets/blessed-comment.html) so the stream's
// renderPost can build identical DOM to the SSR path. ContentHTML is
// goldmark-rendered + bluemonday-sanitized (see render.LoadLocalCommentContent
// — same trust chain as the focus entry's inline comments).
type blessedCommentResponse struct {
	URL            string               `json:"url"`
	AuthorName     string               `json:"author_name"`
	AuthorDomain   string               `json:"author_domain,omitempty"`
	AuthorAvatar   *render.AvatarConfig `json:"author_avatar,omitempty"`
	Published      string               `json:"published,omitempty"`
	PublishedHuman string               `json:"published_human"`
	ContentHTML    string               `json:"content_html"`
}

// commentAuthorAvatar resolves the avatar config for a comment author so the
// read-focus card can render the same floated avatar the canonical per-post
// page shows (render.loadBlessedCommentsForPost uses the identical fetch +
// fallback). Cached in render's authorAvatarCache (6h), shared with the SSR
// render path in this process, so a warmed canonical page means no extra
// fetch here. Returns nil for an empty domain.
func (s *Server) commentAuthorAvatar(domain string) *render.AvatarConfig {
	if domain == "" {
		return nil
	}
	cfg, ok := render.LoadOrFetchAuthorAvatar(domain)
	if !ok || cfg.BG == "" {
		cfg = render.FallbackAvatarConfig(domain)
	}
	return &cfg
}

// focusCommentResponse is the JSON shape GET /api/v1/stream/focus-comment
// returns for a single post. Read-focus mode shows exactly ONE comment — the
// most recent on the post, blessed or not — sourced from the DS latest-comment
// endpoint with its body fetched from the commenter's origin. HasComment=false
// means the post has no comments (or the lookup/fetch failed); the SPA opens
// focus with no comment panel and drives the full thread to the origin site.
// Comment reuses blessedCommentResponse so the SPA's buildCommentsPanel
// consumes it unchanged.
type focusCommentResponse struct {
	HasComment bool                    `json:"has_comment"`
	Comment    *blessedCommentResponse `json:"comment,omitempty"`
}

// commentThreadEnrichment carries the per-item enrichment data for
// type=comments thread entries. Keyed in a side map by the item's URL
// (the comment URL) to avoid extending the shared CachedFeedItem
// struct with v4-stream-specific fields.
type commentThreadEnrichment struct {
	TargetPostTitle          string
	TargetPostBodyHTML       string
	TargetPostTitleRedundant bool
	TargetPostPublished      string
	TargetPostPublishedHuman string
	TargetPostAuthorDomain   string
	TargetPostAuthorAvatar   *render.AvatarConfig
	CommentBodyHTML          string
}

func toStreamItemResponses(items []feed.CachedFeedItem, enrich map[string]commentThreadEnrichment, postComments map[string][]blessedCommentResponse, titleRedundant map[string]bool, sourcePaths map[string]string, postBodies map[string]string, authorAvatars map[string]*render.AvatarConfig) []streamItemResponse {
	out := make([]streamItemResponse, len(items))
	for i, it := range items {
		resp := streamItemResponse{
			CachedFeedItem: it,
			// StripYear: the v4 stream drops the per-entry year (year-markers
			// segment the stream) so the comment-count column sits closer to
			// the left. JS renderers consume this already-stripped string.
			PublishedHuman: template.StripYear(template.FormatHumanDateTime(it.Published)),
		}
		// Keyed by the pre-normalized it.URL (same convention as the maps
		// above). Present only for cross-author items (own author skipped
		// in buildAuthorAvatars).
		if authorAvatars != nil {
			if av := authorAvatars[it.URL]; av != nil {
				resp.AuthorAvatar = av
			}
		}
		if e, ok := enrich[it.URL]; ok {
			resp.TargetPostTitle = e.TargetPostTitle
			resp.TargetPostBodyHTML = e.TargetPostBodyHTML
			resp.TargetPostTitleRedundant = e.TargetPostTitleRedundant
			resp.TargetPostPublished = e.TargetPostPublished
			resp.TargetPostPublishedHuman = e.TargetPostPublishedHuman
			resp.TargetPostAuthorDomain = e.TargetPostAuthorDomain
			resp.TargetPostAuthorAvatar = e.TargetPostAuthorAvatar
			resp.CommentBodyHTML = e.CommentBodyHTML
		}
		if cs, ok := postComments[it.URL]; ok && len(cs) > 0 {
			resp.BlessedComments = cs
		}
		if titleRedundant != nil && titleRedundant[it.URL] {
			resp.TitleRedundant = true
		}
		if postBodies != nil {
			if html, ok := postBodies[it.URL]; ok && html != "" {
				resp.BodyHTML = html
			}
		}
		// Lookup uses the rendered URL key (pre-NormalizeToHTML below
		// it's the full `/posts/...html` form set by the loader, which
		// matches the keying convention sourcePaths uses).
		if sourcePaths != nil {
			if sp, ok := sourcePaths[it.URL]; ok {
				resp.SourcePath = sp
			}
		}
		// Storage convention is .md (canonical signing target); display
		// surfaces want the .html mount-path form so links resolve to a
		// browsable page. Convert the comment URL + the target URL on
		// the way out — internal lookups still use the .md form via
		// CachedFeedItem.URL / TargetURL.
		resp.URL = polisurl.NormalizeToHTML(resp.URL)
		resp.TargetURL = polisurl.NormalizeToHTML(resp.TargetURL)
		out[i] = resp
	}
	return out
}

// buildAuthorAvatars resolves the avatar config for every CROSS-AUTHOR entry
// on the page (the post author for posts, the commenter for comments). Own-
// author entries are skipped (identity suppressed). Each unique remote domain
// is fetched once via render.LoadOrFetchAuthorAvatar (bounded to 5 concurrent);
// a deterministic hue fallback is synthesized when the author has no custom
// avatar, so cross-author items always get a config. Returns a map keyed by
// the item URL (pre-NormalizeToHTML form, matching the other enrichment maps).
func (s *Server) buildAuthorAvatars(items []feed.CachedFeedItem) map[string]*render.AvatarConfig {
	siteDomain := extractDomainFromURL(s.GetBaseURL())

	uniqueDomains := make(map[string]bool)
	for _, it := range items {
		if it.AuthorDomain == "" || it.AuthorDomain == siteDomain {
			continue
		}
		uniqueDomains[it.AuthorDomain] = true
	}
	if len(uniqueDomains) == 0 {
		return nil
	}

	type result struct {
		domain string
		cfg    render.AvatarConfig
	}
	results := make(chan result, len(uniqueDomains))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for domain := range uniqueDomains {
		wg.Add(1)
		sem <- struct{}{}
		go func(d string) {
			defer wg.Done()
			defer func() { <-sem }()
			cfg, ok := render.LoadOrFetchAuthorAvatar(d)
			if !ok || cfg.BG == "" {
				cfg = render.FallbackAvatarConfig(d)
			}
			results <- result{domain: d, cfg: cfg}
		}(domain)
	}
	wg.Wait()
	close(results)

	byDomain := make(map[string]render.AvatarConfig, len(uniqueDomains))
	for r := range results {
		byDomain[r.domain] = r.cfg
	}

	out := make(map[string]*render.AvatarConfig)
	for _, it := range items {
		if cfg, ok := byDomain[it.AuthorDomain]; ok {
			c := cfg // copy so each pointer is independent
			out[it.URL] = &c
		}
	}
	return out
}

// Structured-query endpoint for the stream filter (plan §4.d).
//
// GET /api/v1/stream/items?qualifier={new,all}&type={posts,comments,profiles,
//                          mentions,dm}&scope={my,my-network,all-polis,@handle}
//                          &time={1h,24h,2d,7d,30d,all}&order={newest,oldest}
//                          &cursor={opaque}&limit={int}
//
// Response: {items: [...], next_cursor: "..."}
//
// Auth posture: matches the existing /api/feed handler — no explicit auth
// check at this layer. Polis-server is owner-trust by construction:
//   - Localhost dev: the owner runs polis-server on their own machine.
//   - Hosted multi-tenant: session auth at the hosted routing layer ensures
//     only the tenant's authenticated session reaches their polis-server.
// DMs are owner-private under this same model — only the owner's session
// can reach the tenant's polis-server in the first place.
//
// Cursor format: base64-encoded JSON {"after_published": "ISO8601"} (newest
// order) or {"before_published": "ISO8601"} (oldest order). Stable under
// concurrent feed writes — new items inserted at the top don't shift older
// pages because pagination is published-timestamp-anchored, not offset-
// anchored.

const (
	defaultStreamLimit = 50
	maxStreamLimit     = 200
	// maxStreamWindowDays caps the look-back window the server is willing
	// to scan, regardless of what the client asks for. `time=all` and any
	// window larger than this get clamped down. The point is to bound the
	// per-request item count + downstream enrichment cost; users wanting
	// older content go to the origin site directly. Tunable dial.
	maxStreamWindowDays = 30
)

// maxStreamWindow is the time.Duration form of the cap, derived once.
var maxStreamWindow = time.Duration(maxStreamWindowDays) * 24 * time.Hour

var validStreamTypes = map[string]string{
	"posts":    "post",
	"comments": "comment",
	// 06-profiles: "follows" removed from grammar. Follow events fold
	// into ACTIVITY as standard entries (see appendFollowSignal in
	// owner-extras.js). The "profiles" type now has its own dispatch
	// (handleStreamItemsProfiles below), backed by the same
	// loadOwnFollowsAsFeedItems data source for scope=my-network in
	// Phase 1; Phase 2 enriches with recent-post + relationship.
	"profiles": "follow", // dispatch short-circuits before feed-type lookup
	"mentions": "",       // no feed-cache representation today; returns empty
	"dm":       "",
	"dms":      "", // step-06/6.b: alias for dm (grammar uses plural; zero migration)
	// step-06/6.b: drafts read from local .polis/bundles/.../posts/drafts.
	// scope locked to me — see drafts dispatch below. Empty feed-cache mapping
	// because the dispatch short-circuits before the cache read.
	"drafts": "",
	// step-06/6.b: activity is the aggregate type. Returns mixed items
	// (no per-type filter) within the chosen scope. Render-mode visual
	// treatment lives in 6.f.2; here it's a no-op (empty feedType lets
	// the cache return all types).
	"activity": "",
	"":         "", // empty = all types from feed cache
}

var validStreamTimes = map[string]time.Duration{
	"1h":  1 * time.Hour,
	"24h": 24 * time.Hour,
	"2d":  48 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"all": 0, // sentinel: no time filter
}

// streamCursor encodes pagination state. Anchored on the last item's
// published timestamp so new items at the top of the feed don't disturb
// pagination of older pages. Includes a per-item ID tiebreaker so items
// sharing a timestamp at the page boundary (batch publishes, concurrent
// writes landing in the same RFC3339 second) don't get dropped or
// double-included.
type streamCursor struct {
	BeforePublished string `json:"before,omitempty"`   // newest-first: next page items published BEFORE this
	BeforeID        string `json:"beforeId,omitempty"` // tiebreaker for items at exactly BeforePublished
	AfterPublished  string `json:"after,omitempty"`    // oldest-first: next page items published AFTER this
	AfterID         string `json:"afterId,omitempty"`  // tiebreaker for items at exactly AfterPublished
}

func encodeStreamCursor(c streamCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeStreamCursor(s string) (streamCursor, error) {
	var c streamCursor
	if s == "" {
		return c, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, fmt.Errorf("decode cursor: %w", err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse cursor: %w", err)
	}
	return c, nil
}

// handleStreamItems implements GET /api/v1/stream/items.
func (s *Server) handleStreamItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	startTime := time.Now()
	q := r.URL.Query()
	qualifier := strings.ToLower(q.Get("qualifier"))
	itemType := strings.ToLower(q.Get("type"))
	scope := strings.ToLower(q.Get("scope"))
	timeWindow := strings.ToLower(q.Get("time"))
	order := strings.ToLower(q.Get("order"))
	// step-06/6.b: sort= is a type-conditional modifier distinct from order=.
	// Grammar: [follows: by date | by name], [drafts: by date | by name].
	// Accepts "by-date" (default) and "by-name" (alphabetic by domain for
	// follows, by title for drafts). Other types must use the default.
	sortBy := strings.ToLower(q.Get("sort"))
	cursorParam := q.Get("cursor")
	// before_url (step-05/5.c): convenience for the stream's initial
	// pagination call from a per-post page. The controller has the SSR'd
	// oldest-sibling URL but no cursor — passing before_url=<url> lets the
	// server look up the item in the result set and construct the cursor
	// for it, avoiding any client-side knowledge of the cursor format.
	// Ignored if `cursor` is also given.
	beforeURL := q.Get("before_url")
	// after_url is the symmetric upward-pagination convenience: scroll-up
	// on a per-post page anchors on the SSR'd focus URL and asks for the
	// next batch of items NEWER than it. Same URL→cursor synthesis as
	// before_url; populates AfterPublished/AfterID instead. Ignored if
	// `cursor` is also given. before_url + after_url are mutually
	// exclusive in practice (either anchor older OR newer of the loaded
	// stream); when both are passed before_url wins (matches the
	// controller's first-fetch convention).
	afterURL := q.Get("after_url")
	// with= is a content-type-specific modifier added 2026-04-29 with the
	// filter-grammar redesign. Currently the only valid value is
	// "comments" (paired with type=posts) — limits results to posts that
	// have at least one blessed comment. Empty (default) preserves the
	// pre-redesign behavior: no extra filter.
	withParam := strings.ToLower(q.Get("with"))

	// Defaults
	if qualifier == "" {
		qualifier = "all"
	}
	if scope == "" {
		// DMs default to my-mutuals (the inbox); everything else defaults
		// to my-network. The DM dispatch (chunk C) rejects my-network /
		// me / all-polis for type=dms — they don't make sense for 1:1
		// conversations between mutuals — so the global my-network
		// default would 400 every DM request that omitted scope.
		if itemType == "dm" || itemType == "dms" {
			scope = "my-mutuals"
		} else {
			scope = "my-network"
		}
	}
	if timeWindow == "" {
		timeWindow = "24h"
	}
	if order == "" {
		order = "newest"
	}

	// Wrap the response writer so the deferred observability emit
	// below can read the final status code on both success (200) and
	// error (4xx/5xx) paths. statusResponseWriter is the same shape
	// used by middleware request logging — captured at write time, no
	// behavioral change to callers downstream.
	sw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	w = sw

	// pub.polis.stream.items_fetched — single emit point for the most-
	// hit v4 endpoint. Captures the structured filter (qualifier/type/
	// scope/time_window/order/sort/with), the response shape
	// (item_count, has_cursor, next_cursor), the outcome (status,
	// duration_ms), and the request_id for cross-boundary correlation.
	// Closes the §3 gap in plans/v4-deploy-readiness.md: prior to this
	// emit, handleStreamItems was responsible for every filter-driven
	// fetch in the v4 SPA and had zero structured event coverage.
	//
	// itemCount + nextCursorSet are populated by the success path before
	// the response writes; error paths leave them at zero. The deferred
	// closure reads them at return time.
	var (
		itemCount     int
		nextCursorSet bool
	)
	defer func() {
		s.LogEvent("pub.polis.stream.items_fetched", map[string]interface{}{
			"qualifier":   qualifier,
			"type":        itemType,
			"scope":       scope,
			"time_window": timeWindow,
			"order":       order,
			"sort":        sortBy,
			"with":        withParam,
			"item_count":  itemCount,
			"has_cursor":  cursorParam != "" || beforeURL != "" || afterURL != "",
			"next_cursor": nextCursorSet,
			"status":      sw.statusCode,
			"duration_ms": time.Since(startTime).Milliseconds(),
			"request_id":  RequestIDFromContext(r.Context()),
		})
	}()

	// Validate qualifier
	if qualifier != "new" && qualifier != "all" {
		writeStreamError(w, http.StatusBadRequest, "qualifier must be 'new' or 'all'")
		return
	}

	// Validate type (empty == all)
	feedType, typeOK := validStreamTypes[itemType]
	if !typeOK {
		writeStreamError(w, http.StatusBadRequest,
			fmt.Sprintf("type must be one of: posts, comments, activity, drafts, profiles, mentions, dm/dms (got %q)", itemType))
		return
	}
	// step-06/6.b: normalize dms → dm so downstream branches don't double-check.
	if itemType == "dms" {
		itemType = "dm"
	}

	// Validate with= modifier.
	//   - "comments" pairs with type=posts: filter to posts that have at
	//     least one blessed comment.
	//   - "pending-blessing" (step-06/6.b) pairs with type=comments:
	//     filter to comment-blessing requests awaiting action by the
	//     local tenant. Only valid on scope=all-polis (semantically: the
	//     "comments to bless" preset surfaces inbound requests from
	//     anyone in the network).
	//   - empty (default) means no extra filter.
	if withParam != "" && withParam != "comments" && withParam != "pending-blessing" {
		writeStreamError(w, http.StatusBadRequest,
			fmt.Sprintf("with must be empty, 'comments', or 'pending-blessing' (got %q)", withParam))
		return
	}
	if withParam == "comments" && feedType != "post" {
		writeStreamError(w, http.StatusBadRequest,
			"with=comments only valid when type=posts")
		return
	}
	if withParam == "pending-blessing" && feedType != "comment" {
		writeStreamError(w, http.StatusBadRequest,
			"with=pending-blessing only valid when type=comments")
		return
	}

	// step-06/6.b → 06-profiles: validate sort=. Type-conditional defaults
	// and allowed values:
	//   - profiles: by-name (default), by-activity. Reflects the third-
	//     clause grammar "all profiles from <scope> by (name | activity)".
	//   - drafts:   by-date (default), by-name (alphabetic by title).
	//   - others:   by-date (default, no-op — primary ordering is
	//                timestamp-based via order=newest|oldest).
	// Cross-type misuse (e.g. by-activity on posts) is rejected here.
	if sortBy == "" {
		if itemType == "profiles" {
			sortBy = "by-name"
		} else {
			sortBy = "by-date"
		}
	}
	switch itemType {
	case "profiles":
		if sortBy != "by-name" && sortBy != "by-activity" {
			writeStreamError(w, http.StatusBadRequest,
				fmt.Sprintf("sort for type=profiles must be 'by-name' or 'by-activity' (got %q)", sortBy))
			return
		}
	case "drafts":
		if sortBy != "by-date" && sortBy != "by-name" {
			writeStreamError(w, http.StatusBadRequest,
				fmt.Sprintf("sort for type=drafts must be 'by-date' or 'by-name' (got %q)", sortBy))
			return
		}
	default:
		if sortBy != "by-date" {
			writeStreamError(w, http.StatusBadRequest,
				fmt.Sprintf("sort for type=%s must be 'by-date' (got %q)", itemType, sortBy))
			return
		}
	}

	// Validate time window
	timeDelta, timeOK := validStreamTimes[timeWindow]
	if !timeOK {
		writeStreamError(w, http.StatusBadRequest,
			fmt.Sprintf("time must be one of: 1h, 24h, 2d, 7d, 30d, all (got %q)", timeWindow))
		return
	}
	// Clamp the window to maxStreamWindow. `time=all` arrives as timeDelta=0
	// (the no-filter sentinel) and gets capped here; any future window
	// larger than the cap likewise collapses to maxStreamWindow.
	if timeDelta == 0 || timeDelta > maxStreamWindow {
		timeDelta = maxStreamWindow
	}

	// Validate order
	if order != "newest" && order != "oldest" {
		writeStreamError(w, http.StatusBadRequest, "order must be 'newest' or 'oldest'")
		return
	}

	// Validate scope
	scopeKind, scopeArg, err := parseStreamScope(scope)
	if err != nil {
		writeStreamError(w, http.StatusBadRequest, err.Error())
		return
	}

	// step-06/6.c review follow-up (review concern #1): tighten the
	// with=pending-blessing constraint to require scope=all-polis.
	// The "comments to bless" preset (comment icon → 6.e) surfaces
	// inbound blessing requests from anyone in the network — so
	// pairing pending-blessing with any other scope is nonsensical.
	// Locking the contract here prevents a silent-accept-then-ignore
	// footgun when the data wiring lands.
	// TODO(step-06/6.e): wire to blessing.FetchPendingRequests for
	// the actual data path (currently with=pending-blessing parses
	// but is a no-op on the cache filter — preset returns all
	// network comments unfiltered until 6.e).
	if withParam == "pending-blessing" && scopeKind != "all-polis" {
		writeStreamError(w, http.StatusBadRequest,
			"with=pending-blessing requires scope=all-polis")
		return
	}

	// No explicit auth gate — owner-trust model (see header comment).

	// Short-circuit types with no feed-cache representation today
	// (mentions). The endpoint contract is uniform — same validation
	// applies — but skip the cache fetch + sort + paginate pipeline.
	// 4.e adds upstream representation; until then, return empty
	// quickly. Placed BEFORE DM and cursor decode so we don't pay
	// for cursor parsing either.
	//
	// profiles dispatch lives just below — handled like drafts (own
	// dispatch, no cursor pagination yet, type-conditional sort).
	if itemType == "mentions" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []interface{}{},
		})
		return
	}

	// step-06/6.b: drafts dispatch. Reads from .polis/bundles/pub.polis.core/
	// posts/drafts/*.md and returns one item per draft. scope locked to
	// "my" (drafts are private; other scopes are nonsensical). The dispatch
	// does its own pagination + sort + JSON write; doesn't fall through to
	// the feed-cache path below.
	if itemType == "drafts" {
		if scopeKind != "my" {
			writeStreamError(w, http.StatusBadRequest,
				fmt.Sprintf("type=drafts requires scope=me (got scope=%s)", scope))
			return
		}
		s.handleStreamItemsDrafts(w, sortBy, parseStreamLimit(q.Get("limit")))
		return
	}

	// 06-profiles: profiles dispatch. Scope=my-network (or its aliases
	// me / my / @<myDomain>) reads outbound + inbound follow data via
	// loadOwnFollowsAsFeedItems and applies the by-name / by-activity
	// modifier. Scope=all-polis is reserved for Phase 3 (DS site
	// directory) and returns empty for now. Other scopes 400 — we
	// don't expose another tenant's follow list.
	if itemType == "profiles" {
		myDomain := extractDomainFromURL(s.GetBaseURL())
		// Possessive-network form (`@<handle>:network`) is the public
		// visitor surface — visible to anyone, but the handle must
		// match the serving tenant (asking another tenant for someone
		// else's network would have to go through DS, which isn't the
		// shape this surface wants). Treat it as "my-network" for the
		// data path so the same buildProfilesList helper covers both
		// the owner-SPA scope=my-network and the public scope=@<this
		// tenant>:network views.
		isOwnSurface := scopeKind == "my" ||
			(scopeKind == "handle" && scopeArg != "" && scopeArg == myDomain) ||
			(scopeKind == "handle-network" && scopeArg != "" && scopeArg == myDomain) ||
			scopeKind == "my-network"
		if !isOwnSurface && scopeKind != "all-polis" {
			writeStreamError(w, http.StatusBadRequest,
				fmt.Sprintf("type=profiles supports scope in {me, my-network, @<this-tenant>, @<this-tenant>:network, all-polis} (got scope=%s)", scope))
			return
		}
		profileScope := "my-network"
		if scopeKind == "all-polis" {
			profileScope = "all-polis"
		}
		s.handleStreamItemsProfiles(w, r, profileScope, sortBy,
			q.Get("search"), q.Get("cursor"), parseStreamLimit(q.Get("limit")))
		return
	}

	// Decode cursor
	cursor, err := decodeStreamCursor(cursorParam)
	if err != nil {
		writeStreamError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Limit
	limit := parseStreamLimit(q.Get("limit"))

	// DM dispatch. Two shapes, branched on scope:
	//   - scope=my-mutuals (or empty)   → inbox: one entry per active
	//                                     conversation with the latest
	//                                     decrypted preview
	//   - scope=@<handle>.polis.pub     → thread: every message in that
	//                                     one conversation, most-recent
	//                                     on top, decrypted server-side
	// Other scopes (me / my-network / all-polis) are rejected — DMs are
	// 1:1 between mutuals and don't have meaningful semantics for those.
	if itemType == "dm" {
		switch scopeKind {
		case "", "my-mutuals":
			s.handleStreamItemsDM(w, r, cursor, limit, order, qualifier)
		case "handle":
			s.handleStreamItemsDMThread(w, r, scopeArg, cursor, limit, order)
		default:
			writeStreamError(w, http.StatusBadRequest,
				fmt.Sprintf("type=dms requires scope=my-mutuals or scope=@<handle>.polis.pub (got scope=%s)", scope))
		}
		return
	}

	// Build items: own-tenant scope reads from local sources (public
	// index, following.json, FollowerState projection, blessed.json) —
	// no DS roundtrip at request time. The network feed cache
	// intentionally omits self-authored events (see feed.FeedHandler's
	// self-skip), so routing scope=my through it would return 0 in
	// production despite the data being on disk locally.
	//
	// step-06/6.e broader fix: scope=my (URL-token form) and
	// scope=@<myDomain> (handle form) are both "this tenant" — same
	// owner, same local data. Both must route through the local
	// loaders (loadOwnContentAsFeedItems / loadOwnFollowsAsFeedItems).
	// The previous narrow gate at scopeKind=="handle" only worked for
	// the handle form, so scope=my for posts/comments silently fell
	// through to streamCacheForScope and returned empty. Per the
	// resolved-decisions table, the two forms are aliases; treat them
	// identically here.
	myDomain := extractDomainFromURL(s.GetBaseURL())
	isOwnSurface := (scopeKind == "handle" && scopeArg != "" && scopeArg == myDomain) ||
		scopeKind == "my"

	// with=comments is own-scope-only. Data source (blessed.json) only
	// exists for the local tenant; cross-scope semantics aren't defined
	// in this cut. (Previously also gated type=follows here; 06-profiles
	// removed type=follows from the grammar — profiles dispatch above
	// handles its own scope validation.)
	if withParam == "comments" && !isOwnSurface {
		writeStreamError(w, http.StatusBadRequest,
			fmt.Sprintf("with=%s requires scope=@<this-tenant> or scope=me", withParam))
		return
	}

	var items []feed.CachedFeedItem
	// sourcePaths is populated only for own-content (posts + comments).
	// Threaded into toStreamItemResponses so own-side items carry the
	// source_path field decorators need for /api/unpublish; nil for
	// other paths leaves the field absent (omitempty).
	var sourcePaths map[string]string
	if isOwnSurface {
		items, sourcePaths, err = s.loadOwnContentAsFeedItems(feedType)
		if err != nil {
			s.LogError("stream items: own-content load failed: %v", err)
			writeStreamError(w, http.StatusInternalServerError, "failed to load items")
			return
		}
		// type=activity (feedType="") on own scope: superset = own
		// posts + comments + follows. loadOwnContentAsFeedItems already
		// returned posts+comments; merge in follows so the activity view
		// shows a true pulse of own actions (rapid-fire #8 fix —
		// previously activity own-scope was missing follows entirely).
		// Reposts/blessings live in separate stores and aren't yet
		// surfaced here; future work can extend the merge.
		if feedType == "" {
			follows, fErr := s.loadOwnFollowsAsFeedItems(myDomain)
			if fErr == nil {
				items = append(items, follows...)
			} else {
				s.LogDebug("stream items: own-follows merge skipped: %v", fErr)
			}
		}
		// type=comments shows this tenant's outbound comments only —
		// one entry per target post (latest comment per post wins).
		// Inbound (others' comments on local posts) is intentionally
		// omitted; that data is reachable via type=posts&with=comments.
		if feedType == "comment" {
			items = dedupeCommentsByTargetPost(items)
		}
		// Populate comment_count for own posts, derived from blessed.json
		// (one count per post path). with=comments additionally filters
		// to posts whose count > 0.
		if feedType == "post" || feedType == "" {
			items = s.populateCommentCountsAndOptionallyFilter(items, withParam == "comments")
		}
	} else {
		cm, opts, err := s.streamCacheForScope(scopeKind, scopeArg)
		if err != nil {
			writeStreamError(w, http.StatusBadRequest, err.Error())
			return
		}
		opts.Type = feedType
		if qualifier == "new" {
			opts.Status = "unread"
		}
		items, err = cm.ListFiltered(opts)
		if err != nil {
			s.LogError("stream items list failed: %v", err)
			writeStreamError(w, http.StatusInternalServerError, "failed to load items")
			return
		}
		// scope=all-polis: union with the network cache so followed-
		// author content shows up alongside the global feed. The
		// global feed cache is populated by syncFeedScoped (24h
		// rolling, no actor filter) and the network feed cache is
		// populated by the unified sync handler (feedSyncHandler,
		// followed-author filter). Without this union, scope=all-polis
		// returned ONLY global content — even though "all polis"
		// semantically includes the user's network. Dedup by item ID.
		if scopeKind == "all-polis" {
			netCM := s.feedCacheForScope("network")
			netOpts := opts
			netOpts.AuthorDomains = nil // no per-author filter on the network cache
			netItems, netErr := netCM.ListFiltered(netOpts)
			if netErr == nil && len(netItems) > 0 {
				seen := make(map[string]struct{}, len(items))
				for _, it := range items {
					seen[it.ID] = struct{}{}
				}
				for _, it := range netItems {
					if _, dup := seen[it.ID]; !dup {
						items = append(items, it)
						seen[it.ID] = struct{}{}
					}
				}
			}
			// netErr is non-fatal — global cache content still
			// returns; log for ops visibility.
			if netErr != nil {
				s.LogDebug("stream items: network-cache union failed for all-polis: %v", netErr)
			}
		}
		// R20-4: drop self-authored items from cross-scope views
		// (my-network, all-polis, my-mutuals, @other-handle). The
		// network feed cache picks up own comments via
		// pub.polis.comment.blessing.granted events (Actor=granter,
		// sourceDomain=me) — by design, the announcement piece keeps
		// surfacing. But the comment ITEM with AuthorDomain=me leaked
		// into "new comments from my network by date" and looked like
		// the network filter wasn't working. Strip those at request
		// time so own comments only appear under scope=me.
		if scopeKind == "my-network" || scopeKind == "all-polis" || scopeKind == "my-mutuals" {
			filtered := items[:0]
			for _, it := range items {
				if it.AuthorDomain == myDomain {
					continue
				}
				filtered = append(filtered, it)
			}
			items = filtered
		}
		// R22 #5: with=pending-blessing actually filters now. Earlier
		// shape parsed the param but didn't apply it (TODO comment
		// upstream); the "comments to bless" preset surfaced every
		// network comment, blessed or not. Cross-reference cache items
		// against the live DS pending-requests list and keep only the
		// ones still awaiting our action.
		if withParam == "pending-blessing" && s.PrivateKey != nil && s.DiscoveryURL != "" {
			// Read-only DS call. Auth-conditional via NewReadDSClient
			// — the DS relationship-query endpoint returns matching
			// records anonymously; FetchPendingRequests post-filters
			// to records addressed to myDomain client-side.
			readClient := s.NewReadDSClient(r)
			pending, perr := blessing.FetchPendingRequests(readClient, myDomain)
			if perr != nil {
				s.LogError("stream items: pending-blessing fetch failed: %v", perr)
				items = items[:0]
			} else {
				pendingURLs := make(map[string]struct{}, len(pending))
				for _, p := range pending {
					pendingURLs[p.CommentURL] = struct{}{}
				}
				filtered := items[:0]
				for _, it := range items {
					if _, ok := pendingURLs[it.URL]; ok {
						filtered = append(filtered, it)
					}
				}
				items = filtered
			}
		}
	}

	// Apply time window filter (in addition to status/type already done by ListFiltered)
	//
	// 2026-05-18: skip the time filter for own-surface scopes (scope=me /
	// scope=my / scope=@<this-tenant>). The maxStreamWindowDays=30 cap is
	// there to bound per-request scan cost on cross-tenant network feeds —
	// own-content reads from a bounded local index (index.jsonl) and has
	// no scan-cost problem. With the cap on, posts older than 30 days
	// silently disappeared from "all posts from me by date" even though
	// the data was right there on disk; user reported their 3 March posts
	// invisible in late May. Owner SPA always sends time=30d as a default
	// (stream.js: the owner filter grammar has no user-visible time slot
	// yet), so this is the right place to special-case rather than asking
	// the client to send time=all. When a per-user time slot lands in the
	// owner UI we'll revisit — at that point an explicit non-default time
	// value should still post-filter.
	if timeDelta > 0 && !isOwnSurface {
		items = filterStreamItemsByTime(items, time.Now().Add(-timeDelta))
	}

	// before_url → cursor convenience (step-05/5.c). When the controller
	// has an anchor URL but no cursor, look up the item in the post-time-
	// filter result set and synthesize the cursor for it. Skipped if a
	// cursor was already given (cursor wins). Skipped silently if the URL
	// isn't found (caller has the wrong URL or the item is filtered out
	// by other params; returning the unfiltered first page is the right
	// fallback — no error).
	if cursor.BeforePublished == "" && cursor.AfterPublished == "" && beforeURL != "" {
		for i := range items {
			if items[i].URL == beforeURL {
				cursor.BeforePublished = items[i].Published
				cursor.BeforeID = items[i].ID
				break
			}
		}
	}
	// after_url → cursor convenience: scroll-up first-fetch from a per-
	// post page anchors on the focus URL and asks for newer items.
	// Identical lookup pattern to before_url; populates AfterPublished/
	// AfterID instead. Same skip-silent-if-not-found semantic — the
	// caller hitting an unknown URL gets the unfiltered first page, not
	// an error. The before_url branch above wins when both are passed
	// (matches the controller's first-fetch convention; in practice the
	// two are mutually exclusive).
	if cursor.BeforePublished == "" && cursor.AfterPublished == "" && afterURL != "" {
		for i := range items {
			if items[i].URL == afterURL {
				cursor.AfterPublished = items[i].Published
				cursor.AfterID = items[i].ID
				break
			}
		}
	}

	// (06-profiles: the type=follows by-name sort block that lived here
	// is gone. type=profiles has its own dispatch above —
	// handleStreamItemsProfiles handles by-name / by-activity sorting
	// before reaching this point. type=drafts likewise has its own
	// dispatch. Other types are by-date only at this layer.)

	// Apply order. Secondary sort key is ID (ascending) — provides a
	// stable tiebreaker within a shared timestamp so the cursor's
	// (BeforePublished, BeforeID) anchor produces consistent pagination
	// across calls. ListFiltered returns newest-first by default, but
	// re-sorting here guarantees the secondary-key invariant regardless
	// of scope-specific upstream behavior.
	if order == "oldest" {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Published != items[j].Published {
				return items[i].Published < items[j].Published
			}
			return items[i].ID < items[j].ID
		})
	} else {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Published != items[j].Published {
				return items[i].Published > items[j].Published
			}
			return items[i].ID < items[j].ID
		})
	}

	// Apply cursor pagination — published-timestamp-anchored
	items = applyStreamCursor(items, cursor, order)

	// Take limit + compute next cursor
	page, nextCursor := paginateStreamItems(items, limit, order)

	// Comment-count stamping. populateCrossTenantCommentCounts stamps the
	// DS TOTAL (blessed + unblessed) onto post items — own and cross-tenant
	// alike. That's the OWNER SPA lens (network aggregation). Canonical
	// pages get the BLESSED-ONLY count already stamped by
	// populateCommentCountsAndOptionallyFilter, matching what the tenant's
	// own SSR shows — the tenant's curated lens. Gated on the surface=spa
	// query param the owner SPA's stream.js sends (driven by body.is-owner;
	// see the Layer-2 owner-gate comment in stream.css). Canonical pages
	// omit the param and skip this stamping.
	if (feedType == "post" || feedType == "") && r.URL.Query().Get("surface") == "spa" {
		s.populateCrossTenantCommentCounts(r, page, myDomain)
	}

	// Build comment-thread enrichment (target post + comment body HTML)
	// for any type=comments view. Without this, cross-scope comment
	// pages (network / all-polis) sent empty target_post_* fields and
	// the renderer fell back to "(untitled)" / "(post body unavailable)"
	// for every entry. buildCommentThreadEnrichments fetches the parent
	// post via LoadOrFetchReplyContext (HTTP-cached, works for any URL)
	// and falls back to the cached excerpt when a local comment body
	// isn't available. Empty map for non-comment types short-circuits
	// the lookup in toStreamItemResponses.
	// Comment-thread enrichment (parent post title/body + comment body HTML)
	// runs for ANY view that can surface comment items, not just the dedicated
	// comments view — the "all"/activity stream renders comments as comment-
	// thread entries too, and without this they showed "(untitled) / (post body
	// unavailable)". buildCommentThreadEnrichments self-filters to comment items
	// (TargetURL set), so it's an empty-map no-op on pure-post pages.
	enrich := s.buildCommentThreadEnrichments(page)

	// Build per-post blessed-comment enrichment for type=posts on own-
	// handle scope. Mirror of buildCommentThreadEnrichments but inverted:
	// posts are local (no remote fetch), comments are blessed and already
	// mirrored locally — same data the SSR per-post focus entry inlines
	// today, brought to the stream-list view so clicking into focus mode
	// on any post reveals its comments without a follow-up fetch.
	var postComments map[string][]blessedCommentResponse
	var titleRedundant map[string]bool
	if isOwnSurface && (feedType == "post" || feedType == "") {
		postComments = s.buildPostCommentEnrichments(page)
		titleRedundant = s.buildPostTitleRedundancy(page)
	} else if feedType == "post" || feedType == "" {
		// Cross-scope (network feed cache) items don't have a local
		// source file, so buildPostTitleRedundancy can't read the
		// markdown body. Fall back to detecting against the cached
		// excerpt — same detector, lower-fidelity input. Same intent:
		// suppress duplicated title chrome on auto-titled posts.
		titleRedundant = make(map[string]bool, len(page))
		for _, it := range page {
			if it.Type != "post" || it.Title == "" || it.Excerpt == "" {
				continue
			}
			if render.TitleStartsFirstSentence(it.Title, it.Excerpt) {
				titleRedundant[it.URL] = true
			}
		}
	}

	// Post body enrichment: ship rendered body HTML alongside post items so
	// the SPA stream renders full bodies inline (matching v4/public's SSR'd
	// behavior) and the entries become focus-eligible via the existing
	// is-expanded marker. Covers own posts (read source markdown from disk
	// via sourcePaths) AND cross-tenant posts (parallel LoadOrFetchReplyContext
	// fetch with disk cache). Comment-thread entries get their target post
	// body via buildCommentThreadEnrichments above, and other types don't
	// carry a post body.
	var postBodies map[string]string
	if feedType == "post" || feedType == "" {
		postBodies = s.buildPostBodies(page, extractDomainFromURL(s.GetBaseURL()), sourcePaths)
	}

	// Per-entry author avatar config (cross-author only) for the stream's
	// floated 28px avatar. Covers both posts (post author) and comments
	// (commenter); the comments view shows the originating post by handle
	// text, so only the commenter needs an avatar here.
	authorAvatars := s.buildAuthorAvatars(page)

	resp := map[string]interface{}{
		"items": toStreamItemResponses(page, enrich, postComments, titleRedundant, sourcePaths, postBodies, authorAvatars),
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
		nextCursorSet = true
	}
	itemCount = len(page)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(resp)
}

// handleStreamItemsDrafts returns the local tenant's unpublished post drafts
// as stream items. Reads from .polis/bundles/pub.polis.core/posts/drafts/*.md
// — the same store the existing /api/drafts handler uses. Step-06/6.b adds
// this so the "all drafts from me" preset (paragraph-icon variant) and the
// inline editor (6.h) can list and resume drafts via the unified stream
// endpoint.
//
// Item shape: ID = filename minus .md suffix; Title = first H1 line;
// Excerpt = first 3 non-blank body lines (truncated 200 chars); Published =
// file mtime (so newest-first ordering works without explicit timestamps in
// the draft). URL = /_/posts/drafts/<id> so a click on the entry routes to
// the editor (App.navigateTo handler in app.js — wired more directly via
// owner-extras in 6.h).
//
// sort=by-name sorts alphabetically by title (case-insensitive), falling
// back to ID when titles match. sort=by-date (default) sorts newest-first
// by mtime. No cursor pagination today — drafts are typically <50 items;
// limit is honored. Cursor support can be added when a tenant's draft
// count makes pagination meaningful.
func (s *Server) handleStreamItemsDrafts(w http.ResponseWriter, sortBy string, limit int) {
	// Drafts ride the existing post renderer (renderPost in stream.js)
	// — they're unpublished posts, and the renderer just needs Title/
	// URL/Excerpt. Type stays "post" so the polymorphic dispatch finds
	// a renderer; an additional `draft: true` flag lets owner-extras /
	// CSS hang draft-specific chrome (e.g. an edit affordance) off the
	// entry without the v4 renderer needing draft awareness.
	// Earlier shape used Type:"draft" — the v4 renderers map had no
	// 'draft' entry and renderEntry silently dropped every draft
	// (rapid-fire #13).
	// PublishedHuman + AuthorDomain are the fields renderPost reads
	// for the entry meta-line (date + byline). Without them the
	// stream renderer left drafts with no visible timestamp and no
	// byline — the cards looked stripped compared to published
	// posts (R20-10).
	type draftItem struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Title   string `json:"title,omitempty"`
		Excerpt string `json:"excerpt,omitempty"`
		// R23 #9: ship the rendered HTML so the SPA stream can show
		// the formatted body in place. Drafts have no canonical URL
		// to lazy-fetch, so without this the renderer fell back to a
		// plain-text excerpt.
		BodyHTML       string `json:"body_html,omitempty"`
		URL            string `json:"url"`
		Published      string `json:"published"`
		PublishedHuman string `json:"published_human,omitempty"`
		AuthorDomain   string `json:"author_domain,omitempty"`
		Draft          bool   `json:"draft"`
	}

	myDomain := extractDomainFromURL(s.GetBaseURL())

	draftsDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	entries, err := os.ReadDir(draftsDir)
	if err != nil {
		// Missing directory == zero drafts. Same fall-through as /api/drafts.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []draftItem{},
		})
		return
	}

	var items []draftItem
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(ent.Name(), ".md")
		mtime := info.ModTime()
		item := draftItem{
			ID:             id,
			Type:           "post",
			Draft:          true,
			URL:            "/_/posts/drafts/" + id,
			Published:      mtime.Format(time.RFC3339),
			PublishedHuman: template.FormatHumanDateTime(mtime.UTC().Format(time.RFC3339)),
			AuthorDomain:   myDomain,
		}
		// Title + excerpt extraction mirrors handleDrafts (handlers.go:500-533).
		if content, rErr := os.ReadFile(filepath.Join(draftsDir, ent.Name())); rErr == nil {
			lines := strings.Split(string(content), "\n")
			bodyStart := 0
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if item.Title == "" && strings.HasPrefix(trimmed, "# ") {
					item.Title = strings.TrimPrefix(trimmed, "# ")
					bodyStart = i + 1
				} else if item.Title == "" && trimmed != "" {
					bodyStart = i
					break
				}
			}
			var excerptLines []string
			for i := bodyStart; i < len(lines) && len(excerptLines) < 3; i++ {
				trimmed := strings.TrimSpace(lines[i])
				if trimmed != "" {
					excerptLines = append(excerptLines, trimmed)
				}
			}
			if len(excerptLines) > 0 {
				excerpt := strings.Join(excerptLines, " ")
				if len(excerpt) > 200 {
					excerpt = excerpt[:200] + "..."
				}
				item.Excerpt = excerpt
			}
			// R23 #9: render the body markdown to HTML so the stream
			// can display the formatted draft in place. Strip the
			// leading H1 (if any) — title is already handled in the
			// dedicated title slot.
			bodyMD := strings.Join(lines[bodyStart:], "\n")
			if rendered, mdErr := render.MarkdownToHTML(bodyMD); mdErr == nil {
				item.BodyHTML = rendered
			}
		}
		// Untitled draft: substitute a placeholder so the renderer's
		// title chrome stays consistent across all entries (R20-10).
		// Without this, drafts that are body-only or empty rendered as
		// just an excerpt or as nothing — visually breaking the column.
		if item.Title == "" {
			item.Title = "(untitled draft)"
		}
		items = append(items, item)
	}

	if sortBy == "by-name" {
		sort.SliceStable(items, func(i, j int) bool {
			ti := strings.ToLower(items[i].Title)
			tj := strings.ToLower(items[j].Title)
			if ti != tj {
				return ti < tj
			}
			return items[i].ID < items[j].ID
		})
	} else {
		// by-date default: newest first.
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].Published > items[j].Published
		})
	}

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
	})
}

// handleStreamItemsProfiles serves the stream-items dispatch for
// type=profiles. Delegates to the same builders /api/profiles uses
// so the v4 stream and the dedicated endpoint share one data path.
// Items carry Type:"profile" with enriched fields; the SPA's
// renderProfile reads these by name.
//
// Phase 3 supports both scopes:
//   - my-network: union of outbound + inbound follows (local), enriched
//     from the network feed cache.
//   - all-polis:  DS site directory via buildAllPolisProfiles.
//     Accepts search + cursor for debounced search +
//     pagination. ErrListSitesNotDeployed degrades to
//     an empty stream (the SPA stays on the surface).
//
// limit is honored. Cursor and search are passed through unchanged
// (empty values are fine — the builder treats them as no-op).
func (s *Server) handleStreamItemsProfiles(w http.ResponseWriter, r *http.Request, scope, sortBy, search, cursor string, limit int) {
	if scope == "all-polis" {
		items, nextCursor, err := s.buildAllPolisProfiles(r, sortBy, search, cursor, limit)
		if err != nil {
			if errors.Is(err, discovery.ErrListSitesNotDeployed) {
				// Old DS — keep the stream surface renderable so
				// the user can still see "all of polis" with a hint
				// (the SPA's empty-state CTA covers messaging).
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"items": []profileItem{},
				})
				return
			}
			s.LogError("stream items: all-polis profiles failed: %v", err)
			writeStreamError(w, http.StatusBadGateway, "discovery-service unavailable")
			return
		}
		if items == nil {
			items = []profileItem{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		resp := map[string]interface{}{
			"items": items,
		}
		if nextCursor != "" {
			resp["next_cursor"] = nextCursor
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// scope=my-network
	items, _, err := s.buildProfilesList("my-network", sortBy)
	if err != nil {
		s.LogError("stream items: profiles load failed: %v", err)
		writeStreamError(w, http.StatusInternalServerError, "failed to load profiles")
		return
	}
	// Search filter — case-insensitive substring against display name
	// and handle. The follow list is small enough that filtering in
	// memory after building is fine (and avoids changing the
	// buildProfilesList signature for the shared /api/profiles caller).
	if q := strings.TrimSpace(search); q != "" {
		needle := strings.ToLower(q)
		filtered := items[:0]
		for _, it := range items {
			if strings.Contains(strings.ToLower(it.DisplayName), needle) ||
				strings.Contains(strings.ToLower(it.AuthorDomain), needle) {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []profileItem{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
	})
}

// handleStreamItemsDM returns DM conversation summaries shaped as
// stream items, ordered by most-recent activity. Step-06/6.g wires
// the long-stub placeholder to dm.Store.LoadIndex().
//
// Privacy posture (Q4 alignment from step-4 pre-execution):
//   - Encrypted message payloads NEVER traverse server→browser via this
//     endpoint. The DM Store decrypts payloads at-rest server-side
//     using the owner's storage key; full message bodies arrive via
//     /api/dm/conversations/<id> which the SPA's existing thread view
//     already uses.
//   - LastPreview (1-line truncated plaintext, intentionally a metadata
//     signal — same shape /api/dm/conversations exposes) IS surfaced
//     here as body_text so the SPA's stream listing can show "Alice:
//     hey, did you see..." style cards without a second roundtrip.
//     The truncation lives on the owner's disk via the at-rest
//     encrypted index (DecryptIndexPreviews unwraps server-side).
//
// Auth posture (TODO — flagged for 6.k auth-aware nav work):
//
//	This endpoint inherits the parent /api/v1/stream/items posture
//	(no explicit auth check). On localhost the binary is owner-trust by
//	construction. On hosted, session auth at the routing layer ensures
//	only the tenant's authenticated session reaches their polis-server.
//	DM previews are the most sensitive surface yet exposed via this
//	endpoint, so the implicit-trust assumption is worth tightening
//	alongside 6.k's broader auth-aware rewrite — likely a per-handler
//	session check before any DM data is read.
//
// Items shape conforms to renderDM in shapes/v4/stream.js:
//   - meta.type = "dm", meta.conversation_id (renderer dataset hook)
//   - meta.sender_domain, meta.sender_name (byline)
//   - meta.body_text (decrypted preview — intentionally not body_html)
//   - meta.unread (drives has-unread state class)
//   - meta.url ("/_/messages/<id>" so click navigates to the thread)
//   - meta.published / meta.published_human (timestamp)
//
// Filtering / paging:
//   - qualifier=new → only conversations with UnreadCount > 0
//   - order=newest (default) sorts by LastMessageAt DESC
//   - limit caps the response; cursor pagination is not yet wired
//     (DM volumes per tenant are typically <100; the listing pages
//     by limit alone for now). Cursor support can be added when a
//     tenant's DM count makes pagination meaningful.
func (s *Server) handleStreamItemsDM(w http.ResponseWriter, r *http.Request, _ streamCursor, limit int, order string, qualifier string) {
	mb, self, _, err := s.dmMailbox()
	if err != nil {
		// No DM mailbox configured (e.g., tenant without keys) → empty list,
		// not an error — same shape as a tenant with zero DMs.
		s.LogWarn("stream items dm: dmMailbox unavailable: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	entries, err := mb.RebuildInbox()
	if err != nil {
		s.LogError("stream items dm: rebuild inbox: %v", err)
		writeStreamError(w, http.StatusInternalServerError, "failed to load DM index")
		return
	}

	type dmItem struct {
		ID             string `json:"id"`
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		SenderDomain   string `json:"sender_domain"`
		SenderName     string `json:"sender_name"`
		Title          string `json:"title,omitempty"`
		BodyText       string `json:"body_text,omitempty"`
		URL            string `json:"url"`
		Published      string `json:"published"`
		PublishedHuman string `json:"published_human,omitempty"`
		Unread         bool   `json:"unread,omitempty"`
		UnreadCount    int    `json:"unread_count,omitempty"`
	}

	out := make([]dmItem, 0, len(entries))
	for _, e := range entries {
		// qualifier=new → only unread.
		if qualifier == "new" && e.Unread == 0 {
			continue
		}
		convID := dm.ComputeConversationID(self, e.Peer)
		out = append(out, dmItem{
			ID:             convID,
			Type:           "dm",
			ConversationID: convID,
			SenderDomain:   e.Peer,
			// SenderName falls back to the peer handle — at this layer we
			// don't have the peer's display name. BodyText is empty: under
			// end-to-end encryption there is no server-readable preview.
			SenderName:     e.Peer,
			Title:          e.Peer,
			BodyText:       "",
			URL:            "/_/messages/" + convID,
			Published:      e.LastMessageAt,
			PublishedHuman: template.FormatHumanDateTime(e.LastMessageAt),
			Unread:         e.Unread > 0,
			UnreadCount:    e.Unread,
		})
	}

	// Sort by LastMessageAt. order=oldest reverses; default is newest first.
	sort.SliceStable(out, func(i, j int) bool {
		if order == "oldest" {
			return out[i].Published < out[j].Published
		}
		return out[i].Published > out[j].Published
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"items": out,
	})
}

// handleStreamItemsDMThread returns the full message history for ONE
// conversation as stream items, ordered most-recent on top. Triggered by
// type=dms+scope=@<peerHandle>. Messages are decrypted server-side using
// the owner's storage key — same posture as DecryptIndexPreviews on the
// inbox path (keys never leave the process; plaintext flows to the
// authenticated session).
//
// Cursor pagination is wired here (unlike the inbox handler, where it's
// not yet needed). The mockup loads N=50 most-recent messages on first
// fetch; the v4 IntersectionObserver re-fires with the last entry's
// cursor to pull older messages as the user scrolls toward the bottom
// (oldest end) of the thread.
//
// Items shape conforms to the thread-mode branch of renderDM in
// shapes/v4/stream.js:
//   - meta.type = "dm-message", meta.conversation_id (renderer hook)
//   - meta.is_mine (drives the indent+tint styling for own messages)
//   - meta.sender_domain, meta.sender_name (byline; sender_name for own
//     messages reads as "you" in the renderer)
//   - meta.body_text (decrypted plaintext)
//   - meta.published / meta.published_human (timestamp)
//   - meta.id (message ID, used as cursor tiebreaker)
func (s *Server) handleStreamItemsDMThread(w http.ResponseWriter, r *http.Request, peerHandle string, cursor streamCursor, limit int, order string) {
	if peerHandle == "" {
		writeStreamError(w, http.StatusBadRequest, "thread scope requires a peer handle")
		return
	}
	mb, _, deks, err := s.dmMailbox()
	if err != nil {
		// No DM mailbox (tenant without keys) → empty thread, same shape
		// as a conversation that doesn't exist yet.
		s.LogWarn("stream items dm-thread: dmMailbox unavailable: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	myDomain := extractDomainFromURL(s.GetBaseURL())
	if myDomain == "" {
		writeStreamError(w, http.StatusInternalServerError, "site base URL not configured")
		return
	}
	convID := dm.ComputeConversationID(myDomain, peerHandle)

	stored, err := mb.ReadConversation(peerHandle, deks)
	if err != nil {
		// Conversation not present yet → empty thread. Not an error; matches
		// the "conversation auto-creates on first send" behavior.
		s.LogDebug("stream items dm-thread: ReadConversation(%s): %v", peerHandle, err)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	type messageItem struct {
		ID             string `json:"id"`
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		SenderDomain   string `json:"sender_domain"`
		SenderName     string `json:"sender_name"`
		BodyText       string `json:"body_text,omitempty"`
		IsMine         bool   `json:"is_mine,omitempty"`
		Locked         bool   `json:"locked,omitempty"`
		// Raw wire box for client-side unlock (phase 4.5). Present only when
		// Locked: the SPA derives the epoch DEK from the user's password
		// (PolisDMCrypto), opens the box in-browser, and replaces the locked
		// placeholder with plaintext. Not secret — it's the encrypted box.
		KeyEpoch       int    `json:"key_epoch,omitempty"`
		Ciphertext     string `json:"ciphertext,omitempty"`
		Nonce          string `json:"nonce,omitempty"`
		BoxPub         string `json:"box_pub,omitempty"`
		Published      string `json:"published"`
		PublishedHuman string `json:"published_human,omitempty"`
	}

	out := make([]messageItem, 0, len(stored))
	for _, msg := range stored {
		// "me" marks the owner's own outgoing copy; incoming carries the peer domain.
		isMine := msg.Dir == dm.DirOut
		senderDomain := msg.From
		senderName := msg.From
		if isMine {
			senderDomain = myDomain
			senderName = "you"
		}
		// For a locked (password-epoch) message the server can't read the body;
		// it ships the raw box + key_epoch so the SPA can open it after the user
		// unlocks that epoch. The placeholder is the fallback shown when the
		// browser crypto stack isn't loaded.
		body := msg.Plaintext
		item := messageItem{
			ID:             msg.ID,
			Type:           "dm-message",
			ConversationID: convID,
			SenderDomain:   senderDomain,
			SenderName:     senderName,
			IsMine:         isMine,
			Locked:         msg.Locked,
			Published:      msg.At,
			PublishedHuman: template.FormatHumanDateTime(msg.At),
		}
		if msg.Locked {
			body = "[locked — unlock with your password to read]"
			item.KeyEpoch = msg.KeyEpoch
			item.Ciphertext = msg.Ciphertext
			item.Nonce = msg.Nonce
			item.BoxPub = msg.BoxPub
		}
		item.BodyText = body
		out = append(out, item)
	}

	// Most-recent-on-top per spec. order=oldest reverses for completeness
	// (chronological top-to-bottom) but the default thread view is newest-
	// first to match the mockup.
	sort.SliceStable(out, func(i, j int) bool {
		if order == "oldest" {
			if out[i].Published != out[j].Published {
				return out[i].Published < out[j].Published
			}
			return out[i].ID < out[j].ID
		}
		if out[i].Published != out[j].Published {
			return out[i].Published > out[j].Published
		}
		return out[i].ID > out[j].ID
	})

	// Cursor pagination. cursor.BeforePublished filters to items strictly
	// older than the cursor; BeforeID is the tiebreaker for ties at exactly
	// that timestamp. Matches the convention used by the regular feed
	// path (line 253-258).
	if cursor.BeforePublished != "" {
		filtered := out[:0]
		for _, it := range out {
			if it.Published < cursor.BeforePublished {
				filtered = append(filtered, it)
				continue
			}
			if it.Published == cursor.BeforePublished && it.ID < cursor.BeforeID {
				filtered = append(filtered, it)
			}
		}
		out = filtered
	}

	var nextCursor string
	if limit > 0 && len(out) > limit {
		last := out[limit-1]
		nextCursor = encodeStreamCursor(streamCursor{
			BeforePublished: last.Published,
			BeforeID:        last.ID,
		})
		out = out[:limit]
	}

	resp := map[string]interface{}{
		"items": out,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// parseStreamScope validates scope and splits it into a kind + optional
// argument. Returns (kind, arg, error). Kinds: "my", "my-network",
// "my-mutuals", "all-polis", "handle" (with arg = the @-stripped domain).
//
// step-06/6.b: accepts `me` as alias for `my` (grammar token alignment —
// internal feed-cache scope is already named `me`, this just brings the
// URL form in line). Adds `my-mutuals` for the envelope-preset scope
// (intersection of following.json and follower projection — computed
// in feedFilterForMutuals below).
func parseStreamScope(scope string) (string, string, error) {
	switch scope {
	case "my", "me":
		return "my", "", nil
	case "my-network", "all-polis", "my-mutuals":
		return scope, "", nil
	}
	if strings.HasPrefix(scope, "@") {
		handle := strings.TrimPrefix(scope, "@")
		if handle == "" {
			return "", "", fmt.Errorf("scope @handle requires a non-empty domain")
		}
		// Possessive-network form: `@<handle>:network`. Used by the
		// public visitor profiles surface — sentence reads "all
		// profiles from <handle>.polis.pub's network". Returned as
		// kind="handle-network" so type-specific handlers can
		// differentiate it from the bare-handle scope.
		if strings.HasSuffix(handle, ":network") {
			h := strings.TrimSuffix(handle, ":network")
			if h == "" {
				return "", "", fmt.Errorf("scope @handle:network requires a non-empty handle")
			}
			return "handle-network", h, nil
		}
		return "handle", handle, nil
	}
	return "", "", fmt.Errorf("scope must be one of: me, my, my-network, my-mutuals, all-polis, @<domain>, @<domain>:network (got %q)", scope)
}

// streamCacheForScope returns the feed cache + filter options matching
// the requested scope. Translates the v4-stream scope vocabulary
// (my / my-network / all-polis / @handle) into the existing feed-cache
// scope keys (me / network / global / per-author filter). Delegates to
// the existing feedCacheForScope/feedFilterForScope helpers where
// possible so any future refinement (additional filter dimensions on
// "me", scope-specific cache pruning, etc.) flows through automatically.
func (s *Server) streamCacheForScope(kind, arg string) (*feed.CacheManager, feed.FilterOptions, error) {
	switch kind {
	case "all-polis":
		return s.feedCacheForScope("global"), s.feedFilterForScope("global"), nil
	case "my-network":
		return s.feedCacheForScope("network"), s.feedFilterForScope("network"), nil
	case "my-mutuals":
		// step-06/6.b: filter the network cache to authors that are mutuals
		// (intersection of following.json and the follower projection at
		// .polis/ds/<domain>/pub.polis.core/state/pub.polis.follow.json).
		// Both data sources are local; no DS roundtrip at request time.
		// Empty intersection produces an empty FilterOptions.AuthorDomains
		// map which the cache treats as "no filter" — guard against that
		// here by returning an explicit empty-result sentinel.
		mutuals := s.computeMutualsSet()
		if len(mutuals) == 0 {
			// Honest empty: return a filter that matches nothing.
			return s.feedCacheForScope("network"), feed.FilterOptions{
				AuthorDomains: map[string]bool{"__no_mutuals__": true},
			}, nil
		}
		return s.feedCacheForScope("network"), feed.FilterOptions{
			AuthorDomains: mutuals,
		}, nil
	case "my":
		// Delegate to feedFilterForScope("me") — same author-domain filter
		// the existing /api/feed page uses for its "me" scope. Validates the
		// scope is configurable: feedFilterForScope returns an empty filter
		// when the base URL isn't set, which would silently match everything.
		opts := s.feedFilterForScope("me")
		if len(opts.AuthorDomains) == 0 {
			return nil, feed.FilterOptions{}, fmt.Errorf("scope=my requires a configured site base URL")
		}
		return s.feedCacheForScope("me"), opts, nil
	case "handle":
		// No existing helper for arbitrary handles; build inline. If a
		// helper lands in feedFilterForScope (e.g., feedFilterForHandle),
		// switch to it.
		return s.feedCacheForScope("network"), feed.FilterOptions{
			AuthorDomains: map[string]bool{arg: true},
		}, nil
	}
	return nil, feed.FilterOptions{}, fmt.Errorf("unknown scope kind: %s", kind)
}

// computeMutualsSet returns the set of handles that are both followed by
// this tenant AND follow this tenant back (mutual follow relationship).
// Step-06/6.b: backs scope=my-mutuals and the envelope-icon DM preset.
//
// Data sources (both local — no DS roundtrip at request time):
//   - Outbound: content/pub.polis.core/follow/following.json (managed by
//     polis follow / polis unfollow; loaded via following.Load).
//   - Inbound: .polis/ds/<discovery-domain>/pub.polis.core/state/
//     pub.polis.follow.json — the FollowerState projection synced from
//     pub.polis.follow.{announced,removed} events by FollowHandler.
//
// Returns an empty map when either side is missing, so the caller can
// short-circuit to an empty result. Domain comparison is case-insensitive
// (FollowerState lowercases on write; following.json hosts URL-form, so
// extractDomainFromURL + lowercase normalize for the intersection).
//
// step-06 final-review item #5: memoized on `s` with mtime + TTL
// invalidation. Per-request recompute (~O(N+M)) was wasteful for
// tenants with thousands of follows; cache lives until either source
// file's mtime changes OR mutualsCacheTTL expires (whichever first).
// Concurrent callers serialize on s.mutualsCacheMu; the lock is held
// for the duration of the recompute so the second caller gets the
// freshly-cached value rather than recomputing in parallel.
func (s *Server) computeMutualsSet() map[string]bool {
	outboundPath := following.DefaultPath(s.DataDir)
	store := stream.NewStore(s.DataDir, s.GetDiscoveryDomain(), "pub.polis.core")
	inboundPath := store.StatePath("pub.polis.follow")

	// Stat both source files. If either is missing, the call falls
	// through to the recompute path (which honestly returns nil).
	// nil time.Time on stat-error → cache check fails closed.
	var outboundMt, inboundMt time.Time
	if info, err := os.Stat(outboundPath); err == nil {
		outboundMt = info.ModTime()
	}
	if info, err := os.Stat(inboundPath); err == nil {
		inboundMt = info.ModTime()
	}

	s.mutualsCacheMu.Lock()
	defer s.mutualsCacheMu.Unlock()

	// Cache hit: TTL still valid AND both mtimes match.
	if s.mutualsCacheValue != nil &&
		time.Since(s.mutualsCacheAt) < mutualsCacheTTL &&
		s.mutualsOutboundMt.Equal(outboundMt) &&
		s.mutualsInboundMt.Equal(inboundMt) {
		return s.mutualsCacheValue
	}

	// Recompute under lock so concurrent callers don't duplicate work.
	mutuals := s.computeMutualsSetUncached()
	s.mutualsCacheValue = mutuals
	s.mutualsCacheAt = time.Now()
	s.mutualsOutboundMt = outboundMt
	s.mutualsInboundMt = inboundMt
	return mutuals
}

// computeMutualsSetUncached is the actual O(N+M) computation. Split
// out so computeMutualsSet can wrap it with memoization. Returns nil
// when either source is missing/empty (honest empty result for the
// caller's short-circuit).
func (s *Server) computeMutualsSetUncached() map[string]bool {
	// Outbound (who I follow).
	outbound := make(map[string]bool)
	if ff, err := following.Load(following.DefaultPath(s.DataDir)); err == nil && ff != nil {
		for _, e := range ff.All() {
			d := strings.ToLower(extractDomainFromURL(e.URL))
			if d != "" {
				outbound[d] = true
			}
		}
	}
	if len(outbound) == 0 {
		return nil
	}

	// Inbound (who follows me) — load via the stream package's Store.
	store := stream.NewStore(s.DataDir, s.GetDiscoveryDomain(), "pub.polis.core")
	var fs stream.FollowerState
	if err := store.LoadState("pub.polis.follow", &fs); err != nil {
		// Missing/empty projection means we have no recorded followers yet.
		// Returning nil makes scope=my-mutuals an honest empty result.
		return nil
	}
	if len(fs.Followers) == 0 {
		return nil
	}

	mutuals := make(map[string]bool)
	for _, f := range fs.Followers {
		d := strings.ToLower(f)
		if outbound[d] {
			mutuals[d] = true
		}
	}
	return mutuals
}

// loadOwnContentAsFeedItems reads the local public index (the same source
// SSR uses + /api/posts reads from) and synthesizes CachedFeedItem records
// for the tenant's own posts/comments. Used when the stream's filter
// asks for scope=@<own-domain> — the network feed cache filters self
// events out by design (FeedHandler's self-skip), so AuthorDomains={self}
// on the network cache returns 0 even when there are local posts.
//
// feedType: "post" / "comment" / "" (all). Empty index entries (or entries
// with unparseable JSON lines) are skipped silently per LoadPublicIndex.
// Mount-path conversion mirrors render.PageRenderer.sourceToMountPath
// (post/ -> posts/, comment/ -> comments/, .md -> .html). The resulting
// URL is path-only ("/posts/...") matching feed-cache items, so the
// before_url cursor lookup compares apples to apples.
func (s *Server) loadOwnContentAsFeedItems(feedType string) ([]feed.CachedFeedItem, map[string]string, error) {
	entries, err := metadata.LoadPublicIndex(s.DataDir)
	if err != nil {
		return nil, nil, err
	}
	myDomain := extractDomainFromURL(s.GetBaseURL())
	myAuthorURL := s.GetBaseURL()
	out := make([]feed.CachedFeedItem, 0, len(entries))
	// URL → source path map. Only own-handle content carries a
	// stable source path; cross-tenant items don't (the unpublish
	// action doesn't apply to them anyway). Keyed by the rendered
	// URL form `/posts/.../foo.html` so toStreamItemResponses can
	// look it up via the same key it uses for thread enrichments.
	sourcePaths := make(map[string]string, len(entries))
	for _, e := range entries {
		isPost := strings.HasPrefix(e.Path, "content/pub.polis.core/post/") || e.Type == "post"
		isComment := strings.HasPrefix(e.Path, "content/pub.polis.core/comment/") || e.Type == "comment"
		var itemType string
		var url string
		switch {
		case isPost:
			itemType = "post"
			url = "/" + strings.TrimSuffix(strings.Replace(e.Path, "content/pub.polis.core/post/", "posts/", 1), ".md") + ".html"
		case isComment:
			itemType = "comment"
			url = "/" + strings.TrimSuffix(strings.Replace(e.Path, "content/pub.polis.core/comment/", "comments/", 1), ".md") + ".html"
		default:
			continue
		}
		if feedType != "" && feedType != itemType {
			continue
		}
		// Excerpt comes from the source markdown (same path /api/posts uses).
		// Failure is non-fatal — empty excerpt is acceptable, the entry still
		// renders with title + date.
		var excerpt string
		if raw, rErr := os.ReadFile(filepath.Join(s.DataDir, e.Path)); rErr == nil {
			excerpt = makeExcerpt(stripFrontmatter(string(raw)), feed.ExcerptCharCap)
		}
		item := feed.CachedFeedItem{
			ID:           e.CurrentVersion,
			Type:         itemType,
			Title:        e.Title,
			URL:          url,
			Published:    e.Published,
			Hash:         e.CurrentVersion,
			AuthorURL:    myAuthorURL,
			AuthorDomain: myDomain,
			Excerpt:      excerpt,
		}
		// Comments carry the target-post URL from the public index's
		// in_reply_to entry. Used by the stream's `type=comments`
		// thread view to dedupe by target post and to anchor the
		// inline post-body enrichment.
		if isComment && e.InReplyTo != nil {
			item.TargetURL = e.InReplyTo.URL
			item.TargetDomain = extractDomainFromURL(e.InReplyTo.URL)
		}
		out = append(out, item)
		sourcePaths[url] = e.Path
	}
	return out, sourcePaths, nil
}

// loadOwnFollowsAsFeedItems builds feed items for the stream's
// type=follows view: outbound follows (handles this tenant follows) +
// inbound follows (handles that follow this tenant). Both sources are
// local — outbound from following.json, inbound from the synced
// pub.polis.follow.json projection cache. No DS roundtrip at request
// time; the projection is kept fresh by the existing cursor sync loop.
//
// First-cut metadata: outbound entries carry added_at as Published;
// inbound entries leave Published empty (the projection's FollowerState
// only stores the deduped handle list, not per-follower timestamps —
// extending the projection to record AnnouncedAt per follower is a
// follow-up). For now, missing-Published items sort last.
func (s *Server) loadOwnFollowsAsFeedItems(myDomain string) ([]feed.CachedFeedItem, error) {
	myURL := s.GetBaseURL()
	out := []feed.CachedFeedItem{}

	// Outbound: who I follow.
	followFile, err := following.Load(following.DefaultPath(s.DataDir))
	if err == nil && followFile != nil {
		for _, e := range followFile.All() {
			targetDomain := extractDomainFromURL(e.URL)
			out = append(out, feed.CachedFeedItem{
				ID:           "follow:out:" + targetDomain,
				Type:         "follow",
				Title:        e.URL,
				URL:          e.URL,
				Published:    e.AddedAt,
				AuthorURL:    myURL,
				AuthorDomain: myDomain,
				TargetURL:    e.URL,
				TargetDomain: targetDomain,
			})
		}
	}

	// Inbound: who follows me. Read from the projection cache rather
	// than calling DS at request time. Missing state file → empty list,
	// no error (fresh tenants have no projection yet).
	discoveryDomain := s.GetDiscoveryDomain()
	if discoveryDomain != "" {
		store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
		var fs stream.FollowerState
		if err := store.LoadState("pub.polis.follow", &fs); err == nil {
			for _, follower := range fs.Followers {
				followerURL := "https://" + follower
				out = append(out, feed.CachedFeedItem{
					ID:           "follow:in:" + follower,
					Type:         "follow",
					Title:        followerURL,
					URL:          followerURL,
					AuthorURL:    followerURL,
					AuthorDomain: follower,
					TargetURL:    myURL,
					TargetDomain: myDomain,
				})
			}
		}
	}

	return out, nil
}

// dedupeCommentsByTargetPost reduces a list of own outbound comments to
// one entry per target post — the latest comment per post wins. Items
// without a TargetURL (legacy index entries missing in_reply_to) pass
// through unchanged so they aren't dropped on a tenant-by-tenant basis.
//
// Used by the stream's `type=comments` thread view so each entry is
// "post + the latest comment <handle> made on it" rather than a stream
// of repeated comments by the same author on the same post.
func dedupeCommentsByTargetPost(items []feed.CachedFeedItem) []feed.CachedFeedItem {
	latestByTarget := make(map[string]int) // target_url → index of latest item
	keepUnkeyed := []int{}                 // items without target_url, kept as-is
	for i, it := range items {
		if it.Type != "comment" || it.TargetURL == "" {
			keepUnkeyed = append(keepUnkeyed, i)
			continue
		}
		if existing, ok := latestByTarget[it.TargetURL]; ok {
			if it.Published > items[existing].Published {
				latestByTarget[it.TargetURL] = i
			}
		} else {
			latestByTarget[it.TargetURL] = i
		}
	}
	out := make([]feed.CachedFeedItem, 0, len(latestByTarget)+len(keepUnkeyed))
	for _, idx := range keepUnkeyed {
		out = append(out, items[idx])
	}
	for _, idx := range latestByTarget {
		out = append(out, items[idx])
	}
	return out
}

// buildCommentThreadEnrichments returns a per-comment enrichment map for
// the stream's `type=comments` thread view. For each comment item on
// the page, this fetches the target post's title/published/body from the
// reply-context-cache (or fresh fetch on miss) and renders both the
// post body markdown and the local comment body markdown to HTML.
//
// Concurrency: parallelizes target-post fetches with a semaphore of 5
// concurrent requests; per-fetch timeout is whatever DefaultHTTPClient
// is configured with (3s default). Failed fetches are non-fatal — the
// enrichment map simply omits the URL, so the client renders a
// degraded entry without inline post body.
func (s *Server) buildCommentThreadEnrichments(items []feed.CachedFeedItem) map[string]commentThreadEnrichment {
	enrich := make(map[string]commentThreadEnrichment, len(items))
	siteDomain := extractDomainFromURL(s.GetBaseURL())

	// Collect unique target URLs that need fetching.
	type itemInfo struct {
		commentURL     string
		commentMD      string // local comment body markdown (own comments only)
		commentExcerpt string // cached excerpt fallback (remote comments)
		targetURL      string
	}
	var infos []itemInfo
	uniqueTargets := make(map[string]bool)
	for _, it := range items {
		if it.Type != "comment" || it.TargetURL == "" {
			continue
		}
		// Local comment markdown — read from disk via the URL → path
		// reverse mapping that mirrors loadOwnContentAsFeedItems.
		// Returns "" for remote comments (no local file); the cached
		// excerpt covers that case in the render branch below.
		commentBodyMD := readLocalCommentBody(s.DataDir, it.URL)
		infos = append(infos, itemInfo{
			commentURL:     it.URL,
			commentMD:      commentBodyMD,
			commentExcerpt: it.Excerpt,
			targetURL:      it.TargetURL,
		})
		uniqueTargets[it.TargetURL] = true
	}
	if len(infos) == 0 {
		return enrich
	}

	// Parallel fetch of target posts. 24h TTL on body data; cache-only
	// hit short-circuits the HTTP call.
	type fetchResult struct {
		url   string
		entry render.ReplyContextEntry
		ok    bool
	}
	const ttl = 24 * time.Hour
	results := make(chan fetchResult, len(uniqueTargets))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for targetURL := range uniqueTargets {
		wg.Add(1)
		sem <- struct{}{}
		go func(u string) {
			defer wg.Done()
			defer func() { <-sem }()
			entry, ok := render.LoadOrFetchReplyContext(s.DataDir, u, ttl)
			results <- fetchResult{url: u, entry: entry, ok: ok}
		}(targetURL)
	}
	wg.Wait()
	close(results)

	targetEntries := make(map[string]render.ReplyContextEntry, len(uniqueTargets))
	for r := range results {
		// Even partial entries (cache hit without BodyMD) are useful —
		// title alone gives the renderer something to anchor on.
		targetEntries[r.url] = r.entry
	}

	// Build the enrichment map. Per item, run the same title-dedup the
	// SSR'd entry--post path runs (render.StripLeadingTitleHeading +
	// render.TitleStartsFirstSentence) on the raw markdown — these
	// helpers compare against markdown text so emphasis markers in the
	// title (**bold**) and body line up correctly. Then render to HTML
	// through the shared goldmark + UGC-sanitizer pipeline.
	for _, info := range infos {
		entry := targetEntries[info.targetURL]
		var postHTML string
		var titleRedundant bool
		if entry.BodyMD != "" {
			cleaned := render.StripLeadingTitleHeading(entry.BodyMD, entry.Title)
			titleRedundant = render.TitleStartsFirstSentence(entry.Title, cleaned)
			if rendered, err := render.MarkdownToHTML(cleaned); err == nil {
				postHTML = rendered
			}
		}
		var commentHTML string
		if info.commentMD != "" {
			if rendered, err := render.MarkdownToHTML(info.commentMD); err == nil {
				commentHTML = rendered
			}
		} else if info.commentExcerpt != "" {
			// Remote comment: no local markdown body. Render the cached
			// excerpt as markdown — same pipeline as the local body
			// path, so any markdown formatting in the excerpt comes
			// through cleanly. Falls through to plain text otherwise.
			if rendered, err := render.MarkdownToHTML(info.commentExcerpt); err == nil {
				commentHTML = rendered
			}
		}
		// Parent-post author avatar (drives the activity-view post-region
		// avatar). Cross-author only; hue fallback when the author has no
		// custom avatar. LoadOrFetchAuthorAvatar caches by domain, so calling
		// it per-comment is cheap even when several share a target.
		var targetAvatar *render.AvatarConfig
		if entry.Domain != "" && entry.Domain != siteDomain {
			cfg, ok := render.LoadOrFetchAuthorAvatar(entry.Domain)
			if !ok || cfg.BG == "" {
				cfg = render.FallbackAvatarConfig(entry.Domain)
			}
			targetAvatar = &cfg
		}
		enrich[info.commentURL] = commentThreadEnrichment{
			TargetPostTitle:          entry.Title,
			TargetPostBodyHTML:       postHTML,
			TargetPostTitleRedundant: titleRedundant,
			TargetPostPublished:      entry.Published,
			TargetPostPublishedHuman: template.StripYear(template.FormatHumanDateTime(entry.Published)),
			TargetPostAuthorDomain:   entry.Domain,
			TargetPostAuthorAvatar:   targetAvatar,
			CommentBodyHTML:          commentHTML,
		}
	}
	return enrich
}

// buildPostCommentEnrichments returns a per-post-URL map of rendered
// blessed comments for type=posts items on own-handle scope. Inverse of
// buildCommentThreadEnrichments: target posts are LOCAL (no remote
// fetch needed, since these are the tenant's own posts), and the
// comments listed in blessed.json are also already mirrored locally
// (the publish-time blessing flow copies the source markdown into the
// tenant's content tree). Renders each comment body through the same
// goldmark + UGC-sanitizer pipeline the per-post focus page uses, so
// trust-chain assumptions are unchanged from the SSR per-post path.
//
// The returned map is keyed on the post item's mount-URL
// (e.g., `/posts/20260101/hello.html`) — same key format
// loadOwnContentAsFeedItems emits and populateCommentCountsAndOptionally
// Filter uses, so the lookup in toStreamItemResponses hits without
// flexible MatchesPostPath logic.
//
// Empty result when blessed.json doesn't exist or no items have comments
// — non-fatal for the caller (the panel just won't render).
func (s *Server) buildPostCommentEnrichments(items []feed.CachedFeedItem) map[string][]blessedCommentResponse {
	out := make(map[string][]blessedCommentResponse)

	// Quick filter: only iterate posts with at least one comment.
	// CommentCount was stamped upstream by populateCommentCountsAndOptionally
	// Filter; for own-content paths that ran for feedType "" or "post".
	hasCandidate := false
	for _, it := range items {
		if it.Type == "post" && it.CommentCount > 0 {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		return out
	}

	bc, err := metadata.LoadBlessedComments(s.DataDir)
	if err != nil {
		return out
	}

	// Index blessed[] by mount-URL for O(1) lookup. Same URL-shape
	// derivation as populateCommentCountsAndOptionallyFilter.
	byMountURL := make(map[string][]metadata.BlessedComment, len(bc.Comments))
	for _, pc := range bc.Comments {
		mountURL := "/" + strings.TrimSuffix(strings.Replace(pc.Post, "content/pub.polis.core/post/", "posts/", 1), ".md") + ".html"
		byMountURL[mountURL] = pc.Blessed
	}

	for _, it := range items {
		if it.Type != "post" || it.CommentCount == 0 {
			continue
		}
		blessed := byMountURL[it.URL]
		if len(blessed) == 0 {
			continue
		}
		rendered := make([]blessedCommentResponse, 0, len(blessed))
		for _, b := range blessed {
			html := render.LoadLocalCommentContent(s.DataDir, b.URL)
			authorName := extractDomainFromURL(b.URL)
			// Display URL: mount-path .html so the link resolves to a
			// browsable per-comment page (same convention as
			// render.loadBlessedCommentsForPost).
			displayURL := b.URL
			if strings.HasSuffix(displayURL, ".md") {
				displayURL = strings.TrimSuffix(displayURL, ".md") + ".html"
			}
			rendered = append(rendered, blessedCommentResponse{
				URL:            displayURL,
				AuthorName:     authorName,
				Published:      b.BlessedAt,
				PublishedHuman: template.FormatHumanDate(b.BlessedAt),
				ContentHTML:    html,
			})
		}
		out[it.URL] = rendered
	}
	return out
}

// buildPostTitleRedundancy returns a per-post-URL map flagging posts whose
// body's first sentence already begins with the title text. Same detection
// the SSR sibling-render path runs (render/stream.go) for static stream
// output: applies render.StripLeadingTitleHeading to drop a leading ATX
// heading, then render.TitleStartsFirstSentence on the cleaned body.
//
// Brought to the API path so dynamically-rendered posts (the stream
// filter view) get the same title-chrome suppression as SSR'd posts.
// Without this, posts whose titles auto-derive as a truncation of the
// body's first sentence (e.g., discover.polis.pub) render the title
// once explicitly + once more inside the body — visually duplicated.
//
// Empty result on failure (missing source file, read error) — non-fatal
// for the caller; the post just renders with its title-link visible,
// matching the pre-2026-05-06 behavior.
func (s *Server) buildPostTitleRedundancy(items []feed.CachedFeedItem) map[string]bool {
	out := make(map[string]bool)
	for _, it := range items {
		if it.Type != "post" {
			continue
		}
		// URL → source path. Mirror of loadOwnContentAsFeedItems' forward
		// translation: /posts/YYYYMMDD/slug.html → content/pub.polis.core/post/YYYYMMDD/slug.md
		if !strings.HasPrefix(it.URL, "/posts/") {
			continue
		}
		rel := strings.TrimPrefix(it.URL, "/posts/")
		rel = strings.TrimSuffix(rel, ".html") + ".md"
		srcPath := filepath.Join(s.DataDir, "content", "pub.polis.core", "post", rel)
		raw, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		body := stripFrontmatter(string(raw))
		body = render.StripLeadingTitleHeading(body, it.Title)
		if render.TitleStartsFirstSentence(it.Title, body) {
			out[it.URL] = true
		}
	}
	return out
}

// buildPostBodies fetches + renders the full body HTML for post entries
// in the stream so renderPost can inject them inline via meta.body_html
// (matching the v4/public SSR'd surfaces). Two paths by origin:
//
//   - Own posts: read source markdown from disk via the sourcePaths
//     map (keyed by mount URL → "content/pub.polis.core/post/.../slug.md").
//     Pure local read — no SSRF concerns, no rate limits, faster than
//     LoadOrFetchReplyContext. Also fixes the "bullets in my post don't
//     show in the preview" issue, since the excerpt path strips markdown
//     formatting and the lazy-fetch path was unreliable for own URLs
//     (the SPA's spaHandler can intercept /posts/... requests).
//
//   - Cross-tenant posts: fetch via LoadOrFetchReplyContext (24h disk
//     cache, SSRF gate, body markdown extraction). Same as before.
//
// Output is keyed by item URL; the renderer's existing meta.body_html
// branch (originally wired for drafts) injects the expanded body
// without any JS changes.
func (s *Server) buildPostBodies(items []feed.CachedFeedItem, ownDomain string, sourcePaths map[string]string) map[string]string {
	out := make(map[string]string)

	// Pass 1: own posts. Local file reads, sequential — fast enough
	// without goroutines and avoids competing with the cross-tenant
	// semaphore.
	for _, it := range items {
		if it.Type != "post" {
			continue
		}
		if it.AuthorDomain == "" || it.AuthorDomain != ownDomain {
			continue
		}
		srcPath, ok := sourcePaths[it.URL]
		if !ok || srcPath == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.DataDir, srcPath))
		if err != nil {
			continue
		}
		body := stripFrontmatter(string(raw))
		body = render.StripLeadingTitleHeading(body, it.Title)
		rendered, err := render.MarkdownToHTML(body)
		if err != nil {
			continue
		}
		out[it.URL] = rendered
	}

	// Pass 2 (cross-tenant body fetch) intentionally removed. The SPA's
	// fetchBody() lazy-fetch path (stream.js fetchBody) handles cross-origin
	// reads directly because tenant origins ship Access-Control-Allow-Origin: *
	// via publicContentMiddleware. Doing the fetch server-side, eagerly, for
	// every list render was the source of a 2.5s regression — N items × HTTP
	// round trips, throttled at 5-parallel, plus a racy on-disk cache.
	// Click→read-focus now does one direct browser fetch to the origin for
	// the single post the user opens.
	return out
}

// readLocalCommentBody reads the local comment markdown body for a comment
// URL. Tolerant of both the mount form (/comments/YYYYMMDD/slug.{md,html}) and
// the canonical source form (/content/pub.polis.core/comment/…) so it keeps
// working as the comment URL is canonicalized (Defect 1). Returns the body
// markdown (frontmatter stripped) or "" on failure.
func readLocalCommentBody(dataDir, commentURL string) string {
	rel := polisurl.CommentURLToContentRel(commentURL)
	if rel == "" {
		return ""
	}
	srcPath := filepath.Join(dataDir, filepath.FromSlash(rel))
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return ""
	}
	return stripFrontmatter(string(raw))
}

// populateCommentCountsAndOptionallyFilter walks own-scope post items and
// stamps CommentCount from blessed.json's per-post blessed[] length. When
// filterWithComments is true (with=comments), drops posts with zero
// blessed comments.
//
// Single load of blessed.json per request — the function takes the items
// slice and returns a possibly-filtered slice; non-post items pass through
// untouched (CommentCount stays zero, item kept).
func (s *Server) populateCommentCountsAndOptionallyFilter(items []feed.CachedFeedItem, filterWithComments bool) []feed.CachedFeedItem {
	bc, err := metadata.LoadBlessedComments(s.DataDir)
	if err != nil {
		// No blessed.json → no posts have comments. With filter on,
		// return only non-post items (which means an empty list when
		// the input was all posts). Without the filter, items pass
		// through unchanged (CommentCount stays zero).
		if !filterWithComments {
			return items
		}
		out := items[:0]
		for _, it := range items {
			if it.Type != "post" {
				out = append(out, it)
			}
		}
		return out
	}

	// Build map: post-mount-URL → comment count. Mount URL matches the
	// format loadOwnContentAsFeedItems emits ("/posts/.../slug.html").
	// pc.Post may be in any of three forms depending on which polis-cli
	// version (or migration state) wrote blessed.json:
	//   - absolute URL:  https://<domain>/content/pub.polis.core/post/<date>/<slug>.md
	//   - absolute URL:  https://<domain>/posts/<date>/<slug>.md (mount form)
	//   - source path:   content/pub.polis.core/post/<date>/<slug>.md
	//   - mount path:    posts/<date>/<slug>.md
	// Normalize all of them to "posts/<date>/<slug>" (the mount stem) before
	// composing the .html URL — same approach as metadata.MatchesPostPath's
	// extractPath helper, which the SSR initial render uses. Without this
	// normalization the API path was producing keys like
	// "/https://discover.polis.pub/posts/.../slug.html" for tenants whose
	// blessed.json stored absolute URLs (e.g. discover), and every lookup
	// against it.URL ("/posts/.../slug.html") missed → badge stuck at 0.
	countByURL := make(map[string]int, len(bc.Comments))
	for _, pc := range bc.Comments {
		stem := pc.Post
		if i := strings.Index(stem, "/posts/"); i >= 0 {
			stem = stem[i+1:] // "posts/<date>/<slug>.md"
		} else if i := strings.Index(stem, "/post/"); i >= 0 {
			stem = "posts/" + stem[i+len("/post/"):] // source path → mount
		} else if strings.HasPrefix(stem, "content/pub.polis.core/post/") {
			stem = strings.Replace(stem, "content/pub.polis.core/post/", "posts/", 1)
		}
		stem = strings.TrimSuffix(stem, ".md")
		stem = strings.TrimSuffix(stem, ".html")
		mountURL := "/" + stem + ".html"
		countByURL[mountURL] = len(pc.Blessed)
	}

	out := items[:0]
	for _, it := range items {
		if it.Type == "post" {
			it.CommentCount = countByURL[it.URL]
			if filterWithComments && it.CommentCount == 0 {
				continue
			}
		}
		out = append(out, it)
	}
	return out
}

// populateCrossTenantCommentCounts stamps the TOTAL comment count (blessed +
// unblessed, from DS) on ALL post items in the paginated page — own and
// cross-tenant alike, so the badge means the same thing regardless of author.
// Own posts are pre-stamped with their blessed.json count by
// populateCommentCountsAndOptionallyFilter; that value remains as the fallback
// when DS is unavailable (the DS total overwrites it on success).
//
// Behavior:
//   - Cache hits stamp immediately (no DS roundtrip).
//   - All cache misses on the page are batched into ONE synchronous DS call
//     (capped at streamCountBatchMax) and stamped this render — deterministic
//     regardless of a post's position or cache warmth. The stream response
//     waits up to visibleCountFetchTimeout. On timeout/error own items keep
//     their blessed.json fallback and cross-tenant items stay at zero —
//     fail-soft, the stream never blocks beyond the budget.
//   - Only overflow beyond the batch cap (rare; explicit large `limit`) is
//     deferred to a fire-and-forget goroutine that warms the cache for the
//     next render.
//
// Fetches are wrapped in a per-URL-set singleflight so concurrent stream
// requests for overlapping pages collapse to one DS call.
//
// All errors are logged via LogEvent and swallowed; the stream response
// itself must never fail because of the count fetch. Items mutate
// in-place via the slice header — caller's `page` reflects updates.
func (s *Server) populateCrossTenantCommentCounts(r *http.Request, page []feed.CachedFeedItem, myDomain string) {
	if len(page) == 0 || s.DiscoveryURL == "" {
		return
	}

	// Collect every post's DS-count URL. Each post is queried against DS for
	// its TOTAL comment count (blessed + unblessed) so the badge means the
	// same thing for own and cross-tenant posts.
	//   - Own posts carry a relative mount URL; absolutize it to the canonical
	//     form DS indexed as in_reply_to. Their blessed.json-derived count
	//     (pre-stamped by populateCommentCountsAndOptionallyFilter) remains as
	//     the fallback when DS is unavailable.
	//   - Cross-tenant posts must already carry an absolute https URL
	//     (defensive against malformed cache rows that would never match DS).
	// Items are stamped by DS-URL via stampTargets so own (relative-URL) items
	// get the count keyed under their absolutized form. Cache hits stamp
	// immediately; misses are batched into one synchronous DS call below.
	missing := make([]string, 0, len(page))
	seen := make(map[string]struct{}, len(page))
	stampTargets := make(map[string][]int, len(page))

	for i := range page {
		it := &page[i]
		if it.Type != "post" {
			continue
		}
		dsURL := it.URL
		if it.AuthorDomain == myDomain {
			dsURL = s.absolutizeStreamURL(it.URL)
		}
		if !strings.HasPrefix(dsURL, "https://") {
			continue
		}
		stampTargets[dsURL] = append(stampTargets[dsURL], i)
		if cached, ok := s.getCommentCountCacheEntry(dsURL); ok {
			it.CommentCount = cached
			continue
		}
		if _, dup := seen[dsURL]; !dup {
			seen[dsURL] = struct{}{}
			missing = append(missing, dsURL)
		}
	}

	if len(missing) == 0 {
		return
	}

	// Sync-fetch the whole page's uncached counts in ONE batched DS call
	// (capped at streamCountBatchMax), so every post is stamped this render —
	// no positional/cache-warmth dependence. Bounded by the strict timeout;
	// on timeout/error items keep their pre-stamped value (own: blessed.json
	// fallback; cross-tenant: 0) — fail-soft, the stream never blocks beyond
	// the budget. Overflow beyond the cap (rare; large explicit `limit`) is
	// filled in the background for the next render.
	syncMissing := missing
	var overflow []string
	if len(syncMissing) > streamCountBatchMax {
		overflow = syncMissing[streamCountBatchMax:]
		syncMissing = syncMissing[:streamCountBatchMax]
	}

	ctx, cancel := context.WithTimeout(r.Context(), visibleCountFetchTimeout)
	client := s.NewDSClient(r)
	counts, err := s.commentCountFlight.Do(flightKey(syncMissing), func() (map[string]int, error) {
		return client.FetchCommentCountsCtx(ctx, syncMissing)
	})
	cancel()
	if err != nil {
		s.LogEvent("pub.polis.stream.counts_fetch_failed", map[string]interface{}{
			"url_count": len(syncMissing),
			"error":     err.Error(),
		})
	} else if len(counts) > 0 {
		s.setCommentCountCacheEntries(counts)
		// Stamp every page item mapped to a returned DS URL (own + cross-
		// tenant), keyed under the same dsURL used for the query.
		for dsURL, total := range counts {
			for _, idx := range stampTargets[dsURL] {
				page[idx].CommentCount = total
			}
		}
	}

	// Background fill for overflow beyond the batch cap. Fire-and-forget;
	// uses context.Background() so the goroutine outlives the HTTP request
	// and populates the cache for the next render.
	if len(overflow) > 0 {
		urls := overflow
		bgClient := s.NewDSClient(nil)
		go func() {
			bgCtx, bgCancel := context.WithTimeout(context.Background(), backgroundCountFetchTimeout)
			defer bgCancel()
			bgCounts, bgErr := s.commentCountFlight.Do(flightKey(urls), func() (map[string]int, error) {
				return bgClient.FetchCommentCountsCtx(bgCtx, urls)
			})
			if bgErr != nil {
				s.LogEvent("pub.polis.stream.counts_background_failed", map[string]interface{}{
					"url_count": len(urls),
					"error":     bgErr.Error(),
				})
				return
			}
			if len(bgCounts) > 0 {
				s.setCommentCountCacheEntries(bgCounts)
			}
		}()
	}
}

// flightKey builds a stable singleflight key from a set of URLs. Sorts
// to ensure two pages requesting the same URLs in different orders
// collapse onto the same in-flight call.
func flightKey(urls []string) string {
	if len(urls) == 1 {
		return urls[0]
	}
	sorted := append([]string(nil), urls...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\x00")
}

// absolutizeStreamURL converts an own-post relative mount URL
// ("/posts/YYYYMMDD/slug.html") to the absolute canonical form the DS indexed
// as a comment's in_reply_to (BaseURL + path). Cross-tenant items already carry
// absolute https URLs and pass through unchanged. This is the URL-form bridge
// that lets own posts hit the same DS comment data as cross-tenant posts —
// both the count (Phase 5) and the latest-comment focus fetch rely on it.
func (s *Server) absolutizeStreamURL(itemURL string) string {
	if strings.HasPrefix(itemURL, "/") {
		return s.GetBaseURL() + itemURL
	}
	return itemURL
}

// focusCommentCacheTTL mirrors commentCountCacheTTL — a short window that
// amortizes repeat focus on the same post (and a quick exit→re-enter) without
// the user noticing staleness. The comment body itself has its own 24h disk
// cache via LoadOrFetchCommentHTML; this cache short-circuits the DS roundtrip.
const focusCommentCacheTTL = 30 * time.Second

// handleStreamFocusComment resolves the single most-recent comment for a post
// (blessed or not) for the stream's read-focus mode. GET /api/v1/stream/focus-
// comment?url=<postURL>. Fail-soft: any error or missing comment returns
// {has_comment:false} with HTTP 200 so the focus UX never breaks.
func (s *Server) handleStreamFocusComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	postURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if postURL == "" || s.DiscoveryURL == "" {
		_ = json.NewEncoder(w).Encode(focusCommentResponse{HasComment: false})
		return
	}
	// Absolutize own-post relative mount URLs to the form DS indexed as
	// in_reply_to. Cross-tenant items already carry absolute https URLs.
	postURL = s.absolutizeStreamURL(postURL)

	// Short-TTL cache: repeat focus within the window skips DS + body fetch.
	if resp, ok := s.getFocusCommentCacheEntry(postURL); ok {
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	start := time.Now()
	myDomain := extractDomainFromURL(s.GetBaseURL())

	// Singleflight the whole resolve (DS lookup + body render) so concurrent
	// focus requests for the same post collapse to one DS roundtrip.
	resp, _ := s.latestCommentFlight.Do(postURL, func() (focusCommentResponse, error) {
		ctx, cancel := context.WithTimeout(r.Context(), visibleCountFetchTimeout)
		client := s.NewDSClient(r)
		latest, err := client.FetchLatestCommentsCtx(ctx, []string{postURL})
		cancel()
		if err != nil {
			s.LogEvent("pub.polis.stream.focus_comment_failed", map[string]interface{}{
				"url":   postURL,
				"error": err.Error(),
			})
			return focusCommentResponse{HasComment: false}, nil
		}
		lc, ok := latest[postURL]
		if !ok {
			return focusCommentResponse{HasComment: false}, nil
		}

		// Body source: own comment → local disk (no network/SSRF); cross-
		// tenant → fetch the commenter's origin (24h cached, SSRF-gated).
		// An empty body still returns the comment header (author + date).
		var contentHTML string
		if lc.AuthorDomain != "" && lc.AuthorDomain == myDomain {
			// Owner's OWN comment → its canonical content lives in the owner's
			// public path (legitimate, not a foreign-content violation).
			contentHTML = render.LoadOwnCommentContent(s.DataDir, lc.URL)
		} else {
			contentHTML, _ = render.LoadOrFetchCommentHTML(s.DataDir, lc.URL, 24*time.Hour)
		}

		// Display URL: mount-path .html so the link resolves to a browsable
		// per-comment page (same convention as buildPostCommentEnrichments).
		displayURL := lc.URL
		if strings.HasSuffix(displayURL, ".md") {
			displayURL = strings.TrimSuffix(displayURL, ".md") + ".html"
		}
		authorName := lc.AuthorDomain
		if authorName == "" {
			authorName = extractDomainFromURL(lc.URL)
		}
		return focusCommentResponse{
			HasComment: true,
			Comment: &blessedCommentResponse{
				URL:          displayURL,
				AuthorName:   authorName,
				AuthorDomain: authorName,
				AuthorAvatar: s.commentAuthorAvatar(lc.AuthorDomain),
				Published:    lc.Published,
				// Match the v4 stream meta-line date shape (StripYear +
				// FormatHumanDateTime) so the read-focus comment card's date
				// reads like every other v4 timestamp, not v3's "May 25, 2026".
				PublishedHuman: template.StripYear(template.FormatHumanDateTime(lc.Published)),
				ContentHTML:    contentHTML,
			},
		}, nil
	})

	// Fallback for the tenant's OWN posts: if the DS latest-comment lookup
	// returned nothing, resolve from the LOCAL blessed-comments index — the
	// same authoritative source the canonical per-post page renders from. A
	// locally-blessed comment can be absent from the DS latest-comment result
	// (not every blessed comment round-trips the DS the same way), which
	// produced the "comment shows on the per-post page but not in read-focus"
	// bug. Own-domain only — local has no blessed data for cross-tenant posts.
	if !resp.HasComment {
		if postDomain := extractDomainFromURL(postURL); postDomain != "" && postDomain == myDomain {
			if local := s.localFocusComment(postURL); local.HasComment {
				resp = local
			}
		}
	}

	s.setFocusCommentCacheEntry(postURL, resp)
	s.LogEvent("pub.polis.stream.focus_comment", map[string]interface{}{
		"url":         postURL,
		"has_comment": resp.HasComment,
		"duration_ms": time.Since(start).Milliseconds(),
	})
	_ = json.NewEncoder(w).Encode(resp)
}

// streamBodyResponse is the JSON shape GET /api/v1/stream/body returns: the
// full post body rendered to HTML for read-focus. HasBody=false means the
// body couldn't be resolved (fetch failed / unpublished); the SPA leaves the
// excerpt in place.
type streamBodyResponse struct {
	HasBody  bool   `json:"has_body"`
	BodyHTML string `json:"body_html,omitempty"`
}

// handleStreamBody returns the full rendered body for ONE post so the SPA's
// read-focus can show the whole post (not the truncated excerpt) for cross-
// tenant entries. GET /api/v1/stream/body?url=<postURL>.
//
// Why a server-side proxy instead of a direct browser fetch: the owner SPA
// ships a Content-Security-Policy with `connect-src 'self' https://esm.sh`,
// so the browser can't fetch a followed tenant's origin directly even though
// those origins serve Access-Control-Allow-Origin:* — CSP blocks the connect
// before CORS is consulted. This same-origin endpoint is allowed by 'self';
// it does the cross-origin read server-side (no CSP, no CORS), one post at a
// time (only the post the user opened), with LoadOrFetchReplyContext's 24h
// disk cache + SSRF gate. Fail-soft: any miss returns {has_body:false} at 200.
func (s *Server) handleStreamBody(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	postURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if postURL == "" {
		_ = json.NewEncoder(w).Encode(streamBodyResponse{HasBody: false})
		return
	}
	postURL = s.absolutizeStreamURL(postURL)

	start := time.Now()
	// 24h disk cache + SSRF gate live inside LoadOrFetchReplyContext; the
	// owner's own posts are same-origin (the SPA never proxies them — they
	// ship body_html inline and are already expanded), so this path is
	// effectively cross-tenant only.
	entry, ok := render.LoadOrFetchReplyContext(s.DataDir, postURL, 24*time.Hour)
	resp := streamBodyResponse{HasBody: false}
	if ok && entry.BodyMD != "" {
		// Strip the leading title heading (the entry already shows the title)
		// then render the same goldmark + sanitizer pipeline buildPostBodies
		// uses for own posts, so injected cross-tenant bodies match.
		cleaned := render.StripLeadingTitleHeading(entry.BodyMD, entry.Title)
		if html, err := render.MarkdownToHTML(cleaned); err == nil && strings.TrimSpace(html) != "" {
			resp = streamBodyResponse{HasBody: true, BodyHTML: html}
		}
	}

	s.LogEvent("pub.polis.stream.body", map[string]interface{}{
		"url":         postURL,
		"has_body":    resp.HasBody,
		"duration_ms": time.Since(start).Milliseconds(),
	})
	_ = json.NewEncoder(w).Encode(resp)
}

// localFocusComment resolves the latest BLESSED comment for one of the
// tenant's OWN posts from the local blessed-comments index — the same source
// render.loadBlessedCommentsForPost feeds the canonical per-post page. Returns
// {has_comment:false} when the post has no blessed comments. The date uses the
// v4 meta-line shape (StripYear + FormatHumanDateTime) to match the rest of the
// stream; the body is the locally-stored, already-sanitized comment HTML (same
// trust chain as the DS path's own-comment case). GetBlessedCommentsForPost's
// MatchesPostPath tolerates the mount-URL / source-path / .html-.md variations,
// so the absolutized post URL matches the stored post path directly.
func (s *Server) localFocusComment(postURL string) focusCommentResponse {
	blessed, err := metadata.GetBlessedCommentsForPost(s.DataDir, postURL)
	if err != nil || len(blessed) == 0 {
		return focusCommentResponse{HasComment: false}
	}
	// Read-focus shows exactly one comment — the most recently blessed.
	// BlessedAt is an RFC3339-ish stamp, so a lexical compare orders it.
	latest := blessed[0]
	for _, b := range blessed[1:] {
		if b.BlessedAt > latest.BlessedAt {
			latest = b
		}
	}
	// Display URL: mount-path .html so the link resolves to a browsable
	// per-comment page (same convention as render.loadBlessedCommentsForPost).
	displayURL := latest.URL
	if strings.HasSuffix(displayURL, ".md") {
		displayURL = strings.TrimSuffix(displayURL, ".md") + ".html"
	}
	authorDomain := extractDomainFromURL(latest.URL)
	return focusCommentResponse{
		HasComment: true,
		Comment: &blessedCommentResponse{
			URL:            displayURL,
			AuthorName:     authorDomain,
			AuthorDomain:   authorDomain,
			AuthorAvatar:   s.commentAuthorAvatar(authorDomain),
			Published:      latest.BlessedAt,
			PublishedHuman: template.StripYear(template.FormatHumanDateTime(latest.BlessedAt)),
			ContentHTML:    render.LoadLocalCommentContent(s.DataDir, latest.URL),
		},
	}
}

// focusCommentCacheEntry holds a resolved focus-comment response with expiry.
type focusCommentCacheEntry struct {
	resp      focusCommentResponse
	expiresAt time.Time
}

// getFocusCommentCacheEntry returns the cached focus-comment response for a
// post URL, or (zero, false) if expired or missing. Lazy expiry on read.
func (s *Server) getFocusCommentCacheEntry(url string) (focusCommentResponse, bool) {
	s.focusCommentCacheMu.Lock()
	defer s.focusCommentCacheMu.Unlock()
	if s.focusCommentCache == nil {
		return focusCommentResponse{}, false
	}
	entry, ok := s.focusCommentCache[url]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(s.focusCommentCache, url)
		}
		return focusCommentResponse{}, false
	}
	return entry.resp, true
}

// setFocusCommentCacheEntry stores a resolved focus-comment response under a
// short TTL. Both has-comment and no-comment results are cached so a post with
// no comments doesn't re-hit DS on every focus.
func (s *Server) setFocusCommentCacheEntry(url string, resp focusCommentResponse) {
	s.focusCommentCacheMu.Lock()
	defer s.focusCommentCacheMu.Unlock()
	if s.focusCommentCache == nil {
		s.focusCommentCache = make(map[string]*focusCommentCacheEntry)
	}
	s.focusCommentCache[url] = &focusCommentCacheEntry{
		resp:      resp,
		expiresAt: time.Now().Add(focusCommentCacheTTL),
	}
}

// focusCommentSingleflight collapses concurrent focus-comment resolves for the
// same post URL into one. Mirrors commentCountSingleflight (server.go) but
// carries a focusCommentResponse.
type focusCommentFlightCall struct {
	wg  sync.WaitGroup
	val focusCommentResponse
	err error
}

type focusCommentSingleflight struct {
	mu       sync.Mutex
	inflight map[string]*focusCommentFlightCall
}

// Do executes fn() exactly once per key while one is in flight; concurrent
// callers with the same key block and receive the same result.
func (g *focusCommentSingleflight) Do(key string, fn func() (focusCommentResponse, error)) (focusCommentResponse, error) {
	g.mu.Lock()
	if g.inflight == nil {
		g.inflight = make(map[string]*focusCommentFlightCall)
	}
	if c, present := g.inflight[key]; present {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &focusCommentFlightCall{}
	c.wg.Add(1)
	g.inflight[key] = c
	g.mu.Unlock()

	if fn != nil {
		c.val, c.err = fn()
	}

	g.mu.Lock()
	delete(g.inflight, key)
	g.mu.Unlock()
	c.wg.Done()

	return c.val, c.err
}

// filterStreamItemsByTime drops items published before the cutoff. Items
// without a parseable Published timestamp are kept (defensive — better to
// over-include than over-filter on bad data). Allocates a fresh slice
// rather than reusing the input via items[:0]; if upstream ever returns
// a slice into internal cache state, in-place filtering would corrupt
// the cache.
func filterStreamItemsByTime(items []feed.CachedFeedItem, cutoff time.Time) []feed.CachedFeedItem {
	out := make([]feed.CachedFeedItem, 0, len(items))
	for _, it := range items {
		t, err := time.Parse(time.RFC3339, it.Published)
		if err != nil {
			out = append(out, it)
			continue
		}
		if !t.Before(cutoff) {
			out = append(out, it)
		}
	}
	return out
}

// applyStreamCursor drops items already consumed by previous pages.
// Cursor comparison is two-keyed: primary key is parsed time.Time
// (robust to RFC3339 representation differences — `T12:00:00Z` vs.
// `T12:00:00.000Z` would compare unequal under string comparison but
// equal as time.Time). Secondary key is item ID (string compare on the
// stable hex ID), used as tiebreaker when timestamps are equal at the
// page boundary.
//
// For newest order: keep items where (published < cursor.before) OR
// (published == cursor.before AND id > cursor.beforeId).
// For oldest order: keep items where (published > cursor.after) OR
// (published == cursor.after AND id > cursor.afterId).
func applyStreamCursor(items []feed.CachedFeedItem, c streamCursor, order string) []feed.CachedFeedItem {
	if order == "newest" {
		if c.BeforePublished == "" {
			return items
		}
		cursorTime, cursorTimeOK := parseStreamTime(c.BeforePublished)
		out := make([]feed.CachedFeedItem, 0, len(items))
		for _, it := range items {
			itTime, itTimeOK := parseStreamTime(it.Published)
			if cursorTimeOK && itTimeOK {
				if itTime.Before(cursorTime) {
					out = append(out, it)
					continue
				}
				if itTime.Equal(cursorTime) && it.ID > c.BeforeID {
					out = append(out, it)
				}
				continue
			}
			// Defensive fallback for unparseable timestamps: lex compare.
			// Should not happen with feed cache items (published is
			// RFC3339-emitted upstream) but avoids data loss.
			if it.Published < c.BeforePublished {
				out = append(out, it)
			} else if it.Published == c.BeforePublished && it.ID > c.BeforeID {
				out = append(out, it)
			}
		}
		return out
	}
	// oldest order
	if c.AfterPublished == "" {
		return items
	}
	cursorTime, cursorTimeOK := parseStreamTime(c.AfterPublished)
	out := make([]feed.CachedFeedItem, 0, len(items))
	for _, it := range items {
		itTime, itTimeOK := parseStreamTime(it.Published)
		if cursorTimeOK && itTimeOK {
			if itTime.After(cursorTime) {
				out = append(out, it)
				continue
			}
			if itTime.Equal(cursorTime) && it.ID > c.AfterID {
				out = append(out, it)
			}
			continue
		}
		if it.Published > c.AfterPublished {
			out = append(out, it)
		} else if it.Published == c.AfterPublished && it.ID > c.AfterID {
			out = append(out, it)
		}
	}
	return out
}

// parseStreamTime parses a feed-item Published timestamp via time.Parse.
// Tolerates both RFC3339 ("...Z") and RFC3339Nano (".000000000Z");
// returns ok=false if neither parses.
func parseStreamTime(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// paginateStreamItems takes the first `limit` items + computes a cursor
// anchored on the last item's (Published, ID). The ID tiebreaker handles
// the case where multiple items share a Published timestamp at the page
// boundary — without it, items with the same timestamp on the next page
// would be silently dropped. Returns ("", "") when the page exhausts
// the dataset.
func paginateStreamItems(items []feed.CachedFeedItem, limit int, order string) ([]feed.CachedFeedItem, string) {
	if len(items) == 0 {
		return items, ""
	}
	if len(items) <= limit {
		return items, ""
	}
	page := items[:limit]
	last := page[limit-1]
	if order == "newest" {
		return page, encodeStreamCursor(streamCursor{
			BeforePublished: last.Published,
			BeforeID:        last.ID,
		})
	}
	return page, encodeStreamCursor(streamCursor{
		AfterPublished: last.Published,
		AfterID:        last.ID,
	})
}

func parseStreamLimit(raw string) int {
	if raw == "" {
		return defaultStreamLimit
	}
	var n int
	_, err := fmt.Sscanf(raw, "%d", &n)
	if err != nil || n <= 0 {
		return defaultStreamLimit
	}
	if n > maxStreamLimit {
		return maxStreamLimit
	}
	return n
}

func writeStreamError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": msg,
	})
}

// handleNotificationRules returns the registry's notification rule
// declarations as JSON. Step-06/6.f.2: backs the SPA's activity-mode
// renderer (owner-extras.js fetches once at init, indexes by event
// type, looks up per-entry on afterRender to format the rule
// template + icon).
//
// Read-only. The same rule data that used to live in the public-served
// bundle.json (now relocated to private registry.json per 6.f.1) — no
// secret content here. A dedicated endpoint keeps the SPA's index.html
// statically cacheable while the rule set can update independently
// (Patrol/Medic re-syncs rules on tenant cycles).
func (s *Server) handleNotificationRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	reg, err := bundle.LoadRegistryOrInit(s.DataDir)
	if err != nil {
		s.LogError("notification rules: load registry: %v", err)
		writeStreamError(w, http.StatusInternalServerError, "registry unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// step-06/6.f.2 review nit #10: short-lived browser caching.
	// Rules change infrequently (only on registry edits / fresh init /
	// Medic remediation); SPA fetches once at init. 5min cache +
	// must-revalidate keeps repeat fetches cheap without making the
	// rules-update latency noticeable.
	w.Header().Set("Cache-Control", "max-age=300, must-revalidate")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"rules": reg.Notifications,
	})
}

// allowedClientEvents is the allowlist of event names the SPA can emit
// via POST /api/v1/event. Step-06/6.e ships exactly one entry; future
// expansion adds entries here as new SPA-emitted telemetry surfaces
// land. Restricting the allowlist prevents the endpoint from becoming
// a generic "log anything" sink that could be abused or noised up
// with unstructured events.
var allowedClientEvents = map[string]bool{
	"pub.polis.stream.preset_loaded": true,
}

// clientEventFieldSchema defines per-event-name field validation:
// allowed field names + a max-length cap for string values.
// Step-06/6.f.2 review concern #2: without this, an attacker (or a
// misbehaving SPA) could submit fields of any shape — nested objects,
// arrays, unbounded strings — and the structured-log writer would
// faithfully serialize them, polluting Axiom queries. The schema
// enforces shape-of-truth: only known string fields, length-capped.
//
// To add a new event: extend allowedClientEvents above AND add a
// schema entry here. Events with no schema entry are accepted but
// drop all fields (anti-spoof default).
const clientEventFieldMaxLen = 256

var clientEventFieldSchemas = map[string]map[string]bool{
	"pub.polis.stream.preset_loaded": {
		"preset_name": true,
		"qualifier":   true,
		"type":        true,
		"scope":       true,
		"modifier":    true,
	},
}

// validateClientEventFields filters the caller's fields to the
// schema-allowed set, enforces string-only values, and length-caps
// strings. Returns a fresh map; the input is left untouched. Unknown
// or non-string-typed fields are silently dropped (callers shouldn't
// be sending them per the schema contract).
func validateClientEventFields(eventName string, in map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	allowed := clientEventFieldSchemas[eventName]
	if allowed == nil {
		return out
	}
	for k, v := range in {
		if !allowed[k] {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if len(s) > clientEventFieldMaxLen {
			s = s[:clientEventFieldMaxLen]
		}
		out[k] = s
	}
	return out
}

// handleClientEvent accepts {event, fields} from the SPA and routes it
// to the standard structured-event log. Step-06/6.e: backs the icon-
// preset usage telemetry. Allowlist-gated; events outside the list
// return 400 (caller bug — the SPA should only emit known events).
//
// step-06/6.f.2 review concern #2: per-event field shape validation
// via clientEventFieldSchemas. Caller fields are filtered to the
// allowed set, coerced to string-only, and length-capped before
// reaching s.LogEvent. Prevents log pollution from arbitrary nested
// structures or unbounded string values.
//
// No auth gate today (matches the owner-trust model used by other
// SPA endpoints — assumes localhost or cookie-bound visitor). Once
// 6.k auth-aware nav lands, this endpoint should likely gate on the
// same auth signal. Tracked for 6.p hardening.
func (s *Server) handleClientEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Event  string                 `json:"event"`
		Fields map[string]interface{} `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeStreamError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !allowedClientEvents[body.Event] {
		writeStreamError(w, http.StatusBadRequest,
			fmt.Sprintf("event %q not in allowlist (step-06/6.e ships %d allowed events; see allowedClientEvents)", body.Event, len(allowedClientEvents)))
		return
	}
	// Filter caller fields through the per-event schema (drops unknown
	// keys, drops non-string values, length-caps strings). Yields a
	// fresh map so the caller's input can't smuggle anything past
	// validation via reference aliasing.
	cleanFields := validateClientEventFields(body.Event, body.Fields)
	// Stamp a source marker so downstream log queries can distinguish
	// SPA-emitted events from server-emitted ones (both share the
	// pub.polis.* namespace). Server-stamped after validation so the
	// caller can't spoof a different origin marker.
	cleanFields["client_origin"] = "owner-spa"
	s.LogEvent(body.Event, cleanFields)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": true,
	})
}
