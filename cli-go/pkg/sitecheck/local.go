package sitecheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// noPrivateState is the reason every owner-only check gives when there is no
// .polis/ directory.
//
// ⭐ This is the whole of the clone handling. A clone is a real directory that
// is genuinely missing things, so the validator does not need to know it IS a
// clone — only to report what was not there to check. The same sentence covers
// a published copy, a backup of public files, and anything else without the
// private tree.
const noPrivateState = "no .polis/ directory — this is a published copy rather than a working site, so owner-only state (keys, permissions, private policies, bundle registry) is not present to check"

// onlyOnTheWire is why a directory cannot check the public licence surfaces
// (SIGNET epic 44 D6): an intermediary can add to what the edge serves, and a
// directory never contains it.
const onlyOnTheWire = "what the edge actually serves — the public robots.txt and rsl.xml, compared with the signed terms — is only visible over the wire; run `polis validate <url>`"

// RunLocal validates a site directory: your own site, or a clone, or any tree
// laid out like one.
//
// Visibility follows WHAT IS THERE, never a mode flag. Everything absent is
// reported as not-applicable with a reason, so validating a clone can never
// look identical to validating your own site.
func RunLocal(siteDir string) *Report {
	return RunLocalWith(siteDir, NewDSKeyLookup(nil, ""))
}

// RunLocalWith is RunLocal with the source of discovery-service keys that
// witness signatures are checked against (SIGNET epic 32 D8). RunLocal fetches
// them over HTTPS from the DS each witness names; a caller holding pinned keys,
// or a test, passes StaticDSKeys. A nil lookup leaves every witness unchecked —
// reported as such, never as a failure.
func RunLocalWith(siteDir string, dsKeys DSKeyLookup) *Report {
	r := &Report{Target: siteDir, Form: FormLocal, Scope: ScopeSite, dsKeys: dsKeys}

	info, err := os.Stat(siteDir)
	if err != nil {
		r.FatalError = fmt.Sprintf("cannot read %s: %v", siteDir, err)
		return r
	}
	if !info.IsDir() {
		r.FatalError = fmt.Sprintf("%s is not a directory", siteDir)
		return r
	}

	hasPrivate := isDir(filepath.Join(siteDir, ".polis"))

	wk, wkErr := site.LoadWellKnown(siteDir)
	pubKey, keyErr := PublishedKey(siteDir)
	chain, chainNote := localResolvingChain(siteDir, wk)

	checkIdentityLocal(r, siteDir, wk, wkErr, hasPrivate)
	checkBundlesLocal(r, siteDir, wk, hasPrivate)
	checkIndexLocal(r, siteDir)
	checkPolicyLocal(r, siteDir, hasPrivate)
	checkContentLocal(r, siteDir, pubKey, keyErr, chain, chainNote)

	return r
}

// ---------- key/handle alignment ----------

