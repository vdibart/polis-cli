// Package storage is the single seam between polis and where its bytes live.
//
// A polis site is a tree of files. On a laptop or a VPS that tree is a real
// filesystem; on a stateless host it is an object store with no directories,
// no file modes and no rename. This package is the interface both satisfy, so
// the rest of the codebase stops caring which it is talking to.
//
// # A key is the polis path
//
// Keys are slash-separated, relative to the site root, with no leading slash
// and no "." or ".." segments:
//
//	content/pub.polis.core/post/20261001/hello.md
//
// That is not a convention, it is a requirement. A published site serves a post
// at exactly that path, so a storage key that matched anything else would make
// an export a reconstruction rather than a copy — and a reconstruction is where
// signatures go to die. Export is List + Get; import is Put at the same key.
//
// # There are no directories
//
// Nothing creates one. A filesystem backend makes parent directories implicitly
// inside Put; an object store does nothing, because a prefix is not a thing you
// create. IsDir survives on Entry and Info because callers genuinely ask it,
// but it means "keys exist beneath this prefix", which is answerable on both.
//
// # What a backend cannot do, it refuses
//
// File modes, modification times and real filesystem paths are not available
// everywhere. A backend declares what it has in Capabilities and returns
// ErrUnsupported for the rest. It never silently does nothing, because the
// callers that depend on those are security checks: Medic heals file
// permissions, and Patrol uses modification times to decide which posts it can
// skip re-verifying. A check that quietly stops checking is worse than one that
// fails.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// ErrNotFound is returned by Get, Stat and Delete when the key does not exist.
//
// ⛔ Test for it with errors.Is, NEVER with os.IsNotExist. os.IsNotExist does
// not unwrap — it understands only *PathError and friends — so it answers false
// for a missing object the moment a backend wraps its error, and a caller that
// branches on it takes the "a real failure happened" arm for an ordinary
// absence. That mistake was made three times during the detach, and the third
// time it reached five packages: a 500 where a 404 belonged, a wrong Patrol
// report, and key provisioning that refused to provision.
//
// ⭐ It WRAPS fs.ErrNotExist, so the modern idiom is correct by default:
//
//	errors.Is(err, fs.ErrNotExist)   // true for a missing key
//	errors.Is(err, storage.ErrNotFound)
//
// The wrap is honest rather than a convenience: for every backend, "this key
// has no value" and "this does not exist" are the same claim.
var ErrNotFound = fmt.Errorf("storage: not found%w", errNotFoundSuffix{})

// errNotFoundSuffix makes ErrNotFound wrap fs.ErrNotExist while keeping its
// message exactly "storage: not found" — the string is in golden test output.
type errNotFoundSuffix struct{}

func (errNotFoundSuffix) Error() string { return "" }
func (errNotFoundSuffix) Is(target error) bool {
	return target == fs.ErrNotExist
}

// ErrExists is returned by a conditional create (IfAbsent) when something is
// already at the key. ⭐ It is the loser of a race, not a failure.
var ErrExists = errors.New("storage: already exists")

// ErrUnsupported is returned when a backend cannot honour an operation or an
// option that its Capabilities do not claim. It is deliberately loud: the
// alternative is a mode that silently is not set, or a modification time that
// is silently zero, on code paths that exist to detect tampering.
var ErrUnsupported = errors.New("storage: unsupported by this backend")

// ErrInvalidKey is returned for a key that is absolute, escapes the root, or is
// otherwise not a site-relative slash path. See CleanKey.
var ErrInvalidKey = errors.New("storage: invalid key")

// Entry is one result from List. It carries only what callers actually read:
// every one of the listing sites this interface replaces used a name and an
// is-directory flag and nothing else, so returning fs.DirEntry would be
// promising metadata no object store can cheaply provide.
type Entry struct {
	// Key is the full key, not a basename, so a caller can pass it straight
	// back to Get without rejoining anything.
	Key string

	// IsDir reports that keys exist beneath this one. On a filesystem it is a
	// real directory; on an object store it is a prefix with children.
	IsDir bool

	// Size and ModTime are what Stat would report for a key, carried by an
	// object store's listing because its list call returns them anyway.
	// ⚠️ ZERO UNLESS THE BACKEND FILLED THEM: a zero ModTime means "not
	// reported", never "unchanged", and a caller that needs the values Stats
	// the key. The POSIX store leaves them zero.
	Size    int64
	ModTime time.Time
}

