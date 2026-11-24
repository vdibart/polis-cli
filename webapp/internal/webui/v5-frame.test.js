// The v5 owner app's frame: its rules, tested without a page. Run with:
//   node --test webapp/internal/webui/v5-frame.test.js

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');

const www = path.join(__dirname, 'www/v5');
const { model } = require(path.join(www, 'frame.js'));
const api = require(path.join(www, 'api.js'));
const help = require(path.join(www, 'help.js'));

test('an address names its section, its view and its enhance group (spec 3.9)', () => {
    assert.deepStrictEqual(model.route('/_/'), { home: true });
    assert.deepStrictEqual(model.route('/_'), { home: true });
    assert.deepStrictEqual(model.route('/_/write'), { section: 'write' });
    assert.deepStrictEqual(model.route('/_/gather/pql/all+posts+from+my+network+by+date'),
        { section: 'gather', sentence: 'all posts from my network by date' });
    assert.deepStrictEqual(model.route('/_/write/pql/all+comments+on+https:%2F%2Fa.example%2Fposts%2Fx.md+to+bless'),
        { section: 'write', sentence: 'all comments on https://a.example/posts/x.md to bless' });
    assert.deepStrictEqual(model.route('/_/enhance/data'), { section: 'enhance', group: 'data' });
    assert.deepStrictEqual(model.route('/_/nowhere'), { home: true });
});

test('a view is addressed by its sentence, and an address reads back as the same view', () => {
    const s = 'all comments on https://a.example/posts/20261110/x.md to bless';
    const a = model.address('write', { sentence: s });
    assert.strictEqual(a, '/_/write/pql/all+comments+on+https%3A%2F%2Fa.example%2Fposts%2F20261110%2Fx.md+to+bless');
    assert.deepStrictEqual(model.route(a), { section: 'write', sentence: s });
    assert.strictEqual(model.address('gather'), '/_/gather');
    assert.strictEqual(model.address('enhance', { group: 'you' }), '/_/enhance/you');
});

test('/_/ opens the section the owner was in last, else gather (3.9)', () => {
    assert.strictEqual(model.lastSection({}), 'gather');
    assert.strictEqual(model.lastSection(null), 'gather');
    assert.strictEqual(model.lastSection({ write: '2026-11-17T10:00:00Z', message: '2026-11-17T12:00:00Z', gather: '2026-11-16T23:00:00Z' }), 'message');
    assert.strictEqual(model.lastSection({ settings: '2026-11-18T00:00:00Z' }), 'gather', 'a name that is not a section is ignored');
});

test('an old notification link\'s fragment goes to its v5 place', () => {
    assert.strictEqual(model.legacyHash('#blessings'), '/_/write/pql/all+comments+from+all+polis+to+bless');
    assert.strictEqual(model.legacyHash('#followers'), '/_/gather/pql/all+activity+about+me');
    assert.strictEqual(model.legacyHash('#/posts/20260101/a.html'), '');
});

test('the current section leads the menu, the rest keep their order, help is last (1.3)', () => {
    assert.deepStrictEqual(model.menuOrder('message'), ['message', 'gather', 'write', 'enhance', 'help']);
    assert.deepStrictEqual(model.menuOrder('write'), ['write', 'gather', 'message', 'enhance', 'help']);
    assert.deepStrictEqual(model.menuOrder(''), ['gather', 'write', 'message', 'enhance', 'help']);
});

