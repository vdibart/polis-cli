// dm.test.mjs — tests the set-password payload builder (../dm.js): the browser must
// produce wrapped blobs that (a) match the server's expected shape and (b) are recoverable
// by BOTH the password and the recovery phrase. Run: `node .../vendor/dm.test.mjs`.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const www = join(here, '..');
const load = (p) => (0, eval)(readFileSync(p, 'utf8'));
load(join(here, 'hash-wasm-argon2.js'));
load(join(here, 'tweetnacl.js'));
load(join(here, 'bip39.js'));
load(join(www, 'dm-crypto.js'));
load(join(www, 'dm-session.js'));
load(join(www, 'dm.js'));

globalThis.nacl.setPRNG((x, n) => globalThis.crypto.getRandomValues(x.subarray(0, n)));

const C = globalThis.PolisDMCrypto;
const hex = (u8) => [...u8].map((b) => b.toString(16).padStart(2, '0')).join('');

let failures = 0;
const check = (name, ok, detail) => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok ? '' : '  — ' + (detail || '')}`);
  if (!ok) failures++;
};

const main = async () => {
  const password = 'a strong enough passphrase';
  const { payload, recoveryPhrase, dek } = await globalThis.PolisDM.buildSetPasswordPayload(password);

  // Shape the server (AddPasswordEpoch) requires.
  check('pubkey is base64 of 32 bytes', C.b64decode(payload.public_key_messages).length === 32);
  check('kdf is argon2id with locked cost', payload.kdf.algo === 'argon2id' && payload.kdf.t === 3 && payload.kdf.m === 65536 && payload.kdf.p === 4);
  check('recovery_kdf is hkdf-sha256', payload.recovery_kdf.algo === 'hkdf-sha256');
  check('both wraps present', !!payload.wrapped_dek && !!payload.wrapped_dek_recovery);
  check('recovery phrase is 12 words', recoveryPhrase.split(' ').length === 12);
  check('dek is 32 bytes', dek.length === 32);

  // The password recovers the DEK.
  const kek = await C.deriveKEKPassword(password, payload.kdf);
  check('password unwraps to the DEK', hex(C.unwrapDEK(payload.wrapped_dek, kek)) === hex(dek));

  // The recovery phrase ALSO recovers the DEK (independent second wrap).
  const recEntropy = C.phraseToEntropy(recoveryPhrase);
  const recKEK = await C.deriveKEKRecovery(recEntropy, payload.recovery_kdf);
  check('recovery phrase unwraps to the DEK', hex(C.unwrapDEK(payload.wrapped_dek_recovery, recKEK)) === hex(dek));

  // A wrong password fails the AEAD tag.
  let wrong = false;
  try {
    const badKEK = await C.deriveKEKPassword('not the password', payload.kdf);
    C.unwrapDEK(payload.wrapped_dek, badKEK);
  } catch (e) { wrong = e.name === 'ErrWrongKey'; }
  check('wrong password fails (ErrWrongKey)', wrong);

  // Each call uses fresh randomness (distinct keypair + salts).
  const second = await globalThis.PolisDM.buildSetPasswordPayload(password);
  check('fresh keypair + salt per call', second.payload.public_key_messages !== payload.public_key_messages && second.payload.kdf.salt !== payload.kdf.salt);

  // ── Phase 4.8: O(1) re-wrap of the SAME DEK ──────────────────────────────
  // Change password: the new password unwraps the same DEK over a fresh salt.
  const newPassword = 'a different, equally strong passphrase';
  const rewrap = await globalThis.PolisDM.buildRewrapPasswordPayload(newPassword, dek);
  check('rewrap uses a fresh password salt', rewrap.kdf.salt !== payload.kdf.salt);
  const newKEK = await C.deriveKEKPassword(newPassword, rewrap.kdf);
  check('new password unwraps the SAME DEK', hex(C.unwrapDEK(rewrap.wrapped_dek, newKEK)) === hex(dek));
  // The OLD password no longer opens the re-wrapped blob.
  let oldFails = false;
  try { C.unwrapDEK(rewrap.wrapped_dek, kek); } catch (e) { oldFails = e.name === 'ErrWrongKey'; }
  check('old password no longer opens the re-wrap', oldFails);

  // Regenerate recovery: a brand-new phrase unwraps the same DEK.
  const regen = await globalThis.PolisDM.buildRegenRecoveryPayload(dek);
  check('regen yields a new 12-word phrase', regen.recoveryPhrase.split(' ').length === 12 && regen.recoveryPhrase !== recoveryPhrase);
  const newRecKEK = await C.deriveKEKRecovery(C.phraseToEntropy(regen.recoveryPhrase), regen.payload.recovery_kdf);
  check('new recovery phrase unwraps the SAME DEK', hex(C.unwrapDEK(regen.payload.wrapped_dek_recovery, newRecKEK)) === hex(dek));

  console.log(failures === 0 ? '\nALL DM SET-PASSWORD TESTS PASS' : `\n${failures} FAILURE(S)`);
  process.exit(failures === 0 ? 0 : 1);
};
main().catch((e) => { console.error(e); process.exit(1); });
