package dm

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// ErrMessagesLocked is returned when an operation needs an epoch's DEK that is not
// available server-side — i.e. a password epoch whose DEK is wrapped under the user's
// password and has not been unlocked (the SPA unlock supplies it in phase 4). Bootstrap
// epochs are never locked (their DEK is server-held in server_dek).
var ErrMessagesLocked = errors.New("messages locked — set/enter your password to send or read this epoch")

// AvailableDEKs returns the epoch DEKs the server can use without a password: the
// bootstrap epoch's server_dek. Password-epoch DEKs are absent (locked) until the SPA
// unlock injects them. Read paths pass this to Mailbox.ReadConversation; a message whose
// epoch is absent comes back Locked.
func AvailableDEKs(kr *Keyring) (map[int][32]byte, error) {
	deks := make(map[int][32]byte)
	for i := range kr.Epochs {
		dek, ok, err := kr.Epochs[i].ServerDEKBytes()
		if err != nil {
			return nil, err
		}
		if ok {
			deks[kr.Epochs[i].ID] = dek
		}
	}
	return deks, nil
}

// LoadAvailableDEKs is AvailableDEKs over a tenant's on-disk keyring. A missing keyring
// yields an empty set (nothing readable yet), not an error.
func LoadAvailableDEKs(siteDir string) (map[int][32]byte, error) {
	kr, err := LoadKeyring(DMDir(siteDir))
	if err != nil {
		if os.IsNotExist(err) {
			return map[int][32]byte{}, nil
		}
		return nil, fmt.Errorf("load keyring: %w", err)
	}
	return AvailableDEKs(kr)
}

// sealingKey holds the material to seal an outgoing message from the sender's current
// epoch: the epoch id, its public messages key, and the DEK (private scalar).
type sealingKey struct {
	epoch int
	pub   [32]byte
	dek   [32]byte
}

// loadCurrentSealing resolves the sender's current-epoch sealing material from the
// keyring. Bootstrap epoch → server_dek. Password epoch with no server-held DEK →
// ErrMessagesLocked (sending awaits the SPA unlock, phase 4).
func loadCurrentSealing(siteDir string) (sealingKey, error) {
	var sk sealingKey
	kr, err := LoadKeyring(DMDir(siteDir))
	if err != nil {
		return sk, fmt.Errorf("load keyring: %w", err)
	}
	cur, err := kr.CurrentEpoch()
	if err != nil {
		return sk, err
	}
	pubRaw, err := base64.StdEncoding.DecodeString(cur.PublicKeyMessages)
	if err != nil || len(pubRaw) != 32 {
		return sk, fmt.Errorf("bad current-epoch public key (epoch %d)", cur.ID)
	}
	dek, ok, err := cur.ServerDEKBytes()
	if err != nil {
		return sk, err
	}
	if !ok {
		return sk, ErrMessagesLocked
	}
	sk.epoch = cur.ID
	copy(sk.pub[:], pubRaw)
	sk.dek = dek
	return sk, nil
}
