package signing

import (
	"testing"
)

func TestEd25519ToX25519_PrivateKey(t *testing.T) {
	privPEM, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	x25519SK, err := Ed25519PrivateKeyToX25519(privPEM)
	if err != nil {
		t.Fatalf("Ed25519PrivateKeyToX25519: %v", err)
	}

	// Should be 32 bytes, not all zeros
	allZero := true
	for _, b := range x25519SK {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("X25519 private key should not be all zeros")
	}
}

func TestEd25519ToX25519_PublicKey(t *testing.T) {
	_, pubSSH, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	x25519PK, err := Ed25519PublicKeyToX25519(pubSSH)
	if err != nil {
		t.Fatalf("Ed25519PublicKeyToX25519: %v", err)
	}

	allZero := true
	for _, b := range x25519PK {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("X25519 public key should not be all zeros")
	}
}

func TestEd25519ToX25519_Deterministic(t *testing.T) {
	privPEM, pubSSH, _ := GenerateKeypair()

	sk1, _ := Ed25519PrivateKeyToX25519(privPEM)
	sk2, _ := Ed25519PrivateKeyToX25519(privPEM)
	if sk1 != sk2 {
		t.Error("X25519 private key derivation should be deterministic")
	}

	pk1, _ := Ed25519PublicKeyToX25519(pubSSH)
	pk2, _ := Ed25519PublicKeyToX25519(pubSSH)
	if pk1 != pk2 {
		t.Error("X25519 public key derivation should be deterministic")
	}
}

func TestEd25519ToX25519_DifferentKeys(t *testing.T) {
	privA, pubA, _ := GenerateKeypair()
	privB, pubB, _ := GenerateKeypair()

	skA, _ := Ed25519PrivateKeyToX25519(privA)
	skB, _ := Ed25519PrivateKeyToX25519(privB)
	if skA == skB {
		t.Error("different Ed25519 keys should produce different X25519 keys")
	}

	pkA, _ := Ed25519PublicKeyToX25519(pubA)
	pkB, _ := Ed25519PublicKeyToX25519(pubB)
	if pkA == pkB {
		t.Error("different Ed25519 public keys should produce different X25519 public keys")
	}
}

func TestEd25519PrivateKeyToX25519_InvalidInput(t *testing.T) {
	_, err := Ed25519PrivateKeyToX25519([]byte("not a key"))
	if err == nil {
		t.Error("should fail for invalid PEM")
	}
}

func TestEd25519PublicKeyToX25519_InvalidInput(t *testing.T) {
	_, err := Ed25519PublicKeyToX25519([]byte("not a key"))
	if err == nil {
		t.Error("should fail for invalid public key")
	}
}
