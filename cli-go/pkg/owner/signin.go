package owner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Signed-in sessions: what a successful provider ceremony leaves behind.
//
// ⛔⛔ DISTINCT FROM AN ENROLMENT SESSION, AND THE DIFFERENCE IS THE WHOLE
// POINT. An enrolment session can do exactly one thing — add a provider — and
// is spent on use. THIS is the real thing: it says the owner is present, and a
// request carrying it passes the gate. ⚠️ Never let one become the other; they
// are sealed under different purposes, so neither can be presented as the
// other.
//
// ⭐⭐ A SESSION IS A SEALED TOKEN, NOT A ROW. It carries the site, the provider
// that opened it, when it was issued, when it expires, and the generations it
// was issued under, all inside the MAC. So any process holding the site's key
// recognises it, a restart signs nobody out, and checking one costs a hash —
// plus one read of the generations, cached per process.
//
// ⭐ ENDING SESSIONS IS A GENERATION BUMP. "Sign out everywhere" raises the
// site's generation; removing a provider raises that provider's. A session
// issued under an older generation no longer matches and is refused. ⚠️ A
// process trusts its cached generations for up to cacheTTL, so another process
// honours a sign-out within that window, not instantly; the process that served
// the sign-out applies it at once.
//
// ⛔ KEYED BY SITE, because hosted runs many tenants in one process: the site
// is inside the MAC, so a session minted for one tenant never opens another.

// signInTTL bounds a signed-in session.
//
// ⭐ Long enough to write and publish without being asked again; short enough
// that a laptop left open in a cafe is not indefinitely authorised.
const signInTTL = 12 * time.Hour

// generationsKey holds the counters that end sessions, in the private store.
const generationsKey = ".polis/owner/generations.json"

// maxRevoked bounds the individually signed-out sessions remembered until they
// expire. ⭐ Past it, signing one out ends them all — the owner's own sessions,
// so the worst case is signing in again.
const maxRevoked = 64

// generations is what decides whether a well-formed session still counts.
type generations struct {
	Everywhere uint64            `json:"everywhere"`
	Providers  map[string]uint64 `json:"providers,omitempty"`
	// Revoked maps a signed-out session's id to its expiry (unix seconds).
	Revoked map[string]int64 `json:"revoked,omitempty"`
}

type sessionToken struct {
	Site     string `json:"site"`
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Issued   int64  `json:"iat"`
	Exp      int64  `json:"exp"`
	Gen      uint64 `json:"gen"`
	PGen     uint64 `json:"pgen"`
}

// siteKey identifies a site for session purposes.
//
// ⛔ THE BASE URL, WHICH IS CONFIGURATION, never anything from a request, and
// the site id beside it: two servers sharing a base URL shared their sessions.
func siteKey(inst Instance) string { return inst.BaseURL + "\x00" + inst.SiteID }

