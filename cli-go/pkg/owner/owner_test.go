package owner

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// stub is the fake provider D7 requires.
//
// ⭐⭐⭐ IT IS THE TEST OF THE SEAM'S AGNOSTICISM, not a convenience. Two
// providers are deferred on non-design grounds — the password one on scope, the
// Vercel one on three endpoints somebody else controls — and each must be
// addable later WITHOUT a redesign. ⛔ If registering, listing, selecting or
// removing a provider ever needs code outside that provider's own file, this
// stub stops being expressible and the tests below fail. That is the day the
// shortcut is cheap to undo, rather than the day a real provider needs it.
type stub struct {
	name      string
	available bool
	enrolled  bool
	owner     bool
	err       error
	asked     *int
}

func (s stub) Name() string            { return s.name }
func (s stub) Available(Instance) bool { return s.available }
func (s stub) Enrolled(Instance) bool  { return s.enrolled }
func (s stub) IsOwner(Instance, *http.Request) (bool, error) {
	if s.asked != nil {
		*s.asked++
	}
	return s.owner, s.err
}

func withProviders(t *testing.T, ps ...Provider) {
	t.Helper()
	reset()
	for _, p := range ps {
		Register(p)
	}
	t.Cleanup(func() {
		reset()
		Register(LoopbackProvider{})
	})
}

func req() *http.Request { return httptest.NewRequest(http.MethodGet, "/api/publish", nil) }

// ⭐ The headline property: any ENROLLED provider may answer yes.
func TestAnyEnrolledProviderSatisfies(t *testing.T) {
	withProviders(t,
		stub{name: "a", available: true, enrolled: true, owner: false},
		stub{name: "b", available: true, enrolled: true, owner: true},
	)
	ok, err := IsOwner(Instance{Mode: mode.RemoteServerSigned}, req())
	if err != nil || !ok {
		t.Fatalf("a disjunction with one yes should pass: ok=%v err=%v", ok, err)
	}
}

// ⛔ Available and Enrolled are separate on purpose. A provider the owner set
// up on a laptop must not answer on a server where its premise is gone.
func TestAnUnavailableProviderNeverAnswers(t *testing.T) {
	asked := 0
	withProviders(t, stub{name: "a", available: false, enrolled: true, owner: true, asked: &asked})

	ok, err := IsOwner(Instance{Mode: mode.RemoteServerSigned}, req())
	if ok {
		t.Error("an unavailable provider answered")
	}
	if !errors.Is(err, ErrNoProvider) {
		t.Errorf("want ErrNoProvider, got %v", err)
	}
	if asked != 0 {
		t.Errorf("an unavailable provider was consulted %d time(s)", asked)
	}
}

func TestAnUnenrolledProviderNeverAnswers(t *testing.T) {
	asked := 0
	withProviders(t, stub{name: "a", available: true, enrolled: false, owner: true, asked: &asked})
	if ok, _ := IsOwner(Instance{Mode: mode.RemoteServerSigned}, req()); ok {
		t.Error("an unenrolled provider answered")
	}
	if asked != 0 {
		t.Errorf("an unenrolled provider was consulted %d time(s)", asked)
	}
}

// ⛔⛔ THE RULE MOST LIKELY TO BE BROKEN BY A WELL-MEANING CHANGE: an error is
// never a yes, and one broken provider must neither admit anyone nor lock the
// owner out of a working one.
func TestAnErrorIsNeverAYesAndNeverBlocksAnother(t *testing.T) {
	withProviders(t,
		stub{name: "broken", available: true, enrolled: true, err: errors.New("boom")},
		stub{name: "works", available: true, enrolled: true, owner: true},
	)
	ok, err := IsOwner(Instance{Mode: mode.RemoteServerSigned}, req())
	if !ok {
		t.Error("a broken provider blocked a working one")
	}
	if err != nil {
		t.Errorf("a satisfied disjunction should not report the failure as an error: %v", err)
	}

	// Alone, a failing provider denies — and says what went wrong.
	withProviders(t, stub{name: "broken", available: true, enrolled: true, err: errors.New("boom")})
	ok, err = IsOwner(Instance{Mode: mode.RemoteServerSigned}, req())
	if ok {
		t.Fatal("a provider that errored was treated as a yes")
	}
	if err == nil {
		t.Error("the failure was swallowed; a caller cannot distinguish a denial from a broken provider")
	}
}

