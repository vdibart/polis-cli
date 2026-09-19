package sitecheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// notServedOverHTTP is the reason every owner-only check gives in remote form.
const notServedOverHTTP = "a site's private state is never served over HTTP — only someone with the directory can check keys, permissions, private policies and the bundle registry"

// indexFreshness is the limit every statement about an enumeration must carry.
//
// ⛔ Signet epic 37 D4. Remote form used to print "a polis site publishes no
// index of these, and HTTP does not list directories" for tags and
// attestations. It was HALF TRUE, and nobody could tell which half: epic 25 put
// both types in index.jsonl, but only a rebuild wrote them until epic 37 made
// them write-triggered — so the sentence was false of some sites, literally true
// of the operator site, and printed for all of them.
//
// ⚠️ The replacement must not trade one overclaim for the opposite one. What
// survives is this: an index is as fresh as its last write or heal, and a type
// with no entries means NOTHING INDEXED, never nothing exists.
const indexFreshness = "an index is only as fresh as the site's last write or heal"

// Remote validates a site it has no copy of, over public HTTP, STORING
// NOTHING. Nothing here writes to disk; a caller who wants the files should run
// `polis clone` and validate the directory.
//
// The identity key is cached PER DOMAIN, so a site with 500 posts costs one key
// fetch rather than 500.
type Remote struct {
	client   *remote.Client
	keyCache map[string][]byte
	keyErrs  map[string]error

	// chains and chainNotes hold each domain's usable key history (epic 31),
	// parsed from the SAME .well-known/polis fetch as the key — the history
	// costs no second request.
	chains     map[string]*site.KeyHistoryBlock
	chainNotes map[string]string

	// SIGNET epic 32: each domain's `witnesses` pointer (from the same
	// .well-known/polis fetch), and the witness set it leads to, fetched once.
	pointers     map[string]string
	witnessFiles map[string]*site.WitnessFile
	witnessNotes map[string]string

	// DSKeys is where witness signatures get their discovery-service keys (D8).
	// NewRemote fetches them over HTTPS from the DS each witness names; a test
	// or a caller with pinned keys replaces it. An unreachable DS leaves a
	// witness unchecked, never failed.
	DSKeys DSKeyLookup

	// bodies remembers every artifact fetched, so the index pass and the content
	// passes over one site fetch each artifact once (SIGNET epic 44).
	bodies map[string]fetched

	// KeyFetches counts how many times a key was actually fetched, as opposed
	// to served from the cache. Exported so a test can assert the caching is
	// real rather than assumed.
	KeyFetches int
}

// NewRemote builds a fetcher for remote validation.
func NewRemote() *Remote {
	return &Remote{
		client:       remote.NewClient(),
		keyCache:     map[string][]byte{},
		keyErrs:      map[string]error{},
		chains:       map[string]*site.KeyHistoryBlock{},
		chainNotes:   map[string]string{},
		pointers:     map[string]string{},
		witnessFiles: map[string]*site.WitnessFile{},
		witnessNotes: map[string]string{},
		DSKeys:       NewDSKeyLookup(nil, ""),
	}
}

// witnessSet returns a site's published witness set, fetched at most once, or
// nil and the reason there is none to read.
func (rr *Remote) witnessSet(base, pointer string) (*site.WitnessFile, string) {
	if f, ok := rr.witnessFiles[base]; ok {
		return f, rr.witnessNotes[base]
	}
	f, note := rr.fetchWitnessSet(base, pointer)
	rr.witnessFiles[base], rr.witnessNotes[base] = f, note
	return f, note
}

func (rr *Remote) fetchWitnessSet(base, pointer string) (*site.WitnessFile, string) {
	if pointer == "" {
		return nil, noWitnessesPublished
	}
	if strings.Contains(pointer, "://") {
		return nil, fmt.Sprintf("the site's `witnesses` pointer %q is not a path on the site, so it was not followed", pointer)
	}
	body, err := rr.client.FetchContent(base + "/" + strings.TrimPrefix(pointer, "/"))
	if err != nil {
		return nil, fmt.Sprintf("the site's `witnesses` pointer names %s, which does not serve (%v). Nothing is failed for it — a witness only ever adds a claim.", pointer, err)
	}
	f, perr := site.WitnessesFromBytes([]byte(body))
	if perr != nil {
		return nil, fmt.Sprintf("the witness file at %s is served and does not parse (%v). Nothing is failed for it — a witness only ever adds a claim.", pointer, perr)
	}
	return f, ""
}

// PublishedKey returns a domain's identity key, fetching it at most once.
func (rr *Remote) PublishedKey(baseURL string) ([]byte, error) {
	base := strings.TrimRight(baseURL, "/")
	if key, ok := rr.keyCache[base]; ok {
		return key, rr.keyErrs[base]
	}
	rr.KeyFetches++

	body, err := rr.client.FetchContent(base + "/.well-known/polis")
	if err != nil {
		err = fmt.Errorf("could not fetch %s/.well-known/polis: %w", base, err)
		rr.keyCache[base], rr.keyErrs[base] = nil, err
		return nil, err
	}
	keys, err := IssuerKeysFromWellKnown(base, []byte(body))
	if err != nil {
		rr.keyCache[base], rr.keyErrs[base] = nil, err
		return nil, err
	}
	rr.keyCache[base] = keys.PublicKey
	rr.keyErrs[base] = nil
	rr.pointers[base] = site.WitnessesPointerFromWellKnown([]byte(body))
	rr.chains[base], rr.chainNotes[base] = keys.Chain, keys.ChainNote
	return rr.keyCache[base], nil
}

// remoteResolvingChain is the key history a remote run may resolve retired keys
// from. ⭐ The domain is the host fetched from, port stripped — the same
// derivation checkRemoteKeyHistory uses and both rotation paths sign.
func remoteResolvingChain(base string, wkBody []byte, publicKey string) (*site.KeyHistoryBlock, string) {
	block, err := KeyHistoryFromWellKnownBytes(wkBody)
	if err != nil {
		return nil, ""
	}
	return ResolvingChain(block, polisurl.ExtractDomain(base), publicKey)
}

