package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mcpgen"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// ⭐ The site's settings and facts over the API: a public read of what the
// site already publishes, and each enhance row as the app's own handler under
// its scope.
func TestSiteSettingsGoThroughTheAppsOwnHandlers(t *testing.T) {
	s, h := queryRoutes(t)
	profile := mint(t, s, "profile")
	appearance := mint(t, s, "appearance")
	status := mint(t, s, "status")
	publish := mint(t, s, "publish")

	siteFacts := func() map[string]any {
		t.Helper()
		w := get(t, h, "/v1/site", "")
		if w.Code != http.StatusOK {
			t.Fatalf("site.get = %d %s", w.Code, w.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	facts := siteFacts()
	if facts["public_key"] == "" || !strings.HasPrefix(asStr(facts["did"]), "did:web:") {
		t.Errorf("site.get = %v, want the key and did:web name", facts)
	}

	if code, out := apiCall(t, h, "POST", "/v1/site/name", profile, `{"author_name":"Vincent Test"}`); code != http.StatusOK {
		t.Fatalf("set_name = %d %v", code, out)
	}
	if code, out := apiCall(t, h, "POST", "/v1/site/about", profile, `{"content":"I write about **polis**."}`); code != http.StatusOK {
		t.Fatalf("set_about = %d %v", code, out)
	}
	facts = siteFacts()
	if facts["author_name"] != "Vincent Test" || !strings.Contains(asStr(facts["about"]), "**polis**") {
		t.Errorf("after the writes, site.get = %v", facts)
	}

	// Rebuild answers exactly what the app's re-render answers (a minimal
	// test site has no theme to render with, so both fail the same way).
	direct := httptest.NewRecorder()
	s.handleRenderPage(direct, httptest.NewRequest(http.MethodPost, "/api/render-page", strings.NewReader(`{"path":"/"}`)))
	if code, _ := apiCall(t, h, "POST", "/v1/site/rebuild", appearance, ``); code != direct.Code {
		t.Errorf("rebuild = %d, the app's own re-render = %d", code, direct.Code)
	}
	if code, out := apiCall(t, h, "GET", "/v1/site/status", status, ""); code != http.StatusOK || out["publish"] == nil {
		t.Errorf("status = %d %v", code, out)
	}

	for _, tc := range []struct{ method, path, token, body string }{
		{"POST", "/v1/site/name", publish, `{"author_name":"x"}`},
		{"POST", "/v1/site/about", appearance, `{"content":"x"}`},
		{"POST", "/v1/site/theme", profile, `{"theme":"vice"}`},
		{"POST", "/v1/site/rebuild", profile, ``},
		{"GET", "/v1/site/status", profile, ""},
		{"POST", "/v1/site/name", "", `{"author_name":"x"}`},
	} {
		if code, _ := apiCall(t, h, tc.method, tc.path, tc.token, tc.body); code != http.StatusForbidden && code != http.StatusUnauthorized {
			t.Errorf("%s %s with the wrong scope = %d, want a refusal", tc.method, tc.path, code)
		}
	}
	desc := fmtDescription(t, h)
	for _, id := range []string{"site.get", "site.set_name", "site.set_avatar", "site.set_about", "site.set_theme", "site.rebuild", "site.status"} {
		if !strings.Contains(desc, `"`+id+`"`) {
			t.Errorf("description lacks %s", id)
		}
	}
}

func asStr(v any) string         { s, _ := v.(string); return s }
func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

// policy:read reads the owner's rules, private then public; nothing else does.
func TestSitePolicyIsReadUnderPolicyRead(t *testing.T) {
	s, h := queryRoutes(t)
	code, out := apiCall(t, h, "GET", "/v1/site/policy", mint(t, s, "policy:read"), "")
	if code != http.StatusOK || out["private"] == nil || out["public"] == nil {
		t.Fatalf("policy = %d %v", code, out)
	}
	if code, _ := apiCall(t, h, "GET", "/v1/site/policy", mint(t, s, "read"), ""); code != http.StatusForbidden {
		t.Errorf("read scope on the rules = %d, want 403", code)
	}
	if !strings.Contains(fmtDescription(t, h), `"site.policy"`) {
		t.Error("description lacks site.policy")
	}
}

// ⛔ policy:write only tightens: a deny is added at the top of the private
// rules; allow and bless are refused; only an added rule can be released.
func TestPolicyWriteOnlyTightens(t *testing.T) {
	s, h := queryRoutes(t)
	pw := mint(t, s, "policy:write")
	rule := `{"rule":"deny pub.polis.comment from all at spam.example"}`
	code, out := apiCall(t, h, "POST", "/v1/site/policy/tighten", pw, rule)
	if code != http.StatusOK {
		t.Fatalf("tighten = %d %v", code, out)
	}
	priv, _ := out["private"].([]any)
	if len(priv) == 0 || !strings.Contains(asString(priv[0].(map[string]any)), "spam.example") {
		t.Errorf("private rules after tighten = %v, want the deny first", priv)
	}
	for _, loose := range []string{`{"rule":"allow pub.polis.dm from all"}`, `{"rule":"bless pub.polis.comment from all"}`} {
		if code, _ := apiCall(t, h, "POST", "/v1/site/policy/tighten", pw, loose); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", loose, code)
		}
	}
	// The owner's default public rules can't be released this way.
	if code, _ := apiCall(t, h, "POST", "/v1/site/policy/release", pw, `{"rule":"deny all from all"}`); code != http.StatusNotFound {
		t.Errorf("release an owner's rule = %d, want 404", code)
	}
	if code, _ := apiCall(t, h, "POST", "/v1/site/policy/release", pw, rule); code != http.StatusOK {
		t.Errorf("release the added rule = %d", code)
	}
	if code, _ := apiCall(t, h, "POST", "/v1/site/policy/tighten", mint(t, s, "policy:read"), rule); code != http.StatusForbidden {
		t.Errorf("policy:read tightened: %d", code)
	}
}

// ⭐ The token contract over the API: three operations under appearance. A
// change is checked whole (every reason named, nothing written), answers with
// the values from before, and putting those back with replace undoes it.
func TestDesignTokensAreSetAndResetUnderAppearance(t *testing.T) {
	s, h := queryRoutes(t)
	appearance := mint(t, s, "appearance")
	profile := mint(t, s, "profile")

	code, out := apiCall(t, h, "GET", "/v1/site/tokens", appearance, "")
	if code != http.StatusOK || out["contract_version"] == nil {
		t.Fatalf("theme.tokens = %d %v", code, out)
	}
	if toks, _ := out["tokens"].([]any); len(toks) < 10 {
		t.Errorf("theme.tokens lists %d tokens, want the whole contract", len(toks))
	}
	if fonts, _ := out["fonts"].([]any); len(fonts) == 0 {
		t.Error("theme.tokens names no font")
	}

	code, out = apiCall(t, h, "POST", "/v1/site/tokens", appearance, `{"tokens":{"site":"#b5562a","font-heading":"Lora"}}`)
	if code != http.StatusOK {
		t.Fatalf("set_tokens = %d %v", code, out)
	}
	own, _ := out["site_values"].(map[string]any)
	if own["site"] != "#b5562a" || own["font-heading"] != "Lora" {
		t.Errorf("after set_tokens, site_values = %v", own)
	}
	if prev, _ := out["previous"].(map[string]any); len(prev) != 0 {
		t.Errorf("previous = %v, want the empty values from before", prev)
	}

	// Refused whole: every reason, and nothing changes.
	code, out = apiCall(t, h, "POST", "/v1/site/tokens", appearance, `{"tokens":{"site":"red","font-ui":"Comic Sans MS","colour":"#fff"}}`)
	// The envelope keeps what the refusal said, inside error.
	reasons, _ := asMap(out["error"])["reasons"].([]any)
	if code != http.StatusBadRequest || len(reasons) != 3 {
		t.Errorf("a bad change = %d %v, want 400 naming all three", code, out)
	}
	code, out = apiCall(t, h, "POST", "/v1/site/tokens", appearance, `{"tokens":{"text-soft":"#cccccc"}}`)
	if code != http.StatusBadRequest || !strings.Contains(asStr(asMap(out["error"])["message"]), "contrast") {
		t.Errorf("a failing contrast = %d %v, want refused with the reason", code, out)
	}
	_, out = apiCall(t, h, "GET", "/v1/site/tokens", appearance, "")
	if own, _ := out["site_values"].(map[string]any); len(own) != 2 {
		t.Errorf("a refused change wrote something: %v", own)
	}

	// Randomise, then Revert: the previous values, put back whole.
	_, out = apiCall(t, h, "POST", "/v1/site/tokens", appearance, `{"tokens":{"bg":"#f3efe6","site":"#2a6f5a"}}`)
	prev, _ := json.Marshal(map[string]any{"tokens": out["previous"], "replace": true})
	_, out = apiCall(t, h, "POST", "/v1/site/tokens", appearance, string(prev))
	if own, _ := out["site_values"].(map[string]any); len(own) != 2 || own["site"] != "#b5562a" || own["bg"] != nil {
		t.Errorf("revert left %v, want exactly the values from before", own)
	}

	code, out = apiCall(t, h, "POST", "/v1/site/tokens/reset", appearance, `{"tokens":["site"]}`)
	if own, _ := out["site_values"].(map[string]any); code != http.StatusOK || own["site"] != nil || own["font-heading"] != "Lora" {
		t.Errorf("reset one = %d %v", code, out)
	}
	code, out = apiCall(t, h, "POST", "/v1/site/tokens/reset", appearance, `{"all":true}`)
	if own, _ := out["site_values"].(map[string]any); code != http.StatusOK || len(own) != 0 {
		t.Errorf("reset all = %d %v", code, out)
	}

	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/v1/site/tokens", ""},
		{"POST", "/v1/site/tokens", `{"tokens":{"site":"#b5562a"}}`},
		{"POST", "/v1/site/tokens/reset", `{"all":true}`},
	} {
		if code, _ := apiCall(t, h, tc.method, tc.path, profile, tc.body); code != http.StatusForbidden && code != http.StatusUnauthorized {
			t.Errorf("%s %s without appearance = %d, want a refusal", tc.method, tc.path, code)
		}
	}
	desc := fmtDescription(t, h)
	for _, id := range []string{"theme.tokens", "site.set_tokens", "site.reset_tokens"} {
		if !strings.Contains(desc, `"`+id+`"`) {
			t.Errorf("description lacks %s", id)
		}
	}

	// ⭐ As MCP tools, and small: the vocabulary is theme.tokens', never a
	// schema's, so no tool lists the tokens.
	ts, err := mcpgen.FromDescription([]byte(desc))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range ts.Tools {
		found[tool.Name] = true
		schema, _ := json.Marshal(tool.InputSchema)
		for _, tok := range theme.V5Tokens {
			if strings.Contains(tok.Name, "-") && strings.Contains(string(schema), `"`+tok.Name+`"`) {
				t.Errorf("%s's schema lists the token %s", tool.Name, tok.Name)
			}
		}
	}
	for _, want := range []string{"theme_tokens", "site_set_tokens", "site_reset_tokens"} {
		if !found[want] {
			t.Errorf("no MCP tool %s", want)
		}
	}
}
