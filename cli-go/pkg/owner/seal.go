package owner

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Sealed tokens: what lets a session or a half-finished ceremony live in the
// client instead of in a process.
//
// ⭐⭐ WHY THIS SHAPE. On a serverless platform the request that begins a
// ceremony and the one that completes it land on different processes, and a
// process may be gone a few idle minutes later. State kept in memory therefore
// fails at random. So the state rides in a cookie the client returns, and the
// instance only has to recognise it: a payload and an HMAC over it, under a key
// held in the site's PRIVATE store.
//
// ⛔ A SEALED TOKEN IS AUTHENTICATED, NOT ENCRYPTED. Nothing secret goes in one:
// a WebAuthn challenge, a provider name, an expiry. A field that would be a
// problem if the owner's browser could read it does not belong here.
//
// ⛔ EVERY TOKEN NAMES ITS PURPOSE INSIDE THE MAC, so a token minted for one job
// can never be presented as another: a ceremony cookie is not a session, and a
// recovery session is not a sign-in. The purpose is not a field the client
// could edit; it is part of the bytes the MAC covers.

// SessionKeyKey is where the MAC key lives, inside the site's private store.
//
// ⛔ Under .polis, which is never served and on a remote backend lives only in
// the PRIVATE store. Never an environment variable: a key a platform holds in
// its project settings is a key the platform can read.
const SessionKeyKey = ".polis/owner/session.key"

const (
	purposeSession   = "session"
	purposeCeremony  = "ceremony"
	purposeEnrolment = "enrolment"
	purposeRecovery  = "recovery"
)

// errSealed is every way a token can fail to open. ⚠️ One error on purpose:
// telling "forged" from "expired" from "another site's" apart tells a caller
// which tokens existed, and the remedy is the same.
var errSealed = errors.New("owner: token is not valid here")

// macKey returns this site's MAC key, creating it the first time.
//
// ⛔ THE CREATE IS CONDITIONAL, never a read-then-write: two processes starting
// at once must not each mint a key, or each would refuse the other's sessions.
// The loser of the race adopts the winner's key, exactly as the identity key
// does.
func macKey(inst Instance) ([]byte, error) {
	if inst.Store == nil {
		return nil, errors.New("owner: this instance has no store, so it cannot sign a session")
	}
	site := siteKey(inst)
	if c := inst.Cache; c != nil {
		c.mu.Lock()
		k := c.keys[site]
		c.mu.Unlock()
		if k != nil {
			return k, nil
		}
	}
	ctx := context.Background()
	key, err := inst.Store.Get(ctx, SessionKeyKey)
	switch {
	case err == nil:
	case errors.Is(err, storage.ErrNotFound):
		fresh := make([]byte, 32)
		if _, rerr := rand.Read(fresh); rerr != nil {
			return nil, rerr
		}
		perr := inst.Store.Put(ctx, SessionKeyKey, fresh, storage.IfAbsent(), storage.WithMode(0o600))
		switch {
		case perr == nil:
			key = fresh
		case errors.Is(perr, storage.ErrExists):
			if key, err = inst.Store.Get(ctx, SessionKeyKey); err != nil {
				return nil, fmt.Errorf("owner: reading the session key another start created: %w", err)
			}
		default:
			return nil, fmt.Errorf("owner: storing the session key: %w", perr)
		}
	default:
		// ⛔ "Could not look" is not "absent": generating here would replace
		// the key whenever the store hiccups, signing the owner out.
		return nil, fmt.Errorf("owner: reading the session key: %w", err)
	}
	if len(key) < 32 {
		// ⛔ Name the problem, never the bytes.
		return nil, fmt.Errorf("owner: the session key at %s is too short to use", SessionKeyKey)
	}
	if c := inst.Cache; c != nil {
		c.mu.Lock()
		c.keys[site] = key
		c.mu.Unlock()
	}
	return key, nil
}

// PrepareSessions loads the MAC key, creating it if this is the site's first
// start, and fills the process cache with what the gate reads.
//
// ⭐ CALLED WHEN THE SITE STARTS, so a request that has proved nothing never
// has to touch the store: the key is already in memory when the first sign-in
// begins. The lazy path in macKey still works for a caller that never prepared.
func PrepareSessions(inst Instance) error {
	if _, err := macKey(inst); err != nil {
		return err
	}
	// ⭐ Warm the enrolment records the gate and the sign-in begin read.
	_ = Active(inst)
	return nil
}

// seal MACs payload under the site's key for one purpose.
func seal(inst Instance, purpose string, payload any) (string, error) {
	key, err := macKey(inst)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + base64.RawURLEncoding.EncodeToString(mac(key, purpose, body)), nil
}

// unseal checks a token's MAC for purpose and decodes its payload.
//
// ⭐ The MAC is checked BEFORE anything is decoded, so a forged token costs a
// hash and nothing else: no store read, no JSON parse of attacker bytes.
func unseal(inst Instance, purpose, token string, into any) error {
	if token == "" || len(token) > 8192 {
		return errSealed
	}
	body, sig, ok := strings.Cut(token, ".")
	if !ok || body == "" || sig == "" {
		return errSealed
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return errSealed
	}
	key, err := macKey(inst)
	if err != nil {
		return err
	}
	if !hmac.Equal(got, mac(key, purpose, body)) {
		return errSealed
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return errSealed
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return errSealed
	}
	return nil
}

func mac(key []byte, purpose, body string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("pub.polis.owner.sealed.v1\x00" + purpose + "\x00" + body))
	return h.Sum(nil)
}

// randomID is a fresh 128-bit identifier, URL-safe.
func randomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
