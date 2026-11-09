package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// stubEnroller is the fake provider D7 requires, exercised through the REAL
// routes rather than against the package directly.
//
// ⭐⭐⭐ THAT IS THE WHOLE POINT. If enrolling, listing or removing a provider
// ever needs code outside the provider's own file, this stub stops working and
// these tests fail — on the day the shortcut is cheap to undo, rather than the
// day the password or Vercel provider turns into a redesign.
type stubEnroller struct {
	began, completed bool
}

func (s *stubEnroller) Name() string                       { return "stub" }
func (s *stubEnroller) Available(inst owner.Instance) bool { return inst.Store != nil }
func (s *stubEnroller) Enrolled(inst owner.Instance) bool  { return owner.IsEnrolledIn(inst, "stub") }

// ⭐ An enrolled stub authorises, which is what makes the removal tests
// realistic: the owner is SIGNED IN via some provider and is pruning the list.
// ⚠️ It only answers when ENROLLED — the disjunction never consults an
// unenrolled provider — so the stranger test is unaffected.
func (s *stubEnroller) IsOwner(owner.Instance, *http.Request) (bool, error) { return true, nil }
func (s *stubEnroller) BeginEnrol(owner.Instance, []byte) (any, []byte, error) {
	s.began = true
	return map[string]string{"challenge": "stub-challenge"}, []byte("stub-state"), nil
}
func (s *stubEnroller) CompleteEnrol(inst owner.Instance, _, state []byte) (any, []byte, error) {
	// ⭐ The state it handed out at begin comes back, from whichever process.
	if string(state) != "stub-state" {
		return nil, nil, fmt.Errorf("stub: state %q did not survive the round trip", state)
	}
	s.completed = true
	return map[string]string{"enrolled": "stub"}, []byte(`{"stub":true}`), nil
}

// ⭐ Registered ONCE, at package init. The seam deliberately offers no way to
// deregister — the set of things that can call you the owner is a property of
// the binary — and tests do not need one: enrolment state is PER SITE, so each
// test's own TempDir isolates it. ⭐⭐ That the tests need no special hook is
// itself evidence the design holds.
var (
	sharedStub  = &stubEnroller{}
	sharedStub2 = &namedStub{name: "stub-two"}
)

func init() {
	owner.Register(sharedStub)
	owner.Register(sharedStub2)
}

func enrolServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	return &Server{
		Mode: mode.Local, BoundToLoopback: true,
		DataDir: dir, BaseURL: "https://alice.example.test",
		ownerStoreV: storage.NewSite(dir),
	}
}

func call(t *testing.T, s *Server, method, path string, h http.HandlerFunc, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	for _, c := range cookies {
		r.AddCookie(c)
	}
	h(w, r)
	return w
}

// cookiesFrom returns the cookies a response set, minus any it cleared.
func cookiesFrom(w *httptest.ResponseRecorder) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.MaxAge >= 0 && c.Value != "" {
			out = append(out, c)
		}
	}
	return out
}

// ⭐ A provider can be enrolled, listed and removed through the real routes
// with no code outside its own file.
func TestANewProviderWorksThroughTheRealRoutes(t *testing.T) {
	stub := sharedStub
	*stub = stubEnroller{}

	s := enrolServer(t)

	begin := call(t, s, http.MethodPost, EnrolPathPrefix+"stub/begin", s.handleEnrol)
	if begin.Code != 200 {
		t.Fatalf("begin: %d %s", begin.Code, begin.Body.String())
	}
	if !stub.began {
		t.Error("BeginEnrol was not reached")
	}
	// ⛔ Without the ceremony cookie there is nothing to complete.
	if w := call(t, s, http.MethodPost, EnrolPathPrefix+"stub/complete", s.handleEnrol); w.Code == 200 {
		t.Fatal("a completion with no ceremony succeeded")
	}
	ceremony := cookiesFrom(begin)
	if w := call(t, s, http.MethodPost, EnrolPathPrefix+"stub/complete", s.handleEnrol, ceremony...); w.Code != 200 {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}
	if !stub.completed {
		t.Error("CompleteEnrol was not reached")
	}
	// ⛔ And the same ceremony cannot complete twice.
	if w := call(t, s, http.MethodPost, EnrolPathPrefix+"stub/complete", s.handleEnrol, ceremony...); w.Code == 200 {
		t.Error("a ceremony completed twice")
	}

	// It must now appear in the list the owner reviews.
	w := call(t, s, http.MethodGet, ProvidersPath, s.handleProviders)
	if !strings.Contains(w.Body.String(), `"stub"`) {
		t.Errorf("the new provider is not listed: %s", w.Body.String())
	}
}

