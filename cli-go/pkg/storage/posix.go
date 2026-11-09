package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// POSIX is a Store backed by a real filesystem rooted at a site directory.
// It is what `local` mode, a VPS, and every existing deployment use, and it is
// the reference implementation: where the interface is ambiguous, what POSIX
// does today is the answer, because thousands of live sites already depend on
// it.
type POSIX struct {
	root string
	opts StoreOptions
}

// NewPOSIX returns a Store rooted at dir. The directory need not exist yet;
// Put creates what it needs.
//
// ⚠️ For a POLIS SITE root, prefer NewSite: it declares .polis private, which
// NewPOSIX on its own does not. NewPOSIX stays general because it is also used
// for roots that are not sites — a theme directory, the installed bundle, the
// CLI themes directory.
func NewPOSIX(dir string, opts ...StoreOption) *POSIX {
	return &POSIX{root: dir, opts: resolveStore(opts)}
}

// NewSite returns a Store rooted at a polis site directory, with .polis declared
// private at 0700.
//
// ⛔ THIS IS THE CONSTRUCTOR FOR A SITE ROOT, and it exists so the rule cannot be
// forgotten. ".polis MUST NOT be world-readable under any circumstances"
// (decided 2026-10-01) is an invariant, not a preference, and an option each
// caller had to remember would eventually be left off exactly once.
// site.Init has encoded the same split since the beginning: 0700 at or under
// .polis, 0755 elsewhere.
func NewSite(dir string, opts ...StoreOption) *POSIX {
	return NewPOSIX(dir, append([]StoreOption{WithPrivatePrefix(".polis", 0o700)}, opts...)...)
}

// Root returns the site directory this store is rooted at. It exists for the
// handful of callers mid-migration that still need to hand a path to something
// outside this interface; prefer RealPath, which states the dependency.
func (p *POSIX) Root() string { return p.root }

// CleanKey validates and normalises a key. A key is site-relative, uses
// forward slashes, and may not escape the root. A trailing slash is not
// significant, so "a/b" and "a/b/" are the same key.
//
// This is exported because every backend needs exactly these rules, and a
// backend that invents its own is a path-traversal bug waiting for a caller
// that passes user input.
func CleanKey(key string) (string, error) {
	// ⛔⛔ REFUSE, NEVER TRIM. This used to TrimSpace the key, and a cleaner
	// that silently changes its input turns every check made on the raw form
	// into a check-then-use bug: the serve handler denied private names on
	// " .polis/..." (its first segment does not start with a dot), then the
	// store trimmed it and read .polis — the DM keyring, served over HTTP.
	if key != strings.TrimSpace(key) {
		return "", fmt.Errorf("%w: %q has leading or trailing whitespace", ErrInvalidKey, key)
	}
	k := key
	if strings.ContainsRune(k, '\x00') {
		return "", fmt.Errorf("%w: %q contains NUL", ErrInvalidKey, key)
	}
	// ⛔ A backslash is a separator on Windows, where a key like
	// `posts\..\.polis\keys\id_ed25519` would escape the checks below and the
	// root. Keys are slash-separated on every OS.
	if strings.ContainsRune(k, '\\') {
		return "", fmt.Errorf("%w: %q contains a backslash; keys are slash-separated", ErrInvalidKey, key)
	}
	// An absolute key is REFUSED, not normalised. Trimming the slash would be
	// friendlier and much worse: a caller that passes a real filesystem path
	// by mistake would get a silently wrong key ("/home/me/site/a.md" becomes
	// "home/me/site/a.md") written somewhere plausible, instead of an error.
	// Keys are site-relative by definition, so a leading slash is a bug.
	if strings.HasPrefix(k, "/") || filepath.IsAbs(k) {
		return "", fmt.Errorf("%w: %q is absolute; keys are site-relative", ErrInvalidKey, key)
	}
	k = strings.TrimSuffix(k, "/")
	if k == "" {
		return "", nil // the root prefix is valid for List
	}
	cleaned := path.Clean(k)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: %q escapes the root", ErrInvalidKey, key)
	}
	if cleaned == "." {
		return "", nil
	}
	return cleaned, nil
}

