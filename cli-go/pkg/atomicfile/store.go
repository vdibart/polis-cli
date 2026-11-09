package atomicfile

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Write writes data at key, atomically, through whatever backend the site is on.
//
// ⭐⭐⭐ THIS IS THE WHOLE OF IT, AND IT IS SHORT FOR A REASON THE INTERFACE
// DOCUMENT ALREADY GAVE: temp-and-rename exists to serve atomicity on a
// filesystem, and **a single-object PUT is already atomic**. So on a backend
// with no real path there is nothing to arrange — the store's own Put is the
// atomic write, and reaching for a temp key would be strictly worse: it would
// double the requests and leave litter behind a failure.
//
// ⛔ NOTHING IS WEAKENED. The guarantee callers depend on is that a reader
// never observes a truncated file. POSIX gets it from rename; an object store
// gets it from the PUT being all-or-nothing at the API. ⚠️ What an object store
// does NOT give is a cross-key transaction, and neither did rename — a caller
// that needs two files to change together never had that and must not start
// assuming it.
//
// ⚠️ WHY THIS WAS URGENT RATHER THAN TIDY. bundle.SaveRegistry writes through
// atomicfile, and NewPageRenderer calls it whenever a site has no theme set.
// On Vercel the filesystem is READ-ONLY outside /tmp, so the first render of a
// new blob-backed instance would have failed on a write it did not need to
// make — and where it happened to succeed, the registry landed somewhere the
// site would never read it back from.
func Write(ctx context.Context, site storage.Site, key string, data []byte, mode fs.FileMode) error {
	if !site.Valid() {
		return fmt.Errorf("atomicfile: site handle is not usable")
	}
	st := site.Store()

	// ⭐ No filesystem: the Put IS the atomic write.
	if !st.Capabilities().RealPath {
		if err := st.Put(ctx, key, data, storage.WithMode(mode)); err != nil {
			return fmt.Errorf("atomicfile: put %s: %w", key, err)
		}
		return nil
	}

	// ⚠️ A filesystem: keep temp-and-rename, and keep it on the SAME
	// directory as the target, because POSIX rename is atomic only within one
	// filesystem. ⛔ That constraint is the reason this helper exists at all,
	// and it is why the temp key is derived from the target rather than put
	// somewhere tidy like a /tmp prefix.
	path, err := st.RealPath(key)
	if err != nil {
		return fmt.Errorf("atomicfile: backend declares a real path but refused one for %s: %w",
			key, err)
	}
	// ⛔ THE PARENT MUST EXIST FIRST, and the store creates it at ITS OWN mode
	// rule — the one that keeps `.polis` at 0700. ⚠️ os.WriteFile below does
	// not create directories the way Put does, which is how the bundle
	// registry broke the moment it stopped going through Put.
	type parentEnsurer interface{ EnsureParent(string) error }
	if pe, ok := st.(parentEnsurer); ok {
		if err := pe.EnsureParent(key); err != nil {
			return fmt.Errorf("atomicfile: create parent of %s: %w", key, err)
		}
	}
	if err := WriteFile(path, data, mode); err != nil {
		return err
	}
	// ⭐ Durable once it returns: the rename is synced too, so a caller may
	// rely on the order of this write and its next one.
	return storage.SyncDir(filepath.Dir(path))
}
