package site

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/did"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// DIDDocumentPath is where a did:web DID Document must live for the no-path
// form of the method: https://<host>/.well-known/did.json.
//
// ⚠️ THIS PATH IS NOT DISCOVERED BY A POINTER, AND THAT IS NOT AN
// INCONSISTENCY. The licence is found through a `license` field in
// .well-known/polis because polis chose that file's location and a site's
// layout is user-configurable. This location is chosen by an external
// standard: did:web resolution IS defined as `/.well-known/did.json`, so a
// pointer would be inert — no resolver would read it — and adding one would
// create a second source of truth for a location nobody can vary.
func DIDDocumentPath(siteDir string) string {
	return filepath.Join(siteDir, ".well-known", "did.json")
}

// BuildDIDDocument renders the site's DID Document without writing it.
//
// The key comes from .well-known/polis rather than from .polis/keys, so the
// DID Document can never publish a different key than the site's own identity
// document publishes. Patrol already checks that those two agree; if they ever
// disagree, this reflects the published one and the disagreement stays exactly
// where Patrol reports it instead of being quietly resolved here.
//
// ⭐ Since SIGNET epic 16 it also projects the site's KEY HISTORY: every retired
// key becomes a verificationMethod (so a signature it made stays checkable
// through the DID) and stays out of assertionMethod (so it no longer speaks for
// this identity). The source is public_key_history in the same document — this
// re-encodes it and asserts nothing new, which is what keeps the whole file
// derived and safe for an actor to regenerate unasked.
//
// A malformed retired key does not fail the build. The document's job is to
// publish the CURRENT key in the shape a resolver reads, and refusing to
// publish that because an old entry will not parse would trade a complete
// document for none at all. The chain's own verifier is where a bad entry is
// reported.
func BuildDIDDocument(siteDir, host string) ([]byte, error) {
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return nil, fmt.Errorf("load well-known: %w", err)
	}
	if wk.PublicKey == "" {
		return nil, fmt.Errorf("no public_key in .well-known/polis")
	}
	pub, err := signing.ParsePublicKey([]byte(wk.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}

	epoch, retired := didKeyHistory(siteDir, wk.PublicKey)
	return did.BuildWithHistory(host, pub, epoch, retired)
}

// didKeyHistory projects a site's published chain into the shape pkg/did wants:
// the current key's epoch, and every retired key oldest-first.
//
// ⚠️ IT IS DELIBERATELY FORGIVING. A site with no chain (not yet provisioned)
// gets epoch 0 and no retired keys — exactly the single-key document polis has
// published since epic 09, byte for byte. A chain whose HEAD does not match the
// key the site publishes is ignored entirely rather than half-applied: the two
// disagreeing is a real finding, and Judge's head-agreement check is where it
// gets reported. Building a document from a chain the site's own public_key
// contradicts would bury that finding under a plausible-looking file.
func didKeyHistory(siteDir, publicKey string) (int, []did.RetiredKey) {
	block, err := LoadKeyHistory(siteDir)
	if err != nil || block == nil {
		return 0, nil
	}
	if block.Current.Key != publicKey {
		return 0, nil
	}
	var retired []did.RetiredKey
	for _, e := range block.History {
		pub, err := signing.ParsePublicKey([]byte(e.Key))
		if err != nil {
			continue
		}
		retired = append(retired, did.RetiredKey{Epoch: e.Epoch, Key: pub})
	}
	return block.Current.Epoch, retired
}

// PublishDIDDocument writes <siteDir>/.well-known/did.json from the site's own
// published key. Mirrors PublishMessagesKey: derive from what the site already
// holds, write it into the well-known directory, and stay idempotent.
//
// The document is DERIVED, so regenerating it asserts nothing new — it says
// only what .well-known/polis already says, in the encoding a DID resolver
// reads. That is what makes it safe for an actor to write on a tenant's behalf,
// and it is precisely the line that forbids the same actor from ever writing
// license.json.
//
// host is the site's canonical host ("alice.polis.pub"), passed explicitly
// rather than read from a global: the caller is the only party that knows where
// this site is actually served.
func PublishDIDDocument(siteDir, host string) error {
	data, err := BuildDIDDocument(siteDir, host)
	if err != nil {
		return err
	}
	dir := filepath.Join(siteDir, ".well-known")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return atomicfile.WriteFile(DIDDocumentPath(siteDir), data, 0644)
}

// DIDDocumentKeyCount is how many keys a did.json names in verificationMethod —
// 0 for no document, or one that does not parse.
//
// ⭐ Retired keys stay in verificationMethod, so this only ever grows. A count
// of two or more is proof the site has rotated.
func DIDDocumentKeyCount(didDoc []byte) int {
	if len(didDoc) == 0 {
		return 0
	}
	var doc struct {
		VerificationMethod []json.RawMessage `json:"verificationMethod"`
	}
	if json.Unmarshal(didDoc, &doc) != nil {
		return 0
	}
	return len(doc.VerificationMethod)
}

// DIDDocumentNeedsWrite reports whether the on-disk document differs from what
// the site's current key and host produce — missing, truncated, stale after a
// rotation, or edited by someone else. Returns the bytes that should be there
// so the caller does not build them twice.
//
// It is a plain byte comparison because did.Marshal is deterministic. That is
// what lets Medic heal tampering without a stored baseline: the correct
// content is recomputable from the site itself at any moment.
func DIDDocumentNeedsWrite(siteDir, host string) (bool, []byte, error) {
	want, err := BuildDIDDocument(siteDir, host)
	if err != nil {
		return false, nil, err
	}
	got, err := os.ReadFile(DIDDocumentPath(siteDir))
	if err != nil {
		if os.IsNotExist(err) {
			return true, want, nil
		}
		return false, nil, err
	}
	return string(got) != string(want), want, nil
}