// RelKey converts an absolute filesystem path under root into a storage key.
// It fails if the path escapes root.
//
// This exists because many path helpers — following.DefaultPath, index.IndexPath,
// actor.RegistryPath and friends — return an absolute path built from the site
// directory, and a key is site-relative. Converting at the call site is the
// mechanical way to migrate those without changing every helper at once.
//
// It is a MIGRATION AID and should shrink, not spread. The destination is path
// helpers that return keys directly, at which point a caller passes the key
// straight through and nothing has to be relativised. Reach for a literal key
// first; use this only where a helper hands you an absolute path.
func RelKey(root, absPath string) (string, error) {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return "", fmt.Errorf("%w: %q is not under %q", ErrInvalidKey, absPath, root)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%w: %q escapes %q", ErrInvalidKey, absPath, root)
	}
	return CleanKey(rel)
}

// abs turns a key into a filesystem path under the root.
func (p *POSIX) abs(key string) (string, error) {
	k, err := CleanKey(key)
	if err != nil {
		return "", err
	}
	if k == "" {
		return p.root, nil
	}
	return filepath.Join(p.root, filepath.FromSlash(k)), nil
}

// mapErr converts a filesystem error into this package's vocabulary. Only
// not-found is translated: a permission error stays a permission error,
// because reporting one as ErrNotFound would turn an outage into silent data
// loss — callers read not-found as "nothing there yet" and write over it.
func mapErr(err error) error { return mapErrFor(err, "") }

// mapErrFor is mapErr with the subject named.
//
// ⛔ A not-found error MUST say WHAT is not found. The first version returned a
// bare ErrNotFound, and Patrol's fleet report went from naming the missing file to
// "failed to read: storage: not found" — an operator reading the report lost the
// one piece of information that made it actionable. Caught by the golden tests.
func mapErrFor(err error, what string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		if what == "" {
			return ErrNotFound
		}
		return fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	return err
}

func (p *POSIX) Get(_ context.Context, key string) ([]byte, error) {
	full, err := p.abs(key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, mapErrFor(err, full)
	}
	return b, nil
}

func (p *POSIX) Put(_ context.Context, key string, data []byte, opts ...PutOption) error {
	o := resolvePut(opts)
	full, err := p.abs(key)
	if err != nil {
		return err
	}
	if full == p.root {
		return fmt.Errorf("%w: cannot write to the root itself", ErrInvalidKey)
	}
	// Parent prefixes are implicit. This is the whole of what the MkdirAll
	// call sites were doing, and why no caller needs to do it any more.
	//
	// ⛔ The MODE matters and is not a detail: a site holds served content and
	// private state under one root, and a parent created at 0755 under .polis
	// would expose keys, drafts and DM state on a shared host. See DirMode.
	if err := p.opts.Err(); err != nil {
		return err
	}
	if err := p.mkdirAllForKey(filepath.Dir(full), key); err != nil {
		return err
	}
	if o.IfAbsent {
		return putIfAbsent(full, data, o.Mode)
	}
	// ⛔ NEVER A PLAIN os.WriteFile. That truncates and then writes, so a
	// reader catches the file empty or half-written, and a crash mid-write
	// truncates it for good — and storage.Append is read-concatenate-Put, so
	// every append (the public index, .versions histories, DM logs) went
	// through it. Temp-and-rename keeps an existing file's mode, as WriteFile
	// did.
	return writeFileAtomic(full, data, o.Mode, true, !o.Rebuildable)
}

// putIfAbsent writes the whole file beside the target, then HARD-LINKS it into
// place. ⭐ link(2) fails with EEXIST if the target exists, atomically and
// across processes, and the target appears complete or not at all — which
// O_CREATE|O_EXCL alone would not give: a reader could see it half-written.
func putIfAbsent(full string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(full), "."+filepath.Base(full)+".new-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // the link, if made, keeps the inode alive
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpName, full); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return err
	}
	// ⭐ A conditional create makes something exactly once — an identity key, a
	// staged rotation key — so it must survive a power loss once it returns.
	return SyncDir(filepath.Dir(full))
}

