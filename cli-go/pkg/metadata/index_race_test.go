package metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The production shape: on hosted, Medic runs in the same process as the
// request handlers and rebuilds the public index on every sweep, while a
// publisher appends to it.
//
// ⚠️ WHAT THIS TEST DOES AND DOES NOT PROVE, stated because I checked rather
// than assumed. It exercises the REAL publish path end to end and asserts no
// line is lost — so it catches a call site that forgets the lock entirely. ⛔ It
// is NOT a reliable race detector: with the lock removed from the append side
// it still passed, because the interleaving window here is too narrow to hit
// consistently.
//
// ⭐ THE GUARD ITSELF IS PROVEN AT THE STORAGE LEVEL, where the same injection
// fails loudly — TestAWholeFileRewriteCannotClobberAConcurrentAppend drops to
// 17 surviving lines of 26. Treat that one as the test of the mechanism and
// this one as the test of the wiring.
func TestAPublishDoesNotLoseItsLineToAConcurrentRebuild(t *testing.T) {
	siteDir := t.TempDir()
	indexPath := filepath.Join(siteDir, BundleContentDir, PublicIndexFilename)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}

	const publishes = 30
	var wg sync.WaitGroup
	wg.Add(2)

	// A publisher, appending one index line per post.
	go func() {
		defer wg.Done()
		for i := 0; i < publishes; i++ {
			err := AppendToPublicIndex(storage.At(siteDir), &IndexEntry{
				Path:  fmt.Sprintf("content/pub.polis.core/post/2026/p%02d.md", i),
				Title: fmt.Sprintf("post %02d", i),
			})
			if err != nil {
				t.Errorf("publish %d: %v", i, err)
			}
		}
	}()

	// Medic, rebuilding the whole file from what it reads — the shape of
	// index.RebuildContentIndex, taking the same lock.
	go func() {
		defer wg.Done()
		for i := 0; i < publishes; i++ {
			_ = storage.WithPathLock(indexPath, func() error {
				data, err := os.ReadFile(indexPath)
				if err != nil {
					return nil // not created yet
				}
				return os.WriteFile(indexPath, data, 0o644)
			})
		}
	}()
	wg.Wait()

	entries, err := LoadPublicIndex(storage.At(siteDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != publishes {
		t.Fatalf("%d entries survived of %d — a rebuild clobbered a publish", len(entries), publishes)
	}
}
