package dm

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

func mustArgon2idKDF(t *testing.T) KDFParams {
	t.Helper()
	p, err := NewArgon2idKDF()
	if err != nil {
		t.Fatalf("NewArgon2idKDF: %v", err)
	}
	return p
}

func randDEK(t *testing.T) []byte {
	t.Helper()
	d := make([]byte, kekLen)
	if _, err := io.ReadFull(rand.Reader, d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDeriveKEKPasswordDeterministic(t *testing.T) {
	p := mustArgon2idKDF(t)
	a, err := DeriveKEKPassword([]byte("correct horse battery staple"), p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeriveKEKPassword([]byte("correct horse battery staple"), p)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("same password + params must derive the same KEK")
	}
	c, _ := DeriveKEKPassword([]byte("a different password"), p)
	if a == c {
		t.Error("different passwords must derive different KEKs")
	}
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	p := mustArgon2idKDF(t)
	kek, err := DeriveKEKPassword([]byte("hunter2-but-strong"), p)
	if err != nil {
		t.Fatal(err)
	}
	dek := randDEK(t)
	blob, err := WrapDEK(dek, kek)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	got, err := UnwrapDEK(blob, kek)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Error("unwrapped DEK != original")
	}
}

func TestUnwrapWrongPasswordIsErrWrongKey(t *testing.T) {
	p := mustArgon2idKDF(t)
	right, _ := DeriveKEKPassword([]byte("the right password"), p)
	wrong, _ := DeriveKEKPassword([]byte("the WRONG password"), p)
	blob, err := WrapDEK(randDEK(t), right)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapDEK(blob, wrong); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong key should return ErrWrongKey, got %v", err)
	}
}

func TestRecoveryKEKRoundTrip(t *testing.T) {
	p, err := NewRecoveryKDF()
	if err != nil {
		t.Fatal(err)
	}
	entropy := randDEK(t) // stand-in for 128-bit BIP39 entropy (real source: task 1.4)
	kek, err := DeriveKEKRecovery(entropy, p)
	if err != nil {
		t.Fatal(err)
	}
	dek := randDEK(t)
	blob, err := WrapDEK(dek, kek)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapDEK(blob, kek)
	if err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("recovery round-trip failed: err=%v equal=%v", err, bytes.Equal(got, dek))
	}
	// A different entropy yields a different KEK → unwrap must fail.
	kek2, _ := DeriveKEKRecovery(randDEK(t), p)
	if _, err := UnwrapDEK(blob, kek2); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong recovery entropy should fail to unwrap, got %v", err)
	}
}

func TestKDFConstructorsAlgoAndSalt(t *testing.T) {
	a := mustArgon2idKDF(t)
	if a.Algo != "argon2id" || a.Salt == "" || a.Memory != defaultArgon2Memory {
		t.Errorf("argon2id KDF malformed: %+v", a)
	}
	r, err := NewRecoveryKDF()
	if err != nil {
		t.Fatal(err)
	}
	if r.Algo != "hkdf-sha256" || r.Salt == "" {
		t.Errorf("recovery KDF malformed: %+v", r)
	}
	// Salts are random per call.
	b := mustArgon2idKDF(t)
	if a.Salt == b.Salt {
		t.Error("two argon2id KDFs should have distinct random salts")
	}
}

func TestDeriveKEKAlgoMismatch(t *testing.T) {
	if _, err := DeriveKEKPassword([]byte("x"), KDFParams{Algo: "hkdf-sha256", Salt: "AAAA"}); err == nil {
		t.Error("DeriveKEKPassword should reject non-argon2id params")
	}
	if _, err := DeriveKEKRecovery([]byte("x"), KDFParams{Algo: "argon2id", Salt: "AAAA"}); err == nil {
		t.Error("DeriveKEKRecovery should reject non-hkdf params")
	}
}
