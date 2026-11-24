package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
)

// ⛔ /api/v1/stream/body makes the site fetch the address it is given, and it
// is public (the v4 stream calls it). It was anyone's fetching proxy: any
// visitor could make any site fetch any public address and keep the result in
// its cache. Now the owner may ask for any post, as post.body on /v1 allows,
// and anyone else only this site's posts or a followed site's.
func TestTheStreamBodyRouteIsNoOnesFetchingProxy(t *testing.T) {
	s := newConfiguredServer(t)
	s.Logger = NewStdoutLogger(LogLevelBasic)
	// ⛔ No network: an allowed fetch is answered here, never by the real host.
	orig := render.DefaultHTTPClient
	render.DefaultHTTPClient = &http.Client{Transport: focusBodyRoundTripper{body: "a post"}}
	t.Cleanup(func() { render.DefaultHTTPClient = orig })
	ff := &following.FollowingFile{Version: "1"}
	ff.Add("https://alice.polis.pub")
	if err := following.Save(s.Site(), ff); err != nil {
		t.Fatal(err)
	}

	ask := func(addr string) (refused, fetched bool, hasBody bool) {
		var body streamBodyResponse
		events := captureEvents(t, func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/body?url="+url.QueryEscape(addr), nil)
			w := httptest.NewRecorder()
			s.handleStreamBody(w, req) // no session: a visitor
			if w.Code != http.StatusOK {
				t.Fatalf("%s: status %d", addr, w.Code)
			}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
		})
		return len(eventsNamed(events, "pub.polis.stream.body_refused")) == 1,
			len(eventsNamed(events, "pub.polis.stream.body")) == 1, body.HasBody
	}

	// An arbitrary address: refused before anything is fetched.
	refused, fetched, has := ask("https://evil.example/posts/20261121/x.html")
	if !refused || fetched || has {
		t.Errorf("arbitrary address: refused=%v fetched=%v has_body=%v; want refused, nothing fetched", refused, fetched, has)
	}
	// A look-alike host is not a followed one.
	if refused, _, _ := ask("https://alice.polis.pub.evil.example/posts/x.html"); !refused {
		t.Error("a host that merely starts with a followed one was allowed")
	}
	// This site's own post and a followed site's post reach the fetch.
	for _, addr := range []string{
		"https://test-site.polis.pub/posts/20261121/mine.html",
		"https://alice.polis.pub/posts/20261121/theirs.html",
	} {
		if refused, fetched, _ := ask(addr); refused || !fetched {
			t.Errorf("%s: refused=%v fetched=%v; want it allowed", addr, refused, fetched)
		}
	}
}

// The owner, signed in, may have the site fetch any post (the v4 app opens
// posts from sites it does not follow), as post.body on /v1 allows.
func TestTheOwnerMayReadAnyPostThroughTheStreamBodyRoute(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.Custodied // polis.pub: the front door marks the owner's session
	s.Logger = NewStdoutLogger(LogLevelBasic)
	orig := render.DefaultHTTPClient
	render.DefaultHTTPClient = &http.Client{Transport: focusBodyRoundTripper{body: "a post"}}
	t.Cleanup(func() { render.DefaultHTTPClient = orig })

	events := captureEvents(t, func() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/body?url="+url.QueryEscape("https://stranger.example/posts/x.html"), nil)
		req.Header.Set(OwnerSessionHeader, "1")
		s.handleStreamBody(httptest.NewRecorder(), req)
	})
	if len(eventsNamed(events, "pub.polis.stream.body_refused")) != 0 || len(eventsNamed(events, "pub.polis.stream.body")) != 1 {
		t.Errorf("the owner's read of an unfollowed site's post was refused: %v", events)
	}
}
