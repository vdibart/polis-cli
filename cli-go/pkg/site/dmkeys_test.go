package site

import (
	"encoding/json"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestProvisionAndPublishMessagesKey(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	// Minimal well-known with just the identity key (as init would have written).
	if err := SaveWellKnownRaw(siteDir, map[string]interface{}{"public_key": string(pubSSH)}); err != nil {
		t.Fatal(err)
	}

	if err := ProvisionAndPublishMessagesKey(siteDir, privPEM); err != nil {
		t.Fatalf("provision+publish: %v", err)
	}

	// Keyring was provisioned with the bootstrap epoch.
	kr, err := dm.LoadKeyring(dm.DMDir(siteDir))
	if err != nil {
		t.Fatalf("load keyring: %v", err)
	}
	if len(kr.Epochs) != 1 || kr.Epochs[0].Kind != dm.EpochKindBootstrap {
		t.Fatalf("expected one bootstrap epoch, got %+v", kr.Epochs)
	}

	// The well-known now carries a public_key_messages block that verifies against the
	// identity key — exactly what a sender checks before encrypting.
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil {
		t.Fatal(err)
	}
	blockJSON, err := json.Marshal(raw["public_key_messages"])
	if err != nil {
		t.Fatal(err)
	}
	var block dm.MessagesKeyBlock
	if err := json.Unmarshal(blockJSON, &block); err != nil {
		t.Fatalf("unmarshal block: %v", err)
	}
	if ok, err := dm.VerifyMessagesKeyEntry(block.Current, pubSSH); err != nil || !ok {
		t.Errorf("published block should verify against the identity key: ok=%v err=%v", ok, err)
	}
	if block.Current.Epoch != 0 {
		t.Errorf("current epoch = %d, want 0 (bootstrap)", block.Current.Epoch)
	}

	// Idempotent: provisioning again keeps the same bootstrap key.
	wantKey := block.Current.Key
	if err := ProvisionAndPublishMessagesKey(siteDir, privPEM); err != nil {
		t.Fatal(err)
	}
	raw2, _ := LoadWellKnownRaw(siteDir)
	blockJSON2, _ := json.Marshal(raw2["public_key_messages"])
	var block2 dm.MessagesKeyBlock
	json.Unmarshal(blockJSON2, &block2)
	if block2.Current.Key != wantKey {
		t.Error("re-provision must not mint a new bootstrap key")
	}
}

// Identity rotation (phase 2.6): re-publishing with a new identity key re-signs the block
// so it verifies under the new key and not the old one — the messages key is unchanged.
func TestPublishMessagesKey_ResignsUnderNewIdentity(t *testing.T) {
	siteDir := t.TempDir()
	oldPriv, oldPub, _ := signing.GenerateKeypair()
	if err := SaveWellKnownRaw(siteDir, map[string]interface{}{"public_key": string(oldPub)}); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionAndPublishMessagesKey(siteDir, oldPriv); err != nil {
		t.Fatal(err)
	}

	readBlock := func() dm.MessagesKeyBlock {
		raw, _ := LoadWellKnownRaw(siteDir)
		b, _ := json.Marshal(raw["public_key_messages"])
		var block dm.MessagesKeyBlock
		json.Unmarshal(b, &block)
		return block
	}
	before := readBlock()

	// Rotate: new identity key, re-sign the block (what rotate_key does).
	newPriv, newPub, _ := signing.GenerateKeypair()
	if err := PublishMessagesKey(siteDir, newPriv); err != nil {
		t.Fatalf("re-sign: %v", err)
	}
	after := readBlock()

	// The messages key is unchanged; only the signature is refreshed.
	if after.Current.Key != before.Current.Key {
		t.Error("messages key must not change on identity rotation")
	}
	if ok, _ := dm.VerifyMessagesKeyEntry(after.Current, newPub); !ok {
		t.Error("re-signed block should verify under the new identity key")
	}
	if ok, _ := dm.VerifyMessagesKeyEntry(after.Current, oldPub); ok {
		t.Error("re-signed block must NOT verify under the old identity key")
	}
}

// TestPublishMessagesKey_PreservesOtherFields guards the Option-A repair path:
// re-publishing the messages key must NOT disturb other well-known fields —
// notably a custom author_name (mayoinmotion's Acoorn identifier string). It
// uses the field-preserving raw write, so the repair restores DMs without
// touching the operator's intentional site-name edit.
func TestPublishMessagesKey_PreservesOtherFields(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	const acoorn = "mayoinmotion (X:115.73440a760bf_Y:188.aa0b7a427df)"
	if err := SaveWellKnownRaw(siteDir, map[string]interface{}{
		"public_key":  string(pubSSH),
		"author_name": acoorn,
		"site_title":  "Polis Lounge",
	}); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionAndPublishMessagesKey(siteDir, privPEM); err != nil {
		t.Fatalf("provision+publish: %v", err)
	}
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["public_key_messages"]; !ok {
		t.Fatal("publish did not add public_key_messages")
	}
	if raw["author_name"] != acoorn {
		t.Errorf("author_name (Acoorn string) was disturbed by republish: %v", raw["author_name"])
	}
	if raw["site_title"] != "Polis Lounge" {
		t.Errorf("site_title disturbed: %v", raw["site_title"])
	}
}
