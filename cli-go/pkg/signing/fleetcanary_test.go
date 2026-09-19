package signing_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/clone"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// The fleet canary — Signet epic 08.
//
// ⭐ IT VERIFIES REAL PRODUCTION ARTIFACTS, not fixtures. Epic 08 is the
// highest-risk epic in Signet: Judge, Patrol, Medic and `polis validate` all
// verify signatures, so a defect in the signing base produces FLEET-WIDE FALSE
// ALARMS rather than a quiet failure. Green unit tests on synthetic content are
// not evidence that a thousand live signatures still verify, because the
// synthetic content was written by the same person as the change.
//
// It does two things at once, over every artifact it can reach:
//
//   - VERIFIES each one against the key its site publishes, under the NEW base.
//   - Runs the LEGACY rule beside the new one on every markdown artifact and
//     reports any disagreement. A disagreement is not automatically a failure —
//     the whole point of the change is that the old rule was wrong for a body
//     line beginning `signature:` — but it must be explainable, and on today's
//     corpus there should be none at all.
//
// ⛔ SKIPPED BY DEFAULT. It walks the public internet, so it is not a CI test:
//
//	POLIS_FLEET_CANARY=1 go test ./pkg/signing/ -run TestFleetCanary -v -timeout 30m
//
// Point it somewhere else with POLIS_FLEET_SITES (comma-separated domains) or
// POLIS_FLEET_DS (a discovery service base URL).
//
// ⭐ SIGNET EPIC 31 ADDED A SECOND COMPARISON. Every markdown artifact is also
// verified the way `polis validate` and `polis preview` now do — current key
// first, then the key the site's published history resolves for the artifact's
// claimed signing time — and the verdict is compared with the current-key-only
// one. Epic 31's D2 predicts ZERO movement on today's fleet: nothing verified
// by the current key may change, and an artifact that passes ONLY through the
// history exists only on a site that has rotated. Every movement is listed.

const defaultFleetDS = "https://ds.polis.pub"

// legacyMarkdownSigningBase is the pre-epic-08 rule, duplicated here rather
// than shared with base_test.go's copy: that one lives in the internal test
// package and this file must be external to import the artifact packages at
// all. A canary comparing against a frozen constant is allowed to carry it
// twice; the alternative is exporting dead code from the package.
func legacyMarkdownSigningBase(content string, isComment bool) string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "signature:") {
			continue
		}
		if isComment && strings.HasPrefix(line, "author:") {
			continue
		}
		out = append(out, line)
	}
	return signing.CanonicalizeContent(strings.Join(out, "\n"))
}

type fleetTally struct {
	mu sync.Mutex

	sites, sitesUnreachable, sitesNoKey int
	byType                              map[string]*typeTally
	disagreements                       []string
	failures                            []string
	preExisting                         []string
	notes                               []string

	// Epic 31: sites publishing a history with at least one rotation, and
	// artifacts whose verdict the history changed.
	sitesRotated    []string
	historyResolved []string

	// Epic 42: live records whose `polis validate <url>` verdict matched the
	// package predicate's.
	recordURLAgreed int

	// Epic 44: sites whose clone and original were validated both ways, and the
	// disagreements on checks epic 44 did not change (reported, not failed).
	paritySites     int
	parityOther     []string
	parityExplained []string // disagreements on a declared clone gap (cloneGaps)
}

// typeTally counts one artifact type across the whole fleet.
//
// ⚠️ `absent` means the site does not serve that artifact OR the fetch failed,
// and the two are not distinguished on purpose: from outside they look the
// same, and pretending otherwise is how a report comes to claim more than it
// checked. Most of the count is honest absence — 32 of 33 sites state no
// terms (epic 29: nothing states terms on a user's behalf) and most follow
// files and blessing lists are unsigned because nothing backfills a signature.
type typeTally struct{ verified, unsigned, invalid, absent int }

