package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// bearerTransport adds a bearer token, as v0 does.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// mcpSession connects the SDK's own client to /mcp, the way an outside
// consumer does: over HTTP, with only a key.
func mcpSession(t *testing.T, s *Server, token string) *mcp.ClientSession {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestMCPRefusesARequestWithoutAValidKey(t *testing.T) {
	s := newConfiguredServer(t)
	for _, auth := range []string{"", "Bearer polis_wrong", "Basic abc"} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		s.handleMCP(w, req)
		if w.Code != http.StatusUnauthorized || !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("auth %q: got %d %q, want 401 with WWW-Authenticate: Bearer", auth, w.Code, w.Header().Get("WWW-Authenticate"))
		}
	}
}

// v0 tries the optional server-to-client stream with a GET and carries on
// when it is refused (measured 2026-10-16/17).
func TestMCPAnswersGETWith405(t *testing.T) {
	s := newConfiguredServer(t)
	token, _, err := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	s.handleMCP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp = %d, want 405", w.Code)
	}
}

// ⭐ v0's handshake exactly as measured on 2026-10-16/17: protocol 2025-11-25
// in the body and the header, both content types accepted.
func TestMCPAnswersV0sHandshake(t *testing.T) {
	s := newConfiguredServer(t)
	token, _, err := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25",` +
		`"capabilities":{},"clientInfo":{"name":"ai-sdk-mcp-client","version":"1.0.0"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	req.Header.Set("User-Agent", "v0bot")
	w := httptest.NewRecorder()
	s.handleMCP(w, req)
	var resp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusOK {
		t.Fatalf("initialize = %d %s", w.Code, w.Body.String())
	}
	if resp.Result.ProtocolVersion != "2025-11-25" || resp.Result.ServerInfo.Name != "polis" || resp.Result.Instructions == "" {
		t.Errorf("initialize result = %+v, want protocol 2025-11-25 from server polis, with instructions", resp.Result)
	}
}

func TestMCPListsTheDemoToolsAndGetSiteOffersOnlyChoosableThemes(t *testing.T) {
	s := newConfiguredServer(t)
	setupTestTheme(t, s, "sols")
	setupTestTheme(t, s, "turbo")
	token, _, err := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	cs := mcpSession(t, s, token)

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
		if len(tl.Description) < 40 {
			t.Errorf("tool %s has a description too thin to choose from: %q", tl.Name, tl.Description)
		}
	}
	for _, want := range []string{"get_site", "list_posts", "set_theme", "get_custom_css", "set_custom_css", "get_publish_status"} {
		if !names[want] {
			t.Errorf("tools/list lacks %s", want)
		}
	}

	// Non-vacuity: the browser app's own list does carry the reserved theme.
	w := httptest.NewRecorder()
	s.handleSettings(w, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if !strings.Contains(w.Body.String(), `"sols"`) {
		t.Fatalf("the settings list no longer carries the reserved theme, so this test proves nothing: %s", w.Body.String())
	}
	text, isErr := callText(t, cs, "get_site", nil)
	if isErr {
		t.Fatalf("get_site failed: %s", text)
	}
	var site struct {
		Themes []struct {
			Name string `json:"name"`
		} `json:"themes"`
	}
	if err := json.Unmarshal([]byte(text), &site); err != nil {
		t.Fatalf("get_site is not JSON: %v %s", err, text)
	}
	got := map[string]bool{}
	for _, th := range site.Themes {
		got[th.Name] = true
	}
	if got["sols"] || !got["turbo"] {
		t.Errorf("get_site themes = %v, want turbo and never the reserved sols", got)
	}
}

// ⛔ A write tool needs the render scope. Only render exists, so the key
// lacking it is written by hand with a scope no tool grants.
func TestMCPRefusesAWriteToAKeyWithoutRenderScope(t *testing.T) {
	s := newConfiguredServer(t)
	setupTestTheme(t, s, "turbo")
	token := "polis_" + strings.Repeat("ab", 32)
	seed := `{"keys":[{"id":"key_x","name":"other","key_hash":"` + hashForTest(token) + `","created_at":"","scopes":["other"]}]}`
	if err := os.WriteFile(filepath.Join(s.DataDir, ".polis", "api-keys.json"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	cs := mcpSession(t, s, token)

	text, isErr := callText(t, cs, "set_theme", map[string]any{"theme": "turbo"})
	if !isErr || !strings.Contains(text, `"render" scope`) {
		t.Errorf("set_theme with a key lacking render = %v %q, want a scope refusal", isErr, text)
	}
	// A read needs only a valid key.
	if text, isErr := callText(t, cs, "get_publish_status", nil); isErr {
		t.Errorf("get_publish_status with a valid key failed: %s", text)
	}
}

func TestMCPRecordsEveryToolCallForTheOwner(t *testing.T) {
	s := newConfiguredServer(t)
	setupTestTheme(t, s, "turbo")
	token, key, err := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	cs := mcpSession(t, s, token)
	callText(t, cs, "get_publish_status", nil)
	callText(t, cs, "set_theme", map[string]any{"theme": "no-such-theme"})

	w := httptest.NewRecorder()
	s.handleMCPCalls(w, httptest.NewRequest(http.MethodGet, "/api/mcp/calls", nil))
	var resp struct {
		Calls []mcpCall `json:"calls"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Calls) != 2 {
		t.Fatalf("call log = %d %s, want two calls", w.Code, w.Body.String())
	}
	first, second := resp.Calls[0], resp.Calls[1]
	if first.Tool != "get_publish_status" || !first.OK || first.KeyID != key.ID {
		t.Errorf("first call = %+v", first)
	}
	if second.Tool != "set_theme" || second.OK || second.Error == "" || !strings.Contains(second.Args, "no-such-theme") {
		t.Errorf("second call = %+v, want a failed set_theme with its arguments and error", second)
	}
	if strings.Contains(w.Body.String(), token) {
		t.Error("the call log carries the key itself")
	}

	// ?since= keeps only what came after.
	w = httptest.NewRecorder()
	s.handleMCPCalls(w, httptest.NewRequest(http.MethodGet, "/api/mcp/calls?since="+second.TS, nil))
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Calls) != 1 {
		t.Errorf("since filter = %s, want one call", w.Body.String())
	}
}

