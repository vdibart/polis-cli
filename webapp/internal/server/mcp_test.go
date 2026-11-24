package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vdibart/polis-cli/cli-go/pkg/agentguide"
	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/mcpgen"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// bearerTransport adds a bearer token, as v0 does.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// mcpSite is a configured site with its routes, /v1 mounted, as an instance
// serves them.
func mcpSite(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	return queryRoutes(t)
}

// mcpSession connects the SDK's own client to /mcp, the way an outside
// consumer does: over HTTP, with only a key.
func mcpSession(t *testing.T, h http.Handler, token string) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(h)
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
	s, _ := mcpSite(t)
	token := mint(t, s, "appearance")
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
	s, _ := mcpSite(t)
	token := mint(t, s, "appearance")
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

// toolNames is what tools/list offers, sorted.
func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// describedTools is what mcpgen makes of the site's description as /v1 shows
// it to token: the tools /mcp must offer that key, no more and no fewer.
func describedTools(t *testing.T, h http.Handler, token string) []string {
	t.Helper()
	w := get(t, h, "/v1/openapi.json", token)
	if w.Code != http.StatusOK {
		t.Fatalf("description = %d", w.Code)
	}
	ts, err := mcpgen.FromDescription(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range ts.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// ⭐ /mcp offers exactly the tools generated from the site's own description,
// for the key presenting the request, and none of the old hand-written ones.
func TestMCPOffersTheToolsGeneratedFromTheSitesOwnDescription(t *testing.T) {
	s, h := mcpSite(t)
	for _, scopes := range [][]string{{"appearance", "read", "status"}, {"read"}, {"publish", "comment"}} {
		token := mint(t, s, scopes...)
		got := toolNames(t, mcpSession(t, h, token))
		want := describedTools(t, h, token)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%v: /mcp offers %v, the description makes %v", scopes, got, want)
		}
		if len(got) < 3 {
			t.Errorf("%v: only %v offered, so this test proves little", scopes, got)
		}
		for _, old := range []string{"get_site", "list_posts", "set_theme", "get_custom_css", "set_custom_css",
			"get_stream_marks", "set_stream_marks", "get_nav_shortcuts", "set_nav_shortcuts", "get_publish_status"} {
			for _, name := range got {
				if name == old {
					t.Errorf("%v: the retired tool %s is still offered", scopes, old)
				}
			}
		}
	}
}

// ⭐ The instance's /mcp serves the workflow guides beside the style guide,
// whatever the key's grant: they read nothing of the site.
func TestMCPServesTheWorkflowGuides(t *testing.T) {
	s, h := mcpSite(t)
	cs := mcpSession(t, h, mint(t, s, "read"))
	for _, g := range agentguide.Guides() {
		res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: g.URI})
		if err != nil || len(res.Contents) != 1 || res.Contents[0].Text != g.Text() {
			t.Errorf("reading %s over /mcp: %v %+v", g.URI, err, res)
		}
	}
}

// ⭐ A call is the API's own operation: it reaches /v1, changes what /v1
// changes, and answers what /v1 answers.
func TestMCPCallReachesTheSitesAPI(t *testing.T) {
	s, h := mcpSite(t)
	setupTestTheme(t, s, "turbo")
	token := mint(t, s, "appearance", "read")
	cs := mcpSession(t, h, token)
	if text, isErr := callText(t, cs, "site_set_theme", map[string]any{"theme": "turbo"}); isErr {
		t.Fatalf("site_set_theme: %s", text)
	}
	if w := get(t, h, "/v1/site", token); !strings.Contains(w.Body.String(), "turbo") {
		t.Errorf("after site_set_theme, /v1/site = %s", w.Body.String())
	}
	text, isErr := callText(t, cs, "site_get", nil)
	if isErr {
		t.Fatalf("site_get: %s", text)
	}
	var viaTool struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &viaTool); err != nil {
		t.Fatalf("site_get is not JSON: %v %s", err, text)
	}
	if w := get(t, h, "/v1/site", token); strings.TrimSpace(string(viaTool.Result)) != strings.TrimSpace(w.Body.String()) {
		t.Errorf("site_get answered\n%s\n/v1/site answers\n%s", viaTool.Result, w.Body.String())
	}
}

