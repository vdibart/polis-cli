package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The passkey provider.
//
// ⭐⭐⭐ IT LIVES IN THE WEBAPP, NOT IN cli-go, AND THAT IS THE DESIGN WORKING
// RATHER THAN AN EXCEPTION TO IT. Registration is compile-time and providers
// are stateless, so a provider can live in whichever module needs it and
// register itself into the seam. The consequence is concrete: the `polis` CLI
// does not grow a WebAuthn dependency it would never use, and the seam in
// cli-go stays free of one too.
//
// ⚠️ If a future provider needs to be reachable from the CLI, move THAT one —
// do not move the seam.
//
// ⛔ THE RELYING-PARTY ID IS THE SECURITY BOUNDARY AND IT IS NOT COSMETIC. A
// browser will only return a credential to an origin matching the RP ID it was
// created for. Getting it wrong does not fail loudly at enrolment; it fails at
// sign-in, later, for an owner who has already thrown away whatever they used
// before. So it is derived from the instance's own base URL and nothing else —
// never from a request header, which an attacker controls.

const passkeyName = "passkey"

// passkeyRecord is what gets stored. ⭐ The seam never parses it: to the store
// it is an opaque blob keyed by provider name.
type passkeyRecord struct {
	// Credentials the owner has registered. ⭐ Plural from the start, so a
	// second authenticator is not a schema change.
	Credentials []webauthn.Credential `json:"credentials"`
	UserID      []byte                `json:"user_id"`
	Enrolled    time.Time             `json:"enrolled"`
}

// WebAuthnUser adapts the single owner to the library's user model.
//
// ⚠️ THERE IS EXACTLY ONE USER AND NO ACCOUNT. polis has no usernames and no
// directory; a site has an owner. The display values are the site's own domain,
// because that is what a browser shows the person when it asks them to confirm.
type webAuthnUser struct {
	id          []byte
	name        string
	credentials []webauthn.Credential
}

func (u *webAuthnUser) WebAuthnID() []byte                         { return u.id }
func (u *webAuthnUser) WebAuthnName() string                       { return u.name }
func (u *webAuthnUser) WebAuthnDisplayName() string                { return u.name }
func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// PasskeyProvider recognises the owner by a WebAuthn credential.
//
// ⛔⛔ IT HAS NO FIELDS, AND A TEST KEEPS IT THAT WAY. It used to hold the
// in-flight ceremonies in two maps. The registry is process-global, so those
// maps were process state: on a serverless platform the begin and the
// completion land on different processes and sign-in failed at random. A
// ceremony's state now leaves with the client in a sealed cookie
// (owner.BeginCeremony) and comes back with the completion.
type PasskeyProvider struct{}

// NewPasskeyProvider builds the provider.
func NewPasskeyProvider() *PasskeyProvider { return &PasskeyProvider{} }

// Name implements owner.Provider.
func (*PasskeyProvider) Name() string { return passkeyName }

// Available reports whether a passkey can work here.
//
// ⛔ IT NEEDS A STORE AND A KNOWN ORIGIN. Without a base URL there is no RP ID,
// and a credential created against the wrong one is a credential that will not
// work when it matters.
// ⛔ NOT WHERE THERE IS AN OPERATOR. A custodied tenant signs in through the
// operator's accounts, and the plan keeps custodied untouched by owner auth;
// without this, passkey enrolment was a live write path on every polis.pub
// tenant, and every gated request read the passkey store.
func (*PasskeyProvider) Available(inst owner.Instance) bool {
	return inst.Store != nil && inst.BaseURL != "" && !capability.For(inst.Mode).HasOperator
}

// unavailable says WHY passkeys are off here: the remedies differ, and a
// custodied tenant told "no base URL configured" goes looking for a setting
// that is not the problem.
func unavailable(inst owner.Instance) error {
	switch {
	case capability.For(inst.Mode).HasOperator:
		return errors.New("passkey: this instance has an operator, which signs its owner in")
	case inst.BaseURL == "":
		return errors.New("passkey: this instance has no base URL configured, so a credential " +
			"would be registered against the wrong origin and fail at sign-in")
	default:
		return errors.New("passkey: this instance has no store for credentials")
	}
}

