package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// countingStore counts reads, so a test can prove an unchanged file is not
// re-read, and can make a listing partial.
type countingStore struct {
	storage.Store
	mu      sync.Mutex
	gets    map[string]int
	listErr error
}

func (c *countingStore) Get(ctx context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	c.gets[key]++
	c.mu.Unlock()
	return c.Store.Get(ctx, key)
}

func (c *countingStore) List(ctx context.Context, prefix string, opts ...storage.ListOption) ([]storage.Entry, error) {
	e, err := c.Store.List(ctx, prefix, opts...)
	if c.listErr != nil {
		return e, c.listErr
	}
	return e, err
}

func (c *countingStore) publicGets() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for k, v := range c.gets {
		if IsPublicKey(k) {
			n += v
		}
	}
	return n
}

var testSite = map[string]string{
	"index.html":                "<h1>home</h1>",
	"posts/20261013/hello.md":   "---\ntitle: hello\n---\nhi",
	"posts/20261013/hello.html": "<p>hi</p>",
	".well-known/polis":         `{"version":"x"}`,
	"public.jsonl":              "{}\n",
	".polis/keys/id_ed25519":    "PRIVATE",
	".env":                      "SECRET=1",
	".polis/deploy/token":       "TOKEN",
}

const publicInTestSite = 5

type deployRig struct {
	f     *fakeVercelAPI
	store *countingStore
	d     *Deployer
	code  Code
}

// newDeployRig is an instance whose function is already on Vercel (PutCode
// ran at provisioning) and whose private store holds testSite.
func newDeployRig(t *testing.T) *deployRig {
	t.Helper()
	ctx := context.Background()
	f := newFakeVercel(t)
	v := f.provisioner(t)
	code := testBuild(t, FormatTarGz, nil)
	if _, err := v.PutCode(ctx, Instance{Name: "alice"}, code); err != nil {
		t.Fatal(err)
	}
	fn, err := FunctionManifestFor(code, "0.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	s := &countingStore{Store: storage.NewObjectStore("alice"), gets: map[string]int{}}
	for k, val := range testSite {
		if err := s.Store.Put(ctx, k, []byte(val)); err != nil {
			t.Fatal(err)
		}
	}
	return &deployRig{f: f, store: s, code: code, d: &Deployer{Store: s, Vercel: v, Project: "alice", Function: fn}}
}

func (r *deployRig) uploads() int {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	n := 0
	for _, c := range r.f.calls {
		if c == "POST /v2/files" {
			n++
		}
	}
	return n
}

func (r *deployRig) deployments() int {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	n := 0
	for _, c := range r.f.calls {
		if c == "POST /v13/deployments" {
			n++
		}
	}
	return n
}

// ⭐⭐ A deploy is the function, by digest, plus exactly the site's public
// tree as static files; nothing private, and the function is not re-sent.
func TestDeployPublishesThePublicTreeBesideTheFunction(t *testing.T) {
	ctx := context.Background()
	r := newDeployRig(t)
	before := r.uploads()
	res, err := r.d.Deploy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID == "" || res.Public != publicInTestSite || res.Hashed != publicInTestSite {
		t.Errorf("result %+v", res)
	}
	got := map[string]string{}
	for _, df := range r.f.deployed {
		got[df.File] = df.SHA
	}
	for _, fn := range r.d.Function.Files {
		if got[fn.File] != fn.SHA {
			t.Errorf("the deployment does not name the function's %s by its digest", fn.File)
		}
	}
	for k, val := range testSite {
		sha, ok := got[".vercel/output/static/"+k]
		if IsPublicKey(k) != ok {
			t.Errorf("%s deployed = %v; public = %v", k, ok, IsPublicKey(k))
		}
		if ok && string(r.f.uploaded[sha]) != val {
			t.Errorf("%s was deployed with other bytes", k)
		}
	}
	if len(got) != publicInTestSite+len(r.d.Function.Files) {
		t.Errorf("the deployment names %d files", len(got))
	}
	// ⛔ Only the pages were uploaded; the function was already there.
	if n := r.uploads() - before; n != publicInTestSite || res.Uploaded != n {
		t.Errorf("%d uploads (result says %d); want only the %d pages", n, res.Uploaded, publicInTestSite)
	}
	for _, k := range []string{".polis/keys/id_ed25519", ".env", ".polis/deploy/token"} {
		for _, data := range r.f.uploaded {
			if string(data) == testSite[k] {
				t.Errorf("%s reached Vercel", k)
			}
		}
	}
}

// ⭐ An unchanged file is neither re-read nor re-sent; a changed one is both,
// alone.
func TestDeployReadsAndSendsOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	r := newDeployRig(t)
	if _, err := r.d.Deploy(ctx); err != nil {
		t.Fatal(err)
	}
	r.store.gets = map[string]int{}
	ups := r.uploads()
	res, err := r.d.Deploy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.store.publicGets() != 0 || res.Hashed != 0 || r.uploads() != ups {
		t.Errorf("an unchanged site: %d public reads, %d hashed, %d uploads", r.store.publicGets(), res.Hashed, r.uploads()-ups)
	}
	if res.Public != publicInTestSite {
		t.Errorf("the redeploy names %d public files", res.Public)
	}

	if err := r.store.Store.Put(ctx, "index.html", []byte("<h1>new home</h1>")); err != nil {
		t.Fatal(err)
	}
	r.store.gets = map[string]int{}
	res, err = r.d.Deploy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.store.gets["index.html"] != 1 || r.store.publicGets() != 1 || res.Hashed != 1 || res.Uploaded != 1 {
		t.Errorf("one changed page: reads %v, %+v", r.store.gets, res)
	}
}

