package server

import (
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/cache"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
)

// blessedFillNegativeTTL is how long a failed render-time fill (unreachable
// author, refused signature) is suppressed before another attempt, so a dead
// origin isn't re-fetched on every render.
const blessedFillNegativeTTL = 15 * time.Minute

var (
	blessedFillMu sync.Mutex
	// Keyed by dataDir+"\x00"+commentURL: each tenant fills its OWN cache, so a
	// fill in flight for one tenant must not dedup another tenant's fill (hosted
	// runs many tenants, with distinct dataDirs, in one process).
	blessedFillInflight = map[string]struct{}{}  // currently populating
	blessedFillNegative = map[string]time.Time{} // -> retry-after
)

// InstallBlessedCacheFiller wires render.BlessedCacheFiller so a render-time
// blessed-comment cache MISS populates the isolated cache from the comment
// author's canonical URL via cache.Ingest — the SAME verify-and-gate +
// provenance plumbing Rosie and the real-time grant path use, so render-created
// entries are never rogue to Rosie's integrity/GC.
//
// dsDomain is the DS scope to write under (matches sync.go's cacheBlessedComment,
// extractDomainFromURL(s.DiscoveryURL)). With no DS configured it is "" and we do
// NOT install the filler — render simply shows a placeholder on a miss.
//
// The fill is ASYNCHRONOUS: it kicks a single-flighted background Ingest and
// returns false (a miss) this pass, so a render NEVER blocks on a foreign GET
// (remote fetches carry a 30s timeout). The comment appears on the next render
// once the background fill lands; the common path (grant-time caching) and Rosie
// keep the cache warm, so this is only a cold-cache backstop. Failures are
// negative-cached so a dead origin doesn't spawn a goroutine per render.
func InstallBlessedCacheFiller(dsDomain string) {
	if dsDomain == "" {
		return
	}
	render.BlessedCacheFiller = func(dataDir, commentURL string) bool {
		key := dataDir + "\x00" + commentURL
		blessedFillMu.Lock()
		if t, ok := blessedFillNegative[key]; ok {
			if time.Now().Before(t) {
				blessedFillMu.Unlock()
				return false
			}
			delete(blessedFillNegative, key)
		}
		if _, inflight := blessedFillInflight[key]; inflight {
			blessedFillMu.Unlock()
			return false
		}
		blessedFillInflight[key] = struct{}{}
		blessedFillMu.Unlock()

		go func() {
			store := cache.Store{DataDir: dataDir, DSDomain: dsDomain}
			desc := cache.NewBlessedDescriptor(cache.DescriptorConfig{})
			res := cache.Ingest(store, desc, commentURL, cache.IngestOptions{})

			blessedFillMu.Lock()
			delete(blessedFillInflight, key)
			if !res.Stored {
				blessedFillNegative[key] = time.Now().Add(blessedFillNegativeTTL)
			}
			blessedFillMu.Unlock()
		}()

		// Populate-for-next-render: this pass shows a placeholder.
		return false
	}
}
