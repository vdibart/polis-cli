// Package atomicfile provides atomic file writes via temp+rename.
//
// Use these helpers anywhere a partial write would corrupt durable state —
// .well-known/polis (the trust anchor) and tenant-private state JSONs under
// .polis/. The temp file is created in the same directory as the target so
// the rename is on the same filesystem (POSIX rename is atomic only across
// the same fs); readers therefore never observe a truncated file.
package atomicfile

import (
	"fmt"
	"os"
)

// WriteFile writes data to path atomically: writes <path>.tmp first, then
// renames onto path. mode applies to the final file. The temp file is
// removed on rename failure.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
