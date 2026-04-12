package signing

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/pem"
	"strings"
	"testing"
)

func TestGenerateKeypair(t *testing.T) {
	privKey, pubKey, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair failed: %v", err)
	}

	// Check private key format
	if !strings.Contains(string(privKey), "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Error("Private key should be in OpenSSH PEM format")
	}
	if !strings.Contains(string(privKey), "-----END OPENSSH PRIVATE KEY-----") {
		t.Error("Private key should have PEM footer")
	}

	// Check public key format
	if !strings.HasPrefix(string(pubKey), "ssh-ed25519 ") {
		t.Error("Public key should start with 'ssh-ed25519 '")
	}
}

func TestGenerateKeypair_Uniqueness(t *testing.T) {
	// Generate two keypairs, they should be different
	priv1, pub1, _ := GenerateKeypair()
	priv2, pub2, _ := GenerateKeypair()

	if string(priv1) == string(priv2) {
		t.Error("Two generated private keys should be different")
	}
	if string(pub1) == string(pub2) {
		t.Error("Two generated public keys should be different")
	}
}

func TestSignContent(t *testing.T) {
	privKey, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair failed: %v", err)
	}

	content := []byte("Hello, World!")
	sig, err := SignContent(content, privKey)
	if err != nil {
		t.Fatalf("SignContent failed: %v", err)
	}

	// Check signature format (SSH signature PEM)
	if !strings.Contains(sig, "-----BEGIN SSH SIGNATURE-----") {
		t.Error("Signature should be in SSH signature PEM format")
	}
	if !strings.Contains(sig, "-----END SSH SIGNATURE-----") {
		t.Error("Signature should have PEM footer")
	}
}

func TestSignContent_Deterministic(t *testing.T) {
	// Ed25519 signatures are deterministic for the same key and content
	privKey, _, _ := GenerateKeypair()
	content := []byte("Test content")

	sig1, _ := SignContent(content, privKey)
	sig2, _ := SignContent(content, privKey)

	if sig1 != sig2 {
		t.Error("Ed25519 signatures should be deterministic")
	}
}

func TestSignContent_DifferentContent(t *testing.T) {
	privKey, _, _ := GenerateKeypair()

	sig1, _ := SignContent([]byte("Content A"), privKey)
	sig2, _ := SignContent([]byte("Content B"), privKey)

	if sig1 == sig2 {
		t.Error("Different content should produce different signatures")
	}
}

func TestVerifySignature(t *testing.T) {
	privKey, pubKey, _ := GenerateKeypair()
	content := []byte("Hello, World!")

	sig, err := SignContent(content, privKey)
	if err != nil {
		t.Fatalf("SignContent failed: %v", err)
	}

	valid, err := VerifySignature(content, pubKey, sig)
	if err != nil {
		t.Fatalf("VerifySignature failed: %v", err)
	}
	if !valid {
		t.Error("Signature should be valid")
	}
}

func TestVerifySignature_TamperedContent(t *testing.T) {
	privKey, pubKey, _ := GenerateKeypair()
	content := []byte("Original content")

	sig, _ := SignContent(content, privKey)

	// Tamper with content
	tamperedContent := []byte("Tampered content")
	valid, err := VerifySignature(tamperedContent, pubKey, sig)
	if err != nil {
		t.Fatalf("VerifySignature failed: %v", err)
	}
	if valid {
		t.Error("Signature should be invalid for tampered content")
	}
}

func TestVerifySignature_WrongKey(t *testing.T) {
	privKey1, _, _ := GenerateKeypair()
	_, pubKey2, _ := GenerateKeypair()

	content := []byte("Hello, World!")
	sig, _ := SignContent(content, privKey1)

	// Verify with different key
	valid, err := VerifySignature(content, pubKey2, sig)
	if err != nil {
		t.Fatalf("VerifySignature failed: %v", err)
	}
	if valid {
		t.Error("Signature should be invalid with wrong public key")
	}
}

