package attestation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The grant — a user's record of what an agent is to her (Signet epic 11,
// designed in epic 46 R3, R12, R15).
//
// ⭐ ISSUED BY THE USER, ABOUT HER OWN SITE, SIGNED WITH HER OWN KEY. It is not
// the operator's permission and not a registry entry: it says "this is what
// <agent> is to me — who provides it, which versioned set of behaviours". Every
// act the agent performs cites this record's URL inside its signed bytes (the
// marker), which is what makes "exactly what did it do for me?" answerable.
//
// ⛔ NOT A CUSTODY GRANT. Custody is the operator holding a key, with nothing
// revocable and nothing enumerated (epic 17 D3). A grant is revocable — by the
// existing withdrawal — and names what it covers.
//
// ⚠️ `basis: hosting-terms` records that the user did not make the record
// herself: polis.pub issued it under its terms. It is honest about that, and it
// is never consent.

// PredicateGrant — the issuer grants an agent the behaviours it names.
const PredicateGrant = "pub.polis.attestation.grant"

// The grant's payload keys. `basis` and `terms` reuse epic 17's keys and values
// (CustodyKeyBasis, CustodyBasisHostingTerms, CustodyBasisUserSigned,
// CustodyKeyTerms) — one vocabulary for "how was this obtained".
const (
	// GrantKeyAgent is REQUIRED: the user's name for the agent. Meaningful only
	// together with this record — nothing about it is global.
	GrantKeyAgent = "agent"
	// GrantKeyProvider is REQUIRED: who supplies the software. A claim, never
	// authority.
	GrantKeyProvider = "provider"
	// GrantKeyBehaviours is REQUIRED: a versioned behaviour set, "<name>/<n>".
	// The provider documents what each set means.
	GrantKeyBehaviours = "behaviours"
)

