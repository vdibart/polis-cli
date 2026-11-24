// The sheet's rich text (v5/rich.js, over the vendored Milkdown kit): an
// untouched body keeps its bytes, and an edit changes only its own region.
// Run with:
//   node --test webapp/internal/webui/v5-rich.test.js
//
// The pure rules run everywhere. The round trips run the real vendored module,
// which needs a DOM: jsdom, found through require('jsdom') or the editor
// round-trip harness's pinned install; without one they are skipped, saying so.

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');
const fs = require('node:fs');

const rich = require(path.join(__dirname, 'www/v5/rich.js'));
const testdom = require(path.join(__dirname, 'v5-testdom.js'));
const { vendorDir, kitFile } = testdom;

test('the editor is given the body only: the seams around it go back as they came', () => {
    assert.deepStrictEqual(rich.seams('\n\nHello\n\n'), { lead: '\n\n', core: 'Hello', tail: '\n\n' });
    assert.deepStrictEqual(rich.seams('Hello'), { lead: '', core: 'Hello', tail: '' });
    assert.deepStrictEqual(rich.seams(''), { lead: '', core: '', tail: '' });
    assert.deepStrictEqual(rich.seams('  indented\n'), { lead: '', core: '  indented', tail: '\n' }, 'leading spaces on the first line are the body\'s');
});

test('the serializer takes the post\'s own bullet and rule', () => {
    assert.deepStrictEqual(rich.serializerOptions('- a\n- b\n\n---\n'), { bullet: '-', bulletOther: '*', rule: '-' });
    assert.deepStrictEqual(rich.serializerOptions('* a\n\n___\n'), { bullet: '*', bulletOther: '-', rule: '_' });
    assert.deepStrictEqual(rich.serializerOptions('Just words.'), { bullet: '-', bulletOther: '*', rule: '*' }, 'none to copy: "-" bullets, "***" rules');
    assert.strictEqual(rich.serializerOptions('- [ ] task\n* b\n').bullet, '*', 'a task item is not the bullet to copy');
});

test('a link whose text is its address goes back bare', () => {
    const fallback = () => 'FALLBACK';
    const link = rich.bareLink(fallback);
    const text = (v) => [{ type: 'text', value: v }];
    assert.strictEqual(link({ url: 'https://a.example/x', children: text('https://a.example/x') }), 'https://a.example/x');
    assert.strictEqual(link({ url: 'mailto:me@a.example', children: text('me@a.example') }), 'me@a.example');
    assert.strictEqual(link({ url: 'https://a.example/', children: text('a site') }), 'FALLBACK');
    assert.strictEqual(link({ url: 'https://a.example/', title: 'T', children: text('https://a.example/') }), 'FALLBACK');
});

test('a body never begins with a "---" rule', () => {
    assert.strictEqual(rich.noLeadingDashRule('---\n\nText'), '***\n\nText');
    assert.strictEqual(rich.noLeadingDashRule('Text\n\n---\n'), 'Text\n\n---\n');
});

