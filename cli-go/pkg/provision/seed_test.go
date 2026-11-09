package provision

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	secretpkg "github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const seedPostKey = "posts/20261016/hello.md"

// branchedSite is a complete site as a branch leaves it: initialised, with a
// post, the owner's recovery key, and some of the origin instance's deploy
// state that must not cross.
func branchedSite(t *testing.T) storage.Site {
	t.Helper()
	s := storage.At(t.TempDir())
	if _, err := site.Init(s, site.InitOptions{BaseURL: "https://seed.example", SiteTitle: "seed", Author: "seed"}); err != nil {
		t.Fatal(err)
	}
	_, recovery, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for key, body := range map[string]string{
		seedPostKey:                   "# hello\n",
		owner.RecoveryPublicKeyKey:    string(recovery),
		".polis/deploy/token":         "origin-deploy-token",
		".polis/deploy/manifest.json": `{"v":"x"}`,
	} {
		if err := s.Store().Put(ctx, key, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// snapshot is every key of a store and its bytes.
func snapshot(t *testing.T, st storage.Store) map[string]string {
	t.Helper()
	ctx := context.Background()
	entries, err := st.List(ctx, "", storage.Recursive())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		b, err := st.Get(ctx, e.Key)
		if err != nil {
			t.Fatal(err)
		}
		out[e.Key] = string(b)
	}
	return out
}

func diff(a, b map[string]string) []string {
	var d []string
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			d = append(d, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			d = append(d, k)
		}
	}
	sort.Strings(d)
	return d
}

// ⭐ A directory seed carries the site, its private residue and its key, and
// leaves the origin instance's deploy state behind.
func TestSeedingADirectoryCarriesTheSiteAndItsKey(t *testing.T) {
	src := branchedSite(t)
	dir := filepath.Join(t.TempDir(), "instance")
	if err := (Directory{}).Seed(context.Background(), Instance{ID: dir}, src); err != nil {
		t.Fatal(err)
	}
	from, to := snapshot(t, src.Store()), snapshot(t, storage.At(dir).Store())
	for k, v := range from {
		if strings.HasPrefix(k, ".polis/deploy/") && k != owner.RecoveryPublicKeyKey {
			if _, ok := to[k]; ok {
				t.Errorf("%s crossed into the seeded instance; the origin's deploy state must not", k)
			}
			continue
		}
		if to[k] != v {
			t.Errorf("%s did not cross unchanged", k)
		}
	}
	for _, k := range []string{site.PrivateKeyKey, site.WellKnownKey, seedPostKey, owner.RecoveryPublicKeyKey} {
		if _, ok := to[k]; !ok {
			t.Errorf("%s is missing from the seeded instance", k)
		}
	}
	// ⛔ The modes a site store keeps: .polis private, the key 0600.
	if fi, err := os.Stat(filepath.Join(dir, ".polis")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf(".polis mode = %v (%v), want 0700", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(site.PrivateKeyKey))); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("identity key mode = %v (%v), want 0600", fi.Mode().Perm(), err)
	}
}

// ⭐ A seeded instance's first start creates nothing: the identity key takes
// the existing-key path, and the adopting init finds the site and writes
// nothing.
func TestASeededInstanceStartsWithoutCreatingAKey(t *testing.T) {
	src := branchedSite(t)
	dst := storage.On("/seeded", storage.NewObjectStore(""))
	if err := SeedStore(context.Background(), dst.Store(), src); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dst.Store())
	k, err := site.EnsureIdentityKey(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	if k.Created {
		t.Fatal("the seeded instance created an identity key")
	}
	want, _ := src.Store().Get(context.Background(), site.PrivateKeyKey)
	if string(k.Private) != string(want) {
		t.Fatal("the seeded instance does not hold the seeded key")
	}
	_, err = site.Init(dst, site.InitOptions{AdoptExistingKey: true, BaseURL: "https://seed.example"})
	if !errors.Is(err, site.ErrAlreadyInitialised) {
		t.Fatalf("the adopting init on a seeded site = %v, want ErrAlreadyInitialised", err)
	}
	if d := diff(before, snapshot(t, dst.Store())); len(d) > 0 {
		t.Errorf("the first start wrote %v", d)
	}
}

