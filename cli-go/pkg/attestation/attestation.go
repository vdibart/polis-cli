// Package attestation implements pub.polis.attestation — a signed claim one
// party makes about someone or something else.
//
// # What the artifact IS
//
// An attestation is a CLAIM EVENT: on this date, this issuer asserted this
// predicate about this subject. That sentence decides everything else in this
// package, and it is what separates an attestation from pub.polis.tag, whose
// envelope this package reuses wholesale.
//
// A tag is a CATEGORY. It is self-directed — structure an author puts on their
// own reading — and the file IS the tag, which is why one tag file holds many
// targets under one created/updated pair. An attestation is other-directed and
// happens at a moment, so bundling several subjects into one file would give
// one timestamp to claims made at different times, make retracting one of them
// a file-surgery problem, and break the requirement that a record render alone.
// Hence ONE FILE, ONE SUBJECT. Issuing the same predicate about five subjects
// writes five files with five signatures; that is correct, not wasteful.
//
// What IS borrowed from pub.polis.tag, verbatim and deliberately, is the proven
// envelope: canonical JSON built from a signing-only struct, a `sha256:` content
// version, one Ed25519 signature in SSH armour, an atomic write. Reusing it
// rather than inventing a second envelope is the whole reason this is a sibling
// type instead of a widening of tag.
//
// # The ordering that makes current_version legal
//
//	canonicalise → hash the canonical bytes into current_version → sign those
//	same bytes
//
// So current_version is computed OVER the signed content and is itself
// UNSIGNED. That is Signet Law 1 — never sign a computation — and pub.polis.tag
// implemented it correctly years before the law was written. Copy the order, do
// not improve it.
//
// ⚠️ Unlike the follow file, which dropped its version hash for want of a
// consumer, this one HAS a consumer: the discovery service uses it to tell when
// a registered content row changed (see discovery.go). Same rule, opposite
// answer, because the facts differ.
//
// # Tolerance is the extensibility hinge
//
// A record whose predicate or subject type a reader does not recognise MUST
// still verify, MUST still parse, and MUST still render legibly. It is simply
// ignored by anything computing over it. This package therefore constrains what
// it WRITES and never what it READS — the asymmetry is the design, not an
// oversight. A reader that rejects unknown predicates means no predicate can
// ever be added without breaking every deployed reader, and "others build on
// it" becomes impossible by construction.
//
// The precedent is deliberate: IETF AIPREF (draft-ietf-aipref-vocab-06 §6.4)
// says unknown labels MUST be ignored rather than treated as failure. Epic 01
// already carries that standard's vocabulary; this carries its robustness rule.
//
// Full specification, sufficient to build a second implementation:
// docs/signet/spec/attestation.md.
package attestation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/index"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier stamped into every record.
//
// ⚠️ Use this, never bare Version — the field records WHO WROTE the record, and
// a bare version number does not say that. Bash writes "polis-cli/$VERSION" into
// the equivalent field, which is how the two implementations stay
// distinguishable in a published artifact.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// TypeName is the content type these records belong to.
const TypeName = "pub.polis.attestation"

// The reserved predicates.
//
// ⚠️ This is a RESERVED LIST, NOT A REGISTRY. Nothing validates against it,
// nothing rejects a predicate outside it, and no lookup table exists. A registry
// is earned once three third-party predicates exist and not before — until then
// it would be a meta-layer designed rather than earned.
//
// The single source of truth for what these mean is
// docs/signet/spec/attestation.md, which every other page points at. Two copies
// of a vocabulary in two files is how this vocabulary rotted once already.
//
// In PROSE the short form ("same-as") is fine and is what the demonstrations
// use. In a FILE, never: the namespace is what makes tolerance meaningful, since
// it lets a reader see whose predicate it is without recognising it. A third
// party's predicate looks like "com.example.reviewed".
const (
	// PredicateSameAs — this identity and that one are the same party.
	// Countersigned as a PAIR of independent records that reference each other,
	// never as one file with two signatures.
	PredicateSameAs = "pub.polis.attestation.same-as"
	// PredicateIntegrity — an integrity OBSERVATION: the issuer examined this
	// site's signed artifacts from a stated vantage at a stated moment.
	//
	// ⛔ THE PREDICATE NAMES WHAT WAS EXAMINED, NEVER WHAT WAS FOUND (Signet epic
	// 05 E1). The finding is the REQUIRED payload key `result`, with no default;
	// a reader that cannot read the payload must not interpret the record. It
	// used to read "this site's signed artifacts verify" — so a failure published
	// under it would have asserted the opposite of what its issuer meant. See
	// spec §4.1 and IntegrityPayload*.
	PredicateIntegrity = "pub.polis.attestation.integrity"
	// PredicateCorrection — this specific VERSION of this work is corrected.
	// The subject carries a version pin; without one the claim is about a
	// mutable object and means very little.
	PredicateCorrection = "pub.polis.attestation.correction"
	// PredicateUsedUnderTerms — I used this work, under these terms, on this
	// date. The consumer's half of a licence conversation.
	PredicateUsedUnderTerms = "pub.polis.attestation.used-under-terms"
	// PredicateAgentDisclosure — this identity is an automated agent, operated
	// by X, scoped to Y.
	PredicateAgentDisclosure = "pub.polis.attestation.agent-disclosure"
	// PredicateEndorsement — I vouch for this party.
	PredicateEndorsement = "pub.polis.attestation.endorsement"
	// PredicateWithdrawal — the record at this URL is retracted by its issuer.
	// The subject is the withdrawn record's URL, pinned to its version.
	//
	// ⚠️ A withdrawal ADDS a record; it never removes one. Retraction is a
	// claim like any other, so it is signed, dated, and permanent — the thing
	// it retracts stays exactly where it was. See Withdraw and epic 03 D6.
	PredicateWithdrawal = "pub.polis.attestation.withdrawal"
	// PredicateCustody — an OPERATOR declares that it holds this tenant's
	// identity key, and says how it signs with it (Signet epic 17).
	//
	// ⭐ Signed with the OPERATOR's own key, so it is the half of custody the
	// operator cannot manufacture for someone else and cannot later disclaim.
	// It is the promise a tenant checks her discovery-service records against.
	// ⛔ It makes custody VISIBLE; it does not reduce it by one bit.
	PredicateCustody = "pub.polis.attestation.custody"
	// PredicateCustodyGrant — a TENANT grants custody of her key to an operator.
	//
	// ⚠️ Under custody the operator holds the key that signs this, so the record
	// is manufacturable — which is why `basis` says how it was obtained. It is
	// the discovery service's machine-checkable authority link, never proof of
	// consent. One grant covers everything the key can do (scope=custodial).
	PredicateCustodyGrant = "pub.polis.attestation.custody-grant"
)

