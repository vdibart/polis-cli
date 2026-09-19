package site

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/jsonextra"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// The site's own key history, published inside .well-known/polis as
// `public_key_history`.
//
// WHY IT EXISTS. A site publishes one `public_key`. Rotate it and every
// artifact signed under the old key — posts, comments, following.json,
// blessed.json, every attestation — stops verifying against what the site
// publishes. The evidence needed to verify them existed only in the discovery
// service's ds_key_history table, so the permanence of an identity routed
// through one central database. That makes "signed → portable and permanent"
// false at exactly the moment it matters most, because a rotation is the
// security escape hatch and must not cost you your history.
//
// ⛔ THE CHAIN SELF-VERIFIES; THE DS IS A WITNESS, NOT A GATE.
// Each entry after genesis carries a `transition_sig` — the OLD key signing the
// handover to the new one — so a reader walks from the current key back to
// genesis with no service in the loop. What the chain alone cannot catch is
// OMISSION (you hold all your own old keys, so you can build a valid chain that
// skips one), BACKDATING (the timestamp is inside the signature, but you chose
// it) and GENESIS (nothing signs for the first key). The DS answers all three
// because it is append-only and third-party. It can prove a published chain
// wrong; it is never asked for permission.
//
// ⚠️ Publishing without verifying would be WORSE than not publishing: a site
// serving a history nobody checks has moved the data off the DS and lost the
// guarantee. VerifyChain and the DS reconciliation ship with this, not after.

// KeyHistoryEntry is one key the site has held.
//
// ⛔ THE JSON SHAPE IS THE PUBLISHED WIRE FORMAT. Do not add omitempty to
// TransitionSig and do not change a field name — strangers' verifiers read
// this. See docs/signet/spec/key-history.md.
type KeyHistoryEntry struct {
	// Epoch orders the chain: 0 is genesis, and each rotation adds one.
	// Explicit rather than inferred from array position, matching
	// public_key_messages.
	Epoch int `json:"epoch"`

	// Key is the OpenSSH-format public key, byte-identical to what
	// .well-known/polis's `public_key` held while this key was current. Same
	// representation as public_key so head-vs-published is a string compare and
	// never a conversion. (public_key_messages uses raw base64; that is a
	// different key and not the model here.)
	Key string `json:"key"`

	// ValidFrom DOES DOUBLE DUTY: it is the validity boundary AND the signed
	// timestamp.
	//
	// ⛔ IT MUST STAY THE BYTE-IDENTICAL STRING THAT WENT INTO THE CANONICAL
	// ROTATION MESSAGE. Normalise the zone, drop the Z, round a fraction, or
	// re-render it through time.Time and every transition signature in the chain
	// stops verifying. Nothing in this package parses it as a time for that
	// reason; it is carried, compared and signed as a string.
	ValidFrom string `json:"valid_from"`

	// ValidUntil is when this key stopped being current. Absent on `current` —
	// being current IS the absence of an end.
	ValidUntil string `json:"valid_until,omitempty"`

	// TransitionSig is the PREDECESSOR key's signature over the canonical
	// rotation message that handed authority to this key.
	//
	// ⛔ NULL ON GENESIS, AND THAT IS HONEST, NOT MISSING. Nothing signed for
	// the first key; ds_key_history models exactly this. A pointer with no
	// omitempty so it marshals as `null` rather than vanishing — an absent field
	// and an explicit null are different claims.
	TransitionSig *string `json:"transition_sig"`

	// Witnesses are discovery-service countersignatures over the rotation that
	// began this epoch (SIGNET epic 32) — the independent date the chain cannot
	// supply for itself, carried beside the entry it witnesses.
	//
	// ⛔ NOT PART OF ANY SIGNATURE. transition_sig covers the fixed five-field
	// rotation message and nothing here changes it. ⛔ omitempty ON PURPOSE: a
	// site that never rotated, or rotated before witnessing, publishes a
	// byte-identical document. Absent means unwitnessed, never invalid.
	Witnesses []discovery.Witness `json:"witnesses,omitempty"`

	// Extra holds members of this entry this build does not model. A rotation
	// carries them through untouched (see KeyHistoryBlock.Extra).
	Extra map[string]json.RawMessage `json:"-"`
}

