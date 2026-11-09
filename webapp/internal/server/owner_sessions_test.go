package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const testOrigin = "https://alice.example.test"

// begun is a begun ceremony: the options the browser gets and the cookies it
// must return.
type begun struct {
	body    []byte
	cookies []*http.Cookie
}

func postWith(t *testing.T, s *Server, path string, body []byte, h http.HandlerFunc, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	for _, c := range cookies {
		r.AddCookie(c)
	}
	h(w, r)
	return w
}

func signInBegin(t *testing.T, s *Server) begun {
	t.Helper()
	w := postWith(t, s, SignInBeginPath, nil, s.handleSignInBegin)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in begin: %d %s", w.Code, w.Body.String())
	}
	return begun{body: w.Body.Bytes(), cookies: cookiesFrom(w)}
}

func gateResponseWith(t *testing.T, s *Server, path string, cookies ...*http.Cookie) int {
	t.Helper()
	h := s.ownerGateMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// remoteServer is a remote, server-signed instance over dir, as a fresh process
// would build it: nothing in memory but its own cache.
func remoteServer(t *testing.T, dir string, store storage.Store) *Server {
	t.Helper()
	s := &Server{Mode: mode.RemoteServerSigned, DataDir: dir, BaseURL: testOrigin}
	s.ownerStoreOnce.Do(func() {})
	s.ownerStoreV = store
	return s
}

// enrolledPasskeyServer is a remote instance with a passkey enrolled through
// the REAL routes by a software authenticator.
func enrolledPasskeyServer(t *testing.T) (*Server, *virtualAuthenticator) {
	t.Helper()
	dir := t.TempDir()
	s := remoteServer(t, dir, storage.NewSite(dir))
	va := newVirtualAuthenticator(t, testOrigin, "alice.example.test")
	enrolment, _, err := owner.OpenEnrolment(s.instance())
	if err != nil {
		t.Fatal(err)
	}
	ec := &http.Cookie{Name: enrolmentCookiePlain, Value: enrolment}
	b := postWith(t, s, EnrolPathPrefix+"passkey/begin", nil, s.handleEnrol, ec)
	if b.Code != http.StatusOK {
		t.Fatalf("enrol begin: %d %s", b.Code, b.Body.String())
	}
	c := postWith(t, s, EnrolPathPrefix+"passkey/complete", va.register(b.Body.Bytes()), s.handleEnrol,
		append(cookiesFrom(b), ec)...)
	if c.Code != http.StatusOK {
		t.Fatalf("enrol complete: %d %s", c.Code, c.Body.String())
	}
	return s, va
}

// ⭐ The whole passkey ceremony, through the real handlers, with real
// signatures: enrol, sign in, pass the gate.
func TestAPasskeyEnrolsAndSignsInThroughTheRealRoutes(t *testing.T) {
	s, va := enrolledPasskeyServer(t)
	b := signInBegin(t, s)
	w := postWith(t, s, SignInCompletePath, va.assert(b.body), s.handleSignInComplete, b.cookies...)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in complete: %d %s", w.Code, w.Body.String())
	}
	session := cookiesFrom(w)
	if code := gateResponseWith(t, s, "/api/publish", session...); code != http.StatusOK {
		t.Errorf("a signed-in owner was refused by the gate: %d", code)
	}
	// ⛔ The same ceremony and assertion cannot be replayed.
	w = postWith(t, s, SignInCompletePath, va.assert(b.body), s.handleSignInComplete, b.cookies...)
	if w.Code == http.StatusOK {
		t.Error("⛔ a sign-in ceremony completed twice")
	}
}

