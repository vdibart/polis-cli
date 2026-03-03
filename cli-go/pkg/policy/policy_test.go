package policy

import (
	"os"
	"path/filepath"
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
		{"allow pub.polis.comment from following", "allow", "pub.polis.comment", "following"},
		{"deny pub.polis.comment.blessing from all", "deny", "pub.polis.comment.blessing", "all"},
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
	p, err = Parse("allow pub.polis.comment from following at friend.com on posts/hello.md")
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

// ============================================================================
// Evaluation tests
// ============================================================================

func TestEvaluate_FirstMatchWins(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "allow pub.polis.comment from following"},
		{Active: true, Rule: "deny pub.polis.comment from all"},
	}

	ctx := EvalContext{
		FollowingDomains: map[string]bool{"alice.com": true},
	}

	evt := Event{Type: "pub.polis.comment.published", ActorDomain: "alice.com"}

	// alice.com is in following -> first rule matches -> allow
	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("Evaluate = %v, want Allow (first match wins)", got)
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
		Type:       "pub.polis.comment.published",
		ActorDomain: "alice.com",
		TargetPath: "posts/2026/01/draft.md",
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

// ============================================================================
// EvaluateExplicit tests
// ============================================================================

func TestEvaluateExplicit_ExplicitAllow(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "allow pub.polis.comment.blessing from following"},
	}

	ctx := EvalContext{FollowingDomains: map[string]bool{"alice.com": true}}
	evt := Event{Type: "pub.polis.comment.blessing.requested", ActorDomain: "alice.com"}

	decision, explicit := EvaluateExplicit(policies, evt, ctx)
	if decision != Allow || !explicit {
		t.Errorf("EvaluateExplicit = (%v, %v), want (Allow, true)", decision, explicit)
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
		{Active: true, Rule: "allow pub.polis.comment.blessing from following"},
	}

	ctx := EvalContext{FollowingDomains: map[string]bool{"alice.com": true}}
	// stranger.com is not following -> no match
	evt := Event{Type: "pub.polis.comment.blessing.requested", ActorDomain: "stranger.com"}

	decision, explicit := EvaluateExplicit(policies, evt, ctx)
	if decision != Allow || explicit {
		t.Errorf("EvaluateExplicit = (%v, %v), want (Allow, false)", decision, explicit)
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
		{Active: true, Rule: "allow pub.polis.comment from following"},
		{Active: true, Rule: "deny pub.polis.comment from all"},
	}

	ctx := EvalContext{
		FollowingDomains: map[string]bool{"friend.com": true},
	}

	// Followed author -> allow
	evt := Event{Type: "pub.polis.comment.published", ActorDomain: "friend.com"}
	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("friend.com comment should be allowed, got %v", got)
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

	// Private: allow comments from friend.com
	os.WriteFile(privPath, []byte(`{"active":true,"policy":"allow pub.polis.comment from all at friend.com"}`+"\n"), 0644)
	// Public: deny all comments
	os.WriteFile(pubPath, []byte(`{"active":true,"policy":"deny pub.polis.comment from all"}`+"\n"), 0644)

	policies, err := LoadPolicies(privPath, pubPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := EvalContext{}

	// friend.com matches private allow first
	evt := Event{Type: "pub.polis.comment.published", ActorDomain: "friend.com"}
	if got := Evaluate(policies, evt, ctx); got != Allow {
		t.Errorf("friend.com should be allowed (private override), got %v", got)
	}

	// stranger.com doesn't match private (wrong domain), falls to public deny
	evt.ActorDomain = "stranger.com"
	if got := Evaluate(policies, evt, ctx); got != Deny {
		t.Errorf("stranger.com should be denied (public rule), got %v", got)
	}
}

func TestSpec_BlessingAutoDecision(t *testing.T) {
	policies := []Policy{
		{Active: true, Rule: "allow pub.polis.comment.blessing from following"},
		{Active: true, Rule: "deny pub.polis.comment.blessing from all at spam.com"},
	}

	ctx := EvalContext{
		FollowingDomains: map[string]bool{"alice.com": true},
	}

	// Followed author -> explicit allow (auto-grant)
	evt := Event{Type: "pub.polis.comment.blessing.requested", ActorDomain: "alice.com"}
	decision, explicit := EvaluateExplicit(policies, evt, ctx)
	if decision != Allow || !explicit {
		t.Errorf("alice.com blessing = (%v, %v), want (Allow, true)", decision, explicit)
	}

	// spam.com -> explicit deny (auto-deny)
	evt.ActorDomain = "spam.com"
	decision, explicit = EvaluateExplicit(policies, evt, ctx)
	if decision != Deny || !explicit {
		t.Errorf("spam.com blessing = (%v, %v), want (Deny, true)", decision, explicit)
	}

	// stranger.com -> no match (manual review)
	evt.ActorDomain = "stranger.com"
	decision, explicit = EvaluateExplicit(policies, evt, ctx)
	if decision != Allow || explicit {
		t.Errorf("stranger.com blessing = (%v, %v), want (Allow, false)", decision, explicit)
	}
}

func TestDefaultPolicyContent(t *testing.T) {
	content := DefaultPolicyContent()
	if content == "" {
		t.Fatal("DefaultPolicyContent() returned empty string")
	}
	// Should be valid JSONL
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.jsonl")
	os.WriteFile(path, []byte(content), 0644)

	policies, err := LoadPolicies(path, "/nonexistent")
	if err != nil {
		t.Fatalf("failed to load default policy content: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
	if !policies[0].Active {
		t.Error("default policy should be active")
	}
	if policies[0].Rule != "allow pub.polis.comment.blessing from following" {
		t.Errorf("default policy rule = %q", policies[0].Rule)
	}
}
