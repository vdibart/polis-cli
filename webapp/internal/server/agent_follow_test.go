package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// followFixture is a configured, registered site whose discovery service
// (the signing fake Rosie's tests use) holds one comment from bob waiting for
// a blessing, and a bob.example that is a polis site.
type followFixture struct {
	s  *Server
	h  http.Handler
	ds *fakeDS
}

func newFollowFixture(t *testing.T) *followFixture {
	t.Helper()
	bob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			_ = json.NewEncoder(w).Encode(map[string]any{"public_key": "ssh-ed25519 AAAA", "author": "Bob", "site_title": "Bob's"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(bob.Close)
	t.Setenv("POLIS_ORIGIN_OVERRIDES", "bob.example="+bob.URL)

	f := &followFixture{ds: newFakeDS(t)}
	f.ds.pending = `{"records":[{"id":1,"type":"pub.polis.comment.blessing",` +
		`"source_url":"https://bob.example/content/pub.polis.core/comment/20261123/hi.md",` +
		`"target_url":"https://test-site.polis.pub/content/pub.polis.core/post/20261123/p.md",` +
		`"actor":"bob.example","status":"pending"}]}`
	f.s, f.h = queryRoutes(t)
	f.s.DiscoveryURL = f.ds.srv.URL
	registered(t, f.s)
	pub := filepath.Join(f.s.DataDir, filepath.FromSlash(policy.PublicRulesKey))
	if err := os.MkdirAll(filepath.Dir(pub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pub, []byte(policy.DefaultPublicPolicyContent()), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *followFixture) writes() []discovery.RelationshipUpdateRequest {
	f.ds.mu.Lock()
	defer f.ds.mu.Unlock()
	return append([]discovery.RelationshipUpdateRequest(nil), f.ds.writes...)
}

// ⭐ An agent granted follow may follow a site over /v1: the new entry in the
// signed follow list carries its marker inside the signature, the blessing
// the follow causes is a marked relationship request, and the act is in the
// act log under its grant. The owner's own follow is unmarked.
func TestAnAgentsFollowIsMarkedInTheSignedFollowList(t *testing.T) {
	f := newFollowFixture(t)
	token, key := mintVia(t, f.s, "Claude Desktop", "read", "follow")
	grant := key["grant"].(string)

	if code, out := apiCall(t, f.h, "POST", "/v1/content/follow", token, `{"url":"https://bob.example"}`); code != http.StatusOK {
		t.Fatalf("agent follow = %d %v", code, out)
	}
	list, err := following.LoadSite(f.s.Site())
	if err != nil {
		t.Fatal(err)
	}
	entry := list.Get("https://bob.example")
	if entry == nil || entry.Agent != "Claude Desktop" || entry.Grant != grant {
		t.Fatalf("the agent's follow entry is not marked: %+v", entry)
	}
	if st, err := following.VerifySite(f.s.Site()); st != following.StatusValid {
		t.Errorf("the marked follow list does not verify: %s %v", st, err)
	}
	raw, _ := os.ReadFile(filepath.Join(f.s.DataDir, "content", "pub.polis.core", "follow", "following.json"))
	if !strings.Contains(string(raw), `"agent": "Claude Desktop"`) {
		t.Errorf("following.json lacks the marker:\n%s", raw)
	}

	writes := f.writes()
	if len(writes) == 0 {
		t.Fatal("the follow caused no blessing, so the marker on it is untested")
	}
	for _, wr := range writes {
		if wr.Action != "grant" || wr.Agent != "Claude Desktop" || wr.Grant != grant {
			t.Errorf("the blessing the follow caused is not marked: %+v", wr)
		}
	}

	acts, _ := connection.Acts(f.s.Site(), grant)
	found := false
	for _, a := range acts {
		if a.Method == "POST" && a.Path == "/v1/content/follow" && a.Status == http.StatusOK {
			found = true
		}
	}
	if !found {
		t.Errorf("the act log lacks the agent's follow: %+v", acts)
	}

	// The agent may undo it: the unfollow is recorded in the act log under
	// its grant, and the entry is gone.
	if code, out := apiCall(t, f.h, "POST", "/v1/content/follow/actions/delete", token, `{"url":"https://bob.example"}`); code != http.StatusOK {
		t.Fatalf("agent unfollow = %d %v", code, out)
	}
	if list, _ := following.LoadSite(f.s.Site()); list.IsFollowing("https://bob.example") {
		t.Error("the agent's unfollow left bob followed")
	}
	acts, _ = connection.Acts(f.s.Site(), grant)
	found = false
	for _, a := range acts {
		if a.Path == "/v1/content/follow/actions/delete" && a.Status == http.StatusOK {
			found = true
		}
	}
	if !found {
		t.Errorf("the act log lacks the agent's unfollow: %+v", acts)
	}

	// The owner's own full token: the entry is unmarked.
	if code, out := apiCall(t, f.h, "POST", "/v1/content/follow", mint(t, f.s), `{"url":"https://bob.example"}`); code != http.StatusOK {
		t.Fatalf("owner follow = %d %v", code, out)
	}
	list, _ = following.LoadSite(f.s.Site())
	if e := list.Get("https://bob.example"); e == nil || e.Agent != "" || e.Grant != "" {
		t.Errorf("the owner's follow is marked: %+v", e)
	}
}

// ⛔ Without the follow scope in its grant an agent may neither follow nor
// unfollow, and the list is unchanged; tag is still never granted.
func TestAnAgentWithoutFollowCannotFollowOrUnfollow(t *testing.T) {
	f := newFollowFixture(t)
	token, _ := mintVia(t, f.s, "Claude Desktop", "read", "publish")
	for _, path := range []string{"/v1/content/follow", "/v1/content/follow/actions/delete"} {
		if code, out := apiCall(t, f.h, "POST", path, token, `{"url":"https://bob.example"}`); code != http.StatusForbidden {
			t.Errorf("%s without follow = %d %v, want 403", path, code, out)
		}
	}
	if list, _ := following.LoadSite(f.s.Site()); list.IsFollowing("https://bob.example") {
		t.Error("a refused follow changed the list")
	}
	if _, err := connection.Issue(f.s.connectionConfig(), "x", "", []string{"read", "tag"}); err == nil {
		t.Error("a grant with tag was issued")
	}
}

// ⭐ MCP serves follow and unfollow as tools to a connection granted follow,
// and the tool's follow is the agent's, marked; a connection without it is
// not offered them.
func TestMCPServesFollowAndUnfollowUnderTheFollowScope(t *testing.T) {
	f := newFollowFixture(t)
	token, key := mintVia(t, f.s, "Claude Desktop", "read", "follow")
	cs := mcpSession(t, f.h, token)
	names := strings.Join(toolNames(t, cs), ",")
	for _, want := range []string{"follow_create", "follow_delete", "follow_list"} {
		if !strings.Contains(","+names+",", ","+want+",") {
			t.Errorf("a follow grant is not offered %s: %s", want, names)
		}
	}
	if text, isErr := callText(t, cs, "follow_create", map[string]any{"url": "https://bob.example"}); isErr {
		t.Fatalf("follow_create: %s", text)
	}
	list, _ := following.LoadSite(f.s.Site())
	if e := list.Get("https://bob.example"); e == nil || e.Grant != key["grant"].(string) {
		t.Errorf("the tool's follow is not marked: %+v", e)
	}
	if text, isErr := callText(t, cs, "follow_delete", map[string]any{"url": "https://bob.example"}); isErr {
		t.Fatalf("follow_delete: %s", text)
	}
	if list, _ := following.LoadSite(f.s.Site()); list.IsFollowing("https://bob.example") {
		t.Error("follow_delete left bob followed")
	}

	other, _ := mintVia(t, f.s, "Reader", "read")
	for _, name := range toolNames(t, mcpSession(t, f.h, other)) {
		if name == "follow_create" || name == "follow_delete" {
			t.Errorf("a connection without follow is offered %s", name)
		}
	}
	if api.AgentWrites["follow.create"] != api.MarkedInSignature || api.AgentWrites["follow.delete"] != api.RecordedInActLog {
		t.Errorf("follow's marker paths = %q / %q", api.AgentWrites["follow.create"], api.AgentWrites["follow.delete"])
	}
}
