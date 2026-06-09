// session.test.mjs — tests the in-browser DEK session manager (../dm-session.js).
// Run: `node webapp/internal/webui/www/vendor/session.test.mjs`. Exits non-zero on failure.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
(0, eval)(readFileSync(join(here, '..', 'dm-session.js'), 'utf8')); // sets globalThis.PolisDMSession
const S = globalThis.PolisDMSession;

let failures = 0;
const check = (name, ok, detail) => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok ? '' : '  — ' + (detail || '')}`);
  if (!ok) failures++;
};
const dek = (fill) => { const u = new Uint8Array(32); u.fill(fill); return u; };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const main = async () => {
  S.lockAll(); // clean slate

  // unlock / isUnlocked / dek / unlockedEpochs
  check('starts locked', !S.isUnlocked(1) && S.unlockedEpochs().length === 0);
  S.unlock(1, dek(0xaa));
  S.unlock(2, dek(0xbb));
  check('unlock marks epochs unlocked', S.isUnlocked(1) && S.isUnlocked(2));
  check('unlockedEpochs sorted', JSON.stringify(S.unlockedEpochs()) === '[1,2]');
  check('dek returns the stored key', S.dek(1)[0] === 0xaa && S.dek(2)[0] === 0xbb);
  check('dek of a locked epoch is null', S.dek(99) === null);

  // bad input rejected
  let threw = false;
  try { S.unlock(3, new Uint8Array(16)); } catch (_) { threw = true; }
  check('unlock rejects non-32-byte dek', threw);

  // onChange fires on unlock + lock
  let events = [];
  const off = S.onChange((eps) => events.push(eps.slice()));
  S.unlock(4, dek(0xcc));
  check('onChange fires on unlock', events.length === 1 && events[0].includes(4));
  S.lockAll();
  check('onChange fires on lockAll', events.length === 2 && events[1].length === 0);
  off();

  // lockAll wipes (zeroes) the DEK bytes — capture the reference first
  const d = dek(0xff);
  S.unlock(5, d);
  S.lockAll();
  check('lockAll zeroes the DEK bytes', d.every((b) => b === 0));
  check('lockAll clears all epochs', S.unlockedEpochs().length === 0 && !S.isUnlocked(5));

  // idle timeout auto-locks
  S.setIdleTimeoutMs(40);
  S.unlock(6, dek(0x11));
  check('unlocked before idle elapses', S.isUnlocked(6));
  await sleep(70);
  check('auto-locks after idle timeout', !S.isUnlocked(6));

  // touch / dek-use defers the idle auto-lock
  S.setIdleTimeoutMs(60);
  S.unlock(7, dek(0x22));
  await sleep(40); S.dek(7);   // activity at 40ms resets the 60ms timer
  await sleep(40);             // 80ms total, but only 40ms since last activity
  check('dek use defers auto-lock', S.isUnlocked(7));
  await sleep(80);
  check('auto-locks once idle past timeout', !S.isUnlocked(7));

  console.log(failures === 0 ? '\nALL SESSION TESTS PASS' : `\n${failures} FAILURE(S)`);
  process.exit(failures === 0 ? 0 : 1);
};
main().catch((e) => { console.error(e); process.exit(1); });