// RunArtifact checks ONE published artifact: is this thing signed by who it
// says?
//
// ⭐ Two fetches — the artifact, and the issuer's .well-known/polis for the key —
// and the key is cached per domain, so checking many artifacts on one site
// costs one key fetch.
//
// ⚠️ The result verifies THAT ARTIFACT and says nothing about the site around
// it. Report.Note carries that caveat so the answer cannot be read as a
// site-level claim.
func (rr *Remote) RunArtifact(contentURL string) *Report {
	r := &Report{
		Target: contentURL,
		Form:   FormRemote,
		Scope:  ScopeArtifact,
		Note:   "This result covers ONE artifact's signature and hash. Index consistency, key permissions, policy files and everything else site-wide were not examined.",
	}

	body, err := rr.client.FetchContent(contentURL)
	if err != nil {
		r.FatalError = fmt.Sprintf("could not fetch %s: %v", contentURL, err)
		return r
	}

	baseURL := remote.ExtractBaseURL(contentURL)

	// SIGNET epic 42 V2: a JSON record is the other signing family. It used to
	// fall through to ParseFrontmatter and read "no frontmatter found" even when
	// it verified.
	if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "{") {
		rr.runRecord(r, contentURL, baseURL, []byte(trimmed))
		return r
	}

	pubKey, keyErr := rr.PublishedKey(baseURL)
	if keyErr != nil {
		r.na("content.artifact", FamilyContent, "no key to verify against — "+keyErr.Error())
		return r
	}

	fm, _, perr := ParseFrontmatter(body)
	if perr != nil {
		r.fail("content.artifact", FamilyContent, "not a signed polis artifact: "+perr.Error(), 1)
		return r
	}

	base := strings.TrimRight(baseURL, "/")
	chain, chainNote := rr.chains[base], rr.chainNotes[base]
	res := VerifyContentWithHistory(body, pubKey, chain, fm.ObjectType())
	switch {
	case res.Signature == SigValid && res.Key.Source == KeyRetired:
		r.pass("content.artifact", FamilyContent, "signature "+res.Key.Describe()+" (site: "+baseURL+")", 1)
	case res.Signature == SigValid:
		r.pass("content.artifact", FamilyContent, "signature verifies against the key published at "+baseURL, 1)
	case res.Signature == SigUnsigned:
		r.pass("content.artifact", FamilyContent, "unsigned — nothing to verify, which is a legal state", 1)
	default:
		detail := errText(res.SigError)
		if chain == nil && chainNote != "" {
			detail = chainNote
		}
		r.fail("content.artifact", FamilyContent, "signature does NOT verify against the key published at "+baseURL, 1, detail)
	}

	switch res.Hash {
	case HashValid:
		r.pass("content.hash", FamilyContent, "body matches its current-version hash", 1)
	case HashMismatch:
		r.fail("content.hash", FamilyContent, "body does NOT match its current-version hash", 1)
	default:
		r.na("content.hash", FamilyContent, "the artifact declares no current-version, so there is no hash to check")
	}

	// SIGNET epic 32 D8 — the witness axis, from the site's published set.
	witFile, witNote := rr.witnessSet(base, rr.pointers[base])
	reportArtifactWitness(r, witFile, witNote, contentURL, res, rr.DSKeys)

	return r
}

// ---------- JSON records (SIGNET epic 42 V2) ----------

// recordProbe reads just enough of a JSON document to tell which record type it
// is and whose key signed it.
type recordProbe struct {
	Type      string          `json:"type"`
	Issuer    string          `json:"issuer"`
	Tag       json.RawMessage `json:"tag"`
	Following json.RawMessage `json:"following"`
	Comments  json.RawMessage `json:"comments"`
}

// recordKind is one JSON record type a per-URL run can verify.
//
// ⚠️ A TABLE, NOT A FRAMEWORK (M2). Each row calls the predicate its own package
// exports — the one `polis validate <dir>`, Judge and the fleet canary already
// call — so nothing here rebuilds a signing base.
type recordKind struct {
	name string
	is   func(p recordProbe) bool
	// signer is the base URL whose published key must verify the record, when
	// the record names one. nil means the host it was served from.
	signer func(p recordProbe) string
	// verify checks the record against the signer's current key and, through
	// chain (nil = current key only), the retired key its claimed signing time
	// selects (SIGNET epic 44 E5).
	verify func(body, pubKey []byte, chain *site.KeyHistoryBlock) recordResult
	// signingTime names the field that is the type's CLAIMED signing time, ""
	// when the type carries none — and then no retired key can ever be selected
	// for it, which a failure must say.
	signingTime string
	// versionRule names the indexEntryRules row that recomputes current_version,
	// "" when this run has none for the type.
	versionRule string
}

// recordResult is what one recordKind row found.
type recordResult struct {
	// status is "valid", "unsigned", "invalid", "unknown", or "" when the body
	// does not parse as this type at all.
	status string
	// version is the record's declared current_version, "" when it has none.
	version string
	// claimed is the record's claimed signing time.
	claimed string
	// retired is the history entry that verified it; nil for the current key.
	retired *site.KeyHistoryEntry
	err     error
}