// ⛔⛔ CRITERION #6: every owner-auth cookie over TLS is __Host-, Secure,
// HttpOnly, SameSite=Strict and short-lived.
func TestOwnerCookiesAreHostPrefixedStrictAndShortLived(t *testing.T) {
	s, va := enrolledPasskeyServer(t)
	tlsReq := func(path string, body []byte, cookies ...*http.Cookie) *http.Request {
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		r.TLS = &tlsMarker
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}
	check := func(what string, w *httptest.ResponseRecorder, max time.Duration) []*http.Cookie {
		t.Helper()
		var set []*http.Cookie
		for _, c := range w.Result().Cookies() {
			if c.MaxAge < 0 {
				continue
			}
			set = append(set, c)
			if !strings.HasPrefix(c.Name, "__Host-") || !c.Secure || !c.HttpOnly ||
				c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
				t.Errorf("%s cookie %s: prefix/Secure/HttpOnly/Strict/Path/Domain wrong: %+v", what, c.Name, c)
			}
			if c.MaxAge <= 0 || time.Duration(c.MaxAge)*time.Second > max {
				t.Errorf("%s cookie %s lives %ds, want at most %v", what, c.Name, c.MaxAge, max)
			}
		}
		if len(set) == 0 {
			t.Errorf("%s set no cookie", what)
		}
		return set
	}
	w := httptest.NewRecorder()
	s.handleSignInBegin(w, tlsReq(SignInBeginPath, nil))
	ceremony := check("sign-in begin", w, 5*time.Minute)
	opts := w.Body.Bytes()
	w = httptest.NewRecorder()
	s.handleSignInComplete(w, tlsReq(SignInCompletePath, va.assert(opts), ceremony...))
	if w.Code != http.StatusOK {
		t.Fatalf("TLS sign-in: %d %s", w.Code, w.Body.String())
	}
	check("sign-in complete", w, 12*time.Hour)

	// The enrolment session from recovery.
	rs, priv := recoveryServer(t)
	ch := issue(t, rs)
	sig, _ := signing.SignContentNS([]byte(ch.Message), priv, owner.RecoveryNamespace)
	check("recovery", postProof(t, rs, ch.Nonce, sig), 15*time.Minute)
}

// ⛔⛔ CRITERION #9: THE SIGN-IN AND RECOVERY ROUTES COST NO STORE OPERATION FOR
// A REQUEST THAT HAS PROVED NOTHING. Every route a stranger can reach is driven
// with nothing, junk, forged cookies and replays, many times; a counting store
// sees zero operations. ⭐ The started process has its key and its cached
// enrolment record in memory, as a real start leaves them.
func TestUnprovenSignInAndRecoveryRequestsCostNoStoreOperation(t *testing.T) {
	s, va := enrolledPasskeyServer(t)
	_, pub, _ := signing.GenerateKeypair()
	rec, _ := owner.NewRecovery(string(pub), "alice.example.test")
	s.Recovery = rec
	owners := signInBegin(t, s)

	counted := &opCountStore{Store: s.ownerStoreV}
	s.ownerStoreV = counted
	s.prepareOwnerAuth()
	counted.reset()

	forged := []*http.Cookie{
		{Name: ceremonyCookiePlain, Value: "e30.AAAA"},
		{Name: signInCookiePlain, Value: "e30.AAAA"},
		{Name: enrolmentCookiePlain, Value: "e30.AAAA"},
	}
	for i := 0; i < 40; i++ {
		ip := fmt.Sprintf("198.51.100.%d:1", i)
		send := func(method, path string, body []byte, h http.HandlerFunc, cookies ...*http.Cookie) {
			r := httptest.NewRequest(method, path, bytes.NewReader(body))
			r.RemoteAddr = ip
			for _, c := range cookies {
				r.AddCookie(c)
			}
			h(httptest.NewRecorder(), r)
		}
		send(http.MethodPost, SignInBeginPath, nil, s.handleSignInBegin)
		send(http.MethodPost, SignInCompletePath, []byte(`{"junk":1}`), s.handleSignInComplete)
		send(http.MethodPost, SignInCompletePath, []byte(`{"junk":1}`), s.handleSignInComplete, forged...)
		// ⭐ A real ceremony cookie with an assertion from the WRONG key.
		stranger := newVirtualAuthenticator(t, testOrigin, "alice.example.test")
		stranger.credID = va.credID
		send(http.MethodPost, SignInCompletePath, stranger.assert(owners.body), s.handleSignInComplete, owners.cookies...)
		send(http.MethodGet, RecoveryChallengePath, nil, s.handleRecoveryChallenge)
		body, _ := json.Marshal(map[string]string{"nonce": "e30.AAAA", "signature": "junk"})
		send(http.MethodPost, RecoveryVerifyPath, body, s.handleRecoveryVerify)
		send(http.MethodPost, EnrolPathPrefix+"passkey/begin", nil, s.handleEnrol, forged...)
		send(http.MethodGet, "/api/publish", nil, s.ownerGateMiddleware(http.NotFoundHandler()).ServeHTTP, forged...)
	}
	if n := counted.total(); n != 0 {
		t.Errorf("⛔ unproven sign-in and recovery requests cost %d store operations: %v", n, counted.ops())
	}

	// ⭐ And past the cache window, a flood costs at most one READ per window
	// per process (the enrolment record refreshing), never a write or a list.
	s.ownerCache().SetTTL(0)
	counted.reset()
	for i := 0; i < 20; i++ {
		postWith(t, s, SignInBeginPath, nil, s.handleSignInBegin)
	}
	for op, n := range counted.byOp() {
		if op != "get" {
			t.Errorf("an expired cache made unproven begins %s the store %d times", op, n)
		}
	}
}

