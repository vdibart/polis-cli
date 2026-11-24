package releasekey

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// placeholder is the line release/release-signing-key.pub holds until the real key is created.
const placeholder = "PLACEHOLDER-NOT-A-KEY no release-signing key has been created yet, so no release can be published or verified"

func sumLine(name string, data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:]) + "  " + name + "\n"
}

func TestVerifyAcceptsTheSignedChecksumsAndRefusesEverythingElse(t *testing.T) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	asset := []byte("an archive")
	checksums := []byte(sumLine("polis-linux-amd64.tar.gz", asset) + sumLine("cli-bash/polis", []byte("#!/bin/bash\n")))
	sig, err := signing.SignContentNS(checksums, priv, Namespace)
	if err != nil {
		t.Fatal(err)
	}

	sums, err := VerifyWith(string(pub), checksums, sig)
	if err != nil {
		t.Fatalf("good signature refused: %v", err)
	}
	if err := sums.Check("polis-linux-amd64.tar.gz", asset); err != nil {
		t.Fatalf("listed asset refused: %v", err)
	}
	if err := sums.Check("polis-linux-amd64.tar.gz", []byte("another archive")); err == nil {
		t.Error("an asset that differs from checksums.txt was accepted")
	}
	if err := sums.Check("unlisted.tar.gz", asset); err == nil {
		t.Error("an asset checksums.txt does not list was accepted")
	}

	tampered := []byte(strings.Replace(string(checksums), "polis-linux", "polis-linux-x", 1))
	if _, err := VerifyWith(string(pub), tampered, sig); err == nil {
		t.Error("a tampered checksums.txt verified")
	}
	if _, err := VerifyWith(string(pub), checksums, ""); err == nil {
		t.Error("a missing signature verified")
	}
	_, otherPub, _ := signing.GenerateKeypair()
	if _, err := VerifyWith(string(otherPub), checksums, sig); err == nil {
		t.Error("a signature by another key verified")
	}
	// Same key, another namespace: a content signature over the same bytes is not a release signature.
	contentSig, _ := signing.SignContent(checksums, priv)
	if _, err := VerifyWith(string(pub), checksums, contentSig); err == nil {
		t.Error("a signature under another namespace verified")
	}
	if _, err := VerifyWith(placeholder, checksums, sig); err != ErrNotConfigured {
		t.Errorf("the placeholder key: got %v, want ErrNotConfigured", err)
	}
}

// ⭐ The release signs with ssh-keygen itself, so the verifier must accept exactly what ssh-keygen makes.
func TestVerifyAcceptsAnSSHKeygenSignature(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "release-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "test release key", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	file := filepath.Join(dir, ChecksumsAsset)
	checksums := []byte(sumLine("vercel-function.json", []byte("{}")))
	if err := os.WriteFile(file, checksums, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("ssh-keygen", "-Y", "sign", "-q", "-f", key, "-n", Namespace, file).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y sign: %v %s", err, out)
	}
	sig, err := os.ReadFile(file + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWith(strings.TrimSpace(string(pub)), checksums, string(sig)); err != nil {
		t.Fatalf("ssh-keygen's signature refused: %v", err)
	}
}

func TestParseChecksumsRefusesWhatSha256sumWouldNotWrite(t *testing.T) {
	for _, bad := range []string{
		"nothex  a\n",
		strings.Repeat("a", 64) + " a\n",
		strings.Repeat("a", 64) + "  \n",
		strings.Repeat("a", 64) + "  a\n" + strings.Repeat("b", 64) + "  a\n",
	} {
		if _, err := ParseChecksums([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// ⛔ ONE SOURCE: release/release-signing-key.pub, this package's constant and
// polis-upgrade's RELEASE_SIGNING_KEY are the same key. Change the file and run
// the release tooling's set-release-key.sh; this test fails on anything else.
func TestThePinnedKeyHasOneSource(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	file, err := os.ReadFile(filepath.Join(root, "release", "release-signing-key.pub"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(file))
	if PublicKey != want {
		t.Errorf("releasekey.PublicKey is not release/release-signing-key.pub: regenerate both copies with the release tooling's set-release-key.sh")
	}
	var upgrade []byte
	// The development tree keeps the bash scripts in cli-bash's bin directory; the public tree flattens them
	// into cli-bash. This test runs in both, so it looks in both.
	for _, p := range []string{filepath.Join("cli-bash", "bin", "polis-upgrade"), filepath.Join("cli-bash", "polis-upgrade")} {
		if upgrade, err = os.ReadFile(filepath.Join(root, p)); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^RELEASE_SIGNING_KEY="([^"]*)"$`).FindSubmatch(upgrade)
	if m == nil || string(m[1]) != want {
		t.Errorf("polis-upgrade's RELEASE_SIGNING_KEY is not release/release-signing-key.pub: regenerate the copies with the release tooling's set-release-key.sh")
	}
	// install.sh: read from its private source in the development repository, or from scripts/install.sh, where it is published.
	var install []byte
	for _, p := range []string{filepath.Join("release", "install.sh"), filepath.Join("scripts", "install.sh")} {
		if install, err = os.ReadFile(filepath.Join(root, p)); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`(?m)^RELEASE_SIGNING_KEY="([^"]*)"$`).FindSubmatch(install); m == nil || string(m[1]) != want {
		t.Errorf("install.sh's RELEASE_SIGNING_KEY is not release/release-signing-key.pub: regenerate the copies with the release tooling's set-release-key.sh")
	}
	if want != placeholder && !Configured(want) {
		t.Errorf("release/release-signing-key.pub is neither the placeholder nor an ssh-ed25519 key")
	}
}
