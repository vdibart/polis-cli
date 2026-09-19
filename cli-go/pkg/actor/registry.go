// Package actor implements pub.polis.actor — an OPERATOR's signed registry of
// the system actors it runs.
//
// # What the artifact IS, and what it is not
//
// An operator runs software that acts on the network: Judge verifies sites,
// Rosie acts for tenants, Patrol and Medic sweep. Without a published record,
// anyone can stand up a lookalike and claim to be one of them. The registry is
// the operator saying, signed: THESE are our actors, THIS is whose authority
// each exercises, and THESE are the actions we expect each to perform.
//
// ⛔ EXPECTED, NEVER ALLOWED. This is not an allow-list, and building one would
// be worse than nothing because it would look like a control. The operator
// holds its actors' private keys BY DEFINITION — that is what makes them its
// actors — so no published list can stop one doing anything. What a published
// list buys is that deviation is OBSERVABLE: a stranger can say "Rosie did X
// and X is not on her list", and the remedy is social — remediation, apology,
// reputation. If a user is uncomfortable with that tradeoff, the user can
// self-host, which is why the exit has to keep working.
//
// ⚠️ So this file catches exactly two things, and it must not be described as
// catching more:
//
//   - An action signed by a key that belongs to no listed actor. ✅ CAUGHT —
//     that is the lookalike case, and one record closes it.
//   - An action by a listed actor outside its published expectations.
//     ⚠️ OBSERVABLE, not prevented. It is information, not a violation the
//     protocol blocks.
//
// # Why a FILE, when we already have attestations
//
// A single fetchable file is checkable by a stranger with `curl`; a query is
// not. The alternative considered — "the registry is every agent-disclosure
// attestation issued by the operator" — makes the check depend on
// infrastructure the checker may not have (PQL, the discovery service, or
// knowing how to walk index.jsonl). The whole point is that an outsider can
// check, so one GET beats an enumeration.
//
// The registry does not replace the per-actor attestations; it is the same fact
// at a different lifetime. The FILE is current state — who is ours right now.
// The ATTESTATIONS are the permanent record — what was claimed, and when. The
// ds_events announcement is DELIVERY — it tells people who were not looking.
// current + history, the public_key + public_key_history shape, reused.
//
// ⛔ ON DISAGREEMENT, THE FILE WINS. It is canonical. Drift between the file
// and the attestations is REPORTABLE and never fatal — and because both are
// public, anyone can detect it, which is the property this whole design is for.
//
// # The countersignature covers the ENTRY, not the file
//
// Each entry may carry a signature made by the ACTOR's own key over that
// entry's contents. It is optional, and the two states make visibly different
// claims: an operator-signed entry says "we say this is ours"; a countersigned
// entry says "and the actor agrees it is listed, and agrees to this scope".
//
// ⭐ The asymmetry is the point: an operator CANNOT unilaterally widen what it
// claims an actor does. Change Judge's expected_actions and Judge's
// countersignature stops verifying until Judge re-signs. That protects the
// reader from the one thing they most need protecting from.
//
// ⛔ Removing an actor, or NARROWING it, requires no countersignature. A
// decommissioned or compromised actor cannot sign, and you must always be able
// to disown one.
//
// ⚠️ THE ENTRY SIGNING BASE BINDS THE OPERATOR, and it has to. A
// countersignature over the entry alone would be replayable: a hostile operator
// could list Judge in ITS registry with a countersignature lifted from ours,
// and it would verify. "The actor agrees it is listed" is meaningless without
// "listed BY WHOM".
//
// # Discovery
//
// By POINTER, never by convention: .well-known/polis → actor_registry. `dir`
// and `mount` are user-configurable per-type declarations in bundle.json, so a
// hardcoded path would contradict a decision already made. DefaultPath below is
// a default for provisioning, not an address.
//
// # It is not operator-only
//
// A self-hoster running their own actors IS an operator and publishes their own
// registry. The type is declared for everyone and present on some — exactly
// pub.polis.license, where a tenant with no terms simply has no license.json.
// That is what keeps the registry check identical self-hosted, with no
// hosted-only special case.
package actor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier stamped into every registry.
//
// ⚠️ Use this, never bare Version — the field records WHO WROTE the file, and a
// bare version number does not say that. Bash writes "polis-cli/$VERSION" into
// the equivalent field.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// TypeName is the content type this file belongs to.
const TypeName = "pub.polis.actor"

