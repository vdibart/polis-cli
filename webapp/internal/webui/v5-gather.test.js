// gather in the v5 owner app: its rules (filters, counts, search, follow)
// and the cards it adds (a person found by search, Follow under an event).
// Run with:
//   node --test webapp/internal/webui/v5-gather.test.js

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');

const g = require(path.join(__dirname, 'www/v5/gather.js'));
const card = require(path.join(__dirname, 'www/v5/card.js'));
const ctx = { me: 'me.example', base: 'https://me.example', now: '2026-11-17T12:00:00Z' };

test('gather\'s views are spec 2.2\'s sentences, and an addressed one picks its filter (3.9)', () => {
    assert.deepStrictEqual(g.KINDS, {
        post: 'all posts from my network',
        comment: 'all comments from my network',
        event: 'all activity about me',
    });
    assert.strictEqual(g.kindOf('all activity about me'), 'event');
    assert.strictEqual(g.kindOf('  All  posts from my network '), 'post');
    assert.strictEqual(g.kindOf('all posts from my network by date'), '', 'any other sentence is a view of its own');
});

test('nothing picked shows everything; the counts react to each other (1.4)', () => {
    const s = { totals: { post: 12, comment: 6, event: 3 }, fresh: { post: 4, comment: 1, event: 2 }, passed: {}, picked: [], newOn: false };
    assert.deepStrictEqual(g.counts(s), { post: 12, comment: 6, event: 3, new: 7 });
    // Picking kinds narrows the new count…
    assert.strictEqual(g.counts({ ...s, picked: ['post', 'event'] }).new, 6);
    // …and picking new narrows the kinds' counts.
    assert.deepStrictEqual(g.counts({ ...s, newOn: true }), { post: 4, comment: 1, event: 2, new: 7 });
});

test('new counts down as new items scroll past, and never below none (1.6)', () => {
    const s = { totals: { post: 2 }, fresh: { post: 2, comment: 0, event: 0 }, passed: { post: 1 }, picked: [], newOn: false };
    assert.strictEqual(g.counts(s).new, 1);
    assert.strictEqual(g.counts({ ...s, passed: { post: 5 } }).new, 0);
});

test('the filters\' words: the count leads its noun', () => {
    assert.strictEqual(g.label('post', 1), '1 post');
    assert.strictEqual(g.label('comment', 6), '6 comments');
    assert.strictEqual(g.label('event', 0), '0 events');
    assert.strictEqual(g.label('new', 3), '3 new');
});

test('a count that is a lower bound says so: the cache filled, so "300+ posts", never a bare 300', () => {
    assert.strictEqual(g.label('post', 300, true), '300+ posts');
    assert.strictEqual(g.label('comment', 1, true), '1+ comments');
    assert.strictEqual(g.label('post', 300, false), '300 posts');
    assert.strictEqual(g.label('new', 4, true), '4 new');
});

test('the three answers merge newest first, a time that cannot be read last', () => {
    const out = g.merge([
        [{ id: 'a', published: '2026-11-17T09:00:00Z' }, { id: 'x', published: '' }],
        [{ id: 'b', published: '2026-11-17T11:00:00Z' }],
        [{ id: 'c', published: '2026-11-16T11:00:00Z' }],
    ]);
    assert.deepStrictEqual(out.map((i) => i.id), ['b', 'a', 'c', 'x']);
});

test('search: a site\'s address goes to that site, anything else is words (2.2)', () => {
    for (const a of ['maren.polis.pub', 'https://okonkwo.dev/', 'Bob.Example']) assert.ok(g.isAddress(a), a);
    for (const w of ['trust networks', 'garden', 'polis.pub is', '.example', 'a..b']) assert.ok(!g.isAddress(w), w);
    assert.strictEqual(g.siteOf('https://Bob.Example/'), 'bob.example');
});

test('Follow is the public site\'s button: + Follow, ✓ Following, − Unfollow on rollover (3.4)', () => {
    assert.strictEqual(g.followLabel(false), '+ Follow');
    assert.strictEqual(g.followLabel(false, true), '+ Follow');
    assert.strictEqual(g.followLabel(true), '✓ Following');
    assert.strictEqual(g.followLabel(true, true), '− Unfollow');
});

test('a post\'s thread is asked as a sentence, and the follow list reads as hosts', () => {
    assert.strictEqual(g.threadSentence('https://bob.example/posts/20261116/g.html'), 'all comments on https://bob.example/posts/20261116/g.html');
    assert.deepStrictEqual(g.followed([{ url: 'https://Bob.Example' }, 'https://ana.example/', {}]), ['bob.example', 'ana.example']);
});

test('someone who followed you: Follow sits under the row, not in a footer (1.5)', () => {
    const p = card.parts({ type: 'event', event: 'new-follower', actor: 'bob.example', link: 'https://bob.example', published: '2026-11-17T11:00:00Z' }, ctx);
    assert.deepStrictEqual(p.cta, { follow: 'bob.example' });
    const h = card.html(p);
    assert.match(h, /<div class="cta"><button class="slim follow" type="button" data-site="bob.example">\+ Follow<\/button><\/div>/);
    assert.doesNotMatch(h, /class="ifoot"/);
    const b = card.parts({ type: 'event', event: 'blessing-granted', actor: 'ana.example', link: 'https://ana.example/c.html' }, ctx);
    assert.strictEqual(b.cta, undefined, 'only a follow carries Follow');
});