// The custody records' payloads (spec custody.md §12). Each record carries ONE
// concept — custody — in one direction; the two directions share `terms`.
const (
	// CustodyKeyHolds is REQUIRED on a declaration: what the operator holds.
	CustodyKeyHolds = "holds"
	// CustodyHoldsIdentityKey is the one value today.
	CustodyHoldsIdentityKey = "identity-key"
	// CustodyKeyAttribution is REQUIRED on a declaration: whose name ends up on
	// what the operator signs with the tenant's key.
	CustodyKeyAttribution = "attribution"
	// CustodyAttributionAsTenant — the operator signs AS the tenant.
	//
	// ⚠️ It must stay legal (epic 17 D2). An operator that signs as the tenant
	// and SAYS SO is being honest about worse behaviour; if `co-signed` were the
	// only value, that operator would simply not declare.
	CustodyAttributionAsTenant = "as-tenant"
	// CustodyAttributionCoSigned — the operator's own key co-signs what it does.
	CustodyAttributionCoSigned = "co-signed"

	// CustodyKeyScope is REQUIRED on a grant.
	CustodyKeyScope = "scope"
	// CustodyScopeCustodial is the only scope: custody authorises everything
	// the key can do, and the record says that rather than implying a list of
	// tasks it does not have (epic 17 D3).
	CustodyScopeCustodial = "custodial"
	// CustodyKeyBasis is REQUIRED on a grant: how it was obtained (D4).
	CustodyKeyBasis = "basis"
	// CustodyBasisHostingTerms — issued by the operator under its hosting terms.
	CustodyBasisHostingTerms = "hosting-terms"
	// CustodyBasisUserSigned — signed by a present person.
	CustodyBasisUserSigned = "user-signed"

	// CustodyKeyTerms is OPTIONAL on either record: an https URL for the terms
	// the custody is held under.
	CustodyKeyTerms = "terms"
)

// The integrity observation's payload (spec §4.1).
const (
	// IntegrityKeyResult is REQUIRED: IntegrityVerified or IntegrityNotVerified.
	IntegrityKeyResult = "result"
	// IntegrityKeyVantage is REQUIRED: where the issuer stood. Stated, never
	// inferred.
	IntegrityKeyVantage = "vantage"
	// IntegrityKeyObserved is REQUIRED: when the examination completed, RFC 3339
	// with a Z. Distinct from `asserted`, which is when the record was signed.
	IntegrityKeyObserved = "observed"
	// IntegrityKeyUnreachable, IntegrityKeyMismatched, IntegrityKeyChecks and
	// IntegrityKeyConsecutive are optional detail. Unreachable and mismatched
	// are kept apart because they are different facts: an artifact that could
	// not be fetched says nothing about the artifact.
	IntegrityKeyUnreachable = "unreachable"
	IntegrityKeyMismatched  = "mismatched"
	IntegrityKeyChecks      = "checks"
	IntegrityKeyConsecutive = "consecutive"

	// IntegrityVerified — every examination completed and verified.
	IntegrityVerified = "verified"
	// IntegrityNotVerified — at least one did not. An observation, never a
	// verdict: "could not verify at `observed` from `vantage`".
	IntegrityNotVerified = "not-verified"
)

// assertedRe matches the RFC 3339, Z, second-precision form every timestamp in
// a record uses.
var assertedRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

// checkIntegrityObservation is the WRITER rule for PredicateIntegrity.
//
// ⛔ `result` HAS NO DEFAULT. A default is how a missing value turns a failure
// into a pass, silently — so a record without it is refused, never filled in.
// Like every rule in Issue, this constrains what we write and nothing a reader
// accepts.
func checkIntegrityObservation(r *Record) error {
	if r.Subject.Type != SubjectIdentity {
		return fmt.Errorf("an integrity observation is about a site: subject type must be %q", SubjectIdentity)
	}
	switch r.Payload[IntegrityKeyResult] {
	case IntegrityVerified, IntegrityNotVerified:
	case "":
		return fmt.Errorf("an integrity observation requires payload %s=%s|%s — there is no default, because a missing result must never read as a pass",
			IntegrityKeyResult, IntegrityVerified, IntegrityNotVerified)
	default:
		return fmt.Errorf("payload %s=%q must be %q or %q",
			IntegrityKeyResult, r.Payload[IntegrityKeyResult], IntegrityVerified, IntegrityNotVerified)
	}
	if strings.TrimSpace(r.Payload[IntegrityKeyVantage]) == "" {
		return fmt.Errorf("an integrity observation requires payload %s=… — where the issuer stood is part of the claim", IntegrityKeyVantage)
	}
	if !assertedRe.MatchString(r.Payload[IntegrityKeyObserved]) {
		return fmt.Errorf("an integrity observation requires payload %s=<RFC 3339 with a Z, e.g. 2026-09-14T12:00:00Z>", IntegrityKeyObserved)
	}
	return nil
}

