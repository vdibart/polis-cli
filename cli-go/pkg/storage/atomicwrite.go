package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// atomicTempMarker sits between a target's base name and the random suffix in
// an atomic write's temp name: ".<base>.tmp-<12 hex>". The name is built from
// it (writeFileAtomic) and recognised by it (IsAtomicTemp), so the two cannot
// drift apart.
const atomicTempMarker = ".tmp-"

// atomicTempSuffixLen is the random suffix's length: 6 bytes, hex-encoded.
const atomicTempSuffixLen = 12

var atomicTempName = regexp.MustCompile(`^\..+` + regexp.QuoteMeta(atomicTempMarker) + `[0-9a-f]{12}$`)

// IsAtomicTemp reports whether a base name is an atomic write's temp file.
//
// ⭐ Listings hide these (see IncludeAtomicTemps): one exists only while a
// write runs, or after a writer was killed between create and rename, and in
// neither case is it a file of the site's. The pattern needs the leading dot
// AND exactly twelve hex characters, so a real file is not mistaken for one.
func IsAtomicTemp(name string) bool {
	return atomicTempName.MatchString(name)
}

// StaleAtomicTempAge is how old a temp file must be before it is treated as a
// killed writer's leftover and removed. A write takes milliseconds, so an hour
// can never catch one in flight.
const StaleAtomicTempAge = time.Hour

// removeStaleTemps deletes a killed writer's leftover temps in dir, best
// effort: an error never stops the write that called it.
//
// ⚠️ ONCE PER DIRECTORY PER PROCESS, not on every write. Scanning on each
// write made a write in a 1000-file directory about nine times slower
// (measured 2026-11-08: 345 µs against 39 µs), and a render writes every page.
// A CLI command is a fresh process, so its first write into a directory
// cleans it; a long-lived server is covered by the first write after it
// starts, and on hosted by Medic's sweep.
func removeStaleTemps(dir string) {
	if _, done := staleTempsCleaned.LoadOrStore(dir, true); done {
		return
	}
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	names, _ := f.Readdirnames(-1)
	_ = f.Close() // a directory opened to list it
	cutoff := time.Now().Add(-StaleAtomicTempAge)
	for _, n := range names {
		if !IsAtomicTemp(n) {
			continue
		}
		p := filepath.Join(dir, n)
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() && fi.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
	}
}

// staleTempsCleaned holds the directories removeStaleTemps has scanned.
var staleTempsCleaned sync.Map

// WriteFileAtomic replaces path with data so that a concurrent reader sees the
// old contents or the new, never an empty or partial file, and a crash
// mid-write leaves the old contents intact.
//
// ⭐ The whole file is written to a temp file beside the target — created
// exclusively under a random name, so two writers never share one — synced,
// then renamed over the target. rename(2) within one directory is atomic.
//
// keepMode decides an EXISTING target's mode: true keeps it (what os.WriteFile
// did, so a store Put changes nothing about modes); false applies mode, which
// the umask trims exactly as os.WriteFile did for a new file (what atomicfile
// always did, since its rename replaced the file too).
//
// ⚠️ A SYMLINK TARGET DEPENDS ON keepMode, and each side keeps what it did
// before temp-and-rename. A store Put (keepMode) writes THROUGH the link, as
// os.WriteFile did; that path is not atomic, and nothing polis writes is a
// link. ⛔ atomicfile (!keepMode) REPLACES the link with a regular file, as its
// rename always did: its callers include Medic repairing tenant files and the
// actor key projection, and writing through a link a tenant planted would
// write outside the tenant.
func WriteFileAtomic(path string, data []byte, mode fs.FileMode, keepMode bool) error {
	return writeFileAtomic(path, data, mode, keepMode, true)
}

// writeFileAtomic is WriteFileAtomic with the fsync optional: sync false is
// for rebuildable output only (see Rebuildable), atomic to readers but not
// durable against a power loss.
func writeFileAtomic(path string, data []byte, mode fs.FileMode, keepMode, sync bool) error {
	existed := false
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&fs.ModeSymlink != 0 {
			if keepMode {
				return os.WriteFile(path, data, mode)
			}
			// Replaced below, like any file; its own mode is irrelevant.
		} else {
			existed = true
			if keepMode {
				mode = fi.Mode().Perm()
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	removeStaleTemps(filepath.Dir(path))
	tmp, err := atomicTempPath(path)
	if err != nil {
		return err
	}
	// ⭐ O_EXCL under a random name: no writer can collide with another's temp.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		// ⚠️ A DIRECTORY WE CANNOT CREATE IN. Temp-and-rename needs write
		// permission on the directory; the in-place write it replaced needed it
		// only on the file. For a store Put (keepMode), fall back to exactly the
		// old write, so a damaged directory behaves as it always did — it simply
		// cannot be atomic there. atomicfile always needed a temp file, so it
		// keeps failing as it did.
		if keepMode && errors.Is(err, fs.ErrPermission) {
			return os.WriteFile(path, data, mode)
		}
		return fmt.Errorf("write tmp: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close() // the write already failed; that error is the one returned
		return fmt.Errorf("write tmp: %w", err)
	}
	if keepMode && existed {
		// The open's mode was trimmed by the umask; an EXISTING file's mode is
		// restored exactly. ⛔ Never for a new file: it gets the umask, as
		// os.WriteFile gave it — chmodding it too created 0644 files under
		// umask 077 where the old write made them 0600.
		if err := f.Chmod(mode); err != nil {
			_ = f.Close() // the chmod already failed; that error is the one returned
			return err
		}
	}
	if sync {
		if err := f.Sync(); err != nil {
			_ = f.Close() // the sync already failed; that error is the one returned
			return fmt.Errorf("sync tmp: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := renameRetrying(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	ok = true
	return nil
}

// atomicTempPath names a fresh temp file beside path.
func atomicTempPath(path string) (string, error) {
	var rnd [atomicTempSuffixLen / 2]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+atomicTempMarker+hex.EncodeToString(rnd[:])), nil
}

// renameBackoff is how long renameRetrying waits before each retry: about
// 310 ms in all.
var renameBackoff = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond}

// renameRetrying renames, retrying briefly where the platform refuses a
// rename over a file another process holds open (Windows; see
// renameRetryable). Everywhere else the first error is returned at once.
func renameRetrying(from, to string) error {
	err := os.Rename(from, to)
	for _, wait := range renameBackoff {
		if err == nil || !renameRetryable(err) {
			return err
		}
		time.Sleep(wait)
		err = os.Rename(from, to)
	}
	return err
}