// ⚠️ "Nothing is configured, so allow everything until setup" is a fully open
// instance during exactly the window an attacker would most like one.
func TestNoProvidersMeansNoNotYes(t *testing.T) {
	withProviders(t)
	ok, err := IsOwner(Instance{Mode: mode.RemoteServerSigned}, req())
	if ok {
		t.Fatal("an instance with no providers admitted a request")
	}
	if !errors.Is(err, ErrNoProvider) {
		t.Errorf("want ErrNoProvider so the caller can tell 'enrol something' from 'wrong credentials', got %v", err)
	}
}

// ⭐⭐⭐ THE NO-OP PROPERTY: a laptop keeps working with no enrolment.
func TestLocalOnLoopbackNeedsNoEnrolment(t *testing.T) {
	reset()
	Register(LoopbackProvider{})
	t.Cleanup(func() { reset(); Register(LoopbackProvider{}) })

	ok, err := IsOwner(Instance{Mode: mode.Local, Loopback: true}, req())
	if err != nil || !ok {
		t.Fatalf("polis serve on a laptop must need no enrolment: ok=%v err=%v", ok, err)
	}
	// ⭐ And the zero value behaves as local, so a caller that has not wired a
	// mode yet is not silently locked out mid-migration.
	if ok, err := IsOwner(Instance{Loopback: true}, req()); err != nil || !ok {
		t.Errorf("an unset mode on loopback should behave as local: ok=%v err=%v", ok, err)
	}
}

// ⛔ The security property of remote: loopback does not apply, so nothing is
// enrolled until the owner says so.
func TestLoopbackDoesNotApplyOffTheLaptop(t *testing.T) {
	reset()
	Register(LoopbackProvider{})
	t.Cleanup(func() { reset(); Register(LoopbackProvider{}) })

	for _, inst := range []Instance{
		{Mode: mode.RemoteServerSigned, Loopback: false},
		{Mode: mode.RemoteClientSigned, Loopback: false},
		{Mode: mode.Custodied, Loopback: false},
		// ⚠️ The subtle one: a remote instance someone port-forwards to while
		// debugging IS on loopback, and must still not be trusted.
		{Mode: mode.RemoteServerSigned, Loopback: true},
		// And a custodied deployment reached through a local proxy.
		{Mode: mode.Custodied, Loopback: true},
	} {
		ok, err := IsOwner(inst, req())
		if ok {
			t.Errorf("loopback trust applied in %+v, where it must not", inst)
		}
		if !errors.Is(err, ErrNoProvider) {
			t.Errorf("%+v: want ErrNoProvider, got %v", inst, err)
		}
	}
}

// ⭐ An owner moving a site to a server must SEE that the provider they relied
// on stopped applying, rather than it vanishing silently.
func TestStatusesListUnavailableProvidersToo(t *testing.T) {
	reset()
	Register(LoopbackProvider{})
	t.Cleanup(func() { reset(); Register(LoopbackProvider{}) })

	st := Statuses(Instance{Mode: mode.RemoteServerSigned})
	if len(st) != 1 || st[0].Name != "loopback" {
		t.Fatalf("expected loopback to be listed, got %+v", st)
	}
	if st[0].Available || st[0].Enrolled {
		t.Errorf("loopback should be listed as unavailable off the laptop, got %+v", st[0])
	}
}

