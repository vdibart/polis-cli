package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// localhostModeHandler wraps the localhost-mode mux (the Run path: polis-server
// and polis-full serve) in its middleware: security headers (outermost) →
// request logging → the localhost guard → mux.
//
// Localhost mode trusts its caller by construction: many handlers serve
// without auth, and hooks run scripts as the user. The bind is loopback-only,
// but a browser on the same machine is also on loopback, and it will carry out
// requests for any page the owner visits. localhostGuard is what stops that
// page acting as the owner.
func localhostModeHandler(logger *Logger, mux http.Handler) http.Handler {
	return securityHeadersMiddleware(requestLoggingMiddleware(logger, localhostGuard(mux)))
}

// localhostCrossOriginAPIRoutes lists the /api/ routes a browser may call from
// another site in localhost mode. ⛔ It is empty, deliberately: the localhost
// /api/widget/* handlers carry no widget-token check (that lives in the hosted
// layer), and no widget can connect to a localhost server, so nothing
// legitimate calls them cross-origin. A route added here needs its own auth.
var localhostCrossOriginAPIRoutes = []string{}

// localhostGuard refuses, with 403:
//   - any request whose Host is not a loopback name, which defeats DNS
//     rebinding (a name the attacker controls, resolving to 127.0.0.1, would
//     otherwise make their page same-origin with this server);
//   - a state-changing /api/ request that a browser sent from another site
//     (CrossSiteWrite), unless the route is in localhostCrossOriginAPIRoutes.
func localhostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "this server answers only to localhost", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && CrossSiteWrite(r) && !localhostCrossOriginAllowed(r.URL.Path) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func localhostCrossOriginAllowed(path string) bool {
	for _, p := range localhostCrossOriginAPIRoutes {
		if p == path {
			return true
		}
	}
	return false
}

// isLoopbackHost reports whether a Host header names this machine's loopback
// interface: localhost, 127.0.0.1 or [::1], with or without a port.
func isLoopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// CrossSiteWrite reports whether r is a state-changing request (any method but
// GET, HEAD and OPTIONS) that a browser sent from another origin: its Origin is
// present and is not the request's own, or its Sec-Fetch-Site is present and is
// neither "same-origin" nor "none". A request with neither header (curl, the
// CLI, a server) is not cross-site; a browser always sends Origin on a
// cross-site POST.
//
// Shared by the localhost guard and the hosted edge, where "the request's own
// origin" is the tenant's.
func CrossSiteWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
		return true
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return true
	}
	return false
}

// sameOrigin reports whether an Origin header names the host the request was
// addressed to. "null" (an opaque origin) is never the same.
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}
