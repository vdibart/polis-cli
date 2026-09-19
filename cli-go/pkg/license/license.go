// Package license implements pub.polis.license — a site's signed, outbound
// statement of the terms under which others may use its author's work.
//
// # The shape of the thing
//
// There are two artifacts and they answer different questions, so a consumer
// never has to reconcile them:
//
//   - license.json — "what are her terms NOW?" Site-level, authored, signed,
//     editable, published. A statement of current intent.
//   - a post's `license:` frontmatter block — "what may I do with THIS post?"
//     Materialised at publish time, inside the signed payload, frozen forever.
//
// Editing license.json therefore never changes a published work. Terms are not
// retroactive, and that is a visible fact rather than a caveat: the two
// artifacts diverging is the honest state of the world.
//
// # Carry the standards, own the binding
//
// Nothing in the vocabulary is ours. `train-ai` and `search` are IETF AIPREF's
// keys with AIPREF's meanings (draft-ietf-aipref-vocab-06); `attribution` is
// RSL's. What polis adds is the binding — the terms sit inside an Ed25519
// signature bound to the author's key and domain, so they travel with the bytes
// and survive the work being quoted, mirrored, or scraped.
//
// A profile (`pub.polis.license.reserved/1`) is a NAME FOR A SELECTION from
// those vocabularies, in the way `CC BY-NC` names a bundle whose legal text
// belongs to Creative Commons. We name selections; we never author terms. The
// checkable rule: every value in a profile must come from a standard's
// vocabulary with that standard's meaning. A profile that needs a value no
// standard defines means we have started drafting a licence — stop.
//
// # Preferences are not grants
//
// AIPREF is explicitly a PREFERENCE signal and explicitly not a licence. RSL is
// genuine licensing. Both travel here, and the spec marks per field which layer
// is doing the work, because calling a preference a grant is caught immediately
// by anyone in that world. See docs/signet/spec/license.md.
//
// # Not to be confused with policy
//
// polis already uses "policy" for the Layer 1 INBOUND grammar — who may comment
// on me, what I accept (docs/general/reference/policy-grammar.md). A licence is
// OUTBOUND: what third parties may do with work I made. Opposite direction,
// different audience, different verbs. They are deliberately not adjacent on
// disk for exactly this reason.
package license

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier written into licence metadata.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// SchemaVersion is the versioned namespace for the licence payload. It appears
// as `v:` in both license.json and the materialised frontmatter block, so a
// consumer reading a 2026 post in 2030 knows which rules to read it under.
const SchemaVersion = "pub.polis.license.v1"

// Profile identifiers. A profile is a semantic tag, NEVER a URL.
//
// This is a deliberate difference from Creative Commons, whose deed URL is
// load-bearing — you must fetch creativecommons.org to learn what BY means.
// Ours is not: the materialisation carries the profile AND the expanded values,
// so if every explanation page on the internet vanished, `train-ai: n ·
// search: y · attribution: required` still says everything operative.
//
// The reason is ownership, not aesthetics. Putting a polis.pub URL into
// everyone's signed frontmatter would make polis.pub a permanent, unrevocable
// dependency of every author's terms, baked into bytes they cannot edit without
// republishing — and would make us the authority on what other people's terms
// mean, forever. The only URL in a materialisation points at the author's own
// domain.
const (
	// ProfileReserved — open for reach, reserved for extraction. The `init`
	// recommendation.
	ProfileReserved = "pub.polis.license.reserved/1"
	// ProfileOpen — everything permitted, including training.
	ProfileOpen = "pub.polis.license.open/1"
)

// AIPREF preference values (draft-ietf-aipref-vocab-06 §6.2). The vocabulary
// has exactly two: y and n. Anything else — including a missing key — resolves
// to "unknown", which is a defined state meaning the author has not said.
const (
	Allow    = "y"
	Disallow = "n"
)

