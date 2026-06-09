package dm

import (
	"encoding/base64"
	"fmt"
	"sort"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// MessagesKeyEntry is one signed messages-key advertisement: an epoch's X25519 public
// key plus an identity-key signature over its canonical form. Senders verify the
// signature against the site's identity public_key before encrypting to `key`.
type MessagesKeyEntry struct {
	Epoch int    `json:"epoch"`
	Key   string `json:"key"` // base64 (std) X25519 public key
	Sig   string `json:"sig"` // identity-key signature over canonicalMessagesKey(epoch, key)
}

// MessagesKeyBlock is the `public_key_messages` block published in `.well-known/polis`:
// the current epoch's signed key plus signed history (older epochs, for stale-addressed
// mail). It is integrity-bound to the DS-attested identity *without* the signing key
// being able to decrypt anything.
type MessagesKeyBlock struct {
	Current MessagesKeyEntry   `json:"current"`
	History []MessagesKeyEntry `json:"history,omitempty"`
}

// canonicalMessagesKey is the stable byte string signed/verified for one entry. Stable
// across implementations — documented in the interop protocol spec (task 2.4); do not
// reorder or reformat once shipped.
func canonicalMessagesKey(epoch int, keyB64 string) []byte {
	return []byte(fmt.Sprintf("pub.polis.public_key_messages\nepoch=%d\nkey=%s", epoch, keyB64))
}

// BuildMessagesKeyBlock builds the signed block from a keyring, signing each epoch's
// public messages key with the identity private key (PEM). `current` is the keyring's
// current epoch; `history` is every other epoch, newest-id first. Signing a public key
// never touches private messages material, so this works uniformly for the server-held
// bootstrap epoch and password epochs alike (see "Signed public_key_messages
// distribution").
func BuildMessagesKeyBlock(kr *Keyring, identityPrivPEM []byte) (*MessagesKeyBlock, error) {
	if len(kr.Epochs) == 0 {
		return nil, fmt.Errorf("keyring has no epochs to advertise")
	}
	sign := func(e Epoch) (MessagesKeyEntry, error) {
		sig, err := signing.SignContent(canonicalMessagesKey(e.ID, e.PublicKeyMessages), identityPrivPEM)
		if err != nil {
			return MessagesKeyEntry{}, fmt.Errorf("sign messages key epoch %d: %w", e.ID, err)
		}
		return MessagesKeyEntry{Epoch: e.ID, Key: e.PublicKeyMessages, Sig: sig}, nil
	}

	cur, err := kr.CurrentEpoch()
	if err != nil {
		return nil, err
	}
	curEntry, err := sign(*cur)
	if err != nil {
		return nil, err
	}

	sorted := append([]Epoch(nil), kr.Epochs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID > sorted[j].ID })
	var history []MessagesKeyEntry
	for _, e := range sorted {
		if e.ID == cur.ID {
			continue
		}
		entry, err := sign(e)
		if err != nil {
			return nil, err
		}
		history = append(history, entry)
	}
	return &MessagesKeyBlock{Current: curEntry, History: history}, nil
}

// VerifyMessagesKeyEntry checks an entry's signature against the site's identity public
// key (OpenSSH). Senders MUST call this before encrypting to the entry's key.
func VerifyMessagesKeyEntry(e MessagesKeyEntry, identityPubSSH []byte) (bool, error) {
	return signing.VerifySignature(canonicalMessagesKey(e.Epoch, e.Key), identityPubSSH, e.Sig)
}

// X25519 decodes an entry's base64 messages key into a 32-byte X25519 public key.
func (e MessagesKeyEntry) X25519() ([32]byte, error) {
	var k [32]byte
	raw, err := base64.StdEncoding.DecodeString(e.Key)
	if err != nil {
		return k, fmt.Errorf("decode messages key (epoch %d): %w", e.Epoch, err)
	}
	if len(raw) != 32 {
		return k, fmt.Errorf("messages key (epoch %d) is %d bytes, want 32", e.Epoch, len(raw))
	}
	copy(k[:], raw)
	return k, nil
}

// EpochEntry returns the block entry for a given epoch id (current or history).
func (b *MessagesKeyBlock) EpochEntry(epoch int) (MessagesKeyEntry, bool) {
	if b.Current.Epoch == epoch {
		return b.Current, true
	}
	for _, e := range b.History {
		if e.Epoch == epoch {
			return e, true
		}
	}
	return MessagesKeyEntry{}, false
}

// VerifyBoxPub authenticates an incoming message's box_pub (DM-1): it must equal the
// sender's published messages key for senderEpoch, where that entry's signature verifies
// against the sender's identity public key. This binds the box-opening key to the
// DS-attested identity, so a third party who replays a signed deliver header (which does
// not cover the box) cannot attach an attacker-generated box_pub and impersonate the
// sender. Returns nil iff authentic. Enforce at RECEIVE time on incoming deliveries only —
// never at read time or on stored messages, whose box_pub may legitimately be an
// unpublished ephemeral key (bootstrap forward re-seal) or the recipient's own key
// (box-to-self).
func VerifyBoxPub(identityPubSSH []byte, block *MessagesKeyBlock, senderEpoch int, boxPub [32]byte) error {
	if block == nil {
		return fmt.Errorf("sender published no public_key_messages block")
	}
	entry, ok := block.EpochEntry(senderEpoch)
	if !ok {
		return fmt.Errorf("sender_epoch %d not in sender's published messages keys", senderEpoch)
	}
	valid, err := VerifyMessagesKeyEntry(entry, identityPubSSH)
	if err != nil {
		return fmt.Errorf("verify sender messages-key signature (epoch %d): %w", senderEpoch, err)
	}
	if !valid {
		return fmt.Errorf("sender messages-key signature invalid for epoch %d (key-swap?)", senderEpoch)
	}
	entryKey, err := entry.X25519()
	if err != nil {
		return fmt.Errorf("decode sender messages key (epoch %d): %w", senderEpoch, err)
	}
	if entryKey != boxPub {
		return fmt.Errorf("box_pub does not match sender's published messages key for epoch %d", senderEpoch)
	}
	return nil
}
