// Package signing provides Ed25519 key generation, SSH signature format signing,
// and Ed25519↔X25519 key conversion for encryption.
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"

	"filippo.io/edwards25519"
)

const (
	// SSH signature namespace for file signing
	sshNamespace = "file"
	// SSH signature magic header
	sshSigMagic = "SSHSIG"
	// SSH signature version
	sshSigVersion = 1
	// Hash algorithm for signature
	hashAlgorithm = "sha512"
)

// GenerateKeypair generates a new Ed25519 keypair and returns them in OpenSSH format.
// Returns (privateKeyPEM, publicKeyOpenSSH, error)
func GenerateKeypair() ([]byte, []byte, error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate keypair: %w", err)
	}

	// Encode private key in OpenSSH format
	privPEM, err := encodePrivateKey(privKey)
	if err != nil {
		return nil, nil, err
	}

	// Encode public key in OpenSSH format
	pubSSH := encodePublicKey(pubKey)

	return privPEM, pubSSH, nil
}

// ValidatePrivateKey checks whether pemData contains a valid Ed25519 private key.
// Returns nil if valid, an error otherwise. The parsed key is not retained.
func ValidatePrivateKey(pemData []byte) error {
	_, err := parsePrivateKey(pemData)
	return err
}

// ParsePrivateKey parses an OpenSSH PEM private key and returns the Ed25519 key.
func ParsePrivateKey(pemData []byte) (ed25519.PrivateKey, error) {
	return parsePrivateKey(pemData)
}

// SignContent signs content with the private key and returns an SSH signature.
// The privateKey should be in OpenSSH PEM format.
// This produces signatures compatible with `ssh-keygen -Y sign`.
func SignContent(content, privateKeyPEM []byte) (string, error) {
	// Parse private key
	privKey, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return "", err
	}

	// Build the SSH signing blob (what ssh-keygen -Y sign actually signs)
	// Format: MAGIC + namespace + reserved + hash_algo + hash(content)
	signingBlob := buildSigningBlob(content)

	// Sign the blob, not the raw content
	sig := ed25519.Sign(privKey, signingBlob)

	// Format as SSH signature
	sshSig, err := formatSSHSignature(privKey.Public().(ed25519.PublicKey), sig)
	if err != nil {
		return "", err
	}

	return sshSig, nil
}

// buildSigningBlob creates the blob that SSH signature verification expects.
// This matches what ssh-keygen -Y sign creates internally.
func buildSigningBlob(content []byte) []byte {
	var blob []byte

	// Magic preamble (literal bytes, not length-prefixed)
	blob = append(blob, []byte(sshSigMagic)...)

	// Namespace (length-prefixed string)
	blob = appendString(blob, sshNamespace)

	// Reserved (length-prefixed empty string)
	blob = appendString(blob, "")

	// Hash algorithm (length-prefixed string)
	blob = appendString(blob, hashAlgorithm)

	// Hash of content (length-prefixed bytes)
	hash := sha512.Sum512(content)
	blob = appendBytes(blob, hash[:])

	return blob
}

// VerifySignature verifies an SSH signature against content using the public key.
// The publicKey should be in OpenSSH format.
// This verifies signatures compatible with `ssh-keygen -Y verify`.
func VerifySignature(content, publicKeySSH []byte, signature string) (bool, error) {
	// Parse public key
	pubKey, err := parsePublicKey(publicKeySSH)
	if err != nil {
		return false, err
	}

	// Parse SSH signature
	sig, err := parseSSHSignature(signature)
	if err != nil {
		return false, err
	}

	// Build the same signing blob that was used for signing
	signingBlob := buildSigningBlob(content)

	// Verify against the signing blob, not the raw content
	return ed25519.Verify(pubKey, signingBlob, sig), nil
}

// encodePrivateKey encodes an Ed25519 private key in OpenSSH PEM format.
func encodePrivateKey(privKey ed25519.PrivateKey) ([]byte, error) {
	// OpenSSH private key format (simplified)
	// This is a simplified version - full implementation would include
	// proper OpenSSH key format with encryption support

	pubKey := privKey.Public().(ed25519.PublicKey)

	// Build the private key blob
	// Format: openssh-key-v1 + null + cipher + kdf + kdf options + num keys + pubkey + privkey
	var blob []byte

	// Auth magic
	authMagic := []byte("openssh-key-v1\x00")
	blob = append(blob, authMagic...)

	// Cipher name (none = unencrypted)
	blob = appendString(blob, "none")

	// KDF name (none = no key derivation)
	blob = appendString(blob, "none")

	// KDF options (empty for none)
	blob = appendString(blob, "")

	// Number of keys
	blob = appendUint32(blob, 1)

	// Public key blob
	pubBlob := encodePublicKeyBlob(pubKey)
	blob = appendBytes(blob, pubBlob)

	// Private key section
	var privSection []byte

	// Check numbers (random, must match)
	checkNum := make([]byte, 4)
	rand.Read(checkNum)
	privSection = append(privSection, checkNum...)
	privSection = append(privSection, checkNum...)

	// Key type
	privSection = appendString(privSection, "ssh-ed25519")

	// Public key
	privSection = appendBytes(privSection, pubKey)

	// Private key (OpenSSH stores priv+pub concatenated for ed25519)
	privSection = appendBytes(privSection, privKey)

	// Comment (empty)
	privSection = appendString(privSection, "")

	// Padding to block size (8 bytes for none cipher)
	for i := 1; len(privSection)%8 != 0; i++ {
		privSection = append(privSection, byte(i))
	}

	// Append private section
	blob = appendBytes(blob, privSection)

	// PEM encode
	pemBlock := &pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: blob,
	}

	return pem.EncodeToMemory(pemBlock), nil
}

