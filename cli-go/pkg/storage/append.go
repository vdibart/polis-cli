package storage

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
)

// Appending, and the lock that makes it mean something.
//
// ⛔⛔ THE INTERFACE HAS NO Append AND WILL NOT GET ONE. Object stores have no
// append, so a backend-specific method would mean a caller silently getting
// kernel-append semantics on one backend and read-modify-write on another —
// and the difference is a lost update, which is the kind of bug nobody can
// reproduce. ⭐ ONE BEHAVIOUR EVERYWHERE: read, concatenate, write.
//
// ⭐⭐⭐ AND THE OBJECTION TO THAT TURNED OUT TO BE ALREADY MOOT. The argument
// against read-modify-write was that it changes the story "from the kernel
// appends to last writer wins". But the public index — the file that argument
// was about — is ALREADY written three ways:
//
//	new entry       kernel O_APPEND                  publish, comment
//	update/remove   whole-file temp + rename         unpublish, dedupe
//	⭐ REBUILD       whole-file atomicfile.WriteFile  MEDIC, EVERY SWEEP
//
// ⛔ Two of the three replace the whole file, so O_APPEND's atomicity buys
// nothing: a rebuild landing after an append clobbers it. **The race already
// existed, invisibly, precisely because one path looked atomic.** This does not
// introduce last-writer-wins; it makes it uniform, and therefore fixable.
//
// ⚠️ WHICH IS WHY THE LOCK IS PER KEY AND NOT PER APPEND. Serialising appends
// against each other would fix nothing — the clobbering writer is a whole-file
// rebuild. Every writer of a key must take the same lock.

// keyLocks serialises writers of one key within this process.
//
// ⛔ IN-PROCESS ONLY, AND THAT IS THE DECIDED SCOPE (2026-10-04), not an
// oversight. ⭐ It is worth more than it sounds: on the hosted deployment the
// background actors run INSIDE the same process as the request handlers, so
// the only instance of this race that exists today is between two goroutines —
// exactly what this fixes.
//
// ⚠️ Across processes — a CLI and a server, or two serverless invocations —
// last writer still wins. ⭐ The recovery is that Medic's rebuild reconstructs
// the index from content on disk, so a lost line self-heals within a sweep.
// ⛔ But actors do not run on a remote instance yet, so that safety net arrives
// with them; do not assume it before.
//
// ⭐ If recovery turns out to be frequent, the next step is a conditional write
// (if-match / if-none-match), which object stores support natively. It was
// deliberately deferred rather than built on speculation.
var keyLocks sync.Map // lockKey -> *sync.Mutex

type lockKey struct {
	root string
	key  string
}

