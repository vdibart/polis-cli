package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
)

// makeFakeDS returns an httptest.Server that responds to
// POST /v1/content/comments/counts with the given fixed counts map.
// requestCount increments once per request — tests can assert how many
// times DS was hit (e.g. singleflight collapsing).
func makeFakeDS(t *testing.T, counts map[string]int, delay time.Duration) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requestCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/content/comments/counts" {
			http.NotFound(w, r)
			return
		}
		requestCount.Add(1)
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"counts": counts})
	}))
	return srv, &requestCount
}

func newCountsTestServer(t *testing.T, ds *httptest.Server) *Server {
	t.Helper()
	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL
	return s
}

// crossTenantPost constructs a cross-tenant post item (different author
// domain than s.GetBaseURL()'s domain).
func crossTenantPost(url, authorDomain string) feed.CachedFeedItem {
	return feed.CachedFeedItem{
		Type:         "post",
		URL:          url,
		AuthorDomain: authorDomain,
		Published:    "2026-05-20T10:00:00Z",
	}
}

// TestPopulateCrossTenantCommentCounts_StampsVisibleItems exercises the
// blocking sync-fetch path for items above the viewport horizon.
func TestPopulateCrossTenantCommentCounts_StampsVisibleItems(t *testing.T) {
	postA := "https://other.example/posts/a.md"
	postB := "https://other.example/posts/b.md"
	ds, _ := makeFakeDS(t, map[string]int{
		postA: 5,
		postB: 2,
	}, 0)
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	page := []feed.CachedFeedItem{
		crossTenantPost(postA, "other.example"),
		crossTenantPost(postB, "other.example"),
	}
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")

	if page[0].CommentCount != 5 {
		t.Errorf("postA: expected count=5, got %d", page[0].CommentCount)
	}
	if page[1].CommentCount != 2 {
		t.Errorf("postB: expected count=2, got %d", page[1].CommentCount)
	}
}

// TestPopulateCrossTenantCommentCounts_OwnPostsGetDSTotal verifies that own
// posts (AuthorDomain == myDomain, relative mount URL) are absolutized to the
// canonical form DS indexed and stamped with the DS TOTAL — overwriting the
// blessed-only count pre-stamped from blessed.json. This makes the badge mean
// "total comments (blessed + unblessed)" for own and cross-tenant posts alike.
func TestPopulateCrossTenantCommentCounts_OwnPostsGetDSTotal(t *testing.T) {
	ownURL := "/posts/2026/05/mine.html" // mount path, not absolute https
	ds, requests := makeFakeDS(t, map[string]int{
		"https://test-site.polis.pub/posts/2026/05/mine.md": 99,
	}, 0)
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	page := []feed.CachedFeedItem{
		{
			Type:         "post",
			URL:          ownURL,
			AuthorDomain: "test-site.polis.pub",
			CommentCount: 3, // pre-stamped blessed-only count from blessed.json
		},
	}
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")

	if page[0].CommentCount != 99 {
		t.Errorf("own post should be stamped with DS total 99, got %d", page[0].CommentCount)
	}
	if requests.Load() != 1 {
		t.Errorf("DS should be queried once for the own post, got %d requests", requests.Load())
	}
}

// TestPopulateCrossTenantCommentCounts_OwnPostKeepsBlessedFallbackOnDSError
// verifies that when DS is unreachable, an own post keeps its pre-stamped
// blessed.json count rather than dropping to zero.
func TestPopulateCrossTenantCommentCounts_OwnPostKeepsBlessedFallbackOnDSError(t *testing.T) {
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	page := []feed.CachedFeedItem{
		{
			Type:         "post",
			URL:          "/posts/2026/05/mine.html",
			AuthorDomain: "test-site.polis.pub",
			CommentCount: 3, // blessed.json fallback
		},
	}
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")

	if page[0].CommentCount != 3 {
		t.Errorf("own post should keep blessed.json fallback 3 on DS error, got %d", page[0].CommentCount)
	}
}

// TestPopulateCrossTenantCommentCounts_SkipsNonHttpsURLs guards against
// malformed cache rows whose URL is not absolute (e.g. a mount path
// leaking through). Those URLs would never match what DS stored and
// would burn a DS roundtrip for nothing.
func TestPopulateCrossTenantCommentCounts_SkipsNonHttpsURLs(t *testing.T) {
	ds, requests := makeFakeDS(t, map[string]int{}, 0)
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	page := []feed.CachedFeedItem{
		{Type: "post", URL: "/posts/garbage.html", AuthorDomain: "other.example"},
	}
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")

	if requests.Load() != 0 {
		t.Errorf("DS should not be called for non-https URLs, got %d requests", requests.Load())
	}
}

// TestPopulateCrossTenantCommentCounts_FailSoftOnDSError verifies the
// stream completes promptly with zero counts when DS returns an error.
// The stream response itself must not fail.
func TestPopulateCrossTenantCommentCounts_FailSoftOnDSError(t *testing.T) {
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	page := []feed.CachedFeedItem{
		crossTenantPost("https://other.example/posts/a.md", "other.example"),
	}
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	start := time.Now()
	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")
	elapsed := time.Since(start)

	if elapsed > visibleCountFetchTimeout+200*time.Millisecond {
		t.Errorf("expected fail-soft within timeout budget, took %v", elapsed)
	}
	if page[0].CommentCount != 0 {
		t.Errorf("expected zero count on DS error, got %d", page[0].CommentCount)
	}
}

