package dm

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestGenerateRecoveryPhraseRoundTrip(t *testing.T) {
	phrase, entropy, err := GenerateRecoveryPhrase()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Fields(phrase)); n != 12 {
		t.Errorf("phrase has %d words, want 12", n)
	}
	if len(entropy) != recoveryEntropyBits/8 {
		t.Errorf("entropy %d bytes, want %d", len(entropy), recoveryEntropyBits/8)
	}
	got, err := PhraseToEntropy(phrase)
	if err != nil {
		t.Fatalf("decode own phrase: %v", err)
	}
	if !bytes.Equal(got, entropy) {
		t.Error("round-trip entropy mismatch")
	}
}

// Standard BIP39 128-bit vector: all-zero entropy.
func TestRecoveryPhraseKnownVector(t *testing.T) {
	const zeroPhrase = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	got, err := PhraseToEntropy(zeroPhrase)
	if err != nil {
		t.Fatalf("known vector should decode: %v", err)
	}
	if !bytes.Equal(got, make([]byte, 16)) {
		t.Errorf("zero-entropy vector decoded to %x, want 16 zero bytes", got)
	}
}

func TestRecoveryPhraseTypoRejected(t *testing.T) {
	// Valid phrase with the last word swapped to another valid word → checksum breaks.
	bad := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon zoo"
	if _, err := PhraseToEntropy(bad); !errors.Is(err, ErrInvalidRecoveryPhrase) {
		t.Fatalf("checksum-breaking phrase should be ErrInvalidRecoveryPhrase, got %v", err)
	}
	// An unknown word is also rejected.
	if _, err := PhraseToEntropy("notaword " + strings.Repeat("abandon ", 10) + "about"); !errors.Is(err, ErrInvalidRecoveryPhrase) {
		t.Errorf("unknown word should be rejected")
	}
}

func TestRecoveryPhraseNormalization(t *testing.T) {
	phrase, entropy, err := GenerateRecoveryPhrase()
	if err != nil {
		t.Fatal(err)
	}
	messy := "  " + strings.ToUpper(strings.ReplaceAll(phrase, " ", "   \t")) + "\n"
	got, err := PhraseToEntropy(messy)
	if err != nil {
		t.Fatalf("normalized messy phrase should decode: %v", err)
	}
	if !bytes.Equal(got, entropy) {
		t.Error("normalization changed the decoded entropy")
	}
}

// 1.4 → 1.2 integration: phrase → entropy → recovery KEK → wrap/unwrap.
func TestRecoveryPhraseUnlocksWrappedDEK(t *testing.T) {
	phrase, entropy, err := GenerateRecoveryPhrase()
	if err != nil {
		t.Fatal(err)
	}
	kp, err := NewRecoveryKDF()
	if err != nil {
		t.Fatal(err)
	}
	kek, err := DeriveKEKRecovery(entropy, kp)
	if err != nil {
		t.Fatal(err)
	}
	dek := randDEK(t)
	blob, err := WrapDEK(dek, kek)
	if err != nil {
		t.Fatal(err)
	}
	// Re-derive from the phrase as a user would on recovery.
	reEntropy, err := PhraseToEntropy(phrase)
	if err != nil {
		t.Fatal(err)
	}
	reKEK, err := DeriveKEKRecovery(reEntropy, kp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapDEK(blob, reKEK)
	if err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("phrase should unlock the recovery wrap: err=%v equal=%v", err, bytes.Equal(got, dek))
	}
}
