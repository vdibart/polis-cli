//go:build !windows

package storage

// renameRetryable is false off Windows: rename(2) replaces a file another
// process holds open, so a failure there is not one a retry would cure.
func renameRetryable(error) bool { return false }
