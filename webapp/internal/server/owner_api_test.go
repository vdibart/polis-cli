package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// ⭐ Every owner-only action the v5 interface draws (spec part 5) is a /v1
// operation (spec 1.2), and ⛔ every one is owner-only: the owner's sign-in or
// full-access token, never an agent's scope.
func TestTheOwnerOnlyActionsAreOwnerOnlyOperations(t *testing.T) {
	s := newConfiguredServer(t)
	var ids, signInOnly []string
	for _, hr := range s.ownerRoutes() {
		op := hr.Op
		ids = append(ids, op.ID())
		if !op.OwnerOnly || op.Auth != apidesc.AuthOwner || op.Scope != apidesc.ScopeFull {
			t.Errorf("%s: owner-only %v, auth %s, scope %s; want owner-only, owner, full", op.ID(), op.OwnerOnly, op.Auth, op.Scope)
		}
		if op.SignInOnly {
			signInOnly = append(signInOnly, op.ID())
		}
	}
	sort.Strings(ids)
	want := []string{
		"agent.disconnect", "agent.list_connections", "agent.list_tokens", "agent.make_token",
		"agent.revoke_token", "agent.rosie", "agent.set_rosie",
		"dm.change_password", "dm.keyring", "dm.new_key", "dm.rewrap_earlier", "dm.set_password",
		"license.set", "site.download", "site.reset_policy", "site.rotate_key", "site.set_policy",
	}
	if strings.Join(ids, " ") != strings.Join(want, " ") {
		t.Errorf("owner-only operations = %v, want %v", ids, want)
	}
	// ⛔ The three a leaked token must never do (Vincent, 2026-11-17).
	sort.Strings(signInOnly)
	if got := strings.Join(signInOnly, " "); got != "agent.make_token dm.change_password dm.new_key dm.rewrap_earlier dm.set_password site.download site.rotate_key" {
		t.Errorf("sign-in-only = %q, want the download, making a token, rotating the key and every change to the messages key", got)
	}
}

// ⛔ A sign-in-only operation refuses every token, the owner's full-access one
// included; the owner signed in (loopback, on a laptop) is served.
func TestSignInOnlyOperationsRefuseEveryToken(t *testing.T) {
	s, h := queryRoutes(t)
	s.BoundToLoopback = true
	full := mint(t, s)
	agent := mint(t, s, "publish", "read")
	signedIn := map[string]string{"Sec-Fetch-Site": "same-origin"}

	for _, c := range []struct{ method, path, body string }{
		{"GET", "/v1/site/download", ""},
		{"POST", "/v1/agents/tokens", `{"name":"script","scopes":["read"]}`},
		{"POST", "/v1/site/key/rotate", ""},
		{"POST", "/v1/messages/password", "{}"},
		{"POST", "/v1/messages/password/change", "{}"},
		{"POST", "/v1/messages/key/new", "{}"},
		{"POST", "/v1/messages/key/earlier", "{}"},
	} {
		for name, tok := range map[string]string{"the owner's full token": full, "an agent's token": agent} {
			if code := send(t, h, c.method, c.path, tok, c.body); code != http.StatusForbidden {
				t.Errorf("%s %s with %s = %d, want 403", c.method, c.path, name, code)
			}
		}
	}
	before, _ := ops.ListAPIKeys(s.Site())
	if code := ownerReq(t, h, "POST", "/v1/agents/tokens", `{"name":"script","scopes":["read"]}`, signedIn); code != http.StatusOK && code != http.StatusCreated {
		t.Errorf("the owner, signed in, makes a token = %d, want it made", code)
	}
	after, _ := ops.ListAPIKeys(s.Site())
	if len(after) != len(before)+1 {
		t.Errorf("tokens %d → %d: the signed-in owner's token was not made, or a refused request made one", len(before), len(after))
	}
	// The download is the owner's on loopback (or waits out its rate limit).
	if code := ownerReq(t, h, "GET", "/v1/site/download", "", signedIn); code != http.StatusOK && code != http.StatusTooManyRequests {
		t.Errorf("the owner, signed in, downloads = %d, want the archive", code)
	}
}

// ⛔ No agent scope reaches an owner-only operation; the owner's full token
// does, for those that are not sign-in-only.
func TestOwnerOnlyOperationsRefuseAgents(t *testing.T) {
	s, h := queryRoutes(t)
	full := mint(t, s)
	agent := mint(t, s, "publish", "read", "profile", "appearance")
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/v1/agents/tokens", ""},
		{"POST", "/v1/agents/tokens/revoke", `{"id":"x"}`},
		{"GET", "/v1/agents/connections", ""},
		{"POST", "/v1/agents/connections/disconnect", `{"grant_id":"x"}`},
		{"GET", "/v1/agents/rosie", ""},
		{"POST", "/v1/agents/rosie", `{"on":false}`},
		{"POST", "/v1/site/license", `{"profile":"reserved"}`},
		{"GET", "/v1/messages/keyring", ""},
	} {
		if code := send(t, h, c.method, c.path, agent, c.body); code != http.StatusForbidden {
			t.Errorf("an agent: %s %s = %d, want 403", c.method, c.path, code)
		}
		if code := send(t, h, c.method, c.path, "", c.body); code != http.StatusUnauthorized {
			t.Errorf("nobody: %s %s = %d, want 401", c.method, c.path, code)
		}
		if code := send(t, h, c.method, c.path, full, c.body); code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Errorf("the owner's full token: %s %s = %d, want it allowed", c.method, c.path, code)
		}
	}
}

