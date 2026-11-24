package server

import (
	"context"
	"encoding/json"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// mintVia mints a key the way the owner does, through POST /api/keys, and
// returns its token and view.
func mintVia(t *testing.T, s *Server, name string, scopes ...string) (string, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "scopes": scopes})
	w := httptest.NewRecorder()
	s.handleAPIKeys(w, httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(string(body))))
	if w.Code != http.StatusCreated {
		t.Fatalf("mint %v = %d %s", scopes, w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	key, _ := resp["key"].(map[string]any)
	token, _ := resp["token"].(string)
	return token, key
}

// ⭐ #1: a scoped key is a personal token: minting it writes the owner's
// signed grant naming the connection and its scopes, and the token points
// at it. ⛔ An agent's key may not hold tag or full, alone or beside others.
func TestMintingAScopedKeyWritesItsGrant(t *testing.T) {
	s := newConfiguredServer(t)
	_, key := mintVia(t, s, "Claude Desktop", "read", "publish")
	grant, _ := key["grant"].(string)
	live, err := connection.Resolve(s.Site(), grant)
	if err != nil || !live.Valid() {
		t.Fatalf("the key's grant is not live: %v %+v (key %v)", err, live, key)
	}
	if live.Agent() != "Claude Desktop" || strings.Join(live.Scopes(), " ") != "read publish" || live.Behaviours() != "agent/1" {
		t.Errorf("grant = %s %v %s", live.Agent(), live.Scopes(), live.Behaviours())
	}
	for _, scopes := range [][]string{{"tag"}, {"read", "tag"}, {"read", "full"}} {
		body, _ := json.Marshal(map[string]any{"name": "x", "scopes": scopes})
		w := httptest.NewRecorder()
		s.handleAPIKeys(w, httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(string(body))))
		if w.Code != http.StatusBadRequest {
			t.Errorf("minting %v = %d, want 400", scopes, w.Code)
		}
	}
	// The owner's own full key carries no grant.
	_, full := mintVia(t, s, "owner", "full")
	if full["grant"] != nil {
		t.Errorf("a full key carries a grant: %v", full)
	}
}