// TestPopulateCrossTenantCommentCounts_HonorsVisibleTimeout exercises
// the strict 500ms cap. With a slow DS (artificially > visibleTimeout),
// the call must return at the timeout boundary, not wait for the slow
// response. Items stay at zero — fail-soft.
func TestPopulateCrossTenantCommentCounts_HonorsVisibleTimeout(t *testing.T) {
	postA := "https://other.example/posts/a.md"
	// 800ms delay > 500ms visibleCountFetchTimeout
	ds, _ := makeFakeDS(t, map[string]int{postA: 7}, 800*time.Millisecond)
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	page := []feed.CachedFeedItem{crossTenantPost(postA, "other.example")}
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	start := time.Now()
	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")
	elapsed := time.Since(start)

	// Should return at or shortly after the 500ms timeout, NOT wait the
	// full 800ms the DS would have taken. Allow 200ms slack for context
	// cancellation propagation and transport teardown.
	if elapsed > visibleCountFetchTimeout+200*time.Millisecond {
		t.Errorf("did not respect visibleCountFetchTimeout: elapsed %v", elapsed)
	}
	if page[0].CommentCount != 0 {
		t.Errorf("expected zero count on timeout, got %d", page[0].CommentCount)
	}
}

// TestPopulateCrossTenantCommentCounts_CacheHitSkipsDS ensures repeat
// calls for the same URLs within the TTL window do not hit DS.
func TestPopulateCrossTenantCommentCounts_CacheHitSkipsDS(t *testing.T) {
	postA := "https://other.example/posts/a.md"
	ds, requests := makeFakeDS(t, map[string]int{postA: 4}, 0)
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	page := []feed.CachedFeedItem{crossTenantPost(postA, "other.example")}
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	// First call populates the cache via DS.
	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")
	if requests.Load() != 1 {
		t.Errorf("expected 1 DS request on cold cache, got %d", requests.Load())
	}
	if page[0].CommentCount != 4 {
		t.Errorf("first call: expected count=4, got %d", page[0].CommentCount)
	}

	// Second call with fresh page must hit cache, not DS.
	page2 := []feed.CachedFeedItem{crossTenantPost(postA, "other.example")}
	s.populateCrossTenantCommentCounts(r, page2, "test-site.polis.pub")
	if requests.Load() != 1 {
		t.Errorf("expected DS to be untouched on cache hit, got %d total requests", requests.Load())
	}
	if page2[0].CommentCount != 4 {
		t.Errorf("second call: expected cached count=4, got %d", page2[0].CommentCount)
	}
}

// TestPopulateCrossTenantCommentCounts_StampsWholePageSynchronously verifies
// the determinism fix: every post on a page (within the batch cap) is stamped
// synchronously in the response, regardless of position. This is the cure for
// the 0↔1↔0 badge flip where below-the-fold posts used to stay at 0 until a
// later render warmed the cache.
func TestPopulateCrossTenantCommentCounts_StampsWholePageSynchronously(t *testing.T) {
	// 15 items — all below a (former) 10-item horizon must still be stamped.
	expected := map[string]int{}
	page := make([]feed.CachedFeedItem, 0, 15)
	for i := 0; i < 15; i++ {
		u := fmt.Sprintf("https://other.example/posts/%d.md", i)
		page = append(page, crossTenantPost(u, "other.example"))
		expected[u] = i + 1
	}

	ds, requests := makeFakeDS(t, expected, 0)
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")

	// EVERY item (including former "below the fold" ones) is stamped now.
	for i := 0; i < 15; i++ {
		if page[i].CommentCount != i+1 {
			t.Errorf("item %d: expected count=%d stamped synchronously, got %d", i, i+1, page[i].CommentCount)
		}
	}
	// One batched DS call for the whole page.
	if requests.Load() != 1 {
		t.Errorf("expected exactly 1 batched DS request, got %d", requests.Load())
	}
}

// TestPopulateCrossTenantCommentCounts_OverflowGoesBackground verifies that a
// page exceeding the batch cap stamps the first streamCountBatchMax items
// synchronously and defers the remainder to a background cache fill.
func TestPopulateCrossTenantCommentCounts_OverflowGoesBackground(t *testing.T) {
	n := streamCountBatchMax + 5
	expected := map[string]int{}
	page := make([]feed.CachedFeedItem, 0, n)
	for i := 0; i < n; i++ {
		u := fmt.Sprintf("https://other.example/posts/%d.md", i)
		page = append(page, crossTenantPost(u, "other.example"))
		expected[u] = i + 1
	}

	ds, _ := makeFakeDS(t, expected, 0)
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")

	// First batch stamped synchronously.
	for i := 0; i < streamCountBatchMax; i++ {
		if page[i].CommentCount != i+1 {
			t.Errorf("synced item %d: expected count=%d, got %d", i, i+1, page[i].CommentCount)
		}
	}
	// Overflow items aren't stamped in this response but the background
	// goroutine warms the cache for the next render.
	overflowURL := page[streamCountBatchMax].URL
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if total, ok := s.getCommentCountCacheEntry(overflowURL); ok && total == streamCountBatchMax+1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if total, ok := s.getCommentCountCacheEntry(overflowURL); !ok || total != streamCountBatchMax+1 {
		t.Errorf("background goroutine should have cached overflow item; got total=%d ok=%v", total, ok)
	}
}

