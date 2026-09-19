package actor

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
)

// The custody check — a tenant's own view of custody, run from HER machine
// (Signet epic 17, docs/signet/spec/custody.md §12).
//
// Three plain questions, answered from what is published:
//
//   - "Who did this?"          — her events at a discovery service, with who
//     signed each and under what authority, as the service recorded them.
//   - "What does this helper do?" — her operator's signed custody declaration
//     about her, verified against the OPERATOR's key.
//   - "What did I allow?"       — her own custody grants, verified against HER
//     key, withdrawals followed.
//
// # ⛔ The independence is in the VERIFICATION, not in the data
//
// On a hosted deployment the operator runs the site AND the discovery service,
// so every byte this reads could be withheld. What it cannot do is FORGE a
// record that passes: each declaration and grant is checked against a key
// fetched from the site that signed it. ⚠️ The event list is different — its
// signed_by and authority are the discovery service's own recording, and
// nothing here re-verifies an event. Say both, every time (CustodyLimit).
//
// # ⛔ Alice is the detector; this check is not
//
// It never decides whether the operator kept its word. Only the tenant knows
// which events she made herself, so the check lays the declaration beside the
// events and stops. Comparing them automatically is epic 18's.
//
// # ⛔ Absence is never a finding
//
// A self-hoster has nothing to declare, and from outside that looks exactly
// like an operator who declares nothing. The only findings are signatures that
// do not verify — the same line `polis attest verify` draws.

// CustodyLimit is the sentence every surface that shows this check must carry.
const CustodyLimit = "This makes custody VISIBLE; it does not reduce it. Declarations and grants were " +
	"verified against keys fetched from the sites that signed them, so an operator can WITHHOLD a record " +
	"from this check but cannot FORGE one that passes. The event list is the discovery service's own " +
	"recording and was not re-verified — and on a hosted site that service may be run by your operator too."

// Withdrawal states of a custody record.
const (
	// WithdrawalNotWithdrawn — no withdrawal was found, by pointer or in the
	// issuer's index.
	WithdrawalNotWithdrawn = "not_withdrawn"
	// WithdrawalWithdrawn — a withdrawal signed by the issuer names this exact
	// record and version.
	WithdrawalWithdrawn = "withdrawn"
	// WithdrawalUnknown — the issuer's withdrawals could not all be read.
	WithdrawalUnknown = "unknown"
)

// Finding kinds for the custody check.
const (
	FindingDeclarationSignatureInvalid = "declaration_signature_invalid"
	FindingGrantSignatureInvalid       = "grant_signature_invalid"
)

// maxStreamPages bounds the event read: 20 pages of 1000.
const maxStreamPages = 20

// maxListedEvents bounds how many events signed by someone else are listed.
const maxListedEvents = 50

