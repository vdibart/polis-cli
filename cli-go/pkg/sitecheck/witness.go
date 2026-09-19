package sitecheck

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// The witness axis — SIGNET epic 32, D8.
//
// A discovery service countersigns what it saw registered, and the site
// publishes those countersignatures. This file answers, for one artifact or one
// rotation: did a witness see these exact bytes, and when?
//
// ⛔ FOUR VALUES, AND EXACTLY ONE OF THEM IS A FAILURE (epic 32 E9).
//
//   - witnessed    — a witness names these bytes and its signature verifies
//     against the key its DS publishes under the key id it names.
//   - unwitnessed  — no witness names these bytes. The artifact still verifies
//     on its own signature; its date is only its own claim. NEVER a failure (D1).
//   - unverifiable — a witness names these bytes but could not be checked (the
//     DS key could not be fetched). A bonus claim went unexamined; nothing is
//     wrong with the artifact. NEVER a failure (D8).
//   - invalid      — a witness names these bytes and its signature does NOT
//     verify against the DS's key. ⛔ THIS FAILS. A published witness is the
//     site ASSERTING that a third party testified; one that does not verify is a
//     false claim about someone else. D1 says nothing may REQUIRE a witness — it
//     does not make a fraudulent one harmless. Same rule as a badly signed
//     following.json (epic 02): the test is whether something was ASSERTED.
//
// ⚠️ A witness of OTHER bytes (a superseded version) is not invalid and never
// fails: true testimony about a different version is normal in a witness set.
//
// ⛔ D1: verifying the ARTIFACT never touches a discovery service. Verifying a
// WITNESS may fetch the DS's key from the domain the record itself names — the
// same trust root as a site's own .well-known/polis — and when that fetch fails
// the answer is "unverifiable", never red. A caller that turns an unreachable DS
// into OutcomeFailed has violated D1.
//
// ⛔ The DS key is never taken from the record. A key sitting inside a record
// anyone could have written is not evidence that it is the DS's key.

// WitnessState is the witness axis for one artifact.
type WitnessState string

const (
	WitnessWitnessed    WitnessState = "witnessed"
	WitnessUnwitnessed  WitnessState = "unwitnessed"
	WitnessUnverifiable WitnessState = "unverifiable"
	// WitnessStateInvalid — a published witness for these bytes does not verify.
	// It takes precedence over witnessed: a valid witness does not launder a
	// forged one published beside it.
	WitnessStateInvalid WitnessState = "invalid"
)

// WitnessCheckStatus is what happened to one witness record.
type WitnessCheckStatus string

const (
	// WitnessVerified — names these bytes, and its signature verifies.
	WitnessVerified WitnessCheckStatus = "verified"
	// WitnessInvalid — names these bytes, and its signature does NOT verify
	// against the DS's key. The site claims testimony it does not have: a FAILURE.
	WitnessInvalid WitnessCheckStatus = "invalid"
	// WitnessNotChecked — names these bytes; the DS key could not be obtained.
	WitnessNotChecked WitnessCheckStatus = "not_checked"
	// WitnessOtherBytes — witnesses a DIFFERENT version of this artifact. True
	// testimony about bytes that are no longer these; superseded, not wrong.
	WitnessOtherBytes WitnessCheckStatus = "other_bytes"
)

