package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// Loading tests
// ============================================================================

func TestLoadPolicies_MissingFiles(t *testing.T) {
	policies, err := LoadPolicies("/nonexistent/private", "/nonexistent/public")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(policies) != 0 {
		t.Errorf("expected 0 policies, got %d", len(policies))
	}
}

func TestLoadPolicies_PrivateAndPublicOrdering(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "private.jsonl")
	pubPath := filepath.Join(dir, "public.jsonl")

	os.WriteFile(privPath, []byte(`{"active":true,"policy":"deny all from all at evil.corp"}`+"\n"), 0644)
	os.WriteFile(pubPath, []byte(`{"active":true,"policy":"allow pub.polis.comment from following"}`+"\n"), 0644)

	policies, err := LoadPolicies(privPath, pubPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(policies))
	}
	// Private first
	if policies[0].Rule != "deny all from all at evil.corp" {
		t.Errorf("first policy = %q, want private rule", policies[0].Rule)
	}
	if policies[1].Rule != "allow pub.polis.comment from following" {
		t.Errorf("second policy = %q, want public rule", policies[1].Rule)
	}
}

func TestLoadPolicies_MalformedSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.jsonl")

	content := `{"active":true,"policy":"allow all from all"}
not valid json
{"active":false,"policy":"deny all from none"}
`
	os.WriteFile(path, []byte(content), 0644)

	policies, err := LoadPolicies(path, "/nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies (malformed skipped), got %d", len(policies))
	}
}

func TestLoadPolicies_EmptyLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.jsonl")

	content := `{"active":true,"policy":"allow all from all"}

{"active":true,"policy":"deny all from none"}
`
	os.WriteFile(path, []byte(content), 0644)

	policies, err := LoadPolicies(path, "/nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(policies) != 2 {
		t.Errorf("expected 2 policies, got %d", len(policies))
	}
}

func TestLoadPolicies_VersionHeaderSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.jsonl")

	// Version header line has no "policy" field -> should be loaded but skipped by evaluator
	content := `{"version":1,"generator":"polis-cli-go/0.57.0"}
{"active":true,"policy":"allow all from all"}
`
	os.WriteFile(path, []byte(content), 0644)

	policies, err := LoadPolicies(path, "/nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Version header is valid JSON with no "policy" field -> loaded as Policy{Active:false, Rule:""}
	// The evaluator will skip it because Active is false and Rule is empty
	if len(policies) != 2 {
		t.Fatalf("expected 2 entries (header + rule), got %d", len(policies))
	}
	// Only the second entry has a valid rule
	if policies[1].Rule != "allow all from all" {
		t.Errorf("second policy rule = %q, want allow all from all", policies[1].Rule)
	}
}

func TestDefaultPaths(t *testing.T) {
	priv, pub := DefaultPaths("/site")
	wantPriv := filepath.Join("/site", ".polis", "policies", "rules.jsonl")
	wantPub := filepath.Join("/site", "policies", "rules.jsonl")
	if priv != wantPriv {
		t.Errorf("private = %q, want %q", priv, wantPriv)
	}
	if pub != wantPub {
		t.Errorf("public = %q, want %q", pub, wantPub)
	}
}

// ============================================================================
// Parsing tests
// ============================================================================

func TestParse_AllKeywordCombos(t *testing.T) {
	tests := []struct {
		rule   string
		action string
		typ    string
		source string
	}{
		{"allow all from all", "allow", "all", "all"},
		{"deny all from all", "deny", "all", "all"},
		{"allow none from none", "allow", "none", "none"},
		{"deny none from none", "deny", "none", "none"},
		{"allow all from following", "allow", "all", "following"},
		{"deny all from followers", "deny", "all", "followers"},
		{"deny pub.polis.comment from all", "deny", "pub.polis.comment", "all"},
		// New v2 decision verbs
		{"bless pub.polis.comment from self", "bless", "pub.polis.comment", "self"},
		{"bless pub.polis.comment from following", "bless", "pub.polis.comment", "following"},
		{"bless pub.polis.comment from thread-blessed", "bless", "pub.polis.comment", "thread-blessed"},
		{"review pub.polis.comment from all", "review", "pub.polis.comment", "all"},
		// Layer 2 outbound verbs (reserved — parseable but not yet evaluated)
		{"emit pub.polis.post from self", "emit", "pub.polis.post", "self"},
		{"omit pub.polis.dm from all", "omit", "pub.polis.dm", "all"},
		// DM inbound
		{"allow pub.polis.dm from self", "allow", "pub.polis.dm", "self"},
		{"deny all from all at evil.corp", "deny", "all", "all"},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			p, err := Parse(tt.rule)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Action != tt.action {
				t.Errorf("action = %q, want %q", p.Action, tt.action)
			}
			if p.Type != tt.typ {
				t.Errorf("type = %q, want %q", p.Type, tt.typ)
			}
			if p.Source != tt.source {
				t.Errorf("source = %q, want %q", p.Source, tt.source)
			}
		})
	}
}

