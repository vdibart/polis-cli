package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

const testV0Key = "v1:test:owner-v0-key"

// fakeV0API is v0's Platform API as Kay uses it.
type fakeV0API struct {
	mu        sync.Mutex
	srv       *httptest.Server
	servers   map[string]map[string]any // id → the registration body
	serverSeq int
	deleted   []string
	chats     []map[string]any // bodies of chat creates and messages
	chatJSON  string           // what GET /chats/{id} answers
}

func newFakeV0API(t *testing.T) *fakeV0API {
	f := &fakeV0API{servers: map[string]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+testV0Key {
			http.Error(w, `{"error":{"message":"Invalid API key"}}`, http.StatusUnauthorized)
			return
		}
		var body map[string]any
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &body)
		}
		p := r.URL.Path
		switch {
		case r.Method == http.MethodGet && p == "/user":
			io.WriteString(w, `{"id":"u1","name":"Owner"}`)
		case r.Method == http.MethodPost && p == "/mcp-servers":
			f.serverSeq++
			id := "srv" + string(rune('0'+f.serverSeq))
			f.servers[id] = body
			json.NewEncoder(w).Encode(map[string]any{"id": id, "name": body["name"], "url": body["url"], "enabled": true})
		case r.Method == http.MethodDelete && strings.HasPrefix(p, "/mcp-servers/"):
			id := strings.TrimPrefix(p, "/mcp-servers/")
			f.deleted = append(f.deleted, id)
			delete(f.servers, id)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && p == "/chats":
			f.chats = append(f.chats, body)
			io.WriteString(w, `{"id":"chat1","webUrl":"https://v0.app/chat/chat1","messages":[]}`)
		case r.Method == http.MethodPost && p == "/chats/chat1/messages":
			f.chats = append(f.chats, body)
			io.WriteString(w, `{"id":"chat1","messages":[]}`)
		case r.Method == http.MethodGet && p == "/chats/chat1":
			io.WriteString(w, f.chatJSON)
		default:
			http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func kayServer(t *testing.T) (*Server, *fakeV0API) {
	t.Helper()
	s := newConfiguredServer(t)
	f := newFakeV0API(t)
	s.kayV0Base = f.srv.URL
	return s, f
}

func kayDo(t *testing.T, h http.HandlerFunc, method, path, body string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	h(w, httptest.NewRequest(method, path, rd))
	return w.Code, w.Body.String()
}

func TestKayConnectChecksTheKeyAndNeverShowsIt(t *testing.T) {
	s, _ := kayServer(t)
	if code, body := kayDo(t, s.handleKayConnect, http.MethodPost, "/api/kay/connect", `{"v0_key":"v1:wrong"}`); code != http.StatusBadRequest {
		t.Fatalf("a key v0 refuses = %d %s, want 400", code, body)
	}
	if code, body := kayDo(t, s.handleKayConnect, http.MethodPost, "/api/kay/connect", `{"v0_key":" `+testV0Key+` "}`); code != http.StatusOK {
		t.Fatalf("connect = %d %s", code, body)
	}
	info, err := os.Stat(s.Site().Path(kayV0KeyKey))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the v0 key file = %v, %v; want mode 600", info, err)
	}
	code, body := kayDo(t, s.handleKay, http.MethodGet, "/api/kay", "")
	if code != http.StatusOK || !strings.Contains(body, `"connected":true`) || strings.Contains(body, testV0Key) {
		t.Errorf("status = %d %s; want connected, and never the key", code, body)
	}
}