// Enrolled reports whether the owner has registered a credential.
func (p *PasskeyProvider) Enrolled(inst owner.Instance) bool {
	if !p.Available(inst) {
		return false
	}
	rec, err := p.load(inst)
	return err == nil && len(rec.Credentials) > 0
}

// IsOwner reports whether the request carries a live signed-in session.
//
// ⛔ THE ASSERTION IS NOT CHECKED HERE, AND THAT IS NOT A SHORTCUT. A passkey
// cannot be proved from an arbitrary request: it needs a challenge the server
// issued and an assertion signed over it, which is a two-step ceremony on the
// sign-in routes. What survives that ceremony is a session, and this is where
// the session is read. ⚠️ Any future change that tries to verify a credential
// from headers alone is reintroducing the forgery the ceremony prevents.
func (*PasskeyProvider) IsOwner(inst owner.Instance, r *http.Request) (bool, error) {
	return owner.SignedIn(inst, signInToken(r)), nil
}

// BeginSignIn issues an assertion challenge, and the ceremony state the
// completion will need.
//
// ⚠️ It is reachable by anyone, because somebody who is not signed in is
// exactly who needs it. ⭐ That is safe: the challenge is useless without the
// credential, and a caller who is not enrolled gets a refusal that says so
// rather than a usable ceremony. ⭐ It writes nothing: the state goes back to
// the caller to be sealed into a cookie, and the credential list is the
// process's cached copy of the enrolment record.
func (p *PasskeyProvider) BeginSignIn(inst owner.Instance) (any, []byte, error) {
	if !p.Enrolled(inst) {
		return nil, nil, errors.New("passkey: no credential is enrolled on this instance")
	}
	w, err := p.rp(inst)
	if err != nil {
		return nil, nil, err
	}
	// ⛔ USER VERIFICATION IS REQUIRED: a security key alone, unlocked and with
	// no PIN or biometric, is not enough to become the owner.
	options, session, err := w.BeginLogin(p.user(inst), webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: %w", err)
	}
	// ⭐ Each ceremony carries its own state, so a stranger's begin cannot
	// replace the owner's: there is no shared slot to replace.
	state, err := json.Marshal(session)
	if err != nil {
		return nil, nil, err
	}
	return options, state, nil
}

