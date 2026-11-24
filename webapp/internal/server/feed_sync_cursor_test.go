package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ⛔ The feed sync advances its cursor past events whose items the prune
// drops. Holding it (on the theory that the items would fit next time)
// froze the sync for good: an item dropped by AGE is never admitted, so the
// same page came back every cycle and nothing newer ever loaded. Here every
// event the discovery service returns is older than the cache keeps, so
// none survives, and the cursor must still move on.
func TestSyncFeed_CursorAdvancesPastPrunedItems(t *testing.T) {
	old := time.Now().Add(-200 * 24 * time.Hour).UTC().Format(time.RFC3339)
	var events []map[string]interface{}
	for i := 1; i <= 5; i++ {
		events = append(events, map[string]interface{}{
			"id": fmt.Sprint(i), "type": "pub.polis.post.published", "actor": "busy.example",
			"created_at": old, "signature": "sig",
			"payload": map[string]interface{}{
				"url":          fmt.Sprintf("https://busy.example/posts/%d.md", i),
				"version":      "sha256:" + strings.Repeat("a", 64),
				"title":        fmt.Sprintf("Old %d", i),
				"published_at": old,
			},
		})
	}
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/polis":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"public_key": string(pub), "key_id": "test-ds"})
		case "/v1/stream":
			var body map[string]interface{}
			raw, _ := json.Marshal(map[string]interface{}{"events": events, "cursor": "5", "has_more": false})
			_ = json.Unmarshal(raw, &body)
			sig, _ := signing.SignContent([]byte(discovery.BuildCanonicalJSON(body)), priv)
			body["ds_signature"] = sig
			body["ds_key_id"] = "test-ds"
			_ = json.NewEncoder(w).Encode(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ds.Close()

	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL
	f := &following.FollowingFile{}
	f.Add("https://busy.example")
	if err := following.SaveSigned(s.Site(), f, s.PrivateKey); err != nil {
		t.Fatal(err)
	}

	s.syncFeed()

	cm := s.feedCacheForScope("network")
	items, err := cm.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("cache holds %d items; every event was past the age limit, so the prune should drop all", len(items))
	}
	cursor, _ := cm.GetCursor()
	if cursor != "5" {
		t.Errorf("cursor = %q after a sync whose items were all pruned, want %q (advanced past them)", cursor, "5")
	}
}
