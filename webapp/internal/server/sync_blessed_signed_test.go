package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// signedSyncSite is an owner instance holding its own key, publishing the
// matching public key, with a SIGNED blessing list listing seed.
func signedSyncSite(t *testing.T, seed ...string) *Server {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)
	wk, _ := json.Marshal(map[string]string{"base_url": "https://owner.polis.pub", "public_key": string(pub)})
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), wk, 0644)
	for _, u := range seed {
		if err := metadata.AddBlessedCommentSigned(dataDir, "posts/20260917/p.md", metadata.BlessedComment{URL: u}, priv); err != nil {
			t.Fatal(err)
		}
	}
	if len(seed) > 0 {
		if st, _ := metadata.VerifyBlessedSite(dataDir); st != metadata.StatusValid {
			t.Fatalf("setup: seeded list is %s, want valid", st)
		}
	}
	return &Server{DataDir: dataDir, BaseURL: "https://owner.polis.pub", DiscoveryURL: "https://ds.polis.pub", PrivateKey: priv}
}

// Epic 21.2: the owner instance's own sync holds the owner's key, so an
// eviction (a signed deny or an unpublish) must leave blessed.json SIGNED.
// The unsigned remove cleared the signature, so a list the author had signed
// silently went unsigned the first time a blessing was withdrawn.
func TestBlessingSyncEvictionKeepsTheListSigned(t *testing.T) {
	for _, evtType := range []string{"pub.polis.comment.blessing.denied", "pub.polis.comment.unpublished"} {
		t.Run(evtType, func(t *testing.T) {
			gone := "https://bob.polis.pub/content/pub.polis.core/comment/20260917/gone.md"
			kept := "https://bob.polis.pub/content/pub.polis.core/comment/20260917/kept.md"
			s := signedSyncSite(t, gone, kept)

			r := (&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{{
				ID:      json.Number("1"),
				Type:    evtType,
				Payload: map[string]interface{}{"comment_url": gone, "target_domain": "owner.polis.pub"},
			}})
			if !r.FilesChanged {
				t.Fatal("expected the eviction to change files")
			}
			if blessedListHas(s.DataDir, gone) {
				t.Fatal("setup: the evicted entry is still listed")
			}
			if st, err := metadata.VerifyBlessedSite(s.DataDir); st != metadata.StatusValid {
				t.Errorf("after eviction blessed.json is %s (%v), want valid", st, err)
			}
		})
	}
}

// Epic 21.2 (widened by oversight): the grant ingest adds to the same list with
// the same key in hand, so it must sign too.
func TestBlessingSyncIngestKeepsTheListSigned(t *testing.T) {
	author := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("---\ntitle: Re: hi\n---\nA reply."))
	}))
	defer author.Close()

	s := signedSyncSite(t, "https://bob.polis.pub/content/pub.polis.core/comment/20260917/old.md")
	commentURL := author.URL + "/comments/20260917/new.md"

	r := (&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{{
		ID:   json.Number("1"),
		Type: "pub.polis.comment.blessing.granted",
		Payload: map[string]interface{}{
			"target_domain": "owner.polis.pub",
			"comment_url":   commentURL,
			"in_reply_to":   "https://owner.polis.pub/posts/20260917/p.md",
		},
	}})
	if !r.FilesChanged {
		t.Fatal("expected the ingest to change files")
	}
	if !blessedListHas(s.DataDir, commentURL) {
		t.Fatal("setup: the ingested entry is not listed")
	}
	if st, err := metadata.VerifyBlessedSite(s.DataDir); st != metadata.StatusValid {
		t.Errorf("after ingest blessed.json is %s (%v), want valid", st, err)
	}
}
