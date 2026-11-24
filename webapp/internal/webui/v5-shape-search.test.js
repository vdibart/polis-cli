// The public site's "search sites" (the following list) matches as the site's
// and the discovery service's search do: every word begins a word of the
// site's handle (its domain without the shared suffix), or begins the domain.
// Run with: node --test webapp/internal/webui/v5-shape-search.test.js
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const src = fs.readFileSync(path.join(__dirname, '../../../cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v5/v5.js'), 'utf8');
const fn = src.match(/function siteMatches[\s\S]*?\n {2}}\n/);
assert.ok(fn, 'v5.js has a siteMatches');
const siteMatches = new Function(fn[0] + '; return siteMatches;')();

test('a site search matches the handle at a word start, never the shared suffix', () => {
  const cases = [
    ['alice.polis.pub', 'ali', true],
    ['alice.polis.pub', 'ALI', true],
    ['alice.polis.pub', 'polis', false],
    ['alice.polis.pub', 'pub', false],
    ['polisfan.polis.pub', 'polis', true],
    ['alice.polis.pub', 'alice.polis.pub', true],
    ['hugo.polis.pub', 'go', false],
    ['carol-go.polis.pub', 'go', true],
    ['carol-go.polis.pub', 'carol-go', true],
    ['example.com', 'example', true],
    ['example.com', 'com', false],
    ['alice.polis.pub', '', true],
    ['alice.polis.pub', 'alice bob', false],
  ];
  for (const [domain, q, want] of cases) assert.strictEqual(siteMatches(domain, q), want, `${domain} / ${q}`);
});

// The toolbar's numbers come from the site's /pql/ counts (spec 3.8); a
// count the server capped keeps the page's own number.
test('the toolbar counts are the /pql/ answers, minus a capped one', () => {
  const fn2 = src.match(/function countsFrom[\s\S]*?\n {2}}\n/);
  assert.ok(fn2, 'v5.js has a countsFrom');
  const countsFrom = new Function(fn2[0] + '; return countsFrom;')();
  assert.deepStrictEqual(countsFrom({ counts: { posts: 12, comments: 8 } }, { counts: { posts: 3 } }), { posts: 12, comments: 8, 'with-comments': 3 });
  assert.deepStrictEqual(countsFrom({ counts: { posts: 500, comments: 8 }, counts_capped: ['posts'] }, { counts: { posts: 3 } }), { comments: 8, 'with-comments': 3 });
  assert.deepStrictEqual(countsFrom(null, {}), {});
});