// SchemaVersion is the registry's version marker.
//
// ⭐ IT IS INSIDE THE SIGNED BYTES, and that is the whole reason it is worth
// having. A marker outside the signature can be stripped, so an artifact could
// be downgraded to whatever rules an attacker prefers. The signing base
// (pub.polis.signing-base.v1) carries no marker precisely because adding one to
// an artifact type that already exists would invalidate every artifact of it —
// this type has no artifacts yet, so it can afford one, and it is the cheap
// moment to do it.
const SchemaVersion = "pub.polis.actor-registry.v1"

// Whose authority an actor exercises.
//
// ⛔ These two values are NOT new vocabulary. They are the already-published
// authority rule, named in the registry: "an actor may perform a USER-AUTHORITY
// action only under a tenant's grant; OPERATOR-AUTHORITY actions need none"
// (docs/ops/admin/operator-guide.md, docs/general/concepts/actors.md). A third word
// for the same concept is exactly the drift to avoid.
const (
	// AuthorityOperator — the actor acts on the operator's own authority.
	// Judge verifying sites needs nobody's permission but the operator's.
	AuthorityOperator = "operator"
	// AuthorityUser — the actor exercises a TENANT's authority, and may do so
	// only under that tenant's grant. Rosie acting for a tenant is this.
	AuthorityUser = "user"
)

// Entry is one actor in an operator's registry.
//
// ⚠️ It names a DOMAIN, not a public key. Domains survive rotation and keys do
// not: rotate Judge's key and every key-bearing entry is stale until the
// registry is rewritten and re-countersigned. The actor's own .well-known/polis
// carries its current key, at the cost of one extra fetch. Keys may be added
// later as defence in depth — and note that would be a WIDENING, so it needs
// every actor to re-countersign.
//
// ⚠️ There is deliberately NO actor_type field. A type would be a summary of
// expected_actions, and summaries drift from what they summarise. `authority`
// is the axis that carries real information.
type Entry struct {
	// Domain is the actor's site origin, e.g. "judge.polis.pub". Bare domain,
	// matching Operator and matching the ds_events actor column — not a URL.
	Domain string `json:"domain"`
	// Authority is AuthorityOperator or AuthorityUser.
	Authority string `json:"authority"`
	// ExpectedActions are the action types the operator expects this actor to
	// perform, fully qualified — e.g. "pub.polis.attestation.integrity".
	//
	// ⛔ DESCRIPTIVE, NEVER PERMISSIVE. Do not name this "allowed", do not
	// describe it as "may", and do not add an evaluator. An empty list is [],
	// never null: "we expect this actor to do nothing" has one byte sequence.
	ExpectedActions []string `json:"expected_actions"`
	// Countersignature is the actor's own signature over EntryCanonicalJSON —
	// optional, and its absence is a weaker claim rather than a defect.
	//
	// Omitted entirely when absent, never written as "".
	Countersignature string `json:"countersignature,omitempty"`
}

// Registry is an operator's signed list of its actors.
type Registry struct {
	// V is SchemaVersion. First signed field, so the file says what it is
	// before it says anything else.
	V string `json:"v"`
	// Operator is THE SITE ORIGIN the registry is served from and its signature
	// binds to — "polis.polis.pub", not "polis.pub" the party. A verifier
	// FETCHES this: https://<operator>/.well-known/polis is where the key that
	// signed this file is published.
	Operator string `json:"operator"`
	// Actors is the list. [] when the operator runs none, never null.
	Actors []Entry `json:"actors"`
	// Asserted is when this state was stated, RFC 3339 with a Z.
	Asserted string `json:"asserted"`
	// Generator records who wrote the file. Inside the signature.
	Generator string `json:"generator"`

	// Signature is an SSH signature over CanonicalJSON. Empty means UNSIGNED,
	// which is a fact and not a defect.
	//
	// ⚠️ There is deliberately NO current_version. The registry is current
	// state rewritten wholesale, like following.json and blessed.json, and
	// neither of those carries one either. A content hash earns its place when
	// something reads it — the attestation type has one because the discovery
	// service uses it. Nothing reads one here yet, and a field nobody consumes
	// is the defect shape this workstream keeps finding.
	Signature string `json:"signature"`

	// unrecognised are the JSON members of the parsed document that this build
	// does not declare — captured by Parse, consulted by Verify and
	// VerifyCountersignature (SIGNET epic 47, tolerant verifiers).
	//
	// ⚠️ Nil for a registry this process built in memory, and that is correct
	// rather than a gap: there were no foreign bytes to meet.
	unrecognised []string
}

