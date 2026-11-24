package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// ⭐ ONE PATH RULE FOR EVERY /v1 OPERATION, written in the developer
// reference (docs/api/developer/reference.md, "Paths") and checked here over
// the API's own description, so a new operation cannot invent a third shape.
//
//	A content type's operations, under /v1/content/{type}:
//	  GET    /v1/content/{type}                  list
//	  POST   /v1/content/{type}                  create
//	  GET    /v1/content/{type}/{id}             get one
//	  PUT    /v1/content/{type}/{id}             update one
//	  POST   /v1/content/{type}/actions/{action} any other action; {action} is the operation's own name
//	  GET    /v1/content/{type}/{view}           a named read of the type (source, body, current), with
//	                                             its subject in the query string
//	A site's settings, agents and messages, under /v1/{area}:
//	  GET    /v1/{area}[/{noun}…]                read
//	  POST   /v1/{area}[/{noun}…]                change
//	  where {area} is site, agents, messages, query or seen, and nouns are kebab-case.
var pathRules = []struct {
	methods string
	re      *regexp.Regexp
}{
	{"GET POST", regexp.MustCompile(`^/v1/content/[a-z]+$`)},
	{"GET PUT", regexp.MustCompile(`^/v1/content/[a-z]+/\{id\}$`)},
	{"POST", regexp.MustCompile(`^/v1/content/[a-z]+/actions/([a-z_]+)$`)},
	{"GET", regexp.MustCompile(`^/v1/content/[a-z]+/[a-z]+$`)},
	{"GET POST", regexp.MustCompile(`^/v1/(site|agents|messages)(/[a-z]+(-[a-z]+)*)*$`)},
	{"GET POST", regexp.MustCompile(`^/v1/(query|seen)$`)},
}

func TestEveryAPIPathFollowsThePathRule(t *testing.T) {
	_, h := queryRoutes(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil))
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for path, methods := range doc.Paths {
		for method, op := range methods {
			m := strings.ToUpper(method)
			checked++
			ok := false
			for _, r := range pathRules {
				sub := r.re.FindStringSubmatch(path)
				if sub == nil || !strings.Contains(" "+r.methods+" ", " "+m+" ") {
					continue
				}
				// An action's path names the action itself.
				if strings.Contains(r.re.String(), "/actions/") && !strings.HasSuffix(op.OperationID, "."+sub[1]) && !strings.HasSuffix(op.OperationID, "_"+sub[1]) {
					t.Errorf("%s %s: the action in the path (%s) is not the operation's own (%s)", m, path, sub[1], op.OperationID)
				}
				ok = true
				break
			}
			if !ok {
				t.Errorf("%s %s follows no path rule (see TestEveryAPIPathFollowsThePathRule and the reference's Paths section)", m, path)
			}
		}
	}
	if checked < 60 {
		t.Fatalf("checked only %d operations", checked)
	}
}
