// enhance in the v5 owner app: its rules (which rows each kind of instance
// draws, search, the terms line, Randomise). Run with:
//   node --test webapp/internal/webui/v5-enhance.test.js

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');

const e = require(path.join(__dirname, 'www/v5/enhance.js'));

const LAPTOP = { hasPublicApi: false, hasOperator: false };
const REMOTE = { hasPublicApi: true, hasOperator: false };
const POLIS_PUB = { hasPublicApi: true, hasOperator: true };

test('the groups and their filters, as 3.6 labels them', () => {
    assert.deepStrictEqual(e.GROUPS.map((g) => g.filter), ['you', 'your site', 'people', 'agents', 'your data']);
    assert.deepStrictEqual(e.GROUPS.map((g) => g.title), ['You', 'Your site', 'People', 'Agents', 'Your data']);
});

test('which kind of instance: polis.pub, a remote instance, the owner\'s machine', () => {
    assert.strictEqual(e.instance(POLIS_PUB), 'polis.pub');
    assert.strictEqual(e.instance(REMOTE), 'remote');
    assert.strictEqual(e.instance(LAPTOP), 'local');
    assert.strictEqual(e.instance(undefined), 'local');
});

test('⛔ no row that does nothing: rows waiting on an operation are absent (AC #2)', () => {
    for (const caps of [LAPTOP, REMOTE, POLIS_PUB]) {
        const r = e.rows({ caps, paths: { '/v1/site/download': {} }, account: { email: 'x@example.com' }, mcp: 'https://me.example/mcp' });
        const all = [].concat(...Object.values(r));
        for (const absent of ['passkey', 'leave', 'show-straight-away', 'email-me', 'kay']) {
            assert.ok(!all.includes(absent), `${e.instance(caps)} draws ${absent}`);
        }
        assert.deepStrictEqual(r.people, ['message-password'], 'people: the messages password, and nothing waiting on an operation');
        const withPolicy = e.rows({ caps, paths: { '/v1/site/download': {}, '/v1/site/policy': {} }, mcp: '' });
        assert.deepStrictEqual(withPolicy.people, ['show-straight-away', 'message-password'], 'Show straight away, where site.set_policy is offered');
    }
});

test('a laptop: no sign-in rows, no agent address; the download is the site\'s', () => {
    const r = e.rows({ caps: LAPTOP, paths: { '/v1/site/download': {} }, mcp: '' });
    assert.deepStrictEqual(r.you, ['name', 'avatar']);
    assert.deepStrictEqual(r.agents, ['rosie', 'tokens', 'connections']);
    assert.deepStrictEqual(r.data, ['key', 'directory', 'download']);
});

test('polis.pub: the account\'s email and sign-out; its download is the verified export', () => {
    const r = e.rows({ caps: POLIS_PUB, paths: {}, account: { email: 'x@example.com' }, mcp: '' });
    assert.deepStrictEqual(r.you, ['name', 'avatar', 'email', 'signed-in']);
    assert.ok(r.data.includes('download'), 'the export stands for the download');
    assert.ok(r.agents.includes('agent-address'));
    assert.strictEqual(e.everywhere(POLIS_PUB), false, 'signing out everywhere on polis.pub waits on gap E2');
    assert.strictEqual(e.agentAddress(POLIS_PUB, '', 'https://vdibart.polis.pub/'), 'https://vdibart.polis.pub/mcp');
});

test('a remote instance: signing out here and everywhere; the description\'s agent address', () => {
    const r = e.rows({ caps: REMOTE, paths: { '/v1/site/download': {} }, mcp: 'https://me.example/mcp' });
    assert.ok(r.you.includes('signed-in') && !r.you.includes('email'));
    assert.strictEqual(e.everywhere(REMOTE), true);
    assert.strictEqual(e.agentAddress(REMOTE, 'https://me.example/mcp', 'https://me.example'), 'https://me.example/mcp');
    assert.strictEqual(e.agentAddress(LAPTOP, '', 'http://localhost:8080'), '');
});

