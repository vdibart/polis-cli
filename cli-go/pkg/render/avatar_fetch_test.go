package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// closeCountingTransport counts CloseIdleConnections calls, which
// http.Client.CloseIdleConnections forwards to its transport.
type closeCountingTransport struct {
	base   http.RoundTripper
	closes atomic.Int32
}

func (c *closeCountingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return c.base.RoundTrip(r)
}

func (c *closeCountingTransport) CloseIdleConnections() {
	c.closes.Add(1)
	if ci, ok := c.base.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}

// A host that fails and then recovers: each failure is logged (rate-limited
// per domain, with the domain, the error class and the duration), closes the
// client's idle connections, and a later render, once the negative cache entry
// has expired, fetches afresh and gets the avatar.
func TestAuthorAvatarFetchFailingThenRecoveringHost(t *testing.T) {
	var mode atomic.Value // "drop" | "status" | "ok"
	mode.Store("drop")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load().(string) {
		case "drop":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		case "status":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		default:
			w.Write([]byte(`{"avatar":{"bg":"#3f6384","fg":"#ffffff"}}`))
		}
	}))
	defer srv.Close()
	// Hijack needs HTTP/1.1; the test server's client speaks it by default.
	domain := strings.TrimPrefix(srv.URL, "https://")

	transport := &closeCountingTransport{base: srv.Client().Transport}
	origClient := authorAvatarClient
	authorAvatarClient = &http.Client{Transport: transport, Timeout: authorAvatarFetchTimeout}
	authorAvatarCache = map[string]authorAvatarEntry{}
	avatarFetchLogs = map[string]*avatarFetchLogState{}
	var mu sync.Mutex
	var logged []map[string]any
	SetAvatarFetchLogger(func(event string, fields map[string]any) {
		if event != "pub.polis.render.avatar_fetch_failed" {
			t.Errorf("event = %q", event)
		}
		mu.Lock()
		logged = append(logged, fields)
		mu.Unlock()
	})
	defer func() {
		authorAvatarClient = origClient
		authorAvatarCache = map[string]authorAvatarEntry{}
		avatarFetchLogs = map[string]*avatarFetchLogState{}
		avatarFetchLogger.Store(nil)
	}()
	expire := func() {
		authorAvatarMu.Lock()
		e := authorAvatarCache[domain]
		e.fetchedAt = time.Now().Add(-authorAvatarNegTTL - time.Second)
		authorAvatarCache[domain] = e
		authorAvatarMu.Unlock()
	}

	// 1. The host drops the connection.
	if _, ok := LoadOrFetchAuthorAvatar(domain); ok {
		t.Fatal("fetch from a host that dropped the connection reported ok")
	}
	if len(logged) != 1 {
		t.Fatalf("logged %d failures, want 1", len(logged))
	}
	if logged[0]["domain"] != domain || logged[0]["error_class"] != "transport" {
		t.Errorf("first failure logged as %v", logged[0])
	}
	if _, ok := logged[0]["duration_ms"].(int64); !ok {
		t.Errorf("duration_ms missing or not an int64: %v", logged[0])
	}
	if transport.closes.Load() != 1 {
		t.Errorf("idle connections closed %d times after a failure, want 1", transport.closes.Load())
	}

	// Within the negative TTL nothing is refetched.
	if _, ok := LoadOrFetchAuthorAvatar(domain); ok || transport.closes.Load() != 1 {
		t.Error("a cached failure refetched within the negative TTL")
	}

	// 2. After the negative TTL the host answers 503: refetched, closed again,
	// and not logged a second time within the interval.
	mode.Store("status")
	expire()
	if _, ok := LoadOrFetchAuthorAvatar(domain); ok {
		t.Fatal("fetch from a 503 host reported ok")
	}
	if len(logged) != 1 {
		t.Errorf("a second failure within the interval was logged: %v", logged)
	}
	if transport.closes.Load() != 2 {
		t.Errorf("idle connections closed %d times after two failures, want 2", transport.closes.Load())
	}

	// 3. The host recovers: the next render after the TTL gets the avatar.
	mode.Store("ok")
	expire()
	cfg, ok := LoadOrFetchAuthorAvatar(domain)
	if !ok || cfg.BG != "#3f6384" {
		t.Fatalf("after recovery got (%+v, %v), want the host's avatar", cfg, ok)
	}

	// 4. A failure after the interval is logged, counting the one suppressed.
	mode.Store("status")
	expire()
	authorAvatarMu.Lock()
	delete(authorAvatarCache, domain)
	authorAvatarMu.Unlock()
	avatarFetchLogMu.Lock()
	avatarFetchLogs[domain].loggedAt = time.Now().Add(-avatarFetchLogInterval - time.Second)
	avatarFetchLogMu.Unlock()
	LoadOrFetchAuthorAvatar(domain)
	if len(logged) != 2 {
		t.Fatalf("logged %d failures, want 2", len(logged))
	}
	if logged[1]["error_class"] != "status" || logged[1]["status"] != http.StatusServiceUnavailable || logged[1]["suppressed"] != 1 {
		t.Errorf("failure after the interval logged as %v", logged[1])
	}
}

// With no sink set (the CLI), a failure is still handled and nothing panics.
func TestAuthorAvatarFetchFailureWithNoLogger(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()
	origClient := authorAvatarClient
	authorAvatarClient = srv.Client()
	avatarFetchLogs = map[string]*avatarFetchLogState{}
	defer func() {
		authorAvatarClient = origClient
		avatarFetchLogs = map[string]*avatarFetchLogState{}
	}()
	if _, ok := fetchAuthorAvatar(strings.TrimPrefix(srv.URL, "https://")); ok {
		t.Error("a body that does not parse reported ok")
	}
}
