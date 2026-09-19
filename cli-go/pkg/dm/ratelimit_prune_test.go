package dm

import (
	"fmt"
	"testing"
	"time"
)

// backdateSenders ages every sender window by d, as if that much time had
// passed. The limiter reads the wall clock, so this is how a test reaches an
// expired window without waiting an hour.
func backdateSenders(rl *RateLimiter, d time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for _, w := range rl.senders {
		w.windowStart = w.windowStart.Add(-d)
	}
}

func sendersLen(rl *RateLimiter) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.senders)
}

// The receiver's limiter now lives for the life of the process (close-out
// HOST 2), so its per-sender map is no longer thrown away with the request. It
// was never swept: one entry per distinct sender domain per recipient site,
// forever. Verification cost gates the growth, but a wildcard-DNS sender mints
// subdomains for free (close-out R2-3).
func TestRateLimiterSweepsExpiredSenderWindows(t *testing.T) {
	rl := NewRateLimiter(0, 0)

	const senders = 4 * senderPruneAt
	for i := 0; i < senders; i++ {
		rl.AllowSender(fmt.Sprintf("s%d.example", i))
	}
	if n := sendersLen(rl); n != senders {
		t.Fatalf("%d live senders tracked, want %d — the fixture is wrong", n, senders)
	}

	// An hour later every one of those windows is expired, and the entries are
	// dead weight. Further traffic must not keep them.
	backdateSenders(rl, 2*time.Hour)
	for i := 0; i < senderPruneAt; i++ {
		rl.AllowSender(fmt.Sprintf("later%d.example", i))
	}
	if n := sendersLen(rl); n > 2*senderPruneAt {
		t.Errorf("%d entries tracked after the old windows expired, want the stale ones dropped", n)
	}
}

// ⛔ Only EXPIRED windows. Dropping a live counter hands a capped sender a
// fresh budget, which is the defect the sweep must not introduce.
func TestRateLimiterSweepKeepsLiveCounters(t *testing.T) {
	rl := NewRateLimiter(0, 0)

	// One sender spends its whole budget.
	for i := 0; i < DefaultPerSenderRate; i++ {
		if !rl.AllowSender("flooder.example") {
			t.Fatalf("delivery %d refused before the limit", i+1)
		}
	}
	if rl.AllowSender("flooder.example") {
		t.Fatal("the limit did not bite")
	}

	// Enough traffic to force sweeps, from senders whose windows have expired.
	for round := 0; round < 3; round++ {
		for i := 0; i < senderPruneAt; i++ {
			rl.AllowSender(fmt.Sprintf("r%d-%d.example", round, i))
		}
		rl.mu.Lock()
		for domain, w := range rl.senders {
			if domain != "flooder.example" {
				w.windowStart = w.windowStart.Add(-2 * time.Hour)
			}
		}
		rl.mu.Unlock()
	}

	if rl.AllowSender("flooder.example") {
		t.Error("a swept map handed the capped sender a fresh budget")
	}
}

// Cleanup is the explicit form, for an owner that has a sweep of its own.
func TestRateLimiterCleanupReportsWhatItDropped(t *testing.T) {
	rl := NewRateLimiter(0, 0)
	for i := 0; i < 10; i++ {
		rl.AllowSender(fmt.Sprintf("s%d.example", i))
	}
	if n := rl.Cleanup(); n != 0 {
		t.Errorf("Cleanup dropped %d live entries, want 0", n)
	}
	backdateSenders(rl, 2*time.Hour)
	if n := rl.Cleanup(); n != 10 {
		t.Errorf("Cleanup dropped %d expired entries, want 10", n)
	}
	if n := sendersLen(rl); n != 0 {
		t.Errorf("%d entries left after Cleanup, want 0", n)
	}
}

// The sweep must stay amortized O(1) per insert even when nothing it scans can
// be freed — the flooder case, where every entry is live.
func BenchmarkAllowSenderAllLive(b *testing.B) {
	rl := NewRateLimiter(1<<30, 1<<30)
	for i := 0; i < b.N; i++ {
		rl.AllowSender(fmt.Sprintf("s%d.example", i))
	}
}
