package following

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// newTestKeys returns a fresh (privatePEM, publicSSH) pair.
func newTestKeys(t *testing.T) ([]byte, []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return priv, pub
}

func sampleFile() *FollowingFile {
	return &FollowingFile{
		Version: "polis-cli-go/test",
		Following: []FollowingEntry{
			{URL: "https://alice.example", AddedAt: "2026-08-28T00:00:00Z", SiteTitle: "Alice"},
			{URL: "https://bob.example", AddedAt: "2026-08-28T00:01:00Z"},
		},
	}
}

// writeWellKnown drops a minimal .well-known/polis carrying pub, so VerifySite
// has an identity key to check against.
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

// TestCanonicalJSONIsTheSpec pins the exact bytes a second implementation must
// reproduce. If this test changes, every signature ever written stops
// verifying — so a diff here is a protocol break, not a refactor.
func TestCanonicalJSONIsTheSpec(t *testing.T) {
	got, err := canonicalJSON(sampleFile())
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	want := `{"version":"polis-cli-go/test","following":[` +
		`{"url":"https://alice.example","added_at":"2026-08-28T00:00:00Z","site_title":"Alice"},` +
		`{"url":"https://bob.example","added_at":"2026-08-28T00:01:00Z"}]}`
	if string(got) != want {
		t.Errorf("canonical JSON drifted.\n got: %s\nwant: %s", got, want)
	}
}

// TestCanonicalJSONExcludesSignature: the signature cannot cover itself.
func TestCanonicalJSONExcludesSignature(t *testing.T) {
	f := sampleFile()
	before, _ := canonicalJSON(f)
	f.Signature = "some-signature"
	after, _ := canonicalJSON(f)
	if string(before) != string(after) {
		t.Errorf("setting Signature changed the canonical form:\n%s\n%s", before, after)
	}
}

// TestCanonicalJSONEmptyListIsStable: "I follow nobody" must have ONE byte
// sequence, not two — a nil slice marshals as null and an empty one as [].
func TestCanonicalJSONEmptyListIsStable(t *testing.T) {
	nilList, _ := canonicalJSON(&FollowingFile{Version: "v"})
	emptyList, _ := canonicalJSON(&FollowingFile{Version: "v", Following: []FollowingEntry{}})
	if string(nilList) != string(emptyList) {
		t.Errorf("nil and empty follow lists canonicalize differently:\n%s\n%s", nilList, emptyList)
	}
	if strings.Contains(string(nilList), "null") {
		t.Errorf("empty follow list canonicalized with null: %s", nilList)
	}
}

// ---------- sign / verify ----------