// UnrecognisedFields returns the JSON members this build does not declare, for
// a registry that came from Parse or Load. Nil for anything built in memory.
//
// A caller that reports a `valid` status MUST also report this when it is
// non-empty: the signature verified over the fields this build knows, and these
// are NOT covered by it (SIGNET epic 47, row 2 of the table).
func (r *Registry) UnrecognisedFields() []string {
	if r == nil {
		return nil
	}
	return r.unrecognised
}

// entryUnrecognised narrows the file-level list to ONE entry's own subtree.
//
// ⭐ The two signing bases in this file cover different field sets, so they have
// different tolerance answers. An unrecognised member at the TOP of the file
// changes the bytes the OPERATOR signed and none of the bytes an actor
// countersigned — EntryCanonicalJSON reads only `v`, `operator` and the entry —
// so folding the two lists together would report an actor's countersignature
// uncheckable because of a field the actor never saw.
//
// Matched by domain rather than by pointer: a caller may hold a copy from a
// range loop, and the domain is what identifies an entry in this file anyway.
func (r *Registry) entryUnrecognised(e *Entry) []string {
	if r == nil || e == nil || len(r.unrecognised) == 0 {
		return nil
	}
	idx := -1
	for i := range r.Actors {
		if r.Actors[i].Domain == e.Domain {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	prefix := fmt.Sprintf("actors[%d].", idx)
	var out []string
	for _, f := range r.unrecognised {
		if strings.HasPrefix(f, prefix) {
			out = append(out, f)
		}
	}
	return out
}

// Parse reads a registry from raw JSON bytes, recording any members this build
// does not declare.
//
// ⭐ EVERY PATH THAT TURNS FOREIGN BYTES INTO A Registry MUST COME THROUGH HERE
// — Load for a file on disk, and any fetch of another operator's registry. A
// plain json.Unmarshal silently drops the unknown members, and then Verify
// cannot tell "this signature is wrong" from "this registry was written by an
// operator running something newer than me" — which, for a file whose whole
// point is that OTHER operators can read it, is the likelier case.
func Parse(raw []byte) (*Registry, error) {
	var r Registry
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	r.unrecognised = signing.UnrecognisedFields(raw, Registry{})
	return &r, nil
}

// SignatureStatus is the outcome of checking a signature.
//
// FOUR values, the same four the follow file and attestations use. Collapsing
// any two makes a judgment the protocol is not allowed to make for a consumer.
type SignatureStatus string

const (
	// StatusUnsigned — no signature. A fact, not a finding.
	StatusUnsigned SignatureStatus = "unsigned"
	// StatusValid — signature present and verifies.
	StatusValid SignatureStatus = "valid"
	// StatusInvalid — signature present and does NOT verify.
	StatusInvalid SignatureStatus = "invalid"
	// StatusUnknown — could not check, because no public key was available.
	// Having failed to look is not the same as having looked and found a
	// problem.
	StatusUnknown SignatureStatus = "unknown"
)

// CanonicalJSON produces the deterministic byte sequence the operator's
// signature covers.
//
// THIS FUNCTION IS THE SPECIFICATION. A second implementation must reproduce
// these exact bytes or its signatures will not verify against ours.
// docs/signet/spec/signing-base.md §5.3 restates it in prose and a test checks
// the two against each other mechanically.
//
// The signable set, in order:
//
//	{"v":…,"operator":…,"actors":[{"domain":…,"authority":…,
//	 "expected_actions":[…],"countersignature":…}],"asserted":…,"generator":…}
//
// Compact encoding/json output — no indentation, no trailing newline, fields in
// struct-declaration order, NOT alphabetical. Empty lists are [], never null.
//
// EXCLUDED: `signature`, which cannot cover itself. Nothing else.
//
// ⭐ COUNTERSIGNATURES ARE INSIDE the operator's signature, deliberately. The
// alternative — excluding them so an actor can countersign without the operator
// re-signing — would let anyone with write access STRIP a countersignature,
// silently downgrading an entry from "the actor agrees" to "we say so" with the
// file signature still verifying. The order of operations is instead: draft the
// entry, have the actor countersign it, then sign the file.
func CanonicalJSON(r *Registry) ([]byte, error) {
	actors := r.Actors
	if actors == nil {
		actors = []Entry{}
	}
	normalised := make([]Entry, len(actors))
	for i, e := range actors {
		normalised[i] = e
		if normalised[i].ExpectedActions == nil {
			normalised[i].ExpectedActions = []string{}
		}
	}

	signable := struct {
		V         string  `json:"v"`
		Operator  string  `json:"operator"`
		Actors    []Entry `json:"actors"`
		Asserted  string  `json:"asserted"`
		Generator string  `json:"generator"`
	}{
		V:         r.V,
		Operator:  r.Operator,
		Actors:    normalised,
		Asserted:  r.Asserted,
		Generator: r.Generator,
	}
	return json.Marshal(signable)
}

// EntryCanonicalJSON produces the bytes an actor's COUNTERSIGNATURE covers.
//
// The signable set, in order:
//
//	{"v":…,"operator":…,"domain":…,"authority":…,"expected_actions":[…]}
//
// ⛔ `v` AND `operator` ARE IN HERE ON PURPOSE, and leaving either out is a
// real vulnerability rather than an untidiness. Without `operator` the
// countersignature is REPLAYABLE — a hostile operator lists Judge in its own
// registry, pastes our countersignature in, and it verifies, so the file now
// says Judge agreed to be that operator's actor. Without `v` the same bytes
// could be reinterpreted under a future schema.
//
// EXCLUDED: `countersignature`, which cannot cover itself. `generator` and
// `asserted` are excluded too — the actor is consenting to WHAT IS CLAIMED
// ABOUT IT, not to which build wrote the file or when the operator last
// restated the list. Including them would break every countersignature on every
// unrelated re-statement of the registry.
func EntryCanonicalJSON(r *Registry, e *Entry) ([]byte, error) {
	actions := e.ExpectedActions
	if actions == nil {
		actions = []string{}
	}
	signable := struct {
		V               string   `json:"v"`
		Operator        string   `json:"operator"`
		Domain          string   `json:"domain"`
		Authority       string   `json:"authority"`
		ExpectedActions []string `json:"expected_actions"`
	}{
		V:               r.V,
		Operator:        r.Operator,
		Domain:          e.Domain,
		Authority:       e.Authority,
		ExpectedActions: actions,
	}
	return json.Marshal(signable)
}

// Countersign returns an actor's signature over its own entry in r.
//
// The caller holds the ACTOR's private key, not the operator's — that is the
// whole point of the countersignature. The entry must already be in r, because
// the bytes bind the operator that is listing it.
func Countersign(r *Registry, e *Entry, actorPrivateKey []byte) (string, error) {
	canonical, err := EntryCanonicalJSON(r, e)
	if err != nil {
		return "", fmt.Errorf("entry canonical JSON: %w", err)
	}
	return signing.SignContent(canonical, actorPrivateKey)
}

// VerifyCountersignature checks an entry's countersignature against the ACTOR's
// published key — which the caller fetches from the actor's own
// .well-known/polis, never from the registry, since the registry deliberately
// holds no keys.
//
// Returns StatusUnsigned when the entry carries none. That is a defined state:
// an operator-signed-only entry is a weaker claim, not a broken one.
func VerifyCountersignature(r *Registry, e *Entry, actorPublicKeySSH []byte) (SignatureStatus, error) {
	if e.Countersignature == "" {
		return StatusUnsigned, nil
	}
	if len(actorPublicKeySSH) == 0 {
		return StatusUnknown, nil
	}
	canonical, err := EntryCanonicalJSON(r, e)
	if err != nil {
		return StatusUnknown, fmt.Errorf("entry canonical JSON: %w", err)
	}
	ok, err := signing.VerifySignature(canonical, actorPublicKeySSH, e.Countersignature)

	// SIGNET epic 47 — the tolerance rule, over THIS ENTRY's own subtree only.
	// See entryUnrecognised for why the file-level list is not the right one.
	switch out := signing.Resolve(ok && err == nil, r.entryUnrecognised(e)); out.Status {
	case signing.StatusUnknown:
		return StatusUnknown, errors.New(out.Explain())
	case signing.StatusInvalid:
		return StatusInvalid, err
	}
	return StatusValid, nil
}

// Sign signs r with the operator's private key, setting r.Signature.
func Sign(r *Registry, operatorPrivateKey []byte) error {
	canonical, err := CanonicalJSON(r)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}
	sig, err := signing.SignContent(canonical, operatorPrivateKey)
	if err != nil {
		return fmt.Errorf("sign registry: %w", err)
	}
	r.Signature = sig
	return nil
}

// Verify checks the operator's signature over the whole registry.
func Verify(r *Registry, operatorPublicKeySSH []byte) (SignatureStatus, error) {
	if r.Signature == "" {
		return StatusUnsigned, nil
	}
	if len(operatorPublicKeySSH) == 0 {
		return StatusUnknown, nil
	}
	canonical, err := CanonicalJSON(r)
	if err != nil {
		return StatusUnknown, fmt.Errorf("canonical JSON: %w", err)
	}
	ok, err := signing.VerifySignature(canonical, operatorPublicKeySSH, r.Signature)

	// SIGNET epic 47 — the tolerance rule. A rebuild that FAILS while the file
	// carries members this build does not declare is "I could not check this",
	// never "this is forged". ⚠️ The `valid` row is silent on purpose — see
	// UnrecognisedFields.
	switch out := signing.Resolve(ok && err == nil, r.unrecognised); out.Status {
	case signing.StatusUnknown:
		return StatusUnknown, errors.New(out.Explain())
	case signing.StatusInvalid:
		return StatusInvalid, err
	}
	return StatusValid, nil
}

// SignAndWrite signs the registry and atomically writes it.
//
// ⛔ It REFUSES to rewrite a registry carrying members this build does not
// declare (signing.GuardRewrite): the rewrite would drop them and sign the rest
// with the operator's key. The way past is RewriteUnsigned.
func SignAndWrite(r *Registry, path string, operatorPrivateKey []byte) error {
	if err := signing.GuardRewrite("actor registry", path, Registry{}); err != nil {
		return err
	}
	if err := Sign(r, operatorPrivateKey); err != nil {
		return err
	}
	return writeRegistry(r, path)
}

// RewriteUnsigned is the ESCAPE HATCH for an operator stranded by
// SignAndWrite's refusal: it rewrites the registry at path from the members
// this build declares, WITHOUT a signature, and returns what it dropped. ⛔ It
// never signs. A registry with nothing unrecognised is left untouched.
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
	if err := writeRegistry(r, path); err != nil {
		return nil, err
	}
	return dropped, nil
}

