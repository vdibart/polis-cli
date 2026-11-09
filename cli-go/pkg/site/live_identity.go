package site

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ErrLiveIdentityConflict is returned when publishing would replace the
// identity readers see with one the site's own history does not name.
var ErrLiveIdentityConflict = fmt.Errorf("publishing would replace the site's live identity key")

// CheckLiveIdentity decides whether a publish may replace the live
// .well-known/polis with the store's. source is the store's document and live
// the one readers are served now; nil means that one is absent.
//
// ⛔⛔ A WRONG KEY NEVER REACHES READERS. A store that is empty or replaced
// beside a live site (a credential pointing at a fresh store, a lost store)
// mints a new identity on first start; published, it would replace the real
// one where every reader and the discovery service sees it. So the key the
// live site publishes must be the store's current key or one its own key
// history names, which is exactly what a deliberate rotation leaves: the old
// key moves into public_key_history and the new one becomes current.
//
// ⭐ It is checked at publish, never at key generation: the publish is the one
// place a new identity reaches readers, and checking at start would have the
// instance fetch its own site while starting.
//
// ⛔ An error names the documents, never a key.
func CheckLiveIdentity(source, live []byte) error {
	liveKey, err := docKey(live, "the live ")
	if err != nil {
		return err
	}
	if liveKey == nil {
		return nil // nothing published yet, or a site that publishes no key
	}
	known, err := docKeys(source)
	if err != nil {
		return err
	}
	if len(known) == 0 {
		return fmt.Errorf("%w: the live %s publishes an identity key and the store's %s publishes none; "+
			"the store looks empty or replaced — restore it rather than publishing over the live site", ErrLiveIdentityConflict, WellKnownKey, WellKnownKey)
	}
	for _, k := range known {
		if bytes.Equal(k, liveKey) {
			return nil
		}
	}
	return fmt.Errorf("%w: the live %s publishes an identity key that is neither the store's current key nor in its "+
		"key history — the store looks replaced; restore it (or point the instance at the right store) rather than "+
		"publishing over the live site", ErrLiveIdentityConflict, WellKnownKey)
}

// docKey reads the public_key of a .well-known/polis; nil when the document
// is absent or names none.
func docKey(doc []byte, which string) (ed25519.PublicKey, error) {
	if doc == nil {
		return nil, nil
	}
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(doc, &wk); err != nil {
		return nil, fmt.Errorf("%s%s does not parse, so whether it publishes an identity is unknown", which, WellKnownKey)
	}
	if strings.TrimSpace(wk.PublicKey) == "" {
		return nil, nil
	}
	k, err := signing.ParsePublicKey([]byte(wk.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("%s%s names an identity key that does not parse", which, WellKnownKey)
	}
	return k, nil
}

// docKeys is every identity key a .well-known/polis names as the site's: its
// public_key and each key of its public_key_history.
func docKeys(doc []byte) ([]ed25519.PublicKey, error) {
	cur, err := docKey(doc, "the store's ")
	if err != nil || doc == nil {
		return nil, err
	}
	var out []ed25519.PublicKey
	if cur != nil {
		out = append(out, cur)
	}
	block, err := KeyHistoryFromWellKnown(doc)
	if err != nil {
		return nil, fmt.Errorf("the store's %s has a key history that does not parse", WellKnownKey)
	}
	if block != nil {
		for _, s := range block.Keys() {
			// ⚠️ An entry that does not parse is skipped, not fatal: it can
			// only fail to match, which refuses, never admits.
			if k, err := signing.ParsePublicKey([]byte(s)); err == nil {
				out = append(out, k)
			}
		}
	}
	return out, nil
}
