package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// Config is what every grant write needs.
type Config struct {
	SiteDir string
	BaseURL string
	// PrivateKey is the USER's key — the grant is her record.
	PrivateKey []byte
	// Discovery registers each record with the DS at the moment it is made. Nil,
	// or a site with no local registration marker, writes the record without
	// registering it; the record on the site is the record.
	Discovery *attestation.DiscoveryConfig
	// Terms is an optional https URL for the hosting terms a hosting-terms grant
	// is issued under.
	Terms string
}

// Written describes one record a flow wrote.
type Written struct {
	ID         string
	URL        string
	Basis      string
	Behaviours string
	Registered bool
	// RegisterErr is non-nil when the DS registration was attempted and failed.
	// Non-fatal: the signed record is on the site.
	RegisterErr error
	// ProjectionErr is non-nil when agents.json or the page could not be
	// refreshed. Non-fatal for the same reason.
	ProjectionErr error
}

// Outcomes of the default rule.
const (
	DefaultIssued    = "issued"
	DefaultStanding  = "skipped-standing"
	DefaultWithdrawn = "skipped-withdrawn"
)

// LiveRosieGrant resolves Rosie's live grant for blessing, or nil.
func LiveRosieGrant(siteDir string) (*attestation.LiveGrant, error) {
	return attestation.ResolveLiveGrant(siteDir, Rosie, RosieSet, BlessingMinVersion)
}

// IssueDefault applies the default rule for one site and issues a
// `hosting-terms` grant for the current version when it allows one.
//
// ⛔ THE RULE (epic 46 R11, R15): at most one default grant per (site, agent,
// version), and NONE AFTER ANY WITHDRAWAL for the (site, agent) pair. The
// blocking fact is the records on the user's own site.
//
// ⛔ NEVER COPY epic 17's declarer, which re-issues a withdrawn declaration by
// design. A user who switched Rosie off stays off — through reboots, through
// this pass, and through every new version.
func IssueDefault(cfg Config) (string, *Written, error) {
	grants, err := attestation.Grants(cfg.SiteDir, Rosie)
	if err != nil {
		return "", nil, fmt.Errorf("read grants: %w", err)
	}
	for _, g := range grants {
		if g.Withdrawn {
			return DefaultWithdrawn, nil, nil
		}
	}
	for _, g := range grants {
		if g.Set == RosieSet && g.Version == RosieVersion {
			return DefaultStanding, nil, nil
		}
	}
	w, err := issue(cfg, attestation.CustodyBasisHostingTerms)
	if err != nil {
		return "", w, err
	}
	return DefaultIssued, w, nil
}

// Enable is the user switching Rosie on: a `user-signed` grant for the current
// version. It writes nothing when a live grant at the current version already
// stands, and reports that as already on.
func Enable(cfg Config) (w *Written, alreadyOn bool, err error) {
	grants, err := attestation.Grants(cfg.SiteDir, Rosie)
	if err != nil {
		return nil, false, fmt.Errorf("read grants: %w", err)
	}
	for _, g := range grants {
		if !g.Withdrawn && g.Signature == attestation.StatusValid && g.Set == RosieSet && g.Version == RosieVersion {
			return nil, true, nil
		}
	}
	w, err = issue(cfg, attestation.CustodyBasisUserSigned)
	return w, false, err
}

// Disable is the user switching Rosie off: a withdrawal of EVERY standing Rosie
// grant, each registered with the DS immediately — the registration at the
// moment it is made is part of the evidence (epic 46 R4).
//
// It carries on past a failed withdrawal so one bad record cannot keep the
// others standing, and returns the first error.
func Disable(cfg Config) (written []*Written, err error) {
	defer func() {
		// ⛔ EVERY BRANCH REFRESHES THE PROJECTION, the failure branch included:
		// a stale agents.json still saying "live" is worse than none.
		perr := RefreshProjections(cfg.SiteDir)
		for _, w := range written {
			w.ProjectionErr = perr
		}
	}()

	grants, gerr := attestation.Grants(cfg.SiteDir, Rosie)
	if gerr != nil {
		return nil, fmt.Errorf("read grants: %w", gerr)
	}
	for _, g := range grants {
		if g.Withdrawn {
			continue
		}
		newID, werr := attestation.Withdraw(cfg.SiteDir, g.ID, cfg.PrivateKey)
		if werr != nil {
			if err == nil {
				err = fmt.Errorf("withdraw %s: %w", g.ID, werr)
			}
			continue
		}
		w := &Written{ID: newID, URL: g.URL, Basis: g.Basis, Behaviours: fmt.Sprintf("%s/%d", g.Set, g.Version)}
		if rec, lerr := attestation.Load(attestation.Path(cfg.SiteDir, newID)); lerr == nil {
			w.Registered, w.RegisterErr = register(cfg, rec, newID)
		}
		written = append(written, w)
	}
	return written, err
}

func issue(cfg Config, basis string) (w *Written, err error) {
	defer func() {
		perr := RefreshProjections(cfg.SiteDir)
		if w != nil {
			w.ProjectionErr = perr
		}
	}()
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("Rosie needs the site's address (POLIS_BASE_URL) to write a record in its name")
	}
	if len(cfg.PrivateKey) == 0 {
		return nil, errors.New("no site key to sign with")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	payload := map[string]string{
		attestation.GrantKeyAgent:      Rosie,
		attestation.GrantKeyProvider:   RosieProvider,
		attestation.GrantKeyBehaviours: RosieBehaviours(),
		attestation.CustodyKeyBasis:    basis,
	}
	if cfg.Terms != "" {
		payload[attestation.CustodyKeyTerms] = cfg.Terms
	}
	rec := &attestation.Record{
		Issuer:    base,
		Predicate: attestation.PredicateGrant,
		Subject:   attestation.Subject{Type: attestation.SubjectIdentity, ID: base},
		Payload:   payload,
	}
	id, err := attestation.Issue(cfg.SiteDir, rec, cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	w = &Written{ID: id, URL: attestation.RecordURL(base, id), Basis: basis, Behaviours: RosieBehaviours()}
	w.Registered, w.RegisterErr = register(cfg, rec, id)
	return w, nil
}

// register announces a record to the DS when the site is registered there.
//
// ⚠️ Registration is checked LOCALLY first: attestation.Register returns nil
// without sending anything when the site has no marker, and a silent nil must
// not be reported as "registered".
func register(cfg Config, rec *attestation.Record, id string) (bool, error) {
	d := cfg.Discovery
	if d == nil || d.DiscoveryURL == "" {
		return false, nil
	}
	if !discovery.IsRegisteredLocally(cfg.SiteDir, d.DiscoveryURL) {
		return false, nil
	}
	c := *d
	if c.DataDir == "" {
		c.DataDir = cfg.SiteDir
	}
	if c.BaseURL == "" {
		c.BaseURL = rec.Issuer
	}
	if err := attestation.Register(rec, id, cfg.PrivateKey, &c); err != nil {
		return false, err
	}
	return true, nil
}