// KeyHistoryBlock is the `public_key_history` value in .well-known/polis:
// the current key plus every key before it.
//
// ⛔ NO DOCUMENT-LEVEL SIGNATURE, DELIBERATELY. Signing the history with the
// current key is circular — the chain exists to establish which key is
// authoritative. The per-entry transition_sig is the right proof and both
// rotation paths already produce it: each entry is signed by the key it
// succeeds, so the chain proves itself link by link. public_key_messages works
// exactly this way. And .well-known/polis being unsigned is a FEATURE here: it
// is the trust root, verified by fetching it from the domain over TLS, which is
// the same act that establishes the domain is the domain.
type KeyHistoryBlock struct {
	Current KeyHistoryEntry `json:"current"`

	// History is every retired key, OLDEST FIRST, so History[len-1] is the key
	// Current succeeded.
	//
	// ⚠️ THIS IS THE ONE PLACE THE SHAPE DELIBERATELY DIVERGES FROM
	// public_key_messages, WHICH SORTS NEWEST FIRST. Two reasons, and both are
	// about the readers: the verifier walk is defined as "current.transition_sig
	// verifies against history[last].key", and ds_key_history is served ordered
	// by valid_from ASC — so Clerk's parity check zips the two directly instead
	// of reversing one of them first. Reordering this breaks both.
	//
	// ⛔ No omitempty, and never nil on the wire: an empty `history: []` is a
	// meaningful, honest state (a site that has never rotated), not a missing
	// field.
	History []KeyHistoryEntry `json:"history"`

	// Extra holds members of the block this build does not model.
	//
	// ⛔ A CHAIN IS APPEND-ONLY AND IS NEVER REBUILT, and a rotation decodes it
	// into these types and writes it back — so without Extra, here and on each
	// entry, a rotation silently altered every entry a newer build had written
	// to. transition_sig covers the fixed five-field rotation message, never
	// the entry JSON, so carrying a member through changes nothing any
	// signature covers. (Witness members ride in discovery.Witness.Extra.)
	//
	// ⛔ Encoded with jsonextra.Marshal: declared members in DECLARATION order,
	// which is what this writer has always emitted — never MarshalSorted, which
	// would reorder the published chain of every site that has rotated.
	Extra map[string]json.RawMessage `json:"-"`
}

type keyHistoryEntryFields KeyHistoryEntry
type keyHistoryBlockFields KeyHistoryBlock

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (e *KeyHistoryEntry) UnmarshalJSON(data []byte) error {
	f := keyHistoryEntryFields(*e)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*e = KeyHistoryEntry(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (e KeyHistoryEntry) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(keyHistoryEntryFields(e), e.Extra)
}

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (b *KeyHistoryBlock) UnmarshalJSON(data []byte) error {
	f := keyHistoryBlockFields(*b)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*b = KeyHistoryBlock(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (b KeyHistoryBlock) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(keyHistoryBlockFields(b), b.Extra)
}

// keyHistoryField is the .well-known/polis key this block lives under.
const keyHistoryField = "public_key_history"

// Predecessor returns the key Current succeeded, and whether there is one.
func (b *KeyHistoryBlock) Predecessor() (KeyHistoryEntry, bool) {
	if len(b.History) == 0 {
		return KeyHistoryEntry{}, false
	}
	return b.History[len(b.History)-1], true
}

// Entries returns the whole chain oldest-first, genesis through current.
func (b *KeyHistoryBlock) Entries() []KeyHistoryEntry {
	out := make([]KeyHistoryEntry, 0, len(b.History)+1)
	out = append(out, b.History...)
	return append(out, b.Current)
}

// Keys returns just the keys, oldest-first — the sequence a parity check
// compares against the DS's witnessed one.
func (b *KeyHistoryBlock) Keys() []string {
	entries := b.Entries()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Key)
	}
	return out
}

// LoadKeyHistory reads a site's published key history.
//
// Returns (nil, nil) when the site publishes none. That is a real state — every
// site was in it until this shipped — and it is not an error.
func LoadKeyHistory(siteDir string) (*KeyHistoryBlock, error) {
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil {
		return nil, err
	}
	return keyHistoryFromRaw(raw)
}

// KeyHistoryFromWellKnown parses the block out of already-fetched
// .well-known/polis bytes. The same predicate for a caller that has the
// document over HTTP rather than on disk.
func KeyHistoryFromWellKnown(data []byte) (*KeyHistoryBlock, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return keyHistoryFromRaw(raw)
}

