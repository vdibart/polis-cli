package dm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/nacl/secretbox"
)

// ComputeConversationID returns a deterministic conversation ID for two domains.
// Both parties compute the same ID: hex(sha256(sorted(domain_a, domain_b)))[:16]
func ComputeConversationID(domainA, domainB string) string {
	domains := []string{domainA, domainB}
	sort.Strings(domains)
	h := sha256.Sum256([]byte(domains[0] + "\n" + domains[1]))
	return hex.EncodeToString(h[:])[:16]
}

// Encrypt encrypts plaintext using NaCl box (X25519 + XSalsa20-Poly1305).
// Returns (ciphertext, nonce, error). The ciphertext includes the Poly1305 MAC.
func Encrypt(plaintext []byte, peerX25519PK, myX25519SK *[32]byte) ([]byte, [24]byte, error) {
	var nonce [24]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, nonce, fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := box.Seal(nil, plaintext, &nonce, peerX25519PK, myX25519SK)
	return ciphertext, nonce, nil
}

// Decrypt decrypts NaCl box ciphertext using the peer's public key and our secret key.
func Decrypt(ciphertext []byte, nonce *[24]byte, peerX25519PK, myX25519SK *[32]byte) ([]byte, error) {
	plaintext, ok := box.Open(nil, ciphertext, nonce, peerX25519PK, myX25519SK)
	if !ok {
		return nil, fmt.Errorf("decryption failed: authentication error")
	}
	return plaintext, nil
}

// DeriveStorageKey derives a symmetric key for at-rest encryption using HKDF-SHA256.
// secret: Ed25519 private key seed (32 bytes)
// salt: site-wide random salt from .polis/storage-salt (32 bytes)
// info: purpose string (e.g., "polis-dm-storage-v1")
func DeriveStorageKey(secret, salt []byte, info string) ([32]byte, error) {
	var key [32]byte
	hkdfReader := hkdf.New(sha256.New, secret, salt, []byte(info))
	if _, err := io.ReadFull(hkdfReader, key[:]); err != nil {
		return key, fmt.Errorf("HKDF derive: %w", err)
	}
	return key, nil
}

// EncryptForStorage encrypts plaintext using NaCl secretbox (XSalsa20-Poly1305)
// with a symmetric key. Returns (ciphertext, nonce, error).
func EncryptForStorage(plaintext []byte, storageKey *[32]byte) ([]byte, [24]byte, error) {
	var nonce [24]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, nonce, fmt.Errorf("generate storage nonce: %w", err)
	}

	ciphertext := secretbox.Seal(nil, plaintext, &nonce, storageKey)
	return ciphertext, nonce, nil
}

// DecryptFromStorage decrypts NaCl secretbox ciphertext using a symmetric key.
func DecryptFromStorage(ciphertext []byte, nonce *[24]byte, storageKey *[32]byte) ([]byte, error) {
	plaintext, ok := secretbox.Open(nil, ciphertext, nonce, storageKey)
	if !ok {
		return nil, fmt.Errorf("storage decryption failed: authentication error")
	}
	return plaintext, nil
}

// MessageIDFromNonce derives a message ID from the transport nonce.
// ID = hex(sha256(nonce))[:16]
func MessageIDFromNonce(nonce [24]byte) string {
	h := sha256.Sum256(nonce[:])
	return hex.EncodeToString(h[:])[:16]
}
