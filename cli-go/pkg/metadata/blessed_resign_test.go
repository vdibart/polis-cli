package metadata

import (
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Re-signing at key rotation. The list verifies against the current key only,
// so a rotation that left it alone would turn it invalid; one that re-signed
// blindly would launder a list that was already failing. These pin both sides.

func TestResignBlessedAfterRotationResignsAValidList(t *testing.T) {
	dir := t.TempDir()
	oldPriv, oldPub := newTestKeys(t)
	newPriv, newPub := newTestKeys(t)
	post := "posts/20260827/hello.md"
	if err := AddBlessedCommentSigned(storage.At(dir), post, BlessedComment{
		URL: "https://bob.example/c/1.md", BlessedAt: "2026-08-30T00:00:00Z", Agent: "rosie", Grant: markerGrantURL,
	}, oldPriv); err != nil {
		t.Fatal(err)
	}
	if err := AddBlessedCommentSigned(storage.At(dir), post, BlessedComment{
		URL: "https://carol.example/c/2.md", BlessedAt: "2026-08-30T00:01:00Z",
	}, oldPriv); err != nil {
		t.Fatal(err)
	}
	before, _ := LoadBlessedComments(storage.At(dir))

	out, err := ResignBlessedAfterRotation(storage.At(dir), oldPub, newPriv)
	if err != nil || out != signing.ResignDone {
		t.Fatalf("outcome = %q, %v; want resigned", out, err)
	}

	after, err := LoadBlessedComments(storage.At(dir))
	if err != nil {
		t.Fatal(err)
	}
	if st, err := VerifyBlessed(after, newPub); st != StatusValid {
		t.Fatalf("under the new key: %s %v", st, err)
	}
	if st, _ := VerifyBlessed(after, oldPub); st != StatusInvalid {
		t.Errorf("under the old key: %s, want invalid", st)
	}
	got, want := after.Comments[0].Blessed, before.Comments[0].Blessed
	if len(got) != len(want) {
		t.Fatalf("entries = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d changed:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	if got[0].Agent != "rosie" || got[0].Grant != markerGrantURL {
		t.Errorf("the agent's marker was lost: %+v", got[0])
	}
}

func TestResignBlessedAfterRotationLeavesAnUnsignedListUnsigned(t *testing.T) {
	dir := t.TempDir()
	_, oldPub := newTestKeys(t)
	newPriv, _ := newTestKeys(t)
	if err := SaveBlessedComments(storage.At(dir), sampleBlessed()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, BlessedKey))

	out, err := ResignBlessedAfterRotation(storage.At(dir), oldPub, newPriv)
	if err != nil || out != signing.ResignUnsigned {
		t.Fatalf("outcome = %q, %v; want unsigned", out, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, BlessedKey))
	if string(after) != string(before) {
		t.Error("an unsigned list was rewritten")
	}
}

func TestResignBlessedAfterRotationCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	_, oldPub := newTestKeys(t)
	newPriv, _ := newTestKeys(t)

	out, err := ResignBlessedAfterRotation(storage.At(dir), oldPub, newPriv)
	if err != nil || out != signing.ResignAbsent {
		t.Fatalf("outcome = %q, %v; want absent", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, BlessedKey)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a blessing list was created: %v", err)
	}
}

// ⛔ The gate. A list that does not verify under the old key is evidence, and
// re-signing it would destroy the evidence.
func TestResignBlessedAfterRotationNeverLaundersAFailingList(t *testing.T) {
	dir := t.TempDir()
	oldPriv, oldPub := newTestKeys(t)
	newPriv, newPub := newTestKeys(t)
	if err := SaveBlessedCommentsSigned(storage.At(dir), sampleBlessed(), oldPriv); err != nil {
		t.Fatal(err)
	}
	// Tamper on disk after signing.
	raw, _ := os.ReadFile(filepath.Join(dir, BlessedKey))
	tampered := strings.Replace(string(raw), "sha256:aaa", "sha256:zzz", 1)
	if err := os.WriteFile(filepath.Join(dir, BlessedKey), []byte(tampered), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := ResignBlessedAfterRotation(storage.At(dir), oldPub, newPriv)
	if err != nil || out != signing.ResignNotValid {
		t.Fatalf("outcome = %q, %v; want not-valid-under-old-key", out, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, BlessedKey))
	if string(after) != tampered {
		t.Error("a failing list was rewritten")
	}
	bc, _ := LoadBlessedComments(storage.At(dir))
	if st, _ := VerifyBlessed(bc, newPub); st == StatusValid {
		t.Error("a tampered list verifies under the new key")
	}
}

func TestResignBlessedAfterRotationRefusesUnrecognisedMembers(t *testing.T) {
	dir := t.TempDir()
	oldPriv, oldPub := newTestKeys(t)
	newPriv, _ := newTestKeys(t)
	if err := SaveBlessedCommentsSigned(storage.At(dir), sampleBlessed(), oldPriv); err != nil {
		t.Fatal(err)
	}
	// A member a newer build wrote. It sits outside the signing base's field
	// set, so the old signature still verifies — and GuardRewrite must refuse.
	raw, _ := os.ReadFile(filepath.Join(dir, BlessedKey))
	withExtra := strings.Replace(string(raw), "{", `{"from_the_future": "x",`, 1)
	if err := os.WriteFile(filepath.Join(dir, BlessedKey), []byte(withExtra), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := ResignBlessedAfterRotation(storage.At(dir), oldPub, newPriv)
	var refused *signing.RewriteRefusedError
	if out != signing.ResignRefused || !errors.As(err, &refused) {
		t.Fatalf("outcome = %q, %v; want refused with a RewriteRefusedError", out, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, BlessedKey))
	if string(after) != withExtra {
		t.Error("a list carrying unrecognised members was rewritten")
	}
}
