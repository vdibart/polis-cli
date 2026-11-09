package cmd

import (
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

// A rotation re-signs the follow file and the blessing list, so the verifiers
// Judge runs (checks 9 and 10) pass on the rotated site with no follow or bless
// in between — and never launders a list that was already failing.

func initSiteWithSignedLists(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	priv, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	f := &following.FollowingFile{Following: []following.FollowingEntry{
		{URL: "https://bob.polis.pub", AddedAt: "2026-09-01T00:00:00Z"},
	}}
	if err := following.SaveSigned(storage.At(dir), f, priv); err != nil {
		t.Fatal(err)
	}
	if err := metadata.AddBlessedCommentSigned(storage.At(dir), "posts/20260901/hello.md", metadata.BlessedComment{
		URL: "https://bob.polis.pub/c/1.md", Agent: "rosie", Grant: "https://alice.polis.pub/attestations/grant-1.json",
	}, priv); err != nil {
		t.Fatal(err)
	}
	// Both verify before the rotation, against the published key.
	if st, err := following.VerifySite(storage.At(dir)); st != following.StatusValid {
		t.Fatalf("follow file before rotation: %s %v", st, err)
	}
	if st, err := metadata.VerifyBlessedSite(storage.At(dir)); st != metadata.StatusValid {
		t.Fatalf("blessing list before rotation: %s %v", st, err)
	}
	return dir
}

func unregisteredDS(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestRotateKeyResignsTheFollowFileAndBlessingList(t *testing.T) {
	dir := initSiteWithSignedLists(t)

	data := runRotateKeyJSON(t, dir, unregisteredDS(t))

	if st, err := following.VerifySite(storage.At(dir)); st != following.StatusValid {
		t.Errorf("follow file after rotation: %s %v", st, err)
	}
	if st, err := metadata.VerifyBlessedSite(storage.At(dir)); st != metadata.StatusValid {
		t.Errorf("blessing list after rotation: %s %v", st, err)
	}
	if data["following_resign"] != "resigned" || data["blessed_resign"] != "resigned" {
		t.Errorf("following_resign = %v, blessed_resign = %v; want resigned for both",
			data["following_resign"], data["blessed_resign"])
	}
	bc, err := metadata.LoadBlessedComments(storage.At(dir))
	if err != nil {
		t.Fatal(err)
	}
	if e := bc.Comments[0].Blessed[0]; e.Agent != "rosie" || e.Grant == "" {
		t.Errorf("the agent's marker did not survive the rotation: %+v", e)
	}
}

// ⛔ A list that was already failing stays failing: the rotation is not a way
// to turn a tampered list into a valid one.
func TestRotateKeyLeavesATamperedListAlone(t *testing.T) {
	dir := initSiteWithSignedLists(t)
	path := filepath.Join(dir, metadata.BlessedKey)
	raw, _ := os.ReadFile(path)
	tampered := strings.Replace(string(raw), "c/1.md", "c/666.md", 1)
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatal(err)
	}

	data := runRotateKeyJSON(t, dir, unregisteredDS(t))

	if data["blessed_resign"] != "not-valid-under-old-key" {
		t.Errorf("blessed_resign = %v, want not-valid-under-old-key", data["blessed_resign"])
	}
	if after, _ := os.ReadFile(path); string(after) != tampered {
		t.Error("the tampered blessing list was rewritten")
	}
	if st, _ := metadata.VerifyBlessedSite(storage.At(dir)); st == metadata.StatusValid {
		t.Error("a tampered blessing list verifies after the rotation")
	}
	// The untouched follow file is still re-signed.
	if data["following_resign"] != "resigned" {
		t.Errorf("following_resign = %v, want resigned", data["following_resign"])
	}
}