func keyHistoryFromRaw(raw map[string]interface{}) (*KeyHistoryBlock, error) {
	if raw == nil {
		return nil, nil
	}
	v, ok := raw[keyHistoryField]
	if !ok || v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var block KeyHistoryBlock
	if err := json.Unmarshal(b, &block); err != nil {
		return nil, fmt.Errorf("%s does not parse: %w", keyHistoryField, err)
	}
	if block.History == nil {
		block.History = []KeyHistoryEntry{}
	}
	return &block, nil
}

// saveKeyHistory writes the block into .well-known/polis without disturbing any
// other field. Goes through the raw map for the same reason PublishMessagesKey
// does: the WellKnown struct does not model this block, and a
// load-struct/mutate/save round-trip would erase it.
//
// allow names the protected members this write means to change (see
// SaveWellKnownRaw): a rotation moves the key and the chain; genesis only adds.
func saveKeyHistory(siteDir string, block *KeyHistoryBlock, extra map[string]interface{}, allow ...IdentityChange) error {
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil {
		return fmt.Errorf("load well-known: %w", err)
	}
	if raw == nil {
		return fmt.Errorf(".well-known/polis not found in %s", siteDir)
	}
	if block.History == nil {
		block.History = []KeyHistoryEntry{}
	}
	raw[keyHistoryField] = block
	for k, v := range extra {
		raw[k] = v
	}
	return SaveWellKnownRaw(siteDir, raw, allow...)
}

// GenesisKeyHistory builds the one-entry chain for a site that has never
// rotated: the key it publishes now, valid from the moment it was created.
//
// ⭐ NOTHING HERE COMES FROM ANYWHERE BUT THE SITE'S OWN DOCUMENT. publicKey is
// .well-known/polis's `public_key`; created is its `created`. The genesis entry
// therefore says only what the site already says, in a second place — which is
// what makes it safe for an actor to write unasked, the same line that lets
// Medic publish did.json and forbids it from ever writing license.json.
//
// transition_sig is null because genesis has no predecessor. That is the honest
// value, not a placeholder.
func GenesisKeyHistory(publicKey, created string) *KeyHistoryBlock {
	return &KeyHistoryBlock{
		Current: KeyHistoryEntry{
			Epoch:         0,
			Key:           publicKey,
			ValidFrom:     created,
			TransitionSig: nil,
		},
		History: []KeyHistoryEntry{},
	}
}

// WriteGenesisKeyHistory publishes the genesis entry for a site that has none,
// built from that site's own public_key and created fields.
//
// ⛔ IT REFUSES TO OVERWRITE. If the site already publishes a history, this is a
// no-op returning false: a chain is append-only, and rebuilding one from the
// current key would silently discard every rotation it records.
//
// ⛔ IT DOES NOT CONSULT THE DS, AND THE CALLER MUST. C's one weakness is that
// it ASSUMES the current key is the genesis key — true for every tenant today,
// not provable from the site alone. A caller healing an existing site must ask
// the DS first and skip a domain the DS reports more than one key for; see
// medic.healKeyHistory. A caller creating a brand-new site (polis init) needs no
// such check, because a key generated seconds ago has no history to contradict.
func WriteGenesisKeyHistory(siteDir string) (bool, error) {
	existing, err := LoadKeyHistory(siteDir)
	if err != nil {
		return false, err
	}
	if existing != nil {
		return false, nil
	}
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return false, fmt.Errorf("load well-known: %w", err)
	}
	if wk.PublicKey == "" {
		return false, fmt.Errorf("no public_key in .well-known/polis")
	}
	if wk.Created == "" {
		// Without `created` there is no honest valid_from. Inventing one — now,
		// say — would assert a validity boundary the author never stated and
		// would be wrong for every artifact signed before it.
		return false, fmt.Errorf("no created timestamp in .well-known/polis to date the genesis key from")
	}
	if err := saveKeyHistory(siteDir, GenesisKeyHistory(wk.PublicKey, wk.Created), nil); err != nil {
		return false, err
	}
	return true, nil
}

