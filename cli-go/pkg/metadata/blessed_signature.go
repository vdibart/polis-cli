package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// SIGNET epic 14 — the blessing list is signed.
//
// `blessed.json` is AUTHORED, not derived. The causality runs one way — the
// blesser decides, the local record is written, and the DS learns afterward —
// so the signature is the author asserting "these are the comments I have
// blessed". It is the same shape as `following.json` (epic 02) and this file is
// deliberately a close copy of `pkg/following`: one proven mechanism, not two.
//
// ⚠️ Signing it does NOT back-sign the individual blessings. The claim is
// "this is my current list", which is true of a file written today regardless of
// when each entry was granted. See Signet epic 14, D5.

// SignatureStatus is the outcome of checking a blessing list's signature.
//
// FOUR values, and the distinctions are load-bearing — a consumer that collapses
// them makes a judgment the protocol is not allowed to make for it (Signet Law
// 2):
//
//   - StatusUnsigned — no signature. A FACT, not a defect. Every file written
//     before this epic has none, and most will keep none until their author
//     next blesses something. Reporting it as a failure turns the fleet red.
//   - StatusValid    — signature present and verifies.
//   - StatusInvalid  — signature present and does NOT verify. Report it; the
//     file is still loaded and still renders. A wrong signature is evidence,
//     not a verdict, and the likeliest cause is a bug rather than an attack.
//   - StatusUnknown  — could not check (no public key available). Not a finding.
//
// ⚠️ These four values duplicate following.SignatureStatus, deliberately.
// `pkg/metadata` must not import `pkg/following`, which drags in the discovery
// client, the stream and the policy engine for a four-constant enum. Two
// instances is not an abstraction (M2, the rule of three) — when a THIRD signed
// artifact appears, hoist this type into `pkg/signing` and collapse all three.
type SignatureStatus string

const (
	StatusUnsigned SignatureStatus = "unsigned"
	StatusValid    SignatureStatus = "valid"
	StatusInvalid  SignatureStatus = "invalid"
	StatusUnknown  SignatureStatus = "unknown"
)

// canonicalBlessedJSON produces the deterministic byte sequence that the
// signature covers. THIS FUNCTION IS THE SPEC — a second implementation must
// reproduce these exact bytes or its signatures will not verify against ours.
//
// The signable set, in order:
//
//	{"version":<generator string>,
//	 "comments":[{"post":…,"blessed":[{"url":…,"version":…,"blessed_at":…}, …]}, …]}
//
// Compact `encoding/json` output (no indentation, no trailing newline), fields
// in Go struct-declaration order, no field omitted. `signature` is excluded — it
// cannot cover itself.
//
// ⚠️ Entry order is NOT normalised. The signature covers the list as the author
// wrote it, exactly as `following.json` does; sorting here would mean the bytes
// on disk and the bytes signed could differ, which is the one thing this
// function exists to prevent.
//
// ⚠️ `version` is FIRST, so stampGenerator must run BEFORE Sign. See
// stampBlessedGenerator.
func canonicalBlessedJSON(bc *BlessedComments) ([]byte, error) {
	// Copy rather than pin in place: Verify calls this on a caller's struct and
	// must not mutate it. The copy is shallow per post entry, which is enough —
	// only the slice headers are rewritten.
	comments := make([]PostComments, len(bc.Comments))
	for i, pc := range bc.Comments {
		// A nil slice marshals as null and an empty one as [], so a post entry
		// with no blessed comments would otherwise have two byte sequences.
		if pc.Blessed == nil {
			pc.Blessed = []BlessedComment{}
		}
		comments[i] = pc
	}

	signable := struct {
		Version  string         `json:"version"`
		Comments []PostComments `json:"comments"`
	}{
		Version:  bc.Version,
		Comments: comments,
	}

	return json.Marshal(signable)
}

// SignBlessed computes the signature over canonicalBlessedJSON(bc) and sets
// bc.Signature.
func SignBlessed(bc *BlessedComments, privateKeyPEM []byte) error {
	canonical, err := canonicalBlessedJSON(bc)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}
	sig, err := signing.SignContent(canonical, privateKeyPEM)
	if err != nil {
		return fmt.Errorf("sign blessed.json: %w", err)
	}
	bc.Signature = sig
	return nil
}

