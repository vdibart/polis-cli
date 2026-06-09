package signing

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
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

// TestParsePrivateKey_DoesNotAliasPEMBuffer is a regression test for R16-13.
// The returned PrivateKey must be a copy of the bytes from the PEM block,
// not a slice aliasing into it. Otherwise ZeroKey on the returned key (e.g.,
// SignContent's defer) zeroes bytes inside the caller's PEM buffer, which:
//
//	(a) defeats R11-7's separate ZeroKey-of-the-PEM-buffer path, and
//	(b) breaks idempotent re-sign with the same buffer.
func TestParsePrivateKey_DoesNotAliasPEMBuffer(t *testing.T) {
	privKeyPEM, _, _ := GenerateKeypair()
	pemSnapshot := append([]byte(nil), privKeyPEM...)

	privKey, err := parsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parsePrivateKey failed: %v", err)
	}

	// Zero the parsed key — should not affect the caller's PEM buffer.
	ZeroKey(privKey)

	if !bytes.Equal(privKeyPEM, pemSnapshot) {
		t.Error("ZeroKey on the parsed key mutated the original PEM buffer — parsePrivateKey must copy, not alias")
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

func TestSignContent_EmbeddedPublicKey(t *testing.T) {
	privKeyPEM, pubKeySSH, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair failed: %v", err)
	}
	content := []byte("test content for embedded pubkey check")

	sig, err := SignContent(content, privKeyPEM)
	if err != nil {
		t.Fatalf("SignContent failed: %v", err)
	}

	// Parse the SSH signature PEM to extract the embedded public key
	block, _ := pem.Decode([]byte(sig))
	if block == nil {
		t.Fatal("failed to decode signature PEM")
	}
	data := block.Bytes
	// Skip magic "SSHSIG" (6 bytes)
	data = data[6:]
	// Skip version (uint32)
	_, data, _ = readUint32(data)
	// Read public key blob
	pubBlob, _, _ := readBytes(data)
	// Inside the blob: key type string + raw key bytes
	_, pubBlob, _ = readString(pubBlob)
	keyBytes, _, _ := readBytes(pubBlob)

	// Parse the expected public key
	expectedPub, err := parsePublicKey(pubKeySSH)
	if err != nil {
		t.Fatalf("parsePublicKey failed: %v", err)
	}

	if !bytes.Equal(keyBytes, expectedPub) {
		t.Errorf("embedded public key mismatch:\n  got  %x\n  want %x", keyBytes, expectedPub)
	}

	// Verify it's not all zeros (the bug this test guards against)
	allZero := true
	for _, b := range keyBytes {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("embedded public key is all zeros — ZeroKey called before extracting pubkey")
	}
}

// parsedSSHSIG holds the fields of an SSHSIG blob needed to verify it the way
// standard tooling (ssh-keygen -Y verify) does: against the EMBEDDED public key.
type parsedSSHSIG struct {
	embeddedKey ed25519.PublicKey
	namespace   string
	hashAlg     string
	rawSig      []byte
}

// parseSSHSIGForTest decodes a PEM-armored SSH SIGNATURE into its fields. Unlike
// the production parseSSHSignature (which discards the embedded key and verifies
// against an externally-supplied key — deliberately lenient), this exposes the
// embedded key so tests can assert strict, standard-SSHSIG verifiability.
func parseSSHSIGForTest(t *testing.T, sig string) parsedSSHSIG {
	t.Helper()
	block, _ := pem.Decode([]byte(sig))
	if block == nil {
		t.Fatal("failed to decode signature PEM")
	}
	data := block.Bytes
	if len(data) < 6 || string(data[:6]) != sshSigMagic {
		t.Fatalf("bad SSHSIG magic")
	}
	data = data[6:]
	var (
		err     error
		pubBlob []byte
		ns      string
		halg    string
		sigBlob []byte
	)
	if _, data, err = readUint32(data); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if pubBlob, data, err = readBytes(data); err != nil {
		t.Fatalf("read pubkey blob: %v", err)
	}
	if ns, data, err = readString(data); err != nil {
		t.Fatalf("read namespace: %v", err)
	}
	if _, data, err = readString(data); err != nil { // reserved
		t.Fatalf("read reserved: %v", err)
	}
	if halg, data, err = readString(data); err != nil {
		t.Fatalf("read hash algorithm: %v", err)
	}
	if sigBlob, _, err = readBytes(data); err != nil {
		t.Fatalf("read signature blob: %v", err)
	}
	// pubBlob: string(keytype) + bytes(key)
	if _, pubBlob, err = readString(pubBlob); err != nil {
		t.Fatalf("read pubkey type: %v", err)
	}
	keyBytes, _, err := readBytes(pubBlob)
	if err != nil {
		t.Fatalf("read pubkey bytes: %v", err)
	}
	// sigBlob: string(keytype) + bytes(rawsig)
	if _, sigBlob, err = readString(sigBlob); err != nil {
		t.Fatalf("read sig type: %v", err)
	}
	rawSig, _, err := readBytes(sigBlob)
	if err != nil {
		t.Fatalf("read raw signature: %v", err)
	}
	return parsedSSHSIG{ed25519.PublicKey(keyBytes), ns, halg, rawSig}
}

