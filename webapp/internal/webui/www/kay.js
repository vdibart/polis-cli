// The Kay page (/_/kay): the owner asks their AI for changes to the site and
// reads the conversation. Kay works through v0; v0 acts on the site only
// through the site's own /mcp, under a key the instance mints with the render
// scope. This page talks to its own origin and nothing else.
//
//   GET    /api/kay              connected? registered?
//   POST   /api/kay/connect      store the owner's v0 key
//   DELETE /api/kay/connect      disconnect
//   POST   /api/kay/ask          start or continue a chat; returns at once
//   GET    /api/kay/chats/{id}   the chat as it stands, plus this site's calls
(function () {
    'use strict';

    const POLL_MS = 2000;
    const POLL_LIMIT_MS = 5 * 60 * 1000;
    const CHAT_KEY = 'polis-kay-chat';

    // Per-viewer convenience only: which chat this browser was in.
    function savedChat() {
        try { return JSON.parse(localStorage.getItem(CHAT_KEY) || 'null'); } catch (e) { return null; }
    }
    function saveChat(c) {
        try { c ? localStorage.setItem(CHAT_KEY, JSON.stringify(c)) : localStorage.removeItem(CHAT_KEY); } catch (e) { /* storage blocked */ }
    }

    async function call(method, path, body) {
        const opts = { method, headers: { 'Content-Type': 'application/json' } };
        if (body) opts.body = JSON.stringify(body);
        const res = await fetch(path, opts);
        if (res.status === 204) return {};
        let data = {};
        try { data = await res.json(); } catch (e) { /* not JSON */ }
        if (!res.ok) throw new Error(data.error || res.statusText || ('HTTP ' + res.status));
        return data;
    }

    function esc(s) {
        return String(s == null ? '' : s)
            .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    }

    // Plain text with paragraphs, `code` and **bold**; v0's replies are short prose.
    function prose(s) {
        return esc(s).split(/\n{2,}/).map(p =>
            '<p>' + p.replace(/`([^`]+)`/g, '<code>$1</code>').replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
                .replace(/\n/g, '<br>') + '</p>').join('');
    }

    const state = { root: null, chat: null, polling: null, pollStart: 0, busy: false, applied: {} };

    // ⭐ The app follows the site: when this site's own log shows Kay switched
    // the public theme or wrote the site's own CSS, the app's chrome follows,
    // with no reload. The log, not v0's account of it, says the change happened.
    // Shared with point and ask (kay-point.js): calls before askedAt are an
    // earlier turn's, and applied records the calls already followed.
    function applyCalls(calls, askedAt, applied) {
        const done = [];
        for (const c of calls || []) {
            if (!c.ok || applied[c.ts]) continue;
            if (askedAt && Date.parse(c.ts) < Date.parse(askedAt)) continue; // an earlier turn
            if (c.tool === 'set_nav_shortcuts') {
                applied[c.ts] = true;
                done.push(c.tool);
                // A shortcut Kay added appears in the nav bar at once.
                if (window.App && typeof window.App.renderNavShortcuts === 'function') window.App.renderNavShortcuts();
                continue;
            }
            if (c.tool === 'set_custom_css') {
                applied[c.ts] = true;
                done.push(c.tool);
                // The site's own CSS changed: the app follows its palette too.
                if (window.App && typeof window.App.followSiteLook === 'function') window.App.followSiteLook();
                continue;
            }
            if (c.tool === 'set_stream_marks') {
                applied[c.ts] = true;
                done.push(c.tool);
                continue; // the stream is redrawn by the surface showing it
            }
            if (c.tool !== 'set_theme') continue;
            applied[c.ts] = true;
            done.push(c.tool);
            let theme = '';
            try { theme = JSON.parse(c.args || '{}').theme || ''; } catch (e) { /* not JSON */ }
            if (theme && window.App && typeof window.App.applySiteTheme === 'function') {
                window.App.applySiteTheme(theme);
                // The site's own CSS stays over a new theme; follow it there too.
                if (typeof window.App.followSiteLook === 'function') window.App.followSiteLook();
            }
        }
        return done;
    }

    function followSiteTheme(calls) {
        applyCalls(calls, state.chat && state.chat.askedAt, state.applied);
    }

    // ⚠️ render can run twice for one visit: the app's start-up finishes after
    // the owner has navigated here and loads the current view again. So a
    // re-render keeps what is being typed, and a turn in progress keeps being
    // followed.
    async function render(container) {
        // Already showing the conversation here: leave it be. Clearing it, even
        // briefly, drops keystrokes typed in the gap.
        if (state.root === container && container.querySelector('.kay-composer')) {
            if (state.busy && !state.polling) startPolling();
            return;
        }
        const box = container.querySelector('.kay-input');
        const draft = box ? box.value : '';
        const wasBusy = state.busy;
        stopPolling();
        state.root = container;
        container.innerHTML = '<div class="kay-page"><div class="kay-loading">Loading…</div></div>';
        let status;
        try {
            status = await call('GET', '/api/kay');
        } catch (e) {
            container.innerHTML = '<div class="kay-page"><p class="kay-error">' + esc(e.message) + '</p></div>';
            return;
        }
        if (!status.connected) {
            renderConnect(container);
            return;
        }
        renderChat(container);
        if (draft) container.querySelector('.kay-input').value = draft;
        const saved = savedChat();
        if (saved && saved.id) {
            state.chat = saved;
            if (wasBusy) {
                setBusy(true);
                startPolling();
            } else {
                refresh(true);
            }
        }
    }

    function header() {
        return '<header class="kay-header"><h1 class="kay-title">Kay</h1>' +
            '<p class="kay-sub">Ask for changes to your site in plain words. Kay works through v0, and can ' +
            'read your site, switch its theme and write its CSS. Nothing it does is signed in your name.</p></header>';
    }

    function renderConnect(container) {
        container.innerHTML = '<div class="kay-page">' + header() +
            '<section class="kay-card kay-connect">' +
            '<h2>Connect Kay to v0</h2>' +
            '<p>Create a key at <a href="https://v0.app/settings/keys" target="_blank" rel="noopener">v0.app/settings/keys</a> ' +
            'and paste it here. It stays on this site and is never shown again.</p>' +
            '<form class="kay-connect-form"><input type="password" class="kay-key" autocomplete="off" ' +
            'placeholder="v0 API key" aria-label="v0 API key"><button type="submit" class="kay-btn">Connect</button></form>' +
            '<p class="kay-error hidden"></p></section></div>';
        const form = container.querySelector('.kay-connect-form');
        form.addEventListener('submit', async (ev) => {
            ev.preventDefault();
            const input = form.querySelector('.kay-key');
            const err = container.querySelector('.kay-error');
            const btn = form.querySelector('button');
            btn.disabled = true;
            err.classList.add('hidden');
            try {
                await call('POST', '/api/kay/connect', { v0_key: input.value });
                input.value = '';
                render(container);
            } catch (e) {
                err.textContent = e.message;
                err.classList.remove('hidden');
                btn.disabled = false;
            }
        });
    }

    function renderChat(container) {
        container.innerHTML = '<div class="kay-page">' + header() +
            '<div class="kay-conversation" aria-live="polite"><p class="kay-empty">Try <em>“Make my site feel like ' +
            'autumn, with burnt orange for links and headings.”</em></p></div>' +
            '<form class="kay-composer"><textarea class="kay-input" rows="2" placeholder="Ask Kay…" ' +
            'aria-label="Ask Kay"></textarea><button type="submit" class="kay-btn">Send</button></form>' +
            '<div class="kay-footer"><button type="button" class="kay-link kay-new">New conversation</button>' +
            '<a class="kay-link kay-v0 hidden" target="_blank" rel="noopener">Open in v0</a>' +
            '<button type="button" class="kay-link kay-disconnect">Disconnect</button></div></div>';
        const form = container.querySelector('.kay-composer');
        const input = form.querySelector('.kay-input');
        form.addEventListener('submit', (ev) => { ev.preventDefault(); ask(input); });
        input.addEventListener('keydown', (ev) => {
            if (ev.key === 'Enter' && (ev.metaKey || ev.ctrlKey)) { ev.preventDefault(); ask(input); }
        });
        container.querySelector('.kay-new').addEventListener('click', () => {
            stopPolling();
            state.chat = null;
            saveChat(null);
            renderChat(container);
        });
        container.querySelector('.kay-disconnect').addEventListener('click', async () => {
            if (!window.confirm('Disconnect Kay? Your v0 key and this site\'s registration with v0 are removed.')) return;
            stopPolling();
            try {
                await call('DELETE', '/api/kay/connect');
                state.chat = null;
                saveChat(null);
                state.root = null; // a full render: the connect form, not the kept conversation
                render(container);
            } catch (e) {
                showError(e.message);
            }
        });
    }

    async function ask(input) {
        const message = input.value.trim();
        if (!message || state.busy) return;
        state.busy = true;
        setBusy(true);
        try {
            const res = await call('POST', '/api/kay/ask', { message, chat_id: state.chat ? state.chat.id : '' });
            const prev = state.chat || {};
            state.chat = { id: res.chat_id, webUrl: res.web_url || prev.webUrl, since: prev.since || res.asked_at, askedAt: res.asked_at };
            saveChat(state.chat);
            input.value = '';
            appendPending(message);
            startPolling();
        } catch (e) {
            showError(e.message);
            state.busy = false;
            setBusy(false);
        }
    }

    function setBusy(on) {
        if (!state.root) return;
        const btn = state.root.querySelector('.kay-composer button');
        if (btn) { btn.disabled = on; btn.textContent = on ? 'Working…' : 'Send'; }
    }

    function appendPending(message) {
        const conv = state.root.querySelector('.kay-conversation');
        const empty = conv.querySelector('.kay-empty');
        if (empty) empty.remove();
        conv.insertAdjacentHTML('beforeend',
            '<div class="kay-msg kay-user kay-pending-user">' + prose(message) + '</div>' +
            '<div class="kay-msg kay-assistant kay-pending"><div class="kay-steps"><div class="kay-step kay-working">' +
            'Kay is thinking…</div></div></div>');
        conv.scrollTop = conv.scrollHeight;
    }

    function startPolling() {
        stopPolling();
        state.pollStart = Date.now();
        state.polling = setInterval(() => refresh(false), POLL_MS);
        refresh(false);
    }

    function stopPolling() {
        if (state.polling) clearInterval(state.polling);
        state.polling = null;
    }

    async function refresh(once) {
        if (!state.chat || !state.root) return;
        let data;
        try {
            const q = state.chat.since ? '?since=' + encodeURIComponent(state.chat.since) : '';
            data = await call('GET', '/api/kay/chats/' + encodeURIComponent(state.chat.id) + q);
        } catch (e) {
            stopPolling();
            state.busy = false;
            setBusy(false);
            if (once) { state.chat = null; saveChat(null); return; }
            showError(e.message);
            return;
        }
        if (data.web_url) state.chat.webUrl = data.web_url;
        // Only this turn's calls move the app: an old switch replayed on a
        // reload of the page must not undo a later one.
        if (!once) followSiteTheme(data.calls);
        draw(data);
        const timedOut = Date.now() - state.pollStart > POLL_LIMIT_MS;
        if (data.done || once || timedOut) {
            stopPolling();
            state.busy = false;
            setBusy(false);
            if (timedOut && !data.done) showError('Kay is taking longer than usual; open the chat in v0 to see it.');
        }
    }

    // Each tool step is matched, in order, with the call this site served for
    // it, which carries the outcome v0 does not report.
    function draw(data) {
        const conv = state.root.querySelector('.kay-conversation');
        const v0link = state.root.querySelector('.kay-v0');
        // ⛔ v0's link is shown only if it is https: a javascript: URL from a
        // hostile or broken response would run in this app's origin on click.
        if (v0link && /^https:\/\//i.test(state.chat.webUrl || '')) { v0link.href = state.chat.webUrl; v0link.classList.remove('hidden'); }
        const calls = (data.calls || []).slice();
        const take = (tool) => {
            const i = calls.findIndex(c => c.tool === tool);
            return i < 0 ? null : calls.splice(i, 1)[0];
        };
        const msgs = data.messages || [];
        if (msgs.length === 0) return;
        conv.innerHTML = msgs.map(m => {
            if (m.role === 'user') return '<div class="kay-msg kay-user">' + prose(m.content) + '</div>';
            const steps = (m.steps || []).filter(s => s.kind === 'tool' || s.kind === 'thinking').map(s => {
                if (s.kind === 'thinking') {
                    return '<div class="kay-step kay-thought">' + esc(s.label || 'Thinking') + '</div>';
                }
                const c = take(s.tool);
                const mark = c ? (c.ok ? '<span class="kay-ok">✓</span>' : '<span class="kay-fail">✗</span>') :
                    (s.finished ? '' : '<span class="kay-spin">…</span>');
                const outcome = c ? (c.ok ? (c.duration_ms / 1000).toFixed(1) + ' s' : esc(c.error || 'failed')) : '';
                const css = s.tool === 'set_custom_css' && s.input && s.input.css
                    ? '<pre class="kay-css">' + esc(s.input.css) + '</pre>' : '';
                return '<div class="kay-step kay-tool">' + mark + ' <strong>' + esc(s.label || s.tool) + '</strong>' +
                    ' <code>' + esc(s.tool) + '</code>' + (outcome ? ' <span class="kay-outcome">' + outcome + '</span>' : '') +
                    (s.reason ? '<div class="kay-reason">' + esc(s.reason) + '</div>' : '') + css + '</div>';
            }).join('');
            const working = !m.done && !m.content ? '<div class="kay-step kay-working">Kay is working…</div>' : '';
            return '<div class="kay-msg kay-assistant">' +
                (steps || working ? '<div class="kay-steps">' + steps + working + '</div>' : '') +
                (m.content ? '<div class="kay-reply">' + prose(m.content) + '</div>' : '') + '</div>';
        }).join('');
        conv.scrollTop = conv.scrollHeight;
    }

    function showError(msg) {
        if (window.App && typeof window.App.showToast === 'function') {
            window.App.showToast(msg, 'error', 6000);
        } else {
            window.alert(msg);
        }
    }

    window.PolisKay = { render, stop: stopPolling, applyCalls };
})();