// Revoking a token through the API ends it, by the id in the body.
func TestRevokeTokenEndsIt(t *testing.T) {
	s, h := queryRoutes(t)
	full := mint(t, s)
	mint(t, s, "read")
	keys, _ := ops.ListAPIKeys(s.Site())
	var id string
	for _, k := range keys {
		if k.GrantID != "" {
			id = k.ID
		}
	}
	if id == "" {
		t.Fatal("no agent token to revoke")
	}
	if code := send(t, h, "POST", "/v1/agents/tokens/revoke", full, `{"id":"`+id+`"}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 200", code)
	}
	after, _ := ops.ListAPIKeys(s.Site())
	for _, k := range after {
		if k.ID == id {
			t.Error("the revoked token is still listed")
		}
	}
	if code := send(t, h, "POST", "/v1/agents/tokens/revoke", full, `{"id":"../x"}`); code != http.StatusBadRequest {
		t.Errorf("an id with a path in it = %d, want 400", code)
	}
}

// ⛔ On polis.pub the site serves no download: downloading is polis.pub's
// verified export, an account action, so the key never leaves through /v1.
func TestPolisPubServesNoDownloadOperation(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.Custodied
	for _, hr := range s.ownerRoutes() {
		if hr.Op.ID() == "site.download" {
			t.Fatal("site.download is an operation on polis.pub")
		}
	}
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	h := editorRoutes(s, webFS)
	code := ownerReq(t, h, "GET", "/v1/site/download", "", map[string]string{OwnerSessionHeader: "1", "Sec-Fetch-Site": "same-origin"})
	if code == http.StatusOK {
		t.Errorf("the owner downloads through /v1 on polis.pub = %d, want refused", code)
	}
}

// The API's description marks each sign-in-only operation, so a client knows
// before calling.
func TestTheDescriptionMarksSignInOnlyOperations(t *testing.T) {
	_, h := queryRoutes(t)
	req := httptest.NewRequest("GET", "/v1/openapi.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var doc struct {
		Paths map[string]map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	op := doc.Paths["/v1/agents/tokens"]["post"]
	if op == nil || op["x-polis-sign-in-only"] != true || op["x-polis-owner-only"] != true {
		t.Errorf("agent.make_token is not described as owner-only and sign-in-only: %v", op)
	}
	if l := doc.Paths["/v1/agents/tokens"]["get"]; l == nil || l["x-polis-sign-in-only"] != nil {
		t.Errorf("agent.list_tokens is marked sign-in-only: %v", l)
	}
}

// ⭐ The owner changes their own rules (site.set_policy) and restores the
// defaults (site.reset_policy) on their full-access token; ⛔ an agent, even
// one granted policy:write, only tightens and never reaches either.
func TestTheOwnerSetsAndResetsTheirRulesAndAnAgentCannot(t *testing.T) {
	s, h := queryRoutes(t)
	full := mint(t, s)
	agent := mint(t, s, "policy:write", "policy:read")
	none := `{"which":"public","rules":["bless pub.polis.comment from self","review pub.polis.comment from all","deny all from all"]}`

	for _, c := range []struct{ path, body string }{{"/v1/site/policy", none}, {"/v1/site/policy/reset", ""}} {
		if code := send(t, h, "POST", c.path, agent, c.body); code != http.StatusForbidden {
			t.Errorf("an agent with policy:write: POST %s = %d, want 403", c.path, code)
		}
	}
	if code := send(t, h, "POST", "/v1/site/policy", full, none); code != http.StatusOK {
		t.Fatalf("the owner sets the public rules = %d", code)
	}
	w := get(t, h, "/v1/site/policy", full)
	if !strings.Contains(w.Body.String(), "review pub.polis.comment from all") || strings.Contains(w.Body.String(), "bless pub.polis.comment from following") {
		t.Errorf("rules after set: %s", w.Body.String())
	}

	// A list with a rule that does not parse changes nothing, and says which.
	bad := sendBody(t, h, "POST", "/v1/site/policy", full, `{"which":"public","rules":["bless pub.polis.comment from all","bless the moon"]}`)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), `"index":1`) {
		t.Errorf("an invalid list = %d %s", bad.Code, bad.Body.String())
	}
	if !strings.Contains(get(t, h, "/v1/site/policy", full).Body.String(), "review pub.polis.comment from all") {
		t.Error("a refused list changed the rules")
	}

	if code := send(t, h, "POST", "/v1/site/policy/reset", full, ""); code != http.StatusOK {
		t.Fatalf("reset = %d", code)
	}
	if !strings.Contains(get(t, h, "/v1/site/policy", full).Body.String(), "bless pub.polis.comment from following") {
		t.Error("the defaults are not back after a reset")
	}

	// Never a tool an agent is offered.
	desc := get(t, h, "/v1/openapi.json", agent).Body.String()
	if strings.Contains(desc, `"site.set_policy"`) || strings.Contains(desc, `"site.reset_policy"`) {
		t.Error("an agent's description offers set_policy or reset_policy")
	}
}
