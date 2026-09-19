package metadata

import (
	"os"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Signet epic 11 — the agent marker on a blessing-list entry.

const markerGrantURL = "https://alice.example/content/pub.polis.core/attestation/20260915T120000Z-0123456789abcdef.json"

// goldenBlessedMarked is the canonical form of a list with ONE entry a user
// agent added. It is goldenBlessed with `agent` and `grant` appended to the
// entry, in that order, after `blessed_at`.
const goldenBlessedMarked = `{"version":"polis-cli-go/0.67.0","comments":[{"post":"https://alice.example/content/pub.polis.core/post/20260827/hello.md","blessed":[{"url":"https://bob.example/content/pub.polis.core/comment/20260828/re-hello.md","version":"sha256:0000000000000000000000000000000000000000000000000000000000000000","blessed_at":"2026-08-30T08:00:00Z","agent":"rosie","grant":"` + markerGrantURL + `"}]}]}`

func TestCanonicalBlessedJSON_MarkedVector(t *testing.T) {
	bc := &BlessedComments{
		Version: "polis-cli-go/0.67.0",
		Comments: []PostComments{{
			Post: "https://alice.example/content/pub.polis.core/post/20260827/hello.md",
			Blessed: []BlessedComment{{
				URL:       "https://bob.example/content/pub.polis.core/comment/20260828/re-hello.md",
				Version:   "sha256:0000000000000000000000000000000000000000000000000000000000000000",
				BlessedAt: "2026-08-30T08:00:00Z",
				Agent:     "rosie",
				Grant:     markerGrantURL,
			}},
		}},
	}
	got, err := canonicalBlessedJSON(bc)
	if err != nil {
		t.Fatalf("canonicalBlessedJSON: %v", err)
	}
	if string(got) != goldenBlessedMarked {
		t.Errorf("marked canonical bytes:\n got: %s\nwant: %s", got, goldenBlessedMarked)
	}
	assertSpecPublishes(t, goldenBlessedMarked)
}

// TestMarkedBlessedListRoundTripsThroughTheGoWriter is the landmine test from
// epic 46 R6: every Go writer reloads and rewrites the list, so the marker must
// survive a load → add → sign cycle by the USER'S OWN later act, and the
// signature must still verify.
func TestMarkedBlessedListRoundTripsThroughTheGoWriter(t *testing.T) {
	dir := t.TempDir()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	post := "posts/20260827/hello.md"

	// The agent's act.
	if err := AddBlessedCommentSigned(dir, post, BlessedComment{
		URL: "https://bob.example/c/1.md", Agent: "rosie", Grant: markerGrantURL,
	}, priv); err != nil {
		t.Fatal(err)
	}
	// The user's own act afterwards, through the same writer.
	if err := AddBlessedCommentSigned(dir, post, BlessedComment{URL: "https://carol.example/c/2.md"}, priv); err != nil {
		t.Fatal(err)
	}

	bc, err := LoadBlessedComments(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := bc.Comments[0].Blessed
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Agent != "rosie" || entries[0].Grant != markerGrantURL {
		t.Errorf("the agent's entry lost its marker: %+v", entries[0])
	}
	if entries[1].Agent != "" || entries[1].Grant != "" {
		t.Errorf("the user's own entry gained a marker: %+v", entries[1])
	}
	if st, err := VerifyBlessed(bc, pub); st != StatusValid {
		t.Fatalf("marked list does not verify: %s %v", st, err)
	}

	// And the raw file carries no marker keys on the unmarked entry at all.
	raw, _ := os.ReadFile(BlessedPath(dir))
	if n := countOccurrences(string(raw), `"agent"`); n != 1 {
		t.Errorf("file has %d agent keys, want 1:\n%s", n, raw)
	}
}

// TestUnsignedEchoWriteLeavesAMarkedListSigned pins the echo trap (epic 46 The
// design §7): the sync's caching path writes the DS-stream echo of the agent's
// own grant UNSIGNED. It must find the URL already present and write nothing,
// or it de-signs the list the agent just signed.
func TestUnsignedEchoWriteLeavesAMarkedListSigned(t *testing.T) {
	dir := t.TempDir()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	post := "posts/20260827/hello.md"
	entry := BlessedComment{URL: "https://bob.example/c/1.md", Agent: "rosie", Grant: markerGrantURL}
	if err := AddBlessedCommentSigned(dir, post, entry, priv); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(BlessedPath(dir))

	if err := AddBlessedComment(dir, post, BlessedComment{URL: entry.URL}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(BlessedPath(dir))
	if string(before) != string(after) {
		t.Fatalf("the unsigned echo rewrote the list:\nbefore: %s\nafter:  %s", before, after)
	}
	if st, _ := VerifyBlessedSiteWithKey(dir, pub); st != StatusValid {
		t.Fatalf("list status after echo = %s, want valid", st)
	}
}

func VerifyBlessedSiteWithKey(dir string, pub []byte) (SignatureStatus, error) {
	bc, err := LoadBlessedComments(dir)
	if err != nil {
		return StatusUnknown, err
	}
	return VerifyBlessed(bc, pub)
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
