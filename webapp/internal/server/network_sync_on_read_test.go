package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
)

// ⭐ THE OWNER READING THEIR NETWORK KEEPS IT CURRENT. The background loop's
// ticker syncs only while a browser tab holds the live connection, and the
// owner's app, an agent over /v1/query and MCP read without one; before this,
// a loaded site never synced again and the owner's network froze at the last
// reload. ⛔ A stranger's read never asks: it would spend this site's DS calls.
func TestOwnersNetworkReadAsksForASyncWhenTheLastIsStale(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = capability.ForHosted()
	s.syncTrigger = make(chan struct{}, 1)

	read := func(sentence string, owner bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/pql/"+sentence, nil)
		req.Header.Set("Accept", "application/json")
		if owner {
			req.Header.Set(OwnerSessionHeader, "1")
		}
		w := httptest.NewRecorder()
		s.handleStreamPQL(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", sentence, w.Code, w.Body.String())
		}
	}
	asked := func() bool {
		select {
		case <-s.syncTrigger:
			return true
		default:
			return false
		}
	}
	const network = "all+posts+from+my+network+by+date"

	read(network, false)
	if asked() {
		t.Error("a stranger's read of my-network asked for a sync")
	}
	read("all+posts+from+all+polis+by+date", true)
	if asked() {
		t.Error("a read of all-polis asked for a sync; only the owner's own network should")
	}

	read(network, true)
	if !asked() {
		t.Fatal("the owner's read of a never-synced network did not ask for a sync")
	}
	read(network, true)
	if asked() {
		t.Error("a second read inside the interval asked again")
	}

	s.lastSyncAsked.Store(time.Now().Add(-networkStaleAfter - time.Second).UnixNano())
	read(network, true)
	if !asked() {
		t.Error("the owner's read did not ask for a sync once the last one was stale")
	}
}
