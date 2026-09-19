package index

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

// R24-2 / SIGNET epic 14. blessed.json is AUTHORED. `polis rebuild` used to
// regenerate it from DS relationship records, which do not carry a blessed
// comment's version pin — so a rebuild silently erased the canonical
// "edited since blessing" signal for every blessed comment on the site.

func writeBlessed(t *testing.T, dataDir string, bc *metadata.BlessedComments) {
	t.Helper()
	if err := metadata.SaveBlessedComments(dataDir, bc); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}
}

// dsServingGrants stands in for a discovery service that knows about the same
// blessings but — as the real one does — has never stored their version pins.
func dsServingGrants(t *testing.T, targetURL string, sourceURLs ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		records := []map[string]string{}
		for _, u := range sourceURLs {
			records = append(records, map[string]string{
				"source_url": u,
				"target_url": targetURL,
				"status":     "granted",
				"updated_at": "2026-08-30T00:00:00Z",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"records": records})
	}))
}

// unverifiedClient skips DS response-signature verification so the fake DS above
// does not have to sign. Production always constructs a verifying client.
func unverifiedClient(url, key string) *discovery.Client {
	c := discovery.NewClient(url, key)
	c.DSKeyCache = nil
	return c
}

// TestRebuildPreservesVersionPins is R24-2's regression test. Rebuild with a DS
// that knows the same blessings but no pins; the pins must survive.
func TestRebuildPreservesVersionPins(t *testing.T) {
	dataDir := t.TempDir()
	post := "posts/20260101/hello.md"
	commentURL := "https://bob.example/content/pub.polis.core/comment/20260101/c1.md"

	writeBlessed(t, dataDir, &metadata.BlessedComments{
		Version: "polis-cli-go/test",
		Comments: []metadata.PostComments{{
			Post: post,
			Blessed: []metadata.BlessedComment{
				{URL: commentURL, Version: "sha256:4abfd339", BlessedAt: "2026-01-01T00:00:00Z"},
			},
		}},
	})

	srv := dsServingGrants(t, "https://alice.example/"+post, commentURL)
	defer srv.Close()

	if _, err := RebuildIndex(dataDir, RebuildOptions{
		Comments:     true,
		DiscoveryURL: srv.URL,
		DiscoveryKey: "k",
		BaseURL:      "https://alice.example",
		NewDSClient:  unverifiedClient,
	}); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}

	bc, err := metadata.LoadBlessedComments(dataDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if len(bc.Comments) != 1 || len(bc.Comments[0].Blessed) != 1 {
		t.Fatalf("blessed list reshaped by rebuild: %+v", bc.Comments)
	}
	if got := bc.Comments[0].Blessed[0].Version; got != "sha256:4abfd339" {
		t.Errorf("version pin = %q, want %q — rebuild erased the "+
			"\"edited since blessing\" signal", got, "sha256:4abfd339")
	}
	if got := bc.Comments[0].Blessed[0].BlessedAt; got != "2026-01-01T00:00:00Z" {
		t.Errorf("blessed_at = %q, want the authored value, not the DS's", got)
	}
}

// TestRebuildPreservesTheSignature: the file is authored and signed. A rebuild
// that rewrote it would clear the signature even if the content were identical,
// because the unsigned write path clears stale signatures by design. So the
// right number of writes here is zero.
func TestRebuildPreservesTheSignature(t *testing.T) {
	dataDir := t.TempDir()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	wkDir := filepath.Join(dataDir, ".well-known")
	if err := os.MkdirAll(wkDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	wk, _ := json.Marshal(map[string]string{"public_key": string(pub)})
	if err := os.WriteFile(filepath.Join(wkDir, "polis"), wk, 0644); err != nil {
		t.Fatalf("write .well-known/polis: %v", err)
	}

	if err := metadata.AddBlessedCommentSigned(dataDir, "posts/20260101/hello.md",
		metadata.BlessedComment{URL: "https://bob.example/c/1.md", Version: "sha256:aaa"}, priv); err != nil {
		t.Fatalf("AddBlessedCommentSigned: %v", err)
	}
	if status, _ := metadata.VerifyBlessedSite(dataDir); status != metadata.StatusValid {
		t.Fatalf("setup: expected a valid signature, got %q", status)
	}

	srv := dsServingGrants(t, "https://alice.example/posts/20260101/hello.md", "https://bob.example/c/1.md")
	defer srv.Close()

	if _, err := RebuildIndex(dataDir, RebuildOptions{
		Comments:     true,
		DiscoveryURL: srv.URL,
		DiscoveryKey: "k",
		BaseURL:      "https://alice.example",
		NewDSClient:  unverifiedClient,
	}); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}

	if status, err := metadata.VerifyBlessedSite(dataDir); status != metadata.StatusValid {
		t.Errorf("status after rebuild = %q (%v), want %q", status, err, metadata.StatusValid)
	}
}