// historyStatus maps site.VerifyWithHistory onto a record status, for the types
// whose own package has no history-aware predicate to call.
//
// unrecognised is the fetched document's unrecognised-member list, so the
// tolerance rule (SIGNET epic 47) applies here too: a record no key in the
// chain verifies, carrying members this build does not declare, is "I could not
// check this" rather than "this is forged".
func historyStatus(signature string, verify func(key []byte) (bool, error), pubKey []byte, chain *site.KeyHistoryBlock, claimed, field string, unrecognised []string) (string, *site.KeyHistoryEntry, error) {
	if signature == "" {
		return "unsigned", nil, nil
	}
	ok, retired, err := site.VerifyWithHistory(verify, pubKey, chain, claimed, field)
	out := signing.Resolve(ok, unrecognised)
	switch out.Status {
	case signing.StatusUnknown:
		return signing.StatusUnknown, nil, errors.New(out.Explain())
	case signing.StatusInvalid:
		return signing.StatusInvalid, nil, err
	}
	return signing.StatusValid, retired, nil
}

// recordKinds is checked in order; the types with a `type` discriminator come
// before the ones recognised by a distinctive field.
//
// ⛔ NO ACTOR REGISTRY ROW, and not by oversight: pkg/actor imports this
// package, so importing pkg/actor here is an import cycle (first found by
// epic 42's full test run).
var recordKinds = []recordKind{
	{
		name: "attestation",
		is:   func(p recordProbe) bool { return p.Type == attestation.TypeName },
		// docs/signet/spec/attestation.md §9: read `issuer`, verify against THAT
		// site's key — a copy served from elsewhere is still the issuer's claim.
		signer: func(p recordProbe) string { return p.Issuer },
		verify: func(body, key []byte, chain *site.KeyHistoryBlock) recordResult {
			// ⭐ Parse, not json.Unmarshal: it records the members this build
			// does not declare, which is what makes the verifier tolerant
			// (SIGNET epic 47). A plain unmarshal drops them silently and the
			// verifier then reports a newer record as forged.
			rec, err := attestation.Parse(body)
			if err != nil {
				return recordResult{err: err}
			}
			s, retired, err := attestation.VerifyWithHistory(rec, key, chain)
			return recordResult{string(s), rec.Version, rec.Asserted, retired, err}
		},
		signingTime: "asserted",
		versionRule: "attestation",
	},
	{
		name: "licence",
		is:   func(p recordProbe) bool { return p.Type == license.TypeName },
		verify: func(body, key []byte, chain *site.KeyHistoryBlock) recordResult {
			f, err := license.Parse(body)
			if err != nil {
				return recordResult{err: err}
			}
			s, retired, err := historyStatus(f.Signature, func(k []byte) (bool, error) { return license.Verify(f, k) }, key, chain, f.Updated, "updated", f.UnrecognisedFields())
			return recordResult{s, f.Version, f.Updated, retired, err}
		},
		signingTime: "updated",
	},
	{
		name: "tag file",
		is:   func(p recordProbe) bool { return len(p.Tag) > 0 },
		verify: func(body, key []byte, chain *site.KeyHistoryBlock) recordResult {
			tf, err := tag.Parse(body)
			if err != nil {
				return recordResult{err: err}
			}
			s, retired, err := tag.VerifyWithHistory(tf, key, chain)
			return recordResult{string(s), tf.Version, tf.Updated, retired, err}
		},
		signingTime: "updated",
		versionRule: "tag",
	},
	{
		// ⚠️ No signing time: `version` is a generator string. Current key only.
		name: "follow file",
		is:   func(p recordProbe) bool { return len(p.Following) > 0 },
		verify: func(body, key []byte, _ *site.KeyHistoryBlock) recordResult {
			f, err := following.Parse(body)
			if err != nil {
				return recordResult{err: err}
			}
			s, err := following.Verify(f, key)
			return recordResult{status: string(s), err: err}
		},
	},
	{
		// ⚠️ No signing time, as the follow file. Current key only.
		name: "blessing list",
		is:   func(p recordProbe) bool { return len(p.Comments) > 0 },
		verify: func(body, key []byte, _ *site.KeyHistoryBlock) recordResult {
			bc, err := metadata.ParseBlessed(body)
			if err != nil {
				return recordResult{err: err}
			}
			s, err := metadata.VerifyBlessed(bc, key)
			return recordResult{status: string(s), err: err}
		},
	},
}

// recordKindFor returns the first record type the probe matches, or nil.
func recordKindFor(p recordProbe) *recordKind {
	for i := range recordKinds {
		if recordKinds[i].is(p) {
			return &recordKinds[i]
		}
	}
	return nil
}

func recordKindNames() string {
	names := make([]string, len(recordKinds))
	for i, k := range recordKinds {
		names[i] = k.name
	}
	return strings.Join(names, ", ")
}

// runRecord is RunArtifact for the JSON signing family: which record this is,
// whether its signature verifies against the key of the site that signed it,
// whether its bytes are its current_version, and the witness axis.
func (rr *Remote) runRecord(r *Report, contentURL, baseURL string, body []byte) {
	v := rr.verifyRecord(body, baseURL)
	switch {
	case v.parseErr != nil:
		r.fail("content.artifact", FamilyContent, "not a signed polis artifact: a JSON document that does not parse: "+v.parseErr.Error(), 1)
		return
	case v.kind == nil:
		r.fail("content.artifact", FamilyContent, "not a signed polis artifact: a JSON document of no record type this run recognises ("+recordKindNames()+")", 1)
		return
	case v.keyErr != nil:
		r.na("content.artifact", FamilyContent, "no key to verify the "+v.kind.name+" against — "+v.keyErr.Error())
		return
	}

	kind := v.kind
	where := "the key published at " + v.signer
	switch v.status {
	case "valid":
		if used := KeyUsedFor(rr.chains[v.signer], v.retired, v.claimed); used.Source == KeyRetired {
			r.pass("content.artifact", FamilyContent, kind.name+" signature "+used.Describe()+" (site: "+v.signer+")", 1)
		} else {
			r.pass("content.artifact", FamilyContent, kind.name+" signature verifies against "+where, 1)
		}
	case "unsigned":
		r.pass("content.artifact", FamilyContent, "unsigned "+kind.name+" — nothing to verify, which is a legal state", 1)
	case "invalid":
		r.fail("content.artifact", FamilyContent, kind.name+" signature does NOT verify against "+where, 1, rr.recordFailureDetail(v))
	case "":
		r.fail("content.artifact", FamilyContent, "not a signed polis artifact: recognised as a "+kind.name+" and does not parse as one: "+errText(v.err), 1)
		return
	default:
		r.na("content.artifact", FamilyContent, "the "+kind.name+" could not be verified — "+errText(v.err))
	}

	rule, hasRule := IndexEntryRuleFor(kind.versionRule)
	switch {
	case v.version == "":
		r.na("content.hash", FamilyContent, "the "+kind.name+" declares no current_version, so there is no hash to check")
	case !hasRule:
		r.na("content.hash", FamilyContent, "the "+kind.name+" declares a current_version and this run has no rule to recompute it, so it was NOT checked")
	default:
		if match, _ := rule.VersionMatches(body, v.version); match {
			r.pass("content.hash", FamilyContent, "record bytes match its current_version", 1)
		} else {
			r.fail("content.hash", FamilyContent, "record bytes do NOT match its current_version", 1)
		}
	}

	// SIGNET epic 32 D8 — a record's witness binds its version, from the signing
	// site's published set.
	witFile, witNote := rr.witnessSet(v.signer, rr.pointers[v.signer])
	reportArtifactWitness(r, witFile, witNote, contentURL, ContentResult{CurrentVersion: v.version}, rr.DSKeys)
}

