package server

import (
	"context"
	"errors"
	"go/scanner"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// testWebFS is the smallest web UI the SPA handler serves.
var testWebFS = fstest.MapFS{
	"index.html":    {Data: []byte(`<!doctype html><html {{theme_attr}}><body>polis</body></html>`)},
	"v5/index.html": {Data: []byte(`<!doctype html><html><head><script nonce="{{csp_nonce}}"></script></head><body>polis</body></html>`)},
}

// countingStore counts every call that reaches it.
type countingStore struct {
	storage.Store
	calls *atomic.Int32
}

func (c countingStore) Get(ctx context.Context, k string) ([]byte, error) {
	c.calls.Add(1)
	return c.Store.Get(ctx, k)
}
func (c countingStore) Put(ctx context.Context, k string, d []byte, o ...storage.PutOption) error {
	c.calls.Add(1)
	return c.Store.Put(ctx, k, d, o...)
}
func (c countingStore) Exists(ctx context.Context, k string) (bool, error) {
	c.calls.Add(1)
	return c.Store.Exists(ctx, k)
}
func (c countingStore) Stat(ctx context.Context, k string) (storage.Info, error) {
	c.calls.Add(1)
	return c.Store.Stat(ctx, k)
}
func (c countingStore) List(ctx context.Context, p string, o ...storage.ListOption) ([]storage.Entry, error) {
	c.calls.Add(1)
	return c.Store.List(ctx, p, o...)
}

// networkEnv is the environment of a server-signed instance on a remote store.
func networkEnv(extra map[string]string) func(string) string {
	kv := map[string]string{"POLIS_MODE": "remote-server-signed", "POLIS_BASE_URL": "https://poc.example"}
	for k, v := range extra {
		kv[k] = v
	}
	return envOf(kv)
}

// newTestNetworkHandler builds the network handler on an in-memory store.
func newTestNetworkHandler(t *testing.T, b storage.Backend, getenv func(string) string) (*Server, http.Handler) {
	t.Helper()
	s, h, err := newNetworkHandler(testWebFS, instanceOptions{DataDir: t.TempDir(), Getenv: getenv, Backend: &b})
	if err != nil {
		t.Fatalf("network handler: %v", err)
	}
	t.Cleanup(s.Close)
	return s, h
}

func netServe(h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = "poc.example"
	r.RemoteAddr = "198.51.100.20:4711"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// ⭐ The function serves the instance over the network: pages through the full
// middleware, with CSP and nosniff, to a Host that is not loopback (the
// localhost guard would refuse it with 403).
func TestTheNetworkHandlerServesPagesWithSecurityHeaders(t *testing.T) {
	_, h := newTestNetworkHandler(t, storage.Backend{Store: storage.NewObjectStore("")}, networkEnv(nil))
	w := netServe(h, "GET", "/_/", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "polis") {
		t.Fatalf("GET /_/ over the network: %d %q, want 200 and the app", w.Code, w.Body.String())
	}
	if csp := w.Header().Get("Content-Security-Policy"); csp == "" {
		t.Error("no Content-Security-Policy on a page served through the network handler")
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if w.Header().Get("X-Request-Id") == "" {
		t.Error("no X-Request-Id: the request logging middleware is not on the network path")
	}
}

// ⛔ Nothing on the network path is loopback-trusted: an owner route with no
// session is refused 401, where a loopback server would serve it.
func TestTheNetworkHandlerIsNeverLoopbackTrusted(t *testing.T) {
	s, h := newTestNetworkHandler(t, storage.Backend{Store: storage.NewObjectStore("")}, networkEnv(nil))
	if s.BoundToLoopback || s.instance().Loopback {
		t.Fatal("network handler: BoundToLoopback is true")
	}
	if s.Mode != mode.RemoteServerSigned {
		t.Fatalf("mode = %q, want remote-server-signed", s.Mode)
	}
	// 401 with a provider enrolled, 403 before one is: either way, refused.
	w := netServe(h, "GET", "/api/settings", nil)
	if (w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden) || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("owner route with no session over the network: %d %q, want an owner refusal", w.Code, w.Body.String())
	}
}

// ⛔ Local mode is refused on the network path whatever the backend: unset
// (no laptop to mean) and explicit alike.
func TestTheNetworkHandlerRefusesLocalMode(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"unset":    {"POLIS_MODE": ""},
		"explicit": {"POLIS_MODE": "local"},
	} {
		_, _, err := newNetworkHandler(testWebFS, instanceOptions{
			DataDir: t.TempDir(), Getenv: networkEnv(env), Backend: &storage.Backend{Store: storage.NewObjectStore("")},
		})
		// ⭐ The network path's own refusal, not only the remote-backend rule
		// in checkModeForBackend, which a remote store also trips today.
		if err == nil || !strings.Contains(err.Error(), "POLIS_MODE") || !strings.Contains(err.Error(), "served over the network") {
			t.Errorf("%s local mode: err = %v, want the network path's refusal naming POLIS_MODE", name, err)
		}
	}
}

// ⛔ A function's disk does not survive it, so the filesystem backend is refused.
func TestTheNetworkHandlerRefusesTheFilesystemBackend(t *testing.T) {
	_, _, err := newNetworkHandler(testWebFS, instanceOptions{DataDir: t.TempDir(), Getenv: networkEnv(nil)})
	if err == nil || !strings.Contains(err.Error(), storage.EnvStorageKind) {
		t.Fatalf("network handler on the filesystem: err = %v, want a refusal naming %s", err, storage.EnvStorageKind)
	}
}

// ⛔ The cross-site refusal holds on the network path: a state-changing
// request from another site's page is refused before anyone asks who sent it.
func TestTheNetworkHandlerRefusesACrossSiteWrite(t *testing.T) {
	_, h := newTestNetworkHandler(t, storage.Backend{Store: storage.NewObjectStore("")}, networkEnv(nil))
	w := netServe(h, "POST", "/api/publish", map[string]string{"Origin": "https://evil.example"})
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "cross-site") {
		t.Fatalf("cross-site POST over the network: %d %q, want 403 cross-site", w.Code, w.Body.String())
	}
}

