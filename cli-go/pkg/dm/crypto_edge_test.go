package dm

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Edge-case tests for crypto.go. The existing crypto_test.go covers
// the happy paths (round-trip, wrong-key rejection, determinism).
// These tests pin down behavior the integrity of the protocol depends
// on: tampered ciphertexts must fail, nonces must affect output, the
// key derivation must spread across all three inputs, and the message
// ID must be collision-resistant within a session.

// ============================================================================
// Encrypt / Decrypt — tampering and nonce sensitivity
// ============================================================================

// Build a fresh keypair triple — used by the tamper tests below.
func makeKeyTriple(t *testing.T) (skA, pkA, skB, pkB [32]byte) {
	t.Helper()
	privA, pubA, _ := signing.GenerateKeypair()
	privB, pubB, _ := signing.GenerateKeypair()
	skA, _ = signing.Ed25519PrivateKeyToX25519(privA)
	pkA, _ = signing.Ed25519PublicKeyToX25519(pubA)
	skB, _ = signing.Ed25519PrivateKeyToX25519(privB)
	pkB, _ = signing.Ed25519PublicKeyToX25519(pubB)
	return
}

// Flipping a single byte in the ciphertext must fail decryption.
// The Poly1305 MAC inside the box catches this; if a refactor ever
// switched to an unauthenticated cipher, this test breaks first.
func TestEncryptDecrypt_RejectsTamperedCiphertext(t *testing.T) {
	skA, pkA, skB, pkB := makeKeyTriple(t)
	ct, nonce, err := Encrypt([]byte("integrity matters"), &pkB, &skA)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Flip a bit in the middle of the ciphertext
	tampered := make([]byte, len(ct))
	copy(tampered, ct)
	tampered[len(tampered)/2] ^= 0x01

	if _, err := Decrypt(tampered, &nonce, &pkA, &skB); err == nil {
		t.Fatal("decryption of tampered ciphertext should fail")
	}
}

// A modified nonce must fail to decrypt — XSalsa20 is a stream cipher,
// so the wrong nonce produces a junk plaintext that fails the Poly1305
// authentication check.
func TestEncryptDecrypt_RejectsWrongNonce(t *testing.T) {
	skA, pkA, skB, pkB := makeKeyTriple(t)
	ct, nonce, _ := Encrypt([]byte("nonce-bound"), &pkB, &skA)

	var wrongNonce [24]byte
	copy(wrongNonce[:], nonce[:])
	wrongNonce[0] ^= 0xFF // any bit change in the nonce

	if _, err := Decrypt(ct, &wrongNonce, &pkA, &skB); err == nil {
		t.Fatal("decryption with wrong nonce should fail")
	}
}

// Two encryptions of the same plaintext under the same key pair must
// produce different ciphertexts — because Encrypt generates a fresh
// random nonce each time. If a future refactor cached the nonce or
// pulled it from a deterministic source, encryption would lose
// semantic security.
func TestEncrypt_NonDeterministicAcrossCalls(t *testing.T) {
	skA, _, _, pkB := makeKeyTriple(t)
	pt := []byte("same plaintext")

	ct1, n1, _ := Encrypt(pt, &pkB, &skA)
	ct2, n2, _ := Encrypt(pt, &pkB, &skA)

	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of the same plaintext under the same key produced identical ciphertexts — nonce reuse")
	}
	if n1 == n2 {
		t.Error("two encryption calls produced identical nonces — RNG broken?")
	}
}

// Empty plaintext is a valid corner case (e.g., DM "typing started"
// signal in a future protocol). Round trip must work.
func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	skA, pkA, skB, pkB := makeKeyTriple(t)
	ct, nonce, err := Encrypt(nil, &pkB, &skA)
	if err != nil {
		t.Fatalf("encrypt empty: %v", err)
	}
	pt, err := Decrypt(ct, &nonce, &pkA, &skB)
	if err != nil {
		t.Fatalf("decrypt empty: %v", err)
	}
	if len(pt) != 0 {
		t.Errorf("decrypted empty should be empty, got %d bytes", len(pt))
	}
}

