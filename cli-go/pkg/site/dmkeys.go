package site

import (
	"errors"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io/fs"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
)

// ProvisionDMKeyring ensures the tenant has a DM keyring with at least the bootstrap
// epoch — the primary deploy-upgrade / init action (Medic does the same on hosted). It is
// idempotent: an existing keyring is returned untouched. Returns the keyring so the caller
// can publish its messages-key block.
func ProvisionDMKeyring(site storage.Site) (*dm.Keyring, error) {
	kr, err := dm.LoadKeyring(site)
	if err == nil {
		return kr, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load keyring: %w", err)
	}
	kr = &dm.Keyring{}
	if _, err := kr.AddBootstrapEpoch(); err != nil {
		return nil, fmt.Errorf("provision bootstrap epoch: %w", err)
	}
	if err := kr.SaveNew(site); err != nil {
		if errors.Is(err, storage.ErrExists) {
			return dm.LoadKeyring(site)
		}
		return nil, fmt.Errorf("save keyring: %w", err)
	}
	return kr, nil
}

// PublishMessagesKey rebuilds the signed public_key_messages block from the tenant's
// keyring and writes it into .well-known/polis (preserving all other fields). Call after
// any keyring mutation (provision, set-password, rekey) and on identity rotation, which
// re-signs the block (task 2.6). identityPrivPEM signs each entry and MUST correspond to
// the well-known public_key, or senders will reject the block.
func PublishMessagesKey(site storage.Site, identityPrivPEM []byte) error {
	kr, err := dm.LoadKeyring(site)
	if err != nil {
		return fmt.Errorf("load keyring: %w", err)
	}
	block, err := dm.BuildMessagesKeyBlock(kr, identityPrivPEM)
	if err != nil {
		return fmt.Errorf("build messages-key block: %w", err)
	}
	raw, err := LoadWellKnownRaw(site)
	if err != nil {
		return fmt.Errorf("load well-known: %w", err)
	}
	if raw == nil {
		// LoadWellKnownRaw reports an absent file as (nil, nil); writing into
		// that map would panic rather than say what is wrong.
		return errNoWellKnown(site, " — there is no identity document to publish a messages key into")
	}
	raw["public_key_messages"] = block
	// Republishing the block is the one write that means to change it.
	if err := SaveWellKnownRaw(site, raw, ChangeMessagesKey); err != nil {
		return fmt.Errorf("save well-known: %w", err)
	}
	return nil
}

// ProvisionAndPublishMessagesKey is the convenience used at init / heal time: provision
// the keyring (if absent) and publish its block into the well-known.
func ProvisionAndPublishMessagesKey(site storage.Site, identityPrivPEM []byte) error {
	if _, err := ProvisionDMKeyring(site); err != nil {
		return err
	}
	return PublishMessagesKey(site, identityPrivPEM)
}

// RemoveMessagesKey deletes the public_key_messages block from
// .well-known/polis, and reports whether there was one. It is the named change
// for a site that is to receive no DMs under the block it publishes: a branch,
// whose DM keyring stays behind with the origin, so the block would advertise
// keys the new site does not hold. DMs are set up fresh there later.
//
// ⛔ Nothing else in the document changes, and the write names
// ChangeMessagesKey, the one protected member it means to drop.
func RemoveMessagesKey(site storage.Site) (bool, error) {
	raw, err := LoadWellKnownRaw(site)
	if err != nil {
		return false, fmt.Errorf("load well-known: %w", err)
	}
	if raw == nil {
		return false, errNoWellKnown(site, "")
	}
	if _, ok := raw["public_key_messages"]; !ok {
		return false, nil
	}
	delete(raw, "public_key_messages")
	if err := SaveWellKnownRaw(site, raw, ChangeMessagesKey); err != nil {
		return false, fmt.Errorf("save well-known: %w", err)
	}
	return true, nil
}