// ⛔ A credential that does not verify is refused before any store call.
func TestAnUnverifiableCredentialReachesNoStoreOnTheNetworkPath(t *testing.T) {
	var calls atomic.Int32
	b := storage.Backend{
		Store: countingStore{storage.NewObjectStore(""), &calls},
		FromRequest: func(func(string) []string) error {
			return errors.New("storage: credential not verified: bad signature")
		},
		StartsOnRequest: true,
	}
	_, h := newTestNetworkHandler(t, b, networkEnv(nil))
	calls.Store(0)
	w := netServe(h, "GET", "/", map[string]string{"x-vercel-oidc-token": "forged"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("forged credential: %d, want 400", w.Code)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("forged credential: %d store calls, want 0", n)
	}
}

// ⭐ /v1 is mounted on a backend that starts on request, built after the start.
func TestV1IsMountedWhenTheSiteStartsOnRequest(t *testing.T) {
	b := storage.Backend{
		Store:           storage.NewObjectStore(""),
		FromRequest:     func(func(string) []string) error { return nil },
		StartsOnRequest: true,
	}
	_, h := newTestNetworkHandler(t, b, networkEnv(nil))
	w := netServe(h, "GET", "/v1/bundles", nil)
	if w.Code == http.StatusNotFound || w.Code >= 500 {
		t.Fatalf("GET /v1/bundles on a request-started backend: %d %q, want it served", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "pub.polis.core") {
		t.Fatalf("GET /v1/bundles: %q, want the core bundle", w.Body.String())
	}
}

// ⛔ Nothing on the network path writes the local disk: construction, a page,
// an owner route and a key-reload check leave the data directory empty.
func TestTheNetworkPathWritesNothingToLocalDisk(t *testing.T) {
	// ⚠️ Writable on purpose: a read-only directory would make a stray write
	// fail silently instead of showing up here.
	dataDir := filepath.Join(t.TempDir(), "site")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	b := storage.Backend{
		Store:           storage.NewObjectStore(""),
		FromRequest:     func(func(string) []string) error { return nil },
		StartsOnRequest: true,
	}
	s, h, err := newNetworkHandler(testWebFS, instanceOptions{DataDir: dataDir, Getenv: networkEnv(nil), Backend: &b})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	netServe(h, "GET", "/", nil)
	netServe(h, "GET", "/api/settings", nil)
	s.ReloadKeysIfChanged()
	s.LogInfo("a line that must not reach a file")
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the network path wrote %d entries to local disk: %v", len(entries), entries)
	}
	if s.PrivateKey == nil {
		t.Fatal("the deferred start did not run: no key loaded")
	}
}

// ⭐ A stdout logger writes no file and keeps no directory.
func TestAStdoutLoggerWritesNoFile(t *testing.T) {
	l := NewStdoutLogger(LogLevelBasic)
	l.logsDir = t.TempDir()
	l.retentionDays = 1
	l.Info("hello")
	l.Event("pub.polis.test", map[string]interface{}{"k": 1})
	l.Request("GET", "/", 200, time.Millisecond, "id")
	l.PruneLogs()
	if entries, _ := os.ReadDir(l.logsDir); len(entries) != 0 {
		t.Fatalf("stdout logger wrote %d files", len(entries))
	}
	if l.file != nil {
		t.Fatal("stdout logger opened a file")
	}
}

// ⛔ Behind Vercel the client IP is X-Real-IP, which Vercel overwrites. A
// client-sent Fly-Client-IP or X-Forwarded-For changes nothing.
func TestBehindVercelTheClientIPIsTheHeaderVercelSets(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Real-IP", "203.0.113.5")
	r.Header.Set("Fly-Client-IP", "198.51.100.66")
	r.Header.Set("X-Forwarded-For", "198.51.100.77")
	if got := remoteIP(r, ProxyVercel); got != "203.0.113.5" {
		t.Fatalf("behind Vercel: client IP %q, want X-Real-IP 203.0.113.5", got)
	}
	r.Header.Del("X-Real-IP")
	if got := remoteIP(r, ProxyVercel); got != "10.0.0.1" {
		t.Fatalf("behind Vercel with no X-Real-IP: client IP %q, want the peer, never a client-sent header", got)
	}
	s, _ := newTestNetworkHandler(t, storage.Backend{Store: storage.NewObjectStore("")}, networkEnv(nil))
	if s.Proxy != ProxyVercel {
		t.Fatalf("network handler proxy = %v, want ProxyVercel", s.Proxy)
	}
}

// staleTimesStore reports ModTimes false, as a store with no change signal would.
type noModTimesStore struct{ storage.Store }

func (noModTimesStore) Capabilities() storage.Capabilities {
	return storage.Capabilities{ModTimes: false}
}

// ⭐ On a remote store the key is stamped through the store, so a key changed
// from outside is picked up; checks are at most one per interval; a store with
// no modification times gets no reload rather than a wrong one.
func TestKeyReloadOnARemoteStoreUsesTheStore(t *testing.T) {
	st := storage.NewObjectStore("")
	s := NewServer(t.TempDir(), "")
	if err := s.UseBackend(storage.Backend{Store: st}); err != nil {
		t.Fatal(err)
	}
	s.Mode = mode.RemoteServerSigned
	if err := s.EnsureIdentityKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := string(s.PublicKey)
	if s.keyReload.loaded == (keyStamp{}) {
		t.Fatal("the key was not stamped through the store")
	}
	if s.ReloadKeysIfChanged() {
		t.Fatal("reloaded with no change")
	}
	// Replace the key from outside, then let the interval pass.
	ctx := context.Background()
	priv, _ := st.Get(ctx, site.PrivateKeyKey)
	time.Sleep(5 * time.Millisecond)
	_ = st.Put(ctx, site.PrivateKeyKey, append(append([]byte{}, priv...), '\n'))
	if s.ReloadKeysIfChanged() {
		t.Fatal("reloaded inside the check interval")
	}
	s.keyReload.mu.Lock()
	s.keyReload.lastCheck = time.Now().Add(-remoteKeyCheckInterval)
	s.keyReload.mu.Unlock()
	if !s.ReloadKeysIfChanged() {
		t.Fatal("a key changed in the store was not reloaded")
	}
	_ = first

	n := NewServer(t.TempDir(), "")
	if err := n.UseBackend(storage.Backend{Store: noModTimesStore{storage.NewObjectStore("")}}); err != nil {
		t.Fatal(err)
	}
	if n.keyCheckDue() {
		t.Fatal("a store with no modification times was checked for a changed key")
	}
}

// ⛔ The function path must contain nothing a loopback listener has: no bind,
// no port, no browser, no localhost guard, no loopback trust. Read from the
// tokens, so a comment that names them does not count.
func TestTheFunctionPathHasNoLocalhostOnlyCode(t *testing.T) {
	files := []string{"instance.go"}
	for _, dir := range []string{"../../vercelentry", "../../api"} {
		m, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range m {
			if !strings.HasSuffix(f, "_test.go") {
				files = append(files, f)
			}
		}
	}
	if len(files) < 3 {
		t.Fatalf("expected instance.go, the vercelentry package and the api function; found %v", files)
	}
	forbiddenIdents := map[string]bool{
		"OpenBrowser": true, "localhostModeHandler": true, "localhostGuard": true,
		"ListenAndServe": true, "FindAvailablePort": true,
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var sc scanner.Scanner
		fset := token.NewFileSet()
		sc.Init(fset.AddFile(f, -1, len(src)), src, nil, 0)
		var prev []string
		for {
			pos, tok, lit := sc.Scan()
			if tok == token.EOF {
				break
			}
			where := fset.Position(pos)
			switch {
			case tok == token.IDENT && forbiddenIdents[lit]:
				t.Errorf("%s: %s on the function path", where, lit)
			case tok == token.STRING && (strings.Contains(lit, "localhost:") || lit == `"PORT"`):
				t.Errorf("%s: %s on the function path", where, lit)
			}
			s := lit
			if s == "" {
				s = tok.String()
			}
			prev = append(prev, s)
			if n := len(prev); n >= 3 && prev[n-3] == "BoundToLoopback" && prev[n-2] == "=" && prev[n-1] == "true" {
				t.Errorf("%s: BoundToLoopback = true on the function path", where)
			}
		}
	}
}

// ⛔ The request path reads r.URL.Path and RawQuery, never RequestURI: behind
// Vercel's rewrite RequestURI arrives decoded and without its query.
func TestNoHandlerReadsRequestURI(t *testing.T) {
	for _, dir := range []string{".", "../api", "../../vercelentry"} {
		m, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range m {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var sc scanner.Scanner
			fset := token.NewFileSet()
			sc.Init(fset.AddFile(f, -1, len(src)), src, nil, 0)
			for {
				pos, tok, lit := sc.Scan()
				if tok == token.EOF {
					break
				}
				if tok == token.IDENT && lit == "RequestURI" {
					t.Errorf("%s: RequestURI read; use r.URL.Path and r.URL.RawQuery", fset.Position(pos))
				}
			}
		}
	}
}
