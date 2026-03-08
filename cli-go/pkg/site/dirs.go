package site

import (
	"os"
	"path/filepath"
	"strings"
)

// PolisDir is the name of the private state directory within a polis site.
const PolisDir = ".polis"

// MkdirAllPrivate creates a directory path, using 0700 permissions for .polis
// and its children, and 0755 for everything else. This prevents .polis from
// being created world-accessible when MkdirAll creates intermediate directories.
func MkdirAllPrivate(siteDir, path string) error {
	rel, err := filepath.Rel(siteDir, path)
	if err != nil {
		// Path is not under siteDir — use default permissions
		return os.MkdirAll(path, 0755)
	}

	// Check if this path is under .polis
	if rel == PolisDir || strings.HasPrefix(rel, PolisDir+string(filepath.Separator)) {
		// Ensure .polis root exists with restricted permissions first
		polisRoot := filepath.Join(siteDir, PolisDir)
		if err := os.MkdirAll(polisRoot, 0700); err != nil {
			return err
		}
		// Create the rest of the path with restricted permissions
		return os.MkdirAll(path, 0700)
	}

	return os.MkdirAll(path, 0755)
}
