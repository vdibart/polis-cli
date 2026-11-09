package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// requestStartedBackend is a remote backend whose site starts on the first
// request, as on Vercel.
func requestStartedBackend(st storage.Store) storage.Backend {
	return storage.Backend{
		Store:           st,
		FromRequest:     func(func(string) []string) error { return nil },
		StartsOnRequest: true,
	}
}

// ⭐ A server-signed instance on a remote store: the first request makes its
// key and the site around it, and .well-known/polis publishes that key.
func TestARemoteServerSignedFirstStartServesItsWellKnown(t *testing.T) {
	// ⚠️ LoadEnv reads the process environment, not the handler's getenv.
	t.Setenv("POLIS_BASE_URL", "https://poc.example")
	st := storage.NewObjectStore("")
	_, h := newTestNetworkHandler(t, requestStartedBackend(st), networkEnv(nil))

	w := netServe(h, "GET", "/.well-known/polis", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /.well-known/polis on the first start: %d %q, want 200", w.Code, w.Body.String())
	}
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wk); err != nil {
		t.Fatal(err)
	}
	pub, err := st.Get(context.Background(), site.PublicKeyKey)
	if err != nil {
		t.Fatal(err)
	}
	if wk.PublicKey == "" || wk.PublicKey != strings.TrimSpace(string(pub)) {
		t.Errorf("published public_key = %q, want the stored key %q", wk.PublicKey, strings.TrimSpace(string(pub)))
	}
}

// ⛔ No domain, no site: the key is made, the site is not, and the instance
// still serves.
func TestWithoutABaseURLTheSiteIsNotInitialised(t *testing.T) {
	t.Setenv("POLIS_BASE_URL", "")
	st := storage.NewObjectStore("")
	_, h := newTestNetworkHandler(t, requestStartedBackend(st), networkEnv(map[string]string{"POLIS_BASE_URL": ""}))

	if w := netServe(h, "GET", "/", nil); w.Code >= 500 {
		t.Fatalf("GET / with no base URL: %d, want the instance still serving", w.Code)
	}
	if ok, _ := st.Exists(context.Background(), site.WellKnownKey); ok {
		t.Error("a site was initialised with no base URL")
	}
	if ok, _ := st.Exists(context.Background(), site.PrivateKeyKey); !ok {
		t.Error("the identity key was not made")
	}
}

// ⭐ Only a server-signed instance initialises itself.
func TestOnlyServerSignedModeInitialisesItself(t *testing.T) {
	for _, m := range []mode.Mode{mode.Local, mode.RemoteClientSigned, mode.Custodied} {
		t.Run(string(m), func(t *testing.T) {
			s := newTestServer(t)
			s.Mode = m
			s.BaseURL = "https://poc.example"
			s.EnsureSite(context.Background())
			if ok, _ := s.Site().Store().Exists(context.Background(), site.WellKnownKey); ok {
				t.Errorf("a %s instance initialised its own site", m)
			}
		})
	}
}

// ⚠️ A key and part of a site: logged, served, and left exactly as found.
func TestAPartialSiteIsLoggedAndServed(t *testing.T) {
	t.Setenv("POLIS_BASE_URL", "https://poc.example")
	ctx := context.Background()
	st := storage.NewObjectStore("")
	planted := []byte(`{"partial":true}`)
	if err := st.Put(ctx, "content/pub.polis.core/bundle.json", planted); err != nil {
		t.Fatal(err)
	}
	_, h := newTestNetworkHandler(t, requestStartedBackend(st), networkEnv(nil))

	if w := netServe(h, "GET", "/", nil); w.Code >= 500 {
		t.Fatalf("GET / on a partial site: %d, want it served", w.Code)
	}
	if ok, _ := st.Exists(ctx, site.WellKnownKey); ok {
		t.Error("a partial site was re-initialised")
	}
	if got, _ := st.Get(ctx, "content/pub.polis.core/bundle.json"); !bytes.Equal(got, planted) {
		t.Error("the partial site's bundle.json was changed")
	}
}

// failBundlePut fails the bundle.json write: a first start that stops after
// claiming the site.
type failBundlePut struct{ storage.Store }

func (f failBundlePut) Put(ctx context.Context, key string, data []byte, opts ...storage.PutOption) error {
	if key == "content/pub.polis.core/bundle.json" {
		return errors.New("injected failure")
	}
	return f.Store.Put(ctx, key, data, opts...)
}

// ⛔⛔ A first start that stopped after the claim is FINISHED by a later start
// once the claiming one has had time to finish, not left half-built for good.
func TestASiteClaimedAndLeftPartWrittenIsFinishedByALaterStart(t *testing.T) {
	t.Setenv("POLIS_BASE_URL", "https://poc.example")
	ctx := context.Background()
	st := storage.NewObjectStore("")
	broken := storage.On("/site", failBundlePut{st})
	if _, err := site.EnsureIdentityKey(ctx, broken); err != nil {
		t.Fatal(err)
	}
	if _, err := site.Init(broken, site.InitOptions{AdoptExistingKey: true, BaseURL: "https://poc.example"}); err == nil {
		t.Fatal("the injected failure did not stop the first start")
	}
	// The claiming start began long ago.
	if err := st.Put(ctx, site.InitPendingKey, []byte("2020-01-01T00:00:00Z\n")); err != nil {
		t.Fatal(err)
	}

	_, h := newTestNetworkHandler(t, requestStartedBackend(st), networkEnv(nil))
	if w := netServe(h, "GET", "/.well-known/polis", nil); w.Code != http.StatusOK {
		t.Fatalf("GET /.well-known/polis: %d", w.Code)
	}
	if ok, _ := st.Exists(ctx, "content/pub.polis.core/bundle.json"); !ok {
		t.Error("⛔ the part-written site was not finished")
	}
	if ok, _ := st.Exists(ctx, site.InitPendingKey); ok {
		t.Error("the pending marker was left on the finished site")
	}
}

// ⭐ A start that lost the race to initialise ran Initialize before the site
// existed; once the site is there, its configuration is read.
func TestAStartThatLostTheRaceReadsTheSiteConfiguration(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.RemoteServerSigned
	s.BaseURL = "https://poc.example"
	s.siteConfigLoaded = false
	s.EnsureSite(context.Background())
	if !s.siteConfigLoaded {
		t.Error("the configuration of a site another start initialised was never read")
	}
}

// ⭐ A first start renders the site, so the front page the public store serves
// at the instance's domain exists from the start; a site with one is left.
func TestAFirstStartRendersTheFrontPage(t *testing.T) {
	t.Setenv("POLIS_BASE_URL", "https://poc.example")
	ctx := context.Background()
	st := storage.NewObjectStore("")
	_, h := newTestNetworkHandler(t, requestStartedBackend(st), networkEnv(nil))
	if w := netServe(h, "GET", "/.well-known/polis", nil); w.Code != http.StatusOK {
		t.Fatalf("GET /.well-known/polis: %d", w.Code)
	}
	if ok, _ := st.Exists(ctx, "index.html"); !ok {
		t.Error("the first start left the site without a front page")
	}
}
