package mcpgen

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vdibart/polis-cli/cli-go/pkg/agentguide"
	"github.com/vdibart/polis-cli/cli-go/pkg/styleguide"
)

// Instructions are sent to every client at initialize.
var Instructions = "These tools act on one polis site: a personal website whose posts and comments are signed " +
	"with its owner's key. Read before you change anything, and change only what the owner asked for: a post, " +
	"comment or blessing goes out signed in the owner's name. " + Note + " Some things only the owner can do; " +
	"for those, call how_do_i and give the owner its steps. Before changing how the site looks, read the " +
	"resource " + styleguide.URI + ": the rules every v5 page follows (a theme supplies tokens, never layout). " +
	"How to suggest a title, draft a reply in the owner's voice, review comments waiting for a blessing, and " +
	"treat other people's content are guides you can read: " + agentguide.URIs() + "."

// NewServer is an MCP server offering a toolset, calling the site through ex.
// The same server runs over any transport: stdio for `polis mcp`, HTTP for a
// hosted MCP endpoint.
func NewServer(ts *Toolset, ex *Executor, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "polis", Version: version},
		&mcp.ServerOptions{Instructions: Instructions})
	for _, t := range ts.Tools {
		t := t
		destructive := t.Destructive
		openWorld := true
		tool := &mcp.Tool{
			Name: t.Name, Title: t.Title, Description: t.Description, InputSchema: t.InputSchema,
			// ⛔ A client decides whether to ask the owner first from these:
			// a write is never read-only, and destructive is said outright.
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: t.ReadOnly, DestructiveHint: &destructive, OpenWorldHint: &openWorld},
		}
		srv.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return errorResult("the arguments are not a JSON object"), nil
				}
			}
			out, err := ts.Call(ctx, ex, t.Name, args)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil
		})
	}
	// The v5 style guide, which the instructions point at: the same text as
	// the public docs page and the instance's /for/agents/style-guide.
	srv.AddResource(&mcp.Resource{
		URI: styleguide.URI, Name: "style-guide", Title: "The v5 style guide",
		Description: "The rules every v5 page follows: tokens, the page frame, cards, alignment, words, small screens.",
		MIMEType:    "text/markdown",
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
			{URI: styleguide.URI, MIMEType: "text/markdown", Text: styleguide.Text()},
		}}, nil
	})
	// The workflow guides, which the instructions name: the know-how any
	// agent needs to do the common things well, served by every MCP server so
	// no agent needs a skill of its own.
	for _, g := range agentguide.Guides() {
		g := g
		srv.AddResource(&mcp.Resource{
			URI: g.URI, Name: g.Name, Title: g.Title, Description: g.Description, MIMEType: agentguide.MIMEType,
		}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: g.URI, MIMEType: agentguide.MIMEType, Text: g.Text()},
			}}, nil
		})
	}
	// Ready-made requests, offered as suggestions (MCP prompts): only those
	// this connection can carry out.
	for _, p := range ts.Prompts {
		p := p
		srv.AddPrompt(&mcp.Prompt{Name: p.Name, Title: p.Title, Description: p.Description},
			func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Description: p.Description, Messages: []*mcp.PromptMessage{
					{Role: "user", Content: &mcp.TextContent{Text: p.Text}},
				}}, nil
			})
	}
	return srv
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// OnToolCall calls record once for every tools/call the server answers, as the
// call ended: the tool's name, when it started, and the failure (nil when it
// succeeded; a tool result marked as an error counts as a failure, with its
// text). It is how each host logs pub.polis.mcp.tool_call, the owner's record
// of what their AI did, so every MCP door logs the same event the same way.
func OnToolCall(srv *mcp.Server, record func(tool string, started time.Time, err error)) {
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, isCall := req.(*mcp.CallToolRequest)
			if !isCall || method != "tools/call" {
				return next(ctx, method, req)
			}
			started := time.Now()
			res, err := next(ctx, method, req)
			failed := err
			if r, ok := res.(*mcp.CallToolResult); ok && err == nil && r.IsError {
				failed = errors.New(resultText(r))
			}
			name := ""
			if call.Params != nil {
				name = call.Params.Name
			}
			record(name, started, failed)
			return res, err
		}
	})
}

func resultText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