func (ft *fleetTally) count(typ string, f func(*typeTally)) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t, ok := ft.byType[typ]
	if !ok {
		t = &typeTally{}
		ft.byType[typ] = t
	}
	f(t)
}

func (ft *fleetTally) note(format string, a ...any) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.notes = append(ft.notes, fmt.Sprintf(format, a...))
}

func (ft *fleetTally) fail(format string, a ...any) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.failures = append(ft.failures, fmt.Sprintf(format, a...))
}

// preExists records an artifact that fails verification under the NEW base and
// failed under the OLD one too. The distinction is the whole reason to run this
// before cutover: an invalid signature the legacy rule also rejected is a fact
// about the fleet, and one only the new rule rejects is a regression this epic
// caused.
func (ft *fleetTally) preExists(format string, a ...any) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.preExisting = append(ft.preExisting, fmt.Sprintf(format, a...))
}

func (ft *fleetTally) disagree(format string, a ...any) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.disagreements = append(ft.disagreements, fmt.Sprintf(format, a...))
}

func TestFleetCanary(t *testing.T) {
	if os.Getenv("POLIS_FLEET_CANARY") == "" {
		t.Skip("set POLIS_FLEET_CANARY=1 to verify live production artifacts over the public internet")
	}

	domains := fleetDomains(t)
	t.Logf("fleet: %d site(s)", len(domains))

	ft := &fleetTally{byType: map[string]*typeTally{}}
	client := remote.NewClient()

	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for _, d := range domains {
		wg.Add(1)
		go func(domain string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			checkFleetSite(ft, client, "https://"+domain)
		}(d)
	}
	wg.Wait()

	reportFleet(t, ft)
}