// recordVerdict is one JSON record, identified and verified.
type recordVerdict struct {
	recordResult
	parseErr error       // not JSON
	kind     *recordKind // nil: JSON of no record type this run recognises
	signer   string      // the base URL whose key the record was checked against
	keyErr   error       // no key could be read for the signer
}

// verifyRecord identifies a JSON record and verifies it against the key of the
// site that signed it — its `issuer` where it names one, else host — resolving a
// retired key through that site's published history. The one path for a
// per-URL run and a site-wide run (SIGNET epic 44 F2).
func (rr *Remote) verifyRecord(body []byte, host string) recordVerdict {
	var v recordVerdict
	var p recordProbe
	if err := json.Unmarshal(body, &p); err != nil {
		v.parseErr = err
		return v
	}
	if v.kind = recordKindFor(p); v.kind == nil {
		return v
	}
	v.signer = strings.TrimRight(host, "/")
	if v.kind.signer != nil {
		if s := strings.TrimRight(v.kind.signer(p), "/"); s != "" {
			v.signer = s
		}
	}
	pubKey, keyErr := rr.PublishedKey(v.signer)
	if keyErr != nil {
		v.keyErr = keyErr
		return v
	}
	v.recordResult = v.kind.verify(body, pubKey, rr.chains[v.signer])
	return v
}

// recordFailureDetail explains an invalid record signature. When the signing
// site's history was consulted, the verifier's own reason says what was tried;
// a type with no signing time says it cannot be resolved; an unusable history
// says why.
func (rr *Remote) recordFailureDetail(v recordVerdict) string {
	detail := errText(v.err)
	if chain := rr.chains[v.signer]; chain != nil && len(chain.History) > 0 && v.kind.signingTime == "" {
		return detail + " — the site has rotated its key, and a " + v.kind.name + " carries no signing time, so no retired key can be selected for it; one signed before the rotation reads as invalid here"
	}
	if note := rr.chainNotes[v.signer]; note != "" {
		return detail + " — " + note
	}
	return detail
}

// RunSite checks a whole site over public HTTP.
//
// ⛔ It stores nothing and it never clones. Cloning is `polis clone`'s job; a
// user who wants the files composes the two commands.
func (rr *Remote) RunSite(baseURL string) *Report {
	base := strings.TrimRight(normalizeBase(baseURL), "/")
	r := &Report{
		Target: base,
		Form:   FormRemote,
		Scope:  ScopeSite,
		Note:   "Checked over public HTTP, storing nothing. Only what the site serves publicly could be examined; see the not-applicable checks for what that leaves out.",
	}

	wkBody, err := rr.client.FetchContent(base + "/.well-known/polis")
	if err != nil {
		r.FatalError = fmt.Sprintf("could not fetch %s/.well-known/polis: %v", base, err)
		return r
	}

	var wk struct {
		PublicKey string `json:"public_key"`
		License   string `json:"license"`
		Bundles   map[string]struct {
			Path string `json:"path"`
		} `json:"bundles"`
	}
	if jerr := json.Unmarshal([]byte(wkBody), &wk); jerr != nil {
		r.fail("identity.well_known", FamilyIdentity, ".well-known/polis is served and does not parse: "+jerr.Error(), 1)
	} else if wk.PublicKey == "" {
		r.fail("identity.well_known", FamilyIdentity, ".well-known/polis is served and publishes no public_key", 1)
	} else {
		r.pass("identity.well_known", FamilyIdentity, "identity document served, parseable, and publishes a key", 1)
	}

	pubKey := []byte(wk.PublicKey)

	// Owner-only state, honestly named.
	r.na("identity.key_files", FamilyIdentity, notServedOverHTTP)
	r.na("identity.key_perms", FamilyIdentity, notServedOverHTTP)
	r.na("identity.key_match", FamilyIdentity, notServedOverHTTP+" — the published key has nothing local to be compared against")
	r.na("bundle.registry", FamilyBundle, notServedOverHTTP)

	rr.checkRemoteDID(r, base, pubKey)
	rr.checkRemoteKeyHistory(r, base, []byte(wkBody), wk.PublicKey)
	reportMessagesKey(r, []byte(wkBody))
	rr.checkRemoteBundles(r, base, wk.Bundles)
	entries := rr.checkRemoteIndex(r, base, pubKey)
	rr.checkRemotePolicies(r, base)
	chain, chainNote := remoteResolvingChain(base, []byte(wkBody), wk.PublicKey)
	census := newWitnessCensus(rr.witnessSet(base, site.WitnessesPointerFromWellKnown([]byte(wkBody))))
	census.lookup = rr.DSKeys
	rr.checkRemoteContent(r, base, entries, pubKey, chain, chainNote, census)
	rr.checkRemoteRecordSets(r, base, entries, pubKey, census)
	census.emit(r, "the posts, comments, attestations and tags the index lists")
	rr.checkRemoteRecords(r, base, wk.License, pubKey, chain)

	return r
}

