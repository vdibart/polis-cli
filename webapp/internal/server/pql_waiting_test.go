package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// waitingDS answers a post's comments, and the blessing records by status:
// the pending ones (the post author's waiting list) apart from the granted.
// The answers can be set after the server starts.
type waitingDS struct {
	mu                         sync.Mutex
	comments, pending, granted string
	srv                        *httptest.Server
}

func newWaitingDS(t *testing.T) *waitingDS {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	d := &waitingDS{comments: `{"count":0,"records":[]}`, pending: `{"records":[]}`, granted: `{"records":[]}`}
	signed := func(w http.ResponseWriter, raw string) {
		var body map[string]interface{}
		_ = json.Unmarshal([]byte(raw), &body)
		sig, _ := signing.SignContent([]byte(discovery.BuildCanonicalJSON(body)), priv)
		body["ds_signature"] = sig
		body["ds_key_id"] = "test-ds"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		switch {
		case r.URL.Path == "/.well-known/polis":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"public_key": string(pub), "key_id": "test-ds"})
		case r.URL.Path == "/v1/content":
			signed(w, d.comments)
		case r.URL.Path == "/v1/relationships" && r.URL.Query().Get("status") == "pending":
			signed(w, d.pending)
		case r.URL.Path == "/v1/relationships":
			signed(w, d.granted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *waitingDS) set(comments, pending, granted string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.comments, d.pending, d.granted = comments, pending, granted
}

func queryItems(t *testing.T, h http.Handler, path, token string) []map[string]any {
	t.Helper()
	w := get(t, h, path, token)
	if w.Code != http.StatusOK {
		t.Fatalf("%s = %d %s", path, w.Code, w.Body.String())
	}
	var env struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return env.Items
}

// ⭐ "all comments on <post> to bless" is the comments waiting for the post
// author's decision, from the author's own pending list: not the blessed, not
// the turned away, not a request on another post. Only the owner has waiting
// comments to see on their own site; anyone else gets nothing.
func TestToBlessOnAPostIsOnlyItsWaitingComments(t *testing.T) {
	s, h := queryRoutes(t)
	ds := newWaitingDS(t)
	s.DiscoveryURL = ds.srv.URL
	base := strings.TrimRight(s.GetBaseURL(), "/")
	post := base + "/posts/20261113/mine.md"
	other := base + "/posts/20261114/other.md"
	blessed := "https://bob.example/comments/20261113/one.md"
	waiting := "https://carol.example/comments/20261113/two.md"
	uncached := "https://dan.example/comments/20261113/three.md" // in the pending list, missed by the content records
	elsewhere := "https://erin.example/comments/20261114/four.md"
	ds.set(
		fmt.Sprintf(`{"count":2,"records":[{"id":1,"type":"pub.polis.comment","url":%q,"actor":"bob.example","created_at":"2026-11-13T10:00:00Z","metadata":{"in_reply_to":%q}},{"id":2,"type":"pub.polis.comment","url":%q,"actor":"carol.example","version":"sha256:c","created_at":"2026-11-13T11:00:00Z","metadata":{"in_reply_to":%q}}]}`, blessed, post, waiting, post),
		fmt.Sprintf(`{"records":[{"id":21,"type":"pub.polis.comment.blessing","source_url":%q,"target_url":%q,"actor":"carol.example","status":"pending","created_at":"2026-11-13T11:00:01Z","metadata":{"comment_version":"sha256:c"}},{"id":22,"type":"pub.polis.comment.blessing","source_url":%q,"target_url":%q,"actor":"dan.example","status":"pending","created_at":"2026-11-13T12:00:00Z"},{"id":23,"type":"pub.polis.comment.blessing","source_url":%q,"target_url":%q,"actor":"erin.example","status":"pending","created_at":"2026-11-14T10:00:00Z"}]}`, waiting, post, uncached, post, elsewhere, other),
		fmt.Sprintf(`{"records":[{"id":9,"type":"pub.polis.comment.blessing","source_url":%q,"target_url":%q,"actor":"test-site.polis.pub","status":"granted"}]}`, blessed, post),
	)
	q := "/v1/query?pql=all+comments+on+" + post + "+to+bless"

	got := queryItems(t, h, q, mint(t, s, "read"))
	if len(got) != 2 || got[0]["url"] != waiting || got[1]["url"] != uncached {
		t.Fatalf("the owner's waiting comments = %v, want carol's then dan's", got)
	}
	for _, it := range got {
		if it["blessed"] != false {
			t.Errorf("%v marked blessed = %v", it["url"], it["blessed"])
		}
	}
	if got[0]["id"] != "2" || got[0]["version"] != "sha256:c" || got[0]["author_domain"] != "carol.example" {
		t.Errorf("a waiting comment the records hold is drawn from them: %v", got[0])
	}
	if got[1]["author_domain"] != "dan.example" || got[1]["published"] != "2026-11-13T12:00:00Z" {
		t.Errorf("a waiting comment the records missed is drawn from the request: %v", got[1])
	}
	// Anyone else: nothing, signed out or with a token that cannot read.
	if n := len(queryItems(t, h, q, "")); n != 0 {
		t.Errorf("a visitor sees %d waiting comments, want 0", n)
	}
	if n := len(queryItems(t, h, q, mint(t, s, "publish"))); n != 0 {
		t.Errorf("a publish token sees %d waiting comments, want 0", n)
	}
	// Another site's post has no waiting comments here.
	if n := len(queryItems(t, h, "/v1/query?pql=all+comments+on+https://alice.example/posts/20261113/x.md+to+bless", mint(t, s, "read"))); n != 0 {
		t.Errorf("another site's post: %d waiting, want 0", n)
	}
}

// ⭐ Each of the owner's posts carries how many comments wait on it, the same
// number "all comments on <post> to bless" returns for it; a post with none
// waiting leaves the count out, and only the owner sees it.
func TestEachPostCarriesItsWaitingCountTheSameAsItsWaitingList(t *testing.T) {
	s, h := queryRoutes(t)
	full := mint(t, s)
	publishThroughAPI(t, h, full, 2)
	posts := queryItems(t, h, "/v1/query?pql=all+posts+from+me", full)
	if len(posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(posts))
	}
	// An own post's item carries its site-relative path; a request names the
	// post by its full address.
	base := strings.TrimRight(s.GetBaseURL(), "/")
	busyPath, quietPath := fmt.Sprint(posts[0]["url"]), fmt.Sprint(posts[1]["url"])
	busy := polisurl.NormalizeToMD(base + busyPath)

	ds := newWaitingDS(t)
	s.DiscoveryURL = ds.srv.URL
	ds.set(`{"count":0,"records":[]}`,
		fmt.Sprintf(`{"records":[{"id":31,"type":"pub.polis.comment.blessing","source_url":"https://a.example/comments/20261113/a.md","target_url":%q,"actor":"a.example","status":"pending","created_at":"2026-11-13T10:00:00Z"},{"id":32,"type":"pub.polis.comment.blessing","source_url":"https://b.example/comments/20261113/b.md","target_url":%q,"actor":"b.example","status":"pending","created_at":"2026-11-13T11:00:00Z"}]}`, busy, busy),
		`{"records":[]}`)

	waitingOf := map[string]any{}
	for _, p := range queryItems(t, h, "/v1/query?pql=all+posts+from+me", full) {
		waitingOf[fmt.Sprint(p["url"])] = p["waiting"]
	}
	list := queryItems(t, h, "/v1/query?pql=all+comments+on+"+busy+"+to+bless", full)
	if len(list) != 2 {
		t.Fatalf("waiting list = %d, want 2", len(list))
	}
	if waitingOf[busyPath] != float64(len(list)) {
		t.Errorf("waiting count = %v, want %d (the list's length); %v", waitingOf[busyPath], len(list), waitingOf)
	}
	if v, ok := waitingOf[quietPath]; !ok || v != nil {
		t.Errorf("a post with none waiting: waiting = %v (present %v), want it left out", v, ok)
	}
	// A visitor sees no waiting counts.
	for _, p := range queryItems(t, h, "/v1/query?pql=all+posts+from+"+extractDomainFromURL(s.GetBaseURL()), "") {
		if p["waiting"] != nil {
			t.Errorf("a visitor sees waiting = %v on %v", p["waiting"], p["url"])
		}
	}
}

