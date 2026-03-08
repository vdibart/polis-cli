// Polis Local App - Client-side JavaScript

const App = {
    currentDraftId: null,
    currentPostPath: null,  // Set when editing a published post
    currentFrontmatter: '',  // Stored frontmatter block for published posts
    currentCommentDraftId: null,
    currentView: 'posts-published',  // Current active view in sidebar
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
        feed: 0,
        feedUnread: 0,
        following: 0,
        followers: 0,
    },

    // SSE connection + polling fallback
    _eventSource: null,
    _countsPollTimer: null,

    // Comments published state
    _commentsPublishedFilter: 'all',

    // Conversations subtab state
    _conversationsSubtab: 'all',
    _conversationsRefreshing: false,

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

    showScreen(name) {
        Object.values(this.screens).forEach(s => {
            if (s) s.classList.add('hidden');
        });
        if (this.screens[name]) {
            this.screens[name].classList.remove('hidden');
        }
        if (name === 'editor') {
            this._updateTopbarMode('editor');
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

    toggleTheme() {
        this.setWebappTheme(this.webappTheme === 'dark' ? 'light' : 'dark');
        this._updateThemeIcon();
    },

    _updateThemeIcon() {
        const btn = document.getElementById('bar-icon-theme');
        if (!btn) return;
        const isDark = this.webappTheme === 'dark';
        btn.innerHTML = isDark
            ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>'
            : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>';
    },

    // Mode toggle: 'feed' or 'posts'
    switchMode(mode) {
        const btns = ['feed', 'posts', 'write'].map(m => document.getElementById(`mode-btn-${m}`));
        btns.forEach(b => b && b.classList.remove('active'));
        if (mode === 'feed') {
            btns[0] && btns[0].classList.add('active');
            this.navigateTo('/social/conversations');
        } else if (mode === 'write') {
            btns[2] && btns[2].classList.add('active');
            this.newPost();
        } else {
            btns[1] && btns[1].classList.add('active');
            this.navigateTo('/posts');
        }
    },

    // Update topbar mode buttons to reflect current view
    _updateTopbarMode(mode) {
        const btns = ['feed', 'posts', 'write'].map(m => document.getElementById(`mode-btn-${m}`));
        btns.forEach(b => b && b.classList.remove('active'));
        if (mode === 'social') {
            btns[0] && btns[0].classList.add('active');
        } else if (mode === 'editor') {
            btns[2] && btns[2].classList.add('active');
        } else {
            btns[1] && btns[1].classList.add('active');
        }
    },

    // Update topbar badge dots from counts
    _updateTopbarBadges() {
        const notifDot = document.getElementById('topbar-notification-dot');
        const blessingDot = document.getElementById('topbar-blessing-dot');

        if (notifDot) {
            const hasUnread = (this.notificationState && this.notificationState.unreadCount > 0);
            notifDot.classList.toggle('hidden', !hasUnread);
        }
        if (blessingDot) {
            const hasPending = this.counts.incomingPending > 0;
            blessingDot.classList.toggle('hidden', !hasPending);
        }
    },

    // Populate topbar domain from site config
    _updateTopbarDomain(baseUrl) {
        const el = document.getElementById('topbar-domain');
        const logo = document.getElementById('topbar-logo');
        if (el && baseUrl) {
            try {
                const domain = new URL(baseUrl).hostname;
                el.textContent = domain;
                if (logo) logo.href = baseUrl;
            } catch (e) {
                el.textContent = '';
            }
        }
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
        ['/',                            { mode: 'my-site', view: 'posts-published', screen: 'dashboard' }],
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
        ['/social',                      { mode: 'social',  view: 'conversations',    screen: 'dashboard' }],
        ['/social/feed',                 { mode: 'social',  view: 'conversations',    screen: 'dashboard' }],
        // Plugin routes injected by _registerPlugins()
        ['/social/following',            { mode: 'social',  view: 'following',        screen: 'dashboard' }],
        ['/social/followers',            { mode: 'social',  view: 'followers',        screen: 'dashboard' }],
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
        'following':           '/social/following',
        'followers':           '/social/followers',
        'settings':            '/settings',
    },

    // Social plugins: each entry defines a social view that gets a sidebar button,
    // route, and dispatch entry. Removing an entry removes the view entirely.
    SOCIAL_PLUGINS: [
        { id: 'pulse',         label: 'Pulse',         path: '/social/pulse',         title: 'Community Pulse',  actions: '',                                                                                                                                                              render: 'renderPulse',                autoRefresh: true  },
        { id: 'conversations', label: 'Feed', path: '/social/conversations', title: '',    actions: '', render: 'renderConversationsTabbed',   autoRefresh: true  },
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
    },

    // Register social plugins: inject routes, view paths, and sidebar buttons.
    // Must run before bindEvents() so dynamically created buttons get click handlers.
    _registerPlugins() {
        const nav = document.getElementById('social-plugins-nav');
        for (const plugin of this.SOCIAL_PLUGINS) {
            // Inject route
            const routeIdx = this.ROUTES.findIndex(([p]) => p === '/social/following');
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
                    // Show domain in header and topbar
                    this.updateDomainDisplay(status.base_url);
                    this._updateTopbarDomain(status.base_url);
                    this._updateThemeIcon();
                    this.siteBaseUrl = status.base_url || '';
                    this.avatarConfig = status.avatar || null;
                    this.authorName = status.author_name || '';
                    await this.loadAllCounts();
                    this._updateTopbarBadges();
                    this.initNotifications();
                    this.initSSE();
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
            // Unknown deep-link path — fall back to default
            await this.loadViewContent();
            this.showScreen('dashboard');
            window.history.replaceState({}, '', this.basePath + '/posts');
            return;
        }

        const { config, params } = route;

        // Normalize short-form URLs (e.g. /_/ → /_/posts, /_/social → /_/social/feed)
        if (config.view) {
            const canonical = this.pathForView(config.view);
            if (canonical !== pathname && pathname !== this.basePath + '/' && pathname !== this.basePath) {
                window.history.replaceState({}, '', canonical);
            } else if (pathname === this.basePath + '/' || pathname === this.basePath || pathname === '/') {
                window.history.replaceState({}, '', this.basePath + '/posts');
            }
        }

        if (config.screen === 'dashboard') {
            if (config.mode) {
                this.sidebarMode = config.mode;
                this._updateSidebarUI(config.mode);
            }
            this.currentView = config.view;
            if (config.tabHint) this._commentsPublishedFilter = config.tabHint;
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

    // Load all counts for sidebar badges via single consolidated endpoint
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
            this.counts.feed = c.feed || 0;
            this.counts.feedUnread = c.feed_unread || 0;
            this.counts.following = c.following || 0;
            this.counts.followers = c.followers || 0;
            this.notificationState.unreadCount = c.notifications_unread || 0;

            this.updateBadges();
            this.updateSidebar();
            this._updateNotificationDot();
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
        this.counts.feed = c.feed || 0;
        this.counts.feedUnread = c.feed_unread || 0;
        this.counts.following = c.following || 0;
        this.counts.followers = c.followers || 0;
        this.notificationState.unreadCount = c.notifications_unread || 0;

        this.updateBadges();
        this.updateSidebar();
        this._updateNotificationDot();
        this._updateTopbarBadges();

        // If on a view that shows items affected by sync, refresh it
        const autoRefreshViews = ['feed', 'blessing-requests', 'followers', 'comments-published',
            ...this.SOCIAL_PLUGINS.filter(p => p.autoRefresh).map(p => p.id)];
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

    // Update sidebar badges
    updateBadges() {
        this.updateBadge('posts-count', this.counts.posts);
        this.updateBadge('drafts-count', this.counts.drafts);
        // My comments (consolidated total)
        const totalComments = (this.counts.myCommentDrafts || 0) + (this.counts.myPending || 0)
                            + (this.counts.myBlessed || 0) + (this.counts.myDenied || 0);
        this.updateBadge('comments-published-count', totalComments);
        // Incoming (on my posts)
        this.updateBadge('incoming-pending-count', this.counts.incomingPending, true);
        // Social
        this.updateBadge('feed-count', this.counts.feedUnread, this.counts.feedUnread > 0);
        this.updateBadge('following-count', this.counts.following);
        this.updateBadge('followers-count', this.counts.followers);
    },

    updateBadge(id, count, isWarning = false) {
        const badge = document.getElementById(id);
        if (badge) {
            badge.textContent = count;
            badge.style.display = count > 0 ? 'inline' : 'none';
            if (isWarning && count > 0) {
                badge.classList.add('warning');
            } else {
                badge.classList.remove('warning');
            }
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

    // Update sidebar visibility based on lifecycle stage
    updateSidebar() {
        this.detectLifecycleStage();
        const stage = this.lifecycleStage;

        // My Site sidebar sections
        const sections = {
            posts: document.getElementById('sidebar-section-posts'),
            comments: document.getElementById('sidebar-section-comments'),
            snippets: document.getElementById('sidebar-section-snippets'),
        };

        // Social sidebar sections
        const socialSections = {
            discover: document.getElementById('sidebar-section-discover'),
        };

        // just_arrived: Write (new post btn) + Snippets (About) + Feed + Settings only
        // first_post: + Posts, Comments
        // active: + On My Posts, full Social
        if (stage === 'just_arrived') {
            if (sections.posts) sections.posts.classList.add('hidden');
            if (sections.comments) sections.comments.classList.add('hidden');
            if (sections.snippets) sections.snippets.classList.remove('hidden');
        } else if (stage === 'first_post') {
            if (sections.posts) sections.posts.classList.remove('hidden');
            if (sections.comments) sections.comments.classList.remove('hidden');
            if (sections.snippets) sections.snippets.classList.remove('hidden');
        } else {
            // active: show everything
            if (sections.posts) sections.posts.classList.remove('hidden');
            if (sections.comments) sections.comments.classList.remove('hidden');
            if (sections.snippets) sections.snippets.classList.remove('hidden');
        }
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
                    <p>Your comment was delivered. The author will decide whether to bless it.</p>
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

        if (postsViews.includes(this.currentView)) {
            if (contentHeader) contentHeader.classList.add('hidden');
        } else if (feedViews.includes(this.currentView)) {
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
                contentTitle.textContent = 'Following';
                contentActions.innerHTML = '<button class="primary" onclick="App.openFollowPanel()">Follow Author</button>';
                if (contentHeader) contentHeader.classList.remove('hidden');
                await this.renderFollowingList(contentList);
                break;

            case 'followers':
                contentTitle.textContent = 'Followers';
                contentActions.innerHTML = '<button class="secondary sync-btn" onclick="App.refreshFollowers(true)">Refresh</button>';
                if (contentHeader) contentHeader.classList.remove('hidden');
                await this.renderFollowersList(contentList);
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

        // Back button (editor)
        document.getElementById('back-btn').addEventListener('click', async () => {
            await this.loadAllCounts();
            history.back();
        });

        // Popstate handler — browser back/forward navigation
        window.addEventListener('popstate', async () => {
            await this._restoreRouteFromURL();
        });

        // Save draft button
        document.getElementById('save-draft-btn').addEventListener('click', async () => {
            await this.saveDraft();
        });

        // Publish button
        document.getElementById('publish-btn').addEventListener('click', async () => {
            await this.publish();
        });

        // Auto-generate filename from title and live preview as user types
        document.getElementById('markdown-input').addEventListener('input', (e) => {
            if (!this.filenameManuallySet && !this.currentPostPath) {
                const markdown = e.target.value;
                const title = this.extractTitleFromMarkdown(markdown);
                if (title) {
                    document.getElementById('filename-input').value = this.slugify(title);
                }
            }
            this.editorUpdatePreview();
        });

        // Editor frontmatter toggle
        const editorFmToggle = document.getElementById('editor-fm-toggle');
        if (editorFmToggle) {
            editorFmToggle.addEventListener('click', () => this.toggleEditorFrontmatter());
        }

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
        });

        // About editor events
        document.getElementById('about-back-btn').addEventListener('click', () => {
            history.back();
        });
        document.getElementById('about-publish-btn').addEventListener('click', () => {
            this.publishAbout();
        });
        let aboutPreviewTimeout = null;
        document.getElementById('about-editor-textarea').addEventListener('input', () => {
            if (aboutPreviewTimeout) clearTimeout(aboutPreviewTimeout);
            aboutPreviewTimeout = setTimeout(() => this.updateAboutPreview(), 300);
        });

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
        document.getElementById('markdown-input').value = '';
        document.getElementById('filename-input').value = '';
        document.getElementById('filename-input').disabled = false;
        document.getElementById('preview-content').innerHTML =
            '<p class="empty-state">Start writing to see a preview.</p>';

        this.updateEditorFmToggle();
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
        document.getElementById('comment-input').value = '';
        if (opts.pushState !== false) {
            window.history.pushState({}, '', this.pathForScreen('newComment'));
        }
        this.showScreen('comment');
    },

    // Alias for newComment (used by sidebar + button)
    newCommentDraft() {
        this.newComment();
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

    // Render posts list
    async renderPostsList(container) {
        try {
            const result = await this.api('GET', '/api/posts');
            const posts = result.posts || [];
            this.counts.posts = posts.length;
            this.updateBadge('posts-count', posts.length);

            if (posts.length === 0) {
                const domain = this.siteBaseUrl ? new URL(this.siteBaseUrl).hostname : '';
                const domainDisplay = domain ? `<a href="${this.escapeHtml(this.siteBaseUrl)}" target="_blank" rel="noopener">${this.escapeHtml(domain)}</a>` : 'your domain';
                container.innerHTML = this._renderPostsSubTabs('posts-published') + `
                    <div class="content-list">
                        <div class="empty-state">
                            <h3>No published posts yet</h3>
                            <p class="subtitle">Write your first post to make your site come alive.</p>
                            <button class="primary" onclick="App.newPost()">Write your first post</button>
                        </div>
                        <div class="welcome-divider"></div>
                        <div class="welcome-panels">
                            <details class="welcome-panel">
                                <summary><span class="icon">&#9656;</span> What to do next</summary>
                                <div class="welcome-panel-body">
                                    <ol class="next-steps">
                                        <li>
                                            <span class="step-number">1</span>
                                            <span class="step-content">
                                                ${this.siteBaseUrl ? `<a href="${this.escapeHtml(this.siteBaseUrl)}" target="_blank" rel="noopener">Visit your site</a>` : `<span>Visit your site</span>`}
                                                <span class="step-detail">See what readers see at ${domain ? this.escapeHtml(domain) : 'your domain'}.</span>
                                            </span>
                                        </li>
                                        <li>
                                            <span class="step-number">2</span>
                                            <span class="step-content">
                                                <a href="#" onclick="event.preventDefault(); navigator.clipboard.writeText(window.location.origin + '/_/').then(() => App.showToast('Dashboard URL copied!', 'success'))">Bookmark your dashboard</a>
                                                <span class="step-detail">The <code>/_/</code> path is your private dashboard &mdash; only you can see it.</span>
                                            </span>
                                        </li>
                                        <li>
                                            <span class="step-number">3</span>
                                            <span class="step-content">
                                                <a href="#" onclick="event.preventDefault(); App.openAboutEditor()">Update your About</a>
                                                <span class="step-detail">Tell visitors who you are.</span>
                                            </span>
                                        </li>
                                        <li>
                                            <span class="step-number">4</span>
                                            <span class="step-content">
                                                <a href="#" onclick="event.preventDefault(); App.navigateTo('/social/following')">Follow an author</a>
                                                <span class="step-detail">Discover conversations and build your feed.</span>
                                            </span>
                                        </li>
                                        <li>
                                            <span class="step-number">5</span>
                                            <span class="step-content">
                                                <a href="#" onclick="event.preventDefault(); navigator.clipboard.writeText('${this.escapeHtml(this.siteBaseUrl || '')}').then(() => App.showToast('URL copied!', 'success'))">Share your link</a>
                                                <span class="step-detail">Anyone can read your site &mdash; no account needed.</span>
                                            </span>
                                        </li>
                                        <li>
                                            <span class="step-number">6</span>
                                            <span class="step-content">
                                                <a href="#" onclick="event.preventDefault(); App.navigateTo('/settings')">Pick a theme</a>
                                                <span class="step-detail">Make it feel like yours.</span>
                                            </span>
                                        </li>
                                        <li>
                                            <span class="step-number">7</span>
                                            <span class="step-content">
                                                <a href="#" onclick="event.preventDefault(); App.navigateTo('/settings')">Download your keys</a>
                                                <span class="step-detail">Back them up somewhere safe. Everything you publish is cryptographically signed.</span>
                                            </span>
                                        </li>
                                    </ol>
                                </div>
                            </details>
                        </div>
                    </div>
                `;
                return;
            }

            container.innerHTML = this._renderPostsSubTabs('posts-published') + `
                <div class="post-list">
                    ${posts.map(post => {
                        const commentCount = post.comment_count || 0;
                        const commentHtml = commentCount > 0 ? `
                            <div class="comment-count${post.pending_comments ? ' has-pending' : ''}">
                                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
                                ${post.pending_comments ? `<span class="pending">${commentCount}</span>` : commentCount}
                            </div>` : '';
                        const excerptHtml = post.excerpt ? `<div class="post-excerpt">${this.escapeHtml(post.excerpt)}</div>` : '';
                        return `
                        <div class="post-row" onclick="App.openPost('${this.escapeHtml(post.path)}')">
                            <div class="post-info">
                                <div class="post-title">${this.escapeHtml(post.title)}</div>
                                ${excerptHtml}
                                <div class="post-meta"><span>${this.formatDate(post.published)}</span></div>
                            </div>
                            <div class="post-status">
                                ${commentHtml}
                                <span class="post-date">${this.formatRelativeTime(post.published)}</span>
                            </div>
                        </div>`;
                    }).join('')}
                </div>
            `;
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

    // Render drafts list
    async renderDraftsList(container) {
        try {
            const result = await this.api('GET', '/api/drafts');
            const drafts = result.drafts || [];
            this.counts.drafts = drafts.length;
            this.updateBadge('drafts-count', drafts.length);

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
                    ${drafts.map(draft => `
                        <div class="post-row" onclick="App.openDraft('${this.escapeHtml(draft.id)}')">
                            <div class="post-info">
                                <div class="post-title">${this.escapeHtml(draft.id)}</div>
                                <div class="post-meta"><span>Edited ${this.formatDate(draft.modified)}</span></div>
                            </div>
                            <div class="post-status">
                                <span class="draft-badge">Draft</span>
                            </div>
                        </div>
                    `).join('')}
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
        this.updateBadge('comments-published-count', total);

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
                    allBlessed.push({ ...c, post: pc.post });
                }
            }
        } catch (err) {
            container.innerHTML = this._renderPostsSubTabs('blessing-requests') + `<div class="post-list"><div class="empty-state"><h3>Failed to load</h3><p>${this.escapeHtml(err.message)}</p></div></div>`;
            return;
        }

        // Update counts
        this.counts.incomingPending = requests.length;
        this.counts.incomingBlessed = allBlessed.length;
        this.updateBadge('incoming-pending-count', requests.length, true);

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

        // Build items using post-row layout
        let items = '';
        if (currentFilter === 'pending' || currentFilter === 'all') {
            items += requests.map((r, idx) => {
                const date = r.created_at || r.timestamp;
                const slug = (r.in_reply_to || '').split('/').pop()?.replace(/\.md$/, '') || 'post';
                const title = `Re: ${slug}`;
                return `
                <div class="post-row" onclick="App.openPendingRequestDetail(${idx})">
                    <div class="post-info">
                        <div class="post-title">${this.escapeHtml(title)}</div>
                        <div class="post-meta">
                            <span class="comment-status-badge pending">pending</span>
                            ${r.author ? `<span class="sep">&middot;</span><span>${this.escapeHtml(r.author)}</span>` : ''}
                        </div>
                    </div>
                    <div class="post-status">
                        <span class="post-date">${this.formatRelativeTime(date)}</span>
                    </div>
                </div>`;
            }).join('');
        }
        if (currentFilter === 'blessed' || currentFilter === 'all') {
            items += allBlessed.map((c, idx) => {
                const domain = this.extractDomainFromUrl(c.post);
                return `
                <div class="post-row" onclick="App.openBlessedCommentDetail(${idx})">
                    <div class="post-info">
                        <div class="post-title">${this.escapeHtml(c.url ? c.url.split('/').pop() : 'comment')}</div>
                        <div class="post-meta">
                            <span class="comment-status-badge blessed">blessed</span>
                            ${domain ? `<span class="sep">&middot;</span><span>${this.escapeHtml(domain)}</span>` : ''}
                        </div>
                    </div>
                    <div class="post-status">
                        <span class="post-date">${this.formatRelativeTime(c.blessed_at)}</span>
                    </div>
                </div>`;
            }).join('');
        }

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

            const themes = settings.themes || [];

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
                                <div class="settings-row-actions">
                                    <button class="btn-copy" id="site-title-edit-btn" onclick="App.editSiteTitle()">Edit</button>
                                </div>
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

                    <div class="settings-section">
                        <div class="settings-section-label">Webapp Appearance</div>
                        <div class="settings-card">
                            <div class="settings-row">
                                <span class="settings-row-label">Color Mode:</span>
                                <span class="settings-row-value">${this.webappTheme === 'light' ? 'Light' : 'Dark'}</span>
                                <div class="settings-row-actions">
                                    <button class="btn-copy" onclick="App.toggleTheme()">${this.webappTheme === 'light' ? 'Switch to Dark' : 'Switch to Light'}</button>
                                </div>
                            </div>
                        </div>
                    </div>

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

    editSiteTitle() {
        const display = document.getElementById('site-title-display');
        const btn = document.getElementById('site-title-edit-btn');
        if (!display || !btn) return;

        const current = display.textContent === 'Not configured' ? '' : display.textContent;
        display.innerHTML = `<input type="text" id="site-title-input" value="${this.escapeHtml(current)}" style="font-size:0.85rem;font-family:var(--font-mono);background:var(--bg-light);border:1px solid var(--border-color);color:var(--text-color);padding:0.25rem 0.5rem;border-radius:3px;width:100%;">`;
        btn.textContent = 'Save';
        btn.onclick = () => App.saveSiteTitle();

        const input = document.getElementById('site-title-input');
        if (input) { input.focus(); input.select(); }
    },

    async saveSiteTitle() {
        const input = document.getElementById('site-title-input');
        const display = document.getElementById('site-title-display');
        const btn = document.getElementById('site-title-edit-btn');
        if (!input || !display || !btn) return;

        const newTitle = input.value.trim();
        try {
            const result = await this.api('POST', '/api/settings/site-title', { site_title: newTitle });
            display.textContent = result.site_title || 'Not configured';
            { const dd = document.getElementById('domain-display'); if (dd) dd.textContent = result.site_title || ''; }
            btn.textContent = 'Edit';
            btn.onclick = () => App.editSiteTitle();
            this.showToast('Site title updated', 'success');
        } catch (err) {
            this.showToast('Failed to update title: ' + err.message, 'error');
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

    // Render markdown preview (always body-only, frontmatter shown separately)
    async renderPreview() {
        const body = document.getElementById('markdown-input').value;
        const previewContent = document.getElementById('preview-content');

        if (!body.trim()) {
            previewContent.innerHTML = '<p class="empty-state">Start writing to see a preview.</p>';
            return;
        }

        try {
            const result = await this.api('POST', '/api/render', { markdown: body });
            previewContent.innerHTML = result.html;
        } catch (err) {
            previewContent.innerHTML = `<p class="error">Render failed: ${this.escapeHtml(err.message)}</p>`;
        }
    },

    // Debounced live preview for editor (300ms, always body-only)
    editorUpdatePreview: (function() {
        let timeout = null;
        return function() {
            if (timeout) clearTimeout(timeout);
            timeout = setTimeout(async () => {
                const body = document.getElementById('markdown-input')?.value || '';
                const previewContent = document.getElementById('preview-content');
                if (!previewContent) return;

                if (!body.trim()) {
                    previewContent.innerHTML = '<p class="empty-state">Start writing to see a preview.</p>';
                    return;
                }

                try {
                    const result = await App.api('POST', '/api/render', { markdown: body });
                    previewContent.innerHTML = result.html;
                } catch (err) {
                    // Don't show errors during typing — leave last good preview
                }
            }, 300);
        };
    })(),

    // Save draft
    async saveDraft() {
        const markdown = document.getElementById('markdown-input').value;

        if (!markdown.trim()) {
            this.showToast('Nothing to save', 'warning');
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

        try {
            const result = await this.api('POST', '/api/drafts', { id, markdown });
            this.currentDraftId = result.id;
            this.showToast('Draft saved', 'success');
        } catch (err) {
            this.showToast('Failed to save draft: ' + err.message, 'error');
        }
    },

    // Publish or republish post
    async publish() {
        const markdown = document.getElementById('markdown-input').value;

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
                    filename: filenameInput || ''
                });
            }

            if (result.success) {
                const action = isRepublish ? 'Republished' : 'Published';
                this.showToast(`${action}: ${result.title}`, 'success');

                // Clear editor and return to dashboard
                this.currentDraftId = null;
                this.currentPostPath = null;
                document.getElementById('markdown-input').value = '';
                document.getElementById('preview-content').innerHTML =
                    '<p class="empty-state">Start writing to see a preview.</p>';

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
            document.getElementById('markdown-input').value = result.markdown;
            document.getElementById('filename-input').value = id;  // Draft ID is the filename
            document.getElementById('filename-input').disabled = false;

            this.updateEditorFmToggle();
            this.updatePublishButton();
            if (opts.pushState !== false) {
                window.history.pushState({}, '', this.pathForScreen('openDraft', { id }));
            }
            this.showScreen('editor');
            this.editorUpdatePreview();
        } catch (err) {
            this.showToast('Failed to load draft: ' + err.message, 'error');
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
            document.getElementById('markdown-input').value = result.markdown;

            this.updateEditorFmToggle();
            this.updatePublishButton();
            if (opts.pushState !== false) {
                window.history.pushState({}, '', this.pathForScreen('openPost', { path: cleanPath }));
            }
            this.showScreen('editor');
            this.editorUpdatePreview();
        } catch (err) {
            this.showToast('Failed to load post: ' + err.message, 'error');
        }
    },

    // Show/hide frontmatter toggle based on editing context
    updateEditorFmToggle() {
        const btn = document.getElementById('editor-fm-toggle');
        if (!btn) return;
        if (this.currentPostPath) {
            btn.classList.remove('hidden');
            btn.textContent = 'Show FM';
        } else {
            btn.classList.add('hidden');
        }
    },

    // Update publish button text and filename visibility based on current state
    updatePublishButton() {
        const btn = document.getElementById('publish-btn');
        const filenameContainer = document.getElementById('filename-container');
        const filenameInput = document.getElementById('filename-input');

        if (this.currentPostPath) {
            // Republishing - filename is locked
            btn.textContent = 'Republish';
            filenameContainer.style.display = 'none';
        } else {
            // New post - filename is editable
            btn.textContent = 'Publish';
            filenameContainer.style.display = 'flex';
            filenameInput.disabled = false;
        }
    },

    // Open a comment draft for editing
    async openCommentDraft(id, opts = {}) {
        try {
            const draft = await this.api('GET', `/api/comments/drafts/${encodeURIComponent(id)}`);
            this.currentCommentDraftId = id;
            document.getElementById('reply-to-url').value = draft.in_reply_to || '';
            document.getElementById('comment-input').value = draft.content || '';
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
        const content = document.getElementById('comment-input').value;

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
        const content = document.getElementById('comment-input').value;

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
            document.getElementById('comment-input').value = '';

            // Switch to comments published view with pending filter
            this._commentsPublishedFilter = 'pending';
            this.currentView = 'comments-published';
            await this.loadAllCounts();
            this.updateSidebar(); // Ensure lifecycle recalculated after count update
            this.fetchNotificationCount(); // Immediate notification refresh after beseech
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
            document.getElementById('about-editor-textarea').value = result.content || '';
            this.showScreen('about');
            this.updateAboutPreview();
        } catch (err) {
            this.showToast('Failed to load about content: ' + err.message, 'error');
        }
    },

    // Update the about editor live preview
    async updateAboutPreview() {
        const textarea = document.getElementById('about-editor-textarea');
        const preview = document.getElementById('about-editor-preview');
        if (!textarea || !preview) return;

        const content = textarea.value;
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
            const textarea = document.getElementById('about-editor-textarea');
            await this.api('POST', '/api/about', { content: textarea.value });
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

    async renderConversationsTabbed(container) {
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
        const markLabel = isUnread ? 'Mark read' : 'Mark unread';

        let threadHtml = '';
        if (group.total_comments > 0) {
            threadHtml = `
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
                    <button class="hover-btn" onclick="event.stopPropagation(); App.${isUnread ? 'markFeedRead' : 'markFeedUnread'}(JSON.parse(this.closest('.feed-item').dataset.ids))">${markLabel}</button>
                </div>
                <div class="item-top">
                    <div class="author-row">
                        <div class="author-avatar" style="${hasCustomAvatar ? this._buildAvatarStyle(customAvatar) : `background: ${avatar.color};`}">${hasCustomAvatar ? '' : avatar.initials}</div>
                        <div>
                            <span class="author-name">${this.escapeHtml(authorName)}</span>
                            <span class="author-domain">&middot; ${this.escapeHtml(domain)}</span>
                        </div>
                    </div>
                    <span class="item-time">${this.escapeHtml(time)}</span>
                </div>
                <div class="item-title">${this.escapeHtml(title)}</div>
                ${group.post_excerpt && !group.post_excerpt.toLowerCase().startsWith(title.toLowerCase().slice(0, 30)) ? `<div class="item-excerpt">${this.escapeHtml(group.post_excerpt)}</div>` : ''}
                ${threadHtml}
            </div>
        `;
    },

    async _handleGroupClick(el) {
        const itemIds = JSON.parse(el.dataset.ids || '[]');
        const url = el.dataset.url || '';
        if (!itemIds || itemIds.length === 0) return;
        try {
            // Mark all items in this group as read on the server
            await Promise.all(itemIds.map(id =>
                this.api('POST', '/api/feed/read', { id })
            ));
            // Update counts and re-render (same pattern as markAllConversationsRead)
            this.counts.feedUnread = Math.max(0, (this.counts.feedUnread || 0) - 1);
            this.updateBadge('feed-count', this.counts.feedUnread, this.counts.feedUnread > 0);
            if (this.currentView === 'conversations') {
                const contentList = document.getElementById('content-list');
                if (contentList) await this.renderConversationsTabbed(contentList);
            }
        } catch (err) {
            this.showToast('Failed to mark as read: ' + err.message, 'error');
        }
        // Open the URL in a background tab
        if (url && url !== '#') window.open(url, '_blank', 'noopener');
    },

    async _renderPostsCommentsSubtab(container, filterHtml) {
        try {
            container.innerHTML = filterHtml + '<div class="feed-list"><div class="empty-state"><p>Loading...</p></div></div>';
            const result = await this.api('GET', '/api/feed/grouped');
            const groups = result.groups || [];

            this.counts.feedUnread = result.unread_items || 0;
            this.updateBadge('feed-count', this.counts.feedUnread, this.counts.feedUnread > 0);

            if (groups.length === 0) {
                const emptyMsg = this.counts.following === 0
                    ? `<h3>No posts or comments yet</h3><p>Follow someone to see their posts here.</p><button class="primary" onclick="App.openFollowPanel()">Follow an author</button>`
                    : `<h3>No items</h3><p>No posts or comments in the feed yet. Click Refresh to check for new content.</p>`;
                container.innerHTML = filterHtml + `<div class="feed-list"><div class="empty-state">${emptyMsg}</div></div>`;

                if (!this._conversationsRefreshing) this._autoRefreshConversations();
                return;
            }

            container.innerHTML = filterHtml + `
                <div class="feed-list">
                    ${groups.map(g => this._renderGroupedItem(g)).join('')}
                </div>
            `;

            // Fetch remote avatars for domains not yet cached
            this._fetchRemoteAvatarsForGroups(groups);

            if (!this._conversationsRefreshing) this._autoRefreshConversations();
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

            // Fetch grouped feed and activity in parallel
            const [groupedResult, activityResult] = await Promise.all([
                this.api('GET', '/api/feed/grouped'),
                this.api('GET', `/api/activity?since=${this._activityCursor}&limit=100`),
            ]);

            const groups = groupedResult.groups || [];
            this.counts.feedUnread = groupedResult.unread_items || 0;
            this.updateBadge('feed-count', this.counts.feedUnread, this.counts.feedUnread > 0);

            const activityEvents = activityResult.events || [];
            if (activityEvents.length > 0) {
                this._mergeActivityEvents(activityEvents);
                this._activityCursor = activityResult.cursor || this._activityCursor;
            }

            // Filter activity events that are already covered by feed groups
            const feedEventTypes = new Set([
                'pub.polis.post.published', 'pub.polis.post.republished',
                'pub.polis.comment.published', 'pub.polis.comment.republished'
            ]);
            const filteredActivity = [...this._activityEvents].reverse().filter(
                evt => !feedEventTypes.has(evt.type)
            );

            // Build merged timeline entries
            const entries = [];
            for (const g of groups) {
                entries.push({ type: 'group', data: g, timestamp: g.last_activity });
            }
            for (const evt of filteredActivity) {
                entries.push({ type: 'activity', data: evt, timestamp: evt.timestamp });
            }

            // Sort by timestamp descending (parse to handle non-ISO formats)
            entries.sort((a, b) => {
                const ta = a.timestamp ? new Date(a.timestamp).getTime() : 0;
                const tb = b.timestamp ? new Date(b.timestamp).getTime() : 0;
                return (tb || 0) - (ta || 0);
            });

            if (entries.length === 0) {
                const emptyMsg = this.counts.following === 0
                    ? `<h3>Your feed is empty</h3><p>Follow someone to see their posts here.</p><button class="primary" onclick="App.openFollowPanel()">Follow an author</button>`
                    : `<h3>Nothing new</h3><p>No items yet. Click Refresh to check for new content.</p>`;
                container.innerHTML = filterHtml + `<div class="feed-list"><div class="empty-state">${emptyMsg}</div></div>`;

                if (!this._conversationsRefreshing) this._autoRefreshConversations();
                return;
            }

            const markReadBtn = (this.counts.feedUnread || 0) > 0
                ? `<button class="feed-btn mark-read" onclick="App.markAllConversationsRead()">Mark all read</button>`
                : '';
            container.innerHTML = filterHtml + `
                <div class="feed-list">
                    <div class="feed-date-sep">
                        <span>Recent</span>
                        ${markReadBtn}
                    </div>
                    ${entries.map(e => {
                        if (e.type === 'group') return this._renderGroupedItem(e.data);
                        return this.renderActivityEvent(e.data);
                    }).join('')}
                </div>
            `;

            // Fetch remote avatars for domains in feed groups
            const feedGroups = entries.filter(e => e.type === 'group').map(e => e.data);
            this._fetchRemoteAvatarsForGroups(feedGroups);

            if (!this._conversationsRefreshing) this._autoRefreshConversations();
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

            this.counts.feed = result.total || 0;
            this.counts.feedUnread = result.unread || 0;
            this.updateBadge('feed-count', this.counts.feedUnread, this.counts.feedUnread > 0);

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

            this.fetchNotificationCount();
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

            this.counts.feed = result.total || 0;
            this.counts.feedUnread = result.unread || 0;
            this.updateBadge('feed-count', this.counts.feedUnread, this.counts.feedUnread > 0);

            if (newItems > 0) {
                this.showToast(`${newItems} new item${newItems > 1 ? 's' : ''}`, 'success');
                if (this.currentView === 'conversations') {
                    const contentList = document.getElementById('content-list');
                    if (contentList) await this.renderConversationsTabbed(contentList);
                }
            }
        } catch (err) {
            console.error('Auto-refresh failed:', err);
        } finally {
            this._conversationsRefreshing = false;
        }
    },

    _updateMarkAllReadBtn() {
        const btn = document.getElementById('mark-all-read-btn');
        if (btn) btn.disabled = (this.counts.feedUnread || 0) === 0;
    },

    async markAllConversationsRead() {
        try {
            await this.api('POST', '/api/feed/read', { all: true });
            this.counts.feedUnread = 0;
            this.updateBadge('feed-count', 0);

            if (this.currentView === 'conversations') {
                const contentList = document.getElementById('content-list');
                if (contentList) await this.renderConversationsTabbed(contentList);
            }
            this.showToast('All items marked as read', 'success');
        } catch (err) {
            this.showToast('Failed: ' + err.message, 'error');
        }
    },

    async markFeedRead(itemIds) {
        try {
            await Promise.all(itemIds.map(id =>
                this.api('POST', '/api/feed/read', { id })
            ));
            this.counts.feedUnread = Math.max(0, (this.counts.feedUnread || 0) - itemIds.length);
            this.updateBadge('feed-count', this.counts.feedUnread, this.counts.feedUnread > 0);

            if (this.currentView === 'conversations') {
                const contentList = document.getElementById('content-list');
                if (contentList) await this.renderConversationsTabbed(contentList);
            }
        } catch (err) {
            this.showToast('Failed: ' + err.message, 'error');
        }
    },

    async markFeedUnread(itemIds) {
        try {
            await Promise.all(itemIds.map(id =>
                this.api('POST', '/api/feed/read', { id, unread: true })
            ));
            this.counts.feedUnread = Math.min((this.counts.feedUnread || 0) + 1, Infinity);
            this.updateBadge('feed-count', this.counts.feedUnread, true);

            if (this.currentView === 'conversations') {
                const contentList = document.getElementById('content-list');
                if (contentList) await this.renderConversationsTabbed(contentList);
            }
        } catch (err) {
            this.showToast('Failed: ' + err.message, 'error');
        }
    },

    async markUnreadFromHere(id) {
        try {
            await this.api('POST', '/api/feed/read', { from_id: id });
            const counts = await this.api('GET', '/api/feed/counts');
            this.counts.feedUnread = counts.unread || 0;
            this.updateBadge('feed-count', this.counts.feedUnread, this.counts.feedUnread > 0);

            if (this.currentView === 'conversations') {
                const contentList = document.getElementById('content-list');
                if (contentList) await this.renderConversationsTabbed(contentList);
            }
        } catch (err) {
            this.showToast('Failed: ' + err.message, 'error');
        }
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

            container.innerHTML = `
                <div class="content-list">
                    ${follows.map(f => {
                        const domain = f.url.replace('https://', '').replace('http://', '').replace(/\/$/, '');
                        const title = f.site_title || f.author_name || domain;
                        const subtitle = f.author_name && f.author_name !== title
                            ? `${this.escapeHtml(f.author_name)} · ${this.escapeHtml(domain)}`
                            : this.escapeHtml(domain);
                        const addedAt = f.added_at ? this.formatDate(f.added_at) : '';
                        return `
                            <div class="content-item following-item">
                                <div class="item-info">
                                    <div class="item-title">${this.escapeHtml(title)}</div>
                                    <div class="item-path">${subtitle}</div>
                                </div>
                                <div class="following-item-actions">
                                    ${addedAt ? `<span class="item-date">Followed: ${addedAt}</span>` : ''}
                                    <button class="danger-small" onclick="event.stopPropagation(); App.unfollowAuthor('${this.escapeHtml(f.url)}')">Unfollow</button>
                                </div>
                            </div>
                        `;
                    }).join('')}
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
        if (input) { input.value = ''; input.focus(); }
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
    normalizeFollowInput(raw) {
        let val = raw.trim();
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
                if (author) return 'https://' + author + '/';
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

            // Filter unfollowed author from cached activity data
            try {
                const unfollowedDomain = new URL(url).hostname;
                this._activityEvents = this._activityEvents.filter(evt => evt.actor !== unfollowedDomain);
            } catch (e) {}
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
        const linkAttrs = linkUrl ? ` href="${this.escapeHtml(linkUrl)}" target="_blank" rel="noopener" style="text-decoration:none;color:inherit;"` : '';

        return `
            <${tag}${linkAttrs} class="activity-item">
                <span class="act-dot"></span>
                <span><span class="act-who">${this.escapeHtml(evt.actor)}</span> ${actionLabel}${detail ? ' — ' + detail : ''}</span>
                <span class="act-when">${this.formatRelativeTime(evt.timestamp)}</span>
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
            this.updateBadge('followers-count', count);

            if (followers.length === 0) {
                container.innerHTML = `<div class="content-list"><div class="empty-state">
                    <h3>No followers yet</h3>
                    <p>When other polis authors follow you, they'll appear here.</p>
                </div></div>`;
                return;
            }

            container.innerHTML = `
                <div class="content-list">
                    <div class="followers-summary">${count} follower${count !== 1 ? 's' : ''}</div>
                    ${followers.map(domain => `
                        <div class="content-item follower-item">
                            <div class="item-info">
                                <div class="item-title">${this.escapeHtml(domain)}</div>
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
                    this.updateBadge('followers-count', this.counts.followers);
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

        // Polling fallback: refresh counts every 60s regardless of SSE.
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
        }, 60000);
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
                        'followers': '/social/followers',
                        'feed': '/social/conversations',
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
            await fetch('/api/widget/token', { credentials: 'same-origin' });
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
        document.getElementById('comment-input').value = intent.text || '';
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
            subtitle: 'The post author will decide whether to bless it.',
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
};

// Start the app
document.addEventListener('DOMContentLoaded', () => App.init());
