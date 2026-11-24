package server

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/mcpgen"
)

// ⭐ On a real site, every tool an agent can be offered has a title for a
// person, wherever its operation is declared (the engine, the API, this
// server), and every prompt names operations the site has.
func TestEveryToolOnASiteHasATitleAndEveryPromptIsReal(t *testing.T) {
	s, h := queryRoutes(t)
	w := get(t, h, "/v1/openapi.json", mint(t, s))
	ts, err := mcpgen.FromDescription(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(ts.Tools) < 20 {
		t.Fatalf("only %d tools", len(ts.Tools))
	}
	for _, tool := range ts.Tools {
		if strings.TrimSpace(tool.Title) == "" {
			t.Errorf("%s has no title: declare one with its operation", tool.Name)
		}
	}
	ops := w.Body.String()
	for _, p := range apidesc.Prompts() {
		for _, n := range p.Needs {
			if !strings.Contains(ops, `"operationId":"`+n+`"`) {
				t.Errorf("prompt %s needs %s, which this site does not describe", p.Name, n)
			}
		}
	}
	if len(ts.Prompts) != len(apidesc.Prompts()) {
		t.Errorf("the owner's full token is offered %d of %d prompts", len(ts.Prompts), len(apidesc.Prompts()))
	}
}

// ⭐ A connection is offered only the prompts its grant covers: a read-only
// agent gets what reads, never what publishes or blesses.
func TestAReadOnlyConnectionIsOfferedOnlyPromptsThatRead(t *testing.T) {
	s, h := queryRoutes(t)
	w := get(t, h, "/v1/openapi.json", mint(t, s, "read", "status"))
	ts, err := mcpgen.FromDescription(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range ts.Prompts {
		got[p.Name] = true
	}
	for _, want := range []string{"whats_new", "how_is_my_site"} {
		if !got[want] {
			t.Errorf("a read and status connection is not offered %s (offered %v)", want, got)
		}
	}
	for _, never := range []string{"draft_a_post", "comments_waiting", "reply_to_a_post", "restyle_my_site", "update_my_bio"} {
		if got[never] {
			t.Errorf("a read-only connection is offered %s", never)
		}
	}
}