test('search finds a setting by its label, words in any order', () => {
    assert.ok(e.matches('Name on your posts', 'posts name'));
    assert.ok(e.matches('Your agent address', 'AGENT'));
    assert.ok(!e.matches('Theme', 'terms'));
    assert.ok(e.matches('anything', ''));
});

test('the terms of use as one line', () => {
    assert.strictEqual(e.termsLine({ stated: false }), 'none stated');
    assert.strictEqual(e.termsLine({ stated: true, terms: { profile: 'pub.polis.license.reserved/1', 'train-ai': 'n', search: 'y', attribution: 'required' } }),
        'Reserved: no AI training · search allowed · attribution required');
});

test('Randomise picks from what each choice token allows, and leaves the scheme and text colours alone', () => {
    const contract = {
        tokens: [
            { name: 'scheme', kind: 'choice', allowed: ['light', 'dark'] },
            { name: 'texture', kind: 'choice', allowed: ['none', 'grain', 'dots'] },
            { name: 'text', kind: 'colour' },
        ],
        fonts: ['Inter', 'Lora'],
    };
    let i = 0;
    const seq = [0.5, 0.9, 0.1, 0.2, 0.3, 0.4, 0.6];
    const t = e.randomTokens(contract, () => seq[i++ % seq.length]);
    assert.ok(['none', 'grain', 'dots'].includes(t.texture));
    assert.ok(!('scheme' in t) && !('text' in t));
    assert.match(t.site, /^#[0-9a-f]{6}$/);
    assert.ok(['Inter', 'Lora'].includes(t['font-content']));
});

test('the avatar\'s Randomise gives a whole avatar in colours', () => {
    const a = e.randomAvatar(() => 0.42);
    for (const k of ['bg', 'fg', 'border', 'pattern_color']) assert.match(a[k], /^#[0-9a-f]{6}$/, k);
    assert.ok(['none', 'rings', 'cross', 'grid', 'dots', 'stripes', 'diamond', 'halves'].includes(a.pattern));
});

test('the key, short', () => {
    assert.strictEqual(e.shortKey('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIq7Lw x'), 'ed25519 · AAAAC3Nz…q7Lw');
    assert.strictEqual(e.shortKey(''), '');
});

test('Randomise sets the site\'s colour against the theme\'s surface: light on a dark one, dark on a light one', () => {
    const lum = (hex) => { const c = [0, 2, 4].map((i) => { const v = parseInt(hex.slice(1 + i, 3 + i), 16) / 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }); return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]; };
    for (let k = 0; k < 20; k++) {
        const onDark = e.randomTokens({ tokens: [], values: { surface: '#1a1520' } }).site;
        const onLight = e.randomTokens({ tokens: [], values: { surface: '#eceae6' } }).site;
        // 3:1 against each surface, the server's check.
        assert.ok((lum(onDark) + 0.05) / (lum('#1a1520') + 0.05) >= 3, onDark + ' on a dark surface');
        assert.ok((lum('#eceae6') + 0.05) / (lum(onLight) + 0.05) >= 3, onLight + ' on a light surface');
    }
});

test('Show straight away reads the public rules as one of three choices, or the owner\'s own (2.4)', () => {
    const P = (rules, priv) => ({ public: rules.map((r) => ({ active: true, policy: r })), private: (priv || []).map((r) => ({ active: true, policy: r })) });
    const def = ['allow pub.polis.dm from following', 'deny pub.polis.dm from all', 'bless pub.polis.comment from self', 'bless pub.polis.comment from following', 'bless pub.polis.comment from thread-blessed', 'review pub.polis.comment from all', 'deny all from all'];
    assert.strictEqual(e.showing(P(def)), 'following', 'the defaults are people you follow');
    const none = e.showingRules(P(def), 'no-one');
    assert.deepStrictEqual(none, ['allow pub.polis.dm from following', 'deny pub.polis.dm from all', 'bless pub.polis.comment from self', 'review pub.polis.comment from all', 'deny all from all'], 'only the comment lines change, in place');
    assert.strictEqual(e.showing(P(none)), 'no-one');
    assert.strictEqual(e.showing(P(e.showingRules(P(def), 'anyone'))), 'anyone');
    assert.deepStrictEqual(e.showingRules(P(none), 'following'), def, 'and back');
    assert.strictEqual(e.showing(P(['bless pub.polis.comment from all at friend.example', ...def])), 'custom', 'rules of the owner\'s own');
    assert.strictEqual(e.showing(P(def, ['review pub.polis.comment from all'])), 'custom', 'the private rules decide comments too');
    assert.strictEqual(e.showing(P(def, ['deny pub.polis.comment from all at spam.example'])), 'following', 'an agent\'s deny is not a choice of its own');
    assert.strictEqual(e.showing(null), 'custom');
});

test('a click anywhere on a closed group opens it; on an open one, only above its rows folds it', () => {
  // closed: the whole card, padding included (a click above the title once did nothing)
  assert.strictEqual(e.togglesGroup(false, false, false, 10, 80), true);
  assert.strictEqual(e.togglesGroup(false, false, false, 300, 80), true);
  // open: the heading band folds it; the rows and the controls never do
  assert.strictEqual(e.togglesGroup(true, false, false, 10, 80), true);
  assert.strictEqual(e.togglesGroup(true, false, false, 120, 80), false);
  assert.strictEqual(e.togglesGroup(true, true, false, 10, 80), false);
  assert.strictEqual(e.togglesGroup(false, false, true, 10, 80), false);
});

test('the agents notice: what an agent may do and how to connect one, only where the site has an agent address', () => {
    const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;');
    const html = e.mcpNote('https://me.polis.pub/mcp', false, '<path/>', esc);
    assert.match(html, /Bring your own AI agent/);
    assert.match(html, /<code>https:\/\/me\.polis\.pub\/mcp<\/code>/);
    assert.match(html, /data-do="copy-addr" data-text="https:\/\/me\.polis\.pub\/mcp"/);
    assert.match(html, /href="\/_\/enhance\/agents#connections"/, 'to the owner\'s agents, in place');
    assert.match(html, /data-do="mcp-hide"/, 'the owner can put it away');
    assert.strictEqual(e.MCP_STEPS.length, 3);
    e.MCP_STEPS.forEach((step) => assert.ok(html.includes(esc(step)), step));
    assert.strictEqual(e.mcpNote('', false, '', esc), '', 'a laptop has no agent address, so no notice');
    assert.strictEqual(e.mcpNote('https://me.polis.pub/mcp', true, '', esc), '', 'put away');
    assert.ok(!e.mcpNote('https://x/"><script>', false, '', esc).includes('"><script>'), 'the address is escaped');
});

test('Make a token offers the description\'s scopes an agent may hold, in its words, and nothing of its own', () => {
    const desc = { 'x-polis-scopes': [
        { name: 'read', sentence: 'Read your drafts', agent: true },
        { name: 'follow', sentence: 'Follow and unfollow sites as you', agent: true },
        { name: 'tag', sentence: 'Tag your posts and comments', agent: false },
        { name: 'policy:write', sentence: 'Tighten your rules', agent: true, warning: 'Only tick this if you want it to change who may reach you.' },
    ] };
    assert.deepStrictEqual(e.tokenScopes(desc), [
        { name: 'read', sentence: 'Read your drafts', warning: '' },
        { name: 'follow', sentence: 'Follow and unfollow sites as you', warning: '' },
        { name: 'policy:write', sentence: 'Tighten your rules', warning: 'Only tick this if you want it to change who may reach you.' },
    ]);
    assert.deepStrictEqual(e.tokenScopes({}), [], 'an older description offers nothing rather than a guess');
    assert.deepStrictEqual(e.tokenScopes(undefined), []);
});
