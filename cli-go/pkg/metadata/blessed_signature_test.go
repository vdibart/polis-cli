package metadata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Modeled on pkg/following/signature_test.go (SIGNET epic 02). The follow file
// is the proven shape; these are the same questions asked of the blessing list.

func newTestKeys(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return priv, pub
}

func sampleBlessed() *BlessedComments {
	return &BlessedComments{
		Version: "polis-cli-go/test",
		Comments: []PostComments{
			{
				Post: "posts/20260101/hello.md",
				Blessed: []BlessedComment{
					{URL: "https://bob.example/c/1.md", Version: "sha256:aaa", BlessedAt: "2026-08-30T00:00:00Z"},
					{URL: "https://carol.example/c/2.md", Version: "sha256:bbb", BlessedAt: "2026-08-30T00:01:00Z"},
				},
			},
			{
				Post: "posts/20260102/second.md",
				Blessed: []BlessedComment{
					{URL: "https://dave.example/c/3.md", Version: "sha256:ccc", BlessedAt: "2026-08-30T00:02:00Z"},
				},
			},
		},
	}
}

// writeWellKnown drops a minimal .well-known/polis carrying pub, so
// VerifyBlessedSite has an identity key to check against.
func writeWellKnown(t *testing.T, siteDir string, pub []byte) {
	t.Helper()
	dir := filepath.Join(siteDir, ".well-known")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir .well-known: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"public_key": string(pub)})
	if err := os.WriteFile(filepath.Join(dir, "polis"), body, 0644); err != nil {
		t.Fatalf("write .well-known/polis: %v", err)
	}
}

// ---------- the signable set ----------

// TestCanonicalBlessedJSONIsTheSpec pins the exact bytes a second
// implementation must reproduce. If this test changes, every signature ever
// written stops verifying — so a diff here is a protocol break, not a refactor.
func TestCanonicalBlessedJSONIsTheSpec(t *testing.T) {
	got, err := canonicalBlessedJSON(sampleBlessed())
	if err != nil {
		t.Fatalf("canonicalBlessedJSON: %v", err)
	}
	want := `{"version":"polis-cli-go/test","comments":[` +
		`{"post":"posts/20260101/hello.md","blessed":[` +
		`{"url":"https://bob.example/c/1.md","version":"sha256:aaa","blessed_at":"2026-08-30T00:00:00Z"},` +
		`{"url":"https://carol.example/c/2.md","version":"sha256:bbb","blessed_at":"2026-08-30T00:01:00Z"}]},` +
		`{"post":"posts/20260102/second.md","blessed":[` +
		`{"url":"https://dave.example/c/3.md","version":"sha256:ccc","blessed_at":"2026-08-30T00:02:00Z"}]}]}`
	if string(got) != want {
		t.Errorf("canonical JSON drifted.\n got: %s\nwant: %s", got, want)
	}
}

// TestCanonicalBlessedJSONExcludesSignature: the signature cannot cover itself.
func TestCanonicalBlessedJSONExcludesSignature(t *testing.T) {
	bc := sampleBlessed()
	before, _ := canonicalBlessedJSON(bc)
	bc.Signature = "some-signature"
	after, _ := canonicalBlessedJSON(bc)
	if string(before) != string(after) {
		t.Errorf("setting Signature changed the canonical form:\n%s\n%s", before, after)
	}
}

// TestCanonicalBlessedJSONEmptyListsAreStable: "I have blessed nothing" and "no
// blessings on this post" must each have ONE byte sequence — a nil slice
// marshals as null and an empty one as [].
func TestCanonicalBlessedJSONEmptyListsAreStable(t *testing.T) {
	nilTop, _ := canonicalBlessedJSON(&BlessedComments{Version: "v"})
	emptyTop, _ := canonicalBlessedJSON(&BlessedComments{Version: "v", Comments: []PostComments{}})
	if string(nilTop) != string(emptyTop) {
		t.Errorf("nil and empty comment lists canonicalize differently:\n%s\n%s", nilTop, emptyTop)
	}
	if strings.Contains(string(nilTop), "null") {
		t.Errorf("empty comment list canonicalized with null: %s", nilTop)
	}

	nilInner, _ := canonicalBlessedJSON(&BlessedComments{Version: "v",
		Comments: []PostComments{{Post: "p.md"}}})
	emptyInner, _ := canonicalBlessedJSON(&BlessedComments{Version: "v",
		Comments: []PostComments{{Post: "p.md", Blessed: []BlessedComment{}}}})
	if string(nilInner) != string(emptyInner) {
		t.Errorf("nil and empty blessed lists canonicalize differently:\n%s\n%s", nilInner, emptyInner)
	}
	if strings.Contains(string(nilInner), "null") {
		t.Errorf("empty blessed list canonicalized with null: %s", nilInner)
	}
}

