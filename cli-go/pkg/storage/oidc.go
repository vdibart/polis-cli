package storage

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OIDCVerifier verifies an OpenID Connect token (RS256) before a backend will
// present it: its signature against the issuer's published keys, then its
// issuer, audience, subject and time claims.
//
// ⛔⛔ WHY A REQUEST-DELIVERED CREDENTIAL MUST BE VERIFIED. RequestCredential is
// one process-wide cell, refreshed from any request that carries the
// credential header. Unverified, a forged header on ONE request replaced the
// token for EVERY request's store calls: a forged expired token denied all
// storage, and if the platform both passed a client's header through and let a
// token address a store other than the one named, a new instance could write
// its private key into an attacker's store. A token is accepted only if the
// issuer signed it for exactly this deployment.
//
// ⭐ PROVIDER-NEUTRAL: this is plain OIDC. Vercel is one issuer.
type OIDCVerifier struct {
	Issuer   string // exact `iss`
	Audience string // must appear in `aud`
	Subject  string // exact `sub`: names the owner, project and environment

	HTTPClient *http.Client
	now        func() time.Time

	mu       sync.Mutex
	keys     map[string]*rsa.PublicKey
	fetched  time.Time     // the last SUCCESSFUL fetch of the key set
	failedAt time.Time     // the last failed fetch, zero after a success
	lastErr  error         // why that fetch failed
	inflight chan struct{} // open while a fetch runs; closed when it ends
}

// errUnverified wraps every refusal. ⛔ It never quotes the token.
var errUnverified = errors.New("storage: credential not verified")

// ErrVerifierUnavailable means a token could not be CHECKED right now — the
// issuer's keys could not be loaded, or the token names a key we have not
// fetched yet and the refetch window has not reopened — as distinct from a
// token that was checked and is wrong. ⛔ Either way the token is NOT accepted.
// ⭐ The difference is what the caller does with the REQUEST: a bad token refuses
// it, an unavailable verifier lets it through on whatever credential was
// already held, so a blip at the issuer does not refuse every public read. It
// wraps errUnverified, so anything that treats every refusal alike still does.
var ErrVerifierUnavailable = fmt.Errorf("%w: the issuer's keys are unavailable", errUnverified)

const oidcSkew = time.Minute

// keyRefetchWindow is how long after a SUCCESSFUL fetch an unknown kid waits
// before the key set is fetched again, so a flood of forged kids cannot hammer
// the issuer. failedFetchRetry is the shorter floor after a FAILED fetch: a
// failure starts no minute-long back-off, but a dead issuer is not asked on
// every request either, since each attempt can take the client's whole timeout.
const (
	keyRefetchWindow = time.Minute
	failedFetchRetry = 5 * time.Second
)

// keyFetchTimeout bounds the shared key-set fetch, which no request's deadline
// does.
const keyFetchTimeout = 10 * time.Second

func (v *OIDCVerifier) clock() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

// Verify reports whether tok is a token the issuer signed for this deployment.
// An error wrapping ErrVerifierUnavailable means it could not be checked now;
// any other error means it was checked and refused.
func (v *OIDCVerifier) Verify(ctx context.Context, tok string) error {
	if v.Issuer == "" || v.Audience == "" || v.Subject == "" {
		return fmt.Errorf("%w: the verifier has no issuer, audience or subject configured", errUnverified)
	}
	// ⛔ The key set is fetched from the issuer's own URL; over plain http
	// anyone on the path could hand us their keys.
	if !isHTTPSURL(v.Issuer) {
		return fmt.Errorf("%w: the issuer is not an https URL", errUnverified)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: not a JWT", errUnverified)
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return fmt.Errorf("%w: header: %v", errUnverified, err)
	}
	// ⛔ RS256 only: an "alg" the token chooses for itself (none, HS256) is
	// the classic way past a JWT check.
	if hdr.Alg != "RS256" {
		return fmt.Errorf("%w: algorithm %q is not RS256", errUnverified, hdr.Alg)
	}
	key, err := v.key(ctx, hdr.Kid)
	if err != nil {
		return err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("%w: signature encoding", errUnverified)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("%w: bad signature", errUnverified)
	}
	var c struct {
		Iss string          `json:"iss"`
		Sub string          `json:"sub"`
		Aud json.RawMessage `json:"aud"`
		Exp int64           `json:"exp"`
		Nbf int64           `json:"nbf"`
	}
	if err := decodeSegment(parts[1], &c); err != nil {
		return fmt.Errorf("%w: claims: %v", errUnverified, err)
	}
	t := v.clock()
	switch {
	case c.Iss != v.Issuer:
		return fmt.Errorf("%w: issuer does not match", errUnverified)
	case c.Sub != v.Subject:
		return fmt.Errorf("%w: subject does not match this deployment", errUnverified)
	case !audienceHas(c.Aud, v.Audience):
		return fmt.Errorf("%w: audience does not match", errUnverified)
	case c.Exp == 0 || t.After(time.Unix(c.Exp, 0).Add(oidcSkew)):
		return fmt.Errorf("%w: expired", errUnverified)
	case c.Nbf != 0 && t.Add(oidcSkew).Before(time.Unix(c.Nbf, 0)):
		return fmt.Errorf("%w: not yet valid", errUnverified)
	}
	return nil
}

