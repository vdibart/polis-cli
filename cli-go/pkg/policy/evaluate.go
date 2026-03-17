package policy

import "strings"

// EvalResult contains the full result of a policy evaluation, including
// the matched rule for audit logging.
type EvalResult struct {
	Decision Decision
	Matched  bool
	Rule     string // the raw policy string that matched, empty if no match
	RuleIdx  int    // index in the policy list (-1 if no match)
}

// Evaluate returns the decision for an event against a set of policies.
// First active matching rule wins. No match returns Allow (default permissive).
func Evaluate(policies []Policy, evt Event, ctx EvalContext) Decision {
	d, _ := EvaluateExplicit(policies, evt, ctx)
	return d
}

// EvaluateExplicit returns the decision and whether an explicit rule matched.
// Returns (Allow, false) when no rule matched (default permissive).
// This distinction is critical for blessing auto-decisions:
//   - (Allow, true)  -> explicit allow -> auto-grant
//   - (Deny, true)   -> explicit deny  -> auto-deny
//   - (Emit, true)   -> explicit emit  -> trigger side-effect
//   - (Omit, true)   -> explicit omit  -> suppress side-effect
//   - (Allow, false)  -> no match      -> manual review
func EvaluateExplicit(policies []Policy, evt Event, ctx EvalContext) (Decision, bool) {
	r := EvaluateWithLog(policies, evt, ctx)
	return r.Decision, r.Matched
}

// EvaluateWithLog returns the full evaluation result including the matched
// rule string and index for audit logging.
func EvaluateWithLog(policies []Policy, evt Event, ctx EvalContext) EvalResult {
	for i, p := range policies {
		if !p.Active {
			continue
		}
		parsed, err := Parse(p.Rule)
		if err != nil {
			continue // skip unparseable rules
		}
		if matches(parsed, evt, ctx) {
			return EvalResult{
				Decision: Decision(parsed.Action),
				Matched:  true,
				Rule:     p.Rule,
				RuleIdx:  i,
			}
		}
	}
	return EvalResult{
		Decision: Allow,
		Matched:  false,
		RuleIdx:  -1,
	}
}

// matches checks if a parsed rule matches the given event and context.
func matches(rule *ParsedRule, evt Event, ctx EvalContext) bool {
	if !matchesType(rule.Type, evt.Type) {
		return false
	}
	if !matchesSource(rule.Source, evt.ActorDomain, ctx) {
		return false
	}
	if rule.Domain != "" && !strings.EqualFold(rule.Domain, evt.ActorDomain) {
		return false
	}
	if rule.Target != "" && rule.Target != evt.TargetPath {
		return false
	}
	return true
}

// matchesType checks if a rule type matches an event type.
// "all" matches everything. "none" matches nothing.
// Otherwise, prefix matching with dot boundary:
// "pub.polis.comment" matches "pub.polis.comment" and "pub.polis.comment.published"
// but "pub.polis.com" does NOT match "pub.polis.comment.published".
func matchesType(ruleType, eventType string) bool {
	switch ruleType {
	case "all":
		return true
	case "none":
		return false
	default:
		if ruleType == eventType {
			return true
		}
		// Prefix match with dot boundary
		return strings.HasPrefix(eventType, ruleType+".")
	}
}

// matchesSource checks if the event actor matches the rule source.
// Domain lookups are case-insensitive (DNS hostnames per RFC 1123).
func matchesSource(source, actorDomain string, ctx EvalContext) bool {
	lowerActor := strings.ToLower(actorDomain)
	switch source {
	case "all":
		return true
	case "none":
		return false
	case "following":
		return ctx.FollowingDomains[lowerActor]
	case "followers":
		return ctx.FollowerDomains[lowerActor]
	case "self":
		return ctx.MyDomain != "" && strings.EqualFold(actorDomain, ctx.MyDomain)
	case "thread-blessed":
		// thread-blessed is resolved by the DS via storage query.
		// Client-side, it always returns false (no local resolution).
		return false
	default:
		return false
	}
}
