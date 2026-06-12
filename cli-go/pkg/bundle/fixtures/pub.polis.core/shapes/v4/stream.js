// =============================================================================
// HANDBOOK TRAIL MARKER
// =============================================================================
// This file is the *consumer* of the URL-as-filter thread. It hydrates the
// stream DOM, applies whatever filter the URL parser handed in, and tracks
// scroll-driven focus changes that update the URL bar (closing the loop:
// scroll → focused entry → URL → bookmarkable).
//
// Trail across files (URL-as-filter):
//   producer  — webapp/internal/webui/www/app.js: intercepts /_/pql/<sentence>
//   grammar   — webapp/internal/webui/www/pql.js: PQL.parse / .compose
//   consumer  — this file (stream.js): hydration + scroll-as-URL-mutation
//
// Pull the thread:
//   github.com/vdibart/polis-cli/blob/main/docs/general/concepts/infinity-stream.md (why)
//   github.com/vdibart/polis-cli/blob/main/docs/general/reference/pql.md              (spec)
//   github.com/vdibart/polis-cli/blob/main/docs/general/concepts/shapes.md           (v3 vs v4)
//   github.com/vdibart/polis-cli/blob/main/docs/handbook/url-as-filter.md   (tour)
//   github.com/vdibart/polis-cli/blob/main/AGENTS.md                        (map)
// =============================================================================

// Polis stream controller.
//
// Hydrates the SSR'd stream DOM, tracks scroll-driven focus changes via
// IntersectionObserver, updates the URL bar via history.replaceState, and
// exposes polymorphic item renderers + entry-insertion plumbing as the
// PolisStream namespace for downstream consumers (lazy body fetch, filter
// integration, owner-SPA compose flow).
//
// Same script source is loaded by:
//   - Public per-post artifacts (via stream.html's <script src="/stream.js">)
//   - Owner SPA's index.html (loaded alongside its own /app.js for now;
//     the SPA's existing screens migrate into this controller progressively)
//
// Detection: any page with <body data-polis-stream-direction="..."> is a
// stream-shape page. Pages without that attribute (legacy v3 templates,
// non-polis content) are inert — the controller's init() returns early.
//
// Public surface (window.PolisStream):
//   .renderEntry(meta)              — dispatch on meta.type, return DOM element
//   .renderers.{post,comment,profile,mention,dm}(meta) — type-specific
//   .appendEntry(domEl)             — insert into .stream + observe + track
//   .clearDynamicEntries()          — remove non-SSR entries (filter reset)
//   .getEntries()                   — current entries array (read-only snapshot)
//   .getFocusedEntry()              — currently-focused entry (for prefetch
//                                      horizon, etc.)
//   .announce(text)                 — write to ARIA live region (screen readers)
//   .focusNext() / .focusPrev()     — j/k keyboard nav targets, also callable
//   .onEscape(fn)                   — register Esc-key handler (popover close,
//                                      dialog dismiss, etc.)
//   .afterRender(type, fn)          — Layer-3 extension hook.
//                                      Listener fires after a renderer produces
//                                      its DOM. owner-extras.js subscribes for
//                                      auth-only chrome (edit/bless rollovers,
//                                      activity-mode metadata signal). Public
//                                      bundle never subscribes; layered cleanly.
//   .registerRenderer(type, fn)     — Layer-4 extension override.
//                                      Replace a base renderer entirely. Used
//                                      when owner-side rendering of a type
//                                      diverges fundamentally (e.g., DM owner
//                                      view with decryption indicator).
//   .registerFilterOption(slot,opt) — filter-slot extension.
//                                      Adds an option to the type / qualifier
//                                      / scope dropdowns. owner-extras.js calls
//                                      this at init to expose owner-only
//                                      values (activity, dms, my-mutuals, …).
//   .setFilterScope(value, opts)    — programmatic scope setter.
//                                      Updates filterScope + slot label, then
//                                      triggers applyFilter (clear + re-fetch).
//                                      opts.label overrides the displayed text.
//   .setFilterType(value, opts)     — programmatic type setter.
//                                      Same shape as setFilterScope; resets
//                                      filterModifier to 'by-date' alongside.
//   .setFilterQualifier(value)      — programmatic qualifier setter.
//   .setFilterModifier(value, opts) — programmatic modifier setter.
//   .refresh()                      — re-trigger applyFilter without
//                                      mutating state. Used by
//                                      owner-extras to pull a freshly-
//                                      published post into the stream
//                                      after the editor's Publish
//                                      button fires.
//
// DIRECTION-HOOK markers tag every line whose semantic flips under stream-
// direction reversal. `grep "DIRECTION-HOOK"` is the audit tool.

