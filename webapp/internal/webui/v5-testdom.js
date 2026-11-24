// A DOM for the tests that run the vendored editor (v5-rich.test.js,
// v5-sheet.test.js): jsdom, found through require('jsdom') or the editor
// round-trip harness's pinned install. Without one, skip says why and the
// tests that need it are skipped. Not a test file itself.

'use strict';
const path = require('node:path');
const fs = require('node:fs');
const { createRequire } = require('node:module');
const { pathToFileURL } = require('node:url');

const vendorDir = path.join(__dirname, 'www/vendor');
const kitFile = fs.readdirSync(vendorDir).find((f) => /^milkdown-kit-\d+\.\d+\.\d+\.js$/.test(f));
const kitURL = kitFile ? pathToFileURL(path.join(vendorDir, kitFile)).href : '';

function findJSDOM() {
    const tries = [() => require('jsdom'),
        () => createRequire(path.join(__dirname, '../../../scripts/editor-roundtrip/package.json'))('jsdom')];
    for (const t of tries) { try { return t().JSDOM; } catch { /* next */ } }
    return null;
}
const JSDOM = findJSDOM();
const skip = JSDOM ? false : 'no jsdom here (npm ci in the editor round-trip harness, or install jsdom)';

let dom = null;
// install puts one jsdom window's globals on node's, once, and answers it.
function install() {
    if (dom) return dom.window;
    dom = new JSDOM('<!doctype html><html><head></head><body></body></html>', { pretendToBeVisual: true, url: 'http://localhost/' });
    const g = globalThis, w = dom.window;
    for (const k of ['window', 'document', 'Node', 'Element', 'HTMLElement', 'Text', 'DocumentFragment', 'MutationObserver',
        'getComputedStyle', 'requestAnimationFrame', 'cancelAnimationFrame', 'DOMParser', 'Range', 'Selection', 'ShadowRoot',
        'customElements', 'DataTransfer', 'ClipboardEvent', 'InputEvent', 'KeyboardEvent', 'MouseEvent', 'FocusEvent']) {
        if (w[k] !== undefined && g[k] === undefined) Object.defineProperty(g, k, { value: w[k], configurable: true, writable: true });
    }
    // Node has Event classes of its own; jsdom's EventTarget takes only jsdom's.
    for (const k of ['Event', 'CustomEvent', 'EventTarget']) Object.defineProperty(g, k, { value: w[k], configurable: true, writable: true });
    for (const k of ['addEventListener', 'removeEventListener', 'dispatchEvent']) g[k] = w[k].bind(w);
    try { Object.defineProperty(g, 'navigator', { value: w.navigator, configurable: true }); } catch { /* node's stays */ }
    // jsdom's storage, in place of node's own (which warns when unconfigured).
    try { Object.defineProperty(g, 'localStorage', { value: w.localStorage, configurable: true, writable: true }); } catch { /* node's stays */ }
    const zero = () => ({ x: 0, y: 0, top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0 });
    w.Range.prototype.getBoundingClientRect = zero;
    w.Range.prototype.getClientRects = () => [];
    w.Element.prototype.scrollIntoView = () => {};
    w.document.elementFromPoint = () => null;
    return w;
}

let kit = null;
// loadKit installs the DOM and imports the vendored editor, once.
async function loadKit() {
    if (kit) return kit;
    install();
    kit = await import(kitURL);
    return kit;
}

module.exports = { JSDOM, skip, install, loadKit, kitFile, kitURL, vendorDir };