// CompleteSignIn verifies an assertion against the ceremony it answers and, on
// success, spends the ceremony and opens a session.
//
// ⛔ VERIFY, THEN SPEND, THEN WRITE. A junk completion fails verification
// before any store operation and consumes nothing. Only a verified assertion
// writes the single-use marker (a replay on another process loses there), then
// re-reads the record FRESH — a credential removed on another process seconds
// ago must not open a session — and persists the advanced signature counter.
//
// ⛔ THE STORED CREDENTIAL IS UPDATED AFTERWARDS, and skipping that is a real
// weakness rather than an optimisation: the signature counter is how a cloned
// authenticator is detected, and a counter that never advances can never go
// backwards suspiciously.
func (p *PasskeyProvider) CompleteSignIn(inst owner.Instance, body []byte, ceremony owner.Ceremony) (any, error) {
	w, err := p.rp(inst)
	if err != nil {
		return nil, err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(ceremony.State, &session); err != nil || session.Challenge == "" {
		return nil, errors.New("passkey: no sign-in is in progress; begin one first")
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("passkey: the browser's response could not be read: %w", err)
	}
	user := p.user(inst)
	credential, err := w.ValidateLogin(user, session, parsed)
	if err != nil {
		// ⚠️ One message for every failure. Distinguishing "unknown credential"
		// from "bad signature" tells a caller which credentials exist.
		return nil, errors.New("passkey: sign-in failed verification")
	}
	// ⛔ A clone warning means the authenticator's counter went backwards: the
	// credential may have been copied. Refuse rather than record and continue.
	if credential.Authenticator.CloneWarning {
		return nil, errors.New("passkey: the authenticator may have been cloned; sign-in refused")
	}
	// ⛔ One assertion per ceremony, across every process.
	if err := owner.Consume(inst, ceremony.ID, ceremony.Expires); err != nil {
		return nil, errors.New("passkey: this sign-in was already completed; begin again")
	}

	rec, err := p.loadFresh(inst)
	if err != nil {
		return nil, errors.New("passkey: sign-in failed verification")
	}
	found := false
	for i := range rec.Credentials {
		if bytes.Equal(rec.Credentials[i].ID, credential.ID) {
			rec.Credentials[i] = *credential
			found = true
		}
	}
	if !found {
		// ⛔ Removed since this process last read the record.
		return nil, errors.New("passkey: sign-in failed verification")
	}
	if blob, err := json.Marshal(rec); err == nil {
		_ = owner.SaveEnrolment(inst, passkeyName, blob)
	}

	token, expires, err := owner.OpenSignIn(inst, passkeyName)
	if err != nil {
		return nil, err
	}
	return signInResult{Token: token, Expires: expires}, nil
}

// signInResult carries the session out to the handler, which sets the cookie.
//
// ⚠️ The token never reaches the response BODY — see the handler. It travels in
// a cookie the browser handles, so a confused owner cannot paste it anywhere.
type signInResult struct {
	Token   string
	Expires time.Time
}

// rp derives the relying-party identity from the instance.
//
// ⛔ FROM THE CONFIGURED BASE URL, NEVER FROM THE REQUEST. A Host header is
// attacker-controlled; a credential registered against a forged RP ID would be
// usable from the attacker's origin.
func (p *PasskeyProvider) rp(inst owner.Instance) (*webauthn.WebAuthn, error) {
	raw := inst.BaseURL
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("passkey: cannot derive an origin from %q", inst.BaseURL)
	}
	return webauthn.New(&webauthn.Config{
		RPDisplayName: u.Hostname(),
		RPID:          u.Hostname(),
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
	})
}

func (p *PasskeyProvider) load(inst owner.Instance) (*passkeyRecord, error) {
	blob, err := owner.LoadEnrolment(inst, passkeyName)
	if err != nil {
		return nil, err
	}
	return decodePasskeyRecord(blob)
}

// loadFresh reads the record from the store, past the process cache.
func (p *PasskeyProvider) loadFresh(inst owner.Instance) (*passkeyRecord, error) {
	blob, err := owner.LoadEnrolmentFresh(inst, passkeyName)
	if err != nil {
		return nil, err
	}
	return decodePasskeyRecord(blob)
}

func decodePasskeyRecord(blob []byte) (*passkeyRecord, error) {
	var rec passkeyRecord
	if err := json.Unmarshal(blob, &rec); err != nil {
		return nil, fmt.Errorf("passkey: stored enrolment is unreadable: %w", err)
	}
	return &rec, nil
}

func (p *PasskeyProvider) user(inst owner.Instance) *webAuthnUser {
	host := inst.BaseURL
	if u, err := url.Parse(host); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	rec, err := p.load(inst)
	if err != nil {
		// ⭐ A fresh owner: a stable id derived from the site, so re-enrolling
		// after removal does not look like a different person to the browser.
		return &webAuthnUser{id: []byte("polis:" + host), name: host}
	}
	id := rec.UserID
	if len(id) == 0 {
		id = []byte("polis:" + host)
	}
	return &webAuthnUser{id: id, name: host, credentials: rec.Credentials}
}