// ⛔ /v1 decides what a key may do, on every call. A key made before keys
// carried a grant is shown the tools of its scopes but /v1 refuses its write,
// and the tool says so; the site is unchanged.
func TestMCPWriteOutsideTheKeysGrantIsRefusedByTheAPI(t *testing.T) {
	s, h := mcpSite(t)
	setupTestTheme(t, s, "turbo")
	token, _, err := ops.MintAPIKey(s.Site(), "old", []string{"appearance"})
	if err != nil {
		t.Fatal(err)
	}
	cs := mcpSession(t, h, token)
	text, isErr := callText(t, cs, "site_set_theme", map[string]any{"theme": "turbo"})
	if !isErr || !strings.Contains(text, "403") {
		t.Errorf("site_set_theme with no grant = %v %q, want the API's 403", isErr, text)
	}
	if w := get(t, h, "/v1/site", mint(t, s, "read")); strings.Contains(w.Body.String(), `"turbo"`) {
		t.Errorf("the refused write changed the theme: %s", w.Body.String())
	}
	// And a read-only key is not offered a write at all.
	for _, name := range toolNames(t, mcpSession(t, h, mint(t, s, "read"))) {
		if strings.HasPrefix(name, "site_set_") || name == "post_create" {
			t.Errorf("a read-only key is offered %s", name)
		}
	}
}

// ⛔ A key bound to /mcp works at /mcp and, through it, at /v1, but presented
// to /v1 directly it is no key; a key bound anywhere else is refused at /mcp.
// Before this, /v1 never looked at a key's binding.
func TestMCPKeyBindingHoldsAtMCPAndAtTheAPI(t *testing.T) {
	s, h := mcpSite(t)
	s.Mode = mode.RemoteServerSigned
	// A personal token with its own grant, bound to /mcp.
	scopes, err := connection.Scopes([]string{"read", "status", "appearance"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.connectionConfig()
	issued, err := connection.Issue(cfg, "an agent on mcp", "test", scopes)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := ops.MintBoundPersonalToken(s.Site(), "bound to /mcp", scopes, issued.URL, issued.ID, mcpEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	if text, isErr := callText(t, mcpSession(t, h, token), "site_status", nil); isErr {
		t.Errorf("a key bound to /mcp at /mcp: %s", text)
	}
	// /v1 answers a key it does not accept as it answers any invalid key.
	if w := get(t, h, "/v1/site/status", token); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Invalid API key") {
		t.Errorf("a key bound to /mcp presented to /v1 directly = %d %s, want refused as an invalid key", w.Code, w.Body.String())
	}
	elsewhere, _, err := ops.MintBoundAPIKey(s.Site(), "elsewhere", []string{"appearance"}, "/mcp/c/card")
	if err != nil {
		t.Fatal(err)
	}
	if code := mcpStatus(s, elsewhere, "198.51.100.3"); code != http.StatusUnauthorized {
		t.Errorf("a key bound to another endpoint at /mcp = %d, want 401", code)
	}
}

// Point and ask and the Kay page are retired: none of their addresses is served.
func TestMCPComponentAddressesAreGone(t *testing.T) {
	s, _ := mcpSite(t)
	s.Mode = mode.RemoteServerSigned
	webFS, _ := fs.Sub(webui.Assets, "www")
	routes := editorRoutes(s, webFS)
	for _, path := range []string{"/mcp/c/card", "/api/kay/components", "/api/kay", "/api/kay/ask", "/api/mcp/calls"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.RemoteAddr = "127.0.0.1:1"
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, w.Code)
		}
	}
}

func TestMCPCallLogIsOwnerOnly(t *testing.T) {
	s, _ := passkeyServer(t)
	if code, _ := gateRefusal(t, s, "/api/mcp/calls"); code == http.StatusOK {
		t.Error("/api/mcp/calls passed the owner gate without a session")
	}
}

// ⭐ The owner's record of what their AI did: every tools/call logs
// pub.polis.mcp.tool_call as it ended, with the tool, the key, whether it
// worked and how long it took (the same event polis.pub's /mcp logs; one
// implementation, mcpgen.OnToolCall, serves both doors).
func TestMCPToolCallLogsItsEvent(t *testing.T) {
	s, h := mcpSite(t)
	logsDir := filepath.Join(t.TempDir(), "logs")
	s.Logger = NewLogger(LogLevelBasic, logsDir)
	token := mint(t, s, "read")
	cs := mcpSession(t, h, token)
	if text, isErr := callText(t, cs, "site_get", nil); isErr {
		t.Fatalf("site_get: %s", text)
	}
	entries, _ := os.ReadDir(logsDir)
	var logs strings.Builder
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(logsDir, e.Name()))
		logs.Write(b)
	}
	for _, line := range strings.Split(logs.String(), "\n") {
		if !strings.Contains(line, "[EVENT:pub.polis.mcp.tool_call]") {
			continue
		}
		for _, want := range []string{"tool=site_get", "ok=true", "key_id=", "duration_ms="} {
			if !strings.Contains(line, want) {
				t.Errorf("tool_call lacks %s: %s", want, line)
			}
		}
		return
	}
	t.Fatalf("no pub.polis.mcp.tool_call logged:\n%s", logs.String())
}
