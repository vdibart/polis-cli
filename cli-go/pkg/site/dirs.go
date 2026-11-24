// ⛔ THE PERMISSION RULE FOR A POLIS SITE LIVES HERE, and these MkdirAll calls are
// deliberately not routed through the storage interface.
//
// Two reasons, both load-bearing. They create directories that may stay EMPTY,
// which a prefix store cannot represent. And they are the authority on the
// 0700-under-.polis split that site.Init encodes and that
// TestNothingUnderPolisIsReadableByGroupOrOther enforces: ".polis can not be
// world-readable under any circumstances".
package site

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
)

// PolisDir is the name of the private state directory within a polis site.
const PolisDir = ".polis"

// MkdirAllPrivate creates a directory path, using 0700 permissions for .polis
// and its children, and 0755 for everything else. This prevents .polis from
// being created world-accessible when MkdirAll creates intermediate directories.
//
// ⛔ PERMISSION RULE: this function IS the authority on that split, and it creates
// directories that are legitimately empty. Both reasons keep it off the storage
// interface.
func MkdirAllPrivate(site storage.Site, path string) error {
	rel, err := filepath.Rel(site.Dir(), path)
	if err != nil {
		// Path is not under the site root — use default permissions
		return os.MkdirAll(path, 0755) //nolint:gosec // G301: not under .polis, so the public default
	}

	// Check if this path is under .polis
	if rel == PolisDir || strings.HasPrefix(rel, PolisDir+string(filepath.Separator)) {
		// Ensure .polis root exists with restricted permissions first
		polisRoot := site.Path(PolisDir)
		if err := os.MkdirAll(polisRoot, 0700); err != nil {
			return err
		}
		// Create the rest of the path with restricted permissions
		return os.MkdirAll(path, 0700)
	}

	// Stays: creates a directory that may remain EMPTY, and carries the
	// 0700-under-.polis rule that nothing may widen.
	return os.MkdirAll(path, 0755) //nolint:gosec // G301: not under .polis (handled above), so the public default
}
