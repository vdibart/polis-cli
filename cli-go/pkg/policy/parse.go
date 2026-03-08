package policy

import (
	"fmt"
	"strings"
)

// Parse parses a policy rule string into a ParsedRule.
//
// Grammar:
//
//	<allow|deny|emit|omit> <type|all|none> from <all|none|following|followers|self|thread-blessed> [at <domain>] [on <target>]
func Parse(rule string) (*ParsedRule, error) {
	tokens := strings.Fields(rule)
	if len(tokens) < 4 {
		return nil, fmt.Errorf("policy rule too short: %q", rule)
	}

	// Token 0: action
	action := tokens[0]
	switch action {
	case "allow", "deny", "emit", "omit":
		// valid
	default:
		return nil, fmt.Errorf("invalid action %q: must be allow, deny, emit, or omit", action)
	}

	// Token 1: type
	typ := tokens[1]
	if typ != "all" && typ != "none" && !strings.Contains(typ, ".") {
		return nil, fmt.Errorf("invalid type %q: must be all, none, or a dotted type prefix", typ)
	}

	// Token 2: "from"
	if tokens[2] != "from" {
		return nil, fmt.Errorf("expected 'from' at position 3, got %q", tokens[2])
	}

	// Token 3: source
	source := tokens[3]
	switch source {
	case "all", "none", "following", "followers", "self", "thread-blessed":
		// valid
	default:
		return nil, fmt.Errorf("invalid source %q: must be all, none, following, followers, self, or thread-blessed", source)
	}

	parsed := &ParsedRule{
		Action: action,
		Type:   typ,
		Source: source,
	}

	// Parse optional clauses: "at <domain>" and "on <target>"
	i := 4
	for i < len(tokens) {
		switch tokens[i] {
		case "at":
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("'at' clause requires a domain value")
			}
			parsed.Domain = tokens[i+1]
			i += 2
		case "on":
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("'on' clause requires a target value")
			}
			parsed.Target = tokens[i+1]
			i += 2
		default:
			return nil, fmt.Errorf("unexpected token %q at position %d", tokens[i], i+1)
		}
	}

	return parsed, nil
}