// TestRebuildDoesNotDropEntriesTheDSNeverSaw: a blessing the DS has no record
// of — an offline grant, a DS reset, a retention gap — must not be deleted by a
// rebuild. The local file is the source; the DS is how the network learns.
func TestRebuildDoesNotDropEntriesTheDSNeverSaw(t *testing.T) {
	dataDir := t.TempDir()
	writeBlessed(t, dataDir, &metadata.BlessedComments{
		Version: "polis-cli-go/test",
		Comments: []metadata.PostComments{{
			Post: "posts/20260101/hello.md",
			Blessed: []metadata.BlessedComment{
				{URL: "https://bob.example/c/1.md", Version: "sha256:aaa"},
				{URL: "https://carol.example/c/2.md", Version: "sha256:bbb"},
			},
		}},
	})

	// The DS only knows about the first one.
	srv := dsServingGrants(t, "https://alice.example/posts/20260101/hello.md", "https://bob.example/c/1.md")
	defer srv.Close()

	count, err := RebuildIndex(dataDir, RebuildOptions{
		Comments:     true,
		DiscoveryURL: srv.URL,
		DiscoveryKey: "k",
		BaseURL:      "https://alice.example",
		NewDSClient:  unverifiedClient,
	})
	if err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	if count.CommentsRebuilt != 2 {
		t.Errorf("CommentsRebuilt = %d, want 2 — the count must describe the "+
			"preserved file, not the DS query", count.CommentsRebuilt)
	}

	bc, _ := metadata.LoadBlessedComments(dataDir)
	if len(bc.Comments[0].Blessed) != 2 {
		t.Errorf("rebuild dropped a blessing the DS did not know about: %+v", bc.Comments[0].Blessed)
	}
}

// TestRebuildRecoversAnAbsentFileFromTheDS: recovery is still available where
// nothing is destroyed by it — but the pin cannot come back, because the DS
// never had it. The empty pin must be left EMPTY rather than invented.
func TestRebuildRecoversAnAbsentFileFromTheDS(t *testing.T) {
	dataDir := t.TempDir()
	srv := dsServingGrants(t, "https://alice.example/posts/20260101/hello.md",
		"https://bob.example/c/1.md", "https://carol.example/c/2.md")
	defer srv.Close()

	if _, err := RebuildIndex(dataDir, RebuildOptions{
		Comments:     true,
		DiscoveryURL: srv.URL,
		DiscoveryKey: "k",
		BaseURL:      "https://alice.example",
		NewDSClient:  unverifiedClient,
	}); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}

	bc, err := metadata.LoadBlessedComments(dataDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if len(bc.Comments) != 1 || len(bc.Comments[0].Blessed) != 2 {
		t.Fatalf("recovery produced %+v", bc.Comments)
	}
	for _, c := range bc.Comments[0].Blessed {
		if c.Version != "" {
			t.Errorf("recovered entry %s has version %q — the DS never had a pin, "+
				"so inventing one claims the author blessed whatever it says today", c.URL, c.Version)
		}
	}
}

// TestRebuildRefusesToOverwriteAnUnparseableFile: a corrupt authored file may
// still hold pins a human can recover by hand. Refusing is honest; overwriting
// destroys evidence with the tool you would reach for to save it.
func TestRebuildRefusesToOverwriteAnUnparseableFile(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	corrupt := []byte(`{"version":"polis-cli-go/x","comments":[{"post":"p.md","bles`)
	if err := os.WriteFile(filepath.Join(dir, metadata.BlessedCommentsFilename), corrupt, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := RebuildIndex(dataDir, RebuildOptions{Comments: true}); err == nil {
		t.Fatal("rebuild silently overwrote an unparseable blessed.json")
	}

	after, err := os.ReadFile(filepath.Join(dir, metadata.BlessedCommentsFilename))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(after) != string(corrupt) {
		t.Error("rebuild modified the file it refused to rebuild")
	}
}

// TestRebuildCreatesAnEmptyFileWhenThereIsNothingToRecover keeps the original
// behaviour for a fresh site: rebuild leaves a well-formed empty list behind.
func TestRebuildCreatesAnEmptyFileWhenThereIsNothingToRecover(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := RebuildIndex(dataDir, RebuildOptions{Comments: true}); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	bc, err := metadata.LoadBlessedComments(dataDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if len(bc.Comments) != 0 {
		t.Errorf("expected an empty list, got %+v", bc.Comments)
	}
}
