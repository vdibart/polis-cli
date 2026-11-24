package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// ⛔ EVERY OPERATION STATES ITS ANSWER. The API sends each operation's own
// shape (no common success wrapper), so the description must say what that
// shape is, or a caller has to guess. Read from the served description, so
// what is checked is what a caller sees: each operation's success response
// has a schema, names a body that is not JSON, or is 204 with no body, and
// every error response is the envelope.
func TestEveryOperationStatesItsAnswer(t *testing.T) {
	_, h := queryRoutes(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string                    `json:"operationId"`
			Responses   map[string]map[string]any `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for path, methods := range doc.Paths {
		for method, op := range methods {
			checked++
			var success string
			for code := range op.Responses {
				if strings.HasPrefix(code, "2") {
					success = code
				}
			}
			if success == "" {
				t.Errorf("%s %s (%s) states no success answer", strings.ToUpper(method), path, op.OperationID)
				continue
			}
			resp := op.Responses[success]
			if _, hasBody := resp["content"]; !hasBody && success != "204" {
				t.Errorf("%s %s (%s) answers %s with no stated shape: give it an Output (or OutputType, or Status 204)",
					strings.ToUpper(method), path, op.OperationID, success)
			}
			for _, code := range []string{"400", "401", "403", "default"} {
				e, ok := op.Responses[code]
				if !ok || !strings.Contains(mustJSON(e), `"enum":["error"]`) {
					t.Errorf("%s %s: the %s answer is not the error envelope", strings.ToUpper(method), path, code)
				}
			}
		}
	}
	if checked < 60 {
		t.Fatalf("checked only %d operations", checked)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// A stated answer is the real one: for a few operations of each kind, every
// field the operation actually sends is one its description names.
func TestAStatedAnswerNamesTheFieldsActuallySent(t *testing.T) {
	s, h := queryRoutes(t)
	owner := mint(t, s)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema struct {
						Properties map[string]any `json:"properties"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	declared := func(path, method, code string) map[string]any {
		return doc.Paths[path][method].Responses[code].Content["application/json"].Schema.Properties
	}
	for _, c := range []struct{ method, path, body, code string }{
		{"POST", "/v1/content/post", `{"markdown":"# Shape\n\ncheck"}`, "201"}, // engine
		{"GET", "/v1/content/post", "", "200"},                                 // engine, public
		{"GET", "/v1/site", "", "200"},                                         // host
		{"GET", "/v1/content/follow", "", "200"},                               // engine, owner
		{"GET", "/v1/seen", "", "200"},                                         // API package
	} {
		code, got := apiCall(t, h, c.method, c.path, owner, c.body)
		if strconv.Itoa(code) != c.code {
			t.Fatalf("%s %s = %d, want %s (the stated success code)", c.method, c.path, code, c.code)
		}
		props := declared(c.path, strings.ToLower(c.method), c.code)
		if len(props) == 0 {
			t.Errorf("%s %s: no declared fields for %s", c.method, c.path, c.code)
			continue
		}
		for k := range got {
			if _, ok := props[k]; !ok {
				t.Errorf("%s %s sends %q, which its stated answer does not name", c.method, c.path, k)
			}
		}
	}
}
