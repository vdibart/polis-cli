package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
)

func sendJSON(t *testing.T, h http.Handler, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func draftsOf(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	entries, _ := s.Site().Store().List(t.Context(), ".polis/bundles/pub.polis.core/posts/drafts/")
	for _, e := range entries {
		if strings.HasSuffix(e.Key, ".md") {
			id := strings.TrimSuffix(e.Key[strings.LastIndex(e.Key, "/")+1:], ".md")
			ids[id] = true
		}
	}
	return ids
}

// ⭐ The owner saves, updates and discards drafts through the API, and
// 'all drafts from me' reads them.
// ⛔ Drafts are the owner's alone (decided 2026-11-16): no agent token, of any
// scope, may save or discard one.
func TestDraftsAreSavedUpdatedAndDiscardedByTheOwnerOnly(t *testing.T) {
	s, h := queryRoutes(t)
	owner := mint(t, s)
	const save = "/v1/content/post/actions/save_draft"
	const discard = "/v1/content/post/actions/discard_draft"

	code, out := sendJSON(t, h, "POST", save, owner, `{"markdown":"# Half a thought\n\nmore later"}`)
	if code != http.StatusOK {
		t.Fatalf("save = %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if id == "" || !draftsOf(t, s)[id] {
		t.Fatalf("saved draft %q not on the site: %v", id, draftsOf(t, s))
	}
	// Reading is PQL's: the saved draft is there.
	if !strings.Contains(get(t, h, "/v1/query?pql=all+drafts+from+me", owner).Body.String(), id) {
		t.Errorf("'all drafts from me' lacks %s", id)
	}
	// With its id, a save replaces that draft rather than making another.
	if code, out := sendJSON(t, h, "POST", save, owner, `{"id":"`+id+`","markdown":"# A whole thought"}`); code != http.StatusOK || out["id"] != id {
		t.Fatalf("update = %d %v", code, out)
	}
	if got, _ := s.Site().Store().Get(t.Context(), ".polis/bundles/pub.polis.core/posts/drafts/"+id+".md"); string(got) != "# A whole thought" {
		t.Errorf("after update the draft reads %q", got)
	}
	if n := len(draftsOf(t, s)); n != 1 {
		t.Errorf("drafts = %d after an update, want 1", n)
	}
	if code, _ := sendJSON(t, h, "POST", save, owner, `{}`); code != http.StatusBadRequest {
		t.Errorf("save without markdown = %d, want 400", code)
	}

	// Every scope an agent can be granted is refused, both ways, and leaves the draft be.
	granted := 0
	for _, sc := range apidesc.Scopes {
		if !apidesc.AgentGrantable(sc.Name) {
			continue
		}
		granted++
		tok := mint(t, s, sc.Name)
		if code := send(t, h, "POST", save, tok, `{"markdown":"agent text"}`); code != http.StatusForbidden {
			t.Errorf("save with a %s token = %d, want 403", sc.Name, code)
		}
		if code := send(t, h, "POST", discard, tok, `{"id":"`+id+`"}`); code != http.StatusForbidden {
			t.Errorf("discard with a %s token = %d, want 403", sc.Name, code)
		}
	}
	if granted == 0 {
		t.Fatal("no grantable scope to try")
	}
	// All of them at once, too.
	var all []string
	for _, sc := range apidesc.Scopes {
		if apidesc.AgentGrantable(sc.Name) {
			all = append(all, sc.Name)
		}
	}
	if code := send(t, h, "POST", save, mint(t, s, all...), `{"markdown":"agent text"}`); code != http.StatusForbidden {
		t.Errorf("save with every grantable scope = %d, want 403", code)
	}
	if code := send(t, h, "POST", save, "", `{"markdown":"x"}`); code != http.StatusUnauthorized {
		t.Errorf("save with no token = %d, want 401", code)
	}
	if n := len(draftsOf(t, s)); n != 1 {
		t.Errorf("drafts = %d after refused saves, want 1", n)
	}

	if code, out := sendJSON(t, h, "POST", discard, owner, `{"id":"`+id+`"}`); code != http.StatusOK || out["discarded"] != true {
		t.Fatalf("discard = %d %v", code, out)
	}
	if draftsOf(t, s)[id] {
		t.Error("the draft is still there after discarding")
	}
	if code := send(t, h, "POST", discard, owner, `{"id":"`+id+`"}`); code != http.StatusNotFound {
		t.Errorf("discarding it again = %d, want 404", code)
	}
	if code := send(t, h, "POST", discard, owner, `{"id":"../../keys/id_ed25519"}`); code != http.StatusNotFound {
		t.Errorf("a path as an id = %d, want 404 (sanitised to a name, and absent)", code)
	}
}

// ⭐ Both operations are in the API's description, marked owner-only, and the
// old /v1/content/<type>/drafts routes, which dispatched verbs nothing
// handled, are gone.
func TestDraftOperationsAreDescribedOwnerOnlyAndTheDeadRoutesAreGone(t *testing.T) {
	s, h := queryRoutes(t)
	var doc struct {
		Paths map[string]map[string]map[string]any `json:"paths"`
	}
	_ = json.Unmarshal(get(t, h, "/v1/openapi.json", "").Body.Bytes(), &doc)
	for _, p := range []string{"/v1/content/post/actions/save_draft", "/v1/content/post/actions/discard_draft"} {
		op := doc.Paths[p]["post"]
		if op == nil {
			t.Errorf("%s is not described", p)
			continue
		}
		if op["x-polis-owner-only"] != true {
			t.Errorf("%s is not marked owner-only: %v", p, op["x-polis-owner-only"])
		}
	}
	owner := mint(t, s)
	for _, r := range []struct{ method, path string }{
		{"GET", "/v1/content/post/drafts"}, {"POST", "/v1/content/post/drafts"},
		{"GET", "/v1/content/post/drafts/x"}, {"DELETE", "/v1/content/post/drafts/x"},
	} {
		if code := send(t, h, r.method, r.path, owner, `{"markdown":"x"}`); code >= 200 && code < 300 {
			t.Errorf("%s %s = %d, want it gone", r.method, r.path, code)
		}
	}
}