// ⚠️ Enrolment is only meaningful where the provider applies, or the list tells
// the owner they have something protecting them that cannot run.
func TestEnrolledIsNeverTrueWhileUnavailable(t *testing.T) {
	withProviders(t, stub{name: "a", available: false, enrolled: true})
	st := Statuses(Instance{Mode: mode.RemoteServerSigned})
	if st[0].Enrolled {
		t.Error("a provider reported enrolled while unavailable; the list would overstate protection")
	}
}

// ⛔ Two providers under one name makes enrolment state ambiguous and silently
// drops one.
func TestDuplicateRegistrationPanics(t *testing.T) {
	reset()
	t.Cleanup(func() { reset(); Register(LoopbackProvider{}) })
	Register(stub{name: "dup"})
	defer func() {
		if recover() == nil {
			t.Error("registering two providers under one name should panic")
		}
	}()
	Register(stub{name: "dup"})
}

// ⭐ Stable order, so the list the owner reviews does not jump around between
// page loads thanks to Go's randomised map iteration.
func TestRegisteredOrderIsStable(t *testing.T) {
	withProviders(t, stub{name: "zeta"}, stub{name: "alpha"}, stub{name: "mid"})
	got := Registered()
	want := []string{"alpha", "mid", "zeta"}
	for i, p := range got {
		if p.Name() != want[i] {
			t.Fatalf("order = %v, want %v", []string{got[0].Name(), got[1].Name(), got[2].Name()}, want)
		}
	}
}

// ⭐ D7: a new provider needs no code outside its own file. If this stops
// compiling or passing, the seam has grown a provider-specific pathway.
func TestANewProviderNeedsNoCodeOutsideItself(t *testing.T) {
	withProviders(t, LoopbackProvider{}, stub{name: "newcomer", available: true, enrolled: true, owner: true})

	// It is registered, listed, selectable and satisfies the disjunction,
	// purely by implementing the interface.
	var found bool
	for _, s := range Statuses(Instance{Mode: mode.RemoteServerSigned}) {
		if s.Name == "newcomer" && s.Available && s.Enrolled {
			found = true
		}
	}
	if !found {
		t.Fatal("a newly registered provider did not appear as available and enrolled")
	}
	if ok, err := IsOwner(Instance{Mode: mode.RemoteServerSigned}, req()); !ok || err != nil {
		t.Errorf("a newly registered provider could not satisfy the seam: ok=%v err=%v", ok, err)
	}
}

// kindedStub is a stub the owner sets up, carrying a ceremony.
type kindedStub struct{ stub }

func (kindedStub) BeginEnrol(Instance, []byte) (any, []byte, error) { return nil, nil, nil }
func (kindedStub) CompleteEnrol(Instance, []byte, []byte) (any, []byte, error) {
	return nil, nil, nil
}
func (kindedStub) Ceremony() string { return "stub-kind" }

// The stub must keep satisfying the seam, or the test above passes vacuously
// false for Enrollable and fails for the wrong reason.
var _ Enroller = kindedStub{}

// ⭐ A client renders the list from these two fields and never from a name, so
// both must come from the provider itself: Enrollable from being an Enroller,
// Kind from being Ceremonial.
func TestStatusesReportEnrollableAndCeremonyKind(t *testing.T) {
	withProviders(t,
		stub{name: "ambient", available: true, enrolled: true},
		kindedStub{stub{name: "kinded", available: true}},
	)
	st := Statuses(Instance{Mode: mode.RemoteServerSigned})
	if len(st) != 2 {
		t.Fatalf("expected two providers, got %+v", st)
	}
	ambient, kinded := st[0], st[1]
	if ambient.Enrollable || ambient.Kind != "" {
		t.Errorf("a provider with nothing to set up should be neither enrollable nor kinded, got %+v", ambient)
	}
	if !kinded.Enrollable || kinded.Kind != "stub-kind" {
		t.Errorf("an Enroller with a ceremony should report both, got %+v", kinded)
	}
}
