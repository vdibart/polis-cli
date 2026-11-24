package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// ⭐ "all profiles from my mutuals" is who the owner and they both follow: the
// sites a message can go to.
func TestMyMutualsAreTheSitesThatFollowBack(t *testing.T) {
	s, h := queryRoutes(t)
	f := &following.FollowingFile{}
	f.Add("https://friend.example")
	f.Add("https://quiet.example")
	if err := following.SaveSigned(s.Site(), f, s.PrivateKey); err != nil {
		t.Fatal(err)
	}
	st := stream.NewStore(s.Site(), s.GetDiscoveryDomain(), "pub.polis.core")
	if err := st.SaveState("pub.polis.follow", stream.FollowerState{Followers: []string{"friend.example", "fan.example"}, Count: 2}); err != nil {
		t.Fatal(err)
	}
	w := get(t, h, "/v1/query?pql=all+profiles+from+my+mutuals", mint(t, s, "read"))
	if w.Code != http.StatusOK {
		t.Fatalf("my mutuals = %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Items) != 1 || env.Items[0]["author_domain"] != "friend.example" {
		t.Errorf("my mutuals = %v, want only friend.example", env.Items)
	}
}

// ⛔ The message operations need the owner's own token: a token carrying every
// agent scope is refused on each.
func TestMessageOperationsNeedTheOwnersToken(t *testing.T) {
	s, h := queryRoutes(t)
	everything := mint(t, s, "read", "publish", "comment", "bless", "appearance", "profile", "policy:read", "policy:write", "notify", "status")
	for _, tc := range []struct{ method, path string }{
		{"GET", "/v1/messages/recipients"},
		{"GET", "/v1/messages/recipient-key?recipient_url=https://friend.example"},
		{"GET", "/v1/messages/protection-status?recipient_url=https://friend.example"},
		{"POST", "/v1/messages/send-sealed"},
	} {
		if code, _ := apiCall(t, h, tc.method, tc.path, everything, `{}`); code != http.StatusForbidden {
			t.Errorf("%s %s with every agent scope = %d, want 403", tc.method, tc.path, code)
		}
	}
	if code, out := apiCall(t, h, "GET", "/v1/messages/recipients", mint(t, s), ""); code != http.StatusOK {
		t.Errorf("recipients with the owner's token = %d %v", code, out)
	}
	desc := fmtDescription(t, h)
	for _, id := range []string{"dm.recipients", "dm.recipient_key", "dm.recipient_protection", "dm.send_sealed"} {
		if !strings.Contains(desc, `"`+id+`"`) {
			t.Errorf("description lacks %s", id)
		}
	}
}

// ⛔ No agent scope reaches messages: every message operation in the live
// description, the engine's and the host's, needs the owner's own token.
func TestNoAgentScopeReachesMessages(t *testing.T) {
	_, h := queryRoutes(t)
	var doc map[string]any
	_ = json.Unmarshal(get(t, h, "/v1/openapi.json", "").Body.Bytes(), &doc)
	n := 0
	for path, item := range doc["paths"].(map[string]any) {
		for method, raw := range item.(map[string]any) {
			op := raw.(map[string]any)
			if op["x-polis-type"] != "pub.polis.dm" || op["x-polis-auth"] != "owner" {
				continue
			}
			n++
			if op["x-polis-scope"] != "full" {
				t.Errorf("%s %s (%v) needs %v; messages need the owner's own token", method, path, op["operationId"], op["x-polis-scope"])
			}
		}
	}
	if n < 8 {
		t.Errorf("found %d message operations, expected the engine's and the host's", n)
	}
}