// ⛔⛔ CRITERION #10: NO SMALL SET OF ADDRESSES CAN EXHAUST THE LIMIT. Five
// addresses hammer the sign-in begin far past their share; the owner, from a
// fresh address, still begins. An address over its share spends none of the
// process cap.
func TestAFewAddressesCannotExhaustTheSignInLimit(t *testing.T) {
	for _, route := range []struct {
		name string
		path string
		h    func(*Server) http.HandlerFunc
	}{
		{"sign-in", SignInBeginPath, func(s *Server) http.HandlerFunc { return s.handleSignInBegin }},
		{"recovery", RecoveryChallengePath, func(s *Server) http.HandlerFunc { return s.handleRecoveryChallenge }},
	} {
		s, _ := enrolledPasskeyServer(t)
		_, pub, _ := signing.GenerateKeypair()
		s.Recovery, _ = owner.NewRecovery(string(pub), "alice.example.test")
		limited := 0
		for i := 0; i < 1000; i++ {
			r := httptest.NewRequest(http.MethodPost, route.path, nil)
			r.RemoteAddr = fmt.Sprintf("203.0.113.%d:4000", i%5)
			w := httptest.NewRecorder()
			route.h(s)(w, r)
			if w.Code == http.StatusTooManyRequests {
				limited++
			}
		}
		if limited < 1000-5*attemptsPerAddress {
			t.Errorf("%s: only %d of 1000 flood requests were limited", route.name, limited)
		}
		r := httptest.NewRequest(http.MethodPost, route.path, nil)
		r.RemoteAddr = "192.0.2.77:5000"
		w := httptest.NewRecorder()
		route.h(s)(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("⛔ %s: the owner on a fresh address was refused while five others held the limit: %d %s",
				route.name, w.Code, w.Body.String())
		}
	}
}

// ⭐ The limiter's shape: a share far below the cap, IPv6 counted by /64, and
// the cap itself does engage across many addresses.
func TestTheAttemptLimiterSharesAndCaps(t *testing.T) {
	if attemptsPerProcess < 10*attemptsPerAddress {
		t.Errorf("the per-address share (%d) is not well below the cap (%d)", attemptsPerAddress, attemptsPerProcess)
	}
	l := newAttemptLimiter()
	for i := 0; i < attemptsPerAddress; i++ {
		if !l.allow(fmt.Sprintf("2001:db8:1:2::%x", i+1)) {
			t.Fatal("an address was refused inside its share")
		}
	}
	if l.allow("2001:db8:1:2::ffff") {
		t.Error("⛔ a fresh address in the same /64 was not counted with its neighbours")
	}
	if !l.allow("2001:db8:1:3::1") {
		t.Error("another /64 was refused")
	}
	l = newAttemptLimiter()
	allowed := 0
	for i := 0; i < attemptsPerProcess*2; i++ {
		if l.allow(fmt.Sprintf("10.%d.%d.1", i/250, i%250)) {
			allowed++
		}
	}
	if allowed != attemptsPerProcess {
		t.Errorf("%d attempts allowed across many addresses, cap is %d", allowed, attemptsPerProcess)
	}
}

// ⛔⛔ CRITERION #4: NO PACKAGE-LEVEL OR PROVIDER STATE IS CONSULTED. The passkey
// provider is registered once into a process-global registry, so any field on
// it is process state; it has none. And the owner-auth files declare no
// package-level variables beyond their route tables.
func TestOwnerAuthKeepsNoProcessState(t *testing.T) {
	if n := reflect.TypeOf(PasskeyProvider{}).NumField(); n != 0 {
		t.Errorf("⛔ PasskeyProvider has %d fields; the registry is process-global, so they are process state", n)
	}
	fset := token.NewFileSet()
	for _, f := range []string{"passkey.go", "signin.go", "recovery.go", "enrol.go", "attemptlimit.go"} {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				for _, spec := range gd.Specs {
					for _, id := range spec.(*ast.ValueSpec).Names {
						t.Errorf("%s: package-level variable %q — owner-auth state must be in the store or a sealed cookie", f, id.Name)
					}
				}
			}
		}
	}
}

// opCountStore counts store operations by kind.
type opCountStore struct {
	storage.Store
	mu   sync.Mutex
	seen []string
}