// A 1MB plaintext must round-trip — sanity check that the API
// doesn't impose surprise size limits.
func TestEncryptDecrypt_LargePlaintext(t *testing.T) {
	skA, pkA, skB, pkB := makeKeyTriple(t)
	pt := make([]byte, 1<<20)
	if _, err := rand.Read(pt); err != nil {
		t.Fatalf("rand: %v", err)
	}

	ct, nonce, err := Encrypt(pt, &pkB, &skA)
	if err != nil {
		t.Fatalf("encrypt 1MB: %v", err)
	}
	got, err := Decrypt(ct, &nonce, &pkA, &skB)
	if err != nil {
		t.Fatalf("decrypt 1MB: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Error("1MB plaintext did not survive round-trip")
	}
}

// ============================================================================
// DeriveStorageKey — spread across inputs
// ============================================================================

// The existing tests cover different-info-produces-different-key.
// These pin down the same property for the *secret* and *salt*
// inputs, so a future refactor that accidentally drops one of them
// from the HKDF input is caught.

func TestDeriveStorageKey_DifferentSecret(t *testing.T) {
	salt := []byte("salt-32bytes-aaaaaaaaaaaaaaaaaaa")
	k1, _ := DeriveStorageKey([]byte("secret-a-32bytes-padding-pad!!"), salt, "info")
	k2, _ := DeriveStorageKey([]byte("secret-b-32bytes-padding-pad!!"), salt, "info")
	if k1 == k2 {
		t.Error("different secrets must produce different keys")
	}
}

func TestDeriveStorageKey_DifferentSalt(t *testing.T) {
	secret := []byte("secret-32bytes-aaaaaaaaaaaaaaa!")
	k1, _ := DeriveStorageKey(secret, []byte("salt-a-32bytes-paddinggggggggg!"), "info")
	k2, _ := DeriveStorageKey(secret, []byte("salt-b-32bytes-paddinggggggggg!"), "info")
	if k1 == k2 {
		t.Error("different salts must produce different keys")
	}
}

// HKDF accepts empty inputs without erroring — pin this down so
// callers can rely on it (e.g., first-run before salt is generated).
// Note: this is descriptive, not prescriptive — if callers should be
// rejected on empty input, that decision belongs at the call site.
func TestDeriveStorageKey_AcceptsEmptyInputs(t *testing.T) {
	if _, err := DeriveStorageKey(nil, nil, ""); err != nil {
		t.Errorf("HKDF should not error on empty inputs: %v", err)
	}
}

// ============================================================================
// EncryptForStorage — tampering
// ============================================================================

func TestStorageEncryption_RejectsTamperedCiphertext(t *testing.T) {
	key, _ := DeriveStorageKey([]byte("secret"), []byte("salt"), "info")
	ct, nonce, _ := EncryptForStorage([]byte("on disk"), &key)

	tampered := make([]byte, len(ct))
	copy(tampered, ct)
	tampered[len(tampered)/2] ^= 0x01

	if _, err := DecryptFromStorage(tampered, &nonce, &key); err == nil {
		t.Fatal("storage decryption of tampered ciphertext should fail")
	}
}

func TestStorageEncryption_RejectsWrongNonce(t *testing.T) {
	key, _ := DeriveStorageKey([]byte("secret"), []byte("salt"), "info")
	ct, nonce, _ := EncryptForStorage([]byte("nonce-bound on disk"), &key)

	var wrongNonce [24]byte
	copy(wrongNonce[:], nonce[:])
	wrongNonce[0] ^= 0xFF

	if _, err := DecryptFromStorage(ct, &wrongNonce, &key); err == nil {
		t.Fatal("storage decryption with wrong nonce should fail")
	}
}

func TestStorageEncryption_NonDeterministicAcrossCalls(t *testing.T) {
	key, _ := DeriveStorageKey([]byte("secret"), []byte("salt"), "info")
	pt := []byte("same data")
	ct1, n1, _ := EncryptForStorage(pt, &key)
	ct2, n2, _ := EncryptForStorage(pt, &key)
	if bytes.Equal(ct1, ct2) {
		t.Error("two storage encryptions of same plaintext produced identical ciphertexts")
	}
	if n1 == n2 {
		t.Error("two storage encryption calls produced identical nonces")
	}
}

// ============================================================================
// MessageIDFromNonce — collision resistance within session
// ============================================================================

// Different nonces must produce different message IDs (16 hex chars
// from sha256 truncation — 64 bits of entropy, well clear of
// collisions at any realistic conversation volume).
func TestMessageIDFromNonce_DifferentNoncesProduceDifferentIDs(t *testing.T) {
	var n1, n2 [24]byte
	copy(n1[:], []byte("aaaaaaaaaaaaaaaaaaaaaaaa"))
	copy(n2[:], []byte("bbbbbbbbbbbbbbbbbbbbbbbb"))

	id1 := MessageIDFromNonce(n1)
	id2 := MessageIDFromNonce(n2)
	if id1 == id2 {
		t.Errorf("different nonces produced same ID: %s", id1)
	}
}

// Single-bit nonce changes should still produce different IDs (sha256
// avalanche). Catches a refactor that accidentally truncates BEFORE
// hashing.
func TestMessageIDFromNonce_SingleBitFlipChangesID(t *testing.T) {
	var n1, n2 [24]byte
	if _, err := rand.Read(n1[:]); err != nil {
		t.Fatal(err)
	}
	n2 = n1
	n2[0] ^= 0x01

	if MessageIDFromNonce(n1) == MessageIDFromNonce(n2) {
		t.Error("one-bit nonce change should change the message ID")
	}
}

// ============================================================================
// ComputeConversationID — domain-input edge cases
// ============================================================================

// Whitespace is not normalized — this is descriptive, not prescriptive.
// If callers should trim whitespace before computing, that's a caller
// concern. Pin the current behavior so a future "helpful" normalization
// doesn't silently change conversation IDs for existing tenants.
func TestComputeConversationID_DoesNotTrimWhitespace(t *testing.T) {
	clean := ComputeConversationID("alice.example", "bob.example")
	padded := ComputeConversationID("alice.example", " bob.example ")
	if clean == padded {
		t.Error("whitespace in inputs is currently NOT trimmed — if this changed, existing conversation IDs would shift")
	}
}

// Distinct strings that differ ONLY in case must produce the same ID
// (DNS is case-insensitive per RFC 1123 — already tested in
// crypto_test.go for one pair; this exhausts more positions).
func TestComputeConversationID_CaseInsensitive_AllPositions(t *testing.T) {
	want := ComputeConversationID("alice.example.com", "bob.example.com")
	variants := []struct {
		a, b string
	}{
		{"ALICE.example.com", "bob.example.com"},
		{"alice.EXAMPLE.com", "bob.example.com"},
		{"alice.example.COM", "bob.example.com"},
		{"alice.example.com", "BOB.example.com"},
		{"alice.example.com", "bob.EXAMPLE.com"},
		{"AlIcE.eXaMpLe.CoM", "bOb.ExAmPlE.cOm"},
	}
	for _, v := range variants {
		if got := ComputeConversationID(v.a, v.b); got != want {
			t.Errorf("case variant (%q,%q) produced %s, want %s", v.a, v.b, got, want)
		}
	}
}

// Empty-string domain is a degenerate case but should still be
// deterministic. Pin the behavior; callers should validate before
// reaching this function but the function itself doesn't panic.
func TestComputeConversationID_EmptyDomainDeterministic(t *testing.T) {
	id := ComputeConversationID("", "alice.example.com")
	if len(id) != 16 || !isHex(id) {
		t.Errorf("expected 16 hex chars even for empty domain, got %q", id)
	}
	again := ComputeConversationID("", "alice.example.com")
	if id != again {
		t.Error("empty-domain conversation ID should be deterministic")
	}
}

func isHex(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
