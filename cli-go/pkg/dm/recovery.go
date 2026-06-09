package dm

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tyler-smith/go-bip39"
)

// recoveryEntropyBits is the recovery-phrase entropy size. 128 bits → a 12-word
// BIP39 phrase: already beyond brute-force (128-bit symmetric security), shorter to
// record. The format is not locked to 12 — moving to 24 (256-bit) later is a cheap
// re-wrap (see "Recovery phrase" in the plan); decoding is length-agnostic.
const recoveryEntropyBits = 128

// ErrInvalidRecoveryPhrase is returned when a phrase fails BIP39 decoding — wrong
// word count, an unknown word, or a failed checksum (i.e. a typo). The checksum lets
// us reject typos *before* attempting any decryption, so a mistyped phrase is never
// mistaken for "wrong phrase."
var ErrInvalidRecoveryPhrase = errors.New("dm: invalid recovery phrase (failed BIP39 checksum)")

// GenerateRecoveryPhrase mints a fresh 12-word BIP39 recovery phrase and returns it
// alongside its raw entropy (the input to DeriveKEKRecovery). The phrase is shown to
// the user once and never stored; only the entropy-derived KEK's wrap is persisted.
func GenerateRecoveryPhrase() (phrase string, entropy []byte, err error) {
	entropy, err = bip39.NewEntropy(recoveryEntropyBits)
	if err != nil {
		return "", nil, fmt.Errorf("generate recovery entropy: %w", err)
	}
	phrase, err = bip39.NewMnemonic(entropy)
	if err != nil {
		return "", nil, fmt.Errorf("encode recovery phrase: %w", err)
	}
	return phrase, entropy, nil
}

// PhraseToEntropy validates a user-entered recovery phrase (BIP39 checksum) and
// returns its entropy. Input is normalized first (trimmed, whitespace-collapsed,
// lowercased) so paste artifacts don't cause spurious failures. A genuinely invalid
// phrase returns ErrInvalidRecoveryPhrase.
func PhraseToEntropy(phrase string) ([]byte, error) {
	entropy, err := bip39.EntropyFromMnemonic(NormalizeRecoveryPhrase(phrase))
	if err != nil {
		return nil, ErrInvalidRecoveryPhrase
	}
	return entropy, nil
}

// NormalizeRecoveryPhrase canonicalizes a phrase for decoding: collapse all runs of
// whitespace to single spaces, trim, and lowercase (BIP39 words are lowercase).
func NormalizeRecoveryPhrase(phrase string) string {
	return strings.ToLower(strings.Join(strings.Fields(phrase), " "))
}