// checkCustody is the WRITER rule for both custody predicates (epic 17).
//
// Like every rule in Issue it constrains what we write and nothing a reader
// accepts. ⛔ No field has a default: a declaration without `attribution` would
// be a promise with the one testable part missing, and a grant without `basis`
// would silently claim a consent it does not carry.
func checkCustody(r *Record) error {
	if r.Subject.Type != SubjectIdentity {
		return fmt.Errorf("%s is about a party: subject type must be %q", r.Predicate, SubjectIdentity)
	}
	oneOf := func(key string, values ...string) error {
		got := r.Payload[key]
		for _, v := range values {
			if got == v {
				return nil
			}
		}
		if got == "" {
			return fmt.Errorf("%s requires payload %s=%s — there is no default",
				r.Predicate, key, strings.Join(values, "|"))
		}
		return fmt.Errorf("payload %s=%q must be one of %s", key, got, strings.Join(values, ", "))
	}

	if r.Predicate == PredicateCustody {
		if err := oneOf(CustodyKeyHolds, CustodyHoldsIdentityKey); err != nil {
			return err
		}
		if err := oneOf(CustodyKeyAttribution, CustodyAttributionAsTenant, CustodyAttributionCoSigned); err != nil {
			return err
		}
	} else {
		if err := oneOf(CustodyKeyScope, CustodyScopeCustodial); err != nil {
			return err
		}
		if err := oneOf(CustodyKeyBasis, CustodyBasisHostingTerms, CustodyBasisUserSigned); err != nil {
			return err
		}
	}
	if terms, ok := r.Payload[CustodyKeyTerms]; ok && !strings.HasPrefix(terms, "https://") {
		return fmt.Errorf("payload %s=%q must be an https URL", CustodyKeyTerms, terms)
	}
	return nil
}

// Subject types.
//
// URI and IDENTITY, and deliberately no third. Every demonstration and every
// downstream epic written down so far is covered by these two; a third type
// with no consumer would be produced-and-never-consumed, which is the exact
// pattern epics 01, 02 and 09 each found in shipped code. Tolerance means a
// third can be added the day something needs it — with its consumer.
const (
	// SubjectURI — a work, a comment, or another attestation. Anything with a
	// permanent URL. May carry a version pin.
	SubjectURI = "uri"
	// SubjectIdentity — a party, named by their site's base URL.
	SubjectIdentity = "identity"
)

// Subject is what a claim is about.
//
// ⚠️ The referent lives under a UNIFORM `id` key rather than a per-type key
// (`uri:` / `identity:`), and that is a tolerance decision rather than a
// stylistic one. A reader meeting a subject type it does not recognise must
// still render the claim legibly — issuer, subject, date. It can only do that if
// the thing being referred to sits under a key it can find WITHOUT understanding
// the type. Per-type key names would make an unknown subject type unreadable,
// which is the same failure tolerance forbids for predicates.
type Subject struct {
	// Type is SubjectURI or SubjectIdentity when we write it. A reader must
	// accept any value — see the package doc.
	Type string `json:"type"`
	// ID is the referent: an https URL either way.
	ID string `json:"id"`
	// Version pins a URI subject to exact bytes, as "sha256:<64 hex>".
	//
	// ⚠️ It is INSIDE the signature, because it is a signed field like any
	// other. A pin outside the signature would be worthless — anyone could
	// repoint the claim at different bytes, which destroys the one property
	// that makes a correction survive an edit of the thing it corrects.
	//
	// Omitted entirely when absent, never written as "".
	Version string `json:"version,omitempty"`
}

// Record is one attestation: one claim, one file, one signature.
type Record struct {
	// Type is always TypeName. It is the first signed field, so a record says
	// what it is before it says anything else — the same shape license.File
	// uses.
	Type string `json:"type"`
	// Issuer is the base URL of the site making the claim. The key that signed
	// the record is the one published at that site.
	Issuer string `json:"issuer"`
	// Predicate is what is being asserted, FULLY QUALIFIED. See the reserved
	// list above; a value outside it is legal and must not be rejected.
	Predicate string `json:"predicate"`
	// Subject is what the claim is about.
	Subject Subject `json:"subject"`
	// Payload is predicate-specific detail. Optional; omitted entirely when
	// empty.
	//
	// ⚠️ Values are STRINGS, and the restriction is load-bearing rather than
	// lazy. JSON number canonicalisation (1 vs 1.0 vs 1e0) is the single most
	// common interoperability failure in signed-JSON formats — it is why RFC
	// 8785 exists and why implementations still get it wrong. Restricting the
	// payload to strings removes the problem from the signing base entirely, so
	// a stranger reproducing our bytes never has to guess how we serialise a
	// number. A count is "1077".
	//
	// Map keys serialise in lexicographic order (Go's encoding/json sorts them);
	// that ordering is part of the specification, not an implementation detail.
	Payload map[string]string `json:"payload,omitempty"`
	// Asserted is when the claim was made, RFC 3339 with a Z, second precision.
	//
	// ONE timestamp, not created/updated: a record that can be updated is not a
	// claim event. A correction to a claim is a new record about the old one.
	//
	// ⚠️ There is deliberately no `expires` field, and ABSENCE HERE MEANS "NO
	// EXPIRY ASSERTED" — explicitly NOT "valid forever". The record states when
	// it was asserted; freshness is the consumer's policy, not a protocol fact.
	// `expires` is reserved as a statement of issuer INTENT (intent is not a
	// computation, so it may be signed) for whenever something needs it. Writing
	// this down now costs nothing; writing it down later leaves every record
	// ever issued ambiguous between "never expires" and "unknown".
	Asserted string `json:"asserted"`
	// Generator records who wrote the record. Inside the signature.
	Generator string `json:"generator"`

	// Version is the sha256 of the canonical signing bytes, as
	// "sha256:<64 hex>".
	//
	// ⚠️ It is NOT SIGNED — it is computed OVER the signed bytes, so it cannot
	// be inside them. The discovery service reads it to tell when a registered
	// content row changed, which is the consumer that earns it a place here at
	// all; the follow file has no such consumer and correctly has no such field.
	Version string `json:"current_version"`

	// WithdrawnBy is the URL of the withdrawal that retracts this record, when
	// there is one — a FORWARD REFERENCE, written by Withdraw (Signet epic 17 D7).
	//
	// ⚠️ It is NOT SIGNED, and that is what lets it be added to a record whose
	// signed bytes are frozen: CanonicalJSON builds from its own hand-listed
	// struct, so the signature, current_version, the id and the URL are all
	// unchanged by it.
	//
	// ⭐ It is DISCOVERY, NOT PROOF. A third party fetching this record has no
	// discovery-service rows to query, so the pointer is how it learns a
	// retraction exists. Follow it and verify the withdrawal it names — that
	// record IS signed, and a false pointer leads to something that does not
	// verify or does not name this record.
	//
	// ⛔ DECLARED HERE ON PURPOSE. Load unmarshals into Record, so an undeclared
	// field is silently dropped by every Go round-trip write — which would
	// silently UN-WITHDRAW a grant. TestForwardReferenceSurvivesAGoRoundTrip.
	WithdrawnBy string `json:"withdrawn_by,omitempty"`

	// Signature is an SSH signature (ssh-keygen -Y compatible) over
	// CanonicalJSON(r). Empty means UNSIGNED, which is a fact and not a defect.
	Signature string `json:"signature"`

	// unrecognised are the JSON members of the parsed document that this build
	// does not declare — captured by Parse, consulted by every Verify in this
	// package (SIGNET epic 47, tolerant verifiers).
	//
	// ⚠️ Nil for a record this process built in memory, and that is correct
	// rather than a gap: there were no foreign bytes to meet.
	//
	// ⚠️ An unrecognised PAYLOAD KEY is not one of these. The payload is an open
	// map by design — the package doc already says an unrecognised predicate
	// loads — so its keys are data, not schema. Only a member of the record
	// itself, or of `subject`, counts.
	unrecognised []string
}