// Terms is one resolved statement of licence terms.
//
// The AIPREF half is `TrainAI` and `Search` — and that is the WHOLE AIPREF
// vocabulary, not a subset of it. Table 1 of draft-ietf-aipref-vocab-06 has two
// rows, and §4.3 forbids any future extension from defining a category that
// contains an existing one, so no umbrella category can ever be added above
// them. Stating both is therefore full coverage: any subset category registered
// later resolves through §5's recursion by walking up to whichever of these two
// contains it.
//
// (Beware summaries claiming an `ai` key: draft-ietf-aipref-attach-04 §3.4
// narrates its own example as "ai=y" where the example reads `train-ai=y`.
// That string appears nowhere in the vocabulary. It is editorial residue from
// the WG's rename of "Foundation Model Production" to "AI Model Training".)
//
// Everything AIPREF cannot express rides the RSL half — attribution strength,
// extent, compensation — because projecting a condition into a bare AIPREF
// permission over-grants, and projecting it into a denial costs reach.
type Terms struct {
	// V is the schema namespace; always SchemaVersion for terms we write.
	V string `json:"v"`
	// Profile is the semantic tag naming this selection.
	Profile string `json:"profile"`

	// TrainAI is AIPREF's `train-ai` (vocab-06 §4.1) — "the act of using an
	// asset in the production or refinement of an AI model that can generate
	// content in one or more modalities". PREFERENCE layer.
	TrainAI string `json:"train-ai,omitempty"`
	// Search is AIPREF's `search` (vocab-06 §4.2) — use in an application whose
	// primary purpose is to select assets and DIRECT USERS TO them. The
	// link-back and the exclusion of summarisation are both inside the category
	// definition, not conditions we attach. PREFERENCE layer.
	Search string `json:"search,omitempty"`

	// AIInput is RSL's `ai-input` usage — the work fetched and summarised into
	// an answer at inference time. LICENCE-TERMS layer, and RSL-only.
	//
	// It is here because AIPREF cannot express it and it is the axis actually
	// hurting publishers: `search` excludes summarisation by definition
	// (vocab-06 §4.2) and `train-ai` is about model production, so an answer
	// engine that summarises without linking back falls under NEITHER AIPREF
	// category and resolves to `unknown`. Leaving the live wound unstated is
	// the thing Decision 2 named — absence "leaves crawlers latitude to act and
	// claim ambiguity".
	//
	// It is deliberately NOT projected into the AIPREF surfaces. Inventing an
	// AIPREF key for it would be authoring vocabulary, and mapping it onto
	// `search=n` would cost exactly the reach the default exists to protect.
	AIInput string `json:"ai-input,omitempty"`

	// Attribution is RSL's attribution requirement. LICENCE-TERMS layer — this
	// one is a grant on a condition, not a wish.
	Attribution string `json:"attribution,omitempty"`

	// Terms points at the author's own human-readable terms page.
	Terms string `json:"terms,omitempty"`
	// Contact is where to ask for anything not granted above. This is what
	// makes a licence more than a NO sign — for anything to ask, there has to
	// be somewhere to ask.
	Contact string `json:"contact,omitempty"`

	// Asserted is when these terms were stated, RFC3339. It is what makes
	// "what were the terms in March?" answerable, which non-retroactivity
	// requires.
	Asserted string `json:"asserted,omitempty"`
}

// Attribution values (RSL).
const AttributionRequired = "required"

// File is the on-disk shape of license.json — the site-level source of truth.
//
// It is an ordinary signed claim by an author about her own terms, and it is
// published. The signature covers everything except the signature itself and
// the version hash derived from it, matching the pub.polis.tag convention.
type File struct {
	Type      string `json:"type"`
	Terms     *Terms `json:"terms"`
	Created   string `json:"created"`
	Updated   string `json:"updated"`
	Generator string `json:"generator"`
	Version   string `json:"current_version"`
	Signature string `json:"signature"`

	// unrecognised are the JSON members of the parsed document that this build
	// does not declare — captured by Parse, consulted by Status (SIGNET epic
	// 47, tolerant verifiers).
	//
	// ⚠️ Nil for a licence this process built in memory, and that is correct
	// rather than a gap: there were no foreign bytes to meet.
	//
	// ⭐ THIS TYPE IS THE ONE MOST LIKELY TO GROW A FIELD. AIPREF and RSL are
	// live standards with their own release cycles, and `terms` carries their
	// vocabulary — so a member this build has never heard of is the EXPECTED
	// case here rather than the exotic one.
	unrecognised []string
}