// ⭐ A comment in a thread carries its words (comment_body_html): an item that
// names only its writer can't be read, blessed or decided. The owner's own
// comment is read from the site's own sources, rendered and sanitised.
func TestAThreadsCommentsCarryTheirWords(t *testing.T) {
	s, h := queryRoutes(t)
	ds := newWaitingDS(t)
	s.DiscoveryURL = ds.srv.URL
	base := strings.TrimRight(s.GetBaseURL(), "/")
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	post := "https://bob.example/posts/20261113/theirs.md"
	mine := base + "/comments/20261113/reply.md"
	if err := s.Site().Store().Put(t.Context(), "content/pub.polis.core/comment/20261113/reply.md",
		[]byte("---\ntitle: Re\n---\nHello **there**.\n<script>alert(1)</script>\n")); err != nil {
		t.Fatal(err)
	}
	ds.set(
		fmt.Sprintf(`{"count":1,"records":[{"id":1,"type":"pub.polis.comment","url":%q,"actor":%q,"created_at":"2026-11-13T10:00:00Z","metadata":{"in_reply_to":%q}}]}`, mine, host, post),
		`{"records":[]}`, `{"records":[]}`,
	)
	items := queryItems(t, h, "/v1/query?pql=all+comments+on+"+post, mint(t, s, "read"))
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	words, _ := items[0]["comment_body_html"].(string)
	if !strings.Contains(words, "<strong>there</strong>") {
		t.Errorf("comment_body_html = %q, want the comment's words rendered", words)
	}
	if strings.Contains(words, "<script") {
		t.Errorf("comment_body_html = %q: a script survived the sanitiser", words)
	}
}
