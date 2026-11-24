// Package connection is the owner's grant to an agent that connects to the
// site's API: a personal token, or (later) an OAuth sign-in, each pointing at
// one signed grant on the owner's site.
//
// ⭐ ONE GRANT PER CONNECTION. Switching off one connection must not touch
// another, and the marker on every act names exactly which connection acted.
// The token only presents the grant; it carries no permissions of its own.
//
// ⛔ THE GRANT IS THE OWNER'S RECORD, signed with her key, made while she is
// present (basis user-signed). Nothing here issues one on her behalf.
//
// ⭐ The artifacts are pkg/attestation's (the grant predicate, its writer rule,
// live-grant resolution); Rosie's own code stays in pkg/agent.
package connection

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The behaviour set a connection's grant names: "act through the site's API
// within the scopes listed in the grant". Version 1 is the API's scope
// vocabulary as of m-22. ⭐ A scope becoming grantable (follow, 2026-11-23) is
// not a new version: each grant names its scopes, so no standing grant gains
// one, and a new connection carries it only if the owner grants it.
const (
	Set     = "agent"
	Version = 1
	// MinVersion is the first version whose grants carry scopes.
	MinVersion = 1
)

// Behaviours is the current behaviour set, "agent/1".
func Behaviours() string { return fmt.Sprintf("%s/%d", Set, Version) }

// nameRe is what a connection's name may be. ⭐ Narrow on purpose: the name is
// written raw into signed frontmatter as `agent:`, so it must need no YAML
// quoting, and it is shown to readers of the site's agents page.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// ValidName reports whether name may name a connection. ⛔ It must read as a
// string to any YAML reader of the signed frontmatter, so it needs a letter
// and must not be one of YAML's words for true, false or null.
func ValidName(name string) bool {
	return nameRe.MatchString(name) && strings.TrimSpace(name) == name && !yamlNonString(name)
}

func yamlNonString(name string) bool {
	switch strings.ToLower(name) {
	case "null", "~", "true", "false", "yes", "no", "on", "off", "y", "n":
		return true
	}
	return !strings.ContainsAny(strings.ToLower(name), "abcdefghijklmnopqrstuvwxyz")
}

// PublicNameWarning is what the owner is told whenever a connection is named:
// the name is in a public record. ⭐ Shown at minting (the keys API) and on
// the consent page.
const PublicNameWarning = "A connection's name is public: it is written into a grant published on your site, " +
	"registered with the discovery service and listed on your site's agents page. " +
	"Choose a name you are happy for anyone to see."

// DefaultName is the name a connection gets when the owner gives none:
// "Agent <n>", one more than the connections the site has ever had. ⭐
// Deliberately says nothing about which software it is.
func DefaultName(site storage.Site) string {
	grants, _ := attestation.Grants(site, "")
	n := 1
	for _, g := range grants {
		if g.Set == Set {
			n++
		}
	}
	return fmt.Sprintf("Agent %d", n)
}

// ErrNotGrantable is a scope an agent's grant may not carry.
var ErrNotGrantable = errors.New("not a scope an agent can be granted")

// Config is what a grant write needs.
type Config struct {
	Site       storage.Site
	BaseURL    string
	PrivateKey []byte
	// Discovery registers each record with the DS as it is made. Nil, or a
	// site not registered there, writes the record without registering it.
	Discovery *attestation.DiscoveryConfig
}

// Issued is a grant written for a connection.
type Issued struct {
	ID          string
	URL         string
	Registered  bool
	RegisterErr error
}

// Issue writes the grant for a new connection named name, provided by
// provider (a claim: the client's own name for itself), allowing scopes.
//
// ⛔ It refuses any scope AgentGrantable refuses, an empty scope list, and a
// name ValidName refuses. Duplicate scopes are kept once; render is stored as
// appearance.
func Issue(cfg Config, name, provider string, scopes []string) (*Issued, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("a connection's name is 1 to 64 letters, digits, spaces, dots, dashes or underscores, starting with a letter or digit")
	}
	if strings.TrimSpace(provider) == "" {
		provider = name
	}
	clean, err := Scopes(scopes)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("the site's address is needed to write a grant in its name")
	}
	if len(cfg.PrivateKey) == 0 {
		return nil, errors.New("no site key to sign with")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	rec := &attestation.Record{
		Issuer:    base,
		Predicate: attestation.PredicateGrant,
		Subject:   attestation.Subject{Type: attestation.SubjectIdentity, ID: base},
		Payload: map[string]string{
			attestation.GrantKeyAgent:      name,
			attestation.GrantKeyProvider:   provider,
			attestation.GrantKeyBehaviours: Behaviours(),
			attestation.CustodyKeyBasis:    attestation.CustodyBasisUserSigned,
			attestation.GrantKeyScopes:     strings.Join(clean, " "),
		},
	}
	id, err := attestation.Issue(cfg.Site, rec, cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	out := &Issued{ID: id, URL: attestation.RecordURL(base, id)}
	out.Registered, out.RegisterErr = register(cfg, rec, id)
	_ = agent.RefreshProjections(cfg.Site)
	return out, nil
}

// Scopes validates and normalises a requested scope list.
func Scopes(scopes []string) ([]string, error) {
	var clean []string
	seen := map[string]bool{}
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == apidesc.ScopeRender {
			s = apidesc.ScopeAppearance
		}
		if !apidesc.AgentGrantable(s) {
			return nil, fmt.Errorf("%q: %w", s, ErrNotGrantable)
		}
		if !seen[s] {
			seen[s] = true
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		return nil, errors.New("a connection needs at least one scope")
	}
	return clean, nil
}

// Withdraw withdraws a connection's grant, so its token stops working at the
// next request. Withdrawing one already withdrawn is not an error.
func Withdraw(cfg Config, grantID string) error {
	if strings.TrimSpace(grantID) == "" {
		return nil
	}
	// ⛔ The projection is refreshed on every branch: a stale agents.json still
	// saying "live" is worse than none.
	defer func() { _ = agent.RefreshProjections(cfg.Site) }()
	newID, err := attestation.Withdraw(cfg.Site, grantID, cfg.PrivateKey)
	if err != nil {
		if strings.Contains(err.Error(), "already withdrawn") {
			return nil
		}
		return err
	}
	// A withdrawal is registered as it is made, as the grant was. Failing to
	// register does not undo it: the record on the site is the record.
	if rec, lerr := attestation.Load(cfg.Site, newID); lerr == nil {
		_, _ = register(cfg, rec, newID)
	}
	return nil
}

// Resolve returns the live grant a token points at, or nil when it is not
// standing. ⛔ Fail closed: an error reads as no grant to the caller.
func Resolve(site storage.Site, grantURL string) (*attestation.LiveGrant, error) {
	return attestation.ResolveGrant(site, grantURL, Set, MinVersion)
}

func register(cfg Config, rec *attestation.Record, id string) (bool, error) {
	d := cfg.Discovery
	if d == nil || d.DiscoveryURL == "" {
		return false, nil
	}
	if !discovery.IsRegisteredLocally(cfg.Site, d.DiscoveryURL) {
		return false, nil
	}
	c := *d
	if !c.Site.Valid() {
		c.Site = cfg.Site
	}
	if c.BaseURL == "" {
		c.BaseURL = rec.Issuer
	}
	if err := attestation.Register(rec, id, cfg.PrivateKey, &c); err != nil {
		return false, err
	}
	return true, nil
}