// TestSignContent_VerifiesAgainstEmbeddedKey is the strict guard: a signature
// must verify against ITS OWN embedded public key, which is exactly what
// ssh-keygen -Y verify and any third party do. The production VerifySignature
// is intentionally lenient (it verifies against the real key fetched from
// .well-known/polis and ignores the embedded key), so it — and Judge/Patrol —
// cannot see a malformed envelope. This test catches ANY envelope corruption
// (the 0.59.0 all-zeros bug, or a wrong/garbled embedded key), generalizing
// TestSignContent_EmbeddedPublicKey beyond the all-zeros case.
func TestSignContent_VerifiesAgainstEmbeddedKey(t *testing.T) {
	privKeyPEM, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair failed: %v", err)
	}
	content := []byte("strict embedded-key verification\nsecond line\n")

	sig, err := SignContent(content, privKeyPEM)
	if err != nil {
		t.Fatalf("SignContent failed: %v", err)
	}

	p := parseSSHSIGForTest(t, sig)

	if p.namespace != sshNamespace {
		t.Errorf("namespace = %q, want %q", p.namespace, sshNamespace)
	}
	if p.hashAlg != hashAlgorithm {
		t.Errorf("hash algorithm = %q, want %q", p.hashAlg, hashAlgorithm)
	}

	// Standard SSHSIG semantics: rebuild the signing blob and verify the raw
	// signature against the EMBEDDED key. A malformed envelope fails here even
	// though the lenient VerifySignature (against the real key) would pass.
	blob := buildSigningBlob(content)
	if !ed25519.Verify(p.embeddedKey, blob, p.rawSig) {
		t.Fatal("signature does not verify against its own embedded public key — " +
			"the SSHSIG envelope is malformed (this is the property 0.59.0 broke " +
			"and that standard ssh-keygen verification enforces)")
	}
}

// TestSignContent_SSHKeygenInterop is the true independent oracle: it verifies a
// freshly-signed signature with the real `ssh-keygen -Y verify` binary, not our
// own Go code. If ssh-keygen rejects it, our SSHSIG envelope is not standard-
// interoperable. Skipped when ssh-keygen is unavailable so the suite stays
// hermetic; CI should run it (and a corpus-wide variant) as the canary that
// would have caught 0.59.0 — see the envelope-remediation plan.
func TestSignContent_SSHKeygenInterop(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available; skipping standard-tooling interop oracle")
	}

	privKeyPEM, pubKeySSH, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair failed: %v", err)
	}
	content := []byte("ssh-keygen interop content\nwith multiple lines\n")

	sig, err := SignContent(content, privKeyPEM)
	if err != nil {
		t.Fatalf("SignContent failed: %v", err)
	}

	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed_signers")
	// allowed_signers line: <principal> <keytype> <keydata> [comment]
	if err := os.WriteFile(allowed, []byte("polis "+strings.TrimSpace(string(pubKeySSH))+"\n"), 0o600); err != nil {
		t.Fatalf("write allowed_signers: %v", err)
	}
	sigFile := filepath.Join(dir, "content.sig")
	if err := os.WriteFile(sigFile, []byte(sig), 0o600); err != nil {
		t.Fatalf("write sig file: %v", err)
	}

	cmd := exec.Command(keygen, "-Y", "verify",
		"-f", allowed, "-I", "polis", "-n", sshNamespace, "-s", sigFile)
	cmd.Stdin = bytes.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen -Y verify rejected a freshly-signed signature "+
			"(SSHSIG envelope is not standard-interoperable): %v\n%s", err, out)
	}
}

func TestParsePrivateKey_Truncated(t *testing.T) {
	privKeyPEM, _, _ := GenerateKeypair()
	block, _ := pem.Decode(privKeyPEM)
	magic := "openssh-key-v1\x00"

	tests := []struct {
		name    string
		cutAt   int
		wantErr string
	}{
		{"truncated at cipher", len(magic) + 2, "parse private key cipher"},
		{"truncated at kdf", len(magic) + 10, "parse private key kdf"},
		{"empty after magic", len(magic), "parse private key cipher"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncated := block.Bytes[:tt.cutAt]
			pemBlock := &pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: truncated}
			_, err := parsePrivateKey(pem.EncodeToMemory(pemBlock))
			if err == nil {
				t.Fatal("expected error for truncated key")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErr)
			}
		})
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
