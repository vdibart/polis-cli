// Tests for following a site from the SPA (www/app.js). Run with:
//   node --test webapp/internal/webui/follow.test.js
//
// Lives one level ABOVE www/ so `//go:embed www` does not ship or serve it,
// matching the other SPA tests.
//
// Why this file exists: the webapp had no reachable way to follow a site that
// is not on polis.pub (the follow panel had no caller), and once it was
// reachable, normalizeFollowInput rewrote ANY <domain>/f/<handle> into
// <handle>.polis.pub, so an outside site using that path shape was misrouted.

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const www = path.join(__dirname, 'www');
const appSrc = fs.readFileSync(path.join(www, 'app.js'), 'utf8');
const indexHtml = fs.readFileSync(path.join(www, 'index.html'), 'utf8');
const ownerExtras = fs.readFileSync(path.join(www, 'owner-extras.js'), 'utf8');

const elements = {};
globalThis.window = { location: { hostname: 'me.example.org' } };
globalThis.document = {
    addEventListener() {},
    getElementById(id) { return elements[id] || null; },
};
(0, eval)(appSrc);
const App = globalThis.window.App;

test('polis.pub follow links and follow pages are rewritten', () => {
    assert.strictEqual(App.normalizeFollowInput('polis.pub/f/alice'), 'https://alice.polis.pub/');
    assert.strictEqual(App.normalizeFollowInput('https://polis.pub/f/alice'), 'https://alice.polis.pub/');
    assert.strictEqual(App.normalizeFollowInput('https://polis.pub/follow?author=bob.example.com'), 'https://bob.example.com/');
});

test('an outside domain is followed as entered', () => {
    assert.strictEqual(App.normalizeFollowInput('alice.example.com'), 'https://alice.example.com/');
    assert.strictEqual(App.normalizeFollowInput('https://Alice.Example.com'), 'https://alice.example.com/');
    assert.strictEqual(App.normalizeFollowInput('https://polis-kay.vercel.app/'), 'https://polis-kay.vercel.app/');
});

test('an outside site whose path looks like a follow link is not sent to polis.pub', () => {
    assert.strictEqual(App.normalizeFollowInput('example.com/f/alice'), 'https://example.com/f/alice/');
    assert.strictEqual(App.normalizeFollowInput('example.com/follow?author=x.polis.pub'), 'https://example.com/follow?author=x.polis.pub/');
});

function followRig(value, apiImpl) {
    elements['follow-url-input'] = { value };
    elements['follow-panel'] = { classList: { add() {}, remove() {} } };
    const toasts = [];
    const posted = [];
    App.showToast = (msg, kind) => toasts.push({ msg, kind });
    App.api = async (method, url, body) => { posted.push({ method, url, body }); return apiImpl(body); };
    App.loadAllCounts = async () => {};
    App.loadViewContent = async () => {};
    return { toasts, posted };
}

test('submitting an outside site posts it unchanged and reports success', async () => {
    const { toasts, posted } = followRig('alice.example.com', async () => ({ data: {} }));
    await App.submitFollow();
    assert.deepStrictEqual(posted, [{ method: 'POST', url: '/api/following', body: { url: 'https://alice.example.com/' } }]);
    assert.ok(toasts.some(t => t.kind === 'success' && t.msg.includes('alice.example.com')), JSON.stringify(toasts));
});

test('a site the server cannot follow shows an error, never silence', async () => {
    const { toasts } = followRig('not-polis.example.com', async () => { throw new Error('not a polis site'); });
    await App.submitFollow();
    assert.ok(toasts.some(t => t.kind === 'error' && t.msg.includes('not a polis site')), JSON.stringify(toasts));
});

test('an http URL is refused with an error before any request', async () => {
    const { toasts, posted } = followRig('http://alice.example.com', async () => ({ data: {} }));
    await App.submitFollow();
    assert.strictEqual(posted.length, 0);
    assert.ok(toasts.some(t => t.kind === 'error'), JSON.stringify(toasts));
});

test('the follow panel is reachable: the avatar menu and the mobile menu open it', () => {
    assert.match(indexHtml, /onclick="App\.openFollowPanel\(\)"[^>]*>.*Follow a site/);
    assert.match(indexHtml, /data-action="follow-site"/);
    assert.match(ownerExtras, /action === 'follow-site'[\s\S]{0,120}openFollowPanel\(\)/);
});
