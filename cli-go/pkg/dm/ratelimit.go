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

// RateLimiter provides in-memory rate limiting for DM deliveries.
// Resets on server restart (acceptable — rate limits are protective, not authoritative).
type RateLimiter struct {
	mu sync.Mutex

	perSenderLimit int
	globalLimit    int

	// Per-sender counters: domain → window
	senders map[string]*rateWindow

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
		rl.senders[domain] = &rateWindow{count: 1, windowStart: now}
		return true
	}

	if w.count >= rl.perSenderLimit {
		return false
	}
	w.count++
	return true
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