// checkRemoteRecordSets verifies the attestations and tag files a site's index
// lists, through the same verifyRecord a per-URL run uses, and reports them in
// the same words as the local form.
//
// ⛔ SIGNET epic 44 F2. This used to say "the site's index lists 2
// attestations, which this run does not verify" — an excellent disclosure of a
// capability epic 42 had already built and never wired to the surface people
// use. Produced but never consumed.
//
// A record the index lists and that does not serve, does not parse as its type,
// or whose issuer's key cannot be read is NOT CHECKED and named — never a
// failure here (index.consistency is where an orphan fails), never a pass.
func (rr *Remote) checkRemoteRecordSets(r *Report, base string, entries []remote.PublicIndexEntry, pubKey []byte, census *witnessCensus) {
	for _, spec := range []struct{ id, entryType, noun string }{
		{"content.attestations", "attestation", "attestation"},
		{"content.tags", "tag", "tag file"},
	} {
		if len(pubKey) == 0 {
			r.na(spec.id, FamilyContent, "no key to verify against — the site publishes no public_key")
			continue
		}
		t := recordTally{statuses: map[string]string{}}
		listed := 0
		for _, e := range entries {
			if e.Type != spec.entryType {
				continue
			}
			listed++
			name := e.GetPath()
			body, err := rr.fetch(contentURLFor(base, e))
			if err != nil {
				t.notChecked = append(t.notChecked, name+": indexed and does not serve")
				continue
			}
			trimmed := []byte(strings.TrimSpace(body))
			v := rr.verifyRecord(trimmed, base)
			switch {
			case v.parseErr != nil || v.kind == nil || v.kind.name != spec.noun || v.status == "":
				t.notChecked = append(t.notChecked, name+": indexed as a "+spec.noun+" and does not parse as one")
				continue
			case v.keyErr != nil:
				t.notChecked = append(t.notChecked, name+": no key to verify it against — "+v.keyErr.Error())
				continue
			}
			t.add(name, v.status, KeyUsedFor(rr.chains[v.signer], v.retired, v.claimed))
			censusRecordBody(census, spec.entryType, trimmed)
		}
		switch {
		case listed == 0:
			r.na(spec.id, FamilyContent, indexedRecordsNone(spec.noun))
		case len(t.statuses) == 0:
			r.na(spec.id, FamilyContent, fmt.Sprintf("the site's index lists %s and none could be checked (%s); %s",
				plural(listed, spec.noun), strings.Join(t.notChecked, "; "), indexFreshness))
		default:
			checkRecordSet(r, spec.id, spec.noun, "", t)
		}
	}
}

// censusRecordBody adds a fetched record to the witness census, exactly as the
// local form adds one read off disk.
func censusRecordBody(c *witnessCensus, entryType string, body []byte) {
	switch entryType {
	case "attestation":
		var rec attestation.Record
		if json.Unmarshal(body, &rec) == nil {
			censusAttestation(c, &rec)
		}
	case "tag":
		var tf tag.TagFile
		if json.Unmarshal(body, &tf) == nil {
			censusTag(c, &tf)
		}
	}
}

// indexedRecordsNone is the reason for a type the index lists nothing of.
//
// ⭐ SIGNET epic 44 D3 restores the `polis validate <record-url>` hint. HISTORY:
// epic 37 added it; epic 33 found it sent the reader somewhere broken
// (RunArtifact parsed every URL as markdown) and it was removed 2026-09-13
// (60213e00); epic 42 V2 fixed the command. ⛔ An instruction inside a true
// statement is a NEW CLAIM — this one was run against a live record before it
// was written back (see the epic 44 plan).
func indexedRecordsNone(noun string) string {
	return fmt.Sprintf("the site's index lists no %ss — that means nothing indexed, never that none exist: %s, and HTTP does not list directories. To check one whose address you know, run `polis validate <record-url>`",
		noun, indexFreshness)
}

// checkRemoteKeyHistory runs the same two predicates as the local form over the
// document this site actually serves.
//
// ⭐ The remote form is the STRONGER one for a key chain, and it is the only
// form a stranger has. It reads the bytes a third party would read, and — unlike
// the local form — it always knows the domain, because it is the host it just
// fetched from. That matters: the domain is inside every transition signature,
// so a local run on a site with no did.json cannot check a rotation that this
// can.
func (rr *Remote) checkRemoteKeyHistory(r *Report, base string, wkBody []byte, publicKey string) {
	block, err := KeyHistoryFromWellKnownBytes(wkBody)
	if err != nil {
		r.fail("identity.key_history", FamilyIdentity, "public_key_history is served and does not parse: "+err.Error(), 1)
		r.na("identity.key_history_head", FamilyIdentity, "the key history could not be read, so its head could not be compared with anything")
		r.na("identity.key_history_witness", FamilyIdentity, dsParityNotRun)
		return
	}
	if block == nil {
		if didDoc, ferr := rr.client.FetchContent(base + "/.well-known/did.json"); ferr == nil {
			if erased := ChainErasure(nil, []byte(didDoc)); !erased.OK {
				r.fail("identity.key_history", FamilyIdentity, erased.Message, 1)
				r.na("identity.key_history_head", FamilyIdentity, "the key history was erased, so there is no head to compare")
				r.na("identity.key_history_witness", FamilyIdentity, dsParityNotRun)
				return
			}
		}
		reason := "no public_key_history served in .well-known/polis — this site publishes only its current key, so anything it signed before a rotation cannot be verified from the site itself"
		r.na("identity.key_history", FamilyIdentity, reason)
		r.na("identity.key_history_head", FamilyIdentity, reason)
		r.na("identity.key_history_witness", FamilyIdentity, dsParityNotRun)
		return
	}

	// The host we fetched from IS the domain the transitions were signed over —
	// as long as it is derived the SAME WAY the rotation derived it.
	//
	// ⚠️ Both rotation paths sign polisurl.ExtractDomain(POLIS_BASE_URL), which
	// strips the port. A validator that kept the port would rebuild a different
	// canonical message and report a perfectly good chain as forged. Found by
	// running this against a real rotated site on localhost:8931, where the
	// signed domain was "localhost" and this said "localhost:8931".
	domain := polisurl.ExtractDomain(base)
	r.fromStatus("identity.key_history", FamilyIdentity, ChainValidity(block, domain))

	// did.json is optional; fetch it only to complete the head comparison, and
	// treat its absence as absence rather than as a problem.
	didDoc, ferr := rr.client.FetchContent(base + "/.well-known/did.json")
	var didBytes []byte
	if ferr == nil {
		didBytes = []byte(didDoc)
	}
	r.fromStatus("identity.key_history_head", FamilyIdentity, ChainHead(block, publicKey, didBytes))
	// SIGNET epic 32: the witnesses ride in the chain this site served.
	reportRotationWitnesses(r, block, domain, rr.DSKeys)
}