// UnrecognisedFields returns the JSON members this build does not declare, for
// a licence that came from Parse or Load. Nil for anything built in memory.
//
// A caller that reports a `valid` status MUST also report this when it is
// non-empty: the signature verified over the fields this build knows, and these
// are NOT covered by it (SIGNET epic 47, row 2 of the table).
func (f *File) UnrecognisedFields() []string {
	if f == nil {
		return nil
	}
	return f.unrecognised
}

// Parse reads a licence from raw JSON bytes, recording any members this build
// does not declare.
//
// ⭐ EVERY PATH THAT TURNS FOREIGN BYTES INTO A File MUST COME THROUGH HERE —
// Load for a file on disk, sitecheck for one fetched over HTTP. A plain
// json.Unmarshal silently drops the unknown members, and then a verifier cannot
// tell "these terms were altered" from "these terms use vocabulary newer than
// me".
func Parse(raw []byte) (*File, error) {
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	f.unrecognised = signing.UnrecognisedFields(raw, File{})
	return &f, nil
}

// SignatureStatus is the outcome of checking a licence's signature.
//
// ⛔ ADDED BY SIGNET EPIC 47, AND ITS ABSENCE UNTIL NOW WAS THE FINDING. The
// other five JSON-family types each declared this enum; `license.Verify`
// returned a bare bool, so the package had no way to say *"I could not check
// this"* at all and every caller had to invent the third answer for itself. A
// tolerant verifier needs a vocabulary with `unknown` in it.
//
// The same four values, in the same order, for the same reason: collapsing any
// two makes a judgment the protocol is not allowed to make on a consumer's
// behalf (Law 2).
type SignatureStatus string

const (
	// StatusUnsigned — no signature. A FACT, not a defect: absent means
	// unstated, and a site that has never stated terms is the common case.
	StatusUnsigned SignatureStatus = "unsigned"
	// StatusValid — signature present and verifies.
	StatusValid SignatureStatus = "valid"
	// StatusInvalid — signature present and does NOT verify. Report it; a
	// wrong signature is evidence, not a verdict.
	StatusInvalid SignatureStatus = "invalid"
	// StatusUnknown — could not check: no key, or (epic 47) signed over a
	// field set wider than this build declares.
	StatusUnknown SignatureStatus = "unknown"
)

// TypeName is the content type this file belongs to.
const TypeName = "pub.polis.license"

// DefaultPath returns the default on-disk location of license.json.
//
// It is only a DEFAULT. Discovery is by pointer — consumers read
// `.well-known/polis` → `license` and follow it, never an assumed path. `dir`
// and `mount` are per-type declarations in bundle.json, so site layout is
// user-configurable and a hardcoded path would contradict a decision already
// made. Everything in this package that needs the real location takes it from
// the pointer; this function exists for provisioning and for defaults.
func DefaultPath(dataDir string) string {
	return filepath.Join(dataDir, "content", "pub.polis.core", "license", "license.json")
}

// Load reads and parses a licence file from disk.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read licence file: %w", err)
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse licence file: %w", err)
	}
	return f, nil
}

// canonicalJSON produces the deterministic bytes that get signed. Excludes the
// signature and the version hash (which is derived from these bytes).
func canonicalJSON(f *File) ([]byte, error) {
	signable := struct {
		Type      string `json:"type"`
		Terms     *Terms `json:"terms"`
		Created   string `json:"created"`
		Updated   string `json:"updated"`
		Generator string `json:"generator"`
	}{
		Type:      f.Type,
		Terms:     f.Terms,
		Created:   f.Created,
		Updated:   f.Updated,
		Generator: f.Generator,
	}
	return json.Marshal(signable)
}

