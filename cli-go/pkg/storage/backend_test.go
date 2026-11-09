package storage

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestBackendSpecDefaultsToPosixAndRefusesAPartialOne(t *testing.T) {
	if s, err := BackendFromEnv(env(nil)); err != nil || s.Kind != BackendPOSIX {
		t.Errorf("unset = %+v, %v; want posix", s, err)
	}
	for name, m := range map[string]map[string]string{
		"source without a kind": {EnvStorageSource: "a"},
		"blob with no source": {EnvStorageKind: BackendVercelBlob, EnvStorageTokenIssuer: "https://i",
			EnvStorageTokenAudience: "a", EnvStorageTokenSubject: "s"},
		"no token verification": {EnvStorageKind: BackendVercelBlob, EnvStorageSource: "a"},
		"an unknown kind":       {EnvStorageKind: "s3ish"},
	} {
		if _, err := BackendFromEnv(env(m)); err == nil {
			t.Errorf("%s: accepted — a partial spec must refuse, never fall back to a filesystem", name)
		}
	}
	if s, err := BackendFromEnv(env(map[string]string{EnvStorageKind: BackendVercelBlob,
		EnvStorageSource: "src", EnvStorageTokenIssuer: "https://i",
		EnvStorageTokenAudience: "a", EnvStorageTokenSubject: "s"})); err != nil || s.Source != "src" {
		t.Errorf("complete blob spec = %+v, %v", s, err)
	}
}

// ⛔ The served-store setting is gone, and a boot that still sets it is
// refused BY NAME on every backend: ignored, an operator would believe a
// public store was still in use.
func TestTheRemovedServedStoreSettingIsRefusedByName(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"posix": {EnvStorageServed: "store_pub456"},
		"vercel-blob": {EnvStorageKind: BackendVercelBlob, EnvStorageSource: "src", EnvStorageServed: "store_pub456",
			EnvStorageTokenIssuer: "https://i", EnvStorageTokenAudience: "a", EnvStorageTokenSubject: "s"},
	} {
		_, err := BackendFromEnv(env(m))
		if err == nil || !strings.Contains(err.Error(), EnvStorageServed) {
			t.Errorf("%s: %v, want a refusal naming %s", name, err, EnvStorageServed)
		}
		if err != nil && strings.Contains(err.Error(), "store_pub456") {
			t.Errorf("%s: the refusal echoed the value: %v", name, err)
		}
	}
}

// fakeJWT is an unsigned token with an expiry — enough for the cell to read.
func fakeJWT(exp time.Time) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		enc.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix()))) + ".sig"
}

// ⛔ LATEST WINS, deliberately: the expiry is unverified, so "keep the later
// expiry" would let one forged far-future token stick until restart.
func TestARequestCredentialIsHeldUntilItsExpiryAndAForgedOneDoesNotStick(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	c := &RequestCredential{now: func() time.Time { return now }}
	if _, err := c.Credential(context.Background()); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("empty cell gave %v", err)
	}
	forged := fakeJWT(now.Add(100 * 365 * 24 * time.Hour))
	genuine := fakeJWT(now.Add(2 * time.Hour))
	c.Offer(forged)
	c.Offer(genuine) // ⭐ the next genuine request displaces the forgery
	if got, _ := c.Credential(context.Background()); got != genuine {
		t.Error("a forged far-future credential survived a genuine offer")
	}
	now = now.Add(2*time.Hour - time.Minute) // inside the margin
	if _, err := c.Credential(context.Background()); !errors.Is(err, ErrNoCredential) {
		t.Error("a credential about to lapse was still handed out")
	}
}

