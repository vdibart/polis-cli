package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/template"
)

func TestPickStreamSiblings(t *testing.T) {
	// Posts in newest-first order: A=newest, F=oldest.
	posts := make([]template.PostData, 6)
	for i := range posts {
		posts[i] = template.PostData{Title: string(rune('A' + i))}
	}

	cases := []struct {
		name           string
		focusIdx       int
		corpus         []template.PostData
		wantAbove      []string // titles in DOM order (top of above rail first)
		wantBelow      []string // titles in DOM order (immediately-below-focus first)
		wantAboveFlags bool     // assert IsAboveFocus=true on every above entry
	}{
		{
			// Homepage case: focus is newest, no newer to show.
			// All sibling slots go to the older rail. Corpus has
			// only 5 older than focus[0] (B..F), so below rail
			// fills with all 5 even though budget is 6.
			name:      "focus at newest → 0 above, all-older below (budget room unused)",
			focusIdx:  0,
			corpus:    posts,
			wantAbove: []string{},
			wantBelow: []string{"B", "C", "D", "E", "F"},
		},
		{
			// Mid-corpus case: 1 newer above + 3 older below = 4. With
			// the post-2026-04-29 budget=6, pad-above kicks in: idx-2
			// (=A) prepends to above. Result: above=[A,B], below=[D,E,F].
			// Total = 5; nextNewerIdx exhausted (=-1) so loop stops.
			name:           "focus mid-list → 2 newer above (padded), 3 older below",
			focusIdx:       2,
			corpus:         posts,
			wantAbove:      []string{"A", "B"},
			wantBelow:      []string{"D", "E", "F"},
			wantAboveFlags: true,
		},
		{
			// End-of-corpus case: no older posts → pad above with
			// nearest-newer first (immediately-newer at bottom of
			// above rail, walking outward). Budget=6 lets all 5
			// available newer pack into the above rail.
			name:           "focus at oldest → 5 newer above (nearest-first), 0 below",
			focusIdx:       5,
			corpus:         posts,
			wantAbove:      []string{"A", "B", "C", "D", "E"},
			wantBelow:      []string{},
			wantAboveFlags: true,
		},
		{
			// Tiny corpus: 2 posts only, focus on newest → 1 below, 0 above.
			name:      "tiny corpus, focus on newest → 1 older below",
			focusIdx:  0,
			corpus:    posts[:2],
			wantAbove: []string{},
			wantBelow: []string{"B"},
		},
		{
			// Tiny corpus: 2 posts only, focus on oldest → 1 above, 0 below.
			name:           "tiny corpus, focus on oldest → 1 newer above",
			focusIdx:       1,
			corpus:         posts[:2],
			wantAbove:      []string{"A"},
			wantBelow:      []string{},
			wantAboveFlags: true,
		},
	}

	titles := func(ps []template.PostData) []string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = p.Title
		}
		return out
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			above, below := pickStreamSiblings(tc.corpus, tc.focusIdx)
			gotAbove := titles(above)
			gotBelow := titles(below)
			if !slicesEqual(gotAbove, tc.wantAbove) {
				t.Errorf("above titles = %v, want %v", gotAbove, tc.wantAbove)
			}
			if !slicesEqual(gotBelow, tc.wantBelow) {
				t.Errorf("below titles = %v, want %v", gotBelow, tc.wantBelow)
			}
			if tc.wantAboveFlags {
				for _, p := range above {
					if !p.IsAboveFocus {
						t.Errorf("above entry %q must have IsAboveFocus=true", p.Title)
					}
				}
			}
			for _, p := range below {
				if p.IsAboveFocus {
					t.Errorf("below entry %q must have IsAboveFocus=false", p.Title)
				}
			}
		})
	}

	// Singleton corpus: no siblings either direction.
	above, below := pickStreamSiblings([]template.PostData{{Title: "only"}}, 0)
	if above != nil || below != nil {
		t.Errorf("singleton corpus should yield nil rails, got above=%v below=%v", above, below)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRenderAll_StreamSmoke asserts that a v4 tenant renders a full site: a
// site-root index.html plus posts/YYYYMMDD/slug.html artifacts, each with the
// focus-content demarcation and SSR'd sibling excerpts.
func TestRenderAll_StreamSmoke(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	writeStreamPost(t, tempDir, "20260315", "alpha", "Alpha", "First body text.")
	writeStreamPost(t, tempDir, "20260320", "bravo", "Bravo", "Second body text.")
	writeStreamPost(t, tempDir, "20260325", "charlie", "Charlie", "Third body text.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:           tempDir,
		BaseURL:           "https://example.com",
		PostsSourceDir:    "content/pub.polis.core/post",
		PostsMountDir:     "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if r.shapeName != "v4" {
		t.Fatalf("expected shapeName=v4, got %q", r.shapeName)
	}

	stats, err := r.RenderAll(true)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if stats.PostsRendered != 3 {
		t.Errorf("posts rendered = %d, want 3", stats.PostsRendered)
	}
	if !stats.IndexGenerated {
		t.Error("IndexGenerated should be true")
	}

	// styles.css must exist and contain stream.css + theme CSS (concat order).
	styles, err := os.ReadFile(filepath.Join(tempDir, "styles.css"))
	if err != nil {
		t.Fatalf("read styles.css: %v", err)
	}
	// step-04/4.a pivoted topbar selector from .v4-nav* to .polis-topbar*.
	// Old .v4-nav* compat rules dropped — all tenants upgrade simultaneously
	// via Medic's version-gated resync.
	if !strings.Contains(string(styles), ".polis-topbar") {
		t.Error("styles.css missing shape (v4) CSS — expected '.polis-topbar' selector")
	}

	// index.html: focus is newest post (Charlie); siblings are Bravo, Alpha.
	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	assertContains(t, indexHTML, `data-polis-focus="true"`, "index focus demarcation")
	assertContains(t, indexHTML, "Charlie", "index focus title")
	assertContains(t, indexHTML, "Bravo", "index sibling: Bravo")
	assertContains(t, indexHTML, "Alpha", "index sibling: Alpha")

	// Per-post page: focus = Alpha, siblings are the two newer posts.
	alphaHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260315", "alpha.html"))
	assertContains(t, alphaHTML, `data-polis-focus="true"`, "alpha focus demarcation")
	assertContains(t, alphaHTML, "Alpha", "alpha page title")
	// Alpha is oldest → siblings pad backwards from index 0 (Charlie, Bravo).
	assertContains(t, alphaHTML, "Charlie", "alpha siblings include Charlie")
	assertContains(t, alphaHTML, "Bravo", "alpha siblings include Bravo")

	// Relative paths for CSS must walk up the right number of levels.
	assertContains(t, alphaHTML, `href="../../styles.css"`, "alpha styles.css href")
	assertContains(t, alphaHTML, `href="../../base.css"`, "alpha base.css href")
}

// TestRenderAll_StreamBlessedCommentsPanel locks in the step-05/5.i Phase B
// comment-READ panel SSR contract:
//   - The `.entry-comments-panel` div is rendered under the focus body
//     and iterates blessed comments via the
//     `snippets/blessed-comment.html` partial (each `.comment` carries
//     `.comment-author` / `.comment-date` / `.comment-body`).
//   - The `<noscript>` rule for the no-JS fallback (Q-PB3) lands in the
//     <head>.
//
// (The original Phase B ADD-1 also asserted on `.entry-comments-badge`
// in the focus meta-line. R27 — commit 1c076b4 — retired that badge in
// favor of the `.timeline-dot` add-comment affordance, so the badge
// assertions were dropped.)
func TestRenderAll_StreamBlessedCommentsPanel(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)
	writeStreamPost(t, tempDir, "20260423", "alpha", "Alpha", "Body.")

	// Bless one comment for the focus post. The path matches what
	// renderStreamFile hands to loadBlessedCommentsForPost (srcRel = the
	// content-source path, not the mounted /posts/.../*.html URL).
	if err := metadata.AddBlessedComment(tempDir,
		"content/pub.polis.core/post/20260423/alpha.md",
		metadata.BlessedComment{
			URL:       "https://bob.example.org/comments/20260424/re-alpha.md",
			Version:   "sha256:abc",
			BlessedAt: "2026-04-24T10:30:00Z",
		}); err != nil {
		t.Fatalf("AddBlessedComment: %v", err)
	}

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	postHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260423", "alpha.html"))

	// ADD-2: noscript fallback in <head>.
	assertContains(t, postHTML, "<noscript>", "noscript block in <head>")
	assertContains(t, postHTML, ".entry-comments-panel {", "noscript style targets the panel")
	assertContains(t, postHTML, "max-height: none;", "noscript style opens panel by default")

	// Panel rendered under focus body.
	assertContains(t, postHTML, `class="entry-comments-panel" id="comments"`,
		"comment panel rendered under focus body")

	// blessed-comment partial substituted: comment block + author domain
	// (extractDomain pulls "bob.example.org" from the comment URL) +
	// human-readable bless date ("April 24, 2026").
	assertContains(t, postHTML, `class="comment"`, "blessed comment block rendered")
	assertContains(t, postHTML, `class="comment-author"`, "comment-author element rendered")
	assertContains(t, postHTML, `class="comment-date"`, "comment-date element rendered")
	assertContains(t, postHTML, `class="comment-body"`, "comment-body element rendered")
	assertContains(t, postHTML, "bob.example.org",
		"comment author domain extracted from URL")
}

