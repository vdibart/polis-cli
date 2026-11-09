//go:build windows

package storage

import (
	"errors"
	"syscall"
)

// renameRetryable reports a rename Windows refused because the target is
// open in another process (ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION),
// which clears once that process closes it. The in-place write this replaced
// succeeded there, so a brief retry keeps that behaviour under concurrent
// reads.
func renameRetryable(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == 5 || errno == 32
}
