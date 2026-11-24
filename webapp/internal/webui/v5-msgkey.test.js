// The v5 app's messages key (msgkey.js): its rules, and its flows run with
// the real browser cryptography (the vendored tweetnacl, hash-wasm, bip39 and
// dm-crypto/dm-session/dm) against an in-memory keyring that does what the
// server's operations do. Run with:
//   node --test webapp/internal/webui/v5-msgkey.test.js

'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const www = path.join(__dirname, 'www');
const load = (p) => (0, eval)(fs.readFileSync(p, 'utf8'));
load(path.join(www, 'vendor/hash-wasm-argon2.js'));
load(path.join(www, 'vendor/tweetnacl.js'));
load(path.join(www, 'vendor/bip39.js'));
load(path.join(www, 'dm-crypto.js'));
load(path.join(www, 'dm-session.js'));
load(path.join(www, 'dm.js'));
globalThis.nacl.setPRNG((x, n) => globalThis.crypto.getRandomValues(x.subarray(0, n)));
globalThis.document = {}; // msgkey.js defines its flows where there is a page
const model = require(path.join(www, 'v5/msgkey.js'));
const Vault = globalThis.PolisV5Key;
const C = globalThis.PolisDMCrypto, S = globalThis.PolisDMSession;

// A keyring, as the server keeps it, behind the operations the app calls.
function server() {
    const kr = { current: 0, revision: 1, epochs: [{ id: 0, kind: 'bootstrap', public_key_messages: C.b64encode(new Uint8Array(32)), created_at: '2026-08-01T00:00:00Z' }] };
    const cur = () => kr.epochs.find((e) => e.id === kr.current);
    const ok = (data) => ({ ok: true, status: 200, data });
    const bad = (e) => ({ ok: false, status: 400, error: e });
    const add = (b) => {
        const id = kr.epochs.length;
        kr.epochs.push({ id, kind: 'password', public_key_messages: b.public_key_messages, wrapped_dek: b.wrapped_dek, wrapped_dek_recovery: b.wrapped_dek_recovery, kdf: b.kdf, recovery_kdf: b.recovery_kdf, created_at: new Date().toISOString() });
        kr.current = id;
        return ok({ epoch_id: id, current_epoch: id });
    };
    return {
        kr,
        get: async (p) => p === '/v1/messages/keyring' ? ok(JSON.parse(JSON.stringify(kr))) : bad('unknown ' + p),
        post: async (p, b) => {
            switch (p) {
                case '/v1/messages/password': return cur().kind === 'bootstrap' ? add(b) : bad('a password epoch already exists');
                case '/v1/messages/key/new': return cur().kind === 'password' ? add(b) : bad('set a password first');
                case '/v1/messages/password/change':
                    if (b.wrapped_dek) { cur().wrapped_dek = b.wrapped_dek; cur().kdf = b.kdf; }
                    if (b.wrapped_dek_recovery) { cur().wrapped_dek_recovery = b.wrapped_dek_recovery; cur().recovery_kdf = b.recovery_kdf; }
                    return ok({ success: true });
                case '/v1/messages/key/earlier': {
                    const e = kr.epochs.find((x) => x.id === b.epoch);
                    if (!e || e.id === kr.current) return bad('not an earlier key');
                    e.wrapped_dek = b.wrapped_dek; e.kdf = b.kdf;
                    return ok({ epoch: e.id });
                }
            }
            return bad('unknown ' + p);
        },
    };
}

// A message from someone else, sealed to the owner's key for an epoch.
function sealedTo(pubB64, epoch, text) {
    const peer = globalThis.nacl.box.keyPair();
    const sealed = C.sealBox(text, pubB64, peer.secretKey);
    return { locked: true, content: '', key_epoch: epoch, ciphertext: sealed.ciphertext, nonce: sealed.nonce, box_pub: C.b64encode(peer.publicKey), timestamp: '2026-11-17T10:00:00Z' };
}

test('what message shows: no password, locked, unlocked (2.3)', () => {
    assert.strictEqual(model.state(null, () => false), 'none');
    assert.strictEqual(model.state({ current: 0, epochs: [{ id: 0, kind: 'bootstrap' }] }, () => false), 'none');
    const kr = { current: 1, epochs: [{ id: 0, kind: 'bootstrap' }, { id: 1, kind: 'password' }] };
    assert.strictEqual(model.state(kr, () => false), 'locked');
    assert.strictEqual(model.state(kr, (id) => id === 1), 'unlocked');
});

