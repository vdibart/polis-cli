package sitecheck

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// IssuerFetch returns the body of a URL, or an error when it could not be
// fetched. A failed fetch is "could not look", never "looked and found
// nothing". Passed in so a caller with its own client (or a test with a map)
// decides how the network is reached.
type IssuerFetch func(url string) ([]byte, error)

// IssuerKeys is what an issuer's published .well-known/polis lets a verifier
// use: its current key, and the key history retired keys may be resolved from.
type IssuerKeys struct {
	PublicKey []byte
	// Chain is nil when the site records no usable rotation — current key only.
	// ⛔ It has already passed ResolvingChain's trust rule: an unverified chain,
	// or one whose head is not the published key, resolves nothing.
	Chain *site.KeyHistoryBlock
	// ChainNote says why a chain that records a rotation is not used; "" when
	// there is nothing to explain.
	ChainNote string
}

// IssuerKeysFromWellKnown reads an issuer's key and trusted chain out of its
// .well-known/polis body. base is the origin the body was fetched from; the
// chain's transition signatures are made over its host, port stripped.
//
// ⭐ The one reading of an issuer's identity document for a foreign record —
// Remote.PublishedKey and VerifyIssuedAttestation both go through it, so the
// trust rule cannot drift between a site-wide run and a single record.
func IssuerKeysFromWellKnown(base string, body []byte) (*IssuerKeys, error) {
	base = strings.TrimRight(base, "/")
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		return nil, fmt.Errorf("%s/.well-known/polis does not parse", base)
	}
	if wk.PublicKey == "" {
		return nil, fmt.Errorf("%s/.well-known/polis publishes no public_key", base)
	}
	chain, note := remoteResolvingChain(base, body, wk.PublicKey)
	return &IssuerKeys{PublicKey: []byte(wk.PublicKey), Chain: chain, ChainNote: note}, nil
}

// IssuedVerdict is a foreign record checked against the site that issued it.
type IssuedVerdict struct {
	// Status is the epic-47 vocabulary: valid · unsigned · unknown · invalid.
	// ⚠️ unknown is "could not look" (no key to fetch, or a field this build
	// does not model) and is never a finding.
	Status attestation.SignatureStatus
	// Issuer is the origin whose key was consulted.
	Issuer string
	// KeyUsed says which key verified a valid record; a retired key is a weaker
	// claim than the current one and a caller must say which it got.
	KeyUsed *KeyUsed
	// ChainNote explains a recorded rotation that could not be used.
	ChainNote string
	// Err explains a non-valid status. Context for a human, not a second
	// channel of truth — read Status.
	Err error
}

// VerifyIssuedAttestation checks an attestation record against the key of the
// site that ISSUED it — docs/signet/spec/attestation.md §9: read `issuer`,
// fetch its .well-known/polis, verify against that key — resolving a retired
// key through the issuer's own published history.
//
// ⭐ This is the sibling attestation.VerifyRecord's comment asks for. That
// function uses the LOCAL site's keys and ignores r.Issuer, which is right for
// a site's own records and wrong for anyone else's; it lives here because
// pkg/attestation cannot import the chain trust rule.
//
// ⛔ Without the history walk, the day an issuer rotates every record it ever
// signed reads as invalid here — permanently, for everyone.
func VerifyIssuedAttestation(r *attestation.Record, fetch IssuerFetch) IssuedVerdict {
	if r == nil || r.Signature == "" {
		return IssuedVerdict{Status: attestation.StatusUnsigned}
	}
	base, err := issuerOrigin(r.Issuer)
	if err != nil {
		return IssuedVerdict{Status: attestation.StatusUnknown, Err: err}
	}
	v := IssuedVerdict{Issuer: base}
	body, err := fetch(base + "/.well-known/polis")
	if err != nil {
		v.Status, v.Err = attestation.StatusUnknown, fmt.Errorf("could not fetch %s/.well-known/polis: %w", base, err)
		return v
	}
	keys, err := IssuerKeysFromWellKnown(base, body)
	if err != nil {
		v.Status, v.Err = attestation.StatusUnknown, err
		return v
	}
	v.ChainNote = keys.ChainNote
	status, retired, verr := attestation.VerifyWithHistory(r, keys.PublicKey, keys.Chain)
	v.Status, v.Err = status, verr
	if status == attestation.StatusValid {
		v.KeyUsed = KeyUsedFor(keys.Chain, retired, r.Asserted)
	}
	return v
}

// issuerOrigin returns an issuer's https origin, without a trailing slash.
// ⛔ Only https: a key fetched over anything else proves nothing about who
// published it.
func issuerOrigin(issuer string) (string, error) {
	if strings.TrimSpace(issuer) == "" {
		return "", fmt.Errorf("the record names no issuer, so there is no key to fetch")
	}
	u, err := url.Parse(strings.TrimSpace(issuer))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("issuer %q is not a URL", issuer)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("issuer %q is not an https origin, so its key cannot be fetched with any assurance", issuer)
	}
	return "https://" + strings.ToLower(u.Host), nil
}
