package v0

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testKey = "v1:test:secret-key-value"

// fakeV0 records each request and answers from a table.
type fakeV0 struct {
	srv    *httptest.Server
	bodies map[string]map[string]any
	auth   []string
}

func newFakeV0(t *testing.T, answers map[string]struct {
	status int
	body   string
}) *fakeV0 {
	f := &fakeV0{bodies: map[string]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		key := r.Method + " " + r.URL.Path
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			f.bodies[key] = m
		}
		a, ok := answers[key]
		if !ok {
			http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(a.status)
		_, _ = io.WriteString(w, a.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type answer = struct {
	status int
	body   string
}

func TestMeChecksTheKeyAndNeverEchoesIt(t *testing.T) {
	f := newFakeV0(t, map[string]answer{
		"GET /user": {200, `{"id":"u1","name":"Owner"}`},
	})
	u, err := New(testKey, f.srv.URL).Me(context.Background())
	if err != nil || u.ID != "u1" {
		t.Fatalf("Me = %+v, %v", u, err)
	}
	if f.auth[0] != "Bearer "+testKey {
		t.Errorf("auth header = %q", f.auth[0])
	}

	bad := newFakeV0(t, map[string]answer{"GET /user": {401, `{"error":{"message":"Invalid API key"}}`}})
	_, err = New(testKey, bad.srv.URL).Me(context.Background())
	if !Unauthorized(err) || strings.Contains(err.Error(), testKey) || !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("a refused key = %v; want Unauthorized, v0's message, and never the key", err)
	}
}

func TestCreateMCPServerUsesBearerAuth(t *testing.T) {
	f := newFakeV0(t, map[string]answer{"POST /mcp-servers": {200, `{"id":"srv1","name":"polis","url":"https://x/mcp","enabled":true}`}})
	s, err := New(testKey, f.srv.URL).CreateMCPServer(context.Background(), "polis", "https://x/mcp", "d", "polis_tok")
	if err != nil || s.ID != "srv1" {
		t.Fatalf("CreateMCPServer = %+v, %v", s, err)
	}
	auth, _ := f.bodies["POST /mcp-servers"]["auth"].(map[string]any)
	if auth["type"] != "bearer" || auth["token"] != "polis_tok" {
		t.Errorf("auth = %v", auth)
	}
}

func TestDeleteMCPServerTreatsGoneAsDone(t *testing.T) {
	f := newFakeV0(t, nil)
	if err := New(testKey, f.srv.URL).DeleteMCPServer(context.Background(), "gone"); err != nil {
		t.Errorf("deleting a server already gone = %v", err)
	}
}

// ⭐ Every chat is async, private and limited to the servers named; ⛔ a chat
// naming none is refused here, because v0 would fall back to every server the
// account has enabled.
func TestAChatIsAsyncPrivateAndLimitedToItsServers(t *testing.T) {
	f := newFakeV0(t, map[string]answer{
		"POST /chats":             {200, `{"id":"c1","webUrl":"https://v0.app/chat/c1","messages":[]}`},
		"POST /chats/c1/messages": {200, `{"id":"c1","messages":[]}`},
	})
	c := New(testKey, f.srv.URL)
	ch, err := c.StartChat(context.Background(), Ask{Message: "hi", System: "be Kay", MCPServerIDs: []string{"srv1"}})
	if err != nil || ch.Key() != "c1" {
		t.Fatalf("StartChat = %+v, %v", ch, err)
	}
	b := f.bodies["POST /chats"]
	if b["responseMode"] != "async" || b["chatPrivacy"] != "private" || b["system"] != "be Kay" {
		t.Errorf("start body = %v", b)
	}
	if ids, _ := b["mcpServerIds"].([]any); len(ids) != 1 || ids[0] != "srv1" {
		t.Errorf("mcpServerIds = %v", b["mcpServerIds"])
	}
	if _, err := c.Continue(context.Background(), "c1", Ask{Message: "more", MCPServerIDs: []string{"srv1"}}); err != nil {
		t.Fatal(err)
	}
	if b := f.bodies["POST /chats/c1/messages"]; b["responseMode"] != "async" || b["mcpServerIds"] == nil {
		t.Errorf("continue body = %v", b)
	}
	if _, err := c.StartChat(context.Background(), Ask{Message: "hi"}); err == nil {
		t.Error("a chat with no MCP servers named was sent")
	}
}

// The shape measured on 2026-10-18 (one tool call, a search, a thought, and the
// final paragraph), written by hand.
const measuredContent = `[[0,[
 ["AssistantMessageContentPart",{"part":{"type":"task-search-tools-v1","taskNameActive":"Finding site tools","taskNameComplete":"Found site tools","createdAt":1792367376476,"finishedAt":1792367376510,"parts":[{"type":"starting-tools-search","query":"get_site"}]}}],
 ["AssistantMessageContentPart",{"part":{"type":"task-thinking-v1","taskNameActive":"Considering color choices","createdAt":1792367385221,"finishedAt":1792367386183,"parts":[{"type":"thinking-end","duration":1,"thought":"Burnt orange could be #B45309."}]}}],
 ["AssistantMessageContentPart",{"part":{"type":"task-dynamic-tool-v1","taskNameActive":"Applying palette","taskNameComplete":"Applied autumn palette","toolDisplayName":"set_custom_css","mcpName":"polis","createdAt":1792367387000,"finishedAt":1792367395555,"parts":[{"type":"starting-dynamic-tool-execution","toolName":"polis_set_custom_css","input":{"css":":root { --color-gold: #b45309; }"},"permissionPrompt":"I'm updating the site's colours."},{"type":"finished-dynamic-tool-execution","toolName":"polis_set_custom_css"}]}}],
 ["AssistantMessageContentPart",{"part":{"type":"some-future-part","taskNameActive":"?"}}],
 ["p",{},["text",{},"Updated the site. It's live now."]]
]]]`

func TestStepsReadsWhatV0Did(t *testing.T) {
	m := Message{Role: "assistant", FinishReason: "stop", Experimental: json.RawMessage(measuredContent)}
	steps := m.Steps()
	if len(steps) != 3 {
		t.Fatalf("steps = %+v, want search, thinking and tool (the unknown part skipped)", steps)
	}
	if steps[0].Kind != "search" || steps[0].Label != "Found site tools" || !steps[0].Finished {
		t.Errorf("search step = %+v", steps[0])
	}
	if steps[1].Kind != "thinking" || !strings.Contains(steps[1].Thought, "B45309") {
		t.Errorf("thinking step = %+v", steps[1])
	}
	tool := steps[2]
	if tool.Kind != "tool" || tool.Tool != "set_custom_css" || tool.Label != "Applied autumn palette" ||
		tool.Reason == "" || !strings.Contains(string(tool.Input), "#b45309") || !tool.Finished {
		t.Errorf("tool step = %+v", tool)
	}
	if !m.Done() {
		t.Error("a stopped assistant message is not done")
	}
	if (Message{Role: "assistant", FinishReason: "tool-calls"}).Done() {
		t.Error("a pause to call tools counted as done")
	}
}

// ⭐ An unrecognised shape costs the steps, never the message.
func TestStepsToleratesAChangedShape(t *testing.T) {
	for _, raw := range []string{``, `null`, `{"x":1}`, `[[0,"text"]]`, `[["AssistantMessageContentPart"]]`} {
		if s := (Message{Experimental: json.RawMessage(raw)}).Steps(); len(s) != 0 {
			t.Errorf("Steps(%s) = %+v, want none", raw, s)
		}
	}
}

// ⭐ v0's content runs its text blocks together, and sometimes the sentences
// inside one; Text gives each block its own paragraph and the sentences a
// space. The shape is the one measured on 2026-10-21.
func TestTextSeparatesWhatV0RunsTogether(t *testing.T) {
	raw := `[[0,[
 ["p",{},["text",{},"I'll start by finding the site tools.I'll read the nav first"]],
 ["AssistantMessageContentPart",{"part":{"type":"task-dynamic-tool-v1","toolDisplayName":"set_nav_shortcuts","parts":[]}}],
 ["p",{},["text",{},"I added a bolt icon. It opens \"all polis\" on v0.dev."]]
]]]`
	m := Message{Content: "I'll start by finding the site tools.I'll read the nav firstI added a bolt icon.", Experimental: json.RawMessage(raw)}
	want := "I'll start by finding the site tools. I'll read the nav first\n\nI added a bolt icon. It opens \"all polis\" on v0.dev."
	if got := m.Text(); got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
	for _, raw := range []string{``, `null`, `[[0,"text"]]`, `[[0,[["AssistantMessageContentPart",{}]]]]`} {
		if got := (Message{Content: "as sent", Experimental: json.RawMessage(raw)}).Text(); got != "as sent" {
			t.Errorf("Text(%s) = %q, want the content as sent", raw, got)
		}
	}
}
