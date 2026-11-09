package cache

import (
	"context"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const (
	victimPost  = "content/pub.polis.core/post/20261001/victim.md"
	victimDraft = ".polis/bundles/pub.polis.core/comments/drafts/secret.md"
)

// ⭐ The depth matters: a blessed-cache key is seven segments deep
// (.polis/ds/<ds>/pub.polis.core/cache/blessed/<author>/), so seven '..' reach
// the site root. A wrong depth makes the probe miss on the old code too and
// proves nothing.
func traversalURLs() []string {
	up := strings.Repeat("../", 7)
	return []string{
		"https://evil.example/content/pub.polis.core/comment/" + up + victimPost,
		"https://evil.example/content/pub.polis.core/comment/" + up + victimDraft,
		"https://evil.example/x.md?q=/comments/" + up + victimDraft,
		"https://../content/pub.polis.core/comment/20260101/x.md",
	}
}

// ⛔⛔ A FORGED EVENT CANNOT DELETE ANYTHING BUT A CACHE ENTRY. The scan proved
// an unpublished event carrying a traversal URL deleted a post and a draft
// through FindBlessed and Evict.
func TestATraversalCommentURLCannotFindOrEvictAFileOutsideTheCache(t *testing.T) {
	ctx := context.Background()
	site := storage.On("/site", storage.NewObjectStore(""))
	st := site.Store()
	_ = st.Put(ctx, victimPost, []byte("victim"))
	_ = st.Put(ctx, victimDraft, []byte("secret draft"))
	_ = st.Put(ctx, ".polis/ds/ds.example/pub.polis.core/cache/blessed/x/placeholder.md", []byte("x"))
	for _, u := range traversalURLs() {
		if _, _, ok := FindBlessed(site, u); ok {
			t.Errorf("FindBlessed found something for %s", u)
		}
		if err := EvictBlessedAnyDS(site, u); err != nil {
			t.Fatal(err)
		}
		if err := EvictBlessed(site, "ds.example", u); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{victimPost, victimDraft} {
		if ok, _ := st.Exists(ctx, k); !ok {
			t.Errorf("⛔ %s was deleted by a forged event", k)
		}
	}
}

// ⛔ And the blessed cache can WRITE nothing outside itself: the scan proved
// StoreBlessed created a post, and Rosie's store overwrote a snippet.
func TestATraversalCommentURLCannotWriteOutsideTheCache(t *testing.T) {
	ctx := context.Background()
	site := storage.On("/site", storage.NewObjectStore(""))
	for _, u := range traversalURLs() {
		if err := StoreBlessed(site, "ds.example", BlessedSidecar{SourceURL: u}, []byte("<script>")); err == nil {
			t.Errorf("StoreBlessed accepted %s", u)
		}
		if err := StoreBlessed(site, "../../..", BlessedSidecar{SourceURL: "https://a.example/comments/20260101/x.md"}, []byte("x")); err == nil {
			t.Error("StoreBlessed accepted a DS domain that walks out of .polis/ds")
		}
	}
	keys, _ := site.Store().List(ctx, "", storage.Recursive())
	for _, e := range keys {
		if !storage.Within(e.Key, ".polis/ds") {
			t.Errorf("⛔ a blessed-cache write landed at %s", e.Key)
		}
	}
}
