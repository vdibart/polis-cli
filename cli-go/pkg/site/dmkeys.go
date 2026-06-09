package site

import (
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
)

// ProvisionDMKeyring ensures the tenant has a DM keyring with at least the bootstrap
// epoch — the primary deploy-upgrade / init action (Medic does the same on hosted). It is
// idempotent: an existing keyring is returned untouched. Returns the keyring so the caller
// can publish its messages-key block.
func ProvisionDMKeyring(siteDir string) (*dm.Keyring, error) {
	dmDir := dm.DMDir(siteDir)
	kr, err := dm.LoadKeyring(dmDir)
	if err == nil {
		return kr, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("load keyring: %w", err)
	}
	kr = &dm.Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		return nil, fmt.Errorf("provision bootstrap epoch: %w", err)
	}
	if err := kr.Save(dmDir); err != nil {
		return nil, fmt.Errorf("save keyring: %w", err)
	}
	return kr, nil
}

// PublishMessagesKey rebuilds the signed public_key_messages block from the tenant's
// keyring and writes it into .well-known/polis (preserving all other fields). Call after
// any keyring mutation (provision, set-password, rekey) and on identity rotation, which
// re-signs the block (task 2.6). identityPrivPEM signs each entry and MUST correspond to
// the well-known public_key, or senders will reject the block.
func PublishMessagesKey(siteDir string, identityPrivPEM []byte) error {
	kr, err := dm.LoadKeyring(dm.DMDir(siteDir))
	if err != nil {
		return fmt.Errorf("load keyring: %w", err)
	}
	block, err := dm.BuildMessagesKeyBlock(kr, identityPrivPEM)
	if err != nil {
		return fmt.Errorf("build messages-key block: %w", err)
	}
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil {
		return fmt.Errorf("load well-known: %w", err)
	}
	raw["public_key_messages"] = block
	if err := SaveWellKnownRaw(siteDir, raw); err != nil {
		return fmt.Errorf("save well-known: %w", err)
	}
	return nil
}

// ProvisionAndPublishMessagesKey is the convenience used at init / heal time: provision
// the keyring (if absent) and publish its block into the well-known.
func ProvisionAndPublishMessagesKey(siteDir string, identityPrivPEM []byte) error {
	if _, err := ProvisionDMKeyring(siteDir); err != nil {
		return err
	}
	return PublishMessagesKey(siteDir, identityPrivPEM)
}