// RecordKeyRotation is THE SEAM. It updates .well-known/polis's `public_key`
// and appends to `public_key_history` in ONE write.
//
// ⛔ ROTATION IS IMPLEMENTED TWICE — cli-go/pkg/cmd/rotate_key.go and
// webapp/internal/server/handlers.go are separate, both complete
// implementations of the same operation. A chain appended in one and not the
// other is a site whose history has holes, and a published key that disagrees
// with its own chain. So neither path is allowed to touch either field
// directly: they both call this, and the two facts cannot drift apart because
// they are one function call. (Same move epic 28 made with
// site.WithdrawLicense.)
//
// ⚠️ validFrom MUST BE THE EXACT STRING that went into the canonical rotation
// message signed by the old key — the same value sent to the DS as `timestamp`.
// It is stored verbatim and never reformatted; see KeyHistoryEntry.ValidFrom.
//
// If the site publishes no history yet, genesis is synthesised from the key
// being retired and the site's `created`, so a rotation never produces a chain
// that starts in the middle. ⚠️ That synthesis assumes the retired key was the
// first — normally already established by the guarded provisioning in Medic, and
// if it is ever wrong the chain is incomplete rather than invalid, which is
// precisely the omission case the DS parity check exists to catch.
//
// witnesses are the discovery service's countersignatures over this rotation,
// when it returned any (SIGNET epic 32). They ride inside the new entry. None is
// fine: an unwitnessed rotation is a weaker claim, not an invalid one.
func RecordKeyRotation(siteDir, newPublicKey, transitionSig, validFrom string, witnesses ...discovery.Witness) error {
	if newPublicKey == "" {
		return fmt.Errorf("no new public key to record")
	}
	if transitionSig == "" {
		return fmt.Errorf("no transition signature to record")
	}
	if validFrom == "" {
		return fmt.Errorf("no rotation timestamp to record")
	}

	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return fmt.Errorf("load well-known: %w", err)
	}
	oldPublicKey := wk.PublicKey
	if oldPublicKey == "" {
		return fmt.Errorf("no public_key in .well-known/polis to rotate away from")
	}

	block, err := LoadKeyHistory(siteDir)
	if err != nil {
		return err
	}
	if block == nil {
		if wk.Created == "" {
			return fmt.Errorf("no key history and no created timestamp in .well-known/polis to date the genesis key from")
		}
		block = GenesisKeyHistory(oldPublicKey, wk.Created)
	}

	retired := block.Current
	retired.ValidUntil = validFrom
	sig := transitionSig
	next := KeyHistoryEntry{
		Epoch:         retired.Epoch + 1,
		Key:           newPublicKey,
		ValidFrom:     validFrom,
		TransitionSig: &sig,
	}
	if len(witnesses) > 0 {
		next.Witnesses, _ = discovery.MergeWitnesses(nil, witnesses...)
	}

	block.History = append(block.History, retired)
	block.Current = next

	// public_key and the chain head move together, in one write, on purpose.
	return saveKeyHistory(siteDir, block, map[string]interface{}{"public_key": newPublicKey},
		ChangePublicKey, ChangeKeyHistory)
}

// ---------- verification ----------

