// The v5 owner app's card: its model, and that it draws the same parts in
// the same order as the card the v5 shape renders in Go (one markup contract,
// two renderers; spec part 4). Run with:
//   node --test webapp/internal/webui/v5-card.test.js

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const card = require(path.join(__dirname, 'www/v5/card.js'));
const goldens = path.join(__dirname, '../../../cli-go/pkg/render/testdata/v5');

// partsOf is a card's top-level parts, in order, by name: the first
// <article class="item <kind>"> in the HTML, its direct children.
function partsOf(html, kind) {
    const start = html.search(new RegExp('<article class="item ' + kind + '[ "]'));
    assert.ok(start >= 0, 'no ' + kind + ' card');
    let i = html.indexOf('>', start) + 1, depth = 0;
    const out = [];
    const voids = /^(br|img|hr|input|meta|link|source|wbr)$/;
    while (i < html.length) {
        const lt = html.indexOf('<', i);
        if (lt < 0) break;
        const gt = html.indexOf('>', lt);
        const tag = html.slice(lt + 1, gt);
        i = gt + 1;
        if (tag.startsWith('!--')) { i = html.indexOf('-->', lt) + 3; continue; }
        if (tag.startsWith('/')) { if (depth === 0) break; depth--; continue; }
        const name = tag.split(/[\s/]/)[0].toLowerCase();
        if (depth === 0) {
            const cls = (tag.match(/class="([^"]*)"/) || [])[1] || '';
            out.push(name + (cls ? '.' + cls.split(' ')[0] : ''));
        }
        if (!voids.test(name) && !tag.endsWith('/')) depth++;
    }
    return out.map(part).filter(Boolean);
}

// part names a card's child by its role in spec 1.5.
function part(t) {
    if (t === 'div.stick') return 'pinned';
    if (t === 'div.hd') return 'first';
    if (/^h[23]/.test(t) || t.endsWith('.on') || t === 'div.what') return 'lead';
    if (t === 'div.ex') return 'body';
    if (t === 'div.ifoot') return 'footer';
    if (t === 'div.thread') return 'thread';
    if (t === 'button.fold') return null;   // "show less" is the app's control, above the rule, not one of the shape's parts
    return t;
}

const ctx = { me: 'me.example', base: 'https://me.example', now: '2026-11-17T12:00:00Z' };

test('the owner app\'s post card has the shape\'s parts, in its order (part 4)', () => {
    const golden = fs.readFileSync(path.join(goldens, 'posts__20260315__alpha.html'), 'utf8');
    const ours = card.html(card.parts({ type: 'post', title: 'Alpha', url: '/posts/20260315/alpha.html', published: '2026-03-15T10:00:00Z', author_domain: 'me.example', body_html: '<p>a</p>', comment_count: 1 }, ctx));
    assert.deepStrictEqual(partsOf(ours, 'post'), partsOf(golden, 'post'));
    assert.deepStrictEqual(partsOf(ours, 'post'), ['pinned', 'first', 'lead', 'body', 'footer', 'thread']);
});

test('the owner app\'s comment card has the shape\'s parts, in its order', () => {
    const golden = fs.readFileSync(path.join(goldens, 'comments__20260326__on-their-post.html'), 'utf8');
    const ours = card.html(card.parts({ type: 'comment', url: '/comments/20260326/on-their-post.html', published: '2026-03-26T10:00:00Z', author_domain: 'me.example', target_url: 'https://them.example/posts/20260301/p.html', target_post_title: 'Their post', comment_body_html: '<p>c</p>' }, ctx));
    assert.deepStrictEqual(partsOf(ours, 'cmt'), partsOf(golden, 'cmt'));
});

test('the owner\'s own item: the date leads the first row, with edit; copy and the link out under the rule', () => {
    const p = card.parts({ type: 'post', title: 'Mine', url: '/posts/20260828/a.html', published: '2026-08-28T16:20:07Z', author_domain: 'me.example', comment_count: 3, waiting: 2 }, ctx);
    assert.strictEqual(p.mine, true);
    assert.strictEqual(p.first.time, '28 Aug 2026');
    assert.strictEqual(p.first.timeLeads, true);
    assert.strictEqual(p.first.handle, '', 'no handle on the owner\'s own items');
    assert.deepStrictEqual(p.first.tools, ['edit']);
    assert.strictEqual(p.out, 'https://me.example/posts/20260828/a.html');
    assert.deepStrictEqual(p.footer.map((f) => f.k), ['comments', 'waiting', 'sp', 'copy', 'out'], 'the footer\'s fixed order');
    assert.strictEqual(p.data.waiting, '2');
});

