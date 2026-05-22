// Polis Local App — client-side JavaScript for the owner SPA.
//
// =============================================================================
// HANDBOOK TRAIL MARKER
// =============================================================================
// This file is part of the URL-as-filter thread. It intercepts every navigation
// inside /_/, dispatches /_/pql/<sentence> through the PQL parser, and hands
// the result to the v4 stream controller.
//
// Trail across files (URL-as-filter):
//   producer  — this file (app.js): navigateTo() + _navigateToPQL()
//   grammar   — pql.js (loaded alongside): PQL.parse / .compose / .parseURL
//   consumer  — bundle-assets/pub.polis.core/shapes/v4/stream.js: hydration
//
// Pull the thread (concept docs):
//   github.com/vdibart/polis-cli/blob/main/docs/general/pql.md
//   github.com/vdibart/polis-cli/blob/main/docs/general/infinity-stream.md
//
// Guided tour (curated walkthrough of this thread):
//   github.com/vdibart/polis-cli/blob/main/docs/handbook/url-as-filter.md
//
// Start here if you're new:
//   github.com/vdibart/polis-cli/blob/main/AGENTS.md
// =============================================================================

const App = {
    currentDraftId: null,
    currentPostPath: null,  // Set when editing a published post
    currentFrontmatter: '',  // Stored frontmatter block for published posts
    currentCommentDraftId: null,
    currentView: 'conversations',  // Current active view in sidebar
    sidebarMode: 'my-site',  // 'my-site' or 'social'
    filenameManuallySet: false,  // Track if user manually edited the filename
    lifecycleStage: 'just_arrived',  // 'just_arrived', 'first_post', or 'active'

    // Setup wizard state
    setupWizardStep: 0,       // 0=configure, 1=deploy, 2=register
    setupWizardDeployTimer: null,
    setupWizardDismissed: false,
    siteRegistered: false,

    // Site info (loaded from /api/settings)
    siteInfo: null,

    // Webapp theme: 'light' or 'dark'
    webappTheme: 'dark',

    // Hosted mode: set via window.__POLIS_HOSTED by the hosted service
    isHosted: !!(window.__POLIS_HOSTED),

    // Snippet state (used by about editor for preview)
    snippetState: {
        editingPath: 'about.md',
    },

    // Data cache for counts
    counts: {
        posts: 0,
        drafts: 0,
        // My comments (outgoing)
        myPending: 0,
        myBlessed: 0,
        myDenied: 0,
        myCommentDrafts: 0,
        // Incoming (on my posts)
        incomingPending: 0,
        incomingBlessed: 0,
        // Social
        feedUnread: 0,
        following: 0,
        followers: 0,
        // DM
        dmUnread: 0,
        // Per-surface "new since last view" flags driving the three
        // topbar nav dots (gateway / comment / envelope). Cleared by
        // App.markSurfaceViewed when the corresponding filter view
        // becomes active.
        hasNewFeed: false,
        hasNewBlessingInbox: false,
        hasNewDM: false,
    },

    // SSE connection + polling fallback
    _eventSource: null,
    _countsPollTimer: null,

    // Comments published state
    _commentsPublishedFilter: 'all',

    // Conversations subtab state
    _conversationsSubtab: 'all',
    _conversationsRefreshing: false,

    // Feed filter state (natural-language sentence filter)
    _feedShowNew: false,        // "New" vs "All" in sentence
    _feedAuthorFilter: null,    // null or domain string (legacy, still used for by-author)
    _feedTimeFilter: '24h',     // '1h', '24h', '2d', '7d', '30d'
    _feedContentType: '',       // '' (items), 'post', 'comment', 'announcement'
    _feedScope: 'network',      // 'network', 'followers', 'global'
    _feedPopoverOpen: null,     // currently open popover filter name, or null
    _feedFilterOnly: false,     // true when re-render is from filter change only (skip DS sync)
    _feedPendingScrollY: 0,     // scroll position to restore after feed render (from sessionStorage)
    _feedStateRestored: false,  // guard flag to prevent double-restore of feed state
    _feedObserver: null,        // IntersectionObserver for viewport-based read marking
    _markReadQueue: [],         // batched item IDs waiting to be marked read
    _markReadTimer: null,       // debounce timer for batch mark-read
    // _feedHasNewTimer removed — feed dot now driven by has_new_feed in counts payload
    _feedEditorOpen: false,
    _feedEditorTitle: '',
    _feedEditorBody: '',
    _feedEditorStatus: '',
    _feedEditorDraftId: null,
    _feedEditorSaveTimer: null,

    // Screen management
    screens: {
        welcome: document.getElementById('welcome-screen'),
        error: document.getElementById('error-screen'),
        dashboard: document.getElementById('dashboard-screen'),
        // step-06/6.c — v4 stream layout container, shown for any PQL
        // filter view. Bare `/_/` lands here; `/_/settings` switches
        // to the dashboard wrapper above.
        stream: document.getElementById('stream-screen'),
    },

    // Site base URL for live links
    siteBaseUrl: '',

    // Custom avatar config (from .well-known/polis)
    avatarConfig: null,
    _pendingAvatar: null,

    // Display name (from .well-known/polis author_name)
    authorName: '',

    // Remote avatar cache: domain -> { avatar, author_name } or null
    _remoteAvatarCache: {},
    _remoteAvatarFetching: {},

    // Auto-save state
    _autoSaveTimer: null,
    _focusMode: false,

    // Inline comment editor state
    _inlineCommentOpen: false,
    _inlineCommentUrl: null,
    _inlineCommentAuthor: '',
    _inlineCommentTitle: '',
    _inlineCommentBody: '',
    _inlineCommentDraftId: null,
    _inlineCommentSaveTimer: null,
    _inlineCommentFocusMode: false,

    // Slash menu state
    _slashMenuVisible: false,
    _slashMenuIndex: 0,
    _slashActiveEditorId: null,
    _linkEditorId: null,

    // Milkdown editor state
    _rawMode: {},  // textareaId -> boolean
    _milkdownReady: false,

    // Milkdown textarea-to-mount mapping (static + dynamic sibling lookup)
    _milkdownIdFor(textareaId) {
        const map = {
            'markdown-input': 'milkdown-post',
            'comment-input': 'milkdown-comment',
            'about-editor-textarea': 'milkdown-about',
            'mc-about-textarea': 'mc-milkdown-about',
            'inline-comment-body': 'milkdown-inline-comment',
            'inline-comment-body-focus': 'milkdown-inline-comment-focus',
        };
        if (map[textareaId]) return map[textareaId];
        const el = document.getElementById(textareaId);
        if (el) {
            const mount = el.parentElement?.querySelector('.milkdown-mount');
            if (mount?.id) return mount.id;
        }
        return null;
    },

    // Find which milkdown-mount contains the current selection
    _getActiveEditorId() {
        const sel = window.getSelection();
        if (!sel.rangeCount) return null;
        const node = sel.getRangeAt(0).commonAncestorContainer;
        const el = node.nodeType === 1 ? node : node.parentElement;
        const mount = el?.closest('.milkdown-mount');
        return mount?.id || null;
    },

    // Get content from Milkdown or textarea (abstraction layer)
    getEditorContent(textareaId) {
        const editorId = this._milkdownIdFor(textareaId);
        if (editorId && window.MilkdownBridge?.isReady(editorId) && !this._rawMode[textareaId]) {
            return window.MilkdownBridge.getMarkdown(editorId);
        }
        const el = document.getElementById(textareaId);
        return el ? el.value : '';
    },

    // Set content in both Milkdown and textarea (abstraction layer)
    setEditorContent(textareaId, markdown) {
        const el = document.getElementById(textareaId);
        if (el) el.value = markdown;
        const editorId = this._milkdownIdFor(textareaId);
        if (editorId && window.MilkdownBridge?.isReady(editorId) && !this._rawMode[textareaId]) {
            window.MilkdownBridge.setMarkdown(editorId, markdown);
        }
    },

    // Lazy-load the Milkdown bridge on first call. The bridge module
    // is dynamically imported (no static <script type="module"> at boot
    // any more) so the read-only flow never pays the esm.sh fetch
    // cost. Memoized via _milkdownLoadPromise so concurrent callers
    // share one import. The importmap entries (index.html) use
    // esm.sh's default unbundled mode so all submodules share a
    // single @milkdown/core instance via URL-level HTTP caching —
    // ?bundle was tried for perf but inlined a private kit/core per
    // submodule, breaking the context-system schema lookup. See the
    // explanatory comment above the importmap in index.html.
    _loadMilkdownBridge() {
        if (!this._milkdownLoadPromise) {
            this._milkdownLoadPromise = import('/milkdown-editor.js').catch((err) => {
                console.warn('Milkdown bridge failed to load:', err);
                this._milkdownLoadPromise = null; // allow retry on next open
                throw err;
            });
        }
        return this._milkdownLoadPromise;
    },

    // Initialize Milkdown for a screen's editor. Awaits the lazy
    // bridge load on first call; subsequent calls fast-path through
    // the cached MilkdownBridge global.
    async _initMilkdown(textareaId) {
        const editorId = this._milkdownIdFor(textareaId);
        if (!editorId) return;
        if (!window.MilkdownBridge) {
            try {
                await this._loadMilkdownBridge();
            } catch (err) {
                this._showTextareaFallback(textareaId);
                return;
            }
        }
        if (!window.MilkdownBridge) {
            this._showTextareaFallback(textareaId);
            return;
        }
        // Use textarea value, but if the container already has user-typed text
        // (milkdown module loaded late), prefer that content
        const container = document.getElementById(editorId);
        const userTyped = container ? container.textContent.trim() : '';
        const textarea = document.getElementById(textareaId);
        const content = userTyped || (textarea ? textarea.value : '');
        try {
            await window.MilkdownBridge.create(editorId, content);
        } catch (err) {
            console.warn('Milkdown init failed, falling back to textarea:', err);
            this._showTextareaFallback(textareaId);
        }
    },

    // Destroy Milkdown instance for a screen
    _destroyMilkdown(textareaId) {
        const editorId = this._milkdownIdFor(textareaId);
        if (!editorId || !window.MilkdownBridge) return;
        // Sync content back to textarea before destroying
        if (window.MilkdownBridge.isReady(editorId) && !this._rawMode[textareaId]) {
            const el = document.getElementById(textareaId);
            if (el) el.value = window.MilkdownBridge.getMarkdown(editorId);
        }
        window.MilkdownBridge.destroy(editorId);
    },

    // Show textarea fallback when Milkdown fails to load
    _showTextareaFallback(textareaId) {
        const textarea = document.getElementById(textareaId);
        if (textarea) textarea.classList.remove('hidden');
        const editorId = this._milkdownIdFor(textareaId);
        if (editorId) {
            const mount = document.getElementById(editorId);
            if (mount) mount.classList.add('hidden');
        }
    },

    // Toggle raw mode for an editor
    _toggleRawMode(textareaId, toggleBtn) {
        const editorId = this._milkdownIdFor(textareaId);
        const textarea = document.getElementById(textareaId);
        const mount = editorId ? document.getElementById(editorId) : null;

        if (this._rawMode[textareaId]) {
            // Switch from raw to WYSIWYG
            this._rawMode[textareaId] = false;
            if (toggleBtn) toggleBtn.textContent = 'Raw';
            if (mount && window.MilkdownBridge) {
                mount.classList.remove('hidden');
                textarea.classList.add('hidden');
                window.MilkdownBridge.setMarkdown(editorId, textarea.value);
            }
        } else {
            // Switch from WYSIWYG to raw
            if (editorId && window.MilkdownBridge?.isReady(editorId)) {
                textarea.value = window.MilkdownBridge.getMarkdown(editorId);
            }
            this._rawMode[textareaId] = true;
            if (toggleBtn) toggleBtn.textContent = 'WYSIWYG';
            if (mount) mount.classList.add('hidden');
            textarea.classList.remove('hidden');
            textarea.focus();
        }
    },

    showScreen(name) {
        // Destroy Milkdown instances when leaving editor screens
        const prevScreens = Object.entries(this.screens)
            .filter(([, s]) => s && !s.classList.contains('hidden'))
            .map(([k]) => k);
        for (const prev of prevScreens) {
            if (prev === 'editor') {
                this._destroyMilkdown('markdown-input');
            }
            if (prev === 'comment') this._destroyMilkdown('comment-input');
        }

        // Hide editor controls when leaving editor
        const editorControls = document.getElementById('editor-controls');
        if (editorControls) editorControls.classList.add('hidden');

        // Show global nav for all screens except welcome/error
        const iconNav = document.getElementById('icon-nav');
        if (iconNav) iconNav.classList.toggle('hidden', name === 'welcome' || name === 'error');

        // Clear v4 stream-controller body classes when leaving the stream
        // view. Without this, a user who entered per-entry focus mode
        // (click on an entry) or editor focus mode (Ctrl+Shift+F) and
        // then navigated away via the avatar dropdown (Settings / Edit
        // About) would carry body.focus-mode or body.is-editor-focus into
        // the destination screen — both classes have stream.css rules
        // that hide .polis-topbar, which is also on the SPA's icon-nav.
        // Result: the icon-nav becomes invisible/unclickable until the
        // user reloads. Stream.js's exitFocusMode only fires when the
        // user explicitly closes focus from within the stream view; the
        // avatar dropdown bypasses that.
        //
        // Same place: clear .active state from preset icons when leaving
        // the stream. The active class is set by owner-extras' loadPreset
        // when a preset fires; once the user navigates away from /feed
        // (Settings / Edit About / editor), the previously-active icon
        // would stay highlighted on the destination screen, suggesting
        // a state ("you are viewing my-posts") that's no longer true.
        if (name !== 'stream') {
            document.body.classList.remove('focus-mode');
            document.body.classList.remove('is-editor-focus');
            const presetBtns = document.querySelectorAll('#icon-nav .nav-icon-btn[id^="nav-btn-"]');
            presetBtns.forEach(btn => btn.classList.remove('active'));
        }

        // step-06/6.c — show the v4 filter widget only on stream-screen.
        // Other screens (legacy dashboard, editor, etc.) hide it so the
        // sentence doesn't confusingly hover over unrelated content.
        const filterEl = document.getElementById('polis-topbar-filter');
        if (filterEl) filterEl.classList.toggle('hidden', name !== 'stream');
        // Pinned-dot is only meaningful when the stream layout is live
        // (it visually marks where the timeline bar terminates).
        const pinnedDot = document.querySelector('#icon-nav .pinned-dot');
        if (pinnedDot) pinnedDot.classList.toggle('hidden', name !== 'stream');

        Object.values(this.screens).forEach(s => {
            if (s) s.classList.add('hidden');
        });
        if (this.screens[name]) {
            this.screens[name].classList.remove('hidden');
        }
        // The 'editor' and 'comment' screen branches were retired in
        // chunk A — those screens no longer exist. Inline editor cards
        // on the v4 stream-screen handle post + comment composition.
    },

    // Theme management
    setWebappTheme(theme) {
        if (theme !== 'light' && theme !== 'dark') theme = 'dark';
        this.webappTheme = theme;
        document.documentElement.dataset.theme = theme;
        try { localStorage.setItem('polis-webapp-theme', theme); } catch (e) {}
        // Persist to server (fire and forget)
        fetch('/api/settings/webapp-theme', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ theme }),
        }).catch(() => {});
    },

    _initTheme(serverTheme) {
        // Priority: localStorage > server config > fallback dark
        let theme;
        try { theme = localStorage.getItem('polis-webapp-theme'); } catch (e) {}
        if (!theme && serverTheme) theme = serverTheme;
        if (!theme) theme = 'dark';
        this.webappTheme = theme;
        document.documentElement.dataset.theme = theme;
    },

    // ── Icon Nav: Avatar, badges, dropdown, active state ──

    // Render avatar button with user initial
    _renderAvatar() {
        const btn = document.getElementById('nav-avatar');
        if (!btn) return;
        if (this.avatarConfig) {
            // Custom avatar: use the saved config with pattern/border/etc
            btn.setAttribute('style', this._buildAvatarStyle(this.avatarConfig));
            btn.textContent = '';  // custom avatars don't show initials
        } else {
            // Default: deterministic from domain
            const domain = this.siteBaseUrl ? (() => { try { return new URL(this.siteBaseUrl).hostname; } catch(e) { return ''; } })() : '';
            const det = this.domainToAvatar(domain || 'me');
            btn.setAttribute('style', `background: ${det.color};`);
            btn.textContent = det.initials;
        }
    },

    // Populate avatar hover menu with user data from /api/nav/state
    async _renderAvatarMenu() {
        const nameEl = document.getElementById('avatar-menu-name');
        const handleEl = document.getElementById('avatar-menu-handle');
        const statsEl = document.getElementById('avatar-menu-stats');

        try {
            const state = await this.api('GET', '/api/nav/state');

            if (nameEl) nameEl.textContent = state.author_name || state.handle || 'Unknown';
            if (handleEl) handleEl.textContent = state.handle || '';
            if (statsEl) {
                const c = state.counts || {};
                statsEl.innerHTML = `<span><strong>${c.followers||0}</strong> followers</span><span><strong>${c.following||0}</strong> following</span><span><strong>${c.posts||0}</strong> posts</span>`;
            }

            // Update avatar if nav state includes avatar config
            if (state.avatar) {
                this.avatarConfig = state.avatar;
                this._renderAvatar();
            }
        } catch (err) {
            // Fallback to existing local data
            if (nameEl) nameEl.textContent = this.authorName || 'Unknown';
            if (handleEl) {
                try { handleEl.textContent = new URL(this.siteBaseUrl).hostname; } catch(e) { handleEl.textContent = ''; }
            }
            if (statsEl) {
                const f = this.counts.followers || 0;
                const fw = this.counts.following || 0;
                const p = this.counts.posts || 0;
                statsEl.innerHTML = `<span><strong>${f}</strong> followers</span><span><strong>${fw}</strong> following</span><span><strong>${p}</strong> posts</span>`;
            }
        }
    },

    // Update which nav icon is highlighted based on current view.
    // step-06/6.a: nav IDs renamed to v4 grammar (gateway/paragraph/comment/people/envelope/edit).
    _updateNavActive(view) {
        // Only the `conversations` view (bare `/_/` landing) maps to an
        // icon through this helper — the v4 stream-screen wraps the
        // rest of the nav state via owner-extras' icon-preset waterfall
        // (syncActiveIconFromFilter), which highlights icons based on
        // the current PQL filter rather than the route name.
        const map = {
            'conversations': 'gateway',
            'settings': null,
        };
        const active = map[view] || null;
        ['gateway', 'paragraph', 'comment', 'people', 'envelope', 'edit'].forEach(id => {
            const btn = document.getElementById(`nav-btn-${id}`);
            if (btn) btn.classList.toggle('active', id === active);
        });
    },

    // Update nav badge dots from counts.
    //
    // All three dots follow one rule: show iff unified sync has written
    // something to the corresponding surface's cache/state past the
    // user's last-viewed cursor. Cleared by App.markSurfaceViewed when
    // the user activates the filter view (icon click, PQL URL, dropdown).
    //
    // Earlier shape drove comment off incomingPending and envelope off
    // dmUnread (per-item counts). Those still populate counts.* for the
    // sidebar lifecycle detector — they just no longer gate the dots.
    _updateNavBadges() {
        const surfaces = [
            ['gateway',  this.counts.hasNewFeed],
            ['comment',  this.counts.hasNewBlessingInbox],
            ['envelope', this.counts.hasNewDM],
        ];
        for (const [name, on] of surfaces) {
            const dot = document.getElementById(`nav-dot-${name}`);
            if (dot) dot.classList.toggle('hidden', !on);
            const dotMobile = document.getElementById(`nav-dot-${name}-mobile`);
            if (dotMobile) dotMobile.classList.toggle('hidden', !on);
        }
    },

    // Advance the viewed cursor for a surface and optimistically clear
    // its dot locally. Called from the filter-activation hook in
    // owner-extras.js whenever the icon bucket changes to one of the
    // three tracked surfaces; also from the SSE counts handler when a
    // flag fires for the currently-active surface (so the user never
    // sees a "new" dot for a view they're already on).
    //
    // Optimistic local clear avoids a /api/counts roundtrip on every
    // nav click — the next SSE counts push reconciles authoritatively.
    async markSurfaceViewed(surface) {
        const map = {
            gateway:  { url: '/api/feed/viewed',             flag: 'hasNewFeed' },
            comment:  { url: '/api/comment/blessing/viewed', flag: 'hasNewBlessingInbox' },
            envelope: { url: '/api/dm/viewed',               flag: 'hasNewDM' },
        };
        const entry = map[surface];
        if (!entry) return;
        if (this.counts[entry.flag]) {
            this.counts[entry.flag] = false;
            this._updateNavBadges();
        }
        try {
            await fetch(entry.url, { method: 'POST', credentials: 'same-origin' });
        } catch (_) { /* next counts roundtrip reconciles */ }
    },

    // Toggle + button dropdown
    _toggleNewDropdown(event) {
        event.stopPropagation();
        const dd = document.getElementById('new-dropdown');
        if (!dd) return;
        const isOpen = dd.classList.contains('open');
        dd.classList.toggle('open', !isOpen);
        if (!isOpen) {
            // Close on outside click
            const close = () => { dd.classList.remove('open'); document.removeEventListener('click', close); };
            setTimeout(() => document.addEventListener('click', close), 0);
        }
    },

    _updateTopbarMode(mode) {
        // Map mode names to nav icon IDs. 'editor' → 'edit' so the
        // write-anchor + button (id="nav-btn-edit") highlights while
        // the editor screen is showing — matches the toggle semantics
        // wired through App.toggleCompose().
        const map = { 'social': 'posts', 'messages': 'messages', 'my-site': 'posts', 'editor': 'edit' };
        const active = map[mode] || 'posts';
        ['feed', 'edit', 'posts', 'comments', 'people', 'messages'].forEach(id => {
            const btn = document.getElementById(`nav-btn-${id}`);
            if (btn) btn.classList.toggle('active', id === active);
        });
    },

    _updateTopbarBadges() {
        this._updateNavBadges();
    },

    _updateTopbarDomain(baseUrl) {
        // No longer needed — avatar menu shows domain instead
    },

    // Toast notification system
    showToast(message, type = 'info', duration = 4000) {
        const container = document.getElementById('toast-container');
        const toast = document.createElement('div');
        toast.className = `toast ${type}`;

        const icons = {
            success: '&#10003;',
            error: '&#10007;',
            warning: '!',
            info: 'i',
        };

        toast.innerHTML = `
            <div class="toast-icon">${icons[type] || icons.info}</div>
            <div class="toast-message">${this.escapeHtml(message)}</div>
            <button class="toast-close" onclick="this.parentElement.remove()">&times;</button>
        `;

        container.appendChild(toast);

        // Auto-dismiss
        if (duration > 0) {
            setTimeout(() => {
                toast.classList.add('toast-out');
                setTimeout(() => toast.remove(), 200);
            }, duration);
        }

        return toast;
    },

    // Suggestion toast — HTML content with action buttons, longer timeout
    showSuggestion(html, duration = 8000) {
        const container = document.getElementById('toast-container');
        const toast = document.createElement('div');
        toast.className = 'toast suggestion';
        toast.innerHTML = `
            <div class="toast-icon">&#9889;</div>
            <div class="toast-message suggestion-content">${html}</div>
            <button class="toast-close" onclick="this.parentElement.remove()">&times;</button>
        `;
        container.appendChild(toast);
        if (duration > 0) {
            setTimeout(() => {
                toast.classList.add('toast-out');
                setTimeout(() => toast.remove(), 200);
            }, duration);
        }
        return toast;
    },

    // Pulsing "Broadcasting to your followers...." below a post item
    showBroadcastPulse(targetItem) {
        const existing = document.getElementById('broadcast-pulse');
        if (existing) existing.remove();
        if (!targetItem) return;

        const el = document.createElement('div');
        el.id = 'broadcast-pulse';
        el.className = 'broadcast-pulse';
        el.textContent = 'Broadcasting to your followers\u2026';

        targetItem.style.position = 'relative';
        targetItem.appendChild(el);

        // Pulse 5 times then remove
        let count = 0;
        const maxPulses = 5;
        el.style.opacity = '0';

        const pulse = () => {
            if (count >= maxPulses) {
                el.style.transition = 'opacity 1.2s ease-out';
                el.style.opacity = '0';
                setTimeout(() => el.remove(), 1300);
                return;
            }
            el.style.transition = 'opacity 1.2s ease-in';
            el.style.opacity = '1';
            setTimeout(() => {
                el.style.transition = 'opacity 1.2s ease-out';
                el.style.opacity = '0';
                count++;
                setTimeout(pulse, 400);
            }, 1600);
        };
        setTimeout(pulse, 500);
    },

    // Confirm modal (replaces browser confirm())
    showConfirmModal(title, message, confirmText = 'Confirm', cancelText = 'Cancel', type = 'default') {
        return new Promise((resolve) => {
            const modal = document.createElement('div');
            modal.className = 'modal-overlay';

            const typeClass = type === 'danger' ? 'danger' : 'primary';

            modal.innerHTML = `
                <div class="modal confirm-modal">
                    <div class="modal-header">
                        <h3>${this.escapeHtml(title)}</h3>
                        <button class="modal-close" data-action="cancel">&times;</button>
                    </div>
                    <div class="modal-body">
                        <p>${this.escapeHtml(message)}</p>
                    </div>
                    <div class="modal-footer">
                        <button class="secondary" data-action="cancel">${this.escapeHtml(cancelText)}</button>
                        <button class="${typeClass}" data-action="confirm">${this.escapeHtml(confirmText)}</button>
                    </div>
                </div>
            `;

            const cleanup = (result) => {
                modal.remove();
                resolve(result);
            };

            modal.querySelectorAll('[data-action="cancel"]').forEach(btn => {
                btn.addEventListener('click', () => cleanup(false));
            });
            modal.querySelector('[data-action="confirm"]').addEventListener('click', () => cleanup(true));
            modal.addEventListener('click', (e) => {
                if (e.target === modal) cleanup(false);
            });

            document.body.appendChild(modal);
            modal.querySelector('[data-action="confirm"]').focus();
        });
    },

    // API calls
    async api(method, path, body = null) {
        const options = {
            method,
            headers: { 'Content-Type': 'application/json' },
        };
        if (body) {
            options.body = JSON.stringify(body);
        }
        const response = await fetch(path, options);
        if (!response.ok) {
            // In hosted mode, redirect to login on 401
            if (this.isHosted && response.status === 401) {
                window.location.href = '//' + window.__POLIS_BASE_DOMAIN;
                return;
            }
            const text = await response.text();
            throw new Error(text || response.statusText);
        }
        return response.json();
    },

    // Intent state (set by URL params, consumed after dashboard loads)
    _pendingIntent: null,

    // Navigation generation counter — prevents stale async loads from overwriting newer navigation
    _navGeneration: 0,

    // ── Deep-link routing ────────────────────────────────────────────────

    // Base path for all SPA routes (/_/ on both localhost and hosted)
    basePath: '/_',

    // Route table: [pattern, config] pairs. Checked in order.
    // Parameterized segments: :id (single segment), :path+ (catch-all, one or more segments).
    // Routes left after chunks A + B: bare `/_/` (default filter
    // landing) and `/_/settings` (the only non-stream surface).
    // Every other filter view — including profiles — is reached via
    // the PQL URL form (`/_/pql/<sentence>`) the chunk-B parser
    // handles. The people-icon click composes to
    // `/_/pql/all+profiles+from+my+network+by+name` automatically
    // via the onFilterChange → composeURL binding.
    ROUTES: [
        ['/',                            { mode: 'social',  view: 'conversations',   screen: 'dashboard' }],
        ['/settings',                    { view: 'settings', screen: 'dashboard' }],
    ],

    // Reverse lookup: view name → canonical path (for pushState).
    VIEW_PATHS: {
        'settings':            '/settings',
    },

    // SOCIAL_PLUGINS retired in chunk A. The pulse plugin is gone (its
    // dashboard widget didn't fit the tier model); the conversations
    // plugin's `/feed` route is gone (replaced by bare `/_/` landing on
    // the default filter). The plugin-dispatch path in loadViewContent
    // is also retired below.
    SOCIAL_PLUGINS: [],

    // Resolve a pathname against the route table.
    // Returns { config, params } or null if no match.
    resolveRoute(pathname) {
        // Strip base path prefix
        let path = pathname;
        if (path.startsWith(this.basePath)) {
            path = path.slice(this.basePath.length);
        }
        // Normalize: ensure leading slash, strip trailing slash (except bare /)
        if (!path.startsWith('/')) path = '/' + path;
        if (path.length > 1 && path.endsWith('/')) path = path.slice(0, -1);
        // Strip .md extension from post paths
        if (path.endsWith('.md')) path = path.slice(0, -3);

        for (const [pattern, config] of this.ROUTES) {
            const params = this._matchPattern(pattern, path);
            if (params !== null) {
                return { config, params };
            }
        }
        return null;
    },

    // Match a route pattern against a path. Returns params object or null.
    _matchPattern(pattern, path) {
        const patternParts = pattern.split('/').filter(Boolean);
        const pathParts = path.split('/').filter(Boolean);

        // Check for catch-all parameter (:name+)
        const lastPattern = patternParts[patternParts.length - 1];
        const hasCatchAll = lastPattern && lastPattern.startsWith(':') && lastPattern.endsWith('+');

        if (hasCatchAll) {
            // Need at least as many path parts as pattern parts
            if (pathParts.length < patternParts.length) return null;
        } else {
            // Exact segment count required
            if (pathParts.length !== patternParts.length) return null;
        }

        const params = {};
        for (let i = 0; i < patternParts.length; i++) {
            const pp = patternParts[i];
            if (pp.startsWith(':') && pp.endsWith('+')) {
                // Catch-all: consume remaining path segments
                const name = pp.slice(1, -1);
                params[name] = pathParts.slice(i).join('/');
                return params;
            } else if (pp.startsWith(':')) {
                // Named parameter: single segment
                params[pp.slice(1)] = pathParts[i];
            } else {
                // Literal match
                if (pp !== pathParts[i]) return null;
            }
        }
        return params;
    },

    // Navigate to a deep-link path. Updates URL bar and renders the route.
    // opts.replace: use replaceState instead of pushState
    // opts.skipRender: only update URL, don't render (used during init)
    async navigateTo(path, opts = {}) {
        // Auto-save-on-navigate retired in chunk A: the v3 editor-screen
        // (and its _buildFullMarkdown / saveDraft helpers) is gone. The
        // v4 inline editor card has its own autosave wired into its
        // close path (see owner-extras.js mountEditorCard).

        // ─── HANDBOOK TRAIL MARKER: URL-as-filter thread ─────────────────────
        // The URL string IS the active filter, not a tracker of it. /_/pql/<sentence>
        // gets parsed by pql.js, pushed back through history.pushState in its
        // canonical form, then handed to the v4 stream controller (stream.js) which
        // re-renders the column. Concept doc: docs/general/pql.md.
        // ─────────────────────────────────────────────────────────────────────
        if (path && (path.indexOf('/pql/') === 0 || path.indexOf('pql/') === 0)) {
            return this._navigateToPQL(path, opts);
        }
        const route = this.resolveRoute(path);
        if (!route) {
            // Unknown-`/_/*`-path fallback (chunk C): render the default
            // filter view + replaceState the URL to the canonical bare
            // basePath. No toast — bookmarks and in-flight links from
            // retired v3 paths land here silently and the user sees a
            // working surface with a clean URL bar. Hard-cutover per
            // plan.
            window.history.replaceState({}, '', this.basePath + '/');
            if (!opts.skipRender) {
                this.sidebarMode = 'social';
                this._updateSidebarUI('social');
                this.currentView = 'conversations';
                this._activateStreamScreen();
                this.showScreen('stream');
            }
            return;
        }

        const { config, params } = route;
        const gen = ++this._navGeneration;
        const fullPath = this.basePath + (path.startsWith('/') ? path : '/' + path);

        // Update URL
        if (opts.replace) {
            window.history.replaceState({}, '', fullPath);
        } else {
            window.history.pushState({}, '', fullPath);
        }

        if (opts.skipRender) return;

        // Dashboard views
        if (config.screen === 'dashboard') {
            if (config.mode) {
                this.sidebarMode = config.mode;
                this._updateSidebarUI(config.mode);
            }
            this.currentView = config.view;
            if (config.tabHint) this._commentsPublishedFilter = config.tabHint;
            this._updateSidebarActiveItem(config.view);
            // step-06/6.c: /feed view (conversations) routes to the v4
            // stream layout (stream-screen) instead of the legacy
            // The `conversations` view is the v4 stream-screen — the
            // canvas that hosts every filter view. The chunk-B PQL
            // parser handles `/_/pql/<sentence>` directly (see
            // _navigateToPQL); this branch handles the bare `/_/`
            // landing.
            if (config.view === 'conversations') {
                this._activateStreamScreen();
                if (gen !== this._navGeneration) return;
                this.showScreen('stream');
                return;
            }
            await this.loadViewContent();
            if (gen !== this._navGeneration) return; // stale
            this.showScreen('dashboard');
            return;
        }

        // Editor / action / screen dispatch retired in chunk A. The
        // /_/posts/new, /_/posts/drafts/:id, /_/posts/:path+,
        // /_/comments/new, /_/comments/drafts/:id routes are gone;
        // the inline editor card on the v4 stream-screen replaces
        // every one of them.
    },

    // Build full URL path for a view name
    pathForView(view) {
        const rel = this.VIEW_PATHS[view];
        return rel ? this.basePath + rel : this.basePath + '/';
    },

    // pathForScreen retired in chunk A — every screen the v3 dispatch
    // used to navigate to (newPost / openDraft / openPost / newComment /
    // openCommentDraft) has been replaced by the v4 stream's inline
    // editor card, which doesn't change the URL.

    // PQL URL dispatch (chunk B). Called from navigateTo when the path
    // is shaped `/_/pql/<sentence>` (or `/pql/<sentence>` relative).
    // Parses the sentence, replaces the URL with the canonical form,
    // activates the stream-screen, and applies the filter once
    // owner-extras + the stream controller are ready. Malformed
    // sentences fall through to the unknown-path fallback (default
    // filter at bare `/_/`).
    async _navigateToPQL(path, opts) {
        opts = opts || {};
        if (!window.PQL || typeof window.PQL.parse !== 'function') {
            // Parser hasn't loaded — punt to the default filter so the
            // user lands somewhere working instead of a blank screen.
            window.history.replaceState({}, '', this.basePath + '/');
            this.sidebarMode = 'social';
            this._updateSidebarUI('social');
            this.currentView = 'conversations';
            this._activateStreamScreen();
            this.showScreen('stream');
            return;
        }
        // Strip a leading `/` and the `pql/` prefix to leave just the
        // encoded sentence. Both `/pql/<...>` and `pql/<...>` flow in.
        var encoded = path.replace(/^\/?pql\//, '');
        var sentence;
        try {
            sentence = decodeURIComponent(encoded).replace(/\+/g, ' ');
        } catch (e) {
            sentence = '';
        }
        var state = window.PQL.parse(sentence);
        if (!state) {
            // Malformed sentence — same fallback as an unknown path.
            window.history.replaceState({}, '', this.basePath + '/');
            this.sidebarMode = 'social';
            this._updateSidebarUI('social');
            this.currentView = 'conversations';
            this._activateStreamScreen();
            this.showScreen('stream');
            return;
        }
        // Push the canonical URL form (or replace if requested).
        var canonical = window.PQL.composeURL(state, this.basePath);
        if (opts.replace) {
            window.history.replaceState({}, '', canonical);
        } else {
            window.history.pushState({}, '', canonical);
        }
        if (opts.skipRender) return;
        // Activate the stream surface, then apply the filter once
        // owner-extras + the controller are ready. PolisOwnerExtras.ready
        // resolves to the controller; setFilter applies the parsed PQL.
        this.sidebarMode = 'social';
        this._updateSidebarUI('social');
        this.currentView = 'conversations';
        this._activateStreamScreen();
        this.showScreen('stream');
        var presetReady = window.PolisOwnerExtras && window.PolisOwnerExtras.ready;
        var apply = () => {
            if (window.PolisStream && typeof window.PolisStream.setFilter === 'function') {
                window.PolisStream.setFilter(state);
            }
        };
        if (presetReady && typeof presetReady.then === 'function') {
            presetReady.then(apply).catch(apply);
        } else {
            apply();
        }
    },

    // Update sidebar section visibility without triggering a view change
    _updateSidebarUI(mode) {
        const mySite = document.getElementById('sidebar-my-site');
        const social = document.getElementById('sidebar-social');
        if (mode === 'social') {
            mySite && mySite.classList.add('hidden');
            social && social.classList.remove('hidden');
        } else {
            social && social.classList.add('hidden');
            mySite && mySite.classList.remove('hidden');
        }
        document.querySelectorAll('.sidebar-mode-toggle .mode-tab').forEach(tab => {
            tab.classList.toggle('active', tab.dataset.sidebarMode === mode);
        });
        // Also update topbar mode toggle
        this._updateTopbarMode(mode);
    },

    // Update sidebar active item highlight without triggering content load
    _updateSidebarActiveItem(view) {
        const settingsBtn = document.getElementById('settings-btn');
        if (settingsBtn) settingsBtn.classList.toggle('active', view === 'settings');
        document.querySelectorAll('.sidebar .nav-item').forEach(item => {
            item.classList.remove('active');
            if (item.dataset.view === view) {
                item.classList.add('active');
            }
        });
        // Also update icon nav highlights
        this._updateNavActive(view);
    },

    // Register social plugins: inject routes, view paths, and sidebar buttons.
    // Must run before bindEvents() so dynamically created buttons get click handlers.
    // _registerPlugins retired in chunk A — SOCIAL_PLUGINS is empty so
    // there's nothing to register. The hook is preserved as a no-op for
    // any callsite that still references it (init() in particular)
    // until the next pass of dead-code removal.
    _registerPlugins() { /* no-op (SOCIAL_PLUGINS retired) */ },

    // Initialize app
    async init() {
        // step-06/6.a: mark this surface as the owner SPA. owner-extras.js
        // (added in 6.d) and CSS body.is-owner gates branch on this class.
        // Public per-post artifacts never set this, so layered owner-only
        // chrome stays cleanly scoped.
        document.body.classList.add('is-owner');

        // Apply theme before first render to avoid flash
        this._initTheme(null);

        // Register social plugins before anything else (must precede bindEvents)
        this._registerPlugins();

        // Parse intent params from URL before anything else
        this._pendingIntent = this.parseIntentParams();

        // Handle widget_connect immediately (redirects away, no dashboard needed)
        if (this._pendingIntent && this._pendingIntent.type === 'widget_connect') {
            this.handleWidgetConnect(this._pendingIntent.returnUrl);
            return;
        }

        try {
            const status = await this.api('GET', '/api/status');
            const validation = status.validation || {};

            switch (validation.status) {
                case 'valid':
                    { const dd = document.getElementById('domain-display'); if (dd) dd.textContent = status.site_title || ''; }
                    // Set data before rendering nav
                    this.siteBaseUrl = status.base_url || '';
                    this.avatarConfig = status.avatar || null;
                    this.authorName = status.author_name || '';
                    // Set site theme for nav variable contract (Phase 0)
                    if (status.active_theme) {
                        this.siteTheme = status.active_theme;
                        document.documentElement.dataset.siteTheme = status.active_theme;
                        try { localStorage.setItem('polis-site-theme', status.active_theme); } catch (e) {}
                    }
                    // Now render nav with data available
                    this.updateDomainDisplay(status.base_url);
                    this._renderAvatar();
                    this._renderAvatarMenu();
                    await this.loadAllCounts();
                    this._updateTopbarBadges();
                    this.initNotifications();
                    this.initSSE();
                    // Re-observe unread feed items when tab becomes visible again
                    document.addEventListener('visibilitychange', () => {
                        if (!document.hidden) {
                            const cl = document.getElementById('content-list');
                            if (cl && this.currentView === 'conversations') {
                                this._observeFeedItems(cl);
                            }
                        }
                    });
                    // Save feed filter state on navigation away (for restore on return)
                    window.addEventListener('beforeunload', () => {
                        if (this.currentView === 'conversations') this._saveFeedState();
                    });
                    this.checkSetupBanner();

                    // Show follow link footer in sidebar
                    const followFooter = document.getElementById('sidebar-follow-link');
                    if (followFooter && this.siteBaseUrl) {
                        followFooter.classList.remove('hidden');
                    }

                    // Auto-issue widget token in hosted mode (fire and forget)
                    if (this.isHosted) {
                        this.ensureWidgetToken();
                        // Set cross-tenant cookie so the widget on other tenants' post pages
                        // can detect this user's instance (localStorage is per-origin)
                        try {
                            if (this.siteBaseUrl) {
                                document.cookie = 'polis_instance=' + encodeURIComponent(this.siteBaseUrl) +
                                    '; domain=.polis.pub; path=/; max-age=31536000; SameSite=Lax; Secure';
                            }
                        } catch (e) {}
                    }

                    // Resolve deep-linked state from current URL path
                    await this._restoreRouteFromURL();

                    // Process pending intent after dashboard is ready
                    if (this._pendingIntent) {
                        await this.processIntent(this._pendingIntent);
                        this._pendingIntent = null;
                    }
                    break;

                case 'not_found':
                    if (this.isHosted) {
                        // In hosted mode, site should always exist
                        this.showToast('Site not ready yet. Refresh in a moment.', 'info');
                    }
                    this.showScreen('welcome');
                    break;

                case 'incomplete':
                case 'invalid':
                    this.renderValidationErrors(validation.errors || []);
                    this.showScreen('error');
                    break;

                default:
                    // Legacy fallback for backwards compatibility
                    if (status.configured) {
                        { const dd = document.getElementById('domain-display'); if (dd) dd.textContent = status.site_title || ''; }
                        this.updateDomainDisplay(status.base_url);
                        this.siteBaseUrl = status.base_url || '';
                        this.initNotifications();
                        await this.loadAllCounts();
                        this.initSSE();

                        // Show follow link footer in sidebar
                        const followFooter2 = document.getElementById('sidebar-follow-link');
                        if (followFooter2 && this.siteBaseUrl) {
                            followFooter2.classList.remove('hidden');
                        }

                        // Resolve deep-linked state from current URL path
                        await this._restoreRouteFromURL();
                        this.checkSetupBanner();
                    } else {
                        this.showScreen('welcome');
                    }
            }
        } catch (err) {
            console.error('Failed to check status:', err);
            this.showScreen('welcome');
        }

        this.bindEvents();
    },

    // Restore view/screen from the current URL path on page load.
    async _restoreRouteFromURL() {
        const pathname = window.location.pathname;

        // PQL URL intercept (chunk B). Init + popstate both flow
        // through this path; if the URL is /_/pql/<sentence>, parse
        // and apply the filter. Note: we use replace mode so popstate
        // doesn't push a duplicate history entry on top of the one
        // the browser already navigated to.
        const stripped = pathname.replace(/^\/_/, '');
        if (stripped.indexOf('/pql/') === 0) {
            return this._navigateToPQL(stripped, { replace: true });
        }

        const route = this.resolveRoute(pathname);

        if (!route) {
            // Unknown deep-link path (chunk C hard-cutover fallback):
            // render the default filter view + replaceState to the
            // canonical bare basePath. Lands on a working surface;
            // user sees a clean URL bar.
            window.history.replaceState({}, '', this.basePath + '/');
            this.sidebarMode = 'social';
            this._updateSidebarUI('social');
            this.currentView = 'conversations';
            this._activateStreamScreen();
            this.showScreen('stream');
            return;
        }

        const { config, params } = route;

        // URL normalization simplified in chunk A: bare /_/ is the
        // canonical default landing (no longer redirected to /_/feed).
        // /_/settings keeps its path. Every filter view goes through
        // /_/pql/<sentence> via the chunk-B intercept earlier in this
        // function. The legacy pathForView-based rewrite is no longer
        // needed since the route table only has paths the user already
        // wrote.

        if (config.screen === 'dashboard') {
            if (config.mode) {
                this.sidebarMode = config.mode;
                this._updateSidebarUI(config.mode);
            }
            this.currentView = config.view;
            if (config.tabHint) this._commentsPublishedFilter = config.tabHint;
            this._updateSidebarActiveItem(config.view);
            // step-06/6.e bugfix: parallel the conversations branch in
            // navigateTo (app.js:808-813). Without this, page-refresh on
            // /_/feed silently falls through to v3 conversations rendered
            // into #content-list inside dashboard-screen — the filter
            // widget shows briefly then disappears as showScreen('dashboard')
            // hides it. _restoreRouteFromURL is the init-time twin of
            // navigateTo; both code paths must handle the v4 stream
            // route the same way.
            if (config.view === 'conversations') {
                this._activateStreamScreen();
                this.showScreen('stream');
                return;
            }
            await this.loadViewContent();
            this.showScreen('dashboard');
            return;
        }

        // Editor / action dispatch retired in chunk A. See navigateTo
        // for the rationale — the v3 action routes no longer exist.
    },

    // Render validation errors on the error screen
    renderValidationErrors(errors) {
        const container = document.getElementById('validation-errors');
        if (!container) return;

        if (errors.length === 0) {
            container.innerHTML = '<div class="error-item"><p>Unknown validation error</p></div>';
            return;
        }

        container.innerHTML = errors.map(err => `
            <div class="error-item">
                <div class="error-item-header">
                    <span class="error-code">${this.escapeHtml(err.code)}</span>
                </div>
                <p class="error-message">${this.escapeHtml(err.message)}</p>
                ${err.path ? `<p class="error-path">Path: <code>${this.escapeHtml(err.path)}</code></p>` : ''}
                ${err.suggestion ? `<p class="error-suggestion">${this.escapeHtml(err.suggestion)}</p>` : ''}
            </div>
        `).join('');
    },

    // Retry validation (reload the page essentially)
    async retryValidation() {
        await this.init();
    },

    // Discard a draft. Called by the v4 stream's Discard rollover CTA
    // (owner-extras.js decorateDraft). The v3 editor screen used to own
    // its own delete-draft-btn handler; when that screen was retired
    // (chunk A) the App.deleteDraft method went with it, leaving the
    // CTA silently broken — click landed a console warning, no network
    // request, draft stayed put. Restored here as a thin wrapper over
    // DELETE /api/drafts/{id}.
    async deleteDraft(draftId) {
        if (!draftId) return;
        await this.api('DELETE', '/api/drafts/' + encodeURIComponent(draftId));
        await this.loadAllCounts();
    },

    // Unpublish a post — moves it back to drafts and emits an
    // unpublish event to the DS. Same chunk-A regression as
    // deleteDraft: owner-extras.js's Unpublish rollover CTA referenced
    // App.unpublishPost which had been removed alongside the v3 editor.
    async unpublishPost(sourcePath) {
        if (!sourcePath) return;
        await this.api('POST', '/api/unpublish', { path: sourcePath });
        await this.loadAllCounts();
    },

    // Load all counts via single consolidated endpoint
    async loadAllCounts() {
        try {
            const c = await this.api('GET', '/api/counts');
            this.counts.posts = c.posts || 0;
            this.counts.drafts = c.drafts || 0;
            this.counts.myPending = c.my_pending || 0;
            this.counts.myBlessed = c.my_blessed || 0;
            this.counts.myDenied = c.my_denied || 0;
            this.counts.myCommentDrafts = c.my_comment_drafts || 0;
            this.counts.incomingPending = c.incoming_pending || 0;
            this.counts.incomingBlessed = c.incoming_blessed || 0;
            this.counts.feedUnread = c.feed_unread || 0;
            this.counts.hasNewFeed = c.has_new_feed || false;
            this.counts.hasNewBlessingInbox = c.has_new_blessing_inbox || false;
            this.counts.hasNewDM = c.has_new_dm || false;
            this.counts.following = c.following || 0;
            this.counts.followers = c.followers || 0;
            this.counts.dmUnread = c.dm_unread || 0;

            this.updateSidebar();
            this._updateTopbarBadges();
        } catch (err) {
            console.error('Failed to load counts:', err);
        }
    },

    // Apply SSE-pushed counts to local state and update UI
    _applyCountsFromSSE(c) {
        this.counts.posts = c.posts || 0;
        this.counts.drafts = c.drafts || 0;
        this.counts.myPending = c.my_pending || 0;
        this.counts.myBlessed = c.my_blessed || 0;
        this.counts.myDenied = c.my_denied || 0;
        this.counts.myCommentDrafts = c.my_comment_drafts || 0;
        this.counts.incomingPending = c.incoming_pending || 0;
        this.counts.incomingBlessed = c.incoming_blessed || 0;
        this.counts.feedUnread = c.feed_unread || 0;
        this.counts.hasNewFeed = c.has_new_feed || false;
        this.counts.hasNewBlessingInbox = c.has_new_blessing_inbox || false;
        this.counts.hasNewDM = c.has_new_dm || false;
        this.counts.following = c.following || 0;
        this.counts.followers = c.followers || 0;
        this.counts.dmUnread = c.dm_unread || 0;

        // Auto-clear: if a flag just fired for the surface the user is
        // already viewing, immediately advance the cursor. Without this
        // the dot pops on while they're staring at the view that should
        // have already "seen" the new items.
        if (window.PolisOwnerExtras && typeof window.PolisOwnerExtras.getCurrentSurfaceIcon === 'function') {
            const cur = window.PolisOwnerExtras.getCurrentSurfaceIcon();
            if (cur === 'gateway' && this.counts.hasNewFeed) this.markSurfaceViewed('gateway');
            else if (cur === 'comment' && this.counts.hasNewBlessingInbox) this.markSurfaceViewed('comment');
            else if (cur === 'envelope' && this.counts.hasNewDM) this.markSurfaceViewed('envelope');
        }

        this.updateSidebar();
        this._updateTopbarBadges();

        // Refresh non-feed views affected by sync
        const autoRefreshViews = ['blessing-requests', 'followers', 'comments-published'];
        if (autoRefreshViews.includes(this.currentView)) {
            const contentList = document.getElementById('content-list');
            if (contentList) this.loadViewContent();
        }
    },

    // Update notification dot visibility
    _updateNotificationDot() {
        const dot = document.getElementById('notification-dot');
        if (dot) {
            dot.classList.toggle('hidden', this.notificationState.unreadCount === 0);
        }
    },

    // Detect lifecycle stage from counts
    detectLifecycleStage() {
        const hasPosts = this.counts.posts > 0;
        const hasOutgoingComments = (this.counts.myPending || 0) + (this.counts.myBlessed || 0) + (this.counts.myDenied || 0) > 0;
        const hasIncomingBlessed = this.counts.incomingBlessed > 0;
        const hasIncomingPending = this.counts.incomingPending > 0;
        const isActive = hasIncomingBlessed || this.counts.posts >= 3;

        if (isActive) {
            this.lifecycleStage = 'active';
        } else if (hasPosts || hasOutgoingComments || hasIncomingPending) {
            this.lifecycleStage = 'first_post';
        } else {
            this.lifecycleStage = 'just_arrived';
        }
    },

    // Update lifecycle stage and welcome panel
    updateSidebar() {
        this.detectLifecycleStage();
        this.updateWelcomePanel();
    },

    // Update the welcome panel based on lifecycle stage and intent
    updateWelcomePanel() {
        const panel = document.getElementById('welcome-panel');
        if (!panel) return;

        // Hide welcome panel on Social tab — it's irrelevant there
        if (this.sidebarMode === 'social') {
            panel.classList.add('hidden');
            return;
        }

        const stage = this.lifecycleStage;
        const intent = this._pendingIntent;
        let html = '';

        if (intent && intent.type === 'comment' && intent.submitted) {
            // Post-comment intent: comment was delivered
            html = `
                <div class="welcome-content">
                    <h3>Comment submitted</h3>
                    <p>Your comment has been delivered to the author.</p>
                    <div class="welcome-actions">
                        <button class="primary" onclick="App.toggleCompose()">Write your first post</button>
                        ${intent.target ? `<a href="${this.escapeHtml(intent.target)}" class="secondary" target="_blank">Back to the post</a>` : ''}
                    </div>
                </div>
            `;
        }
        // first_post and active stages: no welcome panel

        if (html) {
            panel.innerHTML = html;
            panel.classList.remove('hidden');
        } else {
            panel.classList.add('hidden');
        }
    },

    // Copy site URL to clipboard
    async copyShareLink() {
        if (this.siteBaseUrl) {
            try {
                await navigator.clipboard.writeText(this.siteBaseUrl);
                this.showToast('Site link copied', 'success');
            } catch {
                this.showToast('Copy failed', 'error');
            }
        }
    },

    // Build the polis.pub follow link for this site
    getFollowLink() {
        if (!this.siteBaseUrl) return null;
        try {
            const domain = new URL(this.siteBaseUrl).hostname;
            if (domain.endsWith('.polis.pub')) {
                return 'https://polis.pub/f/' + domain.replace('.polis.pub', '');
            }
            return 'https://polis.pub/f/' + domain;
        } catch {
            return null;
        }
    },

    // Copy follow link to clipboard
    async copyFollowLink() {
        const link = this.getFollowLink();
        if (!link) {
            this.showToast('Site not configured yet', 'warning');
            return;
        }
        try {
            await navigator.clipboard.writeText(link);
            this.showToast('Follow link copied!', 'success');
            const btn = document.getElementById('copy-follow-link-btn');
            if (btn) {
                const original = btn.innerHTML;
                btn.classList.add('copied');
                btn.innerHTML = '&#10003; Copied!';
                setTimeout(() => {
                    btn.classList.remove('copied');
                    btn.innerHTML = original;
                }, 2000);
            }
        } catch (err) {
            this.showToast('Failed to copy link', 'error');
        }
    },

    // Load content for current view. After chunk A's v3 hard cutover the
    // only dashboard view left is `settings`; everything else either
    // routes through the v4 stream-screen (the conversations short-
    // circuit in navigateTo, or the chunk-B PQL URL handler) or doesn't
    // exist anymore.
    async loadViewContent() {
        const contentHeader = document.querySelector('.content-header');
        const contentList = document.getElementById('content-list');

        if (this.currentView === 'settings') {
            if (contentHeader) contentHeader.classList.add('hidden');
            this.renderSettings(contentList);
            return;
        }

        // Anything else that reaches this point is unexpected (the route
        // table only includes settings as a dashboard view). Show header
        // empty rather than throw.
        if (contentHeader) contentHeader.classList.remove('hidden');
    },

    // step-06/6.c — activate the v4 stream-screen homepage. Lazy-seeds
    // the initial filter scope (default: my-network) on first activation;
    // subsequent activations don't reset (preserves user's filter state
    // when navigating away and back). The actual data fetch happens
    // inside the v4 stream.js controller via setFilterScope →
    // applyFilter → fetchNextPage.
    //
    // Filter visibility is owned by showScreen — we don't touch it here
    // (review nit cleanup, step-06/6.c follow-up).
    _activateStreamScreen() {
        if (!window.PolisStream) {
            // Controller hasn't loaded yet. Normal path: <script defer>
            // guarantees stream.js loads before DOMContentLoaded, so the
            // retry shouldn't fire in practice. Failure modes that reach
            // here: 404 on /bundle-assets/.../stream.js, network error,
            // syntax error in stream.js parsed too late. Cap the retry
            // budget (review concern, step-06/6.c follow-up) so we don't
            // spin forever silently — surface a toast on exhaustion.
            this._streamControllerRetries = (this._streamControllerRetries || 0) + 1;
            if (this._streamControllerRetries > 50) {
                this.showToast('Stream controller failed to load — try refreshing', 'error');
                return;
            }
            setTimeout(() => this._activateStreamScreen(), 20);
            return;
        }
        this._streamControllerRetries = 0;
        // First activation seeds the default filter. Subsequent
        // activations preserve whatever the user picked.
        if (!this._streamScreenSeeded) {
            this._streamScreenSeeded = true;
            // Default filter: gateway preset — "new activity from my
            // network". Owner-extras owns the preset map + side-effects
            // (telemetry, active-icon state); wait for its ready promise
            // before invoking. Fall back to a plain scope-only seed if
            // owner-extras failed to initialize for any reason.
            var fallback = function () {
                window.PolisStream.setFilterScope('my-network', { label: 'my network' });
            };
            if (window.PolisOwnerExtras && window.PolisOwnerExtras.ready &&
                typeof window.PolisOwnerExtras.ready.then === 'function') {
                window.PolisOwnerExtras.ready.then(function () {
                    if (typeof window.PolisOwnerExtras.loadPreset === 'function') {
                        window.PolisOwnerExtras.loadPreset('gateway');
                    } else {
                        fallback();
                    }
                }).catch(fallback);
            } else {
                fallback();
            }
        }
    },

    // Set active view and update UI
    setActiveView(view, opts = {}) {
        this.currentView = view;

        // Deactivate settings gear if navigating away
        const settingsBtn = document.getElementById('settings-btn');
        if (settingsBtn) settingsBtn.classList.toggle('active', view === 'settings');

        // Update sidebar active state
        document.querySelectorAll('.sidebar .nav-item').forEach(item => {
            item.classList.remove('active');
            if (item.dataset.view === view) {
                item.classList.add('active');
            }
        });

        // Update URL bar
        if (opts.pushState !== false) {
            const path = this.pathForView(view);
            window.history.pushState({}, '', path);
        }

        // Load content for the view
        this.loadViewContent();
    },

    // Toggle settings view from header gear icon
    toggleSettings() {
        if (this.currentView === 'settings') {
            // Go back to the default view for the current sidebar mode
            const defaultView = this.sidebarMode === 'social' ? 'conversations' : 'posts-published';
            this.setActiveView(defaultView);
        } else {
            this.setActiveView('settings');
        }
    },

    // Bind event handlers
    bindEvents() {
        // Init panel events
        const initCloseBtn = document.getElementById('init-close-btn');
        const initCancelBtn = document.getElementById('init-cancel-btn');
        const initExecuteBtn = document.getElementById('init-execute-btn');
        const initOverlay = document.querySelector('#init-panel .wizard-overlay');

        if (initCloseBtn) initCloseBtn.addEventListener('click', () => this.closeInitPanel());
        if (initCancelBtn) initCancelBtn.addEventListener('click', () => this.closeInitPanel());
        if (initExecuteBtn) initExecuteBtn.addEventListener('click', () => this.executeInit());
        if (initOverlay) initOverlay.addEventListener('click', () => this.closeInitPanel());

        // Link panel events
        const linkCloseBtn = document.getElementById('link-close-btn');
        const linkCancelBtn = document.getElementById('link-cancel-btn');
        const linkExecuteBtn = document.getElementById('link-execute-btn');
        const linkOverlay = document.querySelector('#link-panel .wizard-overlay');

        if (linkCloseBtn) linkCloseBtn.addEventListener('click', () => this.closeLinkPanel());
        if (linkCancelBtn) linkCancelBtn.addEventListener('click', () => this.closeLinkPanel());
        if (linkExecuteBtn) linkExecuteBtn.addEventListener('click', () => this.executeLink());
        if (linkOverlay) linkOverlay.addEventListener('click', () => this.closeLinkPanel());

        // Sidebar mode toggle
        document.querySelectorAll('.sidebar-mode-toggle .mode-tab').forEach(tab => {
            tab.addEventListener('click', () => {
                const mode = tab.dataset.sidebarMode;
                if (mode) this.setSidebarMode(mode);
            });
        });

        // Sidebar navigation
        document.querySelectorAll('.sidebar .nav-item').forEach(item => {
            item.addEventListener('click', () => {
                const view = item.dataset.view;
                if (view) {
                    this.setActiveView(view);
                    this.closeMobileNav();
                }
            });
        });

        // Back button removed — editor now uses shared nav

        // Popstate handler — browser back/forward navigation
        window.addEventListener('popstate', async () => {
            await this._restoreRouteFromURL();
        });

        // v3 editor + comment screen wiring retired in chunk A. The
        // publish-btn / delete-draft-btn / markdown-input / editor-*
        // / filename-input / comment-back-btn / save-comment-draft-btn /
        // sign-send-btn listeners + the floating selection toolbar +
        // editor-toolbar wiring + the v3 auto-save trigger are all gone.
        // The v4 inline editor card owns its own publish + autosave +
        // close path (see owner-extras.js).

        // Slash command menu — still used by the v3 inline comment
        // editor (modal-overlay variant kept for now).
        this._initSlashMenu();

        // (about-screen toggles + Milkdown wiring removed — about
        // editor is now inline in the owner-card scratch column.)

        // Milkdown is lazy-loaded on first editor open (see
        // _loadMilkdownBridge / _initMilkdown). Boot-time auto-load
        // and its 10-second textarea-fallback timer were removed —
        // per-editor _showTextareaFallback handles the load-failure
        // case at the moment the user actually needs the editor.
        //
        // Listen for the bridge's ready event so any awaiting code
        // paths that gate on _milkdownReady continue to work.
        if (window.MilkdownBridge) {
            this._milkdownReady = true;
        }
        window.addEventListener('milkdown:ready', () => {
            this._milkdownReady = true;
        });

        // Keyboard shortcuts
        document.addEventListener('keydown', (e) => {
            // Inline comment editor shortcuts (highest priority when open)
            if (this._inlineCommentFocusMode) {
                if (e.key === 'Escape') {
                    e.preventDefault();
                    this._toggleInlineCommentFocusMode();
                    return;
                }
                if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                    e.preventDefault();
                    this._sendFromFocusMode();
                    return;
                }
            }
            if (this._inlineCommentOpen) {
                if (e.key === 'Escape' && !this._slashMenuVisible) {
                    e.preventDefault();
                    this.closeInlineCommentEditor();
                    return;
                }
                if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'F') {
                    e.preventDefault();
                    this._toggleInlineCommentFocusMode();
                    return;
                }
                if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                    e.preventDefault();
                    this.sendInlineComment();
                    return;
                }
            }
            // Ctrl/Cmd + Enter to publish from inline feed editor.
            // (Cmd-S / Cmd-Enter / Cmd-Shift-F for the v3 editor +
            // comment screens were removed in chunk A along with those
            // screens. The v4 inline editor card has its own keyboard
            // wiring in owner-extras.js.)
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && this._feedEditorOpen && this.currentView === 'conversations') {
                e.preventDefault();
                this.publishFromFeed();
            }
            // Escape closes feed filter popovers, feed editor, or exits focus mode
            if (e.key === 'Escape') {
                if (this._feedPopoverOpen) {
                    this._closeFeedPopovers();
                } else if (this._focusMode) {
                    e.preventDefault();
                    this._toggleFocusMode();
                } else if (this._feedEditorOpen) {
                    this.closeFeedEditor();
                }
            }
        });

        // (About editor events removed — inline editor in owner-card.)

    },

    // Default discovery service values (public, hardcoded to match server defaults)
    defaultDiscoveryURL: 'https://ds.polis.pub',

    // Show init flow panel
    showInitFlow() {
        const panel = document.getElementById('init-panel');
        if (panel) {
            // Clear any previous values
            const titleInput = document.getElementById('init-site-title');
            const urlInput = document.getElementById('init-base-url');
            const dsUrlInput = document.getElementById('init-discovery-url');
            if (titleInput) titleInput.value = '';
            if (urlInput) urlInput.value = '';
            if (dsUrlInput) dsUrlInput.value = this.defaultDiscoveryURL;
            panel.classList.remove('hidden');
        }
    },

    // Close init panel
    closeInitPanel() {
        const panel = document.getElementById('init-panel');
        if (panel) panel.classList.add('hidden');
    },

    // Execute site initialization
    async executeInit() {
        const titleInput = document.getElementById('init-site-title');
        const urlInput = document.getElementById('init-base-url');
        const dsUrlInput = document.getElementById('init-discovery-url');
        const executeBtn = document.getElementById('init-execute-btn');

        const siteTitle = titleInput ? titleInput.value.trim() : '';
        const baseUrl = urlInput ? urlInput.value.trim() : '';
        const discoveryUrl = dsUrlInput ? dsUrlInput.value.trim() : '';

        // Disable button while processing
        if (executeBtn) {
            executeBtn.disabled = true;
            executeBtn.textContent = 'Initializing...';
        }

        try {
            const result = await this.api('POST', '/api/init', {
                site_title: siteTitle,
                base_url: baseUrl,
                discovery_url: discoveryUrl,
            });

            this.closeInitPanel();
            this.showToast('Site initialized successfully!', 'success');

            // Update display and show dashboard
            { const dd = document.getElementById('domain-display'); if (dd) dd.textContent = result.site_title || ''; }
            this.updateDomainDisplay(result.base_url);
            this.siteBaseUrl = result.base_url || '';
            this.initNotifications();
            await this.loadAllCounts();
            this.initSSE();
            await this.loadViewContent();
            this.showScreen('dashboard');
            window.history.replaceState({}, '', this.basePath + '/');

            // Show follow link footer in sidebar
            const followFooterInit = document.getElementById('sidebar-follow-link');
            if (followFooterInit && this.siteBaseUrl) {
                followFooterInit.classList.remove('hidden');
            }

            // Open setup wizard to guide through deploy & register
            this.openSetupWizard();
        } catch (err) {
            this.showToast('Failed to initialize site: ' + err.message, 'error');
        } finally {
            if (executeBtn) {
                executeBtn.disabled = false;
                executeBtn.textContent = 'Initialize Site';
            }
        }
    },

    // Show link flow panel
    showLinkFlow() {
        const panel = document.getElementById('link-panel');
        if (panel) {
            // Clear any previous values
            const pathInput = document.getElementById('link-path');
            if (pathInput) pathInput.value = '';
            panel.classList.remove('hidden');
        }
    },

    // Close link panel
    closeLinkPanel() {
        const panel = document.getElementById('link-panel');
        if (panel) panel.classList.add('hidden');
    },

    // Execute site linking
    async executeLink() {
        const pathInput = document.getElementById('link-path');
        const executeBtn = document.getElementById('link-execute-btn');

        const path = pathInput ? pathInput.value.trim() : '';

        if (!path) {
            this.showToast('Please enter a path to your polis site', 'warning');
            return;
        }

        // Disable button while processing
        if (executeBtn) {
            executeBtn.disabled = true;
            executeBtn.textContent = 'Linking...';
        }

        try {
            const result = await this.api('POST', '/api/link', {
                path: path,
            });

            this.closeLinkPanel();
            this.showToast('Site linked successfully!', 'success');

            // Update display and show dashboard
            { const dd = document.getElementById('domain-display'); if (dd) dd.textContent = result.site_title || ''; }
            this.updateDomainDisplay(result.base_url);
            this.initNotifications();
            await this.loadAllCounts();
            this.initSSE();
            await this.loadViewContent();
            this.showScreen('dashboard');
            window.history.replaceState({}, '', this.basePath + '/');
        } catch (err) {
            this.showToast('Failed to link site: ' + err.message, 'error');
        } finally {
            if (executeBtn) {
                executeBtn.disabled = false;
                executeBtn.textContent = 'Link Site';
            }
        }
    },

    // Toggle the post editor open/closed. Called by the ghost first
    // entry's click. Delegates to PolisOwnerExtras (owner-extras.js)
    // which owns the inline editor card mounted inside .stream — that's
    // the same path the nav + (#nav-btn-edit) uses, so both surfaces
    // converge on a single editor instance. Per-view context dispatch
    // (new comment / new follow / new DM) is a later change.
    toggleCompose() {
        if (window.PolisOwnerExtras) {
            const ext = window.PolisOwnerExtras;
            const editorOpen = document.body.classList.contains('has-open-editor');
            const dmComposerOpen = document.body.classList.contains('has-open-dm-composer');
            if (editorOpen) {
                if (typeof ext.closeEditor === 'function') ext.closeEditor();
            } else if (dmComposerOpen) {
                // On DM surfaces openEditor routes to openDMComposer, so
                // the nav + must close it on second press to read as a
                // real toggle (matches the post-editor behavior).
                if (typeof ext.closeDMComposer === 'function') ext.closeDMComposer();
            } else {
                if (typeof ext.openEditor === 'function') ext.openEditor();
            }
            return;
        }
        // owner-extras not loaded — no-op fallback. The legacy full-
        // screen post editor it used to open was retired in chunk A.
    },

    // Render settings page
    async renderSettings(container) {
        try {
            const settings = await this.api('GET', '/api/settings');
            const site = settings.site || {};
            const automations = settings.automations || [];

            // Sync avatar/author_name from settings
            this.avatarConfig = site.avatar || null;
            this.authorName = site.author_name || '';

            // Store existing hooks for advanced panel
            this.existingHooks = settings.existing_hooks || [];

            const themes = (settings.themes || []).filter(t => t.name !== 'sols');

            let automationsHtml = '';
            if (automations.length === 0) {
                automationsHtml = `
                    <div class="empty-state" style="padding: 1.5rem;">
                        <p style="color: var(--text-muted);">No automations configured yet.</p>
                    </div>
                `;
            } else {
                automationsHtml = automations.map(a => `
                    <div class="automation-item">
                        <div class="automation-header">
                            <div class="automation-name">
                                <span class="status-icon">&#10003;</span>
                                ${this.escapeHtml(a.name)}
                            </div>
                            <div class="automation-actions">
                                <button onclick="App.deleteAutomation('${this.escapeHtml(a.id)}')" class="danger">Remove</button>
                            </div>
                        </div>
                        <div class="automation-description">${this.escapeHtml(a.description)}</div>
                    </div>
                `).join('');
            }

            const discoveryStatus = site.discovery_configured
                ? `<span style="color: var(--success-color);">Connected</span>`
                : `<span style="color: var(--warning-color);">Not configured</span>`;
            const discoveryUrl = site.discovery_url || 'Not set';

            container.innerHTML = `
                <div class="settings-container">
                    <div class="settings-section">
                        <div class="settings-section-label">Your Site</div>
                        <div class="settings-card">
                            <div class="settings-row">
                                <span class="settings-row-label">Site:</span>
                                <span class="settings-row-value" id="site-title-display">${this.escapeHtml(site.site_title || 'Not configured')}</span>
                            </div>
                            <div class="settings-row">
                                <span class="settings-row-label">Display Name:</span>
                                <span class="settings-row-value" id="author-name-display">${this.escapeHtml(site.author_name || '') || '<span style="color:var(--text-faint);">Not set</span>'}</span>
                                <div class="settings-row-actions">
                                    <button class="btn-copy" id="author-name-edit-btn" onclick="App.editAuthorName()">Edit</button>
                                </div>
                            </div>
                            <div class="settings-row">
                                <span class="settings-row-label">Avatar:</span>
                                <span class="settings-row-value avatar-preview-row">
                                    <div class="author-avatar" id="avatar-preview" style="${site.avatar ? this._buildAvatarStyle(site.avatar) : `background: ${(() => { const d = this.siteBaseUrl ? (() => { try { return new URL(this.siteBaseUrl).hostname; } catch(e) { return 'me'; } })() : 'me'; return this.domainToAvatar(d).color; })()};`}">${site.avatar ? '' : (() => { const d = this.siteBaseUrl ? (() => { try { return new URL(this.siteBaseUrl).hostname; } catch(e) { return 'me'; } })() : 'me'; return this.domainToAvatar(d).initials; })()}</div>
                                </span>
                                <div class="settings-row-actions">
                                    <button class="btn-copy" onclick="App.randomizeAvatar()">Randomize</button>
                                    <button class="btn-copy" id="avatar-save-btn" onclick="App.saveAvatar()" disabled>Save</button>
                                    ${site.avatar ? `<button class="btn-copy" onclick="App.resetAvatar()">Reset</button>` : ''}
                                </div>
                            </div>
                            <div class="settings-row">
                                <span class="settings-row-label">Public Key:</span>
                                <span class="settings-row-value" id="public-key-display">${this.escapeHtml(this.truncateKey(site.public_key))}</span>
                                <div class="settings-row-actions">
                                    <button class="btn-copy" onclick="App.copyPublicKey('${this.escapeHtml(site.public_key || '')}')">Copy</button>
                                    <button class="btn-danger-sm" onclick="App.openRotatePanel()">Rotate</button>
                                </div>
                            </div>
                            ${this.isHosted ? `
                            <div class="settings-row">
                                <span class="settings-row-label">Discovery:</span>
                                <span class="settings-row-value" id="registration-status">Checking...</span>
                            </div>
                            ` : ''}
                        </div>
                    </div>

                    <!-- Webapp Appearance section removed — color mode is now driven by site theme -->

                    ${themes.length > 0 ? `
                    <div class="settings-section">
                        <div class="settings-section-label">Site Theme</div>
                        <div class="settings-card">
                            <div class="settings-row settings-row-stacked">
                                <select id="theme-select" class="theme-select" onchange="App.onThemeSelectChange()">
                                    ${themes.map(t => {
                                        // Description lookup is always keyed by the internal Name —
                                        // DisplayName is a UI label only, not an identifier.
                                        const desc = this.themeDescriptions[t.name] || '';
                                        // Prefer DisplayName when the bundle provides one (e.g. studio13-nk → "studio13").
                                        // The value written on selection is always the internal Name so the
                                        // registry stays clean.
                                        const visibleName = t.display_name || t.name;
                                        const label = desc ? `${visibleName} — ${desc}` : visibleName;
                                        return `<option value="${this.escapeHtml(t.name)}" ${t.active ? 'selected' : ''} data-original="${t.active ? 'true' : ''}">${this.escapeHtml(label)}</option>`;
                                    }).join('')}
                                </select>
                                <div class="settings-row-actions">
                                    <button class="primary" id="theme-apply-btn" disabled onclick="App.applySelectedTheme()">Change Theme</button>
                                </div>
                            </div>
                            <div id="theme-view-link" class="settings-row" style="display: none; justify-content: center;">
                                <span class="theme-view-link">
                                    Theme updated. <a href="#" onclick="App.viewSite(); return false;">View your site</a>
                                </span>
                            </div>
                        </div>
                    </div>
                    ` : ''}

                    ${!this.isHosted ? `
                    <div class="settings-section">
                        <div class="settings-section-label">Discovery Service</div>
                        <div class="settings-card">
                            <div class="settings-row">
                                <span class="settings-row-label">Status:</span>
                                <span class="settings-row-value">${discoveryStatus}</span>
                            </div>
                            <div class="settings-row">
                                <span class="settings-row-label">URL:</span>
                                <span class="settings-row-value">${this.escapeHtml(discoveryUrl)}</span>
                            </div>
                            <div class="settings-row">
                                <span class="settings-row-label">Registration:</span>
                                <span class="settings-row-value" id="registration-status">Checking...</span>
                            </div>
                            <div id="registration-action" class="settings-action-row"></div>
                            ${!site.discovery_configured ? `
                            <div class="settings-row">
                                <span class="settings-row-label"></span>
                                <span class="settings-row-value" style="color: var(--text-muted); font-size: 0.8rem;">
                                    Set DISCOVERY_SERVICE_URL in your .env file, then restart the webapp.
                                </span>
                            </div>
                            ` : ''}
                        </div>
                    </div>

                    <div class="settings-section">
                        <div class="settings-section-label">Help me...</div>
                        <div class="settings-card">
                            <div class="task-list">
                                <div class="task-item" onclick="App.openWizard('deployment')">
                                    Deploy my content using git
                                    <span class="task-item-arrow">&rarr;</span>
                                </div>
                                <div class="task-item" onclick="App.openWizard('custom')">
                                    Run a custom script when I post or comment
                                    <span class="task-item-arrow">&rarr;</span>
                                </div>
                            </div>
                        </div>
                    </div>
                    ` : ''}

                    ${this.isHosted ? '' : `
                    <div class="settings-section">
                        <div class="settings-section-label">Active Automations</div>
                        <div class="settings-card">
                            ${automationsHtml}
                        </div>
                    </div>
                    `}

                    <div class="settings-section">
                        <div class="settings-section-label">Troubleshooting</div>
                        <div class="settings-card">
                            <div class="settings-row">
                                <span class="settings-row-value" style="white-space: normal; color: var(--text-muted); font-family: inherit; text-align: left;">
                                    Force re-render all posts and comments. Use this if pages look wrong after a theme or snippet change.
                                </span>
                                <div class="settings-row-actions">
                                    <button class="btn-copy" id="rerender-btn" onclick="App.rerenderSite()">Re-render</button>
                                </div>
                            </div>
                        </div>
                    </div>

                    ${this.isHosted ? `
                    <div class="settings-section">
                        <div class="settings-section-label">Account</div>
                        <div class="settings-card">
                            <div class="settings-row">
                                <span class="settings-row-label">Login Email:</span>
                                <span class="settings-row-value" id="account-email-display">Loading...</span>
                                <div class="settings-row-actions">
                                    <button class="btn-copy" onclick="App.showEmailChangeForm()">Change</button>
                                </div>
                            </div>
                            <div id="email-change-form" style="display:none; margin-top: 0.75rem; padding: 0 1rem 0.75rem;">
                                <div style="display: flex; gap: 0.5rem; align-items: center;">
                                    <input type="email" id="new-email-input" placeholder="New email address" style="flex: 1; padding: 0.4rem 0.6rem; background: var(--bg-color); border: 1px solid var(--border-color); color: var(--text-color); border-radius: 4px; font-family: inherit; font-size: 0.9rem;">
                                    <button class="primary" id="email-change-btn" onclick="App.requestEmailChange()">Send confirmation</button>
                                    <button class="btn-copy" onclick="document.getElementById('email-change-form').style.display='none'">Cancel</button>
                                </div>
                                <span id="email-change-status" style="font-size:0.85rem; color:var(--text-muted); margin-top:0.5rem; display:block;"></span>
                            </div>
                        </div>
                    </div>
                    ` : ''}

                    <div class="settings-section">
                        <div class="settings-section-label">Your Data</div>
                        <div class="settings-card">
                            <div class="settings-row">
                                ${this.isHosted ? `
                                <span class="settings-row-value" style="white-space: normal; color: var(--text-muted); font-family: inherit; text-align: left;">Download a zip archive of your entire site &mdash; posts, snippets, config, themes, and cryptographic keys.</span>
                                <div class="settings-row-actions" style="flex-direction: column; align-items: flex-end; gap: 0.35rem;">
                                    <button class="btn-copy" id="export-btn" onclick="App.requestExport()">Export</button>
                                    <span id="export-status" style="font-size: 0.75rem; color: var(--text-muted);"></span>
                                </div>
                                ` : `
                                <span class="settings-row-value" style="white-space: normal; color: var(--text-muted); font-family: inherit; text-align: left;">Download a zip archive of your entire site &mdash; posts, snippets, config, themes, and cryptographic keys.</span>
                                <div class="settings-row-actions">
                                    <button class="btn-copy" onclick="App.downloadSite()">Download</button>
                                </div>
                                `}
                            </div>
                        </div>
                    </div>
                </div>
            `;

            // Fetch registration status after rendering
            if (site.discovery_configured) {
                this.fetchRegistrationStatus();
            } else {
                const statusEl = document.getElementById('registration-status');
                if (statusEl) {
                    statusEl.innerHTML = `<span style="color: var(--text-muted);">Not configured</span>`;
                }
            }

            // Fetch account info for hosted users
            if (this.isHosted) {
                this.fetchAccountEmail();
            }
        } catch (err) {
            container.innerHTML = `
                <div class="content-list">
                    <div class="empty-state">
                        <h3>Failed to load settings</h3>
                        <p>${this.escapeHtml(err.message)}</p>
                    </div>
                </div>
            `;
        }
    },

    // Truncate public key for display
    truncateKey(key) {
        if (!key) return 'Not generated';
        if (key.length <= 50) return key;
        return key.substring(0, 30) + '...' + key.substring(key.length - 15);
    },

    // Copy public key to clipboard
    async copyPublicKey(key) {
        if (!key) {
            this.showToast('No public key to copy', 'warning');
            return;
        }
        try {
            await navigator.clipboard.writeText(key);
            this.showToast('Public key copied to clipboard', 'success');
            // Update button temporarily
            const btn = document.querySelector('.btn-copy');
            if (btn) {
                btn.classList.add('copied');
                btn.textContent = 'Copied!';
                setTimeout(() => {
                    btn.classList.remove('copied');
                    btn.textContent = 'Copy';
                }, 2000);
            }
        } catch (err) {
            this.showToast('Failed to copy: ' + err.message, 'error');
        }
    },

    openRotatePanel() {
        const panel = document.getElementById('key-rotate-panel');
        if (panel) panel.classList.remove('hidden');
    },

    closeRotatePanel() {
        const panel = document.getElementById('key-rotate-panel');
        if (panel) panel.classList.add('hidden');
    },

    async confirmRotateKeys() {
        this.closeRotatePanel();
        try {
            const data = await this.api('POST', '/api/rotate-key');
            // Update the public key display in settings without full reload
            const display = document.getElementById('public-key-display');
            if (display && data.public_key) {
                display.textContent = this.truncateKey(data.public_key);
            }
            this.showToast('Keys rotated successfully', 'success');
        } catch (err) {
            this.showToast(err.message || 'Failed to rotate keys', 'error');
        }
    },

    editAuthorName() {
        const display = document.getElementById('author-name-display');
        const btn = document.getElementById('author-name-edit-btn');
        if (!display || !btn) return;

        const current = display.textContent === 'Not set' ? '' : display.textContent;
        display.innerHTML = `<input type="text" id="author-name-input" value="${this.escapeHtml(current)}" maxlength="50" placeholder="Display name" style="font-size:0.85rem;font-family:var(--font-mono);background:var(--bg-light);border:1px solid var(--border-color);color:var(--text-color);padding:0.25rem 0.5rem;border-radius:3px;width:100%;">`;
        btn.textContent = 'Save';
        btn.onclick = () => App.saveAuthorName();

        const input = document.getElementById('author-name-input');
        if (input) { input.focus(); input.select(); }
    },

    async saveAuthorName() {
        const input = document.getElementById('author-name-input');
        const display = document.getElementById('author-name-display');
        const btn = document.getElementById('author-name-edit-btn');
        if (!input || !display || !btn) return;

        const name = input.value.trim();
        try {
            const result = await this.api('POST', '/api/settings/author-name', { author_name: name });
            this.authorName = result.author_name || '';
            display.innerHTML = this.escapeHtml(result.author_name) || '<span style="color:var(--text-faint);">Not set</span>';
            btn.textContent = 'Edit';
            btn.onclick = () => App.editAuthorName();
            this.showToast(name ? 'Display name updated' : 'Display name cleared', 'success');
        } catch (err) {
            this.showToast('Failed to update name: ' + err.message, 'error');
        }
    },

    downloadSite() {
        window.location.href = '/api/download-site';
    },

    async requestExport() {
        const btn = document.getElementById('export-btn');
        const status = document.getElementById('export-status');
        if (btn) { btn.disabled = true; btn.textContent = 'Sending...'; }
        try {
            const result = await this.api('POST', '/api/export/request', {});
            if (status) {
                status.style.color = 'var(--accent-color)';
                status.textContent = result.message || 'Check your email for a download link.';
            }
            this.showToast('Export link sent to your email', 'success');
        } catch (err) {
            if (status) {
                status.style.color = 'var(--salmon)';
                status.textContent = err.message || 'Failed to request export.';
            }
            this.showToast('Export request failed: ' + err.message, 'error');
        } finally {
            if (btn) { btn.disabled = false; btn.textContent = 'Export'; }
        }
    },

    async fetchAccountEmail() {
        try {
            const info = await this.api('GET', '/api/account/info');
            const el = document.getElementById('account-email-display');
            if (el) el.textContent = info.email || 'Unknown';
        } catch (err) {
            const el = document.getElementById('account-email-display');
            if (el) el.textContent = 'Failed to load';
        }
    },

    showEmailChangeForm() {
        const form = document.getElementById('email-change-form');
        if (form) {
            form.style.display = 'block';
            const input = document.getElementById('new-email-input');
            if (input) { input.value = ''; input.focus(); }
            const status = document.getElementById('email-change-status');
            if (status) status.textContent = '';
        }
    },

    async requestEmailChange() {
        const input = document.getElementById('new-email-input');
        const btn = document.getElementById('email-change-btn');
        const status = document.getElementById('email-change-status');
        const email = input ? input.value.trim() : '';

        if (!email || !email.includes('@') || !email.includes('.')) {
            if (status) { status.style.color = 'var(--salmon)'; status.textContent = 'Please enter a valid email address.'; }
            return;
        }

        if (btn) { btn.disabled = true; btn.textContent = 'Sending...'; }
        try {
            const result = await this.api('POST', '/api/account/change-email', { email });
            if (status) {
                status.style.color = 'var(--accent-color)';
                status.textContent = result.message || 'Check your new email for a confirmation link.';
            }
            this.showToast('Confirmation link sent to your new email', 'success');
        } catch (err) {
            if (status) {
                status.style.color = 'var(--salmon)';
                status.textContent = err.message || 'Failed to request email change.';
            }
        } finally {
            if (btn) { btn.disabled = false; btn.textContent = 'Send confirmation'; }
        }
    },

    async rerenderSite() {
        const btn = document.getElementById('rerender-btn');
        if (btn) { btn.disabled = true; btn.textContent = 'Rendering...'; }
        try {
            const result = await this.api('POST', '/api/render-page', { path: '/' });
            this.showToast(`Re-rendered ${result.posts_rendered} posts, ${result.comments_rendered} comments`, 'success');
        } catch (err) {
            this.showToast('Re-render failed: ' + err.message, 'error');
        } finally {
            if (btn) { btn.disabled = false; btn.textContent = 'Re-render'; }
        }
    },

    // Theme descriptions for the settings panel. Keys are internal theme
    // Names (as stored in the registry). The picker may display a different
    // label via the bundle.Theme.DisplayName field (e.g. studio13-nk stored
    // internally, labeled "studio13" in the picker) — the description keyed
    // by Name still applies.
    themeDescriptions: {
        'especial': 'Dark gold and navy, inspired by Modelo Especial.',
        'especial-light': 'Light variant of especial with warm fog tones.',
        'sols': 'Violet and peach, inspired by Nine Sols.',
        'studio13': 'Stark black and burnt orange, late-night studio energy.',
        'studio13-nk': 'Stark black and burnt orange, late-night studio energy.',
        'turbo': 'Deep blue with bright cyan, retro computing aesthetic.',
        'vice': 'Warm coral and sunset hues, Miami Vice vibes.',
        'zane': 'Neutral dark with teal and salmon, based on a classic editor theme.',
    },

    // Enable/disable the Change Theme button based on dropdown selection
    onThemeSelectChange() {
        const select = document.getElementById('theme-select');
        const btn = document.getElementById('theme-apply-btn');
        if (!select || !btn) return;
        const selectedOpt = select.options[select.selectedIndex];
        const isOriginal = selectedOpt.dataset.original === 'true';
        btn.disabled = isOriginal;
    },

    // Apply the currently selected theme from dropdown
    async applySelectedTheme() {
        const select = document.getElementById('theme-select');
        if (!select) return;
        const name = select.value;
        try {
            await this.api('POST', '/api/settings/theme', { theme: name });
            this.showToast(`Switched to ${name}`, 'success');
            // Update data-original so the new theme is now "current"
            Array.from(select.options).forEach(opt => {
                opt.dataset.original = opt.value === name ? 'true' : '';
            });
            // Disable button again since selection now matches active theme
            const btn = document.getElementById('theme-apply-btn');
            if (btn) btn.disabled = true;
            // Show the "view site" link
            const link = document.getElementById('theme-view-link');
            if (link) link.style.display = 'flex';
            // Apply new theme to nav immediately
            this.siteTheme = name;
            document.documentElement.dataset.siteTheme = name;
            try { localStorage.setItem('polis-site-theme', name); } catch (e) {}
            this._renderAvatar();
        } catch (err) {
            this.showToast('Failed to switch theme: ' + err.message, 'error');
        }
    },

    // Open the site in a background tab
    viewSite() {
        if (this.siteBaseUrl) {
            window.open(this.siteBaseUrl, '_blank');
        }
    },

    // Delete an automation
    async deleteAutomation(id) {
        const confirmed = await this.showConfirmModal('Remove Automation', 'Remove this automation? The hook will no longer run.', 'Remove', 'Cancel', 'danger');
        if (!confirmed) return;
        try {
            await this.api('DELETE', `/api/automations/${encodeURIComponent(id)}`);
            this.showToast('Automation removed', 'success');
            await this.loadViewContent();
        } catch (err) {
            this.showToast('Failed to remove: ' + err.message, 'error');
        }
    },

    // Fetch site registration status from discovery service
    async fetchRegistrationStatus() {
        const statusEl = document.getElementById('registration-status');
        const actionEl = document.getElementById('registration-action');

        if (!statusEl) return;

        try {
            const result = await this.api('GET', '/api/site/registration-status');

            if (!result.configured) {
                statusEl.innerHTML = `<span style="color: var(--text-muted);">Not configured</span>`;
                if (actionEl) actionEl.innerHTML = '';
                return;
            }

            if (result.error) {
                statusEl.innerHTML = `<span style="color: var(--warning-color);">${this.escapeHtml(result.error)}</span>`;
                return;
            }

            if (result.is_registered) {
                // Format the date nicely
                let dateStr = '';
                if (result.created_at) {
                    try {
                        const date = new Date(result.created_at);
                        dateStr = ` (since ${date.toLocaleDateString()})`;
                    } catch (e) {
                        dateStr = '';
                    }
                }
                statusEl.innerHTML = `<span style="color: var(--success-color);">Registered${dateStr}</span>`;
                if (actionEl) {
                    actionEl.innerHTML = `
                        <a class="settings-action-link" onclick="App.openUnregisterPanel()">Unregister from discovery service</a>
                    `;
                }
            } else {
                statusEl.innerHTML = `<span style="color: var(--text-muted);">Not registered</span>`;
                if (actionEl) {
                    actionEl.innerHTML = `
                        <a class="settings-action-link" onclick="App.openRegisterPanel()">Register with discovery service</a>
                    `;
                }
            }
        } catch (err) {
            statusEl.innerHTML = `<span style="color: var(--warning-color);">Unable to check</span>`;
            if (actionEl) {
                actionEl.innerHTML = `<span style="font-size: 0.85em; color: var(--text-muted);">${this.escapeHtml(err.message)}</span>`;
            }
        }
    },

    // Open registration panel
    openRegisterPanel() {
        const panel = document.getElementById('registration-panel');
        const titleEl = document.getElementById('registration-panel-title');
        const bodyEl = document.getElementById('registration-panel-body');
        const footerEl = document.getElementById('registration-panel-footer');

        titleEl.textContent = 'Register Your Site';

        bodyEl.innerHTML = `
            <div class="wizard-section">
                <p>Registering makes your site discoverable to other authors in the polis network.</p>
                <p style="margin-top: 1rem;">This is <strong>not</strong> a username/password account. Registration simply:</p>
                <ul class="wizard-checklist" style="margin-top: 0.5rem;">
                    <li>Lists your site in the public directory</li>
                    <li>Allows others to find and follow your content</li>
                    <li>Enables you to receive and respond to comments</li>
                    <li>Lets you participate in conversations across the network</li>
                </ul>
                <p style="margin-top: 1rem; color: var(--text-muted);">
                    You can unregister at any time. Your content stays on your server - only the directory listing is affected.
                </p>
            </div>
        `;

        footerEl.innerHTML = `
            <button class="secondary" onclick="App.closeRegistrationPanel()">Cancel</button>
            <div class="wizard-footer-spacer"></div>
            <button id="register-btn" class="primary" onclick="App.registerSite()">Register</button>
        `;

        panel.classList.remove('hidden');
        this.bindRegistrationPanelEvents();
    },

    // Open unregistration panel
    openUnregisterPanel() {
        const panel = document.getElementById('registration-panel');
        const titleEl = document.getElementById('registration-panel-title');
        const bodyEl = document.getElementById('registration-panel-body');
        const footerEl = document.getElementById('registration-panel-footer');

        titleEl.textContent = 'Unregister Your Site';

        bodyEl.innerHTML = `
            <div class="wizard-section">
                <p>Are you sure you want to unregister your site?</p>
                <p style="margin-top: 1rem;">Unregistering will:</p>
                <ul class="wizard-checklist" style="margin-top: 0.5rem;">
                    <li>Remove your site from the public directory</li>
                    <li>Prevent others from discovering you through the network</li>
                    <li>Stop new blessing requests from being delivered</li>
                </ul>
                <p style="margin-top: 1rem; font-weight: 500;">
                    Note: This does not delete any content or links that others have already made to your posts.
                    Existing blessed comments and references will remain intact.
                </p>
                <p style="margin-top: 1rem; color: var(--text-muted);">
                    You can re-register anytime to rejoin the community.
                </p>
            </div>
        `;

        footerEl.innerHTML = `
            <button class="secondary" onclick="App.closeRegistrationPanel()">Cancel</button>
            <div class="wizard-footer-spacer"></div>
            <button id="unregister-btn" class="danger" onclick="App.unregisterSite()">Unregister</button>
        `;

        panel.classList.remove('hidden');
        this.bindRegistrationPanelEvents();
    },

    // Close registration panel
    closeRegistrationPanel() {
        const panel = document.getElementById('registration-panel');
        panel.classList.add('hidden');
    },

    // Bind registration panel events
    bindRegistrationPanelEvents() {
        const closeBtn = document.getElementById('registration-close-btn');
        const overlay = document.querySelector('#registration-panel .wizard-overlay');

        if (closeBtn) closeBtn.onclick = () => this.closeRegistrationPanel();
        if (overlay) overlay.onclick = () => this.closeRegistrationPanel();
    },

    // Register site with discovery service
    async registerSite() {
        const btn = document.getElementById('register-btn');
        if (btn) {
            btn.disabled = true;
            btn.textContent = 'Registering...';
        }

        try {
            await this.api('POST', '/api/site/register');
            this.showToast('Site registered successfully!', 'success');
            this.closeRegistrationPanel();
            // Refresh the settings to show updated status
            await this.loadViewContent();
        } catch (err) {
            this.showToast('Registration failed: ' + err.message, 'error');
            if (btn) {
                btn.disabled = false;
                btn.textContent = 'Register';
            }
        }
    },

    // Unregister site from discovery service
    async unregisterSite() {
        const btn = document.getElementById('unregister-btn');
        if (btn) {
            btn.disabled = true;
            btn.textContent = 'Unregistering...';
        }

        try {
            await this.api('POST', '/api/site/unregister');
            this.showToast('Site unregistered successfully', 'success');
            this.closeRegistrationPanel();
            // Refresh the settings to show updated status
            await this.loadViewContent();
        } catch (err) {
            this.showToast('Unregistration failed: ' + err.message, 'error');
            if (btn) {
                btn.disabled = false;
                btn.textContent = 'Unregister';
            }
        }
    },

    // Wizard state
    wizard: {
        templateId: null,
        step: 1,
        totalSteps: 4,
        deploymentType: null, // 'vercel', 'github-pages', or 'git-commit'
        selectedHookTypes: [], // ['post-publish', 'post-republish', 'post-comment']
    },

    // Store existing hooks from settings
    existingHooks: [],

    // Open wizard panel
    openWizard(templateId) {
        this.wizard.templateId = templateId;
        this.wizard.step = 1;
        this.wizard.totalSteps = templateId === 'deployment' ? 4 : 3;
        this.wizard.deploymentType = null;
        this.wizard.selectedHookTypes = [];

        const panel = document.getElementById('wizard-panel');
        panel.classList.remove('hidden');

        this.renderWizardStep();
        this.bindWizardEvents();
    },

    // Close wizard panel
    closeWizard() {
        const panel = document.getElementById('wizard-panel');
        panel.classList.add('hidden');
        this.wizard.templateId = null;
        this.wizard.step = 1;
        this.wizard.deploymentType = null;
        this.wizard.selectedHookTypes = [];
    },

    // Bind wizard events
    bindWizardEvents() {
        const closeBtn = document.getElementById('wizard-close-btn');
        const overlay = document.querySelector('.wizard-overlay');
        const backBtn = document.getElementById('wizard-back-btn');
        const nextBtn = document.getElementById('wizard-next-btn');

        closeBtn.onclick = () => this.closeWizard();
        overlay.onclick = () => this.closeWizard();
        backBtn.onclick = () => this.wizardBack();
        nextBtn.onclick = () => this.wizardNext();
    },

    // Navigate wizard back
    wizardBack() {
        if (this.wizard.step > 1) {
            this.wizard.step--;
            this.renderWizardStep();
        }
    },

    // Navigate wizard forward or complete
    async wizardNext() {
        if (this.wizard.step < this.wizard.totalSteps) {
            if (this.wizard.templateId === 'deployment') {
                // Deployment wizard flow (4 steps)
                if (this.wizard.step === 1) {
                    // Step 1: Validate deployment method selection
                    const selected = document.querySelector('input[name="deployment-type"]:checked');
                    if (!selected) {
                        this.showToast('Please select a deployment method', 'warning');
                        return;
                    }
                    this.wizard.deploymentType = selected.value;
                    this.wizard.step++;
                    this.renderWizardStep();
                } else if (this.wizard.step === 2) {
                    // Step 2: Validate hook type selection
                    const selected = document.querySelectorAll('input[name="hook-type"]:checked');
                    this.wizard.selectedHookTypes = Array.from(selected).map(el => el.value);
                    if (this.wizard.selectedHookTypes.length === 0) {
                        this.showToast('Please select at least one hook type', 'warning');
                        return;
                    }
                    this.wizard.step++;
                    this.renderWizardStep();
                } else if (this.wizard.step === 3) {
                    // Step 3: Create the hooks
                    const nextBtn = document.getElementById('wizard-next-btn');
                    nextBtn.classList.add('btn-loading');
                    nextBtn.disabled = true;

                    try {
                        // Create hooks for each selected type
                        for (const hookType of this.wizard.selectedHookTypes) {
                            await this.api('POST', '/api/automations', {
                                template_id: this.wizard.deploymentType,
                                hook_type: hookType
                            });
                            this.existingHooks.push(hookType);
                        }
                        this.wizard.step++;
                        this.renderWizardStep();
                    } catch (err) {
                        this.showToast('Failed to create automation: ' + err.message, 'error');
                    } finally {
                        nextBtn.classList.remove('btn-loading');
                        nextBtn.disabled = false;
                    }
                }
            } else if (this.wizard.templateId === 'custom') {
                // Custom script wizard flow (3 steps)
                if (this.wizard.step === 1) {
                    // Step 1 -> 2: Just advance
                    this.wizard.step++;
                    this.renderWizardStep();
                } else if (this.wizard.step === 2) {
                    // Step 2: Validate hook type selection and create hooks
                    const selected = document.querySelectorAll('input[name="hook-type"]:checked');
                    this.wizard.selectedHookTypes = Array.from(selected).map(el => el.value);
                    if (this.wizard.selectedHookTypes.length === 0) {
                        this.showToast('Please select at least one hook type', 'warning');
                        return;
                    }

                    const nextBtn = document.getElementById('wizard-next-btn');
                    nextBtn.classList.add('btn-loading');
                    nextBtn.disabled = true;

                    try {
                        // Create hooks for each selected type
                        for (const hookType of this.wizard.selectedHookTypes) {
                            await this.api('POST', '/api/hooks/generate', { hook_type: hookType });
                            this.existingHooks.push(hookType);
                        }
                        this.wizard.step++;
                        this.renderWizardStep();
                    } catch (err) {
                        this.showToast('Failed to create hook: ' + err.message, 'error');
                    } finally {
                        nextBtn.classList.remove('btn-loading');
                        nextBtn.disabled = false;
                    }
                }
            } else {
                this.wizard.step++;
                this.renderWizardStep();
            }
        } else {
            // Complete: close wizard and refresh
            this.closeWizard();
            await this.loadViewContent();
        }
    },

    // Render current wizard step
    renderWizardStep() {
        const titleEl = document.getElementById('wizard-title');
        const currentEl = document.getElementById('wizard-step-current');
        const totalEl = document.getElementById('wizard-step-total');
        const bodyEl = document.getElementById('wizard-body');
        const backBtn = document.getElementById('wizard-back-btn');
        const nextBtn = document.getElementById('wizard-next-btn');

        currentEl.textContent = this.wizard.step;
        totalEl.textContent = this.wizard.totalSteps;

        // Show/hide back button
        backBtn.style.display = this.wizard.step > 1 ? 'inline-block' : 'none';

        // Update button text based on template and step
        if (this.wizard.templateId === 'deployment') {
            // 4-step deployment wizard
            if (this.wizard.step === 3) {
                nextBtn.textContent = 'Create scripts \u2192';
            } else if (this.wizard.step === 4) {
                nextBtn.textContent = 'Done \u2713';
            } else {
                nextBtn.textContent = 'Next \u2192';
            }
        } else if (this.wizard.templateId === 'custom') {
            // 3-step custom wizard
            if (this.wizard.step === 2) {
                nextBtn.textContent = 'Create scripts \u2192';
            } else if (this.wizard.step === 3) {
                nextBtn.textContent = 'Done \u2713';
            } else {
                nextBtn.textContent = 'Next \u2192';
            }
        } else {
            nextBtn.textContent = 'Next \u2192';
        }

        // Get wizard content based on template and step
        const content = this.getWizardContent(this.wizard.templateId, this.wizard.step);
        titleEl.textContent = content.title;
        bodyEl.innerHTML = content.body;
    },

    // Generate hook type checkboxes HTML
    getHookTypeCheckboxes() {
        const hookTypes = [
            { id: 'post-publish', name: 'post-publish', desc: 'Runs after a new post is published' },
            { id: 'post-republish', name: 'post-republish', desc: 'Runs after an existing post is updated' },
            { id: 'post-comment', name: 'post-comment', desc: 'Runs after you bless a comment on your site' },
        ];

        return hookTypes.map(hook => {
            const exists = this.existingHooks.includes(hook.id);
            const disabled = exists ? 'disabled' : '';
            const checked = !exists ? 'checked' : '';
            const existsLabel = exists ? '<span class="hook-exists-inline">(already exists)</span>' : '';
            return `
                <label class="hook-type-checkbox ${exists ? 'disabled' : ''}">
                    <input type="checkbox" name="hook-type" value="${hook.id}" ${disabled} ${checked}>
                    <div class="hook-type-checkbox-content">
                        <div class="hook-type-checkbox-name">${hook.name} ${existsLabel}</div>
                        <div class="hook-type-checkbox-desc">${hook.desc}</div>
                    </div>
                </label>
            `;
        }).join('');
    },

    // Get script preview for deployment type
    getDeploymentScriptPreview() {
        const scripts = {
            'vercel': `#!/bin/bash
set -e
cd "$POLIS_SITE_DIR"
git add -A
git commit -m "$POLIS_COMMIT_MESSAGE"
git push`,
            'github-pages': `#!/bin/bash
set -e
cd "$POLIS_SITE_DIR"
git add -A
git commit -m "$POLIS_COMMIT_MESSAGE"
git push`,
            'git-commit': `#!/bin/bash
set -e
cd "$POLIS_SITE_DIR"
git add -A
git commit -m "$POLIS_COMMIT_MESSAGE"`
        };
        return scripts[this.wizard.deploymentType] || scripts['git-commit'];
    },

    // Get wizard content for a template and step
    getWizardContent(templateId, step) {
        // Deployment wizard (4 steps)
        if (templateId === 'deployment') {
            if (step === 1) {
                return {
                    title: 'Deploy my content using git',
                    body: `
                        <div class="wizard-section">
                            <p>Which deployment method would you like to use?</p>
                            <div class="deployment-options">
                                <label class="deployment-option">
                                    <input type="radio" name="deployment-type" value="vercel">
                                    <div class="deployment-option-content">
                                        <div class="deployment-option-title">Vercel</div>
                                        <div class="deployment-option-desc">Commit, push, and let Vercel auto-deploy</div>
                                    </div>
                                </label>
                                <label class="deployment-option">
                                    <input type="radio" name="deployment-type" value="github-pages">
                                    <div class="deployment-option-content">
                                        <div class="deployment-option-title">GitHub Pages</div>
                                        <div class="deployment-option-desc">Commit, push, and let GitHub Pages rebuild</div>
                                    </div>
                                </label>
                                <label class="deployment-option">
                                    <input type="radio" name="deployment-type" value="git-commit">
                                    <div class="deployment-option-content">
                                        <div class="deployment-option-title">Git repository only</div>
                                        <div class="deployment-option-desc">Commit changes without pushing (manual deployment)</div>
                                    </div>
                                </label>
                            </div>
                        </div>
                    `
                };
            } else if (step === 2) {
                const methodNames = { 'vercel': 'Vercel', 'github-pages': 'GitHub Pages', 'git-commit': 'git' };
                const methodName = methodNames[this.wizard.deploymentType] || 'git';
                return {
                    title: 'Deploy my content using git',
                    body: `
                        <div class="wizard-section">
                            <p>Which events should trigger ${methodName} deployment?</p>
                            <div class="hook-type-checkboxes">
                                ${this.getHookTypeCheckboxes()}
                            </div>
                            <p class="wizard-hint">Scripts will be created at <code>.polis/hooks/{event}.sh</code></p>
                        </div>
                    `
                };
            } else if (step === 3) {
                const selectedHooks = this.wizard.selectedHookTypes.map(h => `<code>${h}</code>`).join(', ');
                return {
                    title: 'Deploy my content using git',
                    body: `
                        <div class="wizard-section">
                            <p>The following script will be created for: ${selectedHooks}</p>
                            <div class="wizard-code-block">
                                <code>${this.escapeHtml(this.getDeploymentScriptPreview())}</code>
                            </div>
                            <div class="wizard-prereqs">
                                <div class="wizard-prereqs-title">Prerequisites:</div>
                                <ul>
                                    <li>Your site directory is a git repository</li>
                                    <li>Git is configured with your name and email</li>
                                    ${this.wizard.deploymentType !== 'git-commit' ? '<li>Remote is configured (origin &rarr; your repo)</li>' : ''}
                                </ul>
                            </div>
                        </div>
                    `
                };
            } else if (step === 4) {
                const createdFiles = this.wizard.selectedHookTypes.map(h =>
                    `<div class="wizard-info-row"><span class="wizard-info-label">Created:</span><span class="wizard-info-value">.polis/hooks/${h}.sh</span></div>`
                ).join('');
                return {
                    title: 'Deploy my content using git',
                    body: `
                        <div class="wizard-section">
                            <div class="wizard-success">
                                <span class="wizard-success-icon">&#10003;</span>
                                Automation created
                            </div>
                            <div class="wizard-info-block">
                                ${createdFiles}
                            </div>
                            <div class="wizard-help-section">
                                <div class="wizard-help-title">To test it:</div>
                                <ul class="wizard-help-list">
                                    <li>Publish a post, update a post, or bless a comment</li>
                                    <li>Check that the corresponding hook ran successfully</li>
                                </ul>
                            </div>
                            <div class="wizard-help-section">
                                <div class="wizard-help-title">If something goes wrong:</div>
                                <ul class="wizard-help-list">
                                    <li>Check that <code>git</code> commands work from your terminal</li>
                                    <li>Edit the scripts at <code>.polis/hooks/*.sh</code></li>
                                </ul>
                            </div>
                        </div>
                    `
                };
            }
        }

        // Custom script wizard (3 steps)
        if (templateId === 'custom') {
            if (step === 1) {
                return {
                    title: 'Custom automation scripts',
                    body: `
                        <div class="wizard-section">
                            <p>Polis supports three hook types that run shell scripts when events occur:</p>
                            <div class="hook-types-explained">
                                <div class="hook-type-explained">
                                    <div class="hook-type-explained-name">post-publish</div>
                                    <div class="hook-type-explained-desc">Runs after you publish a <em>new</em> post. The post file and metadata have been written.</div>
                                </div>
                                <div class="hook-type-explained">
                                    <div class="hook-type-explained-name">post-republish</div>
                                    <div class="hook-type-explained-desc">Runs after you update an <em>existing</em> post. The updated file and metadata have been written.</div>
                                </div>
                                <div class="hook-type-explained">
                                    <div class="hook-type-explained-name">post-comment</div>
                                    <div class="hook-type-explained-desc">Runs after you bless a comment on your site. The comment file has been written to <code>comments/blessed/</code>.</div>
                                </div>
                            </div>
                            <p>Each hook receives environment variables you can use in your script:</p>
                            <div class="wizard-code-block">
                                <code>POLIS_SITE_DIR       # Path to your site directory
POLIS_PATH           # Relative path to the file
POLIS_TITLE          # Title of the post (or in_reply_to URL for comments)
POLIS_COMMIT_MESSAGE # Suggested commit message
POLIS_EVENT          # Event type (post-publish, post-republish, post-comment)
POLIS_VERSION        # Content hash
POLIS_TIMESTAMP      # ISO timestamp</code>
                            </div>
                        </div>
                    `
                };
            } else if (step === 2) {
                return {
                    title: 'Custom automation scripts',
                    body: `
                        <div class="wizard-section">
                            <p>Which hooks would you like to create?</p>
                            <div class="hook-type-checkboxes">
                                ${this.getHookTypeCheckboxes()}
                            </div>
                            <p>A starter script will be created that you can customize:</p>
                            <div class="wizard-code-block">
                                <code>#!/bin/bash
set -e
# Add your custom logic here
echo "Hook triggered: $POLIS_EVENT"
echo "File: $POLIS_PATH"</code>
                            </div>
                            <p class="wizard-hint">Scripts are saved to <code>.polis/hooks/{event}.sh</code></p>
                        </div>
                    `
                };
            } else if (step === 3) {
                const createdFiles = this.wizard.selectedHookTypes.map(h =>
                    `<div class="wizard-info-row"><span class="wizard-info-label">Created:</span><span class="wizard-info-value">.polis/hooks/${h}.sh</span></div>`
                ).join('');
                return {
                    title: 'Custom automation scripts',
                    body: `
                        <div class="wizard-section">
                            <div class="wizard-success">
                                <span class="wizard-success-icon">&#10003;</span>
                                Hook scripts created
                            </div>
                            <div class="wizard-info-block">
                                ${createdFiles}
                            </div>
                            <div class="wizard-help-section">
                                <div class="wizard-help-title">Next steps:</div>
                                <ul class="wizard-help-list">
                                    <li>Edit the scripts to add your custom logic</li>
                                    <li>Test by publishing a post, updating a post, or blessing a comment</li>
                                </ul>
                            </div>
                            <div class="wizard-help-section">
                                <div class="wizard-help-title">Troubleshooting:</div>
                                <ul class="wizard-help-list">
                                    <li>Scripts must be executable (<code>chmod +x</code>)</li>
                                    <li>Use <code>set -e</code> to stop on errors</li>
                                    <li>Check the webapp logs for hook output</li>
                                </ul>
                            </div>
                        </div>
                    `
                };
            }
        }

        // Fallback
        return { title: 'Setup', body: '<p>Unknown wizard step</p>'
        };
    },

    // No-op: right panel removed in editor redesign
    editorUpdatePreview() {},

    // Markdown keyboard shortcuts for raw textareas (Ctrl+B, Ctrl+I, Ctrl+K, Ctrl+E)
    _handleMarkdownShortcut(textarea, e) {
        if (!e.ctrlKey && !e.metaKey) return;
        const shortcuts = {
            'b': { wrap: '**', placeholder: 'bold' },
            'i': { wrap: '*', placeholder: 'italic' },
            'e': { wrap: '`', placeholder: 'code' },
        };

        const key = e.key.toLowerCase();

        if (shortcuts[key]) {
            e.preventDefault();
            e.stopPropagation();
            const { wrap, placeholder } = shortcuts[key];
            const start = textarea.selectionStart;
            const end = textarea.selectionEnd;
            const selected = textarea.value.substring(start, end);
            const text = selected || placeholder;
            const before = textarea.value.substring(0, start);
            const after = textarea.value.substring(end);
            textarea.value = before + wrap + text + wrap + after;
            // Place cursor: if had selection, select the wrapped text; otherwise select placeholder
            if (selected) {
                textarea.selectionStart = start + wrap.length;
                textarea.selectionEnd = start + wrap.length + text.length;
            } else {
                textarea.selectionStart = start + wrap.length;
                textarea.selectionEnd = start + wrap.length + placeholder.length;
            }
            textarea.focus();
            return;
        }

        if (key === 'k') {
            e.preventDefault();
            e.stopPropagation();
            const start = textarea.selectionStart;
            const end = textarea.selectionEnd;
            const selected = textarea.value.substring(start, end);
            const before = textarea.value.substring(0, start);
            const after = textarea.value.substring(end);
            const linkText = selected || 'text';
            textarea.value = before + '[' + linkText + '](url)' + after;
            // Select "url" for easy replacement
            const urlStart = start + 1 + linkText.length + 2;
            textarea.selectionStart = urlStart;
            textarea.selectionEnd = urlStart + 3;
            textarea.focus();
            return;
        }
    },


    // Utility: slugify text for filename
    slugify(text) {
        return text
            .toLowerCase()
            .replace(/[^a-z0-9]+/g, '-')
            .replace(/^-+|-+$/g, '')
            .substring(0, 50) || 'untitled';
    },

    // ========================================================================
    // Social features: sidebar mode, feed, following, remote post
    // ========================================================================

    async setSidebarMode(mode) {
        this.sidebarMode = mode;
        const mySite = document.getElementById('sidebar-my-site');
        const social = document.getElementById('sidebar-social');

        // Toggle sidebar sections
        if (mode === 'social') {
            mySite.classList.add('hidden');
            social.classList.remove('hidden');
            // Refresh counts so follower/following data is up-to-date
            await this.loadAllCounts();
            this.setActiveView('feed');
        } else {
            social.classList.add('hidden');
            mySite.classList.remove('hidden');
            this.setActiveView('posts-published');
        }

        // Update tab active state
        document.querySelectorAll('.sidebar-mode-toggle .mode-tab').forEach(tab => {
            tab.classList.toggle('active', tab.dataset.sidebarMode === mode);
        });

        // Re-evaluate welcome panel visibility for the new tab
        this.updateWelcomePanel();
    },

    // ── Inline Comment Editor (in-feed reply) ──

    openInlineCommentEditor(postUrl) {
        // Close any existing inline comment editor
        if (this._inlineCommentOpen) {
            this._closeInlineCommentEditorImmediate();
        }

        // Find the feed item by URL. v3 .feed-item entries are no
        // longer rendered (chunk A retired renderConversationsTabbed),
        // so this early-return path is effectively dead — the v4
        // stream uses owner-extras.mountCommentEditor for comment
        // composition. Kept here as a defensive no-op for any caller
        // that still routes through this function.
        const feedItem = document.querySelector(`.feed-item[data-url="${CSS.escape(postUrl)}"]`);
        if (!feedItem) {
            return;
        }

        this._inlineCommentOpen = true;
        this._inlineCommentUrl = postUrl;
        this._inlineCommentBody = '';
        this._inlineCommentDraftId = null;

        // Extract context from the feed item DOM
        this._inlineCommentAuthor = feedItem.querySelector('.author-name')?.textContent || '';
        this._inlineCommentTitle = (feedItem.querySelector('.item-title') || feedItem.querySelector('.item-excerpt'))?.textContent || '';

        // Highlight the Reply button
        feedItem.querySelector('.reply-btn')?.classList.add('active');

        // Create editor element
        const editorEl = document.createElement('div');
        editorEl.id = 'inline-comment-wrapper';
        editorEl.className = 'inline-comment-wrapper';
        editorEl.innerHTML = `
            <div class="inline-comment-card">
                <div class="inline-comment-context">
                    <svg class="inline-comment-reply-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <polyline points="9 17 4 12 9 7"/>
                        <path d="M20 18v-2a4 4 0 0 0-4-4H4"/>
                    </svg>
                    <span>Replying to</span>
                    <span class="inline-comment-context-author">${this.escapeHtml(this._inlineCommentAuthor)}</span>
                    <span class="inline-comment-context-title">&mdash; ${this.escapeHtml(this._inlineCommentTitle)}</span>
                </div>
                <div id="milkdown-inline-comment" class="milkdown-mount"></div>
                <textarea id="inline-comment-body" class="hidden"></textarea>
                <div class="inline-comment-footer">
                    <span class="inline-comment-hint"><kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>F</kbd> focus &middot; <kbd>Esc</kbd> cancel</span>
                    <span class="inline-comment-status" id="inline-comment-status"></span>
                    <button class="inline-comment-cancel" onclick="App.closeInlineCommentEditor()">Cancel</button>
                    <button class="inline-comment-send" id="inline-comment-send" disabled onclick="App.sendInlineComment()">Sign &amp; Send</button>
                </div>
            </div>
        `;

        // Insert after the feed item (as sibling in .feed-list)
        feedItem.insertAdjacentElement('afterend', editorEl);

        // Animate open
        requestAnimationFrame(() => {
            editorEl.classList.add('open');
        });

        // Init Milkdown after animation
        setTimeout(async () => {
            await this._initMilkdown('inline-comment-body');
            // milkdown:change is dispatched on document, not the mount element
            this._inlineCommentChangeHandler = (e) => {
                if (e.detail?.editorId === 'milkdown-inline-comment') {
                    this._onInlineCommentInput();
                }
            };
            document.addEventListener('milkdown:change', this._inlineCommentChangeHandler);
            document.querySelector('#milkdown-inline-comment .ProseMirror')?.focus();
        }, 350);

        // Load existing draft
        this._loadInlineCommentDraft(postUrl);

        // Scroll into view
        setTimeout(() => {
            editorEl.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
        }, 100);
    },

    closeInlineCommentEditor() {
        if (!this._inlineCommentOpen) return;
        this._destroyMilkdown('inline-comment-body');
        if (this._inlineCommentChangeHandler) {
            document.removeEventListener('milkdown:change', this._inlineCommentChangeHandler);
            this._inlineCommentChangeHandler = null;
        }
        if (this._inlineCommentSaveTimer) clearTimeout(this._inlineCommentSaveTimer);

        // Remove active state from Reply button
        document.querySelectorAll('.reply-btn.active').forEach(btn => btn.classList.remove('active'));

        const wrapper = document.getElementById('inline-comment-wrapper');
        if (wrapper) {
            wrapper.classList.remove('open');
            setTimeout(() => wrapper.remove(), 400);
        }

        this._inlineCommentOpen = false;
        this._inlineCommentUrl = null;
        this._inlineCommentAuthor = '';
        this._inlineCommentTitle = '';
        this._inlineCommentBody = '';
        this._inlineCommentDraftId = null;
        this._inlineCommentStatus = '';
    },

    // Immediate close without animation (for switching between items)
    _closeInlineCommentEditorImmediate() {
        this._destroyMilkdown('inline-comment-body');
        if (this._inlineCommentChangeHandler) {
            document.removeEventListener('milkdown:change', this._inlineCommentChangeHandler);
            this._inlineCommentChangeHandler = null;
        }
        if (this._inlineCommentSaveTimer) clearTimeout(this._inlineCommentSaveTimer);
        document.querySelectorAll('.reply-btn.active').forEach(btn => btn.classList.remove('active'));
        document.getElementById('inline-comment-wrapper')?.remove();
        this._inlineCommentOpen = false;
        this._inlineCommentUrl = null;
        this._inlineCommentAuthor = '';
        this._inlineCommentTitle = '';
        this._inlineCommentBody = '';
        this._inlineCommentDraftId = null;
        this._inlineCommentStatus = '';
    },

    _onInlineCommentInput() {
        this._inlineCommentBody = this.getEditorContent('inline-comment-body') || '';
        const hasContent = !!this._inlineCommentBody.trim();
        const btn = document.getElementById('inline-comment-send');
        if (btn) btn.disabled = !hasContent;
        if (!hasContent) {
            this._updateInlineCommentStatus('');
            return;
        }
        this._updateInlineCommentStatus('Unsaved');
        if (this._inlineCommentSaveTimer) clearTimeout(this._inlineCommentSaveTimer);
        this._inlineCommentSaveTimer = setTimeout(() => this._saveInlineCommentDraft(), 2000);
    },

    _updateInlineCommentStatus(text, saved) {
        const el = document.getElementById('inline-comment-status');
        if (!el) return;
        el.textContent = text;
        if (saved) el.classList.add('saved');
        else el.classList.remove('saved');
    },

    async _saveInlineCommentDraft() {
        const body = this._inlineCommentBody || '';
        if (!body.trim()) return;
        this._updateInlineCommentStatus('Saving...');
        try {
            const result = await this.api('POST', '/api/comments/drafts', {
                id: this._inlineCommentDraftId || '',
                in_reply_to: this._inlineCommentUrl,
                content: body,
            });
            if (result.id) this._inlineCommentDraftId = result.id;
            this._updateInlineCommentStatus('Draft saved', true);
        } catch (e) {
            this._updateInlineCommentStatus('Save failed');
        }
    },

    async _loadInlineCommentDraft(postUrl) {
        try {
            const result = await this.api('GET', '/api/comments/drafts');
            const drafts = result.drafts || [];
            const draft = drafts.find(d => d.in_reply_to === postUrl);
            if (draft && this._inlineCommentUrl === postUrl) {
                this._inlineCommentDraftId = draft.id;
                this._inlineCommentBody = draft.content || '';
                this.setEditorContent('inline-comment-body', this._inlineCommentBody);
                this._updateInlineCommentStatus('Draft loaded', true);
            }
        } catch (e) {
            // Start fresh if draft load fails
        }
    },

    async sendInlineComment() {
        const url = this._inlineCommentUrl;
        const content = this.getEditorContent('inline-comment-body');
        if (!url || !content?.trim()) {
            this.showToast('Please write a comment', 'warning');
            return;
        }

        const confirmed = await this.showConfirmModal(
            'Send for Blessing',
            `Sign this comment and send it to ${this._inlineCommentAuthor || 'the author'} for blessing?`,
            'Sign & Send'
        );
        if (!confirmed) return;

        const btn = document.getElementById('inline-comment-send');
        if (btn) { btn.disabled = true; btn.textContent = 'Sending...'; }

        try {
            const signResult = await this.api('POST', '/api/comments/sign', {
                draft_id: this._inlineCommentDraftId || '',
                in_reply_to: url,
                content: content,
            });

            if (!signResult.success) throw new Error(signResult.error || 'Failed to sign comment');

            try {
                const beseechResult = await this.api('POST', '/api/comments/beseech', {
                    comment_id: signResult.comment?.id || signResult.id,
                });
                if (beseechResult.status === 'blessed') {
                    this.showToast('Comment auto-blessed!', 'success');
                } else {
                    this.showToast('Comment signed & sent for blessing', 'success');
                }
            } catch (beseechErr) {
                this.showToast('Comment signed. Could not send blessing request: ' + beseechErr.message, 'warning', 6000);
            }

            this.closeInlineCommentEditor();
            await this.loadAllCounts();
            // Parent-view refresh removed in chunk A — the
            // renderConversationsTabbed surface this used to refresh
            // is retired. v4 stream handles its own refresh via
            // PolisStream.refresh().
        } catch (err) {
            this.showToast('Failed: ' + err.message, 'error');
            if (btn) { btn.disabled = false; btn.textContent = 'Sign & Send'; }
        }
    },

    // ── Inline Comment Focus Mode ──

    _toggleInlineCommentFocusMode() {
        if (!this._inlineCommentOpen) return;
        this._inlineCommentFocusMode = !this._inlineCommentFocusMode;

        if (this._inlineCommentFocusMode) {
            // Save editor content before moving to focus overlay
            this._inlineCommentBody = this.getEditorContent('inline-comment-body') || '';
            this._destroyMilkdown('inline-comment-body');

            const overlay = document.createElement('div');
            overlay.id = 'inline-comment-focus';
            overlay.className = 'inline-comment-focus';
            overlay.innerHTML = `
                <div class="icf-container">
                    <div class="icf-post">
                        <div class="icf-post-label">Original post</div>
                        <iframe src="${this.escapeHtml(this._inlineCommentUrl)}" class="icf-post-frame" sandbox="allow-same-origin"></iframe>
                    </div>
                    <div class="icf-editor">
                        <div class="icf-editor-label">Your reply to <strong>${this.escapeHtml(this._inlineCommentAuthor)}</strong></div>
                        <div class="icf-editor-body">
                            <div id="milkdown-inline-comment-focus" class="milkdown-mount"></div>
                            <textarea id="inline-comment-body-focus" class="hidden">${this.escapeHtml(this._inlineCommentBody)}</textarea>
                        </div>
                        <div class="icf-footer">
                            <span class="inline-comment-hint"><kbd>Esc</kbd> exit focus</span>
                            <span class="inline-comment-status" id="inline-comment-status-focus"></span>
                            <button class="inline-comment-cancel" onclick="App._toggleInlineCommentFocusMode()">Exit Focus</button>
                            <button class="inline-comment-send" id="inline-comment-send-focus" onclick="App._sendFromFocusMode()">Sign &amp; Send</button>
                        </div>
                    </div>
                </div>
            `;
            document.body.appendChild(overlay);
            document.getElementById('icon-nav')?.classList.add('focus-mode');

            // Init Milkdown in focus overlay
            setTimeout(async () => {
                await this._initMilkdown('inline-comment-body-focus');
                // milkdown:change is dispatched on document, not the mount element
                this._inlineCommentFocusChangeHandler = (e) => {
                    if (e.detail?.editorId === 'milkdown-inline-comment-focus') {
                        this._inlineCommentBody = this.getEditorContent('inline-comment-body-focus') || '';
                        const hasContent = !!this._inlineCommentBody.trim();
                        const btn = document.getElementById('inline-comment-send-focus');
                        if (btn) btn.disabled = !hasContent;
                    }
                };
                document.addEventListener('milkdown:change', this._inlineCommentFocusChangeHandler);
                document.querySelector('#milkdown-inline-comment-focus .ProseMirror')?.focus();
            }, 200);
        } else {
            // Exit focus mode — restore content to inline editor
            const content = this.getEditorContent('inline-comment-body-focus') || this._inlineCommentBody;
            this._destroyMilkdown('inline-comment-body-focus');
            if (this._inlineCommentFocusChangeHandler) {
                document.removeEventListener('milkdown:change', this._inlineCommentFocusChangeHandler);
                this._inlineCommentFocusChangeHandler = null;
            }
            document.getElementById('inline-comment-focus')?.remove();
            document.getElementById('icon-nav')?.classList.remove('focus-mode');
            this._inlineCommentBody = content;

            // Re-init inline Milkdown with saved content
            setTimeout(async () => {
                await this._initMilkdown('inline-comment-body');
                this.setEditorContent('inline-comment-body', content);
                this._inlineCommentChangeHandler = (e) => {
                    if (e.detail?.editorId === 'milkdown-inline-comment') {
                        this._onInlineCommentInput();
                    }
                };
                document.addEventListener('milkdown:change', this._inlineCommentChangeHandler);
                document.querySelector('#milkdown-inline-comment .ProseMirror')?.focus();
            }, 200);
        }
    },

    async _sendFromFocusMode() {
        // Sync content from focus editor back to state
        this._inlineCommentBody = this.getEditorContent('inline-comment-body-focus') || '';
        // Exit focus mode first
        this._destroyMilkdown('inline-comment-body-focus');
        if (this._inlineCommentFocusChangeHandler) {
            document.removeEventListener('milkdown:change', this._inlineCommentFocusChangeHandler);
            this._inlineCommentFocusChangeHandler = null;
        }
        document.getElementById('inline-comment-focus')?.remove();
        document.getElementById('icon-nav')?.classList.remove('focus-mode');
        this._inlineCommentFocusMode = false;
        // Restore inline editor content and send
        this.setEditorContent('inline-comment-body', this._inlineCommentBody);
        await this.sendInlineComment();
    },

    // Viewport-based auto-mark-read: items are marked read when scrolled into view
    _initFeedObserver() {
        if (this._feedObserver) this._feedObserver.disconnect();
        this._feedObserver = new IntersectionObserver((entries) => {
            if (document.hidden) return;
            for (const entry of entries) {
                if (entry.isIntersecting && entry.target.classList.contains('unread')) {
                    const ids = entry.target.dataset.ids;
                    if (ids) {
                        entry.target.classList.remove('unread');
                        this._queueMarkRead(JSON.parse(ids));
                    }
                    this._feedObserver.unobserve(entry.target);
                }
            }
        }, { threshold: 0.5 });
    },

    _queueMarkRead(ids) {
        this._markReadQueue.push(...ids);
        if (this._markReadTimer) clearTimeout(this._markReadTimer);
        this._markReadTimer = setTimeout(() => this._flushMarkRead(), 1000);
    },

    async _flushMarkRead() {
        const ids = [...this._markReadQueue];
        this._markReadQueue = [];
        if (ids.length === 0) return;
        try {
            await Promise.all(ids.map(id => this.api('POST', '/api/feed/read', { id })));
            this.counts.feedUnread = Math.max(0, (this.counts.feedUnread || 0) - ids.length);
            this._updateTopbarBadges();
        } catch (e) {}
    },

    _observeFeedItems(container) {
        this._initFeedObserver();
        container.querySelectorAll('.feed-item.unread').forEach(el => {
            this._feedObserver.observe(el);
        });
    },


    openFollowPanel() {
        const panel = document.getElementById('follow-panel');
        const input = document.getElementById('follow-url-input');
        const suggestion = document.getElementById('follow-suggestion');
        if (panel) panel.classList.remove('hidden');
        if (input) {
            input.value = '';
            input.focus();
            // Autocomplete bare handles to .polis.pub on blur
            if (!input._polisBlurBound) {
                input.addEventListener('blur', () => {
                    const val = input.value.trim();
                    // Bare handle: no dots, no protocol, no slashes — append .polis.pub
                    if (val && !val.includes('.') && !val.includes('/') && !val.includes(':')) {
                        input.value = val + '.polis.pub';
                    }
                });
                input._polisBlurBound = true;
            }
        }
        if (suggestion) {
            if (this.counts.following === 0) {
                suggestion.classList.remove('hidden');
            } else {
                suggestion.classList.add('hidden');
            }
        }
    },

    closeFollowPanel() {
        const panel = document.getElementById('follow-panel');
        if (panel) panel.classList.add('hidden');
    },

    // Normalize follow input: accept bare domains, follow links, and full URLs.
    // Always lowercases the domain for consistent display and comparison.
    normalizeFollowInput(raw) {
        let val = raw.trim().toLowerCase();
        // Strip protocol for analysis
        const bare = val.replace(/^https?:\/\//, '');

        // Detect follow link: polis.pub/f/<handle> or <domain>/f/<handle>
        const followMatch = bare.match(/^[^/]+\/f\/([a-z0-9][a-z0-9-]*[a-z0-9])$/i);
        if (followMatch) {
            return 'https://' + followMatch[1] + '.polis.pub/';
        }

        // Detect follow page: polis.pub/follow?author=<domain>
        if (bare.match(/^[^/]+\/follow\?author=/i)) {
            try {
                const u = new URL(val.startsWith('http') ? val : 'https://' + val);
                const author = u.searchParams.get('author');
                if (author) return 'https://' + author.toLowerCase() + '/';
            } catch(e) {}
        }

        // Bare domain (no protocol, no path or just /): add https://
        if (!val.startsWith('http://') && !val.startsWith('https://')) {
            val = 'https://' + val;
        }

        // Ensure trailing slash
        if (!val.endsWith('/')) val += '/';
        return val;
    },

    async submitFollow() {
        const input = document.getElementById('follow-url-input');
        const raw = (input.value || '').trim();
        if (!raw) {
            this.showToast('Please enter a URL or domain', 'error');
            return;
        }

        const url = this.normalizeFollowInput(raw);
        if (!url.startsWith('https://')) {
            this.showToast('URL must use HTTPS', 'error');
            return;
        }

        // Prevent self-follow
        try {
            const targetHost = new URL(url).hostname;
            if (targetHost === window.location.hostname) {
                this.showToast('You cannot follow your own site', 'error');
                return;
            }
        } catch (e) {
            // URL parsing failed — let server validate
        }

        try {
            this.showToast('Following...', 'info', 2000);
            const result = await this.api('POST', '/api/following', { url });
            this.closeFollowPanel();
            if (result.data && result.data.already_followed) {
                this.showToast('Already following this author', 'info');
            } else {
                const blessed = result.data ? result.data.comments_blessed : 0;
                let msg = 'Now following ' + url;
                if (blessed > 0) msg += ` (blessed ${blessed} comment${blessed > 1 ? 's' : ''})`;
                this.showToast(msg, 'success');
            }
            await this.loadAllCounts();
            await this.loadViewContent();
        } catch (err) {
            this.showToast('Failed to follow: ' + err.message, 'error');
        }
    },

    // Quick follow from suggestion toast (no panel, no confirmation)
    async quickFollow(domain) {
        try {
            const result = await this.api('POST', '/api/following', { url: 'https://' + domain + '/' });
            if (result.data && result.data.already_followed) {
                this.showToast('Already following ' + domain, 'info');
            } else {
                this.showToast('Now following ' + domain, 'success');
            }
            this.loadAllCounts();
        } catch (err) {
            this.showToast('Failed to follow: ' + err.message, 'error');
        }
    },

    async followDiscover() {
        try {
            this.showToast('Following discover.polis.pub...', 'info', 2000);
            const result = await this.api('POST', '/api/following', { url: 'https://discover.polis.pub/' });
            if (result.data && result.data.already_followed) {
                this.showToast('Already following discover.polis.pub', 'info');
            } else {
                this.showToast('Now following discover.polis.pub', 'success');
            }
            await this.loadAllCounts();
            await this.loadViewContent();
        } catch (err) {
            this.showToast('Failed to follow: ' + err.message, 'error');
        }
    },

    async unfollowAuthor(url) {
        const confirmed = await this.showConfirmModal(
            'Unfollow Author',
            'Are you sure you want to unfollow ' + url + '? Any blessed comments from this author will be denied.',
            'Unfollow',
            'Cancel',
            'danger'
        );
        if (!confirmed) return;

        try {
            const result = await this.api('DELETE', '/api/following', { url });
            const denied = result.data ? result.data.comments_denied : 0;
            let msg = 'Unfollowed ' + url;
            if (denied > 0) msg += ` (denied ${denied} comment${denied > 1 ? 's' : ''})`;
            this.showToast(msg, 'success');

            await this.loadAllCounts();
            await this.loadViewContent();

            // Activity data is now cached server-side; no client-side filtering needed
        } catch (err) {
            this.showToast('Failed to unfollow: ' + err.message, 'error');
        }
    },


    async openRemotePost(postUrl, authorUrl, title) {
        const panel = document.getElementById('remote-post-panel');
        const titleEl = document.getElementById('remote-post-title');
        const metaEl = document.getElementById('remote-post-meta');
        const bodyEl = document.getElementById('remote-post-body');

        titleEl.textContent = title || 'Remote Post';
        metaEl.innerHTML = '<p>Loading...</p>';
        bodyEl.innerHTML = '';
        panel.classList.remove('hidden');

        // Build the full post URL from relative path + author base
        let fullUrl;
        if (postUrl.startsWith('https://') || postUrl.startsWith('http://')) {
            fullUrl = postUrl;
        } else {
            const base = authorUrl.replace(/\/$/, '');
            const path = postUrl.startsWith('/') ? postUrl : '/' + postUrl;
            fullUrl = base + path;
        }

        // Build a browser-friendly URL for "Open original" (prefer .html over .md)
        let originalUrl = fullUrl;
        if (originalUrl.endsWith('.md')) {
            originalUrl = originalUrl.slice(0, -3) + '.html';
        }

        const domain = authorUrl.replace('https://', '').replace('http://', '').replace(/\/$/, '');
        metaEl.innerHTML = `
            <span class="remote-post-author">${this.escapeHtml(domain)}</span>
            <a href="${this.escapeHtml(originalUrl)}" target="_blank" class="remote-post-link">Open original &#x2197;</a>
        `;

        try {
            const result = await this.api('GET', '/api/remote/post?url=' + encodeURIComponent(fullUrl));
            bodyEl.innerHTML = `<div class="parchment-preview">${result.content}</div>`;
        } catch (err) {
            bodyEl.innerHTML = `<div class="empty-state"><h3>Failed to load post</h3><p>${this.escapeHtml(err.message)}</p><p><a href="${this.escapeHtml(fullUrl)}" target="_blank">Open in new tab</a></p></div>`;
        }
    },

    closeRemotePost() {
        const panel = document.getElementById('remote-post-panel');
        if (panel) panel.classList.add('hidden');
    },


    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    },

    // Utility: convert .md URL to .html for browser-facing links
    mdToHtmlUrl(url) {
        if (url && url.endsWith('.md')) {
            return url.slice(0, -3) + '.html';
        }
        return url || '';
    },

    // Utility: format date
    formatDate(isoString) {
        if (!isoString) return '';
        const date = new Date(isoString);
        return date.toLocaleDateString('en-US', {
            month: 'short',
            day: 'numeric',
            year: 'numeric'
        });
    },

    // Utility: format time in local timezone
    formatTime(isoString) {
        if (!isoString) return '';
        const date = new Date(isoString);
        return date.toLocaleTimeString('en-US', {
            hour: 'numeric',
            minute: '2-digit',
            hour12: true
        });
    },

    // --- Notification Methods ---

    notificationState: { unreadCount: 0, pollTimer: null, showAll: false, offset: 0, hasMore: true },

    updateDomainDisplay(baseUrl) {
        const el = document.getElementById('domain-display');
        if (!el) return;
        if (!baseUrl) { el.textContent = ''; return; }
        const display = baseUrl.replace(/^https?:\/\//, '');
        el.innerHTML = `<a href="${this.escapeHtml(baseUrl)}" target="_blank" rel="noopener">${this.escapeHtml(display)}</a>`;
    },

    initNotifications() {
        // Notification count is now updated via SSE (no polling needed).
        // Just ensure the dot reflects current state.
        this._updateNotificationDot();
    },

    async fetchNotificationCount() {
        try {
            const resp = await this.api('GET', '/api/notifications/count');
            this.notificationState.unreadCount = resp.unread || 0;
            this._updateNotificationDot();
        } catch (e) {
            // Silently fail — notifications are non-critical
        }
    },

    // Initialize Server-Sent Events for real-time count updates.
    // Replaces notification polling (30s) and feed polling (60s) with
    // push-based updates from the unified sync loop.
    initSSE() {
        if (this._eventSource) {
            this._eventSource.close();
        }

        const sseUrl = '/api/sse';

        this._eventSource = new EventSource(sseUrl);

        this._eventSource.addEventListener('counts', (e) => {
            try {
                const counts = JSON.parse(e.data);
                this._applyCountsFromSSE(counts);
            } catch (err) {
                console.error('SSE counts parse error:', err);
            }
        });

        this._eventSource.onerror = () => {
            // Reconnect with backoff. EventSource auto-reconnects,
            // but if it fails repeatedly we close and retry manually.
            if (this._eventSource && this._eventSource.readyState === EventSource.CLOSED) {
                setTimeout(() => this.initSSE(), 5000);
            }
        };

        // Polling fallback: refresh counts every 30s regardless of SSE.
        // Catches local state changes (CLI edits, other tabs) that don't
        // go through the DS stream, and covers SSE connection gaps.
        this._startCountsPolling();
    },

    _startCountsPolling() {
        if (this._countsPollTimer) {
            clearInterval(this._countsPollTimer);
        }
        this._countsPollTimer = setInterval(() => {
            this.loadAllCounts();
        }, 30000);
    },

    async toggleNotifications() {
        const panel = document.getElementById('notification-panel');
        if (!panel) return;

        if (!panel.classList.contains('hidden')) {
            this.closeNotifications();
            return;
        }

        panel.classList.remove('hidden');
        this.notificationState.offset = 0;
        this.notificationState.hasMore = true;
        this.notificationState.showAll = false;
        document.getElementById('notification-toggle-all').textContent = 'Show All';
        document.getElementById('notification-toggle-all').classList.remove('active');

        // Force an immediate sync + recount when opening the panel
        await this.fetchNotificationCount();
        await this.loadNotifications(false);
    },

    closeNotifications() {
        const panel = document.getElementById('notification-panel');
        if (panel) panel.classList.add('hidden');
    },

    async toggleAllNotifications() {
        this.notificationState.showAll = !this.notificationState.showAll;
        this.notificationState.offset = 0;
        this.notificationState.hasMore = true;

        const btn = document.getElementById('notification-toggle-all');
        if (this.notificationState.showAll) {
            btn.textContent = 'Unread Only';
            btn.classList.add('active');
        } else {
            btn.textContent = 'Show All';
            btn.classList.remove('active');
        }

        await this.loadNotifications(false);
    },

    async loadNotifications(append) {
        const list = document.getElementById('notification-list');
        if (!list) return;

        if (!append) {
            list.innerHTML = '<div class="notification-loading">Loading...</div>';
            this.notificationState.offset = 0;
        }

        const includeRead = this.notificationState.showAll;
        const limit = 20;
        const offset = this.notificationState.offset;

        try {
            const resp = await this.api('GET',
                `/api/notifications?offset=${offset}&limit=${limit}&include_read=${includeRead}`);
            const items = resp.notifications || [];

            if (!append) {
                list.innerHTML = '';
            } else {
                // Remove loading indicator
                const loader = list.querySelector('.notification-loading');
                if (loader) loader.remove();
            }

            if (items.length === 0 && offset === 0) {
                list.innerHTML = '<div class="notification-empty">No notifications</div>';
                this.notificationState.hasMore = false;
                return;
            }

            items.forEach(n => {
                list.appendChild(this.renderNotification(n));
            });

            this.notificationState.offset += items.length;
            this.notificationState.hasMore = this.notificationState.offset < resp.total;

            // Mark displayed unread notifications as read
            const unreadIds = items.filter(n => !n.read_at).map(n => n.id);
            if (unreadIds.length > 0) {
                this.api('POST', '/api/notifications/read', { ids: unreadIds })
                    .then(() => this.fetchNotificationCount())
                    .catch(() => {});
            }

            // Set up infinite scroll
            if (this.notificationState.hasMore) {
                list.onscroll = () => {
                    if (list.scrollTop + list.clientHeight >= list.scrollHeight - 50) {
                        if (this.notificationState.hasMore) {
                            list.onscroll = null; // Prevent duplicate triggers
                            list.insertAdjacentHTML('beforeend',
                                '<div class="notification-loading">Loading more...</div>');
                            this.loadNotifications(true);
                        }
                    }
                };
            }
        } catch (e) {
            if (!append) {
                list.innerHTML = '<div class="notification-empty">Failed to load notifications</div>';
            }
        }
    },

    renderNotification(n) {
        const div = document.createElement('div');
        div.className = 'notification-item' + (n.read_at ? '' : ' unread');

        const icon = n.icon || '\u2139';
        const ruleId = n.rule_id || '';

        div.innerHTML = `
            <div class="notification-type-badge">${icon}</div>
            <div class="notification-body">
                <div class="notification-message">${this.escapeHtml(n.message || '')}</div>
                <div class="notification-meta">${this.formatRelativeTime(n.created_at)}</div>
            </div>
        `;

        // Resolve click target from the link field (set by notification rules).
        // External URLs (posts from followed authors) open in a new tab.
        // Internal paths (/_/...) navigate within the SPA.
        // Legacy hash links (/_/#...) are also supported for old notifications.
        let link = n.link || '';

        // For post URLs, convert .md to .html for browser viewing
        if (link.startsWith('http') && link.endsWith('.md')) {
            link = link.replace(/\.md$/, '.html');
        }

        if (link) {
            div.style.cursor = 'pointer';
            div.onclick = () => {
                this.closeNotifications();
                if (link.startsWith('http')) {
                    window.open(link, '_blank', 'noopener');
                } else if (link.startsWith('/_/#')) {
                    // Legacy hash-style notification links (`/_/#blessings`,
                    // `/_/#feed`, etc.) from before chunk A. The paths they
                    // pointed at are all retired; route to bare `/_/` and
                    // let the unknown-path fallback land the user on the
                    // default filter. Friends/family alpha audience absorbs
                    // the broken-link cost per the hard-cutover decision.
                    this.navigateTo('/');
                } else {
                    // Internal SPA path — strip basePath prefix and navigate
                    const path = link.replace(/^\/_/, '');
                    // Handle query params (e.g. ?filter=blessed)
                    const [pathname, query] = path.split('?');
                    if (query) {
                        const params = new URLSearchParams(query);
                        const filter = params.get('filter');
                        if (filter) this._commentsPublishedFilter = filter;
                    }
                    this.navigateTo(pathname);
                }
            };
        }

        return div;
    },

    // --- Setup Wizard ---

    async openSetupWizard() {
        // Auto-detect current step (2-step: Deploy → Register)
        try {
            const result = await this.api('GET', '/api/site/deploy-check');
            if (!result.deployed) {
                this.setupWizardStep = 0; // Deploy
            } else if (!this.siteRegistered) {
                this.setupWizardStep = 1; // Register
            } else {
                return; // All done, don't open
            }
        } catch {
            this.setupWizardStep = 0; // Default to deploy step on error
        }

        this.renderSetupWizard();
        document.getElementById('setup-wizard-panel').classList.remove('hidden');
    },

    renderSetupWizard() {
        const stepsEl = document.getElementById('setup-wizard-steps');
        const contentEl = document.getElementById('setup-wizard-content');
        const actionBtn = document.getElementById('setup-wizard-action-btn');
        const steps = ['Deploy', 'Register'];

        // Render step indicators
        stepsEl.innerHTML = steps.map((label, i) => {
            let cls = 'setup-step';
            if (i < this.setupWizardStep) cls += ' completed';
            else if (i === this.setupWizardStep) cls += ' active';
            else cls += ' pending';
            const icon = i < this.setupWizardStep ? '&#10003;' : (i + 1);
            return `<div class="${cls}"><span class="step-dot">${icon}</span><span class="step-label">${label}</span></div>` +
                   (i < steps.length - 1 ? '<div class="step-line"></div>' : '');
        }).join('');

        // Render step content
        if (this.setupWizardStep === 0) {
            const domain = this.siteBaseUrl ? new URL(this.siteBaseUrl).hostname : 'yourdomain.com';
            contentEl.innerHTML = `
                <div class="wizard-section">
                    <p>Push your site files so that <strong>${this.escapeHtml(domain)}</strong> serves them publicly. Polis works with any static host.</p>
                    <div class="deploy-example">
                        <div class="deploy-example-header">Example: Git-based deploy</div>
                        <pre class="setup-code"><span class="code-comment"># From your site directory</span>
git add -A .
git commit -m "initial polis site"
git push</pre>
                    </div>
                    <p class="hint">Works with GitHub Pages, Netlify, Vercel, Cloudflare Pages, or any host that serves static files. The key file is <code>.well-known/polis</code> &mdash; once that's reachable, you're live.</p>
                    <div id="deploy-status" class="deploy-status">
                        <span class="deploy-spinner"></span>
                        <span id="deploy-status-text">Checking if your site is live...</span>
                    </div>
                </div>`;
            actionBtn.textContent = 'Next';
            actionBtn.disabled = true;
            this.startDeployPolling();
        } else if (this.setupWizardStep === 1) {
            const domain = this.siteBaseUrl ? new URL(this.siteBaseUrl).hostname : '';
            contentEl.innerHTML = `
                <div class="wizard-section">
                    <p>Register <strong>${this.escapeHtml(domain)}</strong> with the discovery network so others can find and interact with your content.</p>
                </div>`;
            actionBtn.textContent = 'Register';
        }
    },

    startDeployPolling() {
        this.stopDeployPolling();
        const poll = async () => {
            try {
                const result = await this.api('GET', '/api/site/deploy-check');
                const statusText = document.getElementById('deploy-status-text');
                if (result.deployed) {
                    this.stopDeployPolling();
                    if (statusText) statusText.textContent = 'Your site is live!';
                    const statusEl = document.getElementById('deploy-status');
                    if (statusEl) statusEl.classList.add('deployed');
                    const actionBtn = document.getElementById('setup-wizard-action-btn');
                    if (actionBtn) {
                        actionBtn.disabled = false;
                        actionBtn.textContent = 'Next';
                    }
                } else if (statusText) {
                    statusText.textContent = 'Waiting for your site to go live...';
                }
            } catch {
                // Silently continue polling
            }
        };
        poll();
        this.setupWizardDeployTimer = setInterval(poll, 5000);
    },

    stopDeployPolling() {
        if (this.setupWizardDeployTimer) {
            clearInterval(this.setupWizardDeployTimer);
            this.setupWizardDeployTimer = null;
        }
    },

    async setupWizardAction() {
        const actionBtn = document.getElementById('setup-wizard-action-btn');
        if (this.setupWizardStep === 0) {
            // Deploy step → advance to Register
            this.stopDeployPolling();
            this.setupWizardStep = 1;
            this.renderSetupWizard();
        } else if (this.setupWizardStep === 1) {
            // Register
            if (actionBtn) {
                actionBtn.disabled = true;
                actionBtn.textContent = 'Registering...';
            }
            try {
                await this.api('POST', '/api/site/register');
                this.siteRegistered = true;
                this.setupWizardDismissed = true;
                // Dismiss the wizard
                document.getElementById('setup-wizard-panel').classList.add('hidden');
                document.getElementById('setup-banner').classList.add('hidden');
                this.showToast('Site registered with discovery network!', 'success');
                // Persist dismissal
                try { await this.api('POST', '/api/site/setup-wizard-dismiss'); } catch {}
            } catch (err) {
                this.showToast('Registration failed: ' + err.message, 'error');
                if (actionBtn) {
                    actionBtn.disabled = false;
                    actionBtn.textContent = 'Register';
                }
            }
        }
    },

    dismissSetupWizard() {
        this.stopDeployPolling();
        document.getElementById('setup-wizard-panel').classList.add('hidden');
        // Persist dismissal
        this.api('POST', '/api/site/setup-wizard-dismiss').catch(() => {});
        this.setupWizardDismissed = true;
        // Show the banner on dashboard if not registered
        if (!this.siteRegistered) {
            document.getElementById('setup-banner').classList.remove('hidden');
        }
    },

    dismissSetupBanner() {
        document.getElementById('setup-banner').classList.add('hidden');
        // Also persist wizard dismissal
        this.api('POST', '/api/site/setup-wizard-dismiss').catch(() => {});
        this.setupWizardDismissed = true;
    },

    async checkSetupBanner() {
        // In hosted mode, setup is handled by the hosting service
        if (this.isHosted) return;
        // Check if wizard dismissed and site registered
        try {
            const settings = await this.api('GET', '/api/settings');
            this.setupWizardDismissed = settings.setup_wizard_dismissed || false;

            // Sync theme from server if not already set locally
            if (!localStorage.getItem('polis-webapp-theme') && settings.webapp_theme) {
                this._initTheme(settings.webapp_theme);
            }

            // Check registration status
            try {
                const regStatus = await this.api('GET', '/api/site/registration-status');
                this.siteRegistered = regStatus.is_registered || false;
            } catch {
                this.siteRegistered = false;
            }

            // Show banner if not dismissed AND not registered
            if (!this.setupWizardDismissed && !this.siteRegistered) {
                // Auto-open wizard on first load after init
                this.openSetupWizard();
            } else if (this.setupWizardDismissed && !this.siteRegistered) {
                document.getElementById('setup-banner').classList.remove('hidden');
            }
        } catch {
            // Can't check, don't show banner
        }
    },

    escapeHtml(str) {
        if (!str) return '';
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    },

    formatRelativeTime(isoString) {
        if (!isoString) return '';
        const date = new Date(isoString);
        const now = new Date();
        const diffMs = now - date;
        const diffMin = Math.floor(diffMs / 60000);
        const diffHour = Math.floor(diffMs / 3600000);
        const diffDay = Math.floor(diffMs / 86400000);

        if (diffMin < 1) return 'just now';
        if (diffMin < 60) return `${diffMin} min ago`;
        if (diffHour < 24) return `${diffHour} hour${diffHour > 1 ? 's' : ''} ago`;
        if (diffDay < 2) return 'yesterday';
        if (diffDay < 7) return `${diffDay} days ago`;
        return this.formatDate(isoString);
    },

    // Fetch remote avatar configs for a list of domains, updating cache and re-rendering feed items
    async _fetchRemoteAvatars(domains) {
        const toFetch = domains.filter(d => !(d in this._remoteAvatarCache) && !this._remoteAvatarFetching[d]);
        if (toFetch.length === 0) return;

        // Mark as in-flight
        toFetch.forEach(d => { this._remoteAvatarFetching[d] = true; });

        // Fetch in parallel
        const results = await Promise.allSettled(toFetch.map(async (domain) => {
            try {
                const data = await this.api('GET', `/api/remote/avatar?domain=${encodeURIComponent(domain)}`);
                this._remoteAvatarCache[domain] = {
                    avatar: data.avatar || null,
                    author_name: data.author_name || '',
                };
            } catch {
                this._remoteAvatarCache[domain] = { avatar: null, author_name: '' };
            } finally {
                delete this._remoteAvatarFetching[domain];
            }
        }));

        // Re-render feed items that now have avatar data
        const updated = toFetch.filter(d => this._remoteAvatarCache[d]?.avatar);
        if (updated.length > 0) {
            updated.forEach(domain => {
                document.querySelectorAll('.feed-item').forEach(el => {
                    const domainEl = el.querySelector('.author-domain');
                    if (!domainEl) return;
                    const text = domainEl.textContent.replace(/^·\s*/, '').trim();
                    if (text !== domain) return;
                    const avatarEl = el.querySelector('.author-avatar');
                    if (!avatarEl) return;
                    const cached = this._remoteAvatarCache[domain];
                    avatarEl.setAttribute('style', this._buildAvatarStyle(cached.avatar));
                    avatarEl.textContent = '';
                    if (cached.author_name) {
                        const nameEl = el.querySelector('.author-name');
                        if (nameEl) nameEl.textContent = cached.author_name;
                    }
                });
            });
        }
    },

    // Extract unique remote domains from feed groups and fetch their avatars
    _fetchRemoteAvatarsForGroups(groups) {
        const myDomain = this.siteBaseUrl ? (() => { try { return new URL(this.siteBaseUrl).hostname; } catch(e) { return ''; } })() : '';
        const domains = [...new Set(groups.map(g => g.post_domain).filter(d => d && d !== myDomain))];
        if (domains.length > 0) this._fetchRemoteAvatars(domains);
    },

    // Generate avatar initials + deterministic HSL color from domain
    domainToAvatar(domain) {
        if (!domain) return { initials: '?', color: '#888' };
        const parts = domain.replace(/^www\./, '').split('.');
        const name = parts[0] || '';
        const initials = name.slice(0, 2).toUpperCase();
        // Deterministic hue from domain hash
        let hash = 0;
        for (let i = 0; i < domain.length; i++) {
            hash = domain.charCodeAt(i) + ((hash << 5) - hash);
        }
        const hue = ((hash % 360) + 360) % 360;
        const color = `hsl(${hue}, 35%, 55%)`;
        return { initials, color };
    },

    // Avatar color utilities
    _hexToRgb(hex) {
        const n = parseInt(hex.slice(1), 16);
        return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
    },
    _relativeLuminance(r, g, b) {
        const [rs, gs, bs] = [r, g, b].map(c => {
            c = c / 255;
            return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
        });
        return 0.2126 * rs + 0.7152 * gs + 0.0722 * bs;
    },
    _contrastRatio(hex1, hex2) {
        const c1 = this._hexToRgb(hex1), c2 = this._hexToRgb(hex2);
        const l1 = this._relativeLuminance(c1.r, c1.g, c1.b);
        const l2 = this._relativeLuminance(c2.r, c2.g, c2.b);
        const lighter = Math.max(l1, l2), darker = Math.min(l1, l2);
        return (lighter + 0.05) / (darker + 0.05);
    },
    _hslToHex(h, s, l) {
        s /= 100; l /= 100;
        const a = s * Math.min(l, 1 - l);
        const f = n => { const k = (n + h / 30) % 12; return l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1)); };
        const toHex = x => Math.round(x * 255).toString(16).padStart(2, '0');
        return '#' + toHex(f(0)) + toHex(f(8)) + toHex(f(4));
    },
    _randomHslToHex(sMin, sMax, lMin, lMax) {
        const h = Math.floor(Math.random() * 360);
        const s = sMin + Math.random() * (sMax - sMin);
        const l = lMin + Math.random() * (lMax - lMin);
        return this._hslToHex(h, s, l);
    },
    _shiftLightness(hex, amount) {
        const { r, g, b } = this._hexToRgb(hex);
        const max = Math.max(r, g, b) / 255, min = Math.min(r, g, b) / 255;
        let h, s, l = (max + min) / 2;
        if (max === min) { h = s = 0; } else {
            const d = max - min;
            s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
            if (max === r / 255) h = ((g / 255 - b / 255) / d + (g < b ? 6 : 0)) * 60;
            else if (max === g / 255) h = ((b / 255 - r / 255) / d + 2) * 60;
            else h = ((r / 255 - g / 255) / d + 4) * 60;
        }
        const newL = Math.max(0, Math.min(100, l * 100 + amount));
        return this._hslToHex(h, s * 100, newL);
    },

    // Avatar SVG patterns
    _avatarPatterns: {
        none: () => '',
        rings: (c) => `<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='14' cy='14' r='10' fill='none' stroke='${c}' stroke-width='1.5'/><circle cx='14' cy='14' r='5' fill='none' stroke='${c}' stroke-width='1'/></svg>`,
        cross: (c) => `<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='4' y1='4' x2='24' y2='24' stroke='${c}' stroke-width='1.5'/><line x1='24' y1='4' x2='4' y2='24' stroke='${c}' stroke-width='1.5'/></svg>`,
        grid: (c) => `<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='9' y1='0' x2='9' y2='28' stroke='${c}' stroke-width='0.8'/><line x1='19' y1='0' x2='19' y2='28' stroke='${c}' stroke-width='0.8'/><line x1='0' y1='9' x2='28' y2='9' stroke='${c}' stroke-width='0.8'/><line x1='0' y1='19' x2='28' y2='19' stroke='${c}' stroke-width='0.8'/></svg>`,
        dots: (c) => `<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='7' cy='7' r='2' fill='${c}'/><circle cx='21' cy='7' r='2' fill='${c}'/><circle cx='14' cy='14' r='2' fill='${c}'/><circle cx='7' cy='21' r='2' fill='${c}'/><circle cx='21' cy='21' r='2' fill='${c}'/></svg>`,
        stripes: (c) => `<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='-2' y1='6' x2='6' y2='-2' stroke='${c}' stroke-width='1.5'/><line x1='5' y1='13' x2='13' y2='5' stroke='${c}' stroke-width='1.5'/><line x1='12' y1='20' x2='20' y2='12' stroke='${c}' stroke-width='1.5'/><line x1='19' y1='27' x2='27' y2='19' stroke='${c}' stroke-width='1.5'/><line x1='26' y1='34' x2='34' y2='26' stroke='${c}' stroke-width='1.5'/></svg>`,
        diamond: (c) => `<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><polygon points='14,4 24,14 14,24 4,14' fill='none' stroke='${c}' stroke-width='1.5'/></svg>`,
        halves: (c) => `<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><rect x='0' y='14' width='28' height='14' fill='${c}' opacity='0.4'/></svg>`,
    },
    _avatarSvgPattern(name, color) {
        const fn = this._avatarPatterns[name];
        if (!fn) return '';
        const svg = fn(color);
        if (!svg) return '';
        return `url(data:image/svg+xml;base64,${btoa(svg)})`;
    },

    // Build inline CSS for a custom avatar config
    _buildAvatarStyle(config) {
        if (!config) return '';
        let style = `background-color: ${config.bg}; color: ${config.fg};`;
        if (config.border && config.border_w > 0) {
            style += ` border: ${config.border_w}px solid ${config.border};`;
        }
        if (config.pattern && config.pattern !== 'none' && config.pattern_color) {
            const uri = this._avatarSvgPattern(config.pattern, config.pattern_color);
            if (uri) style += ` background-image: ${uri}; background-size: cover;`;
        }
        return style;
    },

    // Randomize avatar config
    randomizeAvatar() {
        const patterns = ['none', 'rings', 'cross', 'grid', 'dots', 'stripes', 'diamond', 'halves'];
        let bg, fg;
        for (let i = 0; i < 10; i++) {
            bg = this._randomHslToHex(25, 60, 25, 65);
            fg = '#ffffff';
            if (this._contrastRatio(bg, fg) >= 4.5) break;
            fg = '#1a1a2e';
            if (this._contrastRatio(bg, fg) >= 4.5) break;
            fg = '#ffffff';
        }
        const border = this._randomHslToHex(20, 50, 40, 70);
        const borderW = Math.floor(Math.random() * 4);
        const pattern = patterns[Math.floor(Math.random() * patterns.length)];
        const shift = (Math.random() > 0.5 ? 1 : -1) * (15 + Math.random() * 10);
        const patternColor = this._shiftLightness(bg, shift);

        this._pendingAvatar = { bg, fg, border, border_w: borderW, pattern, pattern_color: patternColor };

        // Update preview if visible (no initials for custom avatars)
        const preview = document.getElementById('avatar-preview');
        if (preview) {
            preview.setAttribute('style', this._buildAvatarStyle(this._pendingAvatar));
            preview.textContent = '';
        }
        // Enable save button
        const saveBtn = document.getElementById('avatar-save-btn');
        if (saveBtn) saveBtn.disabled = false;
    },

    async saveAvatar() {
        const config = this._pendingAvatar;
        if (!config) return;
        try {
            const result = await this.api('POST', '/api/settings/avatar', { avatar: config });
            this.avatarConfig = result.avatar;
            this._pendingAvatar = null;
            this.showToast('Avatar saved', 'success');
        } catch (err) {
            this.showToast('Failed to save avatar: ' + err.message, 'error');
        }
    },

    async resetAvatar() {
        try {
            await this.api('POST', '/api/settings/avatar', { avatar: null });
            this.avatarConfig = null;
            this._pendingAvatar = null;
            this.showToast('Avatar reset', 'success');
            // Update preview to deterministic
            const preview = document.getElementById('avatar-preview');
            if (preview) {
                const domain = this.siteBaseUrl ? (() => { try { return new URL(this.siteBaseUrl).hostname; } catch(e) { return ''; } })() : '';
                const det = this.domainToAvatar(domain || 'me');
                preview.setAttribute('style', `background: ${det.color};`);
                preview.textContent = det.initials;
            }
            const saveBtn = document.getElementById('avatar-save-btn');
            if (saveBtn) saveBtn.disabled = true;
        } catch (err) {
            this.showToast('Failed to reset avatar: ' + err.message, 'error');
        }
    },

    // Strip markdown and truncate at word boundary
    truncateExcerpt(markdown, maxLen = 160) {
        if (!markdown) return '';
        // Strip common markdown syntax
        let text = markdown
            .replace(/^#{1,6}\s+/gm, '')           // headings
            .replace(/\*\*(.+?)\*\*/g, '$1')       // bold
            .replace(/\*(.+?)\*/g, '$1')            // italic
            .replace(/`(.+?)`/g, '$1')              // inline code
            .replace(/\[(.+?)\]\(.+?\)/g, '$1')    // links
            .replace(/^[-*+]\s+/gm, '')             // list items
            .replace(/^>\s+/gm, '')                 // blockquotes
            .replace(/---+/g, '')                   // horizontal rules
            .replace(/\n+/g, ' ')                   // newlines to spaces
            .trim();
        if (text.length <= maxLen) return text;
        const truncated = text.slice(0, maxLen);
        const lastSpace = truncated.lastIndexOf(' ');
        return (lastSpace > maxLen * 0.5 ? truncated.slice(0, lastSpace) : truncated) + '\u2026';
    },

    // ── Widget Token Auto-Issuance ──────────────────────────────────

    // Ensure a widget token exists for this user (hosted mode only).
    // Called on dashboard init so the token is ready for the connect flow.
    async ensureWidgetToken() {
        try {
            const resp = await fetch('/api/widget/token', { credentials: 'same-origin' });
            if (resp.ok) {
                const data = await resp.json();
                if (data.token) {
                    // Set cross-tenant cookie so the widget recognizes us on other tenants
                    document.cookie = 'polis_widget_token=' + encodeURIComponent(data.token) +
                        '; domain=.polis.pub; path=/; max-age=31536000; SameSite=Lax; Secure';
                }
            }
        } catch (_) {
            // Non-fatal: token will be created on-demand during connect
        }
    },

    // ── Intent Params ────────────────────────────────────────────────

    // Parse URL query params into an intent object, then clean the URL.
    parseIntentParams() {
        const params = new URLSearchParams(window.location.search);

        // widget_connect takes priority — redirect immediately
        if (params.get('widget_connect') === 'true') {
            this.cleanIntentURL();
            return { type: 'widget_connect', returnUrl: params.get('return') || '' };
        }

        const intent = params.get('intent');
        if (!intent) return null;

        this.cleanIntentURL();

        switch (intent) {
            case 'comment':
                return {
                    type: 'comment',
                    target: params.get('target') || '',
                    text: params.get('text') || '',
                };
            case 'follow':
                return {
                    type: 'follow',
                    target: params.get('target') || '',
                };
            default:
                return null;
        }
    },

    // Remove intent/query params from the URL without a page reload,
    // preserving the deep-link path.
    cleanIntentURL() {
        const url = new URL(window.location);
        url.searchParams.delete('intent');
        url.searchParams.delete('target');
        url.searchParams.delete('text');
        url.searchParams.delete('widget_connect');
        url.searchParams.delete('return');
        const clean = url.searchParams.toString() ? url.pathname + '?' + url.searchParams : url.pathname;
        window.history.replaceState({}, '', clean);
    },

    // Handle widget_connect: redirect to API endpoint that issues token
    // and redirects back to the return URL.
    handleWidgetConnect(returnUrl) {
        const connectURL = '/api/widget/connect' +
            (returnUrl ? '?return=' + encodeURIComponent(returnUrl) : '');
        window.location.href = connectURL;
    },

    // Process a parsed intent after the dashboard is fully loaded.
    async processIntent(intent) {
        switch (intent.type) {
            case 'comment':
                await this.processCommentIntent(intent);
                break;
            case 'follow':
                await this.processFollowIntent(intent);
                break;
        }
    },

    // intent=comment: open the comment composer pre-filled.
    //
    // The v3 #comment-screen this used to mount into is retired in
    // chunk A. The v4 inline comment editor card (owner-extras.js
    // mountCommentEditor) is the replacement, but mounting it
    // pre-filled from an external intent needs additional plumbing
    // (the inline editor wants to anchor against a stream entry).
    // For now, land the user on the default filter + flash a toast
    // suggesting they navigate to the post and reply manually.
    // Wider intent-flow rewrite tracked separately.
    async processCommentIntent(intent) {
        if (!intent.target) return;
        this.showToast('Open the post you want to reply to and click the comment dot.', 'info', 6000);
    },

    // intent=follow: auto-follow the author and show result.
    async processFollowIntent(intent) {
        if (!intent.target) return;

        // Land the user on the profiles surface (v4 stream + people
        // preset) so the just-followed author shows up in context.
        // Routes through the PQL URL form — no special-case path.
        this.navigateTo('/pql/all+profiles+from+my+network+by+name');

        // Normalize: ensure https:// prefix
        let authorURL = intent.target;
        if (!authorURL.startsWith('https://')) {
            authorURL = 'https://' + authorURL;
        }

        try {
            const result = await this.api('POST', '/api/following', { url: authorURL });
            const domain = authorURL.replace('https://', '').replace(/\/$/, '');
            const alreadyFollowed = result.data && result.data.already_followed;

            const followActions = [
                    { label: 'See your feed', primary: true, action: () => { this.dismissIntentResult(); this.setSidebarMode('social'); this.setActiveView('feed'); } },
                    { label: 'Visit ' + domain, action: () => window.open(authorURL, '_blank') },
                ];
            const postLabel = this.counts.posts === 0 ? 'Write your first post' : 'Write a post';
            followActions.push({ label: postLabel, action: () => { this.dismissIntentResult(); this.toggleCompose(); } });

            this.showIntentResult({
                icon: '&#10003;',
                title: alreadyFollowed ? 'Already following ' + domain : 'Following ' + domain,
                subtitle: alreadyFollowed
                    ? 'You were already following this author.'
                    : 'Their posts will appear in your feed.',
                actions: followActions,
            });

            await this.loadAllCounts();
        } catch (err) {
            this.showToast('Failed to follow: ' + err.message, 'error');
        }
    },

    // Show a full-screen intent result overlay with CTAs.
    showIntentResult(opts) {
        // Remove any existing result overlay
        this.dismissIntentResult();

        const overlay = document.createElement('div');
        overlay.id = 'intent-result-overlay';
        overlay.className = 'intent-result-overlay';

        const actionsHTML = (opts.actions || []).map((a, i) => {
            const cls = a.primary ? 'primary' : 'secondary';
            return `<button class="${cls}" data-intent-action="${i}">${a.label}</button>`;
        }).join('');

        overlay.innerHTML = `
            <div class="intent-result-card">
                <div class="intent-result-icon">${opts.icon || '&#10003;'}</div>
                <h2 class="intent-result-title">${this.escapeHtml(opts.title)}</h2>
                <p class="intent-result-subtitle">${this.escapeHtml(opts.subtitle || '')}</p>
                <div class="intent-result-actions">${actionsHTML}</div>
            </div>
        `;

        // Bind action buttons
        overlay.querySelectorAll('[data-intent-action]').forEach(btn => {
            const idx = parseInt(btn.dataset.intentAction);
            const action = opts.actions[idx];
            if (action && action.action) {
                btn.addEventListener('click', action.action);
            }
        });

        document.getElementById('app').appendChild(overlay);
    },

    dismissIntentResult() {
        const existing = document.getElementById('intent-result-overlay');
        if (existing) existing.remove();
    },

    // Show intent-aware CTAs after comment submission (when triggered by intent).
    showCommentIntentResult(commentUrl, target) {
        const targetDomain = target
            .replace('https://', '')
            .replace('http://', '')
            .replace(/\/.*$/, '');

        this.showIntentResult({
            icon: '&#10003;',
            title: 'Comment submitted to ' + targetDomain,
            subtitle: 'Your comment has been delivered to the author.',
            actions: [
                { label: 'Follow ' + targetDomain, primary: true, action: () => {
                    this.dismissIntentResult();
                    const authorURL = 'https://' + targetDomain;
                    this.api('POST', '/api/following', { url: authorURL })
                        .then(() => {
                            this.showToast('Now following ' + targetDomain, 'success');
                            this.loadAllCounts();
                        })
                        .catch(err => this.showToast('Failed to follow: ' + err.message, 'error'));
                    this.setSidebarMode('social');
                    this.setActiveView('following');
                }},
                { label: 'Back to post', action: () => { window.open(target.replace(/\.md$/, '.html'), '_blank'); this.dismissIntentResult(); } },
                { label: this.counts.posts === 0 ? 'Write your first post' : 'Write a post', action: () => { this.dismissIntentResult(); this.toggleCompose(); } },
            ],
        });
    },

    toggleMobileNav() {
        const sidebar = document.querySelector('.sidebar');
        if (!sidebar) return;
        const isOpen = sidebar.classList.toggle('mobile-open');
        let backdrop = document.querySelector('.sidebar-backdrop');
        if (isOpen) {
            if (!backdrop) {
                backdrop = document.createElement('div');
                backdrop.className = 'sidebar-backdrop';
                backdrop.addEventListener('click', () => this.toggleMobileNav());
                sidebar.parentElement.appendChild(backdrop);
            }
            backdrop.classList.add('visible');
        } else if (backdrop) {
            backdrop.classList.remove('visible');
        }
    },

    closeMobileNav() {
        const sidebar = document.querySelector('.sidebar');
        if (sidebar && sidebar.classList.contains('mobile-open')) {
            sidebar.classList.remove('mobile-open');
            const backdrop = document.querySelector('.sidebar-backdrop');
            if (backdrop) backdrop.classList.remove('visible');
        }
    },

    // ── DM section retired (chunk C: v3-DM port to v4 stream surface) ──
    //
    // The renderDMConversationList / renderDMThread / renderDMNewConversation
    // functions + their helpers (_dmAvatarHtml, _dmFormatDate, _dmKeyDown,
    // sendDM, deleteDMConversation, retryDMMessage, _dmStartConversation,
    // _dmFilterRecipients) and all related state (_dmThreadId,
    // _dmThreadPeerDomain, _dmThreadPeerUrl, _dmRecipients,
    // _dmPendingRecipient) were deleted.
    //
    // Replacement: the v4 stream surface handles DMs via type=dms in PQL.
    // Inbox = scope=my-mutuals (renderDM in stream.js); thread = scope=
    // @<handle> (renderDMMessage in stream.js, server route in
    // handleStreamItemsDMThread). Composer modal lives in owner-extras.js
    // (openDMComposer + buildDMComposerCard). The /api/dm/send,
    // /api/dm/conversations, and /api/dm/recipients endpoints are reused
    // unchanged; only the SPA-side surfaces changed.


    // ── Tag Methods ──

    _tagVocabulary: null,
    _tagVocabularyPromise: null,

    async _fetchTagVocabulary() {
        if (this._tagVocabulary) return this._tagVocabulary;
        if (this._tagVocabularyPromise) return this._tagVocabularyPromise;
        this._tagVocabularyPromise = fetch('/api/tags')
            .then(r => r.json())
            .then(data => {
                this._tagVocabulary = (data.tags || []).map(t => t.tag);
                this._tagVocabularyPromise = null;
                return this._tagVocabulary;
            })
            .catch(() => {
                this._tagVocabularyPromise = null;
                return [];
            });
        return this._tagVocabularyPromise;
    },

    async showTagInput(btn, targetURI) {
        // Remove any existing tag input
        this._removeTagInput();

        const vocab = await this._fetchTagVocabulary();

        const container = document.createElement('div');
        container.className = 'tag-input-inline';
        container.onclick = (e) => e.stopPropagation();

        const input = document.createElement('input');
        input.type = 'text';
        input.placeholder = 'Tag name...';
        input.autocomplete = 'off';
        container.appendChild(input);

        const dropdown = document.createElement('div');
        dropdown.className = 'tag-dropdown';
        container.appendChild(dropdown);

        const renderDropdown = (filter) => {
            const lower = (filter || '').toLowerCase();
            const matches = vocab.filter(t => !lower || t.includes(lower));
            let html = '';
            matches.forEach(t => {
                html += `<div class="tag-dropdown-item" data-tag="${this.escapeHtml(t)}">${this.escapeHtml(t)}</div>`;
            });
            if (lower && !vocab.includes(lower)) {
                html += `<div class="tag-dropdown-item tag-dropdown-create" data-tag="${this.escapeHtml(lower)}">+ Create "${this.escapeHtml(lower)}"</div>`;
            }
            dropdown.innerHTML = html;
            dropdown.style.display = html ? '' : 'none';

            dropdown.querySelectorAll('.tag-dropdown-item').forEach(item => {
                item.onmousedown = (e) => {
                    e.preventDefault();
                    this._applyTag(item.dataset.tag, targetURI);
                    this._removeTagInput();
                };
            });
        };

        input.oninput = () => renderDropdown(input.value);
        input.onkeydown = (e) => {
            if (e.key === 'Enter') {
                e.preventDefault();
                const val = input.value.trim();
                if (val) {
                    this._applyTag(val, targetURI);
                    this._removeTagInput();
                }
            } else if (e.key === 'Escape') {
                this._removeTagInput();
            }
        };
        input.onblur = () => {
            setTimeout(() => this._removeTagInput(), 150);
        };

        // Position relative to the button's parent (item-hover-actions)
        const actions = btn.closest('.item-hover-actions');
        if (actions) {
            actions.appendChild(container);
        } else {
            btn.parentElement.appendChild(container);
        }

        renderDropdown('');
        input.focus();
    },

    async _applyTag(tagName, targetURI) {
        try {
            const resp = await fetch('/api/tags', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ tag: tagName, target_uri: targetURI }),
            });
            const data = await resp.json();
            if (!resp.ok) {
                this.showToast(data.error || 'Failed to apply tag', 'error');
                return;
            }
            // Invalidate cached vocabulary so new tags appear
            this._tagVocabulary = null;
            this.showToast(`Tagged as "${tagName}"`, 'success');
        } catch (e) {
            this.showToast('Failed to apply tag', 'error');
        }
    },

    _removeTagInput() {
        document.querySelectorAll('.tag-input-inline').forEach(el => el.remove());
    },

    // ── Floating Selection Toolbar ──

    _execToolbarCommand(command) {
        const editorId = this._getActiveEditorId() || 'milkdown-post';
        if (!window.MilkdownBridge?.isReady(editorId)) return;
        const pm = document.querySelector(`#${editorId} .ProseMirror`);
        if (command === 'link') {
            // Save selection before opening modal (modal will steal focus)
            const sel = window.getSelection();
            this._linkSelectedText = sel?.toString() || '';
            this._linkSelectionRange = sel?.rangeCount ? sel.getRangeAt(0).cloneRange() : null;
            this._linkEditorId = editorId;
            const modal = document.getElementById('editor-link-modal');
            const input = document.getElementById('editor-link-url');
            modal.classList.remove('hidden');
            input.value = '';
            input.focus();
            return;
        }
        window.MilkdownBridge.runCommand(editorId, command);
        if (pm) pm.focus();
    },

    // ── Focus Mode (v3 editor-screen focus mode) — retired in chunk A
    // along with the editor screen itself. Read-focus mode for posts
    // lives in shapes/v4/stream.js (enterFocusMode); write-focus for
    // the inline editor card is handled by owner-extras. ──

    // ── Link Modal ──

    _cancelLinkModal() {
        document.getElementById('editor-link-modal').classList.add('hidden');
        const pm = document.querySelector('#milkdown-post .ProseMirror');
        if (pm) pm.focus();
    },

    _confirmLinkModal() {
        const url = document.getElementById('editor-link-url').value.trim();
        document.getElementById('editor-link-modal').classList.add('hidden');
        if (!url) return;
        const editorId = this._linkEditorId || 'milkdown-post';
        const pm = document.querySelector(`#${editorId} .ProseMirror`);
        if (!pm) return;
        // Restore the saved selection
        if (this._linkSelectionRange) {
            const sel = window.getSelection();
            sel.removeAllRanges();
            sel.addRange(this._linkSelectionRange);
        }
        // Use Milkdown's toggleLink command with the URL payload
        if (window.MilkdownBridge?.isReady(editorId)) {
            window.MilkdownBridge.runCommand(editorId, 'link', { href: url });
        }
        pm.focus();
    },

    // ── Slash Command Menu ──

    _initSlashMenu() {
        // Document-level delegation: works for all current and future milkdown mounts
        document.addEventListener('input', (e) => {
            const mount = e.target.closest('.milkdown-mount');
            if (!mount) return;
            const sel = window.getSelection();
            if (!sel || !sel.rangeCount) return;
            const node = sel.anchorNode;
            if (!node || node.nodeType !== Node.TEXT_NODE) return;
            const text = node.textContent;
            if (text === '/' && sel.anchorOffset === 1) {
                this._slashActiveEditorId = mount.id;
                this._showSlashMenu(sel);
            } else {
                this._hideSlashMenu();
            }
        });

        document.addEventListener('keydown', (e) => {
            if (!this._slashMenuVisible) return;
            const items = document.querySelectorAll('#editor-slash-menu .slash-item');
            if (e.key === 'ArrowDown') {
                e.preventDefault();
                this._slashMenuIndex = Math.min(this._slashMenuIndex + 1, items.length - 1);
                this._updateSlashSelection(items);
            } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                this._slashMenuIndex = Math.max(this._slashMenuIndex - 1, 0);
                this._updateSlashSelection(items);
            } else if (e.key === 'Enter') {
                e.preventDefault();
                const selected = items[this._slashMenuIndex];
                if (selected) this._selectSlashItem(selected.dataset.type);
            } else if (e.key === 'Escape') {
                this._hideSlashMenu();
            } else if (e.key !== '/') {
                this._hideSlashMenu();
            }
        }, true);

        document.getElementById('editor-slash-menu').addEventListener('mousedown', (e) => {
            e.preventDefault();
            const item = e.target.closest('.slash-item');
            if (item) this._selectSlashItem(item.dataset.type);
        });
    },

    _showSlashMenu(sel) {
        const range = sel.getRangeAt(0);
        const rect = range.getBoundingClientRect();
        const menu = document.getElementById('editor-slash-menu');
        menu.style.left = `${rect.left}px`;
        menu.style.top = `${rect.bottom + 4}px`;
        menu.classList.remove('hidden');
        this._slashMenuVisible = true;
        this._slashMenuIndex = 0;
        this._updateSlashSelection(menu.querySelectorAll('.slash-item'));
    },

    _hideSlashMenu() {
        document.getElementById('editor-slash-menu')?.classList.add('hidden');
        this._slashMenuVisible = false;
    },

    _updateSlashSelection(items) {
        items.forEach((item, i) => {
            item.classList.toggle('selected', i === this._slashMenuIndex);
        });
    },

    _selectSlashItem(type) {
        this._hideSlashMenu();
        const editorId = this._slashActiveEditorId || this._getActiveEditorId() || 'milkdown-post';
        if (!window.MilkdownBridge?.isReady(editorId)) return;
        const pm = document.querySelector(`#${editorId} .ProseMirror`);

        // Delete the "/" character using ProseMirror transaction
        window.MilkdownBridge.deleteBeforeCursor(editorId, 1);

        // Run the command via Milkdown's command API
        window.MilkdownBridge.runCommand(editorId, type);

        if (pm) pm.focus();
    },
};

// Start the app
document.addEventListener('DOMContentLoaded', () => App.init());

// Expose App on window for sibling scripts (owner-extras.js +
// inline event handlers in index.html). The top-level `const App`
// in a classic <script> doesn't auto-populate window, so accessing
// `window.App.method()` would silently fail without this line.
window.App = App;