var behavioursRe = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*)/([1-9][0-9]*)$`)

// ParseBehaviours splits "rosie/2" into its set name and version.
func ParseBehaviours(s string) (set string, version int, ok bool) {
	m := behavioursRe.FindStringSubmatch(s)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// checkGrant is the WRITER rule for PredicateGrant.
//
// ⛔ No field has a default. A grant without `basis` would silently claim a
// consent it does not carry; one without `behaviours` would read as "whatever
// the agent does today", which is the answer R15 exists to rule out.
func checkGrant(r *Record) error {
	if r.Subject.Type != SubjectIdentity {
		return fmt.Errorf("a grant is about the issuer's own site: subject type must be %q", SubjectIdentity)
	}
	if !sameSite(r.Subject.ID, r.Issuer) {
		return fmt.Errorf("a grant's subject must be the issuer's own site (%s), not %s — a user grants an agent for herself",
			r.Issuer, r.Subject.ID)
	}
	for _, key := range []string{GrantKeyAgent, GrantKeyProvider, GrantKeyBehaviours} {
		if strings.TrimSpace(r.Payload[key]) == "" {
			return fmt.Errorf("a grant requires payload %s=… — there is no default", key)
		}
	}
	if _, _, ok := ParseBehaviours(r.Payload[GrantKeyBehaviours]); !ok {
		return fmt.Errorf("payload %s=%q must be a versioned set, e.g. \"rosie/1\"", GrantKeyBehaviours, r.Payload[GrantKeyBehaviours])
	}
	switch got := r.Payload[CustodyKeyBasis]; got {
	case CustodyBasisHostingTerms, CustodyBasisUserSigned:
	case "":
		return fmt.Errorf("a grant requires payload %s=%s|%s — there is no default",
			CustodyKeyBasis, CustodyBasisHostingTerms, CustodyBasisUserSigned)
	default:
		return fmt.Errorf("payload %s=%q must be %q or %q", CustodyKeyBasis, got, CustodyBasisHostingTerms, CustodyBasisUserSigned)
	}
	if terms, ok := r.Payload[CustodyKeyTerms]; ok && !strings.HasPrefix(terms, "https://") {
		return fmt.Errorf("payload %s=%q must be an https URL", CustodyKeyTerms, terms)
	}
	return nil
}

func sameSite(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// GrantEntry is one grant record on a site, with what the site's own records
// say about it. It is what a settings screen and the agents projection show.
type GrantEntry struct {
	Record    *Record
	ID        string
	URL       string
	Agent     string
	Set       string
	Version   int
	Basis     string
	Signature SignatureStatus
	Withdrawn bool
	// WithdrawalURL is the withdrawal that retracts it, when one is known.
	WithdrawalURL string
	// WithdrawnAt is that withdrawal's `asserted`, when the record is on disk.
	WithdrawnAt string
}

// Grants returns every self-issued grant on the site naming agent, oldest first,
// with its withdrawal state. An empty agent returns grants for every agent.
//
// ⚠️ A site that never had a grant yields nil and no error: absence is never a
// finding.
func Grants(siteDir, agent string) ([]GrantEntry, error) {
	records, err := List(siteDir)
	if err != nil {
		return nil, err
	}
	// A withdrawal names what it retracts by URL. Index them first, so a grant
	// is withdrawn by any withdrawal on disk, whether or not the forward
	// pointer made it onto the grant.
	withdrawals := map[string]*Record{}
	for _, r := range records {
		if r.Predicate == PredicateWithdrawal {
			withdrawals[r.Subject.ID] = r
		}
	}

	var out []GrantEntry
	for _, r := range records {
		if r.Predicate != PredicateGrant || !sameSite(r.Subject.ID, r.Issuer) {
			continue
		}
		if agent != "" && r.Payload[GrantKeyAgent] != agent {
			continue
		}
		id := ID(r)
		e := GrantEntry{
			Record: r,
			ID:     id,
			URL:    RecordURL(r.Issuer, id),
			Agent:  r.Payload[GrantKeyAgent],
			Basis:  r.Payload[CustodyKeyBasis],
		}
		e.Set, e.Version, _ = ParseBehaviours(r.Payload[GrantKeyBehaviours])
		e.Signature, _ = VerifyRecord(siteDir, r)
		if w, ok := withdrawals[e.URL]; ok {
			e.Withdrawn = true
			e.WithdrawalURL = RecordURL(w.Issuer, ID(w))
			e.WithdrawnAt = w.Asserted
		} else if r.WithdrawnBy != "" {
			// ⚠️ A pointer with no record behind it still counts. The question a
			// grant answers is "may the agent act?", and the safe answer to an
			// unresolved retraction is no.
			e.Withdrawn = true
			e.WithdrawalURL = r.WithdrawnBy
		}
		out = append(out, e)
	}
	return out, nil
}

// LiveGrant is a grant the site's own records say is standing, resolved for one
// agent and one behaviour set.
//
// ⛔ ITS FIELDS ARE UNEXPORTED ON PURPOSE. Only ResolveLiveGrant constructs one,
// so a signing path that REQUIRES a *LiveGrant cannot be handed a fabricated
// one — the type is what makes "never sign an agent act without a live grant" a
// property of the code rather than a habit of its callers. A zero value is
// refused by every signer (Valid).
type LiveGrant struct {
	url     string
	agent   string
	set     string
	version int
	basis   string
}

// Valid reports whether g was produced by the resolver. Nil-safe.
func (g *LiveGrant) Valid() bool { return g != nil && g.url != "" && g.agent != "" }

// URL is the grant record's source URL — what the marker cites. Never a page.
func (g *LiveGrant) URL() string {
	if g == nil {
		return ""
	}
	return g.url
}

// Agent is the agent name the grant names.
func (g *LiveGrant) Agent() string {
	if g == nil {
		return ""
	}
	return g.agent
}

// Behaviours is the grant's behaviour set, "<name>/<n>".
func (g *LiveGrant) Behaviours() string {
	if g == nil {
		return ""
	}
	return fmt.Sprintf("%s/%d", g.set, g.version)
}

// Basis is how the grant was obtained.
func (g *LiveGrant) Basis() string {
	if g == nil {
		return ""
	}
	return g.basis
}

// ResolveLiveGrant returns the highest-version standing grant for agent whose
// behaviour set is `set` at version ≥ minVersion, or nil when there is none.
//
// ⛔ FAIL CLOSED. A grant counts only if it VERIFIES against the site's
// published key, is self-issued, and NO withdrawal on disk names it. Anything
// the resolver cannot establish reads as "no grant", because the only thing it
// is ever asked is whether an agent may act.
func ResolveLiveGrant(siteDir, agent, set string, minVersion int) (*LiveGrant, error) {
	grants, err := Grants(siteDir, agent)
	if err != nil {
		return nil, err
	}
	var best *GrantEntry
	for i := range grants {
		g := &grants[i]
		if g.Withdrawn || g.Signature != StatusValid || g.Set != set || g.Version < minVersion {
			continue
		}
		if best == nil || g.Version > best.Version {
			best = g
		}
	}
	if best == nil {
		return nil, nil
	}
	return &LiveGrant{url: best.URL, agent: best.Agent, set: best.Set, version: best.Version, basis: best.Basis}, nil
}
