// The stream shape (shapes/v4/stream.js) keeps a sentence's relation when a
// /pql/<sentence> page hydrates from its seed. Run with:
//   node --test webapp/internal/webui/stream-relation.test.js
//
// Loads the real shape with a DOM stub that holds nothing, so init never runs
// and every slot lookup finds no element; then applies a seed through
// PolisStream.setFilter and reads the sentence the shape asks the server for.

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const shapePath = path.join(__dirname,
    '../../../cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.js');

// loadShape evaluates a fresh copy of stream.js and returns its PolisStream
// and the URLs it fetched.
function loadShape() {
    const fetched = [];
    const noop = function () {};
    const el = null;
    globalThis.window = {
        location: { href: 'https://alice.polis.pub/pql/x', pathname: '/pql/x', hash: '', search: '' },
        addEventListener: noop,
        matchMedia: function () { return { matches: false, addEventListener: noop }; },
        innerWidth: 1200,
    };
    globalThis.document = {
        readyState: 'loading', // init waits for DOMContentLoaded, which never comes
        addEventListener: noop,
        removeEventListener: noop,
        querySelector: function () { return el; },
        querySelectorAll: function () { return []; },
        getElementById: function () { return el; },
        createElement: function () {
            return { classList: { add: noop, remove: noop, contains: function () { return false; } },
                setAttribute: noop, appendChild: noop, style: {} };
        },
        body: { classList: { contains: function () { return false; }, add: noop, remove: noop } },
    };
    globalThis.IntersectionObserver = function () { return { observe: noop, unobserve: noop, disconnect: noop }; };
    globalThis.fetch = function (url) {
        fetched.push(String(url));
        return new Promise(noop); // never settles: only the request matters
    };
    window.fetch = globalThis.fetch;
    window.IntersectionObserver = globalThis.IntersectionObserver;
    (0, eval)(fs.readFileSync(shapePath, 'utf8'));
    return { stream: window.PolisStream, fetched: fetched };
}

// sentenceOf turns a fetched /pql/ data URL back into its sentence.
function sentenceOf(url) {
    const p = url.split('?')[0].replace(/^\/pql\//, '');
    return decodeURIComponent(p.replace(/\+/g, ' '));
}

function filterAfter(seed) {
    const { stream, fetched } = loadShape();
    // 'activity' is an owner type: the owner SPA registers it.
    stream.registerFilterOption('type', { value: 'activity', label: 'activity' });
    let last = null;
    stream.onFilterChange(function (snap) { last = snap; });
    stream.setFilter(seed);
    return { last: last, fetched: fetched };
}

const post = 'https://alice.polis.pub/posts/20261113/Hello.html';

test("an 'on' seed asks for that post's comments, not 'from' it", () => {
    const { last, fetched } = filterAfter({ qualifier: 'all', type: 'comments', relation: 'on', scope: post });
    assert.strictEqual(last.relation, 'on');
    assert.ok(fetched.length > 0, 'the shape fetched nothing');
    const url = fetched[fetched.length - 1];
    assert.ok(!url.includes('//alice'), 'the post address must be encoded, or the server collapses its //: ' + url);
    assert.match(sentenceOf(url), /^all comments on https:\/\/alice\.polis\.pub\/posts\/20261113\/Hello\.html( by date)?$/);
});

test("a 'blessed' thread keeps the adjective before the type", () => {
    const { fetched } = filterAfter({ qualifier: 'all', type: 'comments', relation: 'on', scope: post, modifier: 'blessed' });
    assert.strictEqual(sentenceOf(fetched[fetched.length - 1]), 'all blessed comments on ' + post);
});

test("an 'about' seed keeps its relation", () => {
    const { last, fetched } = filterAfter({ qualifier: 'all', type: 'activity', relation: 'about', scope: 'me' });
    assert.strictEqual(last.relation, 'about');
    assert.match(sentenceOf(fetched[fetched.length - 1]), /^all activity about me/);
});

test("a preset without a relation reads 'from'", () => {
    const { stream, fetched } = loadShape();
    stream.setFilter({ qualifier: 'all', type: 'comments', relation: 'on', scope: post });
    stream.setFilter({ qualifier: 'all', type: 'posts', scope: '@alice.polis.pub' });
    assert.match(sentenceOf(fetched[fetched.length - 1]), /^all posts from alice\.polis\.pub/);
});

test("leaving a thread for another type drops the post address", () => {
    const { stream } = loadShape();
    let last = null;
    stream.onFilterChange(function (snap) { last = snap; });
    stream.setFilter({ qualifier: 'all', type: 'comments', relation: 'on', scope: post });
    stream.setFilter({ type: 'posts' });
    assert.strictEqual(last.relation, 'from');
    assert.ok(!String(last.scope).startsWith('https://'), 'the post address is still the scope: ' + last.scope);
});

// The server puts a view's filter in the page as a JSON data block (an inline
// script would never run under the page's script-src 'self'); the shape reads
// it from there.
test('the seed is read from the page\'s data block', () => {
    const { stream } = loadShape();
    const block = { textContent: JSON.stringify({ qualifier: 'all', type: 'comments', relation: 'from', scope: '@alice.polis.pub' }) };
    document.getElementById = function (id) { return id === 'polis-initial-pql' ? block : null; };
    assert.deepStrictEqual(stream.initialPQLSeed(), { qualifier: 'all', type: 'comments', relation: 'from', scope: '@alice.polis.pub' });
    block.textContent = 'not json';
    assert.strictEqual(stream.initialPQLSeed(), null);
    document.getElementById = function () { return null; };
    assert.strictEqual(stream.initialPQLSeed(), null);
});
