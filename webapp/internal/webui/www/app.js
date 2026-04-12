// Polis Local App - Client-side JavaScript

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
        editor: document.getElementById('editor-screen'),
        comment: document.getElementById('comment-screen'),
        about: document.getElementById('about-screen'),
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

    // Initialize Milkdown for a screen's editor
    async _initMilkdown(textareaId) {
        const editorId = this._milkdownIdFor(textareaId);
        if (!editorId || !window.MilkdownBridge) return;
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
            if (prev === 'about') this._destroyMilkdown('about-editor-textarea');
        }

        // Hide editor controls when leaving editor
        const editorControls = document.getElementById('editor-controls');
        if (editorControls) editorControls.classList.add('hidden');

        // Show global nav for all screens except welcome/error
        const iconNav = document.getElementById('icon-nav');
        if (iconNav) iconNav.classList.toggle('hidden', name === 'welcome' || name === 'error');

        Object.values(this.screens).forEach(s => {
            if (s) s.classList.add('hidden');
        });
        if (this.screens[name]) {
            this.screens[name].classList.remove('hidden');
        }
        if (name === 'editor') {
            // Show editor controls in the nav
            if (editorControls) editorControls.classList.remove('hidden');
            // Highlight compose icon
            this._updateTopbarMode('editor');
            this._rawMode['markdown-input'] = false;
            document.getElementById('markdown-input').classList.add('hidden');
            document.getElementById('milkdown-post').classList.remove('hidden');
            const saveStatus = document.getElementById('editor-save-status');
            saveStatus.textContent = '';
            saveStatus.classList.remove('saved');
            // Reset focus mode
            if (this._focusMode) {
                this._focusMode = false;
                document.getElementById('editor-screen').classList.remove('focus-mode');
                document.getElementById('icon-nav').classList.remove('focus-mode');
                document.getElementById('editor-focus-hint').classList.add('hidden');
            }
            this._initMilkdown('markdown-input');
        }
        if (name === 'comment') {
            this._initMilkdown('comment-input');
        }
        if (name === 'about') {
            this._initMilkdown('about-editor-textarea');
        }
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

    // Update which nav icon is highlighted based on current view
    _updateNavActive(view) {
        const map = {
            'posts-published': 'posts', 'posts-drafts': 'posts',
            'comments-published': 'comments', 'blessing-requests': 'comments',
            'following': 'people', 'followers': 'people',
            'dm-list': 'messages', 'dm-thread': 'messages', 'dm-new': 'messages',
            'feed': 'feed', 'conversations': 'feed', 'pulse': 'feed',
            'about': 'posts', 'settings': null
        };
        const active = map[view] || null;
        ['feed', 'posts', 'comments', 'people', 'messages'].forEach(id => {
            const btn = document.getElementById(`nav-btn-${id}`);
            if (btn) btn.classList.toggle('active', id === active);
        });
    },

    // Update nav badge dots from counts
    _updateNavBadges() {
        const feedDot = document.getElementById('nav-dot-feed');
        if (feedDot) feedDot.classList.toggle('hidden', !this.counts.hasNewFeed);
        const commentsDot = document.getElementById('nav-dot-comments');
        const messagesDot = document.getElementById('nav-dot-messages');
        if (commentsDot) {
            const hasItems = (this.counts.incomingPending || 0) > 0;
            commentsDot.classList.toggle('hidden', !hasItems);
        }
        if (messagesDot) {
            const hasItems = (this.counts.dmUnread || 0) > 0;
            messagesDot.classList.toggle('hidden', !hasItems);
        }
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

    // Legacy compatibility stubs
    switchMode(mode) {
        if (mode === 'feed') this.navigateTo('/feed');
        else if (mode === 'messages') this.navigateTo('/messages');
        else this.navigateTo('/posts');
    },

    _updateTopbarMode(mode) {
        // Map mode names to nav icon IDs
        const map = { 'social': 'posts', 'messages': 'messages', 'my-site': 'posts', 'editor': 'compose' };
        const active = map[mode] || 'posts';
        ['feed', 'compose', 'posts', 'comments', 'people', 'messages'].forEach(id => {
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
    ROUTES: [
        ['/',                            { mode: 'social',  view: 'conversations',   screen: 'dashboard' }],
        ['/posts',                       { mode: 'my-site', view: 'posts-published', screen: 'dashboard' }],
        ['/posts/drafts',                { mode: 'my-site', view: 'posts-drafts',    screen: 'dashboard' }],
        ['/posts/new',                   { screen: 'editor', action: 'newPost' }],
        ['/posts/drafts/:id',            { screen: 'editor', action: 'openDraft' }],
        ['/comments',                    { mode: 'my-site', view: 'comments-published', screen: 'dashboard' }],
        ['/comments/drafts',             { mode: 'my-site', view: 'comments-published', screen: 'dashboard', tabHint: 'drafts' }],
        ['/comments/pending',            { mode: 'my-site', view: 'comments-published', screen: 'dashboard', tabHint: 'pending' }],
        ['/comments/blessed',            { mode: 'my-site', view: 'comments-published', screen: 'dashboard', tabHint: 'blessed' }],
        ['/comments/denied',             { mode: 'my-site', view: 'comments-published', screen: 'dashboard', tabHint: 'denied' }],
        ['/comments/new',                { screen: 'comment', action: 'newComment' }],
        ['/comments/drafts/:id',         { screen: 'comment', action: 'openCommentDraft' }],
        ['/blessings',                   { mode: 'my-site', view: 'blessing-requests',    screen: 'dashboard' }],
        ['/snippets',                    { mode: 'my-site', view: 'about',            screen: 'dashboard' }],
        ['/feed',                        { mode: 'social',  view: 'conversations',    screen: 'dashboard' }],
        // Plugin routes injected by _registerPlugins()
        ['/following',                   { mode: 'social',  view: 'following',        screen: 'dashboard' }],
        ['/followers',                   { mode: 'social',  view: 'followers',        screen: 'dashboard' }],
        ['/messages',                    { mode: 'messages', view: 'dm-list',    screen: 'dashboard' }],
        ['/messages/new',                { mode: 'messages', view: 'dm-new',     screen: 'dashboard' }],
        ['/messages/:id',                { mode: 'messages', view: 'dm-thread',  screen: 'dashboard' }],
        ['/settings',                    { view: 'settings', screen: 'dashboard' }],
        ['/posts/:path+',                { screen: 'editor', action: 'openPost' }],
    ],

    // Reverse lookup: view name → canonical path (for pushState)
    VIEW_PATHS: {
        'posts-published':     '/posts',
        'posts-drafts':        '/posts/drafts',
        'comments-published':  '/comments',
        'blessing-requests':   '/blessings',
        'about':               '/snippets',
        'following':           '/following',
        'followers':           '/followers',
        'dm-list':             '/messages',
        'dm-new':              '/messages/new',
        'settings':            '/settings',
    },

    // Social plugins: each entry defines a social view that gets a sidebar button,
    // route, and dispatch entry. Removing an entry removes the view entirely.
    SOCIAL_PLUGINS: [
        { id: 'pulse',         label: 'Pulse',         path: '/pulse',         title: 'Community Pulse',  actions: '',                                                                                                                                                              render: 'renderPulse',                autoRefresh: true  },
        { id: 'conversations', label: 'Feed', path: '/conversations', title: '',    actions: '', render: 'renderConversationsTabbed',   autoRefresh: true  },
    ],

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
        // Auto-save draft when navigating away from editor
        if (this.screens.editor && !this.screens.editor.classList.contains('hidden') && !this.currentPostPath) {
            const markdown = this._buildFullMarkdown();
            if (markdown.trim()) {
                await this.saveDraft(true);
            }
        }

        const route = this.resolveRoute(path);
        if (!route) {
            this.showToast('Page not found', 'warning');
            window.history.replaceState({}, '', this.basePath + '/posts');
            if (!opts.skipRender) {
                this.sidebarMode = 'my-site';
                this._updateSidebarUI('my-site');
                this.currentView = 'posts-published';
                this._updateSidebarActiveItem('posts-published');
                await this.loadViewContent();
                this.showScreen('dashboard');
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
            if (config.view === 'dm-thread') this._dmThreadId = params.id || '';
            this._updateSidebarActiveItem(config.view);
            await this.loadViewContent();
            if (gen !== this._navGeneration) return; // stale
            this.showScreen('dashboard');
            return;
        }

        // Editor/action screens
        switch (config.action) {
            case 'newPost':
                this.newPost({ pushState: false });
                break;
            case 'openDraft':
                await this.openDraft(params.id, { pushState: false });
                break;
            case 'openPost':
                await this.openPost(params.path, { pushState: false });
                break;
            case 'newComment':
                this.newComment({ pushState: false });
                break;
            case 'openCommentDraft':
                await this.openCommentDraft(params.id, { pushState: false });
                break;
        }
    },

    // Build full URL path for a view name
    pathForView(view) {
        const rel = this.VIEW_PATHS[view];
        return rel ? this.basePath + rel : this.basePath + '/posts';
    },

    // Build full URL path for an editor/action screen
    pathForScreen(type, params = {}) {
        switch (type) {
            case 'newPost':      return this.basePath + '/posts/new';
            case 'openDraft':    return this.basePath + '/posts/drafts/' + encodeURIComponent(params.id);
            case 'openPost':     return this.basePath + '/posts/' + params.path;
            case 'newComment':   return this.basePath + '/comments/new';
            case 'openCommentDraft': return this.basePath + '/comments/drafts/' + encodeURIComponent(params.id);
            default:             return this.basePath + '/posts';
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
    _registerPlugins() {
        const nav = document.getElementById('social-plugins-nav');
        for (const plugin of this.SOCIAL_PLUGINS) {
            // Inject route
            const routeIdx = this.ROUTES.findIndex(([p]) => p === '/following');
            this.ROUTES.splice(routeIdx, 0, [plugin.path, { mode: 'social', view: plugin.id, screen: 'dashboard' }]);

            // Inject view path
            this.VIEW_PATHS[plugin.id] = plugin.path;

            // Create sidebar button
            if (nav) {
                const btn = document.createElement('button');
                btn.className = 'nav-item';
                btn.dataset.view = plugin.id;
                btn.textContent = plugin.label;
                nav.appendChild(btn);
            }
        }
    },

    // Initialize app
    async init() {
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
        const route = this.resolveRoute(pathname);

        if (!route) {
            // Unknown deep-link path — fall back to feed (home)
            this.navigateTo('/feed');
            return;
        }

        const { config, params } = route;

        // Normalize short-form URLs (e.g. /_/ → /_/posts, /_/social → /_/feed)
        if (config.view) {
            const canonical = this.pathForView(config.view);
            if (canonical !== pathname && pathname !== this.basePath + '/' && pathname !== this.basePath) {
                window.history.replaceState({}, '', canonical);
            } else if (pathname === this.basePath + '/' || pathname === this.basePath || pathname === '/') {
                window.history.replaceState({}, '', this.basePath + '/feed');
            }
        }

        if (config.screen === 'dashboard') {
            if (config.mode) {
                this.sidebarMode = config.mode;
                this._updateSidebarUI(config.mode);
            }
            this.currentView = config.view;
            if (config.tabHint) this._commentsPublishedFilter = config.tabHint;
            if (config.view === 'dm-thread') this._dmThreadId = params.id || '';
            this._updateSidebarActiveItem(config.view);
            await this.loadViewContent();
            this.showScreen('dashboard');
            return;
        }

        // For editor/action screens, show dashboard first (as fallback) then open
        await this.loadViewContent();
        this.showScreen('dashboard');

        switch (config.action) {
            case 'newPost':
                this.newPost({ pushState: false });
                break;
            case 'openDraft':
                await this.openDraft(params.id, { pushState: false });
                break;
            case 'openPost':
                await this.openPost(params.path, { pushState: false });
                break;
            case 'newComment':
                this.newComment({ pushState: false });
                break;
            case 'openCommentDraft':
                await this.openCommentDraft(params.id, { pushState: false });
                break;
        }
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
        this.counts.following = c.following || 0;
        this.counts.followers = c.followers || 0;
        this.counts.dmUnread = c.dm_unread || 0;

        this.updateSidebar();
        this._updateTopbarBadges();

        // Refresh non-feed views affected by sync
        const autoRefreshViews = ['blessing-requests', 'followers', 'comments-published', 'dm-list'];
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
                        <button class="primary" onclick="App.newPost()">Write your first post</button>
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

    // Load content for current view
    async loadViewContent() {
        const contentHeader = document.querySelector('.content-header');
        const contentTitle = document.getElementById('content-title');
        const contentActions = document.getElementById('content-actions');
        const contentList = document.getElementById('content-list');

        // Plugin dispatch: check if current view is a social plugin
        const plugin = this.SOCIAL_PLUGINS.find(p => p.id === this.currentView);
        if (plugin) {
            if (contentHeader) contentHeader.classList.add('hidden');
            await this[plugin.render](contentList);
            return;
        }

        // Posts-mode views use v3 sub-tabs instead of content-header
        const postsViews = ['posts-published', 'posts-drafts', 'comments-published', 'blessing-requests'];
        // Feed-mode views hide header entirely
        const feedViews = ['following', 'followers'];
        const dmViews = ['dm-list', 'dm-thread', 'dm-new'];

        if (postsViews.includes(this.currentView)) {
            if (contentHeader) contentHeader.classList.add('hidden');
        } else if (feedViews.includes(this.currentView)) {
            if (contentHeader) contentHeader.classList.add('hidden');
        } else if (dmViews.includes(this.currentView)) {
            if (contentHeader) contentHeader.classList.add('hidden');
        } else if (this.currentView === 'settings') {
            if (contentHeader) contentHeader.classList.add('hidden');
        } else {
            if (contentHeader) contentHeader.classList.remove('hidden');
        }

        switch (this.currentView) {
            case 'posts-published':
                await this.renderPostsList(contentList);
                break;

            case 'posts-drafts':
                await this.renderDraftsList(contentList);
                break;

            // MY COMMENTS (all statuses in one tabbed view)
            case 'comments-published':
                await this.renderCommentsPublished(contentList);
                break;

            // ON MY POSTS (incoming - others wrote these)
            case 'blessing-requests':
                await this.renderBlessingRequests(contentList);
                break;

            case 'settings':
                this.renderSettings(contentList);
                break;

            case 'about':
                await this.openAboutEditor();
                return;

            // Social views
            case 'following':
                await this.renderFollowingList(contentList);
                break;

            case 'followers':
                await this.renderFollowersList(contentList);
                break;

            // DM views
            case 'dm-list':
                await this.renderDMConversationList(contentList);
                break;

            case 'dm-thread': {
                const convId = this._dmThreadId || '';
                await this.renderDMThread(contentList, convId);
                break;
            }

            case 'dm-new':
                await this.renderDMNewConversation(contentList);
                break;

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
            // Auto-save draft when navigating away from editor via browser back/forward
            if (this.screens.editor && !this.screens.editor.classList.contains('hidden') && !this.currentPostPath) {
                const markdown = this._buildFullMarkdown();
                if (markdown.trim()) {
                    await this.saveDraft(true);
                }
            }
            await this._restoreRouteFromURL();
        });

        // Publish button
        document.getElementById('publish-btn').addEventListener('click', async () => {
            await this.publish();
        });

        // Delete draft button (in editor header)
        document.getElementById('delete-draft-btn').addEventListener('click', async () => {
            if (this.currentDraftId) {
                await this.deleteDraft(this.currentDraftId);
            }
        });

        // Auto-generate filename from title and live preview as user types (raw textarea mode)
        document.getElementById('markdown-input').addEventListener('input', (e) => {
            if (!this.filenameManuallySet && !this.currentPostPath) {
                const markdown = e.target.value;
                const title = this.extractTitleFromMarkdown(markdown);
                if (title) {
                    document.getElementById('filename-input').value = this.slugify(title);
                }
            }
            // Live preview sync from raw textarea
            this.editorUpdatePreview();
        });

        // Editor help button
        document.getElementById('editor-help-btn').addEventListener('click', () => {
            document.getElementById('editor-help-modal').classList.toggle('hidden');
        });

        // Link modal — Enter to confirm, Escape to cancel
        document.getElementById('editor-link-url').addEventListener('keydown', (e) => {
            if (e.key === 'Enter') { e.preventDefault(); this._confirmLinkModal(); }
            if (e.key === 'Escape') { e.preventDefault(); this._cancelLinkModal(); }
        });

        // Editor title input — Enter moves focus to body
        document.getElementById('editor-title-input').addEventListener('keydown', (e) => {
            if (e.key === 'Enter') {
                e.preventDefault();
                // Focus the Milkdown editor
                const prosemirror = document.querySelector('#milkdown-post .ProseMirror');
                if (prosemirror) prosemirror.focus();
            }
        });

        // Auto-generate filename slug from title input + toggle # indicator
        document.getElementById('editor-title-input').addEventListener('input', (e) => {
            const titleRow = e.target.closest('.editor-title-row');
            if (titleRow) titleRow.classList.toggle('has-value', !!e.target.value.trim());
            if (!this.filenameManuallySet && !this.currentPostPath) {
                const title = e.target.value.trim();
                document.getElementById('filename-input').value = title ? this.slugify(title) : '';
            }
        });

        // Markdown keyboard shortcuts on textarea
        document.getElementById('markdown-input').addEventListener('keydown', (e) => {
            this._handleMarkdownShortcut(document.getElementById('markdown-input'), e);
        });

        // Auto-save: listen for content changes in Milkdown and title input
        const autoSaveTrigger = () => {
            if (this.screens.editor.classList.contains('hidden')) return;
            if (this.currentPostPath) return; // Don't auto-save republishes
            const status = document.getElementById('editor-save-status');
            status.textContent = 'Unsaved';
            status.classList.remove('saved');
            if (this._autoSaveTimer) clearTimeout(this._autoSaveTimer);
            this._autoSaveTimer = setTimeout(() => {
                const markdown = this._buildFullMarkdown();
                if (markdown.trim()) this.saveDraft(true);
            }, 2000);
        };
        document.addEventListener('milkdown:change', (e) => {
            if (e.detail && e.detail.editorId === 'milkdown-post') autoSaveTrigger();
        });
        document.getElementById('editor-title-input').addEventListener('input', autoSaveTrigger);

        // Floating selection toolbar
        document.addEventListener('selectionchange', () => {
            const toolbar = document.getElementById('editor-toolbar');
            if (!toolbar) return;
            const sel = window.getSelection();
            if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
                toolbar.classList.add('hidden');
                return;
            }
            const range = sel.getRangeAt(0);
            const activeId = this._getActiveEditorId();
            if (!activeId) { toolbar.classList.add('hidden'); return; }
            const pm = document.querySelector(`#${activeId} .ProseMirror`);
            if (!pm || !pm.contains(range.commonAncestorContainer)) {
                toolbar.classList.add('hidden');
                return;
            }
            const rect = range.getBoundingClientRect();
            if (rect.width === 0) { toolbar.classList.add('hidden'); return; }
            const toolbarW = 220; // approximate width
            toolbar.style.left = `${Math.max(8, rect.left + rect.width / 2 - toolbarW / 2)}px`;
            toolbar.style.top = `${rect.top - 40}px`;
            toolbar.classList.remove('hidden');
        });

        // Toolbar button clicks (mousedown to prevent selection loss)
        document.getElementById('editor-toolbar').addEventListener('mousedown', (e) => {
            e.preventDefault();
            const btn = e.target.closest('.tb-btn');
            if (btn) this._execToolbarCommand(btn.dataset.command);
        });

        // Hide toolbar on scroll
        document.querySelector('.editor-container')?.addEventListener('scroll', () => {
            document.getElementById('editor-toolbar').classList.add('hidden');
        });

        // Slash command menu
        this._initSlashMenu();

        // About editor raw mode toggle
        document.getElementById('about-raw-toggle').addEventListener('click', () => {
            this._toggleRawMode('about-editor-textarea', document.getElementById('about-raw-toggle'));
        });

        // About editor preview pane toggle
        document.getElementById('about-preview-toggle').addEventListener('click', () => {
            const pane = document.getElementById('about-screen').querySelector('.preview-pane');
            const btn = document.getElementById('about-preview-toggle');
            pane.classList.toggle('collapsed');
            btn.textContent = pane.classList.contains('collapsed') ? 'Show' : 'Hide';
            if (!pane.classList.contains('collapsed')) {
                this.updateAboutPreview();
            }
        });

        // Graceful degradation: if Milkdown fails to load, show textareas
        // Check if milkdown already loaded before this listener was registered
        if (window.MilkdownBridge) {
            this._milkdownReady = true;
        }
        window.addEventListener('milkdown:ready', () => {
            this._milkdownReady = true;
        });
        setTimeout(() => {
            if (!this._milkdownReady) {
                console.warn('Milkdown did not load, falling back to textareas');
                document.querySelectorAll('.milkdown-mount').forEach(m => m.classList.add('hidden'));
                document.querySelectorAll('#markdown-input, #comment-input, #about-editor-textarea').forEach(t => t.classList.remove('hidden'));
            }
        }, 10000);

        // Mark filename as manually set when user edits it
        document.getElementById('filename-input').addEventListener('input', () => {
            this.filenameManuallySet = true;
        });

        // Comment back button
        document.getElementById('comment-back-btn').addEventListener('click', async () => {
            await this.loadAllCounts();
            history.back();
        });

        // Save comment draft button
        document.getElementById('save-comment-draft-btn').addEventListener('click', async () => {
            await this.saveCommentDraft();
        });

        // Sign & send for blessing button
        document.getElementById('sign-send-btn').addEventListener('click', async () => {
            await this.signAndSendComment();
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
            // Ctrl/Cmd + S to save
            if ((e.ctrlKey || e.metaKey) && e.key === 's') {
                e.preventDefault();
                if (!this.screens.editor.classList.contains('hidden')) {
                    this.saveDraft();
                } else if (!this.screens.comment.classList.contains('hidden')) {
                    this.saveCommentDraft();
                }
            }
            // Ctrl/Cmd + Enter to publish
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                e.preventDefault();
                if (!this.screens.editor.classList.contains('hidden')) {
                    this.publish();
                }
            }
            // Ctrl/Cmd + Shift + F to toggle focus mode
            if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'F') {
                e.preventDefault();
                if (!this.screens.editor.classList.contains('hidden')) {
                    this._toggleFocusMode();
                } else if (this._feedEditorOpen) {
                    // Transfer inline editor content to full editor
                    this._openFullEditorFromFeed();
                }
            }
            // Ctrl/Cmd + Enter to publish from inline feed editor
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

        // About editor events
        document.getElementById('about-back-btn').addEventListener('click', () => {
            history.back();
        });
        document.getElementById('about-publish-btn').addEventListener('click', () => {
            this.publishAbout();
        });
        // About textarea input handler (raw mode only — no auto-preview since preview is on-demand now)

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
            window.history.replaceState({}, '', this.pathForView('posts-published'));

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
            window.history.replaceState({}, '', this.pathForView('posts-published'));
        } catch (err) {
            this.showToast('Failed to link site: ' + err.message, 'error');
        } finally {
            if (executeBtn) {
                executeBtn.disabled = false;
                executeBtn.textContent = 'Link Site';
            }
        }
    },

    // New post action
    newPost(opts = {}) {
        this.currentDraftId = null;
        this.currentPostPath = null;
        this.currentFrontmatter = '';
        this.filenameManuallySet = false;
        this.setEditorContent('markdown-input', '');
        document.getElementById('editor-title-input').value = '';
        document.getElementById('filename-input').value = '';
        document.getElementById('filename-input').disabled = false;
        const saveStatus = document.getElementById('editor-save-status');
        saveStatus.textContent = '';
        saveStatus.classList.remove('saved');
        if (this._autoSaveTimer) { clearTimeout(this._autoSaveTimer); this._autoSaveTimer = null; }
        this.updatePublishButton();
        if (opts.pushState !== false) {
            window.history.pushState({}, '', this.pathForScreen('newPost'));
        }
        this.showScreen('editor');
    },

    // New comment action
    newComment(opts = {}) {
        this.currentCommentDraftId = null;
        document.getElementById('reply-to-url').value = '';
        this.setEditorContent('comment-input', '');
        if (opts.pushState !== false) {
            window.history.pushState({}, '', this.pathForScreen('newComment'));
        }
        this.showScreen('comment');
    },

    // Open inline comment editor in feed, or fall back to full-screen
    newCommentDraft(url) {
        if (url && this.currentView === 'conversations') {
            this.openInlineCommentEditor(url);
        } else {
            this.newComment();
        }
    },

    // Render v3 sub-tabs for Posts mode
    _renderPostsSubTabs(activeTab) {
        const tabs = [
            { id: 'posts-published', label: 'Published', countKey: 'posts' },
            { id: 'posts-drafts', label: 'Drafts', countKey: 'drafts' },
            { id: 'comments-published', label: 'Comments', countKey: 'comments' },
            { id: 'blessing-requests', label: 'Blessings', countKey: 'blessingsPending', warning: true },
        ];
        const ctaMap = {
            'posts-published': '<button class="btn-new" onclick="App.newPost()">New Post</button>',
            'posts-drafts': '<button class="btn-new" onclick="App.newPost()">New Post</button>',
            'comments-published': '<button class="btn-new" onclick="App.newComment()">New Comment</button>',
            'blessing-requests': '',
        };
        return `
            <div class="view-tabs">
                <div class="tab-group">
                    ${tabs.map(t => {
                        const count = this.counts[t.countKey] || 0;
                        const countHtml = count > 0 ? `<span class="tab-count${t.warning ? ' warning' : ''}">${count}</span>` : '';
                        const pathMap = { 'posts-published': '/posts', 'posts-drafts': '/posts/drafts', 'comments-published': '/comments', 'blessing-requests': '/blessings' };
                        return `<button class="tab-item${activeTab === t.id ? ' active' : ''}" onclick="App.navigateTo('${pathMap[t.id]}')">${t.label} ${countHtml}</button>`;
                    }).join('')}
                </div>
                <div class="view-actions">
                    ${ctaMap[activeTab] || ''}
                </div>
            </div>
        `;
    },

    // Render posts list (my-content view)
    async renderPostsList(container) {
        if (!this._postsFilter) this._postsFilter = 'published';
        try {
            // Fetch posts, drafts, and about in parallel
            const [postsResult, draftsResult, aboutResult] = await Promise.all([
                this.api('GET', '/api/posts').catch(() => ({ posts: [] })),
                this.api('GET', '/api/drafts').catch(() => ({ drafts: [] })),
                this.api('GET', '/api/about').catch(() => ({ content: '' })),
            ]);
            const posts = postsResult.posts || [];
            const drafts = draftsResult.drafts || [];
            const aboutText = aboutResult.content || '';
            const aboutHtml = aboutResult.content_html || '';
            this._mcAboutText = aboutText;
            this.counts.posts = posts.length;
            this.counts.drafts = drafts.length;



            const domain = this.siteBaseUrl ? (() => { try { return new URL(this.siteBaseUrl).hostname; } catch { return ''; } })() : '';
            const name = this.authorName || domain || 'Untitled';

            // Avatar
            let avatarHtml;
            if (this.avatarConfig) {
                avatarHtml = `<div class="mc-avatar" style="${this._buildAvatarStyle(this.avatarConfig)}"></div>`;
            } else {
                const det = this.domainToAvatar(domain || 'me');
                avatarHtml = `<div class="mc-avatar" style="background: ${det.color};">${det.initials}</div>`;
            }

            const isPublished = this._postsFilter === 'published';
            const items = isPublished ? posts : drafts;

            // Build post list HTML
            let listHtml;
            if (items.length === 0) {
                if (isPublished) {
                    listHtml = `<div class="mc-empty">No published posts yet. <a href="#" onclick="event.preventDefault(); App.newPost()">Write your first post</a>.</div>`;
                } else {
                    listHtml = `<div class="mc-empty">No drafts. <a href="#" onclick="event.preventDefault(); App.newPost()">Start writing</a>.</div>`;
                }
            } else if (isPublished) {
                listHtml = posts.map(post => {
                    const excerptHtml = post.excerpt ? `<div class="mc-excerpt">${this.escapeHtml(post.excerpt)}</div>` : '';
                    const commentCount = post.comment_count || 0;
                    const commentHtml = `<div class="mc-comments"><svg width="14" height="14" viewBox="0 0 24 24"><path d="M3 2C1.9 2 1 2.9 1 4v12c0 1.1.9 2 2 2h12l4 4V4c0-1.1-.9-2-2-2H3zm0 2h14v13.2L15.2 16H3V4z" fill="currentColor"/></svg> ${commentCount}</div>`;
                    const editedHtml = post.modified ? `<span class="mc-edited">&middot; edited ${this.formatRelativeTime(post.modified)}</span>` : '';
                    return `<div class="mc-post" onclick="App.openPost('${this.escapeHtml(post.path)}')">
                        <div class="mc-date">${this.formatDate(post.published)} ${editedHtml}</div>
                        <div class="mc-title">${this.escapeHtml(post.title)}</div>
                        ${excerptHtml}
                        ${commentHtml}
                    </div>`;
                }).join('');
            } else {
                listHtml = drafts.map(draft => {
                    const draftTitle = draft.title || draft.id;
                    const draftExcerpt = draft.excerpt ? `<div class="mc-excerpt">${this.escapeHtml(draft.excerpt)}</div>` : '';
                    return `<div class="mc-post mc-post-draft" onclick="App.openDraft('${this.escapeHtml(draft.id)}')">
                        <div class="mc-date">edited ${this.formatRelativeTime(draft.modified)}</div>
                        <div class="mc-title">${this.escapeHtml(draftTitle)} <span class="mc-draft-badge">draft</span></div>
                        ${draftExcerpt}
                        <button class="mc-delete-draft" onclick="event.stopPropagation(); App.deleteDraft('${this.escapeHtml(draft.id)}')" title="Delete draft"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg></button>
                    </div>`;
                }).join('');
            }

            container.innerHTML = `
                <style>
                    .mc-header { padding: 24px 0 0 0; }
                    .mc-identity { display: flex; align-items: flex-start; gap: 14px; }
                    .mc-avatar { width: 48px; height: 48px; min-width: 48px; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-size: 18px; font-weight: 600; color: #fff; }
                    .mc-info { flex: 1; min-width: 0; }
                    .mc-name { font-family: var(--font-content); font-size: 22px; color: var(--text-primary); line-height: 1.3; }
                    .mc-handle { font-family: var(--font-mono); font-size: 13px; color: var(--text-tertiary); margin-top: 2px; }
                    .mc-about-wrap { margin-top: 10px; padding-left: 0; position: relative; }
                    .mc-about { font-family: var(--font-content); font-size: 15px; color: var(--text-secondary); line-height: 1.5; cursor: pointer; position: relative; }
                    .mc-about p { margin: 0 0 0.6em 0; }
                    .mc-about p:last-child { margin-bottom: 0; }
                    .mc-about a { color: var(--accent); }
                    .mc-about ul, .mc-about ol { margin: 0.4em 0; padding-left: 1.5em; }
                    .mc-about li { margin: 0.2em 0; }
                    .mc-about.clipped { max-height: 4.5em; overflow: hidden; -webkit-mask-image: linear-gradient(to bottom, black 60%, transparent); mask-image: linear-gradient(to bottom, black 60%, transparent); }
                    .mc-about:hover { color: var(--text-primary); }
                    .mc-about-edit-hint { display: inline-flex; align-items: center; gap: 4px; font-size: 11px; color: var(--text-tertiary); opacity: 0.5; transition: opacity 0.15s; cursor: pointer; margin-top: 6px; }
                    .mc-about-edit-hint:hover { opacity: 1; color: var(--accent); }
                    .mc-about-edit-hint svg { width: 12px; height: 12px; }
                    .mc-about-edit-hint svg path, .mc-about-edit-hint svg line { stroke: currentColor; stroke-width: 2; fill: none; }
                    .mc-about-show-more { font-size: 12px; color: var(--accent); cursor: pointer; background: none; border: none; padding: 4px 0; font-family: inherit; margin-top: 2px; }
                    .mc-about-show-more:hover { text-decoration: underline; }
                    .mc-about-edit { font-family: var(--font-content); font-size: 15px; color: var(--text-primary); background: transparent; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 8px 10px; width: 100%; min-height: 80px; resize: vertical; line-height: 1.5; }
                    .mc-about-edit:focus { outline: none; border-color: var(--accent); }
                    .mc-about-actions { display: flex; gap: 8px; margin-top: 8px; }
                    .mc-about-actions button { font-size: 13px; padding: 4px 12px; border-radius: var(--radius-sm); cursor: pointer; border: none; }
                    .mc-about-actions .mc-save { background: var(--accent); color: #fff; }
                    .mc-about-actions .mc-cancel { background: transparent; color: var(--text-secondary); border: 1px solid var(--border); }
                    .mc-stats { font-size: 13px; color: var(--text-tertiary); display: flex; gap: 4px; align-items: center; }
                    .mc-stat { cursor: pointer; padding: 0; background: none; border: none; font-size: 12px; color: var(--text-tertiary); font-family: inherit; }
                    .mc-stat:hover { color: var(--text-secondary); }
                    .mc-stat.active { font-weight: 700; color: var(--text-primary); }
                    .mc-divider { border: none; border-top: 1px solid var(--border); margin: 18px 0 12px 0; }
                    .mc-toolbar { display: flex; justify-content: space-between; align-items: center; padding: 12px 0 4px; }
                    .mc-new-post { font-size: 14px; color: var(--accent); background: none; border: none; cursor: pointer; font-family: inherit; padding: 4px 0; }
                    .mc-new-post:hover { text-decoration: underline; }
                    .mc-post { padding: 18px 0; cursor: pointer; border-radius: var(--radius-sm); transition: background 0.15s; }
                    .mc-post:hover { background: var(--bg-hover); }
                    .mc-date { font-size: 12px; color: var(--text-tertiary); margin-bottom: 4px; }
                    .mc-edited { font-size: 11px; color: var(--text-tertiary); opacity: 0.7; }
                    .mc-title { font-family: var(--font-content); font-size: 18px; color: var(--text-primary); line-height: 1.3; }
                    .mc-excerpt { font-family: var(--font-content); font-size: 14.5px; color: var(--text-secondary); line-height: 1.5; margin-top: 4px; }
                    .mc-comments { font-size: 12px; color: var(--text-secondary); opacity: 0.5; margin-top: 6px; display: flex; align-items: center; gap: 5px; }
                    .mc-comments svg { flex-shrink: 0; }
                    .mc-draft-badge { font-size: 11px; font-family: var(--font-ui, inherit); background: var(--border); color: var(--text-secondary); padding: 1px 6px; border-radius: 3px; margin-left: 6px; vertical-align: middle; }
                    .mc-post-draft { position: relative; }
                    .mc-delete-draft { position: absolute; right: 4px; top: 50%; transform: translateY(-50%); background: none; border: none; color: var(--text-tertiary); font-size: 18px; line-height: 1; padding: 4px 8px; cursor: pointer; opacity: 0; transition: opacity 0.15s, color 0.15s; border-radius: var(--radius-sm); }
                    .mc-post-draft:hover .mc-delete-draft { opacity: 1; }
                    .mc-delete-draft:hover { color: var(--salmon, #c4604a); background: var(--bg-hover); }
                    .mc-empty { font-size: 14px; color: var(--text-tertiary); padding: 32px 0; text-align: center; }
                    .mc-empty a { color: var(--accent); }
                </style>
                <div class="mc-header">
                    <div class="mc-identity">
                        ${avatarHtml}
                        <div class="mc-info">
                            <div class="mc-name">${this.escapeHtml(name)}</div>
                            ${domain ? `<div class="mc-handle">${this.escapeHtml(domain)}</div>` : ''}
                        </div>
                    </div>
                    <div class="mc-about-wrap">
                        <div class="mc-about${aboutText && aboutText.length > 200 ? ' clipped' : ''}" id="mc-about-display" onclick="App._mcStartEditAbout()">${aboutHtml ? aboutHtml : (aboutText ? this.escapeHtml(aboutText) : '<span style="color:var(--text-tertiary);font-style:italic">Add a short bio&hellip;</span>')}</div>
                        ${aboutText && aboutText.length > 200 ? '<button class="mc-about-show-more" onclick="event.stopPropagation(); var d=document.getElementById(\'mc-about-display\'); d.classList.toggle(\'clipped\'); this.textContent=d.classList.contains(\'clipped\')?\'show more\':\'show less\';">show more</button>' : ''}
                        <span class="mc-about-edit-hint" onclick="App._mcStartEditAbout()"><svg viewBox="0 0 24 24"><path d="M17 3a2.83 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><line x1="15" y1="5" x2="19" y2="9"/></svg> edit</span>
                    </div>
                </div>
                <hr class="mc-divider">
                <div class="mc-toolbar">
                    <div class="mc-stats">
                        <button class="mc-stat${isPublished ? ' active' : ''}" onclick="App._mcSetFilter('published')">${posts.length} post${posts.length !== 1 ? 's' : ''}</button>
                        <span>&middot;</span>
                        <button class="mc-stat${!isPublished ? ' active' : ''}" onclick="App._mcSetFilter('drafts')">${drafts.length} draft${drafts.length !== 1 ? 's' : ''}</button>
                    </div>
                    <button class="mc-new-post" onclick="App.newPost()">+ New post</button>
                </div>
                <div id="mc-list">
                    ${listHtml}
                </div>
            `;
            // Store data for filter switching without refetch
            this._mcPostsData = posts;
            this._mcDraftsData = drafts;
        } catch (err) {
            container.innerHTML = `
                <div class="content-list">
                    <div class="empty-state">
                        <h3>Failed to load posts</h3>
                        <p>${this.escapeHtml(err.message)}</p>
                    </div>
                </div>
            `;
        }
    },

    // My-content: switch between published/drafts filter (re-renders list only)
    _mcSetFilter(filter) {
        this._postsFilter = filter;
        const listEl = document.getElementById('mc-list');
        if (!listEl) return;
        const isPublished = filter === 'published';
        const items = isPublished ? (this._mcPostsData || []) : (this._mcDraftsData || []);

        let listHtml;
        if (items.length === 0) {
            if (isPublished) {
                listHtml = `<div class="mc-empty">No published posts yet. <a href="#" onclick="event.preventDefault(); App.newPost()">Write your first post</a>.</div>`;
            } else {
                listHtml = `<div class="mc-empty">No drafts. <a href="#" onclick="event.preventDefault(); App.newPost()">Start writing</a>.</div>`;
            }
        } else if (isPublished) {
            listHtml = items.map(post => {
                const excerptHtml = post.excerpt ? `<div class="mc-excerpt">${this.escapeHtml(post.excerpt)}</div>` : '';
                const editedHtml = post.modified ? `<span class="mc-edited">&middot; edited ${this.formatRelativeTime(post.modified)}</span>` : '';
                return `<div class="mc-post" onclick="App.openPost('${this.escapeHtml(post.path)}')">
                    <div class="mc-date">${this.formatDate(post.published)} ${editedHtml}</div>
                    <div class="mc-title">${this.escapeHtml(post.title)}</div>
                    ${excerptHtml}
                </div>`;
            }).join('');
        } else {
            listHtml = items.map(draft => {
                const draftTitle = draft.title || draft.id;
                const draftExcerpt = draft.excerpt ? `<div class="mc-excerpt">${this.escapeHtml(draft.excerpt)}</div>` : '';
                return `<div class="mc-post mc-post-draft" onclick="App.openDraft('${this.escapeHtml(draft.id)}')">
                    <div class="mc-date">edited ${this.formatRelativeTime(draft.modified)}</div>
                    <div class="mc-title">${this.escapeHtml(draftTitle)} <span class="mc-draft-badge">draft</span></div>
                    ${draftExcerpt}
                    <button class="mc-delete-draft" onclick="event.stopPropagation(); App.deleteDraft('${this.escapeHtml(draft.id)}')" title="Delete draft"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg></button>
                </div>`;
            }).join('');
        }
        listEl.innerHTML = listHtml;

        // Update stat button active states
        document.querySelectorAll('.mc-stat').forEach(btn => {
            const isPostsStat = btn.textContent.includes('post');
            btn.classList.toggle('active', isPublished ? isPostsStat : !isPostsStat);
        });
    },

    // My-content: start editing about text inline
    _mcStartEditAbout() {
        const display = document.getElementById('mc-about-display');
        if (!display) return;
        const wrap = display.parentElement;
        const currentText = (this._mcAboutText != null) ? this._mcAboutText : '';
        // Replace placeholder text
        const text = (currentText === 'Add a short bio\u2026') ? '' : currentText;
        wrap.innerHTML = `
            <div id="mc-milkdown-about" class="milkdown-mount" style="min-height:120px;max-height:300px;overflow-y:auto;border:1px solid var(--border);border-radius:var(--radius-sm);"></div>
            <textarea class="mc-about-edit hidden" id="mc-about-textarea">${this.escapeHtml(text)}</textarea>
            <div class="mc-about-actions">
                <button class="mc-save" onclick="App._mcSaveAbout()">Save</button>
                <button class="mc-cancel" onclick="App._mcCancelEditAbout()">Cancel</button>
            </div>
        `;
        this._rawMode['mc-about-textarea'] = false;
        this._initMilkdown('mc-about-textarea');
    },

    // My-content: cancel about editing
    _mcCancelEditAbout() {
        this._destroyMilkdown('mc-about-textarea');
        const container = document.getElementById('content-list');
        if (container) this.renderPostsList(container);
    },

    // My-content: save about text
    async _mcSaveAbout() {
        const content = this.getEditorContent('mc-about-textarea');
        try {
            await this.api('POST', '/api/about', { content });
            this._mcAboutText = content;
            this._destroyMilkdown('mc-about-textarea');
            this.showToast('About updated', 'success');
            // Re-render to show updated text
            const container = document.getElementById('content-list');
            if (container) this.renderPostsList(container);
        } catch (err) {
            this.showToast('Failed to save: ' + err.message, 'error');
        }
    },

    // Render drafts list
    async renderDraftsList(container) {
        try {
            const result = await this.api('GET', '/api/drafts');
            const drafts = result.drafts || [];
            this.counts.drafts = drafts.length;


            if (drafts.length === 0) {
                container.innerHTML = this._renderPostsSubTabs('posts-drafts') + `
                    <div class="content-list">
                        <div class="empty-state">
                            <h3>No drafts yet</h3>
                            <p>Start writing your first post</p>
                            <button class="primary" onclick="App.newPost()">New Post</button>
                        </div>
                    </div>
                `;
                return;
            }

            container.innerHTML = this._renderPostsSubTabs('posts-drafts') + `
                <div class="post-list">
                    ${drafts.map(draft => {
                        const title = draft.title || draft.id;
                        const excerpt = draft.excerpt || '';
                        return `
                        <div class="post-row" onclick="App.openDraft('${this.escapeHtml(draft.id)}')">
                            <div class="post-info">
                                <div class="post-title">${this.escapeHtml(title)}</div>
                                ${excerpt ? `<div class="post-excerpt">${this.escapeHtml(excerpt)}</div>` : ''}
                                <div class="post-meta"><span>Edited ${this.formatDate(draft.modified)}</span></div>
                            </div>
                            <div class="post-status">
                                <span class="draft-badge">Draft</span>
                            </div>
                        </div>
                    `}).join('')}
                </div>
            `;
        } catch (err) {
            container.innerHTML = `
                <div class="content-list">
                    <div class="empty-state">
                        <h3>Failed to load drafts</h3>
                        <p>${this.escapeHtml(err.message)}</p>
                    </div>
                </div>
            `;
        }
    },

    // Extract hostname from a URL
    extractDomainFromUrl(url) {
        try { return new URL(url).hostname; } catch { return ''; }
    },

    // Render combined "My Comments" view with pill tabs (All/Drafts/Pending/Blessed/Denied)
    async renderCommentsPublished(container, filter) {
        if (filter) this._commentsPublishedFilter = filter;
        const currentFilter = this._commentsPublishedFilter;

        // Fetch all 4 statuses in parallel
        let drafts = [], pending = [], blessed = [], denied = [];
        try {
            const [draftsRes, pendingRes, blessedRes, deniedRes] = await Promise.all([
                this.api('GET', '/api/comments/drafts').catch(() => ({ drafts: [] })),
                this.api('GET', '/api/comments/pending').catch(() => ({ comments: [] })),
                this.api('GET', '/api/comments/blessed').catch(() => ({ comments: [] })),
                this.api('GET', '/api/comments/denied').catch(() => ({ comments: [] })),
            ]);
            drafts = (draftsRes.drafts || []).map(d => ({
                ...d,
                _status: 'draft',
                _sortDate: d.updated_at || d.created_at || '',
                _title: d.content ? d.content.substring(0, 60) : d.id,
                _domain: this.extractDomainFromUrl(d.in_reply_to),
            }));
            pending = (pendingRes.comments || []).map(c => ({
                ...c,
                _status: 'pending',
                _sortDate: c.timestamp || '',
                _title: c.title || c.id,
                _domain: this.extractDomainFromUrl(c.in_reply_to),
            }));
            blessed = (blessedRes.comments || []).map(c => ({
                ...c,
                _status: 'blessed',
                _sortDate: c.timestamp || '',
                _title: c.title || c.id,
                _domain: this.extractDomainFromUrl(c.in_reply_to),
            }));
            denied = (deniedRes.comments || []).map(c => ({
                ...c,
                _status: 'denied',
                _sortDate: c.timestamp || '',
                _title: c.title || c.id,
                _domain: this.extractDomainFromUrl(c.in_reply_to),
            }));
        } catch (err) {
            container.innerHTML = this._renderPostsSubTabs('comments-published') + `<div class="post-list"><div class="empty-state"><h3>Failed to load</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
            return;
        }

        // Update badge counts
        this.counts.myCommentDrafts = drafts.length;
        this.counts.myPending = pending.length;
        this.counts.myBlessed = blessed.length;
        this.counts.myDenied = denied.length;
        const total = drafts.length + pending.length + blessed.length + denied.length;


        // Build filter tabs matching view-tabs style
        const tabClass = (name) => name === currentFilter ? 'tab-item active' : 'tab-item';
        const tabs = `
            <div class="view-tabs" style="margin-bottom: 0.5rem;">
                <div class="tab-group">
                    <button class="${tabClass('all')}" onclick="App.renderCommentsPublished(document.getElementById('content-list'), 'all')">All <span class="tab-count">${total}</span></button>
                    <button class="${tabClass('drafts')}" onclick="App.renderCommentsPublished(document.getElementById('content-list'), 'drafts')">Drafts <span class="tab-count">${drafts.length}</span></button>
                    <button class="${tabClass('blessed')}" onclick="App.renderCommentsPublished(document.getElementById('content-list'), 'blessed')">Blessed <span class="tab-count">${blessed.length}</span></button>
                    <button class="${tabClass('pending')}" onclick="App.renderCommentsPublished(document.getElementById('content-list'), 'pending')">Pending <span class="tab-count">${pending.length}</span></button>
                    <button class="${tabClass('denied')}" onclick="App.renderCommentsPublished(document.getElementById('content-list'), 'denied')">Denied <span class="tab-count">${denied.length}</span></button>
                </div>
            </div>
        `;

        // Filter items
        let items = [];
        if (currentFilter === 'all') items = [...drafts, ...pending, ...blessed, ...denied];
        else if (currentFilter === 'drafts') items = drafts;
        else if (currentFilter === 'pending') items = pending;
        else if (currentFilter === 'blessed') items = blessed;
        else if (currentFilter === 'denied') items = denied;

        // Sort by date descending
        items.sort((a, b) => (b._sortDate || '').localeCompare(a._sortDate || ''));

        if (items.length === 0) {
            const emptyMessages = {
                all: 'No comments yet',
                drafts: 'No comment drafts',
                pending: 'No pending comments',
                blessed: 'No blessed comments',
                denied: 'No denied comments',
            };
            container.innerHTML = this._renderPostsSubTabs('comments-published') + `${tabs}<div class="post-list"><div class="empty-state"><h3>${emptyMessages[currentFilter] || 'No comments'}</h3><p>Write a comment to reply to someone's post</p><button class="primary" onclick="App.newComment()">New Comment</button></div></div>`;
            return;
        }

        const itemsHtml = items.map(item => {
            const date = item._sortDate;
            const onclick = item._status === 'draft'
                ? `App.openCommentDraft('${this.escapeHtml(item.id)}')`
                : `App.viewMyComment('${this.escapeHtml(item.id)}', '${item._status}')`;
            const excerpt = item.excerpt || '';
            const showExcerpt = excerpt && !excerpt.toLowerCase().startsWith((item._title || '').toLowerCase().slice(0, 30));
            return `
                <div class="post-row" onclick="${onclick}">
                    <div class="post-info">
                        <div class="post-title">${this.escapeHtml(item._title)}</div>
                        ${showExcerpt ? `<div class="post-excerpt">${this.escapeHtml(excerpt)}</div>` : ''}
                        <div class="post-meta">
                            <span class="comment-status-badge ${item._status}">${item._status}</span>
                            ${item._domain ? `<span class="sep">&middot;</span><span>${this.escapeHtml(item._domain)}</span>` : ''}
                        </div>
                    </div>
                    <div class="post-status">
                        <span class="post-date">${this.formatRelativeTime(date)}</span>
                    </div>
                </div>
            `;
        }).join('');

        container.innerHTML = this._renderPostsSubTabs('comments-published') + `${tabs}<div class="post-list">${itemsHtml}</div>`;
    },

    // View a comment (my outgoing comments)
    async viewMyComment(id, status) {
        try {
            const result = await this.api('GET', `/api/comments/${status}/${encodeURIComponent(id)}`);
            this.showCommentDetail(result.comment, status);
        } catch (err) {
            this.showToast('Failed to load comment: ' + err.message, 'error');
        }
    },

    // Show comment detail in flyout panel with content preview
    async showCommentDetail(comment, status) {
        const panel = document.getElementById('comment-detail-panel');
        const body = document.getElementById('comment-detail-body');
        const footer = document.getElementById('comment-detail-footer');
        const title = document.getElementById('comment-detail-title');

        title.textContent = comment.title || comment.id;

        body.innerHTML = `
            <div class="comment-detail-meta">
                <div class="comment-detail-row">
                    <span class="comment-detail-label">Status:</span>
                    <span class="comment-detail-value"><span class="comment-status-badge ${status}">${status}</span></span>
                </div>
                <div class="comment-detail-row">
                    <span class="comment-detail-label">In Reply To:</span>
                    <span class="comment-detail-value"><a href="${this.escapeHtml(comment.in_reply_to)}" target="_blank">${this.escapeHtml(this.truncateUrl(comment.in_reply_to))}</a></span>
                </div>
                <div class="comment-detail-row">
                    <span class="comment-detail-label">Submitted:</span>
                    <span class="comment-detail-value">${this.formatDate(comment.timestamp)}</span>
                </div>
            </div>
            <div class="comment-detail-preview">
                <div class="comment-detail-preview-label">Comment</div>
                <div class="comment-detail-preview-content parchment-preview" id="my-comment-preview">
                    <span class="text-muted">Loading comment...</span>
                </div>
            </div>
        `;

        footer.innerHTML = `
            <button class="secondary" onclick="App.closeCommentDetail()">Close</button>
        `;

        panel.classList.remove('hidden');
        this.bindCommentDetailEvents();

        // Fetch and display comment content
        const previewEl = document.getElementById('my-comment-preview');
        if (comment.comment_url) {
            const content = await this.fetchCommentContent(comment.comment_url);
            if (previewEl) {
                if (content) {
                    previewEl.innerHTML = content;
                } else {
                    previewEl.innerHTML = `<a href="${this.escapeHtml(comment.comment_url)}" target="_blank">Open comment in new tab &rarr;</a>`;
                }
            }
        } else if (comment.content) {
            if (previewEl) {
                previewEl.innerHTML = `<pre style="white-space: pre-wrap; margin: 0;">${this.escapeHtml(comment.content)}</pre>`;
            }
        } else {
            if (previewEl) {
                previewEl.innerHTML = '<span class="text-muted">No content preview available</span>';
            }
        }
    },

    // Fetch remote comment/post content for preview
    async fetchCommentContent(url) {
        try {
            const result = await this.api('GET', '/api/remote/post?url=' + encodeURIComponent(url));
            return result.content || '';
        } catch {
            return null;
        }
    },

    // Open pending request detail panel with comment content preview
    async openPendingRequestDetail(index) {
        const request = this._pendingRequests[index];
        const panel = document.getElementById('comment-detail-panel');
        const body = document.getElementById('comment-detail-body');
        const footer = document.getElementById('comment-detail-footer');
        const title = document.getElementById('comment-detail-title');

        title.textContent = 'Blessing Request';

        body.innerHTML = `
            <div class="comment-detail-meta">
                <div class="comment-detail-row">
                    <span class="comment-detail-label">From:</span>
                    <span class="comment-detail-value">${this.escapeHtml(request.author)}</span>
                </div>
                <div class="comment-detail-row">
                    <span class="comment-detail-label">On post:</span>
                    <span class="comment-detail-value"><a href="${this.escapeHtml(request.in_reply_to)}" target="_blank">${this.escapeHtml(this.truncateUrl(request.in_reply_to))}</a></span>
                </div>
                <div class="comment-detail-row">
                    <span class="comment-detail-label">Submitted:</span>
                    <span class="comment-detail-value">${this.formatDate(request.created_at || request.timestamp)}</span>
                </div>
            </div>
            <div class="comment-detail-preview">
                <div class="comment-detail-preview-label">Comment</div>
                <div class="comment-detail-preview-content parchment-preview" id="blessing-comment-preview">
                    <span class="text-muted">Loading comment...</span>
                </div>
            </div>
        `;

        footer.innerHTML = `
            <button class="primary" onclick="App.grantBlessing('${this.escapeHtml(request.comment_version)}', '${this.escapeHtml(request.comment_url)}', '${this.escapeHtml(request.in_reply_to)}'); App.closeCommentDetail();">Bless</button>
            <button class="secondary danger" onclick="App.denyBlessing('${this.escapeHtml(request.comment_url)}', '${this.escapeHtml(request.in_reply_to)}'); App.closeCommentDetail();">Deny</button>
        `;

        panel.classList.remove('hidden');
        this.bindCommentDetailEvents();

        // Fetch and display comment content
        if (request.comment_url) {
            const content = await this.fetchCommentContent(request.comment_url);
            const previewEl = document.getElementById('blessing-comment-preview');
            if (previewEl) {
                if (content) {
                    previewEl.innerHTML = content;
                } else {
                    previewEl.innerHTML = `<a href="${this.escapeHtml(request.comment_url)}" target="_blank">Open comment in new tab &rarr;</a>`;
                }
            }
        }
    },

    // Render combined blessing requests view with tabs (pending/blessed/all)
    _blessingRequestsFilter: 'all',
    async renderBlessingRequests(container, filter) {
        if (filter) this._blessingRequestsFilter = filter;
        const currentFilter = this._blessingRequestsFilter;

        // Fetch both pending requests and blessed comments
        let requests = [];
        let allBlessed = [];
        try {
            const [reqResult, blessedResult] = await Promise.all([
                this.api('GET', '/api/blessing/requests').catch(() => ({ requests: [] })),
                this.api('GET', '/api/blessed-comments').catch(() => ({ comments: [] })),
            ]);
            requests = reqResult.requests || [];
            for (const pc of (blessedResult.comments || [])) {
                for (const c of (pc.blessed || [])) {
                    allBlessed.push({ ...c, post: pc.post, post_title: pc.post_title });
                }
            }
        } catch (err) {
            container.innerHTML = this._renderPostsSubTabs('blessing-requests') + `<div class="post-list"><div class="empty-state"><h3>Failed to load</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
            return;
        }

        // Update counts
        this.counts.incomingPending = requests.length;
        this.counts.incomingBlessed = allBlessed.length;


        // Build filter tabs matching view-tabs style
        const tabClass = (name) => name === currentFilter ? 'tab-item active' : 'tab-item';
        const tabs = `
            <div class="view-tabs" style="margin-bottom: 0.5rem;">
                <div class="tab-group">
                    <button class="${tabClass('all')}" onclick="App.renderBlessingRequests(document.getElementById('content-list'), 'all')">All <span class="tab-count">${requests.length + allBlessed.length}</span></button>
                    <button class="${tabClass('pending')}" onclick="App.renderBlessingRequests(document.getElementById('content-list'), 'pending')">Pending <span class="tab-count">${requests.length}</span></button>
                    <button class="${tabClass('blessed')}" onclick="App.renderBlessingRequests(document.getElementById('content-list'), 'blessed')">Blessed <span class="tab-count">${allBlessed.length}</span></button>
                </div>
            </div>
        `;

        // Store for click handlers (avoids serializing JSON into HTML attributes)
        this._pendingRequests = requests;
        this._blessedComments = allBlessed;

        // Build unified list with type tags for sorting
        let allItems = [];
        if (currentFilter === 'pending' || currentFilter === 'all') {
            allItems.push(...requests.map((r, idx) => ({
                type: 'pending', data: r, idx,
                date: r.created_at || r.timestamp || '',
            })));
        }
        if (currentFilter === 'blessed' || currentFilter === 'all') {
            allItems.push(...allBlessed.map((c, idx) => ({
                type: 'blessed', data: c, idx,
                date: c.blessed_at || '',
            })));
        }
        // Sort newest first
        allItems.sort((a, b) => (b.date || '').localeCompare(a.date || ''));

        let items = allItems.map(item => {
            if (item.type === 'pending') {
                const r = item.data;
                const date = r.created_at || r.timestamp;
                const excerpt = r.excerpt || '';
                const domain = r.author || '';
                // Derive post title from in_reply_to URL
                let postLabel = 'a post';
                if (r.in_reply_to) {
                    postLabel = r.in_reply_to.split('/').pop().replace(/\.(md|html)$/, '').replace(/-/g, ' ');
                }
                const excerptHtml = excerpt ? `<div class="post-excerpt" style="font-size:13px;color:var(--text-secondary);margin-top:4px;line-height:1.4;">${this.escapeHtml(excerpt)}</div>` : '';
                return `
                <div class="post-row" onclick="App.openPendingRequestDetail(${item.idx})">
                    <div class="post-info">
                        <div class="post-title">${this.escapeHtml(postLabel)}</div>
                        ${excerptHtml}
                        <div class="post-meta">
                            <span class="comment-status-badge pending">pending</span>
                            ${domain ? `<span class="sep">&middot;</span><span>${this.escapeHtml(domain)}</span>` : ''}
                        </div>
                    </div>
                    <div class="post-status">
                        <span class="post-date">${this.formatRelativeTime(date)}</span>
                    </div>
                </div>`;
            } else {
                const c = item.data;
                const domain = this.extractDomainFromUrl(c.url) || this.extractDomainFromUrl(c.post);
                const excerpt = c.excerpt || '';
                const postTitle = c.post_title || '';
                // Build post link
                let postUrl = '';
                let postLabel = '';
                if (c.post) {
                    postUrl = c.post.replace(/\.md$/, '.html');
                    postLabel = postTitle || postUrl.split('/').pop().replace(/\.html$/, '').replace(/-/g, ' ');
                }
                const excerptHtml = excerpt ? `<div class="post-excerpt" style="font-size:13px;color:var(--text-secondary);margin-top:4px;line-height:1.4;">${this.escapeHtml(excerpt)}</div>` : '';
                return `
                <div class="post-row" onclick="App.openBlessedCommentDetail(${item.idx})">
                    <div class="post-info">
                        <div class="post-title">${postLabel ? (postUrl ? `<a href="${this.escapeHtml(postUrl)}" onclick="event.stopPropagation()" style="color:var(--text-primary);text-decoration:none;">${this.escapeHtml(postLabel)}</a>` : this.escapeHtml(postLabel)) : this.escapeHtml(c.url ? c.url.split('/').pop() : 'comment')}</div>
                        ${excerptHtml}
                        <div class="post-meta">
                            <span class="comment-status-badge blessed">blessed</span>
                            ${domain ? `<span class="sep">&middot;</span><span>${this.escapeHtml(domain)}</span>` : ''}
                        </div>
                    </div>
                    <div class="post-status">
                        <span class="post-date">${this.formatRelativeTime(c.blessed_at)}</span>
                    </div>
                </div>`;
            }
        }).join('');

        if (!items) {
            const msg = currentFilter === 'pending'
                ? 'No pending blessing requests'
                : currentFilter === 'blessed'
                ? 'No blessed comments yet'
                : 'No blessing requests yet';
            items = `<div class="empty-state"><h3>${msg}</h3><p>When someone comments on your posts, their requests appear here</p></div>`;
        }

        container.innerHTML = this._renderPostsSubTabs('blessing-requests') + `${tabs}<div class="post-list">${items}</div>`;
    },

    // Open blessed comment detail panel with content preview
    async openBlessedCommentDetail(index) {
        const comment = this._blessedComments[index];
        const panel = document.getElementById('comment-detail-panel');
        const body = document.getElementById('comment-detail-body');
        const footer = document.getElementById('comment-detail-footer');
        const title = document.getElementById('comment-detail-title');

        title.textContent = 'Blessed Comment';

        body.innerHTML = `
            <div class="comment-detail-meta">
                <div class="comment-detail-row">
                    <span class="comment-detail-label">On post:</span>
                    <span class="comment-detail-value">${this.escapeHtml(comment.post)}</span>
                </div>
                <div class="comment-detail-row">
                    <span class="comment-detail-label">Version:</span>
                    <span class="comment-detail-value" style="font-family: var(--font-mono); font-size: 0.8rem;">${this.escapeHtml(comment.version)}</span>
                </div>
                <div class="comment-detail-row">
                    <span class="comment-detail-label">Blessed:</span>
                    <span class="comment-detail-value">${this.formatDate(comment.blessed_at)}</span>
                </div>
            </div>
            <div class="comment-detail-preview">
                <div class="comment-detail-preview-label">Comment</div>
                <div class="comment-detail-preview-content parchment-preview" id="blessed-comment-preview">
                    <span class="text-muted">Loading comment...</span>
                </div>
            </div>
        `;

        footer.innerHTML = `
            <button class="secondary danger" onclick="App.revokeBlessing('${this.escapeHtml(comment.url)}'); App.closeCommentDetail();">Revoke Blessing</button>
            <button class="secondary" onclick="App.closeCommentDetail()">Close</button>
        `;

        panel.classList.remove('hidden');
        this.bindCommentDetailEvents();

        // Fetch and display comment content
        if (comment.url) {
            const content = await this.fetchCommentContent(comment.url);
            const previewEl = document.getElementById('blessed-comment-preview');
            if (previewEl) {
                if (content) {
                    previewEl.innerHTML = content;
                } else {
                    previewEl.innerHTML = `<a href="${this.escapeHtml(comment.url)}" target="_blank">Open comment in new tab &rarr;</a>`;
                }
            }
        }
    },

    // Close comment detail panel
    closeCommentDetail() {
        const panel = document.getElementById('comment-detail-panel');
        panel.classList.add('hidden');
    },

    // Bind comment detail panel events
    bindCommentDetailEvents() {
        const closeBtn = document.getElementById('comment-detail-close-btn');
        const overlay = document.querySelector('.comment-detail-overlay');

        closeBtn.onclick = () => this.closeCommentDetail();
        overlay.onclick = () => this.closeCommentDetail();
    },

    // Revoke a blessing (remove from blessed-comments.json)
    async revokeBlessing(commentUrl) {
        const confirmed = await this.showConfirmModal('Revoke Blessing', 'Revoke this blessing? The comment will be removed from your blessed comments index.', 'Revoke', 'Cancel', 'danger');
        if (!confirmed) return;

        try {
            await this.api('POST', '/api/blessing/revoke', {
                comment_url: commentUrl
            });

            this.showToast('Blessing revoked', 'success');
            await this.loadAllCounts();
            await this.loadViewContent();
        } catch (err) {
            this.showToast('Failed to revoke: ' + err.message, 'error');
        }
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
                                        const desc = this.themeDescriptions[t.name] || '';
                                        const label = desc ? `${t.name} — ${desc}` : t.name;
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

    // Theme descriptions for the settings panel
    themeDescriptions: {
        'especial': 'Dark gold and navy, inspired by Modelo Especial.',
        'especial-light': 'Light variant of especial with warm fog tones.',
        'sols': 'Violet and peach, inspired by Nine Sols.',
        'studio13': 'Stark black and burnt orange, late-night studio energy.',
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

    // Save draft
    // Build full markdown with title heading prepended from the title input
    _buildFullMarkdown() {
        const title = (document.getElementById('editor-title-input')?.value || '').trim();
        const body = this.getEditorContent('markdown-input');
        if (title) {
            return `# ${title}\n\n${body}`;
        }
        return body;
    },

    async saveDraft(silent = false) {
        const markdown = this._buildFullMarkdown();

        if (!markdown.trim()) {
            if (!silent) this.showToast('Nothing to save', 'warning');
            return;
        }

        // Extract title from first heading or first line
        let title = 'untitled';
        const lines = markdown.split('\n');
        for (const line of lines) {
            const trimmed = line.trim();
            if (trimmed) {
                const headingMatch = trimmed.match(/^#+\s+(.+)$/);
                if (headingMatch) {
                    title = headingMatch[1];
                } else {
                    title = trimmed.substring(0, 50);
                }
                break;
            }
        }

        const id = this.currentDraftId || this.slugify(title);
        const status = document.getElementById('editor-save-status');

        if (silent) {
            status.textContent = 'Saving...';
            status.classList.remove('saved');
        }

        try {
            const result = await this.api('POST', '/api/drafts', { id, markdown });
            this.currentDraftId = result.id;
            if (silent) {
                status.textContent = 'Saved';
                status.classList.add('saved');
            } else {
                this.showToast('Draft saved', 'success');
                status.textContent = 'Saved';
                status.classList.add('saved');
            }
        } catch (err) {
            if (silent) {
                status.textContent = 'Save failed';
                status.classList.remove('saved');
            } else {
                this.showToast('Failed to save draft: ' + err.message, 'error');
            }
        }
    },

    // Publish or republish post
    async publish() {
        const markdown = this._buildFullMarkdown();

        if (!markdown.trim()) {
            this.showToast('Nothing to publish', 'warning');
            return;
        }

        const isRepublish = !!this.currentPostPath;
        const title = isRepublish ? 'Republish Post' : 'Publish Post';
        const message = isRepublish
            ? 'This post will be re-signed with an updated version.'
            : 'This post will be signed and saved to your posts directory.';
        const buttonText = isRepublish ? 'Republish' : 'Publish';

        const confirmed = await this.showConfirmModal(title, message, buttonText);
        if (!confirmed) {
            return;
        }

        const btn = document.getElementById('publish-btn');
        btn.classList.add('btn-loading');
        btn.disabled = true;

        try {
            let result;
            if (isRepublish) {
                result = await this.api('POST', '/api/republish', {
                    path: this.currentPostPath,
                    markdown
                });
            } else {
                // Use filename from input, fall back to auto-generated from title
                const filenameInput = document.getElementById('filename-input').value.trim();
                result = await this.api('POST', '/api/publish', {
                    markdown,
                    filename: filenameInput || '',
                    draft_id: this.currentDraftId || ''
                });
            }

            if (result.success) {
                const action = isRepublish ? 'Republished' : 'Published';
                this.showToast(`${action}: ${result.title}`, 'success');

                // Clear editor and return to dashboard
                this.currentDraftId = null;
                this.currentPostPath = null;
                this.setEditorContent('markdown-input', '');

                // Switch to Published view
                this.currentView = 'posts-published';
                await this.loadAllCounts();
                await this.loadViewContent();

                // Show broadcast pulse on the newly published post
                if (!isRepublish) {
                    const postPath = result.path || '';
                    const item = postPath
                        ? document.querySelector(`.content-item[data-path="${CSS.escape(postPath)}"]`)
                        : document.querySelector('.content-item');
                    this.showBroadcastPulse(item);
                }
                this.updatePublishButton();
                this.showScreen('dashboard');
                window.history.replaceState({}, '', this.pathForView('posts-published'));

                // Update sidebar active state
                this._updateSidebarActiveItem('posts-published');
            }
        } catch (err) {
            this.showToast('Failed to publish: ' + err.message, 'error');
        } finally {
            btn.classList.remove('btn-loading');
            btn.disabled = false;
        }
    },

    // Open a draft for editing
    async openDraft(id, opts = {}) {
        try {
            const result = await this.api('GET', `/api/drafts/${encodeURIComponent(id)}`);
            this.currentDraftId = id;
            this.currentPostPath = null;
            this.currentFrontmatter = '';
            this.filenameManuallySet = true;  // Draft already has a filename
            // Extract title into separate input, load body without heading into editor
            const draftTitle = this.extractTitleFromMarkdown(result.markdown);
            document.getElementById('editor-title-input').value = draftTitle || '';
            const titleRow = document.querySelector('.editor-title-row');
            if (titleRow) titleRow.classList.toggle('has-value', !!(draftTitle || '').trim());
            this.setEditorContent('markdown-input', this._stripTitleHeading(result.markdown));
            document.getElementById('filename-input').value = id;  // Draft ID is the filename
            document.getElementById('filename-input').disabled = false;

            this.updatePublishButton();
            if (opts.pushState !== false) {
                window.history.pushState({}, '', this.pathForScreen('openDraft', { id }));
            }
            this.showScreen('editor');
        } catch (err) {
            this.showToast('Failed to load draft: ' + err.message, 'error');
        }
    },

    // Delete a draft
    async deleteDraft(id) {
        const confirmed = await this.showConfirmModal(
            'Delete Draft',
            'This draft will be permanently deleted.',
            'Delete',
            'Cancel',
            'danger',
        );
        if (!confirmed) return;

        try {
            await this.api('DELETE', `/api/drafts/${encodeURIComponent(id)}`);
            this.showToast('Draft deleted', 'success');

            // If we're in the editor viewing this draft, go back
            if (this.currentDraftId === id) {
                this.currentDraftId = null;
                this.setEditorContent('markdown-input', '');
                this.currentView = 'posts-published';
                await this.loadAllCounts();
                await this.loadViewContent();
                this.showScreen('dashboard');
                window.history.replaceState({}, '', this.pathForView('posts-published'));
            } else {
                // Refresh the list
                await this.loadAllCounts();
                await this.loadViewContent();
            }
        } catch (err) {
            this.showToast('Failed to delete draft: ' + err.message, 'error');
        }
    },

    // Unpublish a post (remove from site and discovery)
    async unpublishPost(path) {
        const confirmed = await this.showConfirmModal(
            'Unpublish Post',
            'This will remove the post from your site and from discovery. Comments on other sites will remain. This cannot be undone.',
            'Unpublish',
            'Cancel',
            'danger',
        );
        if (!confirmed) return;

        try {
            await this.api('POST', '/api/unpublish', { path });
            this.showToast('Post unpublished', 'success');
            this.loadViewContent();
        } catch (err) {
            this.showToast(err.message || 'Failed to unpublish', 'error');
        }
    },

    // Open a published post for editing
    async openPost(path, opts = {}) {
        try {
            // Strip .md extension if present (normalize URL)
            const cleanPath = path.endsWith('.md') ? path.slice(0, -3) : path;
            const result = await this.api('GET', `/api/posts/${encodeURIComponent(path)}`);
            this.currentDraftId = null;
            this.currentPostPath = path;
            // Store frontmatter separately — don't expose it in the textarea
            this.currentFrontmatter = '';
            if (result.raw_markdown) {
                const fmMatch = result.raw_markdown.match(/^---\r?\n[\s\S]*?\r?\n---\r?\n?/);
                if (fmMatch) this.currentFrontmatter = fmMatch[0];
            }
            // Extract title into separate input, load body without heading into editor
            const postTitle = this.extractTitleFromMarkdown(result.markdown);
            document.getElementById('editor-title-input').value = postTitle || '';
            const titleRow = document.querySelector('.editor-title-row');
            if (titleRow) titleRow.classList.toggle('has-value', !!(postTitle || '').trim());
            this.setEditorContent('markdown-input', this._stripTitleHeading(result.markdown));

            this.updatePublishButton();
            if (opts.pushState !== false) {
                window.history.pushState({}, '', this.pathForScreen('openPost', { path: cleanPath }));
            }
            this.showScreen('editor');
        } catch (err) {
            this.showToast('Failed to load post: ' + err.message, 'error');
        }
    },

    // Update publish button text and delete-draft button visibility based on current state
    updatePublishButton() {
        const btn = document.getElementById('publish-btn');
        const deleteBtn = document.getElementById('delete-draft-btn');
        if (this.currentPostPath) {
            btn.textContent = 'Republish';
        } else {
            btn.textContent = 'Publish';
        }
        // Show delete-draft button only when editing a draft
        if (this.currentDraftId && !this.currentPostPath) {
            deleteBtn.classList.remove('hidden');
        } else {
            deleteBtn.classList.add('hidden');
        }
    },

    // Open a comment draft for editing
    async openCommentDraft(id, opts = {}) {
        try {
            const draft = await this.api('GET', `/api/comments/drafts/${encodeURIComponent(id)}`);
            this.currentCommentDraftId = id;
            document.getElementById('reply-to-url').value = draft.in_reply_to || '';
            this.setEditorContent('comment-input', draft.content || '');
            if (opts.pushState !== false) {
                window.history.pushState({}, '', this.pathForScreen('openCommentDraft', { id }));
            }
            this.showScreen('comment');
        } catch (err) {
            this.showToast('Failed to load draft: ' + err.message, 'error');
        }
    },

    // Save comment draft
    async saveCommentDraft() {
        const inReplyTo = document.getElementById('reply-to-url').value.trim();
        const content = this.getEditorContent('comment-input');

        if (!inReplyTo) {
            this.showToast('Please enter the URL of the post you are replying to', 'warning');
            return;
        }

        try {
            const result = await this.api('POST', '/api/comments/drafts', {
                id: this.currentCommentDraftId || '',
                in_reply_to: inReplyTo,
                content: content
            });
            this.currentCommentDraftId = result.id;
            this.showToast('Comment draft saved', 'success');
        } catch (err) {
            this.showToast('Failed to save draft: ' + err.message, 'error');
        }
    },

    // Sign and send comment for blessing
    async signAndSendComment() {
        const inReplyTo = document.getElementById('reply-to-url').value.trim();
        const content = this.getEditorContent('comment-input');

        if (!inReplyTo) {
            this.showToast('Please enter the URL of the post you are replying to', 'warning');
            return;
        }

        if (!content.trim()) {
            this.showToast('Please write a comment', 'warning');
            return;
        }

        const confirmed = await this.showConfirmModal('Send for Blessing', 'Sign this comment and send it for blessing? The post author will need to approve it.', 'Sign & Send', 'Cancel');
        if (!confirmed) return;

        const btn = document.getElementById('sign-send-btn');
        btn.classList.add('btn-loading');
        btn.disabled = true;

        try {
            // First sign the comment
            const signResult = await this.api('POST', '/api/comments/sign', {
                draft_id: this.currentCommentDraftId || '',
                in_reply_to: inReplyTo,
                content: content
            });

            if (!signResult.success) {
                throw new Error('Failed to sign comment');
            }

            // Try to send for blessing
            try {
                const beseechResult = await this.api('POST', '/api/comments/beseech', {
                    comment_id: signResult.comment.id
                });

                if (beseechResult.status === 'blessed') {
                    this.showToast('Your comment was auto-blessed!', 'success');
                } else {
                    this.showToast('Comment signed and sent for blessing', 'success');
                }
            } catch (beseechErr) {
                this.showToast('Comment signed. Could not send blessing request: ' + beseechErr.message, 'warning', 6000);
            }

            // Capture intent state before clearing
            const wasFromIntent = !!this._intentComment;
            const intentTarget = wasFromIntent ? this._intentComment.target : inReplyTo;

            // Clear form and return to dashboard
            this.currentCommentDraftId = null;
            this._intentComment = null;
            document.getElementById('reply-to-url').value = '';
            this.setEditorContent('comment-input', '');

            // Switch to comments published view with pending filter
            this._commentsPublishedFilter = 'pending';
            this.currentView = 'comments-published';
            await this.loadAllCounts();
            this.updateSidebar(); // Ensure lifecycle recalculated after count update
            // Notification fetch disabled — UI bell removed
            // this.fetchNotificationCount();
            await this.loadViewContent();
            this.showScreen('dashboard');
            window.history.replaceState({}, '', this.basePath + '/comments/pending');
            this._updateSidebarActiveItem('comments-published');

            // Show intent-aware CTAs if comment was from an intent param
            if (wasFromIntent) {
                this.showCommentIntentResult(
                    signResult.comment ? signResult.comment.url : '',
                    intentTarget
                );
            }
        } catch (err) {
            this.showToast('Failed to sign comment: ' + err.message, 'error');
        } finally {
            btn.classList.remove('btn-loading');
            btn.disabled = false;
        }
    },

    // Sync pending comments
    async syncComments() {
        const syncBtn = document.getElementById('sync-comments-btn');
        if (!syncBtn) return;

        syncBtn.classList.add('btn-loading');
        syncBtn.disabled = true;

        try {
            const result = await this.api('POST', '/api/comments/sync');

            let messages = [];
            if (result.blessed && result.blessed.length > 0) {
                messages.push(`${result.blessed.length} blessed`);
            }
            if (result.denied && result.denied.length > 0) {
                messages.push(`${result.denied.length} denied`);
            }
            if (result.still_pending && result.still_pending.length > 0) {
                messages.push(`${result.still_pending.length} still pending`);
            }

            const message = messages.length > 0 ? messages.join(', ') : 'No changes';
            this.showToast(`Sync complete: ${message}`, 'success');

            await this.loadAllCounts();
            await this.loadViewContent();
        } catch (err) {
            this.showToast('Sync failed: ' + err.message, 'error');
        } finally {
            syncBtn.classList.remove('btn-loading');
            syncBtn.disabled = false;
        }
    },

    // Grant blessing to an incoming comment request
    async grantBlessing(commentVersion, commentUrl, inReplyTo) {
        const confirmed = await this.showConfirmModal('Bless Comment', 'Bless this comment? It will be added to your blessed comments index.', 'Bless', 'Cancel');
        if (!confirmed) return;

        try {
            await this.api('POST', '/api/blessing/grant', {
                comment_version: commentVersion,
                comment_url: commentUrl,
                in_reply_to: inReplyTo
            });

            this.showToast('Comment blessed!', 'success');

            // Post-action suggestion: follow the commenter back
            try {
                const commenterDomain = new URL(commentUrl).hostname;
                if (commenterDomain && this.siteBaseUrl) {
                    const myDomain = new URL(this.siteBaseUrl).hostname;
                    if (commenterDomain !== myDomain) {
                        this.showSuggestion(
                            `Follow <strong>${this.escapeHtml(commenterDomain)}</strong> back? ` +
                            `<button onclick="App.quickFollow('${this.escapeHtml(commenterDomain)}'); this.textContent='Following!'; this.disabled=true;" style="background:var(--teal);color:var(--bg-color);border:none;padding:2px 8px;border-radius:3px;font-family:inherit;cursor:pointer;font-size:.75rem;">Follow</button>`
                        );
                    }
                }
            } catch (e) { /* non-fatal */ }

            await this.loadAllCounts();
            await this.loadViewContent();
        } catch (err) {
            this.showToast('Failed to bless: ' + err.message, 'error');
        }
    },

    // Deny blessing to an incoming comment request
    async denyBlessing(commentURL, inReplyTo) {
        const confirmed = await this.showConfirmModal('Deny Blessing', 'Deny this blessing request? The commenter will be notified.', 'Deny', 'Cancel', 'danger');
        if (!confirmed) return;

        try {
            await this.api('POST', '/api/blessing/deny', {
                comment_url: commentURL,
                in_reply_to: inReplyTo
            });

            this.showToast('Blessing denied', 'success');
            await this.loadAllCounts();
            await this.loadViewContent();
        } catch (err) {
            this.showToast('Failed to deny: ' + err.message, 'error');
        }
    },

    // About page editor (full-screen, matches post editor pattern)

    async openAboutEditor() {
        try {
            const result = await this.api('GET', '/api/about');
            this.setEditorContent('about-editor-textarea', result.content || '');
            this.showScreen('about');
        } catch (err) {
            this.showToast('Failed to load about content: ' + err.message, 'error');
        }
    },

    // Update the about editor live preview
    async updateAboutPreview() {
        const preview = document.getElementById('about-editor-preview');
        if (!preview) return;

        const content = this.getEditorContent('about-editor-textarea');
        if (!content.trim()) {
            preview.innerHTML = '<p class="empty-state">Start writing to see a preview.</p>';
            return;
        }

        try {
            const result = await this.api('POST', '/api/render', { markdown: content });
            preview.innerHTML = result.html || '<p class="empty-state">Start writing to see a preview.</p>';
        } catch (err) {
            preview.innerHTML = `<pre style="white-space: pre-wrap;">${this.escapeHtml(content)}</pre>`;
        }
    },

    // Publish the about page content
    async publishAbout() {
        const btn = document.getElementById('about-publish-btn');
        btn.classList.add('btn-loading');
        btn.disabled = true;

        try {
            const content = this.getEditorContent('about-editor-textarea');
            await this.api('POST', '/api/about', { content });
            await this.navigateTo('/posts');
            this.showToast('About page published', 'success');
        } catch (err) {
            this.showToast('Failed to publish about page: ' + err.message, 'error');
        } finally {
            btn.classList.remove('btn-loading');
            btn.disabled = false;
        }
    },

    // Truncate URL for display
    truncateUrl(url) {
        if (!url) return '';
        let display = url.replace(/^https?:\/\//, '');
        if (display.length > 50) {
            display = display.substring(0, 47) + '...';
        }
        return display;
    },

    // Utility: extract title from markdown (first # heading)
    extractTitleFromMarkdown(markdown) {
        const lines = markdown.split('\n');
        for (const line of lines) {
            const trimmed = line.trim();
            if (trimmed.startsWith('# ')) {
                return trimmed.substring(2).trim();
            }
        }
        return '';
    },

    // Strip the first H1 heading from markdown (used when loading into editor
    // since the title is displayed in a separate input field)
    _stripTitleHeading(markdown) {
        const lines = markdown.split('\n');
        for (let i = 0; i < lines.length; i++) {
            const trimmed = lines[i].trim();
            if (trimmed === '') continue;
            if (trimmed.startsWith('# ')) {
                // Remove the heading line and any immediately following blank line
                lines.splice(i, 1);
                if (i < lines.length && lines[i].trim() === '') {
                    lines.splice(i, 1);
                }
                return lines.join('\n');
            }
            break; // First non-empty line isn't a heading — don't strip anything
        }
        return markdown;
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

    // ==================== Conversations (Tabbed) ====================

    setConversationsSubtab(tab) {
        this._conversationsSubtab = tab;
        const contentList = document.getElementById('content-list');
        if (contentList) this.renderConversationsTabbed(contentList);
    },

    toggleFeedFilter() {
        this._feedShowNew = !this._feedShowNew;
        const contentList = document.getElementById('content-list');
        if (contentList) this.renderConversationsTabbed(contentList);
    },

    setFeedAuthorFilter(domain) {
        this._feedAuthorFilter = domain || null;
        const contentList = document.getElementById('content-list');
        if (contentList) this.renderConversationsTabbed(contentList);
    },

    clearFeedAuthorFilter() {
        this._feedAuthorFilter = null;
        const contentList = document.getElementById('content-list');
        if (contentList) this.renderConversationsTabbed(contentList);
    },

    setFeedTimeFilter(value) {
        this._feedTimeFilter = value || '24h';
        const contentList = document.getElementById('content-list');
        if (contentList) this.renderConversationsTabbed(contentList);
    },

    // Natural-language sentence filter methods
    setFeedSentenceFilter(filterName, value) {
        switch (filterName) {
            case 'readState':
                this._feedShowNew = (value === 'New');
                break;
            case 'contentType': {
                const typeMap = { 'posts': 'post', 'comments': 'comment', 'announcements': 'announcement', 'items': '' };
                this._feedContentType = typeMap[value] || '';
                break;
            }
            case 'scope': {
                const scopeMap = { 'me': 'me', 'my network': 'network', 'my followers': 'followers', 'all of polis': 'global' };
                const newScope = scopeMap[value] || 'network';
                if (newScope !== this._feedScope) {
                    this._feedScope = newScope;
                    this._closeFeedPopovers();
                    // Scope changes need a full re-fetch (different cache file)
                    this._feedFilterOnly = false;
                    const contentList = document.getElementById('content-list');
                    if (contentList) this.renderConversationsTabbed(contentList);
                    return;
                }
                break;
            }
            case 'timeRange': {
                const timeMap = {
                    'in the last hour': '1h', 'in the last day': '24h',
                    'in the last 2 days': '2d', 'in the last week': '7d',
                    'in the last month': '30d'
                };
                this._feedTimeFilter = timeMap[value] || '24h';
                break;
            }
        }
        this._closeFeedPopovers();
        // Skip auto-refresh on filter-only changes (avoids unnecessary DS sync)
        this._feedFilterOnly = true;
        const contentList = document.getElementById('content-list');
        if (contentList) this.renderConversationsTabbed(contentList);
    },

    openFeedPopover(filterName) {
        if (this._feedPopoverOpen === filterName) {
            this._closeFeedPopovers();
            return;
        }
        this._closeFeedPopovers();
        this._feedPopoverOpen = filterName;
        const word = document.querySelector(`.feed-filter-word[data-filter="${filterName}"]`);
        const popover = document.querySelector(`.feed-popover[data-for="${filterName}"]`);
        const overlay = document.getElementById('feed-filter-overlay');
        if (word) word.classList.add('active');
        if (popover) popover.classList.add('visible');
        if (overlay) overlay.classList.add('active');
    },

    _closeFeedPopovers() {
        this._feedPopoverOpen = null;
        document.querySelectorAll('.feed-filter-word.active').forEach(el => el.classList.remove('active'));
        document.querySelectorAll('.feed-popover.visible').forEach(el => el.classList.remove('visible'));
        const overlay = document.getElementById('feed-filter-overlay');
        if (overlay) overlay.classList.remove('active');
    },

    _buildFeedSentenceFilter(itemCount) {
        const readLabel = this._feedShowNew ? 'New' : 'All';
        const contentLabels = { '': 'items', 'post': 'posts', 'comment': 'comments', 'announcement': 'announcements' };
        const contentLabel = contentLabels[this._feedContentType] || 'items';
        const scopeLabels = { 'me': 'me', 'network': 'my network', 'followers': 'my followers', 'global': 'all of polis' };
        const scopeLabel = scopeLabels[this._feedScope] || 'my network';
        const timeLabels = { '1h': 'in the last hour', '24h': 'in the last day', '2d': 'in the last 2 days', '7d': 'in the last week', '30d': 'in the last month' };
        const timeLabel = timeLabels[this._feedTimeFilter] || 'in the last day';

        const isGlobal = this._feedScope === 'global';
        const timeOptions = isGlobal
            ? [['in the last hour', '1h'], ['in the last day', '24h']]
            : [['in the last hour', '1h'], ['in the last day', '24h'], ['in the last 2 days', '2d'], ['in the last week', '7d'], ['in the last month', '30d']];

        const word = (filterName, label, options) => {
            const optHtml = options.map(([lbl]) =>
                `<button class="feed-pop-opt${lbl === label ? ' selected' : ''}" onclick="App.setFeedSentenceFilter('${filterName}','${lbl.replace(/'/g, "\\'")}')">` +
                `<span class="feed-pop-dot"></span>${this.escapeHtml(lbl)}</button>`
            ).join('');
            return `<span class="feed-filter-anchor"><span class="feed-filter-word${this._feedPopoverOpen === filterName ? ' active' : ''}" data-filter="${filterName}" onclick="App.openFeedPopover('${filterName}')">${this.escapeHtml(label)}<span class="feed-caret"></span></span>` +
                `<div class="feed-popover${this._feedPopoverOpen === filterName ? ' visible' : ''}" data-for="${filterName}">${optHtml}</div></span>`;
        };

        return `
            <div id="feed-filter-overlay" class="feed-overlay${this._feedPopoverOpen ? ' active' : ''}" onclick="App._closeFeedPopovers()"></div>
            <div class="feed-sentence-area">
                <div class="feed-sentence">
                    ${word('readState', readLabel, [['New'], ['All']])}
                    ${word('contentType', contentLabel, [['posts'], ['comments'], ['announcements'], ['items']])}
                    from
                    ${word('scope', scopeLabel, [['my network'], ['my followers'], ['all of polis'], ['me']])}
                    ${word('timeRange', timeLabel, timeOptions)}
                    <svg class="feed-sentence-return" viewBox="0 0 18 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2 2h12v10"/><polyline points="11 9 14 12 17 9"/></svg>
                </div>
            </div>
        `;
    },

    async renderConversationsTabbed(container) {
        // Clear the feed dot immediately and advance the viewed timestamp
        this.counts.hasNewFeed = false;
        this._updateTopbarBadges();
        this.api('POST', '/api/feed/viewed').catch(() => {});
        await this._renderAllSubtab(container, '');
    },

    _titleFromUrl(url) {
        if (!url) return '(untitled)';
        try {
            const filename = new URL(url).pathname.split('/').pop().replace(/\.(md|html)$/, '');
            return filename.split(/[-_]/).map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
        } catch (e) {
            return '(untitled)';
        }
    },

    _renderAnnouncementItem(a) {
        const typeLabels = {
            'pub.polis.follow.announced': 'followed someone',
            'pub.polis.comment.blessing.granted': 'granted a blessing',
            'pub.polis.comment.blessing.requested': 'requested a blessing',
            'pub.polis.site.registered': 'joined polis',
        };
        const actionLabel = typeLabels[a.event_type] || a.event_type;
        const actor = (a.author_domain || '').toLowerCase();
        let detail = '';
        if (a.title) {
            detail = ` — <span class="activity-detail">${this.escapeHtml(a.title)}</span>`;
        } else if (a.target_domain) {
            detail = ` — <span class="activity-detail">${this.escapeHtml(a.target_domain)}</span>`;
        }
        return `
            <div class="activity-item${a.unread ? ' unread' : ''}">
                <span class="act-dot"></span>
                <span><span class="act-who">${this.escapeHtml(actor)}</span> ${actionLabel}${detail}</span>
                <span class="act-when">${this.formatRelativeTime(a.published)}</span>
            </div>
        `;
    },

    _renderGroupedItem(group) {
        const isUnread = group.post_unread || group.unread_comments > 0;
        const title = group.post_title || this._titleFromUrl(group.post_url);
        const linkUrl = group.post_url ? group.post_url.replace(/\.md$/, '.html') : '#';
        const ids = JSON.stringify(group.item_ids);
        const domain = group.post_domain || '';
        const avatar = this.domainToAvatar(domain);
        const myDomain = this.siteBaseUrl ? (() => { try { return new URL(this.siteBaseUrl).hostname; } catch(e) { return ''; } })() : '';
        const isMyDomain = domain && myDomain && domain === myDomain;
        const remoteCached = !isMyDomain && domain ? this._remoteAvatarCache[domain] : null;
        const hasCustomAvatar = (isMyDomain && this.avatarConfig) || (remoteCached && remoteCached.avatar);
        const customAvatar = isMyDomain ? this.avatarConfig : (remoteCached ? remoteCached.avatar : null);
        const defaultName = domain.replace(/\.polis\.pub$/, '').replace(/\./g, ' ');
        const authorName = (isMyDomain && this.authorName) ? this.authorName : (remoteCached && remoteCached.author_name) ? remoteCached.author_name : defaultName;
        const time = this.formatRelativeTime(group.last_activity);

        // Build comment detail cards if available
        let commentsHtml = '';
        const comments = group.comments || [];
        if (comments.length > 0) {
            const shown = comments.slice(0, 3); // Show up to 3 inline
            commentsHtml = shown.map(c => {
                const cDomain = c.author_domain || '';
                const cName = cDomain.replace(/\.polis\.pub$/, '').replace(/\./g, ' ');
                const cAvatar = this.domainToAvatar(cDomain);
                const cText = c.excerpt || c.title || '';
                const cTime = this.formatRelativeTime(c.published);
                return `
                    <div class="comment-detail${c.unread ? ' unread' : ''}">
                        <div class="comment-detail-header">
                            <div class="comment-detail-avatar" style="background: ${cAvatar.color};">${cAvatar.initials}</div>
                            <span class="comment-detail-author">${this.escapeHtml(cName)}</span>
                            <span class="comment-detail-domain">&middot; ${this.escapeHtml(cDomain)}</span>
                            <span class="comment-detail-time">${this.escapeHtml(cTime)}</span>
                        </div>
                        ${cText ? `<div class="comment-detail-text">${this.escapeHtml(cText)}</div>` : ''}
                    </div>`;
            }).join('');
            if (comments.length > 3) {
                commentsHtml += `<div class="comment-detail-more">+ ${comments.length - 3} more</div>`;
            }
        }

        let threadHtml = '';
        if (group.total_comments > 0) {
            threadHtml = `
                <div class="comment-details-section">${commentsHtml}</div>
                <div class="thread-row">
                    <svg class="thread-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
                        <path d="M4 6v6h8" stroke-linecap="round" stroke-linejoin="round"/>
                    </svg>
                    <span>${group.total_comments} ${group.total_comments === 1 ? 'reply' : 'replies'}</span>
                    <button class="reply-btn" onclick="event.stopPropagation(); App.newCommentDraft('${this.escapeHtml(linkUrl)}')">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <polyline points="9 17 4 12 9 7"/>
                            <path d="M20 18v-2a4 4 0 0 0-4-4H4"/>
                        </svg>
                        Reply
                    </button>
                </div>`;
        } else {
            threadHtml = `
                <div class="thread-row" style="color: var(--text-faint);">
                    <svg class="thread-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
                        <path d="M4 6v6h8" stroke-linecap="round" stroke-linejoin="round"/>
                    </svg>
                    <span>No replies yet</span>
                    <button class="reply-btn" onclick="event.stopPropagation(); App.newCommentDraft('${this.escapeHtml(linkUrl)}')">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <polyline points="9 17 4 12 9 7"/>
                            <path d="M20 18v-2a4 4 0 0 0-4-4H4"/>
                        </svg>
                        Reply
                    </button>
                </div>`;
        }

        return `
            <div class="feed-item${isUnread ? ' unread' : ''}"
               data-ids='${ids}'
               data-url="${this.escapeHtml(linkUrl)}"
               onclick="App._handleGroupClick(this)">
                <div class="item-hover-actions">
                    <button class="hover-btn hover-btn-tag" onclick="event.stopPropagation(); App.showTagInput(this, '${this.escapeHtml(linkUrl)}')" title="Tag this post">Tag</button>
                </div>
                <div class="item-top">
                    <div class="author-row" onclick="event.stopPropagation(); window.open('https://${this.escapeHtml(domain)}', '_blank')" title="${this.escapeHtml(domain)}">
                        <div class="author-avatar" style="${hasCustomAvatar ? this._buildAvatarStyle(customAvatar) : `background: ${avatar.color};`}">${hasCustomAvatar ? '' : avatar.initials}</div>
                        <div>
                            <span class="author-name">${this.escapeHtml(authorName)}</span>
                            <span class="author-domain">&middot; ${this.escapeHtml(domain)}</span>
                        </div>
                    </div>
                    <span class="item-time">${this.escapeHtml(time)}</span>
                </div>
                ${(() => { const strip = s => s.toLowerCase().replace(/[`*_~\[\]'"]/g, ''); return group.post_excerpt && strip(group.post_excerpt).startsWith(strip(title).slice(0, 30)); })()
                    ? `<div class="item-excerpt">${this.escapeHtml(group.post_excerpt)}</div>`
                    : `<div class="item-title">${this.escapeHtml(title)}</div>${group.post_excerpt ? `<div class="item-excerpt">${this.escapeHtml(group.post_excerpt)}</div>` : ''}`}
                ${threadHtml}
            </div>
        `;
    },

    _handleGroupClick(el) {
        const url = el.dataset.url || '';
        if (url && url !== '#') window.location.href = url;
    },

    async _renderPostsCommentsSubtab(container, filterHtml) {
        try {
            container.innerHTML = filterHtml + '<div class="feed-list"><div class="empty-state"><p>Loading...</p></div></div>';
            const result = await this.api('GET', '/api/feed/grouped');
            const groups = result.groups || [];

            this.counts.feedUnread = result.unread_items || 0;

            this._updateTopbarBadges();

            if (groups.length === 0) {
                const emptyMsg = this.counts.following === 0
                    ? `<h3>No posts or comments yet</h3><p>Follow someone to see their posts here.</p><button class="primary" onclick="App.openFollowPanel()">Follow an author</button>`
                    : `<h3>No items</h3><p>No posts or comments in the feed yet. Click Refresh to check for new content.</p>`;
                container.innerHTML = filterHtml + `<div class="feed-list"><div class="empty-state">${emptyMsg}</div></div>`;

                if (!this._conversationsRefreshing && !this._feedFilterOnly) this._autoRefreshConversations();
            this._feedFilterOnly = false;
                return;
            }

            container.innerHTML = filterHtml + `
                <div class="feed-list">
                    ${groups.map(g => this._renderGroupedItem(g)).join('')}
                </div>
            `;

            // Fetch remote avatars for domains not yet cached
            this._fetchRemoteAvatarsForGroups(groups);

            if (!this._conversationsRefreshing && !this._feedFilterOnly) this._autoRefreshConversations();
            this._feedFilterOnly = false;
        } catch (err) {
            container.innerHTML = filterHtml + `<div class="feed-list"><div class="empty-state"><h3>Failed to load</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
        }
    },

    _mergeActivityEvents(newEvents) {
        if (!newEvents || newEvents.length === 0) return;
        const existing = new Set(this._activityEvents.map(e => e.id));
        for (const evt of newEvents) {
            if (!existing.has(evt.id)) {
                this._activityEvents.push(evt);
            }
        }
        if (this._activityEvents.length > this._activityMaxEvents) {
            this._activityEvents = this._activityEvents.slice(
                this._activityEvents.length - this._activityMaxEvents
            );
        }
    },

    async _renderActivitySubtab(container, filterHtml) {
        try {
            container.innerHTML = filterHtml + '<div class="content-list"><div class="empty-state"><p>Loading activity...</p></div></div>';
            const result = await this.api('GET', `/api/activity?since=${this._activityCursor}&limit=100`);
            const events = result.events || [];

            if (events.length > 0) {
                this._mergeActivityEvents(events);
                this._activityCursor = result.cursor || this._activityCursor;
            }

            if (this._activityEvents.length === 0) {
                container.innerHTML = filterHtml + `<div class="content-list"><div class="empty-state">
                    <h3>No activity yet</h3>
                    <p>Follow some authors to see their activity here.</p>
                </div></div>`;
                return;
            }

            const hasMore = result.has_more;
            container.innerHTML = filterHtml + `
                <div class="content-list">
                    ${[...this._activityEvents].reverse().map(evt => this.renderActivityEvent(evt)).join('')}
                </div>
                ${hasMore ? '<div class="activity-load-more"><button class="secondary" onclick="App.loadMoreActivity()">Load More</button></div>' : ''}
            `;
        } catch (err) {
            container.innerHTML = filterHtml + `<div class="content-list"><div class="empty-state"><h3>Failed to load activity</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
        }
    },

    async _renderAllSubtab(container, filterHtml) {
        try {
            container.innerHTML = filterHtml + '<div class="feed-list"><div class="empty-state"><p>Loading...</p></div></div>';

            // Fetch grouped feed (includes announcements)
            const scopeParam = this._feedScope && this._feedScope !== 'network' ? `?scope=${this._feedScope}` : '';
            const groupedResult = await this.api('GET', `/api/feed/grouped${scopeParam}`);

            const groups = groupedResult.groups || [];
            const announcements = groupedResult.announcements || [];
            this.counts.feedUnread = groupedResult.unread_items || 0;

            this._updateTopbarBadges();

            // Build merged timeline entries from groups + announcements
            const entries = [];
            for (const g of groups) {
                entries.push({ type: 'group', data: g, timestamp: g.last_activity });
            }
            for (const a of announcements) {
                entries.push({ type: 'announcement', data: a, timestamp: a.published });
            }

            // Sort by timestamp descending (parse to handle non-ISO formats)
            entries.sort((a, b) => {
                const ta = a.timestamp ? new Date(a.timestamp).getTime() : 0;
                const tb = b.timestamp ? new Date(b.timestamp).getTime() : 0;
                return (tb || 0) - (ta || 0);
            });

            // Auto-sync scoped feeds when stale or never-synced
            if (groupedResult.stale && this._feedScope && this._feedScope !== 'network' && !this._feedScopeSyncing) {
                this._feedScopeSyncing = true;
                container.innerHTML = filterHtml + '<div class="feed-list"><div class="empty-state"><p>Syncing...</p></div></div>';
                await this.api('POST', `/api/feed/refresh?scope=${this._feedScope}`);
                this._feedScopeSyncing = false;
                return this._renderAllSubtab(container, filterHtml);
            }
            this._feedScopeSyncing = false;

            if (entries.length === 0) {
                const emptyMsg = this.counts.following === 0
                    ? `<h3>Your feed is empty</h3><p>Follow someone to see their posts here.</p><button class="primary" onclick="App.openFollowPanel()">Follow an author</button>`
                    : `<h3>Nothing new</h3><p>No items yet. Click Refresh to check for new content.</p>`;
                const emptySentence = this._buildFeedSentenceFilter(0);
                container.innerHTML = filterHtml + emptySentence + `<div class="feed-list"><div class="empty-state">${emptyMsg}</div></div>`;

                if (!this._conversationsRefreshing && !this._feedFilterOnly) this._autoRefreshConversations();
                this._feedFilterOnly = false;
                return;
            }

            // Apply feed filters
            let visibleEntries = entries;

            // Content type filter
            if (this._feedContentType) {
                const ct = this._feedContentType;
                visibleEntries = visibleEntries.filter(e => {
                    if (ct === 'post') return e.type === 'group' && e.data.has_post;
                    if (ct === 'comment') return e.type === 'group' && e.data.total_comments > 0;
                    if (ct === 'announcement') return e.type === 'announcement';
                    return true;
                });
            }

            const cutoffMap = { '1h': 3600000, '24h': 86400000, '2d': 172800000, '7d': 604800000, '30d': 2592000000 };

            // Time filter with auto-widen: if default 24h yields nothing, try 7d then all
            let effectiveTimeFilter = this._feedTimeFilter || '24h';
            if (effectiveTimeFilter !== 'all') {
                const cutoff = Date.now() - (cutoffMap[effectiveTimeFilter] || 86400000);
                visibleEntries = visibleEntries.filter(e => {
                    const ts = e.timestamp ? new Date(e.timestamp).getTime() : 0;
                    return ts >= cutoff;
                });
                // Auto-widen only from the default 24h when no content/author filters are active
                if (visibleEntries.length === 0 && effectiveTimeFilter === '24h' && !this._feedAuthorFilter && !this._feedShowNew && !this._feedContentType) {
                    const cutoff7d = Date.now() - cutoffMap['7d'];
                    visibleEntries = entries.filter(e => {
                        const ts = e.timestamp ? new Date(e.timestamp).getTime() : 0;
                        return ts >= cutoff7d;
                    });
                    if (visibleEntries.length > 0) {
                        effectiveTimeFilter = '7d';
                    } else {
                        visibleEntries = entries;
                        effectiveTimeFilter = 'all';
                    }
                    // Update the sentence filter label to match widened range
                    this._feedTimeFilter = effectiveTimeFilter;
                }
            }

            // Show New filter (unread items only)
            if (this._feedShowNew) {
                visibleEntries = visibleEntries.filter(e => {
                    if (e.type === 'group') {
                        return e.data.post_unread || (e.data.unread_comments || 0) > 0;
                    }
                    if (e.type === 'announcement') {
                        return e.data.unread;
                    }
                    return false;
                });
            }

            // Author filter
            if (this._feedAuthorFilter) {
                const af = this._feedAuthorFilter;
                visibleEntries = visibleEntries.filter(e => {
                    if (e.type === 'group') {
                        const g = e.data;
                        if (g.post_domain === af) return true;
                        if (g.comments && g.comments.some(c => c.author_domain === af)) return true;
                        return false;
                    }
                    if (e.type === 'announcement') {
                        const a = e.data;
                        return a.author_domain === af || a.target_domain === af;
                    }
                    return true;
                });
            }

            const sentenceFilterHtml = this._buildFeedSentenceFilter(visibleEntries.length);

            const metaRowHtml = `
                <div class="feed-sentence-meta">
                    <span class="feed-sentence-count">${visibleEntries.length} item${visibleEntries.length !== 1 ? 's' : ''}</span>
                    <button class="feed-compose-btn${this._feedEditorOpen ? ' open' : ''}" onclick="App.toggleFeedEditor()" title="New post">${this._feedEditorOpen ? '\u00d7' : '+'}</button>
                </div>
                <div class="feed-editor-wrapper${this._feedEditorOpen ? ' open' : ''}" id="feed-editor-wrapper">
                    <div class="feed-editor-card">
                        <div class="feed-editor-title-row${(this._feedEditorTitle || '').trim() ? ' has-value' : ''}">
                            <span class="feed-editor-hash">#</span>
                            <input type="text" class="feed-editor-title" id="feed-editor-title"
                                placeholder="title" value="${this.escapeHtml(this._feedEditorTitle || '')}"
                                oninput="App._onFeedEditorInput()">
                        </div>
                        <div id="milkdown-feed" class="milkdown-mount"></div>
                        <textarea id="feed-editor-body" class="hidden">${this.escapeHtml(this._feedEditorBody || '')}</textarea>
                        <div class="feed-editor-footer">
                            <span class="feed-editor-hint"><kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>F</kbd> focus</span>
                            <span class="feed-editor-status" id="feed-editor-status">${this._feedEditorStatus || ''}</span>
                            <button class="feed-editor-publish" onclick="App.publishFromFeed()"
                                ${(this._feedEditorTitle || '').trim() || (this._feedEditorBody || '').trim() ? '' : 'disabled'}>Publish</button>
                        </div>
                    </div>
                </div>
            `;

            // Save inline comment state before re-render destroys DOM
            const _savedInlineCommentUrl = this._inlineCommentOpen ? this._inlineCommentUrl : null;
            if (this._inlineCommentOpen) {
                this._inlineCommentBody = this.getEditorContent('inline-comment-body') || this._inlineCommentBody;
                this._destroyMilkdown('inline-comment-body');
            }

            container.innerHTML = filterHtml + sentenceFilterHtml + metaRowHtml + `
                <div class="feed-list">
                    ${visibleEntries.length === 0
                        ? `<div class="empty-state"><p>No items match the current filters.</p>
                            <button class="feed-btn" onclick="App._feedTimeFilter='24h'; App._feedShowNew=false; App._feedContentType=''; App.renderConversationsTabbed(document.getElementById('content-list'));">Show all</button></div>`
                        : visibleEntries.map(e => {
                            if (e.type === 'group') return this._renderGroupedItem(e.data);
                            if (e.type === 'announcement') return this._renderAnnouncementItem(e.data);
                            return '';
                        }).join('')}
                </div>
            `;

            // Fetch remote avatars for domains in feed groups
            const feedGroups = entries.filter(e => e.type === 'group').map(e => e.data);
            this._fetchRemoteAvatarsForGroups(feedGroups);

            // Observe unread items for viewport-based auto-marking
            this._observeFeedItems(container);

            // Restore inline comment editor if it was open
            if (_savedInlineCommentUrl) {
                this._inlineCommentOpen = false; // Reset so openInlineCommentEditor doesn't try to close
                this.openInlineCommentEditor(_savedInlineCommentUrl);
            }

            if (!this._conversationsRefreshing && !this._feedFilterOnly) this._autoRefreshConversations();
            this._feedFilterOnly = false;
        } catch (err) {
            container.innerHTML = filterHtml + `<div class="feed-list"><div class="empty-state"><h3>Failed to load</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
        }
    },

    async refreshConversations() {
        if (this._conversationsRefreshing) return;
        this._conversationsRefreshing = true;
        this.showToast('Refreshing...', 'info', 3000);

        try {
            const result = await this.api('POST', '/api/feed/refresh');
            const newItems = result.new_items || 0;

            this.counts.feedUnread = result.unread || 0;

            this._updateTopbarBadges();

            if (newItems > 0) {
                this.showToast(`${newItems} new item${newItems > 1 ? 's' : ''}`, 'success');
            } else {
                this.showToast('Feed up to date', 'success');
            }

            // Re-render if still on conversations view
            if (this.currentView === 'conversations') {
                const contentList = document.getElementById('content-list');
                if (contentList) await this.renderConversationsTabbed(contentList);
            }

            // Notification fetch disabled — UI bell removed
            // this.fetchNotificationCount();
        } catch (err) {
            this.showToast('Refresh failed: ' + err.message, 'error');
        } finally {
            this._conversationsRefreshing = false;
        }
    },

    async _autoRefreshConversations() {
        if (this._conversationsRefreshing) return;
        this._conversationsRefreshing = true;

        try {
            const result = await this.api('POST', '/api/feed/refresh');
            const newItems = result.new_items || 0;

            this.counts.feedUnread = result.unread || 0;

            this._updateTopbarBadges();

            if (newItems > 0) {
                this.showToast(`${newItems} new item${newItems > 1 ? 's' : ''}`, 'success');
                if (this.currentView === 'conversations') {
                    await this._patchFeedDOM();
                }
            }
        } catch (err) {
            console.error('Auto-refresh failed:', err);
        } finally {
            this._conversationsRefreshing = false;
        }
    },

    // Surgically update the feed DOM: prepend new groups, update existing ones with new comments.
    async _patchFeedDOM() {
        const feedList = document.querySelector('.feed-list');
        if (!feedList) return;

        const scopeParam = this._feedScope && this._feedScope !== 'network' ? `?scope=${this._feedScope}` : '';
        const groupedResult = await this.api('GET', `/api/feed/grouped${scopeParam}`);
        const groups = groupedResult.groups || [];

        this.counts.feedUnread = groupedResult.unread_items || 0;
        this._updateTopbarBadges();

        // Index existing DOM items by post URL
        const existingByUrl = new Map();
        for (const el of feedList.querySelectorAll('.feed-item[data-url]')) {
            existingByUrl.set(el.dataset.url, el);
        }

        // Walk new groups: prepend brand-new posts, update existing ones in-place
        const toInsertBefore = feedList.querySelector('.feed-item, .activity-item');
        for (const group of groups) {
            const url = group.post_url ? group.post_url.replace(/\.md$/, '.html') : '';
            const existing = url ? existingByUrl.get(url) : null;
            const html = this._renderGroupedItem(group);

            if (existing) {
                // Skip items whose inline comment editor is open (editor is next sibling)
                if (this._inlineCommentOpen && existing.dataset.url === this._inlineCommentUrl) continue;

                // Update in-place: swap outer HTML to pick up new comments / unread state
                const oldIds = existing.dataset.ids || '[]';
                const newIds = JSON.stringify(group.item_ids);
                if (oldIds !== newIds || existing.classList.contains('unread') !== (group.post_unread || group.unread_comments > 0)) {
                    const tmp = document.createElement('div');
                    tmp.innerHTML = html;
                    const newEl = tmp.firstElementChild;
                    existing.replaceWith(newEl);
                    this._observeSingleFeedItem(newEl);
                }
            } else if (url) {
                // New group — insert at top of feed list
                const tmp = document.createElement('div');
                tmp.innerHTML = html;
                const newEl = tmp.firstElementChild;
                if (toInsertBefore) {
                    feedList.insertBefore(newEl, toInsertBefore);
                } else {
                    feedList.appendChild(newEl);
                }
                this._observeSingleFeedItem(newEl);
            }
        }

        // Fetch avatars for any new domains
        this._fetchRemoteAvatarsForGroups(groups);
    },

    // Observe a single feed item for viewport-based read tracking
    _observeSingleFeedItem(el) {
        if (el.classList.contains('unread') && this._feedObserver) {
            this._feedObserver.observe(el);
        }
    },

    // Inline feed editor
    toggleFeedEditor() {
        this._feedEditorOpen = !this._feedEditorOpen;
        const wrapper = document.getElementById('feed-editor-wrapper');
        const btn = document.querySelector('.feed-compose-btn');
        if (wrapper) wrapper.classList.toggle('open', this._feedEditorOpen);
        if (btn) {
            btn.classList.toggle('open', this._feedEditorOpen);
            btn.textContent = this._feedEditorOpen ? '\u00d7' : '+';
        }
        if (this._feedEditorOpen) {
            setTimeout(async () => {
                await this._initMilkdown('feed-editor-body');
                // Wire Milkdown change events to autosave
                document.getElementById('milkdown-feed')?.addEventListener('milkdown:change', () => {
                    this._onFeedEditorInput();
                });
                const title = document.getElementById('feed-editor-title');
                if (title) title.focus();
            }, 350);
        } else {
            this._destroyMilkdown('feed-editor-body');
        }
    },

    closeFeedEditor() {
        this._destroyMilkdown('feed-editor-body');
        this._feedEditorOpen = false;
        this._feedEditorTitle = '';
        this._feedEditorBody = '';
        this._feedEditorStatus = '';
        this._feedEditorDraftId = null;
        const wrapper = document.getElementById('feed-editor-wrapper');
        const btn = document.querySelector('.feed-compose-btn');
        if (wrapper) wrapper.classList.remove('open');
        if (btn) {
            btn.classList.remove('open');
            btn.textContent = '+';
        }
    },

    _autoGrowFeedEditor(el) {
        el.style.height = 'auto';
        el.style.height = el.scrollHeight + 'px';
    },

    _onFeedEditorInput() {
        const title = document.getElementById('feed-editor-title');
        if (title) this._feedEditorTitle = title.value;
        const titleRow = document.querySelector('.feed-editor-title-row');
        if (titleRow) titleRow.classList.toggle('has-value', !!(this._feedEditorTitle || '').trim());
        this._feedEditorBody = this.getEditorContent('feed-editor-body') || '';
        const hasContent = (this._feedEditorTitle || '').trim() || (this._feedEditorBody || '').trim();
        const pub = document.querySelector('.feed-editor-publish');
        if (pub) pub.disabled = !hasContent;
        if (!hasContent) { this._updateFeedEditorStatus(''); return; }
        this._updateFeedEditorStatus('Unsaved');
        if (this._feedEditorSaveTimer) clearTimeout(this._feedEditorSaveTimer);
        this._feedEditorSaveTimer = setTimeout(() => this._saveFeedEditorDraft(), 2000);
    },

    _updateFeedEditorStatus(text, saved) {
        this._feedEditorStatus = text;
        const el = document.getElementById('feed-editor-status');
        if (el) {
            el.textContent = text;
            el.classList.toggle('saved', !!saved);
        }
    },

    _slugFromTitle(title) {
        return title.trim().toLowerCase()
            .replace(/[^a-z0-9\s-]/g, '')
            .replace(/\s+/g, '-')
            .replace(/-+/g, '-')
            .substring(0, 60)
            .replace(/-$/, '') || '';
    },

    async _saveFeedEditorDraft() {
        const title = this._feedEditorTitle || '';
        const body = this._feedEditorBody || '';
        if (!title.trim() && !body.trim()) return;
        this._updateFeedEditorStatus('Saving...');
        try {
            // Generate a slug-based ID from the title on first save
            if (!this._feedEditorDraftId && title.trim()) {
                this._feedEditorDraftId = this._slugFromTitle(title);
            }
            const markdown = title.trim() ? `# ${title}\n\n${body}` : body;
            const result = await this.api('POST', '/api/drafts', {
                id: this._feedEditorDraftId || '',
                markdown,
            });
            if (result.id) this._feedEditorDraftId = result.id;
            this._updateFeedEditorStatus('Saved', true);
        } catch (e) {
            this._updateFeedEditorStatus('Save failed');
        }
    },

    async publishFromFeed() {
        const title = this._feedEditorTitle || '';
        const body = this._feedEditorBody || '';
        if (!title.trim() && !body.trim()) return;
        const markdown = title.trim() ? `# ${title}\n\n${body}` : body;
        const pub = document.querySelector('.feed-editor-publish');
        if (pub) { pub.disabled = true; pub.textContent = 'Publishing...'; }
        try {
            const result = await this.api('POST', '/api/publish', {
                markdown,
                filename: '',
                draft_id: this._feedEditorDraftId || '',
            });
            if (result.success) {
                this.showToast(`Published: ${result.title || 'Post'}`, 'success');
                this.closeFeedEditor();
                await this.loadAllCounts();
                const cl = document.getElementById('content-list');
                if (cl) await this.renderConversationsTabbed(cl);
            }
        } catch (e) {
            this.showToast('Failed to publish: ' + e.message, 'error');
            if (pub) { pub.disabled = false; pub.textContent = 'Publish'; }
        }
    },

    _openFullEditorFromFeed() {
        // Save current inline content as draft, then open full editor in focus mode
        const title = this._feedEditorTitle || '';
        const body = this.getEditorContent('feed-editor-body') || '';
        this._focusFromFeed = true;
        this.closeFeedEditor();
        this.navigateTo('/posts/new');
        // Set content and enter focus mode after editor screen opens
        setTimeout(() => {
            const titleInput = document.getElementById('editor-title-input');
            if (titleInput && title.trim()) titleInput.value = title;
            const textarea = document.getElementById('markdown-input');
            if (textarea) textarea.value = body;
            this.setEditorContent('markdown-input', body);
            // Enter focus mode
            if (!this._focusMode) this._toggleFocusMode();
        }, 300);
    },

    // ── Inline Comment Editor (in-feed reply) ──

    openInlineCommentEditor(postUrl) {
        // Close any existing inline comment editor
        if (this._inlineCommentOpen) {
            this._closeInlineCommentEditorImmediate();
        }

        // Find the feed item by URL
        const feedItem = document.querySelector(`.feed-item[data-url="${CSS.escape(postUrl)}"]`);
        if (!feedItem) {
            this.newComment();
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
            const cl = document.getElementById('content-list');
            if (cl) await this.renderConversationsTabbed(cl);
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

    async renderFollowingList(container) {
        try {
            const result = await this.api('GET', '/api/following');
            let follows = result.following || [];

            // Deduplicate by normalized URL (strip trailing slash)
            const seen = new Set();
            follows = follows.filter(f => {
                const norm = f.url.replace(/\/+$/, '');
                if (seen.has(norm)) return false;
                seen.add(norm);
                return true;
            });

            if (follows.length === 0) {
                container.innerHTML = `
                    <div class="content-list">
                        <div class="empty-state onboarding-empty">
                            <h3>Get started by following an author</h3>
                            <p>Following an author means their posts appear in your Conversations feed
                               and their comments on your site are automatically blessed.</p>
                            <div class="content-item following-item onboarding-follow-card">
                                <div class="item-info">
                                    <div class="item-title">discover.polis.pub</div>
                                </div>
                                <div class="following-item-actions">
                                    <button class="primary" onclick="App.followDiscover()">Follow</button>
                                </div>
                                <div class="onboarding-follow-desc">A community hub that aggregates conversations from across the polis network.</div>
                            </div>
                            <button class="secondary" onclick="App.openFollowPanel()">Follow Another Author</button>
                        </div>
                    </div>
                `;
                return;
            }

            // Fetch followers to show below following list
            let followersHtml = '';
            try {
                const followersResult = await this.api('GET', '/api/followers/count');
                const followers = followersResult.followers || [];
                const fCount = followersResult.count || followers.length;
                this.counts.followers = fCount;

                if (followers.length > 0) {
                    followersHtml = `
                        <div class="followers-divider">
                            <span class="section-label-inline">${fCount} Follower${fCount !== 1 ? 's' : ''}</span>
                        </div>
                        ${followers.map(domain => domain.toLowerCase()).map(domain => `
                            <div class="content-item follower-item" onclick="window.open('https://${this.escapeHtml(domain)}', '_blank')" style="cursor: pointer;">
                                <div class="item-info">
                                    <div class="item-title">${this.escapeHtml(domain)}</div>
                                </div>
                                <div class="follower-actions">
                                    <button class="feed-filter-link" onclick="event.stopPropagation(); window.open('https://${this.escapeHtml(domain)}', '_blank')">Visit</button>
                                </div>
                            </div>
                        `).join('')}
                    `;
                } else {
                    followersHtml = `
                        <div class="followers-divider">
                            <span class="section-label-inline">Followers</span>
                        </div>
                        <p class="followers-empty">No followers yet. When others follow you, they'll appear here.</p>
                    `;
                }
            } catch (e) {
                console.error('Failed to fetch followers:', e);
                followersHtml = `
                    <div class="followers-divider">
                        <span class="section-label-inline">Followers</span>
                    </div>
                    <p class="followers-empty">Could not load followers.</p>
                `;
            }

            container.innerHTML = `
                <div class="content-list">
                    <div class="following-list-header">
                        <span class="section-label-inline">${follows.length} Following</span>
                        <button class="primary" onclick="App.openFollowPanel()">Follow Author</button>
                    </div>
                    ${follows.map(f => {
                        const domain = f.url.replace('https://', '').replace('http://', '').replace(/\/$/, '').toLowerCase();
                        const title = f.site_title || f.author_name || domain;
                        const subtitle = f.author_name && f.author_name !== title
                            ? `${this.escapeHtml(f.author_name)} · ${this.escapeHtml(domain)}`
                            : this.escapeHtml(domain);
                        const addedAt = f.added_at ? this.formatDate(f.added_at) : '';
                        const siteUrl = f.url.replace(/\/$/, '');
                        return `
                            <div class="content-item following-item" onclick="window.open('${this.escapeHtml(siteUrl)}', '_blank')" style="cursor: pointer;">
                                <div class="item-info">
                                    <div class="item-title">${this.escapeHtml(title)}</div>
                                    <div class="item-path">${subtitle}</div>
                                </div>
                                <div class="following-item-actions">
                                    ${addedAt ? `<span class="item-date">Followed: ${addedAt}</span>` : ''}
                                    <button class="feed-filter-link" onclick="event.stopPropagation(); window.open('https://${this.escapeHtml(domain)}', '_blank')" title="Visit site">Visit</button>
                                    <button class="danger-small" onclick="event.stopPropagation(); App.unfollowAuthor('${this.escapeHtml(f.url)}')">Unfollow</button>
                                </div>
                            </div>
                        `;
                    }).join('')}
                    ${followersHtml}
                </div>
            `;
        } catch (err) {
            container.innerHTML = `<div class="content-list"><div class="empty-state"><h3>Failed to load following</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
        }
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

    // ==================== Activity Stream ====================

    _activityCursor: '0',
    _activityEvents: [],
    _activityMaxEvents: 500,

    async renderActivityStream(container) {
        try {
            container.innerHTML = '<div class="content-list"><div class="empty-state"><p>Loading activity...</p></div></div>';
            const result = await this.api('GET', `/api/activity?since=${this._activityCursor}&limit=100`);
            const events = result.events || [];

            if (events.length > 0) {
                this._activityEvents = this._activityEvents.concat(events);
                if (this._activityEvents.length > this._activityMaxEvents) {
                    this._activityEvents = this._activityEvents.slice(
                        this._activityEvents.length - this._activityMaxEvents
                    );
                }
                this._activityCursor = result.cursor || this._activityCursor;
            }

            if (this._activityEvents.length === 0) {
                container.innerHTML = `<div class="content-list"><div class="empty-state">
                    <h3>No activity yet</h3>
                    <p>Follow some authors to see their activity here.</p>
                </div></div>`;
                return;
            }

            const hasMore = result.has_more;
            container.innerHTML = `
                <div class="content-list">
                    ${[...this._activityEvents].reverse().map(evt => this.renderActivityEvent(evt)).join('')}
                </div>
                ${hasMore ? '<div class="activity-load-more"><button class="secondary" onclick="App.loadMoreActivity()">Load More</button></div>' : ''}
            `;
        } catch (err) {
            container.innerHTML = `<div class="content-list"><div class="empty-state"><h3>Failed to load activity</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
        }
    },

    _hiddenActivityTypes: new Set([
        'pub.polis.site.key_rotated',
    ]),

    renderActivityEvent(evt) {
        if (this._hiddenActivityTypes.has(evt.type)) return '';
        // Normalize domains to lowercase for consistent display
        const actor = (evt.actor || '').toLowerCase();
        if (evt.payload) {
            if (evt.payload.target_domain) evt.payload.target_domain = evt.payload.target_domain.toLowerCase();
            if (evt.payload.source_domain) evt.payload.source_domain = evt.payload.source_domain.toLowerCase();
        }
        const typeLabels = {
            'pub.polis.post.published': 'published a post',
            'pub.polis.post.republished': 'republished a post',
            'pub.polis.comment.published': 'published a comment',
            'pub.polis.comment.republished': 'republished a comment',
            'pub.polis.comment.blessing.requested': 'requested a blessing',
            'pub.polis.comment.blessing.granted': 'granted a blessing',
            'pub.polis.comment.blessing.denied': 'denied a blessing',
            'pub.polis.follow.announced': 'followed someone',
            'pub.polis.follow.removed': 'unfollowed someone',
        };
        const actionLabel = typeLabels[evt.type] || evt.type;
        const typeBadge = evt.type.split('.').pop();

        let detail = '';
        if (evt.payload) {
            if (evt.payload.title) {
                detail = `<span class="activity-detail">${this.escapeHtml(evt.payload.title)}</span>`;
            } else if (evt.payload.url) {
                detail = `<span class="activity-detail">${this.escapeHtml(evt.payload.url)}</span>`;
            } else if (evt.payload.source_domain && evt.type.includes('blessing')) {
                // For blessing events, show the comment author (source) since the actor is the granter
                detail = `<span class="activity-detail">${this.escapeHtml(evt.payload.source_domain)}</span>`;
            } else if (evt.payload.target_domain) {
                detail = `<span class="activity-detail">${this.escapeHtml(evt.payload.target_domain)}</span>`;
            }
        }

        // Build clickable link for content events (posts/comments with url)
        let linkUrl = '';
        if (evt.payload && evt.payload.url) {
            try {
                linkUrl = evt.payload.url.replace(/\.md$/, '.html');
            } catch (e) {}
        }

        const tag = linkUrl ? 'a' : 'div';
        const linkAttrs = linkUrl ? ` href="${this.escapeHtml(linkUrl)}" rel="noopener" style="text-decoration:none;color:inherit;"` : '';

        return `
            <${tag}${linkAttrs} class="activity-item">
                <span class="act-dot"></span>
                <span><span class="act-who">${this.escapeHtml(actor)}</span> ${actionLabel}${detail ? ' — ' + detail : ''}</span>
                <span class="act-when">${this.formatRelativeTime(evt.created_at || evt.timestamp)}</span>
            </${tag}>
        `;
    },

    async refreshActivity() {
        const contentList = document.getElementById('content-list');
        if (!contentList) return;
        if (this.currentView === 'conversations') {
            await this.renderConversationsTabbed(contentList);
        } else {
            await this.renderActivityStream(contentList);
        }
    },

    async resetActivity() {
        this._activityCursor = '0';
        this._activityEvents = [];
        const contentList = document.getElementById('content-list');
        if (!contentList) return;
        if (this.currentView === 'conversations') {
            await this.renderConversationsTabbed(contentList);
        } else {
            await this.renderActivityStream(contentList);
        }
    },

    async loadMoreActivity() {
        const contentList = document.getElementById('content-list');
        if (!contentList) return;
        if (this.currentView === 'conversations') {
            await this.renderConversationsTabbed(contentList);
        } else {
            await this.renderActivityStream(contentList);
        }
    },

    // ==================== Community Pulse ====================

    async renderPulse(container) {
        try {
            container.innerHTML = '<div class="content-list"><div class="empty-state"><p>Loading pulse...</p></div></div>';
            const data = await this.api('GET', '/api/pulse');

            // Empty state: no network yet
            if (data.network.following === 0) {
                container.innerHTML = `<div class="content-list"><div class="empty-state">
                    <h3>No network yet</h3>
                    <p>Follow some authors to see your community pulse.</p>
                    <button class="primary" onclick="App.openFollowPanel()">Follow Author</button>
                </div></div>`;
                return;
            }

            let html = '<div class="pulse-dashboard">';

            // Card 1: Your Network
            html += '<div class="pulse-card">';
            html += '<div class="pulse-card-title">Your Network</div>';
            html += '<div class="pulse-stats-row">';
            html += `<div class="pulse-stat"><div class="pulse-stat-value">${data.network.following}</div><div class="pulse-stat-label">Following</div></div>`;
            html += `<div class="pulse-stat"><div class="pulse-stat-value">${data.network.followers}</div><div class="pulse-stat-label">Followers</div></div>`;
            html += `<div class="pulse-stat"><div class="pulse-stat-value">${data.network.feed_unread}</div><div class="pulse-stat-label">Unread</div></div>`;
            if (data.network.incoming_pending > 0) {
                html += `<div class="pulse-stat"><div class="pulse-stat-value pulse-stat-warning">${data.network.incoming_pending}</div><div class="pulse-stat-label">Pending</div></div>`;
            }
            html += '</div></div>';

            // Card 2: Recent from Your Network
            html += '<div class="pulse-card">';
            html += '<div class="pulse-card-title">Recent from Your Network</div>';
            if (data.recent.length === 0) {
                html += '<div class="pulse-empty">No recent items in the last 7 days.</div>';
            } else {
                data.recent.forEach(item => {
                    const typeBadge = item.type === 'post' ? 'Post' : 'Comment';
                    const unreadDot = item.unread ? '<span class="pulse-unread-dot"></span>' : '';
                    html += `<div class="pulse-highlight">
                        <span class="pulse-type-badge">${typeBadge}</span>
                        <span class="pulse-highlight-title">${this.escapeHtml(item.title || '(untitled)')}</span>
                        ${unreadDot}
                        <span class="pulse-highlight-meta">${this.escapeHtml(item.author_domain)} &middot; ${this.formatDate(item.published)}</span>
                    </div>`;
                });
            }
            html += '</div>';

            // Card 3: Most Active
            html += '<div class="pulse-card">';
            html += '<div class="pulse-card-title">Most Active</div>';
            if (data.top_authors.length === 0) {
                html += '<div class="pulse-empty">No activity in the last 30 days.</div>';
            } else {
                data.top_authors.forEach(author => {
                    const parts = [];
                    if (author.post_count > 0) parts.push(`${author.post_count} post${author.post_count !== 1 ? 's' : ''}`);
                    if (author.comment_count > 0) parts.push(`${author.comment_count} comment${author.comment_count !== 1 ? 's' : ''}`);
                    html += `<div class="pulse-author">
                        <span class="pulse-author-domain">${this.escapeHtml(author.domain)}</span>
                        <span class="pulse-author-stats">${parts.join(', ')}</span>
                    </div>`;
                });
            }
            html += '</div>';

            // Card 4: Your Site
            html += '<div class="pulse-card">';
            html += '<div class="pulse-card-title">Your Site</div>';
            html += '<div class="pulse-stats-row">';
            html += `<div class="pulse-stat"><div class="pulse-stat-value">${data.site.posts}</div><div class="pulse-stat-label">Posts</div></div>`;
            html += `<div class="pulse-stat"><div class="pulse-stat-value">${data.site.incoming_blessed}</div><div class="pulse-stat-label">Comments</div></div>`;
            if (data.site.incoming_pending > 0) {
                html += `<div class="pulse-stat"><div class="pulse-stat-value pulse-stat-warning">${data.site.incoming_pending}</div><div class="pulse-stat-label">Requests</div></div>`;
            }
            html += '</div></div>';

            html += '</div>';
            container.innerHTML = html;
        } catch (err) {
            container.innerHTML = `<div class="content-list"><div class="empty-state"><h3>Failed to load pulse</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
        }
    },

    // ==================== Followers ====================

    async renderFollowersList(container) {
        try {
            container.innerHTML = '<div class="content-list"><div class="empty-state"><p>Loading followers...</p></div></div>';
            const result = await this.api('GET', '/api/followers/count');
            const followers = result.followers || [];
            const count = result.count || 0;

            this.counts.followers = count;


            if (followers.length === 0) {
                container.innerHTML = `<div class="content-list"><div class="empty-state">
                    <h3>No followers yet</h3>
                    <p>When other polis authors follow you, they'll appear here.</p>
                </div></div>`;
                return;
            }

            container.innerHTML = `
                <div class="content-list">
                    <div class="following-list-header">
                        <span class="section-label-inline">${count} Follower${count !== 1 ? 's' : ''}</span>
                        <button class="secondary sync-btn" onclick="App.refreshFollowers(true)">Refresh</button>
                    </div>
                    ${followers.map(domain => domain.toLowerCase()).map(domain => `
                        <div class="content-item follower-item" onclick="window.open('https://${this.escapeHtml(domain)}', '_blank')" style="cursor: pointer;">
                            <div class="item-info">
                                <div class="item-title">${this.escapeHtml(domain)}</div>
                            </div>
                            <div class="follower-actions">
                                <button class="feed-filter-link" onclick="event.stopPropagation(); window.open('https://${this.escapeHtml(domain)}', '_blank')">Visit</button>
                            </div>
                        </div>
                    `).join('')}
                </div>
            `;
        } catch (err) {
            container.innerHTML = `<div class="content-list"><div class="empty-state"><h3>Failed to load followers</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
        }
    },

    async refreshFollowers(fullRefresh) {
        if (fullRefresh) {
            const contentList = document.getElementById('content-list');
            if (contentList) {
                contentList.innerHTML = '<div class="content-list"><div class="empty-state"><p>Refreshing followers...</p></div></div>';
                try {
                    const result = await this.api('GET', '/api/followers/count?refresh=true');
                    this.counts.followers = result.count || 0;

                    await this.renderFollowersList(contentList);
                } catch (err) {
                    contentList.innerHTML = `<div class="content-list"><div class="empty-state"><h3>Refresh failed</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
                }
            }
        } else {
            const contentList = document.getElementById('content-list');
            if (contentList) await this.renderFollowersList(contentList);
        }
    },

    escapeHtml(text) {
        if (!text) return '';
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
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
                    // Legacy hash links from old notifications — map to paths
                    const hash = link.replace(/^\/_\/#/, '');
                    const legacyMap = {
                        'blessings': '/blessings',
                        'followers': '/followers',
                        'feed': '/conversations',
                        'my-comments-blessed': '/comments/blessed',
                        'my-comments-denied': '/comments/denied',
                    };
                    this.navigateTo(legacyMap[hash] || '/' + hash);
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
    async processCommentIntent(intent) {
        if (!intent.target) return;

        this.currentCommentDraftId = null;
        document.getElementById('reply-to-url').value = intent.target;
        this.setEditorContent('comment-input', intent.text || '');
        this._intentComment = intent;  // stash for post-action CTAs
        window.history.replaceState({}, '', this.pathForScreen('newComment'));
        this.showScreen('comment');
    },

    // intent=follow: auto-follow the author and show result.
    async processFollowIntent(intent) {
        if (!intent.target) return;

        // Navigate to the following view so the user lands in the right place
        this.sidebarMode = 'social';
        this._updateSidebarUI('social');
        this.currentView = 'following';
        this._updateSidebarActiveItem('following');
        await this.loadViewContent();
        window.history.replaceState({}, '', this.pathForView('following'));

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
            followActions.push({ label: postLabel, action: () => { this.dismissIntentResult(); this.newPost(); } });

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
                { label: this.counts.posts === 0 ? 'Write your first post' : 'Write a post', action: () => { this.dismissIntentResult(); this.newPost(); } },
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

    // ── DM Helpers ──────────────────────────────────────────────────────

    _dmAvatarColor(domain) {
        let hash = 0;
        for (let i = 0; i < domain.length; i++) {
            hash = domain.charCodeAt(i) + ((hash << 5) - hash);
        }
        const hue = Math.abs(hash) % 360;
        return `hsl(${hue}, 35%, 45%)`;
    },

    _dmAvatarHtml(domain, size, cssClass) {
        const initial = (domain || '?')[0].toUpperCase();
        const bg = this._dmAvatarColor(domain);
        return `<div class="${cssClass}" style="background: ${bg};">${initial}</div>`;
    },

    _dmFormatDate(isoString) {
        if (!isoString) return '';
        const d = new Date(isoString);
        const now = new Date();
        const isToday = d.toDateString() === now.toDateString();
        const yesterday = new Date(now); yesterday.setDate(yesterday.getDate() - 1);
        const isYesterday = d.toDateString() === yesterday.toDateString();
        if (isToday) return 'Today';
        if (isYesterday) return 'Yesterday';
        return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: d.getFullYear() !== now.getFullYear() ? 'numeric' : undefined });
    },

    // ── DM: Conversation List (Screen 1 + 6) ───────────────────────────

    async renderDMConversationList(container) {
        container.innerHTML = '<div class="empty-state"><p>Loading messages...</p></div>';
        try {
            const data = await this.api('GET', '/api/dm/conversations');
            const conversations = data.conversations || [];

            if (conversations.length === 0) {
                container.innerHTML = `
                    <div class="dm-header">
                        <div class="dm-title">Messages</div>
                        <div class="dm-header-actions">
                            <button class="btn-ghost" onclick="App.navigateTo('/messages/new')">
                                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
                                New
                            </button>
                        </div>
                    </div>
                    <div class="conv-empty">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
                        <p>No messages yet</p>
                        <button class="btn-ghost" onclick="App.navigateTo('/messages/new')">Start a conversation</button>
                    </div>
                    <div class="e2e-badge">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
                        End-to-end encrypted
                    </div>`;
                return;
            }

            // Sort by last_message_at descending
            conversations.sort((a, b) => (b.last_message_at || '').localeCompare(a.last_message_at || ''));

            const listHtml = conversations.map(c => {
                const isUnread = c.unread_count > 0;
                const preview = c.last_preview || '';
                return `
                <div class="conv-item${isUnread ? ' unread' : ''}" onclick="App.navigateTo('/messages/${this.escapeHtml(c.id)}')">
                    ${this._dmAvatarHtml(c.peer_domain, 36, 'conv-avatar')}
                    <div class="conv-body">
                        <div class="conv-top">
                            <div>
                                <span class="conv-name">${this.escapeHtml(c.peer_domain.split('.')[0])}</span>
                                <span class="conv-domain">${this.escapeHtml(c.peer_domain)}</span>
                            </div>
                            <span class="conv-time">${this.formatRelativeTime(c.last_message_at)}</span>
                        </div>
                        <div class="conv-preview">${this.escapeHtml(preview)}</div>
                    </div>
                    ${isUnread ? `<span class="conv-unread-badge">${c.unread_count}</span>` : ''}
                </div>`;
            }).join('');

            container.innerHTML = `
                <div class="dm-header">
                    <div class="dm-title">Messages</div>
                    <div class="dm-header-actions">
                        <button class="btn-ghost" onclick="App.navigateTo('/messages/new')">
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
                            New
                        </button>
                    </div>
                </div>
                <div class="conv-list">${listHtml}</div>
                <div class="e2e-badge">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
                    End-to-end encrypted
                </div>`;
        } catch (err) {
            container.innerHTML = `<div class="empty-state"><h3>Failed to load messages</h3><p>${this.escapeHtml(err.message)}</p></div>`;
        }
    },

    // ── DM: Thread View (Screen 2 + 3) ──────────────────────────────────

    async renderDMThread(container, convId) {
        if (!convId) {
            container.innerHTML = '<div class="empty-state"><p>No conversation selected</p></div>';
            return;
        }

        // Handle new conversation (not yet created on backend)
        const isNewConv = convId.startsWith('__new__');
        if (isNewConv) {
            const peerDomain = this._dmThreadPeerDomain || convId.replace('__new__', '');
            const peerUrl = this._dmThreadPeerUrl || 'https://' + peerDomain;
            const peerName = peerDomain.split('.')[0];

            container.innerHTML = `
                <div class="thread-header">
                    <button class="thread-back" onclick="App.navigateTo('/messages')">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
                    </button>
                    <div class="thread-peer">
                        ${this._dmAvatarHtml(peerDomain, 28, 'thread-peer-avatar')}
                        <div>
                            <span class="thread-peer-name">${this.escapeHtml(peerName)}</span>
                            <span class="thread-peer-domain">${this.escapeHtml(peerDomain)}</span>
                        </div>
                    </div>
                </div>
                <div class="messages"></div>
                <div class="compose-bar">
                    <textarea class="compose-input" id="dm-compose-input" placeholder="Write a message..." rows="1" oninput="App._dmAutoGrow(this)" onkeydown="App._dmKeyDown(event, '${this.escapeHtml(convId)}', '${this.escapeHtml(peerUrl)}')"></textarea>
                    <button class="compose-send" id="dm-send-btn" onclick="App.sendDM('${this.escapeHtml(convId)}', '${this.escapeHtml(peerUrl)}')" disabled>
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
                    </button>
                </div>
                <div class="e2e-badge">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
                    End-to-end encrypted
                </div>`;

            const input = document.getElementById('dm-compose-input');
            const sendBtn = document.getElementById('dm-send-btn');
            if (input && sendBtn) {
                input.addEventListener('input', () => { sendBtn.disabled = !input.value.trim(); });
                input.focus();
            }
            return;
        }

        container.innerHTML = '<div class="empty-state"><p>Loading conversation...</p></div>';
        try {
            const data = await this.api('GET', `/api/dm/conversations/${encodeURIComponent(convId)}`);
            const messages = data.messages || [];
            const peerDomain = data.peer_domain || '';
            const peerUrl = data.peer_url || '';

            // Build message HTML grouped by date
            let messagesHtml = '';
            let lastDate = '';
            let lastDirection = '';
            const myDomain = this.siteBaseUrl ? new URL(this.siteBaseUrl).hostname : '';

            messages.forEach((msg, i) => {
                const msgDate = this._dmFormatDate(msg.timestamp);
                if (msgDate !== lastDate) {
                    messagesHtml += `<div class="msg-date-sep">${this.escapeHtml(msgDate)}</div>`;
                    lastDate = msgDate;
                    lastDirection = '';
                }

                const isOutgoing = msg.from === myDomain;
                const direction = isOutgoing ? 'outgoing' : 'incoming';
                const nextMsg = messages[i + 1];
                const nextDirection = nextMsg ? (nextMsg.from === myDomain ? 'outgoing' : 'incoming') : '';
                const isGroupLast = nextDirection !== direction || (nextMsg && this._dmFormatDate(nextMsg.timestamp) !== msgDate);

                // Reply reference
                let replyRefHtml = '';
                if (msg.reply_to_id) {
                    const refMsg = messages.find(m => m.id === msg.reply_to_id);
                    if (refMsg) {
                        const refPreview = refMsg.content.length > 60 ? refMsg.content.slice(0, 60) + '...' : refMsg.content;
                        const refAuthor = refMsg.from === myDomain ? 'You' : peerDomain.split('.')[0];
                        replyRefHtml = `<div class="msg-reply-ref"><span class="ref-author">${this.escapeHtml(refAuthor)}</span> ${this.escapeHtml(refPreview)}</div>`;
                    }
                }

                // Unsent indicator
                let unsentHtml = '';
                if (msg.status === 'unsent') {
                    unsentHtml = `<div class="msg-unsent">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
                        Not delivered — <a href="#" onclick="App.retryDMMessage('${convId}','${msg.id}'); return false;">Retry</a>
                    </div>`;
                }

                const timeHtml = `<div class="msg-time">${new Date(msg.timestamp).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}</div>`;

                messagesHtml += `
                    <div class="msg-row ${direction}${isGroupLast ? ' msg-group-last' : ''}${msg.status === 'unsent' ? ' msg-unsent-row' : ''}">
                        <div>
                            ${replyRefHtml}
                            <div class="msg-bubble">${this.escapeHtml(msg.content)}</div>
                            ${timeHtml}
                            ${unsentHtml}
                        </div>
                    </div>`;

                lastDirection = direction;
            });

            const peerName = peerDomain.split('.')[0];

            container.innerHTML = `
                <div class="thread-header">
                    <button class="thread-back" onclick="App.navigateTo('/messages')">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
                    </button>
                    <div class="thread-peer">
                        ${this._dmAvatarHtml(peerDomain, 28, 'thread-peer-avatar')}
                        <div>
                            <span class="thread-peer-name">${this.escapeHtml(peerName)}</span>
                            <span class="thread-peer-domain">${this.escapeHtml(peerDomain)}</span>
                        </div>
                    </div>
                    <div class="thread-actions">
                        <button class="thread-action-btn" onclick="App.deleteDMConversation('${this.escapeHtml(convId)}')" title="Delete conversation">
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
                        </button>
                    </div>
                </div>
                <div class="messages">${messagesHtml}</div>
                <div class="compose-bar">
                    <textarea class="compose-input" id="dm-compose-input" placeholder="Write a message..." rows="1" oninput="App._dmAutoGrow(this)" onkeydown="App._dmKeyDown(event, '${this.escapeHtml(convId)}', '${this.escapeHtml(peerUrl)}')"></textarea>
                    <button class="compose-send" id="dm-send-btn" onclick="App.sendDM('${this.escapeHtml(convId)}', '${this.escapeHtml(peerUrl)}')" disabled>
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
                    </button>
                </div>
                <div class="e2e-badge">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
                    End-to-end encrypted
                </div>`;

            // Scroll to bottom of messages
            const msgContainer = container.querySelector('.messages');
            if (msgContainer) msgContainer.scrollTop = msgContainer.scrollHeight;

            // Enable send button when input has content
            const input = document.getElementById('dm-compose-input');
            const sendBtn = document.getElementById('dm-send-btn');
            if (input && sendBtn) {
                input.addEventListener('input', () => {
                    sendBtn.disabled = !input.value.trim();
                });
            }
        } catch (err) {
            container.innerHTML = `<div class="empty-state"><h3>Failed to load conversation</h3><p>${this.escapeHtml(err.message)}</p></div>`;
        }
    },

    _dmAutoGrow(el) {
        el.style.height = 'auto';
        el.style.height = Math.min(el.scrollHeight, 120) + 'px';
    },

    _dmKeyDown(event, convId, peerUrl) {
        if (event.key === 'Enter' && !event.shiftKey) {
            event.preventDefault();
            this.sendDM(convId, peerUrl);
        }
    },

    async sendDM(convId, recipientUrl) {
        const input = document.getElementById('dm-compose-input');
        const sendBtn = document.getElementById('dm-send-btn');
        if (!input || !input.value.trim()) return;

        const content = input.value.trim();
        input.value = '';
        input.style.height = 'auto';
        if (sendBtn) sendBtn.disabled = true;

        try {
            const result = await this.api('POST', '/api/dm/send', {
                recipient_url: recipientUrl,
                content: content,
            });

            // If this was a new conversation, reload to get the real conv ID
            if (convId.startsWith('__new__')) {
                // Fetch conversations to find the new one
                const convData = await this.api('GET', '/api/dm/conversations');
                const convs = convData.conversations || [];
                const recipientDomain = new URL(recipientUrl).hostname;
                const newConv = convs.find(c => c.peer_domain === recipientDomain);
                if (newConv) {
                    this._dmThreadId = newConv.id;
                    this.navigateTo('/messages/' + newConv.id, { replace: true });
                    return;
                }
            }

            // Re-render to show the new message
            if (this.currentView === 'dm-thread') {
                const contentList = document.getElementById('content-list');
                if (contentList) await this.renderDMThread(contentList, this._dmThreadId || convId);
            }

            if (result.warning) {
                this.showToast('Message saved but delivery pending', 'info');
            }
        } catch (err) {
            this.showToast('Failed to send: ' + err.message, 'error');
            // Restore the content so user can retry
            input.value = content;
            if (sendBtn) sendBtn.disabled = false;
        }
    },

    async deleteDMConversation(convId) {
        const confirmed = await this.showConfirmModal(
            'Delete Conversation',
            'This will permanently delete this conversation and all messages. This cannot be undone.',
            'Delete', 'Cancel', 'danger'
        );
        if (!confirmed) return;

        try {
            await this.api('DELETE', `/api/dm/conversations/${encodeURIComponent(convId)}`);
            this.showToast('Conversation deleted', 'success');
            this.navigateTo('/messages');
        } catch (err) {
            this.showToast('Failed to delete: ' + err.message, 'error');
        }
    },

    async retryDMMessage(convId, msgId) {
        try {
            await this.api('POST', '/api/dm/retry', {});
            this.showToast('Retrying unsent messages...', 'info');
            // Re-render
            if (this.currentView === 'dm-thread') {
                const contentList = document.getElementById('content-list');
                if (contentList) await this.renderDMThread(contentList, convId);
            }
        } catch (err) {
            this.showToast('Retry failed: ' + err.message, 'error');
        }
    },

    // ── DM: New Conversation (Screen 4 + 5) ────────────────────────────

    async renderDMNewConversation(container) {
        container.innerHTML = '<div class="empty-state"><p>Loading recipients...</p></div>';
        try {
            const data = await this.api('GET', '/api/dm/recipients');
            const recipients = data.recipients || [];

            const recipientListHtml = recipients.map(r => {
                const canDM = r.status === 'open';
                const name = r.author_name || r.domain.split('.')[0];
                const badge = r.follows_us ? 'Follows you' : ({ 'no-dm': 'No DM', 'no-follow': 'Not following you', unknown: '' }[r.status] || '');
                const badgeClass = r.follows_us ? 'follows-you' : r.status;

                return `
                <div class="recipient-item${canDM ? '' : ' disabled'}" ${canDM ? `onclick="App._dmStartConversation('${this.escapeHtml(r.url)}', '${this.escapeHtml(r.domain)}')"` : ''}>
                    ${this._dmAvatarHtml(r.domain, 32, 'recipient-avatar')}
                    <div class="recipient-info">
                        <div class="recipient-name">${this.escapeHtml(name)}</div>
                        <div class="recipient-url">${this.escapeHtml(r.domain)}</div>
                    </div>
                    ${badge ? `<span class="recipient-status ${this.escapeHtml(badgeClass)}">${this.escapeHtml(badge)}</span>` : ''}
                </div>`;
            }).join('');

            container.innerHTML = `
                <div class="thread-header">
                    <button class="thread-back" onclick="App.navigateTo('/messages')">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
                    </button>
                    <div class="thread-peer">
                        <span class="thread-peer-name">New Message</span>
                    </div>
                </div>
                <div class="new-conv-search">
                    <div class="new-conv-search-icon">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
                    </div>
                    <input type="text" class="new-conv-input" id="dm-search-input" placeholder="Search by name or domain..." oninput="App._dmFilterRecipients()">
                </div>
                <div class="section-label">Following</div>
                <div class="recipient-list" id="dm-recipient-list">
                    ${recipientListHtml || '<div class="conv-empty"><p>No authors to message. Follow someone first.</p></div>'}
                </div>
                <div class="e2e-badge">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
                    End-to-end encrypted
                </div>
                <div class="dm-policy-hint">Messages require a mutual follow</div>`;

            // Store recipients for search filtering
            this._dmRecipients = recipients;
        } catch (err) {
            container.innerHTML = `<div class="empty-state"><h3>Failed to load recipients</h3><p>${this.escapeHtml(err.message)}</p></div>`;
        }
    },

    _dmRecipients: [],

    _dmFilterRecipients() {
        const input = document.getElementById('dm-search-input');
        const listEl = document.getElementById('dm-recipient-list');
        if (!input || !listEl) return;

        const query = input.value.trim().toLowerCase();
        const filtered = this._dmRecipients.filter(r => {
            if (!query) return true;
            return r.domain.toLowerCase().includes(query) || (r.author_name || '').toLowerCase().includes(query);
        });

        listEl.innerHTML = filtered.map(r => {
            const canDM = r.status === 'open';
            const name = r.author_name || r.domain.split('.')[0];
            const badge = r.follows_us ? 'Follows you' : ({ 'no-dm': 'No DM', 'no-follow': 'Not following you', unknown: '' }[r.status] || '');
            const badgeClass = r.follows_us ? 'follows-you' : r.status;

            return `
            <div class="recipient-item${canDM ? '' : ' disabled'}" ${canDM ? `onclick="App._dmStartConversation('${this.escapeHtml(r.url)}', '${this.escapeHtml(r.domain)}')"` : ''}>
                ${this._dmAvatarHtml(r.domain, 32, 'recipient-avatar')}
                <div class="recipient-info">
                    <div class="recipient-name">${this.escapeHtml(name)}</div>
                    <div class="recipient-url">${this.escapeHtml(r.domain)}</div>
                </div>
                ${badge ? `<span class="recipient-status ${this.escapeHtml(badgeClass)}">${this.escapeHtml(badge)}</span>` : ''}
            </div>`;
        }).join('') || '<div class="conv-empty"><p>No matching recipients</p></div>';
    },

    _dmStartConversation(recipientUrl, recipientDomain) {
        // Compute conversation ID (same algorithm as backend: sha256(sorted domains)[:16])
        const myDomain = this.siteBaseUrl ? new URL(this.siteBaseUrl).hostname : '';
        if (!myDomain) {
            this.showToast('Site URL not configured', 'error');
            return;
        }
        const domains = [myDomain, recipientDomain].sort();
        // Use a simple hash for the client-side; the server will create the real one
        // Navigate to the conversation (backend auto-creates on first message)
        this._dmPendingRecipient = { url: recipientUrl, domain: recipientDomain };
        // For now, navigate to a new thread with the peer domain as ID placeholder
        // The actual conv ID comes from the backend after first message
        this._dmThreadPeerUrl = recipientUrl;
        this._dmThreadPeerDomain = recipientDomain;
        this._dmThreadId = '__new__' + recipientDomain;
        this.currentView = 'dm-thread';
        this.navigateTo('/messages/__new__' + recipientDomain);
    },

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

    // ── Focus Mode ──

    _toggleFocusMode() {
        this._focusMode = !this._focusMode;
        const screen = document.getElementById('editor-screen');
        const nav = document.getElementById('icon-nav');
        const hint = document.getElementById('editor-focus-hint');

        if (this._focusMode) {
            screen.classList.add('focus-mode');
            nav.classList.add('focus-mode');
            hint.classList.remove('hidden');
        } else {
            screen.classList.remove('focus-mode');
            nav.classList.remove('focus-mode');
            hint.classList.add('hidden');
            // If we entered focus from the feed editor, return to feed with content
            if (this._focusFromFeed) {
                this._focusFromFeed = false;
                // Grab content from the full editor before leaving
                const title = document.getElementById('editor-title-input')?.value || '';
                const body = this.getEditorContent('markdown-input') || '';
                this._destroyMilkdown('markdown-input');
                // Restore to feed with inline editor open
                this._feedEditorTitle = title;
                this._feedEditorBody = body;
                this._feedEditorOpen = true;
                this.navigateTo('/feed');
                // Init Milkdown in the feed editor after render completes
                setTimeout(async () => {
                    await this._initMilkdown('feed-editor-body');
                    document.getElementById('milkdown-feed')?.addEventListener('milkdown:change', () => {
                        this._onFeedEditorInput();
                    });
                }, 500);
            }
        }
    },

    // TODO: paragraph dimming (iA Writer-style) — needs investigation into
    // Milkdown/ProseMirror DOM structure for reliable cursor-to-block mapping

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
