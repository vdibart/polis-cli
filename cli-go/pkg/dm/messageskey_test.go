package dm

import (
	"encoding/base64"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// keyringWithTwoEpochs returns a keyring at epoch 1 (bootstrap + password).
func keyringWithTwoEpochs(t *testing.T) *Keyring {
	t.Helper()
	k := &Keyring{}
	if _, err := k.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := k.SetPassword([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestBuildAndVerifyMessagesKeyBlock(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	kr := keyringWithTwoEpochs(t)

	block, err := BuildMessagesKeyBlock(kr, privPEM)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// current = epoch 1; history = [epoch 0].
	if block.Current.Epoch != 1 {
		t.Errorf("current epoch = %d, want 1", block.Current.Epoch)
	}
	if len(block.History) != 1 || block.History[0].Epoch != 0 {
		t.Fatalf("history = %+v, want [epoch 0]", block.History)
	}
	// Every entry verifies against the identity public key.
	for _, e := range append([]MessagesKeyEntry{block.Current}, block.History...) {
		ok, err := VerifyMessagesKeyEntry(e, pubSSH)
		if err != nil || !ok {
			t.Errorf("entry epoch %d should verify: ok=%v err=%v", e.Epoch, ok, err)
		}
	}
	// The current entry's key matches the keyring's current epoch pubkey.
	cur, _ := kr.CurrentEpoch()
	if block.Current.Key != cur.PublicKeyMessages {
		t.Error("current entry key != keyring current epoch pubkey")
	}
	x, err := block.Current.X25519()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := base64.StdEncoding.DecodeString(cur.PublicKeyMessages)
	if base64.StdEncoding.EncodeToString(x[:]) != base64.StdEncoding.EncodeToString(want) {
		t.Error("X25519() decode mismatch")
	}
}

func TestVerifyMessagesKeyEntryRejectsTamperAndWrongIdentity(t *testing.T) {
	privPEM, pubSSH, _ := signing.GenerateKeypair()
	kr := keyringWithTwoEpochs(t)
	block, err := BuildMessagesKeyBlock(kr, privPEM)
	if err != nil {
		t.Fatal(err)
	}

	// Tampered key → signature no longer matches.
	tampered := block.Current
	other, _, _ := NewEpochKeypair()
	tampered.Key = base64.StdEncoding.EncodeToString(other[:])
	if ok, _ := VerifyMessagesKeyEntry(tampered, pubSSH); ok {
		t.Error("tampered messages key should fail verification")
	}

	// A different identity key must not verify a real entry.
	_, otherPubSSH, _ := signing.GenerateKeypair()
	if ok, _ := VerifyMessagesKeyEntry(block.Current, otherPubSSH); ok {
		t.Error("entry should not verify against a different identity key")
	}
}

func TestMessagesKeyBlockEpochEntry(t *testing.T) {
	privPEM, _, _ := signing.GenerateKeypair()
	block, err := BuildMessagesKeyBlock(keyringWithTwoEpochs(t), privPEM)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := block.EpochEntry(0); !ok {
		t.Error("epoch 0 should be found in history")
	}
	if _, ok := block.EpochEntry(1); !ok {
		t.Error("epoch 1 should be the current entry")
	}
	if _, ok := block.EpochEntry(99); ok {
		t.Error("absent epoch should not be found")
	}
}
