package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// ⛔⛔ A FRESH REMOTE OWNER MUST BE ABLE TO REACH ENROLMENT THROUGH THE GATE.
// The gate matched the enrolment prefix as an exact path, so
// /api/enrol/passkey/begin was refused 403 before the handler could honour
// recovery's enrolment session. The existing enrolment tests called the
// handler directly and never saw it; this one goes through the gate.
func TestEnrolmentIsReachableThroughTheGate(t *testing.T) {
	s := enrolServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false
	reached := map[string]bool{}
	h := s.ownerGateMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached[r.URL.Path] = true
	}))
	for _, p := range []string{"/api/enrol/passkey/begin", "/api/enrol/passkey/complete"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, p, nil))
		if !reached[p] {
			t.Errorf("%s was refused by the gate; a fresh owner could never enrol", p)
		}
	}
	// ⛔ The prefix must not open anything else: an ordinary private route is
	// still refused to a stranger.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/publish", nil))
	if reached["/api/publish"] {
		t.Error("a private route reached its handler without an owner")
	}
}

// ⭐ And the handler itself still refuses a stranger: passing the gate is not
// authorisation, enrolAuthorised is.
func TestTheEnrolmentHandlerRefusesAStranger(t *testing.T) {
	s := enrolServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false
	w := httptest.NewRecorder()
	s.handleEnrol(w, httptest.NewRequest(http.MethodPost, "/api/enrol/passkey/begin", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("a stranger got %d from the enrolment handler; want 401", w.Code)
	}
}