// TestRenderAll_StreamBlessedCommentsPanel_Empty locks in the no-comments
// case: panel is still DOM-present (so the noscript fallback + future
// JS-driven appends work uniformly).
//
// (Originally also asserted that the focus comment-badge gained an
// `.is-empty` class — R27, commit 1c076b4, retired that badge entirely
// in favor of the `.timeline-dot` add-comment affordance, so those
// assertions were dropped.)
func TestRenderAll_StreamBlessedCommentsPanel_Empty(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)
	writeStreamPost(t, tempDir, "20260423", "alpha", "Alpha", "Body.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	postHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260423", "alpha.html"))

	// Panel still rendered (DOM-present for noscript fallback uniformity).
	assertContains(t, postHTML, `class="entry-comments-panel" id="comments"`,
		"panel rendered even with zero comments (DOM-present + visually hidden)")

	// No `.comment` elements inside the panel.
	if strings.Contains(postHTML, `class="comment"`) {
		t.Errorf("expected no .comment elements in empty panel, got rendered comment markup")
	}
}

// TestRenderAll_StreamDoesNotEmitV3Archives — step-06/6.m. v3's
// page-based renderer emitted "archive" pages — flat HTML lists at
// posts/index.html, comments/index.html, tag/<slug>.html, etc. — that
// v4's unified-stream architecture replaces with the live stream
// controller (filter widget + paginated /api/v1/stream/items). The
// legacy archive pages have no place in v4: the stream covers all
// the use cases (filtering by type, by author, by tag, etc.), and
// the static archive HTML would shadow the stream's dynamic results
// if it stuck around.
//
// renderStreamAll already documents this in its package comment:
// "v4 does not emit per-comment pages, tag pages, or an archive
// index — those surfaces are subsumed by the stream controller."
// This test locks the contract: after a v4 RenderAll, none of the
// legacy archive paths exist on disk. Catches a regression where
// someone routes v4 RenderAll through the v3 RenderArchive path
// (which still exists for v3 tenants).
//
// NOTE: stale v3 archive files from a tenant's pre-migration era
// aren't deleted by v4 — they sit on disk as dead artifacts. Per
// resolved-decision: alpha state, no redirects, accept the dead
// files. They're not actively served by anything beyond static-file
// fall-through.
func TestRenderAll_StreamDoesNotEmitV3Archives(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)
	writeStreamPost(t, tempDir, "20260507", "p1", "Post 1", "Body.")
	writeStreamPost(t, tempDir, "20260508", "p2", "Post 2", "Body.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// v4 emits per-post HTML + a top-level index.html (focus = newest
	// post). It must NOT emit the v3-style archive paths.
	legacyArchives := []string{
		"posts/index.html",
		"comments/index.html",
		"tag/index.html",
		"comment/index.html",
	}
	for _, p := range legacyArchives {
		full := filepath.Join(tempDir, p)
		if _, err := os.Stat(full); err == nil {
			t.Errorf("v4 RenderAll emitted legacy v3 archive path %q — must not exist post-step-6", p)
		}
	}

	// And the top-level /posts/<date>/<slug>.html files DO exist (sanity
	// check — without this, a regression that disables ALL emission
	// would slip past the absence checks above).
	if _, err := os.Stat(filepath.Join(tempDir, "posts", "20260507", "p1.html")); err != nil {
		t.Errorf("expected per-post page emitted at posts/20260507/p1.html: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "index.html")); err != nil {
		t.Errorf("expected v4 home index.html emitted: %v", err)
	}
}

// TestRenderAll_StreamCommentWriteSurface — step-06/6.j. v3 mounted a
// `{{> theme:polis-widget}}` snippet on canonical pages that doubled
// as a comment-WRITE CTA + create-account teaser. v4 SHAPE dropped this
// in step-05; 6.j restores it. Per-post pages (focus entry only — NOT
// siblings) ship:
//
//   1. A `#polis-widget` mount inside the focus article — the host
//      element the polis.pub-served widget.js attaches a closed Shadow
//      DOM to (which renders the comment form / signup CTA / owner
//      "Edit →" depending on visitor auth state).
//   2. A `.polis-widget-fallback` light-DOM child rendering "Reply with
//      your polis identity at polis.pub →" — visible only when the
//      widget script never loads or fails to attach (Shadow DOM hides
//      the light DOM children once attachShadow runs).
//   3. The widget script tag (`<script src=".../widget-X.Y.Z.js" defer>`)
//      with the bundle's WidgetVersion templated in.
//   4. A `<noscript>` fallback link for visitors with JS disabled.
//
// Locks the contract that downstream theme overrides + future template
// edits don't drop the comment-WRITE surface again.
func TestRenderAll_StreamCommentWriteSurface(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)
	writeStreamPost(t, tempDir, "20260507", "writesurf", "WriteSurf", "Body.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	postHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260507", "writesurf.html"))

	// 1. Mount point with the data-* attrs widget.js reads.
	assertContains(t, postHTML, `id="polis-widget"`,
		"comment-WRITE mount missing — widget will have nothing to attach to")
	assertContains(t, postHTML, `data-page-type="post"`,
		"polis-widget mount must declare page_type=post")
	assertContains(t, postHTML, `data-author=`,
		"polis-widget mount must carry data-author for widget state detection")

	// 2. Light-DOM fallback CTA (hidden once widget attaches a Shadow DOM).
	assertContains(t, postHTML, `class="polis-widget-fallback"`,
		"fallback CTA missing — visitors whose widget script fails to load see nothing")
	assertContains(t, postHTML, "polis.pub",
		"fallback CTA must point back to polis.pub")

	// 3. Widget script tag with the templated version.
	assertContains(t, postHTML, "polis.pub/widget-"+WidgetVersion+".js",
		"widget script tag missing or wrong version — widget never hydrates")
	assertContains(t, postHTML, `crossorigin="anonymous"`,
		"widget script tag must declare crossorigin=anonymous")
	assertContains(t, postHTML, "defer",
		"widget script tag must defer (not block parser)")

	// 4. Noscript fallback for JS-disabled visitors.
	if !strings.Contains(postHTML, "<noscript>") {
		t.Error("noscript fallback missing from comment-WRITE block")
	}
	assertContains(t, postHTML, `polis-widget-fallback--noscript`,
		"noscript link should carry the noscript fallback class")
}