// ⛔ Info CANNOT SEE A SYMLINK, for two independent reasons, and anything that
// needs to must stay on os.Lstat and say so:
//
//  1. Stat FOLLOWS links (it is os.Stat, not os.Lstat), so it reports the target.
//  2. Mode is Perm() only — the TYPE BITS are discarded, so ModeSymlink is not
//     merely unset, it is unrepresentable.
//
// That is deliberate: a key-value backend has no links, so an interface that
// pretended to answer would answer wrongly. Patrol's symlink detection and
// Medic's quarantine both depend on the real distinction.
// Info is what Stat reports. Size is always meaningful. ModTime and Mode are
// zero unless the backend's Capabilities claim them.
type Info struct {
	Size    int64
	ModTime time.Time
	Mode    fs.FileMode
	IsDir   bool
}

// Capabilities describes what a backend can actually do. These are properties
// of the BACKEND, not of the deployment mode, and they are deliberately
// separate from the mode-derived capability set the web interface fetches:
// mixing the two would make "can this store file modes" depend on where the
// site is hosted, which is not true.
type Capabilities struct {
	// Modes reports that file modes are stored and enforced. False on an
	// object store, which makes Medic's permission healing inapplicable
	// rather than failing.
	Modes bool

	// ModTimes reports that Stat returns a meaningful ModTime. False means
	// Patrol cannot use mtime to skip unchanged content and must re-verify.
	ModTimes bool

	// RealPath reports that RealPath returns a usable filesystem path. Only
	// archive and zip streaming need one; everything else goes through this
	// interface.
	RealPath bool

	// MaxPutBytes is the largest value Put accepts, or 0 for unbounded. A
	// serverless host caps request bodies well below the size of a large
	// post with embedded media.
	MaxPutBytes int64
}

// Store is the interface every backend implements.
//
// Every method takes a context because a backend is a network call and a
// serverless invocation has a deadline. The filesystem backend ignores it; the
// interface must not foreclose it.
type Store interface {
	// Get returns the whole value at key, or ErrNotFound.
	Get(ctx context.Context, key string) ([]byte, error)

	// Put writes data at key, creating or replacing it. Parent prefixes are
	// implicit: a caller never creates one.
	Put(ctx context.Context, key string, data []byte, opts ...PutOption) error

	// Exists reports whether key is present. It is separate from Stat
	// because two thirds of the call sites it replaces wanted only this, and
	// an object store answers it with a cheaper request than a full stat.
	Exists(ctx context.Context, key string) (bool, error)

	// Stat returns metadata for key, or ErrNotFound. Fields the backend
	// cannot report are zero; check Capabilities rather than guessing.
	Stat(ctx context.Context, key string) (Info, error)

	// List returns the direct children of prefix, or everything beneath it
	// with Recursive. An empty prefix lists the whole store. A missing
	// prefix is an empty result, not ErrNotFound: "nothing there" and "no
	// such directory" are the same statement about a prefix.
	List(ctx context.Context, prefix string, opts ...ListOption) ([]Entry, error)

	// Delete removes key, or everything beneath it with Recursive. Deleting
	// a key that does not exist returns ErrNotFound unless IgnoreMissing.
	Delete(ctx context.Context, key string, opts ...DeleteOption) error

	// RealPath returns a filesystem path for key, for the few operations
	// that genuinely need one. Returns ErrUnsupported unless
	// Capabilities().RealPath. Reach for this last: it is the escape hatch,
	// and an escape hatch that reads like an accessor gets used like one.
	RealPath(key string) (string, error)

	// Capabilities reports what this backend can do.
	Capabilities() Capabilities
}

// PutOption configures a Put.
type PutOption func(*PutOptions)

// PutOptions is the resolved set of Put settings.
type PutOptions struct {
	// Mode is the file mode to apply. Ignored by backends without
	// Capabilities().Modes; it is not an error to ask, because the same
	// caller runs against both.
	Mode fs.FileMode

	// IfAbsent makes the Put a CONDITIONAL CREATE: it writes only if nothing
	// is at the key, and otherwise returns ErrExists without touching it.
	IfAbsent bool

	// Rebuildable marks output that can be regenerated from the site's
	// sources. See Rebuildable().
	Rebuildable bool
}