// UnrecognisedFields returns the JSON members this build does not declare, for
// a record that came from Parse or Load. Nil for anything built in memory.
//
// A caller that reports a `valid` status MUST also report this when it is
// non-empty: the signature verified over the fields this build knows, and these
// are NOT covered by it (SIGNET epic 47, row 2 of the table).
func (r *Record) UnrecognisedFields() []string {
	if r == nil {
		return nil
	}
	return r.unrecognised
}

// Parse reads a record from raw JSON bytes, recording any members this build
// does not declare.
//
// ⭐ EVERY PATH THAT TURNS FOREIGN BYTES INTO A Record MUST COME THROUGH HERE —
// Load for a file on disk, sitecheck for one fetched over HTTP. A plain
// json.Unmarshal silently drops the unknown members, and then Verify cannot
// tell "this signature is wrong" from "this record was issued by something
// newer than me".
func Parse(raw []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	r.unrecognised = signing.UnrecognisedFields(raw, Record{})
	return &r, nil
}

// SignatureStatus is the outcome of checking a record's signature.
//
// FOUR values, and collapsing any two of them makes a judgment the protocol is
// not allowed to make on a consumer's behalf. The same four the signed follow
// file uses, for the same reason.
type SignatureStatus string

const (
	// StatusUnsigned — no signature. A fact, not a finding.
	StatusUnsigned SignatureStatus = "unsigned"
	// StatusValid — signature present and verifies.
	StatusValid SignatureStatus = "valid"
	// StatusInvalid — signature present and does NOT verify. Report it; the
	// record is still readable. A wrong signature is evidence, not a verdict.
	StatusInvalid SignatureStatus = "invalid"
	// StatusUnknown — could not check, because no public key was available.
	// Having failed to look is not the same as having looked and found a
	// problem.
	StatusUnknown SignatureStatus = "unknown"
)

// CanonicalJSON produces the deterministic byte sequence the signature covers.
//
// THIS FUNCTION IS THE SPECIFICATION. A second implementation must reproduce
// these exact bytes or its signatures will not verify against ours, and ours
// will not verify for it. docs/signet/spec/attestation.md restates it in prose
// and a test checks the two against each other mechanically.
//
// The signable set, in order:
//
//	{"type":…,"issuer":…,"predicate":…,"subject":{"type":…,"id":…,"version":…},
//	 "payload":{…},"asserted":…,"generator":…}
//
// Compact encoding/json output — no indentation, no trailing newline, fields in
// struct-declaration order. `payload` and `subject.version` are omitted entirely
// when empty rather than written as empty values. Payload keys sort
// lexicographically.
//
// EXCLUDED, and a second implementer must know both: `signature`, which cannot
// cover itself, and `current_version`, which is the hash OF these bytes.
func CanonicalJSON(r *Record) ([]byte, error) {
	signable := struct {
		Type      string            `json:"type"`
		Issuer    string            `json:"issuer"`
		Predicate string            `json:"predicate"`
		Subject   Subject           `json:"subject"`
		Payload   map[string]string `json:"payload,omitempty"`
		Asserted  string            `json:"asserted"`
		Generator string            `json:"generator"`
	}{
		Type:      r.Type,
		Issuer:    r.Issuer,
		Predicate: r.Predicate,
		Subject:   r.Subject,
		Payload:   r.Payload,
		Asserted:  r.Asserted,
		Generator: r.Generator,
	}

	// A present-but-empty payload and an absent one must have ONE byte
	// sequence, the same pin the follow file puts on an empty roster. omitempty
	// covers nil and len 0 alike, so this is belt-and-braces against a future
	// edit that drops the tag.
	if len(signable.Payload) == 0 {
		signable.Payload = nil
	}

	return json.Marshal(signable)
}

