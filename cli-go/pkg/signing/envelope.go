package signing

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// SSHSIG envelope (identity-field) inspection & repair.
//
// Background: polis-cli-go 0.59.0 zeroed the private key *before* deriving the
// public key, so SignContent embedded an all-zero Ed25519 key in the SSHSIG
// `publickey` field while the 64-byte signature itself stayed valid (see the
// signer fix + regression test in signing.go / signing_test.go). The embedded
// key is envelope metadata and is NOT covered by the signature, so it can be
// corrected with public information only — no private key required.
//
// Internal polis verifiers (Judge, Patrol, preview, the DS) ignore the embedded
// key and verify against the author's `.well-known/polis` key, so a zeroed
// envelope is inert internally; it only fails STANDARD SSHSIG tooling
// (`ssh-keygen -Y verify`). These helpers power the Patrol detection and Medic
// keyless repair.

// decodeBareSignature base64-decodes a bare (unarmored) SSHSIG as stored in a
// content frontmatter `signature:` field.
func decodeBareSignature(bareSig string) ([]byte, error) {
	clean := strings.Join(strings.Fields(bareSig), "") // drop any whitespace/newlines
	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("decode bare signature: %w", err)
	}
	return raw, nil
}

// walkEnvelope parses decoded SSHSIG bytes and returns the embedded public key
// and the raw 64-byte signature. It mirrors the field order that
// formatSSHSignature emits: magic, version, publickey, namespace, reserved,
// hashalg, signature.
func walkEnvelope(raw []byte) (embeddedKey []byte, rawSig []byte, err error) {
	if len(raw) < 6 || string(raw[:6]) != sshSigMagic {
		return nil, nil, fmt.Errorf("invalid signature magic")
	}
	data := raw[6:]
	if _, data, err = readUint32(data); err != nil { // version
		return nil, nil, fmt.Errorf("parse version: %w", err)
	}
	var pubBlob []byte
	if pubBlob, data, err = readBytes(data); err != nil { // publickey blob
		return nil, nil, fmt.Errorf("parse publickey blob: %w", err)
	}
	// publickey blob = string(keytype) + string(keydata)
	_, rest, err := readString(pubBlob) // keytype ("ssh-ed25519")
	if err != nil {
		return nil, nil, fmt.Errorf("parse key type: %w", err)
	}
	if embeddedKey, _, err = readBytes(rest); err != nil {
		return nil, nil, fmt.Errorf("parse embedded key: %w", err)
	}
	if _, data, err = readString(data); err != nil { // namespace
		return nil, nil, fmt.Errorf("parse namespace: %w", err)
	}
	if _, data, err = readString(data); err != nil { // reserved
		return nil, nil, fmt.Errorf("parse reserved: %w", err)
	}
	if _, data, err = readString(data); err != nil { // hash algorithm
		return nil, nil, fmt.Errorf("parse hash algorithm: %w", err)
	}
	var sigBlob []byte
	if sigBlob, _, err = readBytes(data); err != nil { // signature blob
		return nil, nil, fmt.Errorf("parse signature blob: %w", err)
	}
	if _, sigBlob, err = readString(sigBlob); err != nil { // sig keytype
		return nil, nil, fmt.Errorf("parse signature key type: %w", err)
	}
	if rawSig, _, err = readBytes(sigBlob); err != nil {
		return nil, nil, fmt.Errorf("parse raw signature: %w", err)
	}
	return embeddedKey, rawSig, nil
}

// EmbeddedKey returns the Ed25519 public key advertised in a bare SSHSIG's
// envelope. Internal verifiers ignore this field; it matters only for standard
// SSHSIG tooling.
func EmbeddedKey(bareSig string) (ed25519.PublicKey, error) {
	raw, err := decodeBareSignature(bareSig)
	if err != nil {
		return nil, err
	}
	key, _, err := walkEnvelope(raw)
	if err != nil {
		return nil, err
	}
	if len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("embedded key is %d bytes, want %d", len(key), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(key), nil
}

// IsZeroedEnvelope reports whether a bare SSHSIG embeds an all-zero public key
// (the 0.59.0 defect). Errors are returned for un-parseable signatures so a
// caller can distinguish "malformed" from "zeroed".
func IsZeroedEnvelope(bareSig string) (bool, error) {
	key, err := EmbeddedKey(bareSig)
	if err != nil {
		return false, err
	}
	return isAllZero(key), nil
}

func isAllZero(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// RepairEnvelope rewrites the SSHSIG `publickey` field of a bare signature with
// realPubKey, leaving the 64-byte signature byte-identical. It returns the
// corrected bare base64 and whether a change was made. Idempotent: returns
// changed=false when the embedded key already equals realPubKey. No private key
// is required — the embedded key is not covered by the signature.
//
// The envelope is regenerated with formatSSHSignature, the same function that
// produced the original signature, so the result differs from a correctly-signed
// original only in the previously-zeroed key field.
func RepairEnvelope(bareSig string, realPubKey ed25519.PublicKey) (string, bool, error) {
	raw, err := decodeBareSignature(bareSig)
	if err != nil {
		return "", false, err
	}
	embedded, rawSig, err := walkEnvelope(raw)
	if err != nil {
		return "", false, err
	}
	if bytes.Equal(embedded, realPubKey) {
		return bareSig, false, nil
	}
	fixedPEM, err := formatSSHSignature(realPubKey, rawSig)
	if err != nil {
		return "", false, err
	}
	return bareFromPEM(fixedPEM), true, nil
}

// ParsePublicKey parses an OpenSSH-format Ed25519 public key
// ("ssh-ed25519 <base64> [comment]") into an ed25519.PublicKey. Consumers that
// hold the author's key as the well-known OpenSSH string (Patrol, Medic) use
// this to feed RepairEnvelope.
func ParsePublicKey(openSSHPubKey []byte) (ed25519.PublicKey, error) {
	return parsePublicKey(openSSHPubKey)
}

// bareFromPEM strips SSH SIGNATURE armor + newlines, yielding the single-line
// base64 form stored in frontmatter (matches publish.extractSignatureBase64).
func bareFromPEM(pemStr string) string {
	var b strings.Builder
	for _, line := range strings.Split(pemStr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}
