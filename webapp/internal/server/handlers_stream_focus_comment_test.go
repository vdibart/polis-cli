package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/cache"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
)

// makeFakeLatestDS returns an httptest.Server answering
// POST /v1/content/comments/latest with the given latest map. It records the
// request count (singleflight/cache assertions) and the URLs of the most
// recent request (absolutization assertions).
func makeFakeLatestDS(t *testing.T, latest map[string]map[string]string, delay time.Duration) (*httptest.Server, *atomic.Int32, *[]string) {
	t.Helper()
	var requestCount atomic.Int32
	lastURLs := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/content/comments/latest" {
			http.NotFound(w, r)
			return
		}
		requestCount.Add(1)
		var body struct {
			URLs []string `json:"urls"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*lastURLs = body.URLs
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"latest": latest})
	}))
	return srv, &requestCount, lastURLs
}

func decodeFocus(t *testing.T, w *httptest.ResponseRecorder) focusCommentResponse {
	t.Helper()
	var resp focusCommentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

func TestHandleStreamFocusComment_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/stream/focus-comment?url=x", nil)
	w := httptest.NewRecorder()
	s.handleStreamFocusComment(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleStreamFocusComment_EmptyURL(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = "https://ds.example" // set so empty-url is the reason, not missing DS
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/focus-comment", nil)
	w := httptest.NewRecorder()
	s.handleStreamFocusComment(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if resp := decodeFocus(t, w); resp.HasComment {
		t.Errorf("expected has_comment=false for empty url")
	}
}

func TestHandleStreamFocusComment_NoDiscoveryURL(t *testing.T) {
	s := newConfiguredServer(t)
	s.DiscoveryURL = ""
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/focus-comment?url=https://x.example/posts/a.md", nil)
	w := httptest.NewRecorder()
	s.handleStreamFocusComment(w, r)
	if resp := decodeFocus(t, w); resp.HasComment {
		t.Errorf("expected has_comment=false when no DS configured")
	}
}

func TestHandleStreamFocusComment_NoCommentForPost(t *testing.T) {
	ds, _, _ := makeFakeLatestDS(t, map[string]map[string]string{}, 0)
	defer ds.Close()
	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL

	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/focus-comment?url=https://other.example/posts/a.md", nil)
	w := httptest.NewRecorder()
	s.handleStreamFocusComment(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if resp := decodeFocus(t, w); resp.HasComment {
		t.Errorf("expected has_comment=false when DS returns no latest comment")
	}
}

func TestHandleStreamFocusComment_OwnCommentFromLocalDisk(t *testing.T) {
	postMd := "https://test-site.polis.pub/posts/20260525/mine.md"
	ds, _, _ := makeFakeLatestDS(t, map[string]map[string]string{
		postMd: {
			"url":           "https://test-site.polis.pub/comments/20260526/reply.md",
			"author_domain": "test-site.polis.pub",
			"published":     "2026-05-26T00:00:00Z",
			"version":       "sha256:v1",
		},
	}, 0)
	defer ds.Close()

	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL

	// Seed the local comment at the mount path LoadLocalCommentContent reads.
	commentDir := filepath.Join(s.DataDir, "comments", "20260526")
	if err := os.MkdirAll(commentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\ntimestamp: 2026-05-26T00:00:00Z\n---\n\nThis is my local reply body.\n"
	if err := os.WriteFile(filepath.Join(commentDir, "reply.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	// Own post sent as a relative mount URL — must be absolutized + normalized.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/focus-comment?url=/posts/20260525/mine.html", nil)
	w := httptest.NewRecorder()
	s.handleStreamFocusComment(w, r)

	resp := decodeFocus(t, w)
	if !resp.HasComment || resp.Comment == nil {
		t.Fatalf("expected has_comment=true with a comment, got %+v", resp)
	}
	if !strings.Contains(resp.Comment.ContentHTML, "This is my local reply body.") {
		t.Errorf("expected local body rendered, got %q", resp.Comment.ContentHTML)
	}
	if resp.Comment.AuthorName != "test-site.polis.pub" {
		t.Errorf("unexpected author_name: %q", resp.Comment.AuthorName)
	}
	if !strings.HasSuffix(resp.Comment.URL, ".html") {
		t.Errorf("expected display url to be .html mount form, got %q", resp.Comment.URL)
	}
}

// When the DS latest-comment lookup returns nothing for one of the tenant's
// OWN posts, read-focus must fall back to the local blessed-comments index —
// the same source the canonical per-post page renders from. Regression for the
// "comment shows on the per-post page but not in read-focus" bug.
func TestHandleStreamFocusComment_OwnPostFallsBackToLocalBlessed(t *testing.T) {
	ds, _, _ := makeFakeLatestDS(t, map[string]map[string]string{}, 0) // DS knows nothing
	defer ds.Close()

	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL

	// Seed blessed.json with one blessed comment (cross-tenant commenter) for
	// the tenant's own post.
	blessedJSON := `{"version":"v1","comments":[{"post":"content/pub.polis.core/post/20260525/mine.md","blessed":[{"url":"https://vdibart.polis.pub/comments/20260526/reply.md","version":"sha256:v1","blessed_at":"2026-05-26T00:00:00Z"}]}]}`
	blessedDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "comment")
	if err := os.MkdirAll(blessedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blessedDir, "blessed.json"), []byte(blessedJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed the foreign blessed comment body in the ISOLATED cache (where the
	// grant/sync flow + Rosie put it); a foreign comment is never read from a
	// public path.
	md := "---\ntimestamp: 2026-05-26T00:00:00Z\n---\n\nThe blessed reply body.\n"
	if err := cache.StoreBlessed(s.DataDir, "ds.polis.pub", cache.BlessedSidecar{SourceURL: "https://vdibart.polis.pub/comments/20260526/reply.md", FetchedAt: "t"}, []byte(md)); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/focus-comment?url=/posts/20260525/mine.html", nil)
	w := httptest.NewRecorder()
	s.handleStreamFocusComment(w, r)

	resp := decodeFocus(t, w)
	if !resp.HasComment || resp.Comment == nil {
		t.Fatalf("expected has_comment=true via local-blessed fallback, got %+v", resp)
	}
	if !strings.Contains(resp.Comment.ContentHTML, "The blessed reply body.") {
		t.Errorf("expected local blessed body rendered, got %q", resp.Comment.ContentHTML)
	}
	if resp.Comment.AuthorName != "vdibart.polis.pub" {
		t.Errorf("unexpected author_name: %q", resp.Comment.AuthorName)
	}
	// author_domain + author_avatar drive the read-focus card's static handle
	// and floated avatar (matching the canonical per-post card).
	if resp.Comment.AuthorDomain != "vdibart.polis.pub" {
		t.Errorf("unexpected author_domain: %q", resp.Comment.AuthorDomain)
	}
	if resp.Comment.AuthorAvatar == nil {
		t.Error("expected author_avatar config (real or fallback) for the read-focus avatar")
	}
	// v4 meta-line date shape — stripped year (not "May 26, 2026").
	if strings.Contains(resp.Comment.PublishedHuman, "2026") {
		t.Errorf("expected stripped-year v4 date, got %q", resp.Comment.PublishedHuman)
	}
}

// focusBodyRoundTripper serves a comment .md for the cross-tenant body fetch.
type focusBodyRoundTripper struct{ body string }

func (c focusBodyRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	md := "---\ntimestamp: 2026-05-26T00:00:00Z\nin_reply_to: https://other.example/posts/a.md\n---\n\n" + c.body + "\n"
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(md)), Header: make(http.Header)}, nil
}

func TestHandleStreamFocusComment_CrossTenantFetchesRemoteBody(t *testing.T) {
	postMd := "https://other.example/posts/a.md"
	ds, _, _ := makeFakeLatestDS(t, map[string]map[string]string{
		postMd: {
			"url":           "https://bob.example.com/comments/20260526/r.md",
			"author_domain": "bob.example.com",
			"published":     "2026-05-26T00:00:00Z",
			"version":       "sha256:v2",
		},
	}, 0)
	defer ds.Close()

	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL

	orig := render.DefaultHTTPClient
	render.DefaultHTTPClient = &http.Client{Transport: focusBodyRoundTripper{body: "A thoughtful cross-tenant reply."}}
	defer func() { render.DefaultHTTPClient = orig }()

	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/focus-comment?url="+postMd, nil)
	w := httptest.NewRecorder()
	s.handleStreamFocusComment(w, r)

	resp := decodeFocus(t, w)
	if !resp.HasComment || resp.Comment == nil {
		t.Fatalf("expected has_comment=true, got %+v", resp)
	}
	if !strings.Contains(resp.Comment.ContentHTML, "A thoughtful cross-tenant reply.") {
		t.Errorf("expected remote body rendered, got %q", resp.Comment.ContentHTML)
	}
	if resp.Comment.AuthorName != "bob.example.com" {
		t.Errorf("unexpected author_name: %q", resp.Comment.AuthorName)
	}
}

func TestHandleStreamFocusComment_AbsolutizesOwnPostURL(t *testing.T) {
	// Own post sent as a relative mount URL; DS must receive the absolute
	// canonical .md form (BaseURL + path, .html → .md).
	ds, _, lastURLs := makeFakeLatestDS(t, map[string]map[string]string{}, 0)
	defer ds.Close()
	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL

	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/focus-comment?url=/posts/20260525/mine.html", nil)
	w := httptest.NewRecorder()
	s.handleStreamFocusComment(w, r)

	want := "https://test-site.polis.pub/posts/20260525/mine.md"
	found := false
	for _, u := range *lastURLs {
		if u == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected DS to receive absolutized .md url %q, got %v", want, *lastURLs)
	}
}

func TestHandleStreamFocusComment_DSErrorFailsSoft(t *testing.T) {
	// DS returns 500 — the handler must still respond 200 has_comment:false.
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ds.Close()
	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL

	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/focus-comment?url=https://other.example/posts/a.md", nil)
	w := httptest.NewRecorder()
	s.handleStreamFocusComment(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (fail-soft), got %d", w.Code)
	}
	if resp := decodeFocus(t, w); resp.HasComment {
		t.Errorf("expected has_comment=false on DS error")
	}
}

func TestHandleStreamFocusComment_CachesResult(t *testing.T) {
	postMd := "https://other.example/posts/cached.md"
	ds, requests, _ := makeFakeLatestDS(t, map[string]map[string]string{}, 0)
	defer ds.Close()
	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL

	url := "/api/v1/stream/focus-comment?url=" + postMd
	for i := 0; i < 3; i++ {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		s.handleStreamFocusComment(w, r)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("expected exactly 1 DS request across 3 cached calls, got %d", got)
	}
}

// --- /api/v1/stream/body (read-focus full-body proxy) -----------------------

func TestHandleStreamBody_CrossTenantFetchesRemoteBody(t *testing.T) {
	s := newConfiguredServer(t)

	orig := render.DefaultHTTPClient
	render.DefaultHTTPClient = &http.Client{Transport: focusBodyRoundTripper{body: "The full cross-tenant post body."}}
	defer func() { render.DefaultHTTPClient = orig }()

	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/body?url=https://other.example/posts/a.md", nil)
	w := httptest.NewRecorder()
	s.handleStreamBody(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp streamBodyResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.HasBody {
		t.Fatalf("expected has_body=true, got %+v", resp)
	}
	if !strings.Contains(resp.BodyHTML, "The full cross-tenant post body.") {
		t.Errorf("expected rendered remote body, got %q", resp.BodyHTML)
	}
}

func TestHandleStreamBody_MissingURL(t *testing.T) {
	s := newConfiguredServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stream/body", nil)
	w := httptest.NewRecorder()
	s.handleStreamBody(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 fail-soft, got %d", w.Code)
	}
	var resp streamBodyResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.HasBody {
		t.Errorf("expected has_body=false for empty url, got %+v", resp)
	}
}

func TestHandleStreamBody_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/stream/body?url=x", nil)
	w := httptest.NewRecorder()
	s.handleStreamBody(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
