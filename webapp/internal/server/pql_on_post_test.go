package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// threadDS answers a post's comments and its granted blessings, signed as a
// real discovery service signs its query responses.
func threadDS(t *testing.T, comments, granted string) *httptest.Server {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signed := func(w http.ResponseWriter, raw string) {
		var body map[string]interface{}
		_ = json.Unmarshal([]byte(raw), &body)
		sig, _ := signing.SignContent([]byte(discovery.BuildCanonicalJSON(body)), priv)
		body["ds_signature"] = sig
		body["ds_key_id"] = "test-ds"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/.well-known/polis":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"public_key": string(pub), "key_id": "test-ds"})
		case r.URL.Path == "/v1/content" && r.URL.Query().Get("metadata.in_reply_to") != "":
			signed(w, comments)
		case r.URL.Path == "/v1/relationships":
			signed(w, granted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ⭐ "all comments on <post>" is every comment on the post, oldest first, each
// marked blessed or not; blessing never filters it. "all blessed comments on
// <post>" keeps only the blessed ones. Both are public.
func TestCommentsOnAPostAreAllShownAndMarked(t *testing.T) {
	const post = "https://alice.example/posts/20261113/Hello.md"
	c1 := "https://bob.example/comments/20261113/one.md"
	c2 := "https://carol.example/comments/20261114/two.md"
	ds := threadDS(t,
		fmt.Sprintf(`{"count":2,"records":[{"id":2,"type":"pub.polis.comment","url":%q,"actor":"carol.example","version":"sha256:b","created_at":"2026-11-14T10:00:00Z","metadata":{"in_reply_to":%q}},{"id":1,"type":"pub.polis.comment","url":%q,"actor":"bob.example","version":"sha256:a","created_at":"2026-11-13T10:00:00Z","metadata":{"in_reply_to":%q}}]}`, c2, post, c1, post),
		fmt.Sprintf(`{"records":[{"id":9,"type":"pub.polis.comment.blessing","source_url":%q,"target_url":%q,"actor":"alice.example","status":"granted"}]}`, c1, post),
	)
	s, h := queryRoutes(t)
	s.DiscoveryURL = ds.URL

	read := func(path string) []map[string]any {
		t.Helper()
		w := get(t, h, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, w.Code, w.Body.String())
		}
		var env struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Items
	}
	all := read("/v1/query?pql=all+comments+on+" + post)
	if len(all) != 2 || all[0]["url"] != c1 || all[1]["url"] != c2 {
		t.Fatalf("all comments = %v, want both, oldest first", all)
	}
	if all[0]["blessed"] != true || all[1]["blessed"] != false {
		t.Errorf("marks = %v, %v; want blessed then not", all[0]["blessed"], all[1]["blessed"])
	}
	// Each comment names who wrote it, as 'from' sentences do. The DS sends
	// the actor as a bare domain, which used to come back as no author at all.
	if all[0]["author_domain"] != "bob.example" || all[0]["author_url"] != "https://bob.example" ||
		all[1]["author_domain"] != "carol.example" || all[1]["author_url"] != "https://carol.example" {
		t.Errorf("authors = %v %v, %v %v", all[0]["author_domain"], all[0]["author_url"], all[1]["author_domain"], all[1]["author_url"])
	}
	only := read("/v1/query?pql=all+blessed+comments+on+" + post)
	if len(only) != 1 || only[0]["url"] != c1 {
		t.Errorf("blessed only = %v, want bob's", only)
	}
	// The path's case survived the sentence.
	if !strings.Contains(fmt.Sprint(all[0]["target_url"]), "/Hello.md") {
		t.Errorf("target_url = %v", all[0]["target_url"])
	}
	// The path form: the address's slashes encoded, so no router collapses
	// "//" and redirects. The envelope's own url comes back the same way.
	encoded := strings.ReplaceAll(post, "/", "%2F")
	w := get(t, h, "/pql/all+comments+on+"+encoded, "")
	if w.Code != http.StatusOK {
		t.Fatalf("path form = %d %s (Location %q)", w.Code, w.Body.String(), w.Header().Get("Location"))
	}
	var env struct {
		URL   string           `json:"url"`
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Items) != 2 || env.URL != "/pql/all+comments+on+"+encoded {
		t.Errorf("path form: %d items, url %q", len(env.Items), env.URL)
	}
	if w := get(t, h, "/v1/query?pql=all+posts+on+"+post, ""); w.Code != http.StatusBadRequest {
		t.Errorf("posts on a post = %d, want 400", w.Code)
	}
}

// ⛔ THE CORE PROMISE: on a site's own posts, comments that aren't blessed
// never appear to anyone but the owner, on any surface the site serves. The
// owner, reading with a token that may read their content, sees everything.
func TestOnTheSitesOwnPostsOnlyBlessedCommentsAppear(t *testing.T) {
	s, h := queryRoutes(t)
	post := strings.TrimRight(s.GetBaseURL(), "/") + "/posts/20261113/mine.md"
	blessedOne := "https://bob.example/comments/20261113/one.md"
	notBlessed := "https://spam.example/comments/20261113/two.md"
	ds := threadDS(t,
		fmt.Sprintf(`{"count":2,"records":[{"id":1,"type":"pub.polis.comment","url":%q,"actor":"bob.example","created_at":"2026-11-13T10:00:00Z","metadata":{"in_reply_to":%q}},{"id":2,"type":"pub.polis.comment","url":%q,"actor":"spam.example","created_at":"2026-11-13T11:00:00Z","metadata":{"in_reply_to":%q}}]}`, blessedOne, post, notBlessed, post),
		fmt.Sprintf(`{"records":[{"id":9,"type":"pub.polis.comment.blessing","source_url":%q,"target_url":%q,"actor":"test-site.polis.pub","status":"granted"}]}`, blessedOne, post),
	)
	s.DiscoveryURL = ds.URL
	urls := func(path, token string) []string {
		t.Helper()
		w := get(t, h, path, token)
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, w.Code, w.Body.String())
		}
		var env struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		var out []string
		for _, it := range env.Items {
			out = append(out, fmt.Sprint(it["url"]))
		}
		return out
	}
	encoded := strings.ReplaceAll(post, "/", "%2F")
	for _, path := range []string{"/v1/query?pql=all+comments+on+" + post, "/pql/all+comments+on+" + encoded} {
		if got := urls(path, ""); len(got) != 1 || got[0] != blessedOne {
			t.Errorf("a visitor on %s sees %v, want only the blessed comment", path, got)
		}
	}
	// A token that can't read the owner's content is a visitor too.
	if got := urls("/v1/query?pql=all+comments+on+"+post, mint(t, s, "publish")); len(got) != 1 {
		t.Errorf("publish-scoped token sees %v, want only the blessed comment", got)
	}
	// The owner sees everything, marked.
	if got := urls("/v1/query?pql=all+comments+on+"+post, mint(t, s, "read")); len(got) != 2 {
		t.Errorf("the owner sees %v, want both", got)
	}
}

