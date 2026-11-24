package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ⛔⛔ EVERY /v1 ERROR IS ONE ENVELOPE, WHATEVER ANSWERED IT.
//
// Each operation the API describes is called twice, with no credential and
// with the owner's own token and a body that makes no sense, and every error
// it answers must be {"status":"error","error":{"code","message"}} as JSON:
// the router's refusals, the engine's, a host route's {"error": msg} and the
// plain text of a browser-app handler a /v1 operation forwards to.
func TestEveryAPIErrorIsTheEnvelope(t *testing.T) {
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
	errorsSeen := 0
	for path, methods := range doc.Paths {
		concrete := strings.ReplaceAll(path, "{id}", "x")
		for method := range methods {
			m := strings.ToUpper(method)
			if path == "/v1/site/key/rotate" || path == "/v1/site/download" {
				continue // sign-in only: refused to every token, and tested on their own
			}
			for _, token := range []string{"", owner} {
				req := httptest.NewRequest(m, concrete+"?url=nonsense&sentence=%7B", strings.NewReader(`{"__nonsense": [1, 2`))
				req.Header.Set("Content-Type", "application/json")
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code < 400 {
					continue
				}
				errorsSeen++
				var env struct {
					Status string `json:"status"`
					Error  struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") ||
					json.Unmarshal(rec.Body.Bytes(), &env) != nil || env.Status != "error" || env.Error.Code == "" || env.Error.Message == "" {
					t.Errorf("%s %s (token=%v) answered %d with %q, not the envelope", m, path, token != "", rec.Code, rec.Body.String())
				}
			}
		}
	}
	if errorsSeen < 40 {
		t.Fatalf("only %d error answers were checked; the census is not reaching the routes", errorsSeen)
	}
}

// A browser-app handler a /v1 operation forwards to answers in plain text
// (http.Error); through /v1 the caller gets the envelope with its message.
func TestAForwardedHandlersPlainTextErrorArrivesAsTheEnvelope(t *testing.T) {
	s, h := queryRoutes(t)
	code, out := apiCall(t, h, "POST", "/v1/site/about", mint(t, s), `not json`)
	inner, _ := out["error"].(map[string]any)
	if code != http.StatusBadRequest || out["status"] != "error" || inner["code"] != "invalid_request" || inner["message"] == "" {
		t.Errorf("forwarded error = %d %v", code, out)
	}
}
