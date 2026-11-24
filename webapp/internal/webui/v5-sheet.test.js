// The sheet, the v5 owner app's one editor (spec 1.14): its rules. Run with:
//   node --test webapp/internal/webui/v5-sheet.test.js
//
// The rules run everywhere; the sheet and the field in a DOM, with the real
// vendored editor, need jsdom (v5-testdom.js) and are skipped without it.

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');

require(path.join(__dirname, 'www/v5/rich.js'));
const sheet = require(path.join(__dirname, 'www/v5/sheet.js'));
const testdom = require(path.join(__dirname, 'v5-testdom.js'));

test('the publish button names what it does (1.14)', () => {
    assert.strictEqual(sheet.label('new'), 'Publish');
    assert.strictEqual(sheet.label('edit'), 'Republish');
    assert.strictEqual(sheet.label('reply'), 'Publish reply');
});

test('formatting wraps the selection, or marks the line, in markdown', () => {
    assert.deepStrictEqual(sheet.format('a word here', 2, 6, 'bold'), { value: 'a **word** here', a: 4, z: 8 });
    assert.deepStrictEqual(sheet.format('a word here', 2, 6, 'italic'), { value: 'a _word_ here', a: 3, z: 7 });
    assert.deepStrictEqual(sheet.format('one\ntwo', 5, 5, 'quote'), { value: 'one\n> two', a: 7, z: 7 });
    assert.deepStrictEqual(sheet.format('one\ntwo', 4, 7, 'list'), { value: 'one\n- two', a: 6, z: 9 });
    assert.strictEqual(sheet.format('see this', 4, 8, 'link').value, 'see [this](https://)');
    assert.strictEqual(sheet.format('x', 0, 0, 'nope').value, 'x');
});

test('in markdown, every button the rich text has does its markdown', () => {
    assert.deepStrictEqual(sheet.format('a word here', 2, 6, 'code'), { value: 'a `word` here', a: 3, z: 7 });
    assert.strictEqual(sheet.format('Title\nmore', 0, 0, 'heading').value, '# Title\nmore', 'the first line is the title');
    assert.strictEqual(sheet.format('Title\nmore', 7, 7, 'heading').value, 'Title\n## more');
    assert.strictEqual(sheet.format('Title\n## more', 9, 9, 'body').value, 'Title\nmore', 'body text takes the marker off');
    assert.strictEqual(sheet.format('Title\n- more', 9, 9, 'numbers').value, 'Title\n1. more', 'a bullet becomes a number in place');
    assert.strictEqual(sheet.format('Title\n> more', 9, 9, 'list').value, 'Title\n- more');
    const d = sheet.format('one two', 3, 3, 'divider');
    assert.strictEqual(d.value, 'one\n\n---\n\ntwo');
    assert.strictEqual(sheet.format('', 0, 0, 'divider').value, '***\n\n', 'never "---" at the very start, where it reads as frontmatter');
});

test('an edit is republishable only once it differs from what was opened (the dirty flag)', () => {
    const opened = '# A post\n\n- one\n';
    assert.strictEqual(sheet.republishable('edit', opened, opened), false, 'opened and left alone: never sent again, so its bytes and signature stay');
    assert.strictEqual(sheet.republishable('edit', opened + 'more\n', opened), true);
    assert.strictEqual(sheet.republishable('edit', '  ', opened), false, 'no words, nothing to publish');
    assert.strictEqual(sheet.republishable('new', 'Hello', 'Hello'), true, 'a new post or a reply needs only words');
    assert.strictEqual(sheet.republishable('reply', '', ''), false);
});

test('each kind of writing goes to its /v1 operation, never /api/', () => {
    assert.deepStrictEqual(sheet.request('new', '# Hi\n\nthere', {}), { method: 'POST', path: '/v1/content/post', body: { markdown: '# Hi\n\nthere' } });
    // A post written in a draft names it, so the server removes the draft once
    // the post is out (no second call to discard it).
    assert.deepStrictEqual(sheet.request('new', 'x', {}, 'draft-1'), { method: 'POST', path: '/v1/content/post', body: { markdown: 'x', draft_id: 'draft-1' } });
    assert.deepStrictEqual(sheet.request('edit', 'new text', { path: 'content/pub.polis.core/post/20261116/a.md', id: 'sha256:ab' }),
        { method: 'PUT', path: '/v1/content/post/sha256%3Aab', body: { path: 'content/pub.polis.core/post/20261116/a.md', markdown: 'new text' } });
    assert.deepStrictEqual(sheet.request('reply', 'yes', { url: 'https://b.example/comments/x.md', root: 'https://a.example/posts/p.md' }),
        { method: 'POST', path: '/v1/content/comment', body: { in_reply_to: 'https://b.example/comments/x.md', root_post: 'https://a.example/posts/p.md', markdown: 'yes' } });
});

