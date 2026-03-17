package policycheck

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
)

func TestScanRequired_FollowingSource(t *testing.T) {
	policies := []policy.Policy{
		{Active: true, Rule: "allow pub.polis.dm from following"},
		{Active: true, Rule: "deny pub.polis.dm from all"},
	}
	ctx := ScanRequired(policies, "pub.polis.dm")
	if !ctx.NeedsFollowing {
		t.Error("expected NeedsFollowing=true")
	}
	if ctx.NeedsFollowers {
		t.Error("expected NeedsFollowers=false")
	}
}

func TestScanRequired_AllSource(t *testing.T) {
	policies := []policy.Policy{
		{Active: true, Rule: "deny pub.polis.dm from all"},
	}
	ctx := ScanRequired(policies, "pub.polis.dm")
	if ctx.NeedsFollowing {
		t.Error("expected NeedsFollowing=false")
	}
}

func TestScanRequired_NoMatchingRules(t *testing.T) {
	policies := []policy.Policy{
		{Active: true, Rule: "allow pub.polis.post from all"},
	}
	ctx := ScanRequired(policies, "pub.polis.dm")
	if ctx.NeedsFollowing {
		t.Error("expected NeedsFollowing=false")
	}
}

func TestScanRequired_FollowersSource(t *testing.T) {
	policies := []policy.Policy{
		{Active: true, Rule: "allow pub.polis.dm from followers"},
	}
	ctx := ScanRequired(policies, "pub.polis.dm")
	if !ctx.NeedsFollowers {
		t.Error("expected NeedsFollowers=true")
	}
}

func TestScanRequired_InactiveRulesIgnored(t *testing.T) {
	policies := []policy.Policy{
		{Active: false, Rule: "allow pub.polis.dm from following"},
	}
	ctx := ScanRequired(policies, "pub.polis.dm")
	if ctx.NeedsFollowing {
		t.Error("expected NeedsFollowing=false for inactive rule")
	}
}