// CustodyRecord is one declaration or grant, as checked.
type CustodyRecord struct {
	URL       string            `json:"url"`
	Issuer    string            `json:"issuer"`
	Subject   string            `json:"subject"`
	Asserted  string            `json:"asserted"`
	Payload   map[string]string `json:"payload,omitempty"`
	Signature string            `json:"signature"`
	// KeyNote is set when a RETIRED key verified the record — a weaker claim.
	KeyNote     string `json:"key_note,omitempty"`
	Withdrawal  string `json:"withdrawal"`
	WithdrawnBy string `json:"withdrawn_by,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// CustodyEvent is one event signed by someone other than the site.
type CustodyEvent struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	SignedBy  string `json:"signed_by"`
	Authority string `json:"authority"`
}

// CustodyAgentAct is one event a user agent signed with the site's key.
type CustodyAgentAct struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Agent     string `json:"agent"`
	Grant     string `json:"grant"`
	// GrantState is what the discovery service found about the grant when it
	// recorded the act (active · withdrawn · not-found · unknown); "" when it
	// recorded none. ⚠️ The service's recording, like the rest of the event.
	GrantState string `json:"grant_state,omitempty"`
}

// CustodyEvents tallies the site's events at a discovery service.
type CustodyEvents struct {
	Source    string `json:"source"`
	Examined  int    `json:"examined"`
	Truncated bool   `json:"truncated,omitempty"`
	// Error is set when the service could not be read: "could not look", never
	// a finding.
	Error string `json:"error,omitempty"`
	// SignedWithYourKey counts authority=self. ⚠️ Under custody that includes
	// whatever the operator signed as you. It is SignedByYou + SignedByAgent.
	SignedWithYourKey int `json:"signed_with_your_key"`
	// SignedByYou is SignedWithYourKey minus the acts a user agent marked.
	// ⚠️ Still includes whatever the operator signed as you: an operator actor
	// never carries a marker (epic 46), so custody is not separable here.
	SignedByYou int `json:"signed_by_you"`
	// SignedByAgent counts acts signed with your key BY A USER AGENT under a
	// grant — the event carries the agent marker (`agent` and `grant`) the
	// discovery service copied from the signed act (Signet epic 46).
	SignedByAgent int `json:"signed_by_agent"`
	// AgentActs lists them, each with the grant it cites (bounded).
	AgentActs []CustodyAgentAct `json:"agent_acts"`
	// NotRecorded counts events stored before the service recorded authority.
	NotRecorded   int            `json:"not_recorded"`
	Authority     map[string]int `json:"authority"`
	SignedByOther []CustodyEvent `json:"signed_by_other"`
}

// CustodyCheck is the full result.
type CustodyCheck struct {
	Site string `json:"site"`
	// Operator is the operator whose declarations were read, when there was one.
	Operator string `json:"operator,omitempty"`
	// OperatorSource is "given" (named by the person running the check) or
	// "granted" (the subject of the site's newest standing grant).
	OperatorSource string          `json:"operator_source,omitempty"`
	Declarations   []CustodyRecord `json:"declarations"`
	Grants         []CustodyRecord `json:"grants"`
	Events         *CustodyEvents  `json:"events,omitempty"`
	Notes          []string        `json:"notes"`
	Findings       []Finding       `json:"findings"`
	Limit          string          `json:"limit"`
}

// CheckCustody runs the check for siteDomain. givenOperator and dsURL are
// optional; without dsURL no events are read.
func CheckCustody(siteDomain, givenOperator, dsURL string, fetch Fetcher) *CustodyCheck {
	c := &CustodyCheck{
		Site:         bareDomain(siteDomain),
		Declarations: []CustodyRecord{},
		Grants:       []CustodyRecord{},
		Notes:        []string{},
		Findings:     []Finding{},
		Limit:        CustodyLimit,
	}

	// ---- "What did I allow?" — the site's own grants, against its own key.
	tenant, err := loadCustodyIdentity(fetch, c.Site)
	if err != nil {
		c.note("could not read %s's published identity, so its grants could not be checked: %v", c.Site, err)
	} else {
		c.Grants = c.collect(tenant, attestation.PredicateCustodyGrant, func(r *attestation.Record) bool {
			return sameDomain(r.Issuer, c.Site)
		})
	}

	// ---- whose declaration to read
	operator := bareDomain(givenOperator)
	switch {
	case operator != "":
		c.OperatorSource = "given"
	default:
		operator = newestStandingGrantee(c.Grants)
		if operator != "" {
			c.OperatorSource = "granted"
		}
	}

	// ---- "What does this helper do?" — the operator's declaration, against
	// the OPERATOR's key.
	if operator == "" {
		c.note("no operator was named and %s publishes no standing custody grant, so no declaration was read. "+
			"That describes what is published, not whether anyone holds the key: a self-hosted site has nothing to declare "+
			"and looks exactly like this. Pass --operator <domain> to read a specific operator's declarations", c.Site)
	} else {
		c.Operator = operator
		op, err := loadCustodyIdentity(fetch, operator)
		if err != nil {
			c.note("could not read %s's published identity, so its declarations could not be checked: %v", operator, err)
		} else {
			c.Declarations = c.collect(op, attestation.PredicateCustody, func(r *attestation.Record) bool {
				return sameDomain(r.Issuer, operator) && sameDomain(r.Subject.ID, c.Site)
			})
			if len(c.Declarations) == 0 {
				c.note("%s publishes no custody declaration about %s — a fact about what it published, not a finding", operator, c.Site)
			}
		}
	}

	// ---- "Who did this?" — the site's events, as the discovery service
	// recorded them.
	if strings.TrimSpace(dsURL) != "" {
		c.Events = readCustodyEvents(fetch, strings.TrimRight(dsURL, "/"), c.Site)
	}
	return c
}

func (c *CustodyCheck) note(format string, args ...interface{}) {
	c.Notes = append(c.Notes, fmt.Sprintf(format, args...))
}

// custodyIdentity is what one site publishes that the check needs.
type custodyIdentity struct {
	domain   string
	base     string
	pub      []byte
	chain    *site.KeyHistoryBlock
	indexURL string
	fetch    Fetcher
}

func loadCustodyIdentity(fetch Fetcher, domain string) (*custodyIdentity, error) {
	base := "https://" + domain
	body, err := fetch(base + "/.well-known/polis")
	if err != nil {
		return nil, err
	}
	var wk struct {
		PublicKey string `json:"public_key"`
		Bundles   map[string]struct {
			Path string `json:"path"`
		} `json:"bundles"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		return nil, fmt.Errorf("%s/.well-known/polis does not parse: %v", base, err)
	}
	if wk.PublicKey == "" {
		return nil, fmt.Errorf("%s/.well-known/polis publishes no public_key", base)
	}
	id := &custodyIdentity{domain: domain, base: base, pub: []byte(wk.PublicKey), fetch: fetch}
	if block, err := sitecheck.KeyHistoryFromWellKnownBytes(body); err == nil {
		id.chain, _ = sitecheck.ResolvingChain(block, domain, wk.PublicKey)
	}
	// By pointer, never by convention: the index sits beside the bundle
	// manifest .well-known/polis names (recipe 4).
	if b, ok := wk.Bundles["pub.polis.core"]; ok && b.Path != "" {
		id.indexURL = base + "/" + strings.TrimPrefix(path.Join(path.Dir(b.Path), "index.jsonl"), "/")
	}
	return id, nil
}