func (rr *Remote) checkRemoteDID(r *Report, base string, pubKey []byte) {
	body, err := rr.client.FetchContent(base + "/.well-known/did.json")
	if err != nil {
		r.na("identity.did_document", FamilyIdentity, "no .well-known/did.json served — most sites publish none, and absence is honest")
		return
	}
	if len(pubKey) == 0 {
		r.na("identity.did_document", FamilyIdentity, "no published key to compare the DID document against")
		return
	}
	var parsed struct {
		VerificationMethod []struct {
			PublicKeyJwk struct {
				X string `json:"x"`
			} `json:"publicKeyJwk"`
		} `json:"verificationMethod"`
	}
	if jerr := json.Unmarshal([]byte(body), &parsed); jerr != nil {
		r.fail("identity.did_document", FamilyIdentity, "did.json is served and does not parse: "+jerr.Error(), 1)
		return
	}
	wantX, err := jwkX(pubKey)
	if err != nil {
		r.na("identity.did_document", FamilyIdentity, "the published key could not be parsed, so the DID document cannot be compared: "+err.Error())
		return
	}
	for _, vm := range parsed.VerificationMethod {
		if vm.PublicKeyJwk.X == wantX {
			r.pass("identity.did_document", FamilyIdentity, "did.json publishes the same key as .well-known/polis", 1)
			return
		}
	}
	r.fail("identity.did_document", FamilyIdentity, "did.json publishes a DIFFERENT key than .well-known/polis — a stale DID document answers 200 with a retired key, which a resolver cannot tell from a good one", 1)
}

func (rr *Remote) checkRemoteBundles(r *Report, base string, bundles map[string]struct {
	Path string `json:"path"`
}) {
	if len(bundles) == 0 {
		r.na("bundle.declared", FamilyBundle, "the identity document declares no bundles, so there is nothing to look for")
		r.na("bundle.json", FamilyBundle, "the identity document declares no bundles, so there is no declaration to fetch")
		return
	}

	names := make([]string, 0, len(bundles))
	for name := range bundles {
		names = append(names, name)
	}
	sort.Strings(names)

	var missing []string
	var core []byte
	for _, name := range names {
		p := strings.TrimPrefix(bundles[name].Path, "/")
		body, err := rr.client.FetchContent(base + "/" + p)
		if err != nil {
			missing = append(missing, fmt.Sprintf("%s declares %s, which does not serve", name, bundles[name].Path))
			continue
		}
		if name == "pub.polis.core" {
			core = []byte(body)
		}
	}
	if len(missing) > 0 {
		r.fail("bundle.declared", FamilyBundle, "declared bundle file(s) do not serve", len(names), missing...)
	} else {
		r.pass("bundle.declared", FamilyBundle, fmt.Sprintf("%d declared bundle(s) serve at their declared paths", len(names)), len(names))
	}

	if core == nil {
		r.na("bundle.json", FamilyBundle, "the core bundle declaration could not be fetched, so its contents were not checked")
		return
	}
	r.fromStatus("bundle.json", FamilyBundle, BundleJSONBytes(core, nil))
}

// noPhantomsRemotely is what a remote index.consistency cannot do, said in every
// result it gives.
const noPhantomsRemotely = "phantom detection is not possible remotely (HTTP does not list directories)"

