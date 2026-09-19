package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The localhost web app used to refuse no request from another website. A page
// the owner visited could POST to it with a text/plain body (no preflight):
// write a hook, then publish to run it; sign a licence; rotate the key. With a
// DNS-rebinding Host it could also read /api/download-site, private key
// included. These tests pin the localhost-mode guard that closes that.

// localhostMux is the editor-surface mux Run builds, hooks enabled, wrapped the
// way Run wraps it.
func localhostMux(t *testing.T) http.Handler {
	t.Helper()
	s := newTestServer(t)
	s.EnableHooks = true
	mux := http.NewServeMux()
	SetupRoutes(mux, s)
	return localhostModeHandler(nil, mux)
}

// reached is a stand-in mux that records whether a request got through.
func reachedMux(hit *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hit = true
		w.WriteHeader(http.StatusOK)
	})
}

func localRequest(method, path, host string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	req.Host = host
	req.Header.Set("Content-Type", "text/plain")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

var crossSiteHeaders = map[string]string{
	"Origin":         "https://evil.example",
	"Sec-Fetch-Site": "cross-site",
}

// The review's attack sequence, each step on its own, against the real routes.
func TestLocalhostRefusesCrossSiteWrites(t *testing.T) {
	h := localhostMux(t)
	for _, path := range []string{
		"/api/automations",
		"/api/publish",
		"/api/site/license",
		"/api/settings/rosie",
		"/api/rotate-key",
	} {
		for name, headers := range map[string]map[string]string{
			"origin and fetch-site": crossSiteHeaders,
			"origin only":           {"Origin": "https://evil.example"},
			"fetch-site only":       {"Sec-Fetch-Site": "cross-site"},
			"same-site sibling":     {"Origin": "http://localhost:9999", "Sec-Fetch-Site": "same-site"},
			"opaque origin":         {"Origin": "null"},
		} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, localRequest(http.MethodPost, path, "localhost:8080", headers))
			if w.Code != http.StatusForbidden {
				t.Errorf("%s POST %s: status %d, want 403", name, path, w.Code)
			}
		}
	}
}

// Every /api/ route SetupRoutes registers is refused a cross-site write. The
// widget routes are included on purpose: on localhost they carry no widget
// token check (that lives in the hosted layer), and no widget can connect to a
// localhost server, so nothing legitimate calls them cross-origin.
func TestLocalhostRefusesCrossSiteWritesOnEveryAPIRoute(t *testing.T) {
	routes := setupRoutesAPIPaths(t)
	if len(routes) < 50 {
		t.Fatalf("found only %d /api/ routes in SetupRoutes; the scan is broken", len(routes))
	}
	var widget int
	for _, path := range routes {
		if strings.HasPrefix(path, "/api/widget/") {
			widget++
		}
		var hit bool
		h := localhostModeHandler(nil, reachedMux(&hit))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, localRequest(http.MethodPost, path, "127.0.0.1:8080", crossSiteHeaders))
		if w.Code != http.StatusForbidden || hit {
			t.Errorf("cross-site POST %s: status %d, reached=%v; want 403, not reached", path, w.Code, hit)
		}
	}
	if widget == 0 {
		t.Error("no /api/widget/ route seen; the widget routes must be in the scan")
	}
}

// The localhost exemption list is deliberately empty. Adding a route to it
// must be a reviewed decision, and must come with that route's own auth.
func TestLocalhostCrossOriginExemptionsAreExactlyNone(t *testing.T) {
	if len(localhostCrossOriginAPIRoutes) != 0 {
		t.Errorf("localhost cross-origin exemptions = %v, want none", localhostCrossOriginAPIRoutes)
	}
}

func TestLocalhostAllowsSameOriginAndNonBrowserWrites(t *testing.T) {
	for name, tc := range map[string]struct {
		host    string
		headers map[string]string
	}{
		"same origin":            {"localhost:8080", map[string]string{"Origin": "http://localhost:8080", "Sec-Fetch-Site": "same-origin"}},
		"same origin, 127.0.0.1": {"127.0.0.1:8080", map[string]string{"Origin": "http://127.0.0.1:8080"}},
		"same origin, [::1]":     {"[::1]:8080", map[string]string{"Origin": "http://[::1]:8080", "Sec-Fetch-Site": "same-origin"}},
		"no origin (CLI, curl)":  {"localhost:8080", nil},
		"user-initiated":         {"localhost:8080", map[string]string{"Sec-Fetch-Site": "none"}},
	} {
		for _, path := range []string{"/api/automations", "/api/publish", "/api/site/license", "/api/settings/rosie", "/api/rotate-key"} {
			var hit bool
			h := localhostModeHandler(nil, reachedMux(&hit))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, localRequest(http.MethodPost, path, tc.host, tc.headers))
			if w.Code != http.StatusOK || !hit {
				t.Errorf("%s POST %s: status %d, reached=%v; want through", name, path, w.Code, hit)
			}
		}
	}
}

