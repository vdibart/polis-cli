// message in the v5 owner app: its rules (the list, the filters, who seals,
// the picker). Run with:
//   node --test webapp/internal/webui/v5-message.test.js

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');

const m = require(path.join(__dirname, 'www/v5/message.js'));

test('every conversation is spec 2.3\'s sentence', () => {
    assert.strictEqual(m.LIST, 'all messages from my mutuals');
});

test('a sentence naming one person opens that conversation (3.9)', () => {
    assert.strictEqual(m.conversationSite('all messages from maren.polis.pub'), 'maren.polis.pub');
    assert.strictEqual(m.conversationSite('All messages from https://Okonkwo.dev/'), 'okonkwo.dev');
    assert.strictEqual(m.conversationSite('all messages from my mutuals'), '');
    assert.strictEqual(m.conversationSite('all messages from all polis'), '');
    assert.strictEqual(m.address('maren.polis.pub'), '/_/message/pql/all+messages+from+maren.polis.pub');
});

test('the filters: conversations, narrowed while unread is picked, and the unread ones (1.4)', () => {
    const convos = [{ unread: 2 }, { unread: 0 }, { unread: 1 }];
    assert.deepStrictEqual(m.counts(convos, false), { conversations: 3, unread: 2 });
    assert.deepStrictEqual(m.counts(convos, true), { conversations: 2, unread: 2 });
    assert.strictEqual(m.label('conversations', 1), '1 conversation');
    assert.strictEqual(m.label('conversations', 3), '3 conversations');
    assert.strictEqual(m.label('unread', 2), '2 unread');
});

test('the owner\'s own messages, and the latest that can be read', () => {
    assert.ok(m.mine({ from: 'me' }, 'me.example'));
    assert.ok(m.mine({ from: 'Me.Example' }, 'me.example'));
    assert.ok(!m.mine({ from: 'ana.example' }, 'me.example'));
    const msgs = [{ content: 'a' }, { content: 'b' }, { content: '', locked: true }];
    assert.strictEqual(m.latest(msgs).content, 'b');
    const opened = [{ content: 'a' }, { content: '', locked: true, text: 'opened here' }];
    assert.strictEqual(m.words(m.latest(opened)), 'opened here', 'a sealed message opened in this tab reads as its words');
    assert.strictEqual(m.latest([]), null);
});

test('sealing follows the messages key: the site\'s with no password, the browser\'s under one (2.3)', () => {
    assert.strictEqual(m.sealing(null), 'site', 'no keyring yet: the site makes one');
    assert.strictEqual(m.sealing({ current: 0, epochs: [{ id: 0, kind: 'bootstrap' }] }), 'site');
    assert.strictEqual(m.sealing({ current: 1, epochs: [{ id: 0, kind: 'bootstrap' }, { id: 1, kind: 'password' }] }), 'browser');
});

test('the picker: who you follow each other with, then one-way follows, as 3.5 labels them', () => {
    const g = m.picker(['maren.polis.pub', 'samirh.polis.pub'], [
        { site: 'maren.polis.pub', relationship: 'mutual' },
        { site: 'lena-k.polis.pub', relationship: 'follows-you' },
        { site: 'tobiasw.net', relationship: 'you-follow' },
    ], { 'samirh.polis.pub': true }, '');
    assert.deepStrictEqual(g.mutual, [
        { site: 'maren.polis.pub', note: '', action: 'Message' },
        { site: 'samirh.polis.pub', note: 'conversation open', action: 'Open' },
    ]);
    assert.deepStrictEqual(g.oneWay, [
        { site: 'lena-k.polis.pub', note: 'follows you', action: 'Follow back' },
        { site: 'tobiasw.net', note: 'you follow them; they don’t follow you yet', action: '' },
    ]);
    const narrowed = m.picker(['maren.polis.pub', 'samirh.polis.pub'], [], {}, 'SAM');
    assert.deepStrictEqual(narrowed.mutual.map((r) => r.site), ['samirh.polis.pub']);
});

test('someone you follow each other with who takes no messages from you says why, with no button (dm.recipients)', () => {
    const g = m.picker(['maren.polis.pub'], [], {}, '', { 'maren.polis.pub': 'Does not accept DMs from you' });
    assert.deepStrictEqual(g.mutual, [{ site: 'maren.polis.pub', note: 'Does not accept DMs from you', action: '' }]);
});
