package server

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

var tlsMarker = tls.ConnectionState{}

func recoveryServer(t *testing.T) (*Server, []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := owner.NewRecovery(string(pub), "alice.example.test")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := &Server{Mode: mode.RemoteServerSigned, Recovery: rec, DataDir: dir, BaseURL: "https://alice.example.test"}
	s.ownerStoreOnce.Do(func() {})
	s.ownerStoreV = storage.NewSite(dir)
	return s, priv
}

func issue(t *testing.T, s *Server) owner.Challenge {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleRecoveryChallenge(w, httptest.NewRequest(http.MethodGet, RecoveryChallengePath, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("challenge: status %d body %s", w.Code, w.Body.String())
	}
	var ch owner.Challenge
	if err := json.Unmarshal(w.Body.Bytes(), &ch); err != nil {
		t.Fatalf("challenge is not JSON: %v", err)
	}
	return ch
}

func postProof(t *testing.T, s *Server, nonce, sig string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"nonce": nonce, "signature": sig})
	r := httptest.NewRequest(http.MethodPost, RecoveryVerifyPath, strings.NewReader(string(body)))
	r.TLS = &tlsMarker
	w := httptest.NewRecorder()
	s.handleRecoveryVerify(w, r)
	return w
}

// ⭐ The whole flow over HTTP, with real crypto: ask, sign, exchange for a
// session cookie.
func TestRecoveryExchangesAProofForAnEnrolmentSession(t *testing.T) {
	s, priv := recoveryServer(t)
	ch := issue(t, s)

	sig, err := signing.SignContentNS([]byte(ch.Message), priv, owner.RecoveryNamespace)
	if err != nil {
		t.Fatal(err)
	}
	w := postProof(t, s, ch.Nonce, sig)
	if w.Code != http.StatusOK {
		t.Fatalf("a valid proof was rejected: %d %s", w.Code, w.Body.String())
	}

	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.Contains(c.Name, "enrol") {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no enrolment cookie was set")
	}
	// ⛔ The three attributes that keep a session token from leaking.
	if !cookie.HttpOnly {
		t.Error("the enrolment cookie is readable by script")
	}
	if !cookie.Secure {
		t.Error("the enrolment cookie is not Secure on a TLS request")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Error("the enrolment cookie is sent cross-site")
	}
	if !strings.HasPrefix(cookie.Name, "__Host-") {
		t.Errorf("on TLS the cookie should carry the __Host- prefix, got %q", cookie.Name)
	}
	// ⚠️ And the token must not be echoed where a confused owner could paste it.
	if strings.Contains(w.Body.String(), cookie.Value) {
		t.Error("the session token is echoed in the response body")
	}
	if _, err := owner.CheckEnrolment(s.instance(), cookie.Value); err != nil {
		t.Error("the issued session is not valid")
	}
}

// ⛔ ONE STATUS AND ONE MESSAGE FOR EVERY PROOF FAILURE. Separating "unknown
// nonce" from "bad signature" tells a caller which nonces existed.
func TestEveryProofFailureLooksTheSame(t *testing.T) {
	s, priv := recoveryServer(t)

	unknown := postProof(t, s, "never-issued", "sig")

	ch := issue(t, s)
	other, _, _ := signing.GenerateKeypair()
	badSig, _ := signing.SignContentNS([]byte(ch.Message), other, owner.RecoveryNamespace)
	wrongKey := postProof(t, s, ch.Nonce, badSig)

	ch2 := issue(t, s)
	good, _ := signing.SignContentNS([]byte(ch2.Message), priv, owner.RecoveryNamespace)
	_ = postProof(t, s, ch2.Nonce, good)
	replayed := postProof(t, s, ch2.Nonce, good)

	for name, w := range map[string]*httptest.ResponseRecorder{
		"unknown nonce": unknown, "wrong key": wrongKey, "replayed": replayed,
	} {
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, w.Code)
		}
	}
	if unknown.Body.String() != wrongKey.Body.String() || unknown.Body.String() != replayed.Body.String() {
		t.Errorf("the three failures are distinguishable:\n  unknown: %s  wrongkey: %s  replay: %s",
			unknown.Body.String(), wrongKey.Body.String(), replayed.Body.String())
	}
}

// ⭐ Every refusal carries a remedy. An owner meeting this endpoint is by
// definition locked out; "unauthorized" alone tells them nothing to act on.
func TestRefusalsCarryARemedy(t *testing.T) {
	s, _ := recoveryServer(t)
	w := postProof(t, s, "nope", "sig")
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["remedy"] == "" {
		t.Errorf("no remedy in a proof refusal: %v", body)
	}

	// And the no-key case names the variable to set.
	bare := &Server{Mode: mode.RemoteServerSigned}
	w = httptest.NewRecorder()
	bare.handleRecoveryChallenge(w, httptest.NewRequest(http.MethodGet, RecoveryChallengePath, nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 when no recovery key is configured", w.Code)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !strings.Contains(body["remedy"], owner.RecoveryKeyEnv) {
		t.Errorf("the remedy does not name %s: %v", owner.RecoveryKeyEnv, body)
	}
}

// ⚠️ Both routes are public, so the rate limit is the only thing between them
// and free compute.
func TestRecoveryIsRateLimited(t *testing.T) {
	s, _ := recoveryServer(t)
	var lastCode int
	for i := 0; i < attemptsPerAddress+5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, RecoveryChallengePath, nil)
		r.RemoteAddr = "203.0.113.9:1234"
		s.handleRecoveryChallenge(w, r)
		lastCode = w.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Errorf("after %d requests the limiter had not engaged (status %d)", attemptsPerAddress+5, lastCode)
	}
}

// ⭐ One visitor must not be able to lock everyone else out — which is why the
// limiter keys on the forwarded client rather than on RemoteAddr alone.
func TestOneClientCannotExhaustAnother(t *testing.T) {
	// ⭐ Telling two clients apart by X-Forwarded-For is only valid BEHIND A
	// TRUSTED PROXY; without one, the header is the client's own claim.
	s, _ := recoveryServer(t)
	s.Proxy = ProxyFly
	for i := 0; i < attemptsPerAddress+5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, RecoveryChallengePath, nil)
		r.RemoteAddr = "10.0.0.1:1"
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		s.handleRecoveryChallenge(w, r)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, RecoveryChallengePath, nil)
	r.RemoteAddr = "10.0.0.1:1" // same proxy
	r.Header.Set("X-Forwarded-For", "198.51.100.4")
	s.handleRecoveryChallenge(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("a second client behind the same proxy was rate-limited at %d", w.Code)
	}
}

// ⛔ Both routes must be reachable on a locked-down instance, or recovery is
// impossible by construction.
func TestRecoveryRoutesSurviveTheOwnerGate(t *testing.T) {
	s := &Server{Mode: mode.RemoteServerSigned}
	for _, path := range []string{RecoveryChallengePath, RecoveryVerifyPath} {
		if !isPublicAPIRoute(path) {
			t.Errorf("%s is not on the public allow-list; a locked-out owner could never reach it", path)
		}
		if code := gateResponse(t, s, path); code != http.StatusOK {
			t.Errorf("%s returned %d through the gate", path, code)
		}
	}
}

func TestRecoveryRejectsWrongMethods(t *testing.T) {
	s, _ := recoveryServer(t)
	w := httptest.NewRecorder()
	s.handleRecoveryVerify(w, httptest.NewRequest(http.MethodGet, RecoveryVerifyPath, nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET on verify returned %d, want 405", w.Code)
	}
}
