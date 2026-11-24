package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mcpgen"
)

// ⭐ End to end: tools generated from a running site's own description, and
// called through it, do what the API does, under the token's scope.
func TestGeneratedToolsWorkAgainstARealSite(t *testing.T) {
	s, h := queryRoutes(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	desc, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ts, err := mcpgen.FromDescription(desc)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range ts.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"query_pql", "post_create", "site_get", "site_set_name", "comment_bless", "seen_mark", mcpgen.HowDoIName} {
		if !names[want] {
			t.Errorf("no %s tool generated from the site's description", want)
		}
	}
	for name := range names {
		if strings.HasPrefix(name, "dm_") {
			t.Errorf("%s offered to an agent", name)
		}
	}

	ctx := context.Background()
	ex := &mcpgen.Executor{Base: srv.URL, Token: mint(t, s, "publish", "read")}
	if out, err := ts.Call(ctx, ex, "post_create", map[string]any{"markdown": "# Through a tool\n\nbody"}); err != nil {
		t.Fatalf("post_create: %v", err)
	} else if !strings.Contains(string(out), "Through a tool") {
		t.Errorf("post_create answered %s", out)
	}
	out, err := ts.Call(ctx, ex, "query_pql", map[string]any{"pql": "all posts from me"})
	if err != nil || !strings.Contains(string(out), "Through a tool") || !strings.Contains(string(out), mcpgen.Note) {
		t.Errorf("query_pql = %s, %v", out, err)
	}
	// Outside the token's scope, the site refuses and the tool says so.
	if _, err := ts.Call(ctx, ex, "site_set_name", map[string]any{"author_name": "x"}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("site_set_name with no profile scope: %v, want a 403", err)
	}
}