// TestPopulateCrossTenantCommentCounts_NoDSURLNoop guards against
// calling the function on an unconfigured server (DiscoveryURL == "").
func TestPopulateCrossTenantCommentCounts_NoDSURLNoop(t *testing.T) {
	s := newConfiguredServer(t) // no DiscoveryURL
	page := []feed.CachedFeedItem{
		crossTenantPost("https://other.example/posts/a.md", "other.example"),
	}
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	// Should not panic, should not modify items.
	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")
	if page[0].CommentCount != 0 {
		t.Errorf("expected unchanged count, got %d", page[0].CommentCount)
	}
}

// TestPopulateCrossTenantCommentCounts_EmptyPage is a defensive check.
func TestPopulateCrossTenantCommentCounts_EmptyPage(t *testing.T) {
	ds, requests := makeFakeDS(t, map[string]int{}, 0)
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	r := httptest.NewRequest("GET", "/api/stream/items", nil)
	s.populateCrossTenantCommentCounts(r, nil, "test-site.polis.pub")
	if requests.Load() != 0 {
		t.Errorf("DS should not be called on empty page")
	}
}

// writeBlessedJSON writes a blessed.json with one post → one comment entry,
// using the given pc.Post key form (regression coverage for the URL-format
// variance discovered on discover.polis.pub where blessed.json stores the
// absolute URL form).
func writeBlessedJSON(t *testing.T, dataDir, postKey string) {
	t.Helper()
	dir := filepath.Join(dataDir, "content", "pub.polis.core", "comment")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bc := metadata.BlessedComments{
		Version: "test",
		Comments: []metadata.PostComments{{
			Post: postKey,
			Blessed: []metadata.BlessedComment{{
				URL:       "https://other.example/comments/20260525/c.md",
				Version:   "sha256:x",
				BlessedAt: "2026-05-25T00:00:00Z",
			}},
		}},
	}
	data, _ := json.MarshalIndent(bc, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "blessed.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPopulateCommentCountsAndOptionallyFilter_HandlesAllPostKeyForms is the
// regression test for the canonical-stream-shows-0 bug. blessed.json's pc.Post
// can be in any of several forms (absolute URL, source path, mount path)
// depending on which polis-cli version wrote it. The count-stamper must
// normalize all of them to the mount URL ("/posts/<date>/<slug>.html") so the
// stamping hits the feed item's it.URL.
func TestPopulateCommentCountsAndOptionallyFilter_HandlesAllPostKeyForms(t *testing.T) {
	itemURL := "/posts/20260524/the-internet-already-solved-these-problems-the-the.html"
	cases := []struct {
		name    string
		postKey string
	}{
		{
			name:    "absolute_url_content_form",
			postKey: "https://discover.polis.pub/content/pub.polis.core/post/20260524/the-internet-already-solved-these-problems-the-the.md",
		},
		{
			name:    "absolute_url_mount_form",
			postKey: "https://discover.polis.pub/posts/20260524/the-internet-already-solved-these-problems-the-the.md",
		},
		{
			name:    "source_path",
			postKey: "content/pub.polis.core/post/20260524/the-internet-already-solved-these-problems-the-the.md",
		},
		{
			name:    "mount_path",
			postKey: "posts/20260524/the-internet-already-solved-these-problems-the-the.md",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newConfiguredServer(t)
			writeBlessedJSON(t, s.DataDir, c.postKey)
			items := []feed.CachedFeedItem{{
				Type:         "post",
				URL:          itemURL,
				AuthorDomain: "test-site.polis.pub",
			}}
			out := s.populateCommentCountsAndOptionallyFilter(items, false)
			if len(out) != 1 {
				t.Fatalf("expected 1 item, got %d", len(out))
			}
			if out[0].CommentCount != 1 {
				t.Errorf("key form %q: expected count=1, got %d", c.postKey, out[0].CommentCount)
			}
		})
	}
}

// TestFlightKey verifies the singleflight key is order-independent.
func TestFlightKey(t *testing.T) {
	a := flightKey([]string{"https://a.example/x.md", "https://b.example/y.md"})
	b := flightKey([]string{"https://b.example/y.md", "https://a.example/x.md"})
	if a != b {
		t.Errorf("flightKey should be order-independent: %q != %q", a, b)
	}
	if got := flightKey([]string{"https://solo.example/x.md"}); got != "https://solo.example/x.md" {
		t.Errorf("single-URL flightKey should be the URL itself, got %q", got)
	}
}