// ⭐ The first ask registers the site's /mcp with v0 under a render-scoped key
// it mints, and the chat names only that server; the next ask reuses it.
func TestKayAskRegistersOnceAndLimitsTheChatToThisSite(t *testing.T) {
	s, f := kayServer(t)
	kayDo(t, s.handleKayConnect, http.MethodPost, "/api/kay/connect", `{"v0_key":"`+testV0Key+`"}`)

	code, body := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"make it autumn"}`)
	if code != http.StatusOK || !strings.Contains(body, `"chat_id":"chat1"`) {
		t.Fatalf("ask = %d %s", code, body)
	}
	if len(f.servers) != 1 {
		t.Fatalf("registrations = %d, want 1", len(f.servers))
	}
	reg := f.servers["srv1"]
	if reg["url"] != "https://test-site.polis.pub/mcp" {
		t.Errorf("registered url = %v", reg["url"])
	}
	auth, _ := reg["auth"].(map[string]any)
	token, _ := auth["token"].(string)
	key, ok := ops.LookupAPIKey(s.Site(), token)
	if auth["type"] != "bearer" || !ok || key.Unscoped() || !key.Allows(ops.ScopeRender) {
		t.Fatalf("v0 was given %v, which resolves to %+v (%v); want a render-scoped key", auth, key, ok)
	}
	chat := f.chats[0]
	if ids, _ := chat["mcpServerIds"].([]any); len(ids) != 1 || ids[0] != "srv1" {
		t.Errorf("the chat names servers %v, want only srv1", chat["mcpServerIds"])
	}
	if sys, _ := chat["system"].(string); !strings.Contains(sys, "You are Kay") {
		t.Errorf("system prompt = %q", sys)
	}

	// A follow-up continues the chat and reuses the registration.
	if code, body := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"warmer","chat_id":"chat1"}`); code != http.StatusOK {
		t.Fatalf("follow-up = %d %s", code, body)
	}
	if f.serverSeq != 1 || len(f.chats) != 2 {
		t.Errorf("registrations made = %d, chat calls = %d; want 1 and 2", f.serverSeq, len(f.chats))
	}

	// Revoking Kay's key makes the next ask register again, removing the old.
	if err := ops.RevokeAPIKey(s.Site(), key.ID); err != nil {
		t.Fatal(err)
	}
	kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"again"}`)
	if f.serverSeq != 2 || len(f.deleted) != 1 || f.deleted[0] != "srv1" {
		t.Errorf("after a revoke: registrations = %d, deleted = %v; want a new one and srv1 removed", f.serverSeq, f.deleted)
	}
}

func TestKayAskRefusesWithoutAKeyOrAPublicAddress(t *testing.T) {
	s, _ := kayServer(t)
	if code, _ := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"hi"}`); code != http.StatusConflict {
		t.Errorf("ask before connecting = %d, want 409", code)
	}
	kayDo(t, s.handleKayConnect, http.MethodPost, "/api/kay/connect", `{"v0_key":"`+testV0Key+`"}`)
	s.BaseURL = "http://localhost:8080"
	code, body := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"hi"}`)
	if code != http.StatusBadGateway || !strings.Contains(body, "public address") {
		t.Errorf("ask from localhost = %d %s, want a refusal naming the address", code, body)
	}
	if code, _ := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":""}`); code != http.StatusBadRequest {
		t.Errorf("an empty message = %d, want 400", code)
	}
}

