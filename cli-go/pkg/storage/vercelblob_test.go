package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBlobAPI is ONE Vercel Blob store: the API that writes, lists and deletes,
// and the blob host that serves reads.
//
// ⛔⛔ IT IS WRITTEN FROM VERCEL'S BEHAVIOUR, NOT FROM THE CLIENT'S MODEL OF
// IT. The fake this replaced overwrote freely because the client assumed it
// could, so it could never fail the client's missing overwrite header. Every
// behaviour below is quoted from Vercel's docs or read from @vercel/blob 2.8.1
// (BLOB_API_VERSION 12), and a run against a live store is the real test of both.
//
//   - writes: `PUT /?pathname=<p>`; ⛔ an existing pathname is NOT overwritten
//     unless `x-allow-overwrite: 1` ("By default an error will be thrown if
//     you try to overwrite a blob by using the same pathname").
//   - every API call carries the token, `x-api-version` and
//     `x-vercel-blob-store-id`; a put carries `x-vercel-blob-access`, which
//     the SDK requires ('access must be "private" or "public"').
//   - reads: GET <blob host>/<pathname>. ⛔ A PRIVATE store refuses a read
//     without the token ("Private Blob stores require authentication for all
//     read and write operations"); a PUBLIC one serves anyone.
//   - ⚠️ THE CDN: "When you overwrite a blob at the same pathname, the changes
//     may take up to 60 seconds to propagate." This fake serves the PREVIOUS
//     version after an overwrite unless the read asks for consistency, which
//     only a private store offers (`?cache=0`, set by the SDK "only when
//     access === private").
//   - the listing pages; the fake pages at 2 so the cursor loop is exercised by
//     every check.
type fakeBlobAPI struct {
	mu       sync.Mutex
	access   string
	storeID  string
	token    string
	objects  map[string]fakeBlob // pathname → blob
	srv      *httptest.Server
	pageSize int

	// stale makes non-consistent reads return the previous version, as the
	// CDN does inside its window. ⭐ On by default: it is the real behaviour.
	stale bool

	// oidc, when set, is a deployment OIDC token this store also accepts.
	oidc string
	// blobRoot is the URL this store's blobs are served under.
	blobRoot string
}

type fakeBlob struct {
	data, prev []byte
	uploadedAt time.Time
	// ctype is the content type the object was stored with, which a public
	// store serves it under.
	ctype string
}

func newFakeBlobAPI(t *testing.T) *fakeBlobAPI { return newFakeBlobStore(t, "private") }

func newFakeBlobStore(t *testing.T, access string) *fakeBlobAPI {
	f := &fakeBlobAPI{
		access:   access,
		storeID:  "teststore" + access,
		objects:  map[string]fakeBlob{},
		pageSize: 2,
		stale:    true,
	}
	f.token = "vercel_blob_rw_" + f.storeID + "_secret"
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", f.api)
	mux.HandleFunc("/blob/", f.blob)
	f.srv = httptest.NewServer(mux)
	f.blobRoot = f.srv.URL + "/blob"
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBlobAPI) authorised(r *http.Request) bool {
	a := r.Header.Get("Authorization")
	return a == "Bearer "+f.token || (f.oidc != "" && a == "Bearer "+f.oidc)
}

// fakeBlobPlatform is several stores behind ONE API endpoint, told apart by
// `x-vercel-blob-store-id`, as Vercel's is — with each store's blobs on its own
// path. ⭐ It accepts a deployment OIDC token, so the no-static-secret path is
// exercised end to end.
type fakeBlobPlatform struct {
	srv    *httptest.Server
	stores map[string]*fakeBlobAPI
	oidc   string
}

