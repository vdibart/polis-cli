package dm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
)

// Version is the CLI version, set by cmd/root.go at startup (the project-wide
// version-propagation convention; wiring is task 1.10). GetGenerator stamps it
// into keyring.json so written files record which build produced them.
var Version = "dev"

// GetGenerator returns the generator string written into DM metadata files.
func GetGenerator() string { return "polis-cli-go/" + Version }

const (
	// keyringFile is the on-disk keyring, under the tenant DM dir
	// (.polis/bundles/pub.polis.core/dm).
	keyringFile = "keyring.json"

	// KeyringSchemaVersion is the current keyring.json format version. Medic/Tailor
	// use it as the heal-idempotency marker; a keyring below this is upgraded.
	KeyringSchemaVersion = 1

	// Epoch kinds.
	EpochKindBootstrap = "bootstrap" // epoch 0: server-held key, no password
	EpochKindPassword  = "password"  // epoch 1+: password-wrapped DEK
)

// KDFParams records how a wrapping key (KEK) was derived, so an unwrap can
// reproduce it. Argon2id is used for the password KEK; HKDF-SHA256 for the
// high-entropy recovery-phrase KEK (no slow KDF needed there). The Argon2id cost
// fields are omitted for hkdf-sha256.
type KDFParams struct {
	Algo    string `json:"algo"` // "argon2id" | "hkdf-sha256"
	Salt    string `json:"salt"` // base64 (std encoding)
	Time    uint32 `json:"t,omitempty"`
	Memory  uint32 `json:"m,omitempty"`
	Threads uint8  `json:"p,omitempty"`
}

// Epoch is one generation of DM key material. Epoch 0 is the bootstrap epoch
// (server-held, no password); epochs 1+ are password epochs whose DEK is stored
// twice — wrapped under the password KEK (wrapped_dek) and under the
// recovery-phrase KEK (wrapped_dek_recovery). Either unwraps the same DEK.
type Epoch struct {
	ID                 int        `json:"id"`
	Kind               string     `json:"kind"`
	PublicKeyMessages  string     `json:"public_key_messages"`            // base64 x25519 pub
	ServerHeld         bool       `json:"server_held"`                    // true only for the bootstrap epoch
	ServerDEK          string     `json:"server_dek,omitempty"`           // base64 X25519 private DEK, plaintext, bootstrap epoch only — operator-readable by design; present during the forwarding window, then cleared (see FORMAT.md)
	WrappedDEK         string     `json:"wrapped_dek,omitempty"`          // base64 secretbox(nonce||ct)
	WrappedDEKRecovery string     `json:"wrapped_dek_recovery,omitempty"` // base64 secretbox(nonce||ct)
	KDF                *KDFParams `json:"kdf,omitempty"`                  // password KEK params
	RecoveryKDF        *KDFParams `json:"recovery_kdf,omitempty"`         // recovery-phrase KEK params
	CreatedAt          string     `json:"created_at"`                     // RFC3339
}

// Keyring is the per-user DM key material: an ordered list of epochs plus the
// pointer to the current one. The wrapped DEKs never leave it; the password and
// recovery phrase themselves are never stored.
type Keyring struct {
	SchemaVersion int     `json:"schema_version"`
	Revision      int     `json:"revision"` // monotonic; bumped on every mutation (CAS — task 1.5)
	Generator     string  `json:"generator,omitempty"`
	Epochs        []Epoch `json:"epochs"`
	Current       int     `json:"current"`
}

// keyringPath returns the keyring.json path for a tenant DM dir.
func keyringPath(dmDir string) string { return filepath.Join(dmDir, keyringFile) }

// LoadKeyring reads keyring.json from a tenant DM dir. A missing file returns an
// error satisfying os.IsNotExist, so callers can distinguish "not provisioned
// yet" from a real read/parse failure. Unknown JSON fields are tolerated
// (forward-compatibility for schema growth).
func LoadKeyring(dmDir string) (*Keyring, error) {
	data, err := os.ReadFile(keyringPath(dmDir))
	if err != nil {
		return nil, err // *os.PathError; os.IsNotExist-friendly
	}
	var k Keyring
	if err := json.Unmarshal(data, &k); err != nil {
		return nil, fmt.Errorf("parse keyring.json: %w", err)
	}
	return &k, nil
}

// Save writes the keyring atomically with 0600 perms, stamping the schema version
// and generator. Callers that mutate the keyring should bump Revision first (the
// server-side compare-and-swap discipline — task 1.5).
func (k *Keyring) Save(dmDir string) error {
	if k.SchemaVersion == 0 {
		k.SchemaVersion = KeyringSchemaVersion
	}
	k.Generator = GetGenerator()
	data, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal keyring: %w", err)
	}
	if err := os.MkdirAll(dmDir, 0700); err != nil {
		return fmt.Errorf("create dm dir: %w", err)
	}
	if err := atomicfile.WriteFile(keyringPath(dmDir), data, 0600); err != nil {
		return fmt.Errorf("write keyring.json: %w", err)
	}
	return nil
}

// BrowserView returns a copy of the keyring safe to hand to the SPA: the server-held
// bootstrap DEK (server_dek) is stripped, because the browser never needs the bootstrap
// private key and must never receive it. Everything else — epoch pubkeys, the wrapped DEK
// blobs, and the KDF params — IS included: the browser needs them to derive the KEK from
// the user's password and unwrap a password epoch's DEK, and the wrapped blobs are useless
// to anyone without the password. This is the single place the "never serve server_dek"
// rule lives.
func (k *Keyring) BrowserView() *Keyring {
	out := *k
	out.Epochs = make([]Epoch, len(k.Epochs))
	copy(out.Epochs, k.Epochs)
	for i := range out.Epochs {
		out.Epochs[i].ServerDEK = ""
	}
	return &out
}

// CurrentEpoch returns the epoch selected by the keyring's Current pointer.
func (k *Keyring) CurrentEpoch() (*Epoch, error) { return k.EpochByID(k.Current) }

// EpochByID returns the epoch with the given id, or an error if absent.
func (k *Keyring) EpochByID(id int) (*Epoch, error) {
	for i := range k.Epochs {
		if k.Epochs[i].ID == id {
			return &k.Epochs[i], nil
		}
	}
	return nil, fmt.Errorf("epoch %d not found in keyring", id)
}
