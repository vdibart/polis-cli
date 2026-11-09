package owner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Ceremonies and the single-use rule.
//
// ⭐ A ceremony is "begin, then complete": a WebAuthn challenge, an enrolment
// round trip. Its state is whatever the provider needs to check the answer, and
// it rides in a sealed cookie between the two requests — so the process that
// completes it need not be the one that began it.
//
// ⛔⛔ A SEALED TOKEN CAN BE PRESENTED TWICE, AND ONLY THE STORE CAN SAY NO. A
// cookie is a copy; nothing stops a client sending it again. So completing a
// ceremony writes a marker named by the ceremony's id, with IfAbsent, and a
// second completion — on this process or any other — loses the race.
//
// ⭐ VERIFY FIRST, THEN CONSUME. The marker is written only after the proof
// checked out, so a junk completion costs no store operation and consumes
// nothing: a stranger cannot spend the owner's ceremony by guessing at it.

// usedPrefix holds the single-use markers, one directory per UTC day of expiry.
const usedPrefix = ".polis/owner/used"

// ceremonyTTL bounds a begun ceremony. ⭐ Long enough to find a security key
// and fumble a PIN once; short enough that an abandoned tab is not a standing
// invitation.
const ceremonyTTL = 5 * time.Minute

// ErrCeremony means a ceremony token is missing, forged, expired, for another
// purpose, or already used. ⚠️ One error for all of them, deliberately.
var ErrCeremony = errors.New("owner: no ceremony is in progress; begin one first")

type ceremonyToken struct {
	Site    string `json:"site"`
	Purpose string `json:"purpose"`
	ID      string `json:"id"`
	Exp     int64  `json:"exp"`
	Bound   string `json:"bound,omitempty"`
	State   []byte `json:"state,omitempty"`
}

// Ceremony is a begun ceremony, as recovered from its token.
type Ceremony struct {
	// ID names it for the single-use marker.
	ID string
	// Bound is what it was tied to when it began (an enrolment session's id),
	// or empty.
	Bound string
	// State is the provider's own opaque state.
	State   []byte
	Expires time.Time
}

// BeginCeremony seals a ceremony's state for purpose, to ride in a cookie.
//
// ⛔ purpose names the COMPLETION that may consume it ("signin",
// "enrol:passkey"): a sign-in ceremony can never complete an enrolment, and the
// reverse. bound ties it to something else that must still be live at
// completion, such as the enrolment session that authorised it.
func BeginCeremony(inst Instance, purpose, bound string, state []byte) (string, time.Time, error) {
	id, err := randomID()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := clock(inst).Add(ceremonyTTL)
	tok, err := seal(inst, purposeCeremony, ceremonyToken{
		Site: siteKey(inst), Purpose: purpose, ID: id, Exp: exp.Unix(), Bound: bound, State: state,
	})
	return tok, exp, err
}

// OpenCeremony reads a ceremony token for purpose WITHOUT consuming it.
//
// ⭐ It costs no store operation: a forged, expired or foreign token is refused
// by the MAC and the clock alone. Whether it was already USED is only known by
// Consume, which the caller reaches after the proof has verified.
func OpenCeremony(inst Instance, purpose, token string) (Ceremony, error) {
	var c ceremonyToken
	if err := unseal(inst, purposeCeremony, token, &c); err != nil {
		return Ceremony{}, ErrCeremony
	}
	exp := time.Unix(c.Exp, 0)
	if c.Site != siteKey(inst) || c.Purpose != purpose || !clock(inst).Before(exp) {
		return Ceremony{}, ErrCeremony
	}
	return Ceremony{ID: c.ID, Bound: c.Bound, State: c.State, Expires: exp}, nil
}

// Consume spends a single-use id, across every process sharing the store.
//
// ⛔ Call it only AFTER the proof verified. It returns ErrCeremony if the id was
// already spent — the caller must then refuse, because a second completion is a
// replay whichever process saw the first.
func Consume(inst Instance, id string, expires time.Time) error {
	if inst.Store == nil {
		return errors.New("owner: this instance has no store, so a ceremony cannot be consumed")
	}
	key, err := usedKey(id, expires)
	if err != nil {
		return err
	}
	ctx := context.Background()
	perr := inst.Store.Put(ctx, key, []byte(expires.UTC().Format(time.RFC3339)), storage.IfAbsent(), storage.WithMode(0o600))
	switch {
	case perr == nil:
	case errors.Is(perr, storage.ErrExists):
		return ErrCeremony
	default:
		return fmt.Errorf("owner: recording a used ceremony: %w", perr)
	}
	sweepUsed(inst)
	return nil
}

// spent reports whether id has already been consumed. ⭐ Read-only, for a
// token that authorises several steps (an enrolment session) and is checked at
// each before it is finally spent.
func spent(inst Instance, id string, expires time.Time) (bool, error) {
	if inst.Store == nil {
		return true, nil
	}
	key, err := usedKey(id, expires)
	if err != nil {
		return true, err
	}
	return inst.Store.Exists(context.Background(), key)
}

func usedKey(id string, expires time.Time) (string, error) {
	// ⛔ The id came out of a MAC-checked token, so it is ours — but it is
	// still checked, because it becomes a storage key.
	if id == "" || strings.ContainsAny(id, `/\.`) || len(id) > 64 {
		return "", errors.New("owner: unusable ceremony id")
	}
	return usedPrefix + "/" + expires.UTC().Format("20060102") + "/" + id, nil
}

// sweepUsed deletes marker directories whose every token has expired.
//
// ⭐ Markers only need to outlive the tokens they guard, and each sits in the
// directory of its token's expiry day, so a directory for a day before today
// guards nothing. ⚠️ At most once a day per process, and only after a
// successful completion: a request that proved nothing never reaches here.
func sweepUsed(inst Instance) {
	today := clock(inst).UTC().Format("20060102")
	site := siteKey(inst)
	if c := inst.Cache; c != nil {
		c.mu.Lock()
		done := c.swept[site] == today
		c.swept[site] = today
		c.mu.Unlock()
		if done {
			return
		}
	}
	ctx := context.Background()
	entries, err := inst.Store.List(ctx, usedPrefix)
	if err != nil {
		return
	}
	for _, e := range entries {
		day := e.Key
		if i := strings.LastIndex(day, "/"); i >= 0 {
			day = day[i+1:]
		}
		if len(day) == 8 && day < today {
			_ = inst.Store.Delete(ctx, usedPrefix+"/"+day, storage.DeleteRecursive(), storage.IgnoreMissing())
		}
	}
}
