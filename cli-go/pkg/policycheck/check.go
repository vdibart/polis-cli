// Package policycheck provides reusable remote policy evaluation.
// It composes the policy, remote, and following packages to check
// whether a remote site accepts events (e.g., DMs) from a given domain.
package policycheck

import (
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
)

// Result is the outcome of a remote policy check.
type Result struct {
	Status           string   // "open", "no-dm", "no-follow", "unknown"
	Reason           string   // human-readable explanation, empty if open
	FollowsUs        bool     // whether recipient follows myDomain
	FetchedFollowing []string // domains the recipient follows (nil if not fetched)
	PolicyDecided    bool     // true if public policy produced an explicit decision
}

// RequiredContext describes what data a policy set needs for evaluation.
type RequiredContext struct {
	NeedsFollowing bool // at least one DM rule uses "following" source
	NeedsFollowers bool // at least one DM rule uses "followers" source
}

// ScanRequired parses policies matching an event type prefix and returns
// which context sources are referenced. This determines what data
// to fetch before evaluation.
func ScanRequired(policies []policy.Policy, eventType string) RequiredContext {
	var ctx RequiredContext
	for _, p := range policies {
		if !p.Active {
			continue
		}
		parsed, err := policy.Parse(p.Rule)
		if err != nil {
			continue
		}
		if !typeMatches(parsed.Type, eventType) {
			continue
		}
		switch parsed.Source {
		case "following":
			ctx.NeedsFollowing = true
		case "followers":
			ctx.NeedsFollowers = true
		}
	}
	return ctx
}

// typeMatches checks if a rule type matches an event type (same logic as policy.matchesType).
func typeMatches(ruleType, eventType string) bool {
	switch ruleType {
	case "all":
		return true
	case "none":
		return false
	default:
		if ruleType == eventType {
			return true
		}
		return strings.HasPrefix(eventType, ruleType+".")
	}
}

// hasDMRules returns true if any active policy matches the pub.polis.dm event type.
func hasDMRules(policies []policy.Policy) bool {
	for _, p := range policies {
		if !p.Active {
			continue
		}
		parsed, err := policy.Parse(p.Rule)
		if err != nil {
			continue
		}
		if typeMatches(parsed.Type, "pub.polis.dm") {
			return true
		}
	}
	return false
}

// convertPolicies converts remote.Policy slice to policy.Policy slice.
// Both types have identical structure but are separate Go types.
func convertPolicies(rp []remote.Policy) []policy.Policy {
	if rp == nil {
		return nil
	}
	pp := make([]policy.Policy, len(rp))
	for i, p := range rp {
		pp[i] = policy.Policy{Active: p.Active, Rule: p.Rule}
	}
	return pp
}

// CheckDMEligibility checks whether recipientDomain accepts DMs from myDomain.
// Uses https://<recipientDomain> as the base URL.
func CheckDMEligibility(client *remote.Client, recipientDomain, myDomain string) Result {
	return CheckDMEligibilityURL(client, "https://"+recipientDomain, myDomain)
}

// CheckDMEligibilityURL checks whether the site at baseURL accepts DMs from myDomain.
//
// Two-phase evaluation:
//
//  1. Fetch recipient's public policies. If explicit DM rules exist, evaluate them
//     (fetching their following.json only if the policy references the "following" source).
//     An explicit allow → open. An explicit deny → no-dm. This short-circuits.
//
//  2. If no public DM rules found, fall back to mutual-follow check: fetch their
//     following.json and look for myDomain. If found → open + FollowsUs=true.
//     If not found → no-follow.
//
// FetchedFollowing and FollowsUs are populated whenever following.json is fetched
// (either for policy evaluation or the mutual-follow fallback).
func CheckDMEligibilityURL(client *remote.Client, baseURL, myDomain string) Result {
	baseURL = strings.TrimSuffix(baseURL, "/")

	// Phase 1: Fetch and evaluate public policies
	remotePolicies, err := client.FetchPolicies(baseURL)
	if err != nil {
		return Result{Status: "unknown", Reason: "Could not reach site"}
	}

	policies := convertPolicies(remotePolicies)

	if hasDMRules(policies) {
		// Public DM rules exist — evaluate them
		return evaluateWithPolicies(client, baseURL, myDomain, policies)
	}

	// Phase 2: No public DM rules — fall back to mutual-follow check
	return checkMutualFollow(client, baseURL, myDomain)
}

// evaluateWithPolicies runs policy evaluation against explicit public DM rules.
func evaluateWithPolicies(client *remote.Client, baseURL, myDomain string, policies []policy.Policy) Result {
	required := ScanRequired(policies, "pub.polis.dm")

	recipientDomain := extractDomainFromBaseURL(baseURL)
	evalCtx := policy.EvalContext{
		MyDomain: recipientDomain,
	}

	result := Result{PolicyDecided: true}

	if required.NeedsFollowing {
		domains, err := client.FetchFollowingList(baseURL)
		if err != nil {
			return Result{Status: "unknown", Reason: "Could not reach site"}
		}
		result.FetchedFollowing = domains
		evalCtx.FollowingDomains = make(map[string]bool, len(domains))
		for _, d := range domains {
			evalCtx.FollowingDomains[strings.ToLower(d)] = true
		}
		result.FollowsUs = evalCtx.FollowingDomains[strings.ToLower(myDomain)]
	}

	evt := policy.Event{
		Type:        "pub.polis.dm",
		ActorDomain: strings.ToLower(myDomain),
	}

	decision, matched := policy.EvaluateExplicit(policies, evt, evalCtx)

	if !matched {
		// No rule matched — default permissive
		result.Status = "open"
		return result
	}

	switch decision {
	case policy.Allow:
		result.Status = "open"
	default:
		result.Status = "no-dm"
		result.Reason = "Does not accept DMs from you"
	}
	return result
}

// checkMutualFollow fetches the recipient's following.json and checks if myDomain is in it.
func checkMutualFollow(client *remote.Client, baseURL, myDomain string) Result {
	domains, err := client.FetchFollowingList(baseURL)
	if err != nil {
		return Result{Status: "unknown", Reason: "Could not reach site"}
	}

	result := Result{FetchedFollowing: domains}

	myLower := strings.ToLower(myDomain)
	for _, d := range domains {
		if strings.ToLower(d) == myLower {
			result.Status = "open"
			result.FollowsUs = true
			return result
		}
	}

	result.Status = "no-follow"
	result.Reason = "Not following you"
	return result
}

// extractDomainFromBaseURL extracts just the hostname from a base URL.
func extractDomainFromBaseURL(baseURL string) string {
	s := baseURL
	if idx := strings.Index(s, "://"); idx != -1 {
		s = s[idx+3:]
	}
	if idx := strings.Index(s, "/"); idx != -1 {
		s = s[:idx]
	}
	if idx := strings.Index(s, ":"); idx != -1 {
		s = s[:idx]
	}
	return s
}
