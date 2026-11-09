package owner

import (
	"errors"
	"time"
)

// The enrolment session: the narrowest thing that can follow a proof.
//
// ⛔⛔ IT IS NOT A LOGIN, AND THE DIFFERENCE IS THE POINT. Proving you hold the
// recovery key buys exactly ONE capability: enrolling a provider. It cannot
// publish, read direct messages, change the licence or export the site. ⭐ So a
// proof that leaks — a signature pasted into the wrong window, a challenge
// captured from a screen share — cannot be spent on anything except adding a
// way to log in, which the owner then sees in their provider list.
//
// ⭐ A SEALED TOKEN, sealed under its own purpose, so it can never be presented
// as a signed-in session and nothing but the enrolment routes accept it. Any
// process holding the site's key recognises it, so the recovery that opened it
// and the enrolment that spends it may be served by different processes.
//
// ⛔ AND IT IS SINGLE-USE ACROSS PROCESSES. Completing an enrolment writes a
// marker named by the session's id (Consume), and a second completion with the
// same cookie — on any process — finds it and is refused. ⭐ Checking validity
// does not spend it, because enrolment is two requests (begin, then complete).

// enrolmentTTL bounds the session. ⭐ Long enough to complete a WebAuthn
// ceremony including a fumbled first attempt; short enough that an abandoned
// tab is not a standing invitation.
const enrolmentTTL = 15 * time.Minute

// ErrNoEnrolmentSession means the token is unknown, expired or already spent.
//
// ⚠️ One error for all three, deliberately: distinguishing them tells a caller
// which tokens existed, and the remedy is identical.
var ErrNoEnrolmentSession = errors.New("owner: enrolment session is unknown, expired or already used")

type enrolmentToken struct {
	Site string `json:"site"`
	ID   string `json:"id"`
	Exp  int64  `json:"exp"`
}

// EnrolmentSession is a live enrolment session, as recovered from its token.
type EnrolmentSession struct {
	ID      string
	Expires time.Time
}

// OpenEnrolment mints an enrolment session. ⭐ Call this ONLY after a proof
// has verified.
func OpenEnrolment(inst Instance) (string, time.Time, error) {
	id, err := randomID()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := clock(inst).Add(enrolmentTTL)
	tok, err := seal(inst, purposeEnrolment, enrolmentToken{Site: siteKey(inst), ID: id, Exp: exp.Unix()})
	return tok, exp, err
}

// CheckEnrolment reports whether a token may still enrol, WITHOUT consuming it.
//
// ⭐ The MAC and expiry are checked first, so a forged or stale cookie costs
// no store operation; only a genuine one is checked against the used markers.
func CheckEnrolment(inst Instance, token string) (EnrolmentSession, error) {
	var t enrolmentToken
	if token == "" {
		return EnrolmentSession{}, ErrNoEnrolmentSession
	}
	if err := unseal(inst, purposeEnrolment, token, &t); err != nil {
		return EnrolmentSession{}, ErrNoEnrolmentSession
	}
	exp := time.Unix(t.Exp, 0)
	if t.Site != siteKey(inst) || !clock(inst).Before(exp) || t.ID == "" {
		return EnrolmentSession{}, ErrNoEnrolmentSession
	}
	used, err := spent(inst, t.ID, exp)
	if err != nil || used {
		return EnrolmentSession{}, ErrNoEnrolmentSession
	}
	return EnrolmentSession{ID: t.ID, Expires: exp}, nil
}

// ConsumeEnrolment spends a session. ⛔ Call it when an enrolment COMPLETES,
// after the provider verified and before the record is saved: a replay racing
// the first completion loses here, on whichever process it lands.
func ConsumeEnrolment(inst Instance, sess EnrolmentSession) error {
	if err := Consume(inst, sess.ID, sess.Expires); err != nil {
		if errors.Is(err, ErrCeremony) {
			return ErrNoEnrolmentSession
		}
		return err
	}
	return nil
}
