package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

func queryRoutes(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	s := newConfiguredServer(t)
	return s, editorRoutes(s, webFS)
}

func get(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// ⭐ /v1/query is /pql/ through the API: the same handler, the same answer,
// for a sentence about public content and with no credential.
func TestQueryAnswersAsPQLDoesForPublicContent(t *testing.T) {
	_, h := queryRoutes(t)
	viaAPI := get(t, h, "/v1/query?pql=all+posts+by+date", "")
	viaPQL := get(t, h, "/pql/all+posts+by+date", "")
	if viaAPI.Code != http.StatusOK || viaPQL.Code != http.StatusOK {
		t.Fatalf("codes: /v1/query %d %s, /pql/ %d", viaAPI.Code, viaAPI.Body.String(), viaPQL.Code)
	}
	var a, b map[string]any
	if json.Unmarshal(viaAPI.Body.Bytes(), &a) != nil || json.Unmarshal(viaPQL.Body.Bytes(), &b) != nil {
		t.Fatal("not JSON")
	}
	// The envelope's own timestamp may differ by a tick; everything else is the same.
	delete(a, "generated_at")
	delete(b, "generated_at")
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Errorf("/v1/query and /pql/ differ:\n%s\n%s", ja, jb)
	}
}

// ⛔ An owner-private sentence needs the owner's full-access token: none is
// 401, a scoped key is told it is scoped, a full key gets the owner's answer.
func TestQueryNeedsTheOwnersTokenForPrivateSentences(t *testing.T) {
	s, h := queryRoutes(t)
	const path = "/v1/query?pql=all+posts+from+me"
	if w := get(t, h, path, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d %s, want 401", w.Code, w.Body.String())
	}
	scoped, _, err := ops.MintAPIKey(s.Site(), "render only", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	if w := get(t, h, path, scoped); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "insufficient_scope") {
		t.Errorf("render-scoped key: %d %s, want 403 insufficient_scope", w.Code, w.Body.String())
	}
	if w := get(t, h, path, "polis_not-a-real-key"); w.Code == http.StatusOK {
		t.Errorf("invalid key: %d, want a refusal", w.Code)
	}
	full, _, err := ops.MintAPIKey(s.Site(), "owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := get(t, h, path, full); w.Code != http.StatusOK {
		t.Errorf("full key: %d %s, want 200", w.Code, w.Body.String())
	}
	if w := get(t, h, "/v1/query", full); w.Code != http.StatusBadRequest {
		t.Errorf("no pql: %d, want 400", w.Code)
	}
}

// The description lists the query operation, public or owner by sentence.
func TestDescriptionListsTheQueryOperation(t *testing.T) {
	_, h := queryRoutes(t)
	w := get(t, h, "/v1/openapi.json", "")
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	item, _ := doc["paths"].(map[string]any)["/v1/query"].(map[string]any)
	op, _ := item["get"].(map[string]any)
	if op == nil || op["x-polis-auth"] != "public-or-owner" || op["x-polis-write"] != false {
		t.Errorf("query operation = %v", op)
	}
}

// ⭐ A sentence that does not parse is answered with the parser's reason,
// through /pql/ and through /v1/query (what MCP's query_pql calls), so an
// agent can correct it in one retry.
func TestAMalformedSentenceIsAnsweredWithTheReason(t *testing.T) {
	_, h := queryRoutes(t)
	viaPQL := get(t, h, "/pql/all+posts+and+comments+from+me", "")
	// The site reads its own sentences tenant-relative ("all posts" means its
	// own), so here the parser stops at "and" as a modifier it does not know.
	if viaPQL.Code != http.StatusBadRequest || !strings.Contains(viaPQL.Body.String(), `invalid modifier at \"and\"`) ||
		!strings.Contains(viaPQL.Body.String(), "activity covers posts and comments") {
		t.Errorf("/pql/: %d %s", viaPQL.Code, viaPQL.Body.String())
	}
	viaAPI := get(t, h, "/v1/query?pql=all+posts+and+comments+from+me", "")
	var env struct {
		Status string `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if viaAPI.Code != http.StatusBadRequest || json.Unmarshal(viaAPI.Body.Bytes(), &env) != nil ||
		!strings.Contains(env.Error.Message, `invalid modifier at "and"`) || !strings.Contains(env.Error.Message, "activity") {
		t.Errorf("/v1/query: %d %s", viaAPI.Code, viaAPI.Body.String())
	}
}
