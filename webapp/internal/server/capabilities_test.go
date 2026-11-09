package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

func getDescriptor(t *testing.T, s *Server) (int, map[string]interface{}, http.Header) {
	t.Helper()
	r := httptest.NewRequest("GET", CapabilitiesPath, nil)
	w := httptest.NewRecorder()
	s.handleCapabilities(w, r)
	var body map[string]interface{}
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("response is not JSON: %v\n%s", err, w.Body.String())
		}
	}
	return w.Code, body, w.Header()
}

// ⭐ An unconfigured instance is local, because `polis serve` on a laptop is
// the common case and must need no configuration.
func TestUnsetModeServesTheLocalDescriptor(t *testing.T) {
	code, body, _ := getDescriptor(t, &Server{})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if body["canRunHooks"] != true || body["hasPublicApi"] != false {
		t.Errorf("an unset mode did not serve the local set: %v", body)
	}
	if body["v"] != capability.Version {
		t.Errorf("descriptor version = %v, want %q", body["v"], capability.Version)
	}
}

// ⛔ The served descriptor must be exactly what the derivation produces — no
// second table, no per-field override on the way out.
func TestServedDescriptorMatchesTheDerivation(t *testing.T) {
	for _, m := range mode.All() {
		code, body, _ := getDescriptor(t, &Server{Mode: m})
		if code != 200 {
			t.Fatalf("%s: status %d", m, code)
		}
		want, err := json.Marshal(capability.For(m))
		if err != nil {
			t.Fatal(err)
		}
		var expected map[string]interface{}
		_ = json.Unmarshal(want, &expected)
		for k, v := range expected {
			if body[k] != v {
				t.Errorf("%s: %s = %v, want %v", m, k, body[k], v)
			}
		}
	}
}

// ⛔ A capability set says what an instance can do, never who is using it. It
// is public on purpose: a third party must be able to read it before deciding
// how to talk to this site, and our own surface must use the same door.
func TestDescriptorIsReadableCrossOrigin(t *testing.T) {
	_, _, h := getDescriptor(t, &Server{Mode: mode.Custodied})
	if got := h.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q; a third party must be able to read it", got)
	}
	if h.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", h.Get("Content-Type"))
	}
}

// ⚠️ No session, no cookie, no body: a reader that has to authenticate to
// learn an instance has an API cannot use the answer.
func TestDescriptorNeedsNoCredentials(t *testing.T) {
	r := httptest.NewRequest("GET", CapabilitiesPath, nil)
	w := httptest.NewRecorder()
	(&Server{Mode: mode.Custodied}).handleCapabilities(w, r)
	if w.Code != 200 {
		t.Fatalf("an unauthenticated read got %d; the descriptor must be public", w.Code)
	}
}

func TestDescriptorRejectsWrites(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		r := httptest.NewRequest(method, CapabilitiesPath, nil)
		w := httptest.NewRecorder()
		(&Server{}).handleCapabilities(w, r)
		if w.Code != 405 {
			t.Errorf("%s got %d, want 405", method, w.Code)
		}
	}
	// HEAD is how a caller tests for presence alone.
	r := httptest.NewRequest("HEAD", CapabilitiesPath, nil)
	w := httptest.NewRecorder()
	(&Server{}).handleCapabilities(w, r)
	if w.Code != 200 {
		t.Errorf("HEAD got %d, want 200", w.Code)
	}
}

// ⛔ The route must be registered, or the SPA's fetch 404s and the instance
// looks like a served export — which is a different thing entirely.
func TestCapabilitiesRouteIsRegistered(t *testing.T) {
	mux := http.NewServeMux()
	SetupRoutes(mux, &Server{})
	r := httptest.NewRequest("GET", CapabilitiesPath, nil)
	_, pattern := mux.Handler(r)
	if pattern != CapabilitiesPath {
		t.Errorf("no handler registered at %s (matched %q)", CapabilitiesPath, pattern)
	}
}
