// write in the v5 owner app: its rules (views, the filters' counts, a
// post's thread and what can be done to each comment). Run with:
//   node --test webapp/internal/webui/v5-write.test.js

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');

const w = require(path.join(__dirname, 'www/v5/write.js'));
const sheet = require(path.join(__dirname, 'www/v5/sheet.js'));
const card = require(path.join(__dirname, 'www/v5/card.js'));

test('write\'s views are spec 2.1\'s sentences, and an address picks its view (3.9)', () => {
    assert.strictEqual(w.KINDS.post, 'all posts from me');
    assert.strictEqual(w.KINDS.comment, 'all comments from me');
    assert.strictEqual(w.WAITING, 'all comments from all polis to bless');
    assert.deepStrictEqual(w.view('all posts from me'), { kind: 'post' });
    assert.deepStrictEqual(w.view('all comments from all polis to bless'), { waiting: true });
    assert.deepStrictEqual(w.view('all comments on https://me.example/posts/20261110/hello.md to bless'),
        { waiting: true, post: 'https://me.example/posts/20261110/hello.md' }, 'the "comments waiting" email\'s link');
    assert.deepStrictEqual(w.view('all posts from my network'), {});
});

test('the filters: kinds, then waiting, each counting what the others leave (1.4)', () => {
    const s = {
        picked: [], waitingOn: false, waiting: 3, totals: { post: 12, comment: 5 },
        items: { post: [{ waiting: 2 }, { waiting: 1 }, { waiting: 0 }] },
    };
    assert.deepStrictEqual(w.counts(s), { post: 12, comment: 5, waiting: 3 });
    assert.deepStrictEqual(w.counts({ ...s, waitingOn: true }), { post: 2, comment: 0, waiting: 3 }, 'waiting narrows the kinds');
    assert.strictEqual(w.counts({ ...s, picked: ['comment'] }).waiting, 0, 'nothing waits on a comment of the owner\'s');
    assert.strictEqual(w.label('post', 1), '1 post');
    assert.strictEqual(w.label('waiting', 3), '3 waiting');
});

test('a post\'s thread: the waiting comments first, then the rest, with what can be done to each (3.3)', () => {
    const waiting = [{ url: 'https://carol.example/comments/20261113/two.md', author_domain: 'carol.example' }];
    const all = [
        { url: 'https://bob.example/comments/20261113/one.md', author_domain: 'bob.example', blessed: true },
        { url: 'https://carol.example/comments/20261113/two.html', author_domain: 'carol.example', blessed: false },
        { url: 'https://me.example/comments/20261113/mine.md', author_domain: 'me.example', blessed: true },
        { url: 'https://dan.example/comments/20261113/three.md', author_domain: 'dan.example', blessed: false },
    ];
    const rows = w.thread(waiting, all, 'me.example');
    assert.deepStrictEqual(rows.map((r) => [r.c.author_domain, r.state, r.actions.join('|')]), [
        ['carol.example', 'waiting', 'not here|Bless'],
        ['bob.example', 'blessed', 'reply|unbless'],
        ['me.example', 'blessed', 'reply'],
        ['dan.example', 'not blessed', ''],
    ], 'the waiting one appears once, first; your own is never unblessed');
});

test('one post\'s addresses compare equal whatever their form', () => {
    const k = w.postKey('https://me.example/posts/20261110/hello.md');
    assert.strictEqual(w.postKey('/posts/20261110/hello.html'), k);
    assert.strictEqual(w.postKey('posts/20261110/hello.html'.replace(/^/, '/')), k);
    assert.strictEqual(w.postKey('https://me.example/content/pub.polis.core/post/20261110/hello.md'), k);
});

test('editing a comment republishes it as a comment, and replying to yourself says so (3.3)', () => {
    assert.deepStrictEqual(sheet.request('edit', 'new words', { type: 'comment', path: 'comments/20261113/x.md', id: 'c1' }),
        { method: 'PUT', path: '/v1/content/comment/c1', body: { path: 'comments/20261113/x.md', markdown: 'new words' } });
    assert.strictEqual(sheet.request('edit', 'x', { path: 'posts/a.md', id: 'p1' }).path, '/v1/content/post/p1');
});

test('your comment\'s card links out to the post it answered: "Open the post on <site>" (3.3)', () => {
    const p = card.parts({ type: 'comment', url: '/comments/20261113/x.html', author_domain: 'me.example', target_url: 'https://bob.example/posts/20261113/b.html', target_post_title: 'B' },
        { me: 'me.example', base: 'https://me.example' });
    assert.strictEqual(p.out, 'https://bob.example/posts/20261113/b.html');
    assert.match(card.html(p), /aria-label="Open the post on bob\.example"/);
});

test('a republished post of yours says when it was edited, after its date (3.3)', () => {
    const c = { me: 'me.example', base: 'https://me.example' };
    const p = card.parts({ type: 'post', title: 'A', url: '/posts/20261110/a.html', author_domain: 'me.example', published: '2026-11-10T09:00:00Z', edited: '2026-11-18T03:31:10Z' }, c);
    assert.strictEqual(p.first.edited, '18 Nov 2026');
    assert.match(card.html(p), /<time class="hdt"[^>]*>10 Nov 2026<\/time><span class="edited">\u00b7 edited 18 Nov 2026<\/span>/);
    const never = card.parts({ type: 'post', title: 'B', url: '/posts/20261110/b.html', author_domain: 'me.example', published: '2026-11-10T09:00:00Z' }, c);
    assert.doesNotMatch(card.html(never), /edited/);
});

test('what one of your agents made says so after its date and edit, linking to its connection (2.1)', () => {
    const c = { me: 'me.example', base: 'https://me.example' };
    const grant = 'https://me.example/content/pub.polis.core/attestation/20261116T022436Z-e.json';
    const p = card.parts({ type: 'post', title: 'A', url: '/posts/20261116/a.html', author_domain: 'me.example', published: '2026-11-16T02:24:36Z', edited: '2026-11-16T02:53:48Z', agent: 'Claude <Code>', grant }, c);
    assert.strictEqual(p.first.agent, 'Claude <Code>');
    const h = card.html(p);
    assert.match(h, /<span class="edited">· edited 16 Nov 2026<\/span><a class="by-agent" href="\/_\/enhance\/agents#connections" title="Claude &lt;Code&gt;, an agent you connected, made this\. See its connection in enhance › Agents\.">· by Claude &lt;Code&gt;<\/a>/);
    const cmt = card.parts({ type: 'comment', url: '/comments/20261116/x.html', author_domain: 'me.example', published: '2026-11-16T02:53:10Z', agent: 'Claude Code', grant }, c);
    assert.match(card.html(cmt), /· by Claude Code<\/a>/);
    const owner = card.parts({ type: 'post', title: 'B', url: '/posts/20261116/b.html', author_domain: 'me.example', published: '2026-11-16T02:24:36Z' }, c);
    assert.doesNotMatch(card.html(owner), /by-agent/);
    const half = card.parts({ type: 'post', title: 'C', url: '/posts/20261116/c.html', author_domain: 'me.example', published: '2026-11-16T02:24:36Z', agent: 'Claude Code' }, c);
    assert.doesNotMatch(card.html(half), /by-agent/);
});