func checkIdentityLocal(r *Report, siteDir string, wk *site.WellKnown, wkErr error, hasPrivate bool) {
	switch {
	case wkErr != nil && os.IsNotExist(wkErr):
		r.fail("identity.well_known", FamilyIdentity, ".well-known/polis not found — the site has no identity document", 0)
	case wkErr != nil:
		r.fail("identity.well_known", FamilyIdentity, "cannot read .well-known/polis: "+wkErr.Error(), 0)
	case wk.PublicKey == "":
		r.fail("identity.well_known", FamilyIdentity, ".well-known/polis is missing public_key", 1)
	default:
		r.pass("identity.well_known", FamilyIdentity, "identity document present, parseable, and publishes a key", 1)
	}

	keysDir := filepath.Join(siteDir, ".polis", "keys")
	privPath := filepath.Join(keysDir, "id_ed25519")
	pubPath := filepath.Join(keysDir, "id_ed25519.pub")

	if !hasPrivate {
		r.na("identity.key_files", FamilyIdentity, noPrivateState)
		r.na("identity.key_perms", FamilyIdentity, noPrivateState)
		r.na("identity.key_match", FamilyIdentity, noPrivateState+" — the published key cannot be compared with a key file that is not here")
	} else {
		var missing []string
		if !exists(privPath) {
			missing = append(missing, ".polis/keys/id_ed25519")
		}
		if !exists(pubPath) {
			missing = append(missing, ".polis/keys/id_ed25519.pub")
		}
		if len(missing) > 0 {
			r.fail("identity.key_files", FamilyIdentity, "key file(s) missing from a site that has a .polis/ directory", 2, missing...)
		} else {
			r.pass("identity.key_files", FamilyIdentity, "both key files present", 2)
		}

		if exists(privPath) {
			r.fromStatus("identity.key_perms", FamilyIdentity, KeyPerms(siteDir))
		} else {
			r.na("identity.key_perms", FamilyIdentity, "no private key file to check permissions on")
		}

		keyFile, readErr := os.ReadFile(pubPath)
		switch {
		case readErr != nil:
			r.na("identity.key_match", FamilyIdentity, "no readable public key file to compare against .well-known/polis")
		case wk == nil || wk.PublicKey == "":
			r.na("identity.key_match", FamilyIdentity, "no published key in .well-known/polis to compare the key file against")
		case strings.TrimSpace(string(keyFile)) != strings.TrimSpace(wk.PublicKey):
			r.fail("identity.key_match", FamilyIdentity, "the key in .well-known/polis does not match .polis/keys/id_ed25519.pub — the network is being told a different key than this site signs with", 1)
		default:
			r.pass("identity.key_match", FamilyIdentity, "published key matches the key file", 1)
		}
	}

	checkDIDDocument(r, siteDir, wk)
	checkKeyHistoryLocal(r, siteDir, wk)
	if wkErr == nil {
		if body, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis")); err == nil {
			reportMessagesKey(r, body)
		}
	} else {
		r.na("identity.messages_key", FamilyIdentity, "the identity document could not be read, so its messages key could not be checked")
	}
}

// checkKeyHistoryLocal answers the two questions a site can answer about its own
// key history without asking anyone: does the chain prove itself, and does its
// head agree with everywhere else the site states its current key.
//
// ⛔ It also says, out loud, what it did NOT check. See dsParityNotRun.
func checkKeyHistoryLocal(r *Report, siteDir string, wk *site.WellKnown) {
	block, err := site.LoadKeyHistory(siteDir)
	if err != nil {
		r.fail("identity.key_history", FamilyIdentity, "public_key_history is present and does not parse: "+err.Error(), 1)
		r.na("identity.key_history_head", FamilyIdentity, "the key history could not be read, so its head could not be compared with anything")
		r.na("identity.key_history_witness", FamilyIdentity, dsParityNotRun)
		return
	}
	if block == nil {
		didDoc, _ := os.ReadFile(site.DIDDocumentPath(siteDir))
		if erased := ChainErasure(nil, didDoc); !erased.OK {
			r.fail("identity.key_history", FamilyIdentity, erased.Message, 1)
			r.na("identity.key_history_head", FamilyIdentity, "the key history was erased, so there is no head to compare")
			r.na("identity.key_history_witness", FamilyIdentity, dsParityNotRun)
			return
		}
		reason := "no public_key_history in .well-known/polis — the site publishes only its current key, so a rotation would leave everything signed under the old one unverifiable from here"
		r.na("identity.key_history", FamilyIdentity, reason)
		r.na("identity.key_history_head", FamilyIdentity, reason)
		r.na("identity.key_history_witness", FamilyIdentity, dsParityNotRun)
		return
	}

	// The domain is inside every transition signature and .well-known/polis
	// does not record it, so it comes from the site's own DID document.
	didDoc, _ := os.ReadFile(site.DIDDocumentPath(siteDir))
	domain := DomainFromDIDDocument(didDoc)
	if domain == "" && len(block.History) > 0 {
		r.na("identity.key_history", FamilyIdentity,
			"the chain records a rotation, and the transition signature is made over this site's domain — which nothing here states. .well-known/polis has no host field, and there is no .well-known/did.json to read the site's own `id` from, so the handover could not be rebuilt and checked.")
	} else {
		r.fromStatus("identity.key_history", FamilyIdentity, ChainValidity(block, domain))
	}

	pubKey := ""
	if wk != nil {
		pubKey = wk.PublicKey
	}
	r.fromStatus("identity.key_history_head", FamilyIdentity, ChainHead(block, pubKey, didDoc))
	// SIGNET epic 32: rotation witnesses ride inside the chain, so this check is
	// real for the first time — offline, given the DS's key.
	reportRotationWitnesses(r, block, domain, r.dsKeys)
}