// encodePublicKey encodes an Ed25519 public key in OpenSSH format.
func encodePublicKey(pubKey ed25519.PublicKey) []byte {
	blob := encodePublicKeyBlob(pubKey)
	encoded := base64.StdEncoding.EncodeToString(blob)
	return []byte(fmt.Sprintf("ssh-ed25519 %s polis-local\n", encoded))
}

// encodePublicKeyBlob encodes the public key blob.
func encodePublicKeyBlob(pubKey ed25519.PublicKey) []byte {
	var blob []byte
	blob = appendString(blob, "ssh-ed25519")
	blob = appendBytes(blob, pubKey)
	return blob
}

// parsePrivateKey parses an OpenSSH PEM private key.
func parsePrivateKey(pemData []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	if block.Type != "OPENSSH PRIVATE KEY" {
		return nil, fmt.Errorf("unexpected key type: %s", block.Type)
	}

	// Parse the OpenSSH key format
	data := block.Bytes

	// Check auth magic
	magic := "openssh-key-v1\x00"
	if len(data) < len(magic) || string(data[:len(magic)]) != magic {
		return nil, fmt.Errorf("invalid openssh key format")
	}
	data = data[len(magic):]

	// Skip cipher, kdf, kdf options
	_, data, _ = readString(data)
	_, data, _ = readString(data)
	_, data, _ = readString(data)

	// Number of keys
	_, data, _ = readUint32(data)

	// Skip public key blob
	_, data, _ = readBytes(data)

	// Private key section
	privSection, _, _ := readBytes(data)

	// Skip check numbers
	privSection = privSection[8:]

	// Key type
	_, privSection, _ = readString(privSection)

	// Public key
	_, privSection, _ = readBytes(privSection)

	// Private key (ed25519 stores 64 bytes: seed + public)
	privKeyBytes, _, _ := readBytes(privSection)

	if len(privKeyBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size")
	}

	return ed25519.PrivateKey(privKeyBytes), nil
}

// parsePublicKey parses an OpenSSH public key.
func parsePublicKey(sshData []byte) (ed25519.PublicKey, error) {
	parts := splitSSHKey(string(sshData))
	if len(parts) < 2 || parts[0] != "ssh-ed25519" {
		return nil, fmt.Errorf("invalid public key format")
	}

	blob, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode public key: %w", err)
	}

	// Skip key type string
	_, blob, _ = readString(blob)

	// Read public key bytes
	pubBytes, _, _ := readBytes(blob)

	if len(pubBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size")
	}

	return ed25519.PublicKey(pubBytes), nil
}

// formatSSHSignature creates an SSH signature in the standard format.
func formatSSHSignature(pubKey ed25519.PublicKey, sig []byte) (string, error) {
	var blob []byte

	// Magic preamble
	blob = append(blob, []byte(sshSigMagic)...)

	// Version
	blob = appendUint32(blob, sshSigVersion)

	// Public key blob
	pubBlob := encodePublicKeyBlob(pubKey)
	blob = appendBytes(blob, pubBlob)

	// Namespace
	blob = appendString(blob, sshNamespace)

	// Reserved (empty)
	blob = appendString(blob, "")

	// Hash algorithm
	blob = appendString(blob, hashAlgorithm)

	// Signature blob
	var sigBlob []byte
	sigBlob = appendString(sigBlob, "ssh-ed25519")
	sigBlob = appendBytes(sigBlob, sig)
	blob = appendBytes(blob, sigBlob)

	// PEM encode
	pemBlock := &pem.Block{
		Type:  "SSH SIGNATURE",
		Bytes: blob,
	}

	return string(pem.EncodeToMemory(pemBlock)), nil
}

