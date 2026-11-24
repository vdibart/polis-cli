package render

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Author-avatar fetch + cache.
//
// This is the SINGLE SEAM for obtaining a remote author's avatar config. Today
// it reads the author's .well-known/polis "avatar" block; when avatars move
// into the content bundle, repoint fetchAuthorAvatar and nothing else changes.
//
// Cache is in-process with a positive TTL and a shorter negative TTL (so a
// dead/unreachable origin isn't re-hit on every render). Mirrors the intent of
// LoadOrFetchReplyContext (bounded by the caller's concurrency; see
// handlers_stream.go buildAuthorAvatars which fans out at most 5 at a time).

const (
	authorAvatarTTL          = 6 * time.Hour
	authorAvatarNegTTL       = 15 * time.Minute
	authorAvatarFetchTimeout = 3 * time.Second
	authorAvatarMaxBody      = 256 * 1024
)

type authorAvatarEntry struct {
	cfg       AvatarConfig
	ok        bool
	fetchedAt time.Time
}

var (
	authorAvatarMu     sync.Mutex
	authorAvatarCache  = map[string]authorAvatarEntry{}
	authorAvatarClient = &http.Client{Timeout: authorAvatarFetchTimeout}
)

// LoadOrFetchAuthorAvatar returns the avatar config for a remote author domain,
// fetching + caching on miss/expiry. ok=false means the fetch failed (the
// caller should fall back to FallbackAvatarConfig). A reachable site with no
// avatar block returns (zero AvatarConfig, true) — cached as a successful
// "no custom avatar" so we don't re-hit it.
func LoadOrFetchAuthorAvatar(domain string) (AvatarConfig, bool) {
	if domain == "" {
		return AvatarConfig{}, false
	}

	authorAvatarMu.Lock()
	e, present := authorAvatarCache[domain]
	authorAvatarMu.Unlock()
	if present {
		ttl := authorAvatarTTL
		if !e.ok {
			ttl = authorAvatarNegTTL
		}
		if time.Since(e.fetchedAt) < ttl {
			return e.cfg, e.ok
		}
	}

	cfg, ok := fetchAuthorAvatar(domain)

	authorAvatarMu.Lock()
	authorAvatarCache[domain] = authorAvatarEntry{cfg: cfg, ok: ok, fetchedAt: time.Now()}
	authorAvatarMu.Unlock()
	return cfg, ok
}