// TestRenderAll_StreamFilterPartialSubstitutes locks in the SD-1 close
// (step-05/5.h, partial coverage): the `{{> snippets/stream-filter}}`
// partial in stream.html includes correctly + its `{{site_domain}}`
// variable substitutes into the rendered output. Catches template-level
// regressions (broken partial path, missing site_domain wiring,
// data-mobile-hide attrs lost, etc.). Doesn't catch JS-logic regressions
// in the filter widget — deeper headless-JS coverage stays open as the
// alpha trade-off documented in the SD-1 tracker entry.
func TestRenderAll_StreamFilterPartialSubstitutes(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)
	writeStreamPost(t, tempDir, "20260423", "alpha", "Alpha", "Body.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	// Partial included → its outer wrapper must be in the output.
	assertContains(t, indexHTML, `class="sentence-filter"`, "stream-filter partial included")
	// Slot grammar (post-2026-04-29 redesign) — qualifier (locked), type
	// (interactive), scope (identity), modifier (interactive, conditional
	// on type=posts). The time slot was replaced with the modifier slot.
	assertContains(t, indexHTML, `data-filter-slot="qualifier"`, "qualifier slot present")
	assertContains(t, indexHTML, `data-filter-slot="type"`, "type slot present")
	assertContains(t, indexHTML, `data-filter-slot="scope"`, "scope slot present")
	assertContains(t, indexHTML, `data-filter-slot="modifier"`, "modifier slot present")
	// Mobile-hide attribution preserved (filter abbreviation depends on it).
	assertContains(t, indexHTML, `data-mobile-hide="true"`, "data-mobile-hide attr present")
	// {{site_domain}} substitutes into the scope slot. setupStreamTestSite
	// configures the tenant with a domain derived from its base_url; assert
	// the substituted hostname appears inside the scope slot's identity span.
	// The @-prefix was dropped per UI-refinement review 2026-04-28; the
	// scope slot now reads as the bare domain.
	scopeMarker := `data-filter-slot="scope"`
	scopeIdx := strings.Index(indexHTML, scopeMarker)
	if scopeIdx < 0 {
		t.Fatalf("scope slot not found in rendered output")
	}
	// Look for the substituted domain in the ~200 chars after the slot
	// declaration (covers the title attr + the slot's text content).
	tail := indexHTML[scopeIdx:]
	if len(tail) > 300 {
		tail = tail[:300]
	}
	if !strings.Contains(tail, "example.com") {
		t.Errorf("scope slot missing substituted site_domain; got: %q", tail)
	}
}

