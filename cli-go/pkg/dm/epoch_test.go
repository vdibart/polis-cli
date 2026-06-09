package dm

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func TestAddBootstrapEpoch(t *testing.T) {
	k := &Keyring{}
	dek, err := k.AddBootstrapEpoch()
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Epochs) != 1 || k.Epochs[0].Kind != EpochKindBootstrap || !k.Epochs[0].ServerHeld {
		t.Fatalf("bad bootstrap epoch: %+v", k.Epochs)
	}
	if k.Current != 0 || k.Revision != 1 {
		t.Errorf("current=%d revision=%d, want 0/1", k.Current, k.Revision)
	}
	if dek == ([32]byte{}) {
		t.Error("bootstrap DEK should be non-zero")
	}
	if k.Epochs[0].WrappedDEK != "" || k.Epochs[0].KDF != nil {
		t.Error("bootstrap epoch must carry no wrap/kdf")
	}
	// The bootstrap DEK is persisted plaintext in server_dek (operator-readable) and
	// decodes back to the returned DEK.
	got, ok, err := k.Epochs[0].ServerDEKBytes()
	if err != nil || !ok {
		t.Fatalf("server_dek should be present: ok=%v err=%v", ok, err)
	}
	if got != dek {
		t.Error("server_dek must decode to the bootstrap DEK")
	}
	if _, err := k.AddBootstrapEpoch(); err == nil {
		t.Error("second AddBootstrapEpoch should fail")
	}
}

func TestAddPasswordEpoch(t *testing.T) {
	pub := base64.StdEncoding.EncodeToString(make([]byte, 32))
	argon := KDFParams{Algo: "argon2id", Salt: "AAA=", Time: 3, Memory: 65536, Threads: 4}
	rec := KDFParams{Algo: "hkdf-sha256", Salt: "BBB="}

	k := &Keyring{}
	if _, err := k.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	id, err := k.AddPasswordEpoch(pub, "wrapped", "wrappedRec", argon, rec)
	if err != nil {
		t.Fatalf("AddPasswordEpoch: %v", err)
	}
	if id != 1 || k.Current != 1 {
		t.Errorf("new epoch id/current = %d/%d, want 1/1", id, k.Current)
	}
	ep, _ := k.EpochByID(1)
	if ep.Kind != EpochKindPassword || ep.WrappedDEK != "wrapped" || ep.KDF == nil || ep.RecoveryKDF == nil || ep.ServerDEK != "" {
		t.Errorf("malformed appended epoch: %+v", ep)
	}

	// A second call must be rejected — current is now a password epoch.
	if _, err := k.AddPasswordEpoch(pub, "w", "r", argon, rec); err == nil {
		t.Error("second AddPasswordEpoch should fail (use change-password)")
	}

	// Structural validation.
	k2 := &Keyring{}
	k2.AddBootstrapEpoch()
	if _, err := k2.AddPasswordEpoch("not-32-bytes", "w", "r", argon, rec); err == nil {
		t.Error("should reject a bad messages public key")
	}
	if _, err := k2.AddPasswordEpoch(pub, "w", "r", KDFParams{Algo: "pbkdf2"}, rec); err == nil {
		t.Error("should reject a non-argon2id password KDF")
	}
	if _, err := k2.AddPasswordEpoch(pub, "w", "", argon, rec); err == nil {
		t.Error("should reject a missing recovery wrap")
	}
}

func TestClearBootstrapServerDEK(t *testing.T) {
	k := &Keyring{}
	if _, err := k.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	revBefore := k.Revision

	if !k.ClearBootstrapServerDEK() {
		t.Fatal("first clear should report a change")
	}
	if k.Revision != revBefore+1 {
		t.Errorf("clear should bump revision: got %d, want %d", k.Revision, revBefore+1)
	}
	if _, ok, _ := k.Epochs[0].ServerDEKBytes(); ok {
		t.Error("server_dek should be gone after clear")
	}
	// Idempotent: a second clear is a no-op (no revision bump).
	if k.ClearBootstrapServerDEK() {
		t.Error("second clear should report no change")
	}
	if k.Revision != revBefore+1 {
		t.Errorf("idempotent clear must not bump revision again: got %d", k.Revision)
	}
}

// Build a keyring at epoch 1 (password) and return the phrase + the epoch-1 DEK.
func bootstrapThenPassword(t *testing.T, password string) (*Keyring, string, [32]byte) {
	t.Helper()
	k := &Keyring{}
	if _, err := k.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	phrase, dek, err := k.SetPassword([]byte(password))
	if err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	return k, phrase, dek
}

