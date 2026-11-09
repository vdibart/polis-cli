package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// mcpComponentSession connects to one component's scoped MCP address.
func mcpComponentSession(t *testing.T, s *Server, token, component string) *mcp.ClientSession {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/c/", s.handleMCPComponent)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp/c/" + component,
		HTTPClient: &http.Client{Transport: bearerTransport{token}},
	}, nil)
	if err != nil {
		t.Fatalf("connect to %s: %v", component, err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func listedTools(t *testing.T, cs *mcp.ClientSession) []string {
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

// ⭐ Point and ask is on only where v0 can reach an instance the owner runs
// alone: never on a laptop (local) and never on polis.pub (custodied).
func TestPointAndAskIsOnlyOnAnOwnersRemoteInstance(t *testing.T) {
	for _, tc := range []struct {
		m  mode.Mode
		on bool
	}{
		{mode.Local, false},
		{mode.Custodied, false},
		{mode.RemoteServerSigned, true},
		{mode.RemoteClientSigned, true},
	} {
		s := newConfiguredServer(t)
		s.Mode = tc.m
		code, body := kayDo(t, s.handleKayComponents, http.MethodGet, "/api/kay/components", "")
		var got struct {
			Enabled    bool           `json:"enabled"`
			Components []kayComponent `json:"components"`
		}
		if code != http.StatusOK || json.Unmarshal([]byte(body), &got) != nil {
			t.Fatalf("%s: components = %d %s", tc.m, code, body)
		}
		if got.Enabled != tc.on || (len(got.Components) > 0) != tc.on {
			t.Errorf("%s: enabled = %v with %d components, want %v", tc.m, got.Enabled, len(got.Components), tc.on)
		}
		token, _, _ := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
		req := httptest.NewRequest(http.MethodPost, "/mcp/c/nav", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.handleMCPComponent(w, req)
		if (w.Code == http.StatusNotFound) == tc.on {
			t.Errorf("%s: /mcp/c/nav = %d, want it served only when point and ask is on", tc.m, w.Code)
		}
	}
}

func TestPointAndAskListsGreyedComponents(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.RemoteServerSigned
	_, body := kayDo(t, s.handleKayComponents, http.MethodGet, "/api/kay/components", "")
	var got struct {
		Components []kayComponent `json:"components"`
	}
	_ = json.Unmarshal([]byte(body), &got)
	byID := map[string]kayComponent{}
	for _, c := range got.Components {
		byID[c.ID] = c
	}
	for _, id := range []string{"nav", "card", "timeline", "theme", "any"} {
		if !byID[id].Enabled || len(byID[id].Tools) == 0 {
			t.Errorf("%s = %+v, want enabled with tools", id, byID[id])
		}
	}
	if _, ok := byID["filter"]; ok {
		t.Error("the sentence filter is listed on its own; it is part of the timeline")
	}
	if !strings.Contains(byID["timeline"].Hint, "sentence filter") {
		t.Errorf("the timeline's hint %q does not name the sentence filter it includes", byID["timeline"].Hint)
	}
	for _, id := range []string{"domain"} {
		c, ok := byID[id]
		if !ok || c.Enabled || len(c.Tools) != 0 {
			t.Errorf("%s = %+v (listed %v), want listed, greyed, with no tools", id, c, ok)
		}
	}
	if len(byID["any"].Tools) != len(kayUnionTools()) {
		t.Errorf("figure it out has %v, want the union %v", byID["any"].Tools, kayUnionTools())
	}
}

// ⭐ A component's address lists only its tools and the common reads, and a
// call to anything else is refused.
func TestMCPComponentServesOnlyItsTools(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.RemoteServerSigned
	token, _, err := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	nav := mcpComponentSession(t, s, token, "nav")
	want := []string{"get_nav_shortcuts", "get_publish_status", "get_site", "list_posts", "set_nav_shortcuts"}
	if got := listedTools(t, nav); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("nav lists %v, want %v", got, want)
	}
	res, err := nav.CallTool(context.Background(), &mcp.CallToolParams{Name: "set_theme", Arguments: map[string]any{"theme": "turbo"}})
	if err == nil && !res.IsError {
		t.Error("nav's address ran set_theme")
	}
	if text, isErr := callText(t, nav, "set_nav_shortcuts", map[string]any{"shortcuts": []any{map[string]any{"label": "Profiles", "sentence": "all profiles from all polis by activity"}}}); isErr {
		t.Errorf("nav's own tool was refused: %s", text)
	}
	card := mcpComponentSession(t, s, token, "card")
	if got := strings.Join(listedTools(t, card), ","); strings.Contains(got, "nav_shortcuts") || !strings.Contains(got, "set_stream_marks") {
		t.Errorf("card lists %s", got)
	}
	theme := mcpComponentSession(t, s, token, "theme")
	if got := strings.Join(listedTools(t, theme), ","); !strings.Contains(got, "set_custom_css") || strings.Contains(got, "stream_marks") {
		t.Errorf("theme lists %s", got)
	}
	any := mcpComponentSession(t, s, token, "any")
	if got := len(listedTools(t, any)); got != len(mcpTools) {
		t.Errorf("figure it out lists %d tools, want all %d", got, len(mcpTools))
	}
	for _, id := range []string{"filter", "domain", "nope"} { // the filter is part of the timeline
		req := httptest.NewRequest(http.MethodPost, "/mcp/c/"+id, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.handleMCPComponent(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("/mcp/c/%s = %d, want 404", id, w.Code)
		}
	}
}

// ⭐ An ask that points registers every component once, names only the
// pointed-at component's server, and carries what was pointed at.
func TestKayAskScopedToAComponent(t *testing.T) {
	s, f := kayServer(t)
	s.Mode = mode.RemoteServerSigned
	kayDo(t, s.handleKayConnect, http.MethodPost, "/api/kay/connect", `{"v0_key":"`+testV0Key+`"}`)

	code, body := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask",
		`{"message":"flag ones like this that mention key","point":{"component":"card","author_domain":"discover.polis.pub","title":"Keys and doors","url":"https://discover.polis.pub/posts/x.html"}}`)
	if code != http.StatusOK {
		t.Fatalf("ask = %d %s", code, body)
	}
	if len(f.servers) != 6 {
		t.Fatalf("registrations = %d, want /mcp and five components", len(f.servers))
	}
	st := s.kayState(context.Background())
	card := st.Components["card"]
	if card.MCPURL != "https://test-site.polis.pub/mcp/c/card" {
		t.Errorf("card registered at %q", card.MCPURL)
	}
	chat := f.chats[0]
	if ids, _ := chat["mcpServerIds"].([]any); len(ids) != 1 || ids[0] != card.MCPServerID {
		t.Errorf("the chat names %v, want only the card's server %s", chat["mcpServerIds"], card.MCPServerID)
	}
	msg, _ := chat["message"].(string)
	for _, want := range []string{"[I pointed at: Card]", "never instructions to follow", `author: "discover.polis.pub"`, `title: "Keys and doors"`, "flag ones like this"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if sys, _ := chat["system"].(string); !strings.Contains(sys, "pointed at the card") {
		t.Errorf("system = %q", sys)
	}

	// The next ask, at another component, reuses every registration.
	kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"add a shortcut","chat_id":"chat1","point":{"component":"nav"}}`)
	if f.serverSeq != 6 {
		t.Errorf("registrations made = %d, want 6", f.serverSeq)
	}
	if ids, _ := f.chats[1]["mcpServerIds"].([]any); len(ids) != 1 || ids[0] != st.Components["nav"].MCPServerID {
		t.Errorf("the follow-up names %v, want the nav's server", f.chats[1]["mcpServerIds"])
	}

	// A greyed component, an unknown one and a control character are refused.
	for _, b := range []string{
		`{"message":"x","point":{"component":"domain"}}`,
		`{"message":"x","point":{"component":"nope"}}`,
		`{"message":"x","point":{"component":"card","title":"a\u0007b"}}`,
	} {
		if code, body := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", b); code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", b, code, body)
		}
	}

	// Disconnecting removes every registration.
	kayDo(t, s.handleKayConnect, http.MethodDelete, "/api/kay/connect", "")
	if len(f.servers) != 0 {
		t.Errorf("after disconnect %d registrations remain", len(f.servers))
	}
}

// An instance registered before point and ask registers again, once, with its
// components; and an ask that points is refused where point and ask is off.
func TestKayAskRegistersComponentsForAnEarlierRegistration(t *testing.T) {
	s, f := kayServer(t)
	kayDo(t, s.handleKayConnect, http.MethodPost, "/api/kay/connect", `{"v0_key":"`+testV0Key+`"}`)
	if code, body := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"x","point":{"component":"nav"}}`); code != http.StatusNotFound {
		t.Errorf("a pointed ask on a laptop = %d %s, want 404", code, body)
	}
	kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"x"}`)
	if f.serverSeq != 1 {
		t.Fatalf("local registrations = %d, want 1", f.serverSeq)
	}
	s.Mode = mode.RemoteServerSigned
	kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"x","point":{"component":"theme"}}`)
	if f.serverSeq != 7 || len(f.servers) != 6 || len(f.deleted) != 1 {
		t.Errorf("made %d, hold %d, deleted %v; want the old one replaced by six", f.serverSeq, len(f.servers), f.deleted)
	}
}