func (c *opCountStore) note(op, key string) {
	c.mu.Lock()
	c.seen = append(c.seen, op+" "+key)
	c.mu.Unlock()
}
func (c *opCountStore) reset() { c.mu.Lock(); c.seen = nil; c.mu.Unlock() }
func (c *opCountStore) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}
func (c *opCountStore) ops() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.seen...)
}
func (c *opCountStore) byOp() map[string]int {
	out := map[string]int{}
	for _, s := range c.ops() {
		op, _, _ := strings.Cut(s, " ")
		out[op]++
	}
	return out
}
func (c *opCountStore) Get(ctx context.Context, key string) ([]byte, error) {
	c.note("get", key)
	return c.Store.Get(ctx, key)
}
func (c *opCountStore) Put(ctx context.Context, key string, data []byte, opts ...storage.PutOption) error {
	c.note("put", key)
	return c.Store.Put(ctx, key, data, opts...)
}
func (c *opCountStore) Exists(ctx context.Context, key string) (bool, error) {
	c.note("exists", key)
	return c.Store.Exists(ctx, key)
}
func (c *opCountStore) Stat(ctx context.Context, key string) (storage.Info, error) {
	c.note("stat", key)
	return c.Store.Stat(ctx, key)
}
func (c *opCountStore) List(ctx context.Context, prefix string, opts ...storage.ListOption) ([]storage.Entry, error) {
	c.note("list", prefix)
	return c.Store.List(ctx, prefix, opts...)
}
func (c *opCountStore) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
	c.note("delete", key)
	return c.Store.Delete(ctx, key, opts...)
}

// ── separate processes ─────────────────────────────────────────────────

const childEnv = "POLIS_OWNER_AUTH_CHILD_DIR"

// TestOwnerAuthChildProcess is not a test: it is the body of a child process
// the cross-process test starts, serving the owner routes over a site
// directory shared with its siblings. It does nothing unless that test set the
// environment.
func TestOwnerAuthChildProcess(t *testing.T) {
	dir := os.Getenv(childEnv)
	if dir == "" {
		t.Skip("only runs as a child of TestOwnerAuthAcrossSeparateProcesses")
	}
	s := remoteServer(t, dir, storage.NewSite(dir))
	rec, err := owner.NewRecovery(os.Getenv("POLIS_OWNER_AUTH_CHILD_KEY"), "alice.example.test")
	if err != nil {
		t.Fatal(err)
	}
	s.Recovery = rec
	// ⭐ No cache window, so a sign-out on one process is visible on the next
	// request to another; the window itself is tested in cli-go.
	s.ownerCache().SetTTL(0)
	s.prepareOwnerAuth()
	mux := http.NewServeMux()
	for path, h := range map[string]http.HandlerFunc{
		SignInBeginPath: s.handleSignInBegin, SignInCompletePath: s.handleSignInComplete, SignOutPath: s.handleSignOut,
		RecoveryChallengePath: s.handleRecoveryChallenge, RecoveryVerifyPath: s.handleRecoveryVerify,
		EnrolPathPrefix: s.handleEnrol, ProvidersPath: s.handleProviders,
		"/api/probe": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
	} {
		mux.HandleFunc(path, h)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("ADDR %s\n", ln.Addr())
	go func() { _ = http.Serve(ln, s.ownerGateMiddleware(mux)) }()
	_, _ = io.Copy(io.Discard, os.Stdin) // until the parent closes our stdin
}

type child struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	base  string
	err   *bytes.Buffer
}

func startChild(t *testing.T, dir, pub string) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnerAuthChildProcess$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), childEnv+"="+dir, "POLIS_OWNER_AUTH_CHILD_KEY="+pub)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{cmd: cmd, stdin: stdin, err: &stderr}
	t.Cleanup(c.stop)
	sc := bufio.NewScanner(stdout)
	deadline := time.After(30 * time.Second)
	found := make(chan string, 1)
	go func() {
		for sc.Scan() {
			if addr, ok := strings.CutPrefix(sc.Text(), "ADDR "); ok {
				found <- addr
			}
		}
	}()
	select {
	case addr := <-found:
		c.base = "http://" + addr
	case <-deadline:
		t.Fatalf("child did not start: %s", stderr.String())
	}
	return c
}

func (c *child) stop() {
	if c.cmd.ProcessState != nil {
		return
	}
	_ = c.stdin.Close()
	done := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
}

// do sends a request to a child and returns the status, body and cookies set.
func (c *child) do(t *testing.T, method, path string, body []byte, cookies ...*http.Cookie) (int, []byte, []*http.Cookie) {
	t.Helper()
	r, _ := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	for _, ck := range cookies {
		r.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v (child stderr: %s)", method, path, err, c.err.String())
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var set []*http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.MaxAge >= 0 && ck.Value != "" {
			set = append(set, ck)
		}
	}
	return resp.StatusCode, out, set
}

