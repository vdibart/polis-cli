package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/deploy"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
	"github.com/vdibart/polis-cli/cli-go/pkg/secret"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// fakeDeployAPI is the slice of Vercel's API a publish uses: files by digest,
// and deployments that name them.
type fakeDeployAPI struct {
	mu       sync.Mutex
	files    map[string]bool
	deployed [][]provision.DeployFile
	auth     []string
	limited  int // the next N deployment calls are rate limited
	calls    int // deployment calls

	live       []byte // the live site's .well-known/polis; nil serves 404
	liveStatus int    // when set, the live site answers this instead

	settings      map[string]string // project setting id → name
	failEnvDelete bool
}

func newFakeDeployAPI(t *testing.T, have ...string) (*fakeDeployAPI, *httptest.Server) {
	f := &fakeDeployAPI{files: map[string]bool{}, settings: map[string]string{}}
	for _, h := range have {
		f.files[h] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/v2/files":
			b, _ := io.ReadAll(r.Body)
			s := sha1.Sum(b)
			f.files[hex.EncodeToString(s[:])] = true
		case "/v13/deployments":
			f.calls++
			if f.limited > 0 {
				f.limited--
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limited"}}`))
				return
			}
			var body struct {
				Files []provision.DeployFile `json:"files"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			var missing []string
			for _, df := range body.Files {
				if !f.files[df.SHA] {
					missing = append(missing, df.SHA)
				}
			}
			if len(missing) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "missing_files", "missing": missing}})
				return
			}
			f.deployed = append(f.deployed, body.Files)
			_, _ = w.Write([]byte(`{"id":"dpl_test","url":"poc-x.vercel.app"}`))
		case "/v9/projects/poc/env":
			var envs []map[string]string
			for id, k := range f.settings {
				envs = append(envs, map[string]string{"id": id, "key": k})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"envs": envs})
		default:
			if id, ok := strings.CutPrefix(r.URL.Path, "/v9/projects/poc/env/"); ok && r.Method == http.MethodDelete {
				if f.failEnvDelete {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"code":"forbidden"}}`))
					return
				}
				delete(f.settings, id)
				return
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func testFunctionManifest() provision.FunctionManifest {
	sha := func(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }
	return provision.FunctionManifest{V: provision.FunctionManifestV1, Version: "dev-test", Files: []provision.DeployFile{
		{File: ".vercel/output/config.json", SHA: sha("config"), Size: 6, Mode: 0o100644},
		{File: ".vercel/output/functions/api/index.func/.vc-config.json", SHA: sha("vc"), Size: 2, Mode: 0o100644},
		{File: ".vercel/output/functions/api/index.func/bootstrap", SHA: sha("bin"), Size: 3, Mode: 0o100755},
	}}
}

func vercelEnv(extra map[string]string) func(string) string {
	m, _ := json.Marshal(testFunctionManifest())
	kv := map[string]string{
		"VERCEL": "1", vercelProjectEnv: "poc", provision.FunctionManifestEnv: string(m),
	}
	for k, v := range extra {
		kv[k] = v
	}
	return networkEnv(kv)
}

func dirtyMarks(t *testing.T, s storage.Store) int {
	t.Helper()
	e, err := s.List(context.Background(), deploy.DirtyPrefix, storage.Recursive())
	if err != nil {
		t.Fatal(err)
	}
	return len(e)
}

// publishPage writes a page through the instance's own store, as a publish
// does.
func publishPage(t *testing.T, s *Server) {
	t.Helper()
	if err := s.Site().Store().Put(context.Background(), "posts/hello.html", []byte("<p>hello</p>")); err != nil {
		t.Fatal(err)
	}
}

// ⛔ Off Vercel nothing changes: no queue, no marks.
func TestOffVercelThereIsNoDeployQueue(t *testing.T) {
	store := storage.NewObjectStore("")
	s, h := newTestNetworkHandler(t, storage.Backend{Store: store}, networkEnv(nil))
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if s.deployQueue != nil || dirtyMarks(t, store) != 0 {
		t.Error("a deploy queue outside Vercel")
	}
}

