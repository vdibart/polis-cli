// Package did projects a polis site's existing Ed25519 identity key into a
// W3C did:web DID Document.
//
// ONE KEY, TWO SHAPES. Nothing here mints, stores, or signs anything. The key
// is the same key already published as an OpenSSH string in
// .well-known/polis; this package re-encodes those same 32 bytes as a JWK and
// wraps them in the document a DID resolver expects. A polis identity was
// always "a keypair bound to a domain, published at a well-known location,
// anchored in DNS + TLS" — which is did:web in a different encoding. This
// closes the encoding gap and nothing else.
//
// The document is therefore DERIVED, not authored: it is fully determined by
// (host, public key), so any actor may regenerate it at any time without
// asserting anything the site owner did not already say.
package did

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Contexts is the @context array every polis DID Document carries.
//
// ⚠️ THE SECOND ENTRY IS LOAD-BEARING AND WAS VERIFIED, NOT GUESSED.
// `JsonWebKey2020` is defined by the JWS-2020 suite context. The neighbouring
// https://w3id.org/security/jwk/v1 (which an earlier draft of the design doc
// sampled) redirects to the 2025 VCDI JWK context, and that document defines
// the term `JsonWebKey` — NOT `JsonWebKey2020`. Pairing it with our
// verification method would leave the type undefined under JSON-LD expansion,
// which a strict resolver is entitled to reject. Both contexts were fetched
// and diffed during implementation; see the epic's Implementation log.
var Contexts = []string{
	"https://www.w3.org/ns/did/v1",
	"https://w3id.org/security/suites/jws-2020/v1",
}

// KeyFragment identifies a site's first verification method.
//
// A fingerprint-based fragment was considered and rejected: it changes on
// rotation anyway, so it buys no stability and costs readability.
//
// ⚠️ A site with a key history has one fragment per key, numbered from its
// epoch — see FragmentForEpoch. This constant is epoch 0's, which is why a site
// that has never rotated publishes exactly the document it always did.
const KeyFragment = "key-1"

// FragmentForEpoch names the verification method for one key in a site's
// history.
//
// Epochs count from 0 and fragments from 1, so genesis is `key-1` — the
// fragment every polis DID document has carried since epic 09. Publishing a
// history therefore changes NOTHING about a site that has never rotated, which
// is every site today: no fleet-wide document churn, no re-baselining, and the
// golden test that pins the single-key bytes still passes.
func FragmentForEpoch(epoch int) string {
	return fmt.Sprintf("key-%d", epoch+1)
}

// RetiredKey is one key a site used to sign with and no longer does.
//
// ⭐ IT STAYS IN THE DOCUMENT, AND THAT IS THE POINT. A resolver checking a
// signature made two years ago needs the key that made it. Dropping a retired
// key from verificationMethod would make every artifact it signed unverifiable
// through the DID — which is exactly the hole publishing a key history exists
// to close, reopened in the standard shape.
type RetiredKey struct {
	Epoch int
	Key   ed25519.PublicKey
}

// JWK is an RFC 8037 OKP/Ed25519 public key.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
}

// VerificationMethod is a single key entry in a DID Document.
type VerificationMethod struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Controller   string `json:"controller"`
	PublicKeyJwk JWK    `json:"publicKeyJwk"`
}

// Document is the DID Document polis publishes at /.well-known/did.json.
//
// It is a struct rather than a map so field order is fixed by declaration and
// the golden test can assert exact bytes.
//
// ⚠️ NO GENERATOR FIELD, DELIBERATELY. Elsewhere in the codebase a package
// that writes a file stamps it with "polis-cli-go/<version>". This document is
// read by strangers' tooling, in a format we do not own; a non-standard
// top-level field is noise at best and a parse risk at worst. Carry the
// standard, own the binding — decorating someone else's format is the opposite
// of that. The version convention therefore does not apply to this package.
type Document struct {
	Context            []string             `json:"@context"`
	ID                 string               `json:"id"`
	VerificationMethod []VerificationMethod `json:"verificationMethod"`
	Authentication     []string             `json:"authentication"`
	// AssertionMethod is what VC verifiers check when the DID is a credential
	// subject or issuer. Publishing only `authentication` would make the
	// document resolvable but useless for the one thing Phase 1 is for.
	AssertionMethod []string `json:"assertionMethod"`
}

// ID returns the did:web identifier for a host, e.g. "did:web:alice.polis.pub".
//
// Hosted tenants use the SUBDOMAIN form (did:web:alice.polis.pub), never the
// path form (did:web:polis.pub:alice): the path form resolves to
// https://polis.pub/alice/did.json, which does not match polis's per-tenant
// subdomain routing and would need a new apex route to serve.
func ID(host string) string {
	return "did:web:" + host
}

