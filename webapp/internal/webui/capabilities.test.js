// Tests for how the SPA (www/app.js) loads the instance's capability
// descriptor. Run with:
//   node --test webapp/internal/webui/capabilities.test.js
//
// Lives one level ABOVE www/ so `//go:embed www` does not ship or serve it,
// matching pql.test.js and escape.test.js.
//
// Why this file exists: the SPA used to treat ANY failed fetch of
// /api/capabilities as a static export. One cold start or platform error on a
// serverless instance then dropped hasOperator and the DM modes for the whole
// page load. Only a 404 or a non-descriptor answer means "no API" now; any
// other failure retries once, then uses the last-known descriptor.

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const appSrc = fs.readFileSync(path.join(__dirname, 'www', 'app.js'), 'utf8');

globalThis.window = {};
globalThis.document = {
    addEventListener() {},
    getElementById() { return null; },
};

// A localStorage that can be told to throw.
let store = new Map();
let storageThrows = false;
globalThis.localStorage = {
    getItem(k) { if (storageThrows) throw new Error('blocked'); return store.has(k) ? store.get(k) : null; },
    setItem(k, v) { if (storageThrows) throw new Error('blocked'); store.set(k, String(v)); },
    removeItem(k) { if (storageThrows) throw new Error('blocked'); store.delete(k); },
};

// fetch answers from a queue: each entry is a response-like object, or an
// Error to throw.
let answers = [];
let calls = 0;
globalThis.fetch = async () => {
    calls++;
    const a = answers.shift();
    if (a instanceof Error) throw a;
    return a;
};
const json = (status, body) => ({ status, ok: status >= 200 && status < 300, json: async () => body });
const html = () => ({ status: 200, ok: true, json: async () => { throw new SyntaxError('Unexpected token <'); } });
const DESCRIPTOR = { v: 'pub.polis.capabilities.v1', hasPublicApi: true, hasOperator: true };

(0, eval)(appSrc);
const App = globalThis.window.App;
App._capabilitiesRetryMs = 0;

function reset() {
    store = new Map();
    storageThrows = false;
    answers = [];
    calls = 0;
    App.capabilities = App._capabilityDefaults;
    App.capabilitiesUnavailable = false;
}

test('a descriptor is applied and remembered', async () => {
    reset();
    answers = [json(200, DESCRIPTOR)];
    assert.strictEqual(await App._loadCapabilities(), 'known');
    assert.strictEqual(App.capabilities.hasOperator, true);
    assert.strictEqual(JSON.parse(store.get('polis-capabilities')).v, DESCRIPTOR.v);
    assert.strictEqual(calls, 1);
});

test('a 404 is a static export: defaults stand, the remembered copy is dropped', async () => {
    reset();
    store.set('polis-capabilities', JSON.stringify(DESCRIPTOR));
    answers = [json(404, null)];
    assert.strictEqual(await App._loadCapabilities(), 'static');
    assert.strictEqual(App.capabilities.hasOperator, false);
    assert.strictEqual(App.capabilitiesUnavailable, false);
    assert.strictEqual(store.has('polis-capabilities'), false);
    assert.strictEqual(calls, 1, 'a static answer is not retried');
});

test('a 200 that is not JSON (a static host fallback) is a static export', async () => {
    reset();
    answers = [html()];
    assert.strictEqual(await App._loadCapabilities(), 'static');
    assert.strictEqual(App.capabilitiesUnavailable, false);
});

test('JSON without a string v is a static export', async () => {
    reset();
    answers = [json(200, { hello: 'world' })];
    assert.strictEqual(await App._loadCapabilities(), 'static');
});

test('a 5xx is retried once, and a retry that succeeds is used', async () => {
    reset();
    answers = [json(503, null), json(200, DESCRIPTOR)];
    assert.strictEqual(await App._loadCapabilities(), 'known');
    assert.strictEqual(App.capabilities.hasOperator, true);
    assert.strictEqual(calls, 2);
});

test('two 5xx answers fall back to the last-known descriptor', async () => {
    reset();
    store.set('polis-capabilities', JSON.stringify(DESCRIPTOR));
    answers = [json(500, null), json(500, null)];
    assert.strictEqual(await App._loadCapabilities(), 'failed');
    assert.strictEqual(App.capabilities.hasOperator, true);
    assert.strictEqual(App.capabilitiesUnavailable, false);
    assert.strictEqual(store.has('polis-capabilities'), true, 'a failure keeps the remembered copy');
});

test('network errors with nothing remembered set capabilitiesUnavailable', async () => {
    reset();
    answers = [new TypeError('Failed to fetch'), new TypeError('Failed to fetch')];
    assert.strictEqual(await App._loadCapabilities(), 'failed');
    assert.strictEqual(App.capabilitiesUnavailable, true);
    assert.strictEqual(App.capabilities.hasOperator, false);
    assert.strictEqual(calls, 2);
});

test('a 403 is a failure, not a static export', async () => {
    reset();
    answers = [json(403, null), json(403, null)];
    assert.strictEqual(await App._loadCapabilities(), 'failed');
    assert.strictEqual(App.capabilitiesUnavailable, true);
});

test('storage that throws never breaks the load', async () => {
    reset();
    storageThrows = true;
    answers = [json(200, DESCRIPTOR)];
    assert.strictEqual(await App._loadCapabilities(), 'known');
    assert.strictEqual(App.capabilities.hasOperator, true);
    reset();
    storageThrows = true;
    answers = [json(500, null), json(500, null)];
    assert.strictEqual(await App._loadCapabilities(), 'failed');
    assert.strictEqual(App.capabilitiesUnavailable, true);
});

test('a remembered value that is not a descriptor is ignored', async () => {
    reset();
    store.set('polis-capabilities', '{"hasOperator":true}');
    answers = [json(500, null), json(500, null)];
    await App._loadCapabilities();
    assert.strictEqual(App.capabilities.hasOperator, false);
    assert.strictEqual(App.capabilitiesUnavailable, true);
});

test('a descriptor merges over the defaults, never over an earlier one', async () => {
    reset();
    answers = [json(200, DESCRIPTOR)];
    await App._loadCapabilities();
    answers = [json(200, { v: DESCRIPTOR.v, hasPublicApi: true })];
    await App._loadCapabilities();
    assert.strictEqual(App.capabilities.hasOperator, false, 'a field the server stopped sending falls back to the safe value');
});

test('the settings error card replaces the settings when capabilities are unavailable', async () => {
    reset();
    App.capabilitiesUnavailable = true;
    const container = { innerHTML: '' };
    await App.renderSettings(container);
    assert.match(container.innerHTML, /Can't reach this site's server/);
    assert.match(container.innerHTML, /App\.retryCapabilities\(\)/);
});
