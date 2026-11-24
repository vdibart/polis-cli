package mcpgen_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vdibart/polis-cli/cli-go/pkg/agentguide"
	"github.com/vdibart/polis-cli/cli-go/pkg/mcpgen"
	"github.com/vdibart/polis-cli/cli-go/pkg/styleguide"
)

// ⭐ Over the protocol: a client lists the generated tools with their hints
// and calls one through to the site.
func TestServerOffersAndRunsTheTools(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"title":"hello"}]}`))
	}))
	defer site.Close()
	ts, err := mcpgen.FromDescription(description(t))
	if err != nil {
		t.Fatal(err)
	}
	srv := mcpgen.NewServer(ts, &mcpgen.Executor{Base: site.URL}, "test")
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range list.Tools {
		byName[tool.Name] = tool
	}
	if a := byName["post_create"].Annotations; a == nil || a.ReadOnlyHint {
		t.Errorf("post_create offered as read-only: %+v", a)
	}
	if a := byName["post_list"].Annotations; a == nil || !a.ReadOnlyHint {
		t.Errorf("post_list not read-only: %+v", a)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "query_pql", Arguments: map[string]any{"pql": "all posts"}})
	if err != nil || res.IsError {
		t.Fatalf("query_pql: %v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "hello") || !strings.Contains(text, mcpgen.Note) {
		t.Errorf("query_pql answered %s", text)
	}
}

// ⭐ Over the protocol: tools carry their titles, and the prompts this
// connection can carry out are listed and answer with their request.
func TestServerOffersTitlesAndPrompts(t *testing.T) {
	ts, err := mcpgen.FromDescription(description(t))
	if err != nil {
		t.Fatal(err)
	}
	srv := mcpgen.NewServer(ts, &mcpgen.Executor{Base: "https://alice.example"}, "test")
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Title == "" {
			t.Errorf("%s is listed with no title", tool.Name)
		}
	}
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts.Prompts) != len(ts.Prompts) || len(ts.Prompts) == 0 {
		t.Fatalf("prompts listed = %d, toolset has %d", len(prompts.Prompts), len(ts.Prompts))
	}
	p := prompts.Prompts[0]
	got, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: p.Name})
	if err != nil {
		t.Fatal(err)
	}
	want := ""
	for _, tp := range ts.Prompts {
		if tp.Name == p.Name {
			want = tp.Text
		}
	}
	if len(got.Messages) != 1 || got.Messages[0].Content.(*mcp.TextContent).Text != want || want == "" {
		t.Errorf("prompt %s answered %+v", p.Name, got)
	}
}

// ⭐ A client is told at initialize where the v5 style guide is, and can read
// it there: the same text as the published docs page.
func TestAClientIsGivenTheStyleGuide(t *testing.T) {
	ts, err := mcpgen.FromDescription(description(t))
	if err != nil {
		t.Fatal(err)
	}
	srv := mcpgen.NewServer(ts, &mcpgen.Executor{Base: "http://unused.invalid"}, "test")
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if !strings.Contains(cs.InitializeResult().Instructions, styleguide.URI) {
		t.Errorf("the instructions do not say where the style guide is: %s", cs.InitializeResult().Instructions)
	}
	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: styleguide.URI})
	if err != nil || len(res.Contents) != 1 || res.Contents[0].Text != styleguide.Text() || !strings.HasPrefix(res.Contents[0].Text, "# polis v5 style guide") {
		t.Fatalf("reading %s: %v %+v", styleguide.URI, err, res)
	}
}

// ⭐ Every MCP server serves the workflow guides beside the style guide, and
// names them at initialize, so any agent (not only one with a polis skill)
// can read how to title a post, draft a reply, review blessings and treat
// other people's content.
func TestAClientIsGivenTheWorkflowGuides(t *testing.T) {
	ts, err := mcpgen.FromDescription(description(t))
	if err != nil {
		t.Fatal(err)
	}
	srv := mcpgen.NewServer(ts, &mcpgen.Executor{Base: "http://unused.invalid"}, "test")
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, r := range list.Resources {
		listed[r.URI] = true
	}
	if !listed[styleguide.URI] {
		t.Error("the style guide is not listed")
	}
	for _, g := range agentguide.Guides() {
		if !listed[g.URI] {
			t.Errorf("%s is not listed", g.URI)
		}
		if !strings.Contains(cs.InitializeResult().Instructions, g.URI) {
			t.Errorf("the instructions do not name %s", g.URI)
		}
		res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: g.URI})
		if err != nil || len(res.Contents) != 1 || res.Contents[0].Text == "" || res.Contents[0].Text != g.Text() {
			t.Errorf("reading %s: %v %+v", g.URI, err, res)
		}
	}
}
