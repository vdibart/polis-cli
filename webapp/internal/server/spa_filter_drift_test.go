package server

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// TestSPAFilterWidget_NoDriftFromBundleSnippet asserts the SPA's inlined
// copy of the v4 stream filter widget (in webapp/internal/webui/www/
// index.html) stays in sync with the canonical snippet at
// cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/snippets/
// stream-filter.html.
//
// Step-06/6.c review concern: the SPA inlines the filter markup because
// index.html isn't bundle-templated. Without this test, either side
// could add/rename a slot and the two surfaces would silently diverge.
//
// Scope of the comparison: the SET of data-filter-slot values present
// in each file's .sentence-filter block. Both must contain the same
// slot grammar (qualifier, type, scope, modifier, site-typeahead).
//
// What it deliberately does NOT cover (per resolved-decisions table —
// these are intentional divergence points):
//   - sf-slot--interactive vs sf-slot--locked vs sf-slot--identity
//     class variants. Public surface locks qualifier + scope; owner
//     SPA unlocks them. Same data-filter-slot value, different class.
//   - display text inside slots ("posts" vs "by date" vs site_domain
//     substitution). The bundle template renders identity text via
//     {{site_domain}}; the SPA sets the scope label at runtime via
//     setFilterScope.
//   - Element ordering. Both surfaces enforce the slot order via
//     positioning rules; ordering drift is a CSS concern, not a
//     markup-contract concern.
func TestSPAFilterWidget_NoDriftFromBundleSnippet(t *testing.T) {
	spaIndex, err := fs.ReadFile(webui.Assets, "www/index.html")
	if err != nil {
		t.Fatalf("read SPA index.html: %v", err)
	}
	bundleSnippet, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/snippets/stream-filter.html")
	if err != nil {
		t.Fatalf("read bundle stream-filter.html: %v", err)
	}

	spaSlots := extractFilterSlotNames(string(spaIndex))
	snippetSlots := extractFilterSlotNames(string(bundleSnippet))

	if len(spaSlots) == 0 {
		t.Fatal("SPA index.html has no .sentence-filter slots — markup may have been removed")
	}
	if len(snippetSlots) == 0 {
		t.Fatal("bundle snippet has no .sentence-filter slots — file may have been deleted")
	}

	if !equalStringSlices(spaSlots, snippetSlots) {
		t.Errorf("SPA filter widget drifted from bundle snippet.\nSPA slots:    %v\nBundle slots: %v\nFix: keep webapp/internal/webui/www/index.html in sync with cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/snippets/stream-filter.html (.sentence-filter block grammar).",
			spaSlots, snippetSlots)
	}
}

