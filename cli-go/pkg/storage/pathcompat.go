package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// The helpers in this file bridge code that holds an absolute filesystem path
// to a Store, which addresses keys relative to a root.
//
// ⚠️ THEY ARE TRANSITIONAL, like RelKey, and the set should SHRINK. Code that
// has been fully detached carries a Store and a key, and calls Get directly.
// Every use of a helper here marks a caller that still thinks in paths.
//
// ⭐ But they are not a workaround for "the path is outside the site". A Store
// is a root plus keys and NewPOSIX will root anywhere, so a path whose root is
// a theme directory, a bundle directory or the CLI themes directory is still
// addressable — it is simply a different store. That distinction is what keeps
// these from becoming exclusions; see the detach recipe's category B.

// cleanAbs normalises an absolute path before it is split into a root and a key.
//
// ⛔ WITHOUT THIS A TRAILING SLASH ADDRESSES THE WRONG FILE, because
// filepath.Dir("a/b/") is "a/b" and Base is "b" — so every helper below would
// address a/b/b. Reproduced before the fix: ListDir(tenants+"/") was not-found,
// a tenant sweep over it swept ZERO tenants and exited clean, a recursive
// DeleteAtPath("dir/") removed dir/dir and left dir, and WriteAtPath("w/x/")
// wrote w/x/x. Two main.go files pass os.Args straight in, so the input is not
// hypothetical.
func cleanAbs(p string) string { return filepath.Clean(p) }

// splitPath turns a filesystem path into the root and key the helpers below
// address it by. ok is false for the empty path, which names nothing: every os
// call these helpers replace failed on "" with not-exist, and filepath.Clean
// would otherwise turn it into "." and quietly address the working directory.
//
// ⛔ A path with no usable base name IS ITS OWN ROOT, addressed by the empty key.
// "." has no parent to root at; the base of ".." (and of "../..") is "..", which
// CleanKey rightly refuses as a key; and the base of "/" is "/", which it refuses
// as absolute. Splitting those the ordinary way made the helpers refuse paths the
// os calls accepted — `patrol .` from the tenants directory swept zero tenants.
func splitPath(p string) (root, key string, ok bool) {
	if p == "" {
		return "", "", false
	}
	c := cleanAbs(p)
	if base := filepath.Base(c); c == "." || base == ".." || base == string(filepath.Separator) {
		return c, "", true
	}
	return filepath.Dir(c), filepath.Base(c), true
}

// errEmptyPath is what the helpers return for "", mirroring the not-exist the os
// calls gave. It satisfies errors.Is(err, fs.ErrNotExist) through ErrNotFound.
func errEmptyPath() error { return fmt.Errorf("%w: %q", ErrNotFound, "") }

// ReadAtPath reads absPath through a store rooted at its own directory.
//
// Use it where resolution chose between several roots and the winner is only
// known as a path: rooting at the parent asks the interface for exactly the
// file that was chosen, with no special case for which root it came from.
func ReadAtPath(absPath string) ([]byte, error) {
	root, key, ok := splitPath(absPath)
	if !ok {
		return nil, errEmptyPath()
	}
	return NewPOSIX(root).Get(context.Background(), key)
}

// WriteAtPath writes data to absPath through a store rooted at its directory.
//
// ⛔ A MISSING PARENT UNDER .polis IS CREATED 0700. The store is rooted at the
// file's own directory, so when that directory does not exist the store creates
// it, and every missing ancestor, at its DirMode. With the default 0755 a write
// to .polis/x/y/file left .polis/x and .polis/x/y world-readable, and on
// 2026-10-04 that was half the directories under .polis across the hosted fleet.
// The site rule is "0700 at or under .polis", so a root inside a .polis
// directory gets 0700; every other root keeps the default served content needs.
func WriteAtPath(absPath string, data []byte, opts ...PutOption) error {
	root, key, ok := splitPath(absPath)
	if !ok {
		return errEmptyPath()
	}
	return NewPOSIX(root, compatDirMode(root)...).Put(context.Background(), key, data, opts...)
}

// compatDirMode is 0700 for a root at or under a directory named .polis, and
// nothing (the store default) otherwise.
func compatDirMode(root string) []StoreOption {
	for _, seg := range strings.Split(filepath.ToSlash(cleanAbs(root)), "/") {
		if seg == ".polis" {
			return []StoreOption{WithDirMode(0o700)}
		}
	}
	return nil
}

