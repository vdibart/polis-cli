// Package policy provides a declarative rule system for controlling which
// content and events are accepted by a polis site. Policies are stored as
// JSONL files and evaluated client-side (CLI/webapp).
package policy

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Policy is a single line from a rules.jsonl file.
type Policy struct {
	Active bool   `json:"active"`
	Rule   string `json:"policy"`
}

// ParsedRule is the structured representation of a policy rule string.
//
// The Action field may be any of the six writable verbs in the grammar.
// See docs/general/reference/policy-grammar.md for the authoritative spec of which
// verb + type + layer combinations are valid.
type ParsedRule struct {
	Action string // "allow", "deny", "emit", "omit", "bless", or "review"
	Type   string // event type prefix, "all", or "none"
	Source string // "all", "none", "following", "followers", "self", "thread-blessed"
	Domain string // optional: actor domain filter (from "at <domain>")
	Target string // optional: target path filter (from "on <target>")
}

// Decision is the outcome of evaluating an event against a policy set.
type Decision string

const (
	Allow  Decision = "allow"
	Deny   Decision = "deny"
	Emit   Decision = "emit"
	Omit   Decision = "omit"
	Bless  Decision = "bless"  // Layer 1 (tenant inbound): auto-approve comment
	Review Decision = "review" // Layer 1 (tenant inbound): queue for human review
)

// ParseMode indicates which policy layer a rule is being parsed for.
// Different layers accept different verb + type combinations.
//   - TenantMode: rules from tenant rules.jsonl files (Layer 1 + Layer 2).
//   - OperatorMode: DS operator policies (Layer 3, stream ingestion).
//
// The Go code only ever parses tenant files; OperatorMode exists for
// symmetry with the TypeScript parser and for future use if the Go
// package is ever reused by the DS.
type ParseMode int

const (
	TenantMode ParseMode = iota
	OperatorMode
)

// EvalContext provides runtime context for source matching.
type EvalContext struct {
	MyDomain         string // the local site's domain
	FollowingDomains map[string]bool
	FollowerDomains  map[string]bool
	// ThreadBlessedDomains holds the (lowercase) domains with a comment the site
	// owner has already blessed on the thread being evaluated (Signet epic 45
	// D4). It is per request: the caller builds it for one thread from the
	// owner's own blessing list. Nil means no thread, and `thread-blessed`
	// matches nobody.
	ThreadBlessedDomains map[string]bool
}

// Event describes an incoming event to evaluate against policies.
type Event struct {
	Type         string
	ActorDomain  string
	TargetDomain string
	TargetPath   string
}

// The private and public policy files, as keys on the site.
// Private: .polis/policies/rules.jsonl (not published, highest priority)
// Public:  policies/rules.jsonl (published on site, lower priority)
const (
	PrivateRulesKey = ".polis/policies/rules.jsonl"
	PublicRulesKey  = "policies/rules.jsonl"
)

// DefaultPublicPolicyContent returns the default public policy file content for new sites.
// Public policies are published at policies/rules.jsonl and visible to the network.
// They declare the site's public posture — how it engages with the network.
//
// The file uses v2 grammar (see docs/general/reference/policy-grammar.md). Default rules:
//   - DMs accepted only from following (Layer 1 inbound)
//   - DMs denied from everyone else (explicit deny before catch-all)
//   - Self-comments auto-blessed (bless own content)
//   - Comments from followed authors auto-blessed (trust social graph)
//   - Authors with prior thread blessings auto-blessed (thread-trust)
//   - All other commenters land in review queue (explicit pending)
//   - Default-deny catch-all for unknown content types
//
// DM rules must be in the public file because the sender-side pre-flight
// check (policycheck.CheckDMEligibilityURL) fetches the recipient's public
// policy to determine eligibility before sending.
func DefaultPublicPolicyContent() string {
	return `{"version":2,"generator":"` + GetGenerator() + `"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"bless pub.polis.comment from self"}
{"active":true,"policy":"bless pub.polis.comment from following"}
{"active":true,"policy":"bless pub.polis.comment from thread-blessed"}
{"active":true,"policy":"review pub.polis.comment from all"}
{"active":true,"policy":"deny all from all"}
`
}

// DefaultPrivatePolicyContent returns the default private policy file content for new sites.
// Private policies are stored at .polis/policies/rules.jsonl and never published.
// They exist for user-specific overrides that shouldn't be advertised publicly
// (e.g., "deny all from all at stalker.polis.pub" to silently block an actor).
//
// Empty by default — a new site has nothing to hide. All standard rules live
// in the public policy. Private overrides are added as needed.
func DefaultPrivatePolicyContent() string {
	return `{"version":2,"generator":"` + GetGenerator() + `"}
`
}

// Version is the build that writes policy files, set at startup by the CLI
// (cmd/root.go) and the webapp (server.go) beside every other package's. It
// was a hard-coded "0.63.0", so every policy file named a build that did not
// write it.
var Version = "dev"

// GetGenerator is the generator string written into a policy file's header.
//
// ⚠️ It changes with every release, which is why ContentKey compares policy
// content with the header's generator removed: otherwise a version bump would
// flag every default-policy tenant as drifted.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// LoadPolicies loads a site's policies from its private and public JSONL
// files. Private policies are loaded first (higher priority), then public.
// Missing files are silently skipped. Malformed lines are skipped.
func LoadPolicies(site storage.Site) ([]Policy, error) {
	if !site.Valid() {
		return nil, errors.New("load policies: site handle is not usable")
	}
	var policies []Policy

	privPolicies, err := loadJSONL(site, PrivateRulesKey)
	if err != nil {
		return nil, err
	}
	policies = append(policies, privPolicies...)

	pubPolicies, err := loadJSONL(site, PublicRulesKey)
	if err != nil {
		return nil, err
	}
	policies = append(policies, pubPolicies...)

	return policies, nil
}

// LoadPolicyFiles loads a site's private and public rules separately, each in
// file order: the private rules are evaluated first, then the public, and the
// first rule that matches decides.
func LoadPolicyFiles(site storage.Site) (private, public []Policy, err error) {
	if !site.Valid() {
		return nil, nil, errors.New("load policies: site handle is not usable")
	}
	if private, err = loadJSONL(site, PrivateRulesKey); err != nil {
		return nil, nil, err
	}
	if public, err = loadJSONL(site, PublicRulesKey); err != nil {
		return nil, nil, err
	}
	return private, public, nil
}

// loadJSONL reads one policy file and returns its parsed entries.
// Returns empty slice on a missing file. Skips malformed lines.
//
// ⭐ It STREAMS THE BYTES with a bufio.Scanner rather than reading the file
// whole, which is what matters for a file that grows without bound, and
// storage.OpenRead is what keeps that true on a filesystem. A store with no
// filesystem can only hand the value over whole.
//
// ⚠️ A review called the streaming a rationalisation because the PARSED
// entries are accumulated into a slice. That is true and is a different
// question: the parsed form is bounded by what the caller needs, while a whole
// read would hold the raw bytes as well.
func loadJSONL(site storage.Site, key string) ([]Policy, error) {
	file, err := storage.OpenRead(site, key)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	return parseJSONL(file)
}

// parseJSONL parses policy lines from r, skipping blank and malformed ones.
func parseJSONL(r io.Reader) ([]Policy, error) {
	var policies []Policy
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var p Policy
		if err := json.Unmarshal(line, &p); err != nil {
			continue // skip malformed
		}
		policies = append(policies, p)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return policies, nil
}