// SignAndWrite computes the version hash, signs, and atomically writes the
// licence file.
//
// ⛔ It REFUSES to rewrite a licence file carrying members this build does not
// declare (signing.GuardRewrite): stating new terms over it would drop them and
// sign the rest with the user's key. The way past is RewriteUnsigned.
func SignAndWrite(f *File, path string, privateKey []byte) error {
	if err := signing.GuardRewrite("license.json", path, File{}); err != nil {
		return err
	}
	canonical, err := canonicalJSON(f)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}

	hash := sha256.Sum256(canonical)
	f.Version = "sha256:" + hex.EncodeToString(hash[:])

	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return fmt.Errorf("sign licence: %w", err)
	}
	f.Signature = sig

	return writeFile(f, path)
}

// RewriteUnsigned is the ESCAPE HATCH for a user stranded by SignAndWrite's
// refusal: it rewrites the licence file at path from the members this build
// declares, WITHOUT a signature, and returns what it dropped.
//
// ⛔ It never signs, and it states no terms: it only removes what this build
// cannot read. An unsigned licence is reported as such by every verifier, so
// the site's terms read as unverified until the user states them again.
// current_version is recomputed, being a hash rather than a claim. A file with
// nothing unrecognised is left untouched.
func RewriteUnsigned(path string) ([]string, error) {
	f, err := Load(path)
	if err != nil {
		return nil, err
	}
	dropped := f.UnrecognisedFields()
	if len(dropped) == 0 {
		return nil, nil
	}
	canonical, err := canonicalJSON(f)
	if err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	hash := sha256.Sum256(canonical)
	f.Version = "sha256:" + hex.EncodeToString(hash[:])
	f.Signature = ""
	if err := writeFile(f, path); err != nil {
		return nil, err
	}
	return dropped, nil
}

// writeFile marshals and atomically writes a licence file as-is.
func writeFile(f *File, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create licence directory: %w", err)
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal licence: %w", err)
	}
	data = append(data, '\n')

	// 0644: this is PUBLIC served content. Self-hosters serving
	// content/pub.polis.core/ through nginx under a different user must be able
	// to read it — don't tighten a public-facing file to 0600.
	if err := atomicfile.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write licence file: %w", err)
	}
	return nil
}

// Verify checks a licence file's signature against a public key.
//
// ⚠️ IT IS THE RAW PREDICATE, and a bare bool cannot carry the tolerance rule.
// Callers that report a status want VerifyStatus; a caller that must run the
// key-history walk itself (sitecheck, because pkg/site imports this package and
// the dependency cannot be reversed) calls this inside the walk and then passes
// the walk's answer to Status.
func Verify(f *File, publicKeySSH []byte) (bool, error) {
	canonical, err := canonicalJSON(f)
	if err != nil {
		return false, fmt.Errorf("canonical JSON: %w", err)
	}
	return signing.VerifySignature(canonical, publicKeySSH, f.Signature)
}

// Status applies the tolerance rule to a verification outcome obtained
// elsewhere, returning the status to report and the sentence that explains it
// (empty when the status says everything).
//
// ⛔ THIS IS THE PACKAGE'S ONE DECISION POINT, and it is exported because this
// type's key-history walk lives OUTSIDE the package: pkg/site imports
// pkg/license (site.ProvisionLicense), so pkg/license cannot import pkg/site
// and cannot host a VerifyWithHistory the way pkg/tag and pkg/attestation do.
// Splitting the walk out is forced; splitting the RULE out would let the two
// call sites drift.
func Status(f *File, verified bool) (SignatureStatus, string) {
	out := signing.Resolve(verified, f.UnrecognisedFields())
	return SignatureStatus(out.Status), out.Explain()
}

// VerifyStatus is Verify reporting a status instead of a bool, with the
// tolerance rule applied (SIGNET epic 47).
//
// The returned error explains an invalid or unknown result. It is context for a
// human, not a second channel of truth — read the status.
func VerifyStatus(f *File, publicKeySSH []byte) (SignatureStatus, error) {
	if f == nil || f.Signature == "" {
		return StatusUnsigned, nil
	}
	if len(publicKeySSH) == 0 {
		return StatusUnknown, fmt.Errorf("no public key to verify against")
	}
	ok, err := Verify(f, publicKeySSH)
	status, why := Status(f, ok && err == nil)
	switch status {
	case StatusUnknown:
		return StatusUnknown, errors.New(why)
	case StatusInvalid:
		if err != nil {
			return StatusInvalid, fmt.Errorf("signature does not verify: %w", err)
		}
		return StatusInvalid, fmt.Errorf("signature does not verify against the site identity key")
	}
	return StatusValid, nil
}