// TestParse_LegacyEmitBlessingTranslation verifies that legacy
// "emit pub.polis.comment.blessing from X" rules (as written before the v2
// grammar refactor, and still present in DS-signed historical attestations)
// parse to the equivalent v2 form "bless pub.polis.comment from X".
// The raw rule string is not rewritten by this package — callers that
// need the original string must preserve it themselves (EvalResult.Rule
// already does this for signature verification).
func TestParse_LegacyEmitBlessingTranslation(t *testing.T) {
	tests := []struct {
		legacy string
		source string
	}{
		{"emit pub.polis.comment.blessing from self", "self"},
		{"emit pub.polis.comment.blessing from following", "following"},
		{"emit pub.polis.comment.blessing from thread-blessed", "thread-blessed"},
	}
	for _, tt := range tests {
		t.Run(tt.legacy, func(t *testing.T) {
			p, err := Parse(tt.legacy)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Action != "bless" {
				t.Errorf("action = %q, want %q (legacy translated)", p.Action, "bless")
			}
			if p.Type != "pub.polis.comment" {
				t.Errorf("type = %q, want %q (legacy translated)", p.Type, "pub.polis.comment")
			}
			if p.Source != tt.source {
				t.Errorf("source = %q, want %q", p.Source, tt.source)
			}
		})
	}
}

// TestParse_TenantLayerRejection verifies type+verb combinations that are
// not meaningful in tenant policy files are rejected with clear errors.
// See docs/general/policy-grammar.md for the full validation matrix.
func TestParse_TenantLayerRejection(t *testing.T) {
	rejected := []string{
		// Comments have no acceptance layer — allow is meaningless.
		"allow pub.polis.comment from all",
		"allow pub.polis.comment from following",
		// bless/review are comment-only in the tenant layer.
		"bless pub.polis.post from self",
		"review pub.polis.dm from all",
		// allow/deny of post/follow/site are DS-operator concerns.
		"allow pub.polis.post from all",
		"allow pub.polis.follow from all",
	}
	for _, rule := range rejected {
		t.Run(rule, func(t *testing.T) {
			if _, err := Parse(rule); err == nil {
				t.Errorf("expected tenant-mode parse error for %q", rule)
			}
		})
	}
}

// TestParse_OperatorLayerAccepts verifies the DS-operator layer accepts
// the broader set of allow/deny rules that tenant files reject.
func TestParse_OperatorLayerAccepts(t *testing.T) {
	accepted := []string{
		"allow pub.polis.post from all",
		"allow pub.polis.comment from all",
		"allow pub.polis.follow from all",
		"allow pub.polis.site from all",
		"bless pub.polis.comment from following",
		"review pub.polis.comment from all",
	}
	for _, rule := range accepted {
		t.Run(rule, func(t *testing.T) {
			if _, err := ParseInMode(rule, OperatorMode); err != nil {
				t.Errorf("unexpected operator-mode parse error for %q: %v", rule, err)
			}
		})
	}
}

// TestParse_OperatorLayerRejects verifies the DS-operator layer rejects
// emit/omit (which belong to the tenant-outbound Layer 2).
func TestParse_OperatorLayerRejects(t *testing.T) {
	rejected := []string{
		"emit pub.polis.post from self",
		"omit pub.polis.dm from all",
	}
	for _, rule := range rejected {
		t.Run(rule, func(t *testing.T) {
			if _, err := ParseInMode(rule, OperatorMode); err == nil {
				t.Errorf("expected operator-mode parse error for %q", rule)
			}
		})
	}
}

