package policy

import (
	"fmt"
	"strings"
)

// Parse parses a policy rule string in TenantMode (the default for tenant
// rules.jsonl files). Use ParseInMode to select a different mode.
func Parse(rule string) (*ParsedRule, error) {
	return ParseInMode(rule, TenantMode)
}

// ParseInMode parses a policy rule string into a ParsedRule, validating
// the verb + type combination against the target layer.
//
// Grammar:
//
//	<allow|deny|emit|omit|bless|review> <type|all|none> from <all|none|following|followers|self|thread-blessed> [at <domain>] [on <target>]
//
// Legacy form: "emit pub.polis.comment.blessing from <scope>" is accepted
// at read time (so historical DS-signed attestations still verify) and
// translated to "bless pub.polis.comment from <scope>" at parse time.
// Callers that need the original rule string (e.g. signature verification)
// should preserve the raw string separately (see EvalResult.Rule).
//
// See docs/general/reference/policy-grammar.md for the authoritative grammar spec.
func ParseInMode(rule string, mode ParseMode) (*ParsedRule, error) {
	tokens := strings.Fields(rule)
	if len(tokens) < 4 {
		return nil, fmt.Errorf("policy rule too short: %q", rule)
	}

	// Token 0: action
	action := tokens[0]
	switch action {
	case "allow", "deny", "emit", "omit", "bless", "review":
		// valid
	default:
		return nil, fmt.Errorf("invalid action %q: must be allow, deny, emit, omit, bless, or review", action)
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

	// Legacy translation: historical "emit pub.polis.comment.blessing from X"
	// evaluates as if it were "bless pub.polis.comment from X" under the
	// new grammar. The caller preserves the raw string for signatures.
	if parsed.Action == "emit" && parsed.Type == "pub.polis.comment.blessing" {
		parsed.Action = "bless"
		parsed.Type = "pub.polis.comment"
		return parsed, nil
	}

	if err := validateLayer(parsed, mode); err != nil {
		return nil, err
	}

	return parsed, nil
}

// validateLayer checks that a parsed rule's verb + type combination is
// valid for the target layer. See docs/general/reference/policy-grammar.md for
// the full matrix. Catch-all types ("all", "none") are permitted in
// either mode with allow/deny for the defensive-terminal idiom.
func validateLayer(r *ParsedRule, mode ParseMode) error {
	if r.Type == "all" || r.Type == "none" {
		switch r.Action {
		case "allow", "deny":
			return nil
		default:
			return fmt.Errorf("%q is not valid for type %q; only allow/deny may be used with catch-all types", r.Action, r.Type)
		}
	}
	switch mode {
	case OperatorMode:
		return validateOperatorLayer(r)
	default:
		return validateTenantLayer(r)
	}
}

// validateTenantLayer enforces Layer 1 (inbound) + Layer 2 (outbound) verbs
// for tenant policy files. See docs/general/reference/policy-grammar.md.
func validateTenantLayer(r *ParsedRule) error {
	switch r.Type {
	case "pub.polis.dm":
		// Layer 1 inbound (live).
		switch r.Action {
		case "allow", "deny":
			return nil
		case "emit", "omit":
			// Layer 2 outbound (reserved).
			return nil
		default:
			return fmt.Errorf("%q is not valid for pub.polis.dm in tenant policies; use allow, deny, emit, or omit", r.Action)
		}
	case "pub.polis.comment":
		// Layer 1 inbound (live): bless / review / deny.
		// Layer 2 outbound (reserved): emit / omit.
		switch r.Action {
		case "deny", "bless", "review", "emit", "omit":
			return nil
		case "allow":
			return fmt.Errorf("allow is not valid for pub.polis.comment in tenant policies; use bless, review, or deny")
		default:
			return fmt.Errorf("invalid action %q for pub.polis.comment", r.Action)
		}
	default:
		// Any other pub.polis.* type: only Layer 2 outbound (emit/omit)
		// and defensive deny are meaningful. allow and bless/review are
		// DS-operator or comment-specific and rejected in tenant files.
		switch r.Action {
		case "emit", "omit":
			return nil
		case "deny":
			return nil
		case "allow":
			return fmt.Errorf("allow %s is not valid in tenant policies; this is governed by DS operator policies", r.Type)
		case "bless", "review":
			return fmt.Errorf("%q is only valid for pub.polis.comment", r.Action)
		}
	}
	return nil
}

// validateOperatorLayer enforces Layer 3 verbs for DS operator policies.
// Operator policies gate stream ingestion (allow/deny any type) and may
// also carry fallback blessing rules (bless/review on pub.polis.comment).
func validateOperatorLayer(r *ParsedRule) error {
	switch r.Action {
	case "allow", "deny":
		return nil
	case "bless", "review":
		if r.Type != "pub.polis.comment" {
			return fmt.Errorf("%q is only valid for pub.polis.comment", r.Action)
		}
		return nil
	case "emit", "omit":
		return fmt.Errorf("%q is not valid in DS operator policies; emit/omit belong to the tenant outbound layer", r.Action)
	}
	return nil
}
