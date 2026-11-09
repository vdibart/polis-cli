package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

// VercelBlob is a Store backed by ONE Vercel Blob store.
//
// ⭐⭐⭐ IT IS THE OBJECT-STORE SEMANTICS IN object.go PLUS HTTP, and deliberately
// nothing more. Every decision that could be got wrong — the key mapping, the
// prefix boundary, the empty-prefix answer, what Capabilities declares — is
// settled there and tested without a network. This file is transport.
//
// ⛔⛔ THE WIRE PROTOCOL IS VERCEL'S OWN SDK's, NOT OURS. It was read from
// @vercel/blob 2.8.1 (BLOB_API_VERSION = 12), because a client written from our
// own model of the API passed every test against a fake written from the same
// model and was wrong in four places: it sent no access, never stated
// overwrite, used an older endpoint, and read through a URL cache instead of the
// deterministic blob URL. What the SDK does, and therefore what this does:
//   - writes, lists and deletes go to https://vercel.com/api/blob with
//     `x-api-version: 12` and `x-vercel-blob-store-id`;
//   - a put is `PUT /?pathname=<p>` carrying `x-vercel-blob-access`,
//     `x-allow-overwrite`, `x-add-random-suffix`, `x-cache-control-max-age`;
//   - a read is a GET of https://<store-id>.<access>.blob.vercel-storage.com/<p>,
//     a URL fully determined by the store and the pathname;
//   - the store id is the fourth `_`-separated field of the read-write token.
//
// ⛔⛔ ACCESS IS PER STORE, NOT PER OBJECT. A PUBLIC store serves every pathname
// at its own URL to anyone, whatever a site's rewrites say, so nothing private
// may ever be written to one. That is enforced one level up: a site's backend
// opens its store as PRIVATE (openVercelBlobBackend), and readers get the
// public files from the site's deployment, never from a store. ⭐ A PRIVATE store needs the token to
// read, and is the only kind that offers a CONSISTENT read (`?cache=0`), so a
// read after an overwrite sees the overwrite rather than a CDN copy up to a
// minute old.
//
// ⛔ ONE TOKEN, ONE STORE, ONE PREFIX. The prefix namespaces a site inside the
// store; ⚠️ the traversal refusal is then the only thing between two tenants,
// because there is no filesystem to also be bounded by.
type VercelBlob struct {
	cred     CredentialSource
	storeID  string
	access   string
	prefix   string
	api      string
	blobBase string
	hc       *http.Client
}

// VercelBlobConfig is what the store needs to exist.
type VercelBlobConfig struct {
	// Token is a static Vercel Blob read-write token, which also names the
	// store. ⛔ A secret, read through pkg/secret, never from a file in the
	// site. ⭐ The deployed path uses Credential instead, and holds no static
	// secret at all.
	Token string

	// Credential supplies the bearer per call — on Vercel, the OIDC token the
	// platform delivers with each request. Requires StoreID. Exclusive with
	// Token.
	Credential CredentialSource

	// StoreID names the store when the credential does not (OIDC does not).
	// A leading "store_" is accepted and dropped, as the SDK does.
	StoreID string

	// Access is the store's access, "public" or "private", as it was created.
	// ⛔ REQUIRED, NEVER DEFAULTED: Vercel requires it on every put, and a
	// guessed value either fails every write or — worse — is right by accident
	// on a store holding something that must not be public.
	Access string

	// Prefix namespaces this site inside the store. Empty means the whole
	// store belongs to this site.
	Prefix string

	// APIBaseURL overrides https://vercel.com/api/blob, for tests.
	APIBaseURL string

	// BlobBaseURL overrides https://<store-id>.<access>.blob.vercel-storage.com,
	// for tests.
	BlobBaseURL string

	// HTTPClient overrides the client, for tests and for timeout control.
	HTTPClient *http.Client

	// TokenHTTPClient fetches the token issuer's keys; nil means a default.
	TokenHTTPClient *http.Client
}