// checkRemoteIndex verifies that every indexed entry serves and matches its
// current_version, by the same per-type rule the local form uses, and returns
// the entries for the content pass.
//
// ⛔ SIGNET epic 44 F1. This skipped every entry that was not a post — the exact
// line epic 42 removed from the local form — so a site whose index listed only
// attestations got a green tick having fetched nothing. Entries of a type with no
// rule are now counted and named, as locally; nothing is skipped silently.
//
// ⚠️ Phantom detection — a file on disk that the index does not mention — is
// structurally impossible from outside: HTTP does not list directories. Saying
// so is the point; a remote run that silently dropped half of this check would
// let "index consistent" mean less than the local run's identical words.
func (rr *Remote) checkRemoteIndex(r *Report, base string, pubKey []byte) []remote.PublicIndexEntry {
	entries, skipped, err := rr.client.FetchPublicIndexReport(base)
	if err != nil {
		r.na("index.consistency", FamilyIndex, "no index.jsonl served — the site has published nothing, or does not serve its index")
		return nil
	}
	var issues []string
	if skipped != nil && skipped.Skipped > 0 {
		issues = append(issues, malformedLinesIssue(skipped.SkippedLines))
	}
	if len(entries) == 0 && len(issues) == 0 {
		r.pass("index.consistency", FamilyIndex, "empty index; "+noPhantomsRemotely, 0)
		return entries
	}

	checked, unchecked := map[string]int{}, map[string]int{}
	total := 0
	for _, e := range entries {
		rule, ok := indexEntryRules[e.Type]
		if !ok {
			unchecked[e.Type]++
			continue
		}
		checked[e.Type]++
		total++
		body, ferr := rr.fetch(contentURLFor(base, e))
		if ferr != nil {
			issues = append(issues, fmt.Sprintf("orphan: %s (indexed but does not serve)", e.GetPath()))
			continue
		}
		if e.Hash == "" {
			continue
		}
		match, verr := rule.VersionMatches([]byte(body), e.Hash)
		switch {
		case verr != nil:
			issues = append(issues, fmt.Sprintf("unreadable: %s is indexed as a %s and does not parse as one: %s", e.GetPath(), e.Type, verr.Error()))
		case !match:
			issues = append(issues, fmt.Sprintf("hash_mismatch: %s", e.GetPath()))
		}
	}

	if len(issues) > 0 {
		r.fail("index.consistency", FamilyIndex, fmt.Sprintf("%d issues; %s", len(issues), noPhantomsRemotely), total, issues...)
	} else {
		r.pass("index.consistency", FamilyIndex, indexCensus(checked, unchecked)+"; "+noPhantomsRemotely, total)
	}
	return entries
}

// fetch is the client's FetchContent, remembered for the length of this
// Remote's life, so the index pass and the content passes over one site fetch
// each artifact once.
func (rr *Remote) fetch(url string) (string, error) {
	if f, ok := rr.bodies[url]; ok {
		return f.body, f.err
	}
	body, err := rr.client.FetchContent(url)
	if rr.bodies == nil {
		rr.bodies = map[string]fetched{}
	}
	rr.bodies[url] = fetched{body, err}
	return body, err
}

type fetched struct {
	body string
	err  error
}

func (rr *Remote) checkRemotePolicies(r *Report, base string) {
	body, err := rr.client.FetchContent(base + "/policies/rules.jsonl")
	if err != nil {
		r.na("policy.syntax", FamilyPolicy, "no public policies/rules.jsonl served, and private policies are never served — the site's rules could not be read")
		return
	}
	warnings := ParsePolicyLines(body, "policies/rules.jsonl")
	if len(warnings) == 0 {
		r.pass("policy.syntax", FamilyPolicy, "every public rule parses (private rules are not served and were not checked)", 1)
		return
	}
	findings := make([]string, 0, len(warnings))
	for _, w := range warnings {
		findings = append(findings, fmt.Sprintf("%s:%d %q — %s", w.File, w.Line, w.Rule, w.Error))
	}
	r.fail("policy.syntax", FamilyPolicy, fmt.Sprintf("%d public policy rule problem(s)", len(warnings)), 1, findings...)
}

func (rr *Remote) checkRemoteContent(r *Report, base string, entries []remote.PublicIndexEntry, pubKey []byte, chain *site.KeyHistoryBlock, chainNote string, census *witnessCensus) {
	if len(pubKey) == 0 {
		for _, id := range []string{"content.posts", "content.comments"} {
			r.na(id, FamilyContent, "no key to verify against — the site publishes no public_key")
		}
		return
	}

	counts := map[string]*struct {
		examined, verified, retired, unsigned int
		findings, retiredNotes                []string
	}{
		"post":    {},
		"comment": {},
	}

	historyConsulted := chain != nil && len(chain.History) > 0
	for _, e := range entries {
		c, ok := counts[e.Type]
		if !ok {
			continue
		}
		c.examined++
		body, err := rr.fetch(contentURLFor(base, e))
		if err != nil {
			c.findings = append(c.findings, fmt.Sprintf("%s: does not serve", e.GetPath()))
			continue
		}
		res := VerifyContentWithHistory(body, pubKey, chain, signing.MarkdownObjectTypeFor(e.Type, false))
		if res.ParseError != nil {
			c.findings = append(c.findings, fmt.Sprintf("%s: %v", e.GetPath(), res.ParseError))
			continue
		}
		census.add(e.GetPath(), contentURLFor(base, e), res)
		switch res.Signature {
		case SigValid:
			c.verified++
			if res.Key.Source == KeyRetired {
				c.retired++
				c.retiredNotes = append(c.retiredNotes, e.GetPath()+": "+res.Key.Describe())
			}
		case SigUnsigned:
			c.unsigned++
		case SigInvalid:
			c.findings = append(c.findings, signatureFinding(e.GetPath(), res, historyConsulted, chainNote))
		}
		if res.Hash == HashMismatch {
			c.findings = append(c.findings, fmt.Sprintf("%s: body does not match its current-version hash", e.GetPath()))
		}
	}

	for _, spec := range []struct{ id, noun string }{{"content.posts", "post"}, {"content.comments", "comment"}} {
		c := counts[spec.noun]
		if c.examined == 0 {
			r.na(spec.id, FamilyContent, fmt.Sprintf("the index lists no %ss", spec.noun))
			continue
		}
		detail := verifiedDetail(c.examined, spec.noun, c.verified, c.retired, c.unsigned)
		if len(c.findings) > 0 {
			r.fail(spec.id, FamilyContent, detail, c.examined, append(c.findings, c.retiredNotes...)...)
		} else {
			r.passNoting(spec.id, FamilyContent, detail, c.examined, c.retiredNotes...)
		}
	}
}

