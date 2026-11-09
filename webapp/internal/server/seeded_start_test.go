package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/deploy"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ A server-signed instance seeded before its first start adopts the seeded
// identity: the first request creates no key and rewrites no
// .well-known/polis, and the instance publishes the seeded key.
func TestASeededInstanceStartsAsTheSeededSite(t *testing.T) {
	t.Setenv("POLIS_BASE_URL", "https://poc.example")
	ctx := context.Background()
	src := storage.At(t.TempDir())
	if _, err := site.Init(src, site.InitOptions{BaseURL: "https://poc.example", SiteTitle: "seeded", Author: "seeded"}); err != nil {
		t.Fatal(err)
	}
	st := storage.NewObjectStore("")
	if err := provision.SeedStore(ctx, st, src); err != nil {
		t.Fatal(err)
	}
	key, _ := st.Get(ctx, site.PrivateKeyKey)
	wk, _ := st.Get(ctx, site.WellKnownKey)

	_, h := newTestNetworkHandler(t, requestStartedBackend(st), networkEnv(nil))
	w := netServe(h, "GET", "/.well-known/polis", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /.well-known/polis on the first start: %d", w.Code)
	}
	var served, seeded struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &served); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(wk, &seeded)
	if served.PublicKey == "" || served.PublicKey != seeded.PublicKey {
		t.Errorf("served public_key = %q, want the seeded %q", served.PublicKey, seeded.PublicKey)
	}
	if now, _ := st.Get(ctx, site.PrivateKeyKey); string(now) != string(key) {
		t.Error("the first start replaced the seeded identity key")
	}
	if now, _ := st.Get(ctx, site.WellKnownKey); string(now) != string(wk) {
		t.Error("the first start rewrote the seeded .well-known/polis")
	}
	if ok, _ := st.Exists(ctx, site.InitPendingKey); ok {
		t.Error("the first start began initialising a seeded site")
	}
}

// seededVercelStore is a store seeded with a rendered site and holding a
// handed-over deploy token, as a seeded Vercel instance finds it.
func seededVercelStore(t *testing.T, withManifest bool) storage.Store {
	t.Helper()
	ctx := context.Background()
	src := storage.At(t.TempDir())
	if _, err := site.Init(src, site.InitOptions{BaseURL: "https://poc.example", SiteTitle: "seeded", Author: "seeded"}); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"index.html": "<p>front</p>", "posts/seeded.html": "<p>SEEDED</p>"} {
		if err := src.Store().Put(ctx, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	st := storage.NewObjectStore("")
	if err := provision.SeedStore(ctx, st, src); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, deployTokenKey, []byte("prj-token")); err != nil {
		t.Fatal(err)
	}
	if withManifest {
		if err := st.Put(ctx, provision.PublicManifestKey, []byte(`{"v":"pub.polis.vercel-public.v1","files":{}}`)); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// ⭐ A seeded store has never been deployed from, so the request that starts
// the instance deploys it, and the seeded pages become static files. A store
// that has been deployed from is not redeployed by a start.
func TestASeededInstanceDeploysItsSiteOnItsFirstStart(t *testing.T) {
	t.Setenv("POLIS_BASE_URL", "https://poc.example")
	for _, deployed := range []bool{false, true} {
		var have []string
		for _, f := range testFunctionManifest().Files {
			have = append(have, f.SHA)
		}
		api, srv := newFakeDeployAPI(t, have...)
		st := seededVercelStore(t, deployed)
		s, h, err := newNetworkHandler(testWebFS, instanceOptions{DataDir: t.TempDir(), Getenv: vercelEnv(nil), Backend: func() *storage.Backend {
			b := requestStartedBackend(st)
			return &b
		}()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		s.deployAPIBase = srv.URL
		s.deployLiveBase = newLiveSite(t, api).URL

		netServe(h, "GET", "/.well-known/polis", nil)
		netServe(h, "GET", "/.well-known/polis", nil)

		api.mu.Lock()
		n := len(api.deployed)
		var static bool
		if n > 0 {
			for _, f := range api.deployed[0] {
				if f.File == ".vercel/output/static/posts/seeded.html" {
					static = true
				}
			}
		}
		api.mu.Unlock()
		switch {
		case !deployed && n != 1:
			t.Errorf("a never-deployed seeded store made %d deployments over two requests; want 1", n)
		case !deployed && !static:
			t.Error("the seeded page is not a static file of the first deployment")
		case deployed && n != 0:
			t.Errorf("a store already deployed from made %d deployments at start", n)
		}
	}
}

// ⭐ An instance that starts while another is deploying the never-deployed
// store (a lease held, or a mark waiting) leaves no mark of its own, so the
// first deploy is the only one. The rehearsal's first start made two
// deployments 2 s apart: a second instance saw no manifest yet and marked.
func TestASecondStartDuringTheFirstDeployMakesNoSecondDeployment(t *testing.T) {
	t.Setenv("POLIS_BASE_URL", "https://poc.example")
	ctx := context.Background()
	for _, inFlight := range []string{"lease", "mark"} {
		t.Run(inFlight, func(t *testing.T) {
			var have []string
			for _, f := range testFunctionManifest().Files {
				have = append(have, f.SHA)
			}
			api, srv := newFakeDeployAPI(t, have...)
			st := seededVercelStore(t, false)
			switch inFlight {
			case "lease":
				doc := fmt.Sprintf(`{"holder":"another-instance","expires":%q}`, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
				if err := st.Put(ctx, deploy.LeaseKey, []byte(doc)); err != nil {
					t.Fatal(err)
				}
			case "mark":
				if err := st.Put(ctx, deploy.DirtyPrefix+"/00000000000000000001-aaaaaaaaaaaa", []byte("{}")); err != nil {
					t.Fatal(err)
				}
			}
			s, h, err := newNetworkHandler(testWebFS, instanceOptions{DataDir: t.TempDir(), Getenv: vercelEnv(nil), Backend: func() *storage.Backend {
				b := requestStartedBackend(st)
				return &b
			}()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			s.deployAPIBase = srv.URL
			s.deployLiveBase = newLiveSite(t, api).URL

			netServe(h, "GET", "/.well-known/polis", nil)

			entries, _ := st.List(ctx, deploy.DirtyPrefix, storage.Recursive())
			marks := 0
			for _, e := range entries {
				if !e.IsDir {
					marks++
				}
			}
			api.mu.Lock()
			n := len(api.deployed)
			api.mu.Unlock()
			switch inFlight {
			case "lease":
				if marks != 0 || n != 0 {
					t.Errorf("with another instance deploying: %d marks left and %d deployments; want 0 and 0", marks, n)
				}
			case "mark":
				// The waiting mark is carried by one deploy, as any write is.
				if n > 1 {
					t.Errorf("with a deploy already waiting, the start made %d deployments; want at most 1", n)
				}
			}
		})
	}
}