// collect reads every record of one predicate from a site's index, keeps those
// keep accepts, and verifies each against that site's key.
func (c *CustodyCheck) collect(id *custodyIdentity, predicate string, keep func(*attestation.Record) bool) []CustodyRecord {
	out := []CustodyRecord{}
	if id.indexURL == "" {
		c.note("%s publishes no bundle pointer, so its index — and its %s records — cannot be found", id.domain, shortPredicate(predicate))
		return out
	}
	body, err := id.fetch(id.indexURL)
	if err != nil {
		c.note("could not read %s's index, so its %s records were not checked: %v", id.domain, shortPredicate(predicate), err)
		return out
	}

	var candidates, withdrawals []string
	skipped := 0
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e struct {
			Type  string `json:"type"`
			Title string `json:"title"`
			Path  string `json:"path"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			skipped++
			continue
		}
		if e.Type != "attestation" || e.Path == "" {
			continue
		}
		u := id.base + "/" + strings.TrimPrefix(e.Path, "/")
		switch e.Title {
		case predicate:
			candidates = append(candidates, u)
		case attestation.PredicateWithdrawal:
			withdrawals = append(withdrawals, u)
		}
	}
	// ⚠️ Said out loud: a half-unparseable index must not read as a clean list.
	if skipped > 0 {
		c.note("%d line(s) of %s's index did not parse and were not read", skipped, id.domain)
	}

	w := &withdrawalIndex{id: id, urls: withdrawals}
	for _, u := range candidates {
		rec, err := fetchRecord(id, u)
		if err != nil {
			c.note("could not read %s: %v", u, err)
			continue
		}
		if !keep(rec) {
			continue
		}
		out = append(out, c.checkRecord(id, u, rec, w, predicate))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Asserted > out[j].Asserted })
	return out
}

func (c *CustodyCheck) checkRecord(id *custodyIdentity, u string, rec *attestation.Record, w *withdrawalIndex, predicate string) CustodyRecord {
	cr := CustodyRecord{
		URL:         u,
		Issuer:      rec.Issuer,
		Subject:     rec.Subject.ID,
		Asserted:    rec.Asserted,
		Payload:     rec.Payload,
		WithdrawnBy: rec.WithdrawnBy,
	}
	status, retired, verr := attestation.VerifyWithHistory(rec, id.pub, id.chain)
	cr.Signature = string(status)
	if status == attestation.StatusValid {
		cr.KeyNote = sitecheck.KeyUsedFor(id.chain, retired, rec.Asserted).Describe()
	} else if verr != nil {
		cr.Detail = verr.Error()
	}
	if status == attestation.StatusInvalid {
		kind := FindingGrantSignatureInvalid
		if predicate == attestation.PredicateCustody {
			kind = FindingDeclarationSignatureInvalid
		}
		c.Findings = append(c.Findings, Finding{
			Kind:   kind,
			Detail: u + " does not verify against " + id.domain + "'s published key",
		})
	}
	cr.Withdrawal = c.withdrawalOf(id, u, rec, w)
	return cr
}

// withdrawalOf follows the forward reference first, then — because a pointer's
// ABSENCE proves nothing — the issuer's own withdrawals.
func (c *CustodyCheck) withdrawalOf(id *custodyIdentity, u string, rec *attestation.Record, w *withdrawalIndex) string {
	names := func(wr *attestation.Record) bool {
		if wr == nil || wr.Predicate != attestation.PredicateWithdrawal || wr.Subject.Version != rec.Version {
			return false
		}
		canonical := attestation.RecordURL(rec.Issuer, attestation.ID(rec))
		return wr.Subject.ID == u || wr.Subject.ID == canonical
	}
	verifies := func(wr *attestation.Record) bool {
		if !sameDomain(wr.Issuer, id.domain) {
			return false
		}
		st, _, _ := attestation.VerifyWithHistory(wr, id.pub, id.chain)
		return st == attestation.StatusValid
	}

	if rec.WithdrawnBy != "" {
		wr, err := fetchRecord(id, rec.WithdrawnBy)
		if err == nil && names(wr) && verifies(wr) {
			return WithdrawalWithdrawn
		}
		// ⭐ DISCOVERY, NOT PROOF: a pointer that leads nowhere valid is discarded.
		c.note("%s points at a withdrawal (%s) that does not verify or does not name it — the pointer was discarded", u, rec.WithdrawnBy)
	}

	w.load()
	for _, wr := range w.records {
		if names(wr) && verifies(wr) {
			return WithdrawalWithdrawn
		}
	}
	if w.failed > 0 {
		return WithdrawalUnknown
	}
	return WithdrawalNotWithdrawn
}

// withdrawalIndex lazily reads a site's withdrawal records once.
type withdrawalIndex struct {
	id      *custodyIdentity
	urls    []string
	loaded  bool
	records []*attestation.Record
	failed  int
}

func (w *withdrawalIndex) load() {
	if w.loaded {
		return
	}
	w.loaded = true
	for _, u := range w.urls {
		r, err := fetchRecord(w.id, u)
		if err != nil {
			w.failed++
			continue
		}
		w.records = append(w.records, r)
	}
}

func fetchRecord(id *custodyIdentity, u string) (*attestation.Record, error) {
	body, err := id.fetch(u)
	if err != nil {
		return nil, err
	}
	var r attestation.Record
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("%s is not a JSON record: %v", u, err)
	}
	return &r, nil
}

// newestStandingGrantee is the subject of the newest grant that verifies and is
// not withdrawn, or "".
func newestStandingGrantee(grants []CustodyRecord) string {
	for _, g := range grants { // already newest first
		if g.Signature == string(attestation.StatusValid) && g.Withdrawal == WithdrawalNotWithdrawn {
			return bareDomain(g.Subject)
		}
	}
	return ""
}

func readCustodyEvents(fetch Fetcher, dsURL, domain string) *CustodyEvents {
	ev := &CustodyEvents{Source: dsURL, Authority: map[string]int{}, SignedByOther: []CustodyEvent{}, AgentActs: []CustodyAgentAct{}}
	since := "0"
	for page := 0; page < maxStreamPages; page++ {
		u := dsURL + "/v1/stream?actor=" + url.QueryEscape(domain) + "&since=" + url.QueryEscape(since) + "&limit=1000"
		body, err := fetch(u)
		if err != nil {
			ev.Error = err.Error()
			return ev
		}
		var resp discovery.StreamQueryResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			ev.Error = "the discovery service's response does not parse: " + err.Error()
			return ev
		}
		for _, e := range resp.Events {
			ev.Examined++
			switch {
			case e.Authority == "":
				ev.NotRecorded++
				ev.Authority["not_recorded"]++
			case e.Authority == "self":
				ev.SignedWithYourKey++
				ev.Authority["self"]++
				if agent, grant, ok := agentMarker(e.Payload); ok {
					ev.SignedByAgent++
					if len(ev.AgentActs) < maxListedEvents {
						state, _ := e.Payload["grant_state"].(string)
						ev.AgentActs = append(ev.AgentActs, CustodyAgentAct{
							ID: e.ID.String(), Type: e.Type, CreatedAt: e.Timestamp,
							Agent: agent, Grant: grant, GrantState: state,
						})
					}
				} else {
					ev.SignedByYou++
				}
			case strings.HasPrefix(e.Authority, "https://"):
				ev.Authority["grant"]++
			default:
				ev.Authority[e.Authority]++
			}
			if e.SignedBy != "" && !strings.EqualFold(e.SignedBy, domain) && len(ev.SignedByOther) < maxListedEvents {
				ev.SignedByOther = append(ev.SignedByOther, CustodyEvent{
					ID: e.ID.String(), Type: e.Type, CreatedAt: e.Timestamp,
					SignedBy: e.SignedBy, Authority: e.Authority,
				})
			}
		}
		if !resp.HasMore || resp.Cursor == "" || resp.Cursor == since {
			return ev
		}
		since = resp.Cursor
	}
	ev.Truncated = true
	return ev
}

// agentMarker reads the user-agent marker from an event payload. ⛔ Both
// fields or neither: a half marker is not a marker (Signet epic 46), and the
// act is counted as the site's own.
func agentMarker(payload map[string]interface{}) (agent, grant string, ok bool) {
	agent, _ = payload["agent"].(string)
	grant, _ = payload["grant"].(string)
	if agent == "" || grant == "" {
		return "", "", false
	}
	return agent, grant, true
}

// bareDomain lower-cases a domain or origin and strips scheme, path and a
// trailing slash.
func bareDomain(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

func sameDomain(originOrDomain, domain string) bool {
	return bareDomain(originOrDomain) == bareDomain(domain) && bareDomain(domain) != ""
}

func shortPredicate(p string) string {
	return strings.TrimPrefix(p, "pub.polis.attestation.")
}