func TestParse_OptionalClauses(t *testing.T) {
	// at domain
	p, err := Parse("deny all from all at evil.corp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Domain != "evil.corp" {
		t.Errorf("domain = %q, want %q", p.Domain, "evil.corp")
	}
	if p.Target != "" {
		t.Errorf("target = %q, want empty", p.Target)
	}

	// on target
	p, err = Parse("deny pub.polis.comment from all on posts/2026/01/draft.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Target != "posts/2026/01/draft.md" {
		t.Errorf("target = %q, want %q", p.Target, "posts/2026/01/draft.md")
	}
	if p.Domain != "" {
		t.Errorf("domain = %q, want empty", p.Domain)
	}

	// both at and on
	p, err = Parse("bless pub.polis.comment from following at friend.com on posts/hello.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Domain != "friend.com" {
		t.Errorf("domain = %q, want %q", p.Domain, "friend.com")
	}
	if p.Target != "posts/hello.md" {
		t.Errorf("target = %q, want %q", p.Target, "posts/hello.md")
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []string{
		"",
		"allow",
		"allow all",
		"allow all from",
		"invalid all from all",           // bad action
		"grant all from all",             // bad action (not allow/deny/emit/omit)
		"allow all to all",               // "to" instead of "from"
		"allow all from nobody",           // bad source
		"allow all from all at",           // at without domain
		"allow all from all on",           // on without target
		"allow all from all bogus extra",  // unexpected token
	}

	for _, rule := range tests {
		t.Run(rule, func(t *testing.T) {
			_, err := Parse(rule)
			if err == nil {
				t.Errorf("expected error for %q", rule)
			}
		})
	}
}

func TestParse_TypeValidation(t *testing.T) {
	// Single word without dots should fail (unless "all" or "none")
	_, err := Parse("allow badtype from all")
	if err == nil {
		t.Error("expected error for non-dotted type")
	}
}

// TestParse_RoundTrip exercises rules that survive parse → reconstruct
// unchanged. The legacy "emit pub.polis.comment.blessing from X" form is
// intentionally excluded because the parser translates it (see
// TestParse_LegacyEmitBlessingTranslation).
func TestParse_RoundTrip(t *testing.T) {
	rules := []string{
		"bless pub.polis.comment from self",
		"bless pub.polis.comment from following",
		"bless pub.polis.comment from thread-blessed",
		"review pub.polis.comment from all",
		"emit pub.polis.post from self",
		"omit pub.polis.dm from all",
		"allow pub.polis.dm from following",
		"deny pub.polis.dm from all",
		"bless pub.polis.comment from following at friend.com on posts/hello.md",
	}

	for _, rule := range rules {
		t.Run(rule, func(t *testing.T) {
			p, err := Parse(rule)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			reconstructed := p.Action + " " + p.Type + " from " + p.Source
			if p.Domain != "" {
				reconstructed += " at " + p.Domain
			}
			if p.Target != "" {
				reconstructed += " on " + p.Target
			}
			if reconstructed != rule {
				t.Errorf("round-trip mismatch: got %q, want %q", reconstructed, rule)
			}
		})
	}
}

// ============================================================================
// Type matching tests
// ============================================================================

func TestMatchesType(t *testing.T) {
	tests := []struct {
		ruleType  string
		eventType string
		want      bool
	}{
		// "all" matches everything
		{"all", "pub.polis.comment.published", true},
		{"all", "anything", true},

		// "none" matches nothing
		{"none", "pub.polis.comment.published", false},
		{"none", "", false},

		// Exact match
		{"pub.polis.comment", "pub.polis.comment", true},
		{"pub.polis.comment.published", "pub.polis.comment.published", true},

		// Prefix match with dot boundary
		{"pub.polis.comment", "pub.polis.comment.published", true},
		{"pub.polis.comment", "pub.polis.comment.blessing.requested", true},

		// Prefix without dot boundary should NOT match
		{"pub.polis.com", "pub.polis.comment.published", false},

		// Longer rule doesn't match shorter event
		{"pub.polis.comment.published", "pub.polis.comment", false},
	}

	for _, tt := range tests {
		t.Run(tt.ruleType+"_vs_"+tt.eventType, func(t *testing.T) {
			got := matchesType(tt.ruleType, tt.eventType)
			if got != tt.want {
				t.Errorf("matchesType(%q, %q) = %v, want %v", tt.ruleType, tt.eventType, got, tt.want)
			}
		})
	}
}

// ============================================================================
// Source matching tests
// ============================================================================

func TestMatchesSource(t *testing.T) {
	ctx := EvalContext{
		MyDomain:         "mysite.com",
		FollowingDomains: map[string]bool{"alice.com": true, "bob.com": true},
		FollowerDomains:  map[string]bool{"charlie.com": true},
	}

	tests := []struct {
		source string
		actor  string
		want   bool
	}{
		{"all", "anyone.com", true},
		{"none", "anyone.com", false},
		{"following", "alice.com", true},
		{"following", "unknown.com", false},
		{"followers", "charlie.com", true},
		{"followers", "unknown.com", false},
		// self source
		{"self", "mysite.com", true},
		{"self", "other.com", false},
		// thread-blessed always false client-side
		{"thread-blessed", "alice.com", false},
		{"thread-blessed", "mysite.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.source+"_"+tt.actor, func(t *testing.T) {
			got := matchesSource(tt.source, tt.actor, ctx)
			if got != tt.want {
				t.Errorf("matchesSource(%q, %q) = %v, want %v", tt.source, tt.actor, got, tt.want)
			}
		})
	}
}