test('only editing a published post offers to unpublish it, through /v1', () => {
    const post = { type: 'post', path: 'content/pub.polis.core/post/20261122/going.md' };
    assert.strictEqual(sheet.canUnpublish('edit', post), true);
    assert.strictEqual(sheet.canUnpublish('new', post), false, 'not while writing something new');
    assert.strictEqual(sheet.canUnpublish('reply', post), false);
    assert.strictEqual(sheet.canUnpublish('edit', { type: 'comment', path: 'content/pub.polis.core/comment/20261122/c.md' }), false, 'a comment is not offered');
    assert.strictEqual(sheet.canUnpublish('edit', null), false);
    assert.deepStrictEqual(sheet.unpublishRequest(post),
        { method: 'POST', path: '/v1/content/post/actions/unpublish', body: { path: post.path } });
});

// ── In a DOM, with the real vendored editor ────────────────────────────────

const { skip } = testdom;
const until = async (ok, what) => {
    for (let i = 0; i < 300; i++) { if (ok()) return; await new Promise((r) => setTimeout(r, 10)); }
    throw new Error('timed out waiting for ' + what);
};
// stage draws a section's toolbar and pane, with gather's search button or
// write's own +, and an API that records what is sent.
function stage(gather, kitAt) {
    testdom.install();
    globalThis.scrollTo = () => {};
    document.head.innerHTML = kitAt ? '<meta name="polis-editor" content="' + kitAt + '">' : '';
    document.body.className = '';
    document.body.innerHTML = '<div class="tbar">' + (gather ? '<button class="sq" type="button"></button><div class="views kinds"></div>' : '') +
        '</div><div class="pane"></div>';
    const calls = [];
    const record = (method) => async (p, body) => { calls.push({ method, path: p, body }); return { ok: true, data: {} }; };
    return { tbar: document.querySelector('.tbar'), pane: document.querySelector('.pane'), calls, api: { post: record('POST'), put: record('PUT') } };
}
const shown = (el) => !el.hidden;

test('gather\'s reply sheet writes in rich text, with write\'s buttons and the markdown switch', { skip }, async () => {
    await testdom.loadKit();
    const { tbar, pane, calls, api } = stage(true, testdom.kitURL);
    const sq = tbar.querySelector('.sq');
    const s = sheet.create(pane, tbar, { api, button: sq, plusLabel: 'search' });
    const fmt = tbar.querySelector('.fmt');
    assert.ok(fmt, 'the buttons are in gather\'s toolbar');
    assert.strictEqual(fmt.previousElementSibling, sq, 'just after the search button, which closes the sheet');
    assert.strictEqual(fmt.id, '', 'write\'s toolbar keeps the one #fmt');
    assert.deepStrictEqual([...fmt.querySelectorAll('button')].map((b) => b.dataset.fmt),
        ['body', 'heading', 'bold', 'italic', 'code', 'link', 'quote', 'list', 'numbers', 'divider', 'undo', 'redo', 'markdown'], 'the write sheet\'s controls, all of them');
    tbar.click();
    assert.strictEqual(s.isOpen(), false, 'gather\'s toolbar never opens the sheet: only a reply does');

    s.open('reply', { url: 'https://b.example/posts/p.md', site: 'b.example', quote: 'A post' }, 'Short *reply*.');
    const rich = pane.querySelector('.body-rich'), area = pane.querySelector('.body-in'), pub = pane.querySelector('.publish');
    await until(() => shown(rich), 'the rich text');
    assert.strictEqual(shown(area), false);
    assert.strictEqual(pub.disabled, false, 'a reply needs only words');
    fmt.querySelector('[data-fmt=quote]').click();

    // The markdown switch: the reply's markdown in the text area, and back.
    fmt.querySelector('[data-fmt=markdown]').click();
    assert.ok(shown(area) && !shown(rich));
    assert.strictEqual(area.value, '> Short *reply*.');
    assert.strictEqual(fmt.querySelector('[data-fmt=markdown]').getAttribute('aria-pressed'), 'true');
    fmt.querySelector('[data-fmt=markdown]').click();
    await until(() => shown(rich), 'the rich text again');

    pub.click();
    await until(() => calls.length, 'the publish');
    assert.deepStrictEqual(calls, [{ method: 'POST', path: '/v1/content/comment', body: { in_reply_to: 'https://b.example/posts/p.md', markdown: '> Short *reply*.' } }]);
});