// parseSSHSignature parses an SSH signature and returns the raw signature bytes.
func parseSSHSignature(sshSig string) ([]byte, error) {
	block, _ := pem.Decode([]byte(sshSig))
	if block == nil {
		return nil, fmt.Errorf("failed to decode signature PEM")
	}

	if block.Type != "SSH SIGNATURE" {
		return nil, fmt.Errorf("unexpected signature type: %s", block.Type)
	}

	data := block.Bytes

	// Check magic
	if len(data) < 6 || string(data[:6]) != sshSigMagic {
		return nil, fmt.Errorf("invalid signature magic")
	}
	data = data[6:]

	// Skip version, public key, namespace, reserved, hash algorithm
	_, data, _ = readUint32(data)
	_, data, _ = readBytes(data)
	_, data, _ = readString(data)
	_, data, _ = readString(data)
	_, data, _ = readString(data)

	// Signature blob
	sigBlob, _, _ := readBytes(data)

	// Skip key type in signature blob
	_, sigBlob, _ = readString(sigBlob)

	// Raw signature
	rawSig, _, _ := readBytes(sigBlob)

	return rawSig, nil
}

// Helper functions for SSH wire format

func appendUint32(b []byte, v uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, v)
	return append(b, buf...)
}

func appendString(b []byte, s string) []byte {
	return appendBytes(b, []byte(s))
}

func appendBytes(b []byte, data []byte) []byte {
	b = appendUint32(b, uint32(len(data)))
	return append(b, data...)
}

func readUint32(b []byte) (uint32, []byte, error) {
	if len(b) < 4 {
		return 0, b, fmt.Errorf("buffer too short")
	}
	return binary.BigEndian.Uint32(b[:4]), b[4:], nil
}

func readString(b []byte) (string, []byte, error) {
	data, rest, err := readBytes(b)
	return string(data), rest, err
}

func readBytes(b []byte) ([]byte, []byte, error) {
	if len(b) < 4 {
		return nil, b, fmt.Errorf("buffer too short")
	}
	length := binary.BigEndian.Uint32(b[:4])
	if len(b) < int(4+length) {
		return nil, b, fmt.Errorf("buffer too short for data")
	}
	return b[4 : 4+length], b[4+length:], nil
}

// Ed25519PrivateKeyToX25519 converts an Ed25519 private key (OpenSSH PEM) to
// an X25519 private key (32 bytes). This follows the standard derivation:
// SHA-512 the 32-byte seed, clamp the lower 32 bytes.
func Ed25519PrivateKeyToX25519(privateKeyPEM []byte) ([32]byte, error) {
	privKey, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return [32]byte{}, fmt.Errorf("parse private key: %w", err)
	}
	return Ed25519PrivateKeyBytesToX25519(privKey)
}

// Ed25519PrivateKeyBytesToX25519 converts raw Ed25519 private key bytes to X25519.
func Ed25519PrivateKeyBytesToX25519(privKey ed25519.PrivateKey) ([32]byte, error) {
	if len(privKey) != ed25519.PrivateKeySize {
		return [32]byte{}, fmt.Errorf("invalid private key size: %d", len(privKey))
	}

	// Ed25519 private key is seed (32 bytes) || public key (32 bytes)
	seed := privKey.Seed()
	h := sha512.Sum512(seed)

	// Clamp (standard X25519 scalar clamping)
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64

	var x25519Key [32]byte
	copy(x25519Key[:], h[:32])
	return x25519Key, nil
}

// Ed25519PublicKeyToX25519 converts an Ed25519 public key (OpenSSH format string)
// to an X25519 public key (32 bytes). Uses Edwards→Montgomery point conversion.
func Ed25519PublicKeyToX25519(publicKeySSH []byte) ([32]byte, error) {
	pubKey, err := parsePublicKey(publicKeySSH)
	if err != nil {
		return [32]byte{}, fmt.Errorf("parse public key: %w", err)
	}
	return Ed25519PublicKeyBytesToX25519(pubKey)
}

// Ed25519PublicKeyBytesToX25519 converts raw Ed25519 public key bytes to X25519.
func Ed25519PublicKeyBytesToX25519(pubKey ed25519.PublicKey) ([32]byte, error) {
	if len(pubKey) != ed25519.PublicKeySize {
		return [32]byte{}, fmt.Errorf("invalid public key size: %d", len(pubKey))
	}

	// Use filippo.io/edwards25519 for Edwards→Montgomery conversion
	p, err := new(edwards25519.Point).SetBytes(pubKey)
	if err != nil {
		return [32]byte{}, fmt.Errorf("invalid Edwards point: %w", err)
	}

	// Convert Edwards point to Montgomery u-coordinate
	montgomeryBytes := p.BytesMontgomery()
	var x25519Key [32]byte
	copy(x25519Key[:], montgomeryBytes)
	return x25519Key, nil
}

func splitSSHKey(s string) []string {
	var parts []string
	var current []byte
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' {
			if len(current) > 0 {
				parts = append(parts, string(current))
				current = nil
			}
		} else {
			current = append(current, s[i])
		}
	}
	if len(current) > 0 {
		parts = append(parts, string(current))
	}
	return parts
}
