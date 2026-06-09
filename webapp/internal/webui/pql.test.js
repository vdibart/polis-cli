// Golden-driven test for the JS PQL parser (www/pql.js). Run with:
//   node --test webapp/internal/webui/pql.test.js
//
// Lives one level ABOVE www/ so `//go:embed www` does not ship or serve it.
// Reads the SAME canonical corpus the Go and TS parsers assert against
// (docs/general/pql-golden.jsonl), so a grammar drift in any one language
// fails its golden test. Uses node's built-in test runner — no dependencies.

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

// www/pql.js is a browser IIFE that attaches to window.PQL. Provide a window
// and evaluate it in global scope so the IIFE can find it.
globalThis.window = {};
const pqlSrc = fs.readFileSync(path.join(__dirname, 'www', 'pql.js'), 'utf8');
(0, eval)(pqlSrc); // indirect eval → runs in global scope
const PQL = globalThis.window.PQL;

// Repo root: webui → internal → webapp → root (3 up).
const goldenPath = path.join(__dirname, '../../../docs/general/pql-golden.jsonl');

test('golden corpus parse + round-trip', () => {
    const lines = fs.readFileSync(goldenPath, 'utf8').split('\n');
    let checked = 0;
    lines.forEach((raw, idx) => {
        const line = raw.trim();
        if (!line) return;
        const gl = JSON.parse(line);
        const got = PQL.parse(gl.sentence, gl.defaultScope || undefined);
        checked++;

        if (gl.expected === null) {
            assert.strictEqual(got, null, `line ${idx + 1} ${JSON.stringify(gl.sentence)}: expected null, got ${JSON.stringify(got)}`);
            return;
        }

        assert.ok(got, `line ${idx + 1} ${JSON.stringify(gl.sentence)}: expected a filter, got null`);
        const wantMod = gl.expected.modifier === undefined ? null : gl.expected.modifier;
        assert.deepStrictEqual(
            { qualifier: got.qualifier, type: got.type, relation: got.relation, scope: got.scope, modifier: got.modifier },
            { qualifier: gl.expected.qualifier, type: gl.expected.type, relation: gl.expected.relation, scope: gl.expected.scope, modifier: wantMod },
            `line ${idx + 1} ${JSON.stringify(gl.sentence)}`
        );

        if (gl.url) {
            const composed = PQL.compose(got, gl.defaultScope || undefined).replace(/ /g, '+');
            assert.strictEqual(composed, gl.url, `line ${idx + 1} ${JSON.stringify(gl.sentence)}: compose round-trip`);
        }
    });
    assert.ok(checked > 0, 'golden corpus was empty');
});

test('parseURL handles both SPA and public path forms', () => {
    assert.ok(PQL.parseURL({ pathname: '/_/pql/all+activity+from+my+network' }).pqlState);
    assert.ok(PQL.parseURL({ pathname: '/pql/all+posts+by+date' }, '@alice.polis.pub').pqlState);
    assert.strictEqual(PQL.parseURL({ pathname: '/settings' }).pqlState, null);
});