func TestMatchesSource_SelfWithEmptyMyDomain(t *testing.T) {
	ctx := EvalContext{} // MyDomain is empty
	got := matchesSource("self", "anything.com", ctx)
	if got {
		t.Error("self should not match when MyDomain is empty")
	}
}

// ============================================================================
// Evaluation tests
// ============================================================================

func TestEvaluate_FirstMatchWins(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from following"},
		{Active: true, Rule: "deny pub.polis.comment from all"},
	}

	ctx := EvalContext{
		FollowingDomains: map[string]bool{"alice.com": true},
	}

	evt := Event{Type: "pub.polis.comment.published", ActorDomain: "alice.com"}

	// alice.com is in following -> first rule matches -> bless
	if got := Evaluate(policies, evt, ctx); got != Bless {
		t.Errorf("Evaluate = %v, want Bless (first match wins)", got)
	}

	// stranger.com is NOT in following -> first rule doesn't match -> second rule (deny all) matches
	evt.ActorDomain = "stranger.com"
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("Evaluate = %v, want Deny", got)
	}
}

func TestEvaluate_DefaultAllow(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "deny all from all at evil.corp"},
	}

	evt := Event{Type: "pub.polis.comment.published", ActorDomain: "good.com"}
	ctx := EvalContext{}

	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("Evaluate = %v, want Allow (default)", got)
	}
}

func TestEvaluate_InactiveSkipped(t *testing.T) {
	policies := []Policy{
		{Active: false, Rule: "deny all from all"},
		{Active: true, Rule: "allow all from all"},
	}

	evt := Event{Type: "pub.polis.comment.published", ActorDomain: "anyone.com"}
	ctx := EvalContext{}

	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("Evaluate = %v, want Allow (inactive deny skipped)", got)
	}
}

func TestEvaluate_DomainQualifier(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "deny all from all at spam.com"},
	}

	ctx := EvalContext{}

	// From spam.com -> deny
	evt := Event{Type: "pub.polis.follow.announced", ActorDomain: "spam.com"}
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("Evaluate = %v, want Deny for spam.com", got)
	}

	// From good.com -> default allow
	evt.ActorDomain = "good.com"
	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("Evaluate = %v, want Allow for good.com", got)
	}
}

func TestEvaluate_TargetQualifier(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "deny pub.polis.comment from all on posts/2026/01/draft.md"},
	}

	ctx := EvalContext{}

	// Matching target -> deny
	evt := Event{
		Type:        "pub.polis.comment.published",
		ActorDomain: "alice.com",
		TargetPath:  "posts/2026/01/draft.md",
	}
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("Evaluate = %v, want Deny for matching target", got)
	}

	// Different target -> default allow
	evt.TargetPath = "posts/2026/02/other.md"
	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("Evaluate = %v, want Allow for non-matching target", got)
	}
}

func TestEvaluate_EmptyPolicies(t *testing.T) {
	evt := Event{Type: "pub.polis.comment.published", ActorDomain: "anyone.com"}
	ctx := EvalContext{}

	if got := Evaluate(nil, evt, ctx); got != Allow {
		t.Errorf("Evaluate = %v, want Allow for empty policies", got)
	}
}

func TestEvaluate_MalformedRuleSkipped(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "this is not valid"},
		{Active: true, Rule: "allow all from all"},
	}

	evt := Event{Type: "pub.polis.comment.published", ActorDomain: "anyone.com"}
	ctx := EvalContext{}

	// Malformed first rule skipped, second rule matches
	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("Evaluate = %v, want Allow", got)
	}
}

func TestEvaluate_BlessDecision(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from self"},
	}

	ctx := EvalContext{MyDomain: "mysite.com"}
	evt := Event{Type: "pub.polis.comment", ActorDomain: "mysite.com"}

	decision, explicit := EvaluateExplicit(policies, evt, ctx)
	if decision != Bless || !explicit {
		t.Errorf("EvaluateExplicit = (%v, %v), want (Bless, true)", decision, explicit)
	}
}

