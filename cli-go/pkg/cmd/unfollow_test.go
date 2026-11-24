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
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// `polis unfollow` denied the author's blessings and rewrote following.json
// by hand, so the follow list skipped its signed write point and no stream
// event told the DS or other sites. It now goes through the one unfollow
// (ops.Unfollow, following.UnfollowWithDenial), as the web app and the API do:
// the author's granted blessings are turned away by the site's rules, the
// author leaves the signed follow list, and the output keeps its shape.
func TestUnfollowDeniesTheAuthorsGrantedBlessingsThroughTheSharedUnfollow(t *testing.T) {
	if os.Getenv("POLIS_TEST_UNFOLLOW_HELPER") == "1" {
		dataDir = os.Getenv("POLIS_TEST_DIR")
		jsonOutput = true
		handleUnfollow([]string{"https://bob.example"})
		os.Exit(0)
	}
	bobComment := "https://bob.example/comments/20260101/one.md"

	dsPriv, dsPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var denied []string
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]interface{}{"public_key": string(dsPub), "key_id": "k1"})
		case r.URL.Path == "/v1/relationships" && r.Method == http.MethodGet:
			recs := []interface{}{}
			if r.URL.Query().Get("status") == "granted" {
				recs = append(recs, map[string]interface{}{"id": 1, "type": "pub.polis.comment.blessing", "source_url": bobComment,
					"target_url": "https://alice.polis.pub/posts/20260101/hello.md", "status": "granted"})
			}
			raw, _ := json.Marshal(map[string]interface{}{"count": len(recs), "records": recs})
			var data map[string]interface{}
			json.Unmarshal(raw, &data)
			sig, _ := signing.SignContent([]byte(discovery.BuildCanonicalJSON(data)), dsPriv)
			data["ds_signature"], data["ds_key_id"] = sig, "k1"
			json.NewEncoder(w).Encode(data)
		case r.URL.Path == "/v1/relationships":
			var body struct {
				SourceURL string `json:"source_url"`
				Action    string `json:"action"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Action == "deny" {
				mu.Lock()
				denied = append(denied, body.SourceURL)
				mu.Unlock()
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		default:
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		}
	}))
	defer ds.Close()

	dir := t.TempDir()
	s := storage.At(dir)
	if _, err := site.Init(s, site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := discovery.WriteRegistrationMarker(s, ds.URL, "alice.polis.pub", ""); err != nil {
		t.Fatal(err)
	}
	// Rules that bless only what the site follows: unfollowing bob leaves his
	// blessed comment matched by nothing, so it is turned away. (The default
	// rules also review every comment, which still matches after an unfollow.)
	if err := os.WriteFile(filepath.Join(dir, "policies", "rules.jsonl"), []byte(`{"version":2}`+"\n"+
		`{"active":true,"policy":"bless pub.polis.comment from following"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := following.LoadSite(s)
	f.Add("https://bob.example")
	if err := following.Save(s, f); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestUnfollowDeniesTheAuthorsGrantedBlessingsThroughTheSharedUnfollow$")
	cmd.Env = append(os.Environ(), "POLIS_TEST_UNFOLLOW_HELPER=1", "POLIS_TEST_DIR="+dir,
		"POLIS_BASE_URL=https://alice.polis.pub", "DISCOVERY_SERVICE_URL="+ds.URL)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("polis unfollow: %v\n%s\n%s", err, out, stderr.String())
	}
	var got struct {
		Status string `json:"status"`
		Data   struct {
			AuthorURL      string `json:"author_url"`
			CommentsFound  int    `json:"comments_found"`
			CommentsDenied int    `json:"comments_denied"`
			Removed        bool   `json:"removed_from_following"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got.Status != "success" || got.Data.AuthorURL != "https://bob.example" || !got.Data.Removed || got.Data.CommentsDenied != 1 {
		t.Errorf("output = %+v, want the same shape: success, removed, one denied\n%s", got, out)
	}
	mu.Lock()
	sort.Strings(denied)
	mu.Unlock()
	if strings.Join(denied, ",") != bobComment {
		t.Errorf("the DS was asked to deny %v, want bob's blessed comment", denied)
	}
	after, _ := following.LoadSite(s)
	if after.IsFollowing("https://bob.example") {
		t.Error("bob is still in the follow list")
	}
	if after.Signature == "" {
		t.Error("the follow list was rewritten without its signature (not through its signed write point)")
	}
}
