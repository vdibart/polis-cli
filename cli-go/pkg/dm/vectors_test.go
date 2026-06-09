package dm

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/nacl/secretbox"
)

// Cross-implementation key-derivation/wrap vectors. These are DETERMINISTIC (fixed
// salts/nonces) so any second implementation — the browser WASM (phase 4), a reference
// decryptor (phase F3) — must reproduce the exact same bytes. The golden file is
// generated on first run and then committed; subsequent runs compare against it, so a
// param/encoding change that would break cross-impl interop fails the test.
//
// Regenerate intentionally by deleting testdata/cross-impl-vectors.json.

const vectorsPath = "testdata/cross-impl-vectors.json"

// Fixed inputs (public test values).
const (
	vecPassword   = "correct horse battery staple"
	vecArgonSalt  = "AAECAwQFBgcICQoLDA0ODw==" // bytes 0x00..0x0f
	vecArgonT     = 3
	vecArgonM     = 65536 // KiB
	vecArgonP     = 4
	vecRecEntropy = "000102030405060708090a0b0c0d0e0f"
	vecRecSalt    = "EBESExQVFhcYGRobHB0eHw==" // bytes 0x10..0x1f
	// BIP39 standard 128-bit all-zero vector.
	vecBIP39Phrase  = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	vecBIP39Entropy = "00000000000000000000000000000000"
	vecDEK          = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	vecWrapNonce    = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYX" // 24 bytes 0x00..0x17
)

type crossImplVectors struct {
	Argon2idKEKHex  string `json:"argon2id_kek_hex"`
	RecoveryKEKHex  string `json:"recovery_kek_hex"`
	BIP39EntropyHex string `json:"bip39_entropy_hex"`
	WrappedDEKB64   string `json:"wrapped_dek_b64"`
}

func computeVectors(t *testing.T) crossImplVectors {
	t.Helper()

	pwKEK, err := DeriveKEKPassword([]byte(vecPassword), KDFParams{Algo: "argon2id", Salt: vecArgonSalt, Time: vecArgonT, Memory: vecArgonM, Threads: vecArgonP})
	if err != nil {
		t.Fatal(err)
	}
	recEntropy, _ := hex.DecodeString(vecRecEntropy)
	recKEK, err := DeriveKEKRecovery(recEntropy, KDFParams{Algo: "hkdf-sha256", Salt: vecRecSalt})
	if err != nil {
		t.Fatal(err)
	}
	bip39Entropy, err := PhraseToEntropy(vecBIP39Phrase)
	if err != nil {
		t.Fatal(err)
	}
	// Deterministic wrapped DEK: fixed nonce + the password KEK.
	dek, _ := hex.DecodeString(vecDEK)
	nb, _ := base64.StdEncoding.DecodeString(vecWrapNonce)
	var nonce [24]byte
	copy(nonce[:], nb)
	wrapped := secretbox.Seal(nonce[:], dek, &nonce, &pwKEK)

	return crossImplVectors{
		Argon2idKEKHex:  hex.EncodeToString(pwKEK[:]),
		RecoveryKEKHex:  hex.EncodeToString(recKEK[:]),
		BIP39EntropyHex: hex.EncodeToString(bip39Entropy),
		WrappedDEKB64:   base64.StdEncoding.EncodeToString(wrapped),
	}
}

func TestCrossImplVectors(t *testing.T) {
	got := computeVectors(t)

	// Sanity: the BIP39 vector is the documented all-zero entropy.
	if got.BIP39EntropyHex != vecBIP39Entropy {
		t.Errorf("BIP39 entropy = %s, want %s", got.BIP39EntropyHex, vecBIP39Entropy)
	}
	// Sanity: the wrapped DEK unwraps to the input DEK with the password KEK.
	pwKEK, _ := DeriveKEKPassword([]byte(vecPassword), KDFParams{Algo: "argon2id", Salt: vecArgonSalt, Time: vecArgonT, Memory: vecArgonM, Threads: vecArgonP})
	unwrapped, err := UnwrapDEK(got.WrappedDEKB64, pwKEK)
	if err != nil {
		t.Fatalf("wrapped vector must unwrap: %v", err)
	}
	if dek, _ := hex.DecodeString(vecDEK); !bytes.Equal(unwrapped, dek) {
		t.Errorf("unwrapped DEK mismatch")
	}

	// Golden compare / generate-on-miss.
	data, err := os.ReadFile(vectorsPath)
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(filepath.Dir(vectorsPath), 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}
		out, _ := json.MarshalIndent(got, "", "  ")
		if wErr := os.WriteFile(vectorsPath, append(out, '\n'), 0o644); wErr != nil {
			t.Fatal(wErr)
		}
		t.Logf("generated %s — commit it; re-run to verify", vectorsPath)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var want crossImplVectors
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("cross-impl vectors drifted from golden %s.\n got:  %+v\n want: %+v\n(delete the file to regenerate intentionally)", vectorsPath, got, want)
	}
}
