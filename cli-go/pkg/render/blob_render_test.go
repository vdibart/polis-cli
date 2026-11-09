package render

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐⭐ THE DETACH'S WHOLE PREMISE, TESTED END TO END: render a site whose
// storage is an OBJECT STORE and require the rendered HTML to land in it.
//
// The claim is that rendering writes to Blob "with no
// render-specific code" because every write in this package goes through the
// storage seam. ⛔ That is a claim worth proving rather than asserting — the
// seam has been wrong before, and this package is where the path/key confusion
// silently emptied the tag index.
//
// ⚠️ A site needs its bundle payload installed before it can render, so this
// seeds through site.Init on POSIX and then COPIES to the object store. That
// is also the migration path a real instance takes, so it is worth exercising.
func TestRenderWritesToAnObjectBackedSite(t *testing.T) {
	ctx := context.Background()

	// 1. a real, initialised site on POSIX
	posix := storage.At(t.TempDir())
	if _, err := site.Init(posix, site.InitOptions{
		BaseURL:   "https://alice.example.test",
		Author:    "Alice",
		SiteTitle: "Alice",
	}); err != nil {
		// ⛔ FAIL, never skip: a skip here passed the test with nothing proven.
		t.Fatalf("site.Init: %v", err)
	}
	// ⭐ PUBLISHED, not dropped in place: the renderer works from the public
	// index of published posts, so a raw markdown file is never rendered — the
	// earlier version of this test did that and rendered only index.html.
	priv, err := posix.Store().Get(ctx, site.PrivateKeyKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publish.PublishPost(posix, "# Hello\n\nHello body.\n", "hello", priv); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// 2. copy it to an object store — a flat keyspace, no directories, no modes
	blobStore := storage.NewObjectStore("tenants/alice")
	entries, err := posix.Store().List(ctx, "", storage.Recursive())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		data, gerr := posix.Store().Get(ctx, e.Key)
		if gerr != nil {
			t.Fatal(gerr)
		}
		if perr := blobStore.Put(ctx, e.Key, data); perr != nil {
			t.Fatal(perr)
		}
	}
	blob := storage.On(t.TempDir(), blobStore)
	// ⛔ What exists BEFORE the render. The copied site already holds template
	// HTML, so "some HTML is in the store" proved nothing; only HTML the render
	// CREATED, carrying the post's own text, does.
	before := map[string]bool{}
	if pre, err := blobStore.List(ctx, "", storage.Recursive()); err == nil {
		for _, e := range pre {
			before[e.Key] = true
		}
	}

	// 3. render THROUGH THE OBJECT STORE
	r, err := NewPageRenderer(PageConfig{
		Site:    blob,
		BaseURL: "https://alice.example.test",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer against an object-backed site: %v", err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll against an object-backed site: %v", err)
	}

	// 4. the rendered HTML must be IN THE OBJECT STORE, not on any disk
	rendered, err := blobStore.List(ctx, "", storage.Recursive())
	if err != nil {
		t.Fatal(err)
	}
	var created []string
	postPage := ""
	for _, e := range rendered {
		if !strings.HasSuffix(e.Key, ".html") || before[e.Key] {
			continue
		}
		created = append(created, e.Key)
		// ⭐ The post's OWN page, not the index, which quotes the body too.
		if e.Key == "index.html" {
			continue
		}
		if b, gerr := blobStore.Get(ctx, e.Key); gerr == nil && strings.Contains(string(b), "Hello body.") {
			postPage = e.Key
		}
	}
	if len(created) == 0 {
		t.Fatalf("the render created no HTML in the object store (it holds %d keys, all from before)", len(rendered))
	}
	if postPage == "" {
		t.Fatalf("no page the render created carries the post's text; created: %v", created)
	}
	t.Logf("render created %d HTML keys; the post's page is %s", len(created), postPage)

	// ⛔⛔ NOTHING MAY REACH THE FILESYSTEM. A blob-backed instance must write
	// every byte to its store, and on Vercel the filesystem is READ-ONLY
	// outside /tmp — so a write that reaches a real path is not a leak to tidy
	// up later, it is a failure.
	//
	// ⚠️ IT WAS ONE WRITE, AND IT WAS IN THE RENDER PATH. NewPageRenderer
	// picks a theme when none is set, and persisting that choice went through
	// atomicfile on a real path, so the FIRST render of a new blob-backed
	// instance would have failed. The store-addressed atomic write closed it: with no RealPath, the
	// store's Put IS the atomic write.
	leaked, err := storage.ListDir(blob.Dir())
	if err == nil && len(leaked) > 0 {
		var names []string
		for _, e := range leaked {
			names = append(names, e.Key)
		}
		sort.Strings(names)
		t.Errorf("rendering wrote %d entries to the filesystem instead of the store: %v",
			len(leaked), names)
	}

}