// ⛔ Seeding twice refuses, and the second seed changes nothing.
func TestSeedingTwiceRefuses(t *testing.T) {
	ctx := context.Background()
	dst := storage.NewObjectStore("")
	if err := SeedStore(ctx, dst, branchedSite(t)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dst)
	err := SeedStore(ctx, dst, branchedSite(t))
	if !errors.Is(err, ErrSeedRefused) {
		t.Fatalf("a second seed = %v, want ErrSeedRefused", err)
	}
	if d := diff(before, snapshot(t, dst)); len(d) > 0 {
		t.Errorf("the refused seed wrote %v", d)
	}
}

// ⛔ An instance that already made its own key refuses a seed, and says why.
func TestAnInstanceThatHoldsAKeyRefusesASeed(t *testing.T) {
	ctx := context.Background()
	dst := storage.On("/started", storage.NewObjectStore(""))
	if _, err := site.EnsureIdentityKey(ctx, dst); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dst.Store())
	err := SeedStore(ctx, dst.Store(), branchedSite(t))
	if !errors.Is(err, ErrSeedRefused) || !strings.Contains(err.Error(), site.PrivateKeyKey) {
		t.Fatalf("seed over a key = %v, want ErrSeedRefused naming %s", err, site.PrivateKeyKey)
	}
	if d := diff(before, snapshot(t, dst.Store())); len(d) > 0 {
		t.Errorf("the refused seed wrote %v", d)
	}
}

// failingPut fails every Put after the first n.
type failingPut struct {
	storage.Store
	n, puts int
	max     int64
}

func (f *failingPut) Put(ctx context.Context, key string, data []byte, opts ...storage.PutOption) error {
	f.puts++
	if f.puts > f.n {
		return errors.New("store unavailable")
	}
	return f.Store.Put(ctx, key, data, opts...)
}

func (f *failingPut) Capabilities() storage.Capabilities {
	c := f.Store.Capabilities()
	c.MaxPutBytes = f.max
	return c
}

// ⛔ A seed that fails part way leaves no site publishing a key the store
// lacks: .well-known/polis is written last, so whatever landed publishes
// nothing, and the first start refuses to build a site over it.
func TestAHalfFailedSeedPublishesNoIdentity(t *testing.T) {
	ctx := context.Background()
	for _, n := range []int{0, 1, 3} {
		inner := storage.NewObjectStore("")
		dst := &failingPut{Store: inner, n: n}
		if err := SeedStore(ctx, dst, branchedSite(t)); err == nil {
			t.Fatalf("n=%d: the seed succeeded through a failing store", n)
		}
		if ok, _ := inner.Exists(ctx, site.WellKnownKey); ok {
			t.Errorf("n=%d: a half-failed seed left %s", n, site.WellKnownKey)
		}
		hasKey, _ := inner.Exists(ctx, site.PrivateKeyKey)
		if (n > 0) != hasKey {
			t.Errorf("n=%d: key present = %v; the key is the first write", n, hasKey)
		}
	}
}

// ⛔ A source whose key is not the one it publishes, or a file too large for
// the target, is refused before anything is written.
func TestASeedThatCannotLandWritesNothing(t *testing.T) {
	ctx := context.Background()

	wrong := branchedSite(t)
	other, _, _ := signing.GenerateKeypair()
	if err := wrong.Store().Put(ctx, site.PrivateKeyKey, other); err != nil {
		t.Fatal(err)
	}
	dst := storage.NewObjectStore("")
	if err := SeedStore(ctx, dst, wrong); err == nil || !strings.Contains(err.Error(), "not the key") {
		t.Errorf("a mismatched key = %v, want a refusal", err)
	}
	if got := snapshot(t, dst); len(got) > 0 {
		t.Errorf("a refused seed wrote %d files", len(got))
	}

	small := &failingPut{Store: storage.NewObjectStore(""), n: 1 << 30, max: 8}
	if err := SeedStore(ctx, small, branchedSite(t)); err == nil || !strings.Contains(err.Error(), "nothing is seeded") {
		t.Errorf("a file over the limit = %v, want a refusal", err)
	}
	if small.puts != 0 {
		t.Errorf("a seed over the limit wrote %d files", small.puts)
	}
}