test('items the sentence says are the owner\'s are drawn as the owner\'s, with or without an author', () => {
    const p = card.parts({ type: 'post', title: 'Mine', url: '/posts/20261118/a.html', published: '2026-11-18T10:00:00Z', author_domain: '' }, { own: true, now: ctx.now });
    assert.strictEqual(p.mine, true);
    assert.deepStrictEqual(p.first.tools, ['edit']);
});

test('someone else\'s item: new dot, handle, verb, the time at the right, and reply (1.5)', () => {
    const p = card.parts({ type: 'post', title: 'Theirs', url: 'https://bob.example/posts/20261117/b.html', published: '2026-11-17T10:00:00Z', author_domain: 'bob.example', new: true }, ctx);
    assert.strictEqual(p.mine, false);
    assert.strictEqual(p.first.newDot, true);
    assert.strictEqual(p.first.handle, 'bob.example');
    assert.strictEqual(p.first.time, '2h');
    assert.ok(!p.first.timeLeads);
    assert.deepStrictEqual(p.footer.map((f) => f.k), ['comments', 'sp', 'copy', 'out', 'reply']);
    const c = card.parts({ type: 'comment', url: 'https://bob.example/comments/x.html', published: '2026-11-16T10:00:00Z', author_domain: 'bob.example' }, ctx);
    assert.strictEqual(c.first.verb, 'commented on');
    assert.strictEqual(c.first.time, 'yesterday');
});

test('an event has a verb and its line, and no footer (1.5)', () => {
    const p = card.parts({ type: 'event', event: 'new-follower', actor: 'ridenhour.polis.pub', message: 'ridenhour.polis.pub started following you', link: 'https://ridenhour.polis.pub', published: '2026-03-09T00:00:37Z' }, ctx);
    assert.strictEqual(p.kind, 'event');
    assert.strictEqual(p.first.handle, 'ridenhour.polis.pub');
    assert.strictEqual(p.first.verb, 'followed you');
    assert.strictEqual(p.first.time, '9 Mar');
    assert.deepStrictEqual(p.footer, []);
    assert.doesNotMatch(card.html(p), /class="ifoot"/);
    assert.strictEqual(p.lead, null, 'a follow points at no one but the person: no lead line');
    const b = card.parts({ type: 'event', event: 'blessing-granted', actor: 'david.polis.pub', link: 'https://me.example/comments/20260304/hello.html', published: '2026-03-04T20:22:19Z' }, ctx);
    assert.strictEqual(b.first.verb, 'blessed your comment on', 'spec 3.4\'s label');
    assert.deepStrictEqual(b.lead, { tag: 'lead', text: 'me.example/comments/20260304/hello', url: 'https://me.example/comments/20260304/hello.html' });
});

test('a title the body already opens with is left out, so the card is untitled', () => {
    const p = card.parts({ type: 'post', title: 'Claude on', title_redundant: true, url: '/posts/x.html', author_domain: 'me.example', excerpt: 'Claude on the theme…' }, ctx);
    assert.strictEqual(p.lead, null);
    assert.strictEqual(p.untitled, true);
    assert.match(card.html(p), /class="item post untitled"/);
});

