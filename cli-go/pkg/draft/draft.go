// Package draft keeps the owner's unfinished post writing: saved, updated by
// id, and discarded. A draft is private, unsigned markdown under .polis (never
// served), one file per draft.
//
// ⛔ DRAFTS ARE THE OWNER'S ALONE. Saving one is on the owner-only list (v5
// spec 1.2): agents act directly and keep no drafts for approval (decided
// 2026-11-16), so the operations that call this are never reachable with an
// agent's token. Reading drafts is a PQL read ('all drafts from me'), as every
// read is.
package draft

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Dir is where post drafts live, as a site key.
const Dir = ".polis/bundles/pub.polis.core/posts/drafts"

// MaxSize is the largest draft accepted, the same as a post's markdown.
const MaxSize = 256 << 10

// ErrTooLarge is a draft over MaxSize.
var ErrTooLarge = errors.New("draft content too large (max 256KB)")

var idUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// SanitizeID makes an id safe as a file name: anything but letters, digits,
// '_' and '-' becomes '-'. It never fails; "" stays "".
func SanitizeID(id string) string { return idUnsafe.ReplaceAllString(id, "-") }

// Key is a draft's site key.
func Key(id string) string { return Dir + "/" + SanitizeID(id) + ".md" }

// Save writes a draft and answers its id: a new one when id is "", the same
// one (replaced) when given.
func Save(site storage.Site, id, markdown string) (string, error) {
	if len(markdown) > MaxSize {
		return "", ErrTooLarge
	}
	if id == "" {
		id = fmt.Sprintf("draft-%d", time.Now().UnixNano())
	}
	id = SanitizeID(id)
	if err := site.Store().Put(context.Background(), Key(id), []byte(markdown)); err != nil {
		return "", fmt.Errorf("save draft: %w", err)
	}
	return id, nil
}

// Discard removes a draft. A draft that is not there is fs.ErrNotExist, so a
// caller can tell "nothing to discard" from a failure.
func Discard(site storage.Site, id string) error {
	id = SanitizeID(id)
	if id == "" {
		return fmt.Errorf("draft id is required")
	}
	ctx := context.Background()
	if _, err := site.Store().Stat(ctx, Key(id)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("draft %q: %w", id, fs.ErrNotExist)
		}
		return fmt.Errorf("discard draft: %w", err)
	}
	if err := site.Store().Delete(ctx, Key(id)); err != nil {
		return fmt.Errorf("discard draft: %w", err)
	}
	return nil
}