// ⭐ On Vercel with no deploy token, the site's writes stand and stay marked:
// the site is behind, never lost.
func TestOnVercelWithNoTokenTheSiteStaysBehind(t *testing.T) {
	store := storage.NewObjectStore("")
	s, h := newTestNetworkHandler(t, storage.Backend{Store: store}, vercelEnv(nil))
	if s.deployQueue == nil {
		t.Fatal("no deploy queue on Vercel")
	}
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if dirtyMarks(t, store) == 0 {
		t.Error("the site's first writes left no mark, so they would never be deployed")
	}
	if err := s.deploySite(context.Background(), store, vercelEnv(nil)); err != errNoDeployToken {
		t.Errorf("deploy with no token: %v", err)
	}
}

// ⭐⭐ On Vercel with a token, the request after a write deploys the site: the
// function by its manifest's digests, the public tree as static files, nothing
// private, and the marks cleared.
func TestOnVercelTheSiteIsDeployedAtTheEndOfTheRequest(t *testing.T) {
	fn := testFunctionManifest()
	var have []string
	for _, f := range fn.Files {
		have = append(have, f.SHA)
	}
	api, srv := newFakeDeployAPI(t, have...)
	store := storage.NewObjectStore("")
	if err := store.Put(context.Background(), deployTokenKey, []byte("prj-token\n")); err != nil {
		t.Fatal(err)
	}
	s, h, err := newNetworkHandler(testWebFS, instanceOptions{DataDir: t.TempDir(), Getenv: vercelEnv(nil), Backend: &storage.Backend{Store: store}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.deployAPIBase = srv.URL
	s.deployLiveBase = newLiveSite(t, api).URL
	publishPage(t, s)
	netServe(h, "GET", "/", nil)

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.deployed) != 1 {
		t.Fatalf("%d deployments; want 1", len(api.deployed))
	}
	names := map[string]bool{}
	for _, f := range api.deployed[0] {
		names[f.File] = true
		if k, ok := strings.CutPrefix(f.File, ".vercel/output/static/"); ok && !provision.IsPublicKey(k) {
			t.Errorf("a private key was deployed: %s", k)
		}
	}
	for _, f := range fn.Files {
		if !names[f.File] {
			t.Errorf("the function's %s is not in the deployment", f.File)
		}
	}
	if !names[".vercel/output/static/posts/hello.html"] {
		t.Error("the published page is not a static file")
	}
	if names[".vercel/output/static/"+deployTokenKey] {
		t.Error("the deploy token was deployed")
	}
	for _, a := range api.auth {
		if a != "Bearer prj-token" {
			t.Errorf("a call was made without the instance's deploy token")
		}
	}
	if n := dirtyMarks(t, store); n != 0 {
		t.Errorf("%d marks remain after the deploy", n)
	}
}

// vercelRig is an instance on Vercel with a token and a fake platform that
// already holds the function.
func vercelRig(t *testing.T) (*Server, http.Handler, storage.Store, *fakeDeployAPI) {
	t.Helper()
	return vercelRigWith(t, nil, true)
}

// vercelRigWith is vercelRig with extra environment, and with or without a
// deploy token already in the store.
func vercelRigWith(t *testing.T, extra map[string]string, stored bool) (*Server, http.Handler, storage.Store, *fakeDeployAPI) {
	t.Helper()
	var have []string
	for _, f := range testFunctionManifest().Files {
		have = append(have, f.SHA)
	}
	api, srv := newFakeDeployAPI(t, have...)
	store := storage.NewObjectStore("")
	if stored {
		if err := store.Put(context.Background(), deployTokenKey, []byte("prj-token")); err != nil {
			t.Fatal(err)
		}
	}
	s, h, err := newNetworkHandler(testWebFS, instanceOptions{DataDir: t.TempDir(), Getenv: vercelEnv(extra), Backend: &storage.Backend{Store: store}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.deployAPIBase = srv.URL
	s.deployLiveBase = newLiveSite(t, api).URL
	return s, h, store, api
}

// newLiveSite is the site readers are served: the .well-known/polis in
// api.live, 404 when that is nil, or api.liveStatus when set.
func newLiveSite(t *testing.T, api *fakeDeployAPI) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		switch {
		case r.URL.Path != "/.well-known/polis":
			http.NotFound(w, r)
		case api.liveStatus != 0:
			w.WriteHeader(api.liveStatus)
		case api.live == nil:
			http.NotFound(w, r)
		default:
			_, _ = w.Write(api.live)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wellKnownWithKey(t *testing.T) []byte {
	t.Helper()
	_, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"version": "0.1", "public_key": string(pub)})
	return b
}

func deployStatusOf(t *testing.T, s *Server) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleDeployStatus(w, httptest.NewRequest(http.MethodGet, "/api/deploy-status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("deploy-status: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// ⭐ A rate limit delays the publish and is respected: the write stays
// waiting, the owner is told why and until when, and the platform is not asked
// again before then.
func TestARateLimitDelaysThePublishAndIsRespected(t *testing.T) {
	s, h, store, api := vercelRig(t)
	api.limited = 1
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if dirtyMarks(t, store) == 0 {
		t.Fatal("a rate-limited deploy dropped its write")
	}
	st := deployStatusOf(t, s)
	if st["state"] != "behind" || st["retry_after"] == nil || !strings.Contains(st["reason"].(string), "429") {
		t.Errorf("status after a rate limit: %v", st)
	}
	calls := api.calls
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if api.calls != calls {
		t.Error("the platform was asked again inside its Retry-After")
	}
	// The wait passes.
	sst := s.readDeployStatus(context.Background(), store)
	past := sst.LastErrorAt.Add(-time.Hour)
	sst.RetryAfter = &past
	b, _ := json.Marshal(sst)
	_ = store.Put(context.Background(), deployStatusKey, b)
	st = deployStatusOf(t, s) // ⭐ the owner's poll deploys what waited
	if st["state"] != "live" || dirtyMarks(t, store) != 0 {
		t.Errorf("after the wait, the poll did not deploy: %v", st)
	}
}

// ⭐ The status says live once deployed, with the deployment and the public
// file count; a site with writes and no token says behind and why.
func TestTheDeployStatusTellsTheOwnerWhereTheSiteIs(t *testing.T) {
	s, h, store, _ := vercelRig(t)
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	st := deployStatusOf(t, s)
	if st["state"] != "live" || st["last_deploy"] == nil || st["public_files"].(float64) < 1 || st["max_files"].(float64) != provision.MaxDeploymentFiles {
		t.Errorf("status after a deploy: %v", st)
	}
	if _, ok := st["token"]; ok {
		t.Error("the status says the token is missing")
	}

	_ = store.Delete(context.Background(), deployTokenKey)
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	st = deployStatusOf(t, s)
	if st["state"] != "behind" || st["token"] != "missing" || st["behind_since"] == nil || !strings.Contains(st["reason"].(string), "no deploy token") {
		t.Errorf("status with no token: %v", st)
	}
	if strings.Contains(fmt.Sprint(st), "prj-token") {
		t.Error("the status names the token")
	}
}

// ⛔ The status is the owner's: refused to anyone else over the network.
func TestTheDeployStatusIsOwnerOnly(t *testing.T) {
	_, h, _, _ := vercelRig(t)
	w := netServe(h, "GET", "/api/deploy-status", nil)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Errorf("deploy-status with no session: %d", w.Code)
	}
}

// Off Vercel the status says publishing is not a deploy here.
func TestTheDeployStatusOffVercel(t *testing.T) {
	s, _ := newTestNetworkHandler(t, storage.Backend{Store: storage.NewObjectStore("")}, networkEnv(nil))
	if st := deployStatusOf(t, s); st["state"] != "not-applicable" {
		t.Errorf("off Vercel: %v", st)
	}
}

// ⛔⛔ A store that is empty or replaced beside a live site never publishes its
// new identity over the live one: the deploy is refused before Vercel is
// asked, the write stays waiting, and the owner is told why.
func TestAReplacedStoreNeverDeploysOverTheLiveIdentity(t *testing.T) {
	s, h, store, api := vercelRig(t)
	api.live = wellKnownWithKey(t)
	if err := store.Put(context.Background(), site.WellKnownKey, wellKnownWithKey(t)); err != nil {
		t.Fatal(err)
	}
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if api.calls != 0 {
		t.Fatalf("⛔ %d deployment calls although the store publishes another identity", api.calls)
	}
	if dirtyMarks(t, store) == 0 {
		t.Error("the refused deploy dropped its write")
	}
	st := deployStatusOf(t, s)
	if reason, _ := st["reason"].(string); st["state"] != "behind" || !strings.Contains(reason, "identity") {
		t.Errorf("status after the refusal: %v", st)
	}
}

// ⭐ The store's own key deploys.
func TestTheStoresOwnIdentityDeploys(t *testing.T) {
	s, h, store, api := vercelRig(t)
	doc := wellKnownWithKey(t)
	api.live = doc
	_ = store.Put(context.Background(), site.WellKnownKey, doc)
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if len(api.deployed) != 1 {
		t.Fatalf("%d deployments, want 1", len(api.deployed))
	}
}

// ⛔ A live site that cannot be read refuses a store that has never published
// its key; ⭐ once this store has deployed its key, an outage of the live site
// does not stop the next publish.
func TestAnUnreadableLiveSiteRefusesOnlyAnIdentityNotPublishedBefore(t *testing.T) {
	s, h, store, api := vercelRig(t)
	doc := wellKnownWithKey(t)
	_ = store.Put(context.Background(), site.WellKnownKey, doc)
	api.liveStatus = http.StatusBadGateway
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if api.calls != 0 {
		t.Fatal("⛔ deployed an identity never published from here while the live site could not be read")
	}

	api.mu.Lock()
	api.liveStatus, api.live = 0, doc
	api.mu.Unlock()
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if len(api.deployed) != 1 {
		t.Fatalf("with the live site readable: %d deployments, want 1", len(api.deployed))
	}

	api.mu.Lock()
	api.liveStatus = http.StatusBadGateway
	api.mu.Unlock()
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if len(api.deployed) != 2 {
		t.Errorf("⛔ a live-site outage stopped a publish of the key this store already deployed (%d deployments)", len(api.deployed))
	}
}

// ⭐⭐ The handover: a token in the write-only setting is moved into the
// private store at the first deploy, used for it, and its setting deleted
// with the token itself.
func TestAHandedOverDeployTokenIsAdoptedAndItsSettingDeleted(t *testing.T) {
	s, h, store, api := vercelRigWith(t, map[string]string{secret.EnvDeployToken: "handed-token"}, false)
	api.settings["env_1"] = secret.EnvDeployToken
	api.settings["env_2"] = "POLIS_MODE"
	if st := deployStatusOf(t, s); st["token"] != "handed-over" {
		t.Errorf("before the first deploy, token = %v; want handed-over", st["token"])
	}
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if got, err := store.Get(context.Background(), deployTokenKey); err != nil || string(got) != "handed-token" {
		t.Fatalf("the stored token is %q (%v); want the handed-over one", got, err)
	}
	if len(api.deployed) != 1 {
		t.Fatalf("%d deployments, want 1", len(api.deployed))
	}
	for _, a := range api.auth {
		if a != "Bearer handed-token" {
			t.Errorf("a call was made with %q", a)
		}
	}
	if len(api.settings) != 1 || api.settings["env_2"] != "POLIS_MODE" {
		t.Errorf("settings left: %v; want only the deploy token's deleted", api.settings)
	}
	st := deployStatusOf(t, s)
	if _, ok := st["token"]; ok || st["token_setting_left"] != nil {
		t.Errorf("status after adoption: %v", st)
	}
}

// ⛔ A token already adopted is never adopted again: a deployment older than
// its setting's deletion must not undo a renewal.
func TestAnAdoptedTokenIsNeverHandedBackOverANewerOne(t *testing.T) {
	s, h, store, _ := vercelRigWith(t, map[string]string{secret.EnvDeployToken: "old-token"}, false)
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	_ = store.Put(context.Background(), deployTokenKey, []byte("renewed-token")) // a later handover
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if got, _ := store.Get(context.Background(), deployTokenKey); string(got) != "renewed-token" {
		t.Errorf("⛔ the stored token became %q; an old handover replaced a renewal", got)
	}
}

// ⚠️ A setting the token cannot delete is reported to the owner; the token is
// adopted and the publish goes ahead.
func TestASettingThatCannotBeDeletedIsReported(t *testing.T) {
	s, h, store, api := vercelRigWith(t, map[string]string{secret.EnvDeployToken: "handed-token"}, false)
	api.settings["env_1"] = secret.EnvDeployToken
	api.failEnvDelete = true
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if ok, _ := store.Exists(context.Background(), deployTokenKey); !ok {
		t.Fatal("the token was not adopted")
	}
	if len(api.deployed) != 1 {
		t.Errorf("%d deployments, want 1", len(api.deployed))
	}
	if st := deployStatusOf(t, s); st["token_setting_left"] != true {
		t.Errorf("status does not say the setting is left: %v", st)
	}
	if strings.Contains(fmt.Sprint(deployStatusOf(t, s)), "handed-token") {
		t.Error("⛔ the status names the token")
	}
}

// releaseStub makes the instance's release fetch answer for version with a
// function whose files the fake API already holds.
func releaseStub(s *Server, api *fakeDeployAPI, version string, fetched *[]string) {
	sha := func(x string) string { h := sha1.Sum([]byte(x)); return hex.EncodeToString(h[:]) }
	m := provision.FunctionManifest{V: provision.FunctionManifestV1, Version: version, Files: []provision.DeployFile{
		{File: ".vercel/output/config.json", SHA: sha("config-" + version), Size: 9, Mode: 0o100644},
		{File: ".vercel/output/functions/api/index.func/.vc-config.json", SHA: sha("vc-" + version), Size: 5, Mode: 0o100644},
		{File: ".vercel/output/functions/api/index.func/bootstrap", SHA: sha("bin-" + version), Size: 6, Mode: 0o100755},
	}}
	api.mu.Lock()
	for _, f := range m.Files {
		api.files[f.SHA] = true
	}
	api.mu.Unlock()
	s.releaseFunctionFor = func(_ context.Context, v string) (provision.FunctionManifest, func(context.Context, provision.DeployFile) ([]byte, error), error) {
		if fetched != nil {
			*fetched = append(*fetched, v)
		}
		if v != version {
			return provision.FunctionManifest{}, nil, fmt.Errorf("no release %s", v)
		}
		return m, nil, nil
	}
}

func deployedFunctionSHA(api *fakeDeployAPI, i int) string {
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, f := range api.deployed[i] {
		if strings.HasSuffix(f.File, "/bootstrap") {
			return f.SHA
		}
	}
	return ""
}

func applyRelease(t *testing.T, s *Server, version string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleDeployApply(w, httptest.NewRequest(http.MethodPost, "/api/deploy/apply", strings.NewReader(`{"version":"`+version+`"}`)))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// ⭐⭐ The owner applies a release: it is deployed with the current pages, and
// every later publish keeps deploying it.
func TestAnAppliedReleaseIsDeployedAndKept(t *testing.T) {
	s, h, store, api := vercelRig(t)
	releaseStub(s, api, "9.9.9", nil)
	publishPage(t, s)
	netServe(h, "GET", "/", nil) // the dev build's function
	devSHA := deployedFunctionSHA(api, 0)

	code, out := applyRelease(t, s, "9.9.9")
	if code != http.StatusOK || out["applied"] != "9.9.9" || out["deployed"] != true {
		t.Fatalf("apply: %d %v", code, out)
	}
	if len(api.deployed) != 2 || deployedFunctionSHA(api, 1) == devSHA {
		t.Fatalf("the apply did not deploy the release's function (%d deployments)", len(api.deployed))
	}
	names := map[string]bool{}
	for _, f := range api.deployed[1] {
		names[f.File] = true
	}
	if !names[".vercel/output/static/posts/hello.html"] {
		t.Error("the apply deployed the function without the site's pages")
	}
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if len(api.deployed) != 3 || deployedFunctionSHA(api, 2) != deployedFunctionSHA(api, 1) {
		t.Error("⛔ a publish after the apply deployed another function than the one applied")
	}
	if st := deployStatusOf(t, s); st["function_version"] != "9.9.9" || st["function_applied"] != true {
		t.Errorf("status after the apply: %v", st)
	}
	if ok, _ := store.Exists(context.Background(), deployFunctionKey); !ok {
		t.Error("the applied release was not recorded")
	}
}

// ⛔ Nothing is recorded for a version that is not a release, or a release
// whose function cannot be fetched: either would leave every later publish
// refusing.
func TestAnApplyThatCannotBeDeployedRecordsNothing(t *testing.T) {
	s, _, store, api := vercelRig(t)
	releaseStub(s, api, "9.9.9", nil)
	for v, want := range map[string]int{"dev-build": http.StatusBadRequest, "": http.StatusBadRequest, "8.8.8": http.StatusBadGateway} {
		if code, _ := applyRelease(t, s, v); code != want {
			t.Errorf("apply %q: %d, want %d", v, code, want)
		}
	}
	if ok, _ := store.Exists(context.Background(), deployFunctionKey); ok {
		t.Error("⛔ a release that cannot be deployed was recorded")
	}
	if len(api.deployed) != 0 {
		t.Error("a refused apply deployed")
	}
}

// ⛔ A record that does not name a release refuses the deploy rather than
// falling back to the instance's own, possibly older, function.
func TestAnUnreadableAppliedReleaseRefusesTheDeploy(t *testing.T) {
	s, h, store, api := vercelRig(t)
	_ = store.Put(context.Background(), deployFunctionKey, []byte(`{"version":"not-a-release"}`))
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if api.calls != 0 {
		t.Error("⛔ deployed although the applied release could not be read")
	}
	if dirtyMarks(t, store) == 0 {
		t.Error("the refused deploy dropped its write")
	}
}

// ⛔ Applying is the owner's alone, and means nothing off Vercel.
func TestApplyIsOwnerOnlyAndOnlyOnVercel(t *testing.T) {
	_, h, _, _ := vercelRig(t)
	if w := netServe(h, "POST", "/api/deploy/apply", map[string]string{"Content-Type": "application/json"}); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Errorf("apply with no session: %d", w.Code)
	}
	off, _ := newTestNetworkHandler(t, storage.Backend{Store: storage.NewObjectStore("")}, networkEnv(nil))
	if code, _ := applyRelease(t, off, "9.9.9"); code != http.StatusConflict {
		t.Errorf("apply off Vercel: %d", code)
	}
}

// ⭐ A write request's time is split in the log between its handler and the
// deploy that followed, with the store calls of each, and the deploy's own
// line splits it by stage. This is what a slow publish is diagnosed from.
func TestAPublishLogsWhereItsTimeWent(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	s, h, _, api := vercelRig(t)
	publishPage(t, s)
	netServe(h, "POST", "/api/anything", nil)

	api.mu.Lock()
	n := len(api.deployed)
	api.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d deployments; want 1", n)
	}
	out := buf.String()
	for _, want := range []string{
		"[i] timing: POST /api/anything request_id=",
		": handler ", "(store: ", "then deploy ",
		"[i] deploy: ", "(list ", ", read ", ", create ", ", upload ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the log does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "prj-token") {
		t.Error("the log carries the deploy token")
	}
}

// ⛔ A release's function is pinned on first use: once the instance accepted a
// manifest for a version, a different one for that version (replaced release
// assets) is refused, at apply and at every later publish.
func TestAReplacedReleaseFunctionIsRefused(t *testing.T) {
	s, h, store, api := vercelRig(t)
	releaseStub(s, api, "9.9.9", nil)
	if code, out := applyRelease(t, s, "9.9.9"); code != http.StatusOK {
		t.Fatalf("apply: %d %v", code, out)
	}
	if ok, _ := store.Exists(context.Background(), functionPinPrefix+"9.9.9"); !ok {
		t.Fatal("applying did not pin the release's function")
	}
	deploys := len(api.deployed)

	// The same version's assets are swapped for a matching pair.
	good := s.releaseFunctionFor
	s.releaseFunctionFor = func(ctx context.Context, v string) (provision.FunctionManifest, func(context.Context, provision.DeployFile) ([]byte, error), error) {
		m, src, err := good(ctx, v)
		m.Files = append([]provision.DeployFile(nil), m.Files...)
		m.Files[2].SHA = strings.Repeat("e", 40)
		return m, src, err
	}
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if len(api.deployed) != deploys {
		t.Fatal("⛔ a publish deployed a replaced release's function")
	}
	if code, out := applyRelease(t, s, "9.9.9"); code == http.StatusOK {
		t.Fatalf("⛔ re-applying a replaced release was accepted: %v", out)
	}

	// The genuine assets still deploy.
	s.releaseFunctionFor = good
	publishPage(t, s)
	netServe(h, "GET", "/", nil)
	if len(api.deployed) != deploys+1 {
		t.Errorf("the genuine release no longer deploys (%d deployments, want %d)", len(api.deployed), deploys+1)
	}
}

// ⛔ For the instance's own version, the running executable is the anchor: a
// manifest naming another bootstrap is refused even on first use.
func TestTheOwnReleaseMustNameTheRunningFunction(t *testing.T) {
	sha := func(x string) string { h := sha1.Sum([]byte(x)); return hex.EncodeToString(h[:]) }
	for _, tc := range []struct {
		running string
		ok      bool
	}{
		{sha("bin-9.9.9"), true},
		{strings.Repeat("e", 40), false},
		{"", true}, // not a deployed function: nothing to compare against
	} {
		s := newConfiguredServer(t)
		s.CLIVersion = "9.9.9"
		releaseStub(s, &fakeDeployAPI{files: map[string]bool{}}, "9.9.9", nil)
		running := tc.running
		s.runningBootstrapSHA = func() (string, error) { return running, nil }
		_, _, err := s.trustedReleaseFunction(context.Background(), s.Site().Store(), "9.9.9", func(string) string { return "" })
		if (err == nil) != tc.ok {
			t.Errorf("running %q: err = %v, want ok=%v", tc.running, err, tc.ok)
		}
	}
}

func TestFunctionManifestFingerprintIgnoresOrderOnly(t *testing.T) {
	a := provision.FunctionManifest{Version: "1.2.3", Files: []provision.DeployFile{{File: "a", SHA: "1"}, {File: "b", SHA: "2"}}}
	b := provision.FunctionManifest{Version: "v1.2.3", Files: []provision.DeployFile{{File: "b", SHA: "2"}, {File: "a", SHA: "1"}}}
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("order or a v prefix changed the fingerprint")
	}
	b.Files[0].Mode = 0o755
	if a.Fingerprint() == b.Fingerprint() {
		t.Error("a mode change did not change the fingerprint")
	}
}
