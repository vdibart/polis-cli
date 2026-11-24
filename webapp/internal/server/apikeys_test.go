package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

func mintViaAPI(t *testing.T, s *Server, body string) (int, map[string]interface{}) {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleAPIKeys(w, httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(body)))
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func TestAPIKeysMintListRevoke(t *testing.T) {
	s := newConfiguredServer(t)

	code, resp := mintViaAPI(t, s, `{"name":"Kay","scopes":["render"]}`)
	if code != http.StatusCreated {
		t.Fatalf("mint = %d %v", code, resp)
	}
	token, _ := resp["token"].(string)
	key, _ := resp["key"].(map[string]interface{})
	id, _ := key["id"].(string)
	if !strings.HasPrefix(token, "polis_") || id == "" {
		t.Fatalf("mint response lacks a token or an id: %v", resp)
	}
	got, ok := ops.LookupAPIKey(s.Site(), token)
	if !ok || !got.Allows(ops.ScopeRender) || got.Unscoped() {
		t.Fatalf("the minted token does not resolve to a render-scoped key: %+v %v", got, ok)
	}

	// The list never carries a hash or the token.
	w := httptest.NewRecorder()
	s.handleAPIKeys(w, httptest.NewRequest(http.MethodGet, "/api/keys", nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "key_hash") || strings.Contains(w.Body.String(), token) {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	// ⭐ A scoped key is a personal token: it presents a signed grant, and its
	// render scope is granted under its current name, appearance.
	if !strings.Contains(w.Body.String(), `"scopes":["appearance"]`) || !strings.Contains(w.Body.String(), `"grant":"https://`) {
		t.Errorf("list does not show the key's scopes and grant: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	s.handleAPIKey(w, httptest.NewRequest(http.MethodDelete, "/api/keys/"+id, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", w.Code, w.Body.String())
	}
	if _, ok := ops.LookupAPIKey(s.Site(), token); ok {
		t.Error("a revoked token still resolves")
	}
	w = httptest.NewRecorder()
	s.handleAPIKey(w, httptest.NewRequest(http.MethodDelete, "/api/keys/"+id, nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("revoking twice = %d, want 404", w.Code)
	}
}

func TestAPIKeysRefusesBadInput(t *testing.T) {
	s := newConfiguredServer(t)
	for _, body := range []string{`not json`, `{"name":""}`, `{"name":"x","scopes":["everything"]}`} {
		if code, _ := mintViaAPI(t, s, body); code != http.StatusBadRequest {
			t.Errorf("mint %s = %d, want 400", body, code)
		}
	}
	w := httptest.NewRecorder()
	s.handleAPIKeys(w, httptest.NewRequest(http.MethodPut, "/api/keys", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /api/keys = %d, want 405", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleAPIKey(w, httptest.NewRequest(http.MethodGet, "/api/keys/x", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/keys/x = %d, want 405", w.Code)
	}
}

// ⛔ Minting a key is an owner act: a stranger reaching a remote instance is
// refused at the gate before the handler runs.
func TestAPIKeysAreOwnerOnly(t *testing.T) {
	s, _ := passkeyServer(t)
	for _, path := range []string{"/api/keys", "/api/keys/key_x"} {
		if code, _ := gateRefusal(t, s, path); code == http.StatusOK {
			t.Errorf("%s passed the owner gate without a session", path)
		}
	}
}

// The keys file stays private to the site.
func TestAPIKeysFileIsNotWorldReadable(t *testing.T) {
	s := newConfiguredServer(t)
	if code, _ := mintViaAPI(t, s, `{"name":"Kay","scopes":["render"]}`); code != http.StatusCreated {
		t.Fatal("mint failed")
	}
	info, err := os.Stat(s.Site().Path(".polis/api-keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o004 != 0 {
		t.Errorf("api-keys.json is world-readable: %v", info.Mode().Perm())
	}
}
