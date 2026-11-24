package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
)

// pqlRequest performs a GET against /pql/<path-tail> with the given Accept
// header and returns the status + decoded body.
func pqlRequest(t *testing.T, s *Server, pathTail, accept string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/pql/"+pathTail, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	s.handleStreamPQL(w, req)
	var resp map[string]interface{}
	if w.Body.Len() > 0 {
		_ = json.NewDecoder(w.Body).Decode(&resp)
	}
	return w.Code, resp
}

func TestStreamPQL_MethodNotAllowed(t *testing.T) {
	s := newConfiguredServer(t)
	req := httptest.NewRequest(http.MethodPost, "/pql/all+posts+by+date", nil)
	w := httptest.NewRecorder()
	s.handleStreamPQL(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestStreamPQL_JSONEnvelope_TenantRelative(t *testing.T) {
	s := newConfiguredServer(t)
	code, body := pqlRequest(t, s, "all+posts+by+date", "application/json")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["version"] != "pql.v1" {
		t.Errorf("version = %v, want pql.v1", body["version"])
	}
	if body["tenant"] != "test-site.polis.pub" {
		t.Errorf("tenant = %v", body["tenant"])
	}
	// query echoes the RESOLVED sentence (explicit scope); url is the short
	// shareable tenant-relative form (clause omitted).
	if body["query"] != "all posts from test-site.polis.pub by date" {
		t.Errorf("query = %v", body["query"])
	}
	if body["url"] != "/pql/all+posts+by+date" {
		t.Errorf("url = %v, want /pql/all+posts+by+date", body["url"])
	}
	parsed, _ := body["parsed"].(map[string]interface{})
	if parsed["type"] != "posts" || parsed["relation"] != "from" {
		t.Errorf("parsed = %v", parsed)
	}
	// tenant-relative: omitted clause defaults scope to the serving tenant
	if parsed["scope"] != "@test-site.polis.pub" {
		t.Errorf("parsed.scope = %v, want @test-site.polis.pub", parsed["scope"])
	}
	if _, ok := body["items"]; !ok {
		t.Errorf("missing items")
	}
	if _, ok := body["pagination"].(map[string]interface{}); !ok {
		t.Errorf("missing pagination object: %v", body["pagination"])
	}
}

func TestStreamPQL_FormatJSONOverride(t *testing.T) {
	s := newConfiguredServer(t)
	// No Accept header — ?format=json forces the JSON branch.
	req := httptest.NewRequest(http.MethodGet, "/pql/all+posts+by+date?format=json", nil)
	w := httptest.NewRecorder()
	s.handleStreamPQL(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&body)
	if body["version"] != "pql.v1" {
		t.Errorf("expected JSON envelope, got %v", body)
	}
}

// htmlShellRequest hits the HTML branch (no Accept: application/json).
func htmlShellRequest(t *testing.T, s *Server, pathTail string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/pql/"+pathTail, nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	s.handleStreamPQL(w, req)
	return w.Code, w.Body.String()
}

func TestStreamPQL_HTMLShell_SeedsNonDefault(t *testing.T) {
	s := newConfiguredServer(t)
	// Write a minimal rendered shell the HTML branch can serve.
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"),
		[]byte("<html><head><title>x</title></head><body>stream</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-default (comments) → shell carries the injected seed.
	code, body := htmlShellRequest(t, s, "all+comments+by+date")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(body, seedOpen) {
		t.Errorf("expected seed injection for comments view, body: %s", body)
	}
	if !strings.Contains(body, "\"type\":\"comments\"") {
		t.Errorf("expected type=comments in seed, body: %s", body)
	}

	// Default (posts/by-date at tenant scope) → no seed (the shell IS that view).
	code2, body2 := htmlShellRequest(t, s, "all+posts+by+date")
	if code2 != http.StatusOK {
		t.Fatalf("expected 200, got %d", code2)
	}
	if strings.Contains(body2, "polis-initial-pql") {
		t.Errorf("default posts view must not inject a seed, body: %s", body2)
	}
}

// A sentence with a relation keeps it in the seed: without it "all comments
// on <post>" hydrates as "all comments from <post>", which the server refuses.
func TestStreamPQL_HTMLShell_SeedKeepsTheRelation(t *testing.T) {
	s := newConfiguredServer(t)
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"),
		[]byte("<html><head><title>x</title></head><body>stream</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	post := "https://alice.polis.pub/posts/20261113/Hello.html"
	for _, tc := range []struct{ tail, relation, scope string }{
		{"all+comments+on+https:%2F%2Falice.polis.pub%2Fposts%2F20261113%2FHello.html", "on", post},
		{"all+blessed+comments+on+https:%2F%2Falice.polis.pub%2Fposts%2F20261113%2FHello.html", "on", post},
		{"all+activity+about+me", "about", "me"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/pql/"+tc.tail, nil)
		req.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		s.handleStreamPQL(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", tc.tail, w.Code, w.Body.String())
		}
		body := w.Body.String()
		i := strings.Index(body, seedOpen)
		if i < 0 {
			t.Fatalf("%s: no seed in %s", tc.tail, body)
		}
		raw := body[i+len(seedOpen):]
		raw = raw[:strings.Index(raw, "</script>")]
		var seed map[string]string
		if err := json.Unmarshal([]byte(raw), &seed); err != nil {
			t.Fatalf("%s: seed is not JSON: %v (%s)", tc.tail, err, raw)
		}
		if seed["relation"] != tc.relation || seed["scope"] != tc.scope {
			t.Errorf("%s: seed relation %q scope %q, want %q %q", tc.tail, seed["relation"], seed["scope"], tc.relation, tc.scope)
		}
	}
}

func TestStreamPQL_HTMLShell_InjectsBaseHref(t *testing.T) {
	s := newConfiguredServer(t)
	// Shell with a bare relative stylesheet ref — exactly what breaks under
	// /pql/<sentence> without a <base>.
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"),
		[]byte(`<html><head><link rel="stylesheet" href="base.css"></head><body>stream</body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Both the default and a seeded view must carry the <base> so relative
	// assets resolve against the site root, not /pql/.
	for _, tail := range []string{"all+posts+by+date", "all+comments+by+date"} {
		code, body := htmlShellRequest(t, s, tail)
		if code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", tail, code)
		}
		if !strings.Contains(body, `<base href="/">`) {
			t.Errorf("%s: expected <base href=\"/\"> injected, body: %s", tail, body)
		}
		// The base must precede the relative <link> so the browser applies it.
		if strings.Index(body, `<base href="/">`) > strings.Index(body, `href="base.css"`) {
			t.Errorf("%s: <base> must precede relative <link>", tail)
		}
	}
}

func TestStreamPQL_HTMLShell_404WhenNoShell(t *testing.T) {
	s := newConfiguredServer(t) // no index.html written
	code, _ := htmlShellRequest(t, s, "all+posts+by+date")
	if code != http.StatusNotFound {
		t.Errorf("expected 404 when no rendered shell exists, got %d", code)
	}
}

func TestStreamPQL_Malformed(t *testing.T) {
	s := newConfiguredServer(t)
	code, body := pqlRequest(t, s, "all+from+my+network", "application/json") // missing type
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
	if body["error"] == nil {
		t.Errorf("expected error message, got %v", body)
	}
}

// The tenant serves "about" only for the owner's own activity; any other
// "about" belongs to the discovery service's /pql/.
func TestStreamPQL_AboutRelationOnlyForTheOwnersActivity(t *testing.T) {
	s := newConfiguredServer(t)
	if code, body := pqlRequest(t, s, "all+activity+about+me", "application/json"); code != http.StatusOK {
		t.Errorf("all activity about me: got %d (%v), want 200", code, body)
	}
	for _, tail := range []string{"all+activity+about+alice.example", "all+comments+about+me"} {
		if code, body := pqlRequest(t, s, tail, "application/json"); code != http.StatusBadRequest {
			t.Errorf("%s: got %d (%v), want 400", tail, code, body)
		}
	}
}

func TestStreamPQL_FQEventTypeRejectedOnTenant(t *testing.T) {
	s := newConfiguredServer(t)
	code, body := pqlRequest(t, s, "all+pub.polis.follow.announced+from+alice.polis.pub", "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for FQ event type, got %d (%v)", code, body)
	}
}

func TestStreamPQL_ExplicitScopePassesThrough(t *testing.T) {
	s := newConfiguredServer(t)
	code, body := pqlRequest(t, s, "all+comments+from+all+polis+to+bless", "application/json")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	parsed, _ := body["parsed"].(map[string]interface{})
	if parsed["scope"] != "all-polis" || parsed["modifier"] != "to-bless" {
		t.Errorf("parsed = %v", parsed)
	}
}

func TestStreamPQL_UnderlyingErrorPassThrough(t *testing.T) {
	s := newConfiguredServer(t)
	// drafts require scope=me; my-network must surface the handler's 400.
	code, body := pqlRequest(t, s, "all+drafts+from+my+network", "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 passthrough, got %d (%v)", code, body)
	}
}

// A shell that marks the place (v5's index.html) says, on a view other than
// its own, that the view needs script; the default view, and a shell without
// the mark (v4's), are served as they were.
func TestStreamPQL_HTMLShell_SaysAViewNeedsScript(t *testing.T) {
	s := newConfiguredServer(t)
	marked := "<html><head><title>x</title></head><body><main>" + viewNeedsScriptMark + "posts</main></body></html>"
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"), []byte(marked), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, body := htmlShellRequest(t, s, "all+comments+by+date"); !strings.Contains(body, "<noscript><p class=\"needs-script\">") || strings.Contains(body, viewNeedsScriptMark) {
		t.Errorf("a view's page should carry the note in place of the mark, body: %s", body)
	}
	if _, body := htmlShellRequest(t, s, "all+posts+by+date"); strings.Contains(body, "<noscript>") {
		t.Errorf("the default view is the page itself and needs no note, body: %s", body)
	}

	unmarked := "<html><head><title>x</title></head><body>stream</body></html>"
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"), []byte(unmarked), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, body := htmlShellRequest(t, s, "all+comments+by+date"); strings.Contains(body, "<noscript>") {
		t.Errorf("a shell without the mark is served as it was, body: %s", body)
	}
}

// seedOpen opens the view's filter in a shell: a JSON data block.
const seedOpen = `<script type="application/json" id="polis-initial-pql">`

// The view's filter reaches the page as data, never as an inline script:
// tenant pages are served with script-src 'self', under which an inline
// script never runs (and a view's address then showed the default posts).
// Every <script> the handler adds is a data block or has a src.
func TestStreamPQL_HTMLShell_AddsNoInlineScript(t *testing.T) {
	s := newConfiguredServer(t)
	shell := "<html><head><title>x</title></head><body>stream</body></html>"
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"), []byte(shell), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tail := range []string{"all+comments+by+date", "all+posts+with+comments", "all+profiles+from+my+network"} {
		_, body := htmlShellRequest(t, s, tail)
		for _, tag := range scriptTags.FindAllString(body, -1) {
			if !strings.Contains(tag, `type="application/json"`) && !strings.Contains(tag, " src=") {
				t.Errorf("%s: an inline script reached the page, which the CSP will not run: %s", tail, tag)
			}
		}
		if !strings.Contains(body, seedOpen) {
			t.Errorf("%s: the view's filter is not in the page", tail)
		}
	}
}

var scriptTags = regexp.MustCompile(`<script\b[^>]*>`)

// The root serves the home page, with 200 and no redirect, to a browser and a
// crawler alike (v5 spec 4.2, SEO rule 5; every shape). It used to answer
// HTML clients with a 302 to /pql/all+posts+by+date, whose canonical was /:
// a loop for search engines.
func TestTenantRoot_ServesTheHomePage(t *testing.T) {
	s := newConfiguredServer(t)
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"), []byte("<html><head></head><body>home</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := tenantStaticHandler(s)
	for _, accept := range []string{"text/html,application/xhtml+xml", ""} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
		w := httptest.NewRecorder()
		h(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "home") {
			t.Errorf("Accept %q: / answered %d, want 200 with the home page", accept, w.Code)
		}
	}
}

// /index.html is the home page under a second address: it redirects to /, so a
// page has one address, keeping the query, and nothing in the query can send
// the reader anywhere else.
func TestTenantRoot_IndexRedirectNeverLeavesTheSite(t *testing.T) {
	s := newConfiguredServer(t)
	h := tenantStaticHandler(s)
	for q, want := range map[string]string{
		"":                   "/",
		"search=agent":       "/?search=agent",
		"//evil.example":     "/?//evil.example",
		"x=@evil.example":    "/?x=@evil.example",
		"%2F%2Fevil.example": "/?%2F%2Fevil.example",
	} {
		target := "/index.html"
		if q != "" {
			target += "?" + q
		}
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		h(w, req)
		loc := w.Header().Get("Location")
		if w.Code != http.StatusMovedPermanently || loc != want {
			t.Errorf("%s: %d %q, want 301 %q", target, w.Code, loc, want)
		}
		if u, err := url.Parse(loc); err != nil || u.Host != "" || u.Scheme != "" || u.Path != "/" {
			t.Errorf("%s redirected off the home page: %q", target, loc)
		}
	}
}

// A view's address is navigation, not content: "noindex, follow" (v5 spec
// 4.2, SEO rule 6), on the default view and every other.
func TestStreamPQL_HTMLShell_IsNoindexFollow(t *testing.T) {
	s := newConfiguredServer(t)
	if err := os.WriteFile(filepath.Join(s.DataDir, "index.html"), []byte("<html><head><title>x</title></head><body>stream</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tail := range []string{"all+posts+by+date", "all+comments+by+date"} {
		_, body := htmlShellRequest(t, s, tail)
		if !strings.Contains(body, `<meta name="robots" content="noindex, follow">`) {
			t.Errorf("%s: the view's page is not noindex, follow", tail)
		}
	}
}

// A PQL read that names no window gets the widest one the server allows, not
// the stream endpoint's 24h; a named window is still honoured, and the count
// reads the same window as the list.
func TestStreamPQL_NoWindowReadsTheWidestAndCountsFollowTheWindow(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "Recent", URL: "posts/r.md", Published: now.Add(-30 * time.Minute).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "LastWeek", URL: "posts/w.md", Published: now.Add(-6 * 24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "TooOld", URL: "posts/o.md", Published: now.Add(-40 * 24 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})
	titles := func(body map[string]interface{}) []string {
		var out []string
		items, _ := body["items"].([]interface{})
		for _, it := range items {
			out = append(out, it.(map[string]interface{})["title"].(string))
		}
		return out
	}
	count := func(body map[string]interface{}) interface{} {
		c, _ := body["counts"].(map[string]interface{})
		return c["posts"]
	}

	code, body := pqlRequest(t, s, "all+posts+from+my+network+by+date?counts=posts", "application/json")
	if code != http.StatusOK {
		t.Fatalf("no window: %d %v", code, body)
	}
	if got := titles(body); len(got) != 2 || got[0] != "Recent" || got[1] != "LastWeek" {
		t.Errorf("no window: got %v, want [Recent LastWeek] (30 days, not 24h)", got)
	}
	if n := count(body); n != float64(2) {
		t.Errorf("no window: counts.posts = %v, want 2", n)
	}

	_, body = pqlRequest(t, s, "all+posts+from+my+network+by+date?time=24h&counts=posts", "application/json")
	if got := titles(body); len(got) != 1 || got[0] != "Recent" {
		t.Errorf("time=24h: got %v, want [Recent]", got)
	}
	if n := count(body); n != float64(1) {
		t.Errorf("time=24h: counts.posts = %v, want 1 (the count reads the list's window)", n)
	}
}

// ⭐ A COUNT THAT IS THE CACHE'S LIMIT IS A LOWER BOUND, AND SAYS SO. The
// owner's feed cache keeps the newest posts up to a bound; a network that
// outposts it (discover.polis.pub alone does, and every polis.pub site follows
// it from signup) fills it, and the count then reads the bound forever. It is
// listed in counts_capped, so the filter can read "300+ posts".
func TestStreamPQL_ACountThatFillsTheCacheIsALowerBound(t *testing.T) {
	s := newConfiguredServer(t)
	if err := s.feedCacheForScope("network").SaveConfig(&feed.FeedConfig{MaxPosts: 2}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "A", URL: "posts/a.md", Published: now.Add(-1 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "B", URL: "posts/b.md", Published: now.Add(-50 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
		{Type: "post", Title: "C", URL: "posts/c.md", Published: now.Add(-60 * time.Hour).Format(time.RFC3339), AuthorURL: "https://a.pub", AuthorDomain: "a.pub"},
	})
	capped := func(body map[string]interface{}) bool {
		list, _ := body["counts_capped"].([]interface{})
		for _, w := range list {
			if w == "posts" {
				return true
			}
		}
		return false
	}

	_, body := pqlRequest(t, s, "all+posts+from+my+network+by+date?counts=posts", "application/json")
	if c, _ := body["counts"].(map[string]interface{}); c["posts"] != float64(2) {
		t.Fatalf("counts.posts = %v, want 2 (the cache kept two)", c["posts"])
	}
	if !capped(body) {
		t.Errorf("a count at the cache's limit is not in counts_capped: %v", body["counts_capped"])
	}

	_, body = pqlRequest(t, s, "all+posts+from+my+network+by+date?time=24h&counts=posts", "application/json")
	if capped(body) {
		t.Errorf("a count under the cache's limit (one post in 24h) is in counts_capped")
	}
}
