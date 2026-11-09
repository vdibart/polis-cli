package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
)

// ⛔ (1) A spoofed X-Forwarded-For per request from ONE connection is limited,
// and the owner, on another connection, can still get a challenge.
func TestSpoofedForwardedForCannotExhaustRecovery(t *testing.T) {
	s, _ := recoveryServer(t)
	limited := 0
	for i := 0; i < 64; i++ {
		r := httptest.NewRequest(http.MethodGet, RecoveryChallengePath, nil)
		r.RemoteAddr = "203.0.113.50:4000"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		w := httptest.NewRecorder()
		s.handleRecoveryChallenge(w, r)
		if w.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("64 spoofed X-Forwarded-For values from one connection were never limited")
	}
	r := httptest.NewRequest(http.MethodGet, RecoveryChallengePath, nil)
	r.RemoteAddr = "192.0.2.7:5000"
	w := httptest.NewRecorder()
	s.handleRecoveryChallenge(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("the owner could not obtain a challenge after the flood: %d %s", w.Code, w.Body.String())
	}
}

// passkeyServer is a remote instance with one enrolled passkey credential.
func passkeyServer(t *testing.T, creds ...string) (*Server, *PasskeyProvider) {
	t.Helper()
	s := enrolServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false
	if len(creds) == 0 {
		creds = []string{"AQ=="}
	}
	var parts []string
	for _, c := range creds {
		parts = append(parts, `{"id":"`+c+`"}`)
	}
	if err := owner.SaveEnrolment(s.instance(), passkeyName,
		[]byte(`{"credentials":[`+strings.Join(parts, ",")+`],"user_id":"dQ=="}`)); err != nil {
		t.Fatal(err)
	}
	return s, NewPasskeyProvider()
}

// ⛔ (2) Each sign-in ceremony carries its own state: a stranger's begin does
// not replace the owner's, and a junk completion consumes nothing.
func TestAStrangersBeginOrJunkCompletionCannotTouchTheOwnersSignIn(t *testing.T) {
	s, va := enrolledPasskeyServer(t)
	owners := signInBegin(t, s)
	_ = signInBegin(t, s) // a stranger's, in another browser
	// A junk completion with the owner's ceremony cookie.
	w := postWith(t, s, SignInCompletePath, []byte(`{"junk":true}`), s.handleSignInComplete, owners.cookies...)
	if w.Code == http.StatusOK {
		t.Fatal("a junk completion succeeded")
	}
	// ⭐ The owner's ceremony still completes.
	w = postWith(t, s, SignInCompletePath, va.assert(owners.body), s.handleSignInComplete, owners.cookies...)
	if w.Code != http.StatusOK {
		t.Errorf("the owner's sign-in failed after a stranger's begin and a junk completion: %d %s", w.Code, w.Body.String())
	}
}

// ⛔ (4) Sign-in requires user verification.
func TestSignInRequiresUserVerification(t *testing.T) {
	s, p := passkeyServer(t)
	opts, _, err := p.BeginSignIn(s.instance())
	if err != nil {
		t.Fatal(err)
	}
	a, ok := opts.(*protocol.CredentialAssertion)
	if !ok || a.Response.UserVerification != protocol.VerificationRequired {
		t.Errorf("sign-in options do not require user verification: %+v", opts)
	}
}

// ⛔ (3) Removing a credential ends every passkey session, re-enrolling does
// not revive them, and ONE credential can be removed on its own.
func TestRemovingACredentialEndsSessionsAndReEnrollingDoesNotReviveThem(t *testing.T) {
	s, p := passkeyServer(t, "AQ==", "Ag==")
	inst := s.instance()
	stolen, _, _ := owner.OpenSignIn(inst, passkeyName)
	if err := p.RemoveCredential(inst, "Ag"); err != nil {
		t.Fatal(err)
	}
	if owner.SignedIn(inst, stolen) {
		t.Error("⛔ a session survived the removal of a credential")
	}
	rec, _ := p.load(inst)
	if len(rec.Credentials) != 1 {
		t.Errorf("%d credentials after removing one of two", len(rec.Credentials))
	}
	stolen, _, _ = owner.OpenSignIn(inst, passkeyName)
	if err := owner.DeleteEnrolment(inst, passkeyName); err != nil {
		t.Fatal(err)
	}
	_ = owner.SaveEnrolment(inst, passkeyName, []byte(`{"credentials":[{"id":"Aw=="}],"user_id":"dQ=="}`))
	if owner.SignedIn(inst, stolen) {
		t.Error("⛔ re-enrolling revived a session from before the removal")
	}
}

// ⛔ (5) A recovery session can enrol and nothing else, and is not a sign-in.
func TestARecoverySessionCanOnlyEnrol(t *testing.T) {
	s, _ := passkeyServer(t)
	tok, _, err := owner.OpenEnrolment(s.instance())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		r := httptest.NewRequest(m, ProvidersPath+"?name="+passkeyName, nil)
		r.AddCookie(&http.Cookie{Name: enrolmentCookiePlain, Value: tok})
		w := httptest.NewRecorder()
		s.handleProviders(w, r)
		if w.Code == http.StatusOK {
			t.Errorf("a recovery session could %s providers", m)
		}
	}
	if _, err := owner.CheckEnrolment(s.instance(), tok); err != nil {
		t.Error("a refused providers call consumed the recovery session")
	}
	// ⛔ Presented as a session cookie, it opens nothing.
	if code := gateResponseWith(t, s, "/api/publish", &http.Cookie{Name: signInCookiePlain, Value: tok}); code == http.StatusOK {
		t.Error("⛔ a recovery session passed the gate as a sign-in")
	}
}

