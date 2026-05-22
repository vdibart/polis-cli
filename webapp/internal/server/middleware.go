package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// publicContentLimiter is a per-IP fixed-window rate limiter for public
// content + structured-query endpoints. Same shape as the hosted-package
// RateLimiter (per-IP, fixed window) but lives here so polis-server in
// localhost mode also has rate-limiting independent of the hosted wrapper.
type publicContentLimiter struct {
	mu       sync.Mutex
	limiters map[string]*publicContentLimiterEntry
	rate     int
	window   time.Duration
}

type publicContentLimiterEntry struct {
	count    int
	windowAt time.Time
}

func newPublicContentLimiter(rate int, window time.Duration) *publicContentLimiter {
	return &publicContentLimiter{
		limiters: make(map[string]*publicContentLimiterEntry),
		rate:     rate,
		window:   window,
	}
}

// Allow returns (allowed, retryAfter). When allowed is false, retryAfter
// is the seconds until the current window expires (suitable for the
// `Retry-After` header).
func (l *publicContentLimiter) Allow(ip string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	entry, ok := l.limiters[ip]
	if !ok || now.Sub(entry.windowAt) > l.window {
		l.limiters[ip] = &publicContentLimiterEntry{count: 1, windowAt: now}
		return true, 0
	}
	entry.count++
	if entry.count <= l.rate {
		return true, 0
	}
	retry := int(l.window.Seconds() - now.Sub(entry.windowAt).Seconds())
	if retry < 1 {
		retry = 1
	}
	return false, retry
}

// Cleanup prunes entries whose window has expired. Returns the number
// removed. Mirrors hosted.RateLimiter.Cleanup; the Reaper sweep in the
// hosted package calls this so the in-memory map doesn't grow with
// distinct IPs ever hitting the structured-query endpoint.
//
// Without this, the singleton instance — populated by every public
// /api/v1/stream/items request — would grow indefinitely. Pre-fix
// (2026-05-18) a humiliating side-effect was that the leftmost-XFF
// IP-extraction bug (see remoteIP doc-comment) let an attacker plant
// an unbounded number of entries by spoofing the header per request.
// The XFF fix closes the spoof; this Cleanup closes the long-tail
// natural-growth case.
func (l *publicContentLimiter) Cleanup() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	removed := 0
	for ip, entry := range l.limiters {
		if now.Sub(entry.windowAt) > l.window {
			delete(l.limiters, ip)
			removed++
		}
	}
	return removed
}

// CleanupSharedPublicContentLimiter exposes the package-level
// sharedPublicContentLimiter's Cleanup to the hosted-package Reaper
// without making the singleton itself exported. Keeps the limiter
// identity an implementation detail of this package while still
// allowing the Reaper to drain its map.
func CleanupSharedPublicContentLimiter() int {
	return sharedPublicContentLimiter.Cleanup()
}

// remoteIP extracts the request's client IP for rate-limiting purposes.
//
// SECURITY CONTRACT (2026-05-18, fixes a humiliation-grade spoof bug):
// when behind a trusted proxy that APPENDS to `X-Forwarded-For` (Fly,
// most reverse proxies), the leftmost entry is the attacker-controlled
// client header and the RIGHTMOST entry is what the proxy itself set.
// An attacker can blast unbounded traffic past per-IP rate caps by
// sending a different `X-Forwarded-For: <random>` per request if the
// limiter reads the leftmost. We now read the rightmost.
//
// `Fly-Client-IP` is set by Fly's proxy directly and isn't spoofable
// by the client; if present we trust it absolutely. This mirrors the
// posture in webapp/internal/hosted/hosted.go:clientIP.
//
// In localhost / no-proxy mode there's no XFF and no Fly-Client-IP, so
// we fall back to RemoteAddr's host portion. Same behavior as before
// for that case.
func remoteIP(r *http.Request) string {
	// Fly's proxy sets this directly; cannot be spoofed by the client.
	if flyIP := r.Header.Get("Fly-Client-IP"); flyIP != "" {
		return strings.TrimSpace(flyIP)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Rightmost entry is what the trusted proxy set; everything
		// to the left is attacker-controlled.
		if i := strings.LastIndex(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[i+1:])
		}
		return strings.TrimSpace(xff)
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}

