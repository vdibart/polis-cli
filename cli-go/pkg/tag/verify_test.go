package tag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func tagTestSite(t *testing.T) (siteDir string, priv []byte, pub []byte) {
	t.Helper()
	siteDir = t.TempDir()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(siteDir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk, _ := json.Marshal(map[string]string{"public_key": string(pub)})
	if err := os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), wk, 0644); err != nil {
		t.Fatal(err)
	}
	return siteDir, priv, pub
}

func writeTag(t *testing.T, siteDir string, tf *TagFile) string {
	t.Helper()
	path := TagPath(siteDir, tf.Tag)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(tf, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func signedTagFile(t *testing.T, priv []byte, name string) *TagFile {
	t.Helper()
	tf := &TagFile{
		Tag:       name,
		Targets:   []TagTarget{{URI: "https://alice.example/posts/x.md", Added: "2026-01-01T00:00:00Z"}},
		Created:   "2026-01-01T00:00:00Z",
		Updated:   "2026-01-01T00:00:00Z",
		Generator: "test",
	}
	canonical, err := canonicalJSON(tf)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, priv)
	if err != nil {
		t.Fatal(err)
	}
	tf.Signature = sig
	return tf
}

func TestVerify_SignedTagVerifiesAgainstTheSiteKey(t *testing.T) {
	_, priv, pub := tagTestSite(t)
	status, err := Verify(signedTagFile(t, priv, "reading"), pub)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if status != StatusValid {
		t.Errorf("status = %s, want valid", status)
	}
}

// Unsigned is a FACT. Tag files written before signing shipped carry no
// signature, and reporting those as broken would tell every early adopter
// their site is damaged.
func TestVerify_UnsignedIsAFactNotAFailure(t *testing.T) {
	_, _, pub := tagTestSite(t)
	status, err := Verify(&TagFile{Tag: "reading"}, pub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusUnsigned {
		t.Errorf("status = %s, want unsigned", status)
	}
}

func TestVerify_TamperedTargetsDoNotVerify(t *testing.T) {
	_, priv, pub := tagTestSite(t)
	tf := signedTagFile(t, priv, "reading")
	tf.Targets = append(tf.Targets, TagTarget{URI: "https://mallory.example/posts/y.md", Added: "2026-01-02T00:00:00Z"})

	status, _ := Verify(tf, pub)
	if status != StatusInvalid {
		t.Errorf("status = %s, want invalid — a target added after signing must not verify", status)
	}
}

// No key is "I did not look", never "I looked and it was wrong".
func TestVerify_NoKeyIsUnknownNotInvalid(t *testing.T) {
	_, priv, _ := tagTestSite(t)
	status, err := Verify(signedTagFile(t, priv, "reading"), nil)
	if status != StatusUnknown {
		t.Errorf("status = %s, want unknown", status)
	}
	if err == nil {
		t.Error("want an error explaining why it could not be checked")
	}
}

func TestVerifySite_ReportsPerTagStatusAgainstThePublishedKey(t *testing.T) {
	siteDir, priv, _ := tagTestSite(t)
	otherPriv, _, _ := signing.GenerateKeypair()

	writeTag(t, siteDir, signedTagFile(t, priv, "reading"))
	writeTag(t, siteDir, signedTagFile(t, otherPriv, "forged"))
	writeTag(t, siteDir, &TagFile{Tag: "bare", Created: "2026-01-01T00:00:00Z"})

	got, err := VerifySite(siteDir)
	if err != nil {
		t.Fatalf("VerifySite: %v", err)
	}
	want := map[string]SignatureStatus{
		"reading": StatusValid,
		"forged":  StatusInvalid,
		"bare":    StatusUnsigned,
	}
	for name, wantStatus := range want {
		if got[name] != wantStatus {
			t.Errorf("%s = %s, want %s", name, got[name], wantStatus)
		}
	}
}

// A site that has tagged nothing is an ordinary site, not a broken one.
func TestVerifySite_NoTagsIsNotAnError(t *testing.T) {
	siteDir, _, _ := tagTestSite(t)
	got, err := VerifySite(siteDir)
	if err != nil {
		t.Fatalf("a site with no tags must not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d statuses, want 0", len(got))
	}
}
