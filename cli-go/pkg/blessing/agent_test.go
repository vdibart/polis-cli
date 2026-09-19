package blessing

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Signet epic 11 — a user agent's blessing decisions are signed with the user's
// key and marked, and never made without a live grant.

type recordingDS struct {
	srv   *httptest.Server
	calls atomic.Int32
	last  discovery.RelationshipUpdateRequest
}

func newRecordingDS(t *testing.T) *recordingDS {
	d := &recordingDS{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &d.last)
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func agentSite(t *testing.T) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, key
}

var testRequest = &IncomingRequest{
	CommentURL:     "https://bob.polis.pub/content/pub.polis.core/comment/20260915/re.md",
	CommentVersion: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	InReplyTo:      "https://alice.polis.pub/posts/20260915/hello.md",
}

func TestAgentActsWithoutALiveGrantSignNothing(t *testing.T) {
	dir, key := agentSite(t)
	ds := newRecordingDS(t)
	client := discovery.NewClient(ds.srv.URL, "")
	before, _ := os.ReadFile(metadata.BlessedPath(dir))

	for name, g := range map[string]*attestation.LiveGrant{"nil": nil, "zero": {}} {
		if _, err := GrantAsAgent(dir, testRequest, client, nil, key, g); !errors.Is(err, agent.ErrNoLiveGrant) {
			t.Errorf("GrantAsAgent(%s grant) err = %v, want ErrNoLiveGrant", name, err)
		}
		if _, err := DenyAsAgent(testRequest.CommentURL, testRequest.InReplyTo, client, key, g); !errors.Is(err, agent.ErrNoLiveGrant) {
			t.Errorf("DenyAsAgent(%s grant) err = %v, want ErrNoLiveGrant", name, err)
		}
	}
	if n := ds.calls.Load(); n != 0 {
		t.Fatalf("%d relationship requests were sent without a live grant", n)
	}
	if after, _ := os.ReadFile(metadata.BlessedPath(dir)); string(after) != string(before) {
		t.Fatalf("blessed.json was written without a live grant:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestAgentGrantIsSignedWithTheUsersKeyAndMarkedInBothPlaces(t *testing.T) {
	dir, key := agentSite(t)
	if _, _, err := agent.IssueDefault(agent.Config{SiteDir: dir, BaseURL: "https://alice.polis.pub", PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	live, _ := agent.LiveRosieGrant(dir)
	ds := newRecordingDS(t)

	if _, err := GrantAsAgent(dir, testRequest, discovery.NewClient(ds.srv.URL, ""), nil, key, live); err != nil {
		t.Fatal(err)
	}

	// The relationship request.
	if ds.last.Agent != agent.Rosie || ds.last.Grant != live.URL() {
		t.Fatalf("relationship request marker = %q/%q, want rosie/%s", ds.last.Agent, ds.last.Grant, live.URL())
	}

	// The blessed.json entry — marked, and the list verifies against the USER's
	// published key.
	bc, err := metadata.LoadBlessedComments(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := bc.Comments[0].Blessed[0]
	if e.Agent != agent.Rosie || e.Grant != live.URL() || e.Version != testRequest.CommentVersion {
		t.Fatalf("blessed entry = %+v", e)
	}
	if st, err := metadata.VerifyBlessedSite(dir); st != metadata.StatusValid {
		t.Fatalf("marked list status = %s (%v), want valid", st, err)
	}
}

func TestAgentDenyIsMarked(t *testing.T) {
	dir, key := agentSite(t)
	_, _, _ = agent.IssueDefault(agent.Config{SiteDir: dir, BaseURL: "https://alice.polis.pub", PrivateKey: key})
	live, _ := agent.LiveRosieGrant(dir)
	ds := newRecordingDS(t)
	if _, err := DenyAsAgent(testRequest.CommentURL, testRequest.InReplyTo, discovery.NewClient(ds.srv.URL, ""), key, live); err != nil {
		t.Fatal(err)
	}
	if ds.last.Action != "deny" || ds.last.Agent != agent.Rosie || ds.last.Grant != live.URL() {
		t.Fatalf("deny request = %+v", ds.last)
	}
}

// The user's own act stays unmarked: the invariant's other half.
func TestTheUsersOwnGrantCarriesNoMarker(t *testing.T) {
	dir, key := agentSite(t)
	ds := newRecordingDS(t)
	if _, err := Grant(dir, testRequest, discovery.NewClient(ds.srv.URL, ""), nil, key); err != nil {
		t.Fatal(err)
	}
	if ds.last.Agent != "" || ds.last.Grant != "" {
		t.Fatalf("the user's own grant request is marked: %+v", ds.last)
	}
	bc, _ := metadata.LoadBlessedComments(dir)
	if e := bc.Comments[0].Blessed[0]; e.Agent != "" || e.Grant != "" {
		t.Fatalf("the user's own blessed entry is marked: %+v", e)
	}
}
