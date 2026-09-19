package site

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/did"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// SIGNET epic 32 D7 — TIER 1: the primitives are SUFFICIENT to project a
// did:webvh log.
//
// ⛔⛔ READ THIS BEFORE CITING THE TEST. Passing it does NOT mean polis supports
// did:webvh. There is no did:webvh output, command or resolver in polis, and
// there must not be one on the strength of this file. It means something
// smaller and more useful: every field a did:webvh log entry requires has a
// POPULATED SOURCE in what polis already publishes. "We could project into it"
// and "we support it" are different claims; only the first is true.
//
// ⭐ WHY IT EXISTS: it is a regression test for the projection rule. Someone who
// later reshapes public_key_history — drops valid_from, stops carrying witnesses,
// merges transition_sig away — fails HERE, with the did:webvh field they just
// made unreachable named in the message, instead of finding out years later.
//
// The fixture is a real site that really rotated once, through
// RecordKeyRotation, with a discovery-service witness over that rotation.

type webvhField struct {
	field  string // the did:webvh log-entry field
	source string // where polis carries it — the name a reader follows
	value  func() string
	note   string // what the projection must do, when it is not a copy
}

func TestTier1_EveryDidWebvhLogEntryFieldHasAPopulatedPolisSource(t *testing.T) {
	const host = "alice.polis.pub"
	k0, k1 := newKeypair(t), newKeypair(t)
	dsPriv, dsPubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	dsPub := strings.TrimSpace(string(dsPubSSH))

	dir := writeSite(t, k0.pub, "2026-03-03T05:35:09Z")
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	ts := "2026-09-15T10:22:03Z"
	canonical, _ := discovery.MakeKeyRotationCanonicalJSON(host, k0.pub, k1.pub, ts)
	transitionSig, err := signing.SignContent(canonical, k0.priv)
	if err != nil {
		t.Fatal(err)
	}
	w := discovery.Witness{
		Action: discovery.WitnessActionKeyRotation, Domain: host,
		OldKey: k0.pub, NewKey: k1.pub, Timestamp: ts, TransitionSig: transitionSig,
		DS: "https://ds.polis.pub", DSKeyID: "ds-primary", WitnessedAt: "2026-09-15T10:22:03.417Z",
	}
	wc, _ := w.Canonical()
	if w.Signature, err = signing.SignContent(wc, dsPriv); err != nil {
		t.Fatal(err)
	}
	if err := RecordKeyRotation(dir, k1.pub, transitionSig, ts, w); err != nil {
		t.Fatal(err)
	}

	block := mustLoad(t, dir)
	if err := VerifyChain(block, host); err != nil {
		t.Fatalf("fixture chain must verify: %v", err)
	}
	entries := block.Entries()
	if len(entries) != 2 {
		t.Fatalf("fixture must hold genesis + one rotation, got %d entries", len(entries))
	}

	// stateAt is the DID document at an epoch — the same projection
	// .well-known/did.json publishes, built for that epoch's key and the keys
	// retired before it.
	stateAt := func(i int) string {
		pub, err := signing.ParsePublicKey([]byte(entries[i].Key))
		if err != nil {
			t.Fatal(err)
		}
		var retired []did.RetiredKey
		for _, e := range entries[:i] {
			rp, _ := signing.ParsePublicKey([]byte(e.Key))
			retired = append(retired, did.RetiredKey{Epoch: e.Epoch, Key: rp})
		}
		doc, err := did.BuildWithHistory(host, pub, entries[i].Epoch, retired)
		if err != nil {
			t.Fatal(err)
		}
		return string(doc)
	}
	entryHash := func(i int) string {
		b, _ := json.Marshal(entries[i])
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}

	for i, e := range entries {
		i, e := i, e
		sig := func() string {
			if e.TransitionSig == nil {
				return ""
			}
			return *e.TransitionSig
		}

		fields := []webvhField{
			{"versionTime", "public_key_history[].valid_from", func() string { return e.ValidFrom }, "verbatim; already RFC 3339 UTC"},
			{"versionId", "public_key_history[].epoch (+1) and a hash of the entry", func() string {
				if e.Epoch < 0 || entryHash(i) == "" {
					return ""
				}
				return "populated"
			}, "version number is epoch+1; entryHash is computed at log creation"},
			{"state", ".well-known/did.json projected at this epoch (pkg/site BuildDIDDocument / pkg/did)", func() string { return stateAt(i) }, "already a published projection"},
			{"parameters.updateKeys", "public_key_history[].key", func() string { return e.Key }, "re-encoded from OpenSSH to multikey"},
			{"parameters.scid", "the genesis entry + genesis state (computable at log creation)", func() string {
				if i != 0 {
					return "inherited from genesis"
				}
				return stateAt(0) + e.Key
			}, "the {SCID} self-reference is computed over the first entry when the log is created"},
			{"parameters.portable", "ours to set in the log's first entry", func() string { return "true" }, "a projection choice, not a stored fact"},
		}
		if i == 0 {
			fields = append(fields, webvhField{"proof", "none for genesis — a did:webvh proof would be minted GOING FORWARD by the current key",
				func() string { return "not required from history" }, "⚠️ the one field that cannot be sourced for past entries"})
		} else {
			fields = append(fields,
				webvhField{"proof", "public_key_history[].transition_sig (the predecessor key over the handover)", sig,
					"polis's proof is an SSHSIG over the rotation message; a did:webvh eddsa-jcs-2022 proof must be re-minted going forward"},
				webvhField{"witness", "public_key_history[].witnesses (SIGNET epic 32 — the DS countersignature)", func() string {
					for _, x := range e.Witnesses {
						if x.VerifySignature(dsPub) == nil {
							return x.WitnessedAt
						}
					}
					return ""
				}, "⭐ testimony from the moment of rotation; the one field that could never be added later"},
			)
		}

		for _, f := range fields {
			if f.value() == "" {
				t.Errorf("epoch %d: did:webvh `%s` has NO POPULATED SOURCE — expected it from %s (%s). If you just reshaped the key history, you made did:webvh unreachable.",
					e.Epoch, f.field, f.source, f.note)
			}
		}
	}
}
