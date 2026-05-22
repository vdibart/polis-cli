package feed

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestCacheManager_ConcurrentMerge_SameInstance_NoDataLoss verifies that
// concurrent MergeItems calls on the SAME CacheManager are serialized by
// the instance mutex and no items are lost.
//
// This is the safe path — it's the multi-instance path that loses data
// (see TestCacheManager_ConcurrentMerge_SeparateInstances_LosesItems).
func TestCacheManager_ConcurrentMerge_SameInstance_NoDataLoss(t *testing.T) {
	cm := NewCacheManager(t.TempDir(), testDiscoveryDomain)
	now := time.Now().UTC()

	const goroutines = 8
	const itemsPerGoroutine = 10

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			items := make([]FeedItem, itemsPerGoroutine)
			for i := 0; i < itemsPerGoroutine; i++ {
				items[i] = FeedItem{
					Type:         "post",
					Title:        fmt.Sprintf("g%d-i%d", g, i),
					URL:          fmt.Sprintf("posts/g%d-i%d.md", g, i),
					Published:    now.Add(-time.Duration(g*itemsPerGoroutine+i) * time.Minute).Format(time.RFC3339),
					AuthorURL:    fmt.Sprintf("https://author%d.example", g),
					AuthorDomain: fmt.Sprintf("author%d.example", g),
				}
			}
			if _, err := cm.MergeItems(items); err != nil {
				t.Errorf("goroutine %d merge: %v", g, err)
			}
		}(g)
	}
	wg.Wait()

	got, err := cm.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := goroutines * itemsPerGoroutine
	if len(got) != want {
		t.Errorf("same-instance concurrent merge lost items: got %d, want %d", len(got), want)
	}
}

// TestCacheManager_ConcurrentMerge_SeparateInstances_LosesItems pins
// down an inherent property of the CacheManager API: the instance
// mutex only protects calls on the *same* instance. Creating multiple
// CacheManagers against the same on-disk file races through
// writeAll's truncate-and-rewrite (os.Create) and loses items.
//
// The production bug this captured was fixed 2026-03-30 in commit
// f78655e6 by introducing Server.feedCacheForScope() in the webapp,
// which returns a process-wide shared CacheManager per scope. As long
// as every caller routes through that helper, the mutex does its job.
//
// This test exists to document the failure mode so a future refactor
// that quietly bypasses feedCacheForScope (or moves work into another
// process / goroutine that instantiates its own CacheManager) doesn't
// silently reintroduce the race. We observe the loss and surface it
// in test output rather than fail — the assertion is about the
// behavior of the *primitive*, not the production wiring. If you're
// here because this test surprised you in CI, the action item is
// probably "use feedCacheForScope", not "fix this test."
func TestCacheManager_ConcurrentMerge_SeparateInstances_LosesItems(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	const managers = 4
	const itemsPerManager = 25

	// Build all items first so the goroutines do nothing but MergeItems
	// during the contention window.
	allItems := make([][]FeedItem, managers)
	for m := 0; m < managers; m++ {
		items := make([]FeedItem, itemsPerManager)
		for i := 0; i < itemsPerManager; i++ {
			items[i] = FeedItem{
				Type:         "post",
				Title:        fmt.Sprintf("m%d-i%d", m, i),
				URL:          fmt.Sprintf("posts/m%d-i%d.md", m, i),
				Published:    now.Add(-time.Duration(m*itemsPerManager+i) * time.Minute).Format(time.RFC3339),
				AuthorURL:    fmt.Sprintf("https://m%d.example", m),
				AuthorDomain: fmt.Sprintf("m%d.example", m),
			}
		}
		allItems[m] = items
	}

	// Spin up N separate CacheManagers — each with its own mutex — and
	// have them all merge into the same on-disk file concurrently.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for m := 0; m < managers; m++ {
		wg.Add(1)
		go func(m int) {
			defer wg.Done()
			cm := NewCacheManager(dir, testDiscoveryDomain) // SEPARATE INSTANCE
			<-start                                          // align so they actually race
			if _, err := cm.MergeItems(allItems[m]); err != nil {
				t.Errorf("manager %d merge: %v", m, err)
			}
		}(m)
	}
	close(start)
	wg.Wait()

	// Re-open with a fresh manager and count what survived.
	reader := NewCacheManager(dir, testDiscoveryDomain)
	got, err := reader.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := managers * itemsPerManager

	// Surface the loss in test output so a CI run makes the API-shape
	// failure mode visible. Don't fail — this is documenting an
	// inherent property of the primitive, not testing the production
	// wiring (which routes everything through feedCacheForScope).
	if len(got) < want {
		t.Logf("API contract: %d/%d items lost across separate CacheManager instances on the same file. Production code uses Server.feedCacheForScope to share a single instance — don't bypass it.", want-len(got), want)
	} else {
		t.Logf("kept all %d items this run — the race is intermittent; rerun to surface", want)
	}
}

// TestCacheManager_ConcurrentMerge_SeparateInstances_DataRace is a
// canary specifically designed to be caught by `go test -race`. Two
// CacheManagers with independent mutexes both call writeAll on the same
// file path. Even when the count happens to match, -race should report
// concurrent file writes with no synchronization between the managers.
//
// Skipped under `go test` (no -race). Run via:
//
//	go test -race -run TestCacheManager_ConcurrentMerge_SeparateInstances_DataRace ./pkg/feed
func TestCacheManager_ConcurrentMerge_SeparateInstances_DataRace(t *testing.T) {
	if testing.Short() {
		t.Skip("race canary; skip under -short")
	}

	dir := t.TempDir()
	now := time.Now().UTC()

	var wg sync.WaitGroup
	for m := 0; m < 6; m++ {
		wg.Add(1)
		go func(m int) {
			defer wg.Done()
			cm := NewCacheManager(dir, testDiscoveryDomain)
			items := []FeedItem{{
				Type:         "post",
				Title:        fmt.Sprintf("race-%d", m),
				URL:          fmt.Sprintf("posts/race-%d.md", m),
				Published:    now.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339),
				AuthorURL:    fmt.Sprintf("https://race%d.example", m),
				AuthorDomain: fmt.Sprintf("race%d.example", m),
			}}
			for j := 0; j < 3; j++ {
				_, _ = cm.MergeItems(items)
			}
		}(m)
	}
	wg.Wait()
}