func TestSignThenVerify(t *testing.T) {
	priv, pub := newTestKeys(t)
	f := sampleFile()

	if err := Sign(f, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if f.Signature == "" {
		t.Fatal("Sign left the signature empty")
	}

	status, err := Verify(f, pub)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if status != StatusValid {
		t.Errorf("status = %q, want %q", status, StatusValid)
	}
}

// TestSignatureSurvivesTheFile is the round trip that matters: sign, write,
// read back off disk, verify. An in-memory-only test would not catch a
// marshalling change that alters the bytes the signature covers.
func TestSignatureSurvivesTheFile(t *testing.T) {
	priv, pub := newTestKeys(t)
	path := filepath.Join(t.TempDir(), "following.json")

	f := sampleFile()
	if err := SaveSigned(path, f, priv); err != nil {
		t.Fatalf("SaveSigned: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	status, err := Verify(loaded, pub)
	if err != nil {
		t.Fatalf("Verify after round trip: %v", err)
	}
	if status != StatusValid {
		t.Errorf("status after round trip = %q, want %q", status, StatusValid)
	}
}

// TestTamperedFileIsInvalidAndStillLoads is the D4 guarantee, in one test.
//
// A follow list is how a person reaches their network. A bad signature must be
// REPORTED and the file must still be USABLE — refusing to load it would
// disconnect someone as a security response, and the likeliest cause is a polis
// bug, not an attacker. A wrong signature is evidence, not a verdict.
func TestTamperedFileIsInvalidAndStillLoads(t *testing.T) {
	priv, pub := newTestKeys(t)
	path := filepath.Join(t.TempDir(), "following.json")

	f := sampleFile()
	if err := SaveSigned(path, f, priv); err != nil {
		t.Fatalf("SaveSigned: %v", err)
	}

	// Tamper: splice an author into the JSON on disk, leaving the signature
	// untouched — exactly what an attacker editing the file would do.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tampered := strings.Replace(string(raw),
		`"url": "https://bob.example"`,
		`"url": "https://mallory.example"`, 1)
	if tampered == string(raw) {
		t.Fatal("tamper failed to change the file — the fixture shape moved")
	}
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("a tampered follow file must STILL LOAD, got error: %v", err)
	}
	if loaded.Count() != 2 {
		t.Errorf("a tampered follow file must keep serving its entries, got %d", loaded.Count())
	}
	if !loaded.IsFollowing("https://mallory.example") {
		t.Error("the tampered entry should be present — the file is used, not filtered")
	}

	status, verr := Verify(loaded, pub)
	if status != StatusInvalid {
		t.Errorf("status = %q, want %q", status, StatusInvalid)
	}
	if verr == nil {
		t.Error("an invalid signature should carry an explanation for the human chasing it")
	}
}

// TestUnsignedIsUnsignedNotInvalid is THE day-one trap. Backfill is epic 11, so
// the overwhelming majority of tenants have unsigned follow files for a long
// time. A checker that reads absent-as-failure turns the whole fleet red on its
// first sweep.
func TestUnsignedIsUnsignedNotInvalid(t *testing.T) {
	_, pub := newTestKeys(t)

	legacy := sampleFile() // no Signature — a pre-epic-02 file
	status, err := Verify(legacy, pub)
	if err != nil {
		t.Errorf("an unsigned file is a FACT, not an error: %v", err)
	}
	if status != StatusUnsigned {
		t.Errorf("status = %q, want %q", status, StatusUnsigned)
	}
	if status == StatusInvalid {
		t.Error("unsigned must never collapse into invalid")
	}
}

// TestVerifyWrongKeyIsInvalid — signed by someone else's key.
func TestVerifyWrongKeyIsInvalid(t *testing.T) {
	priv, _ := newTestKeys(t)
	_, otherPub := newTestKeys(t)

	f := sampleFile()
	if err := Sign(f, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	status, _ := Verify(f, otherPub)
	if status != StatusInvalid {
		t.Errorf("status = %q, want %q", status, StatusInvalid)
	}
}

// TestVerifyWithoutKeyIsUnknown — cannot check is not the same as failed.
func TestVerifyWithoutKeyIsUnknown(t *testing.T) {
	priv, _ := newTestKeys(t)
	f := sampleFile()
	if err := Sign(f, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	status, _ := Verify(f, nil)
	if status != StatusUnknown {
		t.Errorf("status = %q, want %q", status, StatusUnknown)
	}
}

// ---------- write-path behaviour ----------

// TestSaveClearsSignature: an unsigned write must degrade to "unsigned", never
// leave a now-stale signature behind. Stale would manufacture a false
// "does not verify" finding for a human to chase; unsigned costs nobody
// anything.
func TestSaveClearsSignature(t *testing.T) {
	priv, pub := newTestKeys(t)
	path := filepath.Join(t.TempDir(), "following.json")

	f := sampleFile()
	if err := Sign(f, priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	f.Add("https://carol.example") // content changes; the signature is now void

	if err := Save(path, f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	status, _ := Verify(loaded, pub)
	if status != StatusUnsigned {
		t.Errorf("status after unsigned Save = %q, want %q (never %q)",
			status, StatusUnsigned, StatusInvalid)
	}
}

// TestSaveSignedWithoutKeyWritesUnsigned — no key, no signature, no error.
func TestSaveSignedWithoutKeyWritesUnsigned(t *testing.T) {
	_, pub := newTestKeys(t)
	path := filepath.Join(t.TempDir(), "following.json")

	if err := SaveSigned(path, sampleFile(), nil); err != nil {
		t.Fatalf("SaveSigned with no key: %v", err)
	}
	loaded, _ := Load(path)
	status, _ := Verify(loaded, pub)
	if status != StatusUnsigned {
		t.Errorf("status = %q, want %q", status, StatusUnsigned)
	}
}

// ---------- VerifySite ----------

func TestVerifySite(t *testing.T) {
	priv, pub := newTestKeys(t)

	t.Run("signed site verifies", func(t *testing.T) {
		siteDir := t.TempDir()
		writeWellKnown(t, siteDir, pub)
		if err := SaveSigned(DefaultPath(siteDir), sampleFile(), priv); err != nil {
			t.Fatalf("SaveSigned: %v", err)
		}
		status, err := VerifySite(siteDir)
		if err != nil {
			t.Fatalf("VerifySite: %v", err)
		}
		if status != StatusValid {
			t.Errorf("status = %q, want %q", status, StatusValid)
		}
	})

	t.Run("site with no follow file at all is unsigned", func(t *testing.T) {
		siteDir := t.TempDir()
		writeWellKnown(t, siteDir, pub)
		status, err := VerifySite(siteDir)
		if err != nil {
			t.Errorf("a site that follows nobody is not an error: %v", err)
		}
		if status != StatusUnsigned {
			t.Errorf("status = %q, want %q", status, StatusUnsigned)
		}
	})

	t.Run("legacy unsigned follow file is unsigned", func(t *testing.T) {
		siteDir := t.TempDir()
		writeWellKnown(t, siteDir, pub)
		if err := Save(DefaultPath(siteDir), sampleFile()); err != nil {
			t.Fatalf("Save: %v", err)
		}
		status, err := VerifySite(siteDir)
		if err != nil {
			t.Errorf("an unsigned file is a fact, not an error: %v", err)
		}
		if status != StatusUnsigned {
			t.Errorf("status = %q, want %q", status, StatusUnsigned)
		}
	})

	// A signed file with no .well-known to check it against is UNKNOWN, not a
	// failure — we could not look, which is different from having looked and
	// found a problem.
	t.Run("signed but no well-known is unknown", func(t *testing.T) {
		siteDir := t.TempDir()
		if err := SaveSigned(DefaultPath(siteDir), sampleFile(), priv); err != nil {
			t.Fatalf("SaveSigned: %v", err)
		}
		status, _ := VerifySite(siteDir)
		if status != StatusUnknown {
			t.Errorf("status = %q, want %q", status, StatusUnknown)
		}
	})

	t.Run("tampered site is invalid", func(t *testing.T) {
		siteDir := t.TempDir()
		writeWellKnown(t, siteDir, pub)
		path := DefaultPath(siteDir)
		if err := SaveSigned(path, sampleFile(), priv); err != nil {
			t.Fatalf("SaveSigned: %v", err)
		}
		raw, _ := os.ReadFile(path)
		tampered := strings.Replace(string(raw), "https://bob.example", "https://mallory.example", 1)
		if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
			t.Fatalf("write tampered: %v", err)
		}
		status, _ := VerifySite(siteDir)
		if status != StatusInvalid {
			t.Errorf("status = %q, want %q", status, StatusInvalid)
		}
	})
}

// ---------- the generator stamp (E4) ----------

// TestSaveSignedStampsGeneratorInsideTheSignature is the regression test for
// E4, and the assertion that matters is the SECOND one.
//
// Stamping `version` is easy to get right and easy to get subtly wrong: stamp
// it AFTER Sign and the file still writes, still parses, still looks correct in
// an editor — and never verifies again, because the signature covers a
// different version string than the one on disk. The status assertion is what
// catches that; the field assertion alone would pass either way.
func TestSaveSignedStampsGeneratorInsideTheSignature(t *testing.T) {
	priv, pub := newTestKeys(t)
	path := filepath.Join(t.TempDir(), "following.json")

	old := Version
	Version = "9.9.9"
	defer func() { Version = old }()

	f := sampleFile()
	f.Version = "polis-cli-go/0.56.0" // the stale value E4 found in production
	if err := SaveSigned(path, f, priv); err != nil {
		t.Fatalf("SaveSigned: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != "polis-cli-go/9.9.9" {
		t.Errorf("version = %q, want %q — the writer was not stamped", got.Version, "polis-cli-go/9.9.9")
	}

	status, err := Verify(got, pub)
	if status != StatusValid {
		t.Fatalf("status = %q (%v), want valid — the stamp is OUTSIDE the signature, "+
			"which means stampGenerator ran after Sign", status, err)
	}
}

// TestSaveStampsGenerator — the unsigned path records the writer too. A file
// that degrades to unsigned should still say who wrote it.
func TestSaveStampsGenerator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "following.json")

	old := Version
	Version = "1.2.3"
	defer func() { Version = old }()

	f := sampleFile()
	f.Version = "polis-cli-go/0.56.0"
	if err := Save(path, f); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != "polis-cli-go/1.2.3" {
		t.Errorf("version = %q, want %q", got.Version, "polis-cli-go/1.2.3")
	}
	if got.Signature != "" {
		t.Errorf("Save left a signature: %q", got.Signature)
	}
}

// TestGetGenerator pins the format. Bash writes "polis-cli/$VERSION" into the
// same field, so the prefix is what distinguishes the two implementations in a
// published artifact — it is not cosmetic.
func TestGetGenerator(t *testing.T) {
	old := Version
	Version = "0.67.0"
	defer func() { Version = old }()

	if got := GetGenerator(); got != "polis-cli-go/0.67.0" {
		t.Errorf("GetGenerator() = %q, want %q", got, "polis-cli-go/0.67.0")
	}
}
