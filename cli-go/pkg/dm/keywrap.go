package dm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
)

// ErrWrongKey is returned when a wrapped DEK fails to open — i.e. the wrong
// password or recovery phrase. Because the wrap is an AEAD (NaCl secretbox), a
// failed Poly1305 tag *is* the validation signal: there is no separate verifier
// and no server-side guess oracle. Callers surface this as "that password didn't
// unlock" / "that phrase didn't unlock."
var ErrWrongKey = errors.New("dm: wrong password or recovery phrase (AEAD authentication failed)")

const (
	kekLen           = 32 // KEK / DEK size in bytes (NaCl secretbox key, X25519 scalar)
	kdfSaltLen       = 16 // per-epoch KDF salt
	recoveryHKDFInfo = "polis-dm-recovery-kek-v1"

	// Default Argon2id cost for the password KEK. Starting values — task 1.9 revisits
	// them once measured against a phone browser/WASM. Both Go and the browser WASM
	// MUST use the params recorded in keyring.json for a wrapped blob to round-trip
	// across implementations; these are only the defaults for a freshly minted epoch.
	defaultArgon2Time    uint32 = 3
	defaultArgon2Memory  uint32 = 64 * 1024 // KiB (= 64 MiB)
	defaultArgon2Threads uint8  = 4
)

// NewArgon2idKDF returns fresh password-KEK params: algo argon2id, a random salt,
// and the default cost. Stored in the epoch's `kdf`.
func NewArgon2idKDF() (KDFParams, error) {
	salt, err := newKDFSalt()
	if err != nil {
		return KDFParams{}, err
	}
	return KDFParams{
		Algo:    "argon2id",
		Salt:    salt,
		Time:    defaultArgon2Time,
		Memory:  defaultArgon2Memory,
		Threads: defaultArgon2Threads,
	}, nil
}

// NewRecoveryKDF returns fresh recovery-KEK params: algo hkdf-sha256 and a random
// salt. Stored in the epoch's `recovery_kdf`. No cost params — the recovery phrase
// is already high-entropy, so HKDF (fast) suffices.
func NewRecoveryKDF() (KDFParams, error) {
	salt, err := newKDFSalt()
	if err != nil {
		return KDFParams{}, err
	}
	return KDFParams{Algo: "hkdf-sha256", Salt: salt}, nil
}

func newKDFSalt() (string, error) {
	b := make([]byte, kdfSaltLen)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("generate kdf salt: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// DeriveKEKPassword derives the 32-byte password KEK via Argon2id, using the cost
// params + salt recorded in p (which must be argon2id).
func DeriveKEKPassword(password []byte, p KDFParams) ([32]byte, error) {
	var kek [32]byte
	if p.Algo != "argon2id" {
		return kek, fmt.Errorf("password KEK requires argon2id params, got %q", p.Algo)
	}
	salt, err := base64.StdEncoding.DecodeString(p.Salt)
	if err != nil {
		return kek, fmt.Errorf("decode kdf salt: %w", err)
	}
	out := argon2.IDKey(password, salt, p.Time, p.Memory, p.Threads, kekLen)
	copy(kek[:], out)
	return kek, nil
}

// DeriveKEKRecovery derives the 32-byte recovery KEK via HKDF-SHA256 over the
// high-entropy recovery-phrase entropy, using the salt recorded in p (which must be
// hkdf-sha256).
func DeriveKEKRecovery(entropy []byte, p KDFParams) ([32]byte, error) {
	var kek [32]byte
	if p.Algo != "hkdf-sha256" {
		return kek, fmt.Errorf("recovery KEK requires hkdf-sha256 params, got %q", p.Algo)
	}
	salt, err := base64.StdEncoding.DecodeString(p.Salt)
	if err != nil {
		return kek, fmt.Errorf("decode recovery kdf salt: %w", err)
	}
	r := hkdf.New(sha256.New, entropy, salt, []byte(recoveryHKDFInfo))
	if _, err := io.ReadFull(r, kek[:]); err != nil {
		return kek, fmt.Errorf("hkdf derive recovery kek: %w", err)
	}
	return kek, nil
}

// WrapDEK seals the DEK under the KEK with NaCl secretbox, returning
// base64(nonce ‖ ciphertext). The KEK only ever wraps the DEK — it never touches a
// message.
func WrapDEK(dek []byte, kek [32]byte) (string, error) {
	var nonce [24]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return "", fmt.Errorf("generate wrap nonce: %w", err)
	}
	sealed := secretbox.Seal(nonce[:], dek, &nonce, &kek) // output = nonce ‖ ct
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// UnwrapDEK opens a base64(nonce ‖ ciphertext) blob with the KEK. A wrong KEK fails
// the Poly1305 tag and returns ErrWrongKey — this is the password/phrase validation.
func UnwrapDEK(blob string, kek [32]byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, fmt.Errorf("decode wrapped dek: %w", err)
	}
	if len(raw) < 24 {
		return nil, fmt.Errorf("wrapped dek too short (%d bytes)", len(raw))
	}
	var nonce [24]byte
	copy(nonce[:], raw[:24])
	dek, ok := secretbox.Open(nil, raw[24:], &nonce, &kek)
	if !ok {
		return nil, ErrWrongKey
	}
	return dek, nil
}
