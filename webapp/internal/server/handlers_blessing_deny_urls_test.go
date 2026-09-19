package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The v4 stream hands the SPA rendered (.html) URLs, and the DS matches a
// blessing relationship on its exact source_url/target_url, which are the .md
// forms. handleBlessingGrant normalises both; deny passed them through, so a
// Deny clicked in the stream named a relationship that does not exist (found in
// the close-out browser walk of C12).
func TestHandleBlessingDeny_SendsMarkdownURLsToTheDS(t *testing.T) {
	var mu sync.Mutex
	var got map[string]interface{}
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/relationships" {
			mu.Lock()
			json.NewDecoder(r.Body).Decode(&got)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	}))
	defer ds.Close()

	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL
	markAsRegistered(t, s)

	body := jsonBody(t, map[string]string{
		"comment_url": "https://bob.example/comments/20260917/reply.html",
		"in_reply_to": "https://alice.example/posts/20260917/hello.html",
	})
	rr := httptest.NewRecorder()
	s.handleBlessingDeny(rr, httptest.NewRequest(http.MethodPost, "/api/blessing/deny", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("deny = %d: %s", rr.Code, rr.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if got["source_url"] != "https://bob.example/comments/20260917/reply.md" ||
		got["target_url"] != "https://alice.example/posts/20260917/hello.md" {
		t.Errorf("DS got source_url=%v target_url=%v, want the .md forms", got["source_url"], got["target_url"])
	}
}
