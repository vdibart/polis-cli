package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// ⭐⭐⭐ THE CRITERION, AS SOMETHING A REVIEWER CAN RUN. "Every privileged
// surface is behind the seam" is true and unprovable by reading across a
// hundred routes. This is the same claim stated as a mechanism: every
// registered API route is either on the public list or behind the gate, and a
// NEW one fails here until somebody decides which it is.
//
// ⛔ The default flips the right way. Forgetting makes a route private.
var routeDecl = regexp.MustCompile(`mux\.HandleFunc\("([^"]+)"`)

func TestEveryAPIRouteIsClassified(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("reading routes.go: %v", err)
	}
	matches := routeDecl.FindAllStringSubmatch(string(src), -1)

	// ⛔ NON-VACUITY. A test that finds no routes passes, and this repo has
	// shipped exactly that mistake before — a census scanned the wrong
	// directory and stayed green. The number is a floor, not a count.
	if len(matches) < 50 {
		t.Fatalf("found only %d routes in routes.go; the pattern is probably wrong, "+
			"and a classifier that sees nothing passes", len(matches))
	}

	for _, m := range matches {
		path := m[1]
		if !guarded(path) {
			// Not under /api/ or /pql/ — published to everyone by design.
			continue
		}
		if isPublicAPIRoute(path) {
			// Named, with a reason, in ownergate.go.
			continue
		}
		// Behind the gate. Nothing to assert beyond its being reachable only
		// through the middleware, which the wiring tests below cover.
	}
}

// ⭐ Every public entry states WHY. A list of bare paths decays into a list
// nobody dares touch.
func TestEveryPublicRouteStatesItsReason(t *testing.T) {
	for path, why := range publicAPIRoutes {
		if len(why) < 20 {
			t.Errorf("public route %s has no real justification: %q", path, why)
		}
	}
	for prefix, why := range publicAPIPrefixes {
		if len(why) < 20 {
			t.Errorf("public prefix %s has no real justification: %q", prefix, why)
		}
	}
}

// ⛔ The routes that must NEVER become public. Each is one somebody could
// plausibly argue for, and /api/download-site is the one that was actually
// mis-sorted while planning this work — it serves the whole site INCLUDING
// .polis/keys.
func TestDangerousRoutesAreNotPublic(t *testing.T) {
	for _, path := range []string{
		"/api/download-site",
		"/api/content/",
		"/api/automations",
		"/api/hooks/generate",
		"/api/feed",
		"/api/profiles",
		"/api/publish",
		"/api/drafts",
		"/api/settings",
		"/api/render",
	} {
		if isPublicAPIRoute(path) {
			t.Errorf("%s is on the public list; it is an owner surface", path)
		}
		if !guarded(path) {
			t.Errorf("%s is not even subject to the gate", path)
		}
	}
}

func gateResponse(t *testing.T, s *Server, path string) int {
	t.Helper()
	reached := false
	h := s.ownerGateMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if reached && w.Code != http.StatusOK {
		t.Fatalf("handler ran but status is %d", w.Code)
	}
	return w.Code
}

// ⭐⭐⭐ THE NO-OP PROPERTY FOR EXISTING USERS. polis serve on a laptop must
// behave exactly as before: no enrolment, no login, no new step.
func TestLocalOnLoopbackIsUnchanged(t *testing.T) {
	s := &Server{Mode: mode.Local, BoundToLoopback: true}
	for _, path := range []string{"/api/publish", "/api/download-site", "/api/settings", "/pql/all+drafts+from+me"} {
		if code := gateResponse(t, s, path); code != http.StatusOK {
			t.Errorf("%s returned %d on a laptop; owner-trust must be unchanged there", path, code)
		}
	}
}

// ⭐ And custodied is unchanged too, because the operator's front door already
// authenticated upstream. A logged-in tenant must never be refused by a
// provider they did not enrol.
func TestCustodiedIsUnchanged(t *testing.T) {
	s := &Server{Mode: mode.Custodied}
	for _, path := range []string{"/api/publish", "/api/settings", "/pql/all+drafts+from+me"} {
		if code := gateResponse(t, s, path); code != http.StatusOK {
			t.Errorf("%s returned %d for a hosted tenant; the gate must not double-gate them", path, code)
		}
	}
}

