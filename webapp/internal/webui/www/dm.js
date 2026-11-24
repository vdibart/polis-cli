// dm.js — high-level browser DM operations, built on PolisDMCrypto (crypto primitives) +
// PolisDMSession (in-memory DEK cache). This is where the password-epoch flows live; the
// server never sees the password, KEK, DEK, or recovery phrase (see security-model.md).
//
// Load order: vendored libs (tweetnacl, hash-wasm, bip39) → dm-crypto.js → dm-session.js →
// dm.js.
(function () {
  'use strict';

  const C = () => globalThis.PolisDMCrypto;
  const S = () => globalThis.PolisDMSession;

  // Locked Argon2id cost — MUST match the Go defaults (cli-go/pkg/dm/keywrap.go +
  // params_test.go: t=3, m=64MiB, p=4). Recorded per-epoch in keyring.json's kdf block.
  const ARGON = { time: 3, memKiB: 64 * 1024, parallelism: 4 };

  const rand = (n) => globalThis.crypto.getRandomValues(new Uint8Array(n));

  // buildSetPasswordPayload mints the epoch-1 keypair, generates a recovery phrase, derives
  // the password + recovery KEKs over fresh salts, and wraps the DEK under both. Returns
  // the server payload (wrapped blobs + KDF params + epoch pubkey — no secrets), the
  // recovery phrase to display ONCE, and the in-memory DEK for immediate unlock. Pure
  // crypto; no network — so it's unit-testable. The owner app sends the payload to the
  // site's API (www/v5/msgkey.js: /v1/messages/password and /v1/messages/password/change).
  async function buildSetPasswordPayload(password) {
    const kp = globalThis.nacl.box.keyPair(); // publicKey (X25519 pub), secretKey (= DEK)
    const dek = kp.secretKey;

    const entropy = rand(16); // 128-bit → 12-word BIP39
    const recoveryPhrase = C().entropyToPhrase(entropy);

    const kdf = { algo: 'argon2id', salt: C().b64encode(rand(16)), t: ARGON.time, m: ARGON.memKiB, p: ARGON.parallelism };
    const recoveryKdf = { algo: 'hkdf-sha256', salt: C().b64encode(rand(16)) };

    const kek = await C().deriveKEKPassword(password, kdf);
    const recKEK = await C().deriveKEKRecovery(entropy, recoveryKdf);

    const payload = {
      public_key_messages: C().b64encode(kp.publicKey),
      wrapped_dek: C().wrapDEK(dek, kek),
      wrapped_dek_recovery: C().wrapDEK(dek, recKEK),
      kdf,
      recovery_kdf: recoveryKdf,
    };
    return { payload, recoveryPhrase, dek };
  }

  // buildRewrapPasswordPayload re-wraps the CURRENT epoch DEK under a NEW password
  // over a fresh salt (O(1), no new epoch, no pubkey change). The caller
  // must already hold the unlocked DEK (Uint8Array). Returns { wrapped_dek, kdf }.
  async function buildRewrapPasswordPayload(newPassword, dek) {
    const kdf = { algo: 'argon2id', salt: C().b64encode(rand(16)), t: ARGON.time, m: ARGON.memKiB, p: ARGON.parallelism };
    const kek = await C().deriveKEKPassword(newPassword, kdf);
    return { wrapped_dek: C().wrapDEK(dek, kek), kdf };
  }

  // buildRegenRecoveryPayload mints a NEW recovery phrase and re-wraps the CURRENT
  // epoch DEK under it over a fresh salt. Returns the server payload
  // ({ wrapped_dek_recovery, recovery_kdf }) and the new phrase to display once.
  async function buildRegenRecoveryPayload(dek) {
    const entropy = rand(16);
    const recoveryPhrase = C().entropyToPhrase(entropy);
    const recoveryKdf = { algo: 'hkdf-sha256', salt: C().b64encode(rand(16)) };
    const recKEK = await C().deriveKEKRecovery(entropy, recoveryKdf);
    return {
      payload: { wrapped_dek_recovery: C().wrapDEK(dek, recKEK), recovery_kdf: recoveryKdf },
      recoveryPhrase,
    };
  }

  globalThis.PolisDM = {
    buildSetPasswordPayload, buildRewrapPasswordPayload, buildRegenRecoveryPayload,
    ARGON,
  };
})();
