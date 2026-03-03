package policy

import "strings"

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
//   - (Allow, false)  -> no match      -> manual review
func EvaluateExplicit(policies []Policy, evt Event, ctx EvalContext) (Decision, bool) {
	for _, p := range policies {
		if !p.Active {
			continue
		}
		parsed, err := Parse(p.Rule)
		if err != nil {
			continue // skip unparseable rules
		}
		if matches(parsed, evt, ctx) {
			return Decision(parsed.Action), true
		}
	}
	return Allow, false
}

// matches checks if a parsed rule matches the given event and context.
func matches(rule *ParsedRule, evt Event, ctx EvalContext) bool {
	if !matchesType(rule.Type, evt.Type) {
		return false
	}
	if !matchesSource(rule.Source, evt.ActorDomain, ctx) {
		return false
	}
	if rule.Domain != "" && rule.Domain != evt.ActorDomain {
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
func matchesSource(source, actorDomain string, ctx EvalContext) bool {
	switch source {
	case "all":
		return true
	case "none":
		return false
	case "following":
		return ctx.FollowingDomains[actorDomain]
	case "followers":
		return ctx.FollowerDomains[actorDomain]
	default:
		return false
	}
}
