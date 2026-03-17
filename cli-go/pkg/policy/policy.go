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
type ParsedRule struct {
	Action string // "allow", "deny", "emit", or "omit"
	Type   string // event type prefix, "all", or "none"
	Source string // "all", "none", "following", "followers", "self", "thread-blessed"
	Domain string // optional: actor domain filter (from "at <domain>")
	Target string // optional: target path filter (from "on <target>")
}

// Decision is the outcome of evaluating an event against a policy set.
type Decision string

const (
	Allow Decision = "allow"
	Deny  Decision = "deny"
	Emit  Decision = "emit"
	Omit  Decision = "omit"
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
//
// Default emit rules:
//   - Self-comments are auto-blessed (you always bless your own comments)
//   - Comments from followed authors are auto-blessed (trust your social graph)
//   - Authors with prior thread blessings are auto-blessed (thread-trust)
func DefaultPublicPolicyContent() string {
	return `{"version":1,"generator":"polis-cli-go/` + policyVersion + `"}
{"active":true,"policy":"emit pub.polis.comment.blessing from self"}
{"active":true,"policy":"emit pub.polis.comment.blessing from following"}
{"active":true,"policy":"emit pub.polis.comment.blessing from thread-blessed"}
`
}

// DefaultPrivatePolicyContent returns the default private policy file content for new sites.
// Private policies are stored at .polis/policies/rules.jsonl and never published.
//
// Default rules:
//   - Core content types (post, comment, follow, site) allowed from all (user can remove to disable)
//   - DMs accepted only from following (default social boundary)
//   - Self-notifications suppressed (you don't need to see your own actions)
//   - Self-authored feed events suppressed (you don't need to see your own posts in feed)
//   - Default-deny catch-all: unknown content types are denied
func DefaultPrivatePolicyContent() string {
	return `{"version":1,"generator":"polis-cli-go/` + policyVersion + `"}
{"active":true,"policy":"allow pub.polis.post from all"}
{"active":true,"policy":"allow pub.polis.comment from all"}
{"active":true,"policy":"allow pub.polis.follow from all"}
{"active":true,"policy":"allow pub.polis.site from all"}
{"active":true,"policy":"allow pub.polis.dm from following"}
{"active":true,"policy":"deny pub.polis.dm from all"}
{"active":true,"policy":"omit pub.polis.notification from self"}
{"active":true,"policy":"omit pub.polis.feed from self"}
{"active":true,"policy":"deny all from all"}
`
}

// DefaultPolicyContent returns the default public policy file content for new sites.
// Deprecated: Use DefaultPublicPolicyContent instead.
func DefaultPolicyContent() string {
	return DefaultPublicPolicyContent()
}

// policyVersion is the current policy file version string.
const policyVersion = "0.57.0"

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
