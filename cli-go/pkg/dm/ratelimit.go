package dm

import (
	"sync"
	"time"
)

// Default rate limits (can be overridden via config).
const (
	DefaultPerSenderRate = 10  // per hour
	DefaultGlobalRate    = 100 // per hour
)

// senderPruneAt is the map size below which a sweep never runs: scanning a
// small map costs more than the entries do.
const senderPruneAt = 1024

// RateLimiter provides in-memory rate limiting for DM deliveries.
// Resets on server restart (acceptable — rate limits are protective, not authoritative).
type RateLimiter struct {
	mu sync.Mutex

	perSenderLimit int
	globalLimit    int

	// Per-sender counters: domain → window
	senders map[string]*rateWindow
	// pruneAt is the size the map must reach before the next sweep, and
	// lastPrune when the last one ran. See maybePruneLocked.
	pruneAt   int
	lastPrune time.Time

	// Global counter
	global *rateWindow
}

type rateWindow struct {
	count       int
	windowStart time.Time
}

// NewRateLimiter creates a rate limiter with the given limits.
func NewRateLimiter(perSenderLimit, globalLimit int) *RateLimiter {
	if perSenderLimit <= 0 {
		perSenderLimit = DefaultPerSenderRate
	}
	if globalLimit <= 0 {
		globalLimit = DefaultGlobalRate
	}
	return &RateLimiter{
		perSenderLimit: perSenderLimit,
		globalLimit:    globalLimit,
		senders:        make(map[string]*rateWindow),
		pruneAt:        senderPruneAt,
		lastPrune:      time.Now(),
		global:         &rateWindow{windowStart: time.Now()},
	}
}

// AllowGlobal checks and increments the global rate counter.
// Returns false if the global limit is exceeded.
func (rl *RateLimiter) AllowGlobal() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if now.Sub(rl.global.windowStart) > time.Hour {
		rl.global.count = 0
		rl.global.windowStart = now
	}

	if rl.global.count >= rl.globalLimit {
		return false
	}
	rl.global.count++
	return true
}

// AllowSender checks and increments the per-sender rate counter.
// Returns false if the per-sender limit is exceeded.
func (rl *RateLimiter) AllowSender(domain string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	w, ok := rl.senders[domain]
	if !ok || now.Sub(w.windowStart) > time.Hour {
		if !ok {
			// A new key. ⛔ The map outlives the request — one entry per
			// distinct sender domain, for the life of the process — and a
			// wildcard-DNS sender mints subdomains for free, so growth is paid
			// for here rather than left to accumulate.
			rl.maybePruneLocked(now)
		}
		rl.senders[domain] = &rateWindow{count: 1, windowStart: now}
		return true
	}

	if w.count >= rl.perSenderLimit {
		return false
	}
	w.count++
	return true
}

// maybePruneLocked decides whether inserting a new sender should pay for a
// scan. Caller holds mu.
//
// ⛔ A SCAN THAT FREES NOTHING MUST NOT RUN AGAIN ON THE NEXT INSERT. A
// flooder's entries are all live, so every scan would free nothing and the next
// insert would scan again — quadratic in exactly the case that matters. The
// threshold therefore DOUBLES past whatever survives a scan, which makes the
// work amortized O(1) per insert however live the entries are. The time branch
// is what still collects stale entries when the map is large but no longer
// growing, at most one scan per window.
//
// This mirrors the hosted edge limiter's sweep (webapp/internal/hosted), which
// learned the rule first; both guard a map whose keys a caller influences.
func (rl *RateLimiter) maybePruneLocked(now time.Time) {
	if len(rl.senders) < senderPruneAt {
		return // never scan a small map
	}
	if len(rl.senders) < rl.pruneAt && now.Sub(rl.lastPrune) < time.Hour {
		return
	}
	rl.pruneLocked(now)
	rl.lastPrune = now
	rl.resetPruneThresholdLocked()
}

// pruneLocked drops every sender whose window has expired. ⛔ Only EXPIRED
// windows — dropping a live counter would hand a capped sender a fresh budget,
// which is the whole thing the limiter is for. Caller holds mu.
func (rl *RateLimiter) pruneLocked(now time.Time) int {
	removed := 0
	for domain, w := range rl.senders {
		if now.Sub(w.windowStart) > time.Hour {
			delete(rl.senders, domain)
			removed++
		}
	}
	return removed
}

func (rl *RateLimiter) resetPruneThresholdLocked() {
	if grown := 2 * len(rl.senders); grown > senderPruneAt {
		rl.pruneAt = grown
	} else {
		rl.pruneAt = senderPruneAt
	}
}

// Cleanup drops every expired sender window and reports how many went. The
// in-band sweep above keeps the map bounded on its own; this is for an owner
// that runs a sweep of its own (the hosted Reaper does, for its limiters).
func (rl *RateLimiter) Cleanup() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	removed := rl.pruneLocked(now)
	rl.lastPrune = now
	rl.resetPruneThresholdLocked()
	return removed
}

// GlobalStatus returns the current global counter and limit (for logging).
func (rl *RateLimiter) GlobalStatus() (count, limit int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.global.count, rl.globalLimit
}

// SenderStatus returns the current counter and limit for a sender (for logging).
func (rl *RateLimiter) SenderStatus(domain string) (count, limit int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if w, ok := rl.senders[domain]; ok {
		return w.count, rl.perSenderLimit
	}
	return 0, rl.perSenderLimit
}