func TestRoundTrip(t *testing.T) {
	// Full round-trip: generate keys, sign, verify
	privKey, pubKey, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair failed: %v", err)
	}

	testCases := []string{
		"Simple text",
		"Text with\nnewlines\n",
		"Unicode: 你好世界 🎉",
		"", // Empty content
		strings.Repeat("Long content ", 1000),
	}

	for _, content := range testCases {
		t.Run("content_length_"+string(rune(len(content))), func(t *testing.T) {
			sig, err := SignContent([]byte(content), privKey)
			if err != nil {
				t.Fatalf("SignContent failed: %v", err)
			}

			valid, err := VerifySignature([]byte(content), pubKey, sig)
			if err != nil {
				t.Fatalf("VerifySignature failed: %v", err)
			}
			if !valid {
				t.Error("Round-trip verification failed")
			}
		})
	}
}

func TestParsePrivateKey(t *testing.T) {
	privKeyPEM, _, _ := GenerateKeypair()

	privKey, err := parsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parsePrivateKey failed: %v", err)
	}

	if len(privKey) != ed25519.PrivateKeySize {
		t.Errorf("Private key should be %d bytes, got %d", ed25519.PrivateKeySize, len(privKey))
	}
}

func TestParsePublicKey(t *testing.T) {
	_, pubKeySSH, _ := GenerateKeypair()

	pubKey, err := parsePublicKey(pubKeySSH)
	if err != nil {
		t.Fatalf("parsePublicKey failed: %v", err)
	}

	if len(pubKey) != ed25519.PublicKeySize {
		t.Errorf("Public key should be %d bytes, got %d", ed25519.PublicKeySize, len(pubKey))
	}
}

func TestParsePrivateKey_Invalid(t *testing.T) {
	invalidKeys := [][]byte{
		[]byte("not a PEM"),
		[]byte("-----BEGIN RSA PRIVATE KEY-----\nnotvalid\n-----END RSA PRIVATE KEY-----"),
		[]byte(""),
	}

	for i, invalid := range invalidKeys {
		_, err := parsePrivateKey(invalid)
		if err == nil {
			t.Errorf("Case %d: parsePrivateKey should fail for invalid input", i)
		}
	}
}

func TestParsePublicKey_Invalid(t *testing.T) {
	invalidKeys := [][]byte{
		[]byte("not a public key"),
		[]byte("ssh-rsa AAAA..."),
		[]byte(""),
	}

	for i, invalid := range invalidKeys {
		_, err := parsePublicKey(invalid)
		if err == nil {
			t.Errorf("Case %d: parsePublicKey should fail for invalid input", i)
		}
	}
}

func TestParseSSHSignature_Malformed(t *testing.T) {
	tests := []struct {
		name string
		sig  string
	}{
		{"not PEM", "not a signature"},
		{"wrong PEM type", "-----BEGIN CERTIFICATE-----\nYWJj\n-----END CERTIFICATE-----"},
		{"empty PEM body", "-----BEGIN SSH SIGNATURE-----\n-----END SSH SIGNATURE-----"},
		{"wrong magic", "-----BEGIN SSH SIGNATURE-----\nAAAA\n-----END SSH SIGNATURE-----"},
		{"truncated after magic", func() string {
			// SSHSIG magic (6 bytes) but nothing else
			data := []byte("SSHSIG")
			block := &pem.Block{Type: "SSH SIGNATURE", Bytes: data}
			return string(pem.EncodeToMemory(block))
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSSHSignature(tt.sig)
			if err == nil {
				t.Error("parseSSHSignature should return error for malformed input")
			}
		})
	}
}

