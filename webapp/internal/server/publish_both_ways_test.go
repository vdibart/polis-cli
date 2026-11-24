package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/draft"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// ⭐⭐ A POST PUBLISHED THROUGH /v1 CAUSES WHAT ONE PUBLISHED THROUGH /api DID.
//
// The browser app's /api/publish rendered the post, ran the owner's
// post-publish hook and removed the draft it came from; /v1 re-rendered the
// whole site and did nothing else. Both now go through the action layer's
// shared after-write module, so the same post leaves the same files, the hook
// runs either way, and the draft goes either way.

// publishSite is a configured site with hooks on, a post-publish hook that
// records its run, and one saved draft.
func publishSite(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	s := newConfiguredServer(t)
	// A real v4 site, so the render is the real one: the v4 shape renders a
	// post incrementally, and the comparison covers the pages it writes.
	if err := bundle.EnsureReferencePayload(s.Site(), "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SetActiveShapeName(s.Site(), "v4"); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SetActiveThemeName(s.Site(), "turbo"); err != nil {
		t.Fatal(err)
	}
	s.EnableHooks = true
	hook := filepath.Join(s.DataDir, ".polis", "webapp", "hooks", "post-publish.sh")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(s.DataDir, "hook-ran")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho \"$1\" > "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := draft.Save(s.Site(), "draft-both-ways", "# Same post\n\nWritten in a draft first.\n")
	if err != nil {
		t.Fatal(err)
	}
	return s, editorRoutes(s, webFS), id
}

// ⛔ An agent's publish (a scoped token, so a marked write) never removes the
// owner's draft, whatever draft_id it names.
func TestAnAgentsPublishThroughTheAPIKeepsTheOwnersDraft(t *testing.T) {
	s, h, id := publishSite(t)
	b, _ := json.Marshal(map[string]string{"markdown": "# By an agent\n\nHello.\n", "draft_id": id})
	if code, made := apiCall(t, h, "POST", "/v1/content/post", mint(t, s, "publish"), string(b)); code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, made)
	}
	if _, err := os.Stat(s.Site().Path(filepath.FromSlash(draft.Key(id)))); err != nil {
		t.Error("an agent's publish removed the owner's draft")
	}
}
