// dm-session.js — in-browser DEK session for end-to-end encrypted DMs.
//
// Holds unwrapped per-epoch DEKs IN MEMORY ONLY for the life of the session. The DEKs are
// never written to localStorage / sessionStorage / IndexedDB and never sent to the server
// (the whole point: the server can't read password-epoch messages). They are wiped on:
//   - idle timeout (default 15 min, reset on any DEK use or explicit touch()),
//   - tab close / pagehide,
//   - explicit lockAll().
//
// The UI subscribes via onChange() to re-render locked/unlocked state. dm.js / the messages
// view call unlock(epoch, dek) after deriving+unwrapping a DEK with PolisDMCrypto, and
// dek(epoch) when opening a box.
(function () {
  'use strict';

  const DEFAULT_IDLE_MS = 15 * 60 * 1000;

  const deks = new Map();        // epoch (int) -> Uint8Array (the X25519 secret scalar)
  const listeners = new Set();   // change callbacks (unlockedEpochs[]) => void
  let idleMs = DEFAULT_IDLE_MS;
  let idleTimer = null;

  function notify() {
    const epochs = unlockedEpochs();
    for (const fn of listeners) {
      try { fn(epochs); } catch (_) { /* a bad listener must not break locking */ }
    }
    if (typeof document !== 'undefined' && document.dispatchEvent) {
      document.dispatchEvent(new CustomEvent('polis:dm-lock-state', { detail: { unlockedEpochs: epochs } }));
    }
  }

  function clearIdle() {
    if (idleTimer !== null) { clearTimeout(idleTimer); idleTimer = null; }
  }

  // touch resets the idle countdown — call on any DEK use or genuine user activity. No-op
  // when nothing is unlocked.
  function touch() {
    clearIdle();
    if (deks.size === 0) return;
    idleTimer = setTimeout(lockAll, idleMs);
    if (idleTimer && typeof idleTimer.unref === 'function') idleTimer.unref(); // don't hold Node alive
  }

  // unlock stores an epoch's unwrapped DEK. dek is a Uint8Array; the session takes ownership
  // and will zero it on lock — callers must not retain it.
  function unlock(epoch, dek) {
    if (!(dek instanceof Uint8Array) || dek.length !== 32) {
      throw new Error('unlock: dek must be a 32-byte Uint8Array');
    }
    const prev = deks.get(epoch);
    if (prev) prev.fill(0);
    deks.set(epoch, dek);
    touch();
    notify();
  }

  // dek returns an epoch's DEK (or null if locked) and counts as activity (resets idle).
  function dek(epoch) {
    const d = deks.get(epoch);
    if (d) touch();
    return d || null;
  }

  function isUnlocked(epoch) { return deks.has(epoch); }
  function unlockedEpochs() { return [...deks.keys()].sort((a, b) => a - b); }

  // lockAll wipes every DEK (best-effort zeroing) and stops the timer.
  function lockAll() {
    if (deks.size === 0) { clearIdle(); return; }
    for (const d of deks.values()) d.fill(0);
    deks.clear();
    clearIdle();
    notify();
  }

  function setIdleTimeoutMs(ms) {
    idleMs = ms > 0 ? ms : DEFAULT_IDLE_MS;
    if (deks.size > 0) touch(); // re-arm with the new interval
  }

  // onChange subscribes to lock-state changes; returns an unsubscribe function.
  function onChange(fn) { listeners.add(fn); return () => listeners.delete(fn); }

  // Wipe promptly when the tab goes away (memory is freed on close anyway, but zero early).
  if (typeof window !== 'undefined' && window.addEventListener) {
    window.addEventListener('pagehide', lockAll);
  }

  globalThis.PolisDMSession = {
    unlock, dek, isUnlocked, unlockedEpochs, lockAll, touch, setIdleTimeoutMs, onChange,
  };
})();
