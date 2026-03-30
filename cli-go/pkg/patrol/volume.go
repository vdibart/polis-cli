package patrol

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DefaultMinFreeBytes is the default free space warning threshold (100MB).
const DefaultMinFreeBytes int64 = 100 * 1024 * 1024

// CheckVolume verifies that a volume mount point is healthy:
// exists, is a directory, is writable, and has sufficient free space.
func CheckVolume(mountPoint string, minFreeBytes int64) CheckStatus {
	info, err := os.Stat(mountPoint)
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("mount point not found: %v", err)}
	}
	if !info.IsDir() {
		return CheckStatus{OK: false, Message: "mount point is not a directory"}
	}

	// Check free space via statfs
	var stat syscall.Statfs_t
	if err := syscall.Statfs(mountPoint, &stat); err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("statfs failed: %v", err)}
	}
	freeBytes := int64(stat.Bavail) * int64(stat.Bsize)
	if freeBytes < minFreeBytes {
		return CheckStatus{
			OK:      false,
			Message: fmt.Sprintf("low free space: %d bytes (threshold: %d)", freeBytes, minFreeBytes),
		}
	}

	// Verify writable by creating and removing a temp file
	tmpPath := filepath.Join(mountPoint, ".patrol-write-test")
	f, err := os.Create(tmpPath)
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("not writable: %v", err)}
	}
	f.Close()
	os.Remove(tmpPath)

	return CheckStatus{OK: true}
}