test('an earlier key\'s messages: one group per key, counted, with the months they span (2.3)', () => {
    const msgs = [
        { locked: true, key_epoch: 1, timestamp: '2026-08-03T00:00:00Z' },
        { locked: true, key_epoch: 1, timestamp: '2026-10-20T00:00:00Z' },
        { locked: true, key_epoch: 2, timestamp: '2026-11-01T00:00:00Z' },
        { locked: false, key_epoch: 2 },
    ];
    assert.deepStrictEqual(model.earlier(msgs, 2, () => false), [{ epoch: 1, count: 2, months: 'Aug–Oct 2026' }]);
    assert.deepStrictEqual(model.earlier(msgs, 2, (id) => id === 1), [], 'an earlier key open in this tab has no box');
    assert.strictEqual(model.span(['2025-12-01T00:00:00Z', '2026-01-05T00:00:00Z']), 'Dec 2025–Jan 2026');
});

test('the flows, with the real cryptography: set, lock, a wrong password, unlock, open, change, the phrase, a fresh key, an earlier key', { timeout: 120000 }, async () => {
    const srv = server();
    Vault.init(srv);
    await Vault.keyring(true);
    assert.strictEqual(Vault.state(), 'none');

    // Set a password: the phrase comes back once; the server got wraps only.
    const phrase = await Vault.setPassword('first password');
    assert.strictEqual(phrase.split(' ').length, 12);
    assert.ok(Vault.validPhrase(phrase));
    // A wrong word fails the checksum. Shown on a fixed phrase, not by swapping
    // a word of the random one: 12 words carry only a 4-bit checksum, so about
    // one swap in 16 still passes (and the swap is a no-op when the word was
    // already the replacement). BIP39's own test vector: valid as written,
    // invalid with its last word changed.
    const vector = 'abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon ';
    assert.ok(Vault.validPhrase(vector + 'about'), 'the BIP39 test vector is valid');
    assert.ok(!Vault.validPhrase(vector + 'abandon'), 'a wrong word fails the checksum');
    assert.ok(!Vault.validPhrase(vector + 'zzzz'), 'a word not in the list fails');
    assert.doesNotMatch(JSON.stringify(srv.kr), /first password/);
    assert.strictEqual(Vault.state(), 'unlocked');
    const e1 = srv.kr.current, pub1 = srv.kr.epochs[e1].public_key_messages;

    // Locked, then a wrong password, then the right one; a sealed message opens.
    S.lockAll();
    assert.strictEqual(Vault.state(), 'locked');
    await assert.rejects(Vault.unlock('not it', 'password'), (e) => Vault.isWrong(e));
    await Vault.unlock('first password', 'password');
    const m1 = sealedTo(pub1, e1, 'only the owner reads this');
    assert.strictEqual(Vault.open(m1), 'only the owner reads this');

    // Change the password; the phrase still opens it.
    await Vault.changePassword('first password', 'second password');
    S.lockAll();
    await assert.rejects(Vault.unlock('first password', 'password'), (e) => Vault.isWrong(e));
    await Vault.unlock(phrase, 'phrase');
    assert.strictEqual(Vault.open(m1), 'only the owner reads this');
    // After the phrase, a new password.
    await Vault.setNewPassword('third password');
    S.lockAll();
    await Vault.unlock('third password', 'password');

    // A fresh key: a new current key; the earlier one waits, sealed.
    S.lockAll();
    const phrase2 = await Vault.freshKey('fresh password');
    assert.notStrictEqual(phrase2, phrase);
    const e2 = srv.kr.current;
    assert.ok(e2 > e1);
    assert.strictEqual(Vault.open(m1), null, 'the earlier key is not open in this tab');
    assert.deepStrictEqual(model.earlier([m1], e2, Vault.isUnlocked).map((g) => g.epoch), [e1]);

    // The earlier key opens with its own password; then kept under the current one.
    await Vault.unlock('third password', 'password', e1);
    assert.strictEqual(Vault.open(m1), 'only the owner reads this');
    await Vault.keepUnlocked(e1, 'fresh password');
    S.lockAll();
    await Vault.unlock('fresh password', 'password', e1);
    assert.strictEqual(Vault.open(m1), 'only the owner reads this', 'the earlier key now opens with the current password');

    // A new recovery phrase replaces the old one.
    const phrase3 = await Vault.newPhrase('fresh password');
    S.lockAll();
    await assert.rejects(Vault.unlock(phrase2, 'phrase'), (e) => Vault.isWrong(e));
    await Vault.unlock(phrase3, 'phrase');
    S.lockAll();
});
