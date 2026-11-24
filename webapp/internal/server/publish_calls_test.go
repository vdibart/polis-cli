package server

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// publishStoreCalls publishes `before` posts, then counts the store calls the
// next publish makes, on a seeded instance on Vercel.
func publishStoreCalls(t *testing.T, before int) int64 {
	t.Helper()
	t.Setenv("POLIS_BASE_URL", "https://poc.example")
	var have []string
	for _, f := range testFunctionManifest().Files {
		have = append(have, f.SHA)
	}
	api, srv := newFakeDeployAPI(t, have...)
	st := seededVercelStore(t, true)
	s, h, err := newNetworkHandler(testWebFS, instanceOptions{DataDir: t.TempDir(), Getenv: vercelEnv(nil), Backend: func() *storage.Backend {
		b := requestStartedBackend(st)
		return &b
	}()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.deployAPIBase = srv.URL
	s.deployLiveBase = newLiveSite(t, api).URL
	netServe(h, "GET", "/.well-known/polis", nil)
	owner := mint(t, s)
	publish := func(title string) {
		body := fmt.Sprintf(`{"markdown":%q}`, "# "+title+"\n\nA post.\n")
		if code, out := apiCall(t, h, "POST", "/v1/content/post", owner, body); code != http.StatusCreated {
			t.Fatalf("publish %q: %d %v", title, code, out)
		}
	}
	for i := 0; i < before; i++ {
		publish(fmt.Sprintf("Earlier post %d", i))
	}
	var n atomic.Int64
	s.deployStore.trace = func(string, string) { n.Add(1) }
	publish("The measured one")
	s.deployStore.trace = nil
	return n.Load()
}

// ⭐ A publish on a remote instance reads each template, snippet and theme
// file once per render, not once per page. Before, one publish on a
// three-post site made 216 store calls (224 live, 8.6 s of a 12.6 s publish);
// each is a network round trip on a remote store.
func TestAPublishMakesFewStoreCalls(t *testing.T) {
	at3 := publishStoreCalls(t, 2)
	at8 := publishStoreCalls(t, 7)
	t.Logf("store calls per publish: %d at 3 posts, %d at 8 posts", at3, at8)
	// Measured through /v1 (the only publish since the old app's /api/publish
	// was deleted, 2026-11-22): 79 at three posts, the act log and the shared
	// after-write step included. The old route's ceiling was 70.
	if at3 > 85 {
		t.Errorf("a publish on a three-post site made %d store calls; want at most 85 (it was 216 before the render read cache)", at3)
	}
	// Each further post costs its page write and its own source read, never
	// the templates again.
	if per := float64(at8-at3) / 5; per > 6 {
		t.Errorf("each extra post adds %.1f store calls to a publish; want at most 6", per)
	}
}
