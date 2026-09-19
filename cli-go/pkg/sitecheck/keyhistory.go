package sitecheck

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Key-history checks, shared by Judge and `polis validate`.
//
// ⭐ ONE PREDICATE, THREE ENVELOPES — epic 20's D9 applied again. Judge sweeps a
// fleet from disk, `polis validate <dir>` inspects one directory, and
// `polis validate <url>` fetches over public HTTP. All three ask the same two
// questions and must get the same answers, so the questions live here and the
// callers only differ in how they got the bytes.
//
// ⛔ WHAT IS DELIBERATELY NOT HERE: the tenant-vs-DS comparison. That one needs
// a third party (Clerk, in the hosted service), and it is the only one of the three that
// catches omission and backdating. A caller that cannot run it must SAY SO
// rather than let a clean report imply it was covered.

// ChainValidity checks that a published key history proves itself: every
// transition signature verifies against the key it succeeded, back to genesis.
//
// ⭐ NO DISCOVERY SERVICE IS INVOLVED, WHICH IS THE POINT. This is the check
// that makes "verify this yourself, from my site, forever" true — an artifact
// signed before a rotation resolves to the key that was current when it was
// signed, from the site alone.
//
// domain is inside the canonical rotation message, so it is required to check a
// chain that HAS rotations. A genesis-only chain — every site today — has
// nothing signed and verifies without it. A caller that cannot determine the
// domain for a chain with rotations gets a failure saying exactly that, never a
// pass.
//
// A site with no chain at all is NOT a finding. Most sites published none until
// this shipped, absence is honest, and Medic provisions it. The caller reports
// that as not-applicable.
func ChainValidity(block *site.KeyHistoryBlock, domain string) CheckStatus {
	if block == nil {
		return CheckStatus{OK: true, Message: "no public_key_history published"}
	}
	if err := site.VerifyChain(block, domain); err != nil {
		return CheckStatus{OK: false, Message: err.Error()}
	}
	if len(block.History) == 0 {
		return CheckStatus{OK: true, Message: "genesis only — the site has never rotated, so there is no handover to verify"}
	}
	return CheckStatus{OK: true, Message: fmt.Sprintf(
		"%d key(s) checked; every handover is signed by the key that held authority, back to genesis",
		len(block.History)+1)}
}

// ChainErasure answers the one question absence alone cannot: was a chain that
// SHOULD be here erased? It needs no DS call.
//
// did.json PROJECTS the key history — a retired key stays in verificationMethod
// after it leaves assertionMethod — so a DID document naming two or more keys
// is on-disk proof the site has rotated. A rotated site with no
// public_key_history did not "never publish one"; it lost it. That is provable
// with no DS call, which is the point: only Clerk used to notice, by asking.
//
// ⚠️ ABSENCE ALONE STAYS OK. A never-rotated site legitimately publishes no
// history, and so does a site with no did.json. Only the contradiction fails.
func ChainErasure(block *site.KeyHistoryBlock, didDoc []byte) CheckStatus {
	if block != nil {
		return CheckStatus{OK: true}
	}
	if n := site.DIDDocumentKeyCount(didDoc); n >= 2 {
		return CheckStatus{OK: false, Message: fmt.Sprintf(
			"no public_key_history in .well-known/polis, but did.json names %d keys — this site has rotated, so its chain was erased. "+
				"A chain is never rebuilt: its entries carry signatures by keys that no longer exist. Restore .well-known/polis from a backup; "+
				"until then, everything signed before the last rotation cannot be verified from this site", n)}
	}
	return CheckStatus{OK: true, Message: "no public_key_history published"}
}