// fakeSeedAPI answers the calls a Vercel seed makes: the project's settings,
// one setting's value, and the store's access. It records every call.
func fakeSeedAPI(t *testing.T, access string, storeStatus int) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v9/projects/prj_1/env":
			_, _ = w.Write([]byte(`{"envs":[{"id":"env_b","key":"BLOB_READ_WRITE_TOKEN"},{"id":"env_o","key":"OTHER"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/prj_1/env/env_b":
			_, _ = w.Write([]byte(`{"value":"vercel_blob_rw_ABCdef123_sekrit"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/storage/stores/store_ABCdef123":
			if storeStatus != 0 {
				w.WriteHeader(storeStatus)
				return
			}
			_, _ = w.Write([]byte(`{"store":{"access":"` + access + `"}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v9/projects/prj_1/env/env_b":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func seedVercel(t *testing.T, base string, st storage.Store) *Vercel {
	t.Helper()
	v, err := NewVercel(VercelConfig{Token: "t", BaseURL: base})
	if err != nil {
		t.Fatal(err)
	}
	v.openSeedStore = func(cred secretpkg.Value) (storage.Store, string, error) {
		if cred.IsZero() {
			return nil, "", errors.New("no credential")
		}
		return st, "store_ABCdef123", nil
	}
	return v
}

// ⭐ A Vercel seed reads the connected store's token, checks the store is
// private, writes the site into it, and then deletes the setting that carried
// the token, so no deployment created afterwards carries it.
func TestAVercelSeedWritesTheStoreAndDeletesTheTokenSetting(t *testing.T) {
	srv, calls := fakeSeedAPI(t, "private", 0)
	st := storage.NewObjectStore("")
	if err := seedVercel(t, srv.URL, st).Seed(context.Background(), Instance{ID: "prj_1"}, branchedSite(t)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.Exists(context.Background(), site.WellKnownKey); !ok {
		t.Error("the store was not seeded")
	}
	deleted := false
	for _, c := range *calls {
		if c == "DELETE /v9/projects/prj_1/env/env_b" {
			deleted = true
		}
		if strings.Contains(c, "env_o") {
			t.Errorf("the seed touched an unrelated setting: %s", c)
		}
	}
	if !deleted {
		t.Errorf("the token setting was not deleted; calls = %v", *calls)
	}
}

// ⛔ A store that is not private is never seeded, and no error quotes the token.
func TestAVercelSeedRefusesAStoreThatIsNotPrivate(t *testing.T) {
	for _, tc := range []struct {
		access string
		status int
	}{{"public", 0}, {"", http.StatusInternalServerError}} {
		srv, _ := fakeSeedAPI(t, tc.access, tc.status)
		st := storage.NewObjectStore("")
		err := seedVercel(t, srv.URL, st).Seed(context.Background(), Instance{ID: "prj_1"}, branchedSite(t))
		if err == nil {
			t.Fatalf("access %q status %d: seeded", tc.access, tc.status)
		}
		if strings.Contains(err.Error(), "sekrit") || strings.Contains(err.Error(), "vercel_blob_rw_") {
			t.Errorf("the error quotes the token: %v", err)
		}
		if got := snapshot(t, st); len(got) > 0 {
			t.Errorf("access %q: wrote %d files", tc.access, len(got))
		}
	}
}

// ⛔ A store token naming a store other than the instance's is never seeded.
func TestAVercelSeedRefusesAStoreThatIsNotTheInstances(t *testing.T) {
	srv, _ := fakeSeedAPI(t, "private", 0)
	st := storage.NewObjectStore("")
	err := seedVercel(t, srv.URL, st).Seed(context.Background(), Instance{ID: "prj_1", Store: "store_other"}, branchedSite(t))
	if err == nil || !strings.Contains(err.Error(), "store_other") {
		t.Fatalf("err = %v; want a refusal naming the instance's store", err)
	}
	if got := snapshot(t, st); len(got) > 0 {
		t.Errorf("wrote %d files to a store the instance does not use", len(got))
	}
}
