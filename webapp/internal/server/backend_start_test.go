package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// gatedStore refuses everything until its credential has been delivered,
// which is how a request-credentialed backend behaves at cold start.
type gatedStore struct {
	storage.Store
	open *atomic.Bool
}

func (g gatedStore) Exists(ctx context.Context, k string) (bool, error) {
	if !g.open.Load() {
		return false, storage.ErrNoCredential
	}
	return g.Store.Exists(ctx, k)
}
func (g gatedStore) Get(ctx context.Context, k string) ([]byte, error) {
	if !g.open.Load() {
		return nil, storage.ErrNoCredential
	}
	return g.Store.Get(ctx, k)
}
func (g gatedStore) Put(ctx context.Context, k string, d []byte, o ...storage.PutOption) error {
	if !g.open.Load() {
		return storage.ErrNoCredential
	}
	return g.Store.Put(ctx, k, d, o...)
}

// ⭐⭐ A server on a backend that starts on request: it refuses with 503 until
// a request delivers the credential, then starts ONCE — Initialize and the
// identity key, written into the backend rather than the local disk.
func TestAServerOnARequestCredentialedBackendStartsOnTheFirstRequestThatCan(t *testing.T) {
	inner := storage.NewObjectStore("")
	var open atomic.Bool
	var offered atomic.Int32
	s := newTestServer(t)
	s.Mode = mode.RemoteServerSigned
	if err := s.UseBackend(storage.Backend{
		Store: gatedStore{inner, &open},
		FromRequest: func(values func(string) []string) error {
			offered.Add(1)
			if v := values("X-Test-Credential"); len(v) == 1 && v[0] == "ok" {
				open.Store(true)
			}
			return nil
		},
		StartsOnRequest: true,
	}); err != nil {
		t.Fatal(err)
	}
	var reached atomic.Int32
	h := s.backendMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusServiceUnavailable || reached.Load() != 0 {
		t.Fatalf("with no credential: status %d, handler reached %d times; want 503 and 0", w.Code, reached.Load())
	}

	for i := 0; i < 3; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Test-Credential", "ok")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d after the credential: status %d", i, w.Code)
		}
	}
	if offered.Load() != 4 {
		t.Errorf("the credential was offered %d times; every request must offer it", offered.Load())
	}
	if ok, _ := inner.Exists(context.Background(), site.PrivateKeyKey); !ok {
		t.Error("the deferred start did not generate the identity key into the backend")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, site.PrivateKeyKey)); err == nil {
		t.Error("⛔ the identity key was written to the local disk, not the backend")
	}
	if len(s.PrivateKey) == 0 {
		t.Error("the deferred start generated a key but the server did not load it")
	}
}

// ⛔ A backend set after the site is already in use would leave part of the
// server on a disk the instance does not keep.
func TestUseBackendRefusesOnceTheSiteIsInUse(t *testing.T) {
	s := newTestServer(t)
	_ = s.Site()
	if err := s.UseBackend(storage.Backend{Store: storage.NewObjectStore("")}); err == nil {
		t.Error("a backend was accepted after the site had been used")
	}
}

// credentialServer is a server whose backend answers every request's
// credential with fromRequest, on an open store.
func credentialServer(t *testing.T, fromRequest func(func(string) []string) error) (*Server, http.Handler, *atomic.Int32) {
	t.Helper()
	s := newTestServer(t)
	if err := s.UseBackend(storage.Backend{Store: storage.NewObjectStore(""), FromRequest: fromRequest}); err != nil {
		t.Fatal(err)
	}
	var reached atomic.Int32
	return s, s.backendMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) })), &reached
}

// captureLog collects what the standard logger writes during the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(flags) })
	return &buf
}

// ⭐ An issuer the instance cannot reach does not refuse the request: on a
// platform that sends the credential with every request, it used to refuse
// every public read for as long as the outage lasted. Logged by cause, never
// with the token.
func TestAnIssuerOutageDoesNotRefuseTheRequest(t *testing.T) {
	const tok = "eyJ.secret-token-bytes.sig"
	buf := captureLog(t)
	_, h, reached := credentialServer(t, func(values func(string) []string) error {
		if len(values("X-Test-Credential")) == 0 {
			return nil
		}
		return fmt.Errorf("%w: cannot load the issuer's keys: HTTP 500", storage.ErrVerifierUnavailable)
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Test-Credential", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || reached.Load() != 1 {
		t.Fatalf("an issuer outage: status %d, handler reached %d times; want 200 and 1", w.Code, reached.Load())
	}
	if !strings.Contains(buf.String(), "not checked") {
		t.Errorf("the outage was not logged: %q", buf.String())
	}
	if strings.Contains(buf.String(), "secret-token-bytes") {
		t.Error("⛔ the log quotes the token")
	}
}

// ⛔ A credential that was checked and is wrong still refuses the request.
func TestAForgedTokenIsStillRefused(t *testing.T) {
	captureLog(t)
	_, h, reached := credentialServer(t, func(values func(string) []string) error {
		if len(values("X-Test-Credential")) == 0 {
			return nil
		}
		return errors.New("storage: credential not verified: bad signature")
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Test-Credential", "forged")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || reached.Load() != 0 {
		t.Errorf("a forged token: status %d, handler reached %d times; want 400 and 0", w.Code, reached.Load())
	}
}

// ⭐ A POSIX server is untouched: no backend, so the middleware is a no-op.
func TestTheBackendMiddlewareIsANoOpOnAFilesystemSite(t *testing.T) {
	s := newTestServer(t)
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if got := s.backendMiddleware(next); got == nil {
		t.Fatal("nil handler")
	}
	if s.StartsOnRequest() {
		t.Error("a filesystem site claims to start on request")
	}
}
