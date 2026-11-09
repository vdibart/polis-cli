package following

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

// Re-signing at key rotation. The follow file verifies against the current key
// only, so a rotation that left it alone would turn it invalid; one that
// re-signed blindly would launder a file that was already failing.

func resignSite(t *testing.T) storage.Site {
	t.Helper()
	return storage.At(t.TempDir())
}

func TestResignAfterRotationResignsAValidFile(t *testing.T) {
	site := resignSite(t)
	oldPriv, oldPub := newTestKeys(t)
	newPriv, newPub := newTestKeys(t)
	if err := SaveSigned(site, sampleFile(), oldPriv); err != nil {
		t.Fatal(err)
	}

	out, err := ResignAfterRotation(site, oldPub, newPriv)
	if err != nil || out != signing.ResignDone {
		t.Fatalf("outcome = %q, %v; want resigned", out, err)
	}

	f, err := LoadSite(site)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := Verify(f, newPub); st != StatusValid {
		t.Fatalf("under the new key: %s %v", st, err)
	}
	if st, _ := Verify(f, oldPub); st != StatusInvalid {
		t.Errorf("under the old key: %s, want invalid", st)
	}
	want := sampleFile().Following
	if len(f.Following) != len(want) {
		t.Fatalf("entries = %d, want %d", len(f.Following), len(want))
	}
	for i := range want {
		if f.Following[i] != want[i] {
			t.Errorf("entry %d changed:\n got %+v\nwant %+v", i, f.Following[i], want[i])
		}
	}
}

func TestResignAfterRotationLeavesAnUnsignedFileUnsigned(t *testing.T) {
	site := resignSite(t)
	_, oldPub := newTestKeys(t)
	newPriv, _ := newTestKeys(t)
	if err := Save(site, sampleFile()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(site.Path(DefaultKey))

	out, err := ResignAfterRotation(site, oldPub, newPriv)
	if err != nil || out != signing.ResignUnsigned {
		t.Fatalf("outcome = %q, %v; want unsigned", out, err)
	}
	after, _ := os.ReadFile(site.Path(DefaultKey))
	if string(after) != string(before) {
		t.Error("an unsigned file was rewritten")
	}
}

func TestResignAfterRotationCreatesNothing(t *testing.T) {
	site := resignSite(t)
	_, oldPub := newTestKeys(t)
	newPriv, _ := newTestKeys(t)

	out, err := ResignAfterRotation(site, oldPub, newPriv)
	if err != nil || out != signing.ResignAbsent {
		t.Fatalf("outcome = %q, %v; want absent", out, err)
	}
	if _, err := os.Stat(site.Path(DefaultKey)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a follow file was created: %v", err)
	}
}

// ⛔ A stat that FAILS is not absence. If it were, Load would invent an empty
// file and the rotation would sign it. The existence check now goes through
// the site's store, and this pins that it still tells the two apart.
func TestResignAfterRotationTreatsAFailedStatAsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads through a mode-000 directory")
	}
	site := resignSite(t)
	_, oldPub := newTestKeys(t)
	newPriv, _ := newTestKeys(t)
	dir := filepath.Dir(site.Path(DefaultKey))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	out, err := ResignAfterRotation(site, oldPub, newPriv)
	if err == nil || out == signing.ResignAbsent {
		t.Fatalf("outcome = %q, %v; a failed stat must be an error, never absent", out, err)
	}
}

// ⛔ The gate. A file that does not verify under the old key is evidence, and
// re-signing it would destroy the evidence.
func TestResignAfterRotationNeverLaundersAFailingFile(t *testing.T) {
	site := resignSite(t)
	oldPriv, oldPub := newTestKeys(t)
	newPriv, newPub := newTestKeys(t)
	if err := SaveSigned(site, sampleFile(), oldPriv); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(site.Path(DefaultKey))
	tampered := strings.Replace(string(raw), "https://bob.example", "https://mallory.example", 1)
	if err := os.WriteFile(site.Path(DefaultKey), []byte(tampered), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := ResignAfterRotation(site, oldPub, newPriv)
	if err != nil || out != signing.ResignNotValid {
		t.Fatalf("outcome = %q, %v; want not-valid-under-old-key", out, err)
	}
	after, _ := os.ReadFile(site.Path(DefaultKey))
	if string(after) != tampered {
		t.Error("a failing file was rewritten")
	}
	f, _ := LoadSite(site)
	if st, _ := Verify(f, newPub); st == StatusValid {
		t.Error("a tampered file verifies under the new key")
	}
}

func TestResignAfterRotationRefusesUnrecognisedMembers(t *testing.T) {
	site := resignSite(t)
	oldPriv, oldPub := newTestKeys(t)
	newPriv, _ := newTestKeys(t)
	if err := SaveSigned(site, sampleFile(), oldPriv); err != nil {
		t.Fatal(err)
	}
	// A member a newer build wrote, outside the signed field set: the old
	// signature still verifies, and GuardRewrite must refuse.
	raw, _ := os.ReadFile(site.Path(DefaultKey))
	withExtra := strings.Replace(string(raw), "{", `{"from_the_future": "x",`, 1)
	if err := os.WriteFile(site.Path(DefaultKey), []byte(withExtra), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := ResignAfterRotation(site, oldPub, newPriv)
	var refused *signing.RewriteRefusedError
	if out != signing.ResignRefused || !errors.As(err, &refused) {
		t.Fatalf("outcome = %q, %v; want refused with a RewriteRefusedError", out, err)
	}
	after, _ := os.ReadFile(site.Path(DefaultKey))
	if string(after) != withExtra {
		t.Error("a file carrying unrecognised members was rewritten")
	}
}