// TestRenderAll_StreamLayoutAndDateFormat locks in the step-05/5.g layout
// overhaul + SC-3 date-format unification:
//   - Two-column layout markup (.layout / .layout-left / .layout-right /
//     .bottom-fade) renders into the SSR'd output.
//   - Year-marker is emitted from focus.PublishedYear.
//   - Per-entry meta-line uses the entry-meta-line / entry-date-time markup.
//   - published_human carries the date+time format ("April 23, 2026 · 4:12pm")
//     for both the focus and the SSR'd siblings — the same format the
//     /api/v1/stream/items endpoint emits, so SSR'd entries and JS-paginated
//     ones never drift visually.
//
// SC-3 regression guard: if the SSR'd format reverts to date-only, the
// last assertion fails because no `· 4:12pm` substring would land in the
// rendered HTML.
func TestRenderAll_StreamLayoutAndDateFormat(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	// Pin published timestamps so the rendered date assertions are exact.
	writeStreamPostAt(t, tempDir, "20260423", "river", "An afternoon at the river", "2026-04-23T16:12:00Z", "Body.")
	writeStreamPostAt(t, tempDir, "20260421", "spring", "Notes on spring", "2026-04-21T08:30:00Z", "Body.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	// Layout + chrome.
	assertContains(t, indexHTML, `class="layout"`, "two-column layout container")
	assertContains(t, indexHTML, `class="layout-left"`, "sticky bio column")
	assertContains(t, indexHTML, `class="layout-right"`, "scrolling content column")
	assertContains(t, indexHTML, `class="bottom-fade"`, "bottom-fade overlay")
	// Year-marker retired 2026-04-29: was a fixed h2 above .stream that
	// fought scroll-up pagination across years and added little value.
	// The HTML element should NOT be present in any rendered output.
	if strings.Contains(indexHTML, `class="year-marker"`) {
		t.Errorf("year-marker should be retired but its class is in rendered HTML")
	}
	// Top-fade overlay was prototyped in 1.7.25 then removed in 1.7.26
	// (didn't read as intended). The element should NOT be present.
	if strings.Contains(indexHTML, `class="top-fade"`) {
		t.Errorf("top-fade should be removed but its class is in rendered HTML")
	}
	// New entry-meta-line markup (replaces the old .entry-meta grid block).
	assertContains(t, indexHTML, `class="entry-meta-line"`, "entry-meta-line present")
	assertContains(t, indexHTML, `class="entry-date-time"`, "entry-date-time present")
	// Date+time format ("April 23, 2026 · 4:12pm") matches handlers_stream.go's
	// emission. SSR-vs-dynamic drift would surface here as a missing substring.
	assertContains(t, indexHTML, "April 23, 2026 · 4:12pm", "focus published_human full date+time")
	// Sibling also re-formatted — guarantees both ends of the SSR window match.
	assertContains(t, indexHTML, "April 21, 2026 · 8:30am", "sibling published_human full date+time")
}

// TestRenderAll_StreamSiteIdentityBlock asserts the .site-bio + .site-stats
// blocks on the layout-left card render their substituted values per GAP-2:
// bio comes from snippets/about.md (rendered to HTML); posts/followers/
// following counts come from the public index, .polis/ds/<dom>/state/
// pub.polis.follow.json, and content/pub.polis.core/follow/following.json
// respectively.
func TestRenderAll_StreamSiteIdentityBlock(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)
	writeStreamPost(t, tempDir, "20260315", "alpha", "Alpha", "Body.")
	writeStreamPost(t, tempDir, "20260320", "bravo", "Bravo", "Body.")

	// Bio: write a sentinel about.md that distinguishes from any other
	// markdown rendered into the page.
	aboutDir := filepath.Join(tempDir, "site", "snippets")
	if err := os.MkdirAll(aboutDir, 0755); err != nil {
		t.Fatalf("mkdir about: %v", err)
	}
	if err := os.WriteFile(filepath.Join(aboutDir, "about.md"),
		[]byte("Sentinel bio with *emphasis*.\n"), 0644); err != nil {
		t.Fatalf("write about.md: %v", err)
	}

	// Followers: write a JSON array under the DS state path. Use 7 entries
	// (a value that's not a coincidence with posts/following counts).
	dsState := filepath.Join(tempDir, ".polis", "ds", "discover.example.com",
		"pub.polis.core", "state")
	if err := os.MkdirAll(dsState, 0755); err != nil {
		t.Fatalf("mkdir ds state: %v", err)
	}
	followersJSON := `[{"d":"a"},{"d":"b"},{"d":"c"},{"d":"d"},{"d":"e"},{"d":"f"},{"d":"g"}]`
	if err := os.WriteFile(filepath.Join(dsState, "pub.polis.follow.json"),
		[]byte(followersJSON), 0644); err != nil {
		t.Fatalf("write followers: %v", err)
	}

	// Following: write 3 entries via the cli-go following helper format.
	followDir := filepath.Join(tempDir, "content", "pub.polis.core", "follow")
	if err := os.MkdirAll(followDir, 0755); err != nil {
		t.Fatalf("mkdir following: %v", err)
	}
	followingJSON := `{"following":[{"url":"https://x.example.com"},{"url":"https://y.example.com"},{"url":"https://z.example.com"}]}`
	if err := os.WriteFile(filepath.Join(followDir, "following.json"),
		[]byte(followingJSON), 0644); err != nil {
		t.Fatalf("write following: %v", err)
	}

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))

	assertContains(t, indexHTML, `class="site-bio"`, "site-bio block present")
	assertContains(t, indexHTML, "Sentinel bio with", "bio renders about.md content")
	assertContains(t, indexHTML, "<em>emphasis</em>", "bio renders markdown to HTML")

	assertContains(t, indexHTML, `class="site-stats"`, "site-stats block present")
	// All three stats are filter-shortcut buttons. "posts" → the
	// type=posts surface; "followers"/"following" → type=profiles
	// with scope=@<this tenant>:network (the union of outbound +
	// inbound follows). 06-profiles Phase 1 had stripped the latter
	// two to plain spans (the grammar wasn't expressive enough yet);
	// Phase 3 re-wires them now that scope=@<handle>:network exists.
	assertContains(t, indexHTML, `<strong>2</strong><button class="site-stat-link" type="button" data-stat-filter-type="posts"`, "posts count = 2")
	assertContains(t, indexHTML, `<strong>7</strong><button class="site-stat-link" type="button" data-stat-filter-type="profiles" data-stat-filter-modifier="by-activity" data-stat-filter-scope="@`, "followers wired as profiles+:network")
	assertContains(t, indexHTML, `<strong>3</strong><button class="site-stat-link" type="button" data-stat-filter-type="profiles" data-stat-filter-modifier="by-activity" data-stat-filter-scope="@`, "following wired as profiles+:network")
}

// TestRenderAll_StreamSiteIdentityBlock_NoBio asserts the rendered output does
// not fail when about.md is absent — the bio block emits empty content
// rather than bringing the render down with a partial-not-found error.
// (The .site-bio:empty CSS rule then collapses the visual block.)
func TestRenderAll_StreamSiteIdentityBlock_NoBio(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)
	writeStreamPost(t, tempDir, "20260315", "alpha", "Alpha", "Body.")

	// No about.md, no DS state, no following.json.

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll should not fail with missing about.md: %v", err)
	}

	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	// Bio container present but empty (no <p>…</p> inside).
	assertContains(t, indexHTML, `<div class="site-bio"></div>`, "site-bio block empty when about.md absent")
	// Counts default to zero. All three stats are filter-shortcut
	// buttons now: posts → type=posts surface, followers/following
	// → type=profiles with scope=@<this tenant>:network (the union
	// of outbound + inbound follows on this tenant).
	assertContains(t, indexHTML, `<strong>1</strong><button class="site-stat-link" type="button" data-stat-filter-type="posts"`, "posts count = 1 (the one we wrote)")
	assertContains(t, indexHTML, `<strong>0</strong><button class="site-stat-link" type="button" data-stat-filter-type="profiles" data-stat-filter-modifier="by-activity" data-stat-filter-scope="@`, "followers wired as profiles+:network shortcut")
	assertContains(t, indexHTML, `>followers</button>`, "followers label preserved")
	assertContains(t, indexHTML, `>following</button>`, "following label preserved")
}