// ⭐ Two calls v0 makes in parallel are both logged, even when they start in
// the same instant: each is its own object, so neither write can drop the
// other. A call logged by an earlier build, in the single-file log, still
// shows.
func TestMCPCallLogKeepsParallelCallsAndTheOldLog(t *testing.T) {
	s := newConfiguredServer(t)
	at := time.Now().UTC()
	old, _ := json.Marshal(mcpCall{TS: at.Add(-time.Minute).Format(time.RFC3339Nano), KeyID: "k", Tool: "get_site", OK: true})
	if err := s.Site().Store().Put(t.Context(), mcpCallsKey, append(old, '\n')); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, tool := range []string{"set_nav_shortcuts", "get_publish_status"} {
		wg.Add(1)
		go func() { defer wg.Done(); s.recordMCPCall(t.Context(), ops.APIKey{ID: "k"}, tool, nil, at, nil) }()
	}
	wg.Wait()
	all := s.readMCPCalls(t.Context(), time.Time{}, 0)
	if len(all) != 3 || all[0].Tool != "get_site" {
		t.Fatalf("calls = %+v, want the old call then both parallel ones", all)
	}
	tools := map[string]bool{all[1].Tool: true, all[2].Tool: true}
	if !tools["set_nav_shortcuts"] || !tools["get_publish_status"] {
		t.Errorf("parallel calls = %+v, want both", all[1:])
	}
	if got := s.readMCPCalls(t.Context(), at, 0); len(got) != 2 {
		t.Errorf("since the parallel calls = %d calls, want 2", len(got))
	}
	if got := s.readMCPCalls(t.Context(), time.Time{}, 1); len(got) != 1 || got[0].TS != at.Format(time.RFC3339Nano) {
		t.Errorf("limit 1 = %+v, want only a newest call", got)
	}
}

func TestMCPCallLogIsOwnerOnly(t *testing.T) {
	s, _ := passkeyServer(t)
	if code, _ := gateRefusal(t, s, "/api/mcp/calls"); code == http.StatusOK {
		t.Error("/api/mcp/calls passed the owner gate without a session")
	}
}

func hashForTest(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ⭐ The demo's non-trivial change, end to end through MCP: the owner's AI reads
// the theme's properties, writes CSS over them, and the site's styles.css
// carries it after the theme; a later theme switch keeps it.
func TestMCPSetCustomCSSLandsInStylesAndSurvivesAThemeSwitch(t *testing.T) {
	s := newConfiguredServer(t)
	// A real site's installed bundle, so the re-render runs as it does live.
	if err := bundle.EnsureReferencePayload(s.Site(), "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	token, _, err := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	cs := mcpSession(t, s, token)
	if text, isErr := callText(t, cs, "set_theme", map[string]any{"theme": "turbo"}); isErr {
		t.Fatalf("set_theme: %s", text)
	}

	text, _ := callText(t, cs, "get_site", nil)
	if !strings.Contains(text, `"active_theme_properties": [`) || !strings.Contains(text, `"name": "--color-`) {
		t.Errorf("get_site does not list the active theme's custom properties: %s", text)
	}

	css := ":root {\n  --color-accent: #b45309;\n}\n"
	if text, isErr := callText(t, cs, "set_custom_css", map[string]any{"css": css}); isErr {
		t.Fatalf("set_custom_css: %s", text)
	}
	styles, err := os.ReadFile(filepath.Join(s.DataDir, "styles.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(styles), css) || len(styles) < len(css)+1000 {
		t.Errorf("styles.css (%d bytes) does not end with the custom CSS after the theme's", len(styles))
	}
	if text, _ := callText(t, cs, "get_custom_css", nil); !strings.Contains(text, "#b45309") {
		t.Errorf("get_custom_css = %s", text)
	}

	if text, isErr := callText(t, cs, "set_theme", map[string]any{"theme": "vice"}); isErr {
		t.Fatalf("set_theme vice: %s", text)
	}
	styles, _ = os.ReadFile(filepath.Join(s.DataDir, "styles.css"))
	if !strings.HasSuffix(string(styles), css) {
		t.Errorf("after a theme switch, styles.css lost the custom CSS (%d bytes)", len(styles))
	}

	text, isErr := callText(t, cs, "set_custom_css", map[string]any{"css": "@import url(https://evil.example/x.css);"})
	if !isErr || !strings.Contains(text, "@import") {
		t.Errorf("a stylesheet import was not refused with its reason: %v %s", isErr, text)
	}
}