// VerifyBlessed checks bc.Signature against publicKeySSH. It reports a status
// and never an opinion: the caller decides what the status means (Law 2).
//
// The returned error explains a StatusInvalid / StatusUnknown; it is context for
// a human, not a second channel of truth. Read the status.
func VerifyBlessed(bc *BlessedComments, publicKeySSH []byte) (SignatureStatus, error) {
	if bc == nil || bc.Signature == "" {
		return StatusUnsigned, nil
	}
	if len(publicKeySSH) == 0 {
		return StatusUnknown, fmt.Errorf("no public key to verify against")
	}

	canonical, err := canonicalBlessedJSON(bc)
	if err != nil {
		return StatusUnknown, fmt.Errorf("canonical JSON: %w", err)
	}

	ok, err := signing.VerifySignature(canonical, publicKeySSH, bc.Signature)

	// SIGNET epic 47 — the tolerance rule. A rebuild that FAILS while the
	// document carries members this build does not declare is "I could not
	// check this", never "this is forged": the signature covers a wider field
	// set than the one reconstructed above.
	//
	// ⭐ THIS TYPE IS WHY THE EPIC EXISTS. Epic 11's agent marker (`agent`,
	// `grant`) is a signed field added to an entry AFTER blessing lists were
	// already being signed, and a verifier built before it would have called
	// every marked list forged. That one field was sequenced by hand (epic 46
	// R9.1); this rule is so the next one need not be.
	//
	// ⚠️ The `valid` row is silent on purpose — see UnrecognisedFields.
	switch out := signing.Resolve(ok && err == nil, bc.unrecognised); out.Status {
	case signing.StatusUnknown:
		return StatusUnknown, errors.New(out.Explain())
	case signing.StatusInvalid:
		if err != nil {
			return StatusInvalid, fmt.Errorf("signature does not verify: %w", err)
		}
		return StatusInvalid, fmt.Errorf("signature does not verify against the site identity key")
	}
	return StatusValid, nil
}

// VerifyBlessedSite loads a site's blessing list and checks its signature
// against the identity key published in .well-known/polis.
//
// It verifies against the PUBLISHED key rather than .polis/keys/id_ed25519.pub
// because that is the key a third party on the network would fetch and use — so
// this answers the same question the network asks, not a locally convenient
// approximation. Same source as judge checks 8 and 9.
//
// An absent blessing list, an absent .well-known/polis, or an absent public_key
// all yield a non-finding status, never an error-shaped failure: they are
// ordinary states of a site that has not done a thing yet.
func VerifyBlessedSite(siteDir string) (SignatureStatus, error) {
	bc, err := loadBlessedCommentsRaw(siteDir)
	if err != nil {
		// loadBlessedCommentsRaw wraps with %w, so errors.Is sees through it.
		// Without this a site that has never blessed anything would report
		// StatusUnknown ("could not check") when the truthful answer is
		// "there is nothing here yet".
		if errors.Is(err, os.ErrNotExist) {
			// A site that has blessed nothing has no file. Unsigned, not broken.
			return StatusUnsigned, nil
		}
		return StatusUnknown, fmt.Errorf("load blessed.json: %w", err)
	}
	if bc.Signature == "" {
		return StatusUnsigned, nil
	}

	data, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		return StatusUnknown, fmt.Errorf("no .well-known/polis to verify against")
	}
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return StatusUnknown, fmt.Errorf("unparseable .well-known/polis")
	}
	if wk.PublicKey == "" {
		return StatusUnknown, fmt.Errorf("no public_key in .well-known/polis")
	}

	return VerifyBlessed(bc, []byte(wk.PublicKey))
}

// BlessedPath returns the path to a site's blessing list.
func BlessedPath(siteDir string) string {
	return filepath.Join(siteDir, BundleContentDir, "comment", BlessedCommentsFilename)
}