func TestEvaluate_ReviewDecision(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from following"},
		{Active: true, Rule: "review pub.polis.comment from all"},
	}

	ctx := EvalContext{FollowingDomains: map[string]bool{"alice.com": true}}

	// Following -> Bless
	evt := Event{Type: "pub.polis.comment", ActorDomain: "alice.com"}
	if got := Evaluate(policies, evt, ctx); got != Bless {
		t.Errorf("following = %v, want Bless", got)
	}
	// Stranger -> Review (explicit pending via terminal catch-all)
	evt.ActorDomain = "stranger.com"
	if got := Evaluate(policies, evt, ctx); got != Review {
		t.Errorf("stranger = %v, want Review", got)
	}
}

// TestEvaluate_LegacyEmitBlessingEvaluatesAsBless verifies that historical
// DS-signed rule strings (containing "emit pub.polis.comment.blessing from X")
// evaluate to Bless under the new grammar, so old attestations continue to
// verify against the new decision logic.
func TestEvaluate_LegacyEmitBlessingEvaluatesAsBless(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "emit pub.polis.comment.blessing from self"},
	}
	ctx := EvalContext{MyDomain: "mysite.com"}
	evt := Event{Type: "pub.polis.comment", ActorDomain: "mysite.com"}

	result := EvaluateWithLog(policies, evt, ctx)
	if result.Decision != Bless {
		t.Errorf("decision = %v, want Bless (legacy translated)", result.Decision)
	}
	// Raw rule string is preserved verbatim for signature verification.
	if result.Rule != "emit pub.polis.comment.blessing from self" {
		t.Errorf("rule = %q, want legacy string preserved", result.Rule)
	}
}

func TestEvaluate_MixedVerbsFirstMatchWins(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from self"},
		{Active: true, Rule: "bless pub.polis.comment from following"},
		{Active: true, Rule: "deny pub.polis.comment from all"},
	}

	ctx := EvalContext{
		MyDomain:         "mysite.com",
		FollowingDomains: map[string]bool{"alice.com": true},
	}

	// Self -> bless (first rule)
	evt := Event{Type: "pub.polis.comment", ActorDomain: "mysite.com"}
	if got := Evaluate(policies, evt, ctx); got != Bless {
		t.Errorf("self = %v, want Bless", got)
	}

	// Following -> bless (second rule)
	evt.ActorDomain = "alice.com"
	if got := Evaluate(policies, evt, ctx); got != Bless {
		t.Errorf("following = %v, want Bless", got)
	}

	// Stranger -> deny (third rule)
	evt.ActorDomain = "stranger.com"
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("stranger = %v, want Deny", got)
	}
}

// ============================================================================
// EvaluateExplicit tests
// ============================================================================

func TestEvaluateExplicit_ExplicitBless(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from following"},
	}

	ctx := EvalContext{FollowingDomains: map[string]bool{"alice.com": true}}
	evt := Event{Type: "pub.polis.comment", ActorDomain: "alice.com"}

	decision, explicit := EvaluateExplicit(policies, evt, ctx)
	if decision != Bless || !explicit {
		t.Errorf("EvaluateExplicit = (%v, %v), want (Bless, true)", decision, explicit)
	}
}

func TestEvaluateExplicit_ExplicitDeny(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "deny all from all at evil.corp"},
	}

	ctx := EvalContext{}
	evt := Event{Type: "pub.polis.comment.blessing.requested", ActorDomain: "evil.corp"}

	decision, explicit := EvaluateExplicit(policies, evt, ctx)
	if decision != Deny || !explicit {
		t.Errorf("EvaluateExplicit = (%v, %v), want (Deny, true)", decision, explicit)
	}
}

func TestEvaluateExplicit_NoMatch(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from following"},
	}

	ctx := EvalContext{FollowingDomains: map[string]bool{"alice.com": true}}
	// stranger.com is not following -> no match
	evt := Event{Type: "pub.polis.comment", ActorDomain: "stranger.com"}

	decision, explicit := EvaluateExplicit(policies, evt, ctx)
	if decision != Allow || explicit {
		t.Errorf("EvaluateExplicit = (%v, %v), want (Allow, false)", decision, explicit)
	}
}

// ============================================================================
// EvaluateWithLog tests
// ============================================================================

