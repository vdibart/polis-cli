package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
)

func seedNotifications(t *testing.T, s *Server, lines ...string) {
	t.Helper()
	key := notification.StateFileKey(s.GetDiscoveryDomain())
	if err := s.Site().Store().Put(context.Background(), key, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		t.Fatal(err)
	}
}

// ⭐ "all activity about me" is the owner's own activity, from the site's
// notifications, newest first, under the notify scope; a followed author's
// new post is not about the owner and is left out.
func TestActivityAboutMeComesFromTheNotifications(t *testing.T) {
	s, h := queryRoutes(t)
	seedNotifications(t, s,
		`{"id":"n1","rule_id":"new-follower","actor":"alice.example","message":"alice.example started following you","created_at":"2026-11-10T10:00:00Z","event_ids":[1]}`,
		`{"id":"n2","rule_id":"new-post","actor":"bob.example","message":"bob.example published a new post","created_at":"2026-11-11T10:00:00Z","event_ids":[2]}`,
		`{"id":"n3","rule_id":"blessing-granted","actor":"carol.example","message":"carol.example blessed your comment","created_at":"2026-11-12T10:00:00Z","event_ids":[3]}`,
	)
	notify := mint(t, s, "notify")
	read := mint(t, s, "read")
	both := mint(t, s, "notify", "read")
	const q = "/v1/query?pql=all+activity+about+me"

	w := get(t, h, q+"&counts=activity", notify)
	if w.Code != http.StatusOK {
		t.Fatalf("notify: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Items  []map[string]any `json:"items"`
		Counts map[string]int   `json:"counts"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Items) != 2 || env.Items[0]["event"] != "blessing-granted" || env.Items[1]["event"] != "new-follower" {
		t.Fatalf("items = %v, want blessing-granted then new-follower", env.Items)
	}
	if env.Counts["activity"] != 2 {
		t.Errorf("counts = %v", env.Counts)
	}
	if w := get(t, h, q, read); w.Code != http.StatusForbidden {
		t.Errorf("read scope: %d, want 403 (activity is notify)", w.Code)
	}
	if w := get(t, h, q, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", w.Code)
	}
	// new_for needs read as well as notify: neither alone is enough.
	if w := get(t, h, q+"&new_for=gather", notify); w.Code != http.StatusForbidden {
		t.Errorf("notify alone with new_for: %d, want 403", w.Code)
	}
	if w := get(t, h, q+"&new_for=gather", both); w.Code != http.StatusOK {
		t.Errorf("notify+read with new_for: %d %s", w.Code, w.Body.String())
	}
	// Anyone else's activity is the discovery service's.
	if w := get(t, h, "/v1/query?pql=all+activity+about+alice.example", ""); w.Code != http.StatusBadRequest {
		t.Errorf("about another site: %d, want 400", w.Code)
	}
}

// An entry stored in the JavaScript era carries a Date.toString as its time
// (seen on a live site, on a blessing). Every item's published comes back as
// RFC 3339, and one that cannot be read at all is left out, not passed on.
func TestActivityAboutMeTimesAreRFC3339EvenFromOldEntries(t *testing.T) {
	s, h := queryRoutes(t)
	seedNotifications(t, s,
		`{"id":"n1","rule_id":"blessing-granted","actor":"carol.example","message":"carol.example blessed your comment","created_at":"Wed Mar 04 2026 20:22:19 GMT+0000 (Coordinated Universal Time)","event_ids":[1]}`,
		`{"id":"n2","rule_id":"new-follower","actor":"alice.example","message":"alice.example started following you","created_at":"2026-11-10T10:00:00Z","event_ids":[2]}`,
		`{"id":"n3","rule_id":"new-follower","actor":"dan.example","message":"dan.example started following you","created_at":"sometime","event_ids":[3]}`,
	)
	w := get(t, h, "/v1/query?pql=all+activity+about+me", mint(t, s, "notify"))
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var env struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	got := map[string]any{}
	for _, it := range env.Items {
		got[it["id"].(string)] = it["published"]
	}
	if got["n1"] != "2026-03-04T20:22:19Z" || got["n2"] != "2026-11-10T10:00:00Z" {
		t.Errorf("published = %v, want RFC 3339 for n1 and n2", got)
	}
	if v, ok := got["n3"]; !ok || v != nil {
		t.Errorf("n3 (an unreadable time) published = %v, want it left out", v)
	}
}

// ⭐ Every event links to its place in v5 (spec 3.9), worked out from what it
// is about, never the v4 address an older writer stored with it.
func TestActivityAboutMeEventsLinkToTheirV5Places(t *testing.T) {
	s, h := queryRoutes(t)
	me := strings.TrimRight(s.GetBaseURL(), "/")
	post := me + "/posts/20261110/hello.md"
	mine := me + "/comments/20260304/david-hello-world-20260304.md"
	seedNotifications(t, s,
		`{"id":"f1","rule_id":"new-follower","actor":"ridenhour.polis.pub","link":"/_/social/followers","created_at":"2026-11-01T10:00:00Z","event_ids":[1]}`,
		`{"id":"f2","rule_id":"lost-follower","actor":"gone.example","link":"/_/social/followers","created_at":"2026-11-02T10:00:00Z","event_ids":[2]}`,
		// The form seen on a live site: no payload, the comment only in the id.
		`{"id":"blessing-granted:`+mine+`","rule_id":"blessing-granted","actor":"david.polis.pub","link":"/_/comments?filter=blessed","created_at":"2026-11-03T10:00:00Z","event_ids":[3]}`,
		`{"id":"b2","rule_id":"blessing-denied","actor":"erin.example","link":"/_/comments?filter=denied","payload":{"source_url":"https://`+extractDomainFromURL(me)+`/content/pub.polis.core/comment/20261104/mine.md","target_url":"https://erin.example/posts/20261101/x.md"},"created_at":"2026-11-04T10:00:00Z","event_ids":[4]}`,
		`{"id":"c1","rule_id":"new-comment","actor":"bob.example","link":"/_/blessings","payload":{"source_url":"https://bob.example/comments/20261105/c.md","target_url":"`+post+`"},"created_at":"2026-11-05T10:00:00Z","event_ids":[5]}`,
		`{"id":"r1","rule_id":"blessing-requested","actor":"carol.example","link":"/_/blessings","payload":{"source_url":"https://carol.example/comments/20261106/r.md","target_url":"`+post+`"},"created_at":"2026-11-06T10:00:00Z","event_ids":[6]}`,
		`{"id":"r2","rule_id":"blessing-requested","actor":"dan.example","link":"/_/blessings","created_at":"2026-11-07T10:00:00Z","event_ids":[7]}`,
	)
	w := get(t, h, "/v1/query?pql=all+activity+about+me", mint(t, s, "notify"))
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var env struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	got := map[string]any{}
	for _, it := range env.Items {
		got[fmt.Sprint(it["id"])] = it["link"]
	}
	for id, want := range map[string]string{
		"f1":                       "https://ridenhour.polis.pub",
		"f2":                       "https://gone.example",
		"blessing-granted:" + mine: me + "/comments/20260304/david-hello-world-20260304.html",
		"b2":                       "https://" + extractDomainFromURL(me) + "/comments/20261104/mine.html",
		"c1":                       "/_/write#/posts/20261110/hello.html",
		"r1":                       "/_/write/pql/all+comments+on+" + strings.ReplaceAll(post, "/", "%2F") + "+to+bless",
		"r2":                       "/_/write/pql/all+comments+from+all+polis+to+bless",
	} {
		if got[id] != want {
			t.Errorf("%s links to %v, want %s", id, got[id], want)
		}
	}
	for id, link := range got {
		l := fmt.Sprint(link)
		for _, v4 := range []string{"/_/social/", "/_/comments", "/_/blessings", "/_/#"} {
			if strings.HasPrefix(l, v4) {
				t.Errorf("%s still links to a v4 place: %s", id, l)
			}
		}
	}
}
