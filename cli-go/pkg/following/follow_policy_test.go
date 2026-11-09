package following

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// blessingDS is a discovery service holding waiting blessing requests, which
// signs its query responses as the real one does and records every grant it is
// asked for.
type blessingDS struct {
	*httptest.Server
	mu      sync.Mutex
	granted []string
}

func newBlessingDS(t *testing.T, pending, denied []string) *blessingDS {
	t.Helper()
	dsPriv, dsPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	d := &blessingDS{}
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
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/.well-known/polis":
			json.NewEncoder(w).Encode(map[string]interface{}{"public_key": string(dsPub), "key_id": "k1"})
		case r.URL.Path == "/v1/relationships" && r.Method == http.MethodGet:
			recs := records(pending, "pending")
			if r.URL.Query().Get("status") == "denied" {
				recs = records(denied, "denied")
			}
			// Sign exactly what is sent: round-trip through JSON first.
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
				d.mu.Lock()
				d.granted = append(d.granted, body.SourceURL)
				d.mu.Unlock()
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		default:
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		}
	}))
	t.Cleanup(d.Close)
	return d
}

func (d *blessingDS) grants() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := append([]string(nil), d.granted...)
	sort.Strings(out)
	return out
}

func policiesFrom(t *testing.T, rules ...string) []policy.Policy {
	t.Helper()
	dir := t.TempDir()
	content := `{"version":2}` + "\n"
	for _, r := range rules {
		content += `{"active":true,"policy":"` + r + `"}` + "\n"
	}
	p := filepath.Join(dir, filepath.FromSlash(policy.PublicRulesKey))
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, []byte(content), 0644)
	ps, err := policy.LoadPolicies(storage.At(dir))
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

// A follow re-evaluates waiting comments against the site's rules and grants
// only what a rule blesses. `polis follow` once granted every pending and
// denied comment from the author with no rule consulted; this is the seam it
// now calls, so the guarantee is pinned here.
func TestFollowWithBlessing_GrantsOnlyWhatAPolicyBlesses(t *testing.T) {
	author := mockRemoteSite(t, "bob@example.com")
	defer author.Close()
	authorDomain := discovery.ExtractDomainFromURL(author.URL)
	bobPending := "http://" + authorDomain + "/comments/20260101/one.md"
	bobDenied := "http://" + authorDomain + "/comments/20260101/two.md"
	stranger := "https://carol.example/comments/20260101/three.md"

	cases := []struct {
		name  string
		rules []string
		want  []string
	}{
		{"no bless rule grants nothing", []string{"review pub.polis.comment from all", "deny all from all"}, nil},
		{"no rules at all grants nothing", nil, nil},
		{"bless from following grants the followed author only", []string{"bless pub.polis.comment from following", "review pub.polis.comment from all"}, []string{bobPending, bobDenied}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := newBlessingDS(t, []string{bobPending, stranger}, []string{bobDenied})
			dir := t.TempDir()
			if err := discovery.WriteRegistrationMarker(storage.At(dir), ds.URL, "alice.polis.pub", ""); err != nil {
				t.Fatal(err)
			}
			res, err := FollowWithBlessing(storage.At(dir), author.URL, discovery.NewClient(ds.URL), remote.NewClient(),
				testPrivKey(t), policiesFrom(t, c.rules...), &FollowConfig{DataDir: dir, DiscoveryURL: ds.URL, BaseURL: "https://alice.polis.pub"})
			if err != nil {
				t.Fatalf("FollowWithBlessing: %v", err)
			}
			if res.CommentsFound != 3 {
				t.Fatalf("CommentsFound = %d, want 3: the waiting comments were never read, so the test proves nothing", res.CommentsFound)
			}
			got := ds.grants()
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("granted %v, want %v", got, c.want)
			}
			reported := append([]string(nil), res.BlessedComments...)
			sort.Strings(reported)
			if strings.Join(reported, ",") != strings.Join(c.want, ",") || res.CommentsBlessed != len(c.want) {
				t.Errorf("result reports %d blessed %v, want %v", res.CommentsBlessed, reported, c.want)
			}
			if res.BlessedComments == nil {
				t.Error("BlessedComments is nil; it must marshal as [] so callers can always read it")
			}
		})
	}
}
