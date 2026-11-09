// Package contentpath decides which site-relative paths an editing action may
// name.
//
// ⭐ One rule for every surface that takes a path from a caller — the webapp's
// editor routes and the ops engine (/v1/ actions) alike — so a path one accepts
// is never refused by the other, and the second surface cannot be the one that
// forgot to check.
package contentpath

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrTraversal is a path that climbs or carries a NUL.
var ErrTraversal = errors.New("invalid path: traversal not allowed")

// ErrDisallowed is a path outside the content an editor may touch.
var ErrDisallowed = errors.New("invalid path: outside allowed roots")

// allowedPrefixes are the roots an editing action may name.
var allowedPrefixes = []string{
	"content/pub.polis.core/post/",
	"content/pub.polis.core/comment/",
	".polis/bundles/pub.polis.core/posts/drafts/",
	"posts/",    // Mount point (rendered output)
	"comments/", // Mount point (rendered output)
}

// Validate accepts root-level markdown and HTML (index.md, about.html) and
// anything under the allowed roots, and refuses traversal and NUL.
func Validate(path string) error {
	// Canonicalize first to normalize encoded traversals (e.g., ./, //)
	path = filepath.Clean(path)
	if strings.Contains(path, "..") || strings.Contains(path, "\x00") {
		return ErrTraversal
	}
	if (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".html")) && !strings.Contains(path, "/") {
		return nil
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return nil
		}
	}
	return ErrDisallowed
}
