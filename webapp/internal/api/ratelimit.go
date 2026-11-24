package api

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Rate limits on /v1. ⭐ Two budgets, each answering 429 with Retry-After:
//
//   - per token, on writes: what bounds a runaway agent. An agent caught in a
//     loop is stopped at the budget instead of when the owner notices. Keyed
//     by the site and the key's id (a personal token, or a sign-in token's
//     connection), never by address. Deletes have a smaller budget of their
//     own, on top.
//   - per address, on requests that carry no token: the public reads, and
//     the refusals of anyone without one.
//
// ⛔ The owner's own full-access token is not limited: it is the owner, not
// something acting for them, and a bulk job of the owner's must not stall.
//
// Refusals are summarised: the first refusal of a window logs one event for
// that token or address, the rest are silent. The counters are in memory,
// per process, and start again after a restart.
const (
	tokenWritesPerHour   = 200
	tokenDeletesPerHour  = 50
	addressCallsPerHour  = 600
	rateWindow           = time.Hour
	RateLimitedEvent     = "pub.polis.security.rate_limited"
	rateLimiterSweepSize = 10000
)

type windowCounter struct {
	mu      sync.Mutex
	rate    int
	window  time.Duration
	entries map[string]*windowEntry
}

type windowEntry struct {
	start  time.Time
	count  int
	logged bool
}

func newWindowCounter(rate int, window time.Duration) *windowCounter {
	return &windowCounter{rate: rate, window: window, entries: map[string]*windowEntry{}}
}

// take counts one call for key. It reports whether the call is allowed, the
// seconds until the window ends when it is not, and whether this is the
// window's first refusal (the one to log).
func (c *windowCounter) take(key string, now time.Time) (ok bool, retry int, firstRefusal bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= rateLimiterSweepSize {
		for k, e := range c.entries {
			if now.Sub(e.start) >= c.window {
				delete(c.entries, k)
			}
		}
	}
	e, found := c.entries[key]
	if !found || now.Sub(e.start) >= c.window {
		e = &windowEntry{start: now}
		c.entries[key] = e
	}
	if e.count >= c.rate {
		retry = int((c.window - now.Sub(e.start)).Seconds())
		if retry < 1 {
			retry = 1
		}
		first := !e.logged
		e.logged = true
		return false, retry, first
	}
	e.count++
	return true, 0, false
}

// The process's counters: on polis.pub every tenant's /v1 shares them, so a
// caller cannot spread one budget across tenants by address. Tokens are
// keyed with their site, so two sites' keys never share a budget.
var (
	tokenWrites  = newWindowCounter(tokenWritesPerHour, rateWindow)
	tokenDeletes = newWindowCounter(tokenDeletesPerHour, rateWindow)
	addressCalls = newWindowCounter(addressCallsPerHour, rateWindow)
	rateLimitNow = time.Now
)

func tooMany(w http.ResponseWriter, retry int, msg string) {
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	writeError(w, http.StatusTooManyRequests, "rate_limited", msg)
}

// clientAddr is the caller's address as the host resolves it (behind a
// proxy, the client the proxy names), or the socket's peer.
func (h *handlers) clientAddr(r *http.Request) string {
	if h.host != nil && h.host.ClientAddr != nil {
		if a := h.host.ClientAddr(r); a != "" {
			return a
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// edge is every /v1 route's outermost wrapper: the per-address budget for a
// request that carries neither a token nor a site's signature.
func (h *handlers) edge(next http.HandlerFunc) http.HandlerFunc {
	next = cors(next)
	// ⭐ Outermost, so every error a /v1 route answers, its own refusals
	// included, is the one envelope (envelope.go).
	return enveloped(func(w http.ResponseWriter, r *http.Request) {
		// A site-to-site request (a message delivered by another instance)
		// is signed instead, and has its own limits where it is verified.
		// ⭐ The owner's own interface, signed in, is not an anonymous caller.
		if r.Header.Get("Authorization") == "" && r.Header.Get("X-Polis-Signature") == "" && !h.ownerSession(r) {
			addr := h.clientAddr(r)
			if ok, retry, first := addressCalls.take(addr, rateLimitNow()); !ok {
				if first {
					h.logSecurity(RateLimitedEvent, map[string]interface{}{
						"limit": "address", "ip": addr, "path": r.URL.Path,
						"per_hour": addressCallsPerHour, "request_id": w.Header().Get("X-Request-Id"),
					})
				}
				tooMany(w, retry, "Too many requests from this address without a token; try again later.")
				return
			}
		}
		next(w, r)
	})
}

// tokenBudget is the per-token write budget, checked once the token is known
// to be good. It answers the refusal itself and reports false then.
func (h *handlers) tokenBudget(w http.ResponseWriter, r *http.Request, siteID, keyID string) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	key := siteID + "\x00" + keyID
	now := rateLimitNow()
	refuse := func(limit string, perHour, retry int, first bool) bool {
		if first {
			h.logSecurity(RateLimitedEvent, map[string]interface{}{
				"limit": limit, "key_id": keyID, "path": r.URL.Path, "method": r.Method,
				"per_hour": perHour, "request_id": w.Header().Get("X-Request-Id"),
			})
		}
		tooMany(w, retry, "This token has made too many changes this hour; try again later. The site's owner can see its acts and switch it off in their settings.")
		return false
	}
	if r.Method == http.MethodDelete {
		if ok, retry, first := tokenDeletes.take(key, now); !ok {
			return refuse("token_deletes", tokenDeletesPerHour, retry, first)
		}
	}
	if ok, retry, first := tokenWrites.take(key, now); !ok {
		return refuse("token_writes", tokenWritesPerHour, retry, first)
	}
	return true
}