func (p *POSIX) Exists(_ context.Context, key string) (bool, error) {
	full, err := p.abs(key)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(full); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (p *POSIX) Stat(_ context.Context, key string) (Info, error) {
	full, err := p.abs(key)
	if err != nil {
		return Info{}, err
	}
	fi, err := os.Stat(full)
	if err != nil {
		return Info{}, mapErrFor(err, full)
	}
	return Info{
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
		Mode:    fi.Mode().Perm(),
		IsDir:   fi.IsDir(),
	}, nil
}

func (p *POSIX) List(_ context.Context, prefix string, opts ...ListOption) ([]Entry, error) {
	o := resolveList(opts)
	base, err := p.abs(prefix)
	if err != nil {
		return nil, err
	}
	cleanPrefix, _ := CleanKey(prefix)

	// A missing prefix lists empty rather than failing. "No such directory"
	// and "no keys beneath this prefix" are the same statement to a caller,
	// and an object store cannot tell them apart anyway.
	if _, err := os.Stat(base); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var out []Entry
	join := func(name string) string {
		if cleanPrefix == "" {
			return name
		}
		return cleanPrefix + "/" + name
	}

	if !o.Recursive {
		entries, err := os.ReadDir(base)
		if err != nil {
			return nil, mapErrFor(err, base)
		}
		for _, e := range entries {
			if !o.IncludeAtomicTemps && !e.IsDir() && IsAtomicTemp(e.Name()) {
				continue
			}
			out = append(out, Entry{Key: join(e.Name()), IsDir: e.IsDir()})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
		return out, nil
	}

	// ⛔⛔ PARTIAL RESULTS PLUS AN ERROR, and both halves are load-bearing.
	//
	// The first version aborted the walk on the first unreadable entry and
	// returned nil entries. The callers this replaced were WalkDir callbacks that
	// did `if err != nil { return nil }` — SKIP THAT ENTRY AND CARRY ON — so the
	// tolerance lived in the callback and moving the walk in here swallowed it.
	// One directory at mode 000 under post/ then turned a damaged tenant into a
	// clean-looking one: polis validate reported "the post directory holds no
	// files" and exited 0, Patrol verified nothing and raised no alert, and the
	// private-key leak scan came back empty with a planted key beside it.
	//
	// So: unreadable entries are SKIPPED, the readable ones are returned, and the
	// first failure is also returned. A caller that ignores the error now behaves
	// exactly as it did before the detach; a caller that checks it can say "I
	// could not look at all of this", which no caller could express before.
	var skipped error
	skippedAt := base
	_ = filepath.WalkDir(base, func(fullPath string, d fs.DirEntry, err error) error {
		if err != nil {
			if fullPath == base {
				// The root itself is unreadable: nothing to be tolerant about.
				skipped = err
				return err
			}
			if skipped == nil {
				skipped, skippedAt = err, fullPath
			}
			// Skip this entry — and, if it is a directory we cannot read, its
			// subtree — rather than abandoning the whole listing.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if fullPath == base {
			return nil
		}
		if !o.IncludeAtomicTemps && !d.IsDir() && IsAtomicTemp(d.Name()) {
			return nil
		}
		rel, rErr := filepath.Rel(base, fullPath)
		if rErr != nil {
			if skipped == nil {
				skipped = rErr
			}
			return nil
		}
		out = append(out, Entry{Key: join(filepath.ToSlash(rel)), IsDir: d.IsDir()})
		return nil
	})
	if skipped != nil {
		sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
		// Name the ENTRY that could not be read. An entry that vanished mid-walk
		// maps to not-found, and saying the PREFIX was not found would be false:
		// the prefix is there, and everything else under it has been returned.
		return out, mapErrFor(skipped, skippedAt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (p *POSIX) Delete(_ context.Context, key string, opts ...DeleteOption) error {
	o := resolveDelete(opts)
	full, err := p.abs(key)
	if err != nil {
		return err
	}
	if full == p.root {
		return fmt.Errorf("%w: refusing to delete the root", ErrInvalidKey)
	}
	if _, err := os.Stat(full); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if o.IgnoreMissing {
				return nil
			}
			return ErrNotFound
		}
		return err
	}
	if o.Recursive {
		return os.RemoveAll(full)
	}
	return mapErr(os.Remove(full))
}

func (p *POSIX) RealPath(key string) (string, error) {
	return p.abs(key)
}

func (p *POSIX) Capabilities() Capabilities {
	return Capabilities{
		Modes:       true,
		ModTimes:    true,
		RealPath:    true,
		MaxPutBytes: 0,
	}
}

// compile-time check
var _ Store = (*POSIX)(nil)

// EnsureParent creates the parent prefix of key, at the store's own mode rule.
//
// ⭐⭐⭐ IT EXISTS FOR ONE CALLER AND SAYS SO: atomicfile's POSIX branch writes
// a temp file beside the target with os.WriteFile, which does NOT create
// directories the way Put does. Without this, an atomic write to a key whose
// parent does not exist yet fails with "no such file or directory" — exactly
// how the bundle registry broke when it stopped going through Put.
//
// ⛔ IT USES THE STORE'S OWN MODE RULE, not one the caller picks. That rule is
// what keeps `.polis` at 0700, and a caller guessing 0755 here would create
// the private tree world-readable — the private-tree permission failure by a different route.
func (p *POSIX) EnsureParent(key string) error {
	parent := path.Dir(strings.TrimPrefix(key, "/"))
	if parent == "." || parent == "/" {
		// ⚠️ A key at the store root still needs the ROOT to exist. Put
		// creates it; this used to return nil and leave it missing, so an
		// atomic write into a not-yet-created store failed with ENOENT on the
		// .tmp path — the exact failure EnsureParent exists to prevent, one
		// level up. Found when a test rooted a store at a directory that did
		// not exist yet.
		return os.MkdirAll(p.root, p.opts.DirMode)
	}
	// ⛔⛔ A DECLARED PRIVATE PREFIX GOES THROUGH THE STORE'S EXACT RULE; AN
	// ORDINARY ONE GETS os.MkdirAll, BECAUSE THAT IS WHAT THE CALLERS DID.
	//
	// ⚠️ This asymmetry is not a preference, it is compatibility. Every caller
	// this replaces wrote `os.MkdirAll(dir, 0755)` — umask-respecting — and a
	// differential run proved the difference is observable: under umask 077 a
	// content directory came out 0700 before and 0755 after, until this split.
	// ⭐ Going through the store for `.polis` keeps the one guarantee that
	// must not depend on a process setting: a private prefix is exact, so a
	// permissive umask cannot expose keys.
	if p.opts.isDeclaredPrivate(parent) {
		return p.mkdirAllForKey(filepath.Join(p.root, filepath.FromSlash(parent)), parent)
	}
	return os.MkdirAll(filepath.Join(p.root, filepath.FromSlash(parent)), 0o755)
}

// mkdirAllForKey creates dir and its missing ancestors, giving each the mode its
// own key prefix calls for rather than one mode for the whole chain.
//
// ⭐ Walking down rather than calling os.MkdirAll is the point: ".polis" and
// ".polis/dm" may want different modes from "content", and MkdirAll applies a
// single perm to everything it creates.
func (p *POSIX) mkdirAllForKey(dir, key string) error {
	rel, err := filepath.Rel(p.root, dir)
	if err != nil {
		return err
	}
	if rel == "." || rel == "" {
		return os.MkdirAll(p.root, p.opts.DirMode)
	}
	if err := os.MkdirAll(p.root, p.opts.DirMode); err != nil {
		return err
	}
	cur := p.root
	var keyPrefix string
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		if keyPrefix == "" {
			keyPrefix = seg
		} else {
			keyPrefix += "/" + seg
		}
		mode := p.opts.dirModeFor(keyPrefix)
		if err := os.Mkdir(cur, mode); err != nil {
			if !os.IsExist(err) {
				return err
			}
			continue
		}
		// Mkdir applies umask; a declared private directory must be exact.
		//
		// ⛔⛔ THE CHMOD IS UNCONDITIONAL AND MUST STAY SO. I made it
		// conditional on the prefix being declared private, and the
		// differential caught that immediately: `polis clone` relies on this
		// exactness — its directories went 0755 → 0700 under umask 077.
		//
		// ⚠️ WHAT THAT REVEALED is that the codebase is INCONSISTENT by
		// mechanism, not by intent: a directory created through this store is
		// exact, and one created by a hand-rolled os.MkdirAll respects the
		// umask. One rule here cannot match both, so the nuance belongs where
		// the mechanism CHANGES — see atomicfile.EnsureParent's caller.
		if err := os.Chmod(cur, mode); err != nil {
			return err
		}
	}
	return nil
}