test('a person found by search: handle, how you stand, their latest post, Follow (2.2)', () => {
    const p = card.parts({ type: 'profile', author_domain: 'amit.polis.pub', author_url: 'https://amit.polis.pub', relationship: 'follows-you', recent_post: { title: 'On gardens', url: 'https://amit.polis.pub/posts/20261101/g.html' } }, ctx);
    assert.strictEqual(p.kind, 'person');
    assert.strictEqual(p.first.handle, 'amit.polis.pub');
    assert.strictEqual(p.first.verb, 'follows you');
    assert.deepStrictEqual(p.lead, { tag: 'lead', text: 'On gardens', url: 'https://amit.polis.pub/posts/20261101/g.html' });
    assert.deepStrictEqual(p.cta, { follow: 'amit.polis.pub' });
    assert.deepStrictEqual(p.footer, []);
    assert.strictEqual(card.parts({ type: 'profile', author_domain: 'x.example', relationship: 'mutual' }, ctx).first.verb, 'you follow each other');
    const you = card.parts({ type: 'profile', author_domain: 'me.example' }, ctx);
    assert.strictEqual(you.first.verb, 'that’s you');
    assert.strictEqual(you.cta, undefined, 'no Follow for your own site');
});

test('an event\'s link into the owner\'s app reads as what it shows, and opens in place (3.9)', () => {
    assert.strictEqual(card.linkText('/_/write#/posts/20261117/a.html', 'me.example'), 'me.example/posts/20261117/a');
    assert.strictEqual(card.linkText('/_/write/pql/all+comments+from+me'), 'all comments from me');
    const p = card.parts({ type: 'event', event: 'new-comment', actor: 'bob.example', link: '/_/write#/posts/20261117/a.html' }, ctx);
    const h = card.html(p);
    assert.match(h, /<a href="\/_\/write#\/posts\/20261117\/a.html">me.example\/posts\/20261117\/a<\/a>/);
    const away = card.html(card.parts({ type: 'event', event: 'blessing-granted', actor: 'a.example', link: 'https://me.example/comments/c.html' }, ctx));
    assert.match(away, /target="_blank" rel="noopener"/);
});

test('a comment whose post\'s title is not known names the post by its address', () => {
    const p = card.parts({ type: 'comment', author_domain: 'ana.example', url: 'https://ana.example/comments/c.html', target_url: 'https://bob.example/posts/20261116/gardens.html' }, ctx);
    assert.deepStrictEqual(p.lead, { tag: 'on', title: 'posts/20261116/gardens', site: 'bob.example', url: 'https://bob.example/posts/20261116/gardens.html' });
});

test('someone else\'s mark and handle go to their site, in this window; your own items have neither link', () => {
    const h = card.html(card.parts({ type: 'post', title: 'B', url: 'https://bob.example/posts/20261117/b.html', author_domain: 'bob.example', author_url: 'https://bob.example', published: '2026-11-17T10:00:00Z' }, ctx));
    assert.match(h, /<a class="who-mark" href="https:\/\/bob\.example"/);
    assert.match(h, /<a class="hh" href="https:\/\/bob\.example">bob\.example<\/a>/);
    assert.doesNotMatch(h, /class="(who-mark|hh)"[^>]*target=/, 'same window');
    const mine = card.html(card.parts({ type: 'post', title: 'M', url: '/posts/20261117/m.html', author_domain: 'me.example' }, ctx));
    assert.doesNotMatch(mine, /who-mark|class="hh"/);
});

test('while the owner is in gather, what arrived beyond what it drew goes in, by kind', () => {
    const drawn = { post: [{ id: 'p1' }], comment: [{ url: 'https://a.example/c1' }], event: [] };
    assert.deepStrictEqual(g.arrivals(drawn, [[{ id: 'p1' }], [{ url: 'https://a.example/c1' }], []]), {}, 'nothing new beyond what shows');
    assert.deepStrictEqual(g.arrivals(drawn, [[{ id: 'p2' }, { id: 'p1' }], [], []]), { post: [{ id: 'p2' }] }, 'only the new post');
    assert.deepStrictEqual(g.arrivals(drawn, [[], [], [{ id: 'e1' }]]), { event: [{ id: 'e1' }] }, 'a new event');
    assert.deepStrictEqual(g.arrivals(drawn, [null, null, null]), {}, 'a failed ask says nothing');
    assert.deepStrictEqual(g.arrivals({}, [[{ id: 'p1' }], [], []]), { post: [{ id: 'p1' }] });
});

test('a follow link from polis.pub names the site to follow, once, and leaves the address clean (3.9)', () => {
    // As the follow link sends it, after the server checked the token.
    assert.deepStrictEqual(g.followIntent('?intent=follow&target=bob.polis.pub&ft=mac&fe=1795317103'),
        { site: 'bob.polis.pub', search: '' });
    // The target may come as an address; other parameters stay.
    assert.deepStrictEqual(g.followIntent('?view=x&intent=follow&target=https%3A%2F%2FBob.polis.pub%2F&ft=m&fe=1'),
        { site: 'bob.polis.pub', search: '?view=x' });
    // No follow intent: nothing to do.
    assert.strictEqual(g.followIntent(''), null);
    assert.strictEqual(g.followIntent('?intent=comment&target=bob.polis.pub'), null);
    // A target that is not a site's address follows no one, and is still cleared.
    assert.deepStrictEqual(g.followIntent('?intent=follow&target=%3Cscript%3E&ft=m&fe=1'), { site: '', search: '' });
    assert.deepStrictEqual(g.followIntent('?intent=follow'), { site: '', search: '' });
});