// localResolvingChain is the key history a local run may resolve retired keys
// from (SIGNET epic 31).
//
// The domain comes from the site's own did.json, exactly as
// checkKeyHistoryLocal gets it. ⚠️ So a site that has rotated and publishes no
// did.json cannot have its handovers checked from a directory, its history
// resolves nothing, and every pre-rotation failure says why — rather than
// reading as tampering. `polis validate <url>` has no such gap: it knows the
// host it fetched from.
func localResolvingChain(siteDir string, wk *site.WellKnown) (*site.KeyHistoryBlock, string) {
	if wk == nil {
		return nil, ""
	}
	return SiteResolvingChain(siteDir, "", wk.PublicKey)
}

// SiteResolvingChain is the key history a verifier holding a site's DIRECTORY
// may resolve retired keys from — the one loader for `polis validate <dir>`,
// Judge and Patrol (SIGNET epic 42 V4, reusing epic 31's seam).
//
// domain is the host the site's transition signatures were made over. An actor
// knows it the way nothing on disk does — handle + base domain — and passes it;
// "" falls back to the site's own did.json, as `polis validate <dir>` must.
// publicKey is the key the site publishes; ResolvingChain refuses a chain whose
// head is anything else.
func SiteResolvingChain(siteDir, domain, publicKey string) (*site.KeyHistoryBlock, string) {
	block, err := site.LoadKeyHistory(siteDir)
	if err != nil || block == nil {
		return nil, ""
	}
	if domain == "" {
		didDoc, _ := os.ReadFile(site.DIDDocumentPath(siteDir))
		domain = DomainFromDIDDocument(didDoc)
	}
	return ResolvingChain(block, domain, publicKey)
}

// checkDIDDocument asks whether a published DID document still says what the
// site's identity document says.
//
// A did.json is DERIVED — it repeats .well-known/polis's key in the encoding a
// DID resolver reads — so the failure that matters is a STALE one: a document
// answering 200 with a retired key, which a resolver cannot distinguish from a
// good one. An absent document fails honestly and is not a finding.
//
// ⭐ The staleness test is site.DIDDocumentNeedsWrite — the same predicate
// Medic heals from, byte comparison against a deterministically rebuilt
// document. The host comes from the document's OWN `id`, so this works on a
// clone of someone else's site, where our own POLIS_BASE_URL would be wrong.
func checkDIDDocument(r *Report, siteDir string, wk *site.WellKnown) {
	doc, err := os.ReadFile(site.DIDDocumentPath(siteDir))
	if err != nil {
		r.na("identity.did_document", FamilyIdentity, "no .well-known/did.json published — most sites publish none, and absence is honest")
		return
	}
	if wk == nil || wk.PublicKey == "" {
		r.na("identity.did_document", FamilyIdentity, "no published key in .well-known/polis to compare the DID document against")
		return
	}

	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		r.fail("identity.did_document", FamilyIdentity, "did.json is present and does not parse: "+err.Error(), 1)
		return
	}
	host := strings.TrimPrefix(parsed.ID, "did:web:")
	if host == "" || host == parsed.ID {
		r.fail("identity.did_document", FamilyIdentity, fmt.Sprintf("did.json declares id %q, which is not a did:web identifier", parsed.ID), 1)
		return
	}

	stale, _, err := site.DIDDocumentNeedsWrite(siteDir, host)
	if err != nil {
		r.na("identity.did_document", FamilyIdentity, "the DID document could not be rebuilt for comparison: "+err.Error())
		return
	}
	if stale {
		r.fail("identity.did_document", FamilyIdentity, "did.json does not match the key and host this site publishes — a stale DID document answers 200 with a retired key, which a resolver cannot tell from a good one", 1)
		return
	}
	r.pass("identity.did_document", FamilyIdentity, "did.json matches the key published in .well-known/polis", 1)
}

