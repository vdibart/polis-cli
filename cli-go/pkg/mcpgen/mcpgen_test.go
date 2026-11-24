package mcpgen_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mcpgen"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// A query operation as a site describes one, beside the core operations.
var queryOp = apidesc.Operation{
	Type: "pub.polis.query", Action: "pql", Title: "Find posts, comments and people", Method: "GET", Path: "/v1/query",
	Summary: "Read content with a PQL sentence", Description: "Answers PQL.",
	Auth: apidesc.AuthPublicOrOwner, Scope: apidesc.ScopeRead,
	QueryParams: map[string]string{"pql": "The sentence.", "counts": "Types to count."}, RequiredQuery: []string{"pql"},
}

func description(t *testing.T) []byte {
	t.Helper()
	all := append([]apidesc.Operation{queryOp}, ops.NewBuiltinCoreHandler().Operations()...)
	doc := apidesc.Describe(all, apidesc.Manual(), capability.For(mode.RemoteServerSigned), apidesc.Options{SiteURL: "https://alice.example"})
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func tools(t *testing.T) map[string]mcpgen.Tool {
	t.Helper()
	ts, err := mcpgen.FromDescription(description(t))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]mcpgen.Tool{}
	for _, tool := range ts.Tools {
		out[tool.Name] = tool
	}
	return out
}

// ⭐ The tools are what the description offers an agent: no owner-only
// operation, nothing needing the owner's full token (messages), nothing
// another site calls; how_do_i for what only the owner can do.
func TestToolsAreWhatAnAgentMayDo(t *testing.T) {
	got := tools(t)
	for _, want := range []string{"query_pql", "post_create", "post_update", "comment_create", "tag_apply", "post_list", "follow_list", mcpgen.HowDoIName} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing tool %s", want)
		}
	}
	for name := range got {
		if strings.HasPrefix(name, "dm_") {
			t.Errorf("%s offered to an agent: messages need the owner's own token", name)
		}
	}
	if q := got["query_pql"]; q.InputSchema["required"] == nil || !strings.Contains(q.Description, "read scope") {
		t.Errorf("query tool = %+v", q)
	}
}

// ⛔ Taking a post down cannot be undone, so it is the owner's alone: no
// tool offers it, and how_do_i gives the owner the steps instead.
func TestUnpublishIsTheOwnersStepsNotATool(t *testing.T) {
	got := tools(t)
	if _, ok := got["post_unpublish"]; ok {
		t.Fatal("post_unpublish is offered to an agent")
	}
	ts, err := mcpgen.FromDescription(description(t))
	if err != nil {
		t.Fatal(err)
	}
	ans, err := ts.Call(context.Background(), &mcpgen.Executor{Base: "http://unused.invalid"}, mcpgen.HowDoIName, map[string]any{"what": "post.unpublish"})
	if err != nil || !strings.Contains(string(ans), "unpublish") || !strings.Contains(string(ans), "/_/write") {
		t.Errorf("how_do_i post.unpublish = %s %v", ans, err)
	}
}

// ⛔ The hints a client asks the owner by: a read is read-only, a write is not,
// a destructive write says so.
func TestToolsCarryTheDescriptionsHints(t *testing.T) {
	got := tools(t)
	if !got["post_list"].ReadOnly || got["post_create"].ReadOnly || got["post_create"].Destructive {
		t.Errorf("post hints: list %+v create %+v", got["post_list"], got["post_create"])
	}
	if !got["tag_delete"].Destructive || got["tag_delete"].ReadOnly {
		t.Errorf("tag_delete hints = %+v", got["tag_delete"])
	}
}

// ⭐ A call is the request the description says: path, query, body and the
// token; a read's result is labelled, and how_do_i answers from the manual
// entries without a request.
func TestCallsAreTheDescribedRequests(t *testing.T) {
	var seen *http.Request
	var seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen, seenBody = r.Clone(r.Context()), string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	ts, err := mcpgen.FromDescription(description(t))
	if err != nil {
		t.Fatal(err)
	}
	ex := &mcpgen.Executor{Base: srv.URL, Token: "tok"}
	ctx := context.Background()

	out, err := ts.Call(ctx, ex, "query_pql", map[string]any{"pql": "all posts from me", "counts": "posts"})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Method != "GET" || seen.URL.Path != "/v1/query" || seen.URL.Query().Get("pql") != "all posts from me" || seen.Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("query request = %s %s %v", seen.Method, seen.URL, seen.Header)
	}
	if !strings.Contains(string(out), mcpgen.Note) {
		t.Errorf("a read's result isn't labelled: %s", out)
	}

	if _, err := ts.Call(ctx, ex, "post_create", map[string]any{"markdown": "# Hi"}); err != nil {
		t.Fatal(err)
	}
	if seen.Method != "POST" || seen.URL.Path != "/v1/content/post" || !strings.Contains(seenBody, `"markdown":"# Hi"`) {
		t.Errorf("create request = %s %s %s", seen.Method, seen.URL, seenBody)
	}

	seen = nil
	ans, err := ts.Call(ctx, ex, mcpgen.HowDoIName, map[string]any{"what": "key.rotate"})
	if err != nil || seen != nil || !strings.Contains(string(ans), "Rotate") {
		t.Errorf("how_do_i = %s %v (request made: %v)", ans, err, seen != nil)
	}
}

