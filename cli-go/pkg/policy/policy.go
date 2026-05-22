// Package policy provides a declarative rule system for controlling which
// content and events are accepted by a polis site. Policies are stored as
// JSONL files and evaluated client-side (CLI/webapp).
package policy

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// Policy is a single line from a rules.jsonl file.
type Policy struct {
	Active bool   `json:"active"`
	Rule   string `json:"policy"`
}

// ParsedRule is the structured representation of a policy rule string.
//
// The Action field may be any of the six writable verbs in the grammar.
// See docs/general/policy-grammar.md for the authoritative spec of which
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
	MyDomain         string          // the local site's domain
	FollowingDomains map[string]bool
	FollowerDomains  map[string]bool
}

// Event describes an incoming event to evaluate against policies.
type Event struct {
	Type         string
	ActorDomain  string
	TargetDomain string
	TargetPath   string
}

// DefaultPaths returns the private and public policy file paths for a site.
// Private: .polis/policies/rules.jsonl (not published, highest priority)
// Public:  policies/rules.jsonl (published on site, lower priority)
func DefaultPaths(dataDir string) (privatePath, publicPath string) {
	privatePath = filepath.Join(dataDir, ".polis", "policies", "rules.jsonl")
	publicPath = filepath.Join(dataDir, "policies", "rules.jsonl")
	return
}

// DefaultPublicPolicyContent returns the default public policy file content for new sites.
// Public policies are published at policies/rules.jsonl and visible to the network.
// They declare the site's public posture — how it engages with the network.
//
// The file uses v2 grammar (see docs/general/policy-grammar.md). Default rules:
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
	return `{"version":2,"generator":"polis-cli-go/` + policyVersion + `"}
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
	return `{"version":2,"generator":"polis-cli-go/` + policyVersion + `"}
`
}

// policyVersion is the current polis-cli-go version string written into the
// generator field of new policy files. Kept in sync with cli-go/version.txt.
const policyVersion = "0.63.0"

// LoadPolicies loads policies from private and public JSONL files.
// Private policies are loaded first (higher priority), then public.
// Missing files are silently skipped. Malformed lines are skipped.
func LoadPolicies(privatePath, publicPath string) ([]Policy, error) {
	var policies []Policy

	privPolicies, err := loadJSONL(privatePath)
	if err != nil {
		return nil, err
	}
	policies = append(policies, privPolicies...)

	pubPolicies, err := loadJSONL(publicPath)
	if err != nil {
		return nil, err
	}
	policies = append(policies, pubPolicies...)

	return policies, nil
}

// loadJSONL reads a JSONL file and returns parsed Policy entries.
// Returns empty slice on missing file. Skips malformed lines.
func loadJSONL(path string) ([]Policy, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var policies []Policy
	scanner := bufio.NewScanner(file)
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
