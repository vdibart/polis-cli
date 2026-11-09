package server

import (
	"net"
	"sync"
	"time"
)

// The sign-in and recovery limiter.
//
// ⭐⭐ PER PROCESS, AND THAT IS THE DECISION, NOT A GAP. A limit shared exactly
// across instances needs a shared counter, and the only shared thing a remote
// instance has is its store. On Vercel Hobby a Blob store allows 2,000 advanced
// operations a month and exceeding that locks the store for 30 days — so a
// counter written per attempt would let a flood of sign-in attempts take the
// whole site down for a month without ever threatening the owner's key. The
// rule this follows instead: a request that has proved nothing costs no store
// operation. The limit across instances is the platform's edge rate limit,
// which stops a flood before any function runs.
//
// ⭐ And an exact shared limit protects little here. Recovery needs a signature
// from the recovery key and sign-in needs a passkey assertion; neither can be
// guessed, so the limit protects CPU and logs, not the credential.
//
// ⛔⛔ EACH ADDRESS GETS A SHARE WELL BELOW THE PROCESS CAP, so a few addresses
// cannot exhaust it and lock the owner out: an address that has used its share
// is refused WITHOUT spending any of the cap, so k flooding addresses consume
// at most k shares. Exhausting the cap takes perProcess/perAddress distinct
// addresses in one window. ⚠️ IPv6 addresses are counted by their /64, which is
// what one subscriber is routinely given: per full address, one host could
// mint as many "addresses" as it liked.

const (
	// attemptsPerAddress is one address's share per window. ⭐ High enough that
	// an owner fumbling a paste or a PIN a few times is unaffected (a sign-in
	// is two requests: begin and complete).
	attemptsPerAddress = 10

	// attemptsPerProcess caps one process per window, across all addresses.
	// ⛔ Twenty times the share: no small set of addresses reaches it.
	attemptsPerProcess = 200

	attemptWindow = time.Minute
)

// attemptLimiter is a fixed-window limiter with a per-address share and a
// process-wide cap.
type attemptLimiter struct {
	mu         sync.Mutex
	window     time.Time
	counts     map[string]int
	total      int
	perAddress int
	perProcess int
	now        func() time.Time
}

func newAttemptLimiter() *attemptLimiter {
	return &attemptLimiter{
		counts:     map[string]int{},
		perAddress: attemptsPerAddress,
		perProcess: attemptsPerProcess,
		now:        time.Now,
	}
}

// allow reports whether one more attempt from client is within its share and
// the process cap, and counts it if so.
func (l *attemptLimiter) allow(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.window) >= attemptWindow {
		l.window = now
		l.counts = map[string]int{}
		l.total = 0
	}
	who := addressBucket(client)
	// ⛔ An address over its share is refused BEFORE the cap is touched, so it
	// cannot spend anyone else's budget by continuing to knock.
	if l.counts[who] >= l.perAddress {
		return false
	}
	if l.total >= l.perProcess {
		return false
	}
	l.counts[who]++
	l.total++
	return true
}

// exhausted reports whether client has used its share this window, without
// counting anything: for a limiter that counts only failures (/mcp), checked
// before the request costs anything, with allow called after one fails.
func (l *attemptLimiter) exhausted(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.now().Sub(l.window) >= attemptWindow {
		return false
	}
	return l.counts[addressBucket(client)] >= l.perAddress
}

// addressBucket is what an address is counted under: itself for IPv4, its /64
// for IPv6. ⚠️ For counting only, never for trust.
func addressBucket(client string) string {
	ip := net.ParseIP(client)
	if ip == nil || ip.To4() != nil {
		return client
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