// ---------- bundle registry health ----------

func checkBundlesLocal(r *Report, siteDir string, wk *site.WellKnown, hasPrivate bool) {
	if wk == nil || len(wk.Bundles) == 0 {
		r.na("bundle.declared", FamilyBundle, "the identity document declares no bundles, so there is nothing to look for")
	} else {
		var missing []string
		names := make([]string, 0, len(wk.Bundles))
		for name := range wk.Bundles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			// Listing-is-activation: every bundle present in bundles[] is
			// active and must have a bundle.json where it says it does.
			if !exists(filepath.Join(siteDir, wk.Bundles[name].Path)) {
				missing = append(missing, fmt.Sprintf("%s declares %s, which is not there", name, wk.Bundles[name].Path))
			}
		}
		if len(missing) > 0 {
			r.fail("bundle.declared", FamilyBundle, "declared bundle file(s) missing", len(names), missing...)
		} else {
			r.pass("bundle.declared", FamilyBundle, fmt.Sprintf("%d declared bundle(s) present at their declared paths", len(names)), len(names))
		}
	}

	bundlePath := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	if !exists(bundlePath) && (wk == nil || len(wk.Bundles) == 0) {
		r.na("bundle.json", FamilyBundle, "no core bundle.json and none declared — a site from before the bundle refactor")
	} else {
		r.fromStatus("bundle.json", FamilyBundle, BundleJSON(siteDir))
	}

	if !hasPrivate {
		r.na("bundle.registry", FamilyBundle, noPrivateState)
		return
	}
	regPath := filepath.Join(siteDir, ".polis", "bundles", "registry.json")
	data, err := os.ReadFile(regPath)
	if os.IsNotExist(err) {
		r.na("bundle.registry", FamilyBundle, "no .polis/bundles/registry.json — the site has selected no theme or shape yet")
		return
	}
	if err != nil {
		r.fail("bundle.registry", FamilyBundle, "cannot read the bundle registry: "+err.Error(), 1)
		return
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		r.fail("bundle.registry", FamilyBundle, "the bundle registry is present and does not parse: "+err.Error(), 1)
		return
	}
	r.pass("bundle.registry", FamilyBundle, "bundle registry present and parseable", 1)
}

// ---------- index consistency ----------

func checkIndexLocal(r *Report, siteDir string) {
	indexPath := filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl")
	if !exists(indexPath) {
		r.na("index.consistency", FamilyIndex, "no index.jsonl — the site has published nothing")
		return
	}
	r.fromStatus("index.consistency", FamilyIndex, IndexConsistency(siteDir))
}

// ---------- policy parseability ----------

