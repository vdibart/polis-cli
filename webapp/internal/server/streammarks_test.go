package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// ⭐ The owner's rule marks exactly the posts by that author that mention a
// keyword, and the mark comes from the server, in the stream's own response.
func TestStreamMarksMarkTheMatchingPostsOnly(t *testing.T) {
	s := newConfiguredServer(t)
	now := time.Now().UTC()
	seedNetworkFeed(t, s, []feed.FeedItem{
		{Type: "post", Title: "What if social didn't require a platform?", URL: "posts/a.md", AuthorDomain: "discover.polis.pub", AuthorURL: "https://discover.polis.pub", Published: now.Add(-time.Hour).Format(time.RFC3339)},
		{Type: "post", Title: "Signed markdown, no middleman", URL: "posts/b.md", AuthorDomain: "discover.polis.pub", AuthorURL: "https://discover.polis.pub", Published: now.Add(-2 * time.Hour).Format(time.RFC3339)},
		{Type: "post", Title: "My platform thoughts", URL: "posts/c.md", AuthorDomain: "other.pub", AuthorURL: "https://other.pub", Published: now.Add(-3 * time.Hour).Format(time.RFC3339)},
	})
	w := httptest.NewRecorder()
	s.handleStreamMarks(w, httptest.NewRequest(http.MethodPost, "/api/stream-marks",
		strings.NewReader(`{"rules":[{"author_domain":"discover.polis.pub","any_of":["Platform"]}]}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("set marks = %d %s", w.Code, w.Body.String())
	}
	code, resp := streamRequest(t, s, "type=posts")
	if code != http.StatusOK {
		t.Fatalf("stream = %d", code)
	}
	marked := map[string]string{}
	for _, it := range resp["items"].([]interface{}) {
		m := it.(map[string]interface{})
		mark, _ := m["mark"].(string)
		marked[m["title"].(string)] = mark
	}
	if marked["What if social didn't require a platform?"] != "alert" {
		t.Errorf("the matching post is not marked: %v", marked)
	}
	if marked["Signed markdown, no middleman"] != "" || marked["My platform thoughts"] != "" {
		t.Errorf("a post that should not match was marked: %v", marked)
	}
}

func TestStreamMarksRefuseBadRules(t *testing.T) {
	s := newConfiguredServer(t)
	for _, body := range []string{`not json`, `{"rules":[{"author_domain":"discover.polis.pub","any_of":["a.*b"]}]}`, `{"rules":[{"any_of":["x"]}]}`} {
		w := httptest.NewRecorder()
		s.handleStreamMarks(w, httptest.NewRequest(http.MethodPost, "/api/stream-marks", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", body, w.Code)
		}
	}
}

// ⭐ v0's path: set_stream_marks over MCP with a render-scoped key.
func TestMCPSetsStreamMarksAndNavShortcuts(t *testing.T) {
	s := newConfiguredServer(t)
	token, _, err := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	cs := mcpSession(t, s, token)
	if text, isErr := callText(t, cs, "set_stream_marks", map[string]any{"rules": []any{map[string]any{"author_domain": "discover.polis.pub", "any_of": []string{"platform"}}}}); isErr {
		t.Fatalf("set_stream_marks: %s", text)
	}
	if text, _ := callText(t, cs, "get_stream_marks", nil); !strings.Contains(text, "discover.polis.pub") {
		t.Errorf("get_stream_marks = %s", text)
	}
	if text, isErr := callText(t, cs, "set_nav_shortcuts", map[string]any{"shortcuts": []any{map[string]any{"label": "Discover, discussed", "icon": "chat", "sentence": "all posts from discover.polis.pub with comments"}}}); isErr {
		t.Fatalf("set_nav_shortcuts: %s", text)
	}
	text, isErr := callText(t, cs, "set_nav_shortcuts", map[string]any{"shortcuts": []any{map[string]any{"label": "x", "sentence": "https://evil.example/"}}})
	if !isErr || !strings.Contains(text, "PQL") {
		t.Errorf("a URL as a sentence = %v %s, want refused naming PQL", isErr, text)
	}
	w := httptest.NewRecorder()
	s.handleNavShortcuts(w, httptest.NewRequest(http.MethodGet, "/api/nav-shortcuts", nil))
	var got struct {
		Shortcuts []struct{ Label, Icon, Sentence string } `json:"shortcuts"`
		Icons     []string                                 `json:"icons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.Shortcuts) != 1 || got.Shortcuts[0].Sentence != "all posts from discover.polis.pub with comments" || len(got.Icons) == 0 {
		t.Errorf("nav shortcuts after a refused set = %s", w.Body.String())
	}
}

func TestStreamMarkAndShortcutRoutesAreOwnerOnly(t *testing.T) {
	s, _ := passkeyServer(t)
	for _, p := range []string{"/api/stream-marks", "/api/nav-shortcuts"} {
		if code, _ := gateRefusal(t, s, p); code == http.StatusOK {
			t.Errorf("%s passed the owner gate without a session", p)
		}
	}
}

// v0 rewrote "all profiles from all polis by activity" as "all profiles from
// all of polis by date" when the tool gave it two examples and no grammar.
// The tool now states PQL's words, and a refusal names them.
func TestMCPNavShortcutsTellTheCallerThePQLWords(t *testing.T) {
	s := newConfiguredServer(t)
	token, _, err := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	cs := mcpSession(t, s, token)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var desc string
	for _, tool := range tools.Tools {
		if tool.Name == "set_nav_shortcuts" {
			desc = tool.Description
		}
	}
	for _, want := range []string{"all polis", "by activity", "profiles", "words exactly"} {
		if !strings.Contains(desc, want) {
			t.Errorf("set_nav_shortcuts description omits %q: %s", want, desc)
		}
	}
	text, isErr := callText(t, cs, "set_nav_shortcuts", map[string]any{"shortcuts": []any{map[string]any{"label": "Profiles", "sentence": "all profiles from all of polis by date"}}})
	if !isErr || !strings.Contains(text, `"all polis"`) || !strings.Contains(text, "by activity") {
		t.Errorf("refusal = %v %s, want it to name the valid words", isErr, text)
	}
	if text, isErr := callText(t, cs, "set_nav_shortcuts", map[string]any{"shortcuts": []any{map[string]any{"label": "Profiles", "icon": "star", "sentence": "all profiles from all polis by activity"}}}); isErr {
		t.Errorf("the owner's sentence was refused: %s", text)
	}
}
