package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

func offSwitch(names ...string) func(string) bool {
	return func(name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
}

func assertSwitchedOff(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != api.SwitchedOffRetryAfter ||
		!strings.Contains(w.Body.String(), api.SwitchedOffMessage) {
		t.Fatalf("%s = %d (Retry-After %q) %s, want 503 with Retry-After and the switched-off sentence",
			what, w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
}

// ⭐ The live rosie switch sits beside RosiePaused: the sync cycle and catch-up
// both find no grant, so nothing is decided and nothing is sent.
func TestRosieSwitchedOffSendsNoMarkedRequest(t *testing.T) {
	s, ds := rosieServer(t, "bless pub.polis.comment from all")
	switchRosieOnByDefault(t, s)
	s.SwitchedOff = offSwitch(api.SwitchRosie)
	if s.rosieLiveGrant() != nil {
		t.Fatal("rosieLiveGrant returned a grant while rosie is switched off")
	}
	(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{requestedEvent()})
	s.rosieCatchUp()
	if ds.writeCount() != 0 {
		t.Fatalf("%d relationship requests sent while rosie is switched off", ds.writeCount())
	}

	// Switched back on (the func now answers no): the same server decides again,
	// with no reload. The cached tenant server is the reason this is a func.
	s.SwitchedOff = offSwitch()
	if s.rosieLiveGrant() == nil {
		t.Fatal("rosie stayed off after the switch was removed")
	}
}

func TestWidgetCommentsSwitchedOff(t *testing.T) {
	s := newConfiguredServer(t)
	s.SwitchedOff = offSwitch(api.SwitchWidgetComments)
	for _, c := range []struct {
		path string
		h    func(http.ResponseWriter, *http.Request)
		body map[string]string
	}{
		{"/api/widget/comment", s.handleWidgetComment, map[string]string{"target": "https://alice.polis.pub/posts/20260220/hello-world", "text": "hi"}},
	} {
		w := httptest.NewRecorder()
		c.h(w, httptest.NewRequest(http.MethodPost, c.path, jsonBody(t, c.body)))
		assertSwitchedOff(t, c.path, w)
	}
}

func TestServerMCPSwitchedOff(t *testing.T) {
	s := newConfiguredServer(t)
	s.SwitchedOff = offSwitch(api.SwitchMCP)
	w := httptest.NewRecorder()
	s.handleMCP(w, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	assertSwitchedOff(t, "POST /mcp", w)
}

// The switches reach the API: the host's func is handed to /v1 unchanged.
func TestServerHandsItsSwitchesToTheAPI(t *testing.T) {
	s := newConfiguredServer(t)
	s.SwitchedOff = offSwitch(api.SwitchAPITokens)
	h := s.apiHost()
	if h.SwitchedOff == nil || !h.SwitchedOff(api.SwitchAPITokens) || h.SwitchedOff(api.SwitchRosie) {
		t.Fatal("apiHost did not pass the server's SwitchedOff through")
	}
}