// BeginEnrol starts registration, returning the options for the browser and
// the ceremony state for the completion (sealed into a cookie by the seam).
func (p *PasskeyProvider) BeginEnrol(inst owner.Instance, _ []byte) (any, []byte, error) {
	if !p.Available(inst) {
		return nil, nil, unavailable(inst)
	}
	w, err := p.rp(inst)
	if err != nil {
		return nil, nil, err
	}
	user := p.user(inst)

	// ⭐ Exclude what is already registered, so a second attempt with the same
	// authenticator tells the owner plainly instead of silently duplicating.
	var exclude []protocol.CredentialDescriptor
	for _, c := range user.credentials {
		exclude = append(exclude, c.Descriptor())
	}

	options, session, err := w.BeginRegistration(user,
		webauthn.WithExclusions(exclude),
		// ⚠️ Resident keys are NOT required. Requiring them would exclude
		// authenticators that work perfectly well, and A5 chose passkey partly
		// because it asks nothing unusual of the owner's hardware.
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: %w", err)
	}
	state, err := json.Marshal(session)
	if err != nil {
		return nil, nil, err
	}
	return options, state, nil
}

// CompleteEnrol verifies a registration against its ceremony and returns the
// record to keep. ⛔ It does not save it: the seam spends the ceremony first.
func (p *PasskeyProvider) CompleteEnrol(inst owner.Instance, body, state []byte) (any, []byte, error) {
	if !p.Available(inst) {
		return nil, nil, unavailable(inst)
	}
	w, err := p.rp(inst)
	if err != nil {
		return nil, nil, err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(state, &session); err != nil || session.Challenge == "" {
		return nil, nil, errors.New("passkey: no registration is in progress; begin one first")
	}

	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: the browser's response could not be read: %w", err)
	}
	credential, err := w.CreateCredential(p.user(inst), session, parsed)
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: registration failed verification: %w", err)
	}

	// ⭐ Built on a FRESH read: another process may have enrolled a second
	// authenticator a few seconds ago, and appending to a stale copy would
	// drop it.
	rec, err := p.loadFresh(inst)
	if err != nil {
		rec = &passkeyRecord{UserID: p.user(inst).WebAuthnID()}
	}
	rec.Credentials = append(rec.Credentials, *credential)
	rec.Enrolled = time.Now().UTC()

	blob, err := json.Marshal(rec)
	if err != nil {
		return nil, nil, err
	}
	return map[string]interface{}{
		"enrolled":    passkeyName,
		"credentials": len(rec.Credentials),
	}, blob, nil
}

// ownerStore addresses this site's files for the seam.
func (s *Server) ownerStore() storage.Store {
	if !s.Site().Valid() {
		return nil
	}
	s.ownerStoreOnce.Do(func() { s.ownerStoreV = s.Site().Store() })
	return s.ownerStoreV
}

func init() {
	// ⭐ Compile-time registration from the module that owns the dependency.
	owner.Register(NewPasskeyProvider())
}

// RemoveCredential removes one enrolled authenticator, named by its credential
// ID (base64url), and ends every session.
//
// ⭐ Before this, the only removal dropped the whole passkey record: an owner
// who lost one key had to remove all of them. ⛔ Removal ends every passkey
// session — sessions are not tied to the credential that opened them, so
// ending all of them is the only way to be sure the removed one's are gone —
// and removing the last credential removes the provider.
func (p *PasskeyProvider) RemoveCredential(inst owner.Instance, id string) error {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(id, "="))
	if err != nil {
		return errors.New("passkey: the credential ID is not base64url")
	}
	rec, err := p.loadFresh(inst)
	if err != nil {
		return err
	}
	kept := rec.Credentials[:0]
	found := false
	for _, c := range rec.Credentials {
		if bytes.Equal(c.ID, raw) {
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		return errors.New("passkey: no such credential is enrolled")
	}
	if len(kept) == 0 {
		return owner.DeleteEnrolment(inst, passkeyName) // also signs every session out
	}
	rec.Credentials = kept
	blob, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := owner.SaveEnrolment(inst, passkeyName, blob); err != nil {
		return err
	}
	// ⭐ A generation bump: every process stops honouring the sessions.
	return owner.EndProviderSessions(inst, passkeyName)
}
