package server

import (
	"encoding/json"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/cache"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
)

// On a withdrawal the blessing list is updated FIRST, then the cache. The
// list is the authority — the render path re-fetches any listed comment missing
// from the cache — so evicting first and then failing the list write (as this
// once did, with only a warn) left the withdrawn comment listed and about to
// be cached again. Now a failed list write evicts nothing: the pair stays
// consistent and an alertable event says the withdrawal was not applied.
func TestBlessingSyncEvictionUpdatesTheListBeforeTheCache(t *testing.T) {
	gone := "https://bob.polis.pub/content/pub.polis.core/comment/20260917/gone.md"
	s := signedSyncSite(t, gone)
	if err := cache.StoreBlessed(storage.At(s.DataDir), "ds.polis.pub", cache.BlessedSidecar{SourceURL: gone, BlessedVersionHash: "sha256:x"}, []byte("body")); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cache.FindBlessed(storage.At(s.DataDir), gone); !ok {
		t.Fatal("setup: the comment is not cached")
	}

	// Make the list write fail honestly: a member this build does not model,
	// which GuardRewrite refuses to drop.
	path := filepath.Join(s.DataDir, metadata.BlessedKey)
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(raw), "{", `{"from_the_future": 1,`, 1)), 0644)

	r := (&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{{
		ID:      json.Number("1"),
		Type:    "pub.polis.comment.blessing.denied",
		Payload: map[string]interface{}{"comment_url": gone, "target_domain": "owner.polis.pub"},
	}})

	if !blessedListHas(s.Site(), gone) {
		t.Fatal("setup: the list write should have been refused")
	}
	if _, _, ok := cache.FindBlessed(storage.At(s.DataDir), gone); !ok {
		t.Error("the cache was evicted although the list could not be updated: withdrawn comment left listed and uncached")
	}
	if r.FilesChanged {
		t.Error("a withdrawal that was not applied reported files changed")
	}
}

// And the ordinary case still evicts both.
func TestBlessingSyncEvictionStillRemovesBoth(t *testing.T) {
	gone := "https://bob.polis.pub/content/pub.polis.core/comment/20260917/gone.md"
	s := signedSyncSite(t, gone)
	cache.StoreBlessed(storage.At(s.DataDir), "ds.polis.pub", cache.BlessedSidecar{SourceURL: gone, BlessedVersionHash: "sha256:x"}, []byte("body"))
	(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{{
		ID:      json.Number("1"),
		Type:    "pub.polis.comment.blessing.denied",
		Payload: map[string]interface{}{"comment_url": gone, "target_domain": "owner.polis.pub"},
	}})
	if blessedListHas(s.Site(), gone) {
		t.Error("still listed after a withdrawal")
	}
	if _, _, ok := cache.FindBlessed(storage.At(s.DataDir), gone); ok {
		t.Error("still cached after a withdrawal")
	}
}