// openTestBackend opens a Vercel Blob backend on a fake platform whose tokens
// are verified against a fake issuer.
func openTestBackend(t *testing.T, iss *fakeIssuer, p *fakeBlobPlatform) Backend {
	t.Helper()
	spec, err := BackendFromEnv(env(map[string]string{EnvStorageKind: BackendVercelBlob,
		EnvStorageSource:      "store_src123",
		EnvStorageTokenIssuer: iss.srv.URL, EnvStorageTokenAudience: "https://vercel.com/acme",
		EnvStorageTokenSubject: testSubject}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := openVercelBlobBackend(spec, VercelBlobConfig{
		APIBaseURL: p.srv.URL + "/api", BlobBaseURL: p.srv.URL + "/blob/{store}", HTTPClient: p.srv.Client(),
		TokenHTTPClient: iss.srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func headerValues(pairs ...string) func(string) []string {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Add(pairs[i], pairs[i+1])
	}
	return h.Values
}

// ⭐⭐ THE NO-STATIC-SECRET PATH, END TO END: unusable until a request delivers
// a credential the issuer signed for this deployment; then it reads and
// writes, and .polis stays private.
func TestAVercelBlobBackendWorksOnTheCredentialARequestDelivers(t *testing.T) {
	ctx := context.Background()
	iss := newFakeIssuer(t)
	tok := iss.sign(t, iss.key, nil)
	p := newFakeBlobPlatform(t, tok, map[string]string{"src123": "private", "pub456": "public"})
	b := openTestBackend(t, iss, p)
	if err := b.Store.Put(ctx, "content/a.md", []byte("a")); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("before any request, a write gave %v; want ErrNoCredential", err)
	}
	// ⛔ The environment variable's name, sent as a header, must not count.
	if err := b.FromRequest(headerValues("VERCEL_OIDC_TOKEN", tok)); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.Put(ctx, "content/a.md", []byte("a")); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("a header named like the ENVIRONMENT variable was accepted (%v)", err)
	}
	if err := b.FromRequest(headerValues("x-vercel-oidc-token", tok)); err != nil {
		t.Fatalf("a genuine credential was refused: %v", err)
	}
	if err := b.Store.Put(ctx, "content/a.md", []byte("a")); err != nil {
		t.Fatalf("after a request delivered the credential: %v", err)
	}
	if err := b.Store.Put(ctx, ".polis/keys/id_ed25519", []byte("PRIVATE")); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Store.Get(ctx, "content/a.md"); err != nil || string(got) != "a" {
		t.Fatalf("read back %q, %v", got, err)
	}
	// ⛔ One store, and it is the private one: everything, .polis included,
	// is written there and nowhere else.
	if _, ok := p.stores["src123"].objects[".polis/keys/id_ed25519"]; !ok {
		t.Error("the private write did not reach the private store")
	}
	if pub := p.stores["pub456"]; len(pub.objects) != 0 {
		t.Errorf("⛔ %d objects reached a public store", len(pub.objects))
	}
}

// ⛔⛔ A FORGED TOKEN ON ONE REQUEST IS NEVER PRESENTED ON ANOTHER'S STORE
// CALLS, AND CANNOT DENY THEM STORAGE. Unverified, the process-wide cell took
// any token: a forged expired one failed every storage call until the next
// genuine request.
func TestAForgedTokenNeitherReplacesTheCredentialNorDeniesStorage(t *testing.T) {
	ctx := context.Background()
	iss := newFakeIssuer(t)
	genuine := iss.sign(t, iss.key, nil)
	p := newFakeBlobPlatform(t, genuine, map[string]string{"src123": "private", "pub456": "public"})
	b := openTestBackend(t, iss, p)
	if err := b.FromRequest(headerValues("x-vercel-oidc-token", genuine)); err != nil {
		t.Fatal(err)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for name, forged := range map[string]string{
		"another key":     iss.sign(t, other, nil),
		"expired":         iss.sign(t, iss.key, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}),
		"another project": iss.sign(t, iss.key, map[string]any{"sub": "owner:acme:project:evil:environment:production"}),
	} {
		if err := b.FromRequest(headerValues("x-vercel-oidc-token", forged)); err == nil {
			t.Errorf("%s: a forged credential was accepted", name)
		}
		// The next store call — another request's — still works on the genuine token.
		if err := b.Store.Put(ctx, "content/after-"+strings.ReplaceAll(name, " ", "-")+".md", []byte("x")); err != nil {
			t.Errorf("%s: storage was denied after a forged credential: %v", name, err)
		}
	}
}

// ⛔ Two values of the credential header — a client's beside the platform's —
// refuse the request. Header.Get returns the FIRST, which would be the client's.
func TestTwoCredentialHeaderValuesRefuseTheRequest(t *testing.T) {
	iss := newFakeIssuer(t)
	genuine := iss.sign(t, iss.key, nil)
	p := newFakeBlobPlatform(t, genuine, map[string]string{"src123": "private", "pub456": "public"})
	b := openTestBackend(t, iss, p)
	if err := b.FromRequest(headerValues("x-vercel-oidc-token", "client-value", "x-vercel-oidc-token", genuine)); err == nil {
		t.Error("a request with two credential header values was accepted")
	}
}

// ⭐ A credential that could not be CHECKED is not adopted, and the one already
// held keeps working: the request is not the place an issuer blip lands.
func TestAnUncheckableCredentialLeavesTheHeldOneInPlace(t *testing.T) {
	ctx := context.Background()
	iss := newFakeIssuer(t)
	genuine := iss.sign(t, iss.key, nil)
	p := newFakeBlobPlatform(t, genuine, map[string]string{"src123": "private", "pub456": "public"})
	b := openTestBackend(t, iss, p)
	if err := b.FromRequest(headerValues("x-vercel-oidc-token", genuine)); err != nil {
		t.Fatal(err)
	}
	// A key the issuer may have rotated in since the fetch a moment ago.
	rotated := iss.signKid(t, iss.key, "k-rotated", nil)
	if err := b.FromRequest(headerValues("x-vercel-oidc-token", rotated)); !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("an unknown kid inside the window gave %v, want ErrVerifierUnavailable", err)
	}
	if err := b.Store.Put(ctx, "content/still.md", []byte("x")); err != nil {
		t.Errorf("the held credential stopped working after an uncheckable one: %v", err)
	}
}

// ⭐ With the issuer down from the start, an offered token is not adopted: the
// store has no credential, which the deferred start answers with 503.
func TestAnIssuerOutageAtColdStartAdoptsNothing(t *testing.T) {
	ctx := context.Background()
	iss := newFakeIssuer(t)
	iss.failing.Store(true)
	tok := iss.sign(t, iss.key, nil)
	p := newFakeBlobPlatform(t, tok, map[string]string{"src123": "private", "pub456": "public"})
	b := openTestBackend(t, iss, p)
	if err := b.FromRequest(headerValues("x-vercel-oidc-token", tok)); !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("an issuer outage gave %v, want ErrVerifierUnavailable", err)
	}
	if err := b.Store.Put(ctx, "content/a.md", []byte("a")); !errors.Is(err, ErrNoCredential) {
		t.Errorf("a token that was never checked was used: %v", err)
	}
}

