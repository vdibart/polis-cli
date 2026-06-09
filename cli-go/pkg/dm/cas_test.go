package dm

import (
	"errors"
	"testing"
)

func TestSaveCASHappyPath(t *testing.T) {
	dir := t.TempDir()
	base := &Keyring{Revision: 5, Epochs: []Epoch{{ID: 0, Kind: EpochKindBootstrap}}, Current: 0}
	if err := base.Save(dir); err != nil {
		t.Fatal(err)
	}
	// Read at 5, mutate to 6, CAS expecting 5 → writes.
	k, err := LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	k.Revision = 6
	if err := k.SaveCAS(dir, 5); err != nil {
		t.Fatalf("CAS should succeed when disk still at expected: %v", err)
	}
	got, _ := LoadKeyring(dir)
	if got.Revision != 6 {
		t.Errorf("revision = %d, want 6", got.Revision)
	}
}

func TestSaveCASConflictDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	(&Keyring{Revision: 6, Epochs: []Epoch{{ID: 0, Kind: EpochKindBootstrap}}, Current: 0}).Save(dir)

	// Our session read at 6 and mutated to 7.
	mine, _ := LoadKeyring(dir)
	mine.Revision = 7

	// A concurrent writer advances disk to 7 first.
	concurrent, _ := LoadKeyring(dir)
	concurrent.Revision = 7
	concurrent.Epochs = append(concurrent.Epochs, Epoch{ID: 1, Kind: EpochKindPassword})
	if err := concurrent.Save(dir); err != nil {
		t.Fatal(err)
	}

	// Our CAS expecting 6 must now conflict and write nothing.
	if err := mine.SaveCAS(dir, 6); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("want ErrRevisionConflict, got %v", err)
	}
	final, _ := LoadKeyring(dir)
	if final.Revision != 7 || len(final.Epochs) != 2 {
		t.Errorf("failed CAS must not clobber disk: revision=%d epochs=%d (want 7/2)", final.Revision, len(final.Epochs))
	}
}

func TestSaveCASFirstWrite(t *testing.T) {
	dir := t.TempDir()
	k := &Keyring{Revision: 0}
	if err := k.SaveCAS(dir, 0); err != nil {
		t.Fatalf("first write (no file, expected 0) should succeed: %v", err)
	}
	// But claiming a prior revision when no file exists is a conflict.
	dir2 := t.TempDir()
	if err := (&Keyring{Revision: 3}).SaveCAS(dir2, 2); !errors.Is(err, ErrRevisionConflict) {
		t.Errorf("expected-nonzero with no file should conflict, got %v", err)
	}
}
