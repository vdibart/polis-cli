// The owner's messages key in the v5 app (spec 2.3, 2.4): unlocking, opening
// and sealing messages, and setting or changing the messages password. The
// cryptography is today's (dm-crypto.js over the vendored tweetnacl,
// hash-wasm and bip39; dm-session.js holds unlocked keys in this tab's memory
// only), so a blob wrapped here opens in today's app and back.
//
// ⛔ The password, the recovery phrase and every key stay in the browser. The
// site receives only what is wrapped (dm.set_password, dm.change_password,
// dm.new_key, dm.rewrap_earlier), and those are sign-in only.
(function (root) {
  'use strict';

  // ── The rules ────────────────────────────────────────────────────────

  function current(kr) {
    if (!kr || !Array.isArray(kr.epochs)) return null;
    return kr.epochs.filter(function (e) { return e.id === kr.current; })[0] || null;
  }

  // state is what message shows (2.3): 'none' (no keyring yet, or no
  // password: the site seals), 'locked', or 'unlocked'.
  function state(kr, isUnlocked) {
    var cur = current(kr);
    if (!cur || cur.kind !== 'password') return 'none';
    return isUnlocked(cur.id) ? 'unlocked' : 'locked';
  }

  // earlier is a conversation's messages under keys other than the current
  // one that this tab cannot open: one group per such key, oldest first, with
  // its count and the months it spans ("Aug–Oct 2026").
  var MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  function span(times) {
    var ds = times.map(function (t) { return new Date(t); }).filter(function (d) { return !isNaN(d); }).sort(function (a, b) { return a - b; });
    if (!ds.length) return '';
    var f = function (d) { return MONTHS[d.getUTCMonth()]; }, a = ds[0], z = ds[ds.length - 1];
    if (a.getUTCFullYear() !== z.getUTCFullYear()) return f(a) + ' ' + a.getUTCFullYear() + '–' + f(z) + ' ' + z.getUTCFullYear();
    return (f(a) === f(z) ? f(a) : f(a) + '–' + f(z)) + ' ' + z.getUTCFullYear();
  }
  function earlier(messages, currentEpoch, isUnlocked) {
    var by = {};
    (messages || []).forEach(function (m) {
      if (!m.locked || m.key_epoch === currentEpoch || isUnlocked(m.key_epoch)) return;
      (by[m.key_epoch] = by[m.key_epoch] || []).push(m);
    });
    return Object.keys(by).map(Number).sort(function (a, b) { return a - b; }).map(function (e) {
      return { epoch: e, count: by[e].length, months: span(by[e].map(function (m) { return m.timestamp; })) };
    });
  }

  var model = { current: current, state: state, earlier: earlier, span: span };
  if (typeof module !== 'undefined' && module.exports) module.exports = model;

  // ── The cryptography, in this tab ────────────────────────────────────

  if (!root.document) return;
  var C = function () { return root.PolisDMCrypto; };
  var S = function () { return root.PolisDMSession; };
  var D = function () { return root.PolisDM; };

  var api = null, kr = null;

  async function keyring(fresh) {
    if (kr && !fresh) return kr;
    var r = await api.get('/v1/messages/keyring');
    kr = r.ok ? r.data : null;
    return kr;
  }
  function ready() { return !!(C() && S() && D()); }
  function isUnlocked(id) { return !!(S() && S().isUnlocked(id)); }

  async function dekFromPassword(ep, password) {
    var kek = await C().deriveKEKPassword(password, ep.kdf);
    return C().unwrapDEK(ep.wrapped_dek, kek);
  }
  async function dekFromPhrase(ep, phrase) {
    var entropy = C().phraseToEntropy(phrase);   // throws on a phrase that fails its checksum
    var kek = await C().deriveKEKRecovery(entropy, ep.recovery_kdf);
    return C().unwrapDEK(ep.wrapped_dek_recovery, kek);
  }
  function epoch(id) { return (kr && kr.epochs || []).filter(function (e) { return e.id === id; })[0]; }

  // validPhrase: the 12 words pass their checksum (checked before anything
  // is tried, spec 2.3).
  function validPhrase(phrase) {
    try { C().phraseToEntropy(phrase); return String(phrase).trim().split(/\s+/).length === 12; } catch (e) { return false; }
  }

  // unlock opens a key in this tab: the current one, or an earlier one by id,
  // with its password or its recovery phrase. Throws ErrWrongKey on a wrong
  // one: the check is the decryption itself, with no server round trip.
  async function unlock(secret, how, id) {
    await keyring();
    var ep = id === undefined ? current(kr) : epoch(id);
    if (!ep) throw new Error('no such key');
    var dek = how === 'phrase' ? await dekFromPhrase(ep, secret) : await dekFromPassword(ep, secret);
    S().unlock(ep.id, dek);
    return ep.id;
  }

  // open is a message's words, from its box, when this tab holds its key.
  function open(m) {
    if (!m.locked) return m.content;
    var dek = S() && S().dek(m.key_epoch);
    if (!dek || !m.ciphertext) return null;
    try { return C().openBox(m.ciphertext, m.nonce, m.box_pub, dek); } catch (e) { return null; }
  }

  // send seals a message in the browser to the recipient's key and to the
  // owner's own (to read back), then sends the boxes (dm.send_sealed).
  async function send(recipientURL, text) {
    await keyring();
    var cur = current(kr), dek = S().dek(cur.id);
    if (!dek) throw new Error('Unlock your messages to send.');
    var rk = await api.get('/v1/messages/recipient-key?recipient_url=' + encodeURIComponent(recipientURL));
    if (!rk.ok) throw new Error(rk.error);
    return api.post('/v1/messages/send-sealed', {
      recipient_url: recipientURL, sender_epoch: cur.id, recipient_epoch: rk.data.recipient_epoch, box_pub: cur.public_key_messages,
      delivery: C().sealBox(text, rk.data.public_key_messages, dek), sent: C().sealBox(text, cur.public_key_messages, dek),
    });
  }

  // setPassword puts the messages under a password the first time; answers
  // the recovery phrase, shown once.
  async function setPassword(password) {
    var built = await D().buildSetPasswordPayload(password);
    var r = await api.post('/v1/messages/password', built.payload);
    if (!r.ok) throw new Error(r.error);
    S().unlock(r.data.epoch_id, built.dek);
    await keyring(true);
    return built.recoveryPhrase;
  }

  // changePassword: the current password opens the key, the new one wraps it
  // again; the phrase keeps working.
  async function changePassword(currentPassword, newPassword) {
    await keyring(true);
    var ep = current(kr), dek = await dekFromPassword(ep, currentPassword);
    var r = await api.post('/v1/messages/password/change', await D().buildRewrapPasswordPayload(newPassword, dek));
    if (!r.ok) throw new Error(r.error);
    S().unlock(ep.id, dek);
    await keyring(true);
  }

  // setNewPassword, after recovering with the phrase: the key this tab now
  // holds is wrapped again under a new password.
  async function setNewPassword(newPassword) {
    await keyring();
    var ep = current(kr), dek = S().dek(ep.id);
    if (!dek) throw new Error('Unlock your messages first.');
    var r = await api.post('/v1/messages/password/change', await D().buildRewrapPasswordPayload(newPassword, dek));
    if (!r.ok) throw new Error(r.error);
    await keyring(true);
  }

  // newPhrase makes a new recovery phrase (the old one stops working);
  // needs the key, from this tab or the current password.
  async function newPhrase(currentPassword) {
    await keyring(true);
    var ep = current(kr), dek = S().dek(ep.id) || await dekFromPassword(ep, currentPassword);
    var built = await D().buildRegenRecoveryPayload(dek);
    var r = await api.post('/v1/messages/password/change', built.payload);
    if (!r.ok) throw new Error(r.error);
    await keyring(true);
    return built.recoveryPhrase;
  }

  // freshKey starts a fresh key (lost both): a new key, password and phrase.
  async function freshKey(password) {
    var built = await D().buildSetPasswordPayload(password);
    var r = await api.post('/v1/messages/key/new', built.payload);
    if (!r.ok) throw new Error(r.error);
    S().unlock(r.data.epoch_id, built.dek);
    await keyring(true);
    return built.recoveryPhrase;
  }

  // keepUnlocked wraps an earlier key, already open in this tab, under the
  // current password, so it opens with it from now on.
  async function keepUnlocked(id, currentPassword) {
    await keyring(true);
    var cur = current(kr);
    await dekFromPassword(cur, currentPassword);   // the current password, checked
    var dek = S().dek(id);
    if (!dek) throw new Error('Unlock those messages first.');
    var wrap = await D().buildRewrapPasswordPayload(currentPassword, dek);
    var r = await api.post('/v1/messages/key/earlier', { epoch: id, wrapped_dek: wrap.wrapped_dek, kdf: wrap.kdf });
    if (!r.ok) throw new Error(r.error);
    await keyring(true);
  }

  root.PolisV5Key = {
    model: model,
    init: function (a) { api = a; },
    keyring: keyring, ready: ready, isUnlocked: isUnlocked, validPhrase: validPhrase,
    state: function () { return state(kr, isUnlocked); },
    current: function () { return current(kr); },
    unlock: unlock, open: open, send: send,
    setPassword: setPassword, changePassword: changePassword, setNewPassword: setNewPassword,
    newPhrase: newPhrase, freshKey: freshKey, keepUnlocked: keepUnlocked,
    isWrong: function (e) { return !!(e && C() && e instanceof C().ErrWrongKey); },
    onChange: function (fn) { return S() ? S().onChange(fn) : function () {}; },
  };
})(typeof window !== 'undefined' ? window : globalThis);