// TestRenderAll_StreamRelativeDataDir asserts that index.html lands at the tenant
// root even when DataDir is a relative path. Regression sentinel for the bug
// surfaced by step-03/3.f's harness against discover.polis.pub: the prior
// renderStreamAll call pre-joined DataDir at the call site, then renderStreamFile
// auto-joined it again, producing index.html at <DataDir>/<DataDir>/index.html
// when DataDir was relative. Tests using t.TempDir() (absolute) masked it.
func TestRenderAll_StreamRelativeDataDir(t *testing.T) {
	// Chdir to t.TempDir so a relative DataDir of "tenant" resolves under it.
	// t.Chdir restores the original cwd at test end (Go 1.24+); fall back to
	// manual restore for older versions if needed.
	root := t.TempDir()
	origCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir(%s): %v", root, err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCWD) })

	relDir := "tenant"
	absDir := filepath.Join(root, relDir)
	if err := os.MkdirAll(absDir, 0755); err != nil {
		t.Fatalf("mkdir tenant: %v", err)
	}
	setupStreamTestSite(t, absDir)
	writeStreamPost(t, absDir, "20260315", "alpha", "Alpha", "Body.")

	// Construct the renderer with the RELATIVE DataDir, mimicking real-world
	// CLI invocation `polis --data-dir tests/harness/clone render`.
	r, err := NewPageRenderer(PageConfig{
		DataDir:        relDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// Index must land at the tenant root, NOT at <DataDir>/<DataDir>/index.html.
	wantIndex := filepath.Join(absDir, "index.html")
	if _, err := os.Stat(wantIndex); err != nil {
		t.Fatalf("index.html should be at tenant root %s; stat err = %v", wantIndex, err)
	}
	// Defensive: the doubled path (tenant/tenant/index.html) must NOT exist.
	doubled := filepath.Join(absDir, relDir, "index.html")
	if _, err := os.Stat(doubled); err == nil {
		t.Errorf("index.html ALSO created at doubled path %s — regression of pre-3.f bug", doubled)
	}
}

// TestRenderAll_StreamControllerInstalled asserts that render emits the
// stream controller at the tenant root so per-post pages'
// `<script src="/stream.js" defer>` resolves with 200 instead of 404, and
// that the installed file is the real controller (not the historical
// placeholder stub).
func TestRenderAll_StreamControllerInstalled(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)
	writeStreamPost(t, tempDir, "20260315", "alpha", "Alpha", "Body.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	jsPath := filepath.Join(tempDir, "stream.js")
	body, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("stream.js not at tenant root: %v", err)
	}
	// Sanity: contains identifying markers of the real controller (not the
	// historical placeholder stub).
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "IntersectionObserver") {
		t.Errorf("stream.js missing IntersectionObserver — controller not installed:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "data-polis-stream-direction") {
		t.Errorf("stream.js missing stream-direction detection hook:\n%s", bodyStr)
	}
}

// TestRenderAll_StreamSinglePost verifies that a tenant with one post renders
// without siblings but still emits a valid index + post page.
func TestRenderAll_StreamSinglePost(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	writeStreamPost(t, tempDir, "20260325", "solo", "Solo", "Only post.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	assertContains(t, indexHTML, "Solo", "index focus")
	// Stream container always present even with zero siblings — step-04/4.a
	// unified entry-grammar contract: `.stream` wraps the focus + sibling
	// entries. Pre-4.a this assertion checked for `.stream-siblings`, which
	// was the separate sibling section; that's now collapsed into `.stream`.
	assertContains(t, indexHTML, `class="stream"`, "stream container present")
	// And the focus entry itself uses the unified .entry shape. R27
	// (fix(stream): R27 rapid-fire, commit 1c076b4) added `entry--commentable`
	// to the focus class list when the timeline-dot replaced the right-side
	// .entry-comments-badge as the add-comment affordance.
	assertContains(t, indexHTML, "entry entry--post entry--commentable is-focused", "focus entry uses unified grammar")
	// Cache-bust: controller URL carries the active StreamShapeVersion as ?v=
	// so a shape bump invalidates browser cache of the previous /stream.js
	// (step-04/4.g, follow-up tracker option a).
	wantCachebust := `<script src="/stream.js?v=` + bundle.StreamShapeVersion + `" defer></script>`
	assertContains(t, indexHTML, wantCachebust, "controller URL cache-bust query param")
}

// TestRenderAll_StreamFocusTitleRendered locks the focus title fix: the SSR'd
// focus entry must include an explicit <h1 class="focus-title"> element
// sourced from frontmatter `title`. Previously the focus markup had no
// title element at all — it inherited v3's "title comes from a leading
// `# Heading` in the markdown body" convention, so posts authored with
// the title only in frontmatter rendered with no visible title. Siblings
// always show their {{title}}, so the focus was the only entry on the
// page with no heading.
func TestRenderAll_StreamFocusTitleRendered(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	// Author the post body WITHOUT a leading `# Heading` — title lives in
	// frontmatter only. Pre-fix this combination silently dropped the title.
	writeStreamPost(t, tempDir, "20260429", "no-leading-heading", "My Post Title", "Just a body paragraph, no heading on top.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	postHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260429", "no-leading-heading.html"))
	// Post-2026-04-29: focus title wraps in <a class="entry-title-link">
	// pointing at focus_path_url, with the inner h1 carrying .entry-title
	// (the .focus-title class was retired since its styles were identical
	// to .entry-title). Click on the title navigates to the canonical
	// URL, same as siblings.
	// Trailing space inside the class attribute is the empty-substitution
	// form of {{title_link_state}} — appended uniformly even when no
	// extra state class is set, since templating is plain string sub.
	wantTitleLink := `<a class="entry-title-link " href="/posts/20260429/no-leading-heading.html">`
	wantTitleH1 := `<h1 class="entry-title">My Post Title</h1>`
	if !strings.Contains(postHTML, wantTitleLink) {
		t.Errorf("focus title missing entry-title-link wrapper: want %q in rendered HTML", wantTitleLink)
	}
	if !strings.Contains(postHTML, wantTitleH1) {
		t.Errorf("focus title missing h1.entry-title: want %q in rendered HTML", wantTitleH1)
	}

	// Same for the homepage (focus = newest post).
	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	if !strings.Contains(indexHTML, wantTitleLink) {
		t.Errorf("homepage focus missing entry-title-link wrapper: want %q", wantTitleLink)
	}
	if !strings.Contains(indexHTML, wantTitleH1) {
		t.Errorf("homepage focus missing h1.entry-title: want %q", wantTitleH1)
	}
}

// TestRenderAll_StreamLayoutRightStateClass locks the .show-top-fade
// state class on per-post pages at SSR time and its absence on
// /index.html. The class is the SSR-time first-paint hint for the
// stream-top-fade overlay; JS-side updateTopFadeVisibility takes
// over from init() onward (toggles based on scroll geometry +
// paginationDoneTop). Without the SSR class on per-post pages, the
// fade would briefly flicker absent before JS runs.
func TestRenderAll_StreamLayoutRightStateClass(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	writeStreamPost(t, tempDir, "20260315", "alpha", "Alpha", "First.")
	writeStreamPost(t, tempDir, "20260320", "bravo", "Bravo", "Second.")
	writeStreamPost(t, tempDir, "20260325", "charlie", "Charlie", "Third.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// /index.html: focus = newest post (Charlie), no above-focus rail.
	// .layout-right should NOT carry .show-top-fade at SSR time —
	// JS adds it once the user scrolls down past the top.
	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	if strings.Contains(indexHTML, `class="layout-right show-top-fade"`) {
		t.Errorf("index.html should NOT carry .show-top-fade at SSR (JS-only post-scroll)")
	}
	assertContains(t, indexHTML, `class="layout-right"`, "index.html: bare layout-right class")

	// Per-post page (alpha = oldest of 3): pickStreamSiblings produces an
	// above-focus rail (Charlie + Bravo as nearest-newer fill).
	// .layout-right SHOULD carry .show-top-fade for first-paint
	// correctness — JS would toggle it off only when scroll-top with
	// no more upward pagination.
	alphaHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260315", "alpha.html"))
	assertContains(t, alphaHTML, `class="layout-right show-top-fade"`, "per-post page: show-top-fade")

	// Stream-top-fade element renders unconditionally; CSS gates its
	// visibility via the state class on .layout-right. Both pages
	// should contain the element (no per-page conditional in the
	// template — keeps the engine's section dispatch simple).
	assertContains(t, indexHTML, `class="stream-top-fade"`, "index.html: stream-top-fade present")
	assertContains(t, alphaHTML, `class="stream-top-fade"`, "per-post page: stream-top-fade present")
}

// TestRenderAll_StreamHomePathAbsolute locks the v4-specific HomePath fix:
// every {{home_path}} substitution must be the root-relative absolute
// "/index.html" (not a relative "../../index.html"). The stream
// controller calls history.pushState/replaceState on every scroll-driven
// focus change, which rewrites document.baseURI; browsers re-resolve
// relative <a href> against baseURI at click/hover time, so once a
// /posts/YYYYMMDD/foo.html URL has been stamped the relative path
// resolves to nonsense like /posts/20260429/index.html. Same hazard
// the favicon dodges; the assertion below covers the SSR'd home_path
// consumers (.site-avatar-link, .site-identity-text) on a deeply-nested
// per-post page. The wordmark dropped out of this set when it was
// re-pointed at https://polis.pub/ — covered separately below.
func TestRenderAll_StreamHomePathAbsolute(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	writeStreamPost(t, tempDir, "20260429", "deep", "Deep", "Two-dirs-deep post.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// Per-post page (depth 2 from site root): home_path must NOT be
	// "../../index.html" (the natural relative form) — that's the bug.
	// Must be "/index.html" (absolute root-relative), so it survives
	// pushState-driven baseURI changes from the stream controller.
	postHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260429", "deep.html"))
	if strings.Contains(postHTML, `href="../../index.html"`) {
		t.Errorf("post page has relative home_path \"../../index.html\" — would resolve wrong after pushState; expected absolute \"/index.html\"")
	}
	// The SSR'd home_path consumers each expect href="/index.html".
	wantHomeRefs := []string{
		`<a href="/index.html" class="site-avatar-link">`,
		`<a href="/index.html" class="site-identity-text">`,
	}
	for _, want := range wantHomeRefs {
		if !strings.Contains(postHTML, want) {
			t.Errorf("post page missing absolute home_path consumer: %s", want)
		}
	}
	// Wordmark points at polis.pub (the platform brand), not the local site
	// home — the avatar link already covers the "go home" affordance.
	if !strings.Contains(postHTML, `<a class="polis-wordmark" href="https://polis.pub/">`) {
		t.Errorf("post page missing wordmark → polis.pub link")
	}

	// Index (depth 0) — same absolute path so the markup stays uniform
	// across all v4 pages and the same assertion catches future drift.
	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	for _, want := range wantHomeRefs {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("index page missing absolute home_path consumer: %s", want)
		}
	}
	if !strings.Contains(indexHTML, `<a class="polis-wordmark" href="https://polis.pub/">`) {
		t.Errorf("index page missing wordmark → polis.pub link")
	}
}

// TestRenderAll_StreamCanonical asserts D-CANONICAL: per-post pages self-canonical
// to their own absolute .html URL; /index.html canonical-points at the
// currently-newest post's absolute URL.
func TestRenderAll_StreamCanonical(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	writeStreamPost(t, tempDir, "20260315", "alpha", "Alpha", "First.")
	writeStreamPost(t, tempDir, "20260320", "bravo", "Bravo", "Second.")
	writeStreamPost(t, tempDir, "20260325", "charlie", "Charlie", "Third.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// Per-post page: alpha's HTML self-canonicals to alpha's absolute URL.
	alphaHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260315", "alpha.html"))
	wantAlpha := `<link rel="canonical" href="https://example.com/posts/20260315/alpha.html">`
	if !strings.Contains(alphaHTML, wantAlpha) {
		t.Errorf("alpha self-canonical missing.\nWant: %s", wantAlpha)
	}
	// Sanity: alpha must not reference any other post's URL as canonical.
	if strings.Contains(alphaHTML, `<link rel="canonical" href="https://example.com/posts/20260325/charlie.html">`) {
		t.Errorf("alpha canonical incorrectly points at charlie")
	}

	// Index: canonical points at newest post (charlie).
	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	wantIndex := `<link rel="canonical" href="https://example.com/posts/20260325/charlie.html">`
	if !strings.Contains(indexHTML, wantIndex) {
		t.Errorf("index canonical-to-newest missing.\nWant: %s", wantIndex)
	}

	// No legacy `.md` canonical leaking into output.
	if strings.Contains(alphaHTML, `.md"`) && strings.Contains(alphaHTML, `rel="canonical" href="https://example.com/content/`) {
		t.Errorf("legacy .md canonical leaked into per-post HTML")
	}
}

// TestRenderAll_StreamEmptyCorpusCanonical asserts that the empty-state index has
// a self-canonical link to the tenant root index (worker decision flagged in
// step-03 Execution Report 3.b specific section).
func TestRenderAll_StreamEmptyCorpusCanonical(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	wantCanonical := `<link rel="canonical" href="https://example.com/index.html">`
	if !strings.Contains(indexHTML, wantCanonical) {
		t.Errorf("empty-state index canonical missing.\nWant: %s", wantCanonical)
	}
}

// TestRenderAll_StreamEmptyCorpus verifies renderStreamEmptyIndex kicks in when there
// are no posts at all — the tenant page still resolves to something.
func TestRenderAll_StreamEmptyCorpus(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	stats, err := r.RenderAll(true)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if stats.PostsRendered != 0 {
		t.Errorf("empty corpus should render 0 posts, got %d", stats.PostsRendered)
	}
	if !stats.IndexGenerated {
		t.Error("empty corpus should still generate index")
	}

	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	assertContains(t, indexHTML, "No posts yet", "empty-state message")
	assertContains(t, indexHTML, `data-polis-focus="true"`, "empty-state demarcation")
}

// setupStreamTestSite installs the bundle fixture, sets active_shape=v4 and
// active_theme=studio13 (stream-only), and writes a minimal .well-known/polis.
func setupStreamTestSite(t *testing.T, dir string) {
	t.Helper()

	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("install reference payload: %v", err)
	}
	if err := bundle.SetActiveShapeName(dir, "v4"); err != nil {
		t.Fatalf("set active shape v4: %v", err)
	}
	if err := bundle.SetActiveThemeName(dir, "studio13"); err != nil {
		t.Fatalf("set active theme studio13: %v", err)
	}

	wk := filepath.Join(dir, ".well-known")
	if err := os.MkdirAll(wk, 0755); err != nil {
		t.Fatalf("mkdir well-known: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wk, "polis"), []byte(`{
  "base_url": "https://example.com",
  "site_title": "V4 Test",
  "author_name": "V4 Author"
}`), 0644); err != nil {
		t.Fatalf("write well-known/polis: %v", err)
	}

	// Empty index.jsonl to start — tests append via AppendToPublicIndex.
	contentDir := filepath.Join(dir, "content", "pub.polis.core")
	if err := os.MkdirAll(contentDir, 0755); err != nil {
		t.Fatalf("mkdir content: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(""), 0644); err != nil {
		t.Fatalf("write index.jsonl: %v", err)
	}
}

// writeStreamPost creates a post .md file and adds an entry to the public index
// so loadPublicIndex returns it in the expected order.
func writeStreamPost(t *testing.T, dir, date, slug, title, body string) {
	t.Helper()
	published := date[:4] + "-" + date[4:6] + "-" + date[6:8] + "T12:00:00Z"
	writeStreamPostAt(t, dir, date, slug, title, published, body)
}

// writeStreamPostAt is writeStreamPost with an explicit ISO 8601 published
// timestamp, so tests asserting against the human-readable date+time
// format can pin both the date AND the time of day.
func writeStreamPostAt(t *testing.T, dir, date, slug, title, published, body string) {
	t.Helper()
	postDir := filepath.Join(dir, "content", "pub.polis.core", "post", date)
	if err := os.MkdirAll(postDir, 0755); err != nil {
		t.Fatalf("mkdir post: %v", err)
	}
	md := "---\ntitle: " + title + "\npublished: " + published + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(postDir, slug+".md"), []byte(md), 0644); err != nil {
		t.Fatalf("write post md: %v", err)
	}
	entry := &metadata.IndexEntry{
		Type:      "post",
		Path:      "content/pub.polis.core/post/" + date + "/" + slug + ".md",
		Title:     title,
		Published: published,
	}
	if err := metadata.AppendToPublicIndex(dir, entry); err != nil {
		t.Fatalf("append index entry: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertContains(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s: expected output to contain %q", label, needle)
	}
}

// TestStripHTMLComments locks in the comment-stripping behavior. The
// shape templates (stream.html / stream-post.html) embed extensive
// developer-doc HTML comments (DOM contract, protocol-marker
// explanations, design rationales). These are useful when reading the
// source but pure waste at runtime — on a typical rendered per-post
// page they account for ~70% of the uncompressed payload. The strip
// runs at render time before the file hits disk; this test covers the
// behavior in isolation.
func TestStripHTMLComments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no comments unchanged", `<p>Hello</p>`, `<p>Hello</p>`},
		{"single-line comment", `<p>a</p><!-- note --><p>b</p>`, `<p>a</p><p>b</p>`},
		{"multi-line comment", "<p>a</p>\n<!--\n  multi\n  line\n-->\n<p>b</p>", "<p>a</p>\n\n<p>b</p>"},
		{"adjacent comments", `<!--x--><!--y--><p>z</p>`, `<p>z</p>`},
		{"comment with HTML inside", `<p>a</p><!-- <b>not real</b> --><p>b</p>`, `<p>a</p><p>b</p>`},
		{"escaped comment in body content stays", `<p>&lt;!-- not a real comment --&gt;</p>`, `<p>&lt;!-- not a real comment --&gt;</p>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripHTMLComments(tc.in)
			if got != tc.want {
				t.Errorf("StripHTMLComments(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRenderedHTMLHasNoComments asserts that comment-stripping is wired
// into the v4 stream-shape render path. Without the strip, dev-doc
// comments from stream.html / stream-post.html ride along into the
// rendered per-post page and account for ~70% of the uncompressed
// payload that the lazy-fetch flow transfers per scrolled-into-view
// entry.
func TestRenderedHTMLHasNoComments(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)
	writeStreamPostAt(t, tempDir, "20260506", "p1", "First post", "2026-05-06T12:00:00Z", "Body of the first post.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// Both the per-post page AND the index should be comment-free.
	for _, rel := range []string{"posts/20260506/p1.html", "index.html"} {
		out := mustReadFile(t, filepath.Join(tempDir, rel))
		if strings.Contains(out, "<!--") {
			// Find the first one and print surrounding context for debugging.
			idx := strings.Index(out, "<!--")
			start := idx
			end := idx + 200
			if end > len(out) {
				end = len(out)
			}
			t.Errorf("%s: rendered HTML still contains <!-- ... -->. Surrounding:\n%s", rel, out[start:end])
		}
	}
}

func TestStripLeadingTitleHeading(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		title string
		want  string
	}{
		{
			name:  "atx h1 matches title — strip line + trailing blank",
			body:  "# Hello World\n\nFirst paragraph.\n",
			title: "Hello World",
			want:  "First paragraph.\n",
		},
		{
			name:  "atx h2 matches title (case-insensitive)",
			body:  "## hello world\n\nBody.\n",
			title: "Hello World",
			want:  "Body.\n",
		},
		{
			name:  "leading whitespace before heading still strips",
			body:  "\n\n  # Hello World  \n\nBody.\n",
			title: "Hello World",
			want:  "Body.\n",
		},
		{
			name:  "heading text differs from title — keep",
			body:  "# Different Heading\n\nBody.\n",
			title: "Hello World",
			want:  "# Different Heading\n\nBody.\n",
		},
		{
			name:  "no leading heading — keep",
			body:  "Hello World, this is the post.\n",
			title: "Hello World",
			want:  "Hello World, this is the post.\n",
		},
		{
			name:  "empty title — no-op",
			body:  "# Anything\n",
			title: "",
			want:  "# Anything\n",
		},
		{
			name:  "no trailing blank line after heading",
			body:  "# Hello World\nFirst paragraph.\n",
			title: "Hello World",
			want:  "First paragraph.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripLeadingTitleHeading(tc.body, tc.title)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTitleStartsFirstSentence(t *testing.T) {
	cases := []struct {
		name  string
		title string
		body  string
		want  bool
	}{
		{
			name:  "title is exact start of first paragraph",
			title: "Hello World",
			body:  "Hello World, this is the post.\n",
			want:  true,
		},
		{
			name:  "title differs from leading prose",
			title: "An Unrelated Title",
			body:  "Hello World, this is the post.\n",
			want:  false,
		},
		{
			name: "title is auto-truncation of first sentence (discover.polis.pub case)",
			// Title gets cut off mid-word; body has the full sentence.
			title: "What if the blessing system supported delegation? \"I trust A",
			body:  "What if the blessing system supported delegation? \"I trust Alice's judgment.\"\n",
			want:  true,
		},
		{
			name: "curly quotes in body, straight quotes in title — still matches",
			// Frontmatter title typically stores straight quotes; markdown
			// renders curly. Normalization folds them.
			title: "She said \"hello\"",
			body:  "She said “hello” to the room.\n",
			want:  true,
		},
		{
			name: "smart apostrophe in body",
			title: "It's a long story",
			body:  "It’s a long story but I’ll keep it short.\n",
			want:  true,
		},
		{
			name:  "leading heading skipped, then prose checked",
			title: "About things",
			body:  "# Some Other Heading\n\nAbout things and stuff.\n",
			want:  true,
		},
		{
			name:  "empty title — no-op",
			title: "",
			body:  "Anything goes.\n",
			want:  false,
		},
		{
			name:  "empty body — no first line",
			title: "Hello",
			body:  "",
			want:  false,
		},
		{
			name:  "title truncated with literal ellipsis (Apache HTTPD case)",
			title: "Apache HTTPD was released in 1995. Nginx in 2004. Any web se...",
			body:  "Apache HTTPD was released in 1995. Nginx in 2004. Any web server can host a static markdown site.\n",
			want:  true,
		},
		{
			name:  "title truncated with em-dash mid-word (Restoration case)",
			title: "Restoration, not nostalgia. We're not longing for the past—",
			body:  "Restoration, not nostalgia. We're not longing for the past—we're using new technology to do what was once possible only with the open web.\n",
			want:  true,
		},
		{
			name:  "title trailing dots stripped does not over-match unrelated body",
			// titleNorm post-trim = "hello world"; body = "hello mars" — must not match.
			title: "Hello World...",
			body:  "Hello Mars, this is a different post.\n",
			want:  false,
		},
		{
			// Frontmatter parsers that don't unescape YAML double-quoted
			// strings leave the title with literal `\"` where the source
			// had `\"X\"`. The body markdown has the corresponding
			// straight-quoted text. normalizeForTitleCompare strips
			// backslashes so these still match.
			name:  "escaped quotes in title still match straight quotes in body",
			title: `\"Just use RSS\" works for consumption. It doesn't solve ident`,
			body:  "\"Just use RSS\" works for consumption. It doesn't solve identity, signing, or two-way interaction.\n",
			want:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TitleStartsFirstSentence(tc.title, tc.body)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestYAMLUnquote covers the small subset of YAML quoting rules that
// parseFrontmatter + FetchReplyContextFull rely on. The bug fixed
// alongside this test (literal-backslash titles like `\"Just use RSS\"`
// from auto-derived titles surrounding an opening quoted phrase) was
// invisible until the comment-thread title-dedup couldn't match the
// rendered straight-quote body.
func TestYAMLUnquote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"double-quoted with escaped inner quotes",
			`"\"Just use RSS\" works for consumption"`,
			`"Just use RSS" works for consumption`},
		{"double-quoted with escaped backslash",
			`"path\\to\\file"`, `path\to\file`},
		{"single-quoted with doubled apostrophe",
			`'it''s a test'`, `it's a test`},
		{"plain unquoted passes through",
			`hello world`, `hello world`},
		{"empty string", ``, ``},
		{"single char no-op", `a`, `a`},
		{"unmatched quotes pass through",
			`"unterminated`, `"unterminated`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := yamlUnquote(tc.in)
			if got != tc.want {
				t.Errorf("yamlUnquote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRenderAll_StreamSiblingTitleRedundancy locks in the title-redundancy
// fix for SSR'd siblings. Discover.polis.pub-style posts where the
// frontmatter title is auto-derived as a truncation of the body's
// first sentence rendered with the explicit <h1>/<h3> chrome AS a
// duplicate of the body's leading prose. The focus path correctly
// tagged the wrapper with .is-redundant, but the sibling path didn't
// — both should.
func TestRenderAll_StreamSiblingTitleRedundancy(t *testing.T) {
	tempDir := t.TempDir()
	setupStreamTestSite(t, tempDir)

	// Three posts with titles that are auto-truncated prefixes of the
	// body's first sentence — exactly the discover.polis.pub case.
	writeStreamPostAt(t, tempDir, "20260502", "apache",
		"Apache HTTPD was released in 1995. Nginx in 2004. Any web se",
		"2026-05-02T20:00:00Z",
		"Apache HTTPD was released in 1995. Nginx in 2004. Any web server that can serve static files can host a polis site. We don't require a special runtime because the existing ones are fine.")
	writeStreamPostAt(t, tempDir, "20260502", "bash-cli",
		"The bash CLI's CHANGELOG is 1000+ lines of history. Each ent",
		"2026-05-02T18:00:00Z",
		"The bash CLI's CHANGELOG is 1000+ lines of history. Each entry tells a tiny story about a bug found, a feature built, or a decision made.")
	writeStreamPostAt(t, tempDir, "20260502", "newest",
		"Newest post used as focus on the homepage",
		"2026-05-02T22:00:00Z",
		"Newest post used as focus on the homepage — body that does NOT match the title prefix at all, so its own title-link should NOT be flagged redundant.")

	r, err := NewPageRenderer(PageConfig{
		DataDir:        tempDir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// /index.html: focus=newest (should NOT be redundant — title
	// doesn't prefix-match body); siblings = apache, bash-cli (both
	// should be redundant).
	indexHTML := mustReadFile(t, filepath.Join(tempDir, "index.html"))
	t.Logf("index.html length=%d", len(indexHTML))

	// Apache as sibling — must carry .is-redundant.
	apacheSiblingMarker := `<a class="entry-title-link is-redundant" href="/posts/20260502/apache.html">`
	if !strings.Contains(indexHTML, apacheSiblingMarker) {
		// Print the relevant section for debugging
		idx := strings.Index(indexHTML, "/posts/20260502/apache.html")
		if idx > 100 {
			t.Errorf("apache as sibling missing .is-redundant on title-link wrapper. Surrounding HTML:\n%s",
				indexHTML[idx-200:min(idx+200, len(indexHTML))])
		} else {
			t.Errorf("apache as sibling: indexHTML doesn't contain expected marker %q", apacheSiblingMarker)
		}
	}

	// bash-cli as sibling — also must carry .is-redundant.
	bashSiblingMarker := `<a class="entry-title-link is-redundant" href="/posts/20260502/bash-cli.html">`
	if !strings.Contains(indexHTML, bashSiblingMarker) {
		t.Errorf("bash-cli as sibling missing .is-redundant on title-link wrapper")
	}

	// Now check apache's per-post page: apache should be focus +
	// is-redundant (already established by other tests, but sanity
	// check here too).
	apacheHTML := mustReadFile(t, filepath.Join(tempDir, "posts", "20260502", "apache.html"))
	apacheFocusMarker := `<a class="entry-title-link is-redundant" href="/posts/20260502/apache.html">`
	if !strings.Contains(apacheHTML, apacheFocusMarker) {
		t.Errorf("apache as focus missing .is-redundant — helper function broken in focus path too")
	}
}
