package main

import (
	"os"
	"path/filepath"
)

// absentClosedSource reports that rel is not in this tree because this is the
// PUBLIC repository, where closed source (the hosted service, the DS) and
// plans/ are simply absent. A census entry naming such a file says nothing
// there about whether it is stale; in the private tree a missing file is a
// stale entry as usual.
func absentClosedSource(rel string) bool {
	if _, err := os.Stat(filepath.Join(repoRoot, "plans")); !os.IsNotExist(err) {
		return false
	}
	_, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	return os.IsNotExist(err)
}