// Rebuildable marks a Put as DERIVED output — a rendered page, a regenerated
// index — that the next render rebuilds from the site's sources. On POSIX the
// write is still atomic to readers (temp file, rename) but is not fsynced.
//
// ⭐ The fsync is what makes a write survive a power loss. Each one is cheap
// but a render pays it per page: a forced render of 300 posts measured about
// 6% slower with it (ext4 on NVMe; a network volume costs more). A page lost
// to a power cut is regenerated; a
// source, a key or a signed file is not, so ⛔ never mark one of those.
// Backends with no fsync ignore it.
func Rebuildable() PutOption {
	return func(o *PutOptions) { o.Rebuildable = true }
}

// IfAbsent makes a Put create-if-absent, enforced by the BACKEND rather than
// by a read-then-write.
//
// ⛔⛔ THIS IS WHAT STOPS TWO IDENTITIES FOR ONE SITE. Two concurrent cold
// starts that each find no key and each generate one would both "win" a
// read-then-write, and the loser's signatures would verify against nothing.
// Serverless makes concurrent cold starts ordinary. ⚠️ An in-process lock
// does not help: the racing starts are separate processes, so the exclusion
// has to live in the store — a hard link on POSIX, Vercel Blob's refusal to
// overwrite, the object store's own lock.
//
// ⭐ Every backend proves it in the contract suite, including under
// concurrency, so a backend that silently ignored it would fail there.
func IfAbsent() PutOption {
	return func(o *PutOptions) { o.IfAbsent = true }
}

// WithMode sets the file mode on a Put. Backends without mode support ignore
// it: a caller writing a private key asks for 0600 everywhere, and the backend
// that cannot honour it says so through Capabilities rather than by failing a
// write that would otherwise be correct.
func WithMode(mode fs.FileMode) PutOption {
	return func(o *PutOptions) { o.Mode = mode }
}

// ListOption configures a List.
type ListOption func(*ListOptions)

// ListOptions is the resolved set of List settings.
type ListOptions struct {
	Recursive          bool
	IncludeAtomicTemps bool
}

// Recursive makes List return every key beneath the prefix rather than its
// direct children. This is the difference between the ReadDir and the Walk
// call sites it replaces.
func Recursive() ListOption {
	return func(o *ListOptions) { o.Recursive = true }
}

// IncludeAtomicTemps makes a POSIX List return the temp files an atomic write
// leaves beside its target while it runs (IsAtomicTemp). ⚠️ Only for the code
// that cleans up after a killed writer; every other caller must not see them,
// or a sweep racing a write reports a file that was never created.
func IncludeAtomicTemps() ListOption {
	return func(o *ListOptions) { o.IncludeAtomicTemps = true }
}

// DeleteOption configures a Delete.
type DeleteOption func(*DeleteOptions)

// DeleteOptions is the resolved set of Delete settings.
type DeleteOptions struct {
	Recursive     bool
	IgnoreMissing bool
}

// DeleteRecursive removes everything beneath the key as well.
func DeleteRecursive() DeleteOption {
	return func(o *DeleteOptions) { o.Recursive = true }
}

// IgnoreMissing makes Delete return nil instead of ErrNotFound for a key that
// is already absent, which is what most cleanup paths mean.
func IgnoreMissing() DeleteOption {
	return func(o *DeleteOptions) { o.IgnoreMissing = true }
}

