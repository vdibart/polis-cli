package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// ⭐ OBSERVABILITY IS THE ACTION'S, THE SAME WHOEVER WRITES.
//
// A post published through /v1 used to write no pub.polis.post.publish row,
// and the act log recorded an agent's writes and never the owner's. Now a
// write logs one domain event with one field set (request id, operation,
// surface, the agent's marker when there is one), the owner's own write is in
// the act log under the same request id, and an error is logged once with its
// code and the operation it refused.

func observedServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, h := queryRoutes(t)
	s.Logger = NewStdoutLogger(LogLevelBasic)
	return s, h
}

func eventsNamed(events []map[string]interface{}, action string) []map[string]interface{} {
	var out []map[string]interface{}
	for _, e := range events {
		if e["action"] == action {
			out = append(out, e)
		}
	}
	return out
}

func TestAPostPublishedThroughTheAPIIsLoggedOnceWithTheFieldSet(t *testing.T) {
	s, h := observedServer(t)
	owner := mint(t, s)
	var code int
	var made map[string]any
	events := captureEvents(t, func() {
		code, made = apiCall(t, h, "POST", "/v1/content/post", owner, `{"markdown":"# Logged\n\nonce"}`)
	})
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, made)
	}
	pub := eventsNamed(events, "pub.polis.post.publish")
	if len(pub) != 1 {
		t.Fatalf("pub.polis.post.publish rows = %d, want exactly one\n%v", len(pub), events)
	}
	e := pub[0]
	if e["op"] != "pub.polis.post.create" || e["surface"] != "token" || e["result"] != "ok" ||
		e["path"] != made["path"] || e["title"] != "Logged" || e["site"] != "test-site.polis.pub" {
		t.Errorf("event = %v", e)
	}
	if _, ok := e["agent"]; ok {
		t.Error("the owner's own write is logged with an agent")
	}
	// The API's audit row for the write carries the same field set.
	if w := eventsNamed(events, "pub.polis.api.write"); len(w) != 1 || w[0]["op"] != "pub.polis.post.create" ||
		w[0]["surface"] != "token" || w[0]["request_id"] != e["request_id"] {
		t.Errorf("pub.polis.api.write rows = %v, want one with the write's op, surface and request id", w)
	}

	// The owner's write is in the act log, unmarked, with its op.
	acts, err := connection.Acts(s.Site(), "")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range acts {
		if a.Owner() && a.Op == "pub.polis.post.create" && a.Status == http.StatusCreated && a.Surface == "token" {
			found = true
		}
	}
	if !found {
		t.Errorf("acts = %+v, want the owner's post.create", acts)
	}
}

func TestAnAgentsPublishIsLoggedAsTheAgentsAndKeepsItsActRecord(t *testing.T) {
	s, h := observedServer(t)
	agent := mint(t, s, "publish")
	var code int
	events := captureEvents(t, func() {
		code, _ = apiCall(t, h, "POST", "/v1/content/post", agent, `{"markdown":"# By an agent\n\nhello"}`)
	})
	if code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	pub := eventsNamed(events, "pub.polis.post.publish")
	if len(pub) != 1 || pub[0]["surface"] != "agent" || pub[0]["agent"] == nil || pub[0]["grant"] == nil {
		t.Errorf("events = %v, want one publish, surface agent, with the marker", pub)
	}
	if w := eventsNamed(events, "pub.polis.api.write"); len(w) != 1 || w[0]["surface"] != "agent" ||
		w[0]["op"] != "pub.polis.post.create" || w[0]["agent"] == nil || w[0]["grant"] == nil {
		t.Errorf("pub.polis.api.write rows = %v, want one carrying the agent's surface, op and marker", w)
	}
	agents, _ := connection.AgentActs(s.Site())
	if len(agents) != 1 || agents[0].Op != "pub.polis.post.create" {
		t.Errorf("agent acts = %+v, want the one write", agents)
	}
}

func TestAnAPIErrorIsLoggedOnceWithItsCodeAndOperation(t *testing.T) {
	s, h := observedServer(t)
	read := mint(t, s, "read")
	var code int
	events := captureEvents(t, func() {
		code, _ = apiCall(t, h, "POST", "/v1/content/post", read, `{"markdown":"# Not allowed\n\nno"}`)
	})
	if code != http.StatusForbidden {
		t.Fatalf("create under read scope = %d, want 403", code)
	}
	errs := eventsNamed(events, api.APIErrorEvent)
	if len(errs) != 1 {
		t.Fatalf("%s rows = %d, want one\n%v", api.APIErrorEvent, len(errs), events)
	}
	e := errs[0]
	if e["code"] != "insufficient_scope" || e["op"] != "pub.polis.post.create" || e["status"] != float64(http.StatusForbidden) {
		t.Errorf("error event = %v", e)
	}
	// A refused write is nobody's act: the owner's record stays empty.
	acts, _ := connection.Acts(s.Site(), "")
	for _, a := range acts {
		if a.Owner() {
			t.Errorf("a refused write was recorded as the owner's: %+v", a)
		}
	}
}

