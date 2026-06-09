package dm

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"golang.org/x/crypto/nacl/box"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// NewEpochKeypair generates a fresh, independent X25519 DM keypair (Variant B — not
// derived from the identity seed). Returns the public key and the DEK (the X25519
// private scalar) that actually decrypts messages.
func NewEpochKeypair() (pub [32]byte, dek [32]byte, err error) {
	pk, sk, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return pub, dek, fmt.Errorf("generate epoch keypair: %w", err)
	}
	return *pk, *sk, nil
}

func (k *Keyring) nextEpochID() int {
	max := -1
	for _, e := range k.Epochs {
		if e.ID > max {
			max = e.ID
		}
	}
	return max + 1
}

// AddBootstrapEpoch appends epoch 0 — the server-held, no-password bootstrap epoch — to
// an empty keyring and returns the bootstrap DEK. The private DEK is persisted in the
// epoch's server_dek field (plaintext base64): epoch 0 is operator-readable by design,
// and the server needs the DEK to seal its sent copy and forward stale-addressed mail.
// It is cleared (ClearBootstrapServerDEK) past the forwarding window once a password is
// set. Used by `polis init` and Medic (the primary deploy-upgrade path).
func (k *Keyring) AddBootstrapEpoch() (dek [32]byte, err error) {
	if len(k.Epochs) != 0 {
		return dek, fmt.Errorf("bootstrap epoch must be the first epoch")
	}
	pub, sk, err := NewEpochKeypair()
	if err != nil {
		return dek, err
	}
	k.Epochs = append(k.Epochs, Epoch{
		ID:                0,
		Kind:              EpochKindBootstrap,
		PublicKeyMessages: base64.StdEncoding.EncodeToString(pub[:]),
		ServerHeld:        true,
		ServerDEK:         base64.StdEncoding.EncodeToString(sk[:]),
		CreatedAt:         nowRFC3339(),
	})
	k.Current = 0
	k.Revision++
	return sk, nil
}

// ServerDEKBytes decodes a bootstrap epoch's server-held plaintext DEK. ok is false once
// the DEK has been cleared (past the forwarding window) or for a non-bootstrap epoch.
func (e *Epoch) ServerDEKBytes() (dek [32]byte, ok bool, err error) {
	if e.ServerDEK == "" {
		return dek, false, nil
	}
	raw, err := base64.StdEncoding.DecodeString(e.ServerDEK)
	if err != nil {
		return dek, false, fmt.Errorf("decode server_dek (epoch %d): %w", e.ID, err)
	}
	if len(raw) != 32 {
		return dek, false, fmt.Errorf("server_dek (epoch %d) is %d bytes, want 32", e.ID, len(raw))
	}
	copy(dek[:], raw)
	return dek, true, nil
}

// ClearBootstrapServerDEK removes the server-held plaintext bootstrap DEK — called past
// the forwarding window after a password is set, so the operator no longer holds the
// epoch-0 key. Idempotent; bumps Revision and returns true only if it cleared something.
func (k *Keyring) ClearBootstrapServerDEK() bool {
	for i := range k.Epochs {
		if k.Epochs[i].Kind == EpochKindBootstrap && k.Epochs[i].ServerDEK != "" {
			k.Epochs[i].ServerDEK = ""
			k.Revision++
			return true
		}
	}
	return false
}

// AddPasswordEpoch appends a BROWSER-MINTED password epoch and makes it current. The
// browser mints the keypair and derives + wraps the DEK under the password and recovery
// KEKs (the server never sees the password, KEK, DEK, or recovery phrase); this only
// validates the supplied material is structurally well-formed and stores it. Precondition:
// the current epoch is the bootstrap epoch — this is the first password (a later change is
// a re-wrap, not a new epoch). Returns the new epoch id. The caller persists with SaveCAS.
//
// SetPassword (below) is the server-side equivalent used by tests/CLI; AddPasswordEpoch is
// the path the SPA uses so private key material stays in the browser.
func (k *Keyring) AddPasswordEpoch(pubKeyMessagesB64, wrappedDEK, wrappedDEKRecovery string, kdf, recoveryKDF KDFParams) (int, error) {
	cur, err := k.CurrentEpoch()
	if err != nil {
		return 0, err
	}
	if cur.Kind != EpochKindBootstrap {
		return 0, fmt.Errorf("a password epoch already exists (current epoch %d is %q); use change-password", cur.ID, cur.Kind)
	}
	pub, err := base64.StdEncoding.DecodeString(pubKeyMessagesB64)
	if err != nil || len(pub) != 32 {
		return 0, fmt.Errorf("invalid messages public key (want base64 of 32 bytes)")
	}
	if wrappedDEK == "" || wrappedDEKRecovery == "" {
		return 0, fmt.Errorf("wrapped_dek and wrapped_dek_recovery are required")
	}
	if kdf.Algo != "argon2id" {
		return 0, fmt.Errorf("password KDF must be argon2id, got %q", kdf.Algo)
	}
	if recoveryKDF.Algo != "hkdf-sha256" {
		return 0, fmt.Errorf("recovery KDF must be hkdf-sha256, got %q", recoveryKDF.Algo)
	}
	kdfCopy, recCopy := kdf, recoveryKDF
	id := k.nextEpochID()
	k.Epochs = append(k.Epochs, Epoch{
		ID:                 id,
		Kind:               EpochKindPassword,
		PublicKeyMessages:  pubKeyMessagesB64,
		ServerHeld:         false,
		WrappedDEK:         wrappedDEK,
		WrappedDEKRecovery: wrappedDEKRecovery,
		KDF:                &kdfCopy,
		RecoveryKDF:        &recCopy,
		CreatedAt:          nowRFC3339(),
	})
	k.Current = id
	k.Revision++
	return id, nil
}