// lockFor returns the mutex guarding one key of one store.
func lockFor(root, key string) *sync.Mutex {
	actual, _ := keyLocks.LoadOrStore(lockKey{root: root, key: key}, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// lockRoot identifies a store for locking purposes.
//
// ⚠️ Two stores addressing the same bytes must produce the same root, or the
// lock protects nothing. RealPath is used where a backend has one, because two
// POSIX stores rooted differently can still reach one file.
func lockRoot(s Store) string {
	if p, err := s.RealPath(""); err == nil && p != "" {
		return p
	}
	return ""
}

// WithKeyLock runs fn holding the write lock for key.
//
// ⛔ EVERY WRITER OF A KEY MUST USE THIS, not just the appenders. A whole-file
// rewrite that skips it can still clobber a concurrent append, which is the
// exact bug this exists to close.
//
// ⛔⛔ AND IT MUST RESOLVE TO THE SAME MUTEX AS WithPathLock FOR THE SAME FILE.
// During the site-handle migration a package converts to the store-addressed
// form while its neighbours still use the path-addressed one — the public
// index is written by `metadata` and rebuilt by `index`, and if those two took
// DIFFERENT locks the lock would stop excluding anything while still looking
// present. ⚠️ Caught by probing rather than by reasoning: they did not agree,
// and converting one package would have silently un-fixed the race.
//
// ⭐ So where a backend has a real path, both forms key on it.
func WithKeyLock(s Store, key string, fn func() error) error {
	mu := lockFor(lockIdentity(s, key))
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// lockIdentity is the one answer to "which file is this?", shared by both
// locking forms.
//
// ⚠️ On a backend with no real path there is nothing to reconcile with, so the
// store's own root and the key identify it — two handles on one object store
// addressing the same key agree, which is what matters there.
func lockIdentity(s Store, key string) (string, string) {
	if root, err := s.RealPath(""); err == nil && root != "" {
		return "", cleanAbs(filepath.Join(root, key))
	}
	return lockRoot(s), key
}

// Append adds data to the end of key, creating it if absent.
//
// ⭐ Read, concatenate, write — the same on every backend. ⚠️ It holds the key
// lock for the whole read-modify-write, so two appends in this process cannot
// interleave.
//
// ⛔ IT IS NOT FOR LARGE FILES. The whole value is read into memory and written
// back, which is fine for an index line or a log entry and wrong for anything
// that grows without bound. ⚠️ If a caller appends to something large, that is
// a sign the data wants a different shape, not that this wants streaming.
func Append(ctx context.Context, s Store, key string, data []byte, opts ...PutOption) error {
	if s == nil {
		return errors.New("storage: append to a nil store")
	}
	return WithKeyLock(s, key, func() error {
		existing, err := s.Get(ctx, key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			// ⛔ A read failure is NOT an empty file. Treating it as one would
			// silently truncate: the append would write only the new data and
			// the previous contents would be gone.
			return err
		}
		return s.Put(ctx, key, append(existing, data...), opts...)
	})
}

// AppendLine adds data followed by a newline.
//
// ⭐ The common case by far — JSONL indexes, audit logs — and worth naming so
// that callers do not each re-derive whether the newline goes before or after.
// ⚠️ It does NOT insert a separator before the data: a file that does not end
// in a newline gains a joined line, which matches what O_APPEND did.
func AppendLine(ctx context.Context, s Store, key string, line []byte, opts ...PutOption) error {
	return Append(ctx, s, key, append(append([]byte{}, line...), '\n'), opts...)
}

// ── path-addressed forms ────────────────────────────────────────────────

// WithPathLock runs fn holding the write lock for an absolute path.
//
// ⭐⭐ THE PATH FORM EXISTS BECAUSE THE WRITERS THAT MUST SHARE A LOCK DO NOT
// SHARE A STORE. The public index is written by `metadata` (which works in
// siteDir plus a relative path) and rebuilt by `index` (which has an absolute
// path), and neither holds a Store. ⛔ A lock they cannot both take is not a
// lock. ⭐ The absolute path is the one identity every writer of a file
// already has, so it is the key.
//
// ⚠️ IT MUST BE THE SAME STRING FOR THE SAME FILE. cleanAbs normalises, but a
// symlinked route to one file still locks separately — acceptable here, where
// the writers are all inside one process writing a known layout, and worth
// knowing before relying on it somewhere else.
func WithPathLock(path string, fn func() error) error {
	mu := lockFor("", cleanAbs(path))
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// AppendAtPath appends to a file by path, under the same lock.
//
// ⛔ A read failure is not an empty file, for the same reason as Append: a
// truncating append would lose everything already written.
func AppendAtPath(path string, data []byte, opts ...PutOption) error {
	return WithPathLock(path, func() error {
		existing, err := ReadAtPath(path)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return WriteAtPath(path, append(existing, data...), opts...)
	})
}

// AppendLineAtPath appends data and a newline.
//
// ⚠️ No separator is inserted before the data, matching what O_APPEND did.
func AppendLineAtPath(path string, line []byte, opts ...PutOption) error {
	return AppendAtPath(path, append(append([]byte{}, line...), '\n'), opts...)
}