// TestCanonicalBlessedJSONDoesNotMutate: Verify runs on a caller's struct and
// must leave it exactly as it found it, or a verify-then-write path would
// silently rewrite the file it just checked.
func TestCanonicalBlessedJSONDoesNotMutate(t *testing.T) {
	bc := &BlessedComments{Version: "v", Comments: []PostComments{{Post: "p.md"}}}
	if _, err := canonicalBlessedJSON(bc); err != nil {
		t.Fatalf("canonicalBlessedJSON: %v", err)
	}
	if bc.Comments[0].Blessed != nil {
		t.Error("canonicalBlessedJSON pinned the caller's nil slice in place")
	}
}

// ---------- sign / verify ----------

func TestSignThenVerifyRoundTrips(t *testing.T) {
	priv, pub := newTestKeys(t)
	bc := sampleBlessed()

	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}
	if bc.Signature == "" {
		t.Fatal("SignBlessed left Signature empty")
	}

	status, err := VerifyBlessed(bc, pub)
	if err != nil {
		t.Fatalf("VerifyBlessed: %v", err)
	}
	if status != StatusValid {
		t.Errorf("status = %q, want %q", status, StatusValid)
	}
}

// TestTamperedContentDoesNotVerify: the point of the whole exercise. Editing a
// blessed comment's version pin — the "edited since blessing" signal — must
// break the signature.
func TestTamperedContentDoesNotVerify(t *testing.T) {
	priv, pub := newTestKeys(t)
	bc := sampleBlessed()
	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}

	bc.Comments[0].Blessed[0].Version = "sha256:tampered"

	status, _ := VerifyBlessed(bc, pub)
	if status != StatusInvalid {
		t.Errorf("tampered version pin verified as %q, want %q", status, StatusInvalid)
	}
}

// TestAddedEntryDoesNotVerify: someone appending a blessing the author never
// granted is the attack this signature exists to make visible.
func TestAddedEntryDoesNotVerify(t *testing.T) {
	priv, pub := newTestKeys(t)
	bc := sampleBlessed()
	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}

	bc.Comments[0].Blessed = append(bc.Comments[0].Blessed,
		BlessedComment{URL: "https://mallory.example/c/9.md", Version: "sha256:zzz", BlessedAt: "2026-08-30T09:00:00Z"})

	status, _ := VerifyBlessed(bc, pub)
	if status != StatusInvalid {
		t.Errorf("smuggled blessing verified as %q, want %q", status, StatusInvalid)
	}
}

// TestVerifyWithWrongKeyIsInvalid: a signature by a key that is not the site's
// is INVALID, not merely unknown.
func TestVerifyWithWrongKeyIsInvalid(t *testing.T) {
	priv, _ := newTestKeys(t)
	_, otherPub := newTestKeys(t)
	bc := sampleBlessed()
	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}

	status, _ := VerifyBlessed(bc, otherPub)
	if status != StatusInvalid {
		t.Errorf("status = %q, want %q", status, StatusInvalid)
	}
}

// ---------- the four states ----------

// TestUnsignedIsUnsignedNotInvalid is the single most important test here. Most
// blessers will be unsigned for a long time; a check that reads absent-as-failure
// turns the fleet red on its first sweep.
func TestUnsignedIsUnsignedNotInvalid(t *testing.T) {
	_, pub := newTestKeys(t)
	bc := sampleBlessed() // never signed

	status, err := VerifyBlessed(bc, pub)
	if err != nil {
		t.Fatalf("unexpected error for an unsigned file: %v", err)
	}
	if status != StatusUnsigned {
		t.Errorf("status = %q, want %q", status, StatusUnsigned)
	}
}