// ⛔ loopback and operator are providers with nothing to enrol. Saying so
// plainly beats a 404, which reads as "misspelled".
func TestAProviderWithNothingToEnrolSaysSo(t *testing.T) {
	s := enrolServer(t)
	w := call(t, s, http.MethodPost, EnrolPathPrefix+"loopback/begin", s.handleEnrol)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "cannot be enrolled") {
		t.Errorf("unhelpful refusal: %s", w.Body.String())
	}
}

// ⛔ D6: removing the last way in is PERMITTED, but only after an explicit
// acknowledgement that names the recovery path. Correct-and-surprising is
// still bad.
func TestRemovingTheLastProviderRequiresAcknowledgement(t *testing.T) {
	stub := sharedStub
	*stub = stubEnroller{}

	s := enrolServer(t)
	// Enrol it so it is the only enrolled provider on a non-loopback instance.
	if err := owner.SaveEnrolment(s.instance(), "stub", []byte("x")); err != nil {
		t.Fatal(err)
	}
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false

	// First attempt: refused, with the consequence and the remedy.
	w := call(t, s, http.MethodDelete, ProvidersPath+"?name=stub", s.handleProviders)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 for removing the last provider", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	for _, k := range []string{"consequence", "remedy", "confirm"} {
		if body[k] == "" {
			t.Errorf("the refusal has no %s: %v", k, body)
		}
	}
	if !strings.Contains(body["remedy"], owner.RecoveryKeyEnv) {
		t.Errorf("the remedy does not name the recovery key: %v", body)
	}
	if !owner.IsEnrolledIn(s.instance(), "stub") {
		t.Fatal("the provider was removed despite the refusal")
	}

	// Acknowledged: permitted, because on a host the owner controls lockout
	// cannot happen.
	w = call(t, s, http.MethodDelete, ProvidersPath+"?name=stub&acknowledge=recovery", s.handleProviders)
	if w.Code != 200 {
		t.Fatalf("acknowledged removal refused: %d %s", w.Code, w.Body.String())
	}
	if owner.IsEnrolledIn(s.instance(), "stub") {
		t.Error("still enrolled after an acknowledged removal")
	}
}

// ⭐ Removing one of several needs no acknowledgement: the owner keeps a way in.
func TestRemovingOneOfSeveralIsNotGuarded(t *testing.T) {
	s := enrolServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false
	_ = owner.SaveEnrolment(s.instance(), "stub", []byte("x"))
	_ = owner.SaveEnrolment(s.instance(), "stub-two", []byte("x"))

	if w := call(t, s, http.MethodDelete, ProvidersPath+"?name=stub", s.handleProviders); w.Code != 200 {
		t.Errorf("removing one of two was guarded: %d %s", w.Code, w.Body.String())
	}
}

// ⛔ Enrolment is not reachable by a stranger. It is how a locked-out owner
// gets back in, so it accepts an enrolment session OR ownership — and neither
// here means neither.
func TestEnrolmentRefusesAStranger(t *testing.T) {
	s := enrolServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false

	for _, tc := range []struct {
		path string
		h    http.HandlerFunc
	}{
		{EnrolPathPrefix + "stub/begin", s.handleEnrol},
		{ProvidersPath, s.handleProviders},
	} {
		if w := call(t, s, http.MethodPost, tc.path, tc.h); w.Code != http.StatusUnauthorized {
			t.Errorf("%s returned %d to a stranger, want 401", tc.path, w.Code)
		}
	}
}

