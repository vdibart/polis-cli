package dm

import (
	"bytes"
	"errors"
	"testing"
)

func TestRekeyMintsNewEpochCarryingPhrase(t *testing.T) {
	const pw = "compromise rekey password"
	k, phrase, dek1 := bootstrapThenPassword(t, pw)
	entropy, err := PhraseToEntropy(phrase)
	if err != nil {
		t.Fatal(err)
	}

	dek2, err := k.Rekey([]byte(pw), entropy)
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if k.Current != 2 || len(k.Epochs) != 3 { // epoch 0 (bootstrap) + 1 + 2
		t.Fatalf("after rekey: current=%d epochs=%d, want 2/3", k.Current, len(k.Epochs))
	}
	// Fresh keypair: new DEK differs from epoch 1's.
	if bytes.Equal(dek1[:], dek2[:]) {
		t.Error("rekey must mint a fresh DEK")
	}
	ep1, _ := k.EpochByID(1)
	ep2, _ := k.EpochByID(2)
	if ep1.PublicKeyMessages == ep2.PublicKeyMessages {
		t.Error("rekey must advertise a new public key")
	}
	// New epoch opens with the same password AND the same phrase (carried forward).
	if got, err := k.UnlockEpochWithPassword(2, []byte(pw)); err != nil || !bytes.Equal(got, dek2[:]) {
		t.Errorf("new epoch password unlock failed: err=%v equal=%v", err, bytes.Equal(got, dek2[:]))
	}
	if got, err := k.UnlockEpochWithPhrase(2, phrase); err != nil || !bytes.Equal(got, dek2[:]) {
		t.Errorf("new epoch phrase unlock failed: err=%v equal=%v", err, bytes.Equal(got, dek2[:]))
	}
	// Old epoch 1 is still recoverable (kept as-is).
	if got, err := k.UnlockEpochWithPhrase(1, phrase); err != nil || !bytes.Equal(got, dek1[:]) {
		t.Errorf("old epoch should remain phrase-recoverable: err=%v", err)
	}
}

func TestRekeyWrongPhraseRejected(t *testing.T) {
	const pw = "pw"
	k, _, _ := bootstrapThenPassword(t, pw)
	// A valid but different phrase's entropy must not match the current recovery wrap.
	_, otherEntropy, err := GenerateRecoveryPhrase()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Rekey([]byte(pw), otherEntropy); err == nil {
		t.Fatal("Rekey with a non-matching phrase should fail (continuity check)")
	}
}

func TestRegenerateRecoveryPhrase(t *testing.T) {
	const pw = "regen password"
	k, oldPhrase, dek1 := bootstrapThenPassword(t, pw)

	// Caller unlocks epoch 1 (via password) to obtain its DEK, then regenerates.
	dek, err := k.UnlockEpochWithPassword(1, []byte(pw))
	if err != nil {
		t.Fatal(err)
	}
	newPhrase, err := k.RegenerateRecoveryPhrase(map[int][]byte{1: dek})
	if err != nil {
		t.Fatalf("RegenerateRecoveryPhrase: %v", err)
	}
	if newPhrase == oldPhrase {
		t.Error("regenerated phrase should differ")
	}
	// Old phrase no longer opens epoch 1's recovery wrap; new phrase does.
	if _, err := k.UnlockEpochWithPhrase(1, oldPhrase); !errors.Is(err, ErrWrongKey) {
		t.Errorf("old phrase should no longer unlock, got %v", err)
	}
	got, err := k.UnlockEpochWithPhrase(1, newPhrase)
	if err != nil || !bytes.Equal(got, dek1[:]) {
		t.Fatalf("new phrase should unlock same DEK: err=%v equal=%v", err, bytes.Equal(got, dek1[:]))
	}
	// Password wrap untouched.
	if _, err := k.UnlockEpochWithPassword(1, []byte(pw)); err != nil {
		t.Errorf("password should still unlock after regenerate: %v", err)
	}
}

func TestRegenerateRejectsMismatchedDEK(t *testing.T) {
	k, _, _ := bootstrapThenPassword(t, "pw")
	wrong := randDEK(t) // not epoch 1's DEK
	if _, err := k.RegenerateRecoveryPhrase(map[int][]byte{1: wrong}); err == nil {
		t.Fatal("regenerate should reject a DEK that doesn't match the epoch's public key")
	}
	if _, err := k.RegenerateRecoveryPhrase(map[int][]byte{}); err == nil {
		t.Error("regenerate with no DEKs should error")
	}
}
