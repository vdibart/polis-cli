package storage

import "path/filepath"

// Site is a handle on one polis site: where it is, and what it is stored on.
//
// ⭐⭐⭐ IT EXISTS BECAUSE A PATH CANNOT CARRY A BACKEND. The detach routed every
// filesystem call through this package, and that claim is true — but 372 of
// those calls go through the PATH-ADDRESSED helpers (ReadAtPath, WriteAtPath,
// PathAbsent, ListDir, …), and each of those ends in NewPOSIX(root). So they
// went THROUGH the seam without getting BEHIND it: a second backend cannot
// reach them.
//
// ⛔ AND THEY HAVE NOWHERE TO INJECT ONE. `WriteAtPath(abs, data)` is a free
// function; there is no receiver to hold a dependency. The alternative
// considered was a global map of root to store — ⛔ a service locator whose
// failure mode in a process serving many tenants is a CROSS-TENANT READ OR
// WRITE, which is a bug this codebase has actually had, and whose missed call
// sites COMPILE FINE and silently write to the wrong backend.
//
// ⭐⭐ A TYPE CANNOT BE HALF-ADOPTED, and that is the whole argument. 154
// exported functions already take `siteDir string` first, so the call sites
// already carry the site identity — it is merely typed as the weakest thing
// that could carry it. Changing that TYPE (rather than adding a parameter)
// means no call-chain threading, and the compiler refuses the half-converted
// state outright:
//
//	cannot use site.Dir() (value of type string) as storage.Site value
//
// "Half the site on blob and half on local disk" therefore cannot compile.
//
// ⚠️ A Site is a VALUE and is cheap to copy. It is not safe to mutate after
// construction, and nothing offers a way to.
type Site struct {
	// dir is the site root, absolute.
	//
	// ⚠️ Retained for Dir(), which is a migration affordance with an expiry —
	// see the comment there.
	dir string

	// store is what the site's bytes actually live on.
	store Store
}

// At returns a POSIX-backed site handle: a laptop, and the hosted deployment
// as it stands today.
//
// ⛔ NewSite, never NewPOSIX. `.polis` must not be world-readable, and the
// constructor is what carries that rule — a store built the other way creates
// parents at 0755 and would expose keys, drafts and DM state on a shared host.
func At(dir string) Site {
	// ⛔⛔ AN EMPTY ROOT IS A CALLER BUG, NOT "THE CURRENT DIRECTORY".
	//
	// ⚠️ cleanAbs("") returns ".", so without this an empty root quietly
	// became the working directory and the handle reported Valid. Two bundle
	// tests existed specifically to assert that an empty site dir is an error,
	// and the conversion silently disarmed them — which is how it was found.
	//
	// ⭐ Returning the zero Site is what makes Valid() answer the question it
	// was written for, and a caller that checks gets a clear refusal instead
	// of operating on whatever directory the process happens to be in.
	if dir == "" {
		return Site{}
	}
	abs := cleanAbs(dir)
	return Site{dir: abs, store: NewSite(abs)}
}

// On returns a site handle backed by an explicit store.
//
// ⭐ This is how a blob-backed instance is built, at the ONE place per
// deployment where a site is first identified: the CLI at startup, the
// self-hosted server in Initialize, and hosted in loadTenantHandler, per
// tenant. ⚠️ That single construction point is the question a global resolver
// would have left open, answered structurally instead.
func On(dir string, store Store) Site {
	return Site{dir: cleanAbs(dir), store: store}
}

// Store is the site's backing store, and the thing new code should use.
func (s Site) Store() Store { return s.store }

// WithStore is the same site seen through another store, such as a read cache
// over its own (ReadCache). ⭐ The site's identity (ID, and the root behind
// Dir) is unchanged, so nothing that keys on the site can tell them apart.
func (s Site) WithStore(store Store) Site { return Site{dir: s.dir, store: store} }

// Valid reports whether the handle was constructed rather than zero-valued.
//
// ⭐ A zero Site has a nil store, and the failure that produces is a nil
// dereference a long way from the mistake that caused it. A function accepting
// a Site from outside its own package should check, and say what was missing.
func (s Site) Valid() bool { return s.dir != "" && s.store != nil }

// Dir returns the site root as an absolute path.
//
// ⛔⛔ A MIGRATION AFFORDANCE WITH AN EXPIRY — NOT PART OF THE INTERFACE.
//
// ⭐ It exists so a function can convert its SIGNATURE without converting its
// INTERNALS in the same step, which is what lets this migration proceed one
// package at a time with everything compiling throughout. ⚠️ But every call is
// a place that still thinks in paths, and on a backend with no filesystem a
// path means nothing.
//
// ⛔ So it is CENSUSED (cmd/polis/site_dir_census_test.go): every caller is
// listed with a reason and a new one fails the build. ⭐⭐ The census is also
// the progress bar — the count only goes down, and when it reaches the handful
// that genuinely need a real path, the migration is finished.
func (s Site) Dir() string { return s.dir }

// ID is the site's identity for in-process bookkeeping: a map key, a lock
// name. It is stable across handles built for the same site, including a
// hosted tenant handler rebuilt after eviction.
//
// ⛔ IT IS NEVER A LOCATION, and it is shaped so it cannot be used as one: the
// "site:" prefix makes any attempt to open it fail at once instead of reading
// the process working directory. A caller that needs a path wants Dir(), which
// is censused.
func (s Site) ID() string {
	if !s.Valid() {
		return ""
	}
	return "site:" + s.dir
}

// Path joins site-relative elements onto the root.
//
// ⚠️ The same affordance as Dir, with the same expiry, and censused the same
// way. ⭐ Prefer passing the relative key to the Store.
func (s Site) Path(elem ...string) string {
	return filepath.Join(append([]string{s.dir}, elem...)...)
}
