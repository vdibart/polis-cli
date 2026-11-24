package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ⛔⛔ NOTHING THE DESCRIPTION PROMISES ANSWERS unsupported_action.
//
// The whole description a configured site serves (the engine's operations and
// every host route: blessing, following, the site's settings, sources, owner
// routes) is called with the owner's own token, and no operation may answer
// that its action is not available. The generic routes (GET
// /v1/content/comment, DELETE /v1/content/post/{id}, …) still answer it, and
// that is right: they are not described, and the reference names them as not
// offered.
func TestNoDescribedOperationAnswersUnsupportedAction(t *testing.T) {
	s, h := queryRoutes(t)
	owner := mint(t, s)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	called := 0
	for path, methods := range doc.Paths {
		concrete := strings.ReplaceAll(path, "{id}", "x")
		for method := range methods {
			m := strings.ToUpper(method)
			req := httptest.NewRequest(m, concrete, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+owner)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			called++
			if strings.Contains(rec.Body.String(), "unsupported_action") {
				t.Errorf("%s %s is described but answers %d %s", m, path, rec.Code, rec.Body.String())
			}
		}
	}
	if called < 40 {
		t.Fatalf("only %d described operations were called; the census is not reaching the description", called)
	}
}

// The operations the reference names as not offered answer unsupported_action
// and are absent from the description, so no client is invited to call them.
func TestTheOperationsNotOfferedAreNotDescribed(t *testing.T) {
	s, h := queryRoutes(t)
	owner := mint(t, s)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, described string }{
		{"GET", "/v1/content/comment", "/v1/content/comment"},
		{"GET", "/v1/content/comment/x", "/v1/content/comment/{id}"},
		{"GET", "/v1/content/post/x", "/v1/content/post/{id}"},
		{"DELETE", "/v1/content/post/x", "/v1/content/post/{id}"},
		{"POST", "/v1/content/post/actions/render", "/v1/content/post/actions/render"},
		{"POST", "/v1/content/comment/actions/revoke", "/v1/content/comment/actions/revoke"},
		{"POST", "/v1/content/comment/actions/sync", "/v1/content/comment/actions/sync"},
	} {
		if _, ok := doc.Paths[c.described][strings.ToLower(c.method)]; ok {
			t.Errorf("%s %s is described; the reference names it as not offered", c.method, c.described)
		}
		code, out := apiCall(t, h, c.method, c.path, owner, `{}`)
		inner, _ := out["error"].(map[string]any)
		if code != http.StatusBadRequest || inner["code"] != "unsupported_action" {
			t.Errorf("%s %s = %d %v, want 400 unsupported_action", c.method, c.path, code, out)
		}
	}
}