// mockSite creates a test server that serves policies and following.json.
// Pass nil for policies to return 404 on policies endpoint.
// Pass nil for followingDomains to return 404 on following endpoint.
func mockSite(t *testing.T, policies []policy.Policy, followingDomains []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policies/rules.jsonl":
			if policies == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/jsonl")
			for _, p := range policies {
				data, _ := json.Marshal(p)
				fmt.Fprintf(w, "%s\n", data)
			}
		case "/content/pub.polis.core/follow/following.json":
			if followingDomains == nil {
				http.NotFound(w, r)
				return
			}
			entries := make([]map[string]string, len(followingDomains))
			for i, d := range followingDomains {
				entries[i] = map[string]string{
					"url":      "https://" + d,
					"added_at": "2026-01-01T00:00:00Z",
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"version":   "1.0",
				"following": entries,
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func newClient(ts *httptest.Server) *remote.Client {
	c := remote.NewClient()
	c.HTTPClient = ts.Client()
	return c
}

// ── Phase 1: Public policy evaluation ────────────────────────────────

func TestCheckDM_PublicPolicy_AllowFromFollowing_WeAreFollowed(t *testing.T) {
	policies := []policy.Policy{
		{Active: true, Rule: "allow pub.polis.dm from following"},
		{Active: true, Rule: "deny pub.polis.dm from all"},
	}
	ts := mockSite(t, policies, []string{"alice.com", "mysite.com"})
	defer ts.Close()

	result := CheckDMEligibilityURL(newClient(ts), ts.URL, "mysite.com")
	if result.Status != "open" {
		t.Errorf("expected open, got %s (reason: %s)", result.Status, result.Reason)
	}
	if !result.PolicyDecided {
		t.Error("expected PolicyDecided=true")
	}
	if !result.FollowsUs {
		t.Error("expected FollowsUs=true")
	}
}

func TestCheckDM_PublicPolicy_AllowFromFollowing_NotFollowed(t *testing.T) {
	policies := []policy.Policy{
		{Active: true, Rule: "allow pub.polis.dm from following"},
		{Active: true, Rule: "deny pub.polis.dm from all"},
	}
	ts := mockSite(t, policies, []string{"alice.com", "bob.com"})
	defer ts.Close()

	result := CheckDMEligibilityURL(newClient(ts), ts.URL, "mysite.com")
	if result.Status != "no-dm" {
		t.Errorf("expected no-dm, got %s", result.Status)
	}
}

func TestCheckDM_PublicPolicy_DenyFromAll(t *testing.T) {
	policies := []policy.Policy{
		{Active: true, Rule: "deny pub.polis.dm from all"},
	}
	// No following needed for deny-from-all
	ts := mockSite(t, policies, nil)
	defer ts.Close()

	result := CheckDMEligibilityURL(newClient(ts), ts.URL, "mysite.com")
	if result.Status != "no-dm" {
		t.Errorf("expected no-dm, got %s", result.Status)
	}
	if result.FetchedFollowing != nil {
		t.Error("expected no following fetch for deny-from-all")
	}
}

func TestCheckDM_PublicPolicy_AllowFromAll(t *testing.T) {
	policies := []policy.Policy{
		{Active: true, Rule: "allow pub.polis.dm from all"},
	}
	ts := mockSite(t, policies, nil)
	defer ts.Close()

	result := CheckDMEligibilityURL(newClient(ts), ts.URL, "mysite.com")
	if result.Status != "open" {
		t.Errorf("expected open, got %s", result.Status)
	}
	if !result.PolicyDecided {
		t.Error("expected PolicyDecided=true")
	}
}

// ── Phase 2: Mutual-follow fallback (no public DM rules) ────────────

func TestCheckDM_NoPolicies_TheyFollowUs(t *testing.T) {
	// 404 on policies → no DM rules → fall back to following check
	ts := mockSite(t, nil, []string{"alice.com", "mysite.com"})
	defer ts.Close()

	result := CheckDMEligibilityURL(newClient(ts), ts.URL, "mysite.com")
	if result.Status != "open" {
		t.Errorf("expected open, got %s (reason: %s)", result.Status, result.Reason)
	}
	if result.PolicyDecided {
		t.Error("expected PolicyDecided=false")
	}
	if !result.FollowsUs {
		t.Error("expected FollowsUs=true")
	}
}

func TestCheckDM_NoPolicies_TheyDontFollowUs(t *testing.T) {
	ts := mockSite(t, nil, []string{"alice.com", "bob.com"})
	defer ts.Close()

	result := CheckDMEligibilityURL(newClient(ts), ts.URL, "mysite.com")
	if result.Status != "no-follow" {
		t.Errorf("expected no-follow, got %s", result.Status)
	}
	if result.Reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestCheckDM_NoPolicies_EmptyPolicies_TheyFollowUs(t *testing.T) {
	// Empty policies file (no DM rules, but file exists with only non-DM rules)
	policies := []policy.Policy{
		{Active: true, Rule: "allow pub.polis.post from all"},
	}
	ts := mockSite(t, policies, []string{"mysite.com"})
	defer ts.Close()

	result := CheckDMEligibilityURL(newClient(ts), ts.URL, "mysite.com")
	if result.Status != "open" {
		t.Errorf("expected open, got %s (reason: %s)", result.Status, result.Reason)
	}
	if !result.FollowsUs {
		t.Error("expected FollowsUs=true")
	}
}

// ── Error cases ──────────────────────────────────────────────────────

func TestCheckDM_FetchError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	result := CheckDMEligibilityURL(newClient(ts), ts.URL, "mysite.com")
	if result.Status != "unknown" {
		t.Errorf("expected unknown, got %s", result.Status)
	}
}

func TestCheckDM_NoPolicies_FollowingFetchFails(t *testing.T) {
	// Policies 404, following 500
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/policies/rules.jsonl":
			http.NotFound(w, r)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer ts.Close()

	result := CheckDMEligibilityURL(newClient(ts), ts.URL, "mysite.com")
	if result.Status != "unknown" {
		t.Errorf("expected unknown, got %s", result.Status)
	}
}