test('the editor is vendored at a pinned version, with its licences, and loads nothing from elsewhere', () => {
    assert.ok(kitFile, 'www/vendor holds milkdown-kit-<version>.js');
    const src = fs.readFileSync(path.join(vendorDir, kitFile), 'utf8');
    assert.ok(fs.existsSync(path.join(vendorDir, kitFile.replace(/\.js$/, '.LICENSES.txt'))), 'its licence file sits beside it');
    assert.doesNotMatch(src, /\bimport\s*\(|\bfrom\s*["']https?:/, 'one self-contained module: no import of anything else');
    assert.doesNotMatch(src, /(^|[^.\w$])eval\(|new Function\(/, 'no eval (the owner app\'s policy allows none)');
});

// ── The round trips, through the real vendored module ──────────────────────

const { skip } = testdom;
let kit = null;
async function loadKit() { kit = await testdom.loadKit(); return kit; }
async function editorOn(text) {
    const k = await loadKit();
    const el = document.createElement('div');
    document.body.appendChild(el);
    return rich.mount(k, el, text, {});
}
// type inserts words at the start of the first paragraph, as a writer would.
function type(ed, words) {
    const view = ed._view();
    let at = -1;
    view.state.doc.descendants((n, pos) => { if (at < 0 && n.type.name === 'paragraph') { at = pos + 1; return false; } return at < 0; });
    view.dispatch(view.state.tr.insertText(words, at));
}

// Constructs from the editor round-trip fixtures (each kept apart from the
// others by blank lines), and the shapes the serializer was configured for.
const fixtures = {
    'dash bullets and rules': 'Intro.\n\n- one\n- two\n\n---\n\nAfter.\n',
    'bare url': 'See https://example.com now.\n',
    'inline marks and a link': 'A [link](https://example.com "T") and ~~gone~~ text.\n\n![Alt](/a.png)\n',
    'emphasis kinds': 'Some _em_ and __strong__ and `code`.\n\n> quoted\n',
    // ⚠️ These the serializer rewrites as a whole once anything in the post is
    // edited (the round-trip verdict recorded them; none is in a human-written
    // polis post today): a setext heading, "1)" items, a table's delimiter row.
    'normalised by the serializer': 'Title\n=====\n\n* x\n\n1) one\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n',
};

test('a body opened and left alone comes back byte for byte, whatever the serializer would make of it', { skip }, async () => {
    for (const [name, md] of Object.entries(fixtures)) {
        const text = '\n' + md + '\n';
        const ed = await editorOn(text);
        assert.strictEqual(ed.changed(), false, name);
        assert.strictEqual(ed.markdown(), text, name + ': untouched, the exact bytes');
        await ed.destroy();
    }
});

test('an edit changes only its own region, and undoing it is untouched again', { skip }, async () => {
    for (const name of ['dash bullets and rules', 'bare url', 'inline marks and a link', 'emphasis kinds']) {
        const md = fixtures[name];
        const ed = await editorOn(md);
        type(ed, 'EDIT ');
        assert.strictEqual(ed.changed(), true, name);
        const firstPara = md.search(/^(?![-|>#*]|\d[.)] )\S/m);
        const want = md.slice(0, firstPara) + 'EDIT ' + md.slice(firstPara);
        assert.strictEqual(ed.markdown(), want, name + ': the edit and nothing else');
        ed.run('undo');
        assert.strictEqual(ed.changed(), false, name + ': undone');
        assert.strictEqual(ed.markdown(), md, name + ': undone, the exact bytes again');
        await ed.destroy();
    }
});

test('every formatting button does something in rich text, and comes out as markdown', { skip }, async () => {
    const ed = await editorOn('word\n');
    const view = ed._view();
    const sel = (a, b) => view.dispatch(view.state.tr.setSelection(kit.state.TextSelection.create(view.state.doc, a, b)));
    sel(1, 5);
    assert.ok(ed.run('bold'));
    assert.strictEqual(ed.markdown(), '**word**\n');
    assert.ok(ed.active().bold);
    ed.run('bold'); ed.run('italic');
    assert.strictEqual(ed.markdown(), '*word*\n');
    ed.run('italic'); ed.run('code');
    assert.strictEqual(ed.markdown(), '`word`\n');
    ed.run('code');
    ed.run('heading');
    assert.strictEqual(ed.markdown(), '# word\n', 'the first line is the title: "# "');
    assert.ok(ed.active().heading);
    ed.run('body');
    assert.strictEqual(ed.markdown(), 'word\n', 'body text turns a heading back');
    ed.run('quote');
    assert.strictEqual(ed.markdown(), '> word\n');
    ed.run('quote');
    assert.strictEqual(ed.markdown(), 'word\n');
    ed.run('list');
    assert.strictEqual(ed.markdown(), '- word\n');
    ed.run('numbers');
    assert.strictEqual(ed.markdown(), '1. word\n', 'a bulleted list becomes a numbered one in place');
    ed.run('numbers');
    assert.strictEqual(ed.markdown(), 'word\n');
    sel(5, 5);
    ed.run('divider');
    assert.match(ed.markdown(), /^word\n\n\*\*\*\n?$/, 'a divider, "***" when the post has no rule of its own');
    ed.run('undo');
    assert.strictEqual(ed.markdown(), 'word\n');
    ed.run('redo');
    assert.match(ed.markdown(), /\*\*\*/);
    await ed.destroy();

    // Return twice leaves an empty paragraph, which is not written out as "<br />".
    const gap = await editorOn('word\n');
    const vg = gap._view();
    vg.dispatch(vg.state.tr.insert(vg.state.doc.content.size, vg.state.schema.nodes.paragraph.create()));
    assert.strictEqual(gap.changed(), true);
    assert.strictEqual(gap.markdown(), 'word\n');
    await gap.destroy();

    const two = await editorOn('First\n\nSecond\n');
    const v3 = two._view();
    v3.dispatch(v3.state.tr.setSelection(kit.state.TextSelection.create(v3.state.doc, 9, 9)));
    two.run('heading');
    assert.strictEqual(two.markdown(), 'First\n\n## Second\n', 'below the first line a heading is "## "');
    await two.destroy();

    const linked = await rich.mount(kit, document.body.appendChild(document.createElement('div')), 'see this\n', { askLink: () => 'https://a.example/' });
    const v2 = linked._view();
    v2.dispatch(v2.state.tr.setSelection(kit.state.TextSelection.create(v2.state.doc, 5, 9)));
    assert.ok(linked.run('link'));
    assert.strictEqual(linked.markdown(), 'see [this](https://a.example/)\n');
    await linked.destroy();
});
