// Tests for the SPA's HTML-escaping and href-scheme helpers (www/app.js).
// Run with:
//   node --test webapp/internal/webui/escape.test.js
//
// Lives one level ABOVE www/ so `//go:embed www` does not ship or serve it,
// matching pql.test.js.
//
// Why this file exists: escapeHtml escaped < > and & but not the quote
// characters, and five call sites interpolated its output straight into an
// href="…" with no scheme check. A value carrying a double quote could open a
// new HTML attribute, and the admin SPA's CSP sets script-src-attr
// 'unsafe-inline' (internal/server/middleware.go), so an injected inline
// handler would have run.

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const appPath = path.join(__dirname, 'www', 'app.js');
const appSrc = fs.readFileSync(appPath, 'utf8');

// app.js is a classic browser script: it declares `const App = {…}`, registers
// a DOMContentLoaded listener and ends with `window.App = App`. Give it just
// enough of a window/document to evaluate.
globalThis.window = {};
globalThis.document = {
    addEventListener() {},
    getElementById() { return null; },
};
(0, eval)(appSrc); // indirect eval → runs in global scope
const App = globalThis.window.App;

test('App loaded', () => {
    assert.ok(App, 'window.App was not set by app.js');
    assert.strictEqual(typeof App.escapeHtml, 'function');
});

// --- AC #1 -----------------------------------------------------------------

test('escapeHtml escapes both quote characters as well as < > &', () => {
    const out = App.escapeHtml(`a"b'c<d>e&f`);
    assert.ok(!out.includes('"'), `raw double quote survived: ${out}`);
    assert.ok(!out.includes("'"), `raw apostrophe survived: ${out}`);
    assert.ok(!out.includes('<'), `raw < survived: ${out}`);
    assert.ok(!out.includes('>'), `raw > survived: ${out}`);
    // & must be escaped first, or the other entities get double-escaped.
    assert.strictEqual(App.escapeHtml('&'), '&amp;');
    assert.strictEqual(App.escapeHtml('&lt;'), '&amp;lt;');
});

test('escapeHtml still handles the empty and nullish cases', () => {
    assert.strictEqual(App.escapeHtml(''), '');
    assert.strictEqual(App.escapeHtml(null), '');
    assert.strictEqual(App.escapeHtml(undefined), '');
});

test('app.js defines escapeHtml exactly once', () => {
    // Both copies were properties of the same `const App = {…}` literal, so the
    // later one silently won and the earlier one was dead. Fixing only the dead
    // one would have fixed nothing.
    const defs = appSrc.match(/^\s*escapeHtml\s*\(/gm) || [];
    assert.strictEqual(defs.length, 1,
        `expected one escapeHtml definition, found ${defs.length}`);
});

// --- AC #2 -----------------------------------------------------------------

test('safeHref accepts http and https', () => {
    assert.strictEqual(App.safeHref('https://alice.polis.pub/posts/x.html'),
        'https://alice.polis.pub/posts/x.html');
    assert.strictEqual(App.safeHref('http://example.com/'), 'http://example.com/');
});

test('safeHref rejects every non-http(s) scheme and returns empty', () => {
    const hostile = [
        'javascript:alert(1)',
        'JavaScript:alert(1)',
        '  javascript:alert(1)',
        'java\tscript:alert(1)',
        'data:text/html;base64,PHNjcmlwdD4=',
        'vbscript:msgbox(1)',
        'file:///etc/passwd',
        '//evil.example.com/x',       // scheme-relative: inherits the page scheme
        '/posts/x.html',              // relative: no scheme to check
        'posts/x.html',
        '',
        null,
        undefined,
    ];
    for (const u of hostile) {
        assert.strictEqual(App.safeHref(u), '',
            `safeHref should have rejected ${JSON.stringify(u)}`);
    }
});

// --- AC #4 -----------------------------------------------------------------

test('a double quote in a URL cannot open a new HTML attribute', () => {
    // The shape used at the intent.target call site. intent.target comes
    // straight from window.location.search, so it is attacker-controlled.
    const target = 'https://ok.example/x" onfocus="alert(1)" autofocus x="';
    const href = App.safeHref(target);
    const anchor = href
        ? `<a href="${App.escapeHtml(href)}" class="secondary" target="_blank">Back to the post</a>`
        : '';

    // Parse attributes the way a browser does: name="value", where the value
    // runs to the next RAW double quote. An escaped &quot; inside the value
    // does not terminate it, so injected text stays data. Matching on a bare
    // /\s(\w+)=/ instead would report the escaped payload as an attribute and
    // fail a correct implementation.
    const attrs = [...anchor.matchAll(/([a-zA-Z-]+)="([^"]*)"/g)].map(m => m[1]);
    assert.deepStrictEqual(attrs, ['href', 'class', 'target'],
        `unexpected attribute set in: ${anchor}`);

    const hrefValue = anchor.match(/href="([^"]*)"/)[1];
    assert.ok(!hrefValue.includes('"'), 'raw quote inside the href value');
    assert.ok(hrefValue.includes('&quot;'), 'the hostile quotes should be escaped, not dropped');
});

test('a javascript: URL never reaches a rendered href', () => {
    const anchor = App.safeHref('javascript:alert(1)')
        ? `<a href="${App.escapeHtml(App.safeHref('javascript:alert(1)'))}">x</a>`
        : '';
    assert.strictEqual(anchor, '', 'a javascript: URL produced an anchor');
});

// --- AC #3 -----------------------------------------------------------------

test('no escapeHtml output sits inside an inline event-handler attribute', () => {
    // An HTML escaper cannot make that context safe. The HTML parser decodes
    // entities in an attribute value BEFORE the JavaScript inside it is
    // parsed, so an escaped apostrophe (&#39;) becomes a literal apostrophe in
    // the JS source and closes the string. Data belongs in a data- attribute
    // read back through dataset, where it never becomes JS source.
    //
    // Inline handlers that interpolate nothing (onclick="App.downloadSite()")
    // are fine and stay allowed — the defect is data crossing into JS source.
    const offenders = [];
    appSrc.split('\n').forEach((line, i) => {
        const handlers = line.match(/\son[a-z]+="[^"]*"/g) || [];
        for (const h of handlers) {
            if (h.includes('${')) {
                offenders.push(`${i + 1}: ${h.trim()}`);
            }
        }
    });
    assert.deepStrictEqual(offenders, [],
        `data interpolated into an inline event handler:\n${offenders.join('\n')}`);
});

test('every interpolated href in app.js is fed by safeHref', () => {
    // A guard against a new call site added later without the scheme check.
    //
    // Heuristic, and deliberately so: it looks for safeHref on the same line or
    // in the few lines above, because a call site may hoist the checked URL
    // into a local first (updateDomainDisplay does). It will not catch a value
    // laundered through a distant variable. It is a tripwire for the common
    // mistake, not a proof.
    const lines = appSrc.split('\n');
    const WINDOW = 6;
    const offenders = [];
    lines.forEach((line, i) => {
        if (!/href="\$\{/.test(line)) return;
        const context = lines.slice(Math.max(0, i - WINDOW), i + 1).join('\n');
        if (!context.includes('safeHref')) {
            offenders.push(`${i + 1}: ${line.trim()}`);
        }
    });
    assert.deepStrictEqual(offenders, [],
        `href interpolations not routed through safeHref:\n${offenders.join('\n')}`);
});