// checkRemoteRecords verifies the signed JSON artifacts a site serves at known
// paths: the follow file, the blessing list and the licence.
//
// Each is parsed from the fetched bytes and handed to the SAME exported
// verifier the local form and the actors use — following.Verify,
// metadata.VerifyBlessed, license.Verify. Only the byte acquisition differs.
//
// ⭐ The licence goes one step further than the others: once verified, its terms
// are compared against the site's PUBLIC licence surfaces, which is the one
// question nothing else in the system can answer — see public_terms.go.
func (rr *Remote) checkRemoteRecords(r *Report, base, licensePointer string, pubKey []byte, chain *site.KeyHistoryBlock) {
	if len(pubKey) == 0 {
		for _, id := range []string{"content.following", "content.blessed", "content.license"} {
			r.na(id, FamilyContent, "no key to verify against — the site publishes no public_key")
		}
		rr.checkRemotePublicTerms(r, base, nil)
	} else {
		if body, err := rr.client.FetchContent(base + "/content/pub.polis.core/follow/following.json"); err != nil {
			r.na("content.following", FamilyContent, "no following.json served — the site follows nobody, or does not serve its follow file")
		} else {
			f, jerr := following.Parse([]byte(body))
			if jerr != nil {
				r.fail("content.following", FamilyContent, "following.json is served and does not parse: "+jerr.Error(), 1)
			} else {
				st, verr := following.Verify(f, pubKey)
				r.signatureOutcome("content.following", FamilyContent, "following.json", string(st), errText(verr), f.UnrecognisedFields())
			}
		}

		if body, err := rr.client.FetchContent(base + "/content/pub.polis.core/comment/blessed.json"); err != nil {
			r.na("content.blessed", FamilyContent, "no blessed.json served — the site has blessed nothing, or does not serve its blessing list")
		} else {
			bc, jerr := metadata.ParseBlessed([]byte(body))
			if jerr != nil {
				r.fail("content.blessed", FamilyContent, "blessed.json is served and does not parse: "+jerr.Error(), 1)
			} else {
				st, verr := metadata.VerifyBlessed(bc, pubKey)
				r.signatureOutcome("content.blessed", FamilyContent, "blessed.json", string(st), errText(verr), bc.UnrecognisedFields())
			}
		}

		// ⭐ The licence is verified FIRST and its terms handed on, because the
		// public-surface checks are only meaningful against a signed original.
		rr.checkRemotePublicTerms(r, base, rr.checkRemoteLicense(r, base, licensePointer, pubKey, chain))
	}
}

// checkRemoteLicense verifies the site's signed terms and RETURNS them, so the
// public-surface checks can compare the wire against what the author actually
// signed rather than against the wire's own text. Nil means there is nothing
// trustworthy to compare with.
func (rr *Remote) checkRemoteLicense(r *Report, base, pointer string, pubKey []byte, chain *site.KeyHistoryBlock) *license.Terms {
	if pointer == "" {
		r.na("content.license", FamilyContent, "the site states no terms — absent means unstated, not missing")
		return nil
	}
	body, err := rr.client.FetchContent(base + "/" + strings.TrimPrefix(pointer, "/"))
	if err != nil {
		// A dangling pointer is NOT benign: the site advertises terms and
		// serves nothing, so a consumer sees "unstated" for an author who
		// stated something. That is a lost claim of intent.
		r.fail("content.license", FamilyContent, fmt.Sprintf("licence pointer %s resolves to nothing", pointer), 1)
		return nil
	}
	f, jerr := license.Parse([]byte(body))
	if jerr != nil {
		r.fail("content.license", FamilyContent, fmt.Sprintf("licence at %s is unreadable: %v", pointer, jerr), 1)
		return nil
	}
	if f.Terms == nil {
		r.fail("content.license", FamilyContent, fmt.Sprintf("licence at %s carries no terms", pointer), 1)
		return nil
	}
	if verr := f.Terms.Validate(); verr != nil {
		r.fail("content.license", FamilyContent, fmt.Sprintf("licence at %s is invalid: %v", pointer, verr), 1)
		return nil
	}
	ok, retired, verr := site.VerifyWithHistory(func(key []byte) (bool, error) {
		return license.Verify(f, key)
	}, pubKey, chain, f.Updated, "updated")
	historyConsulted := chain != nil && len(chain.History) > 0
	// SIGNET epic 47 — the tolerance rule, through license.Status.
	status, why := license.Status(f, ok)
	switch {
	case status == license.StatusUnknown:
		// ⚠️ The terms are still RETURNED as nil: unknown is not valid, and the
		// public-surface comparison downstream is only meaningful against terms
		// that verified.
		r.na("content.license", FamilyContent, "licence at "+pointer+" could not be checked — "+why)
		return nil
	case !ok && historyConsulted:
		r.fail("content.license", FamilyContent, "licence signature does not verify — terms may have been altered", 1, errText(verr))
		return nil
	case !ok && verr != nil:
		r.fail("content.license", FamilyContent, fmt.Sprintf("licence signature check failed: %v", verr), 1)
		return nil
	case !ok:
		r.fail("content.license", FamilyContent, "licence signature does not verify — terms may have been altered", 1)
		return nil
	case retired != nil:
		r.pass("content.license", FamilyContent, "the site's stated terms are intact; licence "+KeyUsedFor(chain, retired, f.Updated).Describe(), 1)
		return f.Terms
	}
	if note := signing.UncoveredNote(f.UnrecognisedFields()); note != "" {
		r.pass("content.license", FamilyContent, "the site's stated terms are intact and signed by its own key — "+note, 1)
		return f.Terms
	}
	r.pass("content.license", FamilyContent, "the site's stated terms are intact and signed by its own key", 1)
	return f.Terms
}

// contentURLFor resolves an index entry to its canonical fetch URL, supporting
// both modern path-based entries and legacy URL-based ones.
func contentURLFor(base string, e remote.PublicIndexEntry) string {
	if e.URL != "" {
		return e.URL
	}
	return base + "/" + strings.TrimPrefix(e.Path, "/")
}

func normalizeBase(u string) string {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "https://" + u
	}
	return u
}