// publicContentMiddleware applies CORS + per-IP rate limiting suitable
// for public content routes (per D-PROXY) and the structured-query
// endpoint. CORS is `*` because public content is meant to be readable
// cross-origin (the v4 lazy-fetch path in the controller relies on this).
// Rate limiting protects against bursty/abusive clients while leaving
// generous headroom for legitimate scroll sessions.
func publicContentMiddleware(limiter *publicContentLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ip := remoteIP(r)
		if allowed, retry := limiter.Allow(ip); !allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// contextKey is an unexported type for context keys in this package.
type contextKey int

const (
	requestIDKey contextKey = iota
)

// publicContentRateLimit caps requests per remote IP per hour against
// public content + structured-query endpoints. Generous default per plan
// §4.d task 3 (~1000 req/hr/IP); legitimate scroll sessions (~5 req/s)
// don't approach the limit, but burst-y crawlers / abusive clients hit
// it. On cap, return 429 + Retry-After.
const publicContentRateLimit = 1000
const publicContentRateWindow = time.Hour

// sharedPublicContentLimiter is a process-wide singleton. All public-
// content routes share it so a single client can't bypass the limit by
// hitting different routes in sequence.
var sharedPublicContentLimiter = newPublicContentLimiter(publicContentRateLimit, publicContentRateWindow)

// RequestIDFromContext extracts the request ID from the context.
// Returns empty string if not set.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// generateRequestID creates a UUIDv4 using crypto/rand.
func generateRequestID() string {
	var uuid [16]byte
	_, _ = rand.Read(uuid[:])
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// statusResponseWriter wraps http.ResponseWriter to capture the status code.
type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

// shouldSkipRequestLog returns true for paths that are too noisy to log.
func shouldSkipRequestLog(path string) bool {
	if path == "/api/sse" || path == "/favicon.ico" || path == "/favicon.svg" {
		return true
	}
	if strings.HasPrefix(path, "/assets/") {
		return true
	}
	return false
}

// securityHeadersMiddleware adds standard security headers to all responses.
// CSP is applied per-handler (admin SPA handlers inject a nonce + set their
// own admin-SPA CSP; public-content handlers set a stricter no-nonce CSP).
// This middleware sets a default tenant-content CSP as a fallback for any
// HTML response that hits a path we forgot to cover — fail-restrictive.
// API/JSON paths skip CSP (not meaningful on non-HTML).
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		// R18-21: default-fail-closed CSP for HTML paths. Specific
		// handlers (spaHandler) override this with the admin-SPA
		// nonce-bearing variant before writing the response body.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Security-Policy", CSPTenantContent())
		}
		next.ServeHTTP(w, r)
	})
}

// GenerateCSPNonce returns a fresh 16-byte random nonce, base64-encoded.
// Each response gets a new value; nonces are NEVER reused across responses
// (which would defeat their purpose). 16 bytes is well above the CSP3
// minimum recommendation of 128 bits.
func GenerateCSPNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to a fixed string on extreme failure — the resulting
		// CSP will reject all inline scripts (no script-src match), which
		// is strictly worse than admitting them. Better to fail safe.
		return "fallback-nonce-rand-failed"
	}
	return base64.StdEncoding.EncodeToString(b[:])
}