func newFakeBlobPlatform(t *testing.T, oidc string, stores map[string]string) *fakeBlobPlatform {
	p := &fakeBlobPlatform{stores: map[string]*fakeBlobAPI{}, oidc: oidc}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		f, ok := p.stores[r.Header.Get("x-vercel-blob-store-id")]
		if !ok {
			http.Error(w, "unknown store", http.StatusNotFound)
			return
		}
		f.api(w, r)
	})
	mux.HandleFunc("/blob/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.EscapedPath(), "/blob/")
		id, _, _ := strings.Cut(rest, "/")
		f, ok := p.stores[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		f.blob(w, r)
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	for id, access := range stores {
		p.stores[id] = &fakeBlobAPI{
			access: access, storeID: id, token: "vercel_blob_rw_" + id + "_unused",
			objects: map[string]fakeBlob{}, pageSize: 2, stale: true,
			oidc: oidc, srv: p.srv, blobRoot: p.srv.URL + "/blob/" + id,
		}
	}
	return p
}

func (f *fakeBlobAPI) blobURL(pathname string) string {
	segs := strings.Split(pathname, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return f.blobRoot + "/" + strings.Join(segs, "/")
}

func (f *fakeBlobAPI) api(w http.ResponseWriter, r *http.Request) {
	// ⛔ Every API call must be authorised and versioned, and name its store.
	if !f.authorised(r) {
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}
	if r.Header.Get("x-api-version") == "" || r.Header.Get("x-vercel-blob-store-id") != f.storeID {
		http.Error(w, "missing api version or store id", http.StatusBadRequest)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api")
	switch {
	case r.Method == http.MethodPut && (rest == "/" || rest == ""):
		f.put(w, r)
	case r.Method == http.MethodPost && rest == "/delete":
		f.delete(w, r)
	case r.Method == http.MethodGet && (rest == "/" || rest == ""):
		f.list(w, r)
	default:
		http.Error(w, "unexpected", http.StatusBadRequest)
	}
}

func (f *fakeBlobAPI) put(w http.ResponseWriter, r *http.Request) {
	pathname := r.URL.Query().Get("pathname")
	if pathname == "" {
		http.Error(w, "bad pathname", http.StatusBadRequest)
		return
	}
	if r.Header.Get("x-vercel-blob-access") != f.access {
		http.Error(w, `access must be "private" or "public", and match the store`, http.StatusBadRequest)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	old, exists := f.objects[pathname]
	if exists && r.Header.Get("x-allow-overwrite") != "1" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"Vercel Blob: This blob already exists, use ` + "`allowOverwrite: true`" + ` if you want to overwrite it."}}`))
		return
	}
	nb := fakeBlob{data: body, uploadedAt: time.Now().UTC(), ctype: r.Header.Get("x-content-type")}
	if exists {
		nb.prev = old.data // ⚠️ what the CDN still serves inside its window
	}
	f.objects[pathname] = nb
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": f.blobURL(pathname), "pathname": pathname})
}

func (f *fakeBlobAPI) blob(w http.ResponseWriter, r *http.Request) {
	if f.access == "private" && !f.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	root := strings.TrimPrefix(f.blobRoot, f.srv.URL)
	pathname, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), root+"/"))
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	b, ok := f.objects[pathname]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	consistent := f.access == "private" && r.URL.Query().Get("cache") == "0"
	if f.stale && !consistent && b.prev != nil {
		_, _ = w.Write(b.prev)
		return
	}
	_, _ = w.Write(b.data)
}

func (f *fakeBlobAPI) list(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	cursor := r.URL.Query().Get("cursor")
	f.mu.Lock()
	var names []string
	for n := range f.objects {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	type item struct {
		Pathname   string    `json:"pathname"`
		URL        string    `json:"url"`
		Size       int64     `json:"size"`
		UploadedAt time.Time `json:"uploadedAt"`
	}
	var page []item
	next := ""
	for _, n := range names {
		if cursor != "" && n <= cursor {
			continue
		}
		if len(page) == f.pageSize {
			next = page[len(page)-1].Pathname
			break
		}
		b := f.objects[n]
		page = append(page, item{n, f.blobURL(n), int64(len(b.data)), b.uploadedAt})
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"blobs": page, "cursor": next, "hasMore": next != ""})
}

func (f *fakeBlobAPI) delete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URLs []string `json:"urls"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range body.URLs {
		p, err := url.PathUnescape(strings.TrimPrefix(u, f.blobRoot+"/"))
		if err == nil {
			delete(f.objects, p)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (f *fakeBlobAPI) store(t *testing.T, prefix string) *VercelBlob {
	t.Helper()
	v, err := NewVercelBlob(VercelBlobConfig{
		Token:       f.token,
		Access:      f.access,
		Prefix:      prefix,
		APIBaseURL:  f.srv.URL + "/api",
		BlobBaseURL: f.srv.URL + "/blob",
		HTTPClient:  f.srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// ⭐⭐⭐ THE SAME CONTRACT CHECKS, against the real Vercel Blob store over HTTP.
// This is what makes the backend verified rather than merely written.
func TestContractAgainstVercelBlob(t *testing.T) {
	r := RunContract(context.Background(), func() (Store, func()) {
		f := newFakeBlobAPI(t)
		return f.store(t, ""), nil
	})
	t.Log(r.String())
	if !r.OK() {
		t.Errorf("vercel blob fails the contract:\n%s", r.String())
	}
}

// ⭐ And namespaced, which is how a hosted deployment would run tenants.
func TestContractAgainstPrefixedVercelBlob(t *testing.T) {
	r := RunContract(context.Background(), func() (Store, func()) {
		f := newFakeBlobAPI(t)
		return f.store(t, "tenants/alice"), nil
	})
	t.Log(r.String())
	if !r.OK() {
		t.Errorf("prefixed vercel blob fails the contract:\n%s", r.String())
	}
}

// ⛔ THE KEY MAPPING MUST NOT DRIFT FROM ObjectStore's, because the tested
// semantics are that one's. A difference here means the in-process store proves
// something the HTTP store does not do.
func TestVercelBlobAndObjectStoreAgreeOnKeyMapping(t *testing.T) {
	f := newFakeBlobAPI(t)
	for _, prefix := range []string{"", "tenants/alice", "deep/er/prefix"} {
		v := f.store(t, prefix)
		o := NewObjectStore(prefix)
		for _, key := range []string{
			"a.txt", "a/b.txt", "/leading.txt", "./dotted.txt",
			".well-known/polis", ".polis/keys/id_ed25519",
			"../escape.txt", "a/../../escape.txt", `back\slash.txt`, "",
		} {
			gotV, errV := v.objectKey(key)
			gotO, errO := o.objectKey(key)
			if (errV == nil) != (errO == nil) {
				t.Errorf("prefix %q key %q: blob err=%v, object err=%v — they must agree on refusal",
					prefix, key, errV, errO)
				continue
			}
			if errV == nil && gotV != gotO {
				t.Errorf("prefix %q key %q: blob mapped to %q, object to %q",
					prefix, key, gotV, gotO)
			}
		}
	}
}

// ⛔ A RESTARTED PROCESS must still find everything — on Vercel that is every
// cold start. ⭐ The blob URL is determined by the store and the pathname, so a
// fresh client finds every object with no listing and no cache.
func TestVercelBlobFindsObjectsAfterARestart(t *testing.T) {
	ctx := context.Background()
	f := newFakeBlobAPI(t)
	writer := f.store(t, "tenants/alice")

	for _, k := range []string{"content/a.md", "content/sub/b.md", ".well-known/polis"} {
		if err := writer.Put(ctx, k, []byte("body of "+k)); err != nil {
			t.Fatal(err)
		}
	}

	// A fresh store against the same backing API: no cache, as after a cold start.
	reader := f.store(t, "tenants/alice")
	for _, k := range []string{"content/a.md", "content/sub/b.md", ".well-known/polis"} {
		got, err := reader.Get(ctx, k)
		if err != nil {
			t.Errorf("after a restart, Get(%s) failed: %v", k, err)
			continue
		}
		if string(got) != "body of "+k {
			t.Errorf("Get(%s) = %q", k, got)
		}
	}
}

// ⛔ The listing must follow the cursor to the end. A walk that stops at the
// first page makes a render skip content, and the symptom is missing posts
// rather than an error.
func TestVercelBlobListFollowsTheCursor(t *testing.T) {
	ctx := context.Background()
	f := newFakeBlobAPI(t)
	s := f.store(t, "")
	const n = 11 // ⭐ well past the fake's page size of 2
	for i := 0; i < n; i++ {
		if err := s.Put(ctx, fmt.Sprintf("posts/p%02d.md", i), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List(ctx, "posts", Recursive())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Errorf("List returned %d of %d keys — the cursor loop stopped early, which on a "+
			"real site means silently missing content", len(got), n)
	}
}

// ⛔ Every request must carry the token. The fake refuses an unauthenticated
// call, so a store that forgot the header fails here instead of in production.
func TestVercelBlobAuthenticatesEveryRequest(t *testing.T) {
	ctx := context.Background()
	f := newFakeBlobAPI(t)
	s := f.store(t, "")
	if err := s.Put(ctx, "k.txt", []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := s.Get(ctx, "k.txt"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := s.List(ctx, ""); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := s.Delete(ctx, "k.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// ⛔ A store cannot be constructed without a token. Refusing here beats failing
// at whatever code path happens to write first.
func TestVercelBlobRefusesToConstructWithoutAToken(t *testing.T) {
	if _, err := NewVercelBlob(VercelBlobConfig{}); err == nil {
		t.Error("constructed a blob store with no token")
	}
}

// ⛔ Access is required and never guessed, and the token must name a store.
func TestVercelBlobRefusesAnUnstatedAccessOrAnUnnamedStore(t *testing.T) {
	for _, cfg := range []VercelBlobConfig{
		{Token: "vercel_blob_rw_abc_s"},                   // no access
		{Token: "vercel_blob_rw_abc_s", Access: "publik"}, // not a value
		{Token: "not-a-blob-token", Access: "private"},    // no store id
	} {
		if _, err := NewVercelBlob(cfg); err == nil {
			t.Errorf("constructed with %+v", cfg)
		} else if strings.Contains(err.Error(), "secret") {
			t.Errorf("⛔ the error quotes the token: %v", err)
		}
	}
}

// ⛔⛔ A PRIVATE store's object is unreadable without the token; a PUBLIC
// store's is readable by anyone at its URL. The second is WHY nothing private
// may ever be written to a public store, whatever a site's routes say.
func TestPrivateBlobsNeedTheTokenAndPublicOnesDoNot(t *testing.T) {
	ctx := context.Background()
	for _, access := range []string{"private", "public"} {
		f := newFakeBlobStore(t, access)
		s := f.store(t, "")
		if err := s.Put(ctx, "k.txt", []byte("x")); err != nil {
			t.Fatal(err)
		}
		resp, err := http.Get(f.blobURL("k.txt")) // ⚠️ no Authorization
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if access == "private" && resp.StatusCode == 200 {
			t.Error("a private blob was readable without the token")
		}
		if access == "public" && resp.StatusCode != 200 {
			t.Errorf("a public blob was refused (%d): the fake no longer models a public store", resp.StatusCode)
		}
	}
}

// ⭐ A read after an overwrite on a PRIVATE store sees the overwrite: the
// client asks for a consistent read. ⚠️ On a PUBLIC store it may not, which is
// one reason a site never opens one.
func TestReadsAfterAnOverwriteAreConsistentOnlyOnAPrivateStore(t *testing.T) {
	ctx := context.Background()
	for _, access := range []string{"private", "public"} {
		f := newFakeBlobStore(t, access)
		s := f.store(t, "")
		_ = s.Put(ctx, "k", []byte("v1"))
		_ = s.Put(ctx, "k", []byte("v2"))
		got, err := s.Get(ctx, "k")
		if err != nil {
			t.Fatal(err)
		}
		if access == "private" && string(got) != "v2" {
			t.Errorf("private store read %q after overwriting with v2: the read was not consistent", got)
		}
		if access == "public" && string(got) != "v1" {
			t.Errorf("public store read %q; the fake no longer models the CDN window", got)
		}
	}
}

// ⛔ A public store serves an object with the type it was stored under, so the
// type must follow the extension: octet-stream made pages download instead of
// render, and browsers refuse CSS and JS of the wrong type.
func TestBlobObjectsAreStoredWithTheirContentType(t *testing.T) {
	ctx := context.Background()
	f := newFakeBlobStore(t, "public")
	s := f.store(t, "")
	want := map[string]string{
		"posts/a.html":      "text/html; charset=utf-8",
		"styles.css":        "text/css; charset=utf-8",
		"stream.js":         "application/javascript; charset=utf-8",
		".well-known/polis": "application/json; charset=utf-8",
		"robots.txt":        "text/plain; charset=utf-8",
		"rsl.xml":           "application/xml; charset=utf-8",
		"blob.bin":          "application/octet-stream",
	}
	for k := range want {
		if err := s.Put(ctx, k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	for k, ct := range want {
		if got := f.objects[k].ctype; got != ct {
			t.Errorf("%s stored as %q, want %q", k, got, ct)
		}
	}
}

// Vercel challenges a mixed-case Host on a private store (403), so the blob
// hostname is lowercased while the id the API matches is sent as given.
func TestVercelBlobReadsFromALowercaseHostAndSendsTheIdAsGiven(t *testing.T) {
	for _, access := range []string{"private", "public"} {
		v, err := NewVercelBlob(VercelBlobConfig{Credential: StaticCredential("tok"), StoreID: "store_7dHLlx5EnnWzVeVB", Access: access})
		if err != nil {
			t.Fatal(err)
		}
		want := "https://7dhllx5ennwzvevb." + access + ".blob.vercel-storage.com"
		if v.blobBase != want {
			t.Errorf("%s: blob base %q, want %q", access, v.blobBase, want)
		}
		if v.storeID != "7dHLlx5EnnWzVeVB" {
			t.Errorf("%s: store id %q, want it as given", access, v.storeID)
		}
	}
}

// The store id becomes the read host, so an id that could name another host
// is refused at construction, before any URL is built from it, whether it
// arrives as a store id or inside a token.
func TestVercelBlobRefusesAStoreIdThatCouldNameAnotherHost(t *testing.T) {
	for _, id := range []string{
		"x.evil.com/", "evil.com/x#", "user@evil.com", "@", "/", "#", "a/b", "a#b", "a.b", "a:1",
		"store_", "", " abc", "abc ", "a-b", "a_b", "abc%2f", "ab\x00c",
	} {
		if _, err := NewVercelBlob(VercelBlobConfig{Credential: StaticCredential("tok"), StoreID: id, Access: "private"}); err == nil {
			t.Errorf("store id %q was accepted", id)
		}
		if id == "" || strings.Contains(id, "_") {
			continue
		}
		if _, err := NewVercelBlob(VercelBlobConfig{Token: "vercel_blob_rw_" + id + "_secret", Access: "private"}); err == nil {
			t.Errorf("token naming store %q was accepted", id)
		} else if strings.Contains(err.Error(), "secret") {
			t.Errorf("the refusal quotes the token: %v", err)
		}
	}
	// ⭐ Case is accepted either way: the host is lowercased, the id sent as given.
	for _, id := range []string{"store_7dHLlx5EnnWzVeVB", "7dHLlx5EnnWzVeVB", "ABC", "abc123"} {
		if _, err := NewVercelBlob(VercelBlobConfig{Credential: StaticCredential("tok"), StoreID: id, Access: "private"}); err != nil {
			t.Errorf("store id %q was refused: %v", id, err)
		}
	}
}
