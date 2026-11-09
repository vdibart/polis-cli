package server

import (
	"sync"
	"time"
)

// Sync on request: the request half of Hedgerows' "sync on request, catch up
// on cron". On a backend that starts on request (Vercel) nothing runs between
// requests: no background sync loop, no cron yet. So the owner's network
// stream would never receive anything. When the owner asks for it and the
// network feed is stale, one feed sync runs first.
//
// ⭐ BOUNDED, AND NEVER A FAILURE. The request waits at most syncOnRequestBudget
// and then answers with what the cache holds; a sync that is still going
// finishes on its own. A discovery service that is slow or down only makes the
// stream as old as it was.
//
// ⭐ READS ONLY, PRIVATE WRITES ONLY. syncFeed reads the discovery service
// anonymously (no registration needed) and writes the feed cache under
// .polis/, which is not a public file, so it never triggers a deploy.
//
// ⛔ Hosted and laptop servers run StartBackgroundSync instead and are never
// touched by this: it applies only when the backend starts on request.

// syncOnRequestBudget is the longest a stream request waits for the sync.
const syncOnRequestBudget = 3 * time.Second

var syncOnRequestMu sync.Mutex

// syncNetworkOnRequest runs one network feed sync before a stream request is
// answered, when this backend has no background sync and the cache is stale.
func (s *Server) syncNetworkOnRequest() {
	if !s.StartsOnRequest() {
		return
	}
	if stale, _ := s.feedCacheForScope("network").IsStale(); !stale {
		return
	}
	// One at a time. A second request does not wait for the first: it answers
	// from the cache, which the first is filling.
	if !syncOnRequestMu.TryLock() {
		return
	}
	run := s.syncOnRequestFn
	if run == nil {
		run = s.syncFeed
	}
	done := make(chan struct{})
	go func() {
		defer syncOnRequestMu.Unlock()
		defer close(done)
		run()
	}()
	select {
	case <-done:
	case <-time.After(s.syncOnRequestWait()):
		s.LogInfo("stream: the feed sync is still running after %s; answering from the cache", s.syncOnRequestWait())
	}
}

func (s *Server) syncOnRequestWait() time.Duration {
	if s.syncOnRequestBudget > 0 {
		return s.syncOnRequestBudget
	}
	return syncOnRequestBudget
}