func checkPolicyLocal(r *Report, siteDir string, hasPrivate bool) {
	publicPolicies := exists(filepath.Join(siteDir, "policies", "rules.jsonl"))
	privatePolicies := exists(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"))

	if !publicPolicies && !privatePolicies {
		reason := "no policy files — the site has declared no rules"
		if !hasPrivate {
			reason = "no public policy file, and " + noPrivateState
		}
		r.na("policy.syntax", FamilyPolicy, reason)
		return
	}

	warnings := PolicySyntax(siteDir)
	examined := 0
	if publicPolicies {
		examined++
	}
	if privatePolicies {
		examined++
	}
	if len(warnings) == 0 {
		detail := "every rule parses"
		if !hasPrivate {
			detail += " (public rules only; " + noPrivateState + ")"
		}
		r.pass("policy.syntax", FamilyPolicy, detail, examined)
		return
	}
	findings := make([]string, 0, len(warnings))
	for _, w := range warnings {
		findings = append(findings, fmt.Sprintf("%s:%d %q — %s", w.File, w.Line, w.Rule, w.Error))
	}
	r.fail("policy.syntax", FamilyPolicy, fmt.Sprintf("%d policy rule problem(s)", len(warnings)), examined, findings...)
}

// ---------- signed content integrity ----------

func checkContentLocal(r *Report, siteDir string, pubKey []byte, keyErr error, chain *site.KeyHistoryBlock, chainNote string) {
	if keyErr != nil {
		reason := "no key to verify against — " + keyErr.Error()
		for _, id := range []string{"content.posts", "content.comments", "content.following", "content.blessed", "content.license", "content.attestations", "content.tags", "content.witnesses"} {
			r.na(id, FamilyContent, reason)
		}
		r.na("content.license_robots", FamilyContent, onlyOnTheWire)
		r.na("content.license_rsl", FamilyContent, onlyOnTheWire)
		return
	}

	census := newWitnessCensus(localWitnesses(siteDir))
	census.lookup = r.dsKeys
	checkContentTree(r, "content.posts", siteDir, filepath.Join(siteDir, "content", "pub.polis.core", "post"), pubKey, chain, chainNote, signing.TypePost, "post", census)
	checkContentTree(r, "content.comments", siteDir, filepath.Join(siteDir, "content", "pub.polis.core", "comment"), pubKey, chain, chainNote, signing.TypeComment, "comment", census)
	defer census.emit(r, "posts, comments, attestations and tags on disk")

	// ⛔ SIGNET epic 44: a file that is not there has not been verified. Both
	// predicates report an absent file as UNSIGNED — correct for them, since a
	// site that follows nobody is not broken — and this used to turn that into a
	// PASS reading "unsigned — nothing to verify" about bytes it never read. The
	// remote form already said NOT CHECKED; the parity guard found the pair
	// disagreeing.
	if !exists(following.DefaultPath(siteDir)) {
		r.na("content.following", FamilyContent, "no following.json — the site follows nobody, or has never written its follow file")
	} else {
		followStatus, followErr := following.VerifySite(siteDir)
		// Re-read for the unrecognised-member list: VerifySite reports a status
		// and nothing else, and the `valid` row owes the reader the fields the
		// signature does NOT cover (SIGNET epic 47).
		var followExtra []string
		if f, lerr := following.Load(following.DefaultPath(siteDir)); lerr == nil {
			followExtra = f.UnrecognisedFields()
		}
		r.signatureOutcome("content.following", FamilyContent, "following.json", string(followStatus), errText(followErr), followExtra)
	}

	if !exists(metadata.BlessedPath(siteDir)) {
		r.na("content.blessed", FamilyContent, "no blessed.json — the site has blessed nothing")
	} else {
		blessedStatus, blessedErr := metadata.VerifyBlessedSite(siteDir)
		var blessedExtra []string
		if bc, lerr := metadata.LoadBlessedComments(siteDir); lerr == nil {
			blessedExtra = bc.UnrecognisedFields()
		}
		r.signatureOutcome("content.blessed", FamilyContent, "blessed.json", string(blessedStatus), errText(blessedErr), blessedExtra)
	}

	licenseStatus := LicenseIntegrityWithHistory(siteDir, pubKey, chain)
	switch {
	case licenseStatus.OK && licenseStatus.Message == "no terms stated":
		r.na("content.license", FamilyContent, "the site states no terms — absent means unstated, not missing")
	case licenseStatus.OK && strings.HasPrefix(licenseStatus.Message, LicenseUncheckable):
		// ⛔ NOT-APPLICABLE, never a pass. "Could not check" is exactly the
		// thing that must never look like "checked and fine" — and the remote
		// form reports it this way, so anything else makes the two disagree
		// about one site (SIGNET epic 47).
		r.na("content.license", FamilyContent, licenseStatus.Message)
	default:
		r.fromStatus("content.license", FamilyContent, licenseStatus)
	}
	r.na("content.license_robots", FamilyContent, onlyOnTheWire)
	r.na("content.license_rsl", FamilyContent, onlyOnTheWire)

	// SIGNET epic 44 E5: the records resolve retired keys through the same chain
	// the markdown does, by each type's claimed signing time. Witness census for
	// the JSON family (epic 32) rides the same read — a record's `version`
	// already hashes the whole signed record, so a witness binds all of it.
	attestations := recordTally{statuses: map[string]string{}}
	records, badRecords, attErr := attestation.Scan(siteDir)
	attestations.err = attErr
	for _, m := range badRecords {
		attestations.malformed = append(attestations.malformed, m.Name+": does not parse ("+m.Error+")")
	}
	for _, rec := range records {
		s, retired, _ := attestation.VerifyWithHistory(rec, pubKey, chain)
		attestations.add(attestation.ID(rec), string(s), KeyUsedFor(chain, retired, rec.Asserted))
		censusAttestation(census, rec)
	}
	checkRecordSet(r, "content.attestations", "attestation", "the site has issued no attestations", attestations)

	tags := recordTally{statuses: map[string]string{}}
	tagFiles, badTags, tagErr := tag.ScanTags(siteDir)
	tags.err = tagErr
	for _, m := range badTags {
		tags.malformed = append(tags.malformed, m.Name+": does not parse ("+m.Error+")")
	}
	for i := range tagFiles {
		tf := &tagFiles[i]
		s, retired, _ := tag.VerifyWithHistory(tf, pubKey, chain)
		tags.add(tf.Tag, string(s), KeyUsedFor(chain, retired, tf.Updated))
		censusTag(census, tf)
	}
	checkRecordSet(r, "content.tags", "tag file", "the site has issued no tag files", tags)
}

// censusAttestation adds one attestation to the witness census, keyed the way
// its witness is published — by the record's URL path.
func censusAttestation(c *witnessCensus, rec *attestation.Record) {
	id := attestation.ID(rec)
	c.addVersioned("attestation "+id, attestation.RecordURL("", id), rec.Version)
}

// censusTag adds one row per tag target, the unit a tag's witness is published for.
func censusTag(c *witnessCensus, tf *tag.TagFile) {
	for _, target := range tf.Targets {
		rowURL := tag.TagTargetURL("", tf.Tag, polisurl.NormalizeToMD(target.URI))
		c.addVersioned("tag "+tf.Tag+" → "+target.URI, rowURL, tf.Version)
	}
}

// checkContentTree verifies every markdown file under dir. Reports the census
// rather than a verdict: "12 verified, 3 unsigned" and "15 verified" are
// different facts and must not print the same.
//
// A signature the current key rejects is tried against the key the site's
// history resolves for its claimed signing time (epic 31); a pass that needed
// that is counted and named, never folded into the plain count.
func checkContentTree(r *Report, id, siteDir, dir string, pubKey []byte, chain *site.KeyHistoryBlock, chainNote string, typ signing.ObjectType, noun string, census *witnessCensus) {
	if !isDir(dir) {
		r.na(id, FamilyContent, fmt.Sprintf("no %s directory — the site has published no %ss", noun, noun))
		return
	}

	var verified, retired, unsigned, examined int
	var findings, retiredNotes []string
	historyConsulted := chain != nil && len(chain.History) > 0

	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".versions" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(siteDir, path)
		examined++

		data, err := os.ReadFile(path)
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: unreadable — %v", rel, err))
			return nil
		}
		res := VerifyContentWithHistory(string(data), pubKey, chain, typ)
		if res.ParseError != nil {
			findings = append(findings, fmt.Sprintf("%s: %v", rel, res.ParseError))
			return nil
		}
		census.add(rel, filepath.ToSlash(rel), res)
		switch res.Signature {
		case SigValid:
			verified++
			if res.Key.Source == KeyRetired {
				retired++
				retiredNotes = append(retiredNotes, rel+": "+res.Key.Describe())
			}
		case SigUnsigned:
			unsigned++
		case SigInvalid:
			findings = append(findings, signatureFinding(rel, res, historyConsulted, chainNote))
		}
		if res.Hash == HashMismatch {
			findings = append(findings, fmt.Sprintf("%s: body does not match its current-version hash", rel))
		}
		return nil
	})

	if examined == 0 {
		r.na(id, FamilyContent, fmt.Sprintf("the %s directory holds no files — the site has published no %ss", noun, noun))
		return
	}
	detail := verifiedDetail(examined, noun, verified, retired, unsigned)
	if len(findings) > 0 {
		r.fail(id, FamilyContent, detail, examined, append(findings, retiredNotes...)...)
		return
	}
	r.passNoting(id, FamilyContent, detail, examined, retiredNotes...)
}

