package ops

import (
	"sync"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
)

// DM delivery rate limiters, one per recipient site directory, kept for the
// life of the process.
//
// ⛔ A limiter is only a limiter if it outlives the request. deliverDM built a
// fresh dm.NewRateLimiter on every call, so each check ran against a counter
// that had just been zeroed: the per-sender and global DM limits the security
// model publishes (10/sender/hr, 100/hr) did not exist on any self-hosted
// deployment, and nothing failed to say so.
//
// **Keyed by site dir, not held on the engine or HandlerEnv**, because the
// recipient is the site, not the object that happens to be serving it. A
// hosted tenant handler — engine included — is built on demand and evicted
// from the tenant cache, and an engine-scoped counter would hand a flooding
// sender a fresh window on every eviction, which is exactly the pressure a
// flood creates. The process-wide scope also matches how the hosted service
// already limits public static content.
//
// ⚠️ This runs AFTER the signed-request verification in the v1 API router,
// which fetches the sender domain's key. It bounds what a verified sender can
// deliver; it is not a defence against unverified floods, which the per-IP
// limits in front of the API are for.
//
// Windows are in-memory and reset when the process restarts, which the
// limiter's own contract already allows: these limits are protective, not
// authoritative.
var (
	dmRateLimitersMu sync.Mutex
	dmRateLimiters   = map[string]*dm.RateLimiter{}
)

// dmRateLimiterFor returns the limiter for a recipient site, creating it on
// first use. The env overrides are read once per site, when the limiter is
// created — they are process-level settings, not per-request ones.
func dmRateLimiterFor(siteDir string) *dm.RateLimiter {
	dmRateLimitersMu.Lock()
	defer dmRateLimitersMu.Unlock()

	if rl, ok := dmRateLimiters[siteDir]; ok {
		return rl
	}
	rl := dm.NewRateLimiter(
		envInt("POLIS_DM_RATE_PER_SENDER", 0),
		envInt("POLIS_DM_RATE_GLOBAL", 0),
	)
	dmRateLimiters[siteDir] = rl
	return rl
}

// forgetDMRateLimiter drops a site's limiter. Tests use it to start from a
// known window; nothing in the serving path calls it, because forgetting a
// counter is what the defect did.
func forgetDMRateLimiter(siteDir string) {
	dmRateLimitersMu.Lock()
	defer dmRateLimitersMu.Unlock()
	delete(dmRateLimiters, siteDir)
}
