package tailor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// A follow file is signed over {version, following}, so the move from the
// legacy metadata/ path must carry its bytes unchanged. Rewriting the version
// on the way (which Tailor once did) makes a signed file stop verifying.
func TestLayoutFollowingMovesSignedFileByteForByte(t *testing.T) {
	dir := createOldSite(t)
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	f := &following.FollowingFile{Version: "polis-cli-go/0.57.0", Following: []following.FollowingEntry{}}
	if err := following.Sign(f, priv); err != nil {
		t.Fatal(err)
	}
	signed, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "metadata", "following.json")
	if err := os.WriteFile(legacy, signed, 0644); err != nil {
		t.Fatal(err)
	}
	blessedBefore, err := os.ReadFile(filepath.Join(dir, "metadata", "blessed-comments.json"))
	if err != nil {
		t.Fatal(err)
	}

	Apply(dir)

	moved, err := os.ReadFile(filepath.Join(dir, "content", "pub.polis.core", "follow", "following.json"))
	if err != nil {
		t.Fatalf("following.json not moved: %v", err)
	}
	if !bytes.Equal(moved, signed) {
		t.Errorf("moved following.json changed:\nbefore %s\nafter  %s", signed, moved)
	}
	var got following.FollowingFile
	if err := json.Unmarshal(moved, &got); err != nil {
		t.Fatal(err)
	}
	if st, err := following.Verify(&got, pub); st != following.StatusValid {
		t.Errorf("moved following.json status = %v (%v), want valid", st, err)
	}

	blessedAfter, err := os.ReadFile(filepath.Join(dir, "content", "pub.polis.core", "comment", "blessed.json"))
	if err != nil {
		t.Fatalf("blessed.json not moved: %v", err)
	}
	if !bytes.Equal(blessedAfter, blessedBefore) {
		t.Errorf("moved blessed.json changed:\nbefore %s\nafter  %s", blessedBefore, blessedAfter)
	}
}

// path-separators fixes only "path" values, and only in the unsigned index:
// a backslash in a title is left alone, and a signed follow file is reported
// and never rewritten.
func TestPathSeparatorsTouchesOnlyPathsAndNeverASignedFile(t *testing.T) {
	dir := createCurrentSite(t)
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	f := &following.FollowingFile{Version: "polis-cli-go/0.59.0", Following: []following.FollowingEntry{{URL: `https://a.example\b`}}}
	if err := following.Sign(f, priv); err != nil {
		t.Fatal(err)
	}
	signed, _ := json.MarshalIndent(f, "", "  ")
	followPath := filepath.Join(dir, "content", "pub.polis.core", "follow", "following.json")
	os.WriteFile(followPath, signed, 0o644)
	index := `{"type":"post","path":"content\\pub.polis.core\\post\\20260227\\t.md","title":"C:\\temp is a path"}` + "\n"
	indexPath := filepath.Join(dir, "content", "pub.polis.core", "index.jsonl")
	os.WriteFile(indexPath, []byte(index), 0o644)

	cr := checkPathSeparators(&runContext{siteDir: dir, dryRun: false})

	got, _ := os.ReadFile(indexPath)
	want := `{"type":"post","path":"content/pub.polis.core/post/20260227/t.md","title":"C:\\temp is a path"}` + "\n"
	if string(got) != want {
		t.Errorf("index.jsonl:\n got %s\nwant %s", got, want)
	}
	after, _ := os.ReadFile(followPath)
	if !bytes.Equal(after, signed) {
		t.Error("signed following.json was rewritten")
	}
	var g following.FollowingFile
	json.Unmarshal(after, &g)
	if st, _ := following.Verify(&g, pub); st != following.StatusValid {
		t.Errorf("following.json status %v", st)
	}
	if cr.Status != StatusFail {
		t.Errorf("got %s %q", cr.Status, cr.Message)
	}
}
