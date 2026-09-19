package sitecheck

import (
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Resolving a signature to the key that made it — SIGNET epic 31.
//
// Epic 16 published a key history so that an artifact signed before a rotation
// could be resolved to the key that was current when it was signed. The
// resolver (site.KeyHistoryBlock.KeyAt) shipped with no caller, so a rotation
// made every earlier post fail verification. This is the caller.
//
// ⛔ THREE RULES, and each one is a decision in the epic plan:
//
//   - D1 — the walk is site.KeyHistoryBlock.EntryAt (KeyAt's body). Nothing
//     here walks the chain a second time.
//   - D2 — CURRENT KEY FIRST. The chain is consulted only when the key the site
//     publishes now has already failed. Every artifact on the network today was
//     signed by a current key, so the common path must not move at all.
//   - D3/D4 — a retired-key pass is a DIFFERENT CLAIM from a current-key pass,
//     and the result says which. The time that selected the retired key is the
//     artifact's own `published:` — self-reported, inside the signature, and
//     checked by nothing here. So the claim is "verifies against the key that
//     was current at its CLAIMED signing time", never "is authentic".

// KeySource names which of a site's keys verified a signature.
type KeySource string

const (
	// KeyCurrent — the key the site publishes as `public_key` now.
	KeyCurrent KeySource = "current"
	// KeyRetired — a key the site's published history says was current at the
	// artifact's claimed signing time, and is not current any more.
	KeyRetired KeySource = "retired"
)

// KeyUsed reports WHICH key a valid signature verified against (epic 31 D4).
//
// ⛔ A boolean would be wrong: "verified against the current key" and "verified
// against a retired key at a claimed time" have different strength, and a
// consumer applying policy (Law 2) has to be able to tell them apart.
type KeyUsed struct {
	Source KeySource `json:"source"`

	// Epoch is the key's position in the site's published history. Nil when no
	// usable history was available — the site publishes none, or it did not
	// verify — so which epoch the current key is cannot honestly be stated.
	Epoch *int `json:"epoch"`

	// ClaimedSigningTime is the artifact's own `published:`, the moment the
	// history was asked about. Set only for a retired key, because only there did
	// the claim decide anything.
	ClaimedSigningTime string `json:"claimed_signing_time,omitempty"`

	// ValidFrom and ValidUntil are the retired key's window, verbatim from the
	// history.
	ValidFrom  string `json:"valid_from,omitempty"`
	ValidUntil string `json:"valid_until,omitempty"`
}

// Describe is the sentence a person reads when a retired key verified
// something. "" for a current-key pass, which needs no qualifying.
//
// ⚠️ The wording is D3's and is load-bearing: it says CLAIMED and it says what
// was not checked. A shorter sentence would read as a stronger claim.
func (k *KeyUsed) Describe() string {
	if k == nil || k.Source != KeyRetired {
		return ""
	}
	epoch := "?"
	if k.Epoch != nil {
		epoch = fmt.Sprint(*k.Epoch)
	}
	return fmt.Sprintf("verified against a RETIRED key (epoch %s, current %s → %s), the key the site's published history says was current at the artifact's CLAIMED signing time %s — not against the key the site publishes now. The signing time is the artifact's own claim and nothing here checks it.",
		epoch, k.ValidFrom, k.ValidUntil, k.ClaimedSigningTime)
}

// ResolvingChain returns the published key history a verifier may resolve
// retired keys from, or nil and the reason it may not.
//
// ⛔ AN UNVERIFIED CHAIN RESOLVES NOTHING. A history entry is only evidence that
// a key was authoritative because the key before it signed the handover; an
// entry whose handover does not verify is an unsupported assertion, and
// resolving a signature through it would launder that assertion into a pass.
// For the same reason the chain's head must be the key the site publishes —
// otherwise "current" and "retired" would not mean what they say.
//
// reason is "" when there is nothing to explain: either the chain is usable, or
// the site records no rotation (absent, or genesis only), so it has no retired
// key a verifier could have missed.
func ResolvingChain(block *site.KeyHistoryBlock, domain, publicKey string) (*site.KeyHistoryBlock, string) {
	if block == nil {
		return nil, ""
	}
	rotated := len(block.History) > 0
	explain := func(s string) string {
		if !rotated {
			return ""
		}
		return s
	}
	if err := site.VerifyChain(block, domain); err != nil {
		return nil, explain("the site's key history records a rotation but could not be used to resolve retired keys: " + err.Error())
	}
	if strings.TrimSpace(block.Current.Key) != strings.TrimSpace(publicKey) {
		return nil, explain("the site's key history records a rotation but could not be used to resolve retired keys: its head is not the key the site publishes")
	}
	return block, ""
}

// VerifySignatureWithHistory verifies signed bytes against a site's current key
// and, only if that fails, against the key its history resolves for the
// artifact's claimed signing time. The one rule, for every markdown verifier
// that has the history — pkg/sitecheck's local and remote forms, pkg/verify,
// and since SIGNET epic 42 V4 Judge (checks 1 and 6) and Patrol.
//
// chain must come from ResolvingChain; nil means current key only, which is
// exactly VerifyContent's behaviour. published is the artifact's `published:`.
//
// The walk itself is site.VerifyWithHistory (SIGNET epic 44), shared with the
// JSON family.
func VerifySignatureWithHistory(signed []byte, sshSig string, pubKey []byte, chain *site.KeyHistoryBlock, published string) (SignatureStatus, *KeyUsed, error) {
	ok, retired, err := site.VerifyWithHistory(func(key []byte) (bool, error) {
		return signing.VerifySignature(signed, key, sshSig)
	}, pubKey, chain, published, "published:")
	if !ok {
		return SigInvalid, nil, err
	}
	return SigValid, KeyUsedFor(chain, retired, published), nil
}

// KeyUsedFor describes the key site.VerifyWithHistory verified with: retired is
// the entry it returned, nil for the current key.
func KeyUsedFor(chain *site.KeyHistoryBlock, retired *site.KeyHistoryEntry, claimed string) *KeyUsed {
	if retired == nil {
		used := &KeyUsed{Source: KeyCurrent}
		if chain != nil {
			epoch := chain.Current.Epoch
			used.Epoch = &epoch
		}
		return used
	}
	epoch := retired.Epoch
	return &KeyUsed{
		Source:             KeyRetired,
		Epoch:              &epoch,
		ClaimedSigningTime: claimed,
		ValidFrom:          retired.ValidFrom,
		ValidUntil:         retired.ValidUntil,
	}
}

// signatureFinding is the line a report prints for an artifact whose signature
// did not verify. When the history was consulted the verifier's own reason says
// what was tried; when a rotated history could not be used, chainNote says why,
// so a failure after a rotation is never indistinguishable from tampering.
func signatureFinding(path string, res ContentResult, historyConsulted bool, chainNote string) string {
	switch {
	case historyConsulted && res.SigError != nil:
		// The verifier's reason already begins "does not verify against the
		// current key, and …".
		return path + ": signature " + res.SigError.Error()
	case chainNote != "":
		return path + ": signature does not verify against the current key — " + chainNote
	}
	return path + ": signature does not verify"
}

// verifiedDetail renders the census line, naming retired-key passes when there
// are any, because "12 verified" and "12 verified, 2 of them by a retired key"
// are different claims (D4).
func verifiedDetail(examined int, noun string, verified, retired, unsigned int) string {
	if retired == 0 {
		return fmt.Sprintf("%s examined: %d signature(s) verified, %d unsigned", plural(examined, noun), verified, unsigned)
	}
	return fmt.Sprintf("%s examined: %d signature(s) verified — %d of them against a RETIRED key, at a claimed signing time — %d unsigned", plural(examined, noun), verified, retired, unsigned)
}
