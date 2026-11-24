package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ⭐ A search for writing on all of polis asks the discovery service, which
// holds every registered post's title: it finds a post by a word of its
// title, whoever wrote it and however old, beyond the ~100 recent posts the
// site has cached. A DS from before search (which answers unfiltered) adds
// only what matches, and a DS that fails leaves the cached answer.
func TestSearchOfAllPolisFindsAnyRegisteredPostByTitle(t *testing.T) {
	const old = `{"id":7,"type":"pub.polis.post","url":"https://carol.example/posts/20240105/winter.md","actor":"carol.example","version":"sha256:c","created_at":"2024-01-05T10:00:00Z","metadata":{"title":"A Winter Garden","published_at":"2024-01-05T10:00:00Z"}}`
	const other = `{"id":8,"type":"pub.polis.post","url":"https://dan.example/posts/20261101/x.md","actor":"dan.example","version":"sha256:d","created_at":"2026-11-01T10:00:00Z","metadata":{"title":"Nothing to see","published_at":"2026-11-01T10:00:00Z"}}`
	search := func(t *testing.T, records string) []map[string]any {
		t.Helper()
		ds := postSearchDS(t, records)
		s, h := queryRoutes(t)
		s.DiscoveryURL = ds.URL
		w := get(t, h, "/v1/query?pql=all+posts+from+all+polis&search=winter", "")
		if w.Code != http.StatusOK {
			t.Fatalf("search = %d %s", w.Code, w.Body.String())
		}
		var env struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Items
	}

	t.Run("a DS that searches", func(t *testing.T) {
		got := search(t, `{"count":1,"records":[`+old+`]}`)
		if len(got) != 1 || got[0]["url"] != "https://carol.example/posts/20240105/winter.html" || got[0]["author_domain"] != "carol.example" {
			t.Fatalf("items = %v, want carol's 2024 post", got)
		}
	})
	t.Run("a DS from before search answers unfiltered", func(t *testing.T) {
		got := search(t, `{"count":2,"records":[`+old+`,`+other+`]}`)
		if len(got) != 1 || got[0]["url"] != "https://carol.example/posts/20240105/winter.html" {
			t.Fatalf("items = %v, want only the post whose title matches", got)
		}
	})
	t.Run("a DS that fails", func(t *testing.T) {
		got := search(t, "")
		if len(got) != 0 {
			t.Fatalf("items = %v, want the (empty) cached answer", got)
		}
	})
}

// postSearchDS is a discovery service answering GET /v1/content for posts
// with records, signed as a real one signs ("" is a failure).
func postSearchDS(t *testing.T, records string) *httptest.Server {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/.well-known/polis":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"public_key": string(pub), "key_id": "test-ds"})
		case r.URL.Path == "/v1/content" && r.URL.Query().Get("type") == "pub.polis.post":
			if r.URL.Query().Get("search") != "winter" {
				t.Errorf("the DS was asked %q, want search=winter", r.URL.RawQuery)
			}
			if records == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var body map[string]interface{}
			_ = json.Unmarshal([]byte(records), &body)
			sig, _ := signing.SignContent([]byte(discovery.BuildCanonicalJSON(body)), priv)
			body["ds_signature"] = sig
			body["ds_key_id"] = "test-ds"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
