// Package pql parses PQL (Polis Query Language) sentences into a filter
// state struct, and composes them back. It is the Go sibling of the JS
// parser (webapp/internal/webui/www/pql.js) and the TS parser
// (discovery-service/core/pql.ts).
//
// =============================================================================
// HANDBOOK TRAIL MARKER — PQL grammar (Go side)
// =============================================================================
// This is the Go parser of the URL-as-filter thread. It walks a PQL sentence
// positionally and produces a Filter the stream dispatcher consumes (its Scope
// values are exactly what webapp parseStreamScope accepts).
//
// The canonical vocabulary lives in docs/general/pql-vocabulary.json; the
// cross-language contract is docs/general/pql-golden.jsonl. All three parsers
// (JS/Go/TS) assert against that golden corpus so the grammar can never drift.
// The tables below are a hand-mirror of the JSON — pql_test.go asserts they
// match it, so editing the JSON without updating here fails the build.
//
// Trail across files (URL-as-filter):
//
//	spec      — docs/general/pql.md
//	vocab     — docs/general/pql-vocabulary.json (canonical) + pql-golden.jsonl
//	JS parser — webapp/internal/webui/www/pql.js
//	Go parser — this file
//	TS parser — discovery-service/core/pql.ts
//
// =============================================================================
//
// Grammar:
//
//	sentence  := qualifier type relation scope [modifier]
//	qualifier := "all" | "new"            ; "new" reserved (UI-locked)
//	type      := "posts"|"comments"|"profiles"|"messages"|"drafts"|"activity"
//	           | <fully-qualified event type>   ; e.g. pub.polis.follow.announced
//	relation  := "from"                   ; authored-by (actor axis)
//	           | "about"                  ; concerns/targets (involved axis)
//	scope     := "me" | "my" "network" | "my" "mutuals" | "all" "polis"
//	           | <handle> | <handle> "'s" "network"
//	modifier  := "by" <key> | "with" <key> | "to" <key>
//
// Type-conditional rules are NOT enforced here — the parser is liberal; the
// dispatcher (webapp/DS) narrows valid combinations per surface (e.g. the DS
// rejects first-person scopes; see docs/general/pql.md).
package pql

import (
	"fmt"
	"regexp"
	"strings"
)

// Filter is the parsed PQL state. Scope/Type use internal values (hyphenated
// scopes, alias-or-FQ types) so downstream dispatch code consumes them directly.
type Filter struct {
	Qualifier string // "all" | "new"
	Type      string // alias internal (posts|comments|profiles|dms|drafts|activity) OR a fully-qualified event type
	Relation  string // "from" (actor) | "about" (involved/target)
	Scope     string // me|my-network|my-mutuals|all-polis|@handle|@handle:network
	Modifier  string // by-date|by-name|by-activity|with-comments|to-bless|"" (none)
}

// ── Vocabulary (hand-mirror of docs/general/pql-vocabulary.json) ──────────────

var qualifiers = map[string]bool{"all": true, "new": true}

var relations = map[string]bool{"from": true, "about": true}

// typeToInternal maps friendly aliases to internal values. Only messages->dms
// is remapped; the rest pass through. Fully-qualified event types bypass this
// map (see eventTypePattern).
var typeToInternal = map[string]string{
	"posts":    "posts",
	"comments": "comments",
	"profiles": "profiles",
	"messages": "dms",
	"drafts":   "drafts",
	"activity": "activity",
}

// internalToTypeToken inverts typeToInternal for Compose.
var internalToTypeToken = func() map[string]string {
	m := make(map[string]string, len(typeToInternal))
	for tok, val := range typeToInternal {
		m[val] = tok
	}
	return m
}()

type scopeAtom struct {
	tokens []string
	value  string
}

// scopeAtoms — multi-token phrases. Order matters when atoms share a leading
// token (e.g. both `my network` and `my mutuals` start with `my`).
var scopeAtoms = []scopeAtom{
	{[]string{"me"}, "me"},
	{[]string{"my", "network"}, "my-network"},
	{[]string{"my", "mutuals"}, "my-mutuals"},
	{[]string{"all", "polis"}, "all-polis"},
}

type modifierClause struct {
	tokens []string
	value  string
}

var modifierClauses = []modifierClause{
	{[]string{"by", "date"}, "by-date"},
	{[]string{"by", "name"}, "by-name"},
	{[]string{"by", "activity"}, "by-activity"},
	{[]string{"with", "comments"}, "with-comments"},
	{[]string{"to", "bless"}, "to-bless"},
}