// ⭐ The manifest is derived: lost, the next deploy reads every page once and
// uploads nothing Vercel already has.
func TestDeployRebuildsALostManifest(t *testing.T) {
	ctx := context.Background()
	r := newDeployRig(t)
	if _, err := r.d.Deploy(ctx); err != nil {
		t.Fatal(err)
	}
	for _, lost := range [][]byte{nil, []byte("not json")} {
		if lost == nil {
			_ = r.store.Store.Delete(ctx, PublicManifestKey)
		} else {
			_ = r.store.Store.Put(ctx, PublicManifestKey, lost)
		}
		ups := r.uploads()
		res, err := r.d.Deploy(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if res.Hashed != publicInTestSite || r.uploads() != ups {
			t.Errorf("manifest %q: hashed %d, uploaded %d", lost, res.Hashed, r.uploads()-ups)
		}
		if _, err := r.store.Store.Get(ctx, PublicManifestKey); err != nil {
			t.Error("the manifest was not rewritten")
		}
	}
}

// ⛔ A partial listing would deploy a site missing pages: refused.
func TestDeployRefusesAPartialListing(t *testing.T) {
	r := newDeployRig(t)
	r.store.listErr = errors.New("one prefix unreadable")
	before := r.deployments()
	if _, err := r.d.Deploy(context.Background()); err == nil {
		t.Fatal("a deploy went ahead on a partial listing")
	}
	if r.deployments() != before {
		t.Error("a deployment was created")
	}
}

// ⭐ A function Vercel no longer has is sent from its source, checked against
// the manifest; with no source, or the wrong bytes, the deploy is refused.
func TestDeploySendsAMissingFunctionOnlyFromAMatchingSource(t *testing.T) {
	ctx := context.Background()
	r := newDeployRig(t)
	for _, fn := range r.d.Function.Files {
		delete(r.f.uploaded, fn.SHA)
	}
	if _, err := r.d.Deploy(ctx); err == nil {
		t.Error("a deploy with no function source went ahead though Vercel lacks the function")
	}

	wrong := testBuild(t, FormatTarGz, map[string][]byte{"functions/api/index.func/bootstrap": []byte("other")})
	src, err := FunctionFilesFromCode(wrong)
	if err != nil {
		t.Fatal(err)
	}
	r.d.FunctionFile = func(ctx context.Context, f DeployFile) ([]byte, error) {
		if strings.HasSuffix(f.File, "/bootstrap") {
			return []byte("other"), nil
		}
		return src(ctx, f)
	}
	if _, err := r.d.Deploy(ctx); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("a function file that does not match its manifest: %v", err)
	}

	if r.d.FunctionFile, err = FunctionFilesFromCode(r.code); err != nil {
		t.Fatal(err)
	}
	res, err := r.d.Deploy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range r.d.Function.Files {
		if _, ok := r.f.uploaded[fn.SHA]; !ok {
			t.Errorf("%s was not sent", fn.File)
		}
	}
	if res.ID == "" {
		t.Error("no deployment")
	}
}