// SetPassword performs the bootstrap→password upgrade: it mints a new password epoch
// with a fresh keypair, generates a recovery phrase, wraps the new DEK under BOTH the
// password KEK (Argon2id) and the recovery KEK (HKDF over the phrase entropy), appends
// RewrapCurrentEpoch overwrites the current password epoch's wrap material with
// browser-supplied blobs (phase 4.8 — the O(1) re-wrap). The BROWSER unwraps the
// current-epoch DEK with the old KEK and re-wraps it under a new password and/or a
// new recovery-phrase KEK over fresh salts; the server only stores the new wrapped
// blob + KDF params. The DEK and the epoch pubkey are unchanged — so this does NOT
// mint a new epoch, re-encrypt any message, or require republishing the messages
// key. Pass empty wrap + nil kdf for the half you're not changing:
//
//   - change password:     wrappedDEK + kdf set, recovery args empty/nil.
//   - regenerate recovery:  wrappedDEKRecovery + recoveryKDF set, password args empty/nil.
//   - both:                 all four set.
//
// Callers bump+CAS the keyring exactly as for AddPasswordEpoch.
func (k *Keyring) RewrapCurrentEpoch(wrappedDEK string, kdf *KDFParams, wrappedDEKRecovery string, recoveryKDF *KDFParams) error {
	cur, err := k.CurrentEpoch()
	if err != nil {
		return err
	}
	if cur.Kind != EpochKindPassword {
		return fmt.Errorf("current epoch %d is %q, not a password epoch; set a password first", cur.ID, cur.Kind)
	}
	changePw := wrappedDEK != "" || kdf != nil
	changeRec := wrappedDEKRecovery != "" || recoveryKDF != nil
	if !changePw && !changeRec {
		return fmt.Errorf("nothing to re-wrap (provide a password wrap, a recovery wrap, or both)")
	}
	if changePw {
		if wrappedDEK == "" || kdf == nil {
			return fmt.Errorf("password re-wrap needs both wrapped_dek and kdf")
		}
		if kdf.Algo != "argon2id" {
			return fmt.Errorf("password KDF must be argon2id, got %q", kdf.Algo)
		}
	}
	if changeRec {
		if wrappedDEKRecovery == "" || recoveryKDF == nil {
			return fmt.Errorf("recovery re-wrap needs both wrapped_dek_recovery and recovery_kdf")
		}
		if recoveryKDF.Algo != "hkdf-sha256" {
			return fmt.Errorf("recovery KDF must be hkdf-sha256, got %q", recoveryKDF.Algo)
		}
	}
	// CurrentEpoch returns a pointer into k.Epochs, so these writes land on the
	// stored epoch directly.
	if changePw {
		kc := *kdf
		cur.WrappedDEK = wrappedDEK
		cur.KDF = &kc
	}
	if changeRec {
		rc := *recoveryKDF
		cur.WrappedDEKRecovery = wrappedDEKRecovery
		cur.RecoveryKDF = &rc
	}
	k.Revision++
	return nil
}

// SetPassword mints epoch 1 (the password epoch), wraps a fresh DEK under
// it, and makes it current. Precondition: the current epoch is a bootstrap epoch (this
// is the first password). Returns the recovery phrase to display once, and the
// in-session DEK (the caller re-encrypts any bootstrap messages forward — orchestration
// lives in the storage layer, task 1.6).
func (k *Keyring) SetPassword(password []byte) (recoveryPhrase string, dek [32]byte, err error) {
	cur, err := k.CurrentEpoch()
	if err != nil {
		return "", dek, err
	}
	if cur.Kind != EpochKindBootstrap {
		return "", dek, fmt.Errorf("SetPassword requires the current epoch to be bootstrap; current is %q", cur.Kind)
	}
	phrase, entropy, err := GenerateRecoveryPhrase()
	if err != nil {
		return "", dek, err
	}
	ep, newDEK, err := newPasswordEpoch(k.nextEpochID(), password, entropy)
	if err != nil {
		return "", dek, err
	}
	k.Epochs = append(k.Epochs, ep)
	k.Current = ep.ID
	k.Revision++
	return phrase, newDEK, nil
}