var (
	handlePattern    = regexp.MustCompile(`(?i)^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	eventTypePattern = regexp.MustCompile(`^pub\.polis\.[a-z]+(\.[a-z]+)+$`)
)

// FirstPersonScopes are the owner-relative scope values that only the
// webapp/auth API resolves (against the authenticated owner). The DS dispatcher
// rejects them; the webapp rejects them for anonymous callers. Exported so
// dispatchers can enforce the boundary without re-deriving it.
var FirstPersonScopes = map[string]bool{
	"me":         true,
	"my-network": true,
	"my-mutuals": true,
}

// IsFirstPerson reports whether a scope value is owner-relative (me/my-*).
func IsFirstPerson(scope string) bool { return FirstPersonScopes[scope] }

// ── Core API ──────────────────────────────────────────────────────────────

// Parse turns a PQL sentence into a Filter. It requires an explicit relation +
// scope clause. Returns an error for any malformed input (callers treat that as
// "render the default filter").
func Parse(sentence string) (*Filter, error) {
	return parse(sentence, false, "")
}

// ParseTenantRelative parses a tenant-relative sentence where the
// "from <scope>" / "about <scope>" clause is OPTIONAL. When omitted, the filter
// defaults to (relation=from, scope=defaultScope) — defaultScope is the serving
// tenant, e.g. "@alice.polis.pub". An explicit relation clause still wins.
func ParseTenantRelative(sentence, defaultScope string) (*Filter, error) {
	return parse(sentence, true, defaultScope)
}

func parse(sentence string, tenantRelative bool, defaultScope string) (*Filter, error) {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(sentence)))
	if len(tokens) < 2 {
		return nil, fmt.Errorf("pql: sentence too short")
	}

	i := 0
	qualifier := tokens[i]
	i++
	if !qualifiers[qualifier] {
		return nil, fmt.Errorf("pql: unknown qualifier %q", qualifier)
	}

	typeToken := tokens[i]
	i++
	typ, ok := resolveType(typeToken)
	if !ok {
		return nil, fmt.Errorf("pql: unknown type %q", typeToken)
	}

	relation := "from"
	scope := ""

	hasRelationClause := i < len(tokens) && relations[tokens[i]]
	switch {
	case hasRelationClause:
		relation = tokens[i]
		i++
		sc, consumed, err := matchScope(tokens, i)
		if err != nil {
			return nil, err
		}
		scope = sc
		i += consumed
	case tenantRelative:
		// No relation clause — default to the serving tenant.
		scope = defaultScope
	default:
		return nil, fmt.Errorf("pql: expected 'from' or 'about'")
	}

	modifier := ""
	if i < len(tokens) {
		mod, consumed, err := matchModifier(tokens, i)
		if err != nil {
			return nil, err
		}
		modifier = mod
		i += consumed
	}
	if i != len(tokens) {
		return nil, fmt.Errorf("pql: unconsumed trailing tokens")
	}

	return &Filter{
		Qualifier: qualifier,
		Type:      typ,
		Relation:  relation,
		Scope:     scope,
		Modifier:  modifier,
	}, nil
}

// resolveType maps an alias to its internal value, or accepts a fully-qualified
// event type verbatim.
func resolveType(token string) (string, bool) {
	if v, ok := typeToInternal[token]; ok {
		return v, true
	}
	if eventTypePattern.MatchString(token) {
		return token, true
	}
	return "", false
}

func matchScope(tokens []string, start int) (string, int, error) {
	for _, atom := range scopeAtoms {
		if matchSequence(tokens, start, atom.tokens) {
			return atom.value, len(atom.tokens), nil
		}
	}
	// Possessive-network form: `<handle>'s network` -> @<handle>:network.
	// Checked before the bare-handle match so the more-specific form wins.
	if start+1 < len(tokens) && tokens[start+1] == "network" {
		pos := tokens[start]
		if strings.HasSuffix(pos, "'s") {
			bare := strings.TrimSuffix(pos, "'s")
			if handlePattern.MatchString(bare) {
				return "@" + bare + ":network", 2, nil
			}
		}
	}
	// Bare handle -> @<handle>.
	if start < len(tokens) && handlePattern.MatchString(tokens[start]) {
		return "@" + tokens[start], 1, nil
	}
	return "", 0, fmt.Errorf("pql: invalid scope")
}

func matchModifier(tokens []string, start int) (string, int, error) {
	for _, clause := range modifierClauses {
		if matchSequence(tokens, start, clause.tokens) {
			return clause.value, len(clause.tokens), nil
		}
	}
	return "", 0, fmt.Errorf("pql: invalid modifier")
}

func matchSequence(tokens []string, start int, seq []string) bool {
	if start+len(seq) > len(tokens) {
		return false
	}
	for i, t := range seq {
		if tokens[start+i] != t {
			return false
		}
	}
	return true
}

// Compose is the inverse of Parse: a Filter -> PQL sentence (space-separated).
// Returns an error for an invalid Filter.
func Compose(f *Filter) (string, error) {
	if f == nil {
		return "", fmt.Errorf("pql: nil filter")
	}
	qualifier := f.Qualifier
	if qualifier == "" {
		qualifier = "all"
	}
	if !qualifiers[qualifier] {
		return "", fmt.Errorf("pql: invalid qualifier %q", f.Qualifier)
	}

	typeToken, err := typeTokenFor(f.Type)
	if err != nil {
		return "", err
	}

	relation := f.Relation
	if relation == "" {
		relation = "from"
	}
	if !relations[relation] {
		return "", fmt.Errorf("pql: invalid relation %q", f.Relation)
	}

	scopeTokens, err := scopeToTokens(f.Scope)
	if err != nil {
		return "", err
	}

	parts := []string{qualifier, typeToken, relation}
	parts = append(parts, scopeTokens...)
	if f.Modifier != "" {
		modTokens, err := modifierToTokens(f.Modifier)
		if err != nil {
			return "", err
		}
		parts = append(parts, modTokens...)
	}
	return strings.Join(parts, " "), nil
}

// typeTokenFor maps an internal type value back to its sentence token (alias or
// the verbatim FQ event type).
func typeTokenFor(internal string) (string, error) {
	if tok, ok := internalToTypeToken[internal]; ok {
		return tok, nil
	}
	if eventTypePattern.MatchString(internal) {
		return internal, nil
	}
	return "", fmt.Errorf("pql: invalid type %q", internal)
}

func scopeToTokens(scope string) ([]string, error) {
	for _, atom := range scopeAtoms {
		if atom.value == scope {
			return append([]string(nil), atom.tokens...), nil
		}
	}
	if strings.HasPrefix(scope, "@") && strings.HasSuffix(scope, ":network") {
		bare := strings.TrimSuffix(strings.TrimPrefix(scope, "@"), ":network")
		if handlePattern.MatchString(bare) {
			return []string{bare + "'s", "network"}, nil
		}
		return nil, fmt.Errorf("pql: invalid possessive-network scope %q", scope)
	}
	if strings.HasPrefix(scope, "@") {
		bare := strings.TrimPrefix(scope, "@")
		if handlePattern.MatchString(bare) {
			return []string{bare}, nil
		}
	}
	return nil, fmt.Errorf("pql: invalid scope %q", scope)
}

func modifierToTokens(modifier string) ([]string, error) {
	for _, clause := range modifierClauses {
		if clause.value == modifier {
			return append([]string(nil), clause.tokens...), nil
		}
	}
	return nil, fmt.Errorf("pql: invalid modifier %q", modifier)
}

// ── URL helpers ─────────────────────────────────────────────────────────────

var pqlPathPattern = regexp.MustCompile(`(?:^|/)(?:_/)?pql/(.+?)/?$`)

// ParseURL extracts a Filter from a request path of the form
// `/pql/<sentence>` or `/_/pql/<sentence>` (tokens joined by `+`). defaultScope
// (the serving tenant, e.g. "@alice.polis.pub") enables tenant-relative
// sentences; pass "" to require an explicit relation clause.
func ParseURL(path, defaultScope string) (*Filter, error) {
	m := pqlPathPattern.FindStringSubmatch(path)
	if m == nil {
		return nil, fmt.Errorf("pql: not a /pql/ path")
	}
	sentence := strings.ReplaceAll(m[1], "+", " ")
	if defaultScope != "" {
		return ParseTenantRelative(sentence, defaultScope)
	}
	return Parse(sentence)
}

// ComposeURL renders a Filter to a /pql/ URL under basePath (e.g. "/_" or "").
// When defaultScope is non-empty and the filter is the tenant default
// (relation=from, scope=defaultScope), the relation+scope clause is omitted so
// the URL reads `…/pql/all+posts+by+date`.
func ComposeURL(f *Filter, basePath, defaultScope string) string {
	composeFilter := f
	if defaultScope != "" && f != nil && f.Relation == "from" && f.Scope == defaultScope {
		// Drop the relation+scope clause for the tenant-relative form.
		clone := *f
		clone.Relation = ""
		clone.Scope = ""
		sentence, err := composeTenantRelative(&clone)
		if err == nil {
			return basePath + "/pql/" + strings.ReplaceAll(sentence, " ", "+")
		}
	}
	sentence, err := Compose(composeFilter)
	if err != nil {
		return basePath + "/"
	}
	return basePath + "/pql/" + strings.ReplaceAll(sentence, " ", "+")
}

// composeTenantRelative composes a sentence with the relation+scope clause
// omitted (qualifier type [modifier]).
func composeTenantRelative(f *Filter) (string, error) {
	qualifier := f.Qualifier
	if qualifier == "" {
		qualifier = "all"
	}
	if !qualifiers[qualifier] {
		return "", fmt.Errorf("pql: invalid qualifier %q", f.Qualifier)
	}
	typeToken, err := typeTokenFor(f.Type)
	if err != nil {
		return "", err
	}
	parts := []string{qualifier, typeToken}
	if f.Modifier != "" {
		modTokens, err := modifierToTokens(f.Modifier)
		if err != nil {
			return "", err
		}
		parts = append(parts, modTokens...)
	}
	return strings.Join(parts, " "), nil
}
