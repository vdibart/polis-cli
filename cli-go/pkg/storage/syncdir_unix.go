//go:build !windows

package storage

import "os"

// SyncDir fsyncs a directory, so a rename, link or unlink inside it survives a
// power loss. ⭐ A synced file renamed into place is not durable until its
// directory entry is; without this, a crash can leave the old name, or none.
// It runs after a conditional create (Put with IfAbsent) and after every
// atomicfile write, and costs one extra fsync each time. ⚠️ A plain Put does
// not call it.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
