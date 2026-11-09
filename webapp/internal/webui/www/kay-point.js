// Point and ask: a Kay icon in the lower left of the owner's stream. Clicking
// it enters component-select mode: hovering a part of the app (the nav, a
// card, the timeline, the page itself for the theme) spotlights it, and
// clicking opens Kay's terminal, scoped to that part. The instance enforces
// the scope (each component has its own MCP address), so the dropdown is the
// owner's choice of what Kay may change, not a hint.
//
// Only where the instance offers it (GET /api/kay/components says so): an
// owner's own remote instance. The Kay page (/_/kay) is a separate surface.
//
//   GET  /api/kay/components   is it on? the components, greyed ones included
//   POST /api/kay/ask          {message, chat_id, point: {component, ...}}
//   GET  /api/kay/chats/{id}   the chat as it stands, plus this site's calls
(function () {
    'use strict';

    const POLL_MS = 1500;
    const POLL_LIMIT_MS = 5 * 60 * 1000;
    const TYPE_MS = 9;

    const st = {
        components: [], byId: {}, mode: 'off', // off | select | terminal
        hover: null, target: null, chat: null, asks: 0, applied: {}, polling: null, pollStart: 0, busy: false,
        raf: 0, shown: {}, // message index → characters already typed
    };

    function esc(s) {
        return String(s == null ? '' : s)
            .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    }

    async function call(method, path, body) {
        const opts = { method, headers: { 'Content-Type': 'application/json' } };
        if (body) opts.body = JSON.stringify(body);
        const res = await fetch(path, opts);
        let data = {};
        try { data = await res.json(); } catch (e) { /* not JSON */ }
        if (!res.ok) throw new Error(data.error || res.statusText || ('HTTP ' + res.status));
        return data;
    }

    // ── where the components are ────────────────────────────────────────

    function onStream() {
        const p = location.pathname;
        const screen = document.getElementById('stream-screen');
        return document.body.classList.contains('is-owner') && (p === '/_/' || p === '/_' || p.startsWith('/_/pql/')) &&
            screen && !screen.classList.contains('hidden');
    }

    function visible(el) {
        if (!el) return false;
        const r = el.getBoundingClientRect();
        return r.width > 0 && r.height > 0;
    }

    function union(rects) {
        const rs = rects.filter(r => r && r.width > 0 && r.height > 0);
        if (!rs.length) return null;
        const l = Math.min(...rs.map(r => r.left)), t = Math.min(...rs.map(r => r.top));
        const R = Math.max(...rs.map(r => r.right)), B = Math.max(...rs.map(r => r.bottom));
        return { left: l, top: t, width: R - l, height: B - t, right: R, bottom: B };
    }

    function navRect() {
        const nav = document.getElementById('icon-nav');
        if (!nav) return null;
        const parts = ['.avatar-wrap', '#nav-btn-gateway', '#nav-btn-paragraph', '#nav-btn-comment', '#nav-btn-people',
            '#nav-btn-envelope', '#nav-shortcuts', '#nav-btn-edit'];
        const r = union(parts.map(q => nav.querySelector(q)).filter(visible).map(e => e.getBoundingClientRect()));
        return r && pad(r, 6);
    }

    function pad(r, n) {
        return { left: r.left - n, top: r.top - n, width: r.width + 2 * n, height: r.height + 2 * n, right: r.right + n, bottom: r.bottom + n };
    }

    function navBottom() {
        const nav = document.getElementById('icon-nav');
        return nav ? nav.getBoundingClientRect().bottom : 0;
    }

    // The timeline is the vertical bar: its x is the dots' centre.
    function timelineX() {
        const dots = document.querySelectorAll('#stream-screen .entry .timeline-dot, #icon-nav .pinned-dot');
        for (const d of dots) {
            const r = d.getBoundingClientRect();
            if (r.width > 0) return r.left + r.width / 2;
        }
        return null;
    }

    function filterRect() {
        const f = document.querySelector('#polis-topbar-filter .sentence-filter') || document.getElementById('polis-topbar-filter');
        return visible(f) ? pad(f.getBoundingClientRect(), 6) : null;
    }

    // The timeline and the sentence filter are one component: the band down
    // the timeline starts at the filter's top, so the two shapes join.
    function timelineRects() {
        const x = timelineX();
        const f = filterRect();
        if (x == null) return f ? [f] : [];
        // ⚠️ The shade cuts one hole per rect, and two holes that overlap
        // cancel out, so where the band would run under the filter it starts
        // below it instead.
        const under = f && x + 14 > f.left && x - 14 < f.right;
        const top = f ? (under ? f.bottom : f.top) : navBottom();
        // A small gap to the filter's left edge is closed, so the outline
        // can run round both as one shape.
        const right = f && !under && f.left - (x + 14) > 0 && f.left - (x + 14) <= 16 ? f.left : x + 14;
        const band = { left: x - 14, top: top, width: right - (x - 14), height: window.innerHeight - top, right: right, bottom: window.innerHeight };
        return f ? [band, f] : [band];
    }

    function inside(r, x, y) {
        return r && x >= r.left && x <= r.right && y >= r.top && y <= r.bottom;
    }

    // hit names the component under the pointer, or null: the smallest wins,
    // so a card beats the timeline under it. ⭐ Nothing answers for the page
    // itself: the theme is chosen from the terminal's dropdown, not pointed at.
    function hit(x, y) {
        const under = document.elementsFromPoint(x, y).filter(e => !e.closest('.kp-layer'));
        const top = under[0] || null;
        if (top && top.closest('#polis-topbar-handle')) return target('domain', top.closest('#polis-topbar-handle'));
        if (top && top.closest('#polis-topbar-filter')) return target('timeline');
        if (inside(navRect(), x, y)) return target('nav');
        if (top && top.closest('#icon-nav')) return null;
        if (timelineRects().some(r => inside(r, x, y))) return target('timeline');
        const card = under.map(e => e.closest('#stream-screen article.entry')).find(Boolean);
        if (card && !card.classList.contains('entry--ghost-compose') && !card.classList.contains('entry--editor')) {
            return target('card', card);
        }
        return null;
    }

    function target(id, el) {
        return { id, el: el || null };
    }

    // rectsOf is where a component is on screen: one rect, or two for the
    // timeline and its filter, or none (the theme has no place to point at).
    function rectsOf(t) {
        if (!t) return [];
        if (t.el) return t.el.isConnected ? [pad(t.el.getBoundingClientRect(), t.id === 'card' ? 8 : 6)] : [];
        if (t.id === 'nav') return [navRect()].filter(Boolean);
        if (t.id === 'timeline') return timelineRects();
        return [];
    }

    function cardContext(el) {
        if (!el) return {};
        const text = q => { const n = el.querySelector(q); return n ? n.textContent.trim().replace(/\s+/g, ' ').slice(0, 300) : ''; };
        const avatar = el.querySelector('.entry-avatar-link');
        // The stream shows another site's post with its handle (revealed on
        // hover) and an avatar labelled with the domain; an own post has neither.
        let author = text('.entry-handle') || (avatar && avatar.getAttribute('aria-label')) || text('.byline-domain');
        if (!author && window.App && App.siteBaseUrl) {
            try { author = new URL(App.siteBaseUrl).host; } catch (e) { /* not a URL */ }
        }
        return {
            author_domain: author === 'author' ? '' : author,
            title: text('.entry-title') || text('.entry-excerpt, .entry-body p'),
            url: el.dataset.polisCanonicalUrl || '',
        };
    }

    function chipText(t) {
        const c = st.byId[t.id] || { label: t.id };
        let s = c.label;
        if (t.id === 'card') {
            const ctx = cardContext(t.el);
            if (ctx.author_domain) s += ' · ' + ctx.author_domain;
        }
        if (!c.enabled) s += ' · Kay can’t change this yet';
        return s;
    }

    // ── the layer: icon, spotlight, chip, terminal ──────────────────────

    let layer, icon, rings, outline, shade, chip, catcher, term;

    function build() {
        loadFont();
        layer = document.createElement('div');
        layer.className = 'kp-layer';
        layer.innerHTML =
            '<div class="kp-catcher hidden"></div>' +
            '<svg class="kp-shade hidden" aria-hidden="true"><path fill-rule="evenodd"></path></svg>' +
            ('<div class="kp-ring hidden"><span class="kp-corner kp-tl"></span><span class="kp-corner kp-tr"></span>' +
            '<span class="kp-corner kp-bl"></span><span class="kp-corner kp-br"></span></div>').repeat(2) +
            '<svg class="kp-outline hidden" aria-hidden="true"><path class="kp-outline-edge"></path><path class="kp-outline-corners"></path></svg>' +
            '<div class="kp-chip hidden"></div>' +
            '<section class="kp-term hidden" role="dialog" aria-label="Kay" aria-modal="false"></section>' +
            '<button type="button" class="kp-icon" aria-label="Change this page with Kay (Ctrl-K)">' +
            '<span class="kp-icon-glyph">k<span class="kp-icon-cursor">_</span></span><span class="kp-icon-esc">ESC</span></button>' +
            '<div class="kp-icon-tip" role="tooltip">change this page with Kay <span class="kp-key">CTRL-K</span></div>';
        document.body.appendChild(layer);
        icon = layer.querySelector('.kp-icon');
        rings = Array.from(layer.querySelectorAll('.kp-ring'));
        shade = layer.querySelector('.kp-shade');
        outline = layer.querySelector('.kp-outline');
        chip = layer.querySelector('.kp-chip');
        catcher = layer.querySelector('.kp-catcher');
        term = layer.querySelector('.kp-term');

        icon.addEventListener('click', toggle);
        catcher.addEventListener('mousemove', (ev) => {
            if (st.mode !== 'select') return;
            st.hover = hit(ev.clientX, ev.clientY);
            paint();
            // Beside the pointer, kept inside the viewport by its real size.
            chip.style.left = Math.max(8, Math.min(ev.clientX + 16, window.innerWidth - chip.offsetWidth - 8)) + 'px';
            chip.style.top = Math.min(ev.clientY + 18, window.innerHeight - chip.offsetHeight - 8) + 'px';
        });
        catcher.addEventListener('click', (ev) => {
            if (st.mode !== 'select') return;
            ev.preventDefault();
            const t = hit(ev.clientX, ev.clientY);
            if (!t) return;
            if (!st.byId[t.id] || !st.byId[t.id].enabled) { flash(chip); return; }
            openTerminal(t);
        });
        document.addEventListener('keydown', (ev) => {
            // Ctrl-K (Cmd-K on a Mac) turns Kay mode on and off. ⚠️ Not inside
            // the post editor, where it is the link shortcut.
            if ((ev.ctrlKey || ev.metaKey) && !ev.altKey && !ev.shiftKey && (ev.key === 'k' || ev.key === 'K')) {
                const t = ev.target;
                if (t && t.closest && !t.closest('.kp-layer') && (t.isContentEditable || t.closest('.milkdown-mount, .ProseMirror'))) return;
                if (!onStream()) return;
                ev.preventDefault();
                toggle();
                return;
            }
            if (ev.key !== 'Escape' || st.mode === 'off') return;
            if (st.mode === 'terminal') closeTerminal(); else exitAll();
        });
    }

    function toggle() {
        if (st.mode === 'off') enterSelect();
        else exitAll();
    }

    // unionOutline traces the edge of a set of rects as SVG path data: the
    // rects cut the plane into a grid of cells, an edge runs wherever a
    // covered cell meets an uncovered one, and a corner with one covered
    // cell of four is an outer corner, which gets a bracket.
    function unionOutline(rs) {
        const xs = [...new Set(rs.flatMap(r => [r.left, r.right]))].sort((a, b) => a - b);
        const ys = [...new Set(rs.flatMap(r => [r.top, r.bottom]))].sort((a, b) => a - b);
        const cov = (i, j) => i >= 0 && j >= 0 && i < xs.length - 1 && j < ys.length - 1 &&
            rs.some(r => inside(r, (xs[i] + xs[i + 1]) / 2, (ys[j] + ys[j + 1]) / 2));
        let edges = '', corners = '';
        // Horizontal edges, merged along each line; then vertical ones.
        for (let j = 0; j < ys.length; j++) {
            let from = null;
            for (let i = 0; i <= xs.length - 1; i++) {
                const on = i < xs.length - 1 && cov(i, j - 1) !== cov(i, j);
                if (on && from === null) from = xs[i];
                if (!on && from !== null) { edges += 'M' + from + ' ' + ys[j] + 'H' + xs[i]; from = null; }
            }
        }
        for (let i = 0; i < xs.length; i++) {
            let from = null;
            for (let j = 0; j <= ys.length - 1; j++) {
                const on = j < ys.length - 1 && cov(i - 1, j) !== cov(i, j);
                if (on && from === null) from = ys[j];
                if (!on && from !== null) { edges += 'M' + xs[i] + ' ' + from + 'V' + ys[j]; from = null; }
            }
        }
        for (let i = 0; i < xs.length; i++) {
            for (let j = 0; j < ys.length; j++) {
                const q = [[-1, -1], [1, -1], [-1, 1], [1, 1]].filter(([dx, dy]) => cov(dx < 0 ? i - 1 : i, dy < 0 ? j - 1 : j));
                if (q.length !== 1) continue;
                const [dx, dy] = q[0];
                const vx = xs[i] - 5 * dx, vy = ys[j] - 5 * dy;
                corners += 'M' + (vx + 12 * dx) + ' ' + vy + 'H' + vx + 'V' + (vy + 12 * dy);
            }
        }
        return { edges, corners };
    }

    function flash(el) {
        el.classList.remove('kp-flash');
        void el.offsetWidth;
        el.classList.add('kp-flash');
    }

    function paint() {
        const t = st.mode === 'select' ? st.hover : st.target;
        const rs = st.mode === 'off' ? [] : rectsOf(t);
        const greyed = !!t && !(st.byId[t.id] && st.byId[t.id].enabled);
        // One rect is a ring; a component in several rects is one outline
        // drawn round their union, with brackets on its outer corners only.
        rings.forEach((ring, i) => {
            const r = rs.length === 1 ? rs[i] : null;
            ring.classList.toggle('hidden', !r);
            if (!r) return;
            ring.classList.toggle('kp-greyed', greyed);
            Object.assign(ring.style, { left: r.left + 'px', top: r.top + 'px', width: r.width + 'px', height: r.height + 'px' });
        });
        outline.classList.toggle('hidden', rs.length < 2);
        if (rs.length > 1) {
            const o = unionOutline(rs);
            outline.setAttribute('viewBox', '0 0 ' + window.innerWidth + ' ' + window.innerHeight);
            outline.classList.toggle('kp-greyed', greyed);
            outline.firstChild.setAttribute('d', o.edges);
            outline.lastChild.setAttribute('d', o.corners);
        }
        // The shade darkens everything but the component: one path, the
        // viewport with a hole per rect, so joined rects read as one shape.
        const dim = rs.length > 0 && !document.body.classList.contains('kp-landed');
        shade.classList.toggle('hidden', !dim);
        if (dim) {
            const W = window.innerWidth, H = window.innerHeight;
            let d = 'M0 0H' + W + 'V' + H + 'H0Z';
            for (const r of rs) d += 'M' + r.left + ' ' + r.top + 'V' + r.bottom + 'H' + r.right + 'V' + r.top + 'Z';
            shade.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
            shade.firstChild.setAttribute('d', d);
        }
        if (!t || !rs.length) {
            chip.classList.add('hidden');
            return;
        }
        if (st.mode === 'select') {
            chip.textContent = chipText(t);
            chip.classList.remove('hidden');
            chip.classList.toggle('kp-greyed', !(st.byId[t.id] && st.byId[t.id].enabled));
        } else {
            chip.classList.add('hidden');
        }
    }

    function loop() {
        paint();
        st.raf = st.mode === 'off' ? 0 : requestAnimationFrame(loop);
    }

    function enterSelect() {
        st.mode = 'select';
        st.hover = null;
        document.body.classList.add('kp-selecting');
        catcher.classList.remove('hidden');
        icon.classList.add('kp-active');
        if (!st.raf) st.raf = requestAnimationFrame(loop);
    }

    function exitAll() {
        st.mode = 'off';
        stopPolling();
        document.body.classList.remove('kp-selecting', 'kp-terminal-open', 'kp-landed');
        catcher.classList.add('hidden');
        term.classList.add('hidden');
        term.classList.remove('kp-docked');
        icon.classList.remove('kp-active');
        st.target = null;
        paint();
    }

    // ── the terminal ────────────────────────────────────────────────────

    function openTerminal(t) {
        st.mode = 'terminal';
        st.target = t;
        catcher.classList.add('hidden');
        document.body.classList.remove('kp-selecting');
        document.body.classList.add('kp-terminal-open');
        loadFont();
        // Keep a card in the clear above the terminal.
        if (t.id === 'card' && t.el) {
            const r = t.el.getBoundingClientRect();
            const room = window.innerHeight * 0.4;
            if (r.top < navBottom() + 8 || r.top > room) window.scrollBy({ top: r.top - navBottom() - 24, behavior: 'smooth' });
        }
        const opts = st.components.map(c => '<option value="' + esc(c.id) + '"' + (c.enabled ? '' : ' disabled') +
            (c.id === t.id ? ' selected' : '') + '>' + esc(c.label.toUpperCase()) + (c.enabled ? '' : ' · NOT YET') + '</option>').join('');
        term.innerHTML =
            '<div class="kp-crt"><div class="kp-grid" aria-hidden="true"></div><div class="kp-scan" aria-hidden="true"></div>' +
            '<header class="kp-bar"><span class="kp-brand">KAY <span class="kp-dim">// POLIS TERMINAL</span></span>' +
            '<label class="kp-target-label">TARGET <select class="kp-target" aria-label="What Kay may change">' + opts + '</select></label>' +
            '<button type="button" class="kp-x" aria-label="Close"><svg viewBox="0 0 12 12" aria-hidden="true"><path d="M2 2l8 8M10 2l-8 8"/></svg></button></header>' +
            '<div class="kp-screen" aria-live="polite"></div>' +
            '<form class="kp-prompt"><span class="kp-caret">&gt;</span>' +
            '<textarea class="kp-input" rows="1" spellcheck="false" aria-label="Ask Kay" placeholder="say what you want changed"></textarea>' +
            '<button type="submit" class="kp-send">RUN</button></form></div>';
        term.classList.remove('hidden', 'kp-docked');
        void term.offsetWidth;
        term.classList.add('kp-rising');
        st.chat = null;
        st.asks = 0;
        st.shown = {};
        const screen = term.querySelector('.kp-screen');
        const ctx = t.id === 'card' ? cardContext(t.el) : {};
        boot(screen, t, ctx);

        const sel = term.querySelector('.kp-target');
        sel.addEventListener('change', () => {
            const id = sel.value;
            st.target = id === 'card' && st.target.id === 'card' ? st.target :
                (id === 'card' ? firstCard() : target(id, null));
            // A new target after an ask is a new conversation: each one is
            // about one component.
            const fresh = !!st.chat;
            if (fresh) { st.chat = null; st.asks = 0; st.shown = {}; }
            line(screen, 'sys', (fresh ? 'NEW SESSION · ' : '') + 'TARGET → ' + (st.byId[id] ? st.byId[id].label.toUpperCase() : id) +
                (id === 'card' && st.target && st.target.el ? ' · ' + (cardContext(st.target.el).author_domain || 'a post') : ''));
        });
        term.querySelector('.kp-x').addEventListener('click', closeTerminal);
        term.addEventListener('pointerdown', (ev) => {
            if (term.classList.contains('kp-docked') && !ev.target.closest('.kp-x')) {
                term.classList.remove('kp-docked');
                document.body.classList.remove('kp-landed');
            }
        });
        const form = term.querySelector('.kp-prompt');
        const input = form.querySelector('.kp-input');
        form.addEventListener('submit', (ev) => { ev.preventDefault(); ask(input); });
        input.addEventListener('keydown', (ev) => {
            if (ev.key === 'Enter' && !ev.shiftKey) { ev.preventDefault(); ask(input); }
        });
        input.addEventListener('input', () => { input.style.height = 'auto'; input.style.height = Math.min(input.scrollHeight, 120) + 'px'; });
        setTimeout(() => input.focus(), 350);
    }

    function firstCard() {
        const card = Array.from(document.querySelectorAll('#stream-screen article.entry'))
            .find(e => !e.classList.contains('entry--ghost-compose') && !e.classList.contains('entry--editor') && visible(e));
        return target('card', card || null);
    }

    function boot(screen, t, ctx) {
        const c = st.byId[t.id];
        const lines = [
            ['dim', 'KAY MAY CHANGE ' + c.hint.toUpperCase() + (ctx.author_domain ? ' · ' + ctx.author_domain : '') + '.'],
            ['kay', t.id === 'any' ? 'GREETINGS. TELL ME WHAT TO CHANGE, AND I WILL WORK OUT WHERE.' : 'GREETINGS. SHALL WE CHANGE SOMETHING?'],
        ];
        let i = 0;
        const next = () => {
            if (i >= lines.length || st.mode !== 'terminal') return;
            const [k, text] = lines[i++];
            typeLine(screen, k, text, next);
        };
        next();
    }

    function line(screen, kind, text) {
        const el = document.createElement('div');
        el.className = 'kp-line kp-' + kind;
        el.textContent = text;
        screen.appendChild(el);
        screen.scrollTop = screen.scrollHeight;
        return el;
    }

    function typeLine(screen, kind, text, done) {
        const el = line(screen, kind, '');
        let n = 0;
        const step = () => {
            n = Math.min(text.length, n + 2);
            el.textContent = text.slice(0, n);
            screen.scrollTop = screen.scrollHeight;
            if (n < text.length) setTimeout(step, TYPE_MS); else if (done) done();
        };
        step();
    }

    function closeTerminal() {
        exitAll();
    }

    async function ask(input) {
        const message = input.value.trim();
        if (!message || st.busy) return;
        const t = st.target;
        const comp = st.byId[t.id];
        if (!comp || !comp.enabled) return;
        const point = { component: t.id };
        if (t.id === 'card') Object.assign(point, cardContext(t.el));
        st.busy = true;
        busy(true);
        term.classList.remove('kp-docked');
        document.body.classList.remove('kp-landed');
        const screen = term.querySelector('.kp-screen');
        line(screen, 'you', '> ' + message);
        input.value = '';
        input.style.height = 'auto';
        try {
            const res = await call('POST', '/api/kay/ask', { message, chat_id: st.chat ? st.chat.id : '', point });
            st.chat = { id: res.chat_id, askedAt: res.asked_at, since: (st.chat && st.chat.since) || res.asked_at };
            st.asks += 1;
            st.turnStart = screen.children.length;
            st.turnEl = document.createElement('div');
            st.turnEl.className = 'kp-turn';
            screen.appendChild(st.turnEl);
            st.turnEl.innerHTML = '<div class="kp-line kp-dim kp-working">KAY IS THINKING<span class="kp-dots"></span></div>';
            startPolling();
        } catch (e) {
            line(screen, 'err', '!! ' + e.message.toUpperCase());
            st.busy = false;
            busy(false);
        }
    }

    function busy(on) {
        const btn = term.querySelector('.kp-send');
        if (btn) { btn.disabled = on; btn.textContent = on ? 'WORKING' : 'RUN'; }
        const sel = term.querySelector('.kp-target');
        if (sel) sel.disabled = on;
        term.classList.toggle('kp-busy', on);
    }

    function startPolling() {
        stopPolling();
        st.pollStart = Date.now();
        st.polling = setInterval(refresh, POLL_MS);
        refresh();
    }

    function stopPolling() {
        if (st.polling) clearInterval(st.polling);
        st.polling = null;
    }

    async function refresh() {
        if (!st.chat || st.mode !== 'terminal') { stopPolling(); return; }
        let data;
        try {
            data = await call('GET', '/api/kay/chats/' + encodeURIComponent(st.chat.id) + '?since=' + encodeURIComponent(st.chat.askedAt));
        } catch (e) {
            stopPolling();
            st.busy = false;
            busy(false);
            line(term.querySelector('.kp-screen'), 'err', '!! ' + e.message.toUpperCase());
            return;
        }
        drawTurn(data);
        const applied = window.PolisKay && window.PolisKay.applyCalls
            ? window.PolisKay.applyCalls(data.calls, st.chat.askedAt, st.applied) : [];
        if (applied.length) landed(applied);
        const timedOut = Date.now() - st.pollStart > POLL_LIMIT_MS;
        if (data.done || timedOut) {
            stopPolling();
            st.busy = false;
            busy(false);
            if (timedOut && !data.done) line(term.querySelector('.kp-screen'), 'err', '!! KAY IS TAKING LONGER THAN USUAL.');
            const input = term.querySelector('.kp-input');
            if (input) input.focus();
        }
    }

    // drawTurn shows the newest assistant message: each tool step as a status
    // line matched with the call this site served for it, then the reply,
    // typed out once.
    function drawTurn(data) {
        const msgs = data.messages || [];
        // ⚠️ Until v0 has this turn's message, the newest assistant message is
        // the previous turn's: wait for the owner's ask to appear first.
        const users = msgs.filter(x => x.role === 'user').length;
        const m = msgs.length && msgs[msgs.length - 1].role === 'assistant' && users >= st.asks ? msgs[msgs.length - 1] : null;
        if (!m || !st.turnEl) return;
        const calls = (data.calls || []).slice();
        const take = (tool) => {
            const i = calls.findIndex(c => c.tool === tool);
            return i < 0 ? null : calls.splice(i, 1)[0];
        };
        const steps = (m.steps || []).filter(s => s.kind === 'tool').map(s => {
            const c = take(s.tool);
            const label = (s.label || s.tool).toUpperCase();
            const status = c ? (c.ok ? '<span class="kp-ok">OK ' + (c.duration_ms / 1000).toFixed(1) + 'S</span>'
                : '<span class="kp-fail">REFUSED</span>') : (s.finished ? '<span class="kp-ok">DONE</span>' : '<span class="kp-run">RUNNING</span>');
            const why = c && !c.ok && c.error ? '<div class="kp-why">' + esc(c.error) + '</div>' :
                (s.reason ? '<div class="kp-why">' + esc(s.reason) + '</div>' : '');
            return '<div class="kp-line kp-step"><span class="kp-step-name">» ' + esc(label) + '</span>' +
                '<span class="kp-leader" aria-hidden="true"></span>' + status + '<code class="kp-tool">' + esc(s.tool) + '</code>' + why + '</div>';
        }).join('');
        let reply = st.turnEl.querySelector('.kp-reply');
        let stepsEl = st.turnEl.querySelector('.kp-steps');
        if (!stepsEl) {
            st.turnEl.innerHTML = '<div class="kp-steps"></div><div class="kp-line kp-kay kp-reply"></div>' +
                '<div class="kp-line kp-dim kp-working">KAY IS WORKING<span class="kp-dots"></span></div>';
            stepsEl = st.turnEl.querySelector('.kp-steps');
            reply = st.turnEl.querySelector('.kp-reply');
        }
        stepsEl.innerHTML = steps;
        const working = st.turnEl.querySelector('.kp-working');
        if (working) working.classList.toggle('hidden', !!m.content || !!m.done);
        const text = (m.content || '').trim();
        const key = st.chat.id + ':' + st.asks;
        const shown = st.shown[key] || 0;
        if (text.length > shown) {
            st.shown[key] = text.length;
            typeInto(reply, text, shown);
        }
        const screen = term.querySelector('.kp-screen');
        screen.scrollTop = screen.scrollHeight;
    }

    function typeInto(el, text, from) {
        let n = from;
        const step = () => {
            if (!el.isConnected) return;
            n = Math.min(text.length, n + 3);
            el.textContent = text.slice(0, n);
            const screen = term.querySelector('.kp-screen');
            if (screen) screen.scrollTop = screen.scrollHeight;
            if (n < text.length) setTimeout(step, TYPE_MS);
        };
        step();
    }

    // landed: a change is live. The stream redraws for a mark, the terminal
    // steps aside so the owner sees the change where they pointed, and the
    // spotlight pulses.
    function landed(tools) {
        if (tools.includes('set_stream_marks') && window.PolisStream && typeof window.PolisStream.refresh === 'function') {
            window.PolisStream.refresh();
        }
        const t = st.target;
        if (t && t.id === 'card' && t.el) {
            const id = t.el.dataset.polisEntryId;
            setTimeout(() => {
                const again = id && document.querySelector('#stream-screen article.entry[data-polis-entry-id="' + CSS.escape(id) + '"]');
                if (again) st.target = target('card', again);
            }, 900);
        }
        const screen = term.querySelector('.kp-screen');
        line(screen, 'sys', 'CHANGE APPLIED · RETURNING CONTROL');
        term.classList.add('kp-docked');
        document.body.classList.add('kp-landed');
        rings.forEach(flash);
        flash(outline);
        setTimeout(() => { const sc = term.querySelector('.kp-screen'); if (sc) sc.scrollTop = sc.scrollHeight; }, 450);
    }

    function loadFont() {
        if (document.getElementById('kp-font')) return;
        const l = document.createElement('link');
        l.id = 'kp-font';
        l.rel = 'stylesheet';
        l.href = 'https://fonts.googleapis.com/css2?family=VT323&family=Share+Tech+Mono&display=swap';
        document.head.appendChild(l);
    }

    // ── start ───────────────────────────────────────────────────────────

    function sync() {
        if (!layer) return;
        const on = onStream();
        layer.classList.toggle('hidden', !on);
        if (!on && st.mode !== 'off') exitAll();
    }

    async function start() {
        let res;
        try { res = await call('GET', '/api/kay/components'); } catch (e) { return; }
        if (!res.enabled) return;
        st.components = res.components || [];
        st.components.forEach(c => { st.byId[c.id] = c; });
        build();
        sync();
        const screen = document.getElementById('stream-screen');
        if (screen) new MutationObserver(sync).observe(screen, { attributes: true, attributeFilter: ['class'] });
        new MutationObserver(sync).observe(document.body, { attributes: true, attributeFilter: ['class'] });
        window.addEventListener('popstate', sync);
        setInterval(sync, 1000); // pushState has no event
    }

    function whenOwner() {
        if (document.body.classList.contains('is-owner')) { start(); return; }
        const mo = new MutationObserver(() => {
            if (document.body.classList.contains('is-owner')) { mo.disconnect(); start(); }
        });
        mo.observe(document.body, { attributes: true, attributeFilter: ['class'] });
    }

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', whenOwner);
    else whenOwner();
})();
