# Vendored browser crypto (DM end-to-end encryption)

These are the third-party libraries that sit in the **password path** for encrypted DMs.
For password epochs, all key derivation and message decryption happen **in the browser** —
the server never sees the password, the KEK, or a password-epoch DEK
(see `docs/general/security/security-model.md`). These files implement that browser-side crypto.

They are **vendored same-origin** (committed here, served by us, `//go:embed`-ed via
`webapp/internal/webui/assets.go`), not loaded from a CDN. Pinning = the exact committed
build; SRI is not used (it's moot for same-origin assets served like the rest of our JS).

## Pinned builds

| File | Source package | Version | Role | License |
|------|----------------|---------|------|---------|
| `hash-wasm-argon2.js` | [`hash-wasm`](https://github.com/Daninet/hash-wasm) | 4.12.0 | **Argon2id** KEK derivation (WASM) — byte-equal to Go `golang.org/x/crypto/argon2` at t=3/m=64MiB/p=4 | MIT |
| `tweetnacl.js` | [`tweetnacl`](https://github.com/dchest/tweetnacl-js) | 1.0.3 | NaCl **box** (X25519+XSalsa20-Poly1305) + **secretbox** — byte-equal to Go `golang.org/x/crypto/nacl` | Unlicense (public domain) |
| `bip39.js` | [`@scure/bip39`](https://github.com/paulmillr/scure-bip39) + [`@noble/hashes`](https://github.com/paulmillr/noble-hashes) | 1.6.0 / 1.8.0 | Recovery-phrase mnemonic ↔ entropy (BIP39) — matches `github.com/tyler-smith/go-bip39` | MIT |

HKDF-SHA256 (the recovery KEK) uses the browser's native **WebCrypto** — no vendored code.

Each file is a single self-contained IIFE (no runtime bundler) that assigns its API to
`globalThis` (`hashwasm`, `nacl`, `scureBip39`). `../dm-crypto.js` wraps them into the
`PolisDMCrypto` API.

## Why these (and not others)

- **Argon2id must match Go's `p=4`.** `libsodium.js`'s `crypto_pwhash` hard-codes
  parallelism=1 and cannot reproduce our params, so a Go-wrapped blob would never unwrap in
  the browser. `hash-wasm` exposes full `parallelism` and matches Go exactly. **Do not
  substitute PBKDF2** — it is not memory-hard and would gut the password-strength guarantee.
- `'wasm-unsafe-eval'` is added to the **admin-SPA** `script-src` (see `CSPAdminSPA` in
  `internal/server/middleware.go`) so the Argon2id WASM can compile. It permits WASM
  compilation only — not JS `eval()` — and does not widen the script-injection surface.

## Regenerating / bumping a version

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