test('a mark takes the site\'s avatar, and only colours reach its style', () => {
    const b64 = (s) => Buffer.from(s).toString('base64');
    const st = model.avatarStyle({ bg: '#397f84', fg: '#ffffff', border: '#4ca85d', border_w: 1, pattern: 'dots', pattern_color: '#71bcc2' }, b64);
    assert.match(st, /^background:#397f84;color:#ffffff;box-shadow:0 0 0 1px #4ca85d;background-image:url\(data:image\/svg\+xml;base64,/);
    assert.strictEqual(model.avatarStyle({ bg: 'red;position:fixed' }, b64), '', 'a non-colour background keeps the default');
    assert.doesNotMatch(model.avatarStyle({ bg: '#123', fg: 'url(x)', pattern: 'dots', pattern_color: '#fff\');x' }, b64), /url\(x\)|x'/);
    assert.strictEqual(model.avatarStyle(null, b64), '');
    assert.match(model.avatarStyle({ bg: 'hsl(280, 35%, 55%)', fg: '#ffffff' }, b64), /^background:hsl\(280, 35%, 55%\);/, 'the colour the site gives someone with no avatar of their own');
    assert.strictEqual(model.avatarStyle({ bg: 'hsl(280, 35%, 55%);x:y' }, b64), '', 'only an hsl() of plain numbers');
    assert.strictEqual(model.initial('vdibart'), 'v');
    assert.strictEqual(model.initial(''), '·');
});

test('each section but enhance has a menu dot, asked without clearing anything (1.6)', () => {
    assert.deepStrictEqual(Object.keys(model.DOT_SENTENCES).sort(), ['enhance', 'gather', 'message', 'write']);
    assert.deepStrictEqual(model.DOT_SENTENCES.enhance, []);
    assert.ok(model.DOT_SENTENCES.write.includes('all comments from all polis to bless'));
    // Messages only in the scope the site takes for them: "all polis" is a 400.
    assert.deepStrictEqual(model.DOT_SENTENCES.message, ['all messages from my mutuals']);
    // Asked again while the app is open: that asking keeps the network synced.
    assert.ok(model.DOT_EVERY > 0 && model.DOT_EVERY <= 2 * 60 * 1000, 'within the site\'s two-minute stale window');
});

test('a link to a place in the app opens in place, its fragment kept; anything else is the browser\'s', () => {
    const to = model.inAppLink('/_/enhance/agents#connections');
    assert.deepStrictEqual(to.route, { section: 'enhance', group: 'agents' });
    assert.strictEqual(to.hash, '#connections');
    assert.deepStrictEqual(model.inAppLink('/_/gather/pql/all+activity+about+me').route, { section: 'gather', sentence: 'all activity about me' });
    assert.deepStrictEqual(model.inAppLink('/_/').route, { home: true });
    assert.strictEqual(model.inAppLink('/posts/20261119/hello.html'), null, 'the site\'s own pages are not the app');
    assert.strictEqual(model.inAppLink('https://maren.polis.pub/'), null);
    assert.strictEqual(model.inAppLink('/_foo'), null);
    assert.strictEqual(model.inAppLink('/_/write', { modified: true }), null, 'a new tab asked for with a key');
    assert.strictEqual(model.inAppLink('/_/write', { target: '_blank' }), null);
    assert.strictEqual(model.inAppLink('/_/write', { download: true }), null);
    assert.ok(model.inAppLink('/_/write', { target: '_self' }));
});

test('the API is asked by /v1/query, the sentence and its options encoded', () => {
    assert.strictEqual(api.queryPath('all posts from me', { new_for: 'write', only_new: 1, limit: '', cursor: null }),
        '/v1/query?pql=all%20posts%20from%20me&new_for=write&only_new=1');
    assert.strictEqual(api.errorText(401, null), 'You are signed out. Sign in again to carry on.');
    assert.strictEqual(api.errorText(400, { error: { message: 'bad' } }), 'bad');
});

test('every section has help: a lead, its controls, and related topics, never "ask kay" (1.10, 3.2)', () => {
    for (const s of ['write', 'gather', 'message', 'enhance']) {
        const h = help[s];
        assert.ok(h && h.lead && h.els.length > 0, s + ' has no help');
        assert.ok(Array.isArray(h.topics), s + ' has no related topics');
        assert.doesNotMatch(JSON.stringify(h), /ask kay|\d\d-[a-z]+\.html/, s + "'s help names a mockup or 'ask kay'");
    }
    assert.match(help.write.els[0][0], /Write something new/);
    assert.match(help.message.els[0][0], /Start a conversation/);
});

// The menu is fitted beside the section's toolbar, which a section builds when it mounts and fills
// when its counts arrive. Fitted only before that (and on a resize), a phone kept the filters
// drawn over the menu. No page here to measure, so this holds the two refits in place.
test('the menu is refitted after a section mounts and whenever its toolbar changes width', () => {
    const src = require('node:fs').readFileSync(path.join(www, 'frame.js'), 'utf8');
    const go = src.slice(src.indexOf('function go(sec'), src.indexOf('function hostOf'));
    assert.ok(go.indexOf('fitWays(') > go.indexOf('s.show(opts, ctx)'), 'go() refits after the section shows');
    assert.match(src, /new root\.ResizeObserver\([\s\S]*?fitWays\([\s\S]*?\.observe\(center\)/, 'the toolbar is watched for width');
    assert.match(src, /function boxLeft\(\)[^\n]*\$\('me'\)/, 'the box starts clear of the owner\'s mark');
});

test('clicking the section you are in goes back to its top, unless a sheet is open (1.13)', () => {
    assert.strictEqual(model.menuClick('gather', 'gather', false, false), 'top');
    assert.strictEqual(model.menuClick('write', 'write', false, false), 'top');
    assert.strictEqual(model.menuClick('write', 'write', false, true), 'stay', 'an open sheet keeps its place');
    assert.strictEqual(model.menuClick('gather', 'gather', true, false), 'again', 'a lit dot still takes you to what arrived');
    assert.strictEqual(model.menuClick('write', 'gather', false, false), 'go');
    assert.strictEqual(model.menuClick('help', 'gather', false, false), 'help');
});

// A tab left open after the sign-in lapsed asked /v1/query five times a minute
// all night, every answer a refused request logged as a security event. The
// app is told once when it is signed out, and the dots stop asking.
test('a 401 tells the app once that it is signed out, and a success clears it', async () => {
    const saved = globalThis.fetch;
    let status = 401;
    globalThis.fetch = async () => ({ ok: status < 300, status, json: async () => ({}) });
    try {
        let told = 0;
        api.onSignedOut(() => { told++; });
        await api.query('all posts from me');
        await api.query('all posts from me');
        assert.strictEqual(api.isSignedOut(), true);
        assert.strictEqual(told, 1, 'told once, not on every refusal');
        status = 200;
        await api.query('all posts from me');
        assert.strictEqual(api.isSignedOut(), false, 'signed in again');
    } finally { globalThis.fetch = saved; }
    const src = require('node:fs').readFileSync(path.join(www, 'frame.js'), 'utf8');
    assert.match(src, /api\.onSignedOut\(function \(\) \{\s*root\.clearInterval\(dotTimer\)/, 'signed out, the dots stop');
    assert.match(src, /api\.isSignedOut\(\)\) return;/, 'a round of dots stops at the first refusal');
});

// The footer (3.2, decided 2026-11-23): terms and privacy are polis.pub's, so
// they appear only on polis.pub; "how it works" opens kay's orientation, whose
// developer link is the GitHub docs.
test('the footer links to polis.pub terms and privacy on polis.pub only', () => {
  assert.strictEqual(model.footOperatorLinks({ hasOperator: true, hasPublicApi: true }), true);
  assert.strictEqual(model.footOperatorLinks({ hasPublicApi: true }), false);
  assert.strictEqual(model.footOperatorLinks({}), false);
  assert.strictEqual(model.footOperatorLinks(undefined), false);
});

test('how it works is kay orienting someone to polis, with the docs inside it', () => {
  const h = help.polis;
  assert.ok(h && h.lead && h.els.length >= 4 && h.topics.length >= 1);
  assert.match(h['else'], /href="https:\/\/github\.com\/vdibart\/polis-cli\/blob\/main\/docs\/README\.md"/);
  for (const s of ['gather', 'write', 'message', 'enhance']) {
    assert.ok(h.els.some((e) => e[0].includes('/_/' + s)), s + ' is introduced');
  }
});