// ⛔ (6) State-changing owner routes refuse another site's page, through
// Server.Handler() with nothing in front of it.
func TestOwnerRoutesRefuseCrossSiteThroughHandler(t *testing.T) {
	s, _ := passkeyServer(t)
	h := s.Handler()
	// ⭐ A SIGNED-IN owner's browser, tricked into a cross-site request: the
	// case CSRF protection exists for. Without a session most of these are
	// refused earlier, which would not prove the cross-site rule.
	session, _, _ := owner.OpenSignIn(s.instance(), passkeyName)
	// ⛔ same-site is a sibling subdomain or another localhost port: the page
	// this refuses. Each header refuses on its own, and neither overrides the
	// other.
	pages := []struct{ name, origin, fetchSite string }{
		{"cross-site", "https://evil.example", "cross-site"},
		{"same-site sibling", "https://evil.example.test", "same-site"},
		{"Sec-Fetch-Site same-site alone", "", "same-site"},
		{"Origin alone", "https://evil.example", ""},
		{"same-site claimed over a foreign Origin", "http://localhost:3000", "same-site"},
	}
	// /api/drafts is an ordinary owner route, not an auth one: a text/plain
	// POST needs no preflight, so the cookie rides along unless refused.
	paths := []string{SignInBeginPath, SignInCompletePath, SignOutPath, ProvidersPath + "?name=x", EnrolPathPrefix + "passkey/begin", RecoveryVerifyPath, "/api/drafts"}
	for _, pg := range pages {
		for _, p := range paths {
			method := http.MethodPost
			if strings.HasPrefix(p, ProvidersPath) {
				method = http.MethodDelete
			}
			r := httptest.NewRequest(method, p, strings.NewReader("{}"))
			r.Header.Set("Content-Type", "text/plain")
			if pg.origin != "" {
				r.Header.Set("Origin", pg.origin)
			}
			if pg.fetchSite != "" {
				r.Header.Set("Sec-Fetch-Site", pg.fetchSite)
			}
			r.AddCookie(&http.Cookie{Name: signInCookiePlain, Value: session})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s: %s %s → %d; want 403", pg.name, method, p, w.Code)
			}
		}
	}
}

// ⭐ The refusal is of other sites' pages, not of the owner: the same signed-in
// POST from the instance's own page reaches the handler, and a widget-token
// route stays callable from the site the widget is embedded on.
func TestOwnerGateCrossSiteRefusalSparesOwnPagesAndWidgets(t *testing.T) {
	s, _ := passkeyServer(t)
	h := s.Handler()
	session, _, _ := owner.OpenSignIn(s.instance(), passkeyName)
	r := httptest.NewRequest(http.MethodPost, "/api/drafts", strings.NewReader(`{"id":"x","markdown":"hello"}`))
	r.Header.Set("Origin", "http://"+r.Host)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(&http.Cookie{Name: signInCookiePlain, Value: session})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
		t.Errorf("same-origin owner POST /api/drafts → %d %s; want it to reach the handler", w.Code, w.Body.String())
	}

	// ⛔ On an instance with no operator nothing checks a widget token, so a
	// sibling page carrying the owner's cookie is refused on the widget
	// routes like on any other.
	for _, path := range []string{"/api/widget/publish", "/api/widget/comment", "/api/widget/follow"} {
		r = httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "text/plain")
		r.Header.Set("Origin", "https://sibling.example.test")
		r.Header.Set("Sec-Fetch-Site", "same-site")
		r.AddCookie(&http.Cookie{Name: signInCookiePlain, Value: session})
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "cross-site request refused") {
			t.Errorf("⛔ remote: a same-site POST %s with the owner's cookie → %d %s; want the cross-site refusal",
				path, w.Code, w.Body.String())
		}
	}

	// ⭐ Hosted: the edge has checked the widget token, so a visitor's widget
	// on someone else's site reaches the handler.
	s.Mode = capability.ForHosted()
	r = httptest.NewRequest(http.MethodPost, "/api/widget/comment", strings.NewReader("{}"))
	r.Header.Set("Origin", "https://someone-elses.example")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "cross-site request refused") {
		t.Errorf("hosted: a widget-token route was refused as cross-site: %d %s", w.Code, w.Body.String())
	}
}

// ⛔ (7) Cookies and session scope.
func TestSessionCookiesAndScope(t *testing.T) {
	s, _ := passkeyServer(t)
	w := httptest.NewRecorder()
	s.handleSignOut(w, httptest.NewRequest(http.MethodPost, SignOutPath, nil))
	for _, c := range w.Result().Cookies() {
		if c.Name == signInCookie && !c.Secure {
			t.Error("sign-out cleared the __Host- cookie without Secure; browsers ignore that")
		}
	}
	inst := s.instance()
	tok, _, _ := owner.OpenSignIn(inst, passkeyName)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.AddCookie(&http.Cookie{Name: signInCookiePlain, Value: tok})
	if signInToken(r) != "" {
		t.Error("the unprefixed session cookie was accepted over TLS")
	}
	other := inst
	other.SiteID = inst.SiteID + "-copy"
	if owner.SignedIn(other, tok) {
		t.Error("⛔ a session opened for one site was valid for another with the same base URL")
	}
}
