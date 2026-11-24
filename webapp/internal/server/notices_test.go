package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// fakeNotices records what the server tells its host.
type fakeNotices struct {
	connected []AgentConnectedNotice
	passkeys  []PasskeyNotice
}

func (f *fakeNotices) AgentConnected(n AgentConnectedNotice) { f.connected = append(f.connected, n) }
func (f *fakeNotices) PasskeyChanged(n PasskeyNotice)        { f.passkeys = append(f.passkeys, n) }

// ⭐ A token made for an agent is a new connection, and the host hears of it;
// the owner's own full token is not one.
func TestMintingAnAgentsTokenTellsTheHost(t *testing.T) {
	s := newConfiguredServer(t)
	f := &fakeNotices{}
	s.Notices = f
	mintVia(t, s, "Claude", "read", "publish")
	if len(f.connected) != 1 {
		t.Fatalf("connected = %+v", f.connected)
	}
	n := f.connected[0]
	if n.Name != "Claude" || n.How != HowToken || strings.Join(n.Scopes, " ") != "read publish" || n.When.IsZero() {
		t.Errorf("notice = %+v", n)
	}
	mintVia(t, s, "mine", "full")
	if len(f.connected) != 1 {
		t.Errorf("the owner's full token was told as a connection: %+v", f.connected)
	}
}

// Self-hosted: no Notices, and minting works as before.
func TestMintingWithNoNoticesSendsNothing(t *testing.T) {
	s := newConfiguredServer(t)
	mintVia(t, s, "Claude", "read")
}

// ⭐ C: a passkey enrolled or removed is told to the host, with the count left.
func TestPasskeyChangesTellTheHost(t *testing.T) {
	dir := t.TempDir()
	s := remoteServer(t, dir, storage.NewSite(dir))
	f := &fakeNotices{}
	s.Notices = f
	va := newVirtualAuthenticator(t, testOrigin, "alice.example.test")
	enrolment, _, err := owner.OpenEnrolment(s.instance())
	if err != nil {
		t.Fatal(err)
	}
	ec := &http.Cookie{Name: enrolmentCookiePlain, Value: enrolment}
	b := postWith(t, s, EnrolPathPrefix+"passkey/begin", nil, s.handleEnrol, ec)
	c := postWith(t, s, EnrolPathPrefix+"passkey/complete", va.register(b.Body.Bytes()), s.handleEnrol, append(cookiesFrom(b), ec)...)
	if c.Code != http.StatusOK {
		t.Fatalf("enrol complete: %d %s", c.Code, c.Body.String())
	}
	if len(f.passkeys) != 1 || !f.passkeys[0].Added || f.passkeys[0].Count != 1 {
		t.Fatalf("after enrolling: %+v", f.passkeys)
	}

	// Removing one of two.
	s2, _ := passkeyServer(t, "AQ==", "Ag==")
	f2 := &fakeNotices{}
	s2.Notices = f2
	tok, _, err := owner.OpenSignIn(s2.instance(), passkeyName)
	if err != nil {
		t.Fatal(err)
	}
	w := call(t, s2, http.MethodDelete, ProvidersPath+"?name="+passkeyName+"&credential=Ag", s2.handleProviders,
		&http.Cookie{Name: signInCookiePlain, Value: tok})
	if w.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	if len(f2.passkeys) != 1 || f2.passkeys[0].Added || f2.passkeys[0].Count != 1 {
		t.Fatalf("after removing: %+v", f2.passkeys)
	}
}