// ⛔ No operation is written into the generator: every name comes from the
// description, so the build fails if one appears in this package's code.
func TestNoOperationIsWrittenIntoTheGenerator(t *testing.T) {
	var names []string
	for _, op := range append([]apidesc.Operation{queryOp}, ops.NewBuiltinCoreHandler().Operations()...) {
		names = append(names, op.ID(), strings.ReplaceAll(op.ID(), ".", "_"))
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			if strings.Contains(string(src), `"`+n+`"`) {
				t.Errorf("%s names the operation %s; tools must come from the description", f, n)
			}
		}
	}
}

// ⭐ The one tool not from the description: a Tron nod that answers without a
// request and is read-only.
func TestEndOfLine(t *testing.T) {
	ts, err := mcpgen.FromDescription(description(t))
	if err != nil {
		t.Fatal(err)
	}
	var tool *mcpgen.Tool
	for i := range ts.Tools {
		if ts.Tools[i].Name == mcpgen.EndOfLineName {
			tool = &ts.Tools[i]
		}
	}
	if tool == nil || !tool.ReadOnly || tool.Destructive {
		t.Fatalf("end_of_line = %+v", tool)
	}
	out, err := ts.Call(context.Background(), &mcpgen.Executor{Base: "http://127.0.0.1:1"}, mcpgen.EndOfLineName, nil)
	if err != nil || !strings.Contains(string(out), "End of line.") {
		t.Errorf("end_of_line answered %s %v", out, err)
	}
}

// ⛔ A tool calls the site's /v1 only, whatever a description says.
func TestToolsCallOnlyTheSitesAPI(t *testing.T) {
	desc := `{"openapi":"3.1.0","paths":{"@evil.example/x":{"get":{"operationId":"x.read","x-polis-auth":"owner","x-polis-scope":"read"}}}}`
	ts, err := mcpgen.FromDescription([]byte(desc))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Call(context.Background(), &mcpgen.Executor{Base: "https://alice.example"}, "x_read", nil); err == nil {
		t.Error("a path outside /v1/ was called")
	}
}

// ⭐ Every tool carries a title for a person, from its operation's declaration
// (x-polis-title); the two tools not from the description carry their own.
func TestEveryToolHasAHumanTitle(t *testing.T) {
	for name, tool := range tools(t) {
		if strings.TrimSpace(tool.Title) == "" {
			t.Errorf("%s has no title", name)
		}
		if tool.Title == tool.Name {
			t.Errorf("%s's title is its name", name)
		}
	}
}

// ⭐ A prompt is offered only where every operation it needs is a tool of
// this connection, so a suggestion never leads to a refusal.
func TestPromptsAreOnlyThoseTheToolsCanCarryOut(t *testing.T) {
	full, err := mcpgen.FromDescription(description(t))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tool := range full.Tools {
		have[tool.Name] = true
	}
	offered := map[string]bool{}
	for _, p := range full.Prompts {
		offered[p.Name] = true
		if p.Title == "" || p.Text == "" {
			t.Errorf("prompt %s has no title or text", p.Name)
		}
	}
	for _, p := range apidesc.Prompts() {
		can := true
		for _, n := range p.Needs {
			can = can && have[strings.ReplaceAll(n, ".", "_")]
		}
		if can != offered[p.Name] {
			t.Errorf("prompt %s: offered %v, but its operations are all tools: %v", p.Name, offered[p.Name], can)
		}
	}
	// A description with only reading offers only the prompts that read.
	var doc map[string]any
	_ = json.Unmarshal(description(t), &doc)
	paths := doc["paths"].(map[string]any)
	for p, item := range paths {
		for m, op := range item.(map[string]any) {
			if o, ok := op.(map[string]any); ok && o["x-polis-write"] == true {
				delete(item.(map[string]any), m)
			}
		}
		if len(item.(map[string]any)) == 0 {
			delete(paths, p)
		}
	}
	b, _ := json.Marshal(doc)
	reader, err := mcpgen.FromDescription(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range reader.Prompts {
		for _, want := range apidesc.Prompts() {
			if want.Name == p.Name && len(want.Needs) > 1 {
				t.Errorf("a read-only connection is offered %s, which writes", p.Name)
			}
		}
	}
	if len(reader.Prompts) == 0 {
		t.Error("a read-only connection is offered no prompt at all")
	}
}