// fetchAuthorAvatar is a package var so tests can stub the network. Default
// implementation reads https://<domain>/.well-known/polis and parses the
// "avatar" block.
//
// ⭐ Every failure is reported (avatarFetchFailed) and closes the client's idle
// connections: a pooled connection that has gone bad must not make every later
// refetch fail too. Once, a running polis-hosted answered "failed" for one
// domain for hours, with nothing logged, until a restart.
var fetchAuthorAvatar = func(domain string) (AvatarConfig, bool) {
	start := time.Now()
	resp, err := authorAvatarClient.Get("https://" + domain + "/.well-known/polis")
	if err != nil {
		avatarFetchFailed(domain, transportErrorClass(err), start, 0, err)
		return AvatarConfig{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		avatarFetchFailed(domain, "status", start, resp.StatusCode, nil)
		return AvatarConfig{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, authorAvatarMaxBody))
	if err != nil {
		avatarFetchFailed(domain, "read", start, resp.StatusCode, err)
		return AvatarConfig{}, false
	}
	var wk struct {
		Avatar *AvatarConfig `json:"avatar"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		avatarFetchFailed(domain, "parse", start, resp.StatusCode, nil)
		return AvatarConfig{}, false
	}
	if wk.Avatar == nil {
		return AvatarConfig{}, true // reachable, no custom avatar
	}
	return *wk.Avatar, true
}

// avatarFetchLogInterval is how often one domain's fetch failures are logged:
// the first failure is logged at once, later ones within the interval are
// counted and reported as `suppressed` on the next line, so a dead host cannot
// flood the log.
const avatarFetchLogInterval = 15 * time.Minute

// avatarFetchLogger receives pub.polis.render.avatar_fetch_failed. It is unset
// in the CLI (nothing is logged) and set by the host that serves renders.
var avatarFetchLogger atomic.Pointer[func(event string, fields map[string]any)]

// SetAvatarFetchLogger sets where author-avatar fetch failures are logged. The
// avatar cache is per process, so this is a process-wide sink: polis-hosted
// sets its own (landlord) logger at boot, before any site starts.
func SetAvatarFetchLogger(logEvent func(event string, fields map[string]any)) {
	avatarFetchLogger.Store(&logEvent)
}

// SetAvatarFetchLoggerIfUnset sets the sink only when none is set, so a site's
// server can supply one without replacing the process's own.
func SetAvatarFetchLoggerIfUnset(logEvent func(event string, fields map[string]any)) {
	avatarFetchLogger.CompareAndSwap(nil, &logEvent)
}

type avatarFetchLogState struct {
	loggedAt   time.Time
	suppressed int
}

var (
	avatarFetchLogMu sync.Mutex
	avatarFetchLogs  = map[string]*avatarFetchLogState{}
)

// avatarFetchFailed closes the client's idle connections, so a later fetch
// dials afresh, and logs the failure, rate-limited per domain. It logs the
// domain, the error class, the duration and (for a transport or read error)
// the error's text; never the response body.
func avatarFetchFailed(domain, class string, start time.Time, status int, err error) {
	authorAvatarClient.CloseIdleConnections()

	avatarFetchLogMu.Lock()
	st := avatarFetchLogs[domain]
	if st == nil {
		st = &avatarFetchLogState{}
		avatarFetchLogs[domain] = st
	}
	if !st.loggedAt.IsZero() && time.Since(st.loggedAt) < avatarFetchLogInterval {
		st.suppressed++
		avatarFetchLogMu.Unlock()
		return
	}
	suppressed := st.suppressed
	st.loggedAt, st.suppressed = time.Now(), 0
	avatarFetchLogMu.Unlock()

	msg := ""
	if err != nil {
		if msg = err.Error(); len(msg) > 200 {
			msg = msg[:200]
		}
	}
	// status is 0 and error "" where the class has none.
	logAvatarFetch("pub.polis.render.avatar_fetch_failed", map[string]any{
		"domain":      domain,
		"error_class": class,
		"duration_ms": time.Since(start).Milliseconds(),
		"suppressed":  suppressed,
		"status":      status,
		"error":       msg,
	})
}

// logAvatarFetch hands an event to the sink, if one is set.
func logAvatarFetch(event string, fields map[string]any) {
	if logger := avatarFetchLogger.Load(); logger != nil && *logger != nil {
		(*logger)(event, fields)
	}
}

// transportErrorClass names a failed request's cause: timeout, dial (DNS or
// connect), tls, or transport for anything else (a dropped connection).
func transportErrorClass(err error) string {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "timeout"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dial"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return "dial"
	}
	var certErr *tls.CertificateVerificationError
	var recErr tls.RecordHeaderError
	if errors.As(err, &certErr) || errors.As(err, &recErr) {
		return "tls"
	}
	return "transport"
}

// FallbackAvatarConfig returns a deterministic hue-based config for an author
// with no custom avatar. The hue is SERVER-AUTHORITATIVE — it's sent to the
// client in author_avatar.bg, so the JS side renders it directly rather than
// recomputing (only the single initial letter is derived client-side). The
// hue formula matches stream.js avatarFallback / app.js domainToAvatar in
// spirit; exact byte-parity isn't required since the server is the source.
func FallbackAvatarConfig(domain string) AvatarConfig {
	return AvatarConfig{BG: fallbackAvatarHue(domain), FG: "#ffffff"}
}

func fallbackAvatarHue(domain string) string {
	var hash int32
	for _, r := range domain {
		hash = int32(r) + (hash << 5) - hash
	}
	hue := int(hash) % 360
	if hue < 0 {
		hue += 360
	}
	return fmt.Sprintf("hsl(%d, 35%%, 55%%)", hue)
}