// ⭐ An owner who is already in can add an authenticator WITHOUT touching the
// recovery key — which is what keeps the recovery key for emergencies rather
// than making it part of routine use.
func TestAnOwnerCanEnrolWithoutTheRecoveryKey(t *testing.T) {
	stub := sharedStub
	*stub = stubEnroller{}

	s := enrolServer(t) // local + loopback: the owner, by construction
	if w := call(t, s, http.MethodPost, EnrolPathPrefix+"stub/begin", s.handleEnrol); w.Code != 200 {
		t.Errorf("an owner was refused enrolment: %d %s", w.Code, w.Body.String())
	}
}

// namedStub is a second enroller, for the several-providers case.
type namedStub struct{ name string }

func (n *namedStub) Name() string                                        { return n.name }
func (n *namedStub) Available(i owner.Instance) bool                     { return i.Store != nil }
func (n *namedStub) Enrolled(i owner.Instance) bool                      { return owner.IsEnrolledIn(i, n.name) }
func (n *namedStub) IsOwner(owner.Instance, *http.Request) (bool, error) { return true, nil }
func (n *namedStub) BeginEnrol(owner.Instance, []byte) (any, []byte, error) {
	return map[string]string{}, nil, nil
}
func (n *namedStub) CompleteEnrol(owner.Instance, []byte, []byte) (any, []byte, error) {
	return map[string]string{}, []byte("x"), nil
}

// ⛔ THE RELYING-PARTY ID COMES FROM CONFIGURATION, NEVER A REQUEST. A browser
// only returns a credential to an origin matching the RP ID it was created
// for, so a credential registered against a forged Host would be usable from
// the attacker's origin — and the failure appears at SIGN-IN, later, for an
// owner who has already discarded their old way in.
func TestPasskeyRefusesWithoutAKnownOrigin(t *testing.T) {
	p := NewPasskeyProvider()
	dir := t.TempDir()
	noOrigin := owner.Instance{Mode: mode.RemoteServerSigned, Store: storage.NewSite(dir)}

	if p.Available(noOrigin) {
		t.Error("passkey reported available with no base URL; a credential would bind to the wrong origin")
	}
	if _, _, err := p.BeginEnrol(noOrigin, nil); err == nil {
		t.Fatal("enrolment began with no known origin")
	}
	withOrigin := noOrigin
	withOrigin.BaseURL = "https://alice.example.test"
	if !p.Available(withOrigin) {
		t.Error("passkey is unavailable with a perfectly good base URL")
	}
}

// ⛔ A passkey cannot be proved from an arbitrary request. It needs a challenge
// the server issued and an assertion signed over it, so answering the
// disjunction directly must always be no.
func TestPasskeyNeverAnswersTheDisjunctionDirectly(t *testing.T) {
	ok, err := NewPasskeyProvider().IsOwner(owner.Instance{BaseURL: "https://alice.example.test"},
		httptest.NewRequest(http.MethodGet, "/api/publish", nil))
	if ok || err != nil {
		t.Errorf("passkey answered a bare request: ok=%v err=%v", ok, err)
	}
}

// ⭐ A half-finished ceremony cannot be completed later from nothing.
func TestPasskeyCompleteNeedsABegin(t *testing.T) {
	p := NewPasskeyProvider()
	dir := t.TempDir()
	inst := owner.Instance{Mode: mode.RemoteServerSigned, Store: storage.NewSite(dir),
		BaseURL: "https://alice.example.test"}
	if _, _, err := p.CompleteEnrol(inst, []byte("{}"), nil); err == nil {
		t.Fatal("completed a registration that never began")
	}
}