func TestEvaluateWithLog_MatchedRule(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from self"},
		{Active: true, Rule: "bless pub.polis.comment from following"},
	}

	ctx := EvalContext{
		MyDomain:         "mysite.com",
		FollowingDomains: map[string]bool{"alice.com": true},
	}

	// Self match -> first rule
	evt := Event{Type: "pub.polis.comment", ActorDomain: "mysite.com"}
	result := EvaluateWithLog(policies, evt, ctx)
	if result.Decision != Bless || !result.Matched {
		t.Errorf("decision = %v, matched = %v, want Bless/true", result.Decision, result.Matched)
	}
	if result.Rule != "bless pub.polis.comment from self" {
		t.Errorf("rule = %q, want self rule", result.Rule)
	}
	if result.RuleIdx != 0 {
		t.Errorf("rule_idx = %d, want 0", result.RuleIdx)
	}

	// Following match -> second rule
	evt.ActorDomain = "alice.com"
	result = EvaluateWithLog(policies, evt, ctx)
	if result.Decision != Bless || !result.Matched {
		t.Errorf("decision = %v, matched = %v, want Bless/true", result.Decision, result.Matched)
	}
	if result.RuleIdx != 1 {
		t.Errorf("rule_idx = %d, want 1", result.RuleIdx)
	}
}

func TestEvaluateWithLog_NoMatch(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from self"},
	}

	ctx := EvalContext{MyDomain: "mysite.com"}
	evt := Event{Type: "pub.polis.comment", ActorDomain: "other.com"}

	result := EvaluateWithLog(policies, evt, ctx)
	if result.Decision != Allow || result.Matched {
		t.Errorf("decision = %v, matched = %v, want Allow/false", result.Decision, result.Matched)
	}
	if result.Rule != "" {
		t.Errorf("rule = %q, want empty", result.Rule)
	}
	if result.RuleIdx != -1 {
		t.Errorf("rule_idx = %d, want -1", result.RuleIdx)
	}
}

// ============================================================================
// Spec examples
// ============================================================================

func TestSpec_DomainBlocklist(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "deny all from all at evil.corp"},
	}

	ctx := EvalContext{}

	// Blocked domain
	evt := Event{Type: "pub.polis.follow.announced", ActorDomain: "evil.corp"}
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("evil.corp should be denied, got %v", got)
	}

	// Other domain
	evt.ActorDomain = "good.com"
	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("good.com should be allowed, got %v", got)
	}
}

func TestSpec_CommentsFromFollowingOnly(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from following"},
		{Active: true, Rule: "deny pub.polis.comment from all"},
	}

	ctx := EvalContext{
		FollowingDomains: map[string]bool{"friend.com": true},
	}

	// Followed author -> bless
	evt := Event{Type: "pub.polis.comment", ActorDomain: "friend.com"}
	if got := Evaluate(policies, evt, ctx); got != Bless {
		t.Errorf("friend.com comment should be blessed, got %v", got)
	}

	// Stranger -> deny
	evt.ActorDomain = "stranger.com"
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("stranger.com comment should be denied, got %v", got)
	}

	// Non-comment event -> default allow (neither rule matches)
	evt = Event{Type: "pub.polis.follow.announced", ActorDomain: "stranger.com"}
	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("follow from stranger should be allowed (not a comment type), got %v", got)
	}
}

func TestSpec_PrivateOverridesPublic(t *testing.T) {
	// Simulate private policy that allows, public policy that denies
	dir := t.TempDir()
	privPath := filepath.Join(dir, "private.jsonl")
	pubPath := filepath.Join(dir, "public.jsonl")

	// Private: bless comments from friend.com
	os.WriteFile(privPath, []byte(`{"active":true,"policy":"bless pub.polis.comment from all at friend.com"}`+"\n"), 0644)
	// Public: deny all comments
	os.WriteFile(pubPath, []byte(`{"active":true,"policy":"deny pub.polis.comment from all"}`+"\n"), 0644)

	policies, err := LoadPolicies(privPath, pubPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := EvalContext{}

	// friend.com matches private bless first
	evt := Event{Type: "pub.polis.comment", ActorDomain: "friend.com"}
	if got := Evaluate(policies, evt, ctx); got != Bless {
		t.Errorf("friend.com should be blessed (private override), got %v", got)
	}

	// stranger.com doesn't match private (wrong domain), falls to public deny
	evt.ActorDomain = "stranger.com"
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("stranger.com should be denied (public rule), got %v", got)
	}
}

func TestSpec_BlessingAutoDecision(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from following"},
		{Active: true, Rule: "deny pub.polis.comment from all at spam.com"},
	}

	ctx := EvalContext{
		FollowingDomains: map[string]bool{"alice.com": true},
	}

	// Followed author -> explicit bless (auto-grant)
	evt := Event{Type: "pub.polis.comment", ActorDomain: "alice.com"}
	decision, explicit := EvaluateExplicit(policies, evt, ctx)
	if decision != Bless || !explicit {
		t.Errorf("alice.com blessing = (%v, %v), want (Bless, true)", decision, explicit)
	}

	// spam.com -> explicit deny (auto-deny)
	evt.ActorDomain = "spam.com"
	decision, explicit = EvaluateExplicit(policies, evt, ctx)
	if decision != Deny || !explicit {
		t.Errorf("spam.com blessing = (%v, %v), want (Deny, true)", decision, explicit)
	}

	// stranger.com -> no match (manual review — default pending)
	evt.ActorDomain = "stranger.com"
	decision, explicit = EvaluateExplicit(policies, evt, ctx)
	if decision != Allow || explicit {
		t.Errorf("stranger.com blessing = (%v, %v), want (Allow, false)", decision, explicit)
	}
}