// ⭐ The poll returns v0's messages with their steps, and the calls this site
// served for Kay's key, which carry each call's outcome.
func TestKayChatReturnsStepsAndThisSitesCalls(t *testing.T) {
	s, f := kayServer(t)
	kayDo(t, s.handleKayConnect, http.MethodPost, "/api/kay/connect", `{"v0_key":"`+testV0Key+`"}`)
	_, askBody := kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"make it autumn"}`)
	var asked struct {
		AskedAt string `json:"asked_at"`
	}
	json.Unmarshal([]byte(askBody), &asked)

	// One call by Kay's key, one by another key: only Kay's is shown.
	st := s.kayState(t.Context())
	kayKey := ops.APIKey{ID: st.APIKeyID, Name: kayKeyName}
	s.recordMCPCall(t.Context(), kayKey, "set_custom_css", json.RawMessage(`{"css":"a{}"}`), nowAfter(asked.AskedAt), nil)
	s.recordMCPCall(t.Context(), ops.APIKey{ID: "key_other"}, "get_site", nil, nowAfter(asked.AskedAt), nil)

	f.chatJSON = `{"id":"chat1","webUrl":"https://v0.app/chat/chat1","messages":[
	 {"role":"user","content":"make it autumn"},
	 {"role":"assistant","content":"Updated. It's live now.","finishReason":"stop","experimental_content":` + kayMeasuredContent + `}]}`
	code, body := kayDo(t, s.handleKayChat, http.MethodGet, "/api/kay/chats/chat1?since="+asked.AskedAt, "")
	if code != http.StatusOK {
		t.Fatalf("poll = %d %s", code, body)
	}
	var resp struct {
		Done     bool         `json:"done"`
		Messages []kayMessage `json:"messages"`
		Calls    []mcpCall    `json:"calls"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Done || len(resp.Messages) != 2 || len(resp.Messages[1].Steps) != 1 || resp.Messages[1].Steps[0].Tool != "set_custom_css" {
		t.Errorf("poll = %+v", resp)
	}
	if len(resp.Calls) != 1 || resp.Calls[0].Tool != "set_custom_css" {
		t.Errorf("calls = %+v, want only Kay's set_custom_css", resp.Calls)
	}

	// Mid-turn: an assistant message pausing for tools is not done.
	f.chatJSON = `{"id":"chat1","messages":[{"role":"user","content":"x"},{"role":"assistant","content":"","finishReason":"tool-calls"}]}`
	_, body = kayDo(t, s.handleKayChat, http.MethodGet, "/api/kay/chats/chat1", "")
	if !strings.Contains(body, `"done":false`) {
		t.Errorf("a turn still calling tools reads done: %s", body)
	}
}

func TestKayDisconnectRemovesTheRegistrationTheKeyAndTheFiles(t *testing.T) {
	s, f := kayServer(t)
	kayDo(t, s.handleKayConnect, http.MethodPost, "/api/kay/connect", `{"v0_key":"`+testV0Key+`"}`)
	kayDo(t, s.handleKayAsk, http.MethodPost, "/api/kay/ask", `{"message":"hi"}`)
	keyID := s.kayState(t.Context()).APIKeyID

	if code, body := kayDo(t, s.handleKayConnect, http.MethodDelete, "/api/kay/connect", ""); code != http.StatusNoContent {
		t.Fatalf("disconnect = %d %s", code, body)
	}
	if len(f.deleted) != 1 || s.apiKeyExists(keyID) {
		t.Errorf("deleted = %v, key still exists = %v", f.deleted, s.apiKeyExists(keyID))
	}
	for _, k := range []string{kayV0KeyKey, kayStateKey} {
		if _, err := os.Stat(s.Site().Path(k)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s survived a disconnect", k)
		}
	}
}

func TestKayRoutesAreOwnerOnly(t *testing.T) {
	s, _ := passkeyServer(t)
	for _, p := range []string{"/api/kay", "/api/kay/connect", "/api/kay/ask", "/api/kay/chats/x"} {
		if code, _ := gateRefusal(t, s, p); code == http.StatusOK {
			t.Errorf("%s passed the owner gate without a session", p)
		}
	}
}

const kayMeasuredContent = `[[0,[["AssistantMessageContentPart",{"part":{"type":"task-dynamic-tool-v1","taskNameComplete":"Applied autumn palette","toolDisplayName":"set_custom_css","createdAt":1792367387000,"finishedAt":1792367395555,"parts":[{"type":"starting-dynamic-tool-execution","input":{"css":"a{}"},"permissionPrompt":"Updating colours."}]}}],["p",{},["text",{},"Updated."]]]]]`

// nowAfter is a moment just after an RFC 3339 time.
func nowAfter(ts string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return t.Add(time.Second)
}