// StatAtPath stats absPath through a store rooted at its own directory.
func StatAtPath(absPath string) (Info, error) {
	root, key, ok := splitPath(absPath)
	if !ok {
		return Info{}, errEmptyPath()
	}
	return NewPOSIX(root).Stat(context.Background(), key)
}

// PathExists reports whether absPath is present, addressed through a store
// rooted at its own directory.
//
// ⚠️ For a DIRECTORY the answer is BACKEND-DEPENDENT, and the earlier version of
// this comment got it wrong. On POSIX an empty directory reads as PRESENT,
// because Exists stats it. On a prefix store it would read as absent, because
// there is no key under it. Where that difference matters — a caller asking
// whether a legacy directory is still there at all — stay on os.Stat and say why.
func PathExists(absPath string) (bool, error) {
	root, key, ok := splitPath(absPath)
	if !ok {
		return false, nil
	}
	return NewPOSIX(root).Exists(context.Background(), key)
}

// ListAtPath returns what is directly under absDir, addressed through a store
// rooted at absDir itself. Keys in the result are relative to absDir.
func ListAtPath(absDir string, opts ...ListOption) ([]Entry, error) {
	if absDir == "" {
		// A missing prefix lists empty, and "" is a path that does not exist.
		return nil, nil
	}
	return NewPOSIX(cleanAbs(absDir)).List(context.Background(), "", opts...)
}

// ListDir returns what is directly under absDir, and returns ErrNotFound when
// absDir DOES NOT EXIST.
//
// ⛔ THIS IS THE REPLACEMENT FOR os.ReadDir, and ListAtPath is not. List returns
// empty for a missing prefix by a deliberate decision — a prefix store cannot
// tell "no such directory" from "no keys beneath it" — but os.ReadDir FAILED, and
// callers built on that: the error is how they said "I could not look" as opposed
// to "there is nothing there".
//
// ⚠️ Converting os.ReadDir to ListAtPath silently destroys that distinction, and
// it did so FOUR times in one evening: Tailor's StaleScopedFeedFiles, Tailor's
// legacy-feed scaffolding, Medic retiring a legacy directory that was never there,
// and the actor guard reporting no problem for a tenants directory it could not
// read. Two were caught only by golden tests.
//
// ⭐ So the rule is mechanical: os.ReadDir becomes ListDir. Reach for ListAtPath
// only when "missing" and "empty" genuinely mean the same thing to the caller.
func ListDir(absDir string, opts ...ListOption) ([]Entry, error) {
	// ⛔ Only "" is refused, because os.ReadDir("") failed and filepath.Clean would
	// turn it into the working directory. ".", ".." and "/" are directories and
	// os.ReadDir listed them: an earlier version refused "." and "/" here, and a
	// sweep handed "." then swept nothing and reported success.
	if absDir == "" {
		return nil, errEmptyPath()
	}
	absDir = cleanAbs(absDir)
	if present, err := PathExists(absDir); err != nil {
		return nil, err
	} else if !present {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, absDir)
	}
	return ListAtPath(absDir, opts...)
}

// DeleteAtPath removes absPath through a store rooted at its own directory.
func DeleteAtPath(absPath string, opts ...DeleteOption) error {
	root, key, ok := splitPath(absPath)
	if !ok {
		if resolveDelete(opts).IgnoreMissing {
			return nil
		}
		return errEmptyPath()
	}
	return NewPOSIX(root).Delete(context.Background(), key, opts...)
}

// PathAbsent reports whether absPath is GENUINELY NOT THERE, as distinct from
// "I could not find out".
//
// ⛔ THIS IS THE REPLACEMENT FOR `os.Stat(p); errors.Is(err, fs.ErrNotExist)`,
// and PathExists is not. PathExists answers "is it there", so its false covers a
// permission error as well as an absence — and the detach converted ~33
// absent-only checks to it, discarding the error, which made a locked or
// unreadable path read as missing. Patrol then reported healthy policies and a
// healthy storage salt as MISSING, Medic's reports stopped converging, and
// site/validate reported PRIVATE_KEY_MISSING for a key that was present but
// unreadable.
//
// ⭐ The point of the separate helper is that the mechanical form stays correct
// even when a caller ignores the error, exactly as ListDir does for os.ReadDir:
//
//	if absent, _ := storage.PathAbsent(p); absent { ... }   // only a real absence
//
// A permission error yields (false, err): not absent, and the reason is available
// to anyone who wants it.
func PathAbsent(absPath string) (bool, error) {
	present, err := PathExists(absPath)
	if err != nil {
		return false, err
	}
	return !present, nil
}
