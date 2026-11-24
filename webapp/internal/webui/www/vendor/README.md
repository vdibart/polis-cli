# Vendored browser libraries

Two groups, both **vendored same-origin** (committed here, served by us, never from a CDN): the
messages key's cryptography, and the owner app's rich-text editor ([below](#the-sheets-rich-text-editor-milkdown-kit)).

## Vendored browser crypto (DM end-to-end encryption)

These are the third-party libraries that sit in the **password path** for encrypted DMs.
For password epochs, all key derivation and message decryption happen **in the browser** —
the server never sees the password, the KEK, or a password-epoch DEK
(see `docs/general/security/security-model.md`). These files implement that browser-side crypto.

They are **vendored same-origin** (committed here, served by us, `//go:embed`-ed via
`webapp/internal/webui/assets.go`), not loaded from a CDN. Pinning = the exact committed
build; SRI is not used (it's moot for same-origin assets served like the rest of our JS).

### Pinned builds

| File | Source package | Version | Role | License |
|------|----------------|---------|------|---------|
| `hash-wasm-argon2.js` | [`hash-wasm`](https://github.com/Daninet/hash-wasm) | 4.12.0 | **Argon2id** KEK derivation (WASM) — byte-equal to Go `golang.org/x/crypto/argon2` at t=3/m=64MiB/p=4 | MIT |
| `tweetnacl.js` | [`tweetnacl`](https://github.com/dchest/tweetnacl-js) | 1.0.3 | NaCl **box** (X25519+XSalsa20-Poly1305) + **secretbox** — byte-equal to Go `golang.org/x/crypto/nacl` | Unlicense (public domain) |
| `bip39.js` | [`@scure/bip39`](https://github.com/paulmillr/scure-bip39) + [`@noble/hashes`](https://github.com/paulmillr/noble-hashes) | 1.6.0 / 1.8.0 | Recovery-phrase mnemonic ↔ entropy (BIP39) — matches `github.com/tyler-smith/go-bip39` | MIT |

HKDF-SHA256 (the recovery KEK) uses the browser's native **WebCrypto** — no vendored code.

Each file is a single self-contained IIFE (no runtime bundler) that assigns its API to
`globalThis` (`hashwasm`, `nacl`, `scureBip39`). `../dm-crypto.js` wraps them into the
`PolisDMCrypto` API.

### Why these (and not others)

- **Argon2id must match Go's `p=4`.** `libsodium.js`'s `crypto_pwhash` hard-codes
  parallelism=1 and cannot reproduce our params, so a Go-wrapped blob would never unwrap in
  the browser. `hash-wasm` exposes full `parallelism` and matches Go exactly. **Do not
  substitute PBKDF2** — it is not memory-hard and would gut the password-strength guarantee.
- `'wasm-unsafe-eval'` is added to the **admin-SPA** `script-src` (see `CSPAdminSPA` in
  `internal/server/middleware.go`) so the Argon2id WASM can compile. It permits WASM
  compilation only — not JS `eval()` — and does not widen the script-injection surface.

### Regenerating / bumping a version

Bundled with `esbuild` (browser IIFE) from the npm packages, e.g.:

```sh
printf "import { argon2id } from 'hash-wasm';\nglobalThis.hashwasm = { argon2id };\n" > e-argon.js
npx esbuild e-argon.js --bundle --format=iife --minify --outfile=hash-wasm-argon2.js
# (tweetnacl: import nacl from 'tweetnacl'; @scure/bip39: import the API + english wordlist)
```

**After any bump, run the conformance gate** and only commit if it passes:

```sh
node webapp/internal/webui/www/vendor/conformance.mjs
```

It re-derives every vector in `cli-go/pkg/dm/testdata/cross-impl-vectors.json` through the
vendored bundles + `dm-crypto.js` and fails on any byte mismatch with Go — a version bump
that breaks Go↔browser interop will not pass.

## The sheet's rich-text editor (Milkdown kit)

| File | Source packages | Version | Role | Licence |
|------|-----------------|---------|------|---------|
| `milkdown-kit-7.23.0.js` | [`@milkdown/kit`](https://github.com/Milkdown/milkdown) (commonmark and GFM presets, history, clipboard) and [`mdast-util-to-markdown`](https://github.com/syntax-tree/mdast-util-to-markdown), with their dependencies (ProseMirror, remark, micromark) | 7.23.0 (`mdast-util-to-markdown` 2.2.4) | The owner app's post editor: markdown ↔ rich text | MIT, every package; each one's text is in `milkdown-kit-7.23.0.LICENSES.txt` |

One self-contained **ES module** (it imports nothing), ~473 KB, ~146 KB gzipped. It is not
loaded with the page: the v5 shell names it, with its content hash, in
`<meta name="polis-editor">`, and `v5/sheet.js` imports it the first time the sheet is wanted.
If it cannot load, the sheet stays a markdown text area. `v5/rich.js` is our code around it (what
the editor is given, how it writes markdown back, the toolbar's commands); this file holds none.

It runs under the owner app's policy (`CSPAdminSPA`) with nothing added: no `eval`, no
`new Function`, no other origin. Inline styles it sets fall under the existing
`style-src 'unsafe-inline'`.

**Rebuilding or bumping it.** The build is esbuild 0.25.10 over exactly this entry, from a
lockfile pinning `@milkdown/kit` 7.23.0:

```js
export * as core from '@milkdown/kit/core';
export * as commonmark from '@milkdown/kit/preset/commonmark';
export * as gfm from '@milkdown/kit/preset/gfm';
export * as history from '@milkdown/kit/plugin/history';
export * as clipboard from '@milkdown/kit/plugin/clipboard';
export * as utils from '@milkdown/kit/utils';
export * as state from '@milkdown/kit/prose/state';
export * as commands from '@milkdown/kit/prose/commands';
export * as schemaList from '@milkdown/kit/prose/schema-list';
export { defaultHandlers } from 'mdast-util-to-markdown';
```

with `bundle`, `format: 'esm'`, `minify`, `platform: 'browser'`, `target: 'es2020'`,
`legalComments: 'none'`, `NODE_ENV=production`, and a one-line banner naming the version and
the licence file. The build script also writes the licence file, from every package whose code
is in the bundle, and has a `--check` mode that fails if the committed files differ from a fresh
build. A new version is a new file name (and the shell's `<meta>` changes with it); after a
bump, `node --test webapp/internal/webui/v5-rich.test.js` (an untouched body keeps its bytes, an
edit stays in its region, every button works) must pass, and so must a markdown round trip over
real posts.