// A draft autosave is logged (it is a write) and renders nothing.
func TestADraftSaveIsLoggedAndRendersNothing(t *testing.T) {
	s, h := observedServer(t)
	owner := mint(t, s)
	events := captureEvents(t, func() {
		if code, out := apiCall(t, h, "POST", "/v1/content/post/actions/save_draft", owner, `{"markdown":"half a thought"}`); code >= 300 {
			t.Fatalf("save_draft = %d %v", code, out)
		}
	})
	if n := len(eventsNamed(events, "pub.polis.post.draft_save")); n != 1 {
		t.Errorf("draft_save rows = %d, want one", n)
	}
	if n := len(eventsNamed(events, "pub.polis.site.render")); n != 0 {
		t.Errorf("a draft save rendered the site %d times", n)
	}
}

// ⭐ A read that makes the site fetch an address logs one event under one name,
// whichever route served it: the API's post.body and the old app's stream
// body share the fetch, and now its event.
func TestAFetchingReadIsLoggedOnceWhicheverRouteServedIt(t *testing.T) {
	s, h := observedServer(t)
	owner := mint(t, s)
	const addr = "https://127.0.0.1/posts/20261121/x.html" // refused by the fetch's private-address gate
	events := captureEvents(t, func() {
		get(t, h, "/v1/content/post/body?url="+addr, owner)
	})
	if n := len(eventsNamed(events, "pub.polis.stream.body")); n != 1 {
		t.Errorf("post.body: pub.polis.stream.body rows = %d, want one", n)
	}
	// The old route, asked for this site's own post (a visitor may), with the
	// fetch answered here.
	orig := render.DefaultHTTPClient
	render.DefaultHTTPClient = &http.Client{Transport: focusBodyRoundTripper{body: "a post"}}
	t.Cleanup(func() { render.DefaultHTTPClient = orig })
	events = captureEvents(t, func() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/body?url=https://test-site.polis.pub/posts/20261121/x.html", nil)
		s.handleStreamBody(httptest.NewRecorder(), req)
	})
	if n := len(eventsNamed(events, "pub.polis.stream.body")); n != 1 {
		t.Errorf("/api/v1/stream/body: pub.polis.stream.body rows = %d, want one", n)
	}
}

// ⭐ Every event a site's server logs names the site, so a shared stream (every
// polis.pub tenant logs to one) can tell one site's writes from another's. An
// event that names a site itself keeps its own.
func TestEveryEventNamesItsSite(t *testing.T) {
	got := withSiteField(map[string]interface{}{"x": 1}, "https://maya.polis.pub")
	if got["site"] != "maya.polis.pub" || got["x"] != 1 {
		t.Errorf("fields = %v", got)
	}
	own := map[string]interface{}{"site": "other.example"}
	if withSiteField(own, "https://maya.polis.pub")["site"] != "other.example" {
		t.Error("an event's own site was overwritten")
	}
	in := map[string]interface{}{"x": 1}
	withSiteField(in, "https://maya.polis.pub")
	if _, ok := in["site"]; ok {
		t.Error("the caller's map was changed")
	}
	if f := withSiteField(map[string]interface{}{}, ""); len(f) != 0 {
		t.Errorf("no base URL: fields = %v, want nothing added", f)
	}
}

// Every write's audit row names its operation, the seen mark's included (its
// route noted the operation only after the scope check, so the row had none).
func TestEveryWritesAuditRowNamesItsOperation(t *testing.T) {
	s, h := observedServer(t)
	owner := mint(t, s)
	var code int
	events := captureEvents(t, func() {
		code, _ = apiCall(t, h, "POST", "/v1/seen", owner, `{"section":"gather","up_to":"2026-11-22T03:00:00Z"}`)
	})
	if code/100 != 2 {
		t.Fatalf("seen.mark = %d", code)
	}
	w := eventsNamed(events, "pub.polis.api.write")
	if len(w) != 1 || w[0]["op"] != "pub.polis.seen.mark" || w[0]["surface"] != "token" {
		t.Errorf("pub.polis.api.write rows = %v, want one naming pub.polis.seen.mark", w)
	}
}
