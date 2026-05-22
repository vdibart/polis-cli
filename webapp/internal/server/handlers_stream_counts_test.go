package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
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

// TestPopulateCrossTenantCommentCounts_SkipsOwnPosts verifies that
// items where AuthorDomain matches myDomain are left alone (their
// counts are already stamped from blessed.json).
func TestPopulateCrossTenantCommentCounts_SkipsOwnPosts(t *testing.T) {
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
			CommentCount: 3, // pre-stamped by populateCommentCountsAndOptionallyFilter
		},
	}
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")

	if page[0].CommentCount != 3 {
		t.Errorf("own post count must be left untouched, got %d", page[0].CommentCount)
	}
	if requests.Load() != 0 {
		t.Errorf("DS should not be called when no cross-tenant items present, got %d requests", requests.Load())
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

// TestPopulateCrossTenantCommentCounts_BackgroundPopulatesCache covers
// the below-the-fold branch: items past visibleHorizon kick off a
// fire-and-forget goroutine. The current call leaves them at zero but
// the cache is populated shortly after so the next call returns counts.
func TestPopulateCrossTenantCommentCounts_BackgroundPopulatesCache(t *testing.T) {
	// 15 items: indices 0..9 are visible, 10..14 are background.
	expected := map[string]int{}
	page := make([]feed.CachedFeedItem, 0, 15)
	for i := 0; i < 15; i++ {
		u := fmt.Sprintf("https://other.example/posts/%d.md", i)
		page = append(page, crossTenantPost(u, "other.example"))
		expected[u] = i + 1
	}

	ds, _ := makeFakeDS(t, expected, 0)
	defer ds.Close()

	s := newCountsTestServer(t, ds)
	r := httptest.NewRequest("GET", "/api/stream/items", nil)

	s.populateCrossTenantCommentCounts(r, page, "test-site.polis.pub")

	// Visible items should be stamped synchronously.
	for i := 0; i < visibleHorizon; i++ {
		if page[i].CommentCount != i+1 {
			t.Errorf("visible item %d: expected count=%d, got %d", i, i+1, page[i].CommentCount)
		}
	}

	// Background items are not stamped in this response (the goroutine
	// outlived the call but the page slice was already returned).
	// However, the background goroutine should populate the cache; wait
	// briefly and verify cache lookups succeed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if total, ok := s.getCommentCountCacheEntry(page[10].URL); ok && total == 11 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if total, ok := s.getCommentCountCacheEntry(page[10].URL); !ok || total != 11 {
		t.Errorf("background goroutine should have populated cache for item 10; got total=%d ok=%v", total, ok)
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