// VerifyChain walks a published key history and reports the first thing wrong
// with it, or nil.
//
// ⭐ NO DISCOVERY SERVICE IS CONSULTED. This is the whole point: an artifact
// signed before a rotation is verifiable from the site alone, forever, by
// anyone. Each entry's transition_sig is checked against the key it succeeded,
// back to genesis.
//
// domain is needed because it is inside the canonical rotation message. When
// the chain has no rotations there is nothing signed and domain is ignored — so
// a genesis-only site (every site today) verifies without knowing its own host.
// A caller that cannot determine the domain for a chain that DOES have
// rotations must report that it could not check, never that the chain passed.
func VerifyChain(block *KeyHistoryBlock, domain string) error {
	if block == nil {
		return fmt.Errorf("no key history to verify")
	}
	entries := block.Entries()

	if entries[0].TransitionSig != nil {
		return fmt.Errorf("genesis (epoch %d) carries a transition_sig; nothing precedes it to have signed one", entries[0].Epoch)
	}
	if entries[0].Epoch != 0 {
		return fmt.Errorf("the chain starts at epoch %d rather than 0, so it is missing its beginning", entries[0].Epoch)
	}
	if block.Current.ValidUntil != "" {
		return fmt.Errorf("the current key carries a valid_until; being current is the absence of an end")
	}

	for i, e := range entries {
		if e.Key == "" {
			return fmt.Errorf("epoch %d publishes no key", e.Epoch)
		}
		if e.ValidFrom == "" {
			return fmt.Errorf("epoch %d publishes no valid_from", e.Epoch)
		}
		if i == 0 {
			continue
		}
		prev := entries[i-1]
		if e.Epoch != prev.Epoch+1 {
			return fmt.Errorf("epoch %d follows epoch %d — the chain skips an entry", e.Epoch, prev.Epoch)
		}
		if e.Key == prev.Key {
			return fmt.Errorf("epoch %d publishes the same key as epoch %d", e.Epoch, prev.Epoch)
		}
		if prev.ValidUntil != e.ValidFrom {
			return fmt.Errorf("epoch %d ends at %q but epoch %d begins at %q — the chain has a gap or an overlap",
				prev.Epoch, prev.ValidUntil, e.Epoch, e.ValidFrom)
		}
		if e.TransitionSig == nil || *e.TransitionSig == "" {
			return fmt.Errorf("epoch %d carries no transition_sig, so nothing shows epoch %d handed over to it", e.Epoch, prev.Epoch)
		}
		if domain == "" {
			return fmt.Errorf("the chain records a rotation but the site's domain is unknown, so the transition signature cannot be rebuilt")
		}
		// valid_from does double duty: the boundary AND the signed timestamp.
		canonical, err := discovery.MakeKeyRotationCanonicalJSON(domain, prev.Key, e.Key, e.ValidFrom)
		if err != nil {
			return fmt.Errorf("epoch %d: cannot rebuild the rotation message: %w", e.Epoch, err)
		}
		ok, err := signing.VerifySignature(canonical, []byte(prev.Key), *e.TransitionSig)
		if err != nil {
			return fmt.Errorf("epoch %d transition signature could not be checked: %w", e.Epoch, err)
		}
		if !ok {
			return fmt.Errorf("epoch %d transition signature does not verify against epoch %d's key — the handover is not attested by the key that held authority", e.Epoch, prev.Epoch)
		}
	}
	return nil
}

// KeyAt returns the key that was current when something was signed at `when`,
// and whether the chain covers that moment. It is the `key_at(t)` of
// docs/signet/spec/key-history.md §4, kept under that name so the spec and the
// code read the same.
//
// ⚠️ THE VERIFIERS CALL EntryAt, NOT THIS. KeyAt is a convenience
// projection that drops the epoch and the window, and epic 31's D4 needs both
// — so if you are here because a grep showed KeyAt with no callers, that is
// EXPECTED and not epic 31 all over again. The reader is
// sitecheck.VerifySignatureWithHistory, via EntryAt below.
//
// Comparison is lexicographic over the published strings rather than parsed
// times. Every timestamp in a chain is written by the rotation path in one
// format (RFC3339, UTC, `Z`, no fraction), so string order is time order — and
// parsing would invite exactly the normalisation that breaks the signatures.
func (b *KeyHistoryBlock) KeyAt(when string) (string, bool) {
	e, ok := b.EntryAt(when)
	return e.Key, ok
}

// EntryAt is KeyAt returning the whole entry, for a verifier that must say
// WHICH key verified something (epoch and window), not just that one did.
//
// ⛔ THE WALK LIVES HERE, ONCE. KeyAt is a projection of this. Recovering the
// epoch afterwards by looking the returned key up in the chain would be a second
// walk — and a wrong one, because nothing forbids a site rotating A → B → A, so
// a key does not name an epoch.
func (b *KeyHistoryBlock) EntryAt(when string) (KeyHistoryEntry, bool) {
	if b == nil || when == "" {
		return KeyHistoryEntry{}, false
	}
	for _, e := range b.Entries() {
		if when < e.ValidFrom {
			continue
		}
		if e.ValidUntil == "" || when < e.ValidUntil {
			return e, true
		}
	}
	return KeyHistoryEntry{}, false
}

// PublishedKeyHistoryPath is where the block lives, for messages that need to
// name it. It is inside the identity document rather than a file of its own:
// a verifier already opens .well-known/polis first, so the history costs no new
// pointer and no second fetch.
func PublishedKeyHistoryPath(siteDir string) string {
	return filepath.Join(siteDir, ".well-known", "polis")
}

// HasKeyHistory reports whether a site publishes a chain at all. Used by the
// actors to tell "not provisioned yet" from "provisioned and wrong", which are
// different events to whoever is watching.
func HasKeyHistory(siteDir string) bool {
	block, err := LoadKeyHistory(siteDir)
	if err != nil || block == nil {
		return false
	}
	_, statErr := os.Stat(PublishedKeyHistoryPath(siteDir))
	return statErr == nil
}
