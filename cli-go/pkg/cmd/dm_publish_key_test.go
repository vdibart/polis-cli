package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func newKeylessDMSite(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	dir := t.TempDir()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := site.SaveWellKnownRaw(storage.At(dir), map[string]interface{}{"public_key": string(pub)}); err != nil {
		t.Fatal(err)
	}
	return dir, priv, pub
}

func publishedBlockVerifies(t *testing.T, dir string, pub []byte) {
	t.Helper()
	raw, err := site.LoadWellKnownRaw(storage.At(dir))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(raw["public_key_messages"])
	var block dm.MessagesKeyBlock
	if err := json.Unmarshal(b, &block); err != nil {
		t.Fatalf("public_key_messages: %v", err)
	}
	if ok, err := dm.VerifyMessagesKeyEntry(block.Current, pub); err != nil || !ok {
		t.Errorf("published block does not verify against the site key: ok=%v err=%v", ok, err)
	}
}

// A site made before encrypted DMs has no keyring. publish-key provisions one
// (and the storage salt) rather than failing, so DMs can be switched on without
// a full Tailor run.
func TestDMPublishKeyCreatesAMissingKeyring(t *testing.T) {
	dir, priv, pub := newKeylessDMSite(t)

	created, err := publishMessagesKey(storage.At(dir), priv, false)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("created = false on a site with no keyring")
	}
	if _, err := dm.LoadKeyring(storage.At(dir)); err != nil {
		t.Fatalf("no keyring after publish-key: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dir, ".polis", "storage-salt")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("storage salt: %v, mode %v", err, info)
	}
	publishedBlockVerifies(t, dir, pub)
}

// An existing keyring is never replaced: the file is byte-identical afterwards.
func TestDMPublishKeyLeavesAnExistingKeyringAlone(t *testing.T) {
	dir, priv, pub := newKeylessDMSite(t)
	if err := site.ProvisionAndPublishMessagesKey(storage.At(dir), priv); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filepath.FromSlash(dm.DMDirKey), "keyring.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	created, err := publishMessagesKey(storage.At(dir), priv, false)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("created = true on a site that already had a keyring")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Error("keyring.json changed")
	}
	publishedBlockVerifies(t, dir, pub)
}

// No keyring here but a published messages key: the keyring is somewhere else,
// so publish-key refuses to mint a replacement unless told to.
func TestDMPublishKeyRefusesToReplaceAPublishedKeyWithoutCreate(t *testing.T) {
	dir, priv, pub := newKeylessDMSite(t)
	if err := site.ProvisionAndPublishMessagesKey(storage.At(dir), priv); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filepath.FromSlash(dm.DMDirKey), "keyring.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	wkBefore, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))

	if _, err := publishMessagesKey(storage.At(dir), priv, false); err != errKeyringElsewhere {
		t.Fatalf("err = %v, want errKeyringElsewhere", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a keyring was created without --create")
	}
	if wkAfter, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis")); !bytes.Equal(wkBefore, wkAfter) {
		t.Error(".well-known/polis changed on a refusal")
	}

	if created, err := publishMessagesKey(storage.At(dir), priv, true); err != nil || !created {
		t.Fatalf("with --create: created=%v err=%v", created, err)
	}
	publishedBlockVerifies(t, dir, pub)
}
