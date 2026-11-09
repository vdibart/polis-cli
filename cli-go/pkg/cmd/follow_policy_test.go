package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// `polis follow` used to grant every pending AND denied blessing from the
// author it followed, with no policy consulted, overturning denies the user had
// made. It now goes through following.FollowWithBlessing with the site's own
// policies, as the web app does: the default `bless pub.polis.comment from
// following` blesses the followed author's comments, and a site without such a
// rule blesses nothing.
func TestFollow_BlessesOnlyWhatTheSitesPoliciesBless(t *testing.T) {
	if os.Getenv("POLIS_TEST_FOLLOW_HELPER") == "1" {
		dataDir = os.Getenv("POLIS_TEST_DIR")
		jsonOutput = true
		handleFollow([]string{"https://bob.example"})
		os.Exit(0)
	}

	author := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/polis" {
			json.NewEncoder(w).Encode(map[string]interface{}{"email": "bob@example.com", "public_key": "ssh-ed25519 AAAA", "author": "Bob"})
			return
		}
		http.NotFound(w, r)
	}))
	defer author.Close()

	bobPending := "https://bob.example/comments/20260101/one.md"
	bobDenied := "https://bob.example/comments/20260101/two.md"
	stranger := "https://carol.example/comments/20260101/three.md"

	cases := []struct {
		name   string
		policy string // "" keeps polis init's defaults
		want   []string
	}{
		{"default policies bless the followed author only", "", []string{bobPending, bobDenied}},
		{"no bless rule blesses nothing", `{"version":2}` + "\n" + `{"active":true,"policy":"review pub.polis.comment from all"}` + "\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds, granted := signedBlessingDS(t, []string{bobPending, stranger}, []string{bobDenied})
			dir := t.TempDir()
			if _, err := site.Init(storage.At(dir), site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
				t.Fatal(err)
			}
			if c.policy != "" {
				if err := os.WriteFile(filepath.Join(dir, "policies", "rules.jsonl"), []byte(c.policy), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := discovery.WriteRegistrationMarker(storage.At(dir), ds.URL, "alice.polis.pub", ""); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(os.Args[0], "-test.run=^TestFollow_BlessesOnlyWhatTheSitesPoliciesBless$")
			cmd.Env = append(os.Environ(),
				"POLIS_TEST_FOLLOW_HELPER=1",
				"POLIS_TEST_DIR="+dir,
				"POLIS_BASE_URL=https://alice.polis.pub",
				"DISCOVERY_SERVICE_URL="+ds.URL,
				"POLIS_ORIGIN_OVERRIDES=bob.example="+author.URL,
			)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("polis follow: %v\n%s", err, out)
			}
			var got struct {
				Data struct {
					CommentsFound   int      `json:"comments_found"`
					CommentsBlessed int      `json:"comments_blessed"`
					BlessedComments []string `json:"blessed_comments"`
				} `json:"data"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, out)
			}
			if got.Data.CommentsFound != 3 {
				t.Fatalf("comments_found = %d, want 3: the waiting comments were never read\n%s", got.Data.CommentsFound, out)
			}
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if g := granted(); strings.Join(g, ",") != strings.Join(want, ",") {
				t.Errorf("DS was asked to grant %v, want %v", g, want)
			}
			reported := append([]string(nil), got.Data.BlessedComments...)
			sort.Strings(reported)
			if strings.Join(reported, ",") != strings.Join(want, ",") || got.Data.CommentsBlessed != len(want) {
				t.Errorf("output reports %d blessed %v, want %v", got.Data.CommentsBlessed, reported, want)
			}
		})
	}
}

// signedBlessingDS is a discovery service holding waiting blessing requests. It
// signs its query responses as the real one does, and records each grant.
func signedBlessingDS(t *testing.T, pending, denied []string) (*httptest.Server, func() []string) {
	t.Helper()
	dsPriv, dsPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var granted []string
	records := func(urls []string, status string) []interface{} {
		out := []interface{}{}
		for i, u := range urls {
			out = append(out, map[string]interface{}{
				"id": i + 1, "type": "pub.polis.comment.blessing", "source_url": u,
				"target_url": "https://alice.polis.pub/posts/20260101/hello.md", "status": status,
			})
		}
		return out
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]interface{}{"public_key": string(dsPub), "key_id": "k1"})
		case r.URL.Path == "/v1/relationships" && r.Method == http.MethodGet:
			recs := records(pending, "pending")
			if r.URL.Query().Get("status") == "denied" {
				recs = records(denied, "denied")
			}
			raw, _ := json.Marshal(map[string]interface{}{"count": len(recs), "records": recs})
			var data map[string]interface{}
			json.Unmarshal(raw, &data)
			sig, err := signing.SignContent([]byte(discovery.BuildCanonicalJSON(data)), dsPriv)
			if err != nil {
				t.Errorf("sign DS response: %v", err)
			}
			data["ds_signature"], data["ds_key_id"] = sig, "k1"
			json.NewEncoder(w).Encode(data)
		case r.URL.Path == "/v1/relationships":
			var body struct {
				SourceURL string `json:"source_url"`
				Action    string `json:"action"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Action == "grant" {
				mu.Lock()
				granted = append(granted, body.SourceURL)
				mu.Unlock()
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		default:
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := append([]string(nil), granted...)
		sort.Strings(out)
		return out
	}
}
