package storage

// SyncDir is a no-op on Windows. ⛔ os.Open gives a read-only handle to a
// directory there, and syncing it fails with "Access is denied", so every
// caller would report an error after its write had landed. NTFS journals the
// rename itself; there is no directory fsync to make.
func SyncDir(dir string) error { return nil }