// OpenSignIn mints a signed-in session for this site, opened by provider.
//
// ⭐ Call it ONLY after a provider has verified a ceremony. Nothing in this
// package checks that for you, because only the provider knows what its own
// proof looks like. ⭐ It reads the generations FRESH, not from the cache: the
// request has proved something, so one read is earned, and a session minted
// under stale generations would die as soon as the cache caught up.
func OpenSignIn(inst Instance, provider string) (string, time.Time, error) {
	if _, err := enrolmentKey(provider); err != nil {
		return "", time.Time{}, err
	}
	g, err := loadGenerations(inst, true)
	if err != nil {
		return "", time.Time{}, err
	}
	id, err := randomID()
	if err != nil {
		return "", time.Time{}, err
	}
	now := clock(inst)
	exp := now.Add(signInTTL)
	tok, err := seal(inst, purposeSession, sessionToken{
		Site: siteKey(inst), Provider: provider, ID: id,
		Issued: now.Unix(), Exp: exp.Unix(),
		Gen: g.Everywhere, PGen: g.Providers[provider],
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

// session opens a token and checks it against the clock and the generations.
//
// ⭐ The MAC and the clock are checked first and cost nothing; only a token
// that passes both reaches the (cached) generations read.
func session(inst Instance, token string) (sessionToken, error) {
	var s sessionToken
	if token == "" {
		return s, errSealed
	}
	if err := unseal(inst, purposeSession, token, &s); err != nil {
		return s, err
	}
	now := clock(inst)
	if s.Site != siteKey(inst) || !now.Before(time.Unix(s.Exp, 0)) || s.ID == "" {
		return s, errSealed
	}
	g, err := loadGenerations(inst, false)
	if err != nil {
		return s, err
	}
	if s.Gen != g.Everywhere || s.PGen != g.Providers[s.Provider] {
		return s, errSealed
	}
	if _, revoked := g.Revoked[s.ID]; revoked {
		return s, errSealed
	}
	return s, nil
}

// SignedIn reports whether a token is a live session for this site.
func SignedIn(inst Instance, token string) bool {
	_, err := session(inst, token)
	return err == nil
}

// SessionProvider names the provider that opened a live session, or "".
func SessionProvider(inst Instance, token string) string {
	s, err := session(inst, token)
	if err != nil {
		return ""
	}
	return s.Provider
}

// SignedInWithin reports whether token is a live session opened no more than d
// ago. ⭐ For acts that outlive a session — enrolling or removing a way to sign
// in — a fresh sign-in is required, so a stolen 12-hour session cannot add its
// own passkey. ⛔ The issue time is inside the MAC, so it cannot be refreshed
// by the client.
func SignedInWithin(inst Instance, token string, d time.Duration) bool {
	s, err := session(inst, token)
	if err != nil {
		return false
	}
	return clock(inst).Sub(time.Unix(s.Issued, 0)) <= d
}

// SignOut ends one session. ⭐ Absent or already invalid is success: signing
// out twice is not an error, and a UI that double-submits must not report one.
//
// ⭐ The cookie is cleared by the caller; this also remembers the session's id
// until it would have expired, so a copy of the cookie elsewhere is dead too.
func SignOut(inst Instance, token string) error {
	s, err := session(inst, token)
	if err != nil {
		return nil
	}
	return updateGenerations(inst, func(g *generations) {
		now := clock(inst).Unix()
		for id, exp := range g.Revoked {
			if exp <= now {
				delete(g.Revoked, id)
			}
		}
		if len(g.Revoked) >= maxRevoked {
			g.Everywhere++
			g.Revoked = nil
			return
		}
		if g.Revoked == nil {
			g.Revoked = map[string]int64{}
		}
		g.Revoked[s.ID] = s.Exp
	})
}

// SignOutAll ends every session for a site.
//
// ⭐ What "sign out everywhere" means. ⚠️ Other processes honour it within
// cacheTTL.
func SignOutAll(inst Instance) error {
	return updateGenerations(inst, func(g *generations) {
		g.Everywhere++
		// ⭐ Revocations from before are moot now: every older session is dead.
		g.Revoked = nil
	})
}

// EndProviderSessions ends every session a provider opened.
//
// ⛔ What removing a way to sign in must call: a credential the owner has just
// revoked must not keep authorising requests through a session it minted
// earlier, and re-enrolling must not revive one. The counter outlives the
// enrolment record, so a fresh enrolment starts above every old session.
func EndProviderSessions(inst Instance, provider string) error {
	if _, err := enrolmentKey(provider); err != nil {
		return err
	}
	return updateGenerations(inst, func(g *generations) {
		if g.Providers == nil {
			g.Providers = map[string]uint64{}
		}
		g.Providers[provider]++
	})
}

// loadGenerations reads the counters, from the cache unless fresh.
func loadGenerations(inst Instance, fresh bool) (generations, error) {
	if inst.Store == nil {
		return generations{}, errors.New("owner: this instance has no store")
	}
	site := siteKey(inst)
	c := inst.Cache
	if c != nil && !fresh {
		c.mu.Lock()
		hit, ok := c.gens[site]
		ttl, now := c.ttl, c.now()
		c.mu.Unlock()
		if ok && now.Sub(hit.at) < ttl {
			return hit.g, nil
		}
	}
	var g generations
	blob, err := inst.Store.Get(context.Background(), generationsKey)
	switch {
	case err == nil:
		if err := json.Unmarshal(blob, &g); err != nil {
			// ⛔ An unreadable file is refused, never read as "generation
			// zero": that would revive every session it had ended.
			return generations{}, fmt.Errorf("owner: %s is unreadable: %w", generationsKey, err)
		}
	case errors.Is(err, storage.ErrNotFound):
	default:
		return generations{}, fmt.Errorf("owner: reading %s: %w", generationsKey, err)
	}
	if c != nil {
		c.mu.Lock()
		c.gens[site] = cachedGenerations{g: g, at: c.now()}
		c.mu.Unlock()
	}
	return g, nil
}

// updateGenerations applies change to a fresh read and writes it back.
//
// ⚠️ A read-modify-write, not a compare-and-swap: two owners signing out on two
// processes in the same instant could lose one revocation. Every change here
// only ever ENDS sessions and the counters only rise, so the worst case is one
// session the owner asked to end staying live until it expires; the owner's
// remedy is "sign out everywhere", which is a single increment.
func updateGenerations(inst Instance, change func(*generations)) error {
	g, err := loadGenerations(inst, true)
	if err != nil {
		return err
	}
	change(&g)
	blob, err := json.Marshal(g)
	if err != nil {
		return err
	}
	if err := inst.Store.Put(context.Background(), generationsKey, blob, storage.WithMode(0o600)); err != nil {
		return fmt.Errorf("owner: writing %s: %w", generationsKey, err)
	}
	if c := inst.Cache; c != nil {
		c.mu.Lock()
		c.gens[siteKey(inst)] = cachedGenerations{g: g, at: c.now()}
		c.mu.Unlock()
	}
	return nil
}