// Reads are not the cross-site rule's business (the browser will not let
// another site read the answer); a cross-site GET addressed to localhost
// passes, including the public, CORS-open stream routes.
func TestLocalhostLetsCrossSiteReadsThrough(t *testing.T) {
	for _, path := range []string{"/api/status", "/pql/everything", "/api/v1/stream/body"} {
		var hit bool
		h := localhostModeHandler(nil, reachedMux(&hit))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, localRequest(http.MethodGet, path, "localhost:8080", crossSiteHeaders))
		if !hit {
			t.Errorf("cross-site GET %s: status %d, not reached", path, w.Code)
		}
	}
}

// DNS rebinding: a name the attacker controls resolves to 127.0.0.1, so the
// browser treats the page and the server as one origin. Only the Host header
// tells them apart.
func TestLocalhostRefusesANonLoopbackHost(t *testing.T) {
	h := localhostMux(t)
	for _, host := range []string{"evil.example:8080", "evil.example", "localhost.evil.example:8080", "127.0.0.1.evil.example", "10.0.0.5:8080", ""} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, localRequest(http.MethodGet, "/api/download-site", host, nil))
		if w.Code != http.StatusForbidden {
			t.Errorf("GET /api/download-site with Host %q: status %d, want 403", host, w.Code)
		}
	}
	// Not only the API: the SPA and every other path answer only to loopback.
	var hit bool
	g := localhostModeHandler(nil, reachedMux(&hit))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, localRequest(http.MethodGet, "/_/", "evil.example:8080", nil))
	if w.Code != http.StatusForbidden || hit {
		t.Errorf("GET /_/ with a rebinding Host: status %d, reached=%v; want 403", w.Code, hit)
	}
}

func TestLocalhostAcceptsEveryLoopbackName(t *testing.T) {
	for _, host := range []string{"localhost", "localhost:8080", "LOCALHOST:8080", "127.0.0.1", "127.0.0.1:65535", "[::1]", "[::1]:8080"} {
		var hit bool
		h := localhostModeHandler(nil, reachedMux(&hit))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, localRequest(http.MethodGet, "/api/download-site", host, nil))
		if w.Code != http.StatusOK || !hit {
			t.Errorf("GET with Host %q: status %d, reached=%v; want through", host, w.Code, hit)
		}
	}
}

// Run must serve through localhostModeHandler, or none of the above applies.
func TestRunServesThroughTheLocalhostGuard(t *testing.T) {
	data, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	if !strings.Contains(string(data), "http.ListenAndServe(addr, localhostModeHandler(server.Logger, mux))") {
		t.Error("Run must pass localhostModeHandler(server.Logger, mux) to ListenAndServe")
	}
}

// setupRoutesAPIPaths reads the /api/ patterns SetupRoutes registers.
func setupRoutesAPIPaths(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	src := string(data)
	start := strings.Index(src, "func SetupRoutes(")
	end := strings.Index(src, "func SetupReaderRoutes(")
	if start < 0 || end < start {
		t.Fatal("cannot find SetupRoutes in routes.go")
	}
	var paths []string
	for _, m := range regexp.MustCompile(`mux\.HandleFunc\("(/api/[^"]*)"`).FindAllStringSubmatch(src[start:end], -1) {
		paths = append(paths, m[1])
	}
	return paths
}

// A7: a self-hosted server exposed directly must not let a client choose the
// IP its limiter counts by sending Fly-Client-IP or X-Forwarded-For.
func TestRemoteIPIgnoresProxyHeadersWithoutATrustedProxy(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.50:9999"
	req.Header.Set("Fly-Client-IP", "203.0.113.42")
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got := remoteIP(req, false); got != "192.0.2.50" {
		t.Errorf("remoteIP without a trusted proxy = %q, want RemoteAddr 192.0.2.50", got)
	}
}

func TestSelfHostedLimiterCountsTheConnectionNotTheHeader(t *testing.T) {
	limiter := newPublicContentLimiter(1, time.Hour)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	for i, spoof := range []string{"203.0.113.1", "203.0.113.2"} {
		req := httptest.NewRequest(http.MethodGet, "/pql/everything", nil)
		req.RemoteAddr = "192.0.2.50:9999"
		req.Header.Set("Fly-Client-IP", spoof)
		req.Header.Set("X-Forwarded-For", spoof)
		w := httptest.NewRecorder()
		publicContentMiddleware(limiter, false, ok)(w, req)
		want := http.StatusOK
		if i == 1 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Errorf("request %d with spoofed IP %s: status %d, want %d", i+1, spoof, w.Code, want)
		}
	}
}