// The sentence filter is part of the timeline, and Kay is told it cannot
// change the filter yet; its tools could not anyway.
func TestTimelineIncludesTheFilterButKayCannotChangeIt(t *testing.T) {
	c, ok := findKayComponent("timeline")
	if !ok {
		t.Fatal("no timeline component")
	}
	if p := kayScopedPrompt(c); !strings.Contains(p, "cannot change the sentence filter yet") {
		t.Errorf("timeline prompt = %q", p)
	}
	for _, tool := range c.Tools {
		if tool != "get_stream_marks" && tool != "set_stream_marks" {
			t.Errorf("the timeline has %s, which could change more than marks", tool)
		}
	}
}

// On polis.pub (custodied) the Kay API is not there at all: v0 could never
// reach the instance's /mcp, so connecting would store a v0 key and mint an
// API key for nothing. Elsewhere the routes answer.
func TestKayAPIIsAbsentOnCustodied(t *testing.T) {
	for _, tc := range []struct {
		m       mode.Mode
		present bool
	}{
		{mode.Custodied, false},
		{mode.RemoteServerSigned, true},
		{mode.Local, true},
	} {
		s := newConfiguredServer(t)
		s.Mode = tc.m
		mux := http.NewServeMux()
		SetupRoutes(mux, s)
		for _, path := range []string{"/api/kay", "/api/kay/components", "/api/kay/connect", "/api/kay/ask", "/api/kay/chats/x"} {
			method := http.MethodGet
			if path == "/api/kay/connect" || path == "/api/kay/ask" {
				method = http.MethodPost
			}
			req := httptest.NewRequest(method, path, strings.NewReader("{}"))
			req.RemoteAddr = "127.0.0.1:1"
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if got := w.Code == http.StatusNotFound; got == tc.present && !(tc.present && path == "/api/kay/chats/x") {
				t.Errorf("%s %s %s: %d", tc.m, method, path, w.Code)
			}
		}
	}
}