// newPasswordEpoch mints a password epoch: fresh keypair, DEK wrapped under a freshly
// salted password KEK and recovery KEK.
func newPasswordEpoch(id int, password, recoveryEntropy []byte) (Epoch, [32]byte, error) {
	var zero [32]byte
	pub, dek, err := NewEpochKeypair()
	if err != nil {
		return Epoch{}, zero, err
	}
	pwKDF, err := NewArgon2idKDF()
	if err != nil {
		return Epoch{}, zero, err
	}
	recKDF, err := NewRecoveryKDF()
	if err != nil {
		return Epoch{}, zero, err
	}
	pwKEK, err := DeriveKEKPassword(password, pwKDF)
	if err != nil {
		return Epoch{}, zero, err
	}
	recKEK, err := DeriveKEKRecovery(recoveryEntropy, recKDF)
	if err != nil {
		return Epoch{}, zero, err
	}
	wrapped, err := WrapDEK(dek[:], pwKEK)
	if err != nil {
		return Epoch{}, zero, err
	}
	wrappedRec, err := WrapDEK(dek[:], recKEK)
	if err != nil {
		return Epoch{}, zero, err
	}
	return Epoch{
		ID:                 id,
		Kind:               EpochKindPassword,
		PublicKeyMessages:  base64.StdEncoding.EncodeToString(pub[:]),
		ServerHeld:         false,
		WrappedDEK:         wrapped,
		WrappedDEKRecovery: wrappedRec,
		KDF:                &pwKDF,
		RecoveryKDF:        &recKDF,
		CreatedAt:          nowRFC3339(),
	}, dek, nil
}

// ChangePassword re-wraps the current epoch's DEK under a new password KEK (fresh
// Argon2id salt). O(1): no new epoch, no keypair change, no message re-encryption; the
// recovery wrap is untouched (the phrase still works). Wrong oldPassword → ErrWrongKey.
func (k *Keyring) ChangePassword(oldPassword, newPassword []byte) error {
	cur, err := k.CurrentEpoch()
	if err != nil {
		return err
	}
	if cur.Kind != EpochKindPassword || cur.KDF == nil {
		return fmt.Errorf("current epoch has no password to change")
	}
	oldKEK, err := DeriveKEKPassword(oldPassword, *cur.KDF)
	if err != nil {
		return err
	}
	dek, err := UnwrapDEK(cur.WrappedDEK, oldKEK) // ErrWrongKey if wrong
	if err != nil {
		return err
	}
	newKDF, err := NewArgon2idKDF()
	if err != nil {
		return err
	}
	newKEK, err := DeriveKEKPassword(newPassword, newKDF)
	if err != nil {
		return err
	}
	wrapped, err := WrapDEK(dek, newKEK)
	if err != nil {
		return err
	}
	cur.WrappedDEK = wrapped
	cur.KDF = &newKDF
	k.Revision++
	return nil
}

// UnlockEpochWithPassword returns an epoch's DEK by deriving the password KEK and
// opening wrapped_dek. Wrong password → ErrWrongKey.
func (k *Keyring) UnlockEpochWithPassword(id int, password []byte) ([]byte, error) {
	ep, err := k.EpochByID(id)
	if err != nil {
		return nil, err
	}
	if ep.KDF == nil || ep.WrappedDEK == "" {
		return nil, fmt.Errorf("epoch %d has no password wrap", id)
	}
	kek, err := DeriveKEKPassword(password, *ep.KDF)
	if err != nil {
		return nil, err
	}
	return UnwrapDEK(ep.WrappedDEK, kek)
}

// UnlockEpochWithPhrase returns an epoch's DEK by deriving the recovery KEK from the
// phrase and opening wrapped_dek_recovery. A mistyped phrase → ErrInvalidRecoveryPhrase
// (caught by the BIP39 checksum before any unwrap); a valid-but-wrong phrase →
// ErrWrongKey.
func (k *Keyring) UnlockEpochWithPhrase(id int, phrase string) ([]byte, error) {
	ep, err := k.EpochByID(id)
	if err != nil {
		return nil, err
	}
	if ep.RecoveryKDF == nil || ep.WrappedDEKRecovery == "" {
		return nil, fmt.Errorf("epoch %d has no recovery wrap", id)
	}
	entropy, err := PhraseToEntropy(phrase)
	if err != nil {
		return nil, err
	}
	kek, err := DeriveKEKRecovery(entropy, *ep.RecoveryKDF)
	if err != nil {
		return nil, err
	}
	return UnwrapDEK(ep.WrappedDEKRecovery, kek)
}
