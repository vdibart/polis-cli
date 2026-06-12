// dm-crypto.js — browser-side DM crypto for end-to-end encrypted messages.
//
// All password-epoch key derivation and message decryption happen here, in the browser:
// the server never sees the password, the KEK, or a password-epoch DEK (see
// docs/general/security/security-model.md). Every primitive is byte-compatible with the Go side
// (cli-go/pkg/dm) so a blob wrapped on one unwraps on the other — conformance is pinned by
// testdata/cross-impl-vectors.json (run vendor/conformance.mjs).
//
// Depends on three vendored, pinned libraries (see vendor/README.md), loaded before this
// file so they have populated globalThis:
//   - hashwasm.argon2id  (hash-wasm)   — Argon2id KEK, matches golang.org/x/crypto/argon2
//   - nacl               (tweetnacl)   — box (X25519+XSalsa20-Poly1305) + secretbox
//   - scureBip39         (@scure/bip39) — recovery-phrase mnemonic <-> entropy
// plus WebCrypto (globalThis.crypto.subtle) for HKDF-SHA256.
(function () {
  'use strict';

  const RECOVERY_HKDF_INFO = 'polis-dm-recovery-kek-v1'; // must match recoveryHKDFInfo in Go
  const KEK_LEN = 32;
  const NONCE_LEN = 24; // NaCl nonce (box + secretbox)

  const subtle = () => globalThis.crypto.subtle;
  const enc = new TextEncoder();
  const dec = new TextDecoder();

  function b64decode(s) {
    const bin = atob(s);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }
  function b64encode(bytes) {
    let bin = '';
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin);
  }

  // ErrWrongKey is thrown when an AEAD open fails — i.e. the wrong password/phrase. There
  // is no separate verifier; the Poly1305 tag IS the password check (matches Go ErrWrongKey).
  class ErrWrongKey extends Error {
    constructor() { super('wrong password or recovery phrase'); this.name = 'ErrWrongKey'; }
  }

  // deriveKEKPassword runs Argon2id over the password with the epoch's recorded params.
  // kdf: { algo:"argon2id", salt:<base64>, t, m, p }. Returns a 32-byte Uint8Array.
  async function deriveKEKPassword(password, kdf) {
    if (kdf.algo !== 'argon2id') throw new Error('unexpected password KDF: ' + kdf.algo);
    return await globalThis.hashwasm.argon2id({
      password,
      salt: b64decode(kdf.salt),
      iterations: kdf.t,
      memorySize: kdf.m,   // KiB
      parallelism: kdf.p,
      hashLength: KEK_LEN,
      outputType: 'binary',
    });
  }

  // deriveKEKRecovery runs HKDF-SHA256 over the BIP39 entropy. kdf: { algo:"hkdf-sha256",
  // salt:<base64> }. info is fixed. Returns a 32-byte Uint8Array.
  async function deriveKEKRecovery(entropy, kdf) {
    if (kdf.algo !== 'hkdf-sha256') throw new Error('unexpected recovery KDF: ' + kdf.algo);
    const ikm = await subtle().importKey('raw', entropy, 'HKDF', false, ['deriveBits']);
    const bits = await subtle().deriveBits(
      { name: 'HKDF', hash: 'SHA-256', salt: b64decode(kdf.salt), info: enc.encode(RECOVERY_HKDF_INFO) },
      ikm, KEK_LEN * 8);
    return new Uint8Array(bits);
  }

  // unwrapDEK opens a wrapped DEK blob (base64 of nonce‖secretbox-ct) with the KEK.
  // Throws ErrWrongKey if the tag fails (wrong password/phrase).
  function unwrapDEK(wrappedB64, kek) {
    const raw = b64decode(wrappedB64);
    const nonce = raw.slice(0, NONCE_LEN);
    const ct = raw.slice(NONCE_LEN);
    const dek = globalThis.nacl.secretbox.open(ct, nonce, kek);
    if (!dek) throw new ErrWrongKey();
    return dek;
  }

  // wrapDEK seals a DEK under the KEK with a fresh random nonce → base64(nonce‖ct).
  function wrapDEK(dek, kek) {
    const nonce = globalThis.nacl.randomBytes(NONCE_LEN);
    const ct = globalThis.nacl.secretbox(dek, nonce, kek);
    const out = new Uint8Array(NONCE_LEN + ct.length);
    out.set(nonce); out.set(ct, NONCE_LEN);
    return b64encode(out);
  }

  // openBox opens a received message box with the recipient's epoch DEK. ciphertext/nonce/
  // boxPub are base64; dek is a Uint8Array. Returns the plaintext string, or throws
  // ErrWrongKey if the box doesn't open (wrong DEK / tamper).
  function openBox(ciphertextB64, nonceB64, boxPubB64, dek) {
    const pt = globalThis.nacl.box.open(b64decode(ciphertextB64), b64decode(nonceB64), b64decode(boxPubB64), dek);
    if (!pt) throw new ErrWrongKey();
    return dec.decode(pt);
  }

  // sealBox seals plaintext to a recipient messages pubkey from the sender's epoch DEK.
  // Returns { ciphertext, nonce } (both base64).
  function sealBox(plaintext, peerPubB64, myDEK) {
    const nonce = globalThis.nacl.randomBytes(NONCE_LEN);
    const ct = globalThis.nacl.box(enc.encode(plaintext), nonce, b64decode(peerPubB64), myDEK);
    return { ciphertext: b64encode(ct), nonce: b64encode(nonce) };
  }

  // phraseToEntropy validates a BIP39 mnemonic (checksum) and returns its entropy bytes.
  // Throws on an invalid phrase. Matches go-bip39.
  function phraseToEntropy(phrase) {
    const normalized = phrase.trim().replace(/\s+/g, ' ').toLowerCase();
    return globalThis.scureBip39.mnemonicToEntropy(normalized, globalThis.scureBip39.wordlist);
  }

  // entropyToPhrase encodes entropy bytes as a BIP39 mnemonic.
  function entropyToPhrase(entropy) {
    return globalThis.scureBip39.entropyToMnemonic(entropy, globalThis.scureBip39.wordlist);
  }

  globalThis.PolisDMCrypto = {
    deriveKEKPassword, deriveKEKRecovery,
    unwrapDEK, wrapDEK, openBox, sealBox,
    phraseToEntropy, entropyToPhrase,
    b64decode, b64encode, ErrWrongKey,
  };
})();
