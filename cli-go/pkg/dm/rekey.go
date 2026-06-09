package dm

import (
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// publicFromDEK derives the X25519 public key for a DEK (private scalar), so callers
// can verify a supplied DEK actually belongs to an epoch before re-wrapping it.
func publicFromDEK(dek []byte) ([32]byte, error) {
	var pub [32]byte
	out, err := curve25519.X25519(dek, curve25519.Basepoint)
	if err != nil {
		return pub, fmt.Errorf("derive public key from DEK: %w", err)
	}
	copy(pub[:], out)
	return pub, nil
}

// Rekey mints a fresh password epoch (new keypair + DEK) and makes it current — the
// forward-secrecy boundary for a suspected compromise. The new DEK is wrapped under the
// password KEK and, per decision (a), the recovery KEK derived from the supplied phrase
// entropy (the phrase must be in-hand). To preserve recovery continuity, the supplied
// entropy is first verified against the current epoch's recovery wrap, so a mistyped
// phrase can't silently split recovery across epochs.
//
// The prior epoch stays in the keyring for deferred recovery; re-encrypting the message
// corpus forward onto the new epoch is the storage layer's job (task 1.6).
func (k *Keyring) Rekey(password, recoveryEntropy []byte) (dek [32]byte, err error) {
	cur, err := k.CurrentEpoch()
	if err != nil {
		return dek, err
	}
	// Continuity: the phrase must be the user's existing one.
	if cur.RecoveryKDF != nil && cur.WrappedDEKRecovery != "" {
		kek, derr := DeriveKEKRecovery(recoveryEntropy, *cur.RecoveryKDF)
		if derr != nil {
			return dek, derr
		}
		if _, uerr := UnwrapDEK(cur.WrappedDEKRecovery, kek); uerr != nil {
			return dek, fmt.Errorf("recovery phrase does not match the current epoch: %w", uerr)
		}
	}
	ep, newDEK, err := newPasswordEpoch(k.nextEpochID(), password, recoveryEntropy)
	if err != nil {
		return dek, err
	}
	k.Epochs = append(k.Epochs, ep)
	k.Current = ep.ID
	k.Revision++
	return newDEK, nil
}

// RegenerateRecoveryPhrase generates a new recovery phrase and re-wraps the recovery
// side (wrapped_dek_recovery, under a fresh per-epoch recovery KDF) of every epoch whose
// DEK the caller supplies — keyed by epoch id, typically the epochs unlocked this
// session. Epochs not supplied keep their existing recovery wrap (still openable by the
// OLD phrase). The password wraps are untouched. Returns the new phrase to display once.
//
// Decision (a): the new entropy is in-hand (freshly generated here). Each supplied DEK
// is verified against its epoch's public key before re-wrapping. This is also the 12→24
// upgrade path (a larger phrase is just a different GenerateRecoveryPhrase).
func (k *Keyring) RegenerateRecoveryPhrase(unlockedDEKs map[int][]byte) (newPhrase string, err error) {
	if len(unlockedDEKs) == 0 {
		return "", fmt.Errorf("RegenerateRecoveryPhrase needs at least one unlocked epoch DEK")
	}
	phrase, entropy, err := GenerateRecoveryPhrase()
	if err != nil {
		return "", err
	}
	for id, dek := range unlockedDEKs {
		ep, eerr := k.EpochByID(id)
		if eerr != nil {
			return "", eerr
		}
		if ep.Kind != EpochKindPassword {
			continue // bootstrap epoch has no recovery wrap
		}
		// Verify the supplied DEK matches this epoch's advertised public key.
		pub, perr := publicFromDEK(dek)
		if perr != nil {
			return "", perr
		}
		if base64.StdEncoding.EncodeToString(pub[:]) != ep.PublicKeyMessages {
			return "", fmt.Errorf("supplied DEK does not match epoch %d's public key", id)
		}
		recKDF, kerr := NewRecoveryKDF()
		if kerr != nil {
			return "", kerr
		}
		recKEK, derr := DeriveKEKRecovery(entropy, recKDF)
		if derr != nil {
			return "", derr
		}
		wrapped, werr := WrapDEK(dek, recKEK)
		if werr != nil {
			return "", werr
		}
		ep.WrappedDEKRecovery = wrapped
		ep.RecoveryKDF = &recKDF
	}
	k.Revision++
	return phrase, nil
}