// writeRegistry marshals and atomically writes a registry as-is.
func writeRegistry(r *Registry, path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create registry directory: %w", err)
	}
	// 0644: this is PUBLIC served content, and a registry a stranger cannot
	// fetch proves nothing. Self-hosters serving content/pub.polis.core/
	// through nginx under a different user must be able to read it.
	if err := atomicfile.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

// Load reads and parses a registry from disk.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	r, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return r, nil
}

// Find returns the entry for a domain, or nil. Comparison is exact; callers
// holding user input should lower-case first.
func (r *Registry) Find(domain string) *Entry {
	for i := range r.Actors {
		if r.Actors[i].Domain == domain {
			return &r.Actors[i]
		}
	}
	return nil
}

// DefaultPath returns the default on-disk location of registry.json.
//
// It is only a DEFAULT. Discovery is by pointer — consumers read
// .well-known/polis → actor_registry and follow it, never an assumed path.
// `dir` and `mount` are per-type declarations in bundle.json, so site layout is
// user-configurable and a hardcoded path would contradict a decision already
// made. This function exists for provisioning and for defaults.
func DefaultPath(dataDir string) string {
	return filepath.Join(dataDir, "content", "pub.polis.core", "actor", "registry.json")
}

// RegistryFilename is the registry's name inside its content directory.
const RegistryFilename = "registry.json"

