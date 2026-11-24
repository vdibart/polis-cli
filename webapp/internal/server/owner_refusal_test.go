package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

type refusalBody struct {
	Error  string  `json:"error"`
	Reason string  `json:"reason"`
	Remedy string  `json:"remedy"`
	WaysIn []wayIn `json:"ways_in"`
}

func gateRefusal(t *testing.T, s *Server, path string) (int, refusalBody) {
	t.Helper()
	w := httptest.NewRecorder()
	s.ownerGateMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	var body refusalBody
	if w.Code != http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("refusal is not JSON: %v (%q)", err, w.Body.String())
		}
	}
	return w.Code, body
}

func findWay(ways []wayIn, name string) (wayIn, bool) {
	for _, w := range ways {
		if w.Name == name {
			return w, true
		}
	}
	return wayIn{}, false
}

// ⭐ The SPA picks setup or sign-in from the refusal, so the two refusals must
// differ in a field a program reads, not only in prose.
func TestGateRefusalNamesNoProviderOnAFreshInstance(t *testing.T) {
	s := enrolServer(t)
	s.Mode, s.BoundToLoopback = mode.RemoteServerSigned, false

	code, body := gateRefusal(t, s, "/api/publish")
	if code != http.StatusForbidden || body.Reason != RefusalNoProvider {
		t.Fatalf("fresh remote instance: got %d reason %q, want 403 %q", code, body.Reason, RefusalNoProvider)
	}
	if body.Remedy == "" {
		t.Error("the no-provider refusal lost its remedy")
	}
	// ⭐ The setup screen enrols from this list, so passkey must be on it with
	// its ceremony kind.
	pk, ok := findWay(body.WaysIn, passkeyName)
	if !ok || pk.Kind != PasskeyCeremony || !pk.Enrollable {
		t.Errorf("passkey should be offered as an enrollable webauthn way in, got %+v (found=%v)", pk, ok)
	}
}

func TestGateRefusalNamesNotSignedInOnceSomethingIsEnrolled(t *testing.T) {
	s, _ := passkeyServer(t)

	code, body := gateRefusal(t, s, "/api/publish")
	if code != http.StatusUnauthorized || body.Reason != RefusalNotSignedIn {
		t.Fatalf("enrolled remote instance: got %d reason %q, want 401 %q", code, body.Reason, RefusalNotSignedIn)
	}
	pk, ok := findWay(body.WaysIn, passkeyName)
	if !ok || pk.Kind != PasskeyCeremony {
		t.Errorf("the sign-in screen needs passkey offered with its kind, got %+v (found=%v)", pk, ok)
	}
}

// ⛔ A GENERIC SIGN-IN: the refusal says which ways in the instance supports,
// never which the owner set up. Two instances configured alike, one with a
// passkey enrolled and one with nothing, must offer byte-identical ways in,
// and no entry may carry an enrolment flag.
func TestGateRefusalNeverSaysWhichWaysInTheOwnerSetUp(t *testing.T) {
	rawWays := func(s *Server) string {
		t.Helper()
		w := httptest.NewRecorder()
		s.ownerGateMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/publish", nil))
		var body map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("refusal is not JSON: %v (%q)", err, w.Body.String())
		}
		return string(body["ways_in"])
	}

	fresh := enrolServer(t)
	fresh.Mode, fresh.BoundToLoopback = mode.RemoteServerSigned, false
	enrolled, _ := passkeyServer(t)

	a, b := rawWays(fresh), rawWays(enrolled)
	if a != b {
		t.Errorf("the ways in differ with what the owner enrolled:\n nothing enrolled: %s\n passkey enrolled: %s", a, b)
	}
	if strings.Contains(b, "enrolled") {
		t.Errorf("the refusal says what is enrolled: %s", b)
	}
	if !strings.Contains(b, passkeyName) {
		t.Fatalf("non-vacuity: passkey should be offered, got %s", b)
	}
}

// ⛔ TRIMMED, NOT OPENED: a provider that cannot work here is not advertised
// to a stranger. The owner sees unavailable ones in the signed-in list.
func TestGateRefusalListsOnlyAvailableWaysIn(t *testing.T) {
	s := enrolServer(t)
	s.Mode, s.BoundToLoopback = mode.RemoteServerSigned, false

	_, body := gateRefusal(t, s, "/api/publish")
	for _, name := range []string{"loopback", "operator"} {
		if _, ok := findWay(body.WaysIn, name); ok {
			t.Errorf("%s is unavailable on a remote instance and must not be offered as a way in", name)
		}
	}
}

// ⭐ /api/providers reports the ceremony kind, so the settings list can offer a
// passkey's set-up action without naming the provider.
func TestProvidersReportsCeremonyKind(t *testing.T) {
	s := enrolServer(t)
	w := call(t, s, http.MethodGet, ProvidersPath, s.handleProviders)
	if w.Code != http.StatusOK {
		t.Fatalf("GET providers: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Providers []struct {
			Name       string `json:"name"`
			Kind       string `json:"kind"`
			Enrollable bool   `json:"enrollable"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range resp.Providers {
		seen[p.Name] = true
		switch p.Name {
		case passkeyName:
			if p.Kind != PasskeyCeremony || !p.Enrollable {
				t.Errorf("passkey: kind %q enrollable %v", p.Kind, p.Enrollable)
			}
		case "loopback":
			if p.Kind != "" || p.Enrollable {
				t.Errorf("loopback has nothing to set up, got kind %q enrollable %v", p.Kind, p.Enrollable)
			}
		}
	}
	if !seen[passkeyName] || !seen["loopback"] {
		t.Errorf("expected passkey and loopback in the list, got %v", seen)
	}
}
