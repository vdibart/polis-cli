package server

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// ⭐ Every write a token makes is attributed in the operator's log: the key's
// id (never the token), whether it was scoped, the scope it needed. Reads are
// not logged here (the request log has them).
func TestAPIWritesAreAttributedToTheirKey(t *testing.T) {
	s := newConfiguredServer(t)
	var mu sync.Mutex
	var events []map[string]interface{}
	var names []string
	logEvent := func(event string, fields map[string]interface{}) {
		mu.Lock()
		defer mu.Unlock()
		names = append(names, event)
		events = append(events, fields)
	}
	engine, err := s.newContentEngine()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.SetupRoutesWithHost(mux, engine, s.Site(), s.apiHost(), logEvent)

	token := mint(t, s, "publish")
	if code, _ := apiCall(t, mux, "POST", "/v1/content/post", token, `{"markdown":"# Audited\n\nbody"}`); code != http.StatusCreated {
		t.Fatalf("publish = %d", code)
	}
	_ = get(t, mux, "/v1/content/post", "")

	mu.Lock()
	defer mu.Unlock()
	found := 0
	for i, n := range names {
		if n != "pub.polis.api.write" {
			continue
		}
		found++
		f := events[i]
		if f["scoped"] != true || f["scope"] != "publish" || f["method"] != "POST" || f["key_id"] == "" {
			t.Errorf("write event = %v", f)
		}
		for _, v := range f {
			if s, ok := v.(string); ok && strings.Contains(s, token) {
				t.Errorf("the token itself is in the event: %v", f)
			}
		}
	}
	if found != 1 {
		t.Errorf("found %d write events (%v), want exactly 1 for one write and one read", found, names)
	}
}