test('every value from an item is escaped; only the server\'s rendered body is HTML', () => {
    const h = card.html(card.parts({ type: 'post', title: '<img src=x onerror=alert(1)>', url: '/posts/"><b>.html', author_domain: 'bob.example"><x', published: 'x', body_html: '<p>ok</p>' }, ctx));
    assert.doesNotMatch(h, /<img src=x/);
    assert.doesNotMatch(h, /"><b>/);
    assert.doesNotMatch(h, /bob\.example"><x/);
    assert.match(h, /<div class="ex"><p>ok<\/p><\/div>/);
});

test('times: the full date for archives, relative for streams', () => {
    assert.strictEqual(card.fullDate('2026-08-28T16:20:07Z'), '28 Aug 2026');
    const now = '2026-11-17T12:00:00Z';
    assert.strictEqual(card.relativeTime('2026-11-17T11:59:30Z', now), 'just now');
    assert.strictEqual(card.relativeTime('2026-11-17T11:15:00Z', now), '45m');
    assert.strictEqual(card.relativeTime('2026-11-16T09:00:00Z', now), 'yesterday');
    assert.strictEqual(card.relativeTime('2025-12-01T09:00:00Z', now), '1 Dec 2025');
});

test('a click opens a card; on an open card the first row, the pinned row or the body folds it; the footer never', () => {
    const at = (sel) => ({ closest: (q) => q.split(',').map((s) => s.trim()).some((s) => s === sel) ? {} : null });
    assert.strictEqual(card.clickFolds(at('.ex'), false), 'open');
    assert.strictEqual(card.clickFolds(at('.hd'), true), 'fold');
    assert.strictEqual(card.clickFolds(at('.ex'), true), 'fold');
    assert.strictEqual(card.clickFolds(at('.ifoot'), true), null);
    assert.strictEqual(card.clickFolds(at('.ifoot'), false), null);
    assert.strictEqual(card.clickFolds(at('a'), false), null);
    assert.strictEqual(card.clickFolds(at('.thread'), false), null);
});

test('the footer: counts at the left; copy (with the item\'s address), the link out and reply at the right', () => {
    const p = card.parts({ type: 'post', title: 'Theirs', url: 'https://bob.example/posts/20261117/b.html', published: '2026-11-17T10:00:00Z', author_domain: 'bob.example', comment_count: 2 }, ctx);
    const h = card.html(p);
    const foot = h.slice(h.indexOf('class="ifoot"'));
    const at = (re) => foot.search(re);
    assert.ok(at(/cm-n/) < at(/class="sp"/) && at(/class="sp"/) < at(/class="copy"/) && at(/class="copy"/) < at(/class="out"/) && at(/class="out"/) < at(/class="link reply"/), foot);
    assert.match(foot, /class="copy"[^>]*data-url="https:\/\/bob\.example\/posts\/20261117\/b\.html"/);
});

test('"show less" sits above the rule, outside the footer; the pinned row has no link out', () => {
    const h = card.html(card.parts({ type: 'post', title: 'Mine', url: '/posts/20260828/a.html', published: '2026-08-28T16:20:07Z', author_domain: 'me.example', comment_count: 1 }, ctx));
    assert.ok(h.indexOf('class="fold"') < h.indexOf('class="ifoot"'), 'fold before the footer');
    assert.doesNotMatch(h.slice(h.indexOf('class="ifoot"')), /class="fold"/);
    const stick = h.slice(h.indexOf('class="stick"'), h.indexOf('class="hd"'));
    assert.doesNotMatch(stick, /class="out"/);
    assert.doesNotMatch(h, /hide comments/);
});

test('another site\'s post can always be opened, and opening it reads the whole post in place of its excerpt', async () => {
    const theirs = card.parts({ type: 'post', author_domain: 'alice.example', url: 'https://alice.example/posts/20261120/a.html', title: 'A', excerpt: 'Short…' }, ctx);
    assert.strictEqual(theirs.data.whole, 'fetch');
    const rendered = card.parts({ type: 'post', author_domain: 'alice.example', url: 'https://alice.example/posts/20261120/a.html', body_html: '<p>All of it</p>' }, ctx);
    assert.strictEqual(rendered.data.whole, undefined, 'a body the server rendered is already whole');
    const mine = card.parts({ type: 'post', author_domain: 'me.example', url: '/posts/20261120/b.html' }, ctx);
    assert.strictEqual(mine.data.whole, undefined, 'the owner\'s own post ships whole');

    // A card on a list wired with a loader: opening swaps the excerpt for the post.
    const classes = (init) => { const s = new Set(init); return { add: (c) => s.add(c), remove: (c) => s.delete(c), contains: (c) => s.has(c), toggle: (c, on) => (on ? s.add(c) : s.delete(c)) }; };
    const ex = { style: {}, scrollHeight: 40, addEventListener() {}, removeEventListener() {}, firstElementChild: null, _html: '<p>Short…</p>' };
    Object.defineProperty(ex, 'innerHTML', { get() { return this._html; }, set(v) { this._html = v; this.firstElementChild = { tagName: 'P', textContent: 'A', remove: () => { this._html = v.replace('<p>A</p>', ''); } }; } });
    const h3 = { textContent: 'A' };
    let asked = 0;
    const list = { _whole: () => { asked++; return Promise.resolve('<p>A</p><p>The whole post.</p>'); }, parentNode: null };
    const c = { dataset: { whole: 'fetch' }, classList: classes([]), parentNode: list,
        querySelector: (q) => (q === '.ex' ? ex : q === 'h3' ? h3 : null) };
    card.open(c);
    await new Promise((r) => setImmediate(r));
    assert.strictEqual(asked, 1);
    assert.strictEqual(ex.innerHTML, '<p>The whole post.</p>', 'the title the card already shows is not repeated');
    assert.strictEqual(c.dataset.whole, undefined, 'read once');

    // A post that can't be read keeps its excerpt and is tried again next time.
    const ex2 = { style: {}, scrollHeight: 40, addEventListener() {}, removeEventListener() {}, innerHTML: '<p>Short…</p>' };
    const c2 = { dataset: { whole: 'fetch' }, classList: classes([]), parentNode: { _whole: () => Promise.resolve('') },
        querySelector: (q) => (q === '.ex' ? ex2 : null) };
    card.open(c2);
    await new Promise((r) => setImmediate(r));
    assert.strictEqual(ex2.innerHTML, '<p>Short…</p>');
    assert.strictEqual(c2.dataset.whole, 'fetch');
});
