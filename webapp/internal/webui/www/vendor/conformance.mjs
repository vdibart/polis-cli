// conformance.mjs — phase-4 Go↔WASM conformance gate.
//
// Loads the VENDORED browser bundles + ../dm-crypto.js and asserts they reproduce the
// Go-generated golden vectors in cli-go/pkg/dm/testdata/cross-impl-vectors.json byte-for-
// byte. A wrapped blob from Go MUST unwrap in the browser stack and vice-versa; this gates
// every other phase-4 task. Run: `node webapp/internal/webui/www/vendor/conformance.mjs`
// (Node 20+: WebCrypto + atob/btoa are globals). Exits non-zero on any mismatch.
//
// The INPUTS below are the public test values from cli-go/pkg/dm/vectors_test.go; the
// committed JSON holds the Go OUTPUTS. Keep the two in sync.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url)); // .../www/vendor
const www = join(here, '..');
const repo = join(here, '../../../../..');
const vectors = JSON.parse(readFileSync(join(repo, 'cli-go/pkg/dm/testdata/cross-impl-vectors.json'), 'utf8'));

// Load the vendored IIFE bundles + dm-crypto.js into the global scope (each assigns
// globalThis.*). Indirect eval runs them at global scope so the assignments take effect.
const loadGlobal = (p) => (0, eval)(readFileSync(p, 'utf8'));
loadGlobal(join(here, 'hash-wasm-argon2.js'));
loadGlobal(join(here, 'tweetnacl.js'));
loadGlobal(join(here, 'bip39.js'));
loadGlobal(join(www, 'dm-crypto.js'));

// tweetnacl bundled for the browser needs an explicit PRNG under Node; the browser supplies
// its own. Only wrap/seal (random nonce) need it.
globalThis.nacl.setPRNG((x, n) => globalThis.crypto.getRandomValues(x.subarray(0, n)));

const C = globalThis.PolisDMCrypto;
const hex = (u8) => [...u8].map((b) => b.toString(16).padStart(2, '0')).join('');
const fromHex = (h) => Uint8Array.from(h.match(/../g).map((b) => parseInt(b, 16)));

// Fixed inputs (mirror cli-go/pkg/dm/vectors_test.go).
const IN = {
  password: 'correct horse battery staple',
  argonKDF: { algo: 'argon2id', salt: 'AAECAwQFBgcICQoLDA0ODw==', t: 3, m: 65536, p: 4 },
  recEntropyHex: '000102030405060708090a0b0c0d0e0f',
  recKDF: { algo: 'hkdf-sha256', salt: 'EBESExQVFhcYGRobHB0eHw==' },
  bip39Phrase: 'abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about',
  dekHex: '000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f',
};

let failures = 0;
const check = (name, ok, detail) => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok ? '' : '  — ' + detail}`);
  if (!ok) failures++;
};

const main = async () => {
  // 1) Argon2id password KEK
  const kek = await C.deriveKEKPassword(IN.password, IN.argonKDF);
  check('argon2id password KEK matches Go', hex(kek) === vectors.argon2id_kek_hex, `got ${hex(kek)}`);

  // 2) HKDF-SHA256 recovery KEK
  const recKEK = await C.deriveKEKRecovery(fromHex(IN.recEntropyHex), IN.recKDF);
  check('hkdf recovery KEK matches Go', hex(recKEK) === vectors.recovery_kek_hex, `got ${hex(recKEK)}`);

  // 3) BIP39 phrase -> entropy (+ round-trip)
  const entropy = C.phraseToEntropy(IN.bip39Phrase);
  check('bip39 phrase -> entropy matches Go', hex(entropy) === vectors.bip39_entropy_hex, `got ${hex(entropy)}`);
  check('bip39 entropy -> phrase round-trips', C.entropyToPhrase(entropy).trim() === IN.bip39Phrase);

  // 4) Unwrap the Go-produced wrapped DEK with the Go-matching KEK (the real interop test).
  const dek = C.unwrapDEK(vectors.wrapped_dek_b64, kek);
  check('unwrap Go-wrapped DEK -> original DEK', hex(dek) === IN.dekHex, `got ${hex(dek)}`);

  // 5) Round-trips within the browser stack (wrap/unwrap, seal/open).
  const rewrapped = C.wrapDEK(dek, kek);
  check('wrapDEK -> unwrapDEK round-trips', hex(C.unwrapDEK(rewrapped, kek)) === IN.dekHex);
  let wrongThrew = false;
  try { C.unwrapDEK(rewrapped, fromHex('11'.repeat(32))); } catch (e) { wrongThrew = e.name === 'ErrWrongKey'; }
  check('unwrapDEK with wrong KEK throws ErrWrongKey', wrongThrew);

  const sender = globalThis.nacl.box.keyPair();
  const recipient = globalThis.nacl.box.keyPair();
  const sealed = C.sealBox('hello over the wire', C.b64encode(recipient.publicKey), sender.secretKey);
  const opened = C.openBox(sealed.ciphertext, sealed.nonce, C.b64encode(sender.publicKey), recipient.secretKey);
  check('sealBox -> openBox round-trips (NaCl box)', opened === 'hello over the wire', `got ${opened}`);

  console.log(failures === 0 ? '\nALL CONFORMANCE VECTORS PASS' : `\n${failures} FAILURE(S)`);
  process.exit(failures === 0 ? 0 : 1);
};
main().catch((e) => { console.error(e); process.exit(1); });
