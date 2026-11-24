// =============================================================================
// HANDBOOK TRAIL MARKER — the v5 public site (URL-as-filter, cards)
// =============================================================================
// Trail across files:
//   pages     — cli-go/pkg/render/v5.go renders every page from
//               cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v5/ (frame.html
//               and the card templates); this file is that folder's v5.js
//   views     — /pql/<sentence> (webapp/internal/server/handlers_pql.go) answers
//               a view's address with index.html and, asked as JSON, its items
//   grammar   — cli-go/pkg/pql/pql.go: the one PQL grammar
//   widget    — Follow and reply, served by polis.pub as /widget-<version>.js
//               (docs/handbook/foreign-site-widget.md)
//
// Pull the thread:
//   https://github.com/vdibart/polis-cli/blob/main/docs/general/concepts/shapes.md       (why)
//   https://github.com/vdibart/polis-cli/blob/main/docs/general/reference/pql.md         (spec)
//   https://github.com/vdibart/polis-cli/blob/main/docs/ds/developer/pql-json-api.md     (the JSON)
//   https://github.com/vdibart/polis-cli/blob/main/AGENTS.md                             (map)
// =============================================================================

/*
    Polis v5 shape controller (/v5.js). Every page reads without it: each card
    is a link to its own page, and the menu is links. What it adds:

      - Opening a card in place (spec 1.5): a click on a folded card fetches
        that item's own page and puts its open card where the folded one was,
        so an opened card is exactly the item's canonical HTML (spec 1.8). A
        click on an open card's first row or its words folds it back.
      - Links inside a card (its title, its date, a link in its words) still
        go where they point.
      - Drawing a view other than the posts. As v4 serves its views,
        /pql/<sentence> answers with index.html; this asks the same address
        for the sentence's items as JSON, and the answer names the sentence
        (parsed), so the view is the address and nothing else, and the
        filter the server also puts in the page (for v4's stream.js) is not
        needed. A post or a comment is
        drawn by taking its card from its own page, folded, so a card is
        drawn once, by its template, and never again here. Following, which
        has no page of its own, is drawn from the <template>s index.html
        carries, filled with escaped values.
      - Search, in each section's toolbar: the sentence asked again with
        search=, or for following, the list narrowed where it stands.
      - Streams (spec 4.2, v4's infinity stream carried over): a post's or a
        comment's page is the item among its neighbours, and a view is a
        stream too. More loads below as the reader nears the end, and above,
        once they first scroll up, when there is something above; the
        reader's place is held while cards are inserted above them.
      - The address follows the card in view (4.2): after the reader's first
        scroll, it becomes the card's own page (pushState once, then
        replaceState); an open card holds it; a reader who landed on a view
        and scrolls back to the top gets the view's address again.
      - Landing on an item's page opens at the item, with a peek of the card
        above (only on a fresh visit, with no #, and without reduced motion).
      - The pinned row (spec 1.5): an open or still card whose heading has
        scrolled up under the menu is marked stuck, and its own first line
        (in its template) waits there. A click on it folds the card, or, on a
        card that cannot fold, brings the card to the top.
*/
(function () {
  'use strict';

  document.body.classList.add('js');   // the toolbars show only where this runs
  const col = document.querySelector('main.col');
  const cache = new Map();   // path -> the open card's HTML, fetched once

  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const fromHTML = (html) => { const t = document.createElement('template'); t.innerHTML = html.trim(); return t.content.firstElementChild; };

  // ---------- A card from its own page ----------
  async function openHTML(path) {
    let html = cache.get(path);
    if (html) return html;
    const res = await fetch(path, { credentials: 'same-origin' });
    if (!res.ok) throw new Error(res.status);
    const doc = new DOMParser().parseFromString(await res.text(), 'text/html');
    const open = doc.querySelector('article.item.open[data-path="' + CSS.escape(path) + '"]');
    if (!open) throw new Error('no card');
    html = open.outerHTML;
    cache.set(path, html);
    return html;
  }

  async function openCard(card) {
    const path = card.dataset.path;
    if (!path) return;
    let html;
    try { html = await openHTML(path); } catch (e) { location.href = path; return; }
    const opened = asNeighbour(fromHTML(html));
    opened.dataset.folded = card.outerHTML;   // to fold back to exactly what was there
    card.replaceWith(opened);
    focusable(opened.parentNode);
    // Its place to reply is the widget's (polis.pub's), mounted now it is in the page.
    if (window.PolisWidget) window.PolisWidget.mount(opened.querySelector('[data-polis-widget="reply"]'));
    return opened;
  }

  function foldCard(card) {
    if (!card.dataset.folded) return;   // only a card the reader opened folds; the page's own item stays open
    const folded = fromHTML(card.dataset.folded);
    card.replaceWith(folded);
    markFits(folded.parentNode);
    const top = folded.getBoundingClientRect().top;
    if (top < 80) window.scrollBy({ top: top - 82, behavior: 'smooth' });
  }

  // The same card, folded: the stylesheet folds a card that is not open, and
  // hides its thread.
  async function foldedCard(path) {
    const card = fromHTML(await openHTML(path));
    card.classList.remove('open');
    asNeighbour(card);
    return card;
  }

  // A card brought into this page from its own is a neighbour here: its h1 (the
  // heading of its own page) becomes a lower one, so a page keeps one h1, and
  // its terms chip states nothing for this page (4.2, SEO rules 2 and 1).
  function asNeighbour(card) {
    card.removeAttribute('data-focus');
    card.setAttribute('data-neighbour', '');
    card.querySelectorAll('h1').forEach((h) => {
      const to = document.createElement(h.classList.contains('on') ? 'div' : 'h3');
      [...h.attributes].forEach((a) => to.setAttribute(a.name, a.value));
      to.innerHTML = h.innerHTML;
      h.replaceWith(to);
    });
    card.querySelectorAll('[rel="license"]').forEach((a) => a.removeAttribute('rel'));
    return card;
  }

  // A body that fits in its four lines does not fade (spec 1.5): only the
  // browser knows how tall it set, so this is marked here.
  // A card opens from the keyboard as from the pointer (spec 1.5): every card that opens takes focus.
  function focusable(root) {
    (root || document).querySelectorAll('article.item:not(.still):not(.ghost)').forEach((c) => { if (!c.hasAttribute('tabindex')) c.tabIndex = 0; });
  }
  function markFits(root) {
    focusable(root.nodeType === 9 ? document : root);
    root.querySelectorAll('.item:not(.open) .ex').forEach((ex) => ex.classList.toggle('fits', ex.scrollHeight <= ex.clientHeight + 2));
  }

  // ---------- Which view this is ----------
  // A sentence names its section: the menu item it lights, and the toolbar.
  // The address is the sentence; the JSON's answer says how it parsed.
  const asked = location.pathname.startsWith('/pql/');
  let view = { sec: 'posts', v: 'posts', path: '/pql/all+posts+by+date' };
  function viewOf(parsed) {
    const path = location.pathname;
    if (parsed.type === 'profiles') return { sec: 'following', v: 'following', path };
    if (parsed.type === 'comments') return { sec: 'comments', v: 'comments', path };
    if (parsed.type === 'posts' && parsed.modifier === 'with-comments') return { sec: 'posts', v: 'cm', path };
    if (parsed.type === 'posts' && (parsed.modifier === 'by-date' || !parsed.modifier)) return { sec: 'posts', v: 'posts', path };
    return { sec: 'posts', v: 'other', path };
  }
  const original = col.innerHTML;   // the posts as render drew them

  // Light the section: its menu item moves into the box, the others trail it
  // in the menu's order (spec 1.3), and its toolbar shows.
  function light(sec) {
    document.body.dataset.sec = sec;
    const box = document.querySelector('.menu .box'), trail = document.querySelector('.menu .trail');
    if (!box || !trail) return;
    const ways = [...document.querySelectorAll('.menu .way')];
    ways.forEach((w) => {
      const on = w.dataset.way === sec;
      w.classList.toggle('on', on);
      if (on) w.setAttribute('aria-current', 'page'); else w.removeAttribute('aria-current');
      (on ? box : trail).appendChild(w);   // in the menu's order, as the page lists them
    });
    document.querySelectorAll('.views a').forEach((a) => a.classList.toggle('on', a.dataset.v === view.v));
    fitMenu();
  }

  // ---------- Streams: loading below and above (spec 4.2) ----------
  // Each direction has its own state: one request at a time, a cursor (or,
  // first, an anchor on the edge card), done when the server sends no more,
  // three retries honouring Retry-After. A view change bumps `drawing`, and an
  // answer to an older one is dropped (v4's generation guard).
  let query = '', drawing = 0, more = null;
  const PAGE = 10;                    // items per request: each card comes from its own page
  const RETRY = [750, 1500, 3000];
  const single = document.body.classList.contains('single');
  const streamable = () => view.sec === 'posts' || view.sec === 'comments';
  const down = { cursor: '', done: false, busy: false, tries: 0, first: true };
  const up = { cursor: '', done: true, busy: false, tries: 0, first: true, started: false };
  const cards = () => [...col.querySelectorAll(':scope > article.item[data-path]:not(.still)')];

  async function ask(path, params) {
    const u = new URL(path, location.origin);
    Object.entries(params).forEach(([k, v]) => v && u.searchParams.set(k, v));
    const res = await fetch(u, { headers: { Accept: 'application/json' }, credentials: 'same-origin' });
    if (!res.ok) { const e = new Error(res.status); e.retryAfter = res.headers.get('Retry-After'); throw e; }
    return res.json();
  }
  // How long to wait before trying again: Retry-After (seconds or a date) when the server says, else the backoff.
  function backoff(state, err) {
    const ra = err && err.retryAfter;
    if (ra) { const s = Number(ra); const ms = isNaN(s) ? Date.parse(ra) - Date.now() : s * 1000; if (ms > 0) return Math.min(ms, 60000); }
    return RETRY[Math.min(state.tries - 1, RETRY.length - 1)];
  }

  // countsFrom: the toolbar's numbers from two /pql/ answers, all posts by
  // date (counting posts and comments) and all posts with comments (counting
  // posts). A count that reached the server's bound (counts_capped) is left
  // out, so the page keeps its own number for it.
  function countsFrom(all, withComments) {
    const out = {};
    const take = (ans, type, name) => {
      const n = ans && ans.counts && ans.counts[type];
      if (typeof n === 'number' && !((ans.counts_capped || []).includes(type))) out[name] = n;
    };
    take(all, 'posts', 'posts');
    take(all, 'comments', 'comments');
    take(withComments, 'posts', 'with-comments');
    return out;
  }

  // ⭐ The toolbar's numbers are what the site's /pql/ counts now. A page the
  // last change did not rewrite was drawn with older ones; these replace them
  // as the page loads (the toolbar shows only with script, so no reader ever
  // sees a number this could not correct). A failure leaves the page's own.
  async function refreshCounts() {
    if (!document.querySelector('.tbar [data-count]')) return;
    let fresh;
    try {
      const [all, withComments] = await Promise.all([
        ask('/pql/all+posts+by+date', { counts: 'posts,comments', limit: '1' }),
        ask('/pql/all+posts+with+comments', { counts: 'posts', limit: '1' }),
      ]);
      fresh = countsFrom(all, withComments);
    } catch (e) { return; }
    for (const [name, n] of Object.entries(fresh)) {
      document.querySelectorAll('.tbar [data-count="' + name + '"]').forEach((b) => {
        b.textContent = String(n);
        const noun = b.nextElementSibling && b.nextElementSibling.matches('[data-noun]') ? b.nextElementSibling : null;
        if (noun) noun.textContent = noun.dataset.noun + (n === 1 ? '' : 's');
      });
    }
  }

  function say(text) { const p = document.createElement('p'); p.className = 'empty'; p.textContent = text; return p; }

  // A card for an item whose own page could not be read: its date, its
  // title, its opening words, all from the JSON and all escaped.
  function plainCard(it) {
    return fromHTML('<article class="item still"><div class="hd"><a class="hdt" href="' + esc(it.url) + '">' + esc(it.published_human || '') + '</a></div>'
      + (it.title ? '<h3><a class="perma" href="' + esc(it.url) + '">' + esc(it.title) + '</a></h3>' : '')
      + '<div class="ex"><p>' + esc(it.excerpt || '') + '</p></div></article>');
  }
  const own = (it) => typeof it.url === 'string' && it.url.startsWith('/') && !it.url.startsWith('//');
  const known = (it) => !!col.querySelector('article.item[data-path="' + CSS.escape(it.url) + '"]');

  // Below: each item takes its place at once, so the order is the sentence's,
  // and its card fills the place when its page arrives (below the reader, so
  // nothing shifts under them).
  function place(items, n) {
    items.forEach((it) => {
      if (own(it) && known(it)) return;   // already on the page (a neighbour the server drew)
      const slot = document.createElement('article');
      slot.className = 'item ghost';
      slot.setAttribute('aria-busy', 'true');
      slot.dataset.path = it.url || '';
      col.insertBefore(slot, more);
      (own(it) ? foldedCard(it.url) : Promise.reject(new Error('not ours')))
        .catch(() => plainCard(it))
        .then((card) => { if (n === drawing && slot.isConnected) { slot.replaceWith(card); markFits(col); } });
    });
  }

  function ensureMore() {
    if (more && more.isConnected) return;
    more = document.createElement('div');
    more.className = 'more';
    col.appendChild(more);
    belowSeen.disconnect();
    belowSeen.observe(more);
  }

  // A fresh view (a search, a filter): both directions start over.
  async function draw(fresh, first) {
    const n = fresh ? ++drawing : drawing;
    if (fresh) {
      col.textContent = '';
      Object.assign(down, { cursor: '', done: false, busy: false, tries: 0, first: false });
      Object.assign(up, { done: true });
      ensureMore();
    }
    let page = first;
    if (!page) {
      try {
        page = await ask(view.path, { search: query, cursor: down.cursor, limit: String(PAGE) });
      } catch (e) {
        if (n === drawing && fresh) col.replaceChildren(say('This view could not be loaded. Every post and comment is also on its own page.'));
        return;
      }
    }
    if (n !== drawing) return;
    const items = page.items || [];
    if (view.sec === 'following') { drawFollowing(items); return; }
    if (fresh && !items.length) { col.replaceChildren(say(query ? 'Nothing here matches.' : view.v === 'cm' ? 'No post has comments yet.' : 'Nothing here yet.')); return; }
    place(items, n);
    down.cursor = (page.pagination && page.pagination.has_more && page.pagination.next_cursor) || '';
    if (!down.cursor) finishDown();
    fillIfShort();
  }

  function finishDown() { down.done = true; if (more) { belowSeen.disconnect(); more.remove(); more = null; } }

  // The next page below: from the cursor, or first anchored on the last card
  // the server drew (before_url, as v4's stream asks).
  async function loadDown() {
    if (down.done || down.busy || !streamable()) return;
    const n = drawing;
    down.busy = true;
    const params = { search: query, limit: String(PAGE) };
    if (down.cursor) params.cursor = down.cursor;
    else if (down.first) { const last = cards().pop(); if (last) params.before_url = last.dataset.path; }
    let page;
    try {
      page = await ask(view.path, params);
    } catch (e) {
      if (n !== drawing) return;
      down.busy = false;
      if (++down.tries <= RETRY.length) setTimeout(loadDown, backoff(down, e));
      else down.tries = 0;   // given up for now; the next time the end comes near tries again
      return;
    }
    if (n !== drawing) return;
    down.busy = false;
    down.tries = 0;
    down.first = false;
    place(page.items || [], n);
    down.cursor = (page.pagination && page.pagination.has_more && page.pagination.next_cursor) || '';
    if (!down.cursor) finishDown();
    fillIfShort();
  }

  // A page too short to scroll fetches below at once (a tall screen otherwise never pages).
  function fillIfShort() {
    if (!down.done && !down.busy && streamable() && document.documentElement.scrollHeight <= innerHeight + 50) loadDown();
  }

  const belowSeen = new IntersectionObserver((entries) => {
    if (entries.some((e) => e.isIntersecting)) loadDown();
  }, { rootMargin: '200px 0px' });   // spec 4.2: as the last card comes within 200px

  // Above: only on a page whose stream has something above the item, and only
  // once the reader first scrolls up (firing both ways at once made the page
  // jump). The cards are fetched first and inserted together, newest at the
  // top, and the reader's place is held: the card they are reading stays put.
  async function loadUp() {
    if (up.done || up.busy) return;
    const n = drawing;
    up.busy = true;
    const params = { order: 'oldest', limit: String(PAGE) };
    if (up.cursor) params.cursor = up.cursor;
    else { const firstCard = cards()[0]; if (firstCard) params.after_url = firstCard.dataset.path; }
    let page;
    try {
      page = await ask(view.path, params);
    } catch (e) {
      if (n !== drawing) return;
      up.busy = false;
      if (++up.tries <= RETRY.length) setTimeout(loadUp, backoff(up, e));
      else up.tries = 0;
      return;
    }
    if (n !== drawing) return;
    const items = (page.items || []).filter((it) => !(own(it) && known(it)));
    const built = await Promise.all(items.map((it) => (own(it) ? foldedCard(it.url) : Promise.reject(new Error('not ours'))).catch(() => plainCard(it))));
    if (n !== drawing) return;
    // Items arrive oldest first; each goes on top in turn, so the newest ends at the top.
    keepPlace(() => built.forEach((card) => col.insertBefore(card, col.firstElementChild)));
    markFits(col);
    up.busy = false;
    up.tries = 0;
    up.cursor = (page.pagination && page.pagination.has_more && page.pagination.next_cursor) || '';
    if (!up.cursor) { up.done = true; aboveSeen.disconnect(); }
    else watchAbove();
  }

  // Run a change above the reader without moving what they are reading.
  function keepPlace(change) {
    const anchor = readingCard() || cards()[0];
    const before = anchor ? anchor.getBoundingClientRect().top : 0;
    change();
    if (anchor && anchor.isConnected) {
      const shift = anchor.getBoundingClientRect().top - before;
      if (shift) window.scrollBy(0, shift);
    }
  }

  const aboveSeen = new IntersectionObserver((entries) => {
    if (entries.some((e) => e.isIntersecting)) loadUp();
  }, { rootMargin: '200px 0px' });
  function watchAbove() {
    aboveSeen.disconnect();
    const firstCard = cards()[0];
    if (firstCard && !up.done) aboveSeen.observe(firstCard);
  }

  // ---------- The address follows the card in view (spec 4.2) ----------
  // Only after the reader scrolls: layout shifts (fonts, the widget, a lazy
  // card) must never move it. The card in view is the last whose top has
  // passed a reading line under the menu. The first change is a pushState, so
  // Back returns to where the reader landed; every later one a replaceState.
  let landingURL = location.pathname + location.search;
  let userScrolled = false, pushedOnce = false, lastY = scrollY, followT;
  // The line sits below the landing's peek, so the item the reader landed on,
  // not the card peeking above it, is the one in view on arrival.
  const peekOf = () => (innerWidth <= 600 ? 90 : 140);
  const readingLine = () => (menuBox ? menuBox.getBoundingClientRect().bottom : 54) + peekOf() + 20;
  function readingCard() {
    const line = readingLine();
    let chosen = null;
    for (const c of cards()) {
      if (c.classList.contains('ghost')) continue;
      if (c.getBoundingClientRect().top <= line) chosen = c; else break;
    }
    return chosen || cards().find((c) => c.getBoundingClientRect().bottom > line) || null;
  }
  function followAddress() {
    if (!userScrolled || !streamable()) return;
    let target;
    // A card the reader opened holds the address on itself while it is in view.
    const opened = cards().find((c) => c.dataset.folded && c.classList.contains('open') && c.getBoundingClientRect().bottom > readingLine() && c.getBoundingClientRect().top < innerHeight);
    if (opened) target = opened.dataset.path;
    else if (scrollY <= 4) target = landingURL;   // the top of the stream: the address the reader landed on (a view's, or the item's)
    else { const c = readingCard(); target = c && c.dataset.path; }
    if (!target || target === location.pathname + location.search) return;
    try {
      if (pushedOnce) history.replaceState(history.state, '', target);
      else { history.pushState(history.state, '', target); pushedOnce = true; }
    } catch (e) { /* an address that cannot change: the page still reads */ }
  }

  // As v4: a scroll event, once the landing has settled, is the reader's. It is
  // the one signal every way of scrolling sends (wheel, a trackpad held down,
  // the scrollbar dragged, touch, keys); a layout shift moves cards, which the
  // reading line below the landing's peek already absorbs.
  //
  // ⭐ Throttled, not debounced: while the reader keeps scrolling the address
  // follows every 150ms, and once more when they stop. A debounce restarted on
  // every event never fired during a long, continuous drag.
  function onScroll() {
    const y = scrollY;
    userScrolled = true;
    if (y < lastY && !up.started && !up.done) { up.started = true; watchAbove(); }   // the first scroll up
    lastY = y;
    if (!followT) followT = setTimeout(() => { followT = 0; followAddress(); }, 150);
  }

  // Landing on an item's page: open at the item, with a peek of the card above
  // (140px, 90 on a phone), only when there is one, there is no #, the visit is
  // a fresh navigation (not a reload or Back), and reduced motion is not asked.
  function landOnItem() {
    const item = col.querySelector('article.item[data-focus]');
    if (!item) return;
    if (location.hash === '#comments') { item.querySelector('.thread')?.scrollIntoView({ block: 'start' }); return; }
    const above = item.previousElementSibling && item.previousElementSibling.matches('article.item[data-neighbour]');
    const nav = (performance.getEntriesByType && performance.getEntriesByType('navigation')[0]) || null;
    const fresh = !nav || nav.type === 'navigate';
    if (!above || location.hash || !fresh || matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    const peek = peekOf();
    const top = item.getBoundingClientRect().top + scrollY - (menuBox ? menuBox.getBoundingClientRect().bottom : 54) - peek;
    if (top > 0) window.scrollTo({ top, behavior: 'instant' });
  }

  // ---------- Following: the one card with no page of its own ----------
  const fill = (tmpl, slots) => tmpl.replace(/⟪(\w+)⟫/g, (m, k) => (k in slots ? slots[k] : ''));
  function drawFollowing(items) {
    const card = document.getElementById('v5-following'), row = document.getElementById('v5-person');
    if (!card || !row) { col.replaceChildren(say('This view could not be loaded. Every post and comment is also on its own page.')); return; }
    const sites = items.filter((it) => it.following && it.author_domain).map((it) => it.author_domain);
    const rows = sites.map((d) => fill(row.innerHTML, { domain: esc(d), initial: esc(d.charAt(0).toUpperCase()) })).join('');
    const count = sites.length + (sites.length === 1 ? ' site' : ' sites');
    col.replaceChildren(fromHTML(fill(card.innerHTML, { count: esc(count), rows })));
    if (!sites.length) col.appendChild(say('Not following anyone yet.'));
    const note = document.querySelector('.tbar[data-for="following"] .note');
    if (note) note.innerHTML = '<b>' + sites.length + '</b> ' + (sites.length === 1 ? 'site' : 'sites');
  }

  // ---------- Search ----------
  let typing;
  // siteMatches: every word of the search begins a word of the site's handle
  // (its domain without the shared suffix: alice.polis.pub → alice), or begins
  // the domain itself; never inside a word, never the suffix. The site's and
  // the discovery service's search follow the same rule.
  function siteMatches(domain, q) {
    const d = String(domain || '').toLowerCase();
    const labels = d.split('.');
    const handle = labels.length >= 3 ? labels.slice(0, -2).join('.') : labels.length === 2 ? labels[0] : d;
    return q.toLowerCase().split(/\s+/).filter(Boolean).every((w) =>
      d.startsWith(w) || handle.split(/[^\p{L}\p{N}]+/u).some((part, i, parts) =>
        parts.slice(i).join(' ').startsWith(w.replace(/[^\p{L}\p{N}]+/gu, ' '))));
  }

  function search(q) {
    query = q.trim();
    // The words ride as ?search= (spec 2.5), so a search can be linked to and survives a reload.
    try {
      const u = new URL(location.href);
      if (query) u.searchParams.set('search', query); else u.searchParams.delete('search');
      history.replaceState(history.state, '', u);
    } catch (e) { /* an address that cannot change keeps the search in the page only */ }
    if (view.sec === 'following') {
      let shown = 0;
      col.querySelectorAll('.rowp').forEach((a) => { const hit = siteMatches(a.dataset.s, query); a.hidden = !hit; shown += hit; });
      col.querySelector('.nomatch')?.remove();
      if (query && !shown) { const p = say('Nothing here matches.'); p.classList.add('nomatch'); col.appendChild(p); }
      return;
    }
    landingURL = location.pathname + location.search;   // the view's address now carries the search
    if (!query && !asked) {   // back to the page as rendered, and its stream from there
      drawing++;
      col.innerHTML = original;
      markFits(col);
      Object.assign(down, { cursor: '', done: false, busy: false, tries: 0, first: true });
      ensureMore();
      return;
    }
    draw(true);
  }
  document.querySelectorAll('.sq[data-find]').forEach((b) => b.addEventListener('click', () => {
    const f = document.querySelector('.find[data-find="' + b.dataset.find + '"]');
    const on = f.classList.toggle('on');
    b.classList.toggle('on', on);
    b.setAttribute('aria-expanded', on ? 'true' : 'false');
    b.setAttribute('aria-label', on ? 'close search' : b.dataset.label);
    const input = f.querySelector('input');
    if (on) input.focus(); else if (input.value) { input.value = ''; search(''); }
  }));
  document.querySelectorAll('.find input').forEach((input) => {
    input.addEventListener('input', () => { clearTimeout(typing); typing = setTimeout(() => search(input.value), 250); });
    input.addEventListener('keydown', (e) => { if (e.key === 'Escape') document.querySelector('.sq[data-find="' + input.closest('.find').dataset.find + '"]').click(); });
  });

  // ---------- The pinned row ----------
  // The menu's bottom edge is where a heading goes under; a card is stuck while
  // its heading is above that line and enough of the card is still below it.
  const menuBox = document.querySelector('.menu .box');
  function stickRows() {
    const line = (menuBox ? menuBox.getBoundingClientRect().bottom : 54) + 10;
    document.querySelectorAll('main.col article.item').forEach((card) => {
      if (!(card.classList.contains('open') || card.classList.contains('still'))) { card.classList.remove('stuck'); return; }
      // The first heading that is drawn: an untitled card's title is in the page
      // but hidden, and a hidden element measures 0x0 at the window's top, which
      // read as "scrolled under the menu" and pinned the card mid-page.
      // An untitled card's heading is its first row (its mark and date).
      const head = (card.classList.contains('untitled') && card.querySelector('.hd')) || [...card.querySelectorAll('h1:not(:empty), h3:not(:empty), .on, .bio, .rows, .ex')].find((h) => h.getClientRects().length) || card;
      const stuck = head.getBoundingClientRect().bottom < line && card.getBoundingClientRect().bottom > line + 90;
      card.classList.toggle('stuck', stuck);
    });
  }
  let ticking = false;
  addEventListener('scroll', () => { if (!ticking) { ticking = true; requestAnimationFrame(() => { ticking = false; stickRows(); }); } }, { passive: true });
  addEventListener('resize', stickRows);

  function toTop(card) {
    const top = card.getBoundingClientRect().top + scrollY - (menuBox ? menuBox.getBoundingClientRect().bottom + 12 : 80);
    window.scrollTo({ top: Math.max(0, top), behavior: 'smooth' });
  }
  document.addEventListener('click', (e) => {
    const row = e.target.closest('.srow');
    if (!row || e.target.closest('a, button')) return;
    const card = row.closest('article.item');
    e.stopPropagation();
    if (card.dataset.folded) { card.classList.remove('stuck'); foldCard(card); return; }
    toTop(card);
  }, true);

  // ---------- Narrowing the menu (spec 1.3) ----------
  // In steps, each tried only when the last does not fit: against the top
  // right corner (Follow), or, once Follow has moved to the footer, the
  // page's edge.
  const menu = document.querySelector('.menu');
  const you = document.querySelector('.you');
  const author = document.querySelector('.author');
  function fitMenu() {
    if (!menu) return;
    const box = menu.querySelector('.box'), trail = menu.querySelector('.trail');
    if (!box || !trail) return;
    if (trail.parentNode !== menu) menu.appendChild(trail);   // back outside the box
    menu.classList.remove('tight', 'inside', 'cramped');
    document.body.classList.remove('squeezed', 'you-low');
    const vw = document.documentElement.clientWidth;
    // What does not fit spills out of an edge of the box (the toolbar pins left, the menu right), so
    // compare the box's first and last items with its own edges.
    const overflows = () => {
      const kids = [...box.querySelectorAll(':scope > *, :scope > .trail > *')].filter((c) => c.offsetWidth > 0);
      if (!kids.length) return false;
      const b = box.getBoundingClientRect();
      return kids.some((c) => { const r = c.getBoundingClientRect(); return r.left < b.left + 2 || r.right > b.right - 2; });
    };
    const fit = (lim) => {
      menu.classList.remove('tight', 'inside', 'cramped');
      document.body.classList.remove('squeezed');
      if (trail.parentNode !== menu) menu.appendChild(trail);
      if (trail.getBoundingClientRect().right <= lim()) return;
      menu.classList.add('tight');
      if (trail.getBoundingClientRect().right <= lim()) return;
      menu.classList.add('inside');
      box.appendChild(trail);
      if (!overflows()) return;
      menu.classList.add('cramped');
      if (overflows()) document.body.classList.add('squeezed');
    };
    const corner = () => (you && you.offsetWidth ? you.getBoundingClientRect().left - 8 : vw - 16);
    fit(corner);
    if (you && you.offsetWidth && you.getBoundingClientRect().left < box.getBoundingClientRect().right + 4) {
      document.body.classList.add('you-low');
      fit(() => vw - 16);
    }
    // Top left, the author's handle gives way to the mark alone before it touches the box.
    document.body.classList.remove('author-short');
    if (author) {
      const ah = author.querySelector('.ah');
      const room = box.getBoundingClientRect().left - 12 - author.getBoundingClientRect().left;
      author.style.maxWidth = Math.max(0, room) + 'px';   // the handle is cut with an ellipsis to the room it has
      if (ah && room < 28 + 10 + 60) document.body.classList.add('author-short');   // too little to read: the mark alone
    }
  }
  addEventListener('resize', fitMenu);
  // Follow draws when polis.pub's widget loads, after this: fit again when it takes its size.
  if (you && 'ResizeObserver' in window) new ResizeObserver(fitMenu).observe(you);
  (document.fonts ? document.fonts.ready : Promise.resolve()).then(fitMenu);

  // ---------- Start ----------
  fitMenu();
  const startQuery = new URLSearchParams(location.search).get('search') || '';
  if (startQuery) {
    query = startQuery.trim();
    const f = document.querySelector('.find[data-find="' + view.sec + '"]');
    if (f) {
      f.classList.add('on');
      document.querySelector('.sq[data-find="' + view.sec + '"]')?.classList.add('on');
      f.querySelector('input').value = startQuery;
    }
  }
  if (asked) {
    // Until the answer comes, the page shows the posts as rendered: hold them back rather than show the wrong view.
    col.classList.add('waiting');
    ask(location.pathname, {}).then((page) => {
      view = viewOf(page.parsed || {});
      light(view.sec);
      col.classList.remove('waiting');
      if (query) draw(true); else draw(true, page);
    }).catch(() => {
      col.classList.remove('waiting');
      col.replaceChildren(say('This view could not be loaded. Every post and comment is also on its own page.'));
    });
  } else if (!single) {
    // The home page: the top of the posts stream, drawn by the server; more below as the reader goes.
    light(view.sec);
    if (query) draw(true); else { ensureMore(); fillIfShort(); }
  } else if (col.querySelector('article.item[data-focus]')) {
    // An item's own page: its stream, above and below (spec 4.2).
    view = { sec: document.body.dataset.sec, v: document.body.dataset.sec, path: document.body.dataset.sec === 'comments' ? '/pql/all+comments+by+date' : '/pql/all+posts+by+date' };
    light(view.sec);   // the same toolbar as the home page: landing here is scrolling the stream to this item
    const item = col.querySelector('article.item[data-focus]');
    // Above loads only when the server drew something above the item (not the newest).
    up.done = !(item.previousElementSibling && item.previousElementSibling.matches('article.item[data-neighbour]'));
    landOnItem();
    ensureMore();
    fillIfShort();
  }
  refreshCounts();
  // The gate goes on after the landing has settled (its own scroll, the fonts,
  // the bodies fitted), so none of that counts as the reader's.
  const settle = document.fonts ? document.fonts.ready : Promise.resolve();
  settle.then(() => requestAnimationFrame(() => requestAnimationFrame(() => { lastY = scrollY; addEventListener('scroll', onScroll, { passive: true }); })));
  markFits(document);
  (document.fonts ? document.fonts.ready : Promise.resolve()).then(() => markFits(document));

  // A card's copy button puts its address on the clipboard, and says so for a moment.
  document.addEventListener('click', (e) => {
    const b = e.target.closest('.ifoot .copy');
    if (!b) return;
    e.preventDefault();
    e.stopPropagation();
    const url = new URL(b.dataset.path || '', location.href).href;
    try { navigator.clipboard.writeText(url).catch(() => {}); } catch (x) { /* no clipboard here */ }
    b.classList.add('done');
    b.title = 'Copied';
    clearTimeout(b.doneT);
    b.doneT = setTimeout(() => { b.classList.remove('done'); b.title = 'Copy the link'; }, 1400);
  }, true);

  // A folded card's count opens the card in place, at its thread (spec 1.5's footer: a button that opens the
  // thread), and pressed again on a card the reader opened, closes it. On an item's own page, where the card
  // stays open, it goes to the thread smoothly rather than jumping to the #comments anchor.
  document.addEventListener('click', (e) => {
    const n = e.target.closest('.ifoot .n');
    const card = n && n.closest('article.item');
    if (!card || card.classList.contains('still')) return;
    e.preventDefault();
    e.stopPropagation();
    if (!card.classList.contains('open')) {
      openCard(card).then((opened) => { if (opened) opened.querySelector('.thread')?.scrollIntoView({ behavior: 'smooth', block: 'center' }); });
    } else if (card.dataset.folded) {
      foldCard(card);
    } else {
      card.querySelector('.thread')?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
  }, true);

  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    const card = e.target;
    if (!(card instanceof HTMLElement) || !card.matches('article.item') || card.classList.contains('still')) return;
    e.preventDefault();
    if (card.classList.contains('open')) foldCard(card); else openCard(card).then((o) => o && o.focus());
  });

  document.addEventListener('click', (e) => {
    const card = e.target.closest('article.item');
    if (!card || card.classList.contains('still') || card.classList.contains('ghost')) return;
    if (e.target.closest('a, button, input, textarea, .thread, .ifoot') || getSelection().toString()) return;
    if (card.classList.contains('open')) {
      // The first row or the words fold an open card (not a link in them, above).
      if (e.target.closest('.hd, .ex')) foldCard(card);
    } else {
      e.preventDefault();
      openCard(card);
    }
  });
})();