// ⛔ The same promise on polis.pub, where the operator provider admits every
// request: a visitor still sees only blessed comments; only the front door's
// owner-session mark (or a read token) shows the rest.
func TestOnTheSitesOwnPostsOnlyBlessedCommentsAppearOnHosted(t *testing.T) {
	s, h := queryRoutes(t)
	s.Mode = capability.ForHosted()
	post := strings.TrimRight(s.GetBaseURL(), "/") + "/posts/20261113/mine.md"
	blessedOne := "https://bob.example/comments/20261113/one.md"
	notBlessed := "https://spam.example/comments/20261113/two.md"
	ds := threadDS(t,
		fmt.Sprintf(`{"count":2,"records":[{"id":1,"type":"pub.polis.comment","url":%q,"actor":"bob.example","created_at":"2026-11-13T10:00:00Z","metadata":{"in_reply_to":%q}},{"id":2,"type":"pub.polis.comment","url":%q,"actor":"spam.example","created_at":"2026-11-13T11:00:00Z","metadata":{"in_reply_to":%q}}]}`, blessedOne, post, notBlessed, post),
		fmt.Sprintf(`{"records":[{"id":9,"type":"pub.polis.comment.blessing","source_url":%q,"target_url":%q,"actor":"test-site.polis.pub","status":"granted"}]}`, blessedOne, post),
	)
	s.DiscoveryURL = ds.URL
	count := func(path string, header bool) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "application/json")
		if header {
			req.Header.Set(OwnerSessionHeader, "1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var env struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return len(env.Items)
	}
	encoded := strings.ReplaceAll(post, "/", "%2F")
	if n := count("/pql/all+comments+on+"+encoded, false); n != 1 {
		t.Errorf("hosted visitor sees %d comments, want only the blessed one", n)
	}
	if n := count("/v1/query?pql=all+comments+on+"+post, false); n != 1 {
		t.Errorf("hosted visitor via /v1 sees %d, want 1", n)
	}
	if n := count("/pql/all+comments+on+"+encoded, true); n != 2 {
		t.Errorf("hosted owner (front-door mark) sees %d, want both", n)
	}
}

// A trailing dot on the host is the same host: the own-post rule still holds.
func TestOwnPostRuleIgnoresATrailingDot(t *testing.T) {
	s := newConfiguredServer(t)
	host := strings.TrimPrefix(strings.TrimPrefix(s.GetBaseURL(), "https://"), "http://")
	host = strings.TrimRight(host, "/")
	for _, p := range []string{"https://" + host + "/posts/x.md", "https://" + strings.ToUpper(host) + "./posts/x.md"} {
		if !s.isOwnPost(p) {
			t.Errorf("%s not recognised as the site's own post", p)
		}
	}
	if s.isOwnPost("https://other.example/posts/x.md") {
		t.Error("another site's post taken for this site's")
	}
}
