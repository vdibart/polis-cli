package sitecheck

import (
	"encoding/json"
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
)

// noMessagesKey is the reason both forms give for a site that publishes no DM
// messages key.
const noMessagesKey = "no public_key_messages in .well-known/polis — this site cannot receive encrypted direct messages. Absence is honest on a site that never enabled them; on a hosted site Medic provisions the block from the tenant's existing keyring"

// MessagesKeyStatus checks the public_key_messages block in a .well-known/polis
// document: present reports whether there is one at all, and st whether every
// entry carries a valid identity-key signature.
//
// ⭐ ONE PREDICATE, THREE ENVELOPES, like the key-history checks: Judge sweeps
// it from disk (item 8), `polis validate <dir>` and `polis validate <url>`
// report it per site. Before this only Judge
// looked, so a Go writer that dropped the block went unnoticed by the one
// command a user runs.
//
// A forged or stripped signature means someone swapped the DM messages key, and
// senders would encrypt to a key the attacker controls. It verifies against the
// CURRENT identity key, so a rotation that re-signs the block passes.
func MessagesKeyStatus(wellKnown []byte) (present bool, st CheckStatus) {
	var wk struct {
		PublicKey         string               `json:"public_key"`
		PublicKeyMessages *dm.MessagesKeyBlock `json:"public_key_messages"`
	}
	if err := json.Unmarshal(wellKnown, &wk); err != nil {
		return false, CheckStatus{OK: false, Message: "unparseable .well-known/polis"}
	}
	if wk.PublicKeyMessages == nil {
		return false, CheckStatus{OK: true, Message: "no public_key_messages (pre-DM-encryption or mid-upgrade)"}
	}
	if wk.PublicKey == "" {
		return true, CheckStatus{OK: false, Message: "public_key_messages present but no public_key to verify against"}
	}
	pub := []byte(wk.PublicKey)
	entries := append([]dm.MessagesKeyEntry{wk.PublicKeyMessages.Current}, wk.PublicKeyMessages.History...)
	for _, e := range entries {
		ok, err := dm.VerifyMessagesKeyEntry(e, pub)
		if err != nil || !ok {
			return true, CheckStatus{OK: false, Message: fmt.Sprintf("public_key_messages epoch %d signature does not verify against the identity key", e.Epoch)}
		}
	}
	return true, CheckStatus{OK: true, Message: fmt.Sprintf("DM messages key published and signed by the identity key (%d epoch(s) checked)", len(entries))}
}

// reportMessagesKey records identity.messages_key for either form.
func reportMessagesKey(r *Report, wellKnown []byte) {
	present, st := MessagesKeyStatus(wellKnown)
	if !present && st.OK {
		r.na("identity.messages_key", FamilyIdentity, noMessagesKey)
		return
	}
	r.fromStatus("identity.messages_key", FamilyIdentity, st)
}