// ⭐⭐⭐ THE PROPERTY THE WHOLE EPIC IS FOR: a signed-in session on a remote
// instance opens the gate that was refusing everything.
func TestASignedInSessionOpensTheGate(t *testing.T) {
	s := enrolServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false
	inst := s.instance()

	// Enrol a passkey record directly: the WebAuthn ceremony needs a browser,
	// but what it LEAVES BEHIND is what the gate reads.
	if err := owner.SaveEnrolment(inst, "passkey",
		[]byte(`{"credentials":[{"id":"AQ=="}],"user_id":"dQ=="}`)); err != nil {
		t.Fatal(err)
	}

	// Before signing in: refused.
	if code := gateResponse(t, s, "/api/publish"); code == http.StatusOK {
		t.Fatal("an enrolled-but-not-signed-in request passed the gate")
	}

	token, _, err := owner.OpenSignIn(inst, passkeyName)
	if err != nil {
		t.Fatal(err)
	}

	// After signing in, with the cookie: allowed.
	reached := false
	h := s.ownerGateMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/publish", nil)
	r.AddCookie(&http.Cookie{Name: signInCookiePlain, Value: token})
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !reached {
		t.Fatal("a signed-in request was refused by the gate")
	}

	// ⛔ And signing out closes it again.
	if err := owner.SignOut(inst, token); err != nil {
		t.Fatal(err)
	}
	reached = false
	r2 := httptest.NewRequest(http.MethodGet, "/api/publish", nil)
	r2.AddCookie(&http.Cookie{Name: signInCookiePlain, Value: token})
	h.ServeHTTP(httptest.NewRecorder(), r2)
	if reached {
		t.Error("a signed-out session still passed the gate")
	}
}

// ⛔ The sign-in ceremony must be reachable by someone who is not signed in —
// otherwise there is no way to ever become signed in.
func TestSignInRoutesAreReachableWhileLockedOut(t *testing.T) {
	s := enrolServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false

	for _, path := range []string{SignInBeginPath, SignInCompletePath} {
		if !isPublicAPIRoute(path) {
			t.Errorf("%s is not public; nobody could ever sign in", path)
		}
		if code := gateResponse(t, s, path); code != http.StatusOK {
			t.Errorf("%s returned %d through the gate", path, code)
		}
	}
	// ⚠️ But sign-OUT is not public: it needs a session to end.
	if isPublicAPIRoute(SignOutPath) {
		t.Error("sign-out is public; it has nothing to do for a stranger")
	}
}

// ⭐ Someone with nothing enrolled is told so, and pointed at the door that
// exists for exactly that case.
func TestSignInWithNothingEnrolledPointsAtRecovery(t *testing.T) {
	s := enrolServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false

	w := call(t, s, http.MethodPost, SignInBeginPath, s.handleSignInBegin)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 with nothing enrolled", w.Code)
	}
	if !strings.Contains(w.Body.String(), RecoveryChallengePath) {
		t.Errorf("the refusal does not point at recovery: %s", w.Body.String())
	}
}

// ⛔ Removing something that is not an enrolment, or not set up, is refused
// and changes nothing. Operator trust and loopback used to answer 200
// {"removed": …} while removing nothing.
func TestRemovingWhatCannotBeRemovedIsRefused(t *testing.T) {
	stub := sharedStub
	*stub = stubEnroller{}
	s := enrolServer(t)
	if err := owner.SaveEnrolment(s.instance(), "stub", []byte("x")); err != nil {
		t.Fatal(err)
	}
	s.Mode = mode.RemoteServerSigned
	s.BoundToLoopback = false
	before := owner.Statuses(s.instance())

	for q, want := range map[string]int{
		"?name=operator&acknowledge=recovery": http.StatusConflict,
		"?name=loopback&acknowledge=recovery": http.StatusConflict,
		"?name=no-such-provider":              http.StatusNotFound,
		"?name=stub-two&acknowledge=recovery": http.StatusNotFound, // registered, not set up
	} {
		w := call(t, s, http.MethodDelete, ProvidersPath+q, s.handleProviders)
		if w.Code != want {
			t.Errorf("DELETE %s = %d %s, want %d", q, w.Code, w.Body.String(), want)
		}
		if strings.Contains(w.Body.String(), `"removed"`) {
			t.Errorf("DELETE %s claims a removal: %s", q, w.Body.String())
		}
	}
	after := owner.Statuses(s.instance())
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("a refused removal changed the providers: %v → %v", before, after)
	}
}