// ChainHead checks that the three places a polis site states its current key
// agree: the chain's head, `public_key`, and the DID document's
// assertionMethod.
//
// ⛔ REPORT, NEVER HEAL. If they disagree something went wrong during a
// rotation, and picking a winner is a judgment about which key is your identity
// — not Medic's call, not any actor's. Same line as license.json.
//
// didDoc may be nil: most sites publish no DID document and absence is honest.
// A did.json that is present and names a different key IS a finding, because a
// resolver reading it cannot tell a stale document from a good one.
func ChainHead(block *site.KeyHistoryBlock, publicKey string, didDoc []byte) CheckStatus {
	if block == nil {
		return CheckStatus{OK: true, Message: "no public_key_history published"}
	}
	head := block.Current.Key
	if head == "" {
		return CheckStatus{OK: false, Message: "public_key_history publishes no current key"}
	}
	if publicKey == "" {
		return CheckStatus{OK: false, Message: "no public_key in .well-known/polis to compare the chain head against"}
	}
	// Both are OpenSSH strings by construction, so this is a compare and never
	// a conversion — the reason the chain stores the key in public_key's format.
	if strings.TrimSpace(head) != strings.TrimSpace(publicKey) {
		return CheckStatus{OK: false, Message: "public_key and the head of public_key_history are different keys — one of them is wrong, and which one is your identity is not a question an actor may answer"}
	}
	if len(didDoc) == 0 {
		return CheckStatus{OK: true, Message: "the chain head is the key the site publishes"}
	}

	assertedX, err := didAssertedKeyX(didDoc)
	if err != nil {
		return CheckStatus{OK: false, Message: "did.json is present and " + err.Error()}
	}
	wantX, err := jwkX([]byte(head))
	if err != nil {
		return CheckStatus{OK: false, Message: "the chain head could not be projected for comparison with did.json: " + err.Error()}
	}
	if assertedX != wantX {
		return CheckStatus{OK: false, Message: "did.json's assertionMethod names a different key than the head of public_key_history — a resolver would treat a retired key as the one that speaks for this identity"}
	}
	return CheckStatus{OK: true, Message: "the chain head, public_key and did.json's assertionMethod are the same key"}
}

// didAssertedKeyX returns the JWK `x` of the key a DID document says currently
// speaks for the identity: the verification method its assertionMethod points
// at.
//
// ⚠️ It follows the POINTER rather than taking the first verificationMethod.
// Since SIGNET epic 16 a document carries retired keys too, so "the first one"
// and "the authoritative one" are no longer the same thing, and a check that
// conflated them would pass a document whose assertionMethod named a retired
// key.
func didAssertedKeyX(didDoc []byte) (string, error) {
	var doc struct {
		VerificationMethod []struct {
			ID           string `json:"id"`
			PublicKeyJwk struct {
				X string `json:"x"`
			} `json:"publicKeyJwk"`
		} `json:"verificationMethod"`
		AssertionMethod []string `json:"assertionMethod"`
	}
	if err := json.Unmarshal(didDoc, &doc); err != nil {
		return "", fmt.Errorf("does not parse: %v", err)
	}
	if len(doc.AssertionMethod) == 0 {
		return "", fmt.Errorf("names no assertionMethod, so it says nothing about which key speaks for this identity")
	}
	target := doc.AssertionMethod[0]
	for _, vm := range doc.VerificationMethod {
		if vm.ID == target {
			return vm.PublicKeyJwk.X, nil
		}
	}
	return "", fmt.Errorf("points assertionMethod at %q, which is not one of its own verification methods", target)
}

// DomainFromDIDDocument reads the host a site says it is served at, out of its
// own published DID document's `id`.
//
// ⚠️ THIS EXISTS BECAUSE .well-known/polis HAS NO HOST FIELD, and the domain is
// inside every transition signature. A local validator has no other
// site-authored statement of where the site lives: POLIS_BASE_URL is the
// operator's environment rather than the site's claim, and the legacy base_url
// some old tenants carry is exactly what Tailor strips. did.json's `id` is the
// site's own published statement, so it works on a clone of somebody else's
// site too — the same reasoning checkDIDDocument already uses.
//
// Returns "" when there is no document or its id is not a did:web identifier.
// The caller must then report that a chain WITH rotations could not be checked,
// never that it passed.
func DomainFromDIDDocument(didDoc []byte) string {
	if len(didDoc) == 0 {
		return ""
	}
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(didDoc, &doc); err != nil {
		return ""
	}
	host := strings.TrimPrefix(doc.ID, "did:web:")
	if host == doc.ID {
		return ""
	}
	return host
}

// KeyHistoryFromWellKnownBytes parses the chain out of fetched
// .well-known/polis bytes, for a caller that has the document over HTTP.
func KeyHistoryFromWellKnownBytes(data []byte) (*site.KeyHistoryBlock, error) {
	return site.KeyHistoryFromWellKnown(data)
}

// dsParityNotRun is what `polis validate` says about the one check it cannot
// perform.
//
// ⛔ D8, AND IT IS NOT PADDING. A published chain proves it is internally
// consistent and proves nothing about whether it is COMPLETE. The two attacks a
// self-signed chain structurally cannot show — an entry quietly omitted, and a
// chain quietly backdated — are exactly what a third-party witness catches, and
// that comparison runs in Clerk, which is hosted-only. Staying silent about it
// would let a clean report be read as "my history was checked against the
// record", which it was not.
const dsParityNotRun = "the published chain was checked against ITSELF, not against the discovery service's record of it. That comparison is what catches an entry quietly omitted or a chain quietly backdated — a chain can be internally perfect and still be incomplete. It runs in the hosted Clerk sweep and there is no self-hosted equivalent, so this report does not cover it."