// TestBuildSigningBlob verifies that the signing blob matches the SSH signature format
// expected by ssh-keygen -Y verify. This is critical for compatibility with the validator.
func TestBuildSigningBlob(t *testing.T) {
	content := []byte("Test content for signing")
	blob := buildSigningBlob(content)

	// The blob should have the following structure:
	// 1. "SSHSIG" (6 bytes literal, not length-prefixed)
	// 2. namespace as SSH string (4-byte length + "file")
	// 3. reserved as SSH string (4-byte length + empty)
	// 4. hash_algorithm as SSH string (4-byte length + "sha512")
	// 5. hash_value as SSH string (4-byte length + 64-byte SHA-512 hash)

	// Verify magic preamble (6 bytes)
	if !bytes.HasPrefix(blob, []byte("SSHSIG")) {
		t.Error("Signing blob should start with 'SSHSIG' magic")
	}

	offset := 6 // After "SSHSIG"

	// Verify namespace (length-prefixed "file")
	// 4 bytes for length (0x00000004) + 4 bytes for "file"
	expectedNamespace := []byte{0, 0, 0, 4, 'f', 'i', 'l', 'e'}
	if !bytes.Equal(blob[offset:offset+8], expectedNamespace) {
		t.Errorf("Expected namespace 'file', got %v", blob[offset:offset+8])
	}
	offset += 8

	// Verify reserved (length-prefixed empty string)
	// 4 bytes for length (0x00000000)
	expectedReserved := []byte{0, 0, 0, 0}
	if !bytes.Equal(blob[offset:offset+4], expectedReserved) {
		t.Errorf("Expected empty reserved, got %v", blob[offset:offset+4])
	}
	offset += 4

	// Verify hash algorithm (length-prefixed "sha512")
	// 4 bytes for length (0x00000006) + 6 bytes for "sha512"
	expectedHashAlgo := []byte{0, 0, 0, 6, 's', 'h', 'a', '5', '1', '2'}
	if !bytes.Equal(blob[offset:offset+10], expectedHashAlgo) {
		t.Errorf("Expected hash algorithm 'sha512', got %v", blob[offset:offset+10])
	}
	offset += 10

	// Verify hash value (length-prefixed 64-byte SHA-512 hash)
	// 4 bytes for length (0x00000040 = 64) + 64 bytes for hash
	if blob[offset] != 0 || blob[offset+1] != 0 || blob[offset+2] != 0 || blob[offset+3] != 64 {
		t.Errorf("Expected hash length 64, got %v", blob[offset:offset+4])
	}
	offset += 4

	// Verify the hash value is correct
	expectedHash := sha512.Sum512(content)
	if !bytes.Equal(blob[offset:offset+64], expectedHash[:]) {
		t.Error("Hash value in signing blob doesn't match SHA-512 of content")
	}

	// Verify total length
	expectedLen := 6 + 8 + 4 + 10 + 4 + 64 // = 96 bytes
	if len(blob) != expectedLen {
		t.Errorf("Expected signing blob length %d, got %d", expectedLen, len(blob))
	}
}

// TestSigningBlobDifferentContent verifies different content produces different blobs
func TestSigningBlobDifferentContent(t *testing.T) {
	blob1 := buildSigningBlob([]byte("Content A"))
	blob2 := buildSigningBlob([]byte("Content B"))

	if bytes.Equal(blob1, blob2) {
		t.Error("Different content should produce different signing blobs")
	}

	// But the prefix (magic + namespace + reserved + hash_algo) should be the same
	// Prefix length: 6 + 8 + 4 + 10 = 28 bytes
	if !bytes.Equal(blob1[:28], blob2[:28]) {
		t.Error("Signing blob prefix should be the same for different content")
	}
}

// Benchmark signing performance
func BenchmarkSignContent(b *testing.B) {
	privKey, _, _ := GenerateKeypair()
	content := []byte("Benchmark content for signing performance test")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SignContent(content, privKey)
	}
}

func TestZeroKey(t *testing.T) {
	key := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	ZeroKey(key)
	for i, b := range key {
		if b != 0 {
			t.Errorf("byte %d not zeroed: got %d", i, b)
		}
	}
}

// Benchmark verification performance
func BenchmarkVerifySignature(b *testing.B) {
	privKey, pubKey, _ := GenerateKeypair()
	content := []byte("Benchmark content for verification performance test")
	sig, _ := SignContent(content, privKey)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		VerifySignature(content, pubKey, sig)
	}
}