(function () {
    'use strict';

    /* ------------------------------------------------------------------
       Constants + detection
       ------------------------------------------------------------------ */

    var STREAM_DIRECTION_ATTR = 'data-polis-stream-direction';
    var FOCUS_DEBOUNCE_MS = 150;
    // scrollY ≤ this counts as "at the top of the page" — restores the
    // landing URL (e.g. /index.html) instead of stamping the current
    // focus's canonical URL. 4px slack absorbs anti-aliasing/momentum
    // jitter without missing a deliberate "back to top" gesture.
    var TOP_RESTORE_THRESHOLD = 4;

    function isStreamPage() {
        return !!(document.body && document.body.hasAttribute(STREAM_DIRECTION_ATTR));
    }

    /* ------------------------------------------------------------------
       State
       ------------------------------------------------------------------ */

    var entries = [];           // .entry DOM elements in document order
    var focusedEntry = null;    // currently focused entry (per scroll position)
    var observer = null;
    var intersectingMap = new Map();  // entry -> intersectionRatio
    var focusUpdateTimer = null;
    var userHasScrolled = false;      // gate URL updates on user-initiated scroll
    var userHasScrolledUp = false;    // gate upward pagination on user scroll-UP (separate from any-scroll)
    var hasPushedFirstUpdate = false; // first focus-change uses pushState (preserves landing URL)
    var landingURL = '';              // path+search at hydrate time
    // landingURLNeedsRestore: only the index-page case (where /index.html
    // is distinct from the focus's own canonical URL) needs scroll-to-top
    // to restore the landing URL. On per-post pages the focus's canonical
    // URL IS the landing URL, AND scrollY=0 can mean "user scrolled UP
    // through the above-focus rail and pagination prepended newer posts"
    // — restoring landingURL there would clobber the correct
    // bestEntry-tracked URL with the original post's URL.
    var landingURLNeedsRestore = false;

    // Lazy body fetch state
    var sessionCache = new Map();      // url -> { body, cachedAt }
    var inFlight = new Map();           // url -> Promise<string|null>
    var prefetchObserver = null;
    var BODY_TTL_MS = 60 * 60 * 1000;   // 1 hour
    var PREFETCH_ROOT_MARGIN = '500px 0px 500px 0px';  // ±500px around viewport

    // Lazy-fetch rate limiting. Prevents thundering-herd
    // against followed origins on a fast scroll: cap concurrent fetches
    // both globally (~8) and per-origin (~4). Excess requests get
    // deferred via re-observing the entry — the prefetch observer fires
    // again when the entry's intersection changes (or a fast scroll
    // brings it back into the prefetch horizon). Sliding-window quotas
    // (e.g., "60 fetches per minute") are intentionally NOT implemented
    // for alpha — the in-flight caps already serve the thundering-herd
    // purpose; sliding-window adds complexity without clear value at
    // the user's expected scale.
    var LAZY_FETCH_MAX_TOTAL = 8;
    var LAZY_FETCH_MAX_PER_ORIGIN = 4;
    var lazyFetchInFlightTotal = 0;
    var lazyFetchInFlightByOrigin = new Map();   // host -> count
    // Origin "kill list" — once a host returns a CORS-misconfig error
    // (TypeError with no response object — fetch's standard CORS-failure
    // signature), skip every subsequent fetch to that host for the rest
    // of the page session. Without this gate, a single misconfigured
    // followed origin would drain retry budget on every entry from that
    // origin scrolled into view. CORS misconfigs require server-side
    // remediation; client retries can't help.
    var lazyFetchCORSDisabled = new Set();
    // Per-URL retry state. Each URL tracks attempt count + a pending
    // retry timer ID so applyFilter / page navigation can cancel them.
    var lazyFetchRetries = new Map();          // url -> { attempts, timerId }
    var LAZY_FETCH_MAX_ATTEMPTS = 3;
    var LAZY_FETCH_BACKOFF_BASE_MS = 750;        // 750ms / 1.5s / 3s

    // Pagination state. The public per-post artifact ships
    // focus + 4 SSR'd siblings; once the user scrolls past those, the
    // controller pages older posts in via /api/v1/stream/items.
    var paginationTenant = '';            // canonical hostname (the tenant scope)
    var paginationCursor = '';            // server-returned cursor for next page
    // Debounced search string. Only meaningful
    // under filterType=profiles && filterScope=all-polis. fetchNextPage
    // appends &search=<encoded> when set; the .entry--search input
    // updates this via setSearchQuery() with a 250ms debounce.
    var currentSearch = '';
    var paginationDone = false;           // true after server returns no next_cursor
    var paginationInFlight = false;       // single-flight gate
    // Fetch-generation token. Bumped on every filter change (applyFilter).
    // Each in-flight fetch captures the generation at request time and
    // discards its response if the generation has since moved — otherwise a
    // default-filter fetch still in flight when a PQL URL applies (e.g. a
    // fresh load of /_/pql/all+messages+from+my+mutuals+by+date) would append
    // its stale posts/activity BELOW the new dms view's "no messages yet"
    // empty state. paginationInFlight=false alone doesn't help: the promise
    // is already scheduled, and appendItemIfNew's id-dedup only protects the
    // refresh case (same filter), not a cross-filter response.
    var paginationGeneration = 0;
    var paginationObserver = null;        // last-entry IntersectionObserver
    var paginationLastEntry = null;       // entry currently being observed
    var paginationFirstFetch = true;      // first call uses before_url=<oldest sibling>
    var PAGINATION_LOOKAHEAD = '200px 0px 200px 0px'; // start fetch when last entry ±200px from viewport
    // Pagination retry/backoff. The previous
    // implementation relied on the IntersectionObserver re-firing on a
    // subsequent scroll to retry transient failures — but the observer
    // only fires on intersection-state CHANGE, so a user already past
    // the last-entry boundary at fail-time wouldn't get a retry until
    // they scrolled out + back. Schedule a delayed retry via setTimeout
    // instead, with exponential backoff (750ms / 1.5s / 3s) up to 3
    // attempts. Honors `Retry-After` for 429/503 responses.
    var paginationRetryAttempts = 0;
    var paginationRetryTimer = null;
    var PAGINATION_MAX_ATTEMPTS = 3;
    var PAGINATION_BACKOFF_BASE_MS = 750;

    // Upward pagination state (scroll-up on per-post pages). Symmetric to
    // the down-pagination block above: scroll-up past the SSR'd focus
    // entry fetches NEWER posts via /api/v1/stream/items?after_url=
    // <focus URL> and prepends them above the focus. Skipped on the
    // homepage (focus is already the newest post — nothing to fetch).
    // Independent state from the down direction so concurrent fetches
    // (user scrolls fast in both directions) don't clobber each other.
    var paginationCursorTop = '';
    var paginationDoneTop = false;
    var paginationInFlightTop = false;
    var paginationTopObserver = null;
    var paginationFirstEntry = null;
    var paginationFirstFetchTop = true;       // first call uses after_url=<focus URL>
    var paginationRetryAttemptsTop = 0;
    var paginationRetryTimerTop = null;

    // Filter state. Mirrors the slot
    // grammar from snippets/stream-filter.html. Public-view defaults
    // match the SSR'd sentence shape; user changes via the interactive
    // slots reissue /api/v1/stream/items with new params.
    var filterType = 'posts';             // posts | comments | profiles | activity | dms | drafts | mentions
    var filterModifier = 'by-date';       // by-date | with-comments | to-bless | by-name (type-conditional)
    var filterQualifier = 'all';          // all | new (owner unlocks new; public locked to all)
    // Scope is a first-class controller variable. Empty
    // string means "let pagination derive from paginationTenant" — the
    // public-surface default ('@<canonical-tenant>'). owner-extras.js
    // sets filterScope='my-network' (or whatever preset) at SPA init so
    // pagination requests use the right grammar value. selectOption(scope)
    // updates this and triggers applyFilter.
    var filterScope = '';                 // '' | 'me' | 'my-network' | 'my-mutuals' | 'all-polis' | '@<handle>'
    var filterOpenDropdown = null;        // currently-open dropdown DOM (or null)
    // Empty messages view auto-opens the DM composer once per filter arrival
    // (see renderEmptyState). This guard stops a refresh-driven re-render from
    // reopening it after the user has closed it — closing collapses to the
    // "yours, truly" ghost and stays there. Reset on every filter change so
    // re-navigating to the messages view opens the composer afresh.
    var dmEmptyComposerOpened = false;
    // Dropdown option sets — display text + API value pairs. Display
    // text matches the slot's natural-language convention; API values
    // match the server's accepted enums.
    //
    // Option lists are extensible. Public bundle ships the
    // public-only set (posts/comments/follows). owner-extras.js
    // calls PolisStream.registerFilterOption(slot, opt) to add owner-
    // only values (activity/dms/drafts) without forking the controller.
    var FILTER_TYPE_OPTIONS = [
        // Public surface type ladder. Each type pairs with a per-type
        // default modifier (FILTER_MODIFIER_OPTIONS_BY_TYPE below) and,
        // on public pages, a host-anchored scope: posts/comments use
        // bare @<host>; profiles forces @<host>:network since the bare
        // form isn't a valid profiles data path on the public surface.
        // owner-extras.js replaces this list entirely on the SPA with
        // the owner-set [activity, posts, comments, profiles, messages,
        // drafts] via replaceFilterOptions — adding profiles here
        // doesn't double-register.
        { label: 'posts',    value: 'posts'    },
        { label: 'comments', value: 'comments' },
        { label: 'profiles', value: 'profiles' }
    ];
    // Modifier options are type-conditional. Each entry maps
    // a type to its allowed [(default), (alt)] pair. Types absent from this
    // map have no modifier slot — the controller hides the slot in
    // updateFilterSlotVisibility (driven from grammar).
    var FILTER_MODIFIER_OPTIONS_BY_TYPE = {
        posts: [
            { label: 'by date', value: 'by-date' },
            { label: 'with comments', value: 'with-comments' }
        ],
        comments: [
            { label: 'by date', value: 'by-date' },
            { label: 'to bless', value: 'to-bless' }
        ],
        // Third-clause grammar for profiles is
        // "by name" (default) / "by activity". Default position
        // matches FILTER_MODIFIER_OPTIONS_BY_TYPE convention: first
        // entry is the default.
        profiles: [
            { label: 'by name', value: 'by-name' },
            { label: 'by activity', value: 'by-activity' }
        ],
        drafts: [
            { label: 'by date', value: 'by-date' },
            { label: 'by name', value: 'by-name' }
        ],
        dms: [
            { label: 'by date', value: 'by-date' },
            { label: 'by name', value: 'by-name' }
        ]
        // activity / mentions: no modifier slot (entry absent).
    };
    // Backwards-compat alias for callers that still index the legacy flat
    // array name. Resolves to the posts-only set (the only type the
    // public surface ships interactivity for).
    var FILTER_MODIFIER_OPTIONS = FILTER_MODIFIER_OPTIONS_BY_TYPE.posts;
    // Qualifier options. Public surface keeps qualifier
    // locked to "all" via the SSR'd template; owner-extras.js sets
    // body.is-owner and unlocks the slot interactively. The option list
    // is owner-only; FILTER_QUALIFIER_OPTIONS remains here as the
    // canonical reference for the dropdown when unlocked.
    var FILTER_QUALIFIER_OPTIONS = [
        { label: 'all', value: 'all' },
        { label: 'new', value: 'new' }
    ];
    // Scope options. Public surface keeps scope locked to
    // the tenant's @<domain> identity text; owner-extras.js unlocks for
    // me / my-network / my-mutuals / all-polis / site (handle typeahead).
    var FILTER_SCOPE_OPTIONS = [
        { label: 'me', value: 'me' },
        { label: 'my network', value: 'my-network' },
        { label: 'my mutuals', value: 'my-mutuals' }, // (indent removed — read as odd in dropdown)
        { label: 'all polis', value: 'all-polis' },
        { label: 'site', value: 'site' } // sentinel; opens typeahead input
    ];

    // Map slot kind → option array, used by toggleSlotDropdown +
    // registerFilterOption. Modifier is special-cased (type-conditional),
    // and scope is type-conditional too for profiles.
    function getFilterOptions(slotKind) {
        if (slotKind === 'type') return FILTER_TYPE_OPTIONS;
        if (slotKind === 'qualifier') return FILTER_QUALIFIER_OPTIONS;
        if (slotKind === 'scope') return scopeOptionsForCurrentType(FILTER_SCOPE_OPTIONS);
        if (slotKind === 'modifier') {
            var raw = FILTER_MODIFIER_OPTIONS_BY_TYPE[filterType] || [];
            return modifierOptionsForCurrentScope(raw);
        }
        return [];
    }

    // Filter modifier options based on the current scope.
    // "to bless" only makes sense when querying network comments —
    // own comments don't need blessing. Drop the to-bless entry when
    // scope=me for type=comments.
    function modifierOptionsForCurrentScope(raw) {
        if (filterType !== 'comments' || filterScope !== 'me') return raw;
        var out = [];
        for (var i = 0; i < raw.length; i++) {
            if (raw[i].value !== 'to-bless') out.push(raw[i]);
        }
        return out;
    }

    // Restrict scope choices when type=profiles. The
    // profile surface only makes sense at network scopes — "me" /
    // "my mutuals" / a single "site" handle don't carry meaningful
    // profile-list semantics. Keep "my network" and "all polis" only.
    //
    // DM extension: when type=dms, the dropdown is owner-side-only and
    // becomes a fast-switcher — pinned "↩ my mutuals" (returns to inbox)
    // followed by the 10 most-recently-active conversations (populated
    // by owner-extras via setDMScopeOptions). Public surface never sees
    // type=dms so this branch is owner-only by construction.
    function scopeOptionsForCurrentType(raw) {
        if (filterType === 'dms') {
            // Pinned return-to-inbox entry + the dynamic recent-conv list.
            // Empty recent list (no active conversations yet, or fetch
            // hasn't populated yet) still surfaces the pinned entry so
            // the user can always navigate back to "my mutuals".
            var pinned = [{ label: '↩ my mutuals', value: 'my-mutuals', pinned: true }];
            return pinned.concat(dmScopeOptions);
        }
        // Drop the "site" typeahead sentinel everywhere except DMs.
        // The typeahead isn't wired to a server-side handle filter for
        // posts/comments/activity yet — surfacing it dead in the dropdown
        // is worse than hiding it until that work lands.
        var withoutSite = [];
        for (var j = 0; j < raw.length; j++) {
            if (raw[j].value !== 'site') withoutSite.push(raw[j]);
        }
        if (filterType !== 'profiles') return withoutSite;
        var allowed = { 'my-network': true, 'all-polis': true };
        var out = [];
        for (var i = 0; i < withoutSite.length; i++) {
            if (allowed[withoutSite[i].value]) out.push(withoutSite[i]);
        }
        return out;
    }

    // DM-specific recent-conversation list for the scope dropdown.
    // Populated by owner-extras via PolisStream.setDMScopeOptions whenever
    // it learns the current inbox state. Each entry: { label, value }
    // where label is the peer's handle (e.g. "alice.polis.pub") and
    // value is the internal scope form ("@alice.polis.pub").
    var dmScopeOptions = [];
    function setDMScopeOptions(opts) {
        if (!Array.isArray(opts)) { dmScopeOptions = []; return; }
        dmScopeOptions = opts.filter(function (o) {
            return o && typeof o.label === 'string' && typeof o.value === 'string';
        }).slice(0, 10);
    }

    // Look up the display label for a slot value by walking the
    // relevant option array. Used by the setter functions below as a
    // fallback when the caller didn't pass an explicit label — without
    // this, internal values like 'my-network' / 'by-name' / 'dms' would
    // surface verbatim in the sentence (e.g. when re-hydrating filter
    // state from a PQL URL through setFilter(state)).
    //
    // Special cases:
    //   - scope @<handle>  → render the bare handle (drop the '@').
    //   - scope (unknown handle) → keep as-is (defensive).
    //   - modifier         → walk per-type table (scoped to filterType).
    //   - any miss         → fall back to the value itself.
    function labelForSlot(slotKind, value) {
        if (typeof value !== 'string' || !value) return value || '';
        if (slotKind === 'scope') {
            if (value.charAt(0) === '@') {
                // Possessive-network form: `@<handle>:network` →
                // `<handle>.polis.pub's network`. Used by the bio
                // "N followers" / "N following" stat-links on public
                // visitor pages — keeps the sentence reading as
                // English ("from discover.polis.pub's network") rather
                // than exposing the internal `:network` suffix.
                var rest = value.substring(1);
                if (rest.slice(-8) === ':network') {
                    return rest.slice(0, -8) + "'s network";
                }
                return rest;
            }
            var sArr = FILTER_SCOPE_OPTIONS;
            for (var i = 0; i < sArr.length; i++) {
                if (sArr[i].value === value) return sArr[i].label;
            }
            return value;
        }
        if (slotKind === 'modifier') {
            var mArr = FILTER_MODIFIER_OPTIONS_BY_TYPE[filterType] || [];
            for (var j = 0; j < mArr.length; j++) {
                if (mArr[j].value === value) return mArr[j].label;
            }
            // Fall through: replace dashes with spaces so 'by-name'
            // reads as 'by name' even on type/modifier mismatches.
            return value.replace(/-/g, ' ');
        }
        var arr;
        if (slotKind === 'type') arr = FILTER_TYPE_OPTIONS;
        else if (slotKind === 'qualifier') arr = FILTER_QUALIFIER_OPTIONS;
        else return value;
        for (var k = 0; k < arr.length; k++) {
            if (arr[k].value === value) return arr[k].label;
        }
        return value;
    }

    // Public extension point. owner-extras.js calls
    // PolisStream.registerFilterOption('type', { label: 'activity',
    // value: 'activity' }) at init to add owner-only options without
    // editing the controller.
    function registerFilterOption(slotKind, opt) {
        if (!opt || typeof opt.value !== 'string') return;
        var arr = getFilterOptions(slotKind);
        // De-dupe by value.
        for (var i = 0; i < arr.length; i++) {
            if (arr[i].value === opt.value) return;
        }
        // opts.prepend places the new option at index 0 (used by
        // owner-extras to put 'activity' atop the type dropdown).
        if (opt.prepend) {
            arr.unshift(opt);
        } else {
            arr.push(opt);
        }
    }

    // Replace the entire option list for a slot. Used by owner-extras
    // to override the public default ([posts, comments, follows]) with
    // the owner-only set ([activity, drafts, dms]) — registerFilterOption
    // can only add, never remove. Mutates the array in place so existing
    // references via getFilterOptions stay live.
    function replaceFilterOptions(slotKind, opts) {
        if (!Array.isArray(opts)) return;
        var arr = getFilterOptions(slotKind);
        arr.length = 0;
        for (var i = 0; i < opts.length; i++) {
            if (opts[i] && typeof opts[i].value === 'string') {
                arr.push(opts[i]);
            }
        }
    }

    // Unlock a slot that was SSR'd as `sf-slot--locked` (public default
    // for qualifier + scope) so it becomes interactive. owner-extras.js
    // calls this for `qualifier` and `scope` at init so the owner can
    // toggle between all/new and switch scopes via the sentence filter.
    // Idempotent — safe to call multiple times.
    function unlockSlot(slotKind) {
        var slot = document.querySelector('.sentence-filter [data-filter-slot="' + slotKind + '"]');
        if (!slot) return;
        // Idempotent: bail if already interactive.
        if (slot.classList.contains('sf-slot--interactive')) return;
        // Public surface SSRs scope as sf-slot--identity (italic accent,
        // no underline) and qualifier as sf-slot--locked. Both need
        // converting to sf-slot--interactive so owner sees the same
        // always-underlined affordance the public type/modifier slots
        // already get.
        slot.classList.remove('sf-slot--locked');
        slot.classList.remove('sf-slot--identity');
        slot.classList.add('sf-slot--interactive');
        slot.removeAttribute('data-filter-locked');
        slot.setAttribute('role', 'button');
        slot.setAttribute('tabindex', '0');
        slot.setAttribute('aria-haspopup', 'listbox');
        slot.setAttribute('aria-expanded', 'false');
        attachSlotHandlers(slot);
    }

    /* ------------------------------------------------------------------
       Init
       ------------------------------------------------------------------ */

    function init() {
        if (!isStreamPage()) {
            return;
        }

        // Manual scroll restoration so the browser doesn't fight the
        // controller's history.replaceState updates during scroll.
        if ('scrollRestoration' in history) {
            history.scrollRestoration = 'manual';
        }

        hydrate();
        setupFocusTracking();
        setupPrefetch();
        setupPagination();
        setupPaginationTop();
        setupFilter();
        setupCommentsPanel();
        setupCardClicks();
        setupAboutToggle();
        setupPostBodyToggles();
        setupStatLinks();
        installLiveRegion();
        setupKeyboardNav();
        setupFocusModeClickOutside();
        setupPinnedDotRefresh();

        // Relativize SSR'd timestamps frozen at render time, then keep them
        // live on a 60s tick. See refreshRelativeTimes for the staleness bug
        // this addresses (static page shows "just now" indefinitely).
        refreshRelativeTimes();
        setInterval(refreshRelativeTimes, 60 * 1000);

        // Auto-engage focus mode on canonical permalink pages.
        // When a visitor lands on /posts/.../*.html
        // or /comments/.../*.html, the SSR template puts the target
        // entry as `.entry.is-focused` — the page IS the focus content,
        // so engaging focus mode immediately matches the permalink
        // intent instead of showing siblings as competing context.
        // Click-outside / scroll-to-exit handlers stay active so the
        // visitor can step out of focus to browse siblings if they
        // want. The pre-existing #comments hash branch is now
        // subsumed; opens the comments panel via the same path.
        //
        // The SSR template ALSO marks
        // the most-recent post as `.entry.is-focused` on `/` and
        // `/index.html` (the subdomain root is the same shell with
        // the most-recent post in focus per D-DEEPLINK). Without the
        // gate below, typing `vdibart.polis.pub` would open the latest
        // post in read-focus immediately — surprising behavior on the
        // root landing. Gate: only auto-engage when the URL path is an
        // explicit permalink (anything OTHER than `/` or
        // `/index.html`). Root visitors see the stream with the
        // focus entry visually anchored but NOT mode-locked, so they
        // can scroll the feed normally.
        var path = window.location.pathname || '/';
        // /pql/<sentence> is a filter landing (a stream), not a permalink —
        // treat it like the root so we don't auto-engage focus mode on the
        // SSR'd focus entry. The PQL seed (applyInitialPQLSeed) drives the view.
        var isPermalink = path !== '/' && path !== '/index.html' &&
            path.indexOf('/pql/') !== 0 && path.indexOf('/_/pql/') !== 0;
        if (isPermalink) {
            var focusEntry = document.querySelector('.entry.is-focused');
            if (focusEntry && isFocusModeEligible(focusEntry)) {
                var focusOpts = window.location.hash === '#comments'
                    ? { scrollTo: 'comments' }
                    : undefined;
                enterFocusMode(focusEntry, focusOpts);
            }
        }

        // Auto-scroll to focus + above-focus peek on per-post pages.
        // Runs BEFORE setupUserScrollGate so the synthetic scroll event
        // fired by window.scrollTo doesn't flip userHasScrolled. The
        // user's first MANUAL scroll after the gate attaches will flip
        // the flag correctly. Pre-conditions ("careful" gates) are
        // checked inside autoScrollToFocusPeek — see its docstring.
        autoScrollToFocusPeek();
        // After auto-scroll settles, recompute top-fade visibility from
        // current geometry. Per-post page after auto-scroll: topmost
        // (above-focus) entry is partially behind the fade → show.
        // /index.html: topmost (focus) is below the fade → hide.
        updateTopFadeVisibility();
        // Tall-monitor / short-corpus fallback: if the SSR'd content
        // doesn't fill the viewport, the user has no scroll-trigger
        // affordance and the IO-based pagination machinery sits idle.
        // Proactively kick off fetchNextPage to extend the stream until
        // it overflows the viewport (or until paginationDone). The
        // success handler's observeLastEntry call will then re-fire
        // the IO observer if the new last entry is still inside the
        // 200px lookahead — natural loop terminating on either
        // viewport-fill or end-of-history.
        fillViewportIfShort();
        setupUserScrollGate();

        // Claim keyboard focus for the page so j/k work without first
        // requiring a click. After `document.body.focus()`, keydown
        // events flow through the document-level listener installed by
        // setupKeyboardNav. Body needs tabindex="-1" (set in stream.html)
        // to be a focusable element. Wrapped in try/catch as a defense
        // against environments where body.focus() throws (some embedded
        // contexts).
        try {
            if (document.body && typeof document.body.focus === 'function') {
                document.body.focus({ preventScroll: true });
            }
        } catch (e) { /* ignore */ }

        // PQL hard cutover: the /pql/ HTML branch injects window.__POLIS_INITIAL_PQL
        // when the landing isn't the default posts view. The SSR shell is always
        // the posts view, so re-filter into the seeded view (comments/profiles)
        // on hydrate. No-op on the common posts landing and on the owner SPA.
        applyInitialPQLSeed();
    }

    // applyInitialPQLSeed re-filters the stream to the server-injected initial
    // PQL filter (see handlers_pql.go HTML branch). Reads the structured filter
    // window.__POLIS_INITIAL_PQL = {qualifier,type,scope[,modifier]} and applies
    // it via setFilter (which resets pagination + fetches). Absent → no-op.
    function applyInitialPQLSeed() {
        var seed = window.__POLIS_INITIAL_PQL;
        if (!seed || typeof seed !== 'object') return;
        setFilter(seed);
    }

    // Gate URL updates AND lazy prefetch on a real user-initiated scroll.
    // Layout-shift events (font swap, image lazy-load, hosted nav-widget
    // mounting at #polis-nav-root, theme-toggle hydration, lazy-fetched
    // bodies expanding entries) all fire the IntersectionObserver and
    // would otherwise poison the URL bar by replacing it to whatever
    // entry is closest to viewport center after the shift. Listening for
    // `scroll` (one-shot, passive, capture) catches all forms of user-
    // initiated scroll: mouse wheel, trackpad, scrollbar drag, touch,
    // arrow keys, spacebar, pgup/pgdown, home/end. scrollIntoView()
    // called by j/k also triggers a scroll event — that's fine, it means
    // a user-initiated keypress produced a scroll.
    //
    // First scroll also kicks off prefetching for the same reason: we
    // don't want to spend bandwidth fetching bodies for a user who
    // navigated to the page and immediately left without engaging.
    // autoScrollToFocusPeek scrolls the page so the focus entry's top
    // edge sits just below the topbar + a peek-amount (~140px). Above-
    // focus siblings extend up under the topbar with the
    // .stream-top-fade overlay smoothly fading their upper portion
    // into the topbar bg. The bottom of the topmost above-focus entry
    // is barely visible, communicating "scroll up for more" — and the
    // user's reflexive scroll-up triggers the top-pagination observer
    // (userHasScrolledUp gate flips on first scrollY decrease).
    //
    // "Careful version" gates — skip auto-scroll when:
    //   - .layout-right doesn't carry .show-top-fade at SSR time (no
    //     above-focus siblings rendered → nothing to peek at).
    //   - URL has a fragment (e.g. #comments) — let the browser's own
    //     scroll-to-fragment behavior take over.
    //   - Navigation is a reload or back/forward — preserve any
    //     browser-restored scroll position.
    //   - User has prefers-reduced-motion: reduce — auto-scroll is a
    //     non-essential motion the user has opted out of.
    function autoScrollToFocusPeek() {
        var layoutRight = document.querySelector('.layout-right');
        if (!layoutRight || !layoutRight.classList.contains('show-top-fade')) {
            return;
        }
        if (window.location.hash !== '') {
            return;
        }
        // Reduced-motion gate. matchMedia is supported everywhere we
        // care about; fail-open if it's missing.
        if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
            return;
        }
        // Navigation-type gate. PerformanceNavigationTiming exposes
        // 'navigate' / 'reload' / 'back_forward' / 'prerender'. Only
        // 'navigate' (a fresh URL load) gets the auto-scroll. Reloads
        // and back/forward keep their browser-saved scroll position.
        if (window.performance && typeof performance.getEntriesByType === 'function') {
            var navEntries = performance.getEntriesByType('navigation');
            if (navEntries && navEntries.length > 0 && navEntries[0].type !== 'navigate') {
                return;
            }
        }
        var focusEl = document.querySelector('.entry.is-focused');
        if (!focusEl) return;
        var topbar = document.querySelector('.polis-topbar');
        var topbarHeight = topbar ? topbar.getBoundingClientRect().height : 50;
        // PEEK_AMOUNT is how far below the topbar the focus's top sits.
        // The above-focus entry's bottom 140px peeks under the topbar
        // (and through the fade). Smaller on narrow viewports so the
        // focus stays the dominant element on mobile.
        var peekAmount = window.innerWidth <= 600 ? 90 : 140;
        var rect = focusEl.getBoundingClientRect();
        var targetScrollY = rect.top + window.scrollY - topbarHeight - peekAmount;
        if (targetScrollY <= 0) return; // nothing to scroll up to
        try {
            // 'instant' so the page doesn't visibly animate from 0 to
            // target — the scroll happens before the user perceives a
            // page-top render.
            window.scrollTo({ top: targetScrollY, behavior: 'instant' });
        } catch (e) {
            // Older browsers don't accept the options object form.
            window.scrollTo(0, targetScrollY);
        }
    }

    // fillViewportIfShort proactively kicks off fetchNextPage when the
    // SSR'd content doesn't fill the viewport — the tall-monitor /
    // short-corpus case where pagination's IO observer would otherwise
    // sit idle (no scroll → userHasScrolled never flips → no fetch).
    //
    // The check is geometry-only: if document.scrollHeight <=
    // window.innerHeight + buffer, content is shorter than the
    // viewport. The +50 buffer absorbs sub-pixel rendering noise.
    //
    // Safe to call once at init: fetchNextPage's success handler
    // calls observeLastEntry on the new last entry, and the IO
    // observer's initial-state callback fires when that entry is
    // still inside the 200px lookahead. So the recursion happens via
    // the natural pagination loop (not via re-calling
    // fillViewportIfShort) and terminates when either content
    // overflows the viewport OR the server returns no next_cursor
    // (paginationDone=true → fetchNextPage early-returns).
    //
    // Skipped when:
    //   - paginationDone (already at end-of-history; nothing more to fetch).
    //   - paginationInFlight (a fetch is already underway).
    //   - !paginationTenant (no canonical link → setupPagination bailed).
    function fillViewportIfShort() {
        if (paginationDone || paginationInFlight) return;
        if (!paginationTenant) return;
        var docHeight = document.documentElement.scrollHeight;
        if (docHeight > window.innerHeight + 50) return;
        fetchNextPage();
    }

    // updateTopFadeVisibility decides whether .show-top-fade should be
    // on the .layout-right element. Called from init(), every scroll,
    // and after upward-fetch state changes. Three cases:
    //
    //   (A) scrollY === 0 AND paginationDoneTop → HIDE.
    //       The user is at the top of the rendered stream and there's
    //       no more content to fetch above. Showing the fade here
    //       would obscure the topmost entry's title with no way for
    //       the user to scroll up to "uncover" it. Closes the
    //       edge case where the topmost loaded entry was
    //       stuck partially-faded at scroll-top.
    //
    //   (B) topmostEntry.rect.top >= topbarHeight + fadeHeight → HIDE.
    //       The topmost entry is fully below the fade overlay — no
    //       content under the topbar to fade. Common case on
    //       /index.html before any scroll: focus = newest is the
    //       topmost entry, sits below topbar with the fade-area
    //       above empty. Hiding keeps the focus's top edge crisp.
    //
    //   (C) otherwise → SHOW.
    //       Topmost entry is partially behind the topbar/fade-area;
    //       the fade does its job (smooth transition into topbar bg)
    //       and the scroll-bar position communicates "more above".
    //
    // Same logic governs both /index.html (fade only after user has
    // scrolled past the focus) and per-post pages (fade always shows
    // until upward pagination is done AND user is at scroll-top).
    function updateTopFadeVisibility() {
        var layoutRight = document.querySelector('.layout-right');
        if (!layoutRight) return;
        // Fade shows on every scrollable view, not
        // just type=posts. Earlier shape gated this on filterType to
        // avoid showing fade on per-post-page filter changes (where
        // the focus-post structure was a posts-only affordance), but
        // on the owner SPA every type benefits from the same gradient
        // under the topbar. The Case (A)/(B)/(C) checks below already
        // handle "no overflow" / "scrolled to top" / "partial overlap"
        // correctly regardless of type.
        // Pick the first VISIBLE entry — defensive in case future
        // filters hide entries without removing them from entries[].
        var topmostEntry = null;
        for (var i = 0; i < entries.length; i++) {
            if (!entries[i].classList.contains('is-hidden-by-filter')) {
                topmostEntry = entries[i];
                break;
            }
        }
        if (!topmostEntry) {
            topmostEntry = layoutRight.querySelector('.entry:not(.is-hidden-by-filter)');
        }
        if (!topmostEntry) {
            layoutRight.classList.remove('show-top-fade');
            return;
        }
        // Case (A): at scroll-top with no more upward pagination.
        if (window.scrollY === 0 && paginationDoneTop) {
            layoutRight.classList.remove('show-top-fade');
            return;
        }
        var topbar = document.querySelector('.polis-topbar');
        var topbarH = topbar ? topbar.getBoundingClientRect().height : 50;
        var fadeH = 120; // matches .stream-top-fade's CSS height
        var rect = topmostEntry.getBoundingClientRect();
        // Case (B): topmost fully below the fade-bottom. Hide.
        if (rect.top >= topbarH + fadeH) {
            layoutRight.classList.remove('show-top-fade');
            return;
        }
        // Case (C): topmost partially behind the fade. Show.
        layoutRight.classList.add('show-top-fade');
    }

    function setupUserScrollGate() {
        // Two-stage gate. The first scroll (any direction) flips
        // userHasScrolled and kicks off down-pagination + prefetch.
        // Up-pagination is deferred to the first scroll-UP event so the
        // common case (user lands on per-post page, scrolls down to read
        // older posts) doesn't fire both directions concurrently — the
        // simultaneous prepend + append produces a visible scrollbar
        // jump and pushes the year-marker off-screen as the prepend's
        // scroll-position adjustment fights the user's downward motion.
        // Tracking scroll direction is cheap and lets each direction's
        // observer attach lazily only when the user signals interest.
        var lastY = window.scrollY;
        var onScroll = function () {
            if (!userHasScrolled) {
                userHasScrolled = true;
                startPrefetching();
                startPagination();
            }
            var currentY = window.scrollY;
            if (!userHasScrolledUp && currentY < lastY) {
                userHasScrolledUp = true;
                startPaginationTop();
            }
            lastY = currentY;
            // Top-fade visibility tracks scroll geometry — recompute on
            // every scroll. Cheap (one rect read + one classList op).
            // Drives the filter-landing "fade only after scroll-down" path
            // (the homepage, now served at the PQL URL /pql/all+posts+by+date)
            // and the per-post "fade releases at scroll-top" edge case.
            updateTopFadeVisibility();
            // URL-restoration on scroll-back-to-top depends on a focus
            // update firing when the scroll position crosses
            // TOP_RESTORE_THRESHOLD. IntersectionObserver only fires when
            // ratios cross thresholds — fine for "moved to a different
            // entry" but it can miss a "same entry, scrolled within it"
            // transition. Cheap to schedule (debounced) and idempotent if
            // the observer already pending'd one.
            scheduleFocusUpdate();
        };
        window.addEventListener('scroll', onScroll, { passive: true, capture: true });
    }

    /* ------------------------------------------------------------------
       Hydration — adopt the SSR'd DOM, no re-render
       ------------------------------------------------------------------ */

    function hydrate() {
        entries = Array.prototype.slice.call(document.querySelectorAll('.entry'));
        focusedEntry = document.querySelector('.entry.is-focused') || entries[0] || null;

        // Stamp each entry with its canonical URL so the focus tracker has a
        // uniform lookup regardless of entry sub-type or rendering shape.
        // Two SSR shapes carry the URL in different elements:
        //   - Expanded form (.entry-body--expanded, post-shape default since
        //     the bake-bodies refactor): URL is on .entry-title-link inside.
        //   - Excerpt form (.entry-body--excerpt, used by the JS renderer
        //     for dynamic entries from filter results before lazy-fetch
        //     expands them): the .entry-body element IS the <a>.
        // The focus entry uses the same .entry-title-link wrapper (so
        // clicking the focus title navigates to the canonical URL — same
        // as siblings), so a single querySelector covers focus + siblings
        // + dynamic entries. Falls back to the current document URL if no
        // anchor is found, which preserves the behavior on any future
        // shape that ships a focus without an inner anchor.
        landingURL = window.location.pathname + window.location.search;
        for (var i = 0; i < entries.length; i++) {
            var entry = entries[i];
            var url = null;
            var a = entry.querySelector('a.entry-title-link, a.entry-body, a.entry-body--excerpt');
            if (a) {
                url = a.getAttribute('href');
            } else if (entry === focusedEntry) {
                url = landingURL;
            }
            if (url) {
                entry.dataset.polisCanonicalUrl = url;
            }
        }
        // Decide whether scrollY-at-top should restore landingURL. On a
        // filter landing (the homepage, now served at the PQL URL
        // /pql/all+posts+by+date) the landing URL is distinct from the
        // focus's canonical URL (/posts/.../newest.html) — the user's
        // mental model is "at the top, I'm on the filter view; once I
        // start scrolling, I'm reading a post." On per-post pages the
        // two are identical, so bestEntry tracking handles every case
        // including scroll-up into a dynamically-prepended above-focus
        // rail (where scrollY → 0 means "viewing prepended newer post",
        // NOT "back at landing").
        if (focusedEntry && focusedEntry.dataset.polisCanonicalUrl) {
            landingURLNeedsRestore = (focusedEntry.dataset.polisCanonicalUrl !== landingURL);
        }

        // Initial pageview analytics fire once at load time (analytics infra
        // hook lives upstream). Subsequent replaceState updates on scroll do
        // NOT fire pageview events — that distinction is the whole point of
        // replaceState vs pushState here.
    }

    /* ------------------------------------------------------------------
       Scroll + focus tracking
       ------------------------------------------------------------------ */

    function setupFocusTracking() {
        if (!('IntersectionObserver' in window)) {
            // Old browser: don't crash, but don't track focus either. The
            // page still renders + scrolls fine without the URL updating.
            return;
        }

        var opts = {
            // Multiple thresholds let us pick a center-of-mass focus rather
            // than a binary visible/not. Tuned for "the entry the reader is
            // most centrally looking at" without flapping.
            threshold: [0, 0.25, 0.5, 0.75, 1.0],
        };

        observer = new IntersectionObserver(onIntersect, opts);
        for (var i = 0; i < entries.length; i++) {
            observer.observe(entries[i]);
        }
    }

    function onIntersect(observerEntries) {
        for (var i = 0; i < observerEntries.length; i++) {
            var oe = observerEntries[i];
            if (oe.intersectionRatio > 0) {
                intersectingMap.set(oe.target, oe.intersectionRatio);
            } else {
                intersectingMap.delete(oe.target);
            }
        }
        scheduleFocusUpdate();
    }

    function scheduleFocusUpdate() {
        if (focusUpdateTimer !== null) {
            return; // already pending
        }
        focusUpdateTimer = setTimeout(function () {
            focusUpdateTimer = null;
            updateFocus();
        }, FOCUS_DEBOUNCE_MS);
    }

    function updateFocus() {
        if (intersectingMap.size === 0) {
            return;
        }

        // Honor SSR's focus until the user actually scrolls. The
        // IntersectionObserver fires not just on user scroll but on any
        // layout shift — font swap, image lazy-load, hosted nav-widget
        // mount at #polis-nav-root, theme-toggle hydration, lazy-fetched
        // bodies replacing skeletons, etc. None of those are user
        // intent to change the focused entry; running updateFocus then
        // would poison the URL bar with a sibling URL and break browser
        // back + relative link resolution. Gate on userHasScrolled (set
        // by the one-shot scroll listener installed in setupUserScrollGate).
        if (!userHasScrolled) {
            return;
        }

        // TOP-EDGE tracking: the focused entry is the one currently occupying
        // the reading band — the entry whose TOP edge has most recently crossed
        // above the reading line (just under the topbar), with the next entry's
        // top still below it. This tracks "the post I've scrolled to the top of"
        // rather than the prior center-distance heuristic, which lagged on tall
        // posts: a post's center only reached the eye-line anchor after the
        // reader had already scrolled well past its top, so the URL updated late
        // and the trigger point read as "too low" on the page.
        //
        // DIRECTION-HOOK: the reading line is a fixed offset from the topbar (a
        // direction-agnostic reference); reversing stream direction doesn't
        // change which entry's top occupies it, so this holds for both
        // newest-top and newest-bottom configs.
        var topbar = document.querySelector('.polis-topbar');
        var topbarHeight = topbar ? topbar.getBoundingClientRect().height : 0;
        // Reading line sits just BELOW the top fade, not just under the topbar.
        // The .stream-top-fade overlay is anchored at --topbar-height and is
        // TOP_FADE_HEIGHT tall (see stream.css), gradient-fading content to
        // transparent across that band — so a post isn't actually readable
        // until its top clears the fade's bottom edge. An earlier topbar+24
        // line selected a post while it was still behind the nav + fade, so by
        // the time the next post filled the readable area the URL still pointed
        // at the faded-out post that had scrolled off. Drop the line to the
        // fade's bottom so the band-occupant is the first FULLY-VISIBLE post.
        var TOP_FADE_HEIGHT = 120;  // keep in sync with .stream-top-fade height
        var readLine = topbarHeight + TOP_FADE_HEIGHT;

        var bestEntry = null;
        var bestTop = -Infinity;       // greatest top among entries above the line
        var firstIntersecting = null;  // topmost intersecting entry (fallback)
        var firstTop = Infinity;
        for (var i = 0; i < entries.length; i++) {
            var entry = entries[i];
            if (!intersectingMap.has(entry)) continue;
            var rect = entry.getBoundingClientRect();
            if (rect.top < firstTop) {
                firstTop = rect.top;
                firstIntersecting = entry;
            }
            // Candidate: this entry's top has crossed above the reading line.
            // The band-occupant is the one whose top is closest to the line
            // FROM ABOVE (greatest top <= readLine) — i.e. the most recently
            // scrolled-to post, with later posts' tops still below the line.
            if (rect.top <= readLine && rect.top > bestTop) {
                bestTop = rect.top;
                bestEntry = entry;
            }
        }
        // Before any entry's top has reached the line (at the very top of the
        // page the first entry's top can still sit below it), track the topmost
        // intersecting entry so focus follows the first post rather than nothing.
        if (!bestEntry) {
            bestEntry = firstIntersecting;
        }

        if (!bestEntry) {
            return;
        }

        // Update DOM state classes only when the focused entry actually
        // changes; URL update below handles "scroll within the same
        // focused entry" + "scroll back to top" cases that don't move
        // the focused entry.
        if (bestEntry !== focusedEntry) {
            if (focusedEntry) {
                focusedEntry.classList.remove('is-focused');
            }
            bestEntry.classList.add('is-focused');
            focusedEntry = bestEntry;
        }

        // Clear has-unread on entries the user has scrolled past.
        // Two failure modes the prior walk had:
        //   1. previousElementSibling stopped at non-.entry siblings
        //      (year-markers, fade nodes, the sticky editor card),
        //      leaving entries above them flagged.
        //   2. The walk only ran once per focus change; on fast scroll
        //      the focus could skip past entries between debounces.
        // Iterate ALL .entry siblings of the focused entry's parent —
        // anything whose DOM position precedes the focus and is still
        // marked unread gets cleared. One-way: scrolling back up
        // doesn't re-flag earlier entries.
        if (focusedEntry && focusedEntry.parentNode) {
            var siblings = focusedEntry.parentNode.querySelectorAll(':scope > .entry.has-unread');
            for (var si = 0; si < siblings.length; si++) {
                var s = siblings[si];
                if (s === focusedEntry) break; // querySelectorAll preserves DOM order
                s.classList.remove('has-unread');
                fireEntryReadEvent(s);
            }
        }

        // Pick target URL based on scroll position. The "at the top
        // restore landing URL" rule fires ONLY when landingURL is
        // distinct from the focus's canonical URL — i.e. the
        // /index.html case (landingURLNeedsRestore set in hydrate()).
        // On per-post pages bestEntry tracking alone is correct,
        // including the case where scroll-up triggers paginationTop
        // and prepends newer posts above the original focus: scrollY
        // can drop to 0 there but the user is reading the topmost
        // prepended entry, not "back at the landing URL".
        //
        // The canonical URL was stamped onto each entry's dataset at
        // hydrate time (see hydrate() above), so the lookup is uniform
        // across focus + siblings + dynamically-added entries.
        //
        // First URL change uses pushState — preserves the user's
        // landing URL in history so the browser back button returns
        // to it. Subsequent changes use replaceState to avoid polluting
        // history with one entry per intermediate scroll position.
        // Scroll-driven URL syncing only makes sense on
        // the public per-post page (data-polis-stream-role=
        // "static-focus-and-siblings"), where the focused entry IS the
        // page's identity. On the owner SPA homepage
        // (data-polis-stream-role="owner-spa-homepage") the URL stays
        // /_/feed regardless of which entry is in focus — the entries
        // are heterogenous (cross-tenant) and pushState'ing their
        // canonical URLs would (a) be cross-origin and SecurityError
        // out, and (b) be semantically wrong even if it worked. Skip
        // the URL-mirror logic entirely outside the public surface.
        if (!isPublicFocusSurface()) return;

        // Type-dependent scroll URL (PQL hard cutover). Only the POSTS view
        // rewrites the address bar to the focused post's canonical URL on
        // scroll (share/SEO — the focused post IS the page identity). The
        // comments/profiles views keep their pre-PQL behavior: no per-entry URL
        // identity, so the bar stays on the PQL filter URL while scrolling.
        if (filterType !== 'posts') return;

        var targetURL;
        var focusModeEntry = document.querySelector('.entry.is-focus-mode');
        if (focusModeEntry) {
            // Read-focus mode pins the URL to the post the reader OPENED.
            // enterFocusMode programmatically lifts the focused post under the
            // topbar; that scroll lands the NEXT post on the 25% read-anchor,
            // so without this branch bestEntry tracking would instantly rewrite
            // the URL bar to that sibling (the one visible faintly under the
            // focused post). The focused entry IS the page identity while focus
            // mode is open — focusModeAutoCloseObserver hands tracking back to
            // bestEntry once the reader scrolls it out of view.
            targetURL = focusModeEntry.dataset.polisCanonicalUrl || landingURL;
        } else if (landingURLNeedsRestore && window.scrollY <= TOP_RESTORE_THRESHOLD) {
            targetURL = landingURL;
        } else {
            targetURL = bestEntry.dataset.polisCanonicalUrl || landingURL;
        }
        var currentPath = window.location.pathname + window.location.search;
        if (targetURL && targetURL !== currentPath) {
            if (!hasPushedFirstUpdate) {
                history.pushState(null, '', targetURL);
                hasPushedFirstUpdate = true;
            } else {
                history.replaceState(null, '', targetURL);
            }
        }
    }

    // Returns true only for the public per-post page where the focused
    // entry's canonical URL is a meaningful URL-bar target.
    function isPublicFocusSurface() {
        var stream = document.querySelector('.stream');
        if (!stream) return false;
        return stream.getAttribute('data-polis-stream-role') === 'static-focus-and-siblings';
    }

    /* ------------------------------------------------------------------
       ARIA live region — accessibility surface for "loaded N more posts",
       "filter changed", "no more results", etc. Visually hidden but
       announced by screen readers. polite (not assertive) so it doesn't
       interrupt active reading.
       ------------------------------------------------------------------ */

    var liveRegion = null;

    function installLiveRegion() {
        liveRegion = document.createElement('div');
        liveRegion.id = 'polis-stream-live';
        liveRegion.setAttribute('role', 'status');
        liveRegion.setAttribute('aria-live', 'polite');
        liveRegion.setAttribute('aria-atomic', 'true');
        // Visually hidden via inline style so it works even if stream.css
        // isn't loaded (degraded path). Standard sr-only pattern.
        liveRegion.style.cssText = (
            'position:absolute;width:1px;height:1px;padding:0;margin:-1px;' +
            'overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0;'
        );
        document.body.appendChild(liveRegion);
    }

    // Emit a CustomEvent when an entry transitions from unread → read so
    // SPA-side listeners (owner-extras) can persist the local read state.
    // Bubbles so a single document-level listener can collect events
    // from any entry. Bundle-shape stays generic — public surfaces have
    // no listener so the event is a no-op there.
    function fireEntryReadEvent(entry) {
        if (!entry || !entry.dataset || !entry.dataset.polisEntryId) return;
        entry.dispatchEvent(new CustomEvent('polis:entry-read', {
            bubbles: true,
            detail: {
                id: entry.dataset.polisEntryId,
                type: entry.dataset.polisEntryType || '',
            },
        }));
    }

    function announce(text) {
        if (!liveRegion || !text) return;
        // Setting textContent twice (clear + set) ensures screen readers
        // re-announce even when the new text equals the previous text.
        liveRegion.textContent = '';
        // Defer to next microtask so the clear is observed before the set.
        Promise.resolve().then(function () {
            if (liveRegion) liveRegion.textContent = String(text);
        });
    }

    /* ------------------------------------------------------------------
       Keyboard navigation — j (next entry), k (previous entry),
       Esc (close popovers; no-op until consumers register handlers).

       j/k are conventional "feed reader" bindings (Gmail / Twitter /
       Reddit / HN). They scroll the target entry into view; the
       IntersectionObserver naturally picks it up as the new focus, which
       updates the URL bar via the existing replaceState path.

       DIRECTION-HOOK: in newest-top mode, j (next) targets the OLDER
       entry below the current focus and k (prev) targets the NEWER entry
       above. Reversal would flip these mappings.

       Skip when focus is in an editable element (input/textarea/
       contenteditable) so typing doesn't get hijacked.
       ------------------------------------------------------------------ */

    var popoverHandlers = [];  // close handlers registered by consumers

    function setupKeyboardNav() {
        document.addEventListener('keydown', onKeyDown);
    }

    function isEditableTarget(target) {
        if (!target) return false;
        var tag = target.tagName;
        if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
        if (target.isContentEditable) return true;
        return false;
    }

    function onKeyDown(ev) {
        if (ev.defaultPrevented) return;
        if (ev.metaKey || ev.ctrlKey || ev.altKey) return;
        if (isEditableTarget(ev.target)) return;

        // Context-aware navigation.
        // The open panel IS a visible mode marker — when it's open,
        // j/k + arrow keys advance through comments inside the panel
        // instead of advancing entries. The user knows which mode
        // they're in by what's on screen.
        var openEntry = getEntryWithOpenPanel();

        switch (ev.key) {
            case 'j':
            case 'ArrowDown':
                if (openEntry) {
                    focusNextComment(openEntry);
                } else if (ev.key === 'j') {
                    focusNext();
                } else {
                    return; // ArrowDown with no open panel: let browser scroll
                }
                ev.preventDefault();
                break;
            case 'k':
            case 'ArrowUp':
                if (openEntry) {
                    focusPrevComment(openEntry);
                } else if (ev.key === 'k') {
                    focusPrev();
                } else {
                    return; // ArrowUp with no open panel: let browser scroll
                }
                ev.preventDefault();
                break;
            case 'c':
                // Toggle focus mode on the j/k focused entry.
                if (focusedEntry && isFocusModeEligible(focusedEntry)) {
                    toggleFocusMode(focusedEntry);
                    ev.preventDefault();
                }
                break;
            case 'Enter':
                // Don't hijack Enter when focus is on a real interactive
                // element — let native activation run. Body-level Enter
                // toggles focus mode on the j/k focused entry, mirroring
                // the click gesture.
                if (isInteractiveActiveElement()) return;
                if (focusedEntry && isFocusModeEligible(focusedEntry)) {
                    toggleFocusMode(focusedEntry);
                    ev.preventDefault();
                }
                break;
            case 'Escape':
                if (popoverHandlers.length > 0) {
                    for (var i = 0; i < popoverHandlers.length; i++) {
                        try { popoverHandlers[i](); } catch (e) { /* ignore */ }
                    }
                    ev.preventDefault();
                }
                break;
        }
    }

    function isInteractiveActiveElement() {
        var ae = document.activeElement;
        if (!ae || ae === document.body) return false;
        var tag = ae.tagName;
        if (tag === 'A' || tag === 'BUTTON') return true;
        if (tag === 'INPUT') {
            var type = (ae.getAttribute('type') || '').toLowerCase();
            // Submit-style inputs activate on Enter; text inputs are caught
            // earlier by isEditableTarget.
            return type === 'submit' || type === 'button' || type === 'reset';
        }
        if (ae.getAttribute && ae.getAttribute('role') === 'button') return true;
        return false;
    }

    function focusNext() {
        if (entries.length === 0) return;
        var idx = focusedEntry ? entries.indexOf(focusedEntry) : -1;
        if (idx === entries.length - 1) {
            // At boundary — keystroke registers as received-but-no-further.
            // Don't scroll (target is already current), just flash.
            flashHighlight(focusedEntry);
            return;
        }
        var next = idx >= 0 ? entries[idx + 1] : entries[0];
        if (next) next.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }

    function focusPrev() {
        if (entries.length === 0) return;
        var idx = focusedEntry ? entries.indexOf(focusedEntry) : 0;
        if (idx <= 0) {
            // At boundary — see focusNext for rationale.
            flashHighlight(focusedEntry || entries[0]);
            return;
        }
        var prev = entries[idx - 1];
        if (prev) prev.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }

    // Brief visual feedback at j/k boundaries so the keystroke registers as
    // received-but-no-further. The .highlight class is defined in stream.css
    // with a transition; we add it, schedule a removal, and let CSS animate.
    // Single-tracked timer/entry so mashing the boundary key cancels the
    // previous removal cleanly — without the cancel, two flashes within
    // 1.8s would briefly remove the class between calls and animate jumpily.
    var flashTimer = null;
    var flashEntry = null;

    function flashHighlight(entry) {
        if (!entry) return;
        if (flashTimer) {
            clearTimeout(flashTimer);
            if (flashEntry) flashEntry.classList.remove('highlight');
        }
        entry.classList.add('highlight');
        flashEntry = entry;
        flashTimer = setTimeout(function () {
            entry.classList.remove('highlight');
            flashTimer = null;
            flashEntry = null;
        }, 1800);
    }

    function registerPopoverCloseHandler(fn) {
        if (typeof fn === 'function') popoverHandlers.push(fn);
    }

    /* ------------------------------------------------------------------
       COMMENTS — focus-mode open / close

       Click the focus-entry body or the focus's comment-badge → the
       blessed-comment panel slides open beneath the body. Click again
       (or scroll out, or press Escape, or press `c`) → it closes.

       Smoothness improvements over the prototype's setTimeout-based
       sequencing:
         1. transitionend (with propertyName === 'max-height') replaces
            the magic FOCUS_DURATION_MS + 150 buffer.
         2. Cancel-in-flight transitions on rapid toggle: snapshot the
            current rendered height + apply as start state, animate to
            target. Avoids the "snap to 0 then back up" jank when the
            user clicks-clicks-clicks.
         3. IntersectionObserver on UP/DOWN sentinels replaces the
            scroll-event listener. Only fires on threshold cross,
            zero per-scroll cost.
         4. CSS handles `prefers-reduced-motion` (transition: none).

       Boundary on j-past-last-comment: close panel + advance to next
       entry ("I've read these, what's next?" auto-progression). User
       can always re-open with `c`.

       Scope: focus entry only. Sibling entries don't get
       panels — their badges keep `href="{url}#comments"`
       to navigate to the canonical post page; the controller's
       fragment-auto-open at init() handles the landing.
       ------------------------------------------------------------------ */

    var COMMENTS_PANEL_DURATION_MS = 320;  // matches stream.css transition
    var COMMENTS_TOPBAR_OFFSET = 80;        // smooth-scroll target offset (comments intent)
    // Default focus scroll (body-click into read-focus) lands the entry TOP at
    // this offset. It's larger than COMMENTS_TOPBAR_OFFSET so the post top clears
    // the .stream-top-fade — that overlay is sticky at --topbar-height (50px) and
    // 120px tall, solid bg for its first ~30px then a gradient to ~170px. ~140px
    // puts the date + first line in the mostly-clear lower band. The fade is
    // HIDDEN in focus mode but reappears on close; since exitFocusMode doesn't
    // re-scroll, this single position keeps the post top visible below the fade
    // in BOTH read-focus and the condensed view after closing.
    var FOCUS_ENTRY_TOPBAR_OFFSET = 140;
    var commentsTransitionEnd = null;       // active transitionend handler

    function setupCommentsPanel() {
        // Comments-panel scaffolding for the SSR'd focus entry. The
        // badge click is handled via the entry-level focus-mode
        // delegation in bindCardClick — entering focus mode on the
        // focus entry calls openCommentsPanel, which animates the
        // panel from max-height:0 to scrollHeight. This function
        // exists as a kept-around hook for future panel-init work
        // (lazy-fetched comment threads, etc.) and is intentionally
        // a no-op today. Esc still closes via the popoverHandlers
        // registered by enterFocusMode.
    }

    // setupCardClicks wires the entry-level click handler that opens
    // focus mode for eligible entries (long body or has comments).
    // Both SSR'd siblings (.entry-body--expanded) and the focus entry
    // (.focus-content) get the handler. Dynamic excerpt-form entries
    // (renderPost's <a class="entry-body--excerpt">, used pre-lazy-
    // fetch for filter results) are skipped — their native link nav
    // takes the user to the canonical URL until the body lazy-fetches
    // in and the .entry-body--expanded form takes over.
    function setupCardClicks() {
        var entries = document.querySelectorAll('.entry--post, .entry--comment-thread');
        for (var i = 0; i < entries.length; i++) {
            bindCardClick(entries[i]);
        }
    }

    // bindCardClick wires click-to-enter-focus-mode for one entry.
    // Idempotent via data-card-click-bound. Called at init for SSR'd
    // entries AND from appendEntry (every dynamically-rendered entry,
    // including excerpt-form posts pre-lazy-fetch) AND from injectBody
    // (post-swap re-bind on already-expanded entries — no-op since
    // cardClickBound is already '1').
    //
    // Click semantics:
    //   - Eligible entry (has show-more OR has comments OR is a post/
    //     comment-thread): every click inside enters focus mode. Title-
    //     link, body, post-body-toggle, comments-badge, AND the excerpt-
    //     form body anchor all funnel into the same gesture so the user
    //     doesn't have to aim. Modifier-clicks (cmd/ctrl/shift) fall
    //     through to native handling so cmd-click still opens the
    //     canonical URL in a new tab.
    //   - Excerpt-form entries trigger lazy-fetch on click (instead of
    //     waiting for the prefetch observer to scroll-into-view), then
    //     enter focus mode. Without this, clicking before scrolling
    //     would navigate to the per-post page — inconsistent with the
    //     post-scroll click-to-focus behavior the rest of the stream has.
    //   - Inline body anchors that aren't title-link / comments-badge /
    //     excerpt-body fall through to native navigation — links inside
    //     the post body work as written.
    function bindCardClick(entry) {
        if (!entry || entry.dataset.cardClickBound === '1') return;

        // Bind on the entry article so every click target — title
        // link, body content, post-body-toggle button, comments badge
        // (which sits in .entry-meta-line outside the body) — funnels
        // through the same handler.
        entry.addEventListener('click', function (ev) {
            // Modifier-clicks always pass through to native handling
            // so power-users can cmd-click links to open new tabs.
            if (ev.metaKey || ev.ctrlKey || ev.shiftKey) return;

            var anchor = ev.target.closest('a');
            var button = ev.target.closest('button');

            // Identify the focus-trigger surfaces. Three anchors mean
            // "enter focus mode": the title wrapper (.entry-title-link,
            // expanded form), the comments-count icon (.entry-comments-
            // badge), and the excerpt-form body wrapper
            // (.entry-body--excerpt, the whole card before lazy-fetch
            // swaps it for a div). One button does: .post-body-toggle.
            // Anything else inside the entry (in-body hyperlinks, etc.)
            // falls through to native handling.
            var isFocusTriggerAnchor = anchor && (
                anchor.classList.contains('entry-title-link') ||
                anchor.classList.contains('entry-comments-badge') ||
                anchor.classList.contains('entry-body--excerpt')
            );
            var isFocusTriggerButton = button && button.classList.contains('post-body-toggle');
            if (anchor && !isFocusTriggerAnchor) return;
            if (button && !isFocusTriggerButton) return;

            // Eligibility gate. For ineligible entries (short body, no
            // comments) we leave native navigation alone so the user can
            // still click through to the canonical URL — preventDefault
            // followed by no focus-mode entry would silently swallow the
            // click. Only when an entry IS eligible do we cancel native
            // nav and route to focus mode.
            if (!isFocusModeEligible(entry)) return;

            if (anchor) ev.preventDefault();

            // While in focus mode, the same gesture that opened it
            // closes it. The chrome-outside-click exit
            // (setupFocusModeClickOutside) handles clicks on the dimmed
            // surroundings; this branch is what makes the focused entry
            // itself feel toggleable.
            if (entry.classList.contains('is-focus-mode')) {
                exitFocusMode();
                return;
            }

            // Excerpt-form entry: kick off the lazy-fetch immediately
            // (don't wait for the prefetch observer to scroll it into
            // view) so the body lands while the user reads the focus.
            // fetchAndExpand handles cache hits, in-flight dedup, rate-
            // limiting, and re-observation. injectBody runs eventually
            // and the new .entry-body--expanded slots in under the
            // already-active focus mode — focus state lives on the
            // entry article, not the body, so the swap doesn't drop it.
            //
            // POSTS ONLY. Comment-thread / profile / mention / DM entries
            // already ship their body inline and their canonical URL is NOT
            // a post page: a comment-thread's polisCanonicalUrl is the v3
            // per-comment URL (/comments/…), which has no focus-content
            // marker → fetchBody returns null → fetchAndExpand tags the entry
            // .is-404 and CSS hides it (the entry "disappears" until reload).
            // observeForPrefetch and ensureFocusBody already gate on this;
            // mirror that guard here so clicking a comment-thread's parent-
            // post title opens focus mode without a doomed body fetch.
            var clickEntryType = entry.dataset.polisEntryType;
            if ((!clickEntryType || clickEntryType === 'post') &&
                !entry.classList.contains('is-expanded') &&
                !entry.classList.contains('is-loading') &&
                !entry.classList.contains('is-404') &&
                entry.dataset.polisCanonicalUrl) {
                fetchAndExpand(entry, entry.dataset.polisCanonicalUrl);
            }
            // Click intent: the comments badge sends the user to the
            // post's comments section ("see/add comments"); every other
            // focus-trigger surface (title-link, body, post-body-toggle,
            // excerpt body) lands them at the post top. The intent is
            // threaded through enterFocusMode → installs a follow-up
            // scroll once the comments panel finishes its open animation.
            var focusOpts = {};
            if (anchor && anchor.classList.contains('entry-comments-badge')) {
                focusOpts.scrollTo = 'comments';
            }
            enterFocusMode(entry, focusOpts);
        });
        entry.dataset.cardClickBound = '1';
    }

    // setupAboutToggle wires the layout-left bio's "show more" / "hide"
    // control. Bio is rendered inside .site-bio-wrap with a [hidden]
    // <button class="site-bio-toggle">; CSS caps the bio at ~1/3 viewport
    // height by default. We measure scrollHeight vs clientHeight here —
    // bios that fit don't overflow, so we leave the button [hidden] and
    // remove the max-height cap by adding .is-expanded (which also
    // clears the bottom-fade mask). Re-measure after fonts load so the
    // first paint's pre-font-load height (which can underestimate) doesn't
    // accidentally hide the button on a borderline bio.
    function setupAboutToggle() {
        var bio = document.querySelector('.layout-left .site-bio');
        var btn = document.querySelector('.layout-left .site-bio-toggle');
        var floating = document.querySelector('.layout-left .site-bio-hide-floating');
        var col = document.querySelector('.layout-left');
        if (!bio || !btn) return;

        function setExpanded(expanded) {
            bio.classList.toggle('is-expanded', expanded);
            btn.textContent = expanded ? 'hide' : 'show more';
            btn.setAttribute('aria-expanded', expanded ? 'true' : 'false');
            updateFloating();
        }

        // Floating hide is only useful when the bio is expanded AND the
        // column actually overflows — otherwise the in-flow toggle is
        // already reachable. Re-evaluate on every state change + on
        // resize so a viewport resize that flips overflow state updates
        // the floating control.
        function updateFloating() {
            if (!floating || !col) return;
            var expanded = bio.classList.contains('is-expanded');
            var overflows = col.scrollHeight > col.clientHeight + 2;
            floating.hidden = !(expanded && overflows);
        }

        function measure() {
            // 2px slack so anti-aliasing doesn't flap the visibility on
            // a bio that's exactly the cap height.
            var overflows = bio.scrollHeight > bio.clientHeight + 2;
            if (overflows) {
                btn.hidden = false;
                // Symmetric reset. Without clearing is-expanded here,
                // once a prior call had wrongly expanded the bio
                // (e.g. because the owner-card was still display:none at
                // first paint, scrollHeight read 0), a subsequent measure
                // wouldn't roll it back. Bio stayed expanded even after
                // the card became visible and the cap clearly applied.
                bio.classList.remove('is-expanded');
                btn.textContent = 'show more';
                btn.setAttribute('aria-expanded', 'false');
            } else {
                btn.hidden = true;
                // No overflow — drop the cap so an in-fonts paint that
                // doesn't quite reach the cap doesn't keep a mask edge.
                bio.classList.add('is-expanded');
                btn.setAttribute('aria-expanded', 'true');
            }
            updateFloating();
        }

        btn.addEventListener('click', function () {
            setExpanded(!bio.classList.contains('is-expanded'));
        });

        // Click anywhere on the bio to toggle. Skip clicks on inner
        // anchors so links inside the bio still navigate. Only do
        // anything when the toggle button is unhidden (i.e. the bio
        // actually overflows — short bios stay non-interactive).
        bio.addEventListener('click', function (ev) {
            if (btn.hidden) return;
            if (ev.target.closest('a')) return;
            setExpanded(!bio.classList.contains('is-expanded'));
        });

        if (floating) {
            floating.addEventListener('click', function () {
                setExpanded(false);
                // Scroll the column back to the top so the user lands
                // at the now-collapsed bio rather than mid-stats.
                if (col) col.scrollTop = 0;
            });
        }

        // Layout shifts (column overflow can change as the follow widget
        // hydrates, fonts swap, etc.) flip floating visibility.
        window.addEventListener('resize', updateFloating);

        measure();
        // Web fonts often load after first paint; re-measure so a bio
        // that crosses the cap only after fonts swap gets its toggle.
        if (document.fonts && document.fonts.ready) {
            document.fonts.ready.then(measure);
        }
    }

    // setupPostBodyToggles wires the per-post "show more" / "hide" control
    // for SSR'd entries (focus + siblings). Body element (.content-body
    // for focus, .entry-content for siblings) is capped at ~1/2 viewport
    // height by CSS; we measure scrollHeight vs clientHeight here and
    // only unhide each entry's [hidden] .post-body-toggle button when
    // its body actually exceeds the cap. Re-measure after fonts load —
    // typography swap can push a borderline body across the threshold.
    //
    // Idempotent via data-body-toggle-bound. Dynamically-added entries
    // (filter results / lazy-fetch'd) hit a different render path
    // (renderPost in stream.js) that produces an .entry-body--excerpt
    // wrapper without these elements; this function silently skips them.
    function setupPostBodyToggles() {
        var entries = document.querySelectorAll('.entry--post');
        for (var i = 0; i < entries.length; i++) {
            bindPostBodyToggle(entries[i]);
        }
    }

    function bindPostBodyToggle(entry) {
        if (!entry || entry.dataset.bodyToggleBound === '1') return;
        var btn = entry.querySelector('.post-body-toggle');
        if (!btn) return;
        // Focus uses .focus-content > .content-body; siblings use
        // .entry-content. Pick whichever this entry has.
        var body = entry.querySelector('.focus-content .content-body, .entry-content');
        if (!body) return;

        // measure() decides whether the body actually overflows the
        // 50vh cap. It's the single source of truth for "is this post
        // long enough to be focus-mode-eligible" (isFocusModeEligible
        // reads btn.hidden). For sub-cap bodies, drop the cap so the
        // mask-image doesn't leave a stale fade edge — and these
        // bodies stay focus-mode-ineligible unless they have comments.
        function measure() {
            var overflows = body.scrollHeight > body.clientHeight + 2;
            if (overflows) {
                btn.hidden = false;
            } else {
                btn.hidden = true;
                body.classList.add('is-expanded');
                btn.setAttribute('aria-expanded', 'true');
            }
        }

        // No click handler here — the entry-level delegation in
        // bindCardClick catches button clicks and routes them to
        // enterFocusMode (or no-op if already in focus mode). Direct
        // body toggling is gone; the body uncap is now a side effect
        // of entering focus mode.

        measure();
        if (document.fonts && document.fonts.ready) {
            document.fonts.ready.then(measure);
        }
        entry.dataset.bodyToggleBound = '1';
    }

    // setupStatLinks wires the layout-left site-stats labels (posts /
    // followers / following) as filter shortcuts. Each click rewrites
    // the sentence filter at the top of the page to the corresponding
    // view state and re-fetches the stream:
    //   "posts"     → type=posts, modifier=by-date
    //   "followers" → type=follows
    //   "following" → type=follows
    // (The DS doesn't currently differentiate inbound vs outbound
    // follow direction in the stream filter; both labels collapse to
    // the same view, which is fine for the "shortcut to <handle>'s
    // follow activity" intent.)
    function setupStatLinks() {
        var links = document.querySelectorAll('.layout-left .site-stat-link[data-stat-filter-type]');
        for (var i = 0; i < links.length; i++) {
            (function (link) {
                link.addEventListener('click', function (ev) {
                    ev.preventDefault();
                    var type = link.getAttribute('data-stat-filter-type');
                    var modifier = link.getAttribute('data-stat-filter-modifier') || '';
                    var scope = link.getAttribute('data-stat-filter-scope') || '';
                    applyStatFilter(type, modifier, scope);
                });
            })(links[i]);
        }
    }

    // applyStatFilter mirrors selectOption's effect for both type and
    // modifier slots in a single pass — sets controller state, updates
    // both slot DOM labels from the canonical FILTER_*_OPTIONS lists,
    // ensures the modifier slot's hidden state matches the new type,
    // and fires applyFilter once. (Calling selectOption twice would
    // also work but produces two fetches for one user action.)
    function applyStatFilter(type, modifier, scope) {
        filterType = type;
        if (modifier) filterModifier = modifier;
        // Personal-lock side-effects for drafts/dms — keeps the
        // sentence + slot states truthful when a stat-link jumps the
        // user into a private view. Called BEFORE setting scope from
        // the stat-link so that applyTypePersonalLock's transition-
        // out-of-personal branch (unlockScopeSlot(true) → filterScope
        // = 'my-network') doesn't clobber the stat-link's explicit
        // scope. Example: from drafts (__wasPersonalType=true) →
        // clicking the bio "N posts" link (scope=me): without the
        // reorder, posts would land on my-network instead of me.
        applyTypePersonalLock(type);
        if (scope) {
            filterScope = scope;
            var scSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="scope"]');
            // Route through labelForSlot so possessive-network values
            // (`@<handle>:network`) render as "<handle>'s network"
            // instead of the raw internal form. Bare `@<handle>` and
            // named atoms (me / my-network / ...) also normalize
            // through the same helper.
            if (scSlot) scSlot.textContent = labelForSlot('scope', scope);
        }

        var typeOpt = null;
        for (var i = 0; i < FILTER_TYPE_OPTIONS.length; i++) {
            if (FILTER_TYPE_OPTIONS[i].value === type) {
                typeOpt = FILTER_TYPE_OPTIONS[i];
                break;
            }
        }
        var typeSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="type"]');
        // Fall back to the bare type value when the public type table
        // doesn't carry an entry for it (e.g., 'profiles' isn't in the
        // public FILTER_TYPE_OPTIONS — it's stat-link-only). Without
        // this fallback the slot label silently stayed on the prior
        // type after a stat-link click.
        if (typeSlot) typeSlot.textContent = typeOpt ? typeOpt.label : type;

        if (modifier) {
            var modOpt = null;
            for (var j = 0; j < FILTER_MODIFIER_OPTIONS.length; j++) {
                if (FILTER_MODIFIER_OPTIONS[j].value === modifier) {
                    modOpt = FILTER_MODIFIER_OPTIONS[j];
                    break;
                }
            }
            var modSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
            if (modSlot && modOpt) modSlot.textContent = modOpt.label;
        }

        updateModifierSlotVisibility();
        applyFilter();
    }

    function getEntryWithOpenPanel() {
        // Single panel open at a time (focus-only scope in Phase B).
        return document.querySelector('.entry.comments-open');
    }

    function toggleCommentsPanel(entry) {
        if (!entry) return;
        if (entry.classList.contains('comments-open')) {
            closeCommentsPanel(entry);
        } else {
            openCommentsPanel(entry);
        }
    }

    function openCommentsPanel(entry) {
        if (!entry || entry.classList.contains('comments-open')) return;

        var panel = entry.querySelector('.entry-comments-panel');
        if (!panel) return;

        // Cancel any in-flight transition by snapshotting the current
        // rendered height as the start state — avoids "snap to 0 then
        // back up" when toggle fires mid-close.
        clearPanelTransitionEnd();
        var current = panel.getBoundingClientRect().height;
        if (current > 0 && current < panel.scrollHeight) {
            panel.style.maxHeight = current + 'px';
            // Force reflow before next style change so the browser
            // commits this start state instead of collapsing the two
            // assignments into one frame.
            void panel.offsetHeight;
        } else {
            panel.style.maxHeight = '0px';
            void panel.offsetHeight;
        }

        entry.classList.add('comments-open');

        // Compute target and start the transition. After max-height
        // settles, release to 'none' so any future content (lazy-loaded
        // replies, hypothetical) doesn't clip. Page-level focus-mode
        // state, smooth-scroll, Esc registration, and scroll-out auto-
        // close all live in enterFocusMode now — this function is
        // narrowly responsible for the panel's max-height transition.
        var targetHeight = panel.scrollHeight;
        requestAnimationFrame(function () {
            panel.style.maxHeight = targetHeight + 'px';
        });

        commentsTransitionEnd = function (ev) {
            if (ev.propertyName !== 'max-height') return;
            panel.removeEventListener('transitionend', commentsTransitionEnd);
            commentsTransitionEnd = null;
            if (entry.classList.contains('comments-open')) {
                panel.style.maxHeight = 'none';
            }
        };
        panel.addEventListener('transitionend', commentsTransitionEnd);

        // Mark unread comments read on open. Class flips inform any theme
        // CSS that wants to differentiate unread comments visually.
        if (entry.classList.contains('has-unread')) {
            entry.classList.remove('has-unread');
            entry.querySelectorAll('.comment.unread').forEach(function (c) {
                c.classList.remove('unread');
            });
            fireEntryReadEvent(entry);
        }

        announce('Comments panel opened');
    }

    function closeCommentsPanel(entry) {
        if (!entry || !entry.classList.contains('comments-open')) return;
        var panel = entry.querySelector('.entry-comments-panel');
        if (!panel) return;

        clearPanelTransitionEnd();

        // Snapshot current rendered height (max-height was 'none' after
        // the open transition settled) so the close animation has a
        // concrete start value.
        var current = panel.scrollHeight;
        panel.style.maxHeight = current + 'px';
        void panel.offsetHeight;  // commit start state

        entry.classList.remove('comments-open');

        // Clear any focused-comment cursor inside the panel.
        var focusedComment = panel.querySelector('.comment.is-focused');
        if (focusedComment) focusedComment.classList.remove('is-focused');

        requestAnimationFrame(function () {
            panel.style.maxHeight = '0px';
        });

        commentsTransitionEnd = function (ev) {
            if (ev.propertyName !== 'max-height') return;
            panel.removeEventListener('transitionend', commentsTransitionEnd);
            commentsTransitionEnd = null;
            // Reset inline max-height so the next open computes scrollHeight
            // fresh (the CSS rule's 0px default takes over).
            if (!entry.classList.contains('comments-open')) {
                panel.style.maxHeight = '';
            }
        };
        panel.addEventListener('transitionend', commentsTransitionEnd);

        announce('Comments panel closed');
    }

    function clearPanelTransitionEnd() {
        if (commentsTransitionEnd) {
            // Find the panel the handler is attached to via the active
            // open entry; on rapid toggle the panel is one — guarded by
            // the .removeEventListener call inside the handler being
            // idempotent if it already fired.
            var open = document.querySelector('.entry.comments-open .entry-comments-panel');
            if (open) open.removeEventListener('transitionend', commentsTransitionEnd);
            commentsTransitionEnd = null;
        }
    }

    /* ------------------------------------------------------------------
       Focus mode

       The user-facing "click into a post" primitive. A post is
       focus-mode-eligible when it has a long body (.post-body-toggle
       unhidden by the overflow check) or has comments (.entry-comments-
       badge not .is-empty). Clicking anywhere in an eligible entry —
       title, body, post-body-toggle, comments-badge — enters focus
       mode. Non-eligible entries are inert: no navigation, no card
       click. Modifier-clicks (cmd/ctrl/shift) on inner anchors fall
       through to native browser behavior so power-users can still
       open the canonical URL in a new tab.

       Effects on entry:
         - body.focus-mode + entry.is-focus-mode classes
         - long body uncaps with an animated max-height transition
         - existing comments panel opens (openCommentsPanel)
         - chrome (topbar, layout-left, sibling entries, fades) dims
           and blurs via CSS keyed off body.focus-mode
         - smooth-scroll the entry under the topbar

       Class taxonomy — three distinct concerns:
         .entry.is-focused      — SSR-anchored or scroll-tracker
                                  centered entry; URL-bar source-of-
                                  truth for which post the user is
                                  reading. Independent of focus mode.
         .entry.is-focus-mode   — entry currently in the focus-mode
                                  interaction (one at a time globally).
         body.focus-mode        — page-level chrome-dim flag. Set
                                  whenever any entry is in focus mode.

       Exit triggers (all → exitFocusMode):
         - Esc (registered via popoverHandlers, same channel as the
           comments panel's Esc handler)
         - Click outside the focus entry (document-level handler)
         - Scroll-out: IntersectionObserver sentinels above + below
           the focus entry, installed AFTER transitions settle so the
           entry's own smooth-scroll-into-view doesn't trip them.
       ------------------------------------------------------------------ */

    var BODY_TRANSITION_MS = 360;             // matches stream.css max-height transition
    var FOCUS_AUTO_CLOSE_INSTALL_DELAY_MS = 480;  // body expand + smooth-scroll settle
    // Auto-close fires when the focus entry no longer overlaps the
    // central ~40% of the viewport. Negative rootMargin shrinks the
    // IntersectionObserver root by 30% top + 30% bottom, so once the
    // entry's bounding box exits that middle band the user has clearly
    // moved on — exit immediately without waiting for the entry to
    // fully clear the chrome bands at the edges.
    //
    // The chrome (topbar 50px + stream-top-fade 120px at the top, and
    // bottom-fade 240px at the bottom) already obscures the outer
    // strips of the viewport. A pixel-perfect match against the chrome
    // (-130px / -200px in the previous revision) still left scrolling
    // feeling sluggish — the user reads "I'm done with this post" well
    // before the entry's bbox actually slides into the fade. Inset to
    // 30% on each side keeps the trigger inside the obviously-visible
    // reading band rather than at its edges.
    //
    // Top inset is fixed at ~90px (matches COMMENTS_TOPBAR_OFFSET so the
    // entry's smooth-scrolled top lands JUST inside the trigger zone) —
    // a percent inset broke short entries on tall viewports: a 150px
    // entry whose top scrolls to 80px sits entirely ABOVE a -30% top
    // margin's trigger zone (240px on an 800px viewport), so the
    // observer fired isIntersecting=false on install and exited focus
    // mode immediately. Bottom stays at -30% so "I've scrolled past
    // this post" still feels right for tall posts.
    var FOCUS_AUTO_CLOSE_MARGIN = '-90px 0px -30% 0px';
    var focusModeAutoCloseObserver = null;

    // isFocusModeEligible — eligibility gate for READ-FOCUS MODE (see the
    // section header in stream.css search "READ-FOCUS MODE" for the full
    // spec). An entry is eligible when it has body content to read against
    // the dimmed/blurred chrome:
    //
    //   - .is-expanded → injectBody has run (own/cross-tenant body_html
    //     shipped server-side, or lazy-fetch completed). SSR'd siblings on
    //     per-post pages also ship with .is-expanded.
    //   - .entry--comment-thread → always renders with target-post body
    //     inline + the handle's comment attached underneath.
    //   - non-empty .entry-comments-badge → entry has comments worth
    //     entering focus to read, even if body content failed to ship.
    //     (Safety net for rendering failures + entry types that don't
    //     carry body_html.)
    //
    // Excluded entry types:
    //   - .is-draft → drafts are conceptually WRITE-FOCUS entries (clicking
    //     opens the embedded editor via decoratePost's draft click handler,
    //     which is the write-focus surface). Without this opt-out, drafts
    //     ship body_html → get .is-expanded → would also trigger
    //     read-focus, overlaying both modes on the same click.
    function isFocusModeEligible(entry) {
        if (!entry) return false;
        if (entry.classList.contains('is-draft')) return false;
        if (entry.classList.contains('is-expanded')) return true;
        if (entry.classList.contains('entry--comment-thread')) return true;
        var badge = entry.querySelector('.entry-comments-badge');
        if (badge && !badge.classList.contains('is-empty')) return true;
        // Cross-tenant excerpt-form post with a truncated excerpt: not yet
        // expanded (no body_html shipped, prefetch skips cross-origin) but
        // there's more to read. Entering focus fetches the full body via
        // ensureFocusBody. renderPost stamps data-polis-truncated.
        if (entry.dataset.polisTruncated === '1') return true;
        return false;
    }

    function enterFocusMode(entry, opts) {
        if (!entry || !isFocusModeEligible(entry)) return;
        if (entry.classList.contains('is-focus-mode')) return;
        opts = opts || {};

        // If a different entry is already in focus mode, exit it first.
        // Click-on-different-eligible-entry path: entry handler runs
        // first, so we exit the previous before entering the new.
        var current = document.querySelector('.entry.is-focus-mode');
        if (current && current !== entry) {
            exitFocusMode();
        }

        document.body.classList.add('focus-mode');
        entry.classList.add('is-focus-mode');

        // Auto-expand the long body if it has an unhidden post-body-toggle.
        // measure() in bindPostBodyToggle already added .is-expanded for
        // sub-cap bodies (mask-edge cleanup), so we only animate when the
        // body is genuinely capped.
        expandFocusBodyIfCapped(entry);

        // Cross-tenant excerpt-form post: the SPA ships no body_html for
        // cross-tenant posts (buildPostBodies only renders own posts) and
        // the prefetch path skips cross-origin URLs, so the entry is still
        // showing the truncated excerpt. Read-focus is exactly where the
        // user committed to reading it, so fetch the full body now — the
        // "one direct browser fetch for the post the user opens" path the
        // server-side buildPostBodies comment defers to. Canonical post
        // pages ship Access-Control-Allow-Origin:* (serve/static.go), so
        // the cross-origin read succeeds. Expand + re-scroll once it lands.
        if (!entry.classList.contains('is-expanded')) {
            var onFocusBodyInjected = function () {
                entry.removeEventListener('polis:body-injected', onFocusBodyInjected);
                if (!entry.classList.contains('is-focus-mode')) return;
                expandFocusBodyIfCapped(entry);
                scrollFocusEntry(entry, opts);
            };
            entry.addEventListener('polis:body-injected', onFocusBodyInjected);
            ensureFocusBody(entry);
        }

        // Comments in focus mode:
        //   - If a panel is already present (SSR'd focus entry on a per-post
        //     page ships its blessed-comments panel), just open it.
        //   - Otherwise, for a stream entry with comments, lazily fetch the
        //     single latest comment (blessed or not), build the panel, and
        //     animate it open. The collapsed stream ships no comment preview,
        //     so this is the only place the one comment is loaded.
        var panel = entry.querySelector('.entry-comments-panel');
        var badge = entry.querySelector('.entry-comments-badge');
        var hasComments = badge && !badge.classList.contains('is-empty');
        if (panel && hasComments) {
            openCommentsPanel(entry);
        } else if (hasComments && !panel) {
            fetchFocusComment(entry);
        }

        // Scroll target depends on click intent:
        //   - 'comments' (badge click): scroll the comments panel to the
        //     top of the reading band — the user clicked specifically to
        //     see/add comments. Defer until the panel's open animation
        //     settles so the rect we measure is the final post-animation
        //     geometry, not an intermediate frame.
        //   - default (title / body / toggle click): scroll the entry top
        //     under the topbar — the user wants to read the post.
        scrollFocusEntry(entry, opts);

        // Esc handler — same registry as the comments-panel Esc.
        registerPopoverCloseHandler(function () {
            if (entry.classList.contains('is-focus-mode')) {
                exitFocusMode();
            }
        });

        // Defer scroll-out auto-close install until transitions + smooth-
        // scroll settle. Installing too early lets the entering scroll
        // itself trip the sentinels.
        setTimeout(function () {
            if (entry.classList.contains('is-focus-mode')) {
                installFocusModeAutoClose(entry);
            }
        }, FOCUS_AUTO_CLOSE_INSTALL_DELAY_MS);

        announce('Post opened in focus mode');
    }

    // expandFocusBodyIfCapped uncaps a focus entry's body when it's
    // genuinely capped (post-body-toggle visible). measure() in
    // bindPostBodyToggle already added .is-expanded for sub-cap bodies,
    // so this no-ops on those. Runs on focus enter AND after a lazy body
    // injection completes (cross-tenant fetch), so the freshly-injected
    // full body shows uncapped rather than re-capping behind the toggle.
    function expandFocusBodyIfCapped(entry) {
        var btn = entry.querySelector('.post-body-toggle');
        var body = entry.querySelector('.focus-content .content-body, .entry-content');
        if (btn && !btn.hidden && body && !body.classList.contains('is-expanded')) {
            animateBodyExpand(body);
            btn.setAttribute('aria-expanded', 'true');
            btn.textContent = 'hide';
            entry.dataset.bodyAutoExpanded = '1';
        }
    }

    // scrollFocusEntry positions a focus entry under the topbar. 'comments'
    // intent (badge click) scrolls the comments panel into the reading band
    // after its open animation settles; the default intent scrolls the entry
    // top under the topbar. Factored out of enterFocusMode so it can re-run
    // after an async body injection grows the entry (cross-tenant read-focus).
    function scrollFocusEntry(entry, opts) {
        opts = opts || {};
        if (opts.scrollTo === 'comments') {
            // The panel's max-height transition runs ~250ms; wait a hair
            // longer so getBoundingClientRect lands on the settled height.
            setTimeout(function () {
                if (!entry.classList.contains('is-focus-mode')) return;
                var p = entry.querySelector('.entry-comments-panel');
                var target = p || entry;       // fall back to entry top if no panel
                var r = target.getBoundingClientRect();
                var entryRect = entry.getBoundingClientRect();
                // Scroll the comment panel up to COMMENTS_TOPBAR_OFFSET — UNLESS
                // the whole focus entry (post + now-open panel) already fits in
                // the viewport below FOCUS_ENTRY_TOPBAR_OFFSET. For a SHORT post
                // (e.g. a quip with no/few comments) the panel-scroll would push
                // the post off the top of the screen (most visible on the newest
                // post, which sits at page top with nothing above it). In that
                // case anchor the ENTRY top instead, keeping the post on screen
                // with its panel below.
                var fits = entryRect.height <= (window.innerHeight - FOCUS_ENTRY_TOPBAR_OFFSET);
                var ty = fits
                    ? window.scrollY + entryRect.top - FOCUS_ENTRY_TOPBAR_OFFSET
                    : window.scrollY + r.top - COMMENTS_TOPBAR_OFFSET;
                if (ty < 0) ty = 0;
                try {
                    window.scrollTo({ top: ty, behavior: 'smooth' });
                } catch (e) {
                    window.scrollTo(0, ty);
                }
            }, 320);
        } else {
            // Smooth-scroll the entry to FOCUS_ENTRY_TOPBAR_OFFSET — high enough
            // to give the body reading room, but low enough that the post top
            // stays below the .stream-top-fade after focus closes (the scroll
            // persists; see the constant's note).
            var rect = entry.getBoundingClientRect();
            var targetY = window.scrollY + rect.top - FOCUS_ENTRY_TOPBAR_OFFSET;
            if (Math.abs(targetY - window.scrollY) > 4) {
                try {
                    window.scrollTo({ top: targetY, behavior: 'smooth' });
                } catch (e) {
                    window.scrollTo(0, targetY);
                }
            }
        }
    }

    // ensureFocusBody kicks off a one-shot full-body fetch for a focus entry
    // that hasn't been expanded yet. Posts only (comment-thread/profile/DM
    // entries ship their final body inline). The {force:true} flag tells
    // fetchAndExpand to proceed cross-origin — the deliberate exception to
    // the prefetch path's same-origin gate: prefetch skips cross-origin to
    // avoid a fetch storm across the whole network feed on scroll, but a
    // single user-opened post is worth one direct read of its origin.
    function ensureFocusBody(entry) {
        if (!entry || entry.classList.contains('is-expanded')) return;
        var type = entry.dataset.polisEntryType;
        if (type && type !== 'post') return;
        var url = entry.dataset.polisCanonicalUrl;
        if (!url) return;
        fetchAndExpand(entry, url, { force: true });
    }

    function exitFocusMode() {
        var entry = document.querySelector('.entry.is-focus-mode');
        if (!entry) return;

        teardownFocusModeAutoClose();

        // Close comments panel if it was opened by enterFocusMode.
        if (entry.classList.contains('comments-open')) {
            closeCommentsPanel(entry);
        }

        // Re-cap the body if we auto-expanded it on enter. The user's
        // explicit show-more clicks now also flow through enterFocusMode,
        // so the auto-expanded flag is the single source of truth here.
        if (entry.dataset.bodyAutoExpanded === '1') {
            var btn = entry.querySelector('.post-body-toggle');
            var body = entry.querySelector('.focus-content .content-body, .entry-content');
            if (body && body.classList.contains('is-expanded')) {
                animateBodyCollapse(body);
            }
            if (btn) {
                btn.setAttribute('aria-expanded', 'false');
                btn.textContent = 'show more';
            }
            delete entry.dataset.bodyAutoExpanded;
        }

        // Signal focus exit so write-focus consumers can tear down. The owner
        // SPA's in-focus comment editor listens for this to close itself when
        // the reader leaves read-focus (Esc / click-outside) — comments are a
        // read-focus-only surface, so the editor must not outlive focus.
        try { entry.dispatchEvent(new CustomEvent('polis:focus-exit')); } catch (e) { /* ignore */ }

        entry.classList.remove('is-focus-mode');
        document.body.classList.remove('focus-mode');

        announce('Focus mode closed');
    }

    function toggleFocusMode(entry) {
        if (!entry) return;
        if (entry.classList.contains('is-focus-mode')) {
            exitFocusMode();
        } else {
            enterFocusMode(entry);
        }
    }

    // animateBodyExpand uncaps a capped body with a smooth max-height
    // transition. Adds .is-expanded (drops the mask-image edge), sets
    // explicit pixel max-height (so the transition has a concrete
    // target — animating to 'none' doesn't tween), then releases to
    // 'none' on transitionend so future content additions don't clip.
    function animateBodyExpand(body) {
        if (!body) return;
        var endHeight = body.scrollHeight;
        body.classList.add('is-expanded');
        // Inline style overrides .is-expanded's max-height: none from CSS,
        // giving the transition a concrete target.
        body.style.maxHeight = endHeight + 'px';

        var done = function (ev) {
            if (ev.propertyName !== 'max-height') return;
            body.removeEventListener('transitionend', done);
            if (body.classList.contains('is-expanded')) {
                body.style.maxHeight = 'none';
            }
        };
        body.addEventListener('transitionend', done);
    }

    // animateBodyCollapse re-caps an expanded body. Snapshots the current
    // rendered height as the start state, removes .is-expanded (which
    // re-applies the mask) and clears the inline style so the CSS 50vh
    // cap takes back over — the transition runs on the resulting
    // max-height delta.
    function animateBodyCollapse(body) {
        if (!body) return;
        var current = body.scrollHeight;
        body.style.maxHeight = current + 'px';
        // Force a reflow so the start state is committed before we
        // change the target. Without this the two assignments collapse
        // into one frame and the transition is skipped.
        void body.offsetHeight;

        body.classList.remove('is-expanded');
        body.style.maxHeight = '';
    }

    // installFocusModeAutoClose observes the focus entry against the
    // viewport-minus-chrome zone defined by FOCUS_AUTO_CLOSE_MARGIN.
    // While the entry intersects the visible content band, focus mode
    // stays open. As soon as the entry slides into a fade or under the
    // topbar, exit fires.
    //
    // Two failure modes the current shape of the rule avoids:
    //
    //   1. The first prototype used absolutely-positioned sentinel divs
    //      at entry-top - 30vh and entry-bottom + 30vh. Problem: the UP
    //      sentinel sits above the viewport at install time (the entry
    //      has just been scrolled to viewport top + 80px offset, putting
    //      "entry top - 30vh" already off-screen above), so the
    //      observer's initial-state callback fired with !isIntersecting
    //      && rect.top < 0 — closing focus mode immediately on open.
    //      That's the "rough" feel reported on the prototype. Observing
    //      the entry itself sidesteps that geometry: at install the
    //      entry is in view, isIntersecting:true, no false trigger.
    //
    //   2. The first revision used a +30%/+30% rootMargin (extending
    //      the viewport). On a 900px viewport that meant the entry had
    //      to scroll ~270px past the viewport edge in either direction
    //      before exit fired — a full screen of dimmed siblings before
    //      the chrome restored. The fade overlays at top (120px sticky)
    //      and bottom (240px fixed) made it worse — the entry was
    //      visually invisible long before its bounding box exited the
    //      raw viewport. Inverting the margin (negative inset matching
    //      the fade heights) makes "visually obscured by chrome" the
    //      trigger, which is what the user reads as "I'm done with
    //      this post."
    function installFocusModeAutoClose(entry) {
        teardownFocusModeAutoClose();
        if (typeof IntersectionObserver !== 'function') return;

        focusModeAutoCloseObserver = new IntersectionObserver(function (records) {
            var openEntry = document.querySelector('.entry.is-focus-mode');
            if (!openEntry) {
                teardownFocusModeAutoClose();
                return;
            }
            for (var i = 0; i < records.length; i++) {
                if (!records[i].isIntersecting) {
                    exitFocusMode();
                    return;
                }
            }
        }, {
            rootMargin: FOCUS_AUTO_CLOSE_MARGIN,
            threshold: 0,
        });

        focusModeAutoCloseObserver.observe(entry);
    }

    function teardownFocusModeAutoClose() {
        if (focusModeAutoCloseObserver) {
            focusModeAutoCloseObserver.disconnect();
            focusModeAutoCloseObserver = null;
        }
    }

    // setupFocusModeClickOutside installs a document-level click handler
    // that exits focus mode whenever the click lands outside the
    // currently-focused entry. The click itself is NOT preventDefault'd
    // — chrome links/buttons stay active, just with a side effect of
    // dismissing focus mode. Bound once at init.
    function setupFocusModeClickOutside() {
        document.addEventListener('click', function (ev) {
            var entry = document.querySelector('.entry.is-focus-mode');
            if (!entry) return;
            if (entry.contains(ev.target)) return;
            exitFocusMode();
        });
    }

    // The pinned-dot becomes a refresh affordance on
    // hover (CSS expands it + swaps in a refresh-arrow icon). Click
    // re-fires the current filter via applyFilter so the user sees a
    // fresh server response without a full page reload.
    function setupPinnedDotRefresh() {
        var dot = document.querySelector('.pinned-dot');
        if (!dot) return;
        dot.addEventListener('click', function (ev) {
            ev.preventDefault();
            applyFilter();
        });
    }

    /* Comment-internal keyboard navigation (j/k or arrow keys when a
       panel is open). focusNextComment / focusPrevComment cycle the
       .is-focused class across the panel's .comment children, scrolling
       each into view and adding a subtle accent border via CSS.
       At j-past-last: close panel + advance to next entry ("auto-
       progression" — the user has read these, hand them the next post).
       At k-past-first: stay on first comment (no analogous "back up
       past the entry" affordance — the user can press Escape or click
       outside to close). */
    function focusNextComment(entry) {
        var panel = entry.querySelector('.entry-comments-panel');
        if (!panel) return;
        var comments = panel.querySelectorAll('.comment');
        if (comments.length === 0) {
            // No comments to advance through — fall through to entry nav.
            exitFocusMode();
            focusNext();
            return;
        }
        var current = panel.querySelector('.comment.is-focused');
        var idx = current ? Array.prototype.indexOf.call(comments, current) : -1;
        if (idx === comments.length - 1) {
            // Past last comment — auto-progress (exit focus mode + advance).
            exitFocusMode();
            focusNext();
            return;
        }
        if (current) current.classList.remove('is-focused');
        var next = comments[idx + 1];
        next.classList.add('is-focused');
        next.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }

    function focusPrevComment(entry) {
        var panel = entry.querySelector('.entry-comments-panel');
        if (!panel) return;
        var comments = panel.querySelectorAll('.comment');
        if (comments.length === 0) return;
        var current = panel.querySelector('.comment.is-focused');
        var idx = current ? Array.prototype.indexOf.call(comments, current) : 0;
        if (idx <= 0) {
            // At first — flash and stay.
            if (comments[0]) flashCommentBoundary(comments[0]);
            return;
        }
        if (current) current.classList.remove('is-focused');
        var prev = comments[idx - 1];
        prev.classList.add('is-focused');
        prev.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }

    function flashCommentBoundary(comment) {
        // Re-use the entry-level highlight pattern but applied to a
        // single comment element. CSS handles the transition; we add
        // and schedule removal.
        comment.classList.add('highlight');
        setTimeout(function () {
            comment.classList.remove('highlight');
        }, 800);
    }

    /* ------------------------------------------------------------------
       Lazy body fetch + session cache + prefetch

       When sibling entries (or dynamically-added entries from filter
       results) approach the viewport, fetch their canonical
       URL, extract the focus body, and inject it inline so the user
       can read full content without leaving the page.

       Lazy-fetched bodies render at compact (excerpt-like) styling,
       NOT prose styling. Only the SSR'd focus entry's
       .focus-content marker triggers prose font-size; lazy-fetched
       bodies land in .entry-content which inherits compact styling.
       The stream stays visually uniform as the user scrolls through
       expanded entries.

       DMs explicitly bypass this path: DM bodies
       live encrypted on disk locally and are decrypted in the owner's
       browser session — no cross-origin fetch is involved.
       observeEntryForPrefetch skips entries with
       data-polis-entry-type="dm".

       Trust chain for injected body HTML: same chain as renderComment's
       innerHTML — search "TRUST CHAIN" for the four assumptions.
       ------------------------------------------------------------------ */

    function setupPrefetch() {
        if (!('IntersectionObserver' in window)) return;
        prefetchObserver = new IntersectionObserver(onPrefetchIntersect, {
            rootMargin: PREFETCH_ROOT_MARGIN,
            threshold: 0,
        });
        // Don't observe entries until the user actually scrolls. Matches
        // the "honor SSR until user engages" principle: a user who
        // landed on the page but immediately navigated away shouldn't
        // have triggered a wave of prefetches against followed origins.
    }

    function startPrefetching() {
        if (!prefetchObserver) return;
        for (var i = 0; i < entries.length; i++) {
            observeEntryForPrefetch(entries[i]);
        }
    }

    function observeEntryForPrefetch(entry) {
        if (!prefetchObserver || !entry) return;
        // Skip if already expanded (already fetched).
        if (entry.classList.contains('is-expanded')) return;
        // Only post entries have the excerpt → fetch → expand flow.
        // Comment-thread / profile / mention / follow / DM are rendered
        // in their final shape by their respective render*() functions
        // (already carry author, date, body HTML inline from the API
        // response). Observing them would trigger fetchBody on their
        // canonical URL — which for comments is a v3 per-comment page
        // that lacks the <main class="focus-content"> protocol marker
        // fetchBody looks for, so the call returns null, fetchAndExpand
        // tags the entry with .is-404, and CSS hides it. Entries appear
        // briefly on render, then disappear when scrolled into the
        // prefetch root margin. This gate excludes all non-post types,
        // not just the entry.dataset.polisEntryType === 'dm' bypass.
        var entryType = entry.dataset.polisEntryType;
        if (entryType && entryType !== 'post') return;
        // SSR'd focus entry already has its body inlined.
        if (entry.querySelector('main.focus-content[data-polis-focus="true"]')) return;
        // Need a canonical URL to fetch from.
        if (!entry.dataset.polisCanonicalUrl) return;
        // Entries that ship their body_html
        // synchronously (currently drafts via handleStreamItemsDrafts)
        // set data-polis-no-prefetch="1" — skip them. Their URL is an
        // SPA deep-link (/_/posts/drafts/<id>) which would otherwise
        // resolve to spaHandler's index.html fallback, fetchBody
        // returns null since there's no focus-content marker, and
        // the entry gets tagged .is-404 and hidden.
        if (entry.dataset.polisNoPrefetch === '1') return;
        prefetchObserver.observe(entry);
    }

    function onPrefetchIntersect(observerEntries) {
        for (var i = 0; i < observerEntries.length; i++) {
            var oe = observerEntries[i];
            if (!oe.isIntersecting) continue;
            // Defensive: if is-expanded was set after observation began
            // (e.g., the deferred injectBody from renderPost's body_html
            // path fired between observation and intersection), the entry
            // already has its body content. Skip the lazy-fetch entirely;
            // firing fetchAndExpand against an already-injected entry
            // wastes a network round-trip and can flip the entry to
            // is-404 on URLs that resolve to the SPA shell.
            if (oe.target.classList.contains('is-expanded')) {
                prefetchObserver.unobserve(oe.target);
                continue;
            }
            var url = oe.target.dataset.polisCanonicalUrl;
            if (!url) continue;
            // One-shot: don't re-fire for the same entry.
            prefetchObserver.unobserve(oe.target);
            fetchAndExpand(oe.target, url);
        }
    }

    function fetchAndExpand(entry, url, opts) {
        // force: this is a user-opened post (read-focus), not a scroll-
        // triggered prefetch. It bypasses the cross-origin skip, the
        // per-origin CORS-disabled gate, and the concurrency deferral
        // below — one direct read of the post's origin is always worth it.
        var force = !!(opts && opts.force);

        // Cache hit (within TTL): inject immediately without a network
        // round-trip. Entries past TTL fall through to a fresh fetch on
        // next prefetch trigger.
        var cached = sessionCache.get(url);
        if (cached && (Date.now() - cached.cachedAt) < BODY_TTL_MS) {
            injectBody(entry, cached.body);
            return;
        }

        // Cross-origin lazy-fetch always fails (polis
        // tenants don't ship Access-Control-Allow-Origin) — skip the
        // attempt entirely instead of letting it through, hitting CORS,
        // and logging once-per-origin. The owner SPA's stream is full
        // of cross-tenant URLs; on the first burst, dozens of entries
        // would race to the broken-CORS branch before the disabled-set
        // catches up. The public per-post page only ever has same-
        // tenant siblings, so this gate is a no-op there.
        if (!isSameOrigin(url) && !force) {
            entry.classList.remove('is-loading');
            return;
        }

        // Already in flight from a previous prefetch attempt: piggyback.
        if (inFlight.has(url)) {
            inFlight.get(url).then(function (body) {
                if (body) injectBody(entry, body);
            }).catch(function () { /* already logged */ });
            return;
        }

        // CORS-misconfigured origins: skip without trying. Repeated
        // entries from the same broken origin would otherwise drain the
        // retry budget on every scroll-into-view. Server-side fix
        // required to recover; client retries can't help.
        var origin = lazyFetchOriginOf(url);
        if (!force && origin && lazyFetchCORSDisabled.has(origin)) {
            entry.classList.remove('is-loading');
            return;
        }

        // Rate-limit gate: re-observe the entry if we're at capacity.
        // Either cap (total or per-origin) being saturated defers the
        // fetch; the prefetch observer fires again on intersection-state
        // change, which gives natural backpressure on a fast scroll.
        if (!force && !lazyFetchCanProceed(origin)) {
            // Re-observe so a later intersection (or scroll change) can
            // retry. Pure "queue + drain" was considered + rejected as
            // alpha-overkill — re-observe is observer-native and falls
            // out for free on filter change (clearStreamForFilterChange
            // unobserves all entries).
            if (prefetchObserver) prefetchObserver.observe(entry);
            return;
        }

        entry.classList.add('is-loading');
        lazyFetchAcquire(origin);
        // Cross-origin forced fetch (read-focus on a cross-tenant post) goes
        // through the same-origin server-side body proxy: the SPA's
        // connect-src CSP ('self' + esm.sh) blocks a direct browser fetch to
        // a followed tenant's origin even though that origin serves CORS:*.
        // Same-origin prefetch keeps the direct fetchBody path.
        var promise = (force && !isSameOrigin(url)) ? fetchBodyViaProxy(url) : fetchBody(url);
        inFlight.set(url, promise);
        promise.then(function (body) {
            inFlight.delete(url);
            lazyFetchRelease(origin);
            // Success path — drop any retry record for this URL so a
            // future TTL-stale refetch starts fresh.
            lazyFetchRetries.delete(url);
            if (body == null) {
                // 404 — silent skip per D-PROXY's "unpublish propagates
                // silently." Mark the entry so CSS .is-404 hides it.
                entry.classList.remove('is-loading');
                entry.classList.add('is-404');
                return;
            }
            sessionCache.set(url, { body: body, cachedAt: Date.now() });
            injectBody(entry, body);
        }).catch(function (err) {
            inFlight.delete(url);
            lazyFetchRelease(origin);
            // Classify the error. CORS misconfigs surface as plain
            // TypeError with no `response` — disable that origin for the
            // rest of the session and don't retry. Network / 5xx errors
            // get exponential backoff up to 3 attempts; 4xx (other than
            // 429) is a permanent "bad request" — don't retry.
            if (isCORSMisconfig(err)) {
                // First time we see this origin fail: log once, mark
                // disabled. Concurrent in-flight fetches that lose the
                // race still hit this branch but skip the log so the
                // console doesn't repeat the same warning N times.
                var firstTime = !!origin && !lazyFetchCORSDisabled.has(origin);
                if (origin) lazyFetchCORSDisabled.add(origin);
                entry.classList.remove('is-loading');
                if (firstTime) {
                    console.warn('[polis-stream] CORS-disabled for origin', origin || '(unknown)');
                }
                return;
            }
            if (!isLazyFetchTransient(err)) {
                entry.classList.remove('is-loading');
                console.warn('[polis-stream] fetch failed (no retry) for', url, err);
                return;
            }
            scheduleLazyFetchRetry(entry, url, err);
        });
    }

    // Lazy-fetch helpers — origin extraction, capacity gate, CORS
    // classification, retry scheduling. Kept inline (not in a separate
    // module) because the controller is a single shipped JS asset.

    function lazyFetchOriginOf(url) {
        try { return new URL(url, window.location.href).host; }
        catch (e) { return ''; }
    }

    // Same-origin check used by fetchAndExpand to skip cross-origin
    // lazy-fetch attempts that would always fail with CORS. Path-only
    // URLs (no scheme/host) are always same-origin.
    function isSameOrigin(url) {
        if (!url || url.charAt(0) === '/') return true;
        try {
            return new URL(url, window.location.href).origin === window.location.origin;
        } catch (e) {
            return false;
        }
    }

    function lazyFetchCanProceed(origin) {
        if (lazyFetchInFlightTotal >= LAZY_FETCH_MAX_TOTAL) return false;
        if (origin) {
            var perOrigin = lazyFetchInFlightByOrigin.get(origin) || 0;
            if (perOrigin >= LAZY_FETCH_MAX_PER_ORIGIN) return false;
        }
        return true;
    }

    function lazyFetchAcquire(origin) {
        lazyFetchInFlightTotal++;
        if (origin) {
            lazyFetchInFlightByOrigin.set(origin, (lazyFetchInFlightByOrigin.get(origin) || 0) + 1);
        }
    }

    function lazyFetchRelease(origin) {
        if (lazyFetchInFlightTotal > 0) lazyFetchInFlightTotal--;
        if (origin) {
            var n = (lazyFetchInFlightByOrigin.get(origin) || 0) - 1;
            if (n > 0) lazyFetchInFlightByOrigin.set(origin, n);
            else lazyFetchInFlightByOrigin.delete(origin);
        }
    }

    // CORS misconfig signature: fetch() rejects with a TypeError + no
    // `status` field (because the response object never reached us).
    // Same signature as a DNS failure or a fully offline origin —
    // alpha-acceptable false positive: disabling a transiently-offline
    // origin for the session is cheaper than driving retry storms.
    function isCORSMisconfig(err) {
        return !!(err && err.name === 'TypeError' && err.status == null);
    }

    function isLazyFetchTransient(err) {
        // Transient: network errors (handled above as TypeError when not
        // CORS), 429 / 5xx HTTP. Permanent: 4xx other than 429.
        if (!err) return false;
        if (err.transient) return true;
        var msg = err.message || '';
        var m = msg.match(/^fetch failed: (\d+)$/);
        if (m) {
            var status = parseInt(m[1], 10);
            return status === 429 || (status >= 500 && status < 600);
        }
        return false;
    }

    function scheduleLazyFetchRetry(entry, url, err) {
        var record = lazyFetchRetries.get(url) || { attempts: 0, timerId: null };
        record.attempts++;
        if (record.attempts > LAZY_FETCH_MAX_ATTEMPTS) {
            entry.classList.remove('is-loading');
            console.warn('[polis-stream] fetch failed after retries for', url, err);
            lazyFetchRetries.delete(url);
            return;
        }
        var delay = (err && err.retryAfterMs)
            ? err.retryAfterMs
            : LAZY_FETCH_BACKOFF_BASE_MS * Math.pow(2, record.attempts - 1);
        if (record.timerId) clearTimeout(record.timerId);
        record.timerId = setTimeout(function () {
            record.timerId = null;
            // Skip retry if the entry was removed (filter change) or
            // already resolved by another path (cache hit). The DOM
            // check is cheap and avoids stale-entry retries.
            if (!entry.isConnected || entry.classList.contains('is-expanded') || entry.classList.contains('is-404')) {
                lazyFetchRetries.delete(url);
                return;
            }
            fetchAndExpand(entry, url);
        }, delay);
        lazyFetchRetries.set(url, record);
    }

    // Origin overrides (test infrastructure only). When window.POLIS_ORIGIN_OVERRIDES
    // is an object mapping hostname -> URL prefix, outbound fetches for matching
    // hosts get rewritten to the configured destination. In production the global
    // is undefined and applyOriginOverride is a no-op.
    function applyOriginOverride(rawUrl) {
        var ovr = (typeof window !== 'undefined') && window.POLIS_ORIGIN_OVERRIDES;
        if (!ovr) return rawUrl;
        try {
            var u = new URL(rawUrl, window.location.href);
            var dst = ovr[u.host];
            if (!dst) return rawUrl;
            // dst is a URL-prefix string (e.g. http://localhost:9000/origin/discover.polis.pub).
            // Append the original path + query.
            var trimmed = dst.replace(/\/+$/, '');
            return trimmed + u.pathname + u.search;
        } catch (e) {
            return rawUrl;
        }
    }

    // fetchBodyViaProxy resolves a post body through the owner webapp's same-
    // origin proxy (/api/v1/stream/body) instead of fetching the remote origin
    // directly — the SPA's connect-src CSP forbids the cross-origin connect.
    // The server fetches + renders the body (24h cached, SSRF-gated) and
    // returns { has_body, body_html }; we hand back the same HTML-string shape
    // fetchBody yields (or null on miss), so the fetchAndExpand pipeline +
    // injectBody are unchanged.
    function fetchBodyViaProxy(url) {
        var proxyURL = '/api/v1/stream/body?url=' + encodeURIComponent(url);
        return fetch(proxyURL, { credentials: 'same-origin' }).then(function (resp) {
            if (resp.status === 404) return null;
            if (resp.status === 429 || resp.status === 503) {
                var err = new Error('fetch failed: ' + resp.status);
                err.status = resp.status;
                err.retryAfterMs = parseRetryAfter(resp.headers.get('Retry-After'));
                err.transient = true;
                throw err;
            }
            if (!resp.ok) {
                var permErr = new Error('fetch failed: ' + resp.status);
                permErr.status = resp.status;
                throw permErr;
            }
            return resp.json();
        }).then(function (data) {
            if (!data || !data.has_body || !data.body_html) return null;
            return data.body_html;
        });
    }

    function fetchBody(url) {
        return fetch(applyOriginOverride(url), { credentials: 'omit' }).then(function (resp) {
            if (resp.status === 404) return null;
            // Throttle / backpressure markers: 429 (rate limit) + 503
            // (service unavailable). Honor Retry-After for the retry
            // delay; surface as a transient error so isLazyFetchTransient
            // schedules a retry rather than dropping permanently.
            if (resp.status === 429 || resp.status === 503) {
                var err = new Error('fetch failed: ' + resp.status);
                err.status = resp.status;
                err.retryAfterMs = parseRetryAfter(resp.headers.get('Retry-After'));
                err.transient = true;
                throw err;
            }
            if (!resp.ok) {
                var permErr = new Error('fetch failed: ' + resp.status);
                permErr.status = resp.status;
                throw permErr;
            }
            return resp.text();
        }).then(function (html) {
            if (html == null) return null;
            var doc = new DOMParser().parseFromString(html, 'text/html');
            // The cross-tenant body extraction protocol marker.
            var focus = doc.querySelector('main.focus-content[data-polis-focus="true"]');
            return focus ? focus.innerHTML : null;
        });
    }

    function injectBody(entry, bodyHtml) {
        var oldBody = entry.querySelector('.entry-body');
        if (!oldBody) return;

        // Restructure: replace the excerpt-link wrapper (<a>) with an
        // expanded body container (<div>). The title becomes its own
        // link — clicking it still navigates to the canonical post URL.
        // The body content sits below as non-link, so intra-body links
        // (e.g., the post linking to other articles) work normally.
        // Nested <a> would be invalid HTML; the parser would close the
        // outer <a> when it encounters an inner one, producing weird
        // DOM. Restructuring sidesteps that.
        var titleEl = oldBody.querySelector('.entry-title');
        var url = oldBody.getAttribute('href') || entry.dataset.polisCanonicalUrl;

        var newBody = document.createElement('div');
        newBody.className = 'entry-body entry-body--expanded';

        if (titleEl) {
            var titleLink = document.createElement('a');
            titleLink.className = 'entry-title-link';
            // Carry forward the title-redundant flag stamped by renderPost.
            // Without this, the body-swap from excerpt → expanded loses
            // the dedup signal: the new .entry-title-link gets default
            // styling (visible h3) even though the body's first sentence
            // begins with the title.
            if (entry.dataset.polisTitleRedundant === '1') {
                titleLink.className += ' is-redundant';
            }
            titleLink.setAttribute('href', url);
            // Clone the title element WITHOUT the .is-redundant class
            // that the excerpt form may carry — the redundancy treatment
            // now lives on the title-link wrapper (matches SSR shape).
            var titleClone = titleEl.cloneNode(true);
            titleClone.classList.remove('is-redundant');
            titleLink.appendChild(titleClone);
            newBody.appendChild(titleLink);
        }

        var content = document.createElement('div');
        content.className = 'entry-content';
        // ── TRUST CHAIN — see the named comment block at the first
        // innerHTML site in renderComment for the four assumptions
        // every innerHTML call site in this file depends on. Lazy-
        // fetched body HTML originates from the same publish-time
        // sanitization + sign-verify chain as comment body_html.
        content.innerHTML = bodyHtml;
        // The author avatar is NOT relocated here anymore. Post chips are now
        // absolutely positioned top-right of the rail with a reserved body
        // gutter (see .entry--post .entry-rail > .entry-avatar-link in
        // stream.css), so the chip stays a rail sibling in both the excerpt and
        // expanded forms — no float-into-the-BFC dance.
        newBody.appendChild(content);

        // Long-post collapse toggle — matches the SSR'd siblings'
        // markup in stream-post.html. Hidden by default; the
        // bindPostBodyToggle call below measures overflow against the
        // 50vh cap and unhides only if the body actually exceeds it.
        // Without this, dynamic post entries (filter results,
        // scroll-paginated, etc.) never get the toggle even when their
        // bodies clearly overflow — only SSR'd entries got toggles
        // since setupPostBodyToggles runs once at init.
        var toggle = document.createElement('button');
        toggle.className = 'post-body-toggle';
        toggle.type = 'button';
        toggle.setAttribute('aria-expanded', 'false');
        toggle.hidden = true;
        toggle.textContent = 'show more';
        newBody.appendChild(toggle);

        oldBody.parentNode.replaceChild(newBody, oldBody);
        entry.classList.remove('is-loading');
        entry.classList.add('is-expanded');
        entry.classList.add('is-loaded');
        // The new .entry-body--expanded is now the click target. Wire
        // the navigate-on-click handler so the body is clickable, not
        // just the title-link inside.
        bindCardClick(entry);
        bindPostBodyToggle(entry);
        // Notify focus mode (and any other listener) that the body content
        // is now in the DOM. enterFocusMode listens for this on a not-yet-
        // expanded focus entry so it can uncap + re-scroll once the async
        // cross-tenant fetch lands. No-op when nothing is listening.
        try {
            entry.dispatchEvent(new CustomEvent('polis:body-injected'));
        } catch (e) { /* CustomEvent unsupported — focus body just stays capped */ }
    }

    /* ------------------------------------------------------------------
       Polymorphic item renderers — match the SSR partial shape from
       shapes/v4/stream-{post,comment,profile,mention,dm}.html exactly
       so the .entry DOM is uniform regardless of who created it
       (server-rendered for static siblings, controller-rendered for
       lazy-fetched bodies + filter results + DMs).

       Renderers use document.createElement / textContent for plain-text
       fields (title, author name, date, excerpt, etc.) so author-
       controlled metadata can never produce executable markup. Five
       call sites (body_html, bio_html, avatar_html, sender_avatar_html)
       use innerHTML — see the TRUST CHAIN comment at the first such
       site (renderComment, search "TRUST CHAIN") for the safety
       argument those calls depend on. If any link in that chain
       changes, audit every innerHTML site.
       ------------------------------------------------------------------ */

    // Defense-in-depth against author-supplied dangerous URL schemes
    // (javascript:, data:, vbscript:, file:) reaching <a href>. The publish-side
    // bluemonday sanitizer strips these from rendered body_html, but feed-cache
    // fields like TargetURL flow through el() directly without going through
    // markdown rendering, so a producer-side check here closes the gap.
    // Accepts http(s)://, mailto:, root-relative /, fragment #, query ?, and any
    // relative path with no scheme prefix.
    function isSafeHref(href) {
        if (typeof href !== 'string' || href === '') return false;
        return /^(?:https?:|mailto:|\/|#|\?|[^:\/?#]*(?:[\/?#]|$))/i.test(href);
    }

    function el(tag, opts) {
        var node = document.createElement(tag);
        if (opts) {
            if (opts.cls) node.className = opts.cls;
            if (opts.text !== undefined && opts.text !== null) node.textContent = String(opts.text);
            if (opts.href) {
                if (isSafeHref(opts.href)) node.setAttribute('href', opts.href);
            }
            if (opts.ariaHidden) node.setAttribute('aria-hidden', 'true');
            if (opts.attrs) {
                for (var k in opts.attrs) {
                    if (Object.prototype.hasOwnProperty.call(opts.attrs, k)) {
                        var v = opts.attrs[k];
                        if (k === 'href' && !isSafeHref(v)) continue;
                        node.setAttribute(k, v);
                    }
                }
            }
            if (opts.children) {
                for (var i = 0; i < opts.children.length; i++) {
                    if (opts.children[i]) node.appendChild(opts.children[i]);
                }
            }
        }
        return node;
    }

    function timelineDot() {
        return el('div', { cls: 'timeline-dot', ariaHidden: true });
    }

    // bodyRail wraps the gutter timeline dot together with the entry's body
    // element(s) in a relative, non-clipping .entry-rail. The dot anchors to
    // the body's FIRST TEXT LINE via CSS (.entry-rail > .timeline-dot), so a
    // single vertical contract holds across posts, comments, focus mode, and
    // the public stream — no per-context magic offsets. `dot` is the
    // timeline-dot node; remaining arguments are the body children appended
    // after it (e.g. the floated avatar, then the body). Falsy args skipped.
    function bodyRail(dot /*, ...children */) {
        var rail = el('div', { cls: 'entry-rail' });
        rail.appendChild(dot);
        for (var i = 1; i < arguments.length; i++) {
            if (arguments[i]) rail.appendChild(arguments[i]);
        }
        return rail;
    }

    // ── Avatar rendering ────────────────────────────────────────────────
    // Mirror of the Go side (render.AvatarHTML + avatarPatterns,
    // cli-go/pkg/render/page.go). The pattern SVG strings here are kept
    // BYTE-IDENTICAL to the Go map so a server-rendered avatar and a
    // client-rendered one paint the same pixels. cfg fields are snake_case
    // to match the server's avatar JSON ({bg,fg,border,border_w,pattern,
    // pattern_color}). Keep all three in step (Go map / this map / app.js
    // _avatarPatterns) when adding a pattern.
    var AVATAR_PATTERNS = {
        none: function () { return ''; },
        rings: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='14' cy='14' r='10' fill='none' stroke='" + c + "' stroke-width='1.5'/><circle cx='14' cy='14' r='5' fill='none' stroke='" + c + "' stroke-width='1'/></svg>"; },
        cross: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='4' y1='4' x2='24' y2='24' stroke='" + c + "' stroke-width='1.5'/><line x1='24' y1='4' x2='4' y2='24' stroke='" + c + "' stroke-width='1.5'/></svg>"; },
        grid: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='9' y1='0' x2='9' y2='28' stroke='" + c + "' stroke-width='0.8'/><line x1='19' y1='0' x2='19' y2='28' stroke='" + c + "' stroke-width='0.8'/><line x1='0' y1='9' x2='28' y2='9' stroke='" + c + "' stroke-width='0.8'/><line x1='0' y1='19' x2='28' y2='19' stroke='" + c + "' stroke-width='0.8'/></svg>"; },
        dots: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='7' cy='7' r='2' fill='" + c + "'/><circle cx='21' cy='7' r='2' fill='" + c + "'/><circle cx='14' cy='14' r='2' fill='" + c + "'/><circle cx='7' cy='21' r='2' fill='" + c + "'/><circle cx='21' cy='21' r='2' fill='" + c + "'/></svg>"; },
        stripes: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='-2' y1='6' x2='6' y2='-2' stroke='" + c + "' stroke-width='1.5'/><line x1='5' y1='13' x2='13' y2='5' stroke='" + c + "' stroke-width='1.5'/><line x1='12' y1='20' x2='20' y2='12' stroke='" + c + "' stroke-width='1.5'/><line x1='19' y1='27' x2='27' y2='19' stroke='" + c + "' stroke-width='1.5'/><line x1='26' y1='34' x2='34' y2='26' stroke='" + c + "' stroke-width='1.5'/></svg>"; },
        diamond: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><polygon points='14,4 24,14 14,24 4,14' fill='none' stroke='" + c + "' stroke-width='1.5'/></svg>"; },
        halves: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><rect x='0' y='14' width='28' height='14' fill='" + c + "' opacity='0.4'/></svg>"; }
    };

    // buildAvatar — DOM-builds a <span class="avatar-initial"> from an avatar
    // config, mirroring render.AvatarHTML. Built via DOM + style properties
    // (NOT innerHTML; TRUST CHAIN-safe — colors set through the CSSOM, the
    // pattern as a self-built base64 data-URI background). A pattern blanks the
    // initial. size > 0 sets inline w/h/line-height. Returns the element.
    function buildAvatar(cfg, initial, size) {
        var span = el('span', { cls: 'avatar-initial' });
        if (size) {
            span.style.width = size + 'px';
            span.style.height = size + 'px';
            span.style.lineHeight = size + 'px';
        }
        if (!cfg || !cfg.bg) {
            span.textContent = initial || '';
            return span;
        }
        span.style.backgroundColor = cfg.bg;
        if (cfg.fg) span.style.color = cfg.fg;
        if (cfg.border && cfg.border_w > 0) {
            span.style.border = cfg.border_w + 'px solid ' + cfg.border;
        }
        var hasPattern = false;
        if (cfg.pattern && cfg.pattern !== 'none' && cfg.pattern_color) {
            var gen = AVATAR_PATTERNS[cfg.pattern];
            if (gen) {
                var svg = gen(cfg.pattern_color);
                if (svg) {
                    span.style.backgroundImage = 'url(data:image/svg+xml;base64,' + btoa(svg) + ')';
                    span.style.backgroundSize = 'cover';
                    hasPattern = true;
                }
            }
        }
        span.textContent = hasPattern ? '' : (initial || '');
        return span;
    }

    // avatarFallback — deterministic hue + single initial for an author with
    // no avatar config. Mirrors app.js domainToAvatar (same hash → hue) so the
    // nav, the SPA, and the stream agree on a domain's fallback color. One
    // initial (not two) reads better in the 28px stream chip.
    function avatarFallback(domain) {
        if (!domain) return { color: 'hsl(0, 0%, 50%)', initial: '?' };
        var name = domain.replace(/^www\./, '').split('.')[0] || '';
        var initial = (name.charAt(0) || '?').toUpperCase();
        var hash = 0;
        for (var i = 0; i < domain.length; i++) {
            hash = domain.charCodeAt(i) + ((hash << 5) - hash);
        }
        var hue = ((hash % 360) + 360) % 360;
        return { color: 'hsl(' + hue + ', 35%, 55%)', initial: initial };
    }

    // ── Relative-time relativization ─────────────────────────────────────
    // FormatHumanDateTime (Go, template/engine.go) computes the relative
    // strings ("just now" / "N minutes ago" / "N hours ago") SERVER-SIDE at
    // render time. On a cached static page — e.g. a published tenant
    // index.html — that string FREEZES at render time and goes stale: a post
    // renders "just now" and still says "just now" an hour later, while the
    // live SPA (which refetches on every filter) shows the correct age. That
    // mismatch is exactly what a reader sees comparing discover.polis.pub
    // (static, "just now") against their own stream (live, "55 minutes ago").
    //
    // Fix: relativize on the CLIENT from the <time datetime="..."> ISO so every
    // view recomputes the age at display time, regardless of when the HTML was
    // rendered. Only the relative window (< 10h) is handled here — older stamps
    // return null and keep the server's absolute "Month D, YYYY · h:mma" text,
    // which sidesteps a UTC-vs-local-timezone mismatch between the SSR fallback
    // (formatted in UTC) and the client. Mirrors FormatHumanDateTime's bands
    // exactly so SSR'd and client-rendered entries read identically.
    function relativeTimeFromISO(iso) {
        if (!iso) return null;
        var t = new Date(iso);
        if (isNaN(t.getTime())) return null;
        var delta = Date.now() - t.getTime();
        if (delta < 0 || delta >= 10 * 3600 * 1000) return null;
        if (delta < 60 * 1000) return 'just now';
        var mins = Math.floor(delta / (60 * 1000));
        if (mins < 2) return '1 minute ago';
        if (mins < 60) return mins + ' minutes ago';
        var hrs = Math.floor(delta / (3600 * 1000));
        if (hrs < 2) return '1 hour ago';
        return hrs + ' hours ago';
    }

    // refreshRelativeTimes rewrites every entry timestamp carrying a parseable
    // datetime attr to its client-computed relative form. Run at init() (to
    // correct stale SSR'd values on a cached page) and on a 60s interval (so an
    // open tab self-updates "just now" → "1 minute ago" without a refetch).
    // Scoped to `.entry-date-time[datetime]`: the "last active" profile line and
    // recent-post-date carry no datetime attr, so they're untouched.
    function refreshRelativeTimes(scope) {
        var root = scope && scope.querySelectorAll ? scope : document;
        var nodes = root.querySelectorAll('.entry-date-time[datetime]');
        for (var i = 0; i < nodes.length; i++) {
            var rel = relativeTimeFromISO(nodes[i].getAttribute('datetime'));
            if (rel) nodes[i].textContent = rel;
        }
    }

    function entryShell(typeClass, meta, extraClasses) {
        // Common shell shared by all entry sub-types: <article> with the
        // unified entry classes + state classes + data attrs. Sub-types
        // append their own children.
        var classes = 'entry ' + typeClass + ' is-loaded';
        // Unread state — drives the timeline-dot's accent halo treatment
        // (stream.css .entry.has-unread .timeline-dot). Honors an
        // explicit meta.unread flag (DMs use this) and falls back to
        // "no read_at timestamp" for posts/comments/follows. Skip the
        // default-unread inference for entry types that don't carry
        // read-state today (focus/profile/mention) — those should stay
        // visually neutral.
        var unread = false;
        if (typeof meta.unread === 'boolean') {
            unread = meta.unread;
        } else if (meta.read_at !== undefined && meta.read_at === '' && !isPublicFocusSurface()) {
            // read_at-based unread inference is an OWNER-activity concept (the
            // dashboard feed's "what's new since I last looked"). The public
            // per-post stream (static-focus-and-siblings) has no per-visitor
            // read state, and its SSR baseline (stream.html / stream-post.html)
            // emits NO has-unread. The stream-items API serializes read_at as
            // "" (no omitempty) for these posts, so applying the inference only
            // on the JS-fetch path made freshly-paginated posts flash the
            // accent (pink) comment badge + timeline dot that the SSR'd focus
            // and siblings never show — the "N posts vs. /index.html" variance.
            // Gate it to non-public surfaces so the public stream stays
            // visually uniform with its SSR baseline.
            unread = true;
        }
        if (unread) classes += ' has-unread';
        if (extraClasses) classes += ' ' + extraClasses;
        var article = el('article', {
            cls: classes,
            attrs: { 'data-polis-entry-type': meta.type || typeClass.replace('entry--', '') },
        });
        if (meta.url) article.dataset.polisCanonicalUrl = meta.url;
        if (meta.id) article.dataset.polisEntryId = meta.id;
        // Total comment count (blessed + unblessed) — read by fetchFocusComment
        // to label the read-focus "see all N comments" CTA. omitempty means a
        // zero-count post leaves this unset (CTA falls back to a count-less form).
        if (meta.comment_count != null) article.dataset.polisCommentCount = meta.comment_count;
        return article;
    }

    function renderPost(meta) {
        // body.is-owner gates SPA-side behavior — owner-extras.js's
        // decoratePost is what runs for each entry on the dashboard
        // and conditionally adds entry--commentable for CROSS-author
        // posts (own posts skip the class so the timeline-dot doesn't
        // pose as a comment-add affordance on your own content).
        // On public pages decoratePost never runs, so we add the
        // class here for parity with the SSR'd stream-post.html (all
        // entries on a single-tenant public list are commentable by
        // a logged-in cross-visitor).
        var ownerMode = document.body.classList.contains('is-owner');
        var article = entryShell('entry--post', meta, ownerMode ? '' : 'entry--commentable');
        // Title-redundancy flag — server-side enrichment runs the same
        // StripLeadingTitleHeading + TitleStartsFirstSentence the SSR
        // path uses, so dynamically-rendered posts get the same title-
        // chrome suppression as static siblings. Stash on the dataset
        // so injectBody (called after lazy-fetch swaps excerpt → expanded)
        // can re-apply .is-redundant to the new title-link wrapper.
        var titleRedundant = !!meta.title_redundant;
        if (titleRedundant) {
            article.dataset.polisTitleRedundant = '1';
        }
        // Truncation marker: a cross-tenant post ships no body_html and a
        // truncated excerpt ("…"/"...") means the excerpt isn't the whole
        // post. Stamp it so isFocusModeEligible lets the entry enter read-
        // focus, where ensureFocusBody fetches the full body. Own posts ship
        // body_html (already is-expanded) so they skip this path.
        if (!meta.body_html && typeof meta.excerpt === 'string' && /(?:…|\.\.\.)\s*$/.test(meta.excerpt)) {
            article.dataset.polisTruncated = '1';
        }
        // entry-meta-line: date+time on left, byline · comment-indicator
        // grouped on right. Both byline-domain-link and entry-comments-
        // badge are siblings inside an .entry-meta-right wrapper with a
        // .entry-meta-sep ("·") between them — flex-distributed by the
        // meta-line's justify-content:space-between. The svg icon is
        // built via setAttribute (no innerHTML) so it stays out of the
        // TRUST CHAIN audit boundary.
        //
        // Comment count is always rendered (even at 0) so the indicator
        // reads as "this post has N comments" at a glance from anywhere
        // in the stream. Empty count just shows "0".
        //
        // Drafts skip both the comment badge and the byline.
        // You can't comment on your own unpublished draft (badge would
        // mislead), and the byline is always "me" (redundant). The
        // right-aligned DRAFT pill's role is covered by the
        // entry-rollover-cta pattern instead.
        var dateTime = el('time', { cls: 'entry-date-time', text: meta.published_human, attrs: meta.published ? { datetime: meta.published } : null });
        // Dot moved out of the meta-line into the body rail (below) so it
        // anchors to the first body line, not the date. Meta-line is date-only.
        var metaLine = el('div', { cls: 'entry-meta-line', children: [dateTime] });
        // Meta line is LEFT-clustered "date · 💬 count". The date sits in a
        // fixed-width slot (CSS --stream-date-slot) so the count column-locks
        // at a constant x down the timeline (server already strips the year).
        // The cross-author handle is no longer shown here — identity moved to
        // the floated avatar in the body (rollover reveals the handle). Drafts
        // skip the badge (you can't comment on your own unpublished draft).
        // Comment indicator — moved BELOW the post (left-aligned with the date
        // and body). Built here but
        // appended to the body rail AFTER the body (see below), so it sits
        // under the body's content AND the "show more" toggle, and rides down
        // when the post expands. Drafts skip it (you can't comment on your own
        // unpublished draft). .add-plus stays in the DOM so the hover badge-+
        // swap (entry-rollover-cta) keeps working unchanged.
        var commentBadge = null;
        if (!meta.draft) {
            var commentCount = (meta.comment_count != null) ? meta.comment_count : 0;
            commentBadge = el('a', {
                cls: 'entry-comments-badge',
                href: (meta.url || '#') + '#comments',
                children: [
                    buildCommentSVG(),
                    buildAddCommentSVG(),
                    el('span', { cls: 'count', text: formatCommentCount(commentCount) }),
                ],
            });
        }
        // Handle in the header row (right side), revealed on entry hover/focus
        // via CSS. It lives in the meta line — NOT inside the avatar — so the
        // body's overflow cap can't clip it and it has room under the header.
        // Links to the post; cross-author only (own author has no avatar).
        if (meta.author_avatar) {
            metaLine.appendChild(el('a', {
                cls: 'entry-handle',
                href: meta.url || '#',
                text: meta.author_domain || '',
            }));
        }
        // Excerpt-form title: .is-redundant hides the h3 via CSS when the
        // server flagged the body as starting with the title text. The
        // excerpt below is a 200-char truncation of that same body, so
        // hiding the h3 doesn't lose content — the user reads it once
        // in the natural prose flow instead of twice.
        var titleH3 = el('h3', {
            cls: titleRedundant ? 'entry-title is-redundant' : 'entry-title',
            text: meta.title,
        });
        var body = el('a', {
            cls: 'entry-body entry-body--excerpt',
            href: meta.url || '#',
            children: [
                titleH3,
                el('p', { cls: 'entry-excerpt', text: meta.excerpt }),
            ],
        });
        article.appendChild(metaLine);
        // Cross-author avatar: a 28px chip floated top-right INSIDE the post
        // body so the text WRAPS around it. Lives in the body rail as a sibling
        // float before the body — which wraps for the excerpt form (the excerpt
        // body is a plain block, not a BFC). For the EXPANDED form, injectBody
        // MOVES this chip into .entry-content (a float inside the capped
        // overflow:hidden BFC wraps; a sibling float would sit detached in the
        // gutter). The chip is a LINK to the post (the handle in the header is
        // the other link); it carries no focus-trigger class, so bindCardClick
        // lets it navigate while a body click opens focus mode. Omitted for
        // own-author entries.
        var avatarLink = null;
        if (meta.author_avatar) {
            var fb = avatarFallback(meta.author_domain || '');
            avatarLink = el('a', {
                cls: 'entry-avatar-link',
                href: meta.url || '#',
                attrs: { 'aria-label': meta.author_domain || 'author' },
                children: [ buildAvatar(meta.author_avatar, fb.initial, 28) ],
            });
        }
        // Body rail: gutter dot + (avatar) + body + (comment badge below). The
        // dot anchors to the body's first line; the badge sits below the body
        // (see .entry-rail / .entry-comments-badge in stream.css).
        article.appendChild(bodyRail(timelineDot(), avatarLink, body, commentBadge));
        // No inline comment preview in the default/collapsed stream. The
        // count badge alone conveys "this post has N comments"; entering
        // read-focus lazily fetches exactly ONE comment (the latest, blessed
        // or not) via fetchFocusComment. This keeps the timeline scannable
        // and drives the full thread to the origin site rather than turning
        // the stream into a comment viewer.
        // Server-rendered body HTML available (drafts ship it
        // synchronously; future enrichment may ship it for cross-tenant
        // posts too). Skip the lazy-fetch path and inject directly so
        // the entry shows formatted markdown instead of a plain-text
        // excerpt. Defer to next tick so the entry is in the DOM and
        // bindPostBodyToggle can measure overflow.
        // Drafts render the rendered HTML as the preview (matches user
        // expectation — markdown syntax in the excerpt would otherwise
        // read as raw `# Header` strings, which is noise).
        // CSS line-clamps .entry.is-draft .entry-content so the height
        // stays excerpt-shaped, and owner-extras attaches the click-
        // to-edit handler at the article level so it survives the
        // excerpt → expanded body swap.
        if (meta.body_html) {
            // Claim is-expanded + skip prefetch IMMEDIATELY,
            // before the deferred injectBody fires. Without these two
            // markers the prefetch observer (started in appendEntry →
            // observeEntryForPrefetch) saw a freshly-rendered article
            // without is-expanded yet and added it to the observation
            // set. Later, when the entry crossed the prefetch root-
            // margin (i.e., once the user had scrolled at some point in
            // the session), fetchAndExpand fired against /posts/.../html
            // — which the localhost server's spaHandler resolves to the
            // SPA shell, NOT the rendered post HTML (the post file lives
            // on disk under dataDir, not in the embedded FS the
            // spaHandler checks). fetchBody parsed the shell, found no
            // focus-content marker, returned null, and fetchAndExpand
            // tagged the entry with is-404. Symptom: post flashes
            // briefly then disappears. Setting these markers up front
            // matches the draft path's intent for the same reason.
            article.classList.add('is-expanded');
            article.dataset.polisNoPrefetch = '1';
            setTimeout(function () { injectBody(article, meta.body_html); }, 0);
        }
        // Drafts ship body_html too (handleStreamItemsDrafts) so they
        // already pick up the no-prefetch + is-expanded markers above.
        // The marker is left on the entry regardless of whether body_html
        // landed, since draft URLs are SPA deep-links (/_/posts/drafts/
        // <id>) that would resolve to the SPA shell anyway.
        if (meta.draft) {
            article.dataset.polisNoPrefetch = '1';
        }
        return article;
    }

    // fetchFocusComment lazily loads the single most-recent comment (blessed
    // or not) for a post when the reader enters focus mode, builds the panel,
    // and animates it open. One fetch per entry (guarded by a dataset flag);
    // same-origin call to the webapp's focus-comment endpoint (no CORS, unlike
    // the cross-origin body prefetch). Fail-soft: any error or no-comment
    // result leaves focus mode open with no comment panel.
    function fetchFocusComment(entry) {
        if (!entry) return;
        if (entry.dataset.polisFocusCommentFetched === '1') {
            // Already resolved once. Re-show the cached comment + (re)open.
            showFocusComment(entry);
            if (entry.querySelector('.entry-comments-panel')) openCommentsPanel(entry);
            return;
        }
        var postURL = entry.dataset.polisCanonicalUrl;
        if (!postURL) return;
        entry.dataset.polisFocusCommentFetched = '1';
        fetch('/api/v1/stream/focus-comment?url=' + encodeURIComponent(postURL), { credentials: 'same-origin' })
            .then(function (resp) { return resp.ok ? resp.json() : null; })
            .then(function (data) {
                if (!data || !data.has_comment || !data.comment) return;
                // The reader may have exited focus before the fetch resolved.
                if (!entry.classList.contains('is-focus-mode')) return;
                setFocusComment(entry, data.comment);
                // Render into the slot (showFocusComment no-ops if the owner
                // SPA's editor already occupies it — the editor's close path
                // will re-show this cached comment).
                showFocusComment(entry);
                openCommentsPanel(entry);
            })
            .catch(function () { /* fail-soft: no comment panel */ });
    }

    // ensureFocusCommentSlot returns the focus entry's single swappable comment
    // slot, creating the panel + (empty) slot if they don't exist yet. The panel
    // sits in the rail just after the comment badge so it shares the indicator's
    // x. Used by the comment fetch (to show the latest comment) AND by the owner
    // SPA's in-focus comment editor (which mounts the editor into the SAME slot).
    // Does NOT open the panel — the caller animates it via openCommentsPanel.
    function ensureFocusCommentSlot(entry) {
        if (!entry) return null;
        var panel = entry.querySelector('.entry-comments-panel.entry-focus-comment');
        if (!panel) {
            panel = el('div', { cls: 'entry-comments-panel entry-focus-comment' });
            panel.appendChild(el('div', { cls: 'entry-focus-comment-slot' }));
            var body = entry.querySelector('.entry-body') || entry.querySelector('.entry-content');
            if (body && body.parentNode) {
                var anchor = body.parentNode.querySelector(':scope > .entry-comments-badge') || body;
                body.parentNode.insertBefore(panel, anchor.nextSibling);
            } else {
                entry.appendChild(panel);
            }
        }
        return panel.querySelector('.entry-focus-comment-slot');
    }

    // setFocusComment caches a comment payload on the entry so showFocusComment
    // can (re)render it — e.g. the owner SPA stamps the just-submitted comment
    // here so it fills the slot when the editor closes. Pass null to clear.
    function setFocusComment(entry, comment) {
        if (entry) entry._polisFocusComment = comment || null;
    }

    // showFocusComment renders the cached comment into the slot, or empties the
    // slot when there's none. Clobber-guard: if the slot is occupied by the
    // owner SPA's comment editor (data-polis-pinned="comment-editor"), leave it
    // — the editor's close path calls this again once it removes itself.
    function showFocusComment(entry) {
        if (!entry) return;
        var slot = ensureFocusCommentSlot(entry);
        if (!slot) return;
        if (slot.querySelector('[data-polis-pinned="comment-editor"]')) return;
        slot.textContent = '';
        if (entry._polisFocusComment) {
            slot.appendChild(buildFocusCommentCard(entry._polisFocusComment));
        }
    }

    // buildFocusCommentCard builds the single-comment card with the SAME meta
    // layout as the post entry: a small timeline dot on the
    // rail aligned to the comment date, the date on the LEFT (non-italic, like
    // the post date), and the commenter handle static on the RIGHT edge
    // (non-italic mono). Box model mirrors .comment-attached so the shared
    // .timeline-dot--small offset lands the dot on the rail.
    function buildFocusCommentCard(comment) {
        // Mirror the SSR canonical blessed-comment.html EXACTLY so read-focus
        // and the per-post page render identically (one .comment-attached--
        // canonical style for both): date on the LEFT + handle static on the
        // RIGHT in the meta line, avatar floated INSIDE .comment-body with the
        // body text wrapping around it.
        var card = el('div', { cls: 'comment-attached comment-attached--canonical' });
        var domain = comment.author_domain || comment.author_name || '';
        var meta = el('div', { cls: 'comment-meta-line' });
        meta.appendChild(el('time', {
            cls: 'comment-date-time',
            text: comment.published_human || '',
        }));
        if (domain) {
            // is-shown: statically visible (not hover-gated). The
            // .entry-handle.is-shown rule is in every stream.css version, so the
            // byline survives a stale-CSS deploy — same robustness reason the
            // SSR blessed-comment.html snippet carries the class.
            meta.appendChild(el('a', {
                cls: 'entry-handle is-shown',
                href: comment.url || '#',
                text: domain,
            }));
        }
        card.appendChild(meta);
        // Commenter avatar — a rail sibling of the comment body (NOT inside it),
        // so it can be absolutely positioned top-right with a reserved body
        // column (same cross-author chip renderPost builds; styled by
        // .comment-attached .entry-rail > .entry-avatar-link in stream.css).
        // buildAvatar consumes the server-resolved author_avatar config.
        var commentAvatar = null;
        if (comment.author_avatar) {
            var fb = avatarFallback(domain);
            commentAvatar = el('a', {
                cls: 'entry-avatar-link',
                href: comment.url || '#',
                attrs: { 'aria-label': domain || 'author' },
                children: [buildAvatar(comment.author_avatar, fb.initial, 28)],
            });
        }
        var body = el('div', { cls: 'comment-body' });
        // ── TRUST CHAIN ── content_html is server-rendered through
        // render.LoadLocalCommentContent (own) or render.LoadOrFetchCommentHTML
        // (cross-tenant) — both goldmark + bluemonday sanitized. The server-side
        // sanitization is the load-bearing guarantee here.
        // ── END TRUST CHAIN ──
        if (comment.content_html) {
            body.insertAdjacentHTML('beforeend', comment.content_html);
        }
        // Body rail: small timeline dot anchored to the comment body's first
        // line + the chip + the comment body. Mirrors the SSR canonical
        // blessed-comment.html exactly.
        card.appendChild(bodyRail(
            el('div', { cls: 'timeline-dot timeline-dot--small', attrs: { 'aria-hidden': 'true' } }),
            commentAvatar,
            body
        ));
        return card;
    }

    // Comment-badge svg, built via DOM APIs (no innerHTML). Single path
    // matches the SSR'd markup in stream-post.html.
    // formatCommentCount — keeps the meta-line badge slot compact even
    // on posts with hundreds of comments. 0-999 render as literal
    // numbers; >=1000 collapses to "1k+", "2k+", … (no more than 3
    // chars including the "+"). The 999 threshold is the sweet spot
    // for matching the meta-line typography — 3-digit numbers fit
    // cleanly in the badge's min-width slot.
    function formatCommentCount(n) {
        n = Number(n) || 0;
        if (n < 1000) return String(n);
        var k = Math.floor(n / 1000);
        return k + 'k+';
    }

    function buildCommentSVG() {
        var SVG_NS = 'http://www.w3.org/2000/svg';
        var svg = document.createElementNS(SVG_NS, 'svg');
        svg.setAttribute('viewBox', '0 0 24 24');
        svg.setAttribute('aria-hidden', 'true');
        var path = document.createElementNS(SVG_NS, 'path');
        path.setAttribute('d', 'M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z');
        svg.appendChild(path);
        return svg;
    }

    // Add-comment glyph — the speech bubble with a "+" inside. This is the
    // hover-state icon for the below-post comment badge on commentable posts
    // (replaces the timeline-dot's old rollover icon). Same bubble path as
    // buildCommentSVG plus two crossing lines; class .icon-add-comment lets the
    // CSS stack it over the rest icon and crossfade on hover.
    function buildAddCommentSVG() {
        var SVG_NS = 'http://www.w3.org/2000/svg';
        var svg = document.createElementNS(SVG_NS, 'svg');
        svg.setAttribute('viewBox', '0 0 24 24');
        svg.setAttribute('aria-hidden', 'true');
        svg.setAttribute('class', 'icon-add-comment');
        var path = document.createElementNS(SVG_NS, 'path');
        path.setAttribute('d', 'M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z');
        svg.appendChild(path);
        var v = document.createElementNS(SVG_NS, 'line');
        v.setAttribute('x1', '12'); v.setAttribute('y1', '7.5');
        v.setAttribute('x2', '12'); v.setAttribute('y2', '12.5');
        svg.appendChild(v);
        var h = document.createElementNS(SVG_NS, 'line');
        h.setAttribute('x1', '9.5'); h.setAttribute('y1', '10');
        h.setAttribute('x2', '14.5'); h.setAttribute('y2', '10');
        svg.appendChild(h);
        return svg;
    }

    // Speech-bubble glyph for the comment-meta-line in entry--comment-thread
    // entries. Same path as buildCommentSVG; differentiated by class so CSS
    // can size + tint independently of the entry-comments-badge SVG.
    function commentMetaIcon() {
        var svg = buildCommentSVG();
        svg.setAttribute('class', 'comment-meta-icon');
        return svg;
    }

    // Title-dedup runs server-side in handlers_stream.go: the enrichment
    // builder applies render.StripLeadingTitleHeading (drops a leading
    // ATX heading matching the title) and render.TitleStartsFirstSentence
    // (flags posts whose first prose sentence begins with the title) on
    // the raw post markdown before rendering to HTML. Doing it on raw
    // markdown matters because emphasis markers (**bold**) survive in
    // the title but get stripped from rendered HTML — comparing against
    // markdown keeps both sides aligned. The flag rides the API response
    // as `target_post_title_redundant` and renderCommentThread honors it
    // by adding .is-redundant to the title-link wrapper.

    // type=comments thread-entry: the post that handle commented on
    // (full body inline) followed by the latest comment by handle on
    // that post. Two timeline dots on the SAME main bar — the post's
    // standard 8px dot at its meta-line + a smaller 5px dot at the
    // comment's italic metadata row. No inner vertical bar.
    //
    // Server enriches each comment item with target_post_* fields and
    // comment_body_html. Missing fields (failed remote fetch) degrade
    // gracefully — we render with whatever's present and a placeholder
    // for the post body.
    //
    // ── TRUST CHAIN — same chain as renderComment's body_html (search
    // for "TRUST CHAIN" in this file). The HTML strings injected via
    // innerHTML here are produced by the Go render.MarkdownToHTML
    // pipeline (goldmark + UGC sanitizer) on the server, then carried
    // verbatim. Same four assumptions apply. ── END TRUST CHAIN ──
    function renderCommentThread(meta) {
        var article = entryShell('entry--comment-thread', meta);
        // Per-view avatar placement — see postFocus below for the full rule.
        // Short version: exactly one avatar per entry, on the focused element.
        // The commenter is the focus in default comments views; the post
        // author is the focus everywhere else (activity / posts / fromMe).
        var commentsView = (filterType === 'comments');
        // POST takes precedence vs COMMENT takes precedence:
        //   - activity view ("all activity ...", "all posts ..."): post is the
        //     subject → post avatar, comment shows handle statically.
        //   - comments view ("all comments from <network>/<all-polis>"): comment
        //     is the subject → commenter avatar, post shows just its handle.
        //   - EXCEPTION: comments view "from me" (filterScope==='me'). The
        //     commenter is the viewer; the viewer's own identity is suppressed
        //     ("I know who I am"). Fall back to post-takes-precedence so the
        //     relevant identity (the post the viewer is commenting on) reads
        //     at a glance.
        var fromMe = commentsView && filterScope === 'me';
        var postFocus = !commentsView || fromMe;

        // Post region: meta-line (post date + author domain) + title
        // link + full body HTML.
        var postMetaLine = el('div', { cls: 'entry-meta-line' });
        // Dot moved out of the meta-line into the post-region body rail (below)
        // so it anchors to the post body's first line, not the date.
        // Date+time wrapped in the same target_url link as the title, so a
        // post whose title is hidden via .is-redundant still has a clickable
        // affordance pointing at the original post (and so users have a
        // second target if they aim at the date out of habit).
        var timeEl = el('time', {
            cls: 'entry-date-time',
            text: meta.target_post_published_human || '',
        });
        if (meta.target_url) {
            postMetaLine.appendChild(el('a', {
                cls: 'entry-date-time-link',
                href: meta.target_url,
                children: [timeEl],
            }));
        } else {
            postMetaLine.appendChild(timeEl);
        }
        // Post author identity in the header, linking to the POST URL.
        // Post-takes-precedence (activity / fromMe): the post has the avatar
        // (added below), so its handle is hover-revealed like a regular post.
        // Comment-takes-precedence (default comments view): static text handle
        // on the right (the post is context).
        if (meta.target_post_author_domain) {
            postMetaLine.appendChild(postFocus ? el('a', {
                cls: 'entry-handle',
                href: meta.target_url || '#',
                text: meta.target_post_author_domain,
            }) : el('a', {
                cls: 'byline-domain-link',
                href: meta.target_url || '#',
                children: [el('span', {
                    cls: 'byline-domain',
                    text: meta.target_post_author_domain,
                })],
            }));
        }

        var postBody = el('div', { cls: 'entry-body entry-body--expanded' });
        if (meta.target_url || meta.target_post_title) {
            var titleLink = el('a', {
                cls: 'entry-title-link',
                href: meta.target_url || '#',
                children: [el('h3', {
                    cls: 'entry-title',
                    text: meta.target_post_title || '(untitled)',
                })],
            });
            postBody.appendChild(titleLink);
        }
        var postContent = el('div', { cls: 'entry-content' });
        if (meta.target_post_body_html) {
            postContent.innerHTML = meta.target_post_body_html;
            // Server-supplied flag: hide title chrome when body's first
            // sentence absorbs the title (see comment block above).
            if (meta.target_post_title_redundant && titleLink && titleLink.classList) {
                titleLink.classList.add('is-redundant');
            }
        } else {
            postContent.appendChild(el('p', {
                cls: 'entry-content--unavailable',
                text: '(post body unavailable)',
            }));
        }
        // Post-takes-precedence (activity / fromMe): the POST author's avatar —
        // a rail sibling (NOT inside the content), so it gets the same
        // reserved-column treatment as a regular post chip (absolute top-right
        // + reserved body gutter; see stream.css). Default comments view: no
        // post avatar — the avatar lives on the commenter instead.
        var postAvatar = null;
        if (postFocus && meta.target_post_author_avatar) {
            var pfb = avatarFallback(meta.target_post_author_domain || '');
            postAvatar = el('a', {
                cls: 'entry-avatar-link',
                href: meta.target_url || '#',
                attrs: { 'aria-label': meta.target_post_author_domain || 'author' },
                children: [ buildAvatar(meta.target_post_author_avatar, pfb.initial, 28) ],
            });
        }
        postBody.appendChild(postContent);
        // Long-post collapse toggle for the post region. Same markup as
        // .entry--post (see stream-post.html); bindPostBodyToggle measures
        // overflow against the 50vh cap and only unhides the button when
        // the cap is actually exceeded — short bodies stay non-interactive.
        // Without this, posts taller than 50vh in this view had no way to
        // expand other than navigating away.
        postBody.appendChild(el('button', {
            cls: 'post-body-toggle',
            text: 'show more',
            attrs: { type: 'button', 'aria-expanded': 'false', hidden: '' },
        }));

        // Comment region — the FOCUS of the comments view. Framed card (left
        // accent + tint, see stream.css). The commenter's identity is the
        // floated 24px avatar (rollover handle, links to the COMMENT's URL);
        // the header carries just the time. The post above is identified by
        // its author handle (text, no avatar), so the one face per entry is
        // the commenter. Built from server-enriched meta.author_avatar.
        var commentBlock = el('div', { cls: 'comment-attached' });
        // Header (full width): time on the left, commenter handle on the right.
        // The element WITHOUT the avatar shows the handle STATICALLY; the one
        // WITH it reveals on hover. Post-takes-precedence → comment has no
        // avatar → static handle (is-shown). Default comments view → comment
        // has the avatar → hover handle. fromMe → suppress the handle entirely
        // (the commenter is the viewer; "I know who I am"). Comes BEFORE the
        // floated avatar so it isn't shrunk.
        var commentMeta = el('div', { cls: 'comment-meta-line' });
        commentMeta.appendChild(el('time', {
            cls: 'comment-date-time',
            text: meta.published_human || '',
        }));
        if (meta.author_domain && !fromMe) {
            commentMeta.appendChild(el('a', {
                cls: postFocus ? 'entry-handle is-shown' : 'entry-handle',
                href: meta.url || '#',
                text: meta.author_domain || '',
            }));
        }
        commentBlock.appendChild(commentMeta);
        // Default comments view only: commenter avatar floats in the card (the
        // focus), so the comment body wraps around it. Post-takes-precedence
        // (activity / fromMe): no commenter avatar.
        var commentAvatar = null;
        if (!postFocus && meta.author_avatar) {
            var cfb = avatarFallback(meta.author_domain || '');
            commentAvatar = el('a', {
                cls: 'entry-avatar-link',
                href: meta.url || '#',
                attrs: { 'aria-label': meta.author_domain || 'author' },
                children: [ buildAvatar(meta.author_avatar, cfb.initial, 28) ],
            });
        }
        var commentBody = el('div', { cls: 'comment-body' });
        if (meta.comment_body_html) {
            commentBody.innerHTML = meta.comment_body_html;
        } else if (meta.excerpt) {
            commentBody.textContent = meta.excerpt;
        }
        // Body rail: the small timeline dot anchored to the comment body's
        // first line (same contract as the post dot — see bodyRail / the
        // .entry-rail rules in stream.css). The dot marks the comment's place
        // on the timeline scan-axis in every view (activity and comments).
        commentBlock.appendChild(bodyRail(
            el('div', { cls: 'timeline-dot timeline-dot--small', attrs: { 'aria-hidden': 'true' } }),
            commentAvatar,
            commentBody
        ));

        article.appendChild(postMetaLine);
        // Post-region body rail: the big post dot anchored to the post body's
        // first line (the post-region rail is a DIRECT child of the entry, so
        // .entry--comment-thread > .entry-rail in stream.css carries the
        // post-body anchor; the comment rail below is nested and carries the
        // comment-body anchor instead).
        article.appendChild(bodyRail(timelineDot(), postAvatar, postBody));
        article.appendChild(commentBlock);
        return article;
    }

    function renderComment(meta) {
        var article = entryShell('entry--comment', meta);
        var entryMeta = el('div', {
            cls: 'entry-meta',
            children: [
                timelineDot(),
                el('time', { cls: 'entry-time', text: meta.date_human }),
            ],
        });
        var byline = el('div', { cls: 'entry-byline' });
        var authorLink = el('a', {
            cls: 'byline-author',
            href: meta.author_domain ? 'https://' + meta.author_domain + '/' : '#',
            text: meta.author_name,
        });
        byline.appendChild(authorLink);
        byline.appendChild(el('span', { cls: 'byline-domain', text: meta.author_domain }));
        if (meta.reply_target_title) {
            var ctx = el('span', { cls: 'entry-context', text: 're: ' });
            ctx.appendChild(el('a', { href: meta.reply_target_url || '#', text: meta.reply_target_title }));
            byline.appendChild(ctx);
        }
        var commentBody = el('div', { cls: 'comment-body' });
        // ── TRUST CHAIN — applies to every innerHTML site in this file ──
        // The HTML strings injected here (body_html / bio_html / avatar_html
        // / sender_avatar_html) are author-controlled but safe to assign
        // via innerHTML because of four assumptions:
        //   1. Sanitization at publish time. cli-go/pkg/render runs author
        //      markdown through bluemonday (UGC policy) before the bytes
        //      ever leave the publishing tenant. Script tags, on-* event
        //      attrs, and javascript: hrefs are stripped at the source.
        //   2. Tamper-evident in transit. The bytes are part of the post
        //      body that the author signed at publish time. The DS verifies
        //      signatures on ingestion; readers re-verifying (deferred
        //      hardening) would catch any modification.
        //   3. No further sanitization on the read side, by design. Once
        //      the body is verified, the reader trusts it verbatim.
        //   4. DM bodies (the dm/ renderer's body_html) are decrypted
        //      locally from owner-encrypted disk storage. The decryption
        //      authority IS the trust boundary; the plaintext is what
        //      the owner originally composed and (per the DM protocol)
        //      passed through the same sanitizer chain.
        // If ANY of these assumptions changes (sanitizer bypass, sign-
        // verify bypass, alternate render path that skips bluemonday,
        // DM plaintext sourced from an untrusted layer), audit every
        // innerHTML call site in this file before shipping.
        // ── END TRUST CHAIN ──
        if (meta.body_html) {
            commentBody.innerHTML = meta.body_html;
        } else if (meta.body_text) {
            commentBody.textContent = meta.body_text;
        }
        var body = el('div', { cls: 'entry-body', children: [byline, commentBody] });
        article.appendChild(entryMeta);
        article.appendChild(body);
        return article;
    }

    // Profile entry renderer.
    //
    // Shape: standard entry-meta-line (last-active + handle byline),
    // entry-title with display_name, profile-state-line with a passive
    // follow-state + relationship summary, an optional .recent-post-
    // attached block showing the author's most-recent post, and an
    // entry-actions container that the rollover-CTA layer (in
    // owner-extras.js / stream.css §entry-rollover-cta) populates with
    // Follow / Unfollow buttons on hover.
    //
    // Expected meta fields (emitted by buildProfilesList in
    // webapp/internal/server/handlers_profiles.go):
    //   id, type ("profile"), author_domain, author_url,
    //   display_name, following (bool), relationship (enum:
    //   mutual | follows-you | you-follow | none), last_active,
    //   published_human ("2h ago"), recent_post { title, url,
    //   excerpt, published_human }
    function renderProfile(meta) {
        var article = entryShell('entry--profile', meta);
        // Preserve key fields on the article element for the CTA layer
        // (owner-extras' rollover handler reads these to drive
        // POST/DELETE /api/following). Stored on dataset rather than
        // a hidden field so the values survive re-renders cleanly.
        if (meta.author_domain) article.dataset.polisProfileDomain = meta.author_domain;
        if (meta.author_url) article.dataset.polisProfileUrl = meta.author_url;
        if (typeof meta.following === 'boolean') {
            article.dataset.polisProfileFollowing = meta.following ? '1' : '0';
        }

        // Meta-line: "last active <relative>" on the left, follow-state
        // indicator on the right (where other entry types show a handle
        // byline). Profile entries don't need a handle byline since the
        // handle is the entry title — surfacing the follow-state in
        // that slot avoids duplication and surfaces the more useful
        // signal. The combined label captures both the binary follow
        // direction AND the bilateral signal: mutual / follows you /
        // following / not following.
        var dateText = meta.published_human ? ('last active ' + meta.published_human) : '';
        var isFollowing = !!meta.following;
        var statusLabel;
        switch (meta.relationship) {
            case 'mutual':       statusLabel = 'mutual'; break;
            case 'follows-you':  statusLabel = 'follows you'; break;
            case 'you-follow':   statusLabel = 'following'; break;
            default:             statusLabel = isFollowing ? 'following' : 'not following';
        }
        var entryMeta = el('div', {
            cls: 'entry-meta-line',
            children: [
                timelineDot(),
                el('span', { cls: 'entry-date-time', text: dateText }),
            ],
        });
        // Follow-state moves INLINE after the date (with a · separator). It
        // used to sit flush-right, but the avatar now occupies the top-right
        // reserved column. When there's no date, lead with the status (no
        // dangling separator).
        if (statusLabel) {
            if (dateText) {
                entryMeta.appendChild(el('span', { cls: 'entry-meta-sep', text: '·' }));
            }
            entryMeta.appendChild(el('span', {
                cls: 'entry-byline follow-state ' +
                     (isFollowing ? 'is-following' : 'is-not-following'),
                text: statusLabel,
            }));
        }
        article.appendChild(entryMeta);

        // Author avatar — the same 28px chip the stream posts show, pinned to
        // the top-right reserved column (positioned in CSS via the
        // --entry-avatar-* vars). Links to the author's site. Breaks up the
        // directory page visually. Server always supplies a config (custom or
        // a deterministic hue fallback) for these cross-author rows.
        if (meta.author_avatar) {
            var fb = avatarFallback(meta.author_domain || '');
            article.appendChild(el('a', {
                cls: 'entry-avatar-link',
                href: meta.author_url || '#',
                attrs: { 'aria-label': meta.author_domain || 'author' },
                children: [ buildAvatar(meta.author_avatar, fb.initial, 28) ],
            }));
        }

        // Title: display name when available, otherwise fall back to
        // the handle (matches the by-name sort key fallback).
        var titleText = meta.display_name || meta.author_domain || '';
        article.appendChild(el('h3', { cls: 'entry-title', text: titleText }));

        // Recent-post-attached: sunset-tinted preview of the author's
        // most-recent post, mirroring the .comment-attached chrome but
        // with --color-follow-accent. Click-to-expand wiring lives in
        // owner-extras.js.
        if (meta.recent_post && (meta.recent_post.title || meta.recent_post.url)) {
            var rp = meta.recent_post;
            var attached = el('div', { cls: 'recent-post-attached' });
            if (rp.url) attached.dataset.polisRecentPostUrl = rp.url;

            var metaLine = el('div', { cls: 'recent-post-meta-line' });
            metaLine.appendChild(el('span', { cls: 'recent-post-label', text: 'Most recent post' }));
            if (rp.published_human) {
                metaLine.appendChild(el('span', { cls: 'recent-post-date', text: rp.published_human }));
            }
            attached.appendChild(metaLine);

            if (rp.title) {
                var titleRow = el('div', { cls: 'recent-post-title' });
                titleRow.appendChild(document.createTextNode(rp.title));
                titleRow.appendChild(el('span', { cls: 'expand-chevron', text: '›' })); // ›
                attached.appendChild(titleRow);
            }
            if (rp.excerpt) {
                attached.appendChild(el('p', { cls: 'recent-post-excerpt', text: rp.excerpt }));
            }
            article.appendChild(attached);
        }

        // Rollover-CTA slot. Hidden at rest; revealed on .entry:hover
        // via the existing .entry-actions CSS. owner-extras.js populates
        // the button (Follow / Unfollow with the right variant) and
        // wires the click handler.
        article.appendChild(el('div', { cls: 'entry-actions' }));

        return article;
    }

    function renderMention(meta) {
        var article = entryShell('entry--mention', meta);
        var entryMeta = el('div', {
            cls: 'entry-meta',
            children: [
                timelineDot(),
                el('time', { cls: 'entry-time', text: meta.date_human }),
            ],
        });
        var byline = el('div', {
            cls: 'entry-byline',
            children: [
                el('span', { cls: 'byline-author', text: meta.source_author_name }),
                el('span', { cls: 'byline-domain', text: meta.source_author_domain }),
            ],
        });
        var headline = el('div', { cls: 'mention-headline' });
        headline.appendChild(document.createTextNode('mentioned you in '));
        headline.appendChild(el('strong', { text: meta.source_title }));
        var excerpt = el('p', { cls: 'mention-excerpt', text: meta.excerpt });
        var body = el('a', {
            cls: 'entry-body',
            href: meta.source_url || '#',
            children: [byline, headline, excerpt],
        });
        article.appendChild(entryMeta);
        article.appendChild(body);
        return article;
    }

    function renderDM(meta) {
        // Inbox row — one entry per conversation. Layout mirrors
        // renderPost: entry-meta-line with timeline-dot + date on the
        // left and a byline-domain-link on the right. Earlier versions
        // tucked the byline under the date and rendered both sender_name
        // and sender_domain — which on accounts without a display name
        // produced a doubled handle ("alice.polis.pub alice.polis.pub").
        // Sticking to sender_domain matches the post entry's
        // author_domain convention.
        var extraClasses = meta.unread ? 'has-unread' : '';
        var article = entryShell('entry--dm', meta, extraClasses);
        if (meta.conversation_id) article.dataset.polisConversationId = meta.conversation_id;
        var dateTime = el('time', {
            cls: 'entry-date-time',
            text: meta.published_human || meta.date_human || '',
            attrs: meta.published ? { datetime: meta.published } : null,
        });
        var metaLine = el('div', { cls: 'entry-meta-line', children: [timelineDot(), dateTime] });
        if (meta.sender_domain) {
            var rightGroup = el('span', { cls: 'entry-meta-right' });
            rightGroup.appendChild(el('a', {
                cls: 'byline-domain-link',
                href: 'https://' + meta.sender_domain + '/',
                children: [el('span', {
                    cls: 'byline-domain',
                    text: meta.sender_domain,
                })],
            }));
            metaLine.appendChild(rightGroup);
        }
        var dmBody = el('div', { cls: 'dm-body' });
        if (meta.body_html) {
            dmBody.innerHTML = meta.body_html;
        } else if (meta.body_text) {
            dmBody.textContent = meta.body_text;
        }
        var body = el('div', { cls: 'entry-body', children: [dmBody] });
        article.appendChild(metaLine);
        article.appendChild(body);
        return article;
    }

    // renderDMMessage — thread-mode DM renderer. One entry per message in
    // a single conversation. is_mine flag from the server drives the
    // indent + mint-tint styling (CSS rules in stream.css under
    // .entry--message.is-mine). meta.sender_name is "you" for own
    // messages, the peer's domain for theirs.
    //
    // Differs from renderDM (inbox mode):
    //   - one entry per MESSAGE, not per conversation
    //   - no preview-truncation — full body shown
    //   - has-unread + unread-count chrome irrelevant (per-message read
    //     state isn't tracked)
    //   - title is absent (the byline + date are the message's identity)
    //
    // The conversation_id is preserved on the article's dataset so any
    // future per-message action wiring (mark read, copy, reply-quote)
    // can route through it.
    function renderDMMessage(meta) {
        var extraClasses = meta.is_mine ? 'is-mine' : '';
        var article = entryShell('entry--message', meta, extraClasses);
        if (meta.conversation_id) article.dataset.polisConversationId = meta.conversation_id;
        if (meta.id) article.dataset.polisMessageId = meta.id;

        var entryMeta = el('div', {
            cls: 'entry-meta-line',
            children: [
                timelineDot(),
                el('span', { cls: 'entry-date-time', text: meta.published_human || '', attrs: meta.published ? { datetime: meta.published } : null }),
                el('span', {
                    cls: 'entry-byline',
                    children: [
                        meta.is_mine
                            ? el('span', { cls: 'you-label', text: 'you' })
                            : document.createTextNode(meta.sender_domain || meta.sender_name || ''),
                    ],
                }),
            ],
        });

        var bodyEl = el('div', { cls: 'entry-body' });
        if (meta.body_text) {
            bodyEl.textContent = meta.body_text;
        }

        article.appendChild(entryMeta);
        article.appendChild(bodyEl);
        return article;
    }

    // Local tenant display name for follow entries: the site_title from
    // .well-known/polis (SSR'd into <body data-polis-site-title="...">),
    // falling back to the subdomain part of paginationTenant when the
    // title isn't set or is just the domain. Public-view phrasing avoids
    // "you" / "your"; the local tenant gets named in third person.
    function localTenantDisplayName() {
        var title = (document.body && document.body.dataset && document.body.dataset.polisSiteTitle) || '';
        title = title.trim();
        // Use site_title only when it's a real human label, not just the
        // bare domain echoed back as the title.
        if (title && title !== paginationTenant) return title;
        // Fall back to the subdomain — drop everything from the first dot
        // onward (e.g. "discover.polis.pub" → "discover").
        if (paginationTenant) {
            var dot = paginationTenant.indexOf('.');
            return dot > 0 ? paginationTenant.substring(0, dot) : paginationTenant;
        }
        return '';
    }

    // Follow feed item (renders within ACTIVITY — there is no dedicated
    // type=follows surface, but follow events still flow through the
    // activity feed via feed/handler.go's followEventToItem path and use
    // this renderer). Server emits:
    //   meta.author_domain — the actor (who followed/unfollowed)
    //   meta.target_domain — the other party
    //   meta.event_type    — pub.polis.follow.announced | .removed
    //
    // Visual structure mirrors the post/comment entry pattern: standard
    // entry-meta-line (date on the left, actor handle as byline on the
    // right) followed by a single italic body line "followed <target>"
    // or "unfollowed <target>". No glyph in the timeline gutter — the
    // timeline-dot is the only marker, matching the rest of the stream.
    function renderFollow(meta) {
        var article = entryShell('entry--follow', meta);
        var actorDomain = meta.author_domain || '';
        var targetDomain = meta.target_domain || '';
        var verbWord = (meta.event_type === 'pub.polis.follow.removed')
            ? 'unfollowed'
            : 'followed';
        var entryMeta = el('div', {
            cls: 'entry-meta-line',
            children: [
                timelineDot(),
                el('time', { cls: 'entry-date-time', text: meta.published_human || '', attrs: meta.published ? { datetime: meta.published } : null }),
            ],
        });
        // Actor leads the body as a clickable link, matching the target's
        // affordance (see renderAnnouncement) — "actor followed target".
        var body = el('p', { cls: 'follow-event-body' });
        if (actorDomain) {
            body.appendChild(el('a', {
                cls: 'actor',
                href: 'https://' + actorDomain,
                text: actorDomain,
            }));
        }
        body.appendChild(el('span', { cls: 'verb', text: verbWord }));
        body.appendChild(el('a', {
            cls: 'target',
            href: 'https://' + targetDomain,
            text: targetDomain,
        }));
        article.appendChild(entryMeta);
        article.appendChild(body);
        return article;
    }

    // renderAnnouncement renders an activity-signal entry — the feed-cache
    // items feed/handler.go stamps Type:"announcement" for events that aren't
    // posts/comments: follow + unfollow, blessing granted + requested, and
    // site-registered. Each is a single "actor <verb> target" line, sharing
    // the .follow-event-body visual treatment (the activity-line style). The
    // actor handle sits in the meta-line byline; the verb + target read as the
    // body. event_type drives the phrasing and which URL the target links to.
    //
    // The synthetic URLs some of these carry (meta.url like "follow:a:b" or
    // "site-registered:a") are NOT linkable — the target link is built from
    // the real domain / target_url per branch instead, never from meta.url
    // unless meta.url is a real https comment URL (blessing.granted).
    function renderAnnouncement(meta) {
        var article = entryShell('entry--announcement', meta);
        var actorDomain = meta.author_domain || '';
        var targetDomain = meta.target_domain || '';
        var eventType = meta.event_type || '';

        // Per-event phrasing: { verb, targetText, targetHref }. targetHref ''
        // renders the target as plain text (no link).
        var verb = '';
        var targetText = '';
        var targetHref = '';
        switch (eventType) {
            case 'pub.polis.follow.announced':
                verb = 'followed';
                targetText = targetDomain;
                targetHref = targetDomain ? 'https://' + targetDomain : '';
                break;
            case 'pub.polis.follow.removed':
                verb = 'unfollowed';
                targetText = targetDomain;
                targetHref = targetDomain ? 'https://' + targetDomain : '';
                break;
            case 'pub.polis.comment.blessing.granted':
                // actor (author_domain) is the post author who granted; the
                // other party (target_domain) is the commenter. meta.url is
                // the real comment URL, so link the commenter to the comment.
                verb = 'blessed a comment by';
                targetText = targetDomain;
                targetHref = (meta.url && /^https?:/i.test(meta.url)) ? meta.url : '';
                break;
            case 'pub.polis.comment.blessing.requested':
                // actor (author_domain) requested; target_domain is the post
                // author. target_url is the post.
                verb = 'requested a blessing from';
                targetText = targetDomain;
                targetHref = (meta.target_url && /^https?:/i.test(meta.target_url)) ? meta.target_url : '';
                break;
            case 'pub.polis.site.registered':
                // Standalone phrase — no second party to link.
                verb = 'joined the network';
                targetText = '';
                targetHref = '';
                break;
            default:
                // Unknown announcement event: render a neutral line rather than
                // dropping it (and never the old "unknown entry type" warn).
                verb = 'had activity';
                targetText = '';
                targetHref = '';
                break;
        }

        // Identity is the ACTIVITY SOURCE (author_domain — e.g. the DS that
        // surfaced the signal), and it follows the standard card affordance:
        // a floated avatar chip + a hover-revealed handle in the meta line,
        // not a static byline. Mirrors renderPost so announcements read like
        // every other entry rather than as a special-case plain-text label.
        var actorHref = actorDomain ? 'https://' + actorDomain : '#';
        var entryMeta = el('div', {
            cls: 'entry-meta-line',
            children: [
                timelineDot(),
                el('time', { cls: 'entry-date-time', text: meta.published_human || '', attrs: meta.published ? { datetime: meta.published } : null }),
            ],
        });
        // The actor handle leads the body as an always-visible clickable link
        // (mirrors the target's affordance), so the line reads "actor verb
        // target" rather than a bare "verb target" whose subject was only
        // hinted by the floated avatar. No hover-revealed meta-line handle —
        // the inline actor IS the handle now, so the avatar's rollover would
        // only duplicate it.
        var body = el('p', { cls: 'follow-event-body' });
        if (actorDomain) {
            body.appendChild(el('a', {
                cls: 'actor',
                href: actorHref,
                text: actorDomain,
            }));
        }
        body.appendChild(el('span', { cls: 'verb', text: verb }));
        if (targetText) {
            body.appendChild(targetHref
                ? el('a', { cls: 'target', href: targetHref, text: targetText })
                : el('span', { cls: 'target', text: targetText }));
        }
        article.appendChild(entryMeta);
        // Floated avatar chip for the activity source. The feed may ship an
        // enriched author_avatar; if not, fall back to the same deterministic
        // hue + initial the nav/SPA use so the chip is always a colored circle
        // rather than a bare letter. Links to the source domain; rollover
        // reveals the handle above (entry-handle), exactly like a post.
        if (actorDomain) {
            var afb = avatarFallback(actorDomain);
            var acfg = meta.author_avatar || { bg: afb.color, fg: '#ffffff' };
            article.appendChild(el('a', {
                cls: 'entry-avatar-link',
                href: actorHref,
                attrs: { 'aria-label': actorDomain },
                children: [ buildAvatar(acfg, afb.initial, 28) ],
            }));
        }
        article.appendChild(body);
        return article;
    }

    // Default 'comment' renderer is the thread-entry shape used by the
    // type=comments rescope: each entry shows the full post handle
    // commented on + the latest comment underneath. The older
    // renderComment is kept for code paths that surface a bare comment
    // without thread context (currently none in the public stream; reserved
    // for the panel-expansion comment list and future owner-side flows).
    var renderers = {
        post: renderPost,
        comment: renderCommentThread,
        profile: renderProfile,
        mention: renderMention,
        dm: renderDM,
        'dm-message': renderDMMessage,
        follow: renderFollow,
        // Activity signals from the feed cache (feed/handler.go Type:
        // "announcement"): follow/unfollow, blessing granted/requested,
        // site-registered. renderAnnouncement branches on event_type.
        announcement: renderAnnouncement,
    };

    // Extension registry. Layer 3: afterRender listeners decorate
    // a base entry post-render. Layer 4: registerRenderer overrides the base
    // renderer entirely. owner-extras.js composes against these; public bundle
    // never subscribes — registry stays empty for non-auth visitors.
    var afterRenderListeners = {};

    // Filter-change subscribers. Fired
    // by every code path that mutates filter state (selectOption from
    // dropdown picks, setFilter / setFilterType / setFilterScope /
    // setFilterQualifier / setFilterModifier from owner-extras).
    // Listener receives a snapshot of the post-change filter state so
    // a single hook can react uniformly to any source. owner-extras
    // uses this to keep body.is-activity-mode in sync with filterType
    // — without it, dropdown-picked type changes left the body class
    // stale, breaking the Layer-2 CSS gate.
    var filterChangeListeners = [];

    function fireFilterChange() {
        var snapshot = {
            qualifier: filterQualifier,
            type: filterType,
            scope: filterScope,
            modifier: filterModifier,
        };
        for (var i = 0; i < filterChangeListeners.length; i++) {
            try {
                filterChangeListeners[i](snapshot);
            } catch (e) {
                console.error('[polis-stream] filterChange listener error:', e);
            }
        }
    }

    function addFilterChangeListener(fn) {
        if (typeof fn !== 'function') return;
        filterChangeListeners.push(fn);
    }

    function fireAfterRender(type, entry, meta) {
        var listeners = afterRenderListeners[type];
        if (!listeners) return;
        for (var i = 0; i < listeners.length; i++) {
            try {
                listeners[i](entry, meta);
            } catch (e) {
                console.error('[polis-stream] afterRender listener error:', e);
            }
        }
    }

    function addAfterRenderListener(type, fn) {
        if (typeof fn !== 'function') return;
        if (!afterRenderListeners[type]) afterRenderListeners[type] = [];
        afterRenderListeners[type].push(fn);
    }

    function registerRenderer(type, fn) {
        if (typeof fn !== 'function') return;
        renderers[type] = fn;
    }

    function renderEntry(meta) {
        var fn = renderers[meta && meta.type];
        if (!fn) {
            console.warn('[polis-stream] unknown entry type:', meta && meta.type);
            return null;
        }
        var entry = fn(meta);
        if (entry) fireAfterRender(meta.type, entry, meta);
        return entry;
    }

    /* ------------------------------------------------------------------
       Entry insertion — append to .stream, observe, track in entries[]
       ------------------------------------------------------------------ */

    function getStreamContainer() {
        return document.querySelector('.stream');
    }

    function appendEntry(domEl) {
        if (!domEl) return null;
        var container = getStreamContainer();
        if (!container) return null;
        // Insert before .stream-fade-out if present, else append at end.
        var fade = container.querySelector(':scope > .stream-fade-out');
        if (fade) {
            container.insertBefore(domEl, fade);
        } else {
            container.appendChild(domEl);
        }
        entries.push(domEl);
        if (observer) observer.observe(domEl);
        // Also subscribe the new entry for lazy-fetch prefetch (skipped
        // for DMs + SSR'd focus + already-expanded entries inside the
        // helper). Only kicks in if the user has already scrolled —
        // until then, prefetchObserver isn't observing anything.
        if (userHasScrolled) observeEntryForPrefetch(domEl);
        // Wire focus-mode click handler on entries that ship in expanded
        // form (post excerpt-form entries get bound only after lazy-
        // fetch restructures them; renderCommentThread output ships
        // expanded, so it needs to be bound here). bindCardClick is a
        // no-op for entries without .focus-content / .entry-body--expanded.
        bindCardClick(domEl);
        // Wire the post-body toggle (show more / hide) — same plumbing
        // used by SSR'd .entry--post cards and lazy-fetched bodies.
        // Idempotent via data-body-toggle-bound; no-op for entries
        // without a .post-body-toggle button. Comment-thread entries
        // ship the toggle inline via renderCommentThread.
        bindPostBodyToggle(domEl);
        return domEl;
    }

    function clearDynamicEntries() {
        // Remove any .entry that wasn't part of the SSR hydration. Detected
        // via a marker class set by appendEntry, since the original entries[]
        // snapshot is mutated. We add the marker class lazily there.
        var container = getStreamContainer();
        if (!container) return;
        var dynamics = container.querySelectorAll('.entry.is-dynamic');
        for (var i = 0; i < dynamics.length; i++) {
            if (observer) observer.unobserve(dynamics[i]);
            if (prefetchObserver) prefetchObserver.unobserve(dynamics[i]);
            dynamics[i].parentNode.removeChild(dynamics[i]);
        }
        // Rebuild entries array from what's left in DOM order.
        entries = Array.prototype.slice.call(container.querySelectorAll('.entry'));
        // Re-anchor focus to whatever's left (typically the SSR'd focus). If
        // the previously-focused entry was a dynamic one we just removed,
        // it's now a detached DOM node and would silently poison
        // focusNext/focusPrev (entries.indexOf returns -1, falling back to
        // entries[0]) and updateFocus comparisons. Reset and let the next
        // observer fire repopulate.
        focusedEntry = container.querySelector('.entry.is-focused') || entries[0] || null;
        intersectingMap.clear();
    }

    /* ------------------------------------------------------------------
       Pagination — scroll-past-end fetches older entries via
       /api/v1/stream/items?scope=@<canonical-tenant>.

       First request uses before_url=<oldest SSR'd sibling URL> so the
       server can synthesize the cursor; subsequent requests pass the
       server-returned `next_cursor`. On end-of-history (no next_cursor),
       the observer disconnects.

       Gated on userHasScrolled like setupPrefetch — layout-shift events
       (font load, nav-widget hydration, lazy-fetch expansions) intersect
       the last entry but shouldn't trigger a network call until the user
       has actually scrolled.
       ------------------------------------------------------------------ */

    function readCanonicalTenant() {
        var link = document.querySelector('link[rel="canonical"]');
        if (!link) return '';
        var href = link.getAttribute('href');
        if (!href) return '';
        try {
            return new URL(href, window.location.href).hostname;
        } catch (e) {
            return '';
        }
    }

    // Returns the scope value to encode into pagination
    // requests. filterScope wins when set (owner SPA assigns it at init
    // and selectOption updates it on user pick); falls back to
    // '@<canonicalTenant>' for the public per-post page where the
    // tenant identity is the implicit scope.
    function currentScopeParam() {
        if (filterScope) return filterScope;
        if (paginationTenant) return '@' + paginationTenant;
        return '';
    }

    function setupPagination() {
        if (!('IntersectionObserver' in window)) return;
        if (!('fetch' in window)) return;
        paginationTenant = readCanonicalTenant();
        if (!paginationTenant) {
            // No canonical link → can't determine tenant scope. Quietly
            // bail; rest of the controller still works (focus tracking,
            // keyboard nav, lazy-fetch on whatever's already on the page).
            return;
        }
        paginationObserver = new IntersectionObserver(onPaginationIntersect, {
            rootMargin: PAGINATION_LOOKAHEAD,
        });
        // Don't observe yet — wait for first scroll, mirroring setupPrefetch's
        // pattern. Observing immediately at hydrate-time was flaky:
        // IntersectionObserver fires its initial-state callback synchronously
        // on registration, and that initial fire gets gated out by the
        // userHasScrolled check in onPaginationIntersect. Once the observer
        // has registered "intersecting" once (because the last SSR'd entry is
        // already inside the 200px lookahead — common on tall viewports with
        // only ~5 SSR'd entries), it only fires again on intersection-state
        // CHANGE — and a user scrolling past an already-intersecting entry
        // produces no state change. Result: pagination silently fails to
        // trigger ~20% of the time, depending on viewport height vs entry
        // count. Deferring observation until startPagination() (called from
        // setupUserScrollGate's first-scroll handler) ensures the initial
        // callback fires AFTER userHasScrolled flips true, so the gate lets
        // it through.
    }

    // Called from setupUserScrollGate's onFirstScroll alongside
    // startPrefetching(). See setupPagination's comment for why observation
    // is deferred to first scroll.
    function startPagination() {
        if (!paginationObserver || paginationDone) return;
        observeLastEntry();
    }

    function observeLastEntry() {
        if (!paginationObserver || paginationDone) return;
        if (paginationLastEntry) {
            paginationObserver.unobserve(paginationLastEntry);
        }
        var last = entries[entries.length - 1];
        if (!last) {
            paginationLastEntry = null;
            return;
        }
        paginationLastEntry = last;
        paginationObserver.observe(last);
    }

    function onPaginationIntersect(observerEntries) {
        // No userHasScrolled gate here. The observer is only attached to a
        // last-entry via startPagination (first scroll) or fetchNextPage's
        // success handler (post-engagement) — both of which require user
        // action. Gating at the callback was redundant with deferred
        // observation AND broke the no-scroll-then-filter path: a user who
        // clicks a filter from the topbar before scrolling would get one
        // fetchNextPage from applyFilter, but the success handler's
        // observeLastEntry would then synthesize an initial-state callback
        // that the gate would drop, leaving pagination silently stuck.
        for (var i = 0; i < observerEntries.length; i++) {
            if (observerEntries[i].isIntersecting) {
                fetchNextPage();
                break;
            }
        }
    }

    // buildPQLDataURL composes the /pql/<sentence> data URL from the current
    // filter state. The bundled shape is self-contained (it must work in the
    // public template without the webapp's pql.js), so the internal→token
    // mapping lives here — it MUST stay in sync with the canonical grammar
    // (docs/general/reference/pql-vocabulary.json). The server re-parses the sentence;
    // transport-only params (time/surface/cursor/before_url/search) are appended
    // by the caller as query params. Replaces the legacy
    // /api/v1/stream/items?scope=&type= construction (PQL hard cutover).
    function buildPQLDataURL() {
        var typeTok;
        switch (filterType) {
            case '':
            case 'mentions': typeTok = 'activity'; break; // empty/vestigial → activity
            case 'dms': typeTok = 'messages'; break;      // internal → grammar token
            default: typeTok = filterType;                // posts|comments|profiles|drafts|activity
        }
        var sc = currentScopeParam();
        var scopeTok;
        switch (sc) {
            case 'my-network': scopeTok = 'my network'; break;
            case 'my-mutuals': scopeTok = 'my mutuals'; break;
            case 'all-polis': scopeTok = 'all polis'; break;
            case 'me':
            case 'my': scopeTok = 'me'; break;
            default:
                if (sc.charAt(0) === '@') {
                    var bare = sc.slice(1);
                    var ni = bare.indexOf(':network');
                    scopeTok = ni >= 0 ? bare.slice(0, ni) + "'s network" : bare;
                } else {
                    scopeTok = sc;
                }
        }
        var parts = [filterQualifier || 'all', typeTok, 'from', scopeTok];
        switch (filterModifier) {
            case 'by-date': parts.push('by date'); break;
            case 'by-name': parts.push('by name'); break;
            case 'by-activity': parts.push('by activity'); break;
            case 'with-comments': parts.push('with comments'); break;
            case 'to-bless': parts.push('to bless'); break;
            // '' (activity / no modifier) → nothing appended
        }
        return '/pql/' + parts.join(' ').replace(/ /g, '+');
    }

    function fetchNextPage() {
        if (paginationInFlight || paginationDone) return;
        // Gate fetches when no scope can be determined. The gate
        // generalizes from "no canonical tenant" to "no scope param at
        // all" — the owner SPA assigns filterScope
        // before the controller starts paginating, so the gate stays
        // effective for both surfaces.
        if (!currentScopeParam()) return;
        paginationInFlight = true;
        // Snapshot the filter generation: if a filter change bumps it while
        // this request is in flight, the resolve handler discards the stale
        // (off-filter) response instead of appending it.
        var fetchGen = paginationGeneration;

        // Build URL from current filter state. Public default is
        // qualifier=all, type=posts, modifier=by-date, scope=@<canonicalTenant>.
        // User-driven filter changes update filterType + filterModifier;
        // applyFilter resets pagination state + retriggers fetch. The
        // public-stream filter has no timeframe option, so we send an
        // explicit time window
        // to bypass the server's 24h default — otherwise scrolling older
        // than 24h, or filtering to "with comments" against an older
        // corpus, would silently return empty. We send 30d (the server's
        // maxStreamWindowDays cap) rather than 'all' so the intent stays
        // visible in URLs/logs and matches the server-enforced ceiling.
        // Scope comes from currentScopeParam() so owner SPA
        // can drive my-network / my-mutuals / etc.; public surface
        // continues to use '@<canonical-tenant>' (paginationTenant).
        // PQL hard cutover: the filter (scope/type/qualifier/modifier) is now
        // carried as a PQL sentence on the path; only transport concerns ride
        // as query params. The server (handlers_pql.go) re-parses the sentence
        // and returns the versioned envelope. We send an explicit time=30d to
        // bypass the server's 24h default (the public stream has no timeframe
        // slot); 30d matches the server-enforced maxStreamWindow ceiling.
        var url = buildPQLDataURL() + '?time=30d';
        // Search modifier — supported on both profiles scopes. all-polis hits
        // the DS site directory; my-network filters the local follow list.
        // (Stays a query param: it's a free-text filter, not part of grammar.)
        if (filterType === 'profiles' &&
            (filterScope === 'all-polis' || filterScope === 'my-network') &&
            currentSearch) {
            url += '&search=' + encodeURIComponent(currentSearch);
        }
        // Surface signal: the owner SPA gets DS-total comment counts
        // (network-aggregation lens); canonical pages get blessed-only
        // (tenant's curated lens). body.is-owner marks the SPA — see the
        // Layer-2 owner-gate comment in stream.css for the full convention.
        if (document.body.classList.contains('is-owner')) {
            url += '&surface=spa';
        }
        var wasFirstFetch = paginationFirstFetch;
        if (paginationCursor) {
            url += '&cursor=' + encodeURIComponent(paginationCursor);
        } else if (paginationFirstFetch) {
            // First call (or first after a filter change): anchor on the
            // oldest entry currently on the page so the server returns
            // items strictly older than that. before_url is converted to
            // a cursor server-side (handlers_stream.go's beforeURL
            // handling). Avoids client-side cursor-format
            // coupling. After applyFilter clears siblings, this anchors
            // on the focus URL — server returns items older than the
            // focus, in the new filter type/time.
            var anchorURL = oldestSSREntryURL();
            if (anchorURL) {
                url += '&before_url=' + encodeURIComponent(anchorURL);
            }
        }

        // credentials: 'same-origin' so the polis_session cookie travels
        // with the request. 'omit' would suffice when the stream endpoint
        // is fully public, but owner-private scopes
        // (scope=my-network / me / my / @<self>) require auth. The
        // admin SPA sends those scopes; without credentials, the
        // hosted gate 401s every paginate. Same-origin (not 'include')
        // because the stream endpoint is always on the SPA's own
        // origin; cross-tenant body fetches go through fetchBody
        // which stays 'omit'.
        // Accept: application/json selects the /pql/ JSON envelope (vs the HTML
        // infinity-stream page the same path serves to browsers).
        fetch(url, { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
            .then(function (resp) {
                // Throttle / backpressure: 429 (rate limit) + 503 (service
                // unavailable) carry a Retry-After hint per RFC 9110. Parse
                // it to drive the retry delay; falls back to exponential
                // backoff if the header is absent or unparseable.
                if (resp.status === 429 || resp.status === 503) {
                    var err = new Error('HTTP ' + resp.status);
                    err.retryAfterMs = parseRetryAfter(resp.headers.get('Retry-After'));
                    err.transient = true;
                    throw err;
                }
                if (!resp.ok) throw new Error('HTTP ' + resp.status);
                return resp.json();
            })
            .then(function (data) {
                // Stale-generation guard: the filter changed while this fetch
                // was in flight (e.g. default→dms on a fresh PQL load). Drop
                // the response entirely — appending it would mix off-filter
                // entries into the new view. The new filter's own fetch has
                // already been kicked off by applyFilter.
                if (fetchGen !== paginationGeneration) return;
                // Success path — reset the retry budget. A subsequent
                // failure starts attempt counting fresh.
                paginationRetryAttempts = 0;
                var items = (data && data.items) || [];
                for (var i = 0; i < items.length; i++) {
                    appendItemIfNew(items[i]);
                }
                paginationCursor = (data && data.pagination && data.pagination.next_cursor) || '';
                paginationFirstFetch = false;
                if (!paginationCursor) {
                    // End of history — no more pages to fetch. Stop
                    // observing the last entry but keep the observer
                    // instance around so a later filter change (5.d)
                    // can reuse it without re-creating.
                    paginationDone = true;
                    if (paginationObserver && paginationLastEntry) {
                        paginationObserver.unobserve(paginationLastEntry);
                    }
                    paginationLastEntry = null;
                    // Empty-state: if a filter change just
                    // landed AND the result set is empty AND the only
                    // remaining entry is the focus, render an honest
                    // "no results" message + reset link. Don't show
                    // the empty state on natural end-of-history during
                    // scroll (siblings + appended entries already in
                    // place — user just hit the bottom of the corpus).
                    if (wasFirstFetch && items.length === 0 && entries.length <= 1) {
                        renderEmptyState();
                    }
                } else {
                    // New entries appended — re-anchor observer on the
                    // new last entry.
                    observeLastEntry();
                }
            })
            .catch(function (err) {
                // Schedule a delayed retry via
                // setTimeout instead of relying on the IntersectionObserver
                // to re-fire. The observer only fires on intersection-state
                // CHANGE, so a user already past the last-entry boundary
                // at fail-time wouldn't get a retry until they scrolled
                // out and back. Honors `Retry-After` for 429/503; falls
                // back to exponential backoff (750ms / 1.5s / 3s) up to
                // PAGINATION_MAX_ATTEMPTS. After exhaustion: log + give
                // up. Filter change (applyFilter) clears any pending timer.
                paginationRetryAttempts++;
                if (paginationRetryAttempts <= PAGINATION_MAX_ATTEMPTS) {
                    var delay = (err && err.retryAfterMs)
                        ? err.retryAfterMs
                        : PAGINATION_BACKOFF_BASE_MS * Math.pow(2, paginationRetryAttempts - 1);
                    if (paginationRetryTimer) clearTimeout(paginationRetryTimer);
                    paginationRetryTimer = setTimeout(function () {
                        paginationRetryTimer = null;
                        fetchNextPage();
                    }, delay);
                } else {
                    if (window.console && console.warn) {
                        console.warn('[polis-stream] pagination fetch failed after retries:', err);
                    }
                    // Reset the attempts counter so a later filter change
                    // gets a fresh retry budget; leave paginationDone false
                    // so a subsequent scroll-trigger can still try.
                    paginationRetryAttempts = 0;
                }
            })
            .then(function () {
                // Only clear the flag if WE'RE still the current generation —
                // a filter change already reset it and may have a fresh fetch
                // in flight; clobbering its flag would let a duplicate run.
                if (fetchGen === paginationGeneration) paginationInFlight = false;
            });
    }

    // parseRetryAfter accepts both the integer-seconds form ("30") and
    // the HTTP-date form ("Wed, 21 Oct 2026 07:28:00 GMT") per RFC 9110.
    // Returns milliseconds, or null when unparseable. The numeric form is
    // overwhelmingly the common case for application-level rate limiting.
    function parseRetryAfter(raw) {
        if (!raw) return null;
        var n = parseInt(raw, 10);
        if (!isNaN(n) && n >= 0) return n * 1000;
        var ts = Date.parse(raw);
        if (isNaN(ts)) return null;
        var ms = ts - Date.now();
        return ms > 0 ? ms : 0;
    }

    // oldestSSREntryURL returns the URL of the oldest SSR'd sibling —
    // used as the before_url anchor on the first pagination fetch.
    // The hydration step stamped data-polis-canonical-url on every entry;
    // the LAST entry in document order is the oldest in newest-top mode
    // (DIRECTION-HOOK).
    function oldestSSREntryURL() {
        var last = entries[entries.length - 1];
        if (!last) return '';
        return (last.dataset && last.dataset.polisCanonicalUrl) || '';
    }

    // newestSSREntryURL returns the URL of the newest currently-loaded
    // entry — used as the after_url anchor on the first upward
    // pagination fetch. On a per-post page with above-focus siblings,
    // entries[0] is the topmost above-focus sibling, NOT the focus —
    // so anchoring on entries[0] avoids re-fetching SSR'd above-focus
    // entries (the dedup at prependItemIfNew would catch them, but
    // anchoring correctly saves the round-trip). On a per-post page
    // with NO above-focus siblings (e.g., focus = newest in corpus
    // but viewed via per-post URL), entries[0] is the focus itself,
    // which is also the right anchor.
    function newestSSREntryURL() {
        var first = entries[0];
        if (!first) return '';
        return (first.dataset && first.dataset.polisCanonicalUrl) || '';
    }

    function appendItemIfNew(item) {
        if (!item) return;
        // Dedup key: posts/comments carry url (canonical document URL);
        // profile/dm/other entity items have no document URL and fall
        // back to id ("profile:<domain>", "dm:<conv-id>", etc.). Either
        // way the key is stamped on dataset.polisCanonicalUrl below so
        // re-fetch dedup and focus-tracking work uniformly.
        var key = item.url || item.id;
        if (!key) return;
        for (var i = 0; i < entries.length; i++) {
            var existing = entries[i];
            if (existing.dataset && existing.dataset.polisCanonicalUrl === key) {
                return;
            }
        }
        // published_human is supplied by the server (handlers_stream.go's
        // streamItemResponse wrapper) — single source of truth for stream
        // date formatting. SSR'd entries (focus + siblings) and JS-appended
        // entries share the same formatter (template.FormatHumanDateTime),
        // so the meta-line never drifts between the SSR window and the
        // pagination window.
        var dom = renderEntry(item);
        if (!dom) return;
        dom.classList.add('is-dynamic');
        if (dom.dataset) dom.dataset.polisCanonicalUrl = key;
        appendEntry(dom);
    }

    /* ------------------------------------------------------------------
       Upward pagination — scroll past the FIRST entry fetches NEWER
       entries via /api/v1/stream/items?after_url=<focus URL>&order=oldest.
       Symmetric to the down direction (fetchNextPage / observeLastEntry
       / appendItemIfNew). The focus is the SSR'd anchor on first fetch;
       subsequent fetches use the server-returned next_cursor.

       Skipped on the homepage: the focus there IS the newest post, so
       there's nothing newer to fetch. Detected via window.location.pathname.

       Scroll-position preservation: prepending DOM nodes pushes existing
       content down, which would visually shift the user's anchor entry.
       fetchPreviousPage's success handler captures the anchor's
       getBoundingClientRect().top before insertion and adjusts
       window.scrollY by the height-delta after insertion so the user's
       view stays anchored to the same content. Architecture doc lines
       314-322 documents this contract.
       ------------------------------------------------------------------ */

    function setupPaginationTop() {
        if (!('IntersectionObserver' in window)) return;
        if (!('fetch' in window)) return;
        // Homepage skip: focus is already the newest post, nothing to
        // fetch upward. Per-post URLs land under /posts/.../*.html.
        var p = window.location.pathname;
        if (p === '/' || p === '' || p === '/index.html' || (/\/index\.html$/).test(p)) {
            paginationDoneTop = true;
            return;
        }
        // "Focus is the newest entry" skip. Two surfaces reach here with the
        // focus already at the very top and NO above-focus siblings rendered:
        //   - a PQL filter landing (/pql/<sentence>, /_/pql/<sentence>) — the
        //     homepage is now served at /pql/all+posts+by+date, so the URL
        //     check above no longer catches it;
        //   - a per-post deeplink whose focus IS the newest post in the corpus.
        // pickStreamSiblings (render/stream.go) emits an above-focus sibling IFF
        // the focus isn't the newest, so the absence of .is-above-focus is a
        // reliable "nothing newer to fetch" signal. Leaving paginationDoneTop
        // false here would (a) keep the top-fade covering the first entry's
        // first line until the user scrolls down + back up (which lets upward
        // pagination conclude and updateTopFadeVisibility Case (A) release the
        // fade), and (b) arm a pointless upward observer. Scoped to the public
        // static-focus surface so the owner SPA's pagination is untouched.
        var streamEl = document.querySelector('.stream');
        if (streamEl &&
            streamEl.getAttribute('data-polis-stream-role') === 'static-focus-and-siblings' &&
            !document.querySelector('.entry.is-above-focus')) {
            paginationDoneTop = true;
            return;
        }
        // Reuse paginationTenant resolved by setupPagination — both
        // directions hit the same /api/v1/stream/items endpoint with
        // the same scope. setupPagination runs first in init() (see
        // init's call order), so paginationTenant is set by the time
        // we reach here. If it's empty (no canonical link), the down
        // direction also bails — nothing to do here either.
        if (!paginationTenant) {
            paginationDoneTop = true;
            return;
        }
        paginationTopObserver = new IntersectionObserver(onPaginationTopIntersect, {
            rootMargin: PAGINATION_LOOKAHEAD,
        });
        // Defer observation to first scroll, mirroring setupPagination's
        // post-1.7.20 pattern: an IntersectionObserver registered before
        // the user has scrolled fires its initial-state callback once
        // (synchronously, with whatever the current intersection state
        // is) and then never again unless the state CHANGES — which
        // breaks pagination silently when the focus is already inside
        // the lookahead at hydrate time.
    }

    function startPaginationTop() {
        if (!paginationTopObserver || paginationDoneTop) return;
        observeFirstEntry();
    }

    function observeFirstEntry() {
        if (!paginationTopObserver || paginationDoneTop) return;
        if (paginationFirstEntry) {
            paginationTopObserver.unobserve(paginationFirstEntry);
        }
        var first = entries[0];
        if (!first) {
            paginationFirstEntry = null;
            return;
        }
        paginationFirstEntry = first;
        paginationTopObserver.observe(first);
    }

    function onPaginationTopIntersect(observerEntries) {
        for (var i = 0; i < observerEntries.length; i++) {
            if (observerEntries[i].isIntersecting) {
                fetchPreviousPage();
                break;
            }
        }
    }

    function fetchPreviousPage() {
        if (paginationInFlightTop || paginationDoneTop) return;
        // Scope-presence gate (matches fetchNextPage).
        if (!currentScopeParam()) return;
        paginationInFlightTop = true;
        // Stale-generation snapshot (see fetchNextPage / paginationGeneration).
        var fetchGenTop = paginationGeneration;

        // order=oldest so the response items come back from oldest-newer
        // to newest-of-batch. We then prepend them in-order: each item
        // lands directly above the previous topmost entry, leaving the
        // newest-of-batch at the very top — chronologically correct.
        // Same scope/with/sort propagation as down direction.
        // PQL hard cutover (mirror of fetchNextPage): filter on the path as a
        // PQL sentence, transport concerns as query params. order=oldest so the
        // batch comes back oldest→newest for in-order prepend.
        var url = buildPQLDataURL() + '?order=oldest';
        // Search modifier (mirror of fetchNextPage). Supported on both
        // profiles scopes.
        if (filterType === 'profiles' &&
            (filterScope === 'all-polis' || filterScope === 'my-network') &&
            currentSearch) {
            url += '&search=' + encodeURIComponent(currentSearch);
        }
        // Surface signal (mirror of fetchNextPage) — see comment there.
        if (document.body.classList.contains('is-owner')) {
            url += '&surface=spa';
        }
        if (paginationCursorTop) {
            url += '&cursor=' + encodeURIComponent(paginationCursorTop);
        } else if (paginationFirstFetchTop) {
            // Anchor on the topmost SSR'd entry — that's entries[0],
            // which is the topmost above-focus sibling when the page
            // SSR'd any, or the focus itself when there are none.
            // Using the focus URL when an above-focus sibling exists
            // would have the server return the SSR'd above-focus
            // siblings again (dedup catches them, but it's a wasted
            // round-trip).
            var anchorURL = newestSSREntryURL();
            if (anchorURL) {
                url += '&after_url=' + encodeURIComponent(anchorURL);
            }
        }

        // Capture the anchor's viewport-relative position BEFORE
        // prepending, so we can restore the user's view after DOM
        // mutation (which would otherwise shift everything down by the
        // height of the prepended content).
        var anchorEl = paginationFirstEntry || document.querySelector('.entry.is-focused');
        var anchorTopBefore = anchorEl ? anchorEl.getBoundingClientRect().top : 0;

        // Same-origin credentials — see comment above the sibling fetch
        // in the downward-pagination block: owner-private scopes need the
        // polis_session cookie. Accept JSON
        // selects the /pql/ envelope (PQL hard cutover).
        fetch(url, { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
            .then(function (resp) {
                if (resp.status === 429 || resp.status === 503) {
                    var err = new Error('HTTP ' + resp.status);
                    err.retryAfterMs = parseRetryAfter(resp.headers.get('Retry-After'));
                    err.transient = true;
                    throw err;
                }
                if (!resp.ok) throw new Error('HTTP ' + resp.status);
                return resp.json();
            })
            .then(function (data) {
                // Stale-generation guard (see fetchNextPage). A filter change
                // mid-flight invalidates this upward response too.
                if (fetchGenTop !== paginationGeneration) return;
                paginationRetryAttemptsTop = 0;
                var items = (data && data.items) || [];
                for (var i = 0; i < items.length; i++) {
                    prependItemIfNew(items[i]);
                }
                paginationCursorTop = (data && data.pagination && data.pagination.next_cursor) || '';
                paginationFirstFetchTop = false;
                if (!paginationCursorTop) {
                    paginationDoneTop = true;
                    if (paginationTopObserver && paginationFirstEntry) {
                        paginationTopObserver.unobserve(paginationFirstEntry);
                    }
                    paginationFirstEntry = null;
                    // paginationDoneTop just flipped — top-fade may now
                    // need to hide once user reaches scroll-top. Recompute
                    // immediately so the rule (A) check (scrollY===0 AND
                    // paginationDoneTop) sees the up-to-date state.
                    updateTopFadeVisibility();
                } else {
                    // New entries prepended — re-anchor observer on the new
                    // first entry.
                    observeFirstEntry();
                }
                // Scroll-position preservation: figure out where the
                // anchor element ended up after the DOM mutation and
                // shift the viewport so it's at the same position as
                // before the prepend.
                if (anchorEl && items.length > 0) {
                    var anchorTopAfter = anchorEl.getBoundingClientRect().top;
                    var delta = anchorTopAfter - anchorTopBefore;
                    if (delta !== 0) {
                        window.scrollBy(0, delta);
                    }
                }
            })
            .catch(function (err) {
                paginationRetryAttemptsTop++;
                if (paginationRetryAttemptsTop <= PAGINATION_MAX_ATTEMPTS) {
                    var delay = (err && err.retryAfterMs)
                        ? err.retryAfterMs
                        : PAGINATION_BACKOFF_BASE_MS * Math.pow(2, paginationRetryAttemptsTop - 1);
                    if (paginationRetryTimerTop) clearTimeout(paginationRetryTimerTop);
                    paginationRetryTimerTop = setTimeout(function () {
                        paginationRetryTimerTop = null;
                        fetchPreviousPage();
                    }, delay);
                } else {
                    if (window.console && console.warn) {
                        console.warn('[polis-stream] upward pagination fetch failed after retries:', err);
                    }
                    paginationRetryAttemptsTop = 0;
                }
            })
            .then(function () {
                if (fetchGenTop === paginationGeneration) paginationInFlightTop = false;
            });
    }

    function prependItemIfNew(item) {
        if (!item || !item.url) return;
        // Dedup against existing entries (SSR'd + already-paginated in
        // either direction).
        for (var i = 0; i < entries.length; i++) {
            var existing = entries[i];
            if (existing.dataset && existing.dataset.polisCanonicalUrl === item.url) {
                return;
            }
        }
        var dom = renderEntry(item);
        if (!dom) return;
        dom.classList.add('is-dynamic');
        if (dom.dataset) dom.dataset.polisCanonicalUrl = item.url;
        prependEntry(dom);
    }

    function prependEntry(domEl) {
        if (!domEl) return null;
        var container = getStreamContainer();
        if (!container) return null;
        // Insert at the TOP of the stream — before whatever's currently
        // first (focus, or a previously-prepended dynamic entry). The
        // year-marker sits OUTSIDE .stream (in .layout-right) so it
        // doesn't interfere with this insertion point.
        var firstChild = container.firstElementChild;
        if (firstChild) {
            container.insertBefore(domEl, firstChild);
        } else {
            container.appendChild(domEl);
        }
        // Track in entries[] at the FRONT so observer-anchor logic
        // (paginationFirstEntry vs. paginationLastEntry) reflects DOM
        // order. Focus tracking (setupFocusTracking's observer) doesn't
        // care about array order — it scans IntersectionObserver
        // results — but observeFirstEntry / observeLastEntry index off
        // entries[0] / entries[n-1] respectively.
        entries.unshift(domEl);
        if (observer) observer.observe(domEl);
        if (userHasScrolled) observeEntryForPrefetch(domEl);
        return domEl;
    }

    /* ------------------------------------------------------------------
       Filter widget — slot-change → reset stream + refetch

       Click on an interactive slot toggles its dropdown; click on an
       option updates the slot label + reissues /api/v1/stream/items
       with the new type/time params. On filter change: clear ALL non-
       focus entries (SSR'd siblings + dynamics), reset pagination
       state, refetch from the top. Empty-state:
       honest message + "Show all posts" reset link (resets to canonical
       default: type=posts, modifier=by-date).

       Selectors per snippets/stream-filter.html / Q-S11 resolution.
       Click-outside / Escape close all dropdowns via existing onEscape API.
       ------------------------------------------------------------------ */

    function setupFilter() {
        var slots = document.querySelectorAll('.sentence-filter .sf-slot--interactive');
        if (!slots.length) return;
        for (var i = 0; i < slots.length; i++) {
            attachSlotHandlers(slots[i]);
        }
        // Close any open dropdown on click outside the filter container.
        document.addEventListener('click', function (ev) {
            if (!filterOpenDropdown) return;
            var filterEl = document.querySelector('.sentence-filter');
            if (filterEl && !filterEl.contains(ev.target)) {
                closeOpenDropdown();
            }
        });
        // Wire ESC into the controller's existing popover-close registry
        // so any open dropdown closes alongside other popovers (compose
        // surface, etc. that step 6 layers in).
        registerPopoverCloseHandler(closeOpenDropdown);
    }

    function attachSlotHandlers(slotEl) {
        // Dedupe so repeated lock/unlock cycles on the
        // scope slot don't accumulate click listeners.
        if (slotEl.__polisSlotWired) return;
        slotEl.__polisSlotWired = true;
        var slotKind = slotEl.getAttribute('data-filter-slot'); // 'type' or 'time'
        slotEl.addEventListener('click', function (ev) {
            ev.stopPropagation();
            toggleSlotDropdown(slotEl, slotKind);
        });
        slotEl.addEventListener('keydown', function (ev) {
            if (ev.key === 'Enter' || ev.key === ' ') {
                ev.preventDefault();
                toggleSlotDropdown(slotEl, slotKind);
            }
        });
    }

    function toggleSlotDropdown(slotEl, slotKind) {
        // Respect the slot's locked state at toggle
        // time. attachSlotHandlers wires the click listener once, so a
        // slot that gets locked after first wire (e.g. the scope slot
        // when applyTypePersonalLock fires for dms/drafts) still has
        // a live click handler. Bail early if the slot has been
        // marked locked — same outcome the user gets if the click
        // listener had been removed entirely.
        if (slotEl.getAttribute('data-filter-locked') === 'true' ||
            slotEl.classList.contains('sf-slot--locked')) {
            return;
        }
        var alreadyOpen = filterOpenDropdown && filterOpenDropdown.parentNode === slotEl;
        closeOpenDropdown();
        if (alreadyOpen) return;
        // Dispatch through getFilterOptions so all slots
        // (including the type-conditional modifier set) flow through
        // one extension point. owner-extras.js registers additional
        // options via PolisStream.registerFilterOption.
        var options = getFilterOptions(slotKind);
        if (!options.length) return;
        var current;
        if (slotKind === 'type') current = filterType;
        else if (slotKind === 'modifier') current = filterModifier;
        else if (slotKind === 'qualifier') current = filterQualifier;
        else if (slotKind === 'scope') current = filterScope;
        else return;
        var dropdown = buildDropdown(slotKind, options, current);
        // Position relative to the slot — the slot itself doesn't have
        // position: relative by default, so set it inline; .sf-dropdown
        // is position:absolute relative to the slot.
        slotEl.style.position = 'relative';
        slotEl.appendChild(dropdown);
        slotEl.setAttribute('aria-expanded', 'true');
        filterOpenDropdown = dropdown;
    }

    function closeOpenDropdown() {
        if (!filterOpenDropdown) return;
        var slotEl = filterOpenDropdown.parentNode;
        if (slotEl) {
            slotEl.removeChild(filterOpenDropdown);
            slotEl.setAttribute('aria-expanded', 'false');
        }
        filterOpenDropdown = null;
    }

    function buildDropdown(slotKind, options, currentValue) {
        var ariaLabel = 'Filter';
        if (slotKind === 'type') ariaLabel = 'Item type';
        else if (slotKind === 'modifier') ariaLabel = 'Item modifier';
        var dropdown = el('div', {
            cls: 'sf-dropdown',
            attrs: {
                role: 'listbox',
                'aria-label': ariaLabel,
            },
        });
        for (var i = 0; i < options.length; i++) {
            (function (opt) {
                var selected = opt.value === currentValue;
                // opt.indent visually nests the option under
                // the prior parent — used to show posts/comments/follows
                // as children of "activity" in the type dropdown.
                var classes = 'sf-option';
                if (opt.indent) classes += ' sf-option--indent';
                var optionEl = el('div', {
                    cls: classes,
                    attrs: { role: 'option', 'aria-selected': selected ? 'true' : 'false' },
                    children: [
                        el('span', { text: opt.label }),
                        selected ? el('span', { cls: 'sf-check', text: '✓' }) : null,
                    ],
                });
                optionEl.addEventListener('click', function (ev) {
                    ev.stopPropagation();
                    selectOption(slotKind, opt);
                });
                dropdown.appendChild(optionEl);
            })(options[i]);
        }
        return dropdown;
    }

    function selectOption(slotKind, opt) {
        // Update controller state.
        if (slotKind === 'type') {
            filterType = opt.value;
            // When type changes, reset modifier to the type's canonical
            // default — the first entry of FILTER_MODIFIER_OPTIONS_BY_
            // TYPE[type]. Previously this was always 'by-date', which
            // was wrong for profiles ('by-name'/'by-activity') and
            // produced an invalid sentence after picking profiles.
            // Types without a modifier table (activity, mentions) fall
            // back to 'by-date' as a safe default; updateModifierSlot-
            // Visibility hides the slot for those.
            var typeModOpts = FILTER_MODIFIER_OPTIONS_BY_TYPE[opt.value];
            var modDefault = (typeModOpts && typeModOpts.length > 0)
                ? typeModOpts[0]
                : { value: 'by-date', label: 'by date' };
            filterModifier = modDefault.value;
            var modSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
            if (modSlot) modSlot.textContent = modDefault.label;
            updateModifierSlotVisibility();
            // Public-visitor scope coupling. On the SPA the owner picks
            // scope independently (applyTypePersonalLock just unlocks
            // the slot for non-personal types); on public pages the
            // scope is host-anchored — bare @<host> for posts/comments,
            // and @<host>:network for profiles (the only valid profiles
            // data path on the public surface). Without this coupling,
            // clicking "profiles" left the scope at @<host>, the server
            // rejected the request, and the stream went blank.
            if (!document.body.classList.contains('is-owner')) {
                var hostDom = document.body.dataset.polisSiteDomain || paginationTenant;
                if (hostDom) {
                    filterScope = (opt.value === 'profiles')
                        ? '@' + hostDom + ':network'
                        : '@' + hostDom;
                    var sSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="scope"]');
                    if (sSlot) sSlot.textContent = labelForSlot('scope', filterScope);
                }
            }
            // Drafts and dms are inherently personal — they only exist
            // in the owner's local store. Force the
            // sentence to "all <type> from me by date" the moment the
            // user picks one, so the sentence reads truthfully even if
            // the prior qualifier/scope didn't match.
            applyTypePersonalLock(opt.value);
        } else if (slotKind === 'modifier') {
            filterModifier = opt.value;
        } else if (slotKind === 'qualifier') {
            // Owner SPA unlocks qualifier (all | new).
            filterQualifier = opt.value;
        } else if (slotKind === 'scope') {
            // Owner SPA scope selection. The 'site' value is
            // a typeahead sentinel — owner-extras.js intercepts to show
            // the input; on resolution it calls setFilterScope('@<handle>').
            // For non-site values we set filterScope directly.
            if (opt.value === 'site') {
                // Don't update filterScope yet; defer to typeahead resolution.
                // owner-extras.js opens the input here.
                closeOpenDropdown();
                return;
            }
            filterScope = opt.value;
            applyScopeModifierGuard();
        }
        // Update slot label in DOM. Pinned dropdown entries carry
        // navigational decoration in their label (e.g. '↩ my mutuals'
        // for the DM-scope return-to-inbox shortcut). That arrow is a
        // dropdown affordance — it shouldn't persist in the slot's
        // resting text. Re-derive a clean label from the canonical
        // option table when the picked entry is pinned.
        var slotEl = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="' + slotKind + '"]');
        if (slotEl) {
            slotEl.textContent = opt.pinned ? labelForSlot(slotKind, opt.value) : opt.label;
        }
        closeOpenDropdown();
        fireFilterChange();
        applyFilter();
    }

    // Programmatic scope setter for owner-extras.js / SPA
    // init / typeahead resolution. Updates the slot label in the DOM and
    // triggers a re-fetch via applyFilter. opts.label overrides the
    // displayed text (for @<handle>.polis.pub typeahead resolutions where
    // the URL value is "@alice.polis.pub" but the slot reads "@alice").
    function setFilterScope(value, opts) {
        filterScope = value || '';
        var slotEl = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="scope"]');
        if (slotEl) {
            var label = (opts && opts.label) || labelForSlot('scope', value);
            slotEl.textContent = label;
        }
        applyScopeModifierGuard();
        fireFilterChange();
        applyFilter();
    }

    // Programmatic type setter — symmetric with setFilterScope.
    // owner-extras.js icon presets call this to load a preset sentence
    // (gateway → setFilterType('activity')) without going through the
    // dropdown UX.
    function setFilterType(value, opts) {
        filterType = value || '';
        // Reset modifier alongside (matches selectOption logic).
        filterModifier = 'by-date';
        var slotEl = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="type"]');
        if (slotEl) {
            slotEl.textContent = (opts && opts.label) || labelForSlot('type', value);
        }
        var modSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
        if (modSlot) modSlot.textContent = 'by date';
        updateModifierSlotVisibility();
        applyTypePersonalLock(filterType);
        fireFilterChange();
        applyFilter();
    }

    // Drafts and dms are private/personal — they only exist in the
    // owner's local store. Forcing qualifier=all + scope=me when the user
    // picks one keeps the sentence truthful regardless of the prior slot
    // values. Also visually LOCK the scope slot
    // (sf-slot--locked = no underline, no click handler) so the user
    // can't try to change it after the fact and end up with a 400 from
    // the server. The lock is reverted when the user switches back to
    // a non-personal type.
    // When scope changes to 'me' on comments, "to bless"
    // becomes nonsensical (own comments don't need blessing). Reset
    // modifier to 'by-date' so the sentence stays valid.
    function applyScopeModifierGuard() {
        if (filterType === 'comments' && filterScope === 'me' && filterModifier === 'to-bless') {
            filterModifier = 'by-date';
            var modSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
            if (modSlot) modSlot.textContent = 'by date';
        }
    }

    // Type-change side effects on scope.
    //   drafts → lock scope to me (private store, no other scope makes sense)
    //   dms    → lock scope to my-mutuals (DMs are between mutuals)
    //   any other type → unlock the slot
    //
    // Do NOT reset scope when switching between non-personal types.
    // Forcing scope back to my-network on every non-personal type-change
    // would clobber the user's selected scope ("all comments from
    // all-polis" → switch type → unwanted "from my network"). Instead the
    // unlock path only restores the slot's interactive state without touching
    // filterScope; the existing scope value carries through. The
    // scope IS still updated when transitioning OUT of a personal
    // type (drafts/dms) since the prior scope was a forced lock —
    // we can't keep "me" / "my-mutuals" if those don't make sense
    // for the new type, so we still reset to my-network in that case.
    var __wasPersonalType = false;
    function applyTypePersonalLock(type) {
        // body.is-owner is set only on the owner SPA (app.js init).
        // Public / cross-visit views must keep the scope slot in its
        // initial sf-slot--identity state (locked to the host's
        // @<domain>). The unlock paths below assume an owner who can
        // legitimately switch between me / my-network / my-mutuals /
        // all-polis — none of which apply when the viewer is a
        // visitor reading the host's content. Bail early on public
        // pages so e.g. clicking the bio "N posts" link doesn't
        // suddenly make the scope slot interactive and expose owner-
        // context options on what should be a fixed-scope view.
        if (!document.body.classList.contains('is-owner')) return;
        var sSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="scope"]');
        if (!sSlot) return;
        var qSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="qualifier"]');

        function lockScope(value, label) {
            filterScope = value;
            filterQualifier = 'all';
            if (qSlot) qSlot.textContent = 'all';
            sSlot.textContent = label;
            sSlot.classList.remove('sf-slot--interactive');
            sSlot.classList.remove('sf-slot--identity');
            sSlot.classList.add('sf-slot--locked');
            sSlot.setAttribute('data-filter-locked', 'true');
            sSlot.removeAttribute('role');
            sSlot.removeAttribute('tabindex');
            sSlot.removeAttribute('aria-haspopup');
            sSlot.removeAttribute('aria-expanded');
        }
        function unlockScopeSlot(resetScope) {
            if (resetScope) {
                filterScope = 'my-network';
                sSlot.textContent = 'my network';
            }
            // Always re-apply the interactive classes + handlers — the
            // slot may have been sf-slot--locked from a prior personal-
            // type pick OR sf-slot--identity from the SSR public default,
            // OR already interactive. attachSlotHandlers is dedup'd via
            // __polisSlotWired so double-binding doesn't accumulate.
            sSlot.classList.remove('sf-slot--locked');
            sSlot.classList.remove('sf-slot--identity');
            sSlot.classList.add('sf-slot--interactive');
            sSlot.removeAttribute('data-filter-locked');
            sSlot.setAttribute('role', 'button');
            sSlot.setAttribute('tabindex', '0');
            sSlot.setAttribute('aria-haspopup', 'listbox');
            sSlot.setAttribute('aria-expanded', 'false');
            attachSlotHandlers(sSlot);
        }

        var personal = (type === 'drafts' || type === 'dms');
        if (type === 'drafts') {
            lockScope('me', 'me');
        } else if (type === 'dms') {
            // DM scope stays INTERACTIVE. Drafts truly lock
            // (the on-disk store has only one author — locking the slot
            // to "me" is honest), but DMs have multiple legitimate
            // scopes — "my mutuals" (the inbox) and "@<peer>" for each
            // active conversation. Locking the slot would prevent the user
            // from switching peers via the dropdown (which is already
            // being populated by owner-extras' refreshDMScopeOptions).
            // The peer handle is an interactive slot.
            // Default to my-mutuals (the inbox) when first arriving on
            // dms via the icon preset; the user picks a peer from the
            // dropdown to drill in.
            filterScope = 'my-mutuals';
            filterQualifier = 'all';
            if (qSlot) qSlot.textContent = 'all';
            sSlot.textContent = 'my mutuals';
            unlockScopeSlot(false);
        } else {
            // Non-personal type. Reset scope to my-network ONLY when
            // we're transitioning out of a personal type (where the
            // scope was a forced lock that doesn't carry forward
            // meaningfully). Switching between non-personal types
            // preserves the user's selected scope.
            unlockScopeSlot(__wasPersonalType);
            // Profiles narrows the legal scope set to
            // {my-network, all-polis}. If the carried-over scope is
            // outside that set (e.g. user was on posts/me, then
            // switched to profiles), coerce to my-network so the
            // sentence stays valid and the dropdown stays consistent
            // with scopeOptionsForCurrentType.
            if (type === 'profiles' &&
                filterScope !== 'my-network' &&
                filterScope !== 'all-polis') {
                filterScope = 'my-network';
                sSlot.textContent = 'my network';
            }
        }
        __wasPersonalType = personal;
    }

    function setFilterQualifier(value) {
        filterQualifier = value || 'all';
        var slotEl = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="qualifier"]');
        if (slotEl) slotEl.textContent = filterQualifier;
        fireFilterChange();
        applyFilter();
    }

    function setFilterModifier(value, opts) {
        filterModifier = value || 'by-date';
        var slotEl = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
        if (slotEl) slotEl.textContent = (opts && opts.label) || labelForSlot('modifier', filterModifier);
        fireFilterChange();
        applyFilter();
    }

    // Batch preset loader. owner-extras.js icon-row clicks
    // call this with all four slot values at once — single applyFilter
    // (one server fetch) instead of four cascaded fetches from calling
    // setFilterScope/Type/Qualifier/Modifier sequentially.
    //
    // preset shape: { qualifier, type, scope, modifier } — any subset.
    // opts shape: { qualifierLabel, typeLabel, scopeLabel, modifierLabel }
    //   — optional display-text overrides (e.g. scope value
    //   "@alice.polis.pub" rendered as "alice").
    //
    // Type changes implicitly reset the modifier to 'by-date' — same
    // behavior as selectOption('type', ...). If the preset specifies
    // an explicit modifier, it wins after the implicit reset.
    function setFilter(preset, opts) {
        // Warn on null/empty preset rather
        // than silently no-op; helps debugging when a caller forgot to
        // populate ICON_PRESETS or passed null by accident.
        if (!preset || typeof preset !== 'object') {
            console.warn('[polis-stream] setFilter called with empty/invalid preset:', preset);
            return;
        }
        opts = opts || {};

        // Defensive validation: caller-supplied type/qualifier values
        // must match a registered option. Catches typos like type='post'
        // (singular) before they propagate into the slot label and a
        // doomed server request. Scope is intentionally NOT validated
        // here — '@<handle>' values are dynamic.
        if (preset.type !== undefined) {
            var typeKnown = FILTER_TYPE_OPTIONS.some(function (o) { return o.value === preset.type; });
            if (!typeKnown) {
                console.warn('[polis-stream] setFilter rejected unknown type:', preset.type);
                return;
            }
        }
        if (preset.qualifier !== undefined && preset.qualifier !== 'all' && preset.qualifier !== 'new') {
            console.warn('[polis-stream] setFilter rejected unknown qualifier:', preset.qualifier);
            return;
        }

        if (preset.qualifier !== undefined) {
            filterQualifier = preset.qualifier;
            var qSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="qualifier"]');
            if (qSlot) qSlot.textContent = opts.qualifierLabel || labelForSlot('qualifier', filterQualifier);
        }
        if (preset.type !== undefined) {
            filterType = preset.type;
            var tSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="type"]');
            if (tSlot) tSlot.textContent = opts.typeLabel || labelForSlot('type', filterType);
            // Type-change resets modifier (matches selectOption).
            filterModifier = 'by-date';
            var modSlotReset = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
            if (modSlotReset) modSlotReset.textContent = 'by date';
            updateModifierSlotVisibility();
            // Same personal-lock side-effects selectOption +
            // setFilterType run. Without this, switching from a personal
            // type (drafts/dms) into a non-personal preset via setFilter
            // left the scope slot stuck locked from the prior state —
            // user couldn't change "from me" on "all comments from me".
            applyTypePersonalLock(filterType);
        }
        if (preset.scope !== undefined) {
            filterScope = preset.scope;
            var sSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="scope"]');
            if (sSlot) sSlot.textContent = opts.scopeLabel || labelForSlot('scope', filterScope);
        }
        // Modifier comes last so it wins over the type-change reset above.
        if (preset.modifier !== undefined) {
            filterModifier = preset.modifier;
            var mSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
            if (mSlot) mSlot.textContent = opts.modifierLabel || labelForSlot('modifier', filterModifier);
        }
        // Final guard — if the resolved combo is type=comments scope=me
        // modifier=to-bless, drop the modifier back to by-date.
        applyScopeModifierGuard();
        fireFilterChange();
        applyFilter();
    }

    // Modifier slot is visible only when the current type
    // has at least one modifier option in FILTER_MODIFIER_OPTIONS_BY_TYPE
    // (posts/comments/follows/drafts). For types with no modifier slot
    // (activity/dms/mentions) the slot stays [hidden] and the sentence
    // reads cleanly without a trailing slot.
    function updateModifierSlotVisibility() {
        var modSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
        if (!modSlot) return;
        var hasModifier = FILTER_MODIFIER_OPTIONS_BY_TYPE[filterType] && FILTER_MODIFIER_OPTIONS_BY_TYPE[filterType].length > 0;
        if (hasModifier) {
            modSlot.removeAttribute('hidden');
        } else {
            modSlot.setAttribute('hidden', '');
        }
    }

    function applyFilter() {
        // Clear all entries except the focus (SSR'd siblings + dynamics).
        // The focus stays — it's the page's main content; the filter
        // changes the surrounding stream. (Public per-post view only;
        // step 6 owner stream may handle this differently.)
        clearStreamForFilterChange();
        // Invalidate any in-flight fetch from the prior filter — its response
        // would otherwise append stale, off-filter entries (see the
        // paginationGeneration declaration). Bump BEFORE resetting the flags
        // so a late resolve compares against the new generation and bails.
        paginationGeneration++;
        // Reset pagination state so the next fetch starts fresh.
        // paginationInFlight MUST be cleared too: the refresh affordance
        // (setupPinnedDotRefresh) routes here, and if a scroll-triggered
        // fetch is mid-flight when the user clicks refresh, fetchNextPage's
        // `if (paginationInFlight) return` guard would swallow the refresh
        // and load nothing. The filter is unchanged on refresh, so any
        // late-arriving response from the prior fetch is dedup-safe
        // (appendItemIfNew keys on entry id).
        paginationInFlight = false;
        paginationInFlightTop = false;
        paginationCursor = '';
        paginationDone = false;
        paginationFirstFetch = true;
        // Cancel any pending retry from a prior failed fetch — the new
        // filter params would invalidate it anyway.
        paginationRetryAttempts = 0;
        if (paginationRetryTimer) {
            clearTimeout(paginationRetryTimer);
            paginationRetryTimer = null;
        }
        if (paginationLastEntry && paginationObserver) {
            paginationObserver.unobserve(paginationLastEntry);
            paginationLastEntry = null;
        }
        // Reset upward direction symmetrically — same filter applies to
        // both directions, so any in-flight upward fetch's results
        // would no longer match the new filter params.
        paginationCursorTop = '';
        paginationDoneTop = false;
        paginationFirstFetchTop = true;
        paginationRetryAttemptsTop = 0;
        if (paginationRetryTimerTop) {
            clearTimeout(paginationRetryTimerTop);
            paginationRetryTimerTop = null;
        }
        if (paginationFirstEntry && paginationTopObserver) {
            paginationTopObserver.unobserve(paginationFirstEntry);
            paginationFirstEntry = null;
        }
        // If the observer was never set up (no canonical link),
        // setupPagination's earlier bail-out left it null. Try once
        // more — if the bail still applies (fetch unsupported, etc.),
        // fetchNextPage's checks below will short-circuit cleanly.
        if (paginationObserver === null) {
            setupPagination();
        }
        if (paginationTopObserver === null && !paginationDoneTop) {
            setupPaginationTop();
        }
        // Trigger a fetch with the new filter params. fetchNextPage
        // builds the URL from filterType / filterModifier / paginationTenant.
        // Upward direction does NOT auto-fetch on filter change — the
        // focus is the only entry left after clearStreamForFilterChange,
        // and the user has to scroll up past it to trigger fetchPrevious
        // Page.
        fetchNextPage();
        // Re-anchor the upward observer ONLY if the user has previously
        // engaged scroll-up. If they haven't, leave the observer
        // detached — the scroll-direction gate in setupUserScrollGate
        // will attach it on first scroll-up. This preserves the design
        // intent that filter-change is a fresh stream and upward
        // pagination is opt-in via scroll direction.
        if (userHasScrolledUp && paginationTopObserver) {
            observeFirstEntry();
        }
    }

    function clearStreamForFilterChange() {
        var container = getStreamContainer();
        if (!container) return;
        // Preserve the SSR'd focus — identified by the cross-tenant
        // body-extraction protocol marker, NOT by the .is-focused class.
        // Scroll-driven focus tracking may have migrated .is-focused to
        // a sibling or dynamic entry by the time filter changes; in
        // that case the previous-filter's dynamic entry would survive
        // clearing and pollute the new filter's stream (e.g. a follow
        // entry sticking around after switching to type=posts).
        var ssrMarker = container.querySelector('.entry .focus-content[data-polis-focus="true"]');
        var ssrFocus = ssrMarker ? ssrMarker.closest('.entry') : null;
        var allEntries = container.querySelectorAll('.entry');
        for (var i = 0; i < allEntries.length; i++) {
            var entry = allEntries[i];
            if (entry === ssrFocus) continue;
            // Pinned entries (data-polis-pinned) survive
            // filter changes. The owner-side inline editor card
            // (data-polis-pinned="editor") needs to stay put while
            // the user changes filter context around it — pinned
            // entries are sticky-across-filter-changes only,
            // and this is where that stickiness lives. Other pinned
            // markers reserved for future surfaces (compose drafts in
            // a different shape, etc.).
            if (entry.hasAttribute('data-polis-pinned')) continue;
            if (observer) observer.unobserve(entry);
            if (prefetchObserver) prefetchObserver.unobserve(entry);
            entry.parentNode.removeChild(entry);
        }
        // Restore .is-focused on the SSR focus — if it had migrated to
        // a removed entry above, the class is gone now. Idempotent
        // when the SSR focus already had it.
        if (ssrFocus) ssrFocus.classList.add('is-focused');
        // Rebuild entries[] from what's left (typically just the focus).
        entries = Array.prototype.slice.call(container.querySelectorAll('.entry'));
        focusedEntry = ssrFocus || entries[0] || null;
        // Focus visibility tracks filterType: focus is always a post, so
        // when type=comments / type=follows the focus doesn't belong in
        // the result set and would otherwise show as ghost content above
        // the new entries. Hide it via CSS class (kept in DOM so
        // switching back to type=posts brings it right back without a
        // page reload).
        applyFocusVisibilityForFilter();
        intersectingMap.clear();
        // Remove any leftover empty-state element from a prior filter change.
        var emptyState = container.querySelector(':scope > .stream-empty-state');
        if (emptyState) emptyState.parentNode.removeChild(emptyState);
        // Clear the empty-state mode flag so the focus entry + year-marker
        // come back into view on the next render cycle (renderEmptyState
        // re-applies it if the new fetch also yields zero items).
        setEmptyStateMode(false);
        // Fresh filter → let the messages empty state auto-open the composer
        // again (it's gated to once per arrival; see renderEmptyState).
        dmEmptyComposerOpened = false;
    }

    // Toggle focus-entry visibility based on the current filterType.
    // The focus is always a post on per-post pages; for type=comments
    // and type=follows it doesn't belong in the rendered list. Hidden
    // via CSS class so switching back to type=posts restores it
    // without a page reload.
    function applyFocusVisibilityForFilter() {
        var focus = document.querySelector('.entry.is-focused');
        if (!focus) return;
        // The SSR focus is PRESERVED across filter changes
        // (clearStreamForFilterChange keeps it as the page's main content),
        // so it has to be hidden whenever it wouldn't actually appear in the
        // active filter's result set — otherwise it shows as a "ghost" first
        // row that doesn't match the filter.
        //   - filterType must be posts (the focus is always a post; comments/
        //     follows/profiles/dms exclude it structurally).
        //   - with-comments narrows to posts with >=1 blessed comment (server
        //     `with=comments`). A focus whose comment_count is 0 is excluded
        //     server-side, so it must be hidden here too — otherwise a
        //     zero-comment post leads the "with comments" stream (the bug that
        //     surfaced the canonical post above the real results).
        var belongs = (filterType === 'posts');
        if (belongs && filterModifier === 'with-comments') {
            var count = parseInt(focus.dataset.polisCommentCount || '0', 10) || 0;
            if (count <= 0) belongs = false;
        }
        if (belongs) {
            focus.classList.remove('is-hidden-by-filter');
        } else {
            focus.classList.add('is-hidden-by-filter');
        }
        // Top-fade was anchored on the (now-hidden) focus or above-focus
        // sibling SSR'd into the page. Recompute against the new
        // first-visible entry so the fade isn't stranded over the top
        // of the rendered list.
        updateTopFadeVisibility();
    }

    function renderEmptyState() {
        var container = getStreamContainer();
        if (!container) return;

        // Messages surface: no "no messages yet" headline or "send a message"
        // CTA. The empty view IS the New Message composer — auto-open it (it
        // takes the ghost-compose slot). Closing it restores the "yours,
        // truly" ghost via closeDMComposer, and that's all that shows; the
        // once-per-arrival guard (dmEmptyComposerOpened, reset on filter
        // change) stops a refresh from reopening it after a deliberate close.
        // setEmptyStateMode(true) still suppresses the SSR focus post so a
        // stray post doesn't sit under the composer/ghost. On read-only
        // localhost (composer can't open) we just leave the ghost — the
        // read-only banner already explains why compose is disabled.
        if (filterType === 'dms') {
            setEmptyStateMode(true);
            var dmExt = window.PolisOwnerExtras;
            var canCompose = document.body.classList.contains('is-owner') &&
                dmExt && typeof dmExt.openEditor === 'function' &&
                !(window.App && typeof window.App.dmMessagingReadOnly === 'function' && window.App.dmMessagingReadOnly());
            var composerOpen = dmExt && typeof dmExt.isDMComposerOpen === 'function' && dmExt.isDMComposerOpen();
            if (canCompose && !dmEmptyComposerOpened && !composerOpen) {
                dmEmptyComposerOpened = true;
                dmExt.openEditor(); // dispatches to the DM composer (new / reply by scope)
            }
            return;
        }

        // One-line explanation. Composed against the current slot
        // values so the message reads truthfully ("no comments from
        // my-network yet"). The headline + a contextual CTA carry the
        // empty surface.
        var typeLabel = filterType || 'items';
        // paginationTenant is empty in the owner SPA homepage
        // (no canonical post on the page to derive it from). Compose
        // the "from <X>" clause from the current scope slot instead so
        // the message reads correctly across all filter combinations.
        var scopeSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="scope"]');
        var scopeText = scopeSlot ? (scopeSlot.textContent || '').trim() : '';
        var fromLabel = scopeText
            ? 'from ' + scopeText
            : (paginationTenant ? 'from ' + paginationTenant : '');
        var detail;
        if (filterType === 'posts' && filterModifier === 'with-comments') {
            detail = ('no posts ' + fromLabel + ' with blessed comments yet.').replace(/\s+/g, ' ');
        } else if (filterType === 'comments' && filterModifier === 'to-bless') {
            // Bless-pending message reads simpler without the
            // "from <scope>" tail — the user is asking "what needs my
            // attention?" not "what does scope X have?".
            detail = 'no comments to bless yet.';
        } else {
            detail = ('no ' + typeLabel + ' ' + fromLabel + ' yet.').replace(/\s+/g, ' ');
        }

        var wrap = el('div', { cls: 'stream-empty-state', attrs: { 'aria-live': 'polite' } });

        // Timeline-dot retained on the bar in empty-state mode so the
        // bar visually terminates here rather than against blank space.
        var dot = el('div', { cls: 'timeline-dot', ariaHidden: true });
        wrap.appendChild(dot);

        // One-line explanation.
        wrap.appendChild(el('p', { cls: 'stream-empty-state-headline', text: detail }));

        // CTA — context-aware. Each filter type gets a CTA
        // that either invokes the most useful local action or links
        // to a place where the user can do something productive.
        var ownerMode = document.body.classList.contains('is-owner') &&
            window.PolisOwnerExtras &&
            typeof window.PolisOwnerExtras.openEditor === 'function';
        var ctaLabel = 'show all posts';
        var ctaAction = function () {
            filterModifier = 'by-date';
            var modSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
            if (modSlot) modSlot.textContent = 'by date';
            selectOption('type', { label: 'posts', value: 'posts' });
        };
        // Shared "find people to follow" action: dismiss the empty state and
        // load the all-polis profile directory in-app (the DS site list). Used
        // by the profiles + comments empty states so "discover sites to follow"
        // keeps the reader inside the app instead of navigating to
        // discover.polis.pub.
        var goToAllPolisProfiles = function () {
            if (wrap.parentNode) wrap.parentNode.removeChild(wrap);
            setEmptyStateMode(false);
            setFilter(
                { qualifier: 'all', type: 'profiles', scope: 'all-polis', modifier: 'by-name' },
                { qualifierLabel: 'all', typeLabel: 'profiles', scopeLabel: 'all polis', modifierLabel: 'by name' }
            );
        };
        if (ownerMode) {
            switch (filterType) {
                case 'posts':
                case 'drafts':
                case 'activity':
                    ctaLabel = 'create post';
                    ctaAction = function () {
                        if (wrap.parentNode) wrap.parentNode.removeChild(wrap);
                        setEmptyStateMode(false);
                        window.PolisOwnerExtras.openEditor();
                    };
                    break;
                case 'comments':
                    // When on "all comments to bless" with no
                    // pending requests, drop into the broader comments
                    // view via applyFilter(scope=all-polis,
                    // modifier=by-date) so the user can browse what's
                    // out there. The scope-modifier guard at
                    // applyTypePersonalLock keeps the sentence valid.
                    if (filterModifier === 'to-bless') {
                        ctaLabel = 'browse all comments';
                        ctaAction = function () {
                            if (wrap.parentNode) wrap.parentNode.removeChild(wrap);
                            setEmptyStateMode(false);
                            filterModifier = 'by-date';
                            var modSlot = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="modifier"]');
                            if (modSlot) modSlot.textContent = 'by date';
                            applyFilter();
                        };
                        break;
                    }
                    ctaLabel = 'discover sites to follow';
                    ctaAction = goToAllPolisProfiles;
                    break;
                case 'profiles':
                    // Keep the reader IN the app: switch to the all-polis
                    // profile directory so they can find authors to follow,
                    // rather than bouncing them out to discover.polis.pub.
                    ctaLabel = 'discover sites to follow';
                    ctaAction = goToAllPolisProfiles;
                    break;
                default:
                    ctaLabel = 'show all posts';
            }
        }
        var cta = el('a', {
            cls: 'stream-empty-state-cta',
            href: '#',
            attrs: { role: 'button' },
            text: ctaLabel,
        });
        cta.addEventListener('click', function (ev) {
            ev.preventDefault();
            ctaAction();
        });
        wrap.appendChild(cta);

        var fade = container.querySelector(':scope > .stream-fade-out');
        if (fade) container.insertBefore(wrap, fade);
        else container.appendChild(wrap);

        // Hide the SSR'd focus entry + year-marker while the empty state
        // is showing. The focus is a single post that doesn't satisfy the
        // current filter (otherwise we wouldn't be here), so showing it
        // alongside an "all is quiet" message reads as contradictory. The
        // class flips off in clearStreamForFilterChange (next filter
        // change) which restores the focus + marker.
        setEmptyStateMode(true);
    }

    function setEmptyStateMode(active) {
        var layoutRight = document.querySelector('.layout-right');
        if (!layoutRight) return;
        if (active) {
            layoutRight.classList.add('is-empty-state');
        } else {
            layoutRight.classList.remove('is-empty-state');
        }
    }

    /* ------------------------------------------------------------------
       Boot + public API
       ------------------------------------------------------------------ */

    // Public per-post artifacts use <script defer>, so DOM is parsed by
    // script-execution time. Owner SPA's index.html may load this without
    // defer — guard against early script execution by checking readyState.
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    // Public surface for downstream consumers (lazy fetch, filter integration,
    // owner-SPA compose flow). Read-only by convention; mutating these is
    // undefined behavior.
    window.PolisStream = {
        renderEntry: renderEntry,
        renderers: renderers,
        appendEntry: function (domEl) {
            // Mark as dynamic so clearDynamicEntries can find it later.
            if (domEl) domEl.classList.add('is-dynamic');
            return appendEntry(domEl);
        },
        clearDynamicEntries: clearDynamicEntries,
        getEntries: function () { return entries.slice(); },
        getFocusedEntry: function () { return focusedEntry; },
        announce: announce,
        focusNext: focusNext,
        focusPrev: focusPrev,
        // Comment-panel API. Programmatic open/close
        // of the focus entry's blessed-comment panel; click handlers in
        // setupCommentsPanel call into the same functions.
        openComments: openCommentsPanel,
        closeComments: closeCommentsPanel,
        toggleComments: toggleCommentsPanel,
        // Read-focus exit hook. Owner SPA wires this into the write-focus
        // openers (post editor, comment editor) so read-focus exits
        // cleanly when the user pivots from "I'm reading this" to "I'm
        // editing this" — otherwise both modes overlay. Applies to all
        // editor entry points. Safe to call when no entry is in focus — no-ops.
        exitFocusMode: exitFocusMode,
        // Read-focus ENTRY + the in-focus comment slot. The owner SPA's
        // in-focus comment editor wires against these: enterFocusMode opens
        // read-focus on the post; ensureFocusCommentSlot returns the swappable
        // slot to mount the editor into (creating it even when the post has no
        // comment yet); setFocusComment caches a comment payload (e.g. the
        // just-submitted one); showFocusComment re-renders the cached comment
        // into the slot (used by the editor's close path so the comment fills
        // the slot when the editor goes away). All no-op safely off-focus.
        enterFocusMode: enterFocusMode,
        ensureFocusCommentSlot: ensureFocusCommentSlot,
        setFocusComment: setFocusComment,
        showFocusComment: showFocusComment,
        // onEscape is the canonical name. registerPopoverCloseHandler kept
        // as an alias for any consumer that already wires against it; safe
        // to remove once internal callers are migrated.
        onEscape: registerPopoverCloseHandler,
        registerPopoverCloseHandler: registerPopoverCloseHandler,
        // Extension registries. Layer 3 + Layer 4 of the
        // layered extension model. owner-extras.js wires against these;
        // public bundle never subscribes.
        afterRender: addAfterRenderListener,
        registerRenderer: registerRenderer,
        // Filter-option extension point. owner-extras.js
        // registers owner-only values (e.g., type=activity, scope=
        // my-mutuals) via PolisStream.registerFilterOption(slot, opt).
        registerFilterOption: registerFilterOption,
        replaceFilterOptions: replaceFilterOptions,
        // DM scope dropdown: owner-extras feeds the recent-conversations
        // list (top 10, sorted by recency) so the scope dropdown for
        // type=dms can serve as a fast-switcher across active threads.
        // The pinned "↩ my mutuals" entry is added automatically by the
        // controller; this setter only receives the dynamic per-thread
        // entries.
        setDMScopeOptions: setDMScopeOptions,
        // owner-extras unlocks qualifier + scope at
        // init so the owner can toggle "all"/"new" and switch scopes
        // via the sentence filter. Public surface stays SSR-locked.
        unlockSlot: unlockSlot,
        // Programmatic filter setters. Owner SPA assigns
        // initial scope at init; icon-row presets call these to load
        // preset sentences without going through dropdown UX. All four
        // trigger applyFilter() which clears + refetches.
        setFilterScope: setFilterScope,
        setFilterType: setFilterType,
        setFilterQualifier: setFilterQualifier,
        setFilterModifier: setFilterModifier,
        // Batch preset loader. Sets qualifier / type /
        // scope / modifier in one pass and triggers a single
        // applyFilter (one server fetch) — avoids the cascaded
        // re-fetches you'd get from calling the individual setters
        // sequentially.
        setFilter: setFilter,
        // Re-run the bio-toggle measurement after owner-extras
        // populates the bio HTML. Idempotent — calling it again just
        // re-attaches handlers and re-measures overflow.
        setupAboutToggle: setupAboutToggle,
        // Filter-change subscription.
        // Listener is invoked AFTER any of the setFilter* / setFilter
        // / selectOption mutations, with a snapshot
        // {qualifier,type,scope,modifier}. owner-extras uses this to
        // sync body.is-activity-mode against filterType regardless of
        // which code path mutated the state.
        onFilterChange: addFilterChangeListener,
        // Current filter state, same shape as the onFilterChange snapshot.
        // addFilterChangeListener does NOT replay to new subscribers, and
        // the initial fireFilterChange happens during hydration (before
        // late subscribers like owner-extras attach), so a getter lets a
        // subscriber seed its own state from the live filter on subscribe.
        getFilter: function () {
            return {
                qualifier: filterQualifier,
                type: filterType,
                scope: filterScope,
                modifier: filterModifier,
            };
        },
        // Re-trigger applyFilter without
        // mutating state. owner-extras calls this after a successful
        // publish to pull the just-published post into the stream.
        // Equivalent to "press refresh on the current filter."
        refresh: function () { applyFilter(); },
        // Search wiring for "all profiles from
        // all of polis". setSearchQuery mutates the module-local
        // search string and re-runs the filter (clears the stream +
        // re-fetches with &search=). Called from the .entry--search
        // input keyup handler in owner-extras.js, debounced 250ms.
        setSearchQuery: function (q) {
            var next = (typeof q === 'string') ? q : '';
            if (next === currentSearch) return;
            currentSearch = next;
            applyFilter();
        },
        getSearchQuery: function () { return currentSearch; },
    };
})();