func audienceHas(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == want {
				return true
			}
		}
	}
	return false
}

func decodeSegment(seg string, out any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// key returns the issuer's key for kid, fetching the key set through the
// issuer's discovery document. ⭐ An unknown kid refetches, at most once per
// keyRefetchWindow after a SUCCESSFUL fetch, so a key rotation at the issuer is
// picked up without letting a flood of forged kids hammer it.
//
// ⛔ A FAILED FETCH STARTS NO BACK-OFF. It used to stamp the same time a
// success did, so one blip at the issuer made every new token read "unknown
// signing key" for a minute — and on a platform that sends the credential with
// every request, that refused every request. Now only failedFetchRetry, a short
// floor, separates attempts, and both a failure and an unknown kid inside the
// window answer ErrVerifierUnavailable rather than a refusal.
//
// ⛔ THE LOCK IS NOT HELD ACROSS THE NETWORK. A slow issuer used to stall every
// verification, even of tokens whose key was already cached. Concurrent misses
// share one in-flight fetch instead of each starting their own.
func (v *OIDCVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	if k, ok := v.keys[kid]; ok {
		v.mu.Unlock()
		return k, nil
	}
	if ch := v.inflight; ch != nil {
		v.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: waiting for the issuer's keys: %v", ErrVerifierUnavailable, ctx.Err())
		}
		return v.keyAfterFetch(kid)
	}
	now := v.clock()
	if !v.fetched.IsZero() && now.Sub(v.fetched) < keyRefetchWindow {
		v.mu.Unlock()
		// ⭐ Possibly a key the issuer rotated in since our last fetch: not
		// adopted, and not held against the request either.
		return nil, fmt.Errorf("%w: unknown signing key, inside the refetch window", ErrVerifierUnavailable)
	}
	if !v.failedAt.IsZero() && now.Sub(v.failedAt) < failedFetchRetry {
		err := v.lastErr
		v.mu.Unlock()
		return nil, fmt.Errorf("%w: cannot load the issuer's keys: %v", ErrVerifierUnavailable, err)
	}
	ch := make(chan struct{})
	v.inflight = ch
	v.mu.Unlock()

	// ⛔ THE SHARED FETCH IS NOT THE LEADING REQUEST'S. It ran on that request's
	// context, so a client that disconnected mid-fetch recorded a failure every
	// waiter shared, and a cold instance answered "unavailable" for the whole
	// failedFetchRetry. It runs detached, under its own timeout, and the request
	// that started it waits like any other.
	go func() {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), keyFetchTimeout)
		defer cancel()
		keys, err := v.fetchKeys(fctx)

		v.mu.Lock()
		if err != nil {
			v.failedAt, v.lastErr = v.clock(), err
		} else {
			v.keys, v.fetched, v.failedAt, v.lastErr = keys, v.clock(), time.Time{}, nil
		}
		v.inflight = nil
		close(ch)
		v.mu.Unlock()
	}()
	select {
	case <-ch:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: waiting for the issuer's keys: %v", ErrVerifierUnavailable, ctx.Err())
	}
	return v.keyAfterFetch(kid)
}

// keyAfterFetch reads kid once a fetch has finished. ⭐ After a SUCCESSFUL fetch
// an unknown kid is a refusal: the issuer was just asked and does not have it.
func (v *OIDCVerifier) keyAfterFetch(kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	if v.lastErr != nil {
		return nil, fmt.Errorf("%w: cannot load the issuer's keys: %v", ErrVerifierUnavailable, v.lastErr)
	}
	return nil, fmt.Errorf("%w: unknown signing key", errUnverified)
}

// isHTTPSURL reports whether s is an absolute https URL with a host.
func isHTTPSURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

func (v *OIDCVerifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	hc := v.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	get := func(u string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
		}
		return json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(out)
	}
	var disc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := get(strings.TrimSuffix(v.Issuer, "/")+"/.well-known/openid-configuration", &disc); err != nil {
		return nil, err
	}
	// ⛔ The discovery document must be the issuer's own.
	if disc.Issuer != v.Issuer || disc.JWKSURI == "" {
		return nil, errors.New("discovery document does not name this issuer or a key set")
	}
	var set struct {
		Keys []struct {
			Kty, Kid, N, E string
		} `json:"keys"`
	}
	if err := get(disc.JWKSURI, &set); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return out, nil
}