// blobAPIVersion is the SDK's BLOB_API_VERSION for the protocol this speaks.
const blobAPIVersion = "12"

// blobCacheMaxAge is the shortest CDN cache Vercel allows (60 s). ⚠️ The SDK
// documents a one-minute minimum, so the 0 this used to send was never honoured.
const blobCacheMaxAge = "60"

// NewVercelBlob returns a Store backed by one Vercel Blob store.
func NewVercelBlob(cfg VercelBlobConfig) (*VercelBlob, error) {
	if cfg.Access != "public" && cfg.Access != "private" {
		return nil, fmt.Errorf(`storage: vercel blob access must be "public" or "private"`)
	}
	var cred CredentialSource
	var storeID string
	switch {
	case cfg.Token != "" && cfg.Credential != nil:
		return nil, fmt.Errorf("storage: vercel blob takes a token or a credential source, not both")
	case cfg.Token != "":
		// ⭐ As the SDK parses it. ⛔ The token is never quoted in the error.
		parts := strings.Split(cfg.Token, "_")
		if len(parts) < 4 || parts[3] == "" {
			return nil, fmt.Errorf("storage: vercel blob token does not name a store (expected vercel_blob_rw_<store-id>_…)")
		}
		cred, storeID = StaticCredential(cfg.Token), parts[3]
	case cfg.Credential != nil:
		if cfg.StoreID == "" {
			return nil, fmt.Errorf("storage: vercel blob with a credential source needs a store id")
		}
		cred, storeID = cfg.Credential, cfg.StoreID
	default:
		// ⛔ Refuse rather than defaulting to an anonymous client. A store that
		// constructs without credentials fails at the first write, in whatever
		// code path happened to write first.
		return nil, fmt.Errorf("storage: vercel blob needs a read-write token or a credential source")
	}
	// ⛔ Refused before any URL is built from it. ⛔ The id is never quoted:
	// from a token it sits beside the secret.
	if !isStoreID(storeID) {
		return nil, fmt.Errorf("storage: vercel blob store id must be letters and digits, with an optional leading store_")
	}
	// ⚠️ Not lowercased: the SDK sends the id as given, and the API matches it.
	storeID = strings.TrimPrefix(storeID, "store_")
	api := cfg.APIBaseURL
	if api == "" {
		api = "https://vercel.com/api/blob"
	}
	blobBase := cfg.BlobBaseURL
	if blobBase == "" {
		// ⛔ THE HOSTNAME IS LOWERCASED, though the id above is not. Vercel's
		// edge answers a mixed-case Host on a private store with a bot
		// challenge (403, X-Vercel-Mitigated: challenge), so every private read
		// failed. Curl and Node lowercase a hostname before sending it and Go
		// does not, so only this client met it; Vercel's own listings give the
		// blob URL in lowercase. Measured against a live store, 2026-10-10.
		blobBase = fmt.Sprintf("https://%s.%s.blob.vercel-storage.com", strings.ToLower(storeID), cfg.Access)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &VercelBlob{
		cred:     cred,
		storeID:  storeID,
		access:   cfg.Access,
		prefix:   cleanPrefix(cfg.Prefix),
		api:      strings.TrimSuffix(api, "/"),
		blobBase: strings.TrimSuffix(blobBase, "/"),
		hc:       hc,
	}, nil
}

// Private reports whether this store is a private one.
func (v *VercelBlob) Private() bool { return v.access == "private" }

// objectKey mirrors ObjectStore.objectKey exactly.
//
// ⛔ THE TWO MUST NOT DRIFT, because the tested semantics are the other one's.
// A test asserts they agree on the same inputs.
func (v *VercelBlob) objectKey(key string) (string, error) {
	o := &ObjectStore{prefix: v.prefix}
	return o.objectKey(key)
}

// blobURL is where an object is read from. ⭐ Determined by the store and the
// pathname alone, so a restarted process needs no listing to find anything.
func (v *VercelBlob) blobURL(objectKey string, consistent bool) string {
	segs := strings.Split(objectKey, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	u := v.blobBase + "/" + strings.Join(segs, "/")
	// ⭐ Only a private store offers a consistent read; the SDK sends it only
	// there, and so does this.
	if consistent && v.Private() {
		u += "?cache=0"
	}
	return u
}

// apiDo calls the Blob API with the headers every API request carries.
func (v *VercelBlob) apiDo(ctx context.Context, method, u string, body []byte, hdr map[string]string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	tok, err := v.cred.Credential(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("x-api-version", blobAPIVersion)
	req.Header.Set("x-vercel-blob-store-id", v.storeID)
	for k, val := range hdr {
		req.Header.Set(k, val)
	}
	return v.hc.Do(req)
}

func (v *VercelBlob) Get(ctx context.Context, key string) ([]byte, error) {
	ok, err := v.objectKey(key)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.blobURL(ok, true), nil)
	if err != nil {
		return nil, err
	}
	// ⛔ A private object is unreadable without the token. A public one needs
	// none, and sending it there would only put a credential on a CDN request.
	if v.Private() {
		tok, cerr := v.cred.Credential(ctx)
		if cerr != nil {
			return nil, cerr
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := v.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: blob get %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("storage: blob get %s: HTTP %d", key, resp.StatusCode)
	}
	// ⛔ BOUNDED. An unbounded read let a store (or anything answering in its
	// place) exhaust the instance's memory with one object.
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBlobObject+1))
	if err != nil {
		return nil, fmt.Errorf("storage: blob get %s: %w", key, err)
	}
	if int64(len(b)) > maxBlobObject {
		return nil, fmt.Errorf("storage: blob get %s: object larger than %d bytes", key, maxBlobObject)
	}
	return b, nil
}

// maxBlobObject is the largest object Get will read. Var so a test can lower it.
var maxBlobObject int64 = 64 << 20

// maxListPages bounds a listing: 1000 pages of 1000 is a million objects, far
// past any site. Var so a test can lower it.
var maxListPages = 1000

func (v *VercelBlob) Put(ctx context.Context, key string, data []byte, opts ...PutOption) error {
	ok, err := v.objectKey(key)
	if err != nil {
		return err
	}
	if ok == "" || ok == v.prefix {
		return fmt.Errorf("storage: cannot write the store root as an object")
	}
	// ⭐ The same bound Get reads to: an object written larger could never be
	// read back.
	if int64(len(data)) > maxBlobObject {
		return fmt.Errorf("storage: blob put %s: object larger than %d bytes", key, maxBlobObject)
	}
	// ⚠️ WithMode is accepted and ignored: Capabilities().Modes is false.
	//
	// ⛔⛔ OVERWRITE IS STATED ON EVERY PUT. Vercel refuses to overwrite an
	// existing pathname unless told it may ("By default an error will be thrown
	// if you try to overwrite a blob"), so a put that omitted it would succeed
	// once and fail every rewrite. ⭐ And the refusal is exactly the
	// conditional create IfAbsent needs, enforced across every cold start.
	allow := "1"
	if resolvePut(opts).IfAbsent {
		allow = "0"
	}
	q := url.Values{"pathname": {ok}}
	resp, err := v.apiDo(ctx, http.MethodPut, v.api+"/?"+q.Encode(), data, map[string]string{
		"x-vercel-blob-access":    v.access,
		"x-content-type":          blobContentType(ok),
		"x-add-random-suffix":     "0", // ⛔ the key IS the address
		"x-allow-overwrite":       allow,
		"x-cache-control-max-age": blobCacheMaxAge,
	})
	if err != nil {
		return fmt.Errorf("storage: blob put %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		// ⭐ The refusal to overwrite is the conditional create losing, not a
		// failure. ⚠️ Recognised by its message because the API gives it no
		// distinct status; only meaningful when overwrite was refused. This
		// decides EXISTENCE on a create we asked to be conditional, never
		// absence.
		if allow == "0" && resp.StatusCode/100 == 4 && bytes.Contains(bytes.ToLower(b), []byte("already exists")) {
			return ErrExists
		}
		// ⛔ The status only, never the body: an API that echoed a header or a
		// submitted value would put a token into the log.
		return fmt.Errorf("storage: blob put %s: HTTP %d", key, resp.StatusCode)
	}
	return nil
}

func (v *VercelBlob) Exists(ctx context.Context, key string) (bool, error) {
	_, err := v.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, err
}

func (v *VercelBlob) Stat(ctx context.Context, key string) (Info, error) {
	ok, err := v.objectKey(key)
	if err != nil {
		return Info{}, err
	}
	blobs, err := v.list(ctx, ok)
	if err != nil {
		return Info{}, err
	}
	for _, b := range blobs {
		if b.Pathname == ok {
			// ⚠️ Mode is left zero rather than invented; Modes is false.
			return Info{Size: b.Size, ModTime: b.UploadedAt}, nil
		}
	}
	return Info{}, fmt.Errorf("%w: %s", ErrNotFound, key)
}

func (v *VercelBlob) List(ctx context.Context, prefix string, opts ...ListOption) ([]Entry, error) {
	res := resolveList(opts)
	base, err := v.objectKey(prefix)
	if err != nil {
		return nil, err
	}
	scan := base
	if scan != "" {
		scan += "/"
	}
	blobs, err := v.list(ctx, scan)
	if err != nil {
		return nil, err
	}

	seenDir := map[string]bool{}
	var out []Entry
	for _, b := range blobs {
		if scan != "" && !strings.HasPrefix(b.Pathname, scan) {
			continue
		}
		rel := strings.TrimPrefix(b.Pathname, scan)
		if rel == "" {
			continue
		}
		storeKey := b.Pathname
		if v.prefix != "" {
			storeKey = strings.TrimPrefix(strings.TrimPrefix(b.Pathname, v.prefix), "/")
		}
		if res.Recursive {
			out = append(out, Entry{Key: storeKey, Size: b.Size, ModTime: b.UploadedAt})
			continue
		}
		if i := strings.Index(rel, "/"); i >= 0 {
			dirKey := storeKey[:len(storeKey)-len(rel)+i]
			if !seenDir[dirKey] {
				seenDir[dirKey] = true
				out = append(out, Entry{Key: dirKey, IsDir: true})
			}
			continue
		}
		out = append(out, Entry{Key: storeKey, Size: b.Size, ModTime: b.UploadedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (v *VercelBlob) Delete(ctx context.Context, key string, opts ...DeleteOption) error {
	res := resolveDelete(opts)
	ok, err := v.objectKey(key)
	if err != nil {
		return err
	}
	// ⛔ Never the whole store: it would wipe .polis/keys. Every backend refuses.
	if ok == "" || ok == v.prefix {
		return fmt.Errorf("%w: refusing to delete the store root", ErrInvalidKey)
	}

	var targets []string
	if res.Recursive {
		scan := ok
		if scan != "" {
			scan += "/"
		}
		blobs, lerr := v.list(ctx, scan)
		if lerr != nil {
			return lerr
		}
		for _, b := range blobs {
			if scan == "" || strings.HasPrefix(b.Pathname, scan) {
				targets = append(targets, b.URL)
			}
		}
		if exists, _ := v.Exists(ctx, key); exists {
			targets = append(targets, v.blobURL(ok, false))
		}
	} else {
		exists, eerr := v.Exists(ctx, key)
		if eerr != nil {
			return eerr
		}
		if !exists {
			if res.IgnoreMissing {
				return nil
			}
			return fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		targets = []string{v.blobURL(ok, false)}
	}

	if len(targets) == 0 {
		if res.IgnoreMissing {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}

	body, _ := json.Marshal(map[string]any{"urls": targets})
	resp, err := v.apiDo(ctx, http.MethodPost, v.api+"/delete", body,
		map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return fmt.Errorf("storage: blob delete %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("storage: blob delete %s: HTTP %d", key, resp.StatusCode)
	}
	return nil
}

// RealPath always refuses. See ObjectStore.RealPath.
func (v *VercelBlob) RealPath(key string) (string, error) {
	return "", fmt.Errorf("%w: vercel blob has no filesystem path (key %q)", ErrUnsupported, key)
}

// Capabilities declares what Vercel Blob genuinely does.
//
// ⛔ Modes IS FALSE. On a blob backend ".polis must not be world-readable" is
// carried by WHERE it is written — a private store, the only kind a site opens —
// never by a weaker version of a mode. ⚠️ Routing alone does not carry it: a
// public store serves every pathname at its own URL.
func (v *VercelBlob) Capabilities() Capabilities {
	return Capabilities{Modes: false, ModTimes: true, RealPath: false}
}

// ── listing ──────────────────────────────────────────────────────────────

type blobItem struct {
	Pathname   string    `json:"pathname"`
	URL        string    `json:"url"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploadedAt"`
}

// list pages through the API.
//
// ⛔ IT FOLLOWS THE CURSOR TO THE END. A listing that silently stops at the
// first page makes a render walk skip content, and the symptom is missing
// posts rather than an error.
func (v *VercelBlob) list(ctx context.Context, prefix string) ([]blobItem, error) {
	var all []blobItem
	cursor := ""
	seen := map[string]bool{}
	for pages := 0; ; pages++ {
		// ⛔ BOUNDED, and a cursor seen before ends it. A server answering the
		// same cursor forever made this loop spin (5,563 requests in 300 ms
		// under test), and the serving path calls it with a background
		// context that never times out.
		if pages >= maxListPages {
			return nil, fmt.Errorf("storage: blob list %q: more than %d pages", prefix, maxListPages)
		}
		q := url.Values{}
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		q.Set("limit", "1000")
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		resp, err := v.apiDo(ctx, http.MethodGet, v.api+"/?"+q.Encode(), nil, nil)
		if err != nil {
			return nil, fmt.Errorf("storage: blob list %q: %w", prefix, err)
		}
		var page struct {
			Blobs   []blobItem `json:"blobs"`
			Cursor  string     `json:"cursor"`
			HasMore bool       `json:"hasMore"`
		}
		dec := json.NewDecoder(resp.Body)
		derr := dec.Decode(&page)
		code := resp.StatusCode
		resp.Body.Close()
		if code/100 != 2 {
			return nil, fmt.Errorf("storage: blob list %q: HTTP %d", prefix, code)
		}
		if derr != nil {
			return nil, fmt.Errorf("storage: blob list %q: %w", prefix, derr)
		}
		all = append(all, page.Blobs...)
		if !page.HasMore {
			return all, nil
		}
		// ⚠️ hasMore with no cursor, or a cursor already followed, can only
		// repeat a page or stop early. Either is an error, never a silent
		// truncation: a short listing reads as missing posts.
		if page.Cursor == "" || seen[page.Cursor] {
			return nil, fmt.Errorf("storage: blob list %q: the API repeated or dropped its cursor", prefix)
		}
		seen[page.Cursor] = true
		cursor = page.Cursor
	}
}

// isNotFound reports a not-found error. ⛔ By identity, never by its text:
// every not-found here wraps ErrNotFound, and matching a message would break
// silently the day the wording changes.
func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// blobContentType is the type a served object is stored with.
//
// ⚠️ A store serves what it holds with the type it was stored under (octet-
// stream once made every page of a public store download instead of render).
// Readers are now served from the deployment, whose header routes set the
// types, so this is the type of a store read only. ⭐ The table matches what the self-hosted serve
// handler sends (webapp/internal/serve/static.go), so a page reads the same
// from either.
func blobContentType(key string) string {
	switch {
	case strings.HasSuffix(key, ".well-known/polis") || strings.Contains(key, ".well-known/") && path.Ext(key) == "":
		return "application/json; charset=utf-8"
	case path.Base(key) == "robots.txt":
		return "text/plain; charset=utf-8"
	}
	switch path.Ext(key) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".jsonl":
		return "application/jsonl; charset=utf-8"
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".xml":
		return "application/xml; charset=utf-8"
	}
	return "application/octet-stream"
}

// vercelOIDCHeader is where Vercel delivers a function's OIDC token: "When
// your application invokes a Vercel Function, the OIDC token is set to the
// x-vercel-oidc-token header". ⛔ Vercel's name; it appears nowhere else.
//
// ⭐ VERCEL_OIDC_TOKEN is read from the ENVIRONMENT only, by a separate hook
// (`vercel env pull` writes it for local development). ⛔ Never from a request:
// a request's headers are chosen by its sender.
//
// ⚠️ UNVERIFIED: whether Vercel strips a client-supplied x-vercel-oidc-token
// before the function sees it. If it does not, a forged header reaches the
// cell; latest-wins (see RequestCredential.Offer) bounds that to "until the
// next genuine request", and the platform refuses a forged token, so nothing
// is ever accepted on it. To be confirmed on the live run.
const vercelOIDCHeader = "x-vercel-oidc-token"

const vercelOIDCEnv = "VERCEL_OIDC_TOKEN"

// openVercelBlobBackend opens a site on its private Vercel Blob store,
// authenticated with the deployment's OIDC token. ⭐ No static secret is held: the token arrives with requests.
// base carries test overrides (endpoints, client); its credential fields are
// ignored.
func openVercelBlobBackend(spec BackendSpec, base VercelBlobConfig) (Backend, error) {
	// ⛔ Every token is verified against the deployment's own issuer before
	// the cell will hold it.
	verifier := &OIDCVerifier{Issuer: spec.TokenIssuer, Audience: spec.TokenAudience, Subject: spec.TokenSubject,
		HTTPClient: base.TokenHTTPClient}
	cell := &RequestCredential{Verify: verifier.Verify}
	mk := func(storeID, access string) (*VercelBlob, error) {
		cfg := base
		cfg.Token = ""
		cfg.Credential, cfg.StoreID, cfg.Access = cell, storeID, access
		if cfg.BlobBaseURL != "" {
			r := strings.NewReplacer("{access}", access, "{store}", strings.TrimPrefix(storeID, "store_"))
			cfg.BlobBaseURL = r.Replace(cfg.BlobBaseURL)
		}
		return NewVercelBlob(cfg)
	}
	// ⛔ Always private: a public store serves every pathname to anyone, and
	// this one holds .polis.
	src, err := mk(spec.Source, "private")
	if err != nil {
		return Backend{}, fmt.Errorf("storage: source store: %w", err)
	}
	return Backend{
		Store: src,
		// ⛔ A request is read ONLY through the platform's header.
		FromRequest: func(values func(string) []string) error {
			vs := values(vercelOIDCHeader)
			switch len(vs) {
			case 0:
				return nil
			case 1:
				return cell.Offer(vs[0])
			default:
				// ⛔ More than one: a client's value beside the platform's.
				// Header.Get returns the FIRST, which would be the client's.
				return fmt.Errorf("storage: %d values of the credential header; refusing the request", len(vs))
			}
		},
		// ⭐ Offer's refusal is returned, not discarded: it is the only record
		// of why a laptop's pulled token was not used.
		FromEnvironment: func(getenv func(string) string) error { return cell.Offer(getenv(vercelOIDCEnv)) },
		StartsOnRequest: true,
	}, nil
}