func TestVerifyWithNoKeyIsUnknown(t *testing.T) {
	priv, _ := newTestKeys(t)
	bc := sampleBlessed()
	if err := SignBlessed(bc, priv); err != nil {
		t.Fatalf("SignBlessed: %v", err)
	}

	status, _ := VerifyBlessed(bc, nil)
	if status != StatusUnknown {
		t.Errorf("status = %q, want %q", status, StatusUnknown)
	}
}

func TestVerifyNilIsUnsigned(t *testing.T) {
	status, err := VerifyBlessed(nil, nil)
	if err != nil || status != StatusUnsigned {
		t.Errorf("VerifyBlessed(nil) = %q, %v; want %q, nil", status, err, StatusUnsigned)
	}
}

// ---------- VerifySite ----------

func TestVerifyBlessedSiteValid(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, pub)

	if err := AddBlessedCommentSigned(siteDir, "posts/20260101/hello.md",
		BlessedComment{URL: "https://bob.example/c/1.md", Version: "sha256:aaa"}, priv); err != nil {
		t.Fatalf("AddBlessedCommentSigned: %v", err)
	}

	status, err := VerifyBlessedSite(siteDir)
	if err != nil {
		t.Fatalf("VerifyBlessedSite: %v", err)
	}
	if status != StatusValid {
		t.Errorf("status = %q, want %q", status, StatusValid)
	}
}

// TestVerifyBlessedSiteNoFileIsUnsigned: a site that has blessed nothing is
// unsigned, not unknown and certainly not a finding.
func TestVerifyBlessedSiteNoFileIsUnsigned(t *testing.T) {
	siteDir := t.TempDir()
	status, err := VerifyBlessedSite(siteDir)
	if err != nil {
		t.Fatalf("unexpected error for a site with no blessing list: %v", err)
	}
	if status != StatusUnsigned {
		t.Errorf("status = %q, want %q", status, StatusUnsigned)
	}
}

// TestVerifyBlessedSiteNoWellKnownIsUnknown: having failed to look is not the
// same as having found a problem.
func TestVerifyBlessedSiteNoWellKnownIsUnknown(t *testing.T) {
	priv, _ := newTestKeys(t)
	siteDir := t.TempDir()
	if err := AddBlessedCommentSigned(siteDir, "posts/p.md",
		BlessedComment{URL: "https://bob.example/c/1.md"}, priv); err != nil {
		t.Fatalf("AddBlessedCommentSigned: %v", err)
	}

	status, _ := VerifyBlessedSite(siteDir)
	if status != StatusUnknown {
		t.Errorf("status = %q, want %q", status, StatusUnknown)
	}
}

// TestVerifyBlessedSiteDetectsOnDiskTampering is the end-to-end version: edit
// the served file by hand and the site reports invalid.
func TestVerifyBlessedSiteDetectsOnDiskTampering(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, pub)

	if err := AddBlessedCommentSigned(siteDir, "posts/p.md",
		BlessedComment{URL: "https://bob.example/c/1.md"}, priv); err != nil {
		t.Fatalf("AddBlessedCommentSigned: %v", err)
	}

	path := BlessedPath(siteDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	edited := strings.Replace(string(raw), "https://bob.example/c/1.md", "https://mallory.example/c/1.md", 1)
	if edited == string(raw) {
		t.Fatal("test setup: nothing was edited")
	}
	if err := os.WriteFile(path, []byte(edited), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	status, _ := VerifyBlessedSite(siteDir)
	if status != StatusInvalid {
		t.Errorf("hand-edited blessing list verified as %q, want %q", status, StatusInvalid)
	}
}

// ---------- every write path ----------

// TestAddSignedProducesAVerifyingFile: the write path IS the migration.
func TestAddSignedProducesAVerifyingFile(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, pub)

	for _, u := range []string{"https://a.example/c/1.md", "https://b.example/c/2.md"} {
		if err := AddBlessedCommentSigned(siteDir, "posts/p.md", BlessedComment{URL: u}, priv); err != nil {
			t.Fatalf("AddBlessedCommentSigned(%s): %v", u, err)
		}
		if status, err := VerifyBlessedSite(siteDir); status != StatusValid {
			t.Fatalf("after adding %s: status = %q (%v), want %q", u, status, err, StatusValid)
		}
	}
}

