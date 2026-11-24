package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The owner's own items carry the agent marker of the version shown, so
// write can say which of them an agent made: agent and grant both, from the
// signed frontmatter, and neither on what the owner made. A body line or a
// nested key named agent is not a marker, and half a marker is none.
func TestOwnItemsCarryTheAgentMarkerOfTheVersionShown(t *testing.T) {
	s := newConfiguredServer(t)
	myDomain := "test-site.polis.pub"
	now := time.Now().UTC()
	grant := "https://test-site.polis.pub/content/pub.polis.core/attestation/20261116T022436Z-e2bacefb87711403.json"
	write := func(rel, content string) {
		full := filepath.Join(s.DataDir, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	posts := []struct{ path, title, content string }{
		{"content/pub.polis.core/post/20261116/by-agent.md", "By agent",
			"---\ntitle: By agent\nagent: Claude Code\ngrant: " + grant + "\n---\nbody"},
		{"content/pub.polis.core/post/20261116/by-owner.md", "By owner",
			"---\ntitle: By owner\n---\nagent: not a marker, a body line\ngrant: nor this"},
		{"content/pub.polis.core/post/20261116/nested.md", "Nested",
			"---\ntitle: Nested\nlicense:\n  agent: Claude Code\n  grant: " + grant + "\n---\nbody"},
		{"content/pub.polis.core/post/20261116/half.md", "Half",
			"---\ntitle: Half\nagent: Claude Code\n---\nbody"},
	}
	for i, p := range posts {
		write(p.path, p.content)
		_ = metadata.AppendPostToIndex(storage.At(s.DataDir), p.path, p.title, now.Add(-time.Duration(i+1)*time.Hour).Format(time.RFC3339), "sha256:"+p.title)
	}
	cpath := "content/pub.polis.core/comment/20261116/agent-comment.md"
	write(cpath, "---\ntitle: Agent comment\nin-reply-to: https://other.polis.pub/posts/foo.md\nagent: Claude Code\ngrant: "+grant+"\n---\nbody")
	_ = metadata.AppendCommentToIndex(storage.At(s.DataDir), cpath, "Agent comment", now.Add(-time.Hour).Format(time.RFC3339), "sha256:c", "https://other.polis.pub/posts/foo.md")

	marks := func(query string) map[string][2]interface{} {
		code, resp := streamRequest(t, s, query)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %v", query, code, resp)
		}
		out := map[string][2]interface{}{}
		items, _ := resp["items"].([]interface{})
		for _, it := range items {
			m := it.(map[string]interface{})
			out[m["title"].(string)] = [2]interface{}{m["agent"], m["grant"]}
		}
		return out
	}

	got := marks("type=posts&scope=@" + myDomain)
	if g := got["By agent"]; g[0] != "Claude Code" || g[1] != grant {
		t.Errorf("agent-made post: agent, grant = %v, want Claude Code and its grant", g)
	}
	for _, title := range []string{"By owner", "Nested", "Half"} {
		if g, ok := got[title]; !ok || g[0] != nil || g[1] != nil {
			t.Errorf("%s: agent, grant = %v (present %v), want neither", title, g, ok)
		}
	}
	got = marks("type=comments&scope=@" + myDomain)
	if g := got["Agent comment"]; g[0] != "Claude Code" || g[1] != grant {
		t.Errorf("agent-made comment: agent, grant = %v, want Claude Code and its grant", g)
	}
}
