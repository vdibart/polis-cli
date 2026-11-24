// Package releasekey pins the key that signs each polis release, and checks a
// release's checksums.txt against it.
//
// ⭐ HOW A RELEASE IS TRUSTED. Every release publishes checksums.txt (the
// SHA-256 of every asset, and of the files polis-upgrade fetches from the
// tagged tree) and checksums.txt.sig, an SSH signature over it made with
// `ssh-keygen -Y sign -n polis-release` by the release key. A program that
// downloads from a release verifies the signature with the key PINNED HERE,
// then each file against checksums.txt.
//
// ⛔ THE KEY IS PINNED, NEVER FETCHED. A key read from the same place as the
// files would prove nothing: whoever could replace the files could replace
// the key. So the public key is compiled in, and a new key reaches a verifier
// only in a new release of it. That fits how it is rotated: only on compromise.
//
// ⛔ ONE SOURCE. The key is release/release-signing-key.pub; this constant and
// polis-upgrade's copy are generated from it by the release tooling (set-release-key.sh),
// and a test fails if any of the three differ.
package releasekey

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Namespace is the SSHSIG namespace release signatures are made under, so a
// signature made by the same key for any other purpose never verifies here.
const Namespace = "polis-release"

// PublicKey is the release key's public half, in OpenSSH form.
// Generated from release/release-signing-key.pub by the release tooling (set-release-key.sh).
const PublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAING2XN2oftAR/Z2d29gQm1vSaTsRyvlTeWt0WxNugooY polis-release"

// ChecksumsAsset and SignatureAsset are the names a release publishes them under.
const (
	ChecksumsAsset = "checksums.txt"
	SignatureAsset = "checksums.txt.sig"
)

// ErrNotConfigured is returned while the pinned key is still the placeholder:
// no release can be verified, and none can have been published.
var ErrNotConfigured = errors.New("releasekey: no release-signing key is pinned in this build")

// Configured reports whether a real key is pinned.
func Configured(pub string) bool {
	return strings.HasPrefix(strings.TrimSpace(pub), "ssh-ed25519 ")
}

// Verify checks sig over checksums with the pinned key and returns the
// checksums, by name. It refuses an unsigned or tampered checksums.txt.
func Verify(checksums []byte, sig string) (Checksums, error) {
	return VerifyWith(PublicKey, checksums, sig)
}

// VerifyWith is Verify with a given public key: for tests, and for nothing else.
func VerifyWith(pub string, checksums []byte, sig string) (Checksums, error) {
	if !Configured(pub) {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(sig) == "" {
		return nil, errors.New("releasekey: the release publishes no checksums.txt.sig")
	}
	ok, err := signing.VerifySignatureNS(checksums, []byte(pub), sig, Namespace)
	if err != nil {
		return nil, fmt.Errorf("releasekey: checksums.txt.sig is not a signature: %w", err)
	}
	if !ok {
		return nil, errors.New("releasekey: checksums.txt is not signed by the polis release key")
	}
	return ParseChecksums(checksums)
}

// Checksums maps a file's name in checksums.txt to its SHA-256, in hex.
type Checksums map[string]string

// ParseChecksums reads sha256sum's format: "<64 hex>  <name>" per line. A
// malformed or repeated line is refused rather than skipped.
func ParseChecksums(b []byte) (Checksums, error) {
	sums := Checksums{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || name == "" || strings.TrimFunc(sum, isHex) != "" {
			return nil, fmt.Errorf("releasekey: checksums.txt line %d is not \"<sha256>  <name>\"", n)
		}
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("releasekey: checksums.txt names %s twice", name)
		}
		sums[name] = strings.ToLower(sum)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sums, nil
}

// Check refuses data that is not the file checksums.txt names.
func (c Checksums) Check(name string, data []byte) error {
	want, ok := c[name]
	if !ok {
		return fmt.Errorf("releasekey: checksums.txt does not list %s", name)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("releasekey: %s is not the file the release signed", name)
	}
	return nil
}

func isHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
