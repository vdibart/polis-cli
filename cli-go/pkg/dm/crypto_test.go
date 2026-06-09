package dm

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestComputeConversationID_Symmetric(t *testing.T) {
	id1 := ComputeConversationID("alice.example.com", "bob.example.com")
	id2 := ComputeConversationID("bob.example.com", "alice.example.com")
	if id1 != id2 {
		t.Errorf("conversation ID should be symmetric: %s != %s", id1, id2)
	}
	if len(id1) != 16 {
		t.Errorf("conversation ID should be 16 hex chars, got %d", len(id1))
	}
}

func TestComputeConversationID_CaseInsensitive(t *testing.T) {
	// This was the exact bug: "Vdibart.polis.pub" vs "vdibart.polis.pub"
	id1 := ComputeConversationID("david.polis.pub", "Vdibart.polis.pub")
	id2 := ComputeConversationID("david.polis.pub", "vdibart.polis.pub")
	if id1 != id2 {
		t.Errorf("conversation ID should be case-insensitive: %s != %s", id1, id2)
	}

	// Mixed case on both sides
	id3 := ComputeConversationID("Alice.Example.COM", "Bob.Example.COM")
	id4 := ComputeConversationID("alice.example.com", "bob.example.com")
	if id3 != id4 {
		t.Errorf("conversation ID should be case-insensitive: %s != %s", id3, id4)
	}
}

func TestComputeConversationID_Unique(t *testing.T) {
	id1 := ComputeConversationID("alice.example.com", "bob.example.com")
	id2 := ComputeConversationID("alice.example.com", "carol.example.com")
	if id1 == id2 {
		t.Error("different peer pairs should produce different conversation IDs")
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	// Generate two keypairs and convert to X25519
	privA, pubA, _ := signing.GenerateKeypair()
	privB, pubB, _ := signing.GenerateKeypair()

	x25519SKA, err := signing.Ed25519PrivateKeyToX25519(privA)
	if err != nil {
		t.Fatalf("convert A private: %v", err)
	}
	x25519PKA, err := signing.Ed25519PublicKeyToX25519(pubA)
	if err != nil {
		t.Fatalf("convert A public: %v", err)
	}
	x25519SKB, err := signing.Ed25519PrivateKeyToX25519(privB)
	if err != nil {
		t.Fatalf("convert B private: %v", err)
	}
	x25519PKB, err := signing.Ed25519PublicKeyToX25519(pubB)
	if err != nil {
		t.Fatalf("convert B public: %v", err)
	}

	// Alice encrypts to Bob
	plaintext := []byte("Hello Bob!")
	ciphertext, nonce, err := Encrypt(plaintext, &x25519PKB, &x25519SKA)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Bob decrypts from Alice
	decrypted, err := Decrypt(ciphertext, &nonce, &x25519PKA, &x25519SKB)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if string(decrypted) != "Hello Bob!" {
		t.Errorf("decrypted content mismatch: %q", string(decrypted))
	}
}

func TestEncryptDecrypt_WrongKey(t *testing.T) {
	privA, _, _ := signing.GenerateKeypair()
	_, pubB, _ := signing.GenerateKeypair()
	privC, _, _ := signing.GenerateKeypair()

	x25519SKA, _ := signing.Ed25519PrivateKeyToX25519(privA)
	x25519PKB, _ := signing.Ed25519PublicKeyToX25519(pubB)
	x25519SKC, _ := signing.Ed25519PrivateKeyToX25519(privC)

	plaintext := []byte("Secret message")
	ciphertext, nonce, _ := Encrypt(plaintext, &x25519PKB, &x25519SKA)

	// Try to decrypt with wrong key (C instead of B)
	_, err := Decrypt(ciphertext, &nonce, &x25519PKB, &x25519SKC)
	if err == nil {
		t.Error("decryption with wrong key should fail")
	}
}

func TestMessageIDFromNonce(t *testing.T) {
	var nonce [24]byte
	copy(nonce[:], []byte("test-nonce-24-bytes!!!!"))

	id := MessageIDFromNonce(nonce)
	if len(id) != 16 {
		t.Errorf("message ID should be 16 hex chars, got %d", len(id))
	}

	// Same nonce should produce same ID
	id2 := MessageIDFromNonce(nonce)
	if id != id2 {
		t.Error("same nonce should produce same message ID")
	}
}