// TestRemoveSignedKeepsTheFileVerifying: an unbless is as much an authored act
// as a bless, and must not leave the signature covering the pre-removal bytes.
func TestRemoveSignedKeepsTheFileVerifying(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, pub)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(AddBlessedCommentSigned(siteDir, "posts/p.md", BlessedComment{URL: "https://a.example/c/1.md"}, priv))
	must(AddBlessedCommentSigned(siteDir, "posts/p.md", BlessedComment{URL: "https://b.example/c/2.md"}, priv))
	must(RemoveBlessedCommentSigned(siteDir, "https://a.example/c/1.md", priv))

	status, err := VerifyBlessedSite(siteDir)
	if status != StatusValid {
		t.Errorf("after a signed removal: status = %q (%v), want %q", status, err, StatusValid)
	}
}

// TestUnsignedWriteClearsAStaleSignature is epic 02's rule, which caught a real
// bash bug: keeping a signature over changed content manufactures an INVALID
// finding (a HIGH alert a human has to chase) where degrading to UNSIGNED is
// honest and costs nobody anything.
func TestUnsignedWriteClearsAStaleSignature(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, pub)

	if err := AddBlessedCommentSigned(siteDir, "posts/p.md",
		BlessedComment{URL: "https://a.example/c/1.md"}, priv); err != nil {
		t.Fatalf("AddBlessedCommentSigned: %v", err)
	}
	if status, _ := VerifyBlessedSite(siteDir); status != StatusValid {
		t.Fatalf("setup: expected a valid signature first, got %q", status)
	}

	// A keyless path writes next — Chaplain, a legacy caller, anything.
	if err := AddBlessedComment(siteDir, "posts/p.md",
		BlessedComment{URL: "https://b.example/c/2.md"}); err != nil {
		t.Fatalf("AddBlessedComment: %v", err)
	}

	bc, err := LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if bc.Signature != "" {
		t.Error("an unsigned write left the old signature in place")
	}
	if status, _ := VerifyBlessedSite(siteDir); status != StatusUnsigned {
		t.Errorf("status = %q, want %q — unsigned is the safe degradation, invalid is a false alarm", status, StatusUnsigned)
	}
}

// TestUnsignedRemoveClearsAStaleSignature: the same rule on the removal path.
func TestUnsignedRemoveClearsAStaleSignature(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, pub)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(AddBlessedCommentSigned(siteDir, "posts/p.md", BlessedComment{URL: "https://a.example/c/1.md"}, priv))
	must(AddBlessedCommentSigned(siteDir, "posts/p.md", BlessedComment{URL: "https://b.example/c/2.md"}, priv))
	must(RemoveBlessedComment(siteDir, "https://a.example/c/1.md"))

	if status, _ := VerifyBlessedSite(siteDir); status != StatusUnsigned {
		t.Errorf("status = %q, want %q", status, StatusUnsigned)
	}
}

// TestSaveBlessedCommentsClearsAStaleSignature covers the bulk-write path
// (polis rebuild), which loads, edits and saves the whole document.
func TestSaveBlessedCommentsClearsAStaleSignature(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, pub)

	if err := AddBlessedCommentSigned(siteDir, "posts/p.md",
		BlessedComment{URL: "https://a.example/c/1.md"}, priv); err != nil {
		t.Fatalf("AddBlessedCommentSigned: %v", err)
	}

	bc, err := LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	bc.Comments[0].Blessed[0].URL = "https://rewritten.example/c/1.md"
	if err := SaveBlessedComments(siteDir, bc); err != nil {
		t.Fatalf("SaveBlessedComments: %v", err)
	}

	if status, _ := VerifyBlessedSite(siteDir); status != StatusUnsigned {
		t.Errorf("status = %q, want %q", status, StatusUnsigned)
	}
}