func TestSpec_BlessFromSelf(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "bless pub.polis.comment from self"},
		{Active: true, Rule: "bless pub.polis.comment from following"},
	}

	ctx := EvalContext{
		MyDomain:         "mysite.com",
		FollowingDomains: map[string]bool{"alice.com": true},
	}

	// Self-comment -> bless (auto-grant)
	evt := Event{Type: "pub.polis.comment", ActorDomain: "mysite.com"}
	decision, explicit := EvaluateExplicit(policies, evt, ctx)
	if decision != Bless || !explicit {
		t.Errorf("self blessing = (%v, %v), want (Bless, true)", decision, explicit)
	}

	// Following -> bless
	evt.ActorDomain = "alice.com"
	decision, explicit = EvaluateExplicit(policies, evt, ctx)
	if decision != Bless || !explicit {
		t.Errorf("following blessing = (%v, %v), want (Bless, true)", decision, explicit)
	}

	// Stranger -> no match -> default Allow/false (pending)
	evt.ActorDomain = "stranger.com"
	decision, explicit = EvaluateExplicit(policies, evt, ctx)
	if decision != Allow || explicit {
		t.Errorf("stranger blessing = (%v, %v), want (Allow, false)", decision, explicit)
	}
}

// ============================================================================
// Default policy content tests
// ============================================================================

func TestDefaultPublicPolicyContent(t *testing.T) {
	content := DefaultPublicPolicyContent()
	if content == "" {
		t.Fatal("DefaultPublicPolicyContent() returned empty string")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "rules.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	policies, err := LoadPolicies(path, "/nonexistent")
	if err != nil {
		t.Fatalf("failed to load default policy content: %v", err)
	}

	// Filter to only active policies with non-empty rules
	var activePolicies []Policy
	for _, p := range policies {
		if p.Active && p.Rule != "" {
			activePolicies = append(activePolicies, p)
		}
	}

	if len(activePolicies) != 7 {
		t.Fatalf("expected 7 active policies, got %d", len(activePolicies))
	}

	// Verify all rules parse correctly under the v2 grammar
	expectedRules := []string{
		"allow pub.polis.dm from following",
		"deny pub.polis.dm from all",
		"bless pub.polis.comment from self",
		"bless pub.polis.comment from following",
		"bless pub.polis.comment from thread-blessed",
		"review pub.polis.comment from all",
		"deny all from all",
	}
	for i, expected := range expectedRules {
		if activePolicies[i].Rule != expected {
			t.Errorf("policy[%d] = %q, want %q", i, activePolicies[i].Rule, expected)
		}
		// Ensure it parses
		if _, err := Parse(activePolicies[i].Rule); err != nil {
			t.Errorf("policy[%d] parse error: %v", i, err)
		}
	}
}

func TestDefaultPrivatePolicyContent(t *testing.T) {
	content := DefaultPrivatePolicyContent()
	if content == "" {
		t.Fatal("DefaultPrivatePolicyContent() returned empty string")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "rules.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	policies, err := LoadPolicies(path, "/nonexistent")
	if err != nil {
		t.Fatalf("failed to load default private policy content: %v", err)
	}

	// Private template is empty — no active rules (just the version header)
	var activePolicies []Policy
	for _, p := range policies {
		if p.Active && p.Rule != "" {
			activePolicies = append(activePolicies, p)
		}
	}

	if len(activePolicies) != 0 {
		t.Fatalf("expected 0 active policies (empty private template), got %d", len(activePolicies))
	}
}

func TestDefaultPublicPolicy_DenyAllCatchAll(t *testing.T) {
	// The default public policy ends with "deny all from all" which should
	// deny unknown content types but allow DMs from following and emit blessings.
	content := DefaultPublicPolicyContent()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	policies, err := LoadPolicies("/nonexistent", path)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	ctx := EvalContext{
		MyDomain:         "mysite.com",
		FollowingDomains: map[string]bool{"friend.com": true},
	}

	// Unknown type should be denied by the catch-all
	evt := Event{Type: "pub.custom.widget.created", ActorDomain: "anyone.com"}
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("unknown type should be denied by catch-all, got %v", got)
	}

	// DM from following should be allowed
	evt = Event{Type: "pub.polis.dm.delivered", ActorDomain: "friend.com"}
	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("DM from following should be allowed, got %v", got)
	}

	// DM from stranger should be denied
	evt = Event{Type: "pub.polis.dm.delivered", ActorDomain: "stranger.com"}
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("DM from stranger should be denied, got %v", got)
	}
}

