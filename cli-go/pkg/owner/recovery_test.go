package owner

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// A real keypair, because the point of this design is that recovery uses the
// SAME signing primitive as publishing. ⭐ If a user can publish, they can
// recover — and a test with a fake signer would not have shown that.
func keypair(t *testing.T) (priv, pub []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

// remoteInstance is a remote site with a real store and its own process cache.
func remoteInstance(t *testing.T) Instance {
	t.Helper()
	return Instance{Mode: mode.RemoteServerSigned, Store: storage.NewSite(t.TempDir()),
		BaseURL: "https://alice.example.test", SiteID: "/site", Cache: NewCache()}
}

func sign(t *testing.T, priv []byte, message string) string {
	t.Helper()
	sig, err := signing.SignContentNS([]byte(message), priv, RecoveryNamespace)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// ⭐ The happy path, end to end with real crypto.
func TestAValidSignatureOverAnIssuedChallengeVerifies(t *testing.T) {
	priv, pub := keypair(t)
	inst := remoteInstance(t)
	r, err := NewRecovery(string(pub), "alice.example.test")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := r.Issue(inst)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(inst, ch.Nonce, sign(t, priv, ch.Message)); err != nil {
		t.Fatalf("a valid proof was rejected: %v", err)
	}
}

// ⛔ A nonce is spent by the proof that uses it, across processes: a second
// process sharing the store refuses the replay. ⭐ A FAILED attempt consumes
// nothing — it proved nothing, so it may not cost a store operation — and the
// owner can still answer the same challenge.
func TestANonceIsSpentByItsProofAndNotByAFailure(t *testing.T) {
	priv, pub := keypair(t)
	inst := remoteInstance(t)
	r, _ := NewRecovery(string(pub), "alice.example.test")

	ch, _ := r.Issue(inst)
	if err := r.Verify(inst, ch.Nonce, sign(t, priv, ch.Message)); err != nil {
		t.Fatal(err)
	}
	// ⭐ Another process: same store, its own cache and its own Recovery.
	other := inst
	other.Cache = NewCache()
	r2, _ := NewRecovery(string(pub), "alice.example.test")
	if err := r2.Verify(other, ch.Nonce, sign(t, priv, ch.Message)); !errors.Is(err, ErrChallengeUnknown) {
		t.Errorf("a used nonce was accepted again by another process: %v", err)
	}

	wrong, _ := keypair(t)
	ch2, _ := r.Issue(inst)
	if err := r.Verify(inst, ch2.Nonce, sign(t, wrong, ch2.Message)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
	if err := r.Verify(inst, ch2.Nonce, sign(t, priv, ch2.Message)); err != nil {
		t.Errorf("a failed attempt spent the owner's challenge: %v", err)
	}
}

// ⛔⛔ THE DOMAIN IS INSIDE THE SIGNED BYTES. Without it, a signature captured
// from one instance verifies against another holding the same recovery key —
// and owners WILL reuse a key across their own sites, so this is a likely
// accident rather than an exotic attack.
func TestASignatureFromAnotherInstanceDoesNotVerifyHere(t *testing.T) {
	priv, pub := keypair(t)
	stagingInst, prodInst := remoteInstance(t), remoteInstance(t)
	stagingInst.BaseURL = "https://staging.example.test"
	staging, _ := NewRecovery(string(pub), "staging.example.test")
	production, _ := NewRecovery(string(pub), "alice.example.test")

	stagingChallenge, _ := staging.Issue(stagingInst)
	proof := sign(t, priv, stagingChallenge.Message)

	// Replay the staging nonce and signature against production.
	if err := production.Verify(prodInst, stagingChallenge.Nonce, proof); err == nil {
		t.Fatal("a proof made for another instance was accepted; the domain is not bound in")
	}

	// And even with a nonce production itself issued, the staging message
	// does not match.
	prodChallenge, _ := production.Issue(prodInst)
	if err := production.Verify(prodInst, prodChallenge.Nonce, proof); !errors.Is(err, ErrBadSignature) {
		t.Errorf("want ErrBadSignature for a cross-instance proof, got %v", err)
	}
}

// ⭐ The message must be self-describing, so a signature made here can never be
// mistaken for a signature over a post.
func TestTheChallengeMessageIsSelfDescribing(t *testing.T) {
	_, pub := keypair(t)
	r, _ := NewRecovery(string(pub), "alice.example.test")
	ch, _ := r.Issue(remoteInstance(t))

	if !strings.HasPrefix(ch.Message, "pub.polis.recovery.challenge.v1\n") {
		t.Errorf("the message does not announce what it is: %q", ch.Message)
	}
	if !strings.Contains(ch.Message, "alice.example.test") {
		t.Error("the message does not carry the instance domain")
	}
	if !strings.Contains(ch.Message, ch.Nonce) {
		t.Error("the message does not carry the nonce")
	}
	// ⭐ Returned explicitly, so no caller reconstructs it and gets it wrong.
	if ch.Message == "" {
		t.Error("the caller is left to build the message itself")
	}
}

func TestChallengesExpire(t *testing.T) {
	priv, pub := keypair(t)
	inst := remoteInstance(t)
	r, _ := NewRecovery(string(pub), "alice.example.test")
	clock := time.Now()
	r.now = func() time.Time { return clock }

	ch, _ := r.Issue(inst)
	clock = clock.Add(challengeTTL + time.Second)
	if err := r.Verify(inst, ch.Nonce, sign(t, priv, ch.Message)); !errors.Is(err, ErrChallengeUnknown) {
		t.Errorf("an expired challenge was accepted: %v", err)
	}
}

// ⚠️ Unknown, forged and already-used are ONE error on purpose: telling them
// apart tells an attacker which nonces existed, and no honest caller needs it.
func TestTheFailureModesAreIndistinguishable(t *testing.T) {
	priv, pub := keypair(t)
	inst := remoteInstance(t)
	r, _ := NewRecovery(string(pub), "alice.example.test")

	never := r.Verify(inst, "never-issued", "sig")

	ch, _ := r.Issue(inst)
	_ = r.Verify(inst, ch.Nonce, sign(t, priv, ch.Message))
	used := r.Verify(inst, ch.Nonce, sign(t, priv, ch.Message))

	// ⛔ A nonce whose MAC was tampered with.
	ch2, _ := r.Issue(inst)
	forgedNonce := tamper(ch2.Nonce)
	forged := r.Verify(inst, forgedNonce, sign(t, priv, challengeMessage("alice.example.test", forgedNonce)))

	if never.Error() != used.Error() || never.Error() != forged.Error() {
		t.Errorf("unknown, used and forged nonces report differently:\n  %v\n  %v\n  %v", never, used, forged)
	}
}

// ⭐ Issuing writes nothing, so a flood of requests for challenges cannot fill
// anything or spend a store allowance; and the owner's own challenge still works
// afterwards, because there is no pool for the flood to evict it from.
func TestAFloodOfChallengesWritesNothingAndEvictsNothing(t *testing.T) {
	priv, pub := keypair(t)
	inst := remoteInstance(t)
	r, _ := NewRecovery(string(pub), "alice.example.test")
	first, err := r.Issue(inst)
	if err != nil {
		t.Fatal(err)
	}
	counted := &countStore{Store: inst.Store}
	inst.Store = counted
	for i := 0; i < 500; i++ {
		if _, err := r.Issue(inst); err != nil {
			t.Fatal(err)
		}
	}
	if n := counted.total(); n != 0 {
		t.Errorf("500 challenges cost %d store operations; issuing must cost none (%v)", n, counted.calls)
	}
	if err := r.Verify(inst, first.Nonce, sign(t, priv, first.Message)); err != nil {
		t.Errorf("the owner's challenge did not survive the flood: %v", err)
	}
}

// ⛔ A malformed key must fail at CONSTRUCTION — while the operator is
// deploying — not at use, when somebody is already locked out.
func TestAMalformedRecoveryKeyFailsAtConstruction(t *testing.T) {
	for _, bad := range []string{"not-a-key", "ssh-ed25519 ####", "ssh-rsa AAAA"} {
		if _, err := NewRecovery(bad, "alice.example.test"); err == nil {
			t.Errorf("%q was accepted as a recovery key", bad)
		}
	}
}

// ⭐ Empty is not an error: local and custodied need no recovery key, and
// refusing to construct would make every caller special-case them.
func TestNoRecoveryKeyIsNotAConstructionError(t *testing.T) {
	r, err := NewRecovery("", "alice.example.test")
	if err != nil {
		t.Fatalf("an absent recovery key should not fail construction: %v", err)
	}
	if r.Configured() {
		t.Error("Configured() is true with no key")
	}
	if _, err := r.Issue(remoteInstance(t)); !errors.Is(err, ErrNoRecoveryKey) {
		t.Errorf("want ErrNoRecoveryKey from Issue, got %v", err)
	}
	if err := r.Verify(remoteInstance(t), "n", "s"); !errors.Is(err, ErrNoRecoveryKey) {
		t.Errorf("want ErrNoRecoveryKey from Verify, got %v", err)
	}
}

// ── enrolment sessions ──────────────────────────────────────────────────

// ⭐ Two-step ceremonies are the norm (begin, then complete), so validity must
// be checkable without spending the session. ⛔ And spending it on one process
// spends it on every process sharing the store.
func TestASessionSurvivesAValidityCheckAndDiesOnConsume(t *testing.T) {
	inst := remoteInstance(t)
	token, _, err := OpenEnrolment(inst)
	if err != nil {
		t.Fatal(err)
	}
	var sess EnrolmentSession
	for i := 0; i < 3; i++ {
		if sess, err = CheckEnrolment(inst, token); err != nil {
			t.Fatal("checking validity spent the session; a two-step ceremony would break")
		}
	}
	if err := ConsumeEnrolment(inst, sess); err != nil {
		t.Fatal(err)
	}
	other := inst
	other.Cache = NewCache()
	if _, err := CheckEnrolment(other, token); err == nil {
		t.Error("a consumed session is still valid on another process")
	}
	if err := ConsumeEnrolment(other, sess); !errors.Is(err, ErrNoEnrolmentSession) {
		t.Errorf("want ErrNoEnrolmentSession on replay, got %v", err)
	}
}

func TestSessionsExpire(t *testing.T) {
	inst := remoteInstance(t)
	now := time.Now()
	inst.Cache.now = func() time.Time { return now }

	token, _, _ := OpenEnrolment(inst)
	now = now.Add(enrolmentTTL + time.Second)
	if _, err := CheckEnrolment(inst, token); err == nil {
		t.Error("an expired session is still valid")
	}
}

func TestEmptyTokenIsNeverValid(t *testing.T) {
	if _, err := CheckEnrolment(remoteInstance(t), ""); err == nil {
		t.Error("the empty token was accepted; a missing cookie would authorise enrolment")
	}
}

// ⛔ A recovery key with no domain must FAIL THE BOOT. Caught by running the
// server, not by a test: every unit test here passes a domain explicitly, while
// a fresh site has no base URL configured — so the binding that stops
// cross-instance replay silently did not exist.
func TestARecoveryKeyWithoutADomainRefusesToStart(t *testing.T) {
	_, pub := keypair(t)
	_, err := NewRecovery(string(pub), "")
	if err == nil {
		t.Fatal("a recovery key with no domain was accepted; cross-instance replay would work")
	}
	if !strings.Contains(err.Error(), "POLIS_BASE_URL") {
		t.Errorf("the refusal should name the remedy: %v", err)
	}
	// ⭐ But no key and no domain is still fine — local and custodied.
	if _, err := NewRecovery("", ""); err != nil {
		t.Errorf("an instance with neither should construct: %v", err)
	}
}

// ── enrolment storage ───────────────────────────────────────────────────

// stateful is a provider that remembers something, which is what every real
// provider after loopback and operator will be.
type stateful struct{ name string }

func (p stateful) Name() string                                  { return p.name }
func (p stateful) Available(Instance) bool                       { return true }
func (p stateful) Enrolled(i Instance) bool                      { return IsEnrolledIn(i, p.name) }
func (p stateful) IsOwner(Instance, *http.Request) (bool, error) { return true, nil }

func siteInstance(t *testing.T) Instance {
	t.Helper()
	return Instance{Mode: mode.RemoteServerSigned, Store: storage.NewSite(t.TempDir())}
}

// ⛔⛔ THE BUG THIS SHAPE EXISTS TO PREVENT. The registry is process-global and
// polis-hosted runs many tenants in one process, so if enrolment hung off the
// PROVIDER rather than the INSTANCE, enrolling on one site would admit the
// owner to every other tenant.
func TestEnrolmentIsPerSiteNotPerProvider(t *testing.T) {
	alice, bob := siteInstance(t), siteInstance(t)
	p := stateful{name: "passkey"}

	if err := SaveEnrolment(alice, p.Name(), []byte(`{"credential":"alice"}`)); err != nil {
		t.Fatal(err)
	}
	if !p.Enrolled(alice) {
		t.Error("alice's own enrolment is not visible to her")
	}
	if p.Enrolled(bob) {
		t.Fatal("alice's enrolment admitted bob; per-site state is leaking across tenants")
	}

	// And the disjunction agrees, which is what actually gates a request.
	withProviders(t, p)
	if ok, _ := IsOwner(bob, req()); ok {
		t.Error("bob's site accepted a request on the strength of alice's enrolment")
	}
	if ok, err := IsOwner(alice, req()); !ok || err != nil {
		t.Errorf("alice's own site refused her: ok=%v err=%v", ok, err)
	}
}

// ⭐ The blob is opaque — the seam never parses it, so a new provider needs no
// change here. D7's requirement, stated as a test.
func TestTheEnrolmentBlobIsOpaque(t *testing.T) {
	inst := siteInstance(t)
	for _, blob := range [][]byte{
		[]byte(`{"webauthn":"credential"}`),
		[]byte(`$argon2id$v=19$m=65536`),
		{0x00, 0xff, 0x10}, // not text at all
	} {
		if err := SaveEnrolment(inst, "whatever", blob); err != nil {
			t.Fatalf("the store refused an opaque blob: %v", err)
		}
		got, err := LoadEnrolment(inst, "whatever")
		if err != nil || string(got) != string(blob) {
			t.Errorf("blob did not round-trip: %q vs %q (%v)", got, blob, err)
		}
	}
}

// ⛔ The provider name reaches this from a URL path on routes reachable BEFORE
// authentication. A name with a slash or .. would address site content outside
// the prefix.
func TestAProviderNameCannotEscapeItsPrefix(t *testing.T) {
	inst := siteInstance(t)
	for _, bad := range []string{
		"../../.polis/keys/id_ed25519", "a/b", `a\b`, "..", ".hidden", "", "UPPER", "with space", "sem;colon",
	} {
		if err := SaveEnrolment(inst, bad, []byte("x")); err == nil {
			t.Errorf("SaveEnrolment accepted %q", bad)
		}
		if _, err := LoadEnrolment(inst, bad); err == nil {
			t.Errorf("LoadEnrolment accepted %q", bad)
		}
		if err := DeleteEnrolment(inst, bad); err == nil {
			t.Errorf("DeleteEnrolment accepted %q", bad)
		}
	}
}

// ⭐ Removal is idempotent: a double-submitting UI must not show a failure for
// reaching the state the owner asked for.
func TestRemovalIsIdempotent(t *testing.T) {
	inst := siteInstance(t)
	_ = SaveEnrolment(inst, "passkey", []byte("x"))
	for i := 0; i < 3; i++ {
		if err := DeleteEnrolment(inst, "passkey"); err != nil {
			t.Fatalf("removal %d failed: %v", i, err)
		}
	}
	if IsEnrolledIn(inst, "passkey") {
		t.Error("still enrolled after removal")
	}
}

// ⚠️ An instance with no store is not "enrolled in everything" nor a crash.
func TestNoStoreMeansNotEnrolled(t *testing.T) {
	inst := Instance{Mode: mode.Local, Loopback: true}
	if IsEnrolledIn(inst, "passkey") {
		t.Error("a storeless instance reported an enrolment")
	}
	if _, err := LoadEnrolment(inst, "passkey"); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("want ErrNotEnrolled, got %v", err)
	}
	if err := DeleteEnrolment(inst, "passkey"); err != nil {
		t.Errorf("removing from a storeless instance should be a no-op: %v", err)
	}
}

// ── signed-in sessions ──────────────────────────────────────────────────

// ⛔⛔ THE MULTI-TENANT PROPERTY AGAIN, for sessions this time. Hosted runs many
// tenants in one process, so a session minted on one site must never authorise
// a request to another — even one sharing the same store and key.
func TestASignedInSessionIsConfinedToItsSite(t *testing.T) {
	alice := remoteInstance(t)
	bob := alice
	bob.BaseURL = "https://bob.example.test"

	token, _, err := OpenSignIn(alice, "passkey")
	if err != nil {
		t.Fatal(err)
	}
	if !SignedIn(alice, token) {
		t.Error("alice's own session is not recognised on her site")
	}
	if SignedIn(bob, token) {
		t.Fatal("alice's session authorised a request to bob's site")
	}
}

func TestSignOutEndsOneSessionAndEverywhereEndsAll(t *testing.T) {
	inst := remoteInstance(t)

	a, _, _ := OpenSignIn(inst, "passkey")
	b, _, _ := OpenSignIn(inst, "passkey")

	if err := SignOut(inst, a); err != nil {
		t.Fatal(err)
	}
	if SignedIn(inst, a) {
		t.Error("a signed-out session is still live")
	}
	if !SignedIn(inst, b) {
		t.Error("signing out one device ended another")
	}
	// ⭐ Signing out twice is not an error.
	if err := SignOut(inst, a); err != nil {
		t.Errorf("signing out twice: %v", err)
	}

	if err := SignOutAll(inst); err != nil {
		t.Fatal(err)
	}
	if SignedIn(inst, b) {
		t.Error("sign out everywhere left a session behind")
	}
	// ⭐ And a session opened afterwards works.
	c, _, _ := OpenSignIn(inst, "passkey")
	if !SignedIn(inst, c) {
		t.Error("a session opened after signing out everywhere is refused")
	}
}

func TestSignedInSessionsExpire(t *testing.T) {
	inst := remoteInstance(t)
	now := time.Now()
	inst.Cache.now = func() time.Time { return now }

	token, _, _ := OpenSignIn(inst, "passkey")
	now = now.Add(signInTTL + time.Minute)
	if SignedIn(inst, token) {
		t.Error("an expired session is still accepted")
	}
}

// ⚠️ An empty token must never pass: a request with no cookie would otherwise
// be treated as signed in.
func TestAnEmptyTokenIsNeverSignedIn(t *testing.T) {
	if SignedIn(remoteInstance(t), "") {
		t.Fatal("the empty token was accepted as a session")
	}
}

// ⭐ Signing out more sessions than the revocation list holds ends them all,
// rather than growing the file without bound.
func TestManySignOutsAreBoundedBySigningOutEverywhere(t *testing.T) {
	inst := remoteInstance(t)
	keep, _, _ := OpenSignIn(inst, "passkey")
	for i := 0; i < maxRevoked+1; i++ {
		tok, _, _ := OpenSignIn(inst, "passkey")
		if err := SignOut(inst, tok); err != nil {
			t.Fatal(err)
		}
	}
	g, err := loadGenerations(inst, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Revoked) > maxRevoked {
		t.Errorf("%d revocations remembered, cap is %d", len(g.Revoked), maxRevoked)
	}
	if SignedIn(inst, keep) {
		t.Error("overflowing the revocation list did not end the remaining sessions")
	}
}

// ⭐ A signature copied out of a chat or an indented terminal block arrives
// with spaces around its lines, blank lines, or Windows line endings. None of
// that changes the signature, so each still verifies.
func TestAPastedSignatureVerifiesDespiteItsWhitespace(t *testing.T) {
	priv, pub := keypair(t)
	inst := remoteInstance(t)
	r, err := NewRecovery(string(pub), "alice.example.test")
	if err != nil {
		t.Fatal(err)
	}
	mangle := map[string]func(string) string{
		"indented": func(s string) string {
			return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ") + "\n"
		},
		"trailing spaces": func(s string) string { return strings.ReplaceAll(s, "\n", "   \n") },
		"blank lines":     func(s string) string { return "\n\n" + strings.ReplaceAll(s, "\n", "\n\n") },
		"CRLF":            func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") },
	}
	for name, f := range mangle {
		t.Run(name, func(t *testing.T) {
			ch, err := r.Issue(inst)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Verify(inst, ch.Nonce, f(sign(t, priv, ch.Message))); err != nil {
				t.Fatalf("a valid proof pasted %s was rejected: %v", name, err)
			}
		})
	}
}