// ⭐ #2: a token acts only while its grant is live. Withdrawing the grant (by
// revoking the key, or directly) refuses the very next write AND read.
func TestAWithdrawnGrantRefusesTheNextWriteAndRead(t *testing.T) {
	s, h := queryRoutes(t)
	token, key := mintVia(t, s, "agent", "read", "publish")
	post := `{"markdown":"# One\n\nbody"}`
	if code := send(t, h, "POST", "/v1/content/post", token, post); code != http.StatusCreated {
		t.Fatalf("live grant may publish: %d", code)
	}
	if code := send(t, h, "GET", "/v1/query?pql=all+posts+from+me", token, ""); code != http.StatusOK {
		t.Fatalf("live grant may read: %d", code)
	}
	grantID := grantIDOf(t, s, key["id"].(string))
	if err := connection.Withdraw(s.connectionConfig(), grantID); err != nil {
		t.Fatal(err)
	}
	if code := send(t, h, "POST", "/v1/content/post", token, post); code != http.StatusForbidden {
		t.Errorf("withdrawn grant publishes: %d", code)
	}
	if code := send(t, h, "GET", "/v1/query?pql=all+posts+from+me", token, ""); code == http.StatusOK {
		t.Errorf("withdrawn grant reads the owner's posts: %d", code)
	}

	// Revoking a key withdraws its grant on the record.
	_, key2 := mintVia(t, s, "second", "read")
	grant2, _ := key2["grant"].(string)
	w := httptest.NewRecorder()
	s.handleAPIKey(w, httptest.NewRequest(http.MethodDelete, "/api/keys/"+key2["id"].(string), nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", w.Code)
	}
	if live, _ := connection.Resolve(s.Site(), grant2); live != nil {
		t.Error("revoking the key left its grant live")
	}
}

func grantIDOf(t *testing.T, s *Server, keyID string) string {
	t.Helper()
	keys, _ := ops.ListAPIKeys(s.Site())
	for _, k := range keys {
		if k.ID == keyID {
			return k.GrantID
		}
	}
	t.Fatalf("no key %s", keyID)
	return ""
}

// ⭐ #3: what an agent writes over the API is marked with its grant: a post
// inside its signature, a policy rule in added_by, and an act with nothing
// signed in the site's private act log. The owner's own key marks nothing.
func TestAgentWritesAreMarked(t *testing.T) {
	s, h := queryRoutes(t)
	token, key := mintVia(t, s, "Claude Desktop", "publish", "profile", "policy:write")
	grant := key["grant"].(string)

	code, out := apiCall(t, h, "POST", "/v1/content/post", token, `{"markdown":"# Marked\n\nfrom an agent"}`)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d %v", code, out)
	}
	src, err := os.ReadFile(filepath.Join(s.DataDir, out["path"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "\nagent: Claude Desktop\ngrant: "+grant+"\n") {
		t.Errorf("post is not marked:\n%s", src)
	}

	if code, out := apiCall(t, h, "POST", "/v1/site/policy/tighten", token, `{"rule":"deny pub.polis.dm from all"}`); code != http.StatusOK {
		t.Fatalf("tighten = %d %v", code, out)
	}
	rules, _ := os.ReadFile(filepath.Join(s.DataDir, policy.PrivateRulesKey))
	if !strings.Contains(string(rules), `"`+policy.AddedByField+`":"`+grant+`"`) {
		t.Errorf("agent's rule not marked with its grant:\n%s", rules)
	}

	if code, out := apiCall(t, h, "POST", "/v1/site/name", token, `{"author_name":"Alice"}`); code >= 300 {
		t.Fatalf("set name = %d %v", code, out)
	}
	acts, _ := connection.Acts(s.Site(), grant)
	var paths []string
	for _, a := range acts {
		paths = append(paths, a.Method+" "+a.Path)
	}
	for _, want := range []string{"POST /v1/content/post", "POST /v1/site/policy/tighten", "POST /v1/site/name"} {
		if !strings.Contains(strings.Join(paths, "|"), want) {
			t.Errorf("act log lacks %s: %v", want, paths)
		}
	}

	// The owner's own full key: unmarked, and not in the act log.
	full := mint(t, s)
	_, out = apiCall(t, h, "POST", "/v1/content/post", full, `{"markdown":"# Mine\n\nbody"}`)
	mine, _ := os.ReadFile(filepath.Join(s.DataDir, out["path"].(string)))
	if strings.Contains(string(mine), "\nagent:") {
		t.Errorf("owner's post is marked:\n%s", mine)
	}
}

// ⭐ #3: an agent's blessing decision carries the marker in the signed
// relationship request.
func TestAgentBlessingDecisionsAreMarked(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.URL.Path+" "+string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer ds.Close()
	s, h := queryRoutes(t)
	s.DiscoveryURL = ds.URL
	registered(t, s)
	token, key := mintVia(t, s, "Claude Desktop", "bless")
	grant := key["grant"].(string)
	comment := `{"comment_url":"https://bob.example/content/pub.polis.core/comment/20261113/hi.md","in_reply_to":"https://test.example/content/pub.polis.core/post/20261113/p.md"}`
	if code, out := apiCall(t, h, "POST", "/v1/content/comment/actions/deny", token, comment); code != http.StatusOK {
		t.Fatalf("deny = %d %v", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, b := range bodies {
		if strings.HasPrefix(b, "/v1/relationships ") && strings.Contains(b, `"agent":"Claude Desktop"`) && strings.Contains(b, `"grant":"`+grant+`"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("deny's relationship request is not marked: %v", bodies)
	}
}

// ⛔ #3's guard: every write the API offers an agent is declared as signed
// with the marker or recorded in the act log. An operation added without a
// place in api.AgentWrites fails the build.
func TestEveryAgentWriteHasAMarkerPath(t *testing.T) {
	_, h := queryRoutes(t)
	var doc map[string]any
	_ = json.Unmarshal([]byte(fmtDescription(t, h)), &doc)
	paths, _ := doc["paths"].(map[string]any)
	for _, item := range paths {
		for _, raw := range item.(map[string]any) {
			op, _ := raw.(map[string]any)
			scope, _ := op["x-polis-scope"].(string)
			if op["x-polis-write"] != true || op["x-polis-auth"] != "owner" || !apidesc.AgentGrantable(scope) {
				continue
			}
			id, _ := op["operationId"].(string)
			if kind, ok := api.AgentWrites[id]; !ok || (kind != api.MarkedInSignature && kind != api.RecordedInActLog) {
				t.Errorf("%s (scope %s) is a write an agent can make with no declared marker path", id, scope)
			}
		}
	}
}

// ⭐ #4: the description a token is served leaves out what its grant cannot
// hold, so no generated tool always answers 403. The public document is whole.
func TestTheDescriptionFitsTheToken(t *testing.T) {
	s, h := queryRoutes(t)
	token, _ := mintVia(t, s, "reader", "read")
	scoped := descriptionFor(t, h, token)
	public := descriptionFor(t, h, "")
	for _, id := range []string{`"post.create"`, `"follow.create"`, `"site.set_name"`} {
		if strings.Contains(scoped, id) {
			t.Errorf("a read-only token is offered %s", id)
		}
		if !strings.Contains(public, id) {
			t.Errorf("the public description lacks %s", id)
		}
	}
	if !strings.Contains(scoped, `"query.pql"`) {
		t.Error("a read token is not offered the query")
	}
}

func descriptionFor(t *testing.T, h http.Handler, token string) string {
	t.Helper()
	w := get(t, h, apidesc.DescriptionPath, token)
	if w.Code != http.StatusOK {
		t.Fatalf("description = %d", w.Code)
	}
	return w.Body.String()
}

// ⭐ #5: a scoped key made before keys carried grants can read but not write,
// and becomes a personal token when the owner opens the keys list.
func TestOldScopedKeysAreRenewedWhenTheOwnerLooks(t *testing.T) {
	s, h := queryRoutes(t)
	old, oldKey, err := ops.MintAPIKey(s.Site(), "old script", []string{"read", "publish", "follow"})
	if err != nil {
		t.Fatal(err)
	}
	post := `{"markdown":"# Old\n\nbody"}`
	if code := send(t, h, "POST", "/v1/content/post", old, post); code != http.StatusForbidden {
		t.Errorf("an old scoped key writes before renewal: %d", code)
	}
	if code := send(t, h, "GET", "/v1/query?pql=all+posts+from+me", old, ""); code != http.StatusOK {
		t.Errorf("an old scoped key cannot read: %d", code)
	}
	w := httptest.NewRecorder()
	s.handleAPIKeys(w, httptest.NewRequest(http.MethodGet, "/api/keys", nil))
	if !strings.Contains(w.Body.String(), `"grant":"https://`) {
		t.Fatalf("list did not renew the key: %s", w.Body.String())
	}
	if code := send(t, h, "POST", "/v1/content/post", old, post); code != http.StatusCreated {
		t.Errorf("a renewed key cannot publish: %d", code)
	}
	keys, _ := ops.ListAPIKeys(s.Site())
	for _, k := range keys {
		if k.ID == oldKey.ID && strings.Contains(strings.Join(k.Scopes, " "), "follow") {
			t.Errorf("renewal kept follow, which a key minted before follow was grantable never agreed to: %v", k.Scopes)
		}
	}
}

// ⭐ A connection's name is public, so minting one says so, and a key minted
// with no name gets a neutral default that names no software.
func TestMintingWarnsTheNameIsPublicAndDefaultsIt(t *testing.T) {
	s := newConfiguredServer(t)
	w := httptest.NewRecorder()
	s.handleAPIKeys(w, httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"scopes":["read"]}`)))
	if w.Code != http.StatusCreated {
		t.Fatalf("mint without a name = %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	key, _ := resp["key"].(map[string]any)
	if key["name"] != "Agent 1" || resp["warning"] != connection.PublicNameWarning {
		t.Errorf("default name / warning = %v / %v", key["name"], resp["warning"])
	}
	_, full := mintVia(t, s, "owner", "full")
	if full["warning"] != nil {
		t.Errorf("a full key (no grant, nothing public) carries the warning")
	}
	_, second := mintVia(t, s, "", "read")
	if second["name"] != "Agent 2" {
		t.Errorf("second default name = %v", second["name"])
	}
}

// ⭐ A sign-in token the host vouches for (polis.pub's sign-in server) acts
// exactly as a personal token: its grant must be live, it is described only
// what its grant holds, and what it writes is marked.
func TestSignInTokensActAsPersonalTokens(t *testing.T) {
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	s := newConfiguredServer(t)
	issued, err := connection.Issue(s.connectionConfig(), "Claude Desktop", "Claude Desktop", []string{"publish"})
	if err != nil {
		t.Fatal(err)
	}
	s.AgentTokens = func(r *http.Request, token string) (api.TokenGrant, bool) {
		if token == "signin-token" {
			return api.TokenGrant{ID: "connection", Scopes: []string{"publish"}, GrantURL: issued.URL}, true
		}
		return api.TokenGrant{}, false
	}
	h := editorRoutes(s, webFS)
	code, out := apiCall(t, h, "POST", "/v1/content/post", "signin-token", `{"markdown":"# Via sign-in\n\nbody"}`)
	if code != http.StatusCreated {
		t.Fatalf("publish = %d %v", code, out)
	}
	src, _ := os.ReadFile(filepath.Join(s.DataDir, out["path"].(string)))
	if !strings.Contains(string(src), "\ngrant: "+issued.URL+"\n") {
		t.Errorf("not marked:\n%s", src)
	}
	if strings.Contains(descriptionFor(t, h, "signin-token"), `"site.set_name"`) {
		t.Error("a publish-only sign-in token is offered site.set_name")
	}
	if code := send(t, h, "GET", "/v1/query?pql=all+posts+from+me", "signin-token", ""); code == http.StatusOK {
		t.Error("a publish-only token reads the owner's posts")
	}
	_ = connection.Withdraw(s.connectionConfig(), issued.ID)
	if code := send(t, h, "POST", "/v1/content/post", "signin-token", `{"markdown":"# Again"}`); code != http.StatusForbidden {
		t.Errorf("after withdrawal = %d", code)
	}
	if code := send(t, h, "POST", "/v1/content/post", "unknown-token", `{"markdown":"# No"}`); code != http.StatusForbidden {
		t.Errorf("unknown token = %d", code)
	}
}

// ⭐ The owner's settings list every connection (personal tokens and
// sign-ins alike) with what it may do and when it last acted, and Switch off
// withdraws its grant, revokes its key and tells the host to revoke its
// sign-in tokens.
func TestConnectionsListAndSwitchOff(t *testing.T) {
	s, h := queryRoutes(t)
	token, key := mintVia(t, s, "Script", "publish")
	if code := send(t, h, "POST", "/v1/content/post", token, `{"markdown":"# x"}`); code != http.StatusCreated {
		t.Fatalf("publish = %d", code)
	}
	signIn, _ := connection.Issue(s.connectionConfig(), "Claude Desktop", "Claude", []string{"read"})
	var revoked []string
	s.ConnectionOff = func(_ context.Context, id string) { revoked = append(revoked, id) }

	// ⚠️ By name, not position: grants written in the same second have no
	// order between them.
	list := s.connectionsSettings()
	byName := map[string]connectionView{}
	for _, c := range list {
		byName[c.Name] = c
	}
	if len(list) != 2 || byName["Claude Desktop"].Via != "sign-in" || byName["Script"].Via != "token" || byName["Script"].LastUsed == "" || !byName["Script"].Live {
		t.Fatalf("list = %+v", list)
	}
	grantID := grantIDOf(t, s, key["id"].(string))
	w := httptest.NewRecorder()
	s.handleConnectionOff(w, httptest.NewRequest(http.MethodPost, "/api/connections/off", strings.NewReader(`{"grant_id":"`+grantID+`"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("off = %d %s", w.Code, w.Body.String())
	}
	if code := send(t, h, "POST", "/v1/content/post", token, `{"markdown":"# y"}`); code == http.StatusCreated {
		t.Error("a switched-off token still publishes")
	}
	if _, ok := ops.LookupAPIKey(s.Site(), token); ok {
		t.Error("the key was not revoked")
	}
	if len(revoked) != 1 || revoked[0] != grantID {
		t.Errorf("host told = %v", revoked)
	}
	for _, c := range s.connectionsSettings() {
		if c.GrantID == grantID && c.Live {
			t.Error("still listed as on")
		}
		if c.Grant == signIn.URL && !c.Live {
			t.Error("the other connection was switched off too")
		}
	}
	w = httptest.NewRecorder()
	s.handleConnectionOff(w, httptest.NewRequest(http.MethodPost, "/api/connections/off", strings.NewReader(`{"grant_id":"nope"}`)))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown = %d", w.Code)
	}
}

// ⭐ #8: a token missing a scope is told which one, in the header MCP's
// step-up reads, as well as the body.
func TestAMissingScopeIsNamedForStepUp(t *testing.T) {
	s, h := queryRoutes(t)
	token, _ := mintVia(t, s, "reader", "read")
	req := httptest.NewRequest("POST", "/v1/content/post", strings.NewReader(`{"markdown":"# x"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || w.Header().Get("WWW-Authenticate") != `Bearer error="insufficient_scope", scope="publish"` {
		t.Errorf("= %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
}

// ⛔ Listing keys from another page (an <img> on a sibling tenant, any page
// against localhost) never signs a grant; the owner's own request does.
func TestRenewalNeedsTheSitesOwnRequest(t *testing.T) {
	s := newConfiguredServer(t)
	if _, _, err := ops.MintAPIKey(s.Site(), "old", []string{"read"}); err != nil {
		t.Fatal(err)
	}
	list := func(site string) string {
		req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		s.handleAPIKeys(w, req)
		return w.Body.String()
	}
	for _, site := range []string{"same-site", "cross-site"} {
		if strings.Contains(list(site), `"grant":`) {
			t.Errorf("a %s list renewed a key", site)
		}
	}
	if !strings.Contains(list("same-origin"), `"grant":`) {
		t.Error("the owner's own list did not renew")
	}
}

// ⛔ An agent cannot send a comment signed earlier, which would publish the
// owner's unmarked words as its act.
func TestAgentsCannotSendCommentsSignedEarlier(t *testing.T) {
	s, h := queryRoutes(t)
	token, _ := mintVia(t, s, "agent", "comment")
	code, out := apiCall(t, h, "POST", "/v1/content/comment", token, `{"comment_id":"20261113-anything"}`)
	if code/100 == 2 {
		t.Errorf("= %d %v", code, out)
	}
}
