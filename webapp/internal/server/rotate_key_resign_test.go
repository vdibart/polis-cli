package server

import (
	"encoding/json"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// The webapp's rotation re-signs the follow file and the blessing list, so the
// verifiers Judge runs (checks 9 and 10) pass on the rotated tenant with no
// follow or bless in between. It is the same guarantee the CLI's rotation makes,
// asserted through the whole handler rather than a restatement of its steps.
func TestHandleRotateKeyResignsTheFollowFileAndBlessingList(t *testing.T) {
	s := newConfiguredServer(t)
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer ds.Close()
	s.DiscoveryURL = ds.URL

	// A real site has a genesis key history, which a rotation appends to.
	wkPath := filepath.Join(s.DataDir, ".well-known", "polis")
	var wk map[string]interface{}
	raw, _ := os.ReadFile(wkPath)
	if err := json.Unmarshal(raw, &wk); err != nil {
		t.Fatal(err)
	}
	wk["created"] = "2026-01-01T00:00:00Z"
	// newConfiguredServer writes public_key with the key file's trailing
	// newline; a real site publishes it trimmed, and the handover is signed
	// over the trimmed key, so trim it here for the chain to verify.
	wk["public_key"] = strings.TrimSpace(wk["public_key"].(string))
	raw, _ = json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(wkPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := site.WriteGenesisKeyHistory(s.Site()); err != nil {
		t.Fatal(err)
	}

	if err := following.SaveSigned(storage.At(s.DataDir), &following.FollowingFile{
		Following: []following.FollowingEntry{{URL: "https://bob.polis.pub", AddedAt: "2026-09-01T00:00:00Z"}},
	}, s.PrivateKey); err != nil {
		t.Fatal(err)
	}
	if err := metadata.AddBlessedCommentSigned(storage.At(s.DataDir), "posts/20260901/hello.md", metadata.BlessedComment{
		URL: "https://bob.polis.pub/c/1.md", Agent: "rosie", Grant: "https://test-site.polis.pub/attestations/grant-1.json",
	}, s.PrivateKey); err != nil {
		t.Fatal(err)
	}
	if st, err := following.VerifySite(storage.At(s.DataDir)); st != following.StatusValid {
		t.Fatalf("follow file before rotation: %s %v", st, err)
	}
	if st, err := metadata.VerifyBlessedSite(storage.At(s.DataDir)); st != metadata.StatusValid {
		t.Fatalf("blessing list before rotation: %s %v", st, err)
	}
	oldPub := strings.TrimSpace(string(s.PublicKey))

	w := httptest.NewRecorder()
	s.handleRotateKey(w, httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("rotate-key = %d: %s", w.Code, w.Body.String())
	}
	if strings.TrimSpace(string(s.PublicKey)) == oldPub {
		t.Fatal("the key did not change")
	}
	// The new entry names the domain its handover was signed under, so the
	// chain verifies when read at another domain.
	block, err := site.LoadKeyHistory(s.Site())
	if err != nil || block == nil {
		t.Fatalf("load key history: %v", err)
	}
	if block.Current.SigningDomain == "" {
		t.Error("the webapp rotation recorded no signing_domain")
	}
	if err := site.VerifyChain(block, "elsewhere.example"); err != nil {
		t.Errorf("the chain must verify when read at another domain: %v", err)
	}

	if st, err := following.VerifySite(storage.At(s.DataDir)); st != following.StatusValid {
		t.Errorf("follow file after rotation: %s %v", st, err)
	}
	if st, err := metadata.VerifyBlessedSite(storage.At(s.DataDir)); st != metadata.StatusValid {
		t.Errorf("blessing list after rotation: %s %v", st, err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["following_resign"] != "resigned" || resp["blessed_resign"] != "resigned" {
		t.Errorf("following_resign = %v, blessed_resign = %v; want resigned for both",
			resp["following_resign"], resp["blessed_resign"])
	}
	bc, err := metadata.LoadBlessedComments(storage.At(s.DataDir))
	if err != nil {
		t.Fatal(err)
	}
	if e := bc.Comments[0].Blessed[0]; e.Agent != "rosie" || e.Grant == "" {
		t.Errorf("the agent's marker did not survive the rotation: %+v", e)
	}
	// The retired private key was never written anywhere new.
	if raw, _ := os.ReadFile(storage.At(s.DataDir).Path(following.DefaultKey)); strings.Contains(string(raw), "PRIVATE") {
		t.Error("key material leaked into the follow file")
	}
}
