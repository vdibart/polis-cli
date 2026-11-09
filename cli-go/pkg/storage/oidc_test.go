package storage

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeIssuer is an OIDC issuer: a discovery document, a key set, and a signer.
// ⭐ Served over TLS, because the verifier refuses an issuer that is not https.
type fakeIssuer struct {
	srv *httptest.Server
	key *rsa.PrivateKey
	kid string

	fetches atomic.Int32  // key-set requests served
	failing atomic.Bool   // answer the key set with a 500
	block   chan struct{} // when set, the key set waits on it
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{key: k, kid: "k1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "jwks_uri": f.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		f.fetches.Add(1)
		if f.block != nil {
			<-f.block
		}
		if f.failing.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		enc := base64.RawURLEncoding
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": f.kid,
			"n": enc.EncodeToString(k.PublicKey.N.Bytes()),
			"e": enc.EncodeToString(big.NewInt(int64(k.PublicKey.E)).Bytes()),
		}}})
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

const testSubject = "owner:acme:project:site:environment:production"

// sign issues a token; claims override the defaults.
func (f *fakeIssuer) sign(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	return f.signKid(t, key, f.kid, claims)
}

// signKid issues a token whose header names kid.
func (f *fakeIssuer) signKid(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	c := map[string]any{
		"iss": f.srv.URL, "aud": "https://vercel.com/acme", "sub": testSubject,
		"exp": time.Now().Add(2 * time.Hour).Unix(), "nbf": time.Now().Unix(),
	}
	for k, v := range claims {
		c[k] = v
	}
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	p, _ := json.Marshal(c)
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(p)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + enc.EncodeToString(sig)
}

func (f *fakeIssuer) verifier() *OIDCVerifier {
	return &OIDCVerifier{Issuer: f.srv.URL, Audience: "https://vercel.com/acme", Subject: testSubject, HTTPClient: f.srv.Client()}
}

// ⛔ Only a token the issuer signed for exactly this deployment verifies.
func TestOnlyATokenTheIssuerSignedForThisDeploymentVerifies(t *testing.T) {
	ctx := context.Background()
	f := newFakeIssuer(t)
	v := f.verifier()
	if err := v.Verify(ctx, f.sign(t, f.key, nil)); err != nil {
		t.Fatalf("a genuine token was refused: %v", err)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	enc := base64.RawURLEncoding
	none := enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString([]byte(`{"sub":"`+testSubject+`"}`)) + "."
	for name, tok := range map[string]string{
		"signed by another key": f.sign(t, other, nil),
		"another project":       f.sign(t, f.key, map[string]any{"sub": "owner:acme:project:evil:environment:production"}),
		"another audience":      f.sign(t, f.key, map[string]any{"aud": "https://vercel.com/evil"}),
		"another issuer":        f.sign(t, f.key, map[string]any{"iss": "https://evil.example"}),
		"expired":               f.sign(t, f.key, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}),
		"not yet valid":         f.sign(t, f.key, map[string]any{"nbf": time.Now().Add(time.Hour).Unix()}),
		"alg none":              none,
		"not a JWT":             "garbage",
	} {
		if err := v.Verify(ctx, tok); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
}

// clockAt is a settable clock for a verifier, starting at the real time so the
// tokens' own exp and nbf still read correctly.
type clockAt struct{ t atomic.Int64 }

func newClock() *clockAt               { c := &clockAt{}; c.t.Store(time.Now().UnixNano()); return c }
func (c *clockAt) now() time.Time      { return time.Unix(0, c.t.Load()) }
func (c *clockAt) add(d time.Duration) { c.t.Add(int64(d)) }

// ⛔ A failed key-set fetch starts no minute-long back-off: once the short
// floor has passed, the next token fetches again and verifies.
func TestAFailedKeyFetchRetriesOnTheNextToken(t *testing.T) {
	ctx := context.Background()
	f := newFakeIssuer(t)
	clk := newClock()
	v := f.verifier()
	v.now = clk.now
	tok := f.sign(t, f.key, nil)

	f.failing.Store(true)
	if err := v.Verify(ctx, tok); !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("an issuer outage gave %v, want ErrVerifierUnavailable", err)
	}
	// Inside the floor: answered without asking the issuer again.
	before := f.fetches.Load()
	if err := v.Verify(ctx, tok); !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("inside the retry floor: %v", err)
	}
	if f.fetches.Load() != before {
		t.Error("a dead issuer was asked again inside the retry floor")
	}
	// The issuer recovers; well under a minute later, the next token verifies.
	f.failing.Store(false)
	clk.add(failedFetchRetry + time.Second)
	if err := v.Verify(ctx, tok); err != nil {
		t.Fatalf("after the issuer recovered, %v later the token was refused: %v", failedFetchRetry+time.Second, err)
	}
}