// RegistryPath returns where this site's actor registry lives, honouring the
// site's own bundle declaration rather than assuming a path.
//
// `dir` is user-configurable per content type, so a site that has moved its
// actor directory is a supported configuration and not a broken one. Falls back
// to the default layout when the bundle cannot be read.
//
// ⚠️ This mirrors site.LicensePath, which lives in pkg/site — the asymmetry is
// forced, not stylistic. pkg/site does not import pkg/license, so the licence
// resolver could live there; the Guard in this package DOES need pkg/site to
// read .well-known/polis, so putting a resolver for this type in pkg/site would
// close an import cycle.
func RegistryPath(siteDir string) string {
	b, err := bundle.LoadBundle(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"))
	if err == nil {
		if dir, err := b.ContentDir(TypeName); err == nil {
			return filepath.Join(siteDir, dir, RegistryFilename)
		}
	}
	return DefaultPath(siteDir)
}

// RegistryURLPath returns the site-relative URL written into the
// `actor_registry` pointer in .well-known/polis.
//
// ⭐ IT POINTS AT THE SIGNED SOURCE, NOT THE RENDERED MOUNT, and that is Law
// 1's corollary rather than a convenience: a permanent address points at the
// bytes that were signed. A mount can be re-rendered, re-themed or moved; the
// content path is what a signature covers. The licence pointer resolves the
// same way for the same reason.
func RegistryURLPath(siteDir string) string {
	rel, err := filepath.Rel(siteDir, RegistryPath(siteDir))
	if err != nil {
		rel = filepath.Join("content", "pub.polis.core", "actor", RegistryFilename)
	}
	return "/" + filepath.ToSlash(rel)
}

// StateRegistry signs a registry, writes it, and points .well-known/polis at
// it, in that order.
//
// The ordering matters and mirrors site.StateLicense: the document exists
// before anything advertises it, so a crash between the two leaves an
// unreferenced file rather than a pointer into nothing.
//
// ⚠️ The reverse ordering is what WithdrawRegistry uses, for the mirror reason.
func StateRegistry(siteDir string, r *Registry, operatorPrivateKey []byte) error {
	path := RegistryPath(siteDir)
	if err := SignAndWrite(r, path, operatorPrivateKey); err != nil {
		return err
	}
	if err := site.SetActorRegistryPointer(siteDir, RegistryURLPath(siteDir)); err != nil {
		return fmt.Errorf("write actor registry pointer: %w", err)
	}
	return nil
}

// WithdrawRegistry removes the pointer and then the file — the reverse of
// StateRegistry, so a crash never leaves anything advertising something gone.
//
// ⛔ Withdrawing the registry does NOT withdraw the per-actor attestations, and
// it must not: those are the permanent record of what was claimed and when. A
// removal is announced by ISSUING a withdrawal attestation, never by deleting a
// claim. Deleting the file only says "there is no current list".
func WithdrawRegistry(siteDir string) error {
	if err := site.SetActorRegistryPointer(siteDir, ""); err != nil {
		return fmt.Errorf("clear actor registry pointer: %w", err)
	}
	if err := os.Remove(RegistryPath(siteDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove registry: %w", err)
	}
	return nil
}

// Attribution names WHOSE KEY made a signature an actor is reporting.
//
// ⛔ IT IS "SIGNS AS", NOT "ACTS AS", and the distinction is IMPERSONATION.
// "Acts as" hides the mechanism and implies the cases differ in AUTHORITY. They
// do not — an actor always acts under grant by the operator that runs it. The
// only thing that differs is WHOSE NAME ENDS UP ON THE SIGNATURE.
//
// ⚠️ Before actors had identities of their own there was ONE possible answer to
// "whose key signed this", so nothing recorded it. There are now two, and an
// event that does not say which is not an audit trail.
//
// ⭐ AttributionAsTenant and AttributionCoSigned are ALREADY PUBLISHED
// vocabulary — docs/ops/admin/operator-guide.md §1, shipped in epic 22.
// AttributionAsSelf is the state this epic creates and the published pair does
// not cover: an actor signing with its own key and nobody else's.
const (
	// AttributionAsSelf — the actor signed with its OWN key, alone. Judge's
	// integrity attestation. ⭐ A reader can disagree with the actor without
	// doubting the tenant.
	AttributionAsSelf = "as-self"
	// AttributionAsTenant — ⛔ THE ACTOR SIGNED WITH THE TENANT'S KEY. FULL
	// STOP. The bytes are IDENTICAL to what the tenant's own CLI would have
	// sent — not similar, identical — so nothing on the wire distinguishes it
	// and the network sees the TENANT assert something.
	//
	// ⛔ This is not delegation. Delegation shows a reader "B acting for A";
	// this is indistinguishable from the principal, and reputation, blame and
	// belief all attach to a tenant for something software did. Do not soften
	// it to "on behalf of".
	//
	// ⭐ Declaring it honestly is BETTER than not declaring. An operator who
	// signs as the tenant AND SAYS SO is being honest about worse behaviour.
	AttributionAsTenant = "as-tenant"
	// AttributionCoSigned — the actor signed with its own key ALONGSIDE the
	// tenant's. Nothing produces this yet.
	AttributionCoSigned = "co-signed"
)
