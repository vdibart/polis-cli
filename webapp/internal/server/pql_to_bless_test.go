package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// ⭐ "to bless" lists every waiting request, from the discovery service's
// pending list: a stranger whose comments were never cached, and a request
// older than the stream's time window, both included. It used to keep only
// cached comments that were pending.
func TestToBlessListsEveryWaitingRequest(t *testing.T) {
	s := newConfiguredServer(t)
	ds := newFakeDS(t)
	s.DiscoveryURL = ds.srv.URL
	stranger := "https://stranger.example/content/pub.polis.core/comment/20260915/hi.md"
	ds.pending = `{"records":[` + pendingRecord(1, stranger, "stranger.example", time.Now().Add(-60*24*time.Hour)) + `]}`

	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	h := editorRoutes(s, webFS)
	full := mint(t, s)
	w := get(t, h, "/v1/query?pql=all+comments+from+all+polis+to+bless&counts=comments", full)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var env struct {
		Items  []map[string]any `json:"items"`
		Counts map[string]int   `json:"counts"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Items) != 1 {
		t.Fatalf("items = %d %s, want the stranger's 60-day-old request", len(env.Items), w.Body.String())
	}
	// The stream presents every item at its rendered (.html) address.
	if got := fmt.Sprint(env.Items[0]["url"]); strings.TrimSuffix(got, ".html") != strings.TrimSuffix(stranger, ".md") {
		t.Errorf("item url = %s", got)
	}
	if env.Counts["comments"] != 1 {
		t.Errorf("counts = %v, want 1 waiting", env.Counts)
	}
}

// ⭐ Anyone may ask which comments are waiting: "to bless" is an ordinary
// public sentence. (Asked of the author's own site, the answer is empty for
// anyone but the author: TestToBlessOnTheAuthorsSiteIsEmptyForOthers.) Blessing controls who is notified and what the author's
// page shows, not who may ask.
func TestToBlessIsPublic(t *testing.T) {
	_, h := queryRoutes(t)
	if w := get(t, h, "/v1/query?pql=all+comments+from+all+polis+to+bless", ""); w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
		t.Errorf("anonymous to bless = %d, want it answered", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/pql/all+comments+from+all+polis+to+bless", nil)
	if PQLRequiresOwner(req) {
		t.Error("to bless is guarded as the owner's; it is public")
	}
}

// ⛔ Who you ask decides what you see: the author's own site strips unblessed
// comments for anyone but the author, so a visitor's "to bless" is empty
// there (the discovery service answers it in full).
func TestToBlessOnTheAuthorsSiteIsEmptyForOthers(t *testing.T) {
	s := newConfiguredServer(t)
	ds := newFakeDS(t)
	s.DiscoveryURL = ds.srv.URL
	stranger := "https://stranger.example/content/pub.polis.core/comment/20260915/hi.md"
	ds.pending = `{"records":[` + pendingRecord(1, stranger, "stranger.example", time.Now().Add(-time.Hour)) + `]}`
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	h := editorRoutes(s, webFS)
	items := func(token string) int {
		t.Helper()
		w := get(t, h, "/v1/query?pql=all+comments+from+all+polis+to+bless", token)
		if w.Code != http.StatusOK {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var env struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return len(env.Items)
	}
	if n := items(""); n != 0 {
		t.Errorf("a visitor sees %d waiting comments on the author's site, want none", n)
	}
	if n := items(mint(t, s, "read")); n != 1 {
		t.Errorf("the author sees %d, want the waiting one", n)
	}
}