// ⭐ After a SUCCESSFUL fetch, an unknown kid inside the window is unavailable,
// not refused (the issuer may have rotated keys since), and does not refetch.
// Once the window has passed it refetches, and a kid the issuer still does not
// have is refused.
func TestAnUnknownKidAfterASuccessfulFetchIsRateLimited(t *testing.T) {
	ctx := context.Background()
	f := newFakeIssuer(t)
	clk := newClock()
	v := f.verifier()
	v.now = clk.now
	if err := v.Verify(ctx, f.sign(t, f.key, nil)); err != nil {
		t.Fatal(err)
	}
	fetched := f.fetches.Load()
	rotated := f.signKid(t, f.key, "k-rotated", nil)
	err := v.Verify(ctx, rotated)
	if !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("an unknown kid inside the window gave %v, want ErrVerifierUnavailable", err)
	}
	if f.fetches.Load() != fetched {
		t.Error("an unknown kid inside the window refetched the key set")
	}
	clk.add(keyRefetchWindow + time.Second)
	err = v.Verify(ctx, rotated)
	if err == nil || errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("a kid the issuer does not have, just after a fresh fetch, gave %v; want a refusal", err)
	}
	if f.fetches.Load() != fetched+1 {
		t.Errorf("after the window: %d fetches, want one more than %d", f.fetches.Load(), fetched)
	}
}

// ⛔ A forged or mismatched token is still REFUSED, never merely unavailable.
func TestAForgedOrMismatchedTokenIsRefusedNotUnavailable(t *testing.T) {
	ctx := context.Background()
	f := newFakeIssuer(t)
	v := f.verifier()
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for name, tok := range map[string]string{
		"a known kid, another key's signature": f.sign(t, other, nil),
		"another project":                      f.sign(t, f.key, map[string]any{"sub": "owner:acme:project:evil:environment:production"}),
		"expired":                              f.sign(t, f.key, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}),
	} {
		err := v.Verify(ctx, tok)
		if err == nil || errors.Is(err, ErrVerifierUnavailable) {
			t.Errorf("%s: %v, want a refusal", name, err)
		}
	}
}

// ⛔ The lock is not held across the network: while a slow fetch is in
// flight, a token whose key is already cached still verifies.
func TestKeyFetchDoesNotHoldTheLock(t *testing.T) {
	ctx := context.Background()
	f := newFakeIssuer(t)
	clk := newClock()
	v := f.verifier()
	v.now = clk.now
	cached := f.sign(t, f.key, nil)
	if err := v.Verify(ctx, cached); err != nil {
		t.Fatal(err)
	}
	clk.add(keyRefetchWindow + time.Second)
	f.block = make(chan struct{})
	defer close(f.block)
	slow := make(chan error, 1)
	go func() { slow <- v.Verify(ctx, f.signKid(t, f.key, "k-new", nil)) }()
	// Wait until the fetch is really in flight.
	deadline := time.Now().Add(5 * time.Second)
	for {
		v.mu.Lock()
		inflight := v.inflight != nil
		v.mu.Unlock()
		if inflight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fetch never started")
		}
		time.Sleep(time.Millisecond)
	}
	done := make(chan error, 1)
	go func() { done <- v.Verify(ctx, cached) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a cached-key token was refused during a fetch: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("⛔ a cached-key token waited on another token's network fetch")
	}
}

// ⛔ The shared fetch is not the leading request's: that request disconnecting
// mid-fetch neither fails the fetch nor marks the issuer unavailable for the
// requests waiting on it.
func TestACancelledLeadingRequestDoesNotFailTheSharedFetch(t *testing.T) {
	f := newFakeIssuer(t)
	v := f.verifier()
	tok := f.sign(t, f.key, nil)
	release := make(chan struct{})
	f.block = release
	lctx, cancel := context.WithCancel(context.Background())
	lead := make(chan error, 1)
	go func() { lead <- v.Verify(lctx, tok) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.fetches.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the fetch never started")
		}
		time.Sleep(time.Millisecond)
	}
	waiter := make(chan error, 1)
	go func() { waiter <- v.Verify(context.Background(), tok) }()
	cancel()
	select {
	case err := <-lead:
		if !errors.Is(err, ErrVerifierUnavailable) {
			t.Errorf("the cancelled leading request got %v, want unavailable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled leading request kept waiting on the fetch")
	}
	close(release)
	select {
	case err := <-waiter:
		if err != nil {
			t.Errorf("⛔ a waiter lost the keys to another request's cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter never got the keys")
	}
	if err := v.Verify(context.Background(), tok); err != nil {
		t.Errorf("a later token was refused: %v", err)
	}
	if n := f.fetches.Load(); n != 1 {
		t.Errorf("%d key-set fetches, want the one shared fetch", n)
	}
}

// ⛔ An issuer that is not https is refused: by the verifier, and at boot.
func TestAnIssuerThatIsNotHTTPSIsRefused(t *testing.T) {
	v := &OIDCVerifier{Issuer: "http://oidc.example", Audience: "a", Subject: "s"}
	if err := v.Verify(context.Background(), "a.b.c"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("an http issuer: %v", err)
	}
	for _, iss := range []string{"http://oidc.example", "oidc.example", "https://", "ftp://oidc.example"} {
		_, err := BackendFromEnv(env(map[string]string{EnvStorageKind: BackendVercelBlob,
			EnvStorageSource: "src", EnvStorageTokenIssuer: iss,
			EnvStorageTokenAudience: "a", EnvStorageTokenSubject: "s"}))
		if err == nil || !strings.Contains(err.Error(), EnvStorageTokenIssuer) {
			t.Errorf("issuer %q at boot: %v, want a refusal naming %s", iss, err, EnvStorageTokenIssuer)
		}
	}
}