func TestDefaultPolicies_PrivateOverridesPublic(t *testing.T) {
	// A private deny rule should shadow the public emit rules.
	dir := t.TempDir()
	privPath := filepath.Join(dir, "private.jsonl")
	pubPath := filepath.Join(dir, "public.jsonl")

	os.WriteFile(privPath, []byte(`{"active":true,"policy":"deny all from all at blocked.com"}`+"\n"), 0644)
	os.WriteFile(pubPath, []byte(DefaultPublicPolicyContent()), 0644)

	policies, err := LoadPolicies(privPath, pubPath)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	ctx := EvalContext{
		MyDomain:         "mysite.com",
		FollowingDomains: map[string]bool{"blocked.com": true},
	}

	// Blessing from blocked.com should be denied by private override
	evt := Event{Type: "pub.polis.comment", ActorDomain: "blocked.com"}
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("comment from blocked domain should be denied, got %v", got)
	}

	// Comment from non-blocked following should be blessed under v2 defaults
	evt = Event{Type: "pub.polis.comment", ActorDomain: "friend.com"}
	ctx.FollowingDomains["friend.com"] = true
	if got := Evaluate(policies, evt, ctx); got != Bless {
		t.Errorf("comment from non-blocked following should be blessed, got %v", got)
	}
}

// TestDefaultPolicies_DiscoverPolisPubLandsInReview is the regression test
// for the bug that motivated the v2 refactor. Under v1 defaults, a comment
// from an unfollowed domain (like discover.polis.pub) matched no emit rule
// and silently fell through to the `deny all from all` terminal — producing
// an auto-deny where a human-review outcome was expected. Under v2 defaults
// the explicit `review pub.polis.comment from all` terminal fires first,
// producing the correct Review decision.
func TestDefaultPolicies_DiscoverPolisPubLandsInReview(t *testing.T) {
	dir := t.TempDir()
	pubPath := filepath.Join(dir, "public.jsonl")
	os.WriteFile(pubPath, []byte(DefaultPublicPolicyContent()), 0644)

	policies, err := LoadPolicies("/nonexistent", pubPath)
	if err != nil {
		t.Fatalf("failed to load defaults: %v", err)
	}

	ctx := EvalContext{
		MyDomain:         "mysite.com",
		FollowingDomains: map[string]bool{"friend.com": true},
	}

	// Unfollowed domain commenting on my post: should land in review,
	// not be auto-denied by a catch-all.
	evt := Event{Type: "pub.polis.comment", ActorDomain: "discover.polis.pub"}
	result := EvaluateWithLog(policies, evt, ctx)
	if result.Decision != Review {
		t.Errorf("decision for discover.polis.pub comment = %v, want Review (the v1 bug would produce Deny)", result.Decision)
	}
	if !result.Matched {
		t.Error("review decision should be explicitly matched, not fall-through")
	}
	if result.Rule != "review pub.polis.comment from all" {
		t.Errorf("matched rule = %q, want explicit review terminal", result.Rule)
	}

	// Followed domain still gets blessed.
	evt.ActorDomain = "friend.com"
	if got := Evaluate(policies, evt, ctx); got != Bless {
		t.Errorf("followed domain = %v, want Bless", got)
	}

	// Self still gets blessed.
	evt.ActorDomain = "mysite.com"
	if got := Evaluate(policies, evt, ctx); got != Bless {
		t.Errorf("self = %v, want Bless", got)
	}
}

func TestDefaultPolicies_VersionHeader(t *testing.T) {
	// Both default policy contents should start with a version header
	for name, content := range map[string]string{
		"public":  DefaultPublicPolicyContent(),
		"private": DefaultPrivatePolicyContent(),
	} {
		lines := strings.Split(strings.TrimSpace(content), "\n")
		if len(lines) < 1 {
			t.Fatalf("%s: expected at least 1 line (header)", name)
		}
		if !strings.Contains(lines[0], `"version"`) {
			t.Errorf("%s: first line should be version header, got: %s", name, lines[0])
		}
		if !strings.Contains(lines[0], `"generator"`) {
			t.Errorf("%s: first line should contain generator, got: %s", name, lines[0])
		}
	}
}
