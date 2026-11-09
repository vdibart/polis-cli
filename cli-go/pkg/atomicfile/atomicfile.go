// Package atomicfile provides atomic file writes via temp+rename.
//
// Use these helpers anywhere a partial write would corrupt durable state —
// .well-known/polis (the trust anchor) and tenant-private state JSONs under
// .polis/. The temp file is created in the same directory as the target so
// the rename is on the same filesystem (POSIX rename is atomic only across
// the same fs); readers therefore never observe a truncated file.
package atomicfile

import (
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// WriteFile writes data to path atomically: writes a temp file beside it,
// created exclusively under a unique dot-led name, syncs it, then renames it
// onto path. mode applies to the final file. The temp file is removed on
// failure. ⚠️ A path that is a symlink is REPLACED by a regular file, never
// written through, so a link planted in a tenant directory cannot redirect the
// write (storage.Put, by contrast, writes through one). Write, not this
// function, also syncs the directory.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	// ⭐ One implementation of temp-and-rename, in pkg/storage. ⛔ The temp name
	// is unique per write: a fixed "<path>.tmp" let two writers to one key share
	// a temp file and tear the target.
	return storage.WriteFileAtomic(path, data, mode, false)
}