func TestSetPasswordBothWrapsUnlock(t *testing.T) {
	const pw = "a strong message password"
	k, phrase, dek := bootstrapThenPassword(t, pw)

	if k.Current != 1 {
		t.Fatalf("current = %d, want 1", k.Current)
	}
	ep, _ := k.CurrentEpoch()
	if ep.Kind != EpochKindPassword || ep.WrappedDEK == "" || ep.WrappedDEKRecovery == "" || ep.KDF == nil || ep.RecoveryKDF == nil {
		t.Fatalf("password epoch missing wraps/kdf: %+v", ep)
	}

	// Password unlocks the same DEK.
	byPw, err := k.UnlockEpochWithPassword(1, []byte(pw))
	if err != nil || !bytes.Equal(byPw, dek[:]) {
		t.Fatalf("password unlock mismatch: err=%v equal=%v", err, bytes.Equal(byPw, dek[:]))
	}
	// Recovery phrase unlocks the same DEK.
	byPhrase, err := k.UnlockEpochWithPhrase(1, phrase)
	if err != nil || !bytes.Equal(byPhrase, dek[:]) {
		t.Fatalf("phrase unlock mismatch: err=%v equal=%v", err, bytes.Equal(byPhrase, dek[:]))
	}
	// Wrong password → ErrWrongKey.
	if _, err := k.UnlockEpochWithPassword(1, []byte("nope")); !errors.Is(err, ErrWrongKey) {
		t.Errorf("wrong password want ErrWrongKey, got %v", err)
	}
	// Typo'd phrase → ErrInvalidRecoveryPhrase.
	if _, err := k.UnlockEpochWithPhrase(1, "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon zoo"); !errors.Is(err, ErrInvalidRecoveryPhrase) {
		t.Errorf("typo phrase want ErrInvalidRecoveryPhrase, got %v", err)
	}
}

func TestSetPasswordRequiresBootstrap(t *testing.T) {
	k, _, _ := bootstrapThenPassword(t, "pw")
	if _, _, err := k.SetPassword([]byte("again")); err == nil {
		t.Error("SetPassword on a non-bootstrap current epoch should fail")
	}
}

func TestChangePassword(t *testing.T) {
	const oldPw, newPw = "old password here", "new password there"
	k, phrase, dek := bootstrapThenPassword(t, oldPw)
	ep, _ := k.CurrentEpoch()
	pubBefore, epochsBefore, recBefore := ep.PublicKeyMessages, len(k.Epochs), ep.WrappedDEKRecovery

	if err := k.ChangePassword([]byte(oldPw), []byte(newPw)); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	ep, _ = k.CurrentEpoch()

	// No new epoch, same keypair, recovery wrap untouched.
	if len(k.Epochs) != epochsBefore || ep.PublicKeyMessages != pubBefore || ep.WrappedDEKRecovery != recBefore {
		t.Error("ChangePassword must not add an epoch, change the pubkey, or touch the recovery wrap")
	}
	// Old password no longer works; new password yields the same DEK.
	if _, err := k.UnlockEpochWithPassword(1, []byte(oldPw)); !errors.Is(err, ErrWrongKey) {
		t.Errorf("old password should no longer unlock, got %v", err)
	}
	got, err := k.UnlockEpochWithPassword(1, []byte(newPw))
	if err != nil || !bytes.Equal(got, dek[:]) {
		t.Fatalf("new password should unlock same DEK: err=%v equal=%v", err, bytes.Equal(got, dek[:]))
	}
	// Recovery phrase still unlocks the same DEK.
	byPhrase, err := k.UnlockEpochWithPhrase(1, phrase)
	if err != nil || !bytes.Equal(byPhrase, dek[:]) {
		t.Errorf("recovery phrase should still unlock after password change")
	}
}

func TestChangePasswordWrongOld(t *testing.T) {
	k, _, _ := bootstrapThenPassword(t, "the real password")
	if err := k.ChangePassword([]byte("wrong old"), []byte("whatever")); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("want ErrWrongKey, got %v", err)
	}
}

// 1.1 + 1.2 + 1.3 + 1.4 end-to-end: persist a keyring and unlock after reload.
func TestKeyringPersistThenUnlock(t *testing.T) {
	const pw = "persisted password"
	dir := t.TempDir()
	k, phrase, dek := bootstrapThenPassword(t, pw)
	if err := k.Save(dir); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.UnlockEpochWithPassword(1, []byte(pw))
	if err != nil || !bytes.Equal(got, dek[:]) {
		t.Fatalf("reloaded password unlock failed: err=%v equal=%v", err, bytes.Equal(got, dek[:]))
	}
	if _, err := reloaded.UnlockEpochWithPhrase(1, phrase); err != nil {
		t.Fatalf("reloaded phrase unlock failed: %v", err)
	}
}
