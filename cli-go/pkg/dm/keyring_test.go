package dm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyringRoundTrip(t *testing.T) {
	dir := t.TempDir()
	k := &Keyring{
		Epochs: []Epoch{
			{ID: 0, Kind: EpochKindBootstrap, PublicKeyMessages: "AAAA", ServerHeld: true, CreatedAt: "2026-01-01T00:00:00Z"},
			{ID: 1, Kind: EpochKindPassword, PublicKeyMessages: "BBBB",
				WrappedDEK: "ct", WrappedDEKRecovery: "ctr",
				KDF:         &KDFParams{Algo: "argon2id", Salt: "c2FsdA==", Time: 3, Memory: 64 * 1024, Threads: 4},
				RecoveryKDF: &KDFParams{Algo: "hkdf-sha256", Salt: "cnNhbHQ="},
				CreatedAt:   "2026-02-01T00:00:00Z"},
		},
		Current: 1,
	}
	if err := k.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Save stamps schema_version + generator.
	if k.SchemaVersion != KeyringSchemaVersion {
		t.Errorf("schema_version = %d, want %d", k.SchemaVersion, KeyringSchemaVersion)
	}
	if k.Generator != GetGenerator() {
		t.Errorf("generator = %q, want %q", k.Generator, GetGenerator())
	}

	// keyring.json is written 0600.
	fi, err := os.Stat(filepath.Join(dir, keyringFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("keyring.json perm = %o, want 600", perm)
	}

	got, err := LoadKeyring(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Current != 1 || len(got.Epochs) != 2 {
		t.Fatalf("round-trip mismatch: current=%d epochs=%d", got.Current, len(got.Epochs))
	}
	if got.Epochs[1].KDF == nil || got.Epochs[1].KDF.Memory != 64*1024 {
		t.Errorf("argon2id kdf params lost: %+v", got.Epochs[1].KDF)
	}
	if got.Epochs[1].RecoveryKDF == nil || got.Epochs[1].RecoveryKDF.Algo != "hkdf-sha256" {
		t.Errorf("recovery kdf params lost: %+v", got.Epochs[1].RecoveryKDF)
	}
	// Bootstrap epoch carries no wraps / kdf.
	if got.Epochs[0].KDF != nil || got.Epochs[0].WrappedDEK != "" {
		t.Errorf("bootstrap epoch should have no kdf/wrap, got kdf=%+v wrapped=%q", got.Epochs[0].KDF, got.Epochs[0].WrappedDEK)
	}
}

func TestLoadKeyringMissing(t *testing.T) {
	if _, err := LoadKeyring(t.TempDir()); !os.IsNotExist(err) {
		t.Fatalf("want os.IsNotExist, got %v", err)
	}
}

func TestKeyringAccessors(t *testing.T) {
	k := &Keyring{Epochs: []Epoch{{ID: 0}, {ID: 1}}, Current: 1}
	cur, err := k.CurrentEpoch()
	if err != nil || cur.ID != 1 {
		t.Fatalf("CurrentEpoch = %+v, err %v; want id 1", cur, err)
	}
	if _, err := k.EpochByID(2); err == nil {
		t.Error("EpochByID(2) should error (absent)")
	}
}

func TestKeyringUnknownFieldTolerance(t *testing.T) {
	dir := t.TempDir()
	raw := `{"schema_version":1,"revision":3,"epochs":[` +
		`{"id":0,"kind":"bootstrap","public_key_messages":"AA","server_held":true,"created_at":"x","future_field":"ignored"}],` +
		`"current":0,"another_future_field":42}`
	if err := os.WriteFile(filepath.Join(dir, keyringFile), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := LoadKeyring(dir)
	if err != nil {
		t.Fatalf("unknown fields should be tolerated, got %v", err)
	}
	if k.Revision != 3 || len(k.Epochs) != 1 || k.Epochs[0].Kind != EpochKindBootstrap {
		t.Fatalf("bad parse with unknown fields: %+v", k)
	}
}

func TestKeyringBrowserViewStripsServerDEK(t *testing.T) {
	k := &Keyring{}
	if _, err := k.AddBootstrapEpoch(); err != nil { // sets server_dek on epoch 0
		t.Fatal(err)
	}
	if _, _, err := k.SetPassword([]byte("pw")); err != nil { // epoch 1: wrapped_dek + kdf
		t.Fatal(err)
	}
	if k.Epochs[0].ServerDEK == "" {
		t.Fatal("precondition: bootstrap epoch should hold a server_dek")
	}

	view := k.BrowserView()

	// server_dek is stripped everywhere...
	for _, e := range view.Epochs {
		if e.ServerDEK != "" {
			t.Errorf("BrowserView must strip server_dek (epoch %d)", e.ID)
		}
	}
	// ...but the wrapped DEK + KDF params the browser needs survive...
	pw, _ := view.EpochByID(1)
	if pw.WrappedDEK == "" || pw.WrappedDEKRecovery == "" || pw.KDF == nil || pw.RecoveryKDF == nil {
		t.Error("BrowserView must keep wrapped_dek + KDF params for password epochs")
	}
	// ...and the original keyring is untouched (copy, not mutation).
	if k.Epochs[0].ServerDEK == "" {
		t.Error("BrowserView must not mutate the original keyring")
	}
}
