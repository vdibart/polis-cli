package owner

import (
	"context"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// countStore counts every store operation, so a test can show that a request
// which has proved nothing costs none.
type countStore struct {
	storage.Store
	mu    sync.Mutex
	calls map[string]int
	keys  []string
}

func (c *countStore) note(op, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[op]++
	c.keys = append(c.keys, op+" "+key)
}

func (c *countStore) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.calls {
		n += v
	}
	return n
}

func (c *countStore) Get(ctx context.Context, key string) ([]byte, error) {
	c.note("get", key)
	return c.Store.Get(ctx, key)
}
func (c *countStore) Put(ctx context.Context, key string, data []byte, opts ...storage.PutOption) error {
	c.note("put", key)
	return c.Store.Put(ctx, key, data, opts...)
}
func (c *countStore) Exists(ctx context.Context, key string) (bool, error) {
	c.note("exists", key)
	return c.Store.Exists(ctx, key)
}
func (c *countStore) Stat(ctx context.Context, key string) (storage.Info, error) {
	c.note("stat", key)
	return c.Store.Stat(ctx, key)
}
func (c *countStore) List(ctx context.Context, prefix string, opts ...storage.ListOption) ([]storage.Entry, error) {
	c.note("list", prefix)
	return c.Store.List(ctx, prefix, opts...)
}
func (c *countStore) Delete(ctx context.Context, key string, opts ...storage.DeleteOption) error {
	c.note("delete", key)
	return c.Store.Delete(ctx, key, opts...)
}

// tamper changes one byte of a sealed token's payload and keeps its MAC.
func tamper(tok string) string {
	body, sig, _ := strings.Cut(tok, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	if len(raw) > 0 {
		raw[len(raw)/2] ^= 0x01
	}
	return base64.RawURLEncoding.EncodeToString(raw) + "." + sig
}

// ⛔⛔ CRITERION #1: A SESSION SURVIVES A RESTART. A "restart" is a new process:
// a new cache and nothing else, over the same store. Before, sessions lived in
// a package map and every restart signed the owner out — on Vercel, every few
// idle minutes.
func TestASessionSurvivesARestartAndSigningOutEverywhereStillEndsIt(t *testing.T) {
	inst := remoteInstance(t)
	tok, _, err := OpenSignIn(inst, "passkey")
	if err != nil {
		t.Fatal(err)
	}
	restarted := inst
	restarted.Cache = NewCache()
	if !SignedIn(restarted, tok) {
		t.Fatal("⛔ a restart signed the owner out")
	}
	if !SignedInWithin(restarted, tok, time.Minute) {
		t.Error("the restarted process lost the session's issue time")
	}

	// ⭐ Signing out everywhere on the restarted process ends it on a third,
	// and on the first once its cache expires.
	if err := SignOutAll(restarted); err != nil {
		t.Fatal(err)
	}
	third := inst
	third.Cache = NewCache()
	if SignedIn(third, tok) {
		t.Error("⛔ a session survived signing out everywhere, on a fresh process")
	}
	inst.Cache.SetTTL(0)
	if SignedIn(inst, tok) {
		t.Error("⛔ the first process kept the session once its cache expired")
	}
}

// ⛔⛔ CRITERION #5: THE KEY IS MADE ONCE, IfAbsent, IN THE PRIVATE STORE. Two
// processes starting at once must agree on one key, or each refuses the other's
// sessions. It lives under .polis — never served, and on a remote backend only
// in the private store — and nothing reads it from the environment.
func TestTheMACKeyIsCreatedOnceInThePrivateStore(t *testing.T) {
	base := remoteInstance(t)
	const n = 16
	toks := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inst := base
			inst.Cache = NewCache()
			if err := PrepareSessions(inst); err != nil {
				t.Error(err)
				return
			}
			toks[i], _, _ = OpenSignIn(inst, "passkey")
		}(i)
	}
	wg.Wait()
	for i, tok := range toks {
		check := base
		check.Cache = NewCache()
		if !SignedIn(check, tok) {
			t.Fatalf("process %d minted a session another process refuses: two keys were created", i)
		}
	}
	if !strings.HasPrefix(SessionKeyKey, ".polis/") {
		t.Errorf("the session key %q is outside .polis, so it could be served", SessionKeyKey)
	}
	key, err := base.Store.Get(context.Background(), SessionKeyKey)
	if err != nil || len(key) != 32 {
		t.Fatalf("no 32-byte key at %s: %v", SessionKeyKey, err)
	}
	// ⭐ Mode 0600 on POSIX: the key is not readable by another local account.
	if dir, ok := base.Store.(interface{ RealPath(string) (string, error) }); ok {
		if p, err := dir.RealPath(SessionKeyKey); err == nil {
			if fi, err := os.Stat(p); err == nil && fi.Mode().Perm() != 0o600 {
				t.Errorf("session key mode %v, want 0600", fi.Mode().Perm())
			}
		}
	}
	// ⛔ Never the environment.
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(f)
		if strings.Contains(string(src), "Getenv") {
			t.Errorf("%s reads the environment; owner-auth state must come from the private store", f)
		}
	}
}

