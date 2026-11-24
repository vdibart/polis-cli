// The widget (polis.pub's widget.js) and nav (nav.js) on a v5 site. Run with:
//   node --test webapp/internal/webui/widget-v5.test.js
//
// Loads the real files with a stub page. The widget's v5 drawing is checked
// through its models (what each state says and does, spec 3.8); nav.js is
// run on a v5 page to check it draws a signed-in visitor's way home and nothing else.

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const hosted = path.join(__dirname, '../hosted');
const noop = function () {};

function stubPage(extra) {
    globalThis.window = { location: { href: 'https://bob.polis.pub/posts/20260101/a.html', hash: '' }, addEventListener: noop };
    globalThis.localStorage = { getItem: () => null, setItem: noop, removeItem: noop };
    globalThis.sessionStorage = { getItem: () => null, setItem: noop, removeItem: noop };
    globalThis.document = Object.assign({
        readyState: 'loading', // startup waits for DOMContentLoaded, which never comes
        cookie: '',
        addEventListener: noop,
        getElementById: () => null,
        querySelectorAll: () => [],
        createElement: () => ({ style: {}, setAttribute: noop, appendChild: noop }),
    }, extra || {});
}

function loadWidget() {
    stubPage();
    (0, eval)(fs.readFileSync(path.join(hosted, 'widget/widget.js'), 'utf8'));
    return window.PolisWidget;
}

const config = { postUrl: 'https://bob.polis.pub/posts/20260101/a.html', author: 'bob.polis.pub' };

test('a stranger replies, or says they have a site (spec 3.8)', () => {
    const m = loadWidget().v5ReplyModel({ state: 'unknown' }, config);
    assert.strictEqual(m.label, 'Reply');
    assert.strictEqual(m.signIn, 'I have a site: sign in');
    assert.strictEqual(m.action, 'Publish');
});

test('a signed-in visitor replies as their own site, published there', () => {
    const w = loadWidget();
    for (const state of ['connected', 'known']) {
        const m = w.v5ReplyModel({ state, instanceUrl: 'https://maren.polis.pub', token: 't' }, config);
        assert.strictEqual(m.label, 'Reply as maren.polis.pub', state);
        assert.strictEqual(m.note, 'Published on your site, signed by you.', state);
        assert.strictEqual(m.action, 'Publish reply', state);
    }
    // Not yet connected on this site: publishing goes through the confirmed connect, and back here.
    const k = w.v5ReplyModel({ state: 'known', instanceUrl: 'https://maren.polis.pub' }, config);
    assert.ok(k.href.startsWith('https://maren.polis.pub/api/widget/connect?return='), k.href);
    assert.ok(decodeURIComponent(k.href.split('return=')[1]).endsWith('#comments'));
});

test('the owner is sent to write, at this post', () => {
    const m = loadWidget().v5ReplyModel({ state: 'owner', instanceUrl: 'https://bob.polis.pub' }, config);
    assert.strictEqual(m.action, 'Open in write');
    assert.strictEqual(m.href, '/_/write#/posts/20260101/a.html');
});

test('the top right: Follow, Following (Unfollow on rollover), or Enhance your site', () => {
    const w = loadWidget();
    assert.strictEqual(w.v5FollowModel({ state: 'unknown' }, false).label, '+ Follow');
    assert.strictEqual(w.v5FollowModel({ state: 'connected' }, false).label, '+ Follow');
    const f = w.v5FollowModel({ state: 'connected' }, true);
    assert.strictEqual(f.label, '✓ Following');
    assert.strictEqual(f.hover, '− Unfollow');
    const o = w.v5FollowModel({ state: 'owner' }, false);
    assert.strictEqual(o.label, 'Enhance your site');
    assert.strictEqual(o.href, '/_/enhance');
});

test('a typed site becomes its address, and nothing else does', () => {
    const n = loadWidget().normaliseSite;
    assert.strictEqual(n('maren'), 'https://maren.polis.pub');
    assert.strictEqual(n(' https://Maren.polis.pub/posts/x '), 'https://maren.polis.pub');
    assert.strictEqual(n('example.org'), 'https://example.org');
    assert.strictEqual(n('javascript:alert(1)'), '');
    assert.strictEqual(n('a b'), '');
    assert.strictEqual(n(''), '');
});