// extractFilterSlotNames returns a sorted, de-duped list of
// data-filter-slot values present inside the .sentence-filter block.
func extractFilterSlotNames(markup string) []string {
	startIdx := strings.Index(markup, `class="sentence-filter"`)
	if startIdx == -1 {
		return nil
	}
	endIdx := strings.Index(markup[startIdx:], `</div>`)
	if endIdx == -1 {
		return nil
	}
	region := markup[startIdx : startIdx+endIdx]

	slotRe := regexp.MustCompile(`data-filter-slot="([^"]+)"`)
	seen := map[string]bool{}
	for _, m := range slotRe.FindAllStringSubmatch(region, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStringSlices(a, b []string) bool {
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

// TestSPA_OwnerExtrasLoaded asserts owner-extras.js is shipped in the
// SPA bundle AND referenced from index.html. step-06/6.d guarantees
// the owner SPA loads owner-extras alongside the v4 stream controller;
// public per-post artifacts must NOT load owner-extras (they don't go
// through this asset bundle).
//
// Catches regressions like: owner-extras.js renamed/moved without the
// <script> tag updating, or the script tag dropped from index.html
// during a refactor.
func TestSPA_OwnerExtrasLoaded(t *testing.T) {
	// Owner-extras file exists in the SPA bundle.
	contents, err := fs.ReadFile(webui.Assets, "www/owner-extras.js")
	if err != nil {
		t.Fatalf("read owner-extras.js from SPA bundle: %v", err)
	}
	if len(contents) < 100 {
		t.Errorf("owner-extras.js too small (%d bytes); expected real content", len(contents))
	}
	if !strings.Contains(string(contents), "PolisOwnerExtras") {
		t.Errorf("owner-extras.js missing PolisOwnerExtras namespace")
	}
	if !strings.Contains(string(contents), "waitForController") {
		t.Errorf("owner-extras.js missing waitForController readiness check")
	}

	// index.html references the script.
	indexBytes, err := fs.ReadFile(webui.Assets, "www/index.html")
	if err != nil {
		t.Fatalf("read SPA index.html: %v", err)
	}
	indexStr := string(indexBytes)
	if !strings.Contains(indexStr, `src="/owner-extras.js"`) {
		t.Errorf("index.html missing <script src=\"/owner-extras.js\"> tag")
	}
	// Owner-extras must load AFTER stream.js (it references window.PolisStream).
	streamIdx := strings.Index(indexStr, `pub.polis.core/shapes/v4/stream.js`)
	extrasIdx := strings.Index(indexStr, `src="/owner-extras.js"`)
	if streamIdx == -1 || extrasIdx == -1 {
		t.Fatal("could not locate stream.js / owner-extras.js script tags for ordering check")
	}
	if extrasIdx < streamIdx {
		t.Errorf("owner-extras.js <script> tag appears before stream.js — owner-extras references window.PolisStream and must load after the controller")
	}
}

// TestSPA_OwnerExtrasIconPresets asserts owner-extras.js wires every
// icon-row preset (gateway/paragraph/comment/people/envelope) and that
// each preset's filter values match the spec in the plan's Icon
// presets table. Catches drift between plan + implementation.
//
// The 'edit' icon is intentionally excluded — it stays on legacy
// onclick (App.navigateTo('/posts/new')) until 6.h replaces it with
// the inline editor trigger.
func TestSPA_OwnerExtrasIconPresets(t *testing.T) {
	contents, err := fs.ReadFile(webui.Assets, "www/owner-extras.js")
	if err != nil {
		t.Fatalf("read owner-extras.js: %v", err)
	}
	src := string(contents)

	// Every preset spec from plan §"Icon presets (sentence filters)"
	// must show up in ICON_PRESETS. Each tuple: (preset key, expected
	// substring to find inside the preset's block).
	required := []struct {
		preset, mustContain string
	}{
		// R22 #12: qualifier locked to 'all' across all presets;
		// the 'new' option was dropped from the dropdown entirely.
		{"gateway", "qualifier: 'all'"},
		{"gateway", "type: 'activity'"},
		{"gateway", "scope: 'my-network'"},
		{"paragraph", "type: 'posts'"},
		{"paragraph", "scope: 'me'"},
		{"comment", "type: 'comments'"},
		{"comment", "scope: 'all-polis'"},
		{"comment", "modifier: 'to-bless'"},
		// 06-profiles: people icon rewires from type=follows to
		// type=profiles. Default scope is my-network, default modifier
		// is by-name (matches the new third-clause grammar).
		{"people", "type: 'profiles'"},
		{"people", "scope: 'my-network'"},
		{"people", "modifier: 'by-name'"},
		{"envelope", "type: 'dms'"},
		// R24 follow-up #4: dms locked to scope=my-mutuals (DMs are
		// between mutuals; drafts is the only type that locks to
		// scope=me). Preset matches the personal-lock target so
		// "all messages from my mutuals by date" stays self-consistent.
		{"envelope", "scope: 'my-mutuals'"},
	}
	for _, tc := range required {
		// Find the preset block, then verify mustContain appears within it.
		blockStart := strings.Index(src, tc.preset+": {")
		if blockStart == -1 {
			t.Errorf("preset key %q not found in ICON_PRESETS", tc.preset)
			continue
		}
		// Block ends at the next "}," at the top level — close enough
		// for this kind of substring check.
		blockEnd := strings.Index(src[blockStart:], "},")
		if blockEnd == -1 {
			t.Errorf("preset block for %q has no closing }", tc.preset)
			continue
		}
		block := src[blockStart : blockStart+blockEnd]
		if !strings.Contains(block, tc.mustContain) {
			t.Errorf("preset %q missing %q (block: %s)", tc.preset, tc.mustContain, block)
		}
	}

	// emitPresetLoadedEvent must POST to /api/v1/event (the allowlist-
	// gated endpoint added in 6.e). Catches refactors that break the
	// telemetry path silently.
	if !strings.Contains(src, "/api/v1/event") {
		t.Errorf("owner-extras.js missing /api/v1/event POST — telemetry path broken")
	}
	if !strings.Contains(src, "pub.polis.stream.preset_loaded") {
		t.Errorf("owner-extras.js missing pub.polis.stream.preset_loaded event name — telemetry would emit unknown event")
	}
}

// TestSPA_OwnerExtrasActivityMode covers the post-R20-5 activity wiring:
// activity is now a heterogenous union of natural per-type renderings.
// Owner-extras must still toggle body.is-activity-mode on filter
// changes (so the Layer-2 CSS body-cap can fire), but the projection
// layer (afterRender hooks, notification-rule consumption,
// ICON_GLYPHS) is intentionally dormant — its plumbing stays in
// owner-extras (and stream.css) so the projection model can be
// revived later without re-wiring from scratch.
func TestSPA_OwnerExtrasActivityMode(t *testing.T) {
	contents, err := fs.ReadFile(webui.Assets, "www/owner-extras.js")
	if err != nil {
		t.Fatalf("read owner-extras.js: %v", err)
	}
	src := string(contents)

	if !strings.Contains(src, "registerActivityDecorators") {
		t.Errorf("owner-extras missing registerActivityDecorators — body.is-activity-mode toggle not wired")
	}
	if !strings.Contains(src, "syncActivityModeClass") {
		t.Errorf("owner-extras missing syncActivityModeClass — body.is-activity-mode toggle helper missing")
	}
	if !strings.Contains(src, "filterType === 'activity'") {
		t.Errorf("syncActivityModeClass must check filterType === 'activity' to gate Layer-2 CSS")
	}
	if !strings.Contains(src, "stream.onFilterChange") {
		t.Errorf("owner-extras must subscribe via stream.onFilterChange — dropdown filter changes won't sync activity mode otherwise")
	}
	// Plumbing-stays-in-place assertions — the projection model is
	// dormant but its scaffolding must remain so a future reactivation
	// doesn't need to reinvent these pieces. If you intentionally rip
	// any of these out, drop the corresponding assertion in the same
	// commit so the contract change is visible in the diff.
	if !strings.Contains(src, "/api/v1/bundle/notification-rules") {
		t.Errorf("owner-extras dropped the notification-rules fetch — keep the plumbing for future projection mode")
	}
	if !strings.Contains(src, "ICON_GLYPHS") {
		t.Errorf("owner-extras dropped ICON_GLYPHS — keep the plumbing for future projection mode")
	}
}

// TestSPA_StreamControllerFilterChangeHook — step-06/6.f.2 review
// concern #1. stream.js must expose the onFilterChange registry AND
// fire it from every filter-mutating call site (selectOption + the
// four setFilter* setters + setFilter batch).
func TestSPA_StreamControllerFilterChangeHook(t *testing.T) {
	jsBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.js")
	if err != nil {
		t.Fatalf("read stream.js: %v", err)
	}
	src := string(jsBytes)

	if !strings.Contains(src, "addFilterChangeListener") {
		t.Errorf("stream.js missing addFilterChangeListener — onFilterChange hook not wired")
	}
	if !strings.Contains(src, "onFilterChange: addFilterChangeListener") {
		t.Errorf("stream.js doesn't expose onFilterChange on PolisStream — owner-extras can't subscribe")
	}
	// fireFilterChange must be called from every filter-mutating call
	// site. Spot-check via callsite count: the function exists once
	// (definition) plus a call from selectOption + 4 setters + setFilter
	// batch = at least 6 occurrences.
	count := strings.Count(src, "fireFilterChange()")
	if count < 6 {
		t.Errorf("expected fireFilterChange() called from at least 6 mutation sites (selectOption + 4 setters + setFilter batch); found %d", count)
	}
}

// TestSPA_StreamCSSHasActivityMode asserts the v4 stream.css ships
// the body.is-activity-mode rules that compact entries in activity
// view. Catches accidental removal of the Layer-2 gate.
func TestSPA_StreamCSSHasActivityMode(t *testing.T) {
	cssBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.css")
	if err != nil {
		t.Fatalf("read stream.css: %v", err)
	}
	src := string(cssBytes)
	if !strings.Contains(src, "body.is-activity-mode") {
		t.Errorf("stream.css missing body.is-activity-mode gate — activity render mode won't compact entries")
	}
	if !strings.Contains(src, ".activity-signal") {
		t.Errorf("stream.css missing .activity-signal style — afterRender's appended element won't render with intended layout")
	}
}

// TestSPA_OwnerExtrasInlineEditor — step-06/6.h. Asserts owner-extras
// has the editor mount/teardown wiring + edit-icon click handler +
// tiered Esc + Ctrl+Shift+F focus-mode handler.
func TestSPA_OwnerExtrasInlineEditor(t *testing.T) {
	contents, err := fs.ReadFile(webui.Assets, "www/owner-extras.js")
	if err != nil {
		t.Fatalf("read owner-extras.js: %v", err)
	}
	src := string(contents)

	// Card lifecycle.
	if !strings.Contains(src, "function openEditor") || !strings.Contains(src, "function closeEditor") {
		t.Errorf("owner-extras missing openEditor / closeEditor — editor card lifecycle not wired")
	}
	// Editor entry must carry the pinned + entry-type sentinels so
	// stream.js's filter-change clear logic preserves it.
	if !strings.Contains(src, `data-polis-pinned`) || !strings.Contains(src, `'editor'`) {
		t.Errorf("editor card missing data-polis-pinned='editor' marker — filter-sticky behavior won't engage")
	}
	if !strings.Contains(src, `data-polis-entry-type`) {
		t.Errorf("editor card missing data-polis-entry-type — DOM grammar broken")
	}
	// Edit icon click handler.
	if !strings.Contains(src, "nav-btn-edit") || !strings.Contains(src, "openEditor()") {
		t.Errorf("edit icon click → openEditor wiring missing")
	}
	// Tiered Esc + Ctrl+Shift+F handler. The handler is scoped to the
	// editor card (not the document) so mid-typing Esc gestures don't
	// inadvertently dismiss the editor — wired via attachEditorKeyboardTo
	// when the card mounts.
	if !strings.Contains(src, "attachEditorKeyboardTo") {
		t.Errorf("attachEditorKeyboardTo missing — editor keyboard handler not wired card-scoped")
	}
	if !strings.Contains(src, "attachEditorKeyboardTo(card)") {
		t.Errorf("mountEditorCard does not call attachEditorKeyboardTo — keyboard handler won't engage")
	}
	if !strings.Contains(src, `'Escape'`) || !strings.Contains(src, "is-editor-focus") {
		t.Errorf("editor keyboard handler missing Esc / focus-mode wiring")
	}
	// Milkdown wiring: a sibling `.milkdown-mount` element must ship
	// inside the editor card body so App._milkdownIdFor's parent-element
	// fallback finds it. Without this, _initMilkdown silently no-ops
	// and the user gets a plain textarea instead of the full Milkdown
	// toolset (resolved-decision #13).
	if !strings.Contains(src, "milkdown-mount") {
		t.Errorf("editor card missing .milkdown-mount sibling — Milkdown won't hydrate")
	}
	// closeEditor must tear down the Milkdown instance so it doesn't
	// leak when the card unmounts.
	if !strings.Contains(src, "_destroyMilkdown") {
		t.Errorf("closeEditor missing _destroyMilkdown call — Milkdown instance leaks on close")
	}
	// Public API.
	if !strings.Contains(src, "openEditor: openEditor") || !strings.Contains(src, "closeEditor: closeEditor") {
		t.Errorf("PolisOwnerExtras public surface missing openEditor / closeEditor")
	}
	// Save / publish wiring against existing endpoints.
	if !strings.Contains(src, "/api/drafts") || !strings.Contains(src, "/api/publish") {
		t.Errorf("editor save / publish must hit existing /api/drafts and /api/publish endpoints")
	}
}

// TestSPA_StreamCSSHasEditorCard — step-06/6.h. The bundle stream.css
// must ship .entry--editor styling + body.is-editor-focus full-screen
// takeover rules.
func TestSPA_StreamCSSHasEditorCard(t *testing.T) {
	cssBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.css")
	if err != nil {
		t.Fatalf("read stream.css: %v", err)
	}
	src := string(cssBytes)
	if !strings.Contains(src, ".entry.entry--editor") && !strings.Contains(src, ".entry--editor") {
		t.Errorf("stream.css missing .entry--editor styling")
	}
	if !strings.Contains(src, "body.is-editor-focus") {
		t.Errorf("stream.css missing body.is-editor-focus full-screen takeover rules")
	}
	// The takeover must hide the topbar + non-editor stream entries +
	// layout-left so the editor truly "takes over."
	if !strings.Contains(src, "body.is-editor-focus .polis-topbar") {
		t.Errorf("focus mode doesn't hide .polis-topbar — full takeover incomplete")
	}
}

// TestSPA_StreamFilterChangePreservesPinned — step-06/6.h. stream.js's
// clearStreamForFilterChange must skip [data-polis-pinned] entries so
// the editor card stays put across filter changes (resolved-decision
// #17: sticky-across-filter-changes only).
func TestSPA_StreamFilterChangePreservesPinned(t *testing.T) {
	jsBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.js")
	if err != nil {
		t.Fatalf("read stream.js: %v", err)
	}
	src := string(jsBytes)
	// The clear loop must include a hasAttribute('data-polis-pinned')
	// check that continues without removing.
	if !strings.Contains(src, "data-polis-pinned") {
		t.Errorf("stream.js doesn't reference data-polis-pinned — pinned entries won't survive filter change")
	}
	// Find the clearStreamForFilterChange function and confirm it
	// references the pinned attribute.
	clearIdx := strings.Index(src, "function clearStreamForFilterChange")
	if clearIdx == -1 {
		t.Fatal("clearStreamForFilterChange function missing")
	}
	clearEnd := strings.Index(src[clearIdx:], "\n    }\n")
	if clearEnd == -1 {
		t.Fatal("clearStreamForFilterChange close not found")
	}
	clearBlock := src[clearIdx : clearIdx+clearEnd]
	if !strings.Contains(clearBlock, "data-polis-pinned") {
		t.Errorf("clearStreamForFilterChange doesn't check data-polis-pinned — pinned entries (editor card) get cleared on filter change")
	}
}

// TestSPA_OwnerExtrasUpdatesActiveIcon — regression test for the
// step-06/6.e bug where preset clicks didn't update which icon shows
// as active. The base SPA's _updateNavActive only fires on view
// changes, but preset clicks don't change the view. owner-extras must
// own the active-icon override since it's the layer that knows about
// presets.
func TestSPA_OwnerExtrasUpdatesActiveIcon(t *testing.T) {
	contents, err := fs.ReadFile(webui.Assets, "www/owner-extras.js")
	if err != nil {
		t.Fatalf("read owner-extras.js: %v", err)
	}
	src := string(contents)

	if !strings.Contains(src, "function setActiveIcon") {
		t.Errorf("owner-extras.js missing setActiveIcon function — preset clicks won't update active state")
	}
	if !strings.Contains(src, "setActiveIcon(presetName)") {
		t.Errorf("loadPreset doesn't call setActiveIcon — active state won't update on preset click")
	}
	// Toggling the .active class is the contract _updateNavActive uses.
	if !strings.Contains(src, "classList.toggle('active'") {
		t.Errorf("setActiveIcon doesn't toggle .active class — won't sync with app.js _updateNavActive")
	}
}

// TestSPA_RestoreRouteAndNavigateTo_BothHandleConversations is the
// regression test for the step-06/6.e bug where _restoreRouteFromURL
// (init-time route resolver) bypassed the conversations branch that
// navigateTo (runtime route handler) had. Without parity, page-refresh
// on /_/feed silently fell through to the v3 conversations renderer
// and the v4 stream-screen never showed.
//
// Both code paths must call _activateStreamScreen + showScreen('stream')
// for view='conversations'. Test asserts the two handlers reference the
// same hook so future workers can't accidentally diverge them.
func TestSPA_RestoreRouteAndNavigateTo_BothHandleConversations(t *testing.T) {
	indexBytes, err := fs.ReadFile(webui.Assets, "www/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	src := string(indexBytes)

	// navigateTo must show stream-screen for conversations.
	navIdx := strings.Index(src, "async navigateTo(path, opts")
	if navIdx == -1 {
		t.Fatal("navigateTo function not found in app.js")
	}
	navEnd := strings.Index(src[navIdx:], "\n    },")
	if navEnd == -1 {
		t.Fatal("navigateTo function close not found")
	}
	navBlock := src[navIdx : navIdx+navEnd]
	if !strings.Contains(navBlock, `config.view === 'conversations'`) {
		t.Errorf("navigateTo missing conversations branch — would route v4 stream view to legacy dashboard")
	}
	if !strings.Contains(navBlock, `_activateStreamScreen`) {
		t.Errorf("navigateTo conversations branch missing _activateStreamScreen call")
	}
	if !strings.Contains(navBlock, `showScreen('stream')`) {
		t.Errorf("navigateTo conversations branch missing showScreen('stream') call")
	}

	// _restoreRouteFromURL must do the same for init-time refresh on /_/feed.
	restIdx := strings.Index(src, "async _restoreRouteFromURL")
	if restIdx == -1 {
		t.Fatal("_restoreRouteFromURL function not found in app.js")
	}
	restEnd := strings.Index(src[restIdx:], "\n    },")
	if restEnd == -1 {
		t.Fatal("_restoreRouteFromURL function close not found")
	}
	restBlock := src[restIdx : restIdx+restEnd]
	if !strings.Contains(restBlock, `config.view === 'conversations'`) {
		t.Errorf("_restoreRouteFromURL missing conversations branch — page-refresh on /_/feed would silently render v3 view")
	}
	if !strings.Contains(restBlock, `_activateStreamScreen`) {
		t.Errorf("_restoreRouteFromURL conversations branch missing _activateStreamScreen call")
	}
	if !strings.Contains(restBlock, `showScreen('stream')`) {
		t.Errorf("_restoreRouteFromURL conversations branch missing showScreen('stream') call")
	}
}

// TestSPA_AvatarMenuStructure asserts the avatar dropdown still
// contains the surviving entries after the inline owner-card bio
// editor replaced the full-screen About editor (R22 follow-up).
// The "Edit About" entry was removed from the menu — the bio is
// now edited from the layout-left scratch column directly. Assert
// the lost entry stays gone so a refactor doesn't accidentally
// resurrect a now-dead App.openAboutEditor reference.
func TestSPA_AvatarMenuStructure(t *testing.T) {
	indexBytes, err := fs.ReadFile(webui.Assets, "www/index.html")
	if err != nil {
		t.Fatalf("read SPA index.html: %v", err)
	}
	idx := string(indexBytes)
	menuIdx := strings.Index(idx, `id="avatar-menu"`)
	if menuIdx == -1 {
		t.Fatal("avatar-menu element missing from index.html")
	}
	menuEnd := strings.Index(idx[menuIdx:], "</div>\n            </div>")
	if menuEnd == -1 {
		menuEnd = len(idx) - menuIdx
	}
	menu := idx[menuIdx : menuIdx+menuEnd]

	if strings.Contains(menu, "App.openAboutEditor") || strings.Contains(menu, "Edit About") {
		t.Errorf("avatar menu still references the removed full-screen About editor — inline owner-card editor is the replacement")
	}
	if !strings.Contains(menu, "App.copyFollowLink") {
		t.Errorf("avatar menu lost Copy follow link entry — regression")
	}
	if !strings.Contains(menu, "App.navigateTo('/settings')") {
		t.Errorf("avatar menu lost Settings entry — regression")
	}
}

// TestSPA_OwnerExtrasStreamItemDecorators — step-06/6.i. owner-extras.js
// must register afterRender decorators for each owner-actionable entry
// type (post / comment / dm) and reference the existing App methods that
// own the round-trip (unpublish / bless / deny). Catches regressions
// like: someone removes the registration, or renames an App method
// without updating the decorator. Layer 3 hooks must always run AFTER
// the base renderer so the .entry-actions block lands in a fully-built
// entry node — that ordering is encoded in stream.js's afterRender
// dispatch and asserted in TestSPA_StreamControllerAfterRenderHook.
func TestSPA_OwnerExtrasStreamItemDecorators(t *testing.T) {
	contents, err := fs.ReadFile(webui.Assets, "www/owner-extras.js")
	if err != nil {
		t.Fatalf("read owner-extras.js: %v", err)
	}
	src := string(contents)

	if !strings.Contains(src, "function registerStreamItemDecorators") {
		t.Errorf("owner-extras missing registerStreamItemDecorators — 6.i decorator suite not wired")
	}
	// Must be called from init chain.
	if !strings.Contains(src, "registerStreamItemDecorators(stream)") {
		t.Errorf("registerStreamItemDecorators not invoked from init chain")
	}
	// Three afterRender registrations.
	for _, kind := range []string{"'post'", "'comment'", "'dm'"} {
		needle := "afterRender(" + kind
		if !strings.Contains(src, needle) {
			t.Errorf("missing afterRender registration for %s entries", kind)
		}
	}
	// Decorators reference the existing App methods (drift-detection:
	// catches a rename that would silently no-op the action).
	for _, method := range []string{"App.unpublishPost", "App.unpublishComment", "App.grantBlessing", "App.denyBlessing"} {
		if !strings.Contains(src, method) {
			t.Errorf("decorators missing reference to %s — owner action would no-op", method)
		}
	}
	// Owner-domain detection: decorators distinguish self vs other via
	// App.siteBaseUrl. If this falls out, a comment from a peer might
	// erroneously offer "unpublish" to the owner (no-op server-side
	// but a confusing UI affordance).
	if !strings.Contains(src, "App.siteBaseUrl") {
		t.Errorf("decorators missing App.siteBaseUrl ownership check — self-vs-other gating broken")
	}
	// source_path must be the path passed to App.unpublishPost /
	// App.unpublishComment for own content. The legacy meta.url form
	// is the rendered .html mount path which /api/unpublish 400s on.
	if !strings.Contains(src, "meta.source_path") {
		t.Errorf("decorators not reading meta.source_path — Unpublish action will 400 against /api/unpublish")
	}
	// After a successful owner action, the stream must re-fetch so
	// the server-side outcome (post dropped, comment unblessed,
	// etc.) reflects in the UI.
	if !strings.Contains(src, "refreshStream") {
		t.Errorf("decorators missing refreshStream wiring — UI stale after owner action")
	}
}

// TestSPA_StreamCSSHasEntryActions — step-06/6.i. stream.css must ship
// .entry-actions hover-revealed chrome rules so the owner-action
// buttons surface on hover and stay hidden otherwise. Visibility is
// gated on body.is-owner (Layer 2 defense in depth — even if a public
// visitor somehow ended up with a decorated DOM, CSS would suppress
// the chrome).
func TestSPA_StreamCSSHasEntryActions(t *testing.T) {
	cssBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.css")
	if err != nil {
		t.Fatalf("read stream.css: %v", err)
	}
	src := string(cssBytes)
	if !strings.Contains(src, ".entry-actions") {
		t.Errorf("stream.css missing .entry-actions selector")
	}
	if !strings.Contains(src, "body.is-owner .entry:hover") {
		t.Errorf("stream.css missing body.is-owner .entry:hover rule — chrome won't reveal on hover")
	}
}
