package server

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
)

// requestStarted puts a test server on a backend that starts on request, as
// a remote instance on Vercel is.
func requestStarted(t *testing.T) *Server {
	t.Helper()
	s := newConfiguredServer(t)
	s.backend.StartsOnRequest = true
	return s
}

// ⭐ On a backend that starts on request, opening the owner's stream with a
// stale cache runs the sync first, so what it brings in is in the answer.
func TestStreamSyncsOnRequestWhenNothingElseDoes(t *testing.T) {
	s := requestStarted(t)
	var calls atomic.Int32
	s.syncOnRequestFn = func() {
		calls.Add(1)
		// What the discovery service would bring in for a followed author.
		seedNetworkFeed(t, s, []feed.FeedItem{{
			Type: "post", Title: "A quip", URL: "posts/q.md", AuthorDomain: "discover.polis.pub",
			AuthorURL: "https://discover.polis.pub", Published: time.Now().UTC().Format(time.RFC3339),
		}})
		// As syncFeed does when it finishes: the cache is now fresh.
		_ = s.feedCacheForScope("network").MarkChecked()
	}
	code, resp := streamRequest(t, s, "type=posts")
	if code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("stream = %d, syncs = %d; want 200 and one sync", code, calls.Load())
	}
	if items, _ := resp["items"].([]interface{}); len(items) != 1 {
		t.Errorf("the stream did not carry what the sync brought in: %v", resp)
	}
	// Fresh now: the next request answers from the cache.
	streamRequest(t, s, "type=posts")
	if calls.Load() != 1 {
		t.Errorf("a fresh cache synced again: %d syncs", calls.Load())
	}
}

// ⛔ A laptop or hosted server has its background sync; the stream never syncs
// on request there.
func TestStreamDoesNotSyncOnRequestWithBackgroundSync(t *testing.T) {
	s := newConfiguredServer(t)
	var calls atomic.Int32
	s.syncOnRequestFn = func() { calls.Add(1) }
	streamRequest(t, s, "type=posts")
	if calls.Load() != 0 {
		t.Errorf("a server that is not request-started synced on request")
	}
}

// ⭐ A slow discovery service costs the stream at most the budget, never a failure.
func TestStreamSyncOnRequestIsBounded(t *testing.T) {
	s := requestStarted(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s.syncOnRequestFn = func() { <-release }
	s.syncOnRequestBudget = 100 * time.Millisecond
	start := time.Now()
	code, _ := streamRequest(t, s, "type=posts")
	if took := time.Since(start); code != http.StatusOK || took > 2*time.Second {
		t.Errorf("stream = %d after %s; want 200 within about the budget", code, took)
	}
}

// ⭐ Two requests at once sync once; the second answers from the cache.
func TestStreamSyncOnRequestRunsOnceAtATime(t *testing.T) {
	s := requestStarted(t)
	var calls atomic.Int32
	release := make(chan struct{})
	s.syncOnRequestFn = func() { calls.Add(1); <-release }
	s.syncOnRequestBudget = 200 * time.Millisecond
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.syncNetworkOnRequest() }()
	}
	wg.Wait()
	close(release)
	if calls.Load() != 1 {
		t.Errorf("concurrent requests ran %d syncs, want 1", calls.Load())
	}
	// Let the held sync finish and release the lock before the next test.
	for i := 0; i < 50 && !syncOnRequestMu.TryLock(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	syncOnRequestMu.Unlock()
}
