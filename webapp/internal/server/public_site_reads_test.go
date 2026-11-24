package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

func seedFollows(t *testing.T, s *Server) {
	t.Helper()
	f := &following.FollowingFile{}
	f.Add("https://friend.example")
	if err := following.SaveSigned(s.Site(), f, s.PrivateKey); err != nil {
		t.Fatal(err)
	}
	st := stream.NewStore(s.Site(), s.GetDiscoveryDomain(), "pub.polis.core")
	if err := st.SaveState("pub.polis.follow", stream.FollowerState{Followers: []string{"fan.example"}, Count: 1}); err != nil {
		t.Fatal(err)
	}
}

// ⭐ A site's public Following list is the sites it follows; the owner's own
// "my network" keeps followers too, marked.
func TestPublicFollowingListIsOnlyFollows(t *testing.T) {
	s, h := queryRoutes(t)
	seedFollows(t, s)
	tenant := strings.TrimPrefix(strings.TrimPrefix(s.GetBaseURL(), "https://"), "http://")
	domains := func(path, token string) []string {
		t.Helper()
		w := get(t, h, path, token)
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, w.Code, w.Body.String())
		}
		var env struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		var out []string
		for _, it := range env.Items {
			out = append(out, asStr(it["author_domain"]))
		}
		return out
	}
	if got := domains("/v1/query?pql=all+profiles+from+"+tenant+"'s+network", ""); len(got) != 1 || got[0] != "friend.example" {
		t.Errorf("public following list = %v, want only friend.example", got)
	}
	if got := domains("/v1/query?pql=all+profiles+from+my+network", mint(t, s, "read")); len(got) != 2 {
		t.Errorf("the owner's own network = %v, want the follow and the follower", got)
	}
}

// site.get carries when the site began and how many sites follow it.
func TestSiteFactsIncludeSinceAndFollowers(t *testing.T) {
	s, h := queryRoutes(t)
	seedFollows(t, s)
	wk, err := site.LoadWellKnown(s.Site())
	if err != nil {
		t.Fatal(err)
	}
	wk.Created = "2026-01-15T09:00:00Z"
	if err := site.SaveWellKnown(s.Site(), wk); err != nil {
		t.Fatal(err)
	}
	w := get(t, h, "/v1/site", "")
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["followers"] != float64(1) {
		t.Errorf("followers = %v, want 1", out["followers"])
	}
	if out["since"] != "2026-01-15T09:00:00Z" {
		t.Errorf("since = %v", out["since"])
	}
}

// search=<words> finds the site's own posts by their words.
func TestSearchFindsTheSitesOwnPosts(t *testing.T) {
	s, h := queryRoutes(t)
	full := mint(t, s)
	for _, md := range []string{`{"markdown":"# Gardens\n\nabout tomatoes"}`, `{"markdown":"# Bicycles\n\nabout gears"}`} {
		if code, out := apiCall(t, h, "POST", "/v1/content/post", full, md); code != http.StatusCreated {
			t.Fatalf("publish = %d %v", code, out)
		}
	}
	w := get(t, h, "/v1/query?pql=all+posts+by+date&search=garden", "")
	var env struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Items) != 1 || !strings.Contains(asStr(env.Items[0]["title"]), "Gardens") {
		t.Errorf("search garden = %v, want only Gardens", env.Items)
	}
}

// A search word matches at the start of a word, never inside one: "go" finds
// a post about Go, not one about Hugo.
func TestSearchMatchesWordStartsNotInsideWords(t *testing.T) {
	s, h := queryRoutes(t)
	full := mint(t, s)
	for _, md := range []string{`{"markdown":"# Go was designed at Google\n\nabout types"}`, `{"markdown":"# Hugo\n\na static site generator, years ago"}`} {
		if code, out := apiCall(t, h, "POST", "/v1/content/post", full, md); code != http.StatusCreated {
			t.Fatalf("publish = %d %v", code, out)
		}
	}
	w := get(t, h, "/v1/query?pql=all+posts+by+date&search=go", "")
	var env struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Items) != 1 || !strings.Contains(asStr(env.Items[0]["title"]), "Go was designed") {
		t.Errorf("search go = %v, want only the post about Go", env.Items)
	}
}