// ProfileTerms returns the expanded values for a named profile.
//
// baseURL is the author's own site — the only URL that ends up in the payload.
// Pass "" to omit the terms and contact pointers.
func ProfileTerms(profile, baseURL, asserted string) (*Terms, error) {
	t := &Terms{V: SchemaVersion, Profile: profile, Asserted: asserted}

	switch profile {
	case ProfileReserved:
		// Open for reach, reserved for extraction.
		//
		// The argument is asymmetry, not taste: an incorrect grant is
		// irreversible and an incorrect denial is a conversation. Terms are not
		// retroactive, so a default that over-grants can never be walked back
		// for content already published — the exact trap Creative Commons had
		// to publish a primer about, where a plain reading of CC BY permits
		// training and people licensed their archives in 2015 without knowing.
		t.TrainAI = Disallow
		t.Search = Allow
		// The extraction axis, closed on both halves: no model production
		// (AIPREF train-ai) and no summarising into an answer (RSL ai-input).
		// Reach stays open via `search`, which is defined as use that links
		// back — the author keeps readers, and the thing that took them stays
		// reserved.
		t.AIInput = Disallow
		t.Attribution = AttributionRequired
	case ProfileOpen:
		t.TrainAI = Allow
		t.Search = Allow
		t.AIInput = Allow
	default:
		return nil, fmt.Errorf("unknown licence profile: %s", profile)
	}

	if baseURL != "" {
		base := strings.TrimRight(baseURL, "/")
		t.Terms = base + "/license"
		t.Contact = base + "/license"
	}
	return t, nil
}

// ParseProfileName maps the short names accepted on the command line and in the
// init prompt to profile identifiers. "none" returns ("", false) — a genuine
// choice to publish no terms, not an error.
func ParseProfileName(name string) (profile string, stated bool, err error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "reserved":
		return ProfileReserved, true, nil
	case "open":
		return ProfileOpen, true, nil
	case "none", "unstated", "":
		return "", false, nil
	default:
		return "", false, fmt.Errorf("unknown licence choice %q (want: reserved, open, none)", name)
	}
}

// New builds a licence file for a profile, ready to sign.
func New(profile, baseURL string) (*File, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	terms, err := ProfileTerms(profile, baseURL, now)
	if err != nil {
		return nil, err
	}
	return &File{
		Type:      TypeName,
		Terms:     terms,
		Created:   now,
		Updated:   now,
		Generator: GetGenerator(),
	}, nil
}

// Validate checks that a Terms carries only values the standards define.
//
// This is the enforcement point for "we name selections, we never author
// terms". If a value ever needs to be accepted here that no standard defines,
// that is the signal to stop and escalate rather than to widen this function.
func (t *Terms) Validate() error {
	if t == nil {
		return fmt.Errorf("terms are nil")
	}
	if t.V != SchemaVersion {
		return fmt.Errorf("unknown licence schema version %q (want %s)", t.V, SchemaVersion)
	}
	if t.Profile == "" {
		return fmt.Errorf("licence has no profile")
	}
	for _, f := range []struct{ name, val string }{
		{"train-ai", t.TrainAI},
		{"search", t.Search},
		{"ai-input", t.AIInput},
	} {
		// Absent is legal and means unknown — AIPREF vocab-06 §5. Only a
		// present-but-wrong value is an error.
		if f.val != "" && f.val != Allow && f.val != Disallow {
			return fmt.Errorf("licence key %s must be %q or %q, got %q", f.name, Allow, Disallow, f.val)
		}
	}
	if t.Attribution != "" && t.Attribution != AttributionRequired {
		return fmt.Errorf("attribution must be %q if present, got %q", AttributionRequired, t.Attribution)
	}
	return nil
}