// CSPTenantContent returns the CSP header value for tenant-served public
// HTML (rendered posts, comments, root pages). Allows the polis-apex
// widget JS plus Google Fonts (loaded by every shape + theme); blocks
// everything else.
func CSPTenantContent() string {
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' https://polis.pub",
		// Google Fonts CSS is loaded via <link rel="stylesheet"> in every
		// shape + theme (v3/v4 stream, studio13-nk, etc.). 'unsafe-inline'
		// permits inline <style> blocks and style="" attributes that
		// theme rendering relies on.
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"img-src 'self' data: https:",
		// Google Fonts serves the actual font files (woff2) from gstatic.
		"font-src 'self' data: https://fonts.gstatic.com",
		// connect-src must include *.polis.pub so cross-tenant client
		// scripts can fetch sibling tenants' public surfaces. Two
		// known consumers: (a) nav.js fetches the visitor's own
		// /api/nav/state to hydrate the cross-visit nav widget;
		// (b) widget.js (the inline comment widget) reads the
		// visitor's following.json to decide its auth-aware mount
		// state. Both targets are public/auth-gated APIs on a polis
		// tenant — the same trust surface frame-ancestors already
		// admits via the *.polis.pub allow-list below.
		"connect-src 'self' https://polis.pub https://*.polis.pub",
		"frame-src 'none'",
		// R20-D-S1 (2026-05-18): bound who can iframe tenant content.
		// Without `frame-ancestors`, only `X-Frame-Options` would stop
		// clickjacking — but hosted only sets that header for /_/*
		// (admin SPA), not for tenant public pages. An attacker iframes
		// `victim.polis.pub/posts/x.html` invisibly under their own UI
		// and clicks through to the widget auth controls. Allowing self
		// + polis.pub + any *.polis.pub keeps the widget's legitimate
		// embed pattern working.
		"frame-ancestors 'self' https://polis.pub https://*.polis.pub",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; ")
}

// CSPAdminSPA returns the CSP header value for the admin SPA (the
// authenticated owner's dashboard). Permits the per-response inline-
// script nonce (used by the importmap in index.html) plus the Milkdown
// editor's esm.sh CDN. Otherwise as restrictive as CSPTenantContent.
//
// IMPORTANT — script-src-attr 'unsafe-inline' is set because index.html
// uses ~40 inline event handlers (`onclick=App.foo()` and similar).
// CSP3 splits the legacy script-src directive into:
//   - script-src-elem: applies to <script> tags. Stays strict via the
//     per-response nonce.
//   - script-src-attr: applies to inline event-handler attributes
//     (onclick, onerror, etc.). 'unsafe-inline' is required because
//     hashes don't apply to event handlers without 'unsafe-hashes',
//     and listing 40+ hashes is brittle and fights every UI change.
//
// Threat admitted: a sanitization bypass that lets attacker HTML reach
// the SPA's renderer can plant an inline event handler. bluemonday
// strips on*= attributes by default, so this requires both a
// sanitization bypass AND the owner clicking the malicious element.
// Acceptable as a short-term posture; a future refactor to delegated
// addEventListener in app.js (replacing inline handlers with
// data-action attributes) will let us drop script-src-attr to 'none'.
func CSPAdminSPA(nonce string) string {
	scriptElem := "'self' 'nonce-" + nonce + "' https://esm.sh"
	return strings.Join([]string{
		"default-src 'self'",
		// Set script-src as the fallback for older browsers AND explicit
		// script-src-elem / script-src-attr for CSP3-aware browsers.
		"script-src " + scriptElem,
		"script-src-elem " + scriptElem,
		"script-src-attr 'unsafe-inline'",
		// SPA loads Google Fonts (Inter + Newsreader) via <link
		// rel="stylesheet"> at the top of index.html.
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"img-src 'self' data: https:",
		"font-src 'self' data: https://fonts.gstatic.com",
		"connect-src 'self' https://esm.sh",
		"frame-src 'none'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; ")
}

// requestLoggingMiddleware logs every HTTP request as structured JSON and
// propagates X-Request-Id through the request context.
func requestLoggingMiddleware(logger *Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read or generate request ID
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Set on response header for client correlation
		w.Header().Set("X-Request-Id", requestID)

		// Store in context
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		r = r.WithContext(ctx)

		path := r.URL.Path

		// Skip noisy paths
		if shouldSkipRequestLog(path) {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		sw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(sw, r)

		duration := time.Since(start)
		logger.Request(r.Method, path, sw.statusCode, duration, requestID)
	})
}