// WitnessCheck is one witness record's outcome.
type WitnessCheck struct {
	DS          string             `json:"ds"`
	DSKeyID     string             `json:"ds_key_id"`
	WitnessedAt string             `json:"witnessed_at"`
	Status      WitnessCheckStatus `json:"status"`
	// Binds says what the witness covers: "artifact" (the whole signed artifact,
	// via artifact_hash) or "version" (whatever `version` hashes — for posts and
	// comments, the body only).
	Binds  string `json:"binds,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// WitnessResult is the witness axis for one artifact.
type WitnessResult struct {
	State WitnessState `json:"state"`
	// EarliestWitnessedAt is the earliest verified witness — the independent
	// date by which these bytes existed. Set only when State is witnessed.
	EarliestWitnessedAt string `json:"earliest_witnessed_at,omitempty"`
	// EarliestBinds is what that earliest witness covers.
	EarliestBinds string         `json:"earliest_binds,omitempty"`
	Checks        []WitnessCheck `json:"checks,omitempty"`
}

// Describe is the sentence a person reads.
func (w *WitnessResult) Describe() string {
	if w == nil {
		return "no witness information"
	}
	switch w.State {
	case WitnessStateInvalid:
		reason := ""
		for _, c := range w.Checks {
			if c.Status == WitnessInvalid {
				reason = " (" + c.Reason + ")"
				break
			}
		}
		return "INVALID WITNESS: the site publishes a discovery-service countersignature for these bytes that does not verify against that discovery service's key" + reason + " — it claims third-party testimony it does not have. The artifact's own signature is judged separately."
	case WitnessWitnessed:
		scope := "the whole signed artifact"
		if w.EarliestBinds == "version" {
			scope = "its `version` only — for a post or comment that is the body, not the frontmatter"
		}
		return fmt.Sprintf("witnessed: a discovery service countersigned these bytes at %s, so they existed by then (the witness covers %s). The artifact's own claimed dates are still its own.", w.EarliestWitnessedAt, scope)
	case WitnessUnverifiable:
		reason := ""
		for _, c := range w.Checks {
			if c.Status == WitnessNotChecked {
				reason = c.Reason
				break
			}
		}
		return "a witness is published for these bytes but could not be checked — " + reason + ". This is not a failure: the artifact verifies on its own signature."
	default:
		return "unwitnessed: no discovery service countersignature covers these bytes. The artifact still verifies on its own signature; its date is only its own claim."
	}
}

// DSKeyLookup returns the public key a discovery service publishes under a key
// id. It is the witness's equivalent of fetching a site's .well-known/polis.
type DSKeyLookup func(ds, keyID string) (string, error)

// NewDSKeyLookup fetches DS keys over HTTPS, once per (DS, key id) per lookup.
// A plain-http DS is refused: the key's only evidence is the TLS connection to
// the domain the record names. requestID, when set, is sent as X-Request-Id so
// the fetch correlates with the run that made it.
func NewDSKeyLookup(hc *http.Client, requestID string) DSKeyLookup {
	if hc == nil {
		// MaybeWrapTransport is a no-op unless POLIS_ORIGIN_OVERRIDES is set, which
		// is test infrastructure only. It lets the recipe book's harness (Signet
		// epic 33) serve a discovery service's key from a fixture, exactly as it
		// already serves every site the CLI fetches.
		hc = &http.Client{Timeout: 10 * time.Second, Transport: remote.MaybeWrapTransport(http.DefaultTransport)}
	}
	type entry struct {
		key string
		err error
	}
	var mu sync.Mutex
	cache := map[string]entry{}
	return func(ds, keyID string) (string, error) {
		u, err := url.Parse(ds)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "", fmt.Errorf("the witness names %q, which is not an https discovery service, so its key cannot be fetched with any assurance", ds)
		}
		cacheKey := ds + "\x00" + keyID
		mu.Lock()
		if e, ok := cache[cacheKey]; ok {
			mu.Unlock()
			return e.key, e.err
		}
		mu.Unlock()
		key, ferr := discovery.FetchDSPublicKey(hc, ds, keyID, requestID)
		if ferr != nil {
			ferr = fmt.Errorf("could not fetch %s's key %q: %v", ds, keyID, ferr)
		}
		mu.Lock()
		cache[cacheKey] = entry{key, ferr}
		mu.Unlock()
		return key, ferr
	}
}

// StaticDSKeys is a lookup over keys the caller already holds — a pinned DS key
// for an offline check, or a test.
func StaticDSKeys(keys map[string]string) DSKeyLookup {
	return func(ds, keyID string) (string, error) {
		if k, ok := keys[ds+"|"+keyID]; ok {
			return k, nil
		}
		return "", fmt.Errorf("no key held for %s key %q", ds, keyID)
	}
}

// checkOne verifies one applicable witness record.
func checkOne(w discovery.Witness, binds string, lookup DSKeyLookup) WitnessCheck {
	c := WitnessCheck{DS: w.DS, DSKeyID: w.DSKeyID, WitnessedAt: w.WitnessedAt, Binds: binds}
	if lookup == nil {
		c.Status, c.Reason = WitnessNotChecked, "no discovery-service key source was available to this check"
		return c
	}
	key, err := lookup(w.DS, w.DSKeyID)
	if err != nil {
		c.Status, c.Reason = WitnessNotChecked, err.Error()
		return c
	}
	if err := w.VerifySignature(key); err != nil {
		c.Status, c.Reason = WitnessInvalid, err.Error()
		return c
	}
	c.Status = WitnessVerified
	return c
}

// CheckContentWitnesses evaluates an artifact's published witness set against
// the artifact's own bytes.
//
// artifactHash is discovery.ArtifactHash over the artifact's signing base ("" if
// the caller cannot compute one); version is the artifact's `version` /
// current-version. A witness carrying artifact_hash applies only when it equals
// artifactHash — it binds the whole signed artifact. One without applies when
// its version equals version, and binds only what `version` hashes.
func CheckContentWitnesses(set []discovery.Witness, artifactHash, version string, lookup DSKeyLookup) *WitnessResult {
	res := &WitnessResult{State: WitnessUnwitnessed}
	var earliest *discovery.Witness
	notChecked, invalid := false, false
	for _, w := range set {
		if w.Action != discovery.WitnessActionContent {
			continue
		}
		var binds string
		switch {
		case w.ArtifactHash != "":
			if artifactHash == "" || w.ArtifactHash != artifactHash {
				res.Checks = append(res.Checks, WitnessCheck{DS: w.DS, DSKeyID: w.DSKeyID, WitnessedAt: w.WitnessedAt, Status: WitnessOtherBytes, Binds: "artifact",
					Reason: "witnesses a different signed version of this artifact"})
				continue
			}
			binds = "artifact"
		default:
			if version == "" || w.Version != version {
				res.Checks = append(res.Checks, WitnessCheck{DS: w.DS, DSKeyID: w.DSKeyID, WitnessedAt: w.WitnessedAt, Status: WitnessOtherBytes, Binds: "version",
					Reason: "witnesses a different version of this artifact"})
				continue
			}
			binds = "version"
		}
		c := checkOne(w, binds, lookup)
		res.Checks = append(res.Checks, c)
		switch c.Status {
		case WitnessVerified:
			wc := w
			if earliest == nil || witnessedBefore(wc, *earliest) {
				earliest = &wc
				res.EarliestBinds = binds
			}
		case WitnessNotChecked:
			notChecked = true
		case WitnessInvalid:
			invalid = true
		}
	}
	if earliest != nil {
		res.EarliestWitnessedAt = earliest.WitnessedAt
	}
	switch {
	case invalid:
		// Precedence: a verified witness beside a forged one does not make the
		// published set honest.
		res.State = WitnessStateInvalid
	case earliest != nil:
		res.State = WitnessWitnessed
	case notChecked:
		res.State = WitnessUnverifiable
	}
	return res
}

func witnessedBefore(a, b discovery.Witness) bool {
	ta, ea := a.WitnessedTime()
	tb, eb := b.WitnessedTime()
	if ea == nil && eb == nil {
		return ta.Before(tb)
	}
	return a.WitnessedAt < b.WitnessedAt
}

// ContradictsClaimedTime reports, for a retired-key pass (epic 31), whether the
// earliest witness post-dates the retired key's window — evidence that the
// artifact's claimed signing time is not when it was registered. A FACT for the
// consumer, never a gate (D1, Law 2). "" when there is nothing to say.
func (w *WitnessResult) ContradictsClaimedTime(key *KeyUsed) string {
	if w == nil || w.State != WitnessWitnessed || key == nil || key.Source != KeyRetired || key.ValidUntil == "" {
		return ""
	}
	seen, err := time.Parse(time.RFC3339Nano, w.EarliestWitnessedAt)
	if err != nil {
		return ""
	}
	// Parsed for comparison only; the chain string is never re-rendered.
	until, err := time.Parse(time.RFC3339, key.ValidUntil)
	if err != nil {
		return ""
	}
	if seen.Before(until) {
		return ""
	}
	return fmt.Sprintf("its earliest witness is %s — AFTER the retired key that signed it stopped being current at %s. The artifact claims to have been signed at %s; the only independent date says it was first registered later. That is what a backdated signature by a retired key looks like, and it is reported, not judged.",
		w.EarliestWitnessedAt, key.ValidUntil, key.ClaimedSigningTime)
}

// rotationWitnessSkew is how far a witness's own clock may sit from a rotation's
// claimed valid_from before it is worth saying so. The DS refuses a rotation
// stamped more than five minutes from its clock, so a larger gap in a VERIFIED
// witness cannot come from the DS and means the two do not describe the same
// moment.
const rotationWitnessSkew = 5 * time.Minute

// reportRotationWitnesses fills identity.key_history_witness from the witnesses
// a published chain carries — the check that, before epic 32, could only ever
// be not-applicable.
//
// ⚠️ What it proves and what it does not: a verified witness dates a rotation
// the chain RECORDS, from outside the site. It cannot show a rotation the chain
// OMITS — the site simply leaves that witness out too. That comparison still
// needs the DS's own record (the hosted Clerk sweep), and the report says so.
func reportRotationWitnesses(r *Report, block *site.KeyHistoryBlock, domain string, lookup DSKeyLookup) {
	const id = "identity.key_history_witness"
	const omission = "A witness dates a rotation the chain records; it cannot reveal one the chain omits — that still needs the discovery service's own record, which runs in the hosted Clerk sweep."
	if block == nil {
		r.na(id, FamilyIdentity, dsParityNotRun)
		return
	}
	entries := block.Entries()
	if len(entries) < 2 {
		// Genesis only: no recorded rotation for a witness to date, and the
		// omission/backdating gap is exactly as it was.
		r.na(id, FamilyIdentity, dsParityNotRun)
		return
	}

	rotations := len(entries) - 1
	var witnessed, unchecked, invalid int
	var notes, invalidNotes []string
	for i := 1; i < len(entries); i++ {
		prev, e := entries[i-1], entries[i]
		sig := ""
		if e.TransitionSig != nil {
			sig = *e.TransitionSig
		}
		var verified *discovery.Witness
		var reasons []string
		for _, w := range e.Witnesses {
			if w.Action != discovery.WitnessActionKeyRotation || w.OldKey != prev.Key || w.NewKey != e.Key ||
				w.Timestamp != e.ValidFrom || w.TransitionSig != sig || (domain != "" && w.Domain != domain) {
				reasons = append(reasons, fmt.Sprintf("a witness by %s does not describe this rotation", w.DS))
				continue
			}
			c := checkOne(w, "", lookup)
			switch c.Status {
			case WitnessVerified:
				wc := w
				if verified == nil || witnessedBefore(wc, *verified) {
					verified = &wc
				}
			case WitnessInvalid:
				// ⛔ E9: the chain carries testimony for this rotation that does not
				// verify against the DS's key. A false claim — a failure, whatever
				// else the entry carries.
				invalid++
				invalidNotes = append(invalidNotes, fmt.Sprintf("epoch %d: a witness by %s (key %q) does NOT verify — the chain claims testimony it does not have: %s", e.Epoch, w.DS, w.DSKeyID, c.Reason))
			default:
				reasons = append(reasons, fmt.Sprintf("a witness by %s %s: %s", w.DS, c.Status, c.Reason))
			}
		}
		switch {
		case verified != nil:
			witnessed++
			note := fmt.Sprintf("epoch %d: witnessed by %s at %s", e.Epoch, verified.DS, verified.WitnessedAt)
			if seen, err1 := verified.WitnessedTime(); err1 == nil {
				if claimed, err2 := time.Parse(time.RFC3339, e.ValidFrom); err2 == nil {
					if d := seen.Sub(claimed); d > rotationWitnessSkew || d < -rotationWitnessSkew {
						note += fmt.Sprintf(" — %s from the chain's valid_from %s, which the discovery service's own freshness rule does not allow for one observation", d.Round(time.Second), e.ValidFrom)
					}
				}
			}
			notes = append(notes, note)
		case len(e.Witnesses) == 0:
			notes = append(notes, fmt.Sprintf("epoch %d: no witness — this rotation's date is only the chain's own claim", e.Epoch))
		default:
			unchecked++
			notes = append(notes, fmt.Sprintf("epoch %d: %s", e.Epoch, strings.Join(reasons, "; ")))
		}
	}

	switch {
	case invalid > 0:
		r.fail(id, FamilyIdentity,
			fmt.Sprintf("%d rotation witness(es) in the chain do not verify against their discovery service's key. %s", invalid, omission),
			rotations, append(invalidNotes, notes...)...)
	case witnessed > 0:
		r.passNoting(id, FamilyIdentity,
			fmt.Sprintf("%d of %d rotation(s) carry a verified discovery-service witness. %s", witnessed, rotations, omission),
			rotations, notes...)
	case unchecked > 0:
		r.na(id, FamilyIdentity, "the chain carries rotation witnesses, but none could be verified ("+strings.Join(notes, "; ")+"). This is not a failure. Without a verified witness, "+dsParityNotRun)
	default:
		r.na(id, FamilyIdentity, "no rotation in the chain carries a witness, so each rotation's date is only the chain's own claim. "+dsParityNotRun)
	}
}

// ---------- content census ----------

// noWitnessesPublished is the reason when a site publishes no witness set.
const noWitnessesPublished = "this site publishes no witnesses (no `witnesses` pointer in .well-known/polis), so none of its content carries an independent date. That is a weaker claim, not a defect: every artifact still verifies, or not, on its own signature."

// localWitnesses reads a directory's published witness set through its pointer.
func localWitnesses(siteDir string) (*site.WitnessFile, string) {
	pointer := site.WitnessesPointer(siteDir)
	if pointer == "" {
		return nil, noWitnessesPublished
	}
	f, err := site.LoadWitnesses(siteDir)
	if err != nil {
		return nil, fmt.Sprintf("the site's `witnesses` pointer names %s, which could not be read (%v). Nothing is failed for it — no witness could be read, so none can be checked — but the owner may want to know the pointer leads nowhere.", pointer, err)
	}
	return f, ""
}

// witnessIndex maps an artifact's URL PATH to its witnesses. Keyed by path, not
// full URL, so a relocated site or a local copy still finds its witnesses.
func witnessIndex(f *site.WitnessFile) map[string][]discovery.Witness {
	if f == nil {
		return nil
	}
	idx := make(map[string][]discovery.Witness, len(f.Witnesses))
	for key, set := range f.Witnesses {
		path := key
		if u, err := url.Parse(key); err == nil && u.Path != "" {
			path = u.Path
		}
		idx[path] = append(idx[path], set...)
	}
	return idx
}

// urlPath returns the path of an absolute or site-relative artifact address.
func urlPath(s string) string {
	p := s
	if u, err := url.Parse(s); err == nil && u.Path != "" {
		p = u.Path
	}
	// A site-relative path from a directory walk parses with no leading slash;
	// a witness key's path always has one. They must compare equal.
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// witnessCensus tallies the witness axis across a site's content and reports it
// as content.witnesses. ⛔ It produces OutcomeFailed for ONE case only: a
// published witness for an artifact's current bytes that does not verify (E9).
// No witness (D1), an uncheckable one (D8) and one for other bytes never fail.
type witnessCensus struct {
	index       map[string][]discovery.Witness // nil when no set could be read
	unavailable string                         // why index is nil
	lookup      DSKeyLookup

	examined, witnessed, unwitnessed, unverifiable, invalid int
	notes, invalidNotes                                     []string
	seenNotes                                               map[string]bool
}

// newWitnessCensus takes the site's witness set and, when there is none, why.
// The caller sets lookup.
func newWitnessCensus(f *site.WitnessFile, unavailable string) *witnessCensus {
	return &witnessCensus{index: witnessIndex(f), unavailable: unavailable, seenNotes: map[string]bool{}}
}

func (c *witnessCensus) note(s string) {
	if c.seenNotes[s] || len(c.notes) >= 50 {
		return
	}
	c.seenNotes[s] = true
	c.notes = append(c.notes, s)
}

// addVersioned records one artifact whose witness binds `version` alone (a
// JSON-family record, where version already hashes the whole signed record).
func (c *witnessCensus) addVersioned(label, path, version string) {
	if c == nil || c.index == nil {
		return
	}
	c.tally(label, CheckContentWitnesses(c.index[urlPath(path)], "", version, c.lookup), nil)
}

// add records one markdown artifact.
func (c *witnessCensus) add(label, path string, res ContentResult) {
	if c == nil || c.index == nil {
		return
	}
	c.tally(label, CheckContentWitnesses(c.index[urlPath(path)], res.ArtifactHash, res.CurrentVersion, c.lookup), res.Key)
}

func (c *witnessCensus) tally(label string, w *WitnessResult, key *KeyUsed) {
	c.examined++
	switch w.State {
	case WitnessStateInvalid:
		c.invalid++
	case WitnessWitnessed:
		c.witnessed++
	case WitnessUnverifiable:
		c.unverifiable++
		for _, ch := range w.Checks {
			if ch.Status == WitnessNotChecked {
				c.note("a witness could not be checked: " + ch.Reason)
			}
		}
	default:
		c.unwitnessed++
	}
	for _, ch := range w.Checks {
		if ch.Status == WitnessInvalid && len(c.invalidNotes) < 50 {
			c.invalidNotes = append(c.invalidNotes, fmt.Sprintf("%s: a published witness by %s (key %q) does NOT verify — the site claims testimony it does not have: %s", label, ch.DS, ch.DSKeyID, ch.Reason))
		}
	}
	if msg := w.ContradictsClaimedTime(key); msg != "" {
		c.note(label + ": " + msg)
	}
}

// emit reports content.witnesses. scope says what was walked.
func (c *witnessCensus) emit(r *Report, scope string) {
	const id = "content.witnesses"
	switch {
	case c.index == nil:
		r.na(id, FamilyContent, c.unavailable)
	case c.examined == 0:
		r.na(id, FamilyContent, "no content was examined, so there was nothing to look for witnesses of")
	default:
		detail := fmt.Sprintf("%s examined (%s): %d witnessed, %d unwitnessed, %d with a witness that could not be checked, %d with a witness that does not verify. Unwitnessed is a weaker claim, never a failure; a witness that does not verify is a false claim.",
			plural(c.examined, "artifact"), scope, c.witnessed, c.unwitnessed, c.unverifiable, c.invalid)
		if c.invalid > 0 {
			r.fail(id, FamilyContent, detail, c.examined, append(c.invalidNotes, c.notes...)...)
			return
		}
		r.passNoting(id, FamilyContent, detail, c.examined, c.notes...)
	}
}

// reportArtifactWitness answers the witness axis for ONE artifact as
// content.witness. Fails only when a published witness for these bytes does not
// verify (E9).
func reportArtifactWitness(r *Report, f *site.WitnessFile, unavailable, artifactURL string, res ContentResult, lookup DSKeyLookup) {
	const id = "content.witness"
	if f == nil {
		r.na(id, FamilyContent, unavailable)
		return
	}
	w := CheckContentWitnesses(witnessIndex(f)[urlPath(artifactURL)], res.ArtifactHash, res.CurrentVersion, lookup)
	detail := w.Describe()
	if msg := w.ContradictsClaimedTime(res.Key); msg != "" {
		detail += " " + msg
	}
	switch w.State {
	case WitnessStateInvalid:
		r.fail(id, FamilyContent, detail, 1)
	case WitnessUnverifiable:
		r.na(id, FamilyContent, detail)
	default:
		r.pass(id, FamilyContent, detail, 1)
	}
}