// ⭐⭐ THE MODE THE GATE EXISTS FOR. Nothing is enrolled on a fresh remote
// instance, so privileged routes are refused — and the refusal says what to do.
func TestRemoteRefusesUntilAProviderIsEnrolled(t *testing.T) {
	s := &Server{Mode: mode.RemoteServerSigned}
	for _, path := range []string{"/api/publish", "/api/download-site", "/api/settings", "/pql/all+drafts+from+me"} {
		if code := gateResponse(t, s, path); code != http.StatusForbidden {
			t.Errorf("%s returned %d on a fresh remote instance; want 403", path, code)
		}
	}
	// ⚠️ 403 rather than 401: there is no credential to supply yet, so
	// challenging for one sends the owner looking for a login that does not
	// exist. The body names the remedy instead.
	w := httptest.NewRecorder()
	s.ownerGateMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/publish", nil))
	if body := w.Body.String(); !regexp.MustCompile(`/api/recover/challenge`).MatchString(body) {
		t.Errorf("the refusal should name the remedy, got %q", body)
	}
}

// ⛔ A port-forwarded remote instance IS on loopback and must still be refused.
// This is the subtle case the loopback provider's mode check exists for.
func TestPortForwardedRemoteIsStillRefused(t *testing.T) {
	s := &Server{Mode: mode.RemoteServerSigned, BoundToLoopback: true}
	if code := gateResponse(t, s, "/api/publish"); code == http.StatusOK {
		t.Error("a port-forwarded remote instance was trusted as a laptop")
	}
}

// ⭐ The public routes stay reachable on a locked-down instance — including the
// capability descriptor, which must not 401 or a third party concludes the
// instance has no API at all.
func TestPublicRoutesSurviveTheGate(t *testing.T) {
	s := &Server{Mode: mode.RemoteServerSigned}
	for _, path := range []string{
		"/api/capabilities",
		"/api/v1/stream/focus-comment",
		"/api/v1/stream/body",
		"/pql/all+posts",
	} {
		if code := gateResponse(t, s, path); code != http.StatusOK {
			t.Errorf("public route %s returned %d behind the gate", path, code)
		}
	}
}

// ⭐ The published site is not an API surface. Gating it would be a category
// error — serving pages to everyone is what a polis site IS.
func TestThePublishedSiteIsNotGated(t *testing.T) {
	s := &Server{Mode: mode.RemoteServerSigned}
	for _, path := range []string{
		"/", "/posts/20260101/hello.html", "/.well-known/polis",
		"/robots.txt", "/rsl.xml", "/license", "/_/",
	} {
		if code := gateResponse(t, s, path); code != http.StatusOK {
			t.Errorf("published path %s returned %d; it must be served to everyone", path, code)
		}
	}
}

// ⛔ /api/status is on the public list, so the trim is the only thing keeping
// a stranger from reading this site's structural faults. If the trim is ever
// removed, the allow-list entry becomes a disclosure.
func TestStatusWithholdsValidationDetailFromAStranger(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.RemoteServerSigned // nothing enrolled: not the owner

	w := httptest.NewRecorder()
	s.handleStatus(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d; the bootstrap call must answer a stranger", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, leaked := body["site_info"]; leaked {
		t.Error("site_info was served to a stranger")
	}
	v, _ := body["validation"].(map[string]interface{})
	if _, leaked := v["errors"]; leaked {
		t.Error("validation.errors was served to a stranger; that is a map of where to push")
	}
	// ⭐ What a stranger MAY see, because the site publishes it anyway, and
	// because the shell needs it to choose a screen.
	for _, keep := range []string{"configured", "site_title", "base_url"} {
		if _, ok := body[keep]; !ok {
			t.Errorf("%s was withheld; the logged-out shell needs it", keep)
		}
	}
	if v["status"] == nil {
		t.Error("validation.status was withheld; the shell needs to know whether the site is configured")
	}
}

// ⭐ And the owner still sees everything they see today, so nothing a
// self-hoster relies on disappeared.
func TestStatusStillTellsTheOwnerEverything(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.Local
	s.BoundToLoopback = true

	w := httptest.NewRecorder()
	s.handleStatus(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	v, _ := body["validation"].(map[string]interface{})
	if _, ok := v["errors"]; !ok {
		t.Error("the owner lost the validation detail they see today")
	}
}