// ⛔ A page that changed after it was hashed is not deployed under the old
// digest; the deploy is abandoned and the next one carries it.
func TestDeployAbandonsWhenAPageChangesUnderIt(t *testing.T) {
	ctx := context.Background()
	r := newDeployRig(t)
	if _, err := r.d.Deploy(ctx); err != nil {
		t.Fatal(err)
	}
	// The manifest claims a digest Vercel lacks for a page whose listing has
	// not changed: what a write between hashing and uploading looks like.
	var m publicManifest
	b, _ := r.store.Store.Get(ctx, PublicManifestKey)
	_ = json.Unmarshal(b, &m)
	e := m.Files["index.html"]
	e.SHA = strings.Repeat("0", 40)
	m.Files["index.html"] = e
	b, _ = json.Marshal(m)
	_ = r.store.Store.Put(ctx, PublicManifestKey, b)
	if _, err := r.d.Deploy(ctx); !errors.Is(err, ErrSiteChanged) {
		t.Errorf("err = %v; want ErrSiteChanged", err)
	}
}

// ⛔ A site over Vercel's per-deployment file cap is refused before anything
// is sent.
func TestDeployRefusesASiteOverTheFileCap(t *testing.T) {
	ctx := context.Background()
	r := newDeployRig(t)
	o := storage.NewObjectStore("big")
	for i := 0; i <= MaxDeploymentFiles; i++ {
		_ = o.Put(ctx, "p/"+strings.Repeat("x", 3)+itoa(i)+".html", nil)
	}
	r.d.Store = o
	before := r.deployments()
	if _, err := r.d.Deploy(ctx); err == nil || !strings.Contains(err.Error(), "per deployment") {
		t.Errorf("err = %v", err)
	}
	if r.deployments() != before {
		t.Error("a deployment was created")
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// ⭐ A manifest round-trips, and one naming anything but the function is
// refused.
func TestTheFunctionManifestNamesOnlyTheFunction(t *testing.T) {
	fn, err := FunctionManifestFor(testBuild(t, FormatTarGz, nil), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(fn)
	back, err := ParseFunctionManifest(b)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.Version != "1.2.3" || len(back.Files) != 3 {
		t.Errorf("round trip: %+v", back)
	}
	good := `{"file":".vercel/output/config.json","sha":"` + strings.Repeat("a", 40) + `","size":1,"mode":33188}`
	for name, doc := range map[string]string{
		"a static file":  `{"v":"pub.polis.vercel-function.v1","version":"1","files":[` + good + `,{"file":".vercel/output/static/index.html","sha":"` + strings.Repeat("b", 40) + `","size":1,"mode":33188}]}`,
		"a private name": `{"v":"pub.polis.vercel-function.v1","version":"1","files":[` + good + `,{"file":".vercel/output/functions/.env","sha":"` + strings.Repeat("b", 40) + `","size":1,"mode":33188}]}`,
		"an escape":      `{"v":"pub.polis.vercel-function.v1","version":"1","files":[` + good + `,{"file":".vercel/output/functions/../../x","sha":"` + strings.Repeat("b", 40) + `","size":1,"mode":33188}]}`,
		"no config":      `{"v":"pub.polis.vercel-function.v1","version":"1","files":[]}`,
		"no version":     `{"v":"pub.polis.vercel-function.v1","version":"","files":[` + good + `]}`,
		"another format": `{"v":"pub.polis.vercel-function.v2","version":"1","files":[` + good + `]}`,
		"a bad digest":   `{"v":"pub.polis.vercel-function.v1","version":"1","files":[{"file":".vercel/output/config.json","sha":"zz","size":1,"mode":33188}]}`,
		"a bad mode":     `{"v":"pub.polis.vercel-function.v1","version":"1","files":[{"file":".vercel/output/config.json","sha":"` + strings.Repeat("a", 40) + `","size":1,"mode":511}]}`,
		"an extra field": `{"v":"pub.polis.vercel-function.v1","version":"1","files":[` + good + `],"extra":1}`,
		"a duplicate":    `{"v":"pub.polis.vercel-function.v1","version":"1","files":[` + good + `,` + good + `]}`,
	} {
		if _, err := ParseFunctionManifest([]byte(doc)); err == nil {
			t.Errorf("a manifest with %s was accepted", name)
		}
	}
}

// ⭐ A released instance fetches its own version's manifest and build; a
// manifest for another version, and a development version, are refused.
func TestTheReleaseManifestIsFetchedForTheInstancesOwnVersion(t *testing.T) {
	ctx := context.Background()
	code := testBuild(t, FormatTarGz, nil)
	m, err := FunctionManifestFor(code, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	served := map[string][]byte{}
	mb, _ := json.Marshal(m)
	served["/"+ReleaseRepo+"/releases/download/v1.2.3/"+FunctionManifestAsset] = mb
	served["/"+ReleaseRepo+"/releases/download/v1.2.3/"+FunctionBuildAsset] = code.Archive
	other, _ := FunctionManifestFor(code, "9.9.9")
	ob, _ := json.Marshal(other)
	served["/"+ReleaseRepo+"/releases/download/v1.2.4/"+FunctionManifestAsset] = ob
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := served[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	old := releaseBase
	releaseBase = srv.URL
	defer func() { releaseBase = old }()

	got, err := FetchFunctionManifest(ctx, srv.Client(), "1.2.3")
	if err != nil || got.Version != "1.2.3" {
		t.Fatalf("fetch: %+v %v", got, err)
	}
	if _, err := FetchFunctionManifest(ctx, srv.Client(), "1.2.4"); err == nil {
		t.Error("a manifest for another version was accepted")
	}
	for _, v := range []string{"dev", "", "1.2.3-dirty"} {
		if _, err := FetchFunctionManifest(ctx, srv.Client(), v); err == nil {
			t.Errorf("version %q fetched a manifest", v)
		}
	}
	src := ReleaseFunctionFiles(srv.Client(), "1.2.3")
	for _, f := range got.Files {
		b, err := src(ctx, f)
		if err != nil || sha1Hex(b) != f.SHA {
			t.Errorf("%s from the release: %v", f.File, err)
		}
	}
}

// ⭐ A rate limit is reported as one, with the wait the platform named, and a
// wait in any other form is treated as unnamed.
func TestARateLimitCarriesItsRetryAfter(t *testing.T) {
	for header, want := range map[string]time.Duration{"30": 30 * time.Second, "": 0, "Wed, 21 Oct 2026 07:28:00 GMT": 0, "999999": 24 * time.Hour} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if header != "" {
				w.Header().Set("Retry-After", header)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"slow down"}}`))
		}))
		v, _ := NewVercel(VercelConfig{Token: "t", BaseURL: srv.URL, HTTPClient: srv.Client()})
		_, _, err := v.createDeployment(context.Background(), "alice", nil)
		srv.Close()
		got, limited := RateLimited(err)
		if !limited || got != want {
			t.Errorf("Retry-After %q: %v %v; want %v", header, got, limited, want)
		}
		if strings.Contains(err.Error(), "slow down") {
			t.Error("the error echoes the body")
		}
	}
	if _, limited := RateLimited(errors.New("x")); limited {
		t.Error("an unrelated error read as a rate limit")
	}
}

// ⭐ Missing files are uploaded concurrently, never more than the bound at
// once, and every one is counted. Measured before: 8 files one after another
// took 1.9 s of a 4 s deploy.
func TestDeployUploadsMissingFilesConcurrently(t *testing.T) {
	ctx := context.Background()
	r := newDeployRig(t)
	for i := 0; i < 3*uploadConcurrency; i++ {
		if err := r.store.Store.Put(ctx, fmt.Sprintf("posts/20261017/p%02d.html", i), []byte(fmt.Sprintf("<p>%d</p>", i))); err != nil {
			t.Fatal(err)
		}
	}
	r.f.uploadDelay = 20 * time.Millisecond
	ups := r.uploads()
	res, err := r.d.Deploy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := publicInTestSite + 3*uploadConcurrency
	if n := r.uploads() - ups; res.Uploaded != want || n != want {
		t.Errorf("uploaded %d (result says %d); want %d", n, res.Uploaded, want)
	}
	if peak := r.f.peakUploads.Load(); peak < 2 || peak > uploadConcurrency {
		t.Errorf("at most %d uploads were in flight at once; want between 2 and %d", peak, uploadConcurrency)
	}
}

// ⛔ The first failed upload fails the deploy, the rest stop, and no
// deployment is created from a partial upload.
func TestDeployStopsAtTheFirstFailedUpload(t *testing.T) {
	ctx := context.Background()
	r := newDeployRig(t)
	for i := 0; i < 3*uploadConcurrency; i++ {
		_ = r.store.Store.Put(ctx, fmt.Sprintf("posts/20261017/p%02d.html", i), []byte(fmt.Sprintf("<p>%d</p>", i)))
	}
	r.f.mu.Lock()
	r.f.failNth["POST /v2/files"] = r.f.seen["POST /v2/files"] + 2
	r.f.mu.Unlock()
	before := r.deployments()
	res, err := r.d.Deploy(ctx)
	if err == nil {
		t.Fatal("a deploy with a failed upload succeeded")
	}
	if total := publicInTestSite + 3*uploadConcurrency; res.Uploaded >= total {
		t.Errorf("uploaded %d of %d after a failure; want fewer", res.Uploaded, total)
	}
	// The first create (which learns what's missing) is the only one.
	if n := r.deployments() - before; n != 1 {
		t.Errorf("%d create calls after a failed upload; want only the first", n)
	}
}