// ⛔ A laptop whose pulled token is refused is TOLD why, by cause, never with
// the token; the backend still opens.
func TestARefusedEnvironmentCredentialIsReportedNotSilent(t *testing.T) {
	iss := newFakeIssuer(t)
	wrong := iss.sign(t, iss.key, map[string]any{"sub": "owner:acme:project:site:environment:development"})
	p := newFakeBlobPlatform(t, wrong, map[string]string{"src123": "private", "pub456": "public"})
	b := openTestBackend(t, iss, p)
	b.offerEnvironment(env(map[string]string{"VERCEL_OIDC_TOKEN": wrong}))
	if b.EnvironmentCredentialErr == nil {
		t.Fatal("a pulled token with the wrong subject was refused silently")
	}
	if !strings.Contains(b.EnvironmentCredentialErr.Error(), "subject") {
		t.Errorf("the refusal does not say why: %v", b.EnvironmentCredentialErr)
	}
	for _, part := range strings.Split(wrong, ".") {
		if strings.Contains(b.EnvironmentCredentialErr.Error(), part) {
			t.Fatal("⛔ the refusal quotes the token")
		}
	}
	good := openTestBackend(t, iss, p)
	good.offerEnvironment(env(map[string]string{"VERCEL_OIDC_TOKEN": iss.sign(t, iss.key, nil)}))
	if good.EnvironmentCredentialErr != nil {
		t.Errorf("a genuine pulled token was reported refused: %v", good.EnvironmentCredentialErr)
	}
	empty := openTestBackend(t, iss, p)
	empty.offerEnvironment(env(nil))
	if empty.EnvironmentCredentialErr != nil {
		t.Errorf("an environment with no token reported an error: %v", empty.EnvironmentCredentialErr)
	}
}
