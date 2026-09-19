package site

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Signet epic 11 asked that the `agents` pointer survive every Go writer of
// .well-known/polis. That is now one row of a general guarantee: see
// TestEveryWellKnownWriterPreservesEveryMember, which runs every writer over a
// fixture carrying every member — `agents` included, and members nobody has
// invented yet.

// Every Go writer of .well-known/polis goes through SaveWellKnown or
// SaveWellKnownRaw — the one point that runs the identity-change check (D5).
// This scan fails the day someone writes the file another way.
func TestNoGoWriterBypassesTheWellKnownWriters(t *testing.T) {
	roots := []string{filepath.Join("..", ".."), filepath.Join("..", "..", "..", "webapp")}
	// Any file-writing call whose line names the identity document by any of
	// the spellings the code base uses. Broader than a caller grep on purpose:
	// the last bypass was a direct atomicfile write, which is not a "caller".
	directWrite := regexp.MustCompile(`(WriteFile|Rename|Create|OpenFile)\(.*(well-known|wellKnown|wkPath|wkDir|"polis"\))`)
	// ⭐ The one allowed direct write, and why: `polis clone` stores someone
	// ELSE's identity document byte for byte as fetched. It preserves every
	// member by construction, and the identity guard must not run — a clone
	// refreshing after the origin rotated is supposed to take the new key.
	allowed := map[string]string{
		"pkg/clone/clone.go": "a clone mirrors the origin's published bytes verbatim",
	}
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			for i, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				// ⛔ No exceptions. bundle.MigrateActiveThemeToRegistry used to be
				// one (a direct write, "because site imports bundle"); the
				// migration now lives in site and uses the guarded writer.
				rel := filepath.ToSlash(path)
				exempt := false
				for suffix := range allowed {
					if strings.HasSuffix(rel, suffix) {
						exempt = true
					}
				}
				if directWrite.MatchString(line) && !exempt {
					t.Errorf("%s:%d writes .well-known/polis directly: %s", path, i+1, trimmed)
				}
			}
			return nil
		})
	}
}