// ContentVersion is the sha256 of a record's canonical bytes.
func ContentVersion(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ID returns the record's stable identifier, which is also its filename stem.
//
// Derived, never stored: `<compact asserted>-<first 16 hex of current_version>`,
// e.g. "20260828T140200Z-3f2a9c1d4e5b6a70". Sortable by date, unique by content.
//
// ⚠️ It is deliberately NOT a signed field. It is computed from the signed
// bytes, so it could not be inside them even if we wanted it there — and Law 1
// says not to sign a computation regardless. A verifier recomputes it, exactly
// as it recomputes current_version, which means a record served at a URL that
// disagrees with its own bytes is detectable.
func ID(r *Record) string {
	stamp := strings.NewReplacer("-", "", ":", "").Replace(r.Asserted)
	digest := strings.TrimPrefix(r.Version, "sha256:")
	if len(digest) > 16 {
		digest = digest[:16]
	}
	return stamp + "-" + digest
}

// Dir returns the directory holding a site's attestation records.
//
// The path is a literal on purpose, as tag.TagPath's is. The CORE bundle's
// layout is fixed by design and not user-configurable, so
// `content/pub.polis.core/attestation` is correct here and not debt (settled
// 2026-09-02). Resolving through the bundle is right
// only when reading a FOREIGN site's declarations, as `polis clone` does.
//
// ⚠️ A different question is still open: the type's declared `mount`
// (/attestations) renders no record view — /attestations/<id> 404s while this
// content path serves — although epic 11 now writes agents.json and its page
// under that mount (agent.MountDir). Epic 06's; see
// TestKnownBug_PathsHardcodeInsteadOfResolvingTheBundle.
func Dir(dataDir string) string {
	return filepath.Join(dataDir, "content", "pub.polis.core", "attestation")
}

// Path returns the on-disk path for a record id.
func Path(dataDir, id string) string {
	return filepath.Join(Dir(dataDir), id+".json")
}

// RecordURL is the permanent, fetchable URL of a record.
//
// It is the CONTENT path, not the /attestations mount, and that is deliberate:
// the content path is the one that actually resolves today, served publicly as
// JSON with CORS by the shared static handler. The mount is where a RENDERED
// view will live once one exists.
//
// This matters more here than elsewhere because a subject may be another
// attestation, so this URL is what a countersignature, a correction or a
// withdrawal points at. A reference to a URL that 404s is not a reference.
// A literal for the same reason as Dir, and the same path, so a record is
// written and linked in one place.
func RecordURL(baseURL, id string) string {
	return strings.TrimRight(baseURL, "/") +
		"/content/pub.polis.core/attestation/" + id + ".json"
}

// predicateRe matches a fully-qualified predicate: at least one dot-separated
// namespace segment before the name.
var predicateRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)+$`)

// versionPinRe matches a "sha256:" content hash.
var versionPinRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Issue builds, signs and writes a new attestation, returning it and its id.
//
// ⚠️ The validation below constrains what we WRITE and says nothing about what a
// reader must accept. That asymmetry is the whole extensibility design: we hold
// ourselves to a fully-qualified predicate and a known subject type because
// writing a bare or malformed one would put junk on the network permanently,
// while a reader that enforced the same rules would make the format unextendable
// by anyone but us. Load() enforces none of this.
//
// ⛔ It REFUSES pub.polis.attestation.withdrawal (Signet epic 33). A withdrawal
// is only ever written by Withdraw, which enforces three safeguards this path
// cannot: the target must verify against this site's own key, one withdrawal per
// claim, and no withdrawing a withdrawal. Accepting the predicate here would let
// `polis attest issue --predicate …withdrawal` publish a permanent, unwithdrawable
// retraction of anything — including a claim this site never made — with none of
// them. The refusal is a WRITER rule only; readers still accept any predicate.
func Issue(dataDir string, r *Record, privateKey []byte) (string, error) {
	if r != nil && r.Predicate == PredicateWithdrawal {
		return "", fmt.Errorf("predicate %q is issued only by withdrawing a claim — "+
			"run `polis attest withdraw <id>`, which checks the claim is yours and not already withdrawn",
			PredicateWithdrawal)
	}
	return issue(dataDir, r, privateKey)
}

// issue is Issue without the withdrawal refusal. Only Withdraw may call it with
// PredicateWithdrawal, after its own safeguards have run.
func issue(dataDir string, r *Record, privateKey []byte) (string, error) {
	if r == nil {
		return "", fmt.Errorf("no record to issue")
	}
	r.Type = TypeName

	if strings.TrimSpace(r.Issuer) == "" {
		return "", fmt.Errorf("issuer is required — a claim nobody made is not a claim")
	}
	if !predicateRe.MatchString(r.Predicate) {
		return "", fmt.Errorf("predicate %q must be fully qualified, e.g. %q or %q",
			r.Predicate, PredicateSameAs, "com.example.reviewed")
	}
	if strings.TrimSpace(r.Subject.ID) == "" {
		return "", fmt.Errorf("subject id is required")
	}
	switch r.Subject.Type {
	case SubjectURI, SubjectIdentity:
	default:
		return "", fmt.Errorf("subject type %q must be %q or %q when issuing",
			r.Subject.Type, SubjectURI, SubjectIdentity)
	}
	if r.Subject.Version != "" {
		if r.Subject.Type != SubjectURI {
			return "", fmt.Errorf("only a %q subject can carry a version pin", SubjectURI)
		}
		if !versionPinRe.MatchString(r.Subject.Version) {
			return "", fmt.Errorf("subject version %q must be \"sha256:\" plus 64 hex characters",
				r.Subject.Version)
		}
	}
	for k, v := range r.Payload {
		if strings.TrimSpace(k) == "" {
			return "", fmt.Errorf("payload has an empty key")
		}
		if v == "" {
			return "", fmt.Errorf("payload key %q has an empty value — omit the key instead", k)
		}
	}
	if r.Predicate == PredicateIntegrity {
		if err := checkIntegrityObservation(r); err != nil {
			return "", err
		}
	}
	if r.Predicate == PredicateCustody || r.Predicate == PredicateCustodyGrant {
		if err := checkCustody(r); err != nil {
			return "", err
		}
	}
	if r.Predicate == PredicateGrant {
		if err := checkGrant(r); err != nil {
			return "", err
		}
	}
	// A forward reference is written by Withdraw onto an EXISTING record, never
	// issued with a new one — a record cannot already be retracted.
	r.WithdrawnBy = ""

	if r.Asserted == "" {
		r.Asserted = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	// Stamped BEFORE signing. `generator` is inside the signing base, so
	// stamping after would produce a record that writes cleanly, parses
	// cleanly, and never verifies again — epic 02's E4, exactly.
	r.Generator = GetGenerator()

	if err := signAndStamp(r, privateKey); err != nil {
		return "", err
	}

	id := ID(r)
	if err := write(Path(dataDir, id), r); err != nil {
		return "", err
	}
	refreshIndex(dataDir)
	return id, nil
}

// refreshIndex brings the site's attestation entries in index.jsonl up to date,
// AT WRITE TIME — the way publish does for a post.
//
// ⛔ Signet epic 37 D1. Before this, records reached the index only when
// somebody ran `polis rebuild`, so a site's answer to "what have you attested?"
// was truthful and arbitrarily stale at the same time, and a reader could not
// tell which. The operator site issued two agent-disclosures and published an
// empty index.
//
// ⚠️ It is the SAME partial rebuild `polis rebuild --attestations` runs, not an
// append. That is what makes a write and a later rebuild byte-identical by
// construction — and Patrol fails a whole site on one line it rejects.
// Withdraw goes through Issue, so a withdrawal is indexed too.
//
// ⚠️ Non-fatal. The record is already signed and on disk; failing here would
// tell the user a claim was not made when it was. Medic heals a missed entry,
// and `polis rebuild --attestations` does by hand.
func refreshIndex(dataDir string) {
	if _, err := index.RebuildContentIndex(dataDir, []string{index.EntryTypeAttestation}); err != nil {
		fmt.Fprintf(os.Stderr, "[!] attestation written, but index.jsonl was not updated: %v (run `polis rebuild --attestations`)\n", err)
	}
}

// signAndStamp performs the ordering that makes current_version legal:
// canonicalise, hash the canonical bytes into current_version, sign those same
// bytes. Borrowed verbatim from pkg/tag's signAndWrite.
func signAndStamp(r *Record, privateKey []byte) error {
	canonical, err := CanonicalJSON(r)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}

	r.Version = ContentVersion(canonical)

	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return fmt.Errorf("sign attestation: %w", err)
	}
	r.Signature = sig
	return nil
}

// write marshals and atomically writes a record.
//
// ⛔ It REFUSES to rewrite a record carrying members this build does not
// declare (signing.GuardRewrite). A new record never meets one; the rewrite
// that can is Withdraw's forward reference onto the retracted record, which
// would drop the members and leave a signature over bytes no longer there.
func write(path string, r *Record) error {
	if err := signing.GuardRewrite("attestation", path, Record{}); err != nil {
		return err
	}
	return writeRecord(path, r)
}

// RewriteUnsigned is the ESCAPE HATCH for a user stranded by write's refusal:
// it rewrites the record at path from the members this build declares, WITHOUT
// a signature, and returns what it dropped.
//
// ⚠️ An attestation is a claim, and an unsigned claim is not one: after this,
// the record verifies as nobody's. current_version is KEPT, because the record's
// id — its filename and every URL that cites it — is derived from it. ⛔ It
// never signs. A record with nothing unrecognised is left untouched.
func RewriteUnsigned(path string) ([]string, error) {
	r, err := Load(path)
	if err != nil {
		return nil, err
	}
	dropped := r.UnrecognisedFields()
	if len(dropped) == 0 {
		return nil, nil
	}
	r.Signature = ""
	if err := writeRecord(path, r); err != nil {
		return nil, err
	}
	return dropped, nil
}

// writeRecord marshals and atomically writes a record as-is.
func writeRecord(path string, r *Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal attestation: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create attestation directory: %w", err)
	}
	// 0644: PUBLIC served content under content/pub.polis.core/. A self-hoster
	// serving it through nginx as a different user must be able to read it —
	// don't tighten a public-facing file to 0600.
	if err := atomicfile.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write attestation: %w", err)
	}
	return nil
}

// Withdraw retracts a record this site issued, by issuing a NEW record whose
// subject is the one being retracted.
//
// ⚠️ IT DELETES NOTHING, AND THAT IS THE ENTIRE POINT. Unreachability is
// evidence for durability — a 404 means "I could not reach it", never "it was
// retracted" — so eviction requires a signed revocation rather than an absence.
// A withdrawal implemented as `rm` would teach the network to read 404 as
// retraction AND destroy the record, which is the one operation nothing can
// undo. See epic 03 D6.
//
// The withdrawal PINS the target's current_version. A claim event is immutable,
// so the pin costs nothing and says exactly which bytes were retracted — which
// matters if the same id is ever served with different content.
//
// ⚠️ ONLY THE ISSUER MAY WITHDRAW, enforced by requiring the target to VERIFY
// against this site's own key — not by trusting its `issuer` field, which is
// just a string anyone can write. A record signed by you saying somebody ELSE's
// claim is retracted is not a withdrawal — it is your opinion about their
// claim, which may be a legitimate thing to publish one day but is a DIFFERENT
// act with a different predicate. Conflating them would let anyone appear to
// retract anyone.
//
// ⚠️ ONE WITHDRAWAL PER CLAIM. A second is refused: it says nothing new and,
// because withdrawals cannot be withdrawn, it is litter nobody can ever clear.
func Withdraw(dataDir, id string, privateKey []byte) (string, error) {
	target, err := Load(Path(dataDir, id))
	if err != nil {
		return "", fmt.Errorf("no attestation %q to withdraw: %w", id, err)
	}
	if target.Version == "" {
		return "", fmt.Errorf("attestation %q has no current_version to pin", id)
	}
	if target.Predicate == PredicateWithdrawal {
		return "", fmt.Errorf("attestation %q is itself a withdrawal; withdrawing one would say nothing", id)
	}

	issuer := target.Issuer
	if strings.TrimSpace(issuer) == "" {
		return "", fmt.Errorf("attestation %q names no issuer", id)
	}

	// ⚠️ THE ISSUER CHECK, AND IT IS A REAL ONE. A non-empty `issuer` field is
	// not evidence of anything — it is a string in a file anyone can write.
	// What proves we may retract a claim is that WE SIGNED IT, so that is what
	// is checked: the record must verify against this site's own published key.
	//
	// Without this, a record that merely SITS in this directory — cached,
	// copied, hand-written, or put there by some future feature that stores
	// foreign records locally — could be "withdrawn", producing a record that
	// claims to be someone else's retraction and is signed with our key. It
	// would never verify for a reader following the spec (§9 resolves the key
	// from `issuer`), so it is not a working impersonation — but it publishes a
	// permanently-invalid record on our site that reads as another party
	// retracting their own claim, and invalid reads as tampering.
	//
	// "I may retract what I can prove I signed" is the whole rule, and it needs
	// no base URL passed in to enforce.
	switch status, verr := VerifyRecord(dataDir, target); status {
	case StatusValid:
	case StatusUnsigned:
		return "", fmt.Errorf("attestation %q is unsigned, so there is no claim of ours to retract", id)
	default:
		return "", fmt.Errorf(
			"attestation %q does not verify against this site's key (%s) — "+
				"you can only withdraw a claim you issued: %v", id, status, verr)
	}

	// One withdrawal per claim. A second says nothing the first did not, and it
	// is PERMANENT — withdrawals cannot themselves be withdrawn, so a stray
	// duplicate is litter nobody can ever clear. Refusing is better than
	// silently succeeding, which would tell the user they did something they
	// did not.
	targetURL := RecordURL(issuer, id)
	existing, err := List(dataDir)
	if err != nil {
		return "", fmt.Errorf("check for an existing withdrawal: %w", err)
	}
	for _, r := range existing {
		if r.Predicate == PredicateWithdrawal && r.Subject.ID == targetURL {
			return "", fmt.Errorf(
				"attestation %q was already withdrawn by %s — a second withdrawal would add "+
					"a permanent record saying nothing new", id, ID(r))
		}
	}

	newID, err := issue(dataDir, &Record{
		Issuer:    issuer,
		Predicate: PredicateWithdrawal,
		Subject: Subject{
			Type:    SubjectURI,
			ID:      RecordURL(issuer, id),
			Version: target.Version,
		},
	}, privateKey)
	if err != nil {
		return "", err
	}

	// ⭐ Epic 17 D7: the retracted record gains a FORWARD REFERENCE to its
	// withdrawal, so a reader holding only the record can learn it was retracted.
	// Only the unsigned region moves — the signed bytes, current_version and the
	// id are untouched. It applies to every withdrawn record, not only grants:
	// Withdraw has no predicate branch, and a stranger holding a withdrawn claim
	// of any kind has the same problem.
	//
	// ⚠️ Non-fatal, like the index refresh. The withdrawal is already signed and
	// on disk, and it is the proof; the pointer is only discovery.
	target.WithdrawnBy = RecordURL(issuer, newID)
	if werr := write(Path(dataDir, id), target); werr != nil {
		fmt.Fprintf(os.Stderr, "[!] withdrawal %s written, but the forward reference on %s was not: %v\n", newID, id, werr)
	}
	return newID, nil
}

// Load reads and parses a record.
//
// ⚠️ It validates NOTHING beyond the file being JSON. An unrecognised predicate,
// an unrecognised subject type, an unknown top-level key — all load. A reader
// that rejected them would make the format unextendable by anyone but us.
func Load(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read attestation: %w", err)
	}
	r, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse attestation: %w", err)
	}
	return r, nil
}

// List returns every record in a site's attestation directory, sorted by id —
// which sorts by assertion date, because the id begins with the timestamp.
//
// A malformed file is skipped rather than failing the listing: one bad record
// must not hide every good one. Scan is the same walk and also names what it
// skipped.
func List(dataDir string) ([]*Record, error) {
	records, _, err := Scan(dataDir)
	return records, err
}

// Malformed names a file in a site's attestation directory that does not
// parse — invalid JSON, or JSON Parse refuses.
//
// ⛔ Malformed means CANNOT BE PARSED and nothing else. A record with an
// unrecognised field parses (it reads `unknown` when verified, SIGNET epic 47),
// and so does one whose signature fails; neither is ever listed here. Medic
// quarantines what is listed here, so widening it would move a user's signed
// artifact on a judgement Medic is not entitled to make.
type Malformed struct {
	Name  string `json:"name"`  // file name within Dir
	Error string `json:"error"` // why it did not parse
	// Grant is true when the bytes name the grant predicate. A malformed grant
	// was never live — nothing could resolve it — but it is the user's own
	// statement about their agent, so whoever moves it says so.
	Grant bool `json:"grant,omitempty"`
}

// Scan is List that also returns the files it skipped because they do not
// parse, sorted by name.
func Scan(dataDir string) ([]*Record, []Malformed, error) {
	entries, err := os.ReadDir(Dir(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read attestation directory: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var records []*Record
	var malformed []Malformed
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(Dir(dataDir), name))
		if err != nil {
			// Unreadable is not malformed: a permissions or I/O fault says
			// nothing about the bytes, and Patrol's other checks own it.
			continue
		}
		r, err := Parse(raw)
		if err != nil {
			malformed = append(malformed, Malformed{
				Name:  name,
				Error: err.Error(),
				Grant: bytes.Contains(raw, []byte(`"`+PredicateGrant+`"`)),
			})
			continue
		}
		records = append(records, r)
	}
	return records, malformed, nil
}

// Verify checks a record's signature against a public key. It reports a status
// and never an opinion; the caller decides what the status means.
//
// The returned error explains an invalid or unknown result. It is context for a
// human, not a second channel of truth — read the status.
func Verify(r *Record, publicKeySSH []byte) (SignatureStatus, error) {
	if r == nil || r.Signature == "" {
		return StatusUnsigned, nil
	}
	if len(publicKeySSH) == 0 {
		return StatusUnknown, fmt.Errorf("no public key to verify against")
	}

	canonical, err := CanonicalJSON(r)
	if err != nil {
		return StatusUnknown, fmt.Errorf("canonical JSON: %w", err)
	}

	ok, err := signing.VerifySignature(canonical, publicKeySSH, r.Signature)

	// SIGNET epic 47 — the tolerance rule. A rebuild that FAILS while the
	// record carries members this build does not declare is "I could not check
	// this", never "this is forged": the signature covers a wider field set
	// than the one reconstructed above.
	//
	// ⭐ It is the same posture Load already takes (an unrecognised predicate
	// or subject type loads rather than being rejected), extended from the
	// READING of a record to the CHECKING of one — M1.
	//
	// ⚠️ The `valid` row is silent on purpose — see UnrecognisedFields.
	switch out := signing.Resolve(ok && err == nil, r.unrecognised); out.Status {
	case signing.StatusUnknown:
		return StatusUnknown, errors.New(out.Explain())
	case signing.StatusInvalid:
		if err != nil {
			return StatusInvalid, fmt.Errorf("signature does not verify: %w", err)
		}
		return StatusInvalid, fmt.Errorf("signature does not verify against the issuer's published key")
	}
	return StatusValid, nil
}

// VerifyWithHistory is Verify for an issuer that may have rotated its key: the
// issuer's current key first, then the key its published history resolves for
// the record's CLAIMED signing time, `asserted` (SIGNET epic 44, E5-wide).
//
// ⛔ WITHOUT THIS, THE DAY AN ISSUER ROTATES, EVERY RECORD IT EVER SIGNED READS
// AS INVALID — permanently, for everyone. That is why epic 05 (Judge publishing
// `integrity` attestations about sites it does not own) depends on it.
//
// chain must already be trusted — sitecheck.ResolvingChain over the ISSUER's
// .well-known/polis — and nil means current key only, which is exactly Verify.
// retired is the history entry that verified the record; nil for the current
// key. ⚠️ A retired-key pass is a weaker claim — `asserted` is the record's own
// statement and nothing here checks it — so a caller must say which it got.
func VerifyWithHistory(r *Record, publicKeySSH []byte, chain *site.KeyHistoryBlock) (SignatureStatus, *site.KeyHistoryEntry, error) {
	if r == nil || r.Signature == "" {
		return StatusUnsigned, nil, nil
	}
	if len(publicKeySSH) == 0 {
		return StatusUnknown, nil, fmt.Errorf("no public key to verify against")
	}
	canonical, err := CanonicalJSON(r)
	if err != nil {
		return StatusUnknown, nil, fmt.Errorf("canonical JSON: %w", err)
	}
	ok, retired, err := site.VerifyWithHistory(func(key []byte) (bool, error) {
		return signing.VerifySignature(canonical, key, r.Signature)
	}, publicKeySSH, chain, r.Asserted, "asserted")

	// SIGNET epic 47 — the tolerance rule, applied AFTER the history walk. A
	// record that no key in the chain verifies, carrying members this build
	// does not declare, is still "I could not check this": the reason the bytes
	// never matched may be the field set rather than the key.
	switch out := signing.Resolve(ok, r.unrecognised); out.Status {
	case signing.StatusUnknown:
		return StatusUnknown, nil, errors.New(out.Explain())
	case signing.StatusInvalid:
		if err == nil {
			err = fmt.Errorf("signature does not verify against the issuer's published key")
		}
		return StatusInvalid, nil, err
	}
	return StatusValid, retired, nil
}

// VerifyAgainst checks a record against every key an issuer publishes, reporting
// valid if any of them verifies it.
//
// Today that is always a set of one, so this is Verify with a loop.
//
// ⚠️ It is NOT how a key history is consulted. Trying every key an issuer ever
// held ignores each key's window, which is the one constraint that stops a
// retired key speaking for moments after it was retired — VerifyWithHistory is
// the rule (SIGNET epics 31 and 44).
func VerifyAgainst(r *Record, keys [][]byte) (SignatureStatus, error) {
	if r == nil || r.Signature == "" {
		return StatusUnsigned, nil
	}
	if len(keys) == 0 {
		return StatusUnknown, fmt.Errorf("no public key to verify against")
	}

	// ⚠️ The tolerance rule needs no separate arm here: Verify already applies
	// it per key, so a record with unrecognised members reports StatusUnknown
	// for every key and the loop reaches the last one holding that status
	// rather than StatusInvalid (SIGNET epic 47).
	var lastErr error
	lastStatus := StatusInvalid
	for _, k := range keys {
		status, err := Verify(r, k)
		if status == StatusValid {
			return StatusValid, nil
		}
		lastStatus, lastErr = status, err
	}
	return lastStatus, lastErr
}

// IdentityKeys returns the public keys an issuer's records may be verified
// against, newest first.
//
// ⚠️ THIS IS THE ONE PLACE THE KEY SOURCE LIVES, and it is a function rather
// than an inlined field read for a specific reason. A site publishes exactly one
// key today — `public_key` in .well-known/polis — so a record signed before a
// rotation cannot be verified from the site alone afterwards. Publishing a key
// HISTORY is its own epic; when it lands it widens THIS FUNCTION, and every
// verification path in this package already goes through it, so nothing else has
// to move. Returning a slice today, rather than a single key, is what makes that
// true: callers already handle "more than one".
//
// A missing or unparseable .well-known/polis yields no keys and no error-shaped
// failure — the caller turns that into StatusUnknown, which is "could not look",
// not "looked and found a problem".
func IdentityKeys(siteDir string) ([][]byte, error) {
	data, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		return nil, fmt.Errorf("no .well-known/polis to verify against")
	}
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return nil, fmt.Errorf("unparseable .well-known/polis")
	}
	if wk.PublicKey == "" {
		return nil, fmt.Errorf("no public_key in .well-known/polis")
	}
	return [][]byte{[]byte(wk.PublicKey)}, nil
}

// VerifyFile checks one record on disk against the issuing site's PUBLISHED
// key.
//
// It uses the published key rather than .polis/keys/id_ed25519.pub because that
// is the key a third party on the network would fetch, so this answers the same
// question everyone else is asking rather than a locally convenient
// approximation.
func VerifyFile(siteDir, path string) (SignatureStatus, error) {
	r, err := Load(path)
	if err != nil {
		return StatusUnknown, err
	}
	return VerifyRecord(siteDir, r)
}

// VerifyRecord checks an already-loaded record against the key published at
// siteDir — the LOCAL site.
//
// ⚠️ IT IGNORES r.Issuer, SO IT IS ONLY CORRECT FOR RECORDS THIS SITE ISSUED.
// That is true of every caller today: VerifyFile and VerifySite walk this site's
// own attestation directory, and `polis attest issue` derives the issuer from
// POLIS_BASE_URL, so issuer and host are always the same site.
//
// A FOREIGN record must NOT be verified with this function. The network rule is
// docs/signet/spec/attestation.md §9: read `issuer`, fetch
// https://<issuer>/.well-known/polis, verify against THAT key. Passing someone
// else's record here checks it against OUR key and returns StatusInvalid — which
// reads as tampering when it is really a wiring mistake, and that is a bad way
// to learn this.
//
// ⭐ That sibling exists: sitecheck.VerifyIssuedAttestation fetches the
// ISSUER's .well-known/polis and resolves retired keys through the issuer's own
// history (close-out F4c). It lives in pkg/sitecheck because the chain trust
// rule does, and this package cannot import it.
//
// ⭐ SIGNET EPIC 11 (review F1): it resolves retired keys through the site's own
// published key history, so a record signed before a rotation still verifies.
// See VerifyRecordResolved.
func VerifyRecord(siteDir string, r *Record) (SignatureStatus, error) {
	status, _, err := VerifyRecordResolved(siteDir, r)
	return status, err
}

// VerifySite checks every record a site has issued.
//
// Returns a status per record id. A site with no attestations yields an empty
// map and no error — having issued none is an ordinary state, not a finding.
func VerifySite(siteDir string) (map[string]SignatureStatus, error) {
	records, err := List(siteDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]SignatureStatus, len(records))
	for _, r := range records {
		status, _ := VerifyRecord(siteDir, r)
		out[ID(r)] = status
	}
	return out, nil
}