func resolvePut(opts []PutOption) PutOptions {
	o := PutOptions{Mode: 0o644}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

func resolveList(opts []ListOption) ListOptions {
	var o ListOptions
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

func resolveDelete(opts []DeleteOption) DeleteOptions {
	var o DeleteOptions
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// DirMode settings govern the permissions a backend gives to parent prefixes it
// creates on a Put. They exist because a polis site is TWO TREES with different
// audiences under one root: content/ is served to the world, and .polis holds
// keys, drafts, DM state and feed cursors.
//
// ⛔ .polis MUST NOT be world-readable under any circumstances. site.Init has
// always encoded that — 0700 at or under .polis, 0755 elsewhere — but nothing
// enforced it, and a Put that created a parent at 0755 would have broken it
// silently on a shared host, where 0755 on .polis/dm lets any local account list
// a user's conversations.

// WithDirMode sets the permissions used for parent prefixes that do not match a
// private prefix. The default is 0755, which is what served content needs.
func WithDirMode(perm fs.FileMode) StoreOption {
	return func(o *StoreOptions) { o.DirMode = perm }
}

// WithPrivatePrefix declares that keys under prefix get perm on every parent
// directory the backend creates for them, overriding DirMode. The
// longest-matching prefix wins, so a nested declaration can tighten further.
//
// ⛔ A backend that cannot honour a declared private prefix MUST refuse at
// construction. An option that is silently ignored is worse than no option: the
// failure it hides is exactly the invisible one this exists to prevent.
func WithPrivatePrefix(prefix string, perm fs.FileMode) StoreOption {
	return func(o *StoreOptions) {
		if o.PrivatePrefixes == nil {
			o.PrivatePrefixes = map[string]fs.FileMode{}
		}
		k, err := CleanKey(prefix)
		if err != nil || k == "" {
			o.BadPrivatePrefix = prefix
			return
		}
		// ⛔ A private mode must still let the OWNER traverse and write, because
		// the store writes into these directories. 0500 on .polis/dm would make
		// the very Put that created it fail with EACCES on the next level down —
		// found by a test that tried it. Refuse at construction rather than at a
		// write months later.
		if perm&0o300 != 0o300 {
			o.BadPrivateMode = prefix + " at " + perm.String()
			return
		}
		o.PrivatePrefixes[k] = perm
	}
}

// StoreOptions are construction-time settings shared by backends.
type StoreOptions struct {
	DirMode          fs.FileMode
	PrivatePrefixes  map[string]fs.FileMode
	BadPrivatePrefix string
	BadPrivateMode   string
}

// Err returns the construction error a backend must surface, or nil.
//
// ⛔ Surfacing it is not optional. A silently ignored privacy option reproduces
// the exact invisible failure the option exists to prevent.
func (o StoreOptions) Err() error {
	if o.BadPrivatePrefix != "" {
		return fmt.Errorf("%w: private prefix %q is not a usable key", ErrInvalidKey, o.BadPrivatePrefix)
	}
	if o.BadPrivateMode != "" {
		return fmt.Errorf("%w: private prefix %s would prevent the store writing into it; "+
			"a private mode must include owner write and execute", ErrInvalidKey, o.BadPrivateMode)
	}
	return nil
}

// StoreOption configures a store at construction.
type StoreOption func(*StoreOptions)

func resolveStore(opts []StoreOption) StoreOptions {
	o := StoreOptions{DirMode: 0o755}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// isDeclaredPrivate reports whether key falls under a prefix declared private.
//
// ⛔⛔ IT EXISTS TO SEPARATE TWO THINGS THAT WERE CONFLATED, and a differential
// run found the conflation. A declared private directory must be EXACT — a
// `.polis` at 0755 because the process umask was permissive is the bug
// WithPrivatePrefix exists to prevent. But an ORDINARY content directory must
// still respect the operator's umask: `umask 077` is an intent, and chmod-ing
// public content to exactly 0755 overrides it in the MORE PERMISSIVE
// direction.
//
// ⚠️ The regression was real and the harness caught it: under umask 077,
// content/pub.polis.core/tag went from 0700 to 0755 the moment parent creation
// moved from os.MkdirAll into the store.
func (o StoreOptions) isDeclaredPrivate(key string) bool {
	for p := range o.PrivatePrefixes {
		if key == p || strings.HasPrefix(key, p+"/") {
			return true
		}
	}
	return false
}

// dirModeFor returns the mode to create parents of key with, honouring the
// longest matching private prefix.
func (o StoreOptions) dirModeFor(key string) fs.FileMode {
	best, mode := -1, o.DirMode
	for p, m := range o.PrivatePrefixes {
		if key == p || strings.HasPrefix(key, p+"/") {
			if len(p) > best {
				best, mode = len(p), m
			}
		}
	}
	return mode
}