// TestSignedWriteStampsGeneratorBeforeSigning: `version` is the FIRST field in
// the signing base, so a stamp applied after signing writes a file that verifies
// nowhere. Asserting the resulting STATUS, not just the field, is what makes
// this test able to fail — see the version-propagation recipe.
func TestSignedWriteStampsGeneratorBeforeSigning(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, pub)

	Version = "9.9.9-test"
	t.Cleanup(func() { Version = "dev" })

	if err := AddBlessedCommentSigned(siteDir, "posts/p.md",
		BlessedComment{URL: "https://a.example/c/1.md"}, priv); err != nil {
		t.Fatalf("AddBlessedCommentSigned: %v", err)
	}

	bc, err := LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if bc.Version != "polis-cli-go/9.9.9-test" {
		t.Errorf("version = %q, want %q", bc.Version, "polis-cli-go/9.9.9-test")
	}
	if status, err := VerifyBlessedSite(siteDir); status != StatusValid {
		t.Errorf("status = %q (%v), want %q — the stamp must land BEFORE the signature", status, err, StatusValid)
	}
}

// TestGeneratorIsRestampedOnEveryWrite: before this epic the generator was
// written only when the file was CREATED, so a long-lived blessing list claimed
// whichever CLI first blessed anything — and once it went inside a signature,
// the site began signing that stale claim (epic 02's E4, exactly).
func TestGeneratorIsRestampedOnEveryWrite(t *testing.T) {
	siteDir := t.TempDir()

	Version = "1.0.0-old"
	if err := AddBlessedComment(siteDir, "posts/p.md", BlessedComment{URL: "https://a.example/c/1.md"}); err != nil {
		t.Fatalf("AddBlessedComment: %v", err)
	}

	Version = "2.0.0-new"
	t.Cleanup(func() { Version = "dev" })
	if err := AddBlessedComment(siteDir, "posts/p.md", BlessedComment{URL: "https://b.example/c/2.md"}); err != nil {
		t.Fatalf("AddBlessedComment: %v", err)
	}

	bc, err := LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if bc.Version != "polis-cli-go/2.0.0-new" {
		t.Errorf("version = %q, want %q — the generator froze at the creating CLI", bc.Version, "polis-cli-go/2.0.0-new")
	}
}

// TestSignedPathWithEmptyKeyFallsThroughToUnsigned: the safe direction. A caller
// that thought it had a key must not produce an INVALID file.
func TestSignedPathWithEmptyKeyFallsThroughToUnsigned(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()
	writeWellKnown(t, siteDir, pub)

	if err := AddBlessedCommentSigned(siteDir, "posts/p.md",
		BlessedComment{URL: "https://a.example/c/1.md"}, priv); err != nil {
		t.Fatalf("AddBlessedCommentSigned: %v", err)
	}
	if err := AddBlessedCommentSigned(siteDir, "posts/p.md",
		BlessedComment{URL: "https://b.example/c/2.md"}, nil); err != nil {
		t.Fatalf("AddBlessedCommentSigned with no key: %v", err)
	}

	if status, _ := VerifyBlessedSite(siteDir); status != StatusUnsigned {
		t.Errorf("status = %q, want %q", status, StatusUnsigned)
	}
}

// TestSignatureSurvivesAReadWriteRoundTrip: the on-disk JSON must carry the
// signature field, and reading it back must reproduce the same canonical bytes.
func TestSignatureSurvivesAReadWriteRoundTrip(t *testing.T) {
	priv, pub := newTestKeys(t)
	siteDir := t.TempDir()

	bc := sampleBlessed()
	if err := SaveBlessedCommentsSigned(siteDir, bc, priv); err != nil {
		t.Fatalf("SaveBlessedCommentsSigned: %v", err)
	}

	reloaded, err := LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	if reloaded.Signature == "" {
		t.Fatal("signature did not survive the write")
	}
	if status, err := VerifyBlessed(reloaded, pub); status != StatusValid {
		t.Errorf("status = %q (%v), want %q", status, err, StatusValid)
	}
}