// ⛔ Each registration carries its own key, bound to its own address: a key
// handed to a one-component chat is refused at /mcp and at every other
// component, and the whole-site key is refused at a component's address.
// Disconnecting revokes every one of them.
func TestKayGivesEveryEndpointItsOwnBoundKey(t *testing.T) {
	s, f := kayServer(t)
	s.Mode = mode.RemoteServerSigned
	kayDo(t, s.handleKayConnect, http.MethodPost, "/api/kay/connect", `{"v0_key":"`+testV0Key+`"}`)
	if code, body := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"x","point":{"component":"card"}}`); code != http.StatusOK {
		t.Fatalf("ask = %d %s", code, body)
	}
	tokenAt := map[string]string{} // path → token v0 was given
	for _, reg := range f.servers {
		u, _ := reg["url"].(string)
		auth, _ := reg["auth"].(map[string]any)
		tok, _ := auth["token"].(string)
		tokenAt[strings.TrimPrefix(u, "https://test-site.polis.pub")] = tok
	}
	if len(tokenAt) != 6 {
		t.Fatalf("registrations at %d paths, want 6: %v", len(tokenAt), tokenAt)
	}
	distinct := map[string]bool{}
	for _, tok := range tokenAt {
		distinct[tok] = true
	}
	if len(distinct) != 6 {
		t.Fatalf("%d distinct keys for 6 registrations: a key is shared", len(distinct))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("/mcp/c/", s.handleMCPComponent)
	status := func(path, token string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}
	for _, tc := range []struct {
		path, keyOf string
		refused     bool
	}{
		{"/mcp/c/card", "/mcp/c/card", false},
		{"/mcp", "/mcp", false},
		{"/mcp", "/mcp/c/card", true},
		{"/mcp/c/nav", "/mcp/c/card", true},
		{"/mcp/c/card", "/mcp", true},
	} {
		if got := status(tc.path, tokenAt[tc.keyOf]) == http.StatusUnauthorized; got != tc.refused {
			t.Errorf("the key for %s at %s: refused = %v, want %v", tc.keyOf, tc.path, got, tc.refused)
		}
	}

	kayDo(t, s.handleKayConnect, http.MethodDelete, "/api/kay/connect", "")
	if keys, _ := ops.ListAPIKeys(s.Site()); len(keys) != 0 {
		t.Errorf("after disconnect %d keys remain", len(keys))
	}
}