test('a comment opened in rich text and left alone is never sent again (the dirty flag)', { skip }, async () => {
    await testdom.loadKit();
    const { tbar, pane, calls, api } = stage(false, testdom.kitURL);
    const s = sheet.create(pane, tbar, { api });
    // Constructs the serializer would rewrite, were the text ever re-serialised.
    const opened = 'Title\n=====\n\n* x\n\n1) one\n';
    s.open('edit', { type: 'comment', path: 'content/pub.polis.core/comment/20261123/c.md', id: 'sha256:c' }, opened);
    const pub = pane.querySelector('.publish'), fmt = tbar.querySelector('.fmt');
    await until(() => shown(pane.querySelector('.body-rich')), 'the rich text');
    assert.strictEqual(pub.disabled, true, 'untouched: Republish is off');
    pub.click();
    fmt.querySelector('[data-fmt=divider]').click();
    assert.strictEqual(pub.disabled, false, 'edited: Republish is on');
    fmt.querySelector('[data-fmt=undo]').click();
    assert.strictEqual(pub.disabled, true, 'undone back to what was opened is untouched again');
    s.close();
    await new Promise((r) => setTimeout(r, 20));
    assert.deepStrictEqual(calls, [], 'nothing was sent');
});

test('the field (the bio\'s editor) gives back the exact bytes until an edit, and markdown after one', { skip }, async () => {
    await testdom.loadKit();
    const box = document.createElement('div');
    box.innerHTML = '<div class="fmt"></div><div class="body-rich" hidden></div><textarea></textarea>';
    document.body.appendChild(box);
    const [fmt, rich, area] = box.children;
    const bio = 'I write about * things.\n\nTitle\n=====\n';
    area.value = bio;
    let inputs = 0;
    const f = sheet.field({ doc: document, fmt, rich, body: area, kitAt: testdom.kitURL, onInput: () => inputs++ });
    assert.strictEqual(await f.ensure(), true, 'the editor came up');
    assert.ok(f.rich() && shown(rich) && !shown(area));
    assert.strictEqual(f.text(), bio, 'untouched: the bytes it was given');
    fmt.querySelector('[data-fmt=quote]').click();
    assert.ok(inputs > 0, 'an edit is reported');
    assert.match(f.text(), /^> I write about/);
    fmt.querySelector('[data-fmt=undo]').click();
    assert.strictEqual(f.text(), bio, 'undone: the exact bytes again');
    f.destroy();
    assert.ok(!f.rich() && shown(area), 'destroyed: the text area is back');
    assert.strictEqual(await f.ensure(), false, 'and the editor does not come back');
    box.remove();
});

test('if the editor cannot load, the field is a markdown text area whose buttons write markdown', { skip }, async () => {
    testdom.install();
    const box = document.createElement('div');
    box.innerHTML = '<div class="fmt"></div><div class="body-rich" hidden></div><textarea></textarea>';
    document.body.appendChild(box);
    const [fmt, rich, area] = box.children;
    area.value = 'some words';
    const f = sheet.field({ doc: document, fmt, rich, body: area, kitAt: testdom.kitURL.replace(/\.js$/, '-missing.js') });
    assert.strictEqual(await f.ensure(), false);
    assert.ok(fmt.classList.contains('no-rich'), 'no markdown switch to offer');
    assert.ok(shown(area) && !shown(rich));
    area.setSelectionRange(0, 4);
    fmt.querySelector('[data-fmt=bold]').click();
    assert.strictEqual(f.text(), '**some** words');
    box.remove();
});