// ⛔⛔⛔ CRITERIA #1, #2 AND #4, IN SEPARATE PROCESSES. Two Servers in one test
// process would share any package-level state and prove nothing, so each step's
// begin and completion are served by DIFFERENT OS PROCESSES over one site
// directory: recovery, enrolment and sign-in all complete across processes, a
// recovery proof and a ceremony cannot be replayed on the other process, a
// session survives its process being stopped, and signing out everywhere ends
// it on a process that never saw the sign-out.
func TestOwnerAuthAcrossSeparateProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("starts child processes")
	}
	dir := t.TempDir()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	a, b := startChild(t, dir, string(pub)), startChild(t, dir, string(pub))

	// Recovery: challenge from A, proof to B.
	code, body, _ := a.do(t, http.MethodGet, RecoveryChallengePath, nil)
	if code != http.StatusOK {
		t.Fatalf("challenge on A: %d %s", code, body)
	}
	var ch owner.Challenge
	_ = json.Unmarshal(body, &ch)
	sig, _ := signing.SignContentNS([]byte(ch.Message), priv, owner.RecoveryNamespace)
	proof, _ := json.Marshal(map[string]string{"nonce": ch.Nonce, "signature": sig})
	code, body, enrolment := b.do(t, http.MethodPost, RecoveryVerifyPath, proof)
	if code != http.StatusOK || len(enrolment) == 0 {
		t.Fatalf("⛔ recovery begun on A did not complete on B: %d %s", code, body)
	}
	// ⛔ #2: the same proof, replayed on A, is refused.
	if code, body, _ = a.do(t, http.MethodPost, RecoveryVerifyPath, proof); code != http.StatusUnauthorized {
		t.Errorf("⛔ a recovery nonce was used twice across processes: %d %s", code, body)
	}

	// Enrolment: begin on A, complete on B.
	va := newVirtualAuthenticator(t, testOrigin, "alice.example.test")
	code, opts, ceremony := a.do(t, http.MethodPost, EnrolPathPrefix+"passkey/begin", nil, enrolment...)
	if code != http.StatusOK {
		t.Fatalf("enrol begin on A: %d %s", code, opts)
	}
	reg := va.register(opts)
	if code, body, _ = b.do(t, http.MethodPost, EnrolPathPrefix+"passkey/complete", reg, append(ceremony, enrolment...)...); code != http.StatusOK {
		t.Fatalf("⛔ enrolment begun on A did not complete on B: %d %s", code, body)
	}
	// ⛔ Its replay on A is refused, and the spent enrolment session opens nothing.
	if code, _, _ = a.do(t, http.MethodPost, EnrolPathPrefix+"passkey/complete", reg, append(ceremony, enrolment...)...); code == http.StatusOK {
		t.Error("⛔ an enrolment completed twice across processes")
	}
	if code, _, _ = a.do(t, http.MethodPost, EnrolPathPrefix+"passkey/begin", nil, enrolment...); code == http.StatusOK {
		t.Error("⛔ a spent enrolment session began another enrolment on the other process")
	}

	// Sign-in: begin on B, complete on A.
	code, opts, ceremony = b.do(t, http.MethodPost, SignInBeginPath, nil)
	if code != http.StatusOK {
		t.Fatalf("sign-in begin on B: %d %s", code, opts)
	}
	code, body, session := a.do(t, http.MethodPost, SignInCompletePath, va.assert(opts), ceremony...)
	if code != http.StatusOK || len(session) == 0 {
		t.Fatalf("⛔ sign-in begun on B did not complete on A: %d %s", code, body)
	}
	if code, _, _ = b.do(t, http.MethodGet, "/api/probe", nil, session...); code != http.StatusOK {
		t.Fatalf("⛔ a session opened on A was refused by B: %d", code)
	}

	// #1: the session survives its process stopping, on a process started after.
	a.stop()
	c := startChild(t, dir, string(pub))
	if code, _, _ = c.do(t, http.MethodGet, "/api/probe", nil, session...); code != http.StatusOK {
		t.Fatalf("⛔ a session did not survive a restart: %d", code)
	}
	// And signing out everywhere on C ends it on B, which never saw the sign-out.
	if code, body, _ = c.do(t, http.MethodPost, SignOutPath+"?everywhere=1", nil, session...); code != http.StatusOK {
		t.Fatalf("sign out everywhere on C: %d %s", code, body)
	}
	if code, _, _ = b.do(t, http.MethodGet, "/api/probe", nil, session...); code == http.StatusOK {
		t.Error("⛔ signing out everywhere did not end the session on another process")
	}
}