// NormalizeHost lowercases a host and rejects anything that is not a bare
// hostname. The caller has usually just run it through url.ExtractDomain, so
// this is a guard against an empty or malformed configuration reaching the
// published document rather than a parser.
func NormalizeHost(host string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", fmt.Errorf("did: empty host")
	}
	if strings.ContainsAny(h, "/:? #") {
		return "", fmt.Errorf("did: %q is not a bare hostname", host)
	}
	return h, nil
}

// PublicKeyJWK projects raw Ed25519 public-key bytes into an RFC 8037 JWK.
func PublicKeyJWK(pub ed25519.PublicKey) (JWK, error) {
	if len(pub) != ed25519.PublicKeySize {
		return JWK{}, fmt.Errorf("did: expected %d-byte ed25519 public key, got %d",
			ed25519.PublicKeySize, len(pub))
	}
	return JWK{
		Kty: "OKP",
		Crv: "Ed25519",
		// base64url, unpadded — RFC 8037 §2.
		X: base64.RawURLEncoding.EncodeToString(pub),
	}, nil
}

// BuildDocument assembles the DID Document for a host and its current identity
// key, with no history. Equivalent to BuildDocumentWithHistory(host, pub, 0,
// nil) and kept because most callers have no history to pass.
func BuildDocument(host string, pub ed25519.PublicKey) (*Document, error) {
	return BuildDocumentWithHistory(host, pub, 0, nil)
}

// BuildDocumentWithHistory assembles the DID Document for a host, its current
// key at `epoch`, and every key it has retired.
//
// ⛔ THE WHOLE DESIGN IS IN WHICH LIST A RETIRED KEY APPEARS IN.
//
//	verificationMethod   every key, current and retired.
//	                     "these keys are mine, and some of them were mine."
//	authentication       the current key only.
//	assertionMethod      the current key only.
//	                     "this is the key that speaks for me NOW."
//
// A retired key must stay resolvable (a signature it made two years ago is
// still checkable) and must stop being authoritative (it can no longer act or
// assert for this identity). Those are different claims, and a DID document has
// two different lists for exactly that. Carry the standard, own the binding —
// epic 09's move, applied a second time to the same key.
//
// ⚠️ THE DOCUMENT IS A PROJECTION. Its source is public_key_history in
// .well-known/polis, which carries validity dates and transition signatures
// this format cannot express — which is precisely why the native shape exists
// alongside it. Never author here; regenerate from the source.
//
// Retired keys are listed NEWEST FIRST after the current one, so reading down
// the list walks backwards in time from now — the direction someone resolving a
// DID to check an old signature is travelling. (The native block is oldest-first
// because a verifier walks it forward from genesis. Different readers, different
// order, one source.)
func BuildDocumentWithHistory(host string, pub ed25519.PublicKey, epoch int, retired []RetiredKey) (*Document, error) {
	h, err := NormalizeHost(host)
	if err != nil {
		return nil, err
	}
	jwk, err := PublicKeyJWK(pub)
	if err != nil {
		return nil, err
	}

	id := ID(h)
	keyID := id + "#" + FragmentForEpoch(epoch)

	methods := []VerificationMethod{{
		ID:         keyID,
		Type:       "JsonWebKey2020",
		Controller: id,
		// The key is its own controller's: a polis site holds one identity
		// key and delegates to nobody. Delegation, when it comes, is a
		// signed statement — not a second key in this document.
		PublicKeyJwk: jwk,
	}}
	for i := len(retired) - 1; i >= 0; i-- {
		rjwk, err := PublicKeyJWK(retired[i].Key)
		if err != nil {
			return nil, fmt.Errorf("did: retired key at epoch %d: %w", retired[i].Epoch, err)
		}
		methods = append(methods, VerificationMethod{
			ID:           id + "#" + FragmentForEpoch(retired[i].Epoch),
			Type:         "JsonWebKey2020",
			Controller:   id,
			PublicKeyJwk: rjwk,
		})
	}

	return &Document{
		Context:            Contexts,
		ID:                 id,
		VerificationMethod: methods,
		// ⛔ Current key ONLY in both. A retired key that stayed here could
		// still authenticate as this identity and still issue assertions in its
		// name, which would make rotation meaningless.
		Authentication:  []string{keyID},
		AssertionMethod: []string{keyID},
	}, nil
}

// Marshal renders a document as stable, indented JSON with a trailing newline.
//
// Stability matters beyond tidiness: Medic compares the bytes it would write
// against the bytes on disk to decide whether the document has drifted, so any
// non-determinism here would make every sweep rewrite every tenant's file.
func Marshal(doc *Document) ([]byte, error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Build is the one-call form: host + key in, publishable bytes out.
func Build(host string, pub ed25519.PublicKey) ([]byte, error) {
	return BuildWithHistory(host, pub, 0, nil)
}

// BuildWithHistory is the one-call form carrying a site's retired keys.
func BuildWithHistory(host string, pub ed25519.PublicKey, epoch int, retired []RetiredKey) ([]byte, error) {
	doc, err := BuildDocumentWithHistory(host, pub, epoch, retired)
	if err != nil {
		return nil, err
	}
	return Marshal(doc)
}