// runNavV5 runs nav.js on a v5 page of bob.polis.pub with the given identity
// block (null: a stranger) and returns what it drew.
function runNavV5(identityBlock) {
    const added = [];
    const rootClasses = new Set();
    let shadowMode = null;
    const shadowChildren = [];
    const bodyChildren = [];
    const el = (tag) => ({
        tag, style: {}, attrs: {}, children: [], className: '', textContent: '', innerHTML: '',
        setAttribute(k, v) { this.attrs[k] = v; },
        appendChild(c) { this.children.push(c); return c; },
    });
    const bodyClasses = new Set();
    const dispatched = [];
    const body = { classList: { contains: (c) => c === 'v5', add: (c) => bodyClasses.add(c) }, appendChild: (c) => { bodyChildren.push(c); c.parentNode = body; return c; } };
    const header = {};
    const root = {
        parentNode: header,
        style: {},
        getAttribute: (k) => (k === 'data-host' ? 'bob.polis.pub' : ''),
        classList: { add: (c) => rootClasses.add(c) },
        attachShadow: (o) => { shadowMode = o.mode; return { appendChild: (c) => shadowChildren.push(c) }; },
    };
    const identity = identityBlock && { textContent: JSON.stringify(identityBlock), remove: noop };
    stubPage({
        body,
        head: { appendChild: (c) => added.push(c) },
        getElementById: (id) => (id === 'polis-nav-root' ? root : id === 'polis-nav-identity' ? identity : null),
        querySelector: () => null,
        createElement: el,
        createTextNode: (t) => ({ text: t }),
    });
    window.dispatchEvent = (e) => dispatched.push(e.type);
    (0, eval)(fs.readFileSync(path.join(hosted, 'nav/nav.js'), 'utf8'));
    return { added, rootClasses, shadowMode, shadowChildren, bodyChildren, root, body, bodyClasses, dispatched };
}

test('on a v5 page, a signed-in visitor\'s way home floats at the lower left, in a closed shadow root', () => {
    const r = runNavV5({ home: 'https://maren.polis.pub', handle: 'maren', state: { home_url: 'https://maren.polis.pub', avatar: { bg: '#8a5a28', fg: '#fff' } } });
    assert.strictEqual(r.shadowMode, 'closed');
    const links = r.shadowChildren.filter((c) => c.tag === 'a');
    assert.strictEqual(links.length, 1, 'one control, nothing else');
    const a = links[0];
    assert.strictEqual(a.href, 'https://maren.polis.pub/_/', 'it leads to their home');
    assert.strictEqual(a.attrs['aria-label'], 'Back to your polis, maren.polis.pub');
    const [mark, words] = a.children;
    assert.strictEqual(mark.className, 'av');
    assert.strictEqual(mark.children[0].text, 'M', 'their mark, by its initial');
    assert.strictEqual(mark.children[1].className, 'hs', 'with the house on it');
    assert.ok(!/maren/.test(mark.children[1].innerHTML), 'the house is a constant');
    assert.strictEqual(words.textContent, 'back to maren.polis.pub', 'rollover and focus reveal where it goes');
    const css = r.shadowChildren.find((c) => c.tag === 'style').textContent;
    assert.ok(/position:fixed;left:20px;bottom:calc\(var\(--foot,52px\) \+ 16px\)/.test(css), 'lower left, above the footer');
    assert.ok(/a:hover \.gl,a:focus-visible \.gl\{max-width:220px;\}/.test(css), 'rollover and keyboard focus widen it');
    assert.strictEqual(r.root.parentNode, r.body, 'out of the top bar, to the end of the page');
    assert.strictEqual(r.root.style.display, 'block', 'shown, though a host with only a shadow root matches :empty');
    assert.ok(r.rootClasses.has('pn-v5'), 'the page learns only the class');
    assert.strictEqual(r.added.length, 0, 'nothing added to the page itself');
});

test('on a v5 page, a stranger and the owner at home see no way home', () => {
    const stranger = runNavV5(null);
    assert.strictEqual(stranger.shadowMode, null);
    const owner = runNavV5({ home: 'https://bob.polis.pub', handle: 'bob', state: { home_url: 'https://bob.polis.pub' } });
    assert.strictEqual(owner.shadowMode, null, 'the owner\'s menu has its own home');
    assert.strictEqual(owner.bodyChildren.length, 0);
});

test('on a v5 page, only the site\'s own author gets pn-owner, so the page shows home and edit (spec 3.8)', () => {
    const owner = runNavV5({ home: 'https://bob.polis.pub', handle: 'bob', state: { home_url: 'https://bob.polis.pub' } });
    assert.ok(owner.bodyClasses.has('pn-owner'), 'the owner, at home, is marked');
    assert.ok(owner.dispatched.includes('resize'), 'the menu is asked to refit around home');
    const visitor = runNavV5({ home: 'https://maren.polis.pub', handle: 'maren', state: { home_url: 'https://maren.polis.pub' } });
    assert.ok(!visitor.bodyClasses.has('pn-owner'), '⛔ a visitor is never marked');
    const stranger = runNavV5(null);
    assert.ok(!stranger.bodyClasses.has('pn-owner'), '⛔ a stranger is never marked');
});