// recordTally is what one pass over a set of signed JSON records found — the
// same shape whether the records were read off disk or fetched over HTTP, so
// both forms report them in the same words (SIGNET epic 44).
type recordTally struct {
	// err is set when the set could not be read at all.
	err error
	// statuses maps each EXAMINED record to valid | unsigned | invalid | unknown.
	statuses map[string]string
	// retired names each record that verified only against a retired key.
	retired []string
	// notChecked names each record that was listed and not examined, and why.
	// ⛔ Never a failure and never folded into the count: it is said.
	notChecked []string
	// malformed names each file in the set's directory that does not parse.
	// ⛔ A FINDING, and never silently skipped (Signet epic 44 C2): the file
	// is published, and no reader can use it.
	malformed []string
}

func (t *recordTally) add(name, status string, used *KeyUsed) {
	t.statuses[name] = status
	if status == "valid" && used != nil && used.Source == KeyRetired {
		t.retired = append(t.retired, name+": "+used.Describe())
	}
}

// checkRecordSet reports a set of signed JSON records. none is the reason when
// the set is empty.
func checkRecordSet(r *Report, id, noun, none string, t recordTally) {
	if t.err != nil {
		r.na(id, FamilyContent, fmt.Sprintf("could not read this site's %ss — %v", noun, t.err))
		return
	}
	if len(t.statuses) == 0 && len(t.malformed) == 0 {
		r.na(id, FamilyContent, none)
		return
	}

	var verified, unsigned int
	var findings []string
	names := make([]string, 0, len(t.statuses))
	for name := range t.statuses {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		switch t.statuses[name] {
		case "valid":
			verified++
		case "unsigned":
			unsigned++
		case "invalid":
			findings = append(findings, fmt.Sprintf("%s: signature does not verify", name))
		default:
			findings = append(findings, fmt.Sprintf("%s: could not be checked", name))
		}
	}
	findings = append(findings, t.malformed...)
	examined := len(t.statuses) + len(t.malformed)
	detail := verifiedDetail(examined, noun, verified, len(t.retired), unsigned)
	if len(t.malformed) > 0 {
		detail += fmt.Sprintf("; %d do not parse", len(t.malformed))
	}
	var notes []string
	if len(t.notChecked) > 0 {
		detail += fmt.Sprintf("; %d more listed and NOT checked", len(t.notChecked))
		notes = append(notes, t.notChecked...)
	}
	notes = append(notes, t.retired...)
	if len(findings) > 0 {
		r.fail(id, FamilyContent, detail, examined, append(findings, notes...)...)
		return
	}
	r.passNoting(id, FamilyContent, detail, examined, notes...)
}

// plural renders a count with its noun. Small, but the report is read by people
// and "1 posts examined" makes a careful tool look careless.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