func fleetDomains(t *testing.T) []string {
	t.Helper()
	if s := os.Getenv("POLIS_FLEET_SITES"); s != "" {
		return strings.Split(s, ",")
	}
	ds := os.Getenv("POLIS_FLEET_DS")
	if ds == "" {
		ds = defaultFleetDS
	}

	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Get(ds + "/v1/sites/list?limit=500")
	if err != nil {
		t.Fatalf("list fleet sites: %v", err)
	}
	defer resp.Body.Close()

	var payload struct {
		Rows []struct {
			Domain string `json:"domain"`
		} `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode fleet site list: %v", err)
	}
	out := make([]string, 0, len(payload.Rows))
	for _, r := range payload.Rows {
		if r.Domain != "" {
			out = append(out, r.Domain)
		}
	}
	sort.Strings(out)
	return out
}

func checkFleetSite(ft *fleetTally, client *remote.Client, base string) {
	ft.mu.Lock()
	ft.sites++
	ft.mu.Unlock()

	// The identity document is read twice: once through the typed client for
	// the key, once as raw JSON for the LICENCE POINTER. remote.WellKnown has
	// no `license` field, and discovery of the licence is by pointer and never
	// by convention — `dir` and `mount` are user-configurable, so a hardcoded
	// path would be answering a different question.
	wk, err := client.FetchWellKnown(base)
	if err != nil {
		ft.mu.Lock()
		ft.sitesUnreachable++
		ft.mu.Unlock()
		ft.note("%s: no .well-known/polis (%v)", base, err)
		return
	}
	var wkRaw struct {
		License string `json:"license"`
	}
	var chain *site.KeyHistoryBlock
	if raw, rerr := client.FetchContent(strings.TrimSuffix(base, "/") + "/.well-known/polis"); rerr == nil {
		_ = json.Unmarshal([]byte(raw), &wkRaw)
		if block, herr := site.KeyHistoryFromWellKnown([]byte(raw)); herr == nil && block != nil {
			var why string
			chain, why = sitecheck.ResolvingChain(block, polisurl.ExtractDomain(base), wk.PublicKey)
			if len(block.History) > 0 {
				ft.mu.Lock()
				ft.sitesRotated = append(ft.sitesRotated, fmt.Sprintf("%s (%d rotation(s), usable=%v %s)", base, len(block.History), chain != nil, why))
				ft.mu.Unlock()
			}
		}
	}
	if wk.PublicKey == "" {
		ft.mu.Lock()
		ft.sitesNoKey++
		ft.mu.Unlock()
		ft.note("%s: publishes no public_key", base)
		return
	}
	pubKey := []byte(wk.PublicKey)

	checkFleetJSON(ft, client, base, pubKey, wkRaw.License)
	checkFleetRegistration(ft, base)

	entries, err := client.FetchPublicIndex(base)
	if err != nil {
		ft.note("%s: no index.jsonl (%v)", base, err)
		ft.note("%s: parity NOT COMPARED — no index to clone from", base)
		return
	}
	rr := sitecheck.NewRemote() // one per site: its caches are not shared across goroutines
	for _, e := range entries {
		switch e.Type {
		case "post", "comment":
			checkFleetMarkdown(ft, client, base, pubKey, chain, e)
		case "tag":
			checkFleetTag(ft, client, rr, base, pubKey, e)
		case "attestation":
			checkFleetAttestation(ft, client, rr, base, pubKey, e)
		}
	}

	// Epic 44 D6 parity runs LAST and only on sites small enough to clone within
	// the fleet's rate limit. Run first on discover.polis.pub (2,339 posts) it
	// drew HTTP 429 and starved the verification pass: every post went uncounted
	// and the remote run never started. A comparison against a rate-limited site
	// would report orphans that are not there, so it is skipped and SAID.
	if max := parityMax(); len(entries) > max {
		ft.note("%s: parity NOT COMPARED — %d index entries exceeds POLIS_FLEET_PARITY_MAX=%d (cloning it trips the site's rate limit)", base, len(entries), max)
		return
	}
	checkFleetParity(ft, base)
}

func parityMax() int {
	if v := os.Getenv("POLIS_FLEET_PARITY_MAX"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return 500
}

func fleetURL(base string, e remote.PublicIndexEntry) string {
	if e.URL != "" {
		return e.URL
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(e.GetPath(), "/")
}

// checkFleetMarkdown is the load-bearing half: every live post and comment,
// verified under the new base AND compared against the legacy one.
func checkFleetMarkdown(ft *fleetTally, client *remote.Client, base string, pubKey []byte, chain *site.KeyHistoryBlock, e remote.PublicIndexEntry) {
	url := fleetURL(base, e)
	body, err := client.FetchContent(url)
	if err != nil {
		ft.count(e.Type, func(t *typeTally) { t.absent++ })
		return
	}

	isComment := e.Type == "comment"
	typ := signing.MarkdownObjectTypeFor(e.Type, false)

	// The canary, at two levels. Byte equality is the strong statement — the
	// reconstruction did not move — and outcome equality is the one a reader
	// cares about, because a false "this content was tampered with" across the
	// fleet is what a defect here actually looks like.
	oldBase := legacyMarkdownSigningBase(body, isComment)
	newBase := signing.MarkdownSigningBase(body, typ)
	if oldBase != newBase {
		ft.disagree("%s: legacy and typed bases differ in BYTES (len %d vs %d)", url, len(oldBase), len(newBase))
	}
	legacyValid := false
	fm, _, perr := sitecheck.ParseFrontmatter(body)
	if perr == nil && fm.Signature != "" {
		sshSig := sitecheck.ReconstructSSHSignature(fm.Signature)
		oldOK, _ := signing.VerifySignature([]byte(oldBase), pubKey, sshSig)
		newOK, _ := signing.VerifySignature([]byte(newBase), pubKey, sshSig)
		legacyValid = oldOK
		if oldOK != newOK {
			ft.disagree("%s: legacy and typed bases differ in VERDICT (old=%v new=%v)", url, oldOK, newOK)
		}
	}

	res := sitecheck.VerifyContent(body, pubKey, typ)

	// Epic 31: the history-aware verdict beside the current-key one.
	withHistory := sitecheck.VerifyContentWithHistory(body, pubKey, chain, typ)
	switch {
	case withHistory.Signature == res.Signature:
	case res.Signature == sitecheck.SigInvalid && withHistory.Signature == sitecheck.SigValid:
		ft.mu.Lock()
		ft.historyResolved = append(ft.historyResolved, fmt.Sprintf("%s: %s", url, withHistory.Key.Describe()))
		ft.mu.Unlock()
	default:
		ft.fail("REGRESSION %s: the history-aware verifier said %s where the current key alone said %s", url, withHistory.Signature, res.Signature)
	}

	switch {
	case res.ParseError != nil:
		ft.count(e.Type, func(t *typeTally) { t.absent++ })
		ft.note("%s: %v", url, res.ParseError)
	case res.Signature == sitecheck.SigValid:
		ft.count(e.Type, func(t *typeTally) { t.verified++ })
	case res.Signature == sitecheck.SigUnsigned:
		ft.count(e.Type, func(t *typeTally) { t.unsigned++ })
	default:
		ft.count(e.Type, func(t *typeTally) { t.invalid++ })
		if legacyValid {
			ft.fail("REGRESSION %s: verified under the legacy base and does NOT verify under the typed one (%v)", url, res.SigError)
		} else {
			ft.preExists("%s: does not verify — and did not under the legacy base either", url)
		}
	}
}

func checkFleetJSON(ft *fleetTally, client *remote.Client, base string, pubKey []byte, licensePtr string) {
	// following.json — the roster the site authored.
	if body, err := client.FetchContent(strings.TrimSuffix(base, "/") + "/content/pub.polis.core/follow/following.json"); err == nil {
		var f following.FollowingFile
		if json.Unmarshal([]byte(body), &f) != nil {
			ft.count("following", func(t *typeTally) { t.absent++ })
		} else {
			status, verr := following.Verify(&f, pubKey)
			recordJSONStatus(ft, "following", base+"/content/pub.polis.core/follow/following.json", string(status), verr)
		}
	} else {
		ft.count("following", func(t *typeTally) { t.absent++ })
	}

	// blessed.json — the blessing list.
	if body, err := client.FetchContent(strings.TrimSuffix(base, "/") + "/content/pub.polis.core/comment/blessed.json"); err == nil {
		var bc metadata.BlessedComments
		if json.Unmarshal([]byte(body), &bc) != nil {
			ft.count("blessed", func(t *typeTally) { t.absent++ })
		} else {
			status, verr := metadata.VerifyBlessed(&bc, pubKey)
			recordJSONStatus(ft, "blessed", base+"/content/pub.polis.core/comment/blessed.json", string(status), verr)
		}
	} else {
		ft.count("blessed", func(t *typeTally) { t.absent++ })
	}

	// license.json — found BY POINTER, never by convention. `dir` and `mount`
	// are user-configurable, so a hardcoded path would be the wrong question.
	if licensePtr == "" {
		ft.count("license", func(t *typeTally) { t.absent++ })
		return
	}
	url := licensePtr
	if !strings.HasPrefix(url, "http") {
		url = strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(url, "/")
	}
	body, err := client.FetchContent(url)
	if err != nil {
		ft.count("license", func(t *typeTally) { t.absent++ })
		return
	}
	var lf license.File
	if json.Unmarshal([]byte(body), &lf) != nil {
		ft.count("license", func(t *typeTally) { t.absent++ })
		return
	}
	if lf.Signature == "" {
		ft.count("license", func(t *typeTally) { t.unsigned++ })
		return
	}
	ok, verr := license.Verify(&lf, pubKey)
	if ok {
		ft.count("license", func(t *typeTally) { t.verified++ })
	} else {
		ft.count("license", func(t *typeTally) { t.invalid++ })
		ft.fail("%s: licence signature does NOT verify (%v)", url, verr)
	}
}

func checkFleetTag(ft *fleetTally, client *remote.Client, rr *sitecheck.Remote, base string, pubKey []byte, e remote.PublicIndexEntry) {
	url := fleetURL(base, e)
	body, err := client.FetchContent(url)
	if err != nil {
		ft.count("tag", func(t *typeTally) { t.absent++ })
		return
	}
	var tf tag.TagFile
	if json.Unmarshal([]byte(body), &tf) != nil {
		ft.count("tag", func(t *typeTally) { t.absent++ })
		return
	}
	status, verr := tag.Verify(&tf, pubKey)
	recordJSONStatus(ft, "tag", url, string(status), verr)
	compareRecordURL(ft, rr, "tag", url, string(status))
}

func checkFleetAttestation(ft *fleetTally, client *remote.Client, rr *sitecheck.Remote, base string, pubKey []byte, e remote.PublicIndexEntry) {
	url := fleetURL(base, e)
	body, err := client.FetchContent(url)
	if err != nil {
		ft.count("attestation", func(t *typeTally) { t.absent++ })
		return
	}
	var r attestation.Record
	if json.Unmarshal([]byte(body), &r) != nil {
		ft.count("attestation", func(t *typeTally) { t.absent++ })
		return
	}
	status, verr := attestation.Verify(&r, pubKey)
	recordJSONStatus(ft, "attestation", url, string(status), verr)
	compareRecordURL(ft, rr, "attestation", url, string(status))
}

// compareRecordURL — SIGNET epic 42 V2. Every indexed live record is also run
// through `polis validate <record-url>`'s own path, and the command must reach
// the package predicate's verdict. Before epic 42 it failed every one of them
// with "no frontmatter found".
func compareRecordURL(ft *fleetTally, rr *sitecheck.Remote, typ, url, status string) {
	r := rr.RunArtifact(url)
	var detail string
	for _, c := range r.Checks {
		if c.Outcome == sitecheck.OutcomeFailed {
			detail += c.ID + ": " + c.Detail + "; "
		}
	}
	good := status == "valid" || status == "unsigned"
	if (good && r.OK()) || (status == "invalid" && !r.OK()) {
		ft.mu.Lock()
		ft.recordURLAgreed++
		ft.mu.Unlock()
		return
	}
	ft.fail("REGRESSION %s: the %s predicate said %s and `polis validate <url>` said ok=%v (%s%s)", url, typ, status, r.OK(), detail, r.FatalError)
}

// checkFleetRegistration — SIGNET epic 42 V3. Every fleet site's registration
// service attestation, verified the way Judge now verifies it: rebuild the
// register canonical, fetch the DS key by attestation_key_id, verify. Before
// epic 42 Judge checked only that the string was non-empty — so this is the
// verdict Judge will start reporting on the next sweep, seen before it ships.
func checkFleetRegistration(ft *fleetTally, base string) {
	ds := os.Getenv("POLIS_FLEET_DS")
	if ds == "" {
		ds = defaultFleetDS
	}
	domain := polisurl.ExtractDomain(base)
	hc := &http.Client{Timeout: 30 * time.Second}
	res, err := (&discovery.Client{BaseURL: ds, HTTPClient: hc}).CheckSiteRegistration(domain)
	if err != nil || !res.IsRegistered || res.ServiceAttestation == "" {
		ft.count("registration", func(t *typeTally) { t.absent++ })
		return
	}
	verr := discovery.VerifyRegistrationAttestation(hc, ds, domain, res.RegistrationVersion, res.AttestationKeyID, res.ServiceAttestation, "")
	switch {
	case verr == nil:
		ft.count("registration", func(t *typeTally) { t.verified++ })
	case errors.Is(verr, discovery.ErrDSKeyUnavailable):
		ft.count("registration", func(t *typeTally) { t.absent++ })
		ft.note("%s: registration attestation not checked (%v)", domain, verr)
	default:
		ft.count("registration", func(t *typeTally) { t.invalid++ })
		ft.fail("%s: registration attestation does NOT verify (%v) — Judge will now report judge.fail.attestation for this tenant", domain, verr)
	}
}

// epic44Checks are the checks SIGNET epic 44 changed. A disagreement between the
// two forms on one of them is this epic's regression; on any other it is a
// pre-existing fact about a form (or about `polis clone`), reported and not
// failed.
var epic44Checks = map[string]bool{
	"index.consistency": true, "content.attestations": true, "content.tags": true,
	"content.following": true, "content.blessed": true,
	"content.license_robots": true, "content.license_rsl": true,
}

// cloneGaps are checks on which a CLONE cannot agree with its original, because
// `polis clone` does not collect what the check reads. ⭐ Declared with the
// reason, so the disagreement is explained rather than silent (oversight's
// ruling on epic 44's live finding). ⚠️ They live HERE, not in sitecheck's
// formParity: that table is about the two forms of `polis validate`, and on an
// owner's own directory both forms genuinely run these. The gap is the clone's.
var cloneGaps = map[string]string{
	"policy.syntax": "`polis clone` does not collect policies/rules.jsonl — a clone copies a site's content, not its rules",
}

// checkFleetParity — SIGNET epic 44 D6, the EVIDENCE layer. The offline gate
// (sitecheck's forms_test.go) proves the two forms agree on a fixture; this
// clones each LIVE site, runs `polis validate <dir>` on the clone and
// `polis validate <url>` on the original, and compares them check by check.
// Before epic 44 remote index.consistency passed polis.polis.pub having fetched
// none of its two entries.
//
// ⚠️ A disagreement can be the clone's, not the validator's: a check only
// passes locally over files `polis clone` actually collected.
func checkFleetParity(ft *fleetTally, base string) {
	dir, err := os.MkdirTemp("", "polis-fleet-parity-")
	if err != nil {
		ft.note("%s: parity not checked — %v", base, err)
		return
	}
	defer os.RemoveAll(dir)
	target := dir + "/site"
	if _, err := clone.Clone(base, target, clone.CloneOptions{FullClone: true}); err != nil {
		ft.note("%s: parity not checked — clone failed: %v", base, err)
		return
	}

	local := sitecheck.RunLocalWith(target, nil)
	rr := sitecheck.NewRemote()
	rr.DSKeys = nil
	remoteReport := rr.RunSite(base)
	if remoteReport.FatalError != "" {
		ft.note("%s: parity NOT COMPARED — the remote run could not start: %s", base, remoteReport.FatalError)
		return
	}

	diffs := sitecheck.CompareForms(local, remoteReport)
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.paritySites++
	for _, d := range diffs {
		id := strings.SplitN(d, ":", 2)[0]
		if why, ok := cloneGaps[id]; ok {
			ft.parityExplained = append(ft.parityExplained, fmt.Sprintf("%s — %s (%s)", base, id, why))
			continue
		}
		if epic44Checks[id] {
			ft.failures = append(ft.failures, fmt.Sprintf("PARITY REGRESSION %s — %s", base, d))
		} else {
			ft.parityOther = append(ft.parityOther, fmt.Sprintf("%s — %s", base, strings.SplitN(d, "\n", 2)[0]))
		}
	}
}

func recordJSONStatus(ft *fleetTally, typ, url, status string, verr error) {
	switch status {
	case "valid":
		ft.count(typ, func(t *typeTally) { t.verified++ })
	case "unsigned":
		ft.count(typ, func(t *typeTally) { t.unsigned++ })
	case "invalid":
		ft.count(typ, func(t *typeTally) { t.invalid++ })
		ft.fail("%s: %s signature does NOT verify (%v)", url, typ, verr)
	default:
		ft.count(typ, func(t *typeTally) { t.absent++ })
	}
}

func reportFleet(t *testing.T, ft *fleetTally) {
	t.Helper()

	types := make([]string, 0, len(ft.byType))
	for k := range ft.byType {
		types = append(types, k)
	}
	sort.Strings(types)

	var totalVerified, totalInvalid int
	t.Logf("sites: %d checked, %d unreachable, %d without a published key",
		ft.sites, ft.sitesUnreachable, ft.sitesNoKey)
	for _, k := range types {
		v := ft.byType[k]
		totalVerified += v.verified
		totalInvalid += v.invalid
		t.Logf("  %-12s verified=%-5d unsigned=%-5d INVALID=%-4d absent/unreadable=%d",
			k, v.verified, v.unsigned, v.invalid, v.absent)
	}
	t.Logf("TOTAL VERIFIED: %d   TOTAL INVALID: %d", totalVerified, totalInvalid)
	t.Logf("RECORD URLS: %d live tag/attestation record(s) — `polis validate <url>` agrees with the package predicate on each (epic 42 V2)", ft.recordURLAgreed)
	t.Logf("PARITY: %d site(s) cloned and validated both ways (epic 44 D6); a disagreement on a check epic 44 changed is listed with the regressions below", ft.paritySites)
	if len(ft.parityExplained) > 0 {
		t.Logf("PARITY, EXPLAINED: %d disagreement(s) on a declared clone gap — the clone did not collect what the check reads:", len(ft.parityExplained))
		for _, p := range ft.parityExplained {
			t.Logf("  %s", p)
		}
	}
	if len(ft.parityOther) > 0 {
		t.Logf("PARITY, NOT EPIC 44's: %d disagreement(s) on checks this epic did not change — a fact about a form or about `polis clone`, reported and not failed:", len(ft.parityOther))
		for _, p := range ft.parityOther {
			t.Logf("  %s", p)
		}
	}

	for _, n := range ft.notes {
		t.Logf("note: %s", n)
	}

	// The canary's own verdict, reported separately from verification: the two
	// bases must agree on every artifact that exists today. A disagreement here
	// is not necessarily wrong, but it must never be a surprise.
	if len(ft.disagreements) == 0 {
		t.Logf("CANARY: legacy and typed signing bases agree on every live markdown artifact")
	} else {
		t.Errorf("CANARY: %d artifact(s) where the legacy and typed bases disagree", len(ft.disagreements))
		for _, d := range ft.disagreements {
			t.Logf("  disagreement: %s", d)
		}
	}

	// ⛔ The gate. A regression is an artifact this epic broke; nothing else in
	// the run may be reported in the same breath, because conflating the two is
	// how a real regression hides behind a site that was already failing.
	if len(ft.failures) > 0 {
		t.Errorf("%d live artifact(s) REGRESSED — they verified under the legacy base and do not under the typed one", len(ft.failures))
		for _, f := range ft.failures {
			t.Logf("  %s", f)
		}
	}

	// Pre-existing failures are REPORTED, LOUDLY, and are not this epic's
	// verdict. They are real and someone should look at them; they are not
	// evidence about the signing base, because the legacy rule rejected them
	// identically.
	if len(ft.preExisting) > 0 {
		t.Logf("PRE-EXISTING: %d live artifact(s) do not verify under EITHER base — not caused by this change, and not fixed by it:", len(ft.preExisting))
		for _, f := range ft.preExisting {
			t.Logf("  %s", f)
		}
	}
	if totalVerified == 0 {
		t.Error("no live artifact verified at all — the canary proved nothing")
	}

	// Epic 31. Movement is not automatically wrong — a site that rotated SHOULD
	// see its old posts resolve — but it must be listed, and on a fleet where
	// nobody has rotated it must be zero.
	t.Logf("KEY HISTORY: %d site(s) publish a history with a rotation", len(ft.sitesRotated))
	for _, s := range ft.sitesRotated {
		t.Logf("  rotated: %s", s)
	}
	if len(ft.historyResolved) == 0 {
		t.Logf("CANARY: history-aware verification agrees with current-key verification on every live markdown artifact")
	} else {
		t.Logf("HISTORY-RESOLVED: %d live artifact(s) verify ONLY through a retired key — the current key alone rejects them:", len(ft.historyResolved))
		for _, h := range ft.historyResolved {
			t.Logf("  %s", h)
		}
	}
}