// ⛔⛔ CRITERION #5: A TAMPERED TOKEN, COOKIE OR GENERATION IS REFUSED. Each
// token kind is altered by one byte with its MAC kept, and each must fail. A
// token minted for one purpose is presented as another. And an edited or
// corrupt generations file never reads as "generation zero".
func TestATamperedTokenOrGenerationIsRefused(t *testing.T) {
	inst := remoteInstance(t)
	_, pub := keypair(t)
	r, _ := NewRecovery(string(pub), "alice.example.test")

	sess, _, _ := OpenSignIn(inst, "passkey")
	enrol, _, _ := OpenEnrolment(inst)
	cer, _, _ := BeginCeremony(inst, "signin", "", []byte("state"))
	ch, _ := r.Issue(inst)

	if SignedIn(inst, tamper(sess)) {
		t.Error("a tampered session was accepted")
	}
	if _, err := CheckEnrolment(inst, tamper(enrol)); err == nil {
		t.Error("a tampered enrolment session was accepted")
	}
	if _, err := OpenCeremony(inst, "signin", tamper(cer)); err == nil {
		t.Error("a tampered ceremony was accepted")
	}
	if err := r.Verify(inst, tamper(ch.Nonce), "sig"); !errors.Is(err, ErrChallengeUnknown) {
		t.Errorf("a tampered challenge got %v, want ErrChallengeUnknown", err)
	}

	// ⛔ Purposes do not cross.
	if SignedIn(inst, enrol) || SignedIn(inst, cer) {
		t.Error("an enrolment session or a ceremony was accepted as a signed-in session")
	}
	if _, err := CheckEnrolment(inst, sess); err == nil {
		t.Error("a signed-in session was accepted as an enrolment session")
	}
	if _, err := OpenCeremony(inst, "enrol:passkey", cer); err == nil {
		t.Error("a sign-in ceremony was accepted by an enrolment completion")
	}

	// ⛔ A session signed under another site's key is refused here.
	other := remoteInstance(t)
	foreign, _, _ := OpenSignIn(other, "passkey")
	if SignedIn(inst, foreign) {
		t.Error("a session sealed under another site's key was accepted")
	}

	// ⛔ An edited generations file: rolling the counter back is detected by
	// the session it was meant to revive no longer matching... and a corrupt
	// file refuses everything rather than reading as zero.
	if err := SignOutAll(inst); err != nil {
		t.Fatal(err)
	}
	if err := inst.Store.Put(context.Background(), generationsKey, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	fresh := inst
	fresh.Cache = NewCache()
	if SignedIn(fresh, sess) {
		t.Error("⛔ a corrupt generations file revived a signed-out session")
	}
	newer, _, err := OpenSignIn(fresh, "passkey")
	if err == nil && SignedIn(fresh, newer) {
		t.Error("a corrupt generations file was read as valid")
	}
}

// ⛔ CRITERION #7: removing a provider ends its sessions on every process, and
// re-enrolling does not revive them.
func TestRemovingAProviderEndsItsSessionsEverywhere(t *testing.T) {
	inst := remoteInstance(t)
	_ = SaveEnrolment(inst, "passkey", []byte("x"))
	pk, _, _ := OpenSignIn(inst, "passkey")
	other, _, _ := OpenSignIn(inst, "stub")
	if err := DeleteEnrolment(inst, "passkey"); err != nil {
		t.Fatal(err)
	}
	elsewhere := inst
	elsewhere.Cache = NewCache()
	if SignedIn(elsewhere, pk) {
		t.Error("⛔ a session opened by a removed provider survived on another process")
	}
	if !SignedIn(elsewhere, other) {
		t.Error("removing one provider ended another provider's session")
	}
	_ = SaveEnrolment(inst, "passkey", []byte("y"))
	if SignedIn(elsewhere, pk) {
		t.Error("⛔ re-enrolling revived a session from before the removal")
	}
}

// ⛔ CRITERION #7: a ceremony is consumed only by its own completion, once,
// across processes.
func TestACeremonyIsConsumedOnceAcrossProcesses(t *testing.T) {
	inst := remoteInstance(t)
	tok, _, _ := BeginCeremony(inst, "signin", "", []byte("s"))
	a, b := inst, inst
	a.Cache, b.Cache = NewCache(), NewCache()
	ca, err := OpenCeremony(a, "signin", tok)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := OpenCeremony(b, "signin", tok)
	if err != nil {
		t.Fatal(err)
	}
	if err := Consume(a, ca.ID, ca.Expires); err != nil {
		t.Fatal(err)
	}
	if err := Consume(b, cb.ID, cb.Expires); !errors.Is(err, ErrCeremony) {
		t.Errorf("⛔ a ceremony was completed twice across processes: %v", err)
	}
}

// ⛔⛔ CRITERION #9, at the seam: a token that has proved nothing — absent,
// forged, expired — costs no store operation once the process has started.
func TestAnUnprovenTokenCostsNoStoreOperation(t *testing.T) {
	inst := remoteInstance(t)
	if err := PrepareSessions(inst); err != nil {
		t.Fatal(err)
	}
	real, _, _ := OpenSignIn(inst, "passkey")
	cer, _, _ := BeginCeremony(inst, "signin", "", nil)
	enrol, _, _ := OpenEnrolment(inst)
	counted := &countStore{Store: inst.Store}
	inst.Store = counted
	for i := 0; i < 50; i++ {
		_ = SignedIn(inst, "")
		_ = SignedIn(inst, "junk.junk")
		_ = SignedIn(inst, tamper(real))
		_, _ = OpenCeremony(inst, "signin", tamper(cer))
		_, _ = CheckEnrolment(inst, tamper(enrol))
		_, _, _ = BeginCeremony(inst, "signin", "", []byte("x"))
	}
	if n := counted.total(); n != 0 {
		t.Errorf("unproven tokens cost %d store operations: %v", n, counted.keys)
	}
}

// ⭐ Used markers are swept once their tokens have expired.
func TestUsedMarkersAreSweptAfterTheyExpire(t *testing.T) {
	inst := remoteInstance(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	inst.Cache.now = func() time.Time { return now }
	old, _, _ := BeginCeremony(inst, "signin", "", nil)
	c, _ := OpenCeremony(inst, "signin", old)
	if err := Consume(inst, c.ID, c.Expires); err != nil {
		t.Fatal(err)
	}
	now = now.Add(48 * time.Hour)
	tok, _, _ := BeginCeremony(inst, "signin", "", nil)
	c2, _ := OpenCeremony(inst, "signin", tok)
	if err := Consume(inst, c2.ID, c2.Expires); err != nil {
		t.Fatal(err)
	}
	entries, _ := inst.Store.List(context.Background(), usedPrefix)
	var days []string
	for _, e := range entries {
		days = append(days, filepath.Base(e.Key))
	}
	sort.Strings(days)
	if len(days) != 1 || days[0] != "20261003" {
		t.Errorf("marker directories after the sweep: %v, want only 20261003", days)
	}
}

// ⛔⛔ CRITERION #4: NO PACKAGE-LEVEL STATE IS CONSULTED. signIns,
// EnrolmentSessions and Recovery's live map were package or process state, so
// two Servers in one test process shared them and a test could not tell a
// working design from a lucky one. This lists every package-level variable in
// the package and allows only the provider registry and error values.
func TestNoOwnerAuthStateLivesInPackageVariables(t *testing.T) {
	allowed := map[string]bool{"mu": true, "providers": true}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					for _, id := range spec.(*ast.ValueSpec).Names {
						if allowed[id.Name] || strings.HasPrefix(id.Name, "Err") || strings.HasPrefix(id.Name, "err") {
							continue
						}
						t.Errorf("%s: package-level variable %q — owner-auth state must live in the store or a sealed token", name, id.Name)
					}
				}
			}
		}
	}
}
