package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// The three requests the security scan proved returned drafts and decrypted
// DMs: from a cross-site page against a local `polis serve`, and
// unauthenticated against a remote instance.
var ownerPQLRequests = []string{
	"/pql/all+drafts+from+me?format=json",
	"/pql/all+messages+from+bob.example.test",
	"/pql/all+messages+from+my+mutuals",
}

// stack is the self-hosted handler chain Run serves.
func stack(s *Server) http.Handler {
	routes := http.NewServeMux()
	SetupRoutes(routes, s)
	return localhostModeHandler(nil, s.backendMiddleware(s.keyReloadMiddleware(s.ownerGateMiddleware(routes))))
}

// ⛔⛔ LOCAL: loopback owner-trust never extends to another site's page.
func TestACrossSitePageCannotReadOwnerPQLFromALocalServer(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.Local
	s.BoundToLoopback = true
	h := stack(s)
	for _, p := range ownerPQLRequests {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		r.Host = "localhost"
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Errorf("cross-site GET %s on a local server returned 200", p)
		}
		if w.Header().Get("Access-Control-Allow-Origin") == "*" {
			t.Errorf("cross-site GET %s carried ACAO *", p)
		}
	}
}

// ⛔ Another localhost port is same-SITE to a browser, not same-origin: a page
// served by some other local dev server is not the owner. Neither header may
// talk the other out of a refusal.
func TestASameSitePageCannotReadOwnerPQLFromALocalServer(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.Local
	s.BoundToLoopback = true
	h := stack(s)
	for _, hdr := range []map[string]string{
		{"Origin": "http://localhost:3000", "Sec-Fetch-Site": "same-site"},
		{"Sec-Fetch-Site": "same-site"},
		{"Origin": "http://localhost:3000", "Sec-Fetch-Site": "same-origin"},
	} {
		r := httptest.NewRequest(http.MethodGet, ownerPQLRequests[0], nil)
		r.Host = "localhost:8080"
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Errorf("GET %s with %v on a local server returned 200", ownerPQLRequests[0], hdr)
		}
	}
}

// ⭐ The owner's own SPA, same origin, is unaffected on loopback.
func TestTheOwnersOwnPageStillReadsOwnerPQLLocally(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.Local
	s.BoundToLoopback = true
	h := stack(s)
	r := httptest.NewRequest(http.MethodGet, ownerPQLRequests[0], nil)
	r.Host = "localhost"
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
		t.Errorf("the owner's own page was refused (%d)", w.Code)
	}
}

// ⛔⛔ REMOTE: an unauthenticated caller gets none of it.
func TestAStrangerCannotReadOwnerPQLFromARemoteInstance(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false
	h := stack(s)
	for _, p := range ownerPQLRequests {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		r.Host = "localhost" // the localhost guard is not what refuses here
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Errorf("unauthenticated GET %s on a remote instance returned 200", p)
		}
	}
}

// ⭐ Public scopes keep their CORS; owner scopes never get a wildcard.
func TestPublicPQLKeepsItsCORSAndOwnerPQLDoesNot(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.Local
	s.BoundToLoopback = true
	h := stack(s)
	get := func(p string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		r.Host = "localhost"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := get("/pql/all+posts+from+all+polis?format=json"); w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("a public scope lost its CORS (status %d)", w.Code)
	}
	if w := get(ownerPQLRequests[0]); w.Header().Get("Access-Control-Allow-Origin") == "*" {
		t.Error("an owner-private response carried ACAO *")
	}
}
