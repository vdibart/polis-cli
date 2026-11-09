package tailor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
)

func writeSitePolicies(t *testing.T, dir string, pub, priv []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "policies", "rules.jsonl"), pub, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"), priv, 0600); err != nil {
		t.Fatal(err)
	}
}

func readSitePolicies(t *testing.T, dir string) (pub, priv []byte) {
	t.Helper()
	pub, _ = os.ReadFile(filepath.Join(dir, "policies", "rules.jsonl"))
	priv, _ = os.ReadFile(filepath.Join(dir, ".polis", "policies", "rules.jsonl"))
	return pub, priv
}

func findCheck(t *testing.T, r *Result, name string) CheckResult {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s check in the result", name)
	return CheckResult{}
}

func flagged(c CheckResult) map[string]string {
	out := map[string]string{}
	for _, a := range c.Actions {
		if a.Op == "flag" {
			out[a.Path] = a.Detail
		}
	}
	return out
}

// `tailor --apply` leaves a policy customised with the recipes in
// docs/cli/user/policies.md byte for byte, and says it did.
func TestApply_LeavesAuthoredPolicyByteIdenticalAndReportsIt(t *testing.T) {
	dir := createCurrentSite(t)
	// "Block a domain" (private) and "Deny all comments outright" (public).
	priv := []byte(policy.DefaultPrivatePolicyContent() +
		`{"active":true,"policy":"deny all from all at spammer.example"}` + "\n")
	pub := []byte(strings.Replace(policy.DefaultPublicPolicyContent(),
		`{"active":true,"policy":"review pub.polis.comment from all"}`,
		`{"active":true,"policy":"deny pub.polis.comment from all"}`+"\n"+
			`{"active":true,"policy":"review pub.polis.comment from all"}`, 1))
	writeSitePolicies(t, dir, pub, priv)

	for run := 1; run <= 2; run++ {
		r := Apply(dir)
		gotPub, gotPriv := readSitePolicies(t, dir)
		if string(gotPub) != string(pub) {
			t.Errorf("run %d: public policy rewritten:\n%s", run, gotPub)
		}
		if string(gotPriv) != string(priv) {
			t.Errorf("run %d: private policy rewritten:\n%s", run, gotPriv)
		}
		c := findCheck(t, r, "policy-content-converge")
		if c.Status != StatusPass {
			t.Errorf("run %d: a customised policy is not a fault; got %s: %s", run, c.Status, c.Message)
		}
		f := flagged(c)
		if _, ok := f["policies/rules.jsonl"]; !ok {
			t.Errorf("run %d: public file not reported: %+v", run, c)
		}
		if _, ok := f[".polis/policies/rules.jsonl"]; !ok {
			t.Errorf("run %d: private file not reported: %+v", run, c)
		}
		if !strings.Contains(c.Message, "own rules") {
			t.Errorf("run %d: message does not say the files were left as the site's own: %q", run, c.Message)
		}
	}
}

// A policy file that does not parse is reported and left alone; the check
// fails, because that one does need a person.
func TestApply_ReportsUnparseablePolicyAndLeavesIt(t *testing.T) {
	dir := createCurrentSite(t)
	pub := []byte(policy.DefaultPublicPolicyContent() + `{"active":true,"policy":"deny all from`)
	writeSitePolicies(t, dir, pub, []byte(policy.DefaultPrivatePolicyContent()))

	r := Apply(dir)
	gotPub, _ := readSitePolicies(t, dir)
	if string(gotPub) != string(pub) {
		t.Errorf("unparseable public policy overwritten:\n%s", gotPub)
	}
	c := findCheck(t, r, "policy-content-converge")
	if c.Status != StatusFail {
		t.Errorf("expected fail for an unparseable file, got %s: %s", c.Status, c.Message)
	}
	if _, ok := flagged(c)["policies/rules.jsonl"]; !ok {
		t.Errorf("unparseable file not reported: %+v", c)
	}
}

// A v1 default is upgraded to v2.
func TestPolicyContentConverge_UpgradesV1Default(t *testing.T) {
	dir := createCurrentSite(t)
	writeSitePolicies(t, dir,
		[]byte(`{"version":1,"generator":"polis-cli-go/0.61.0"}`+"\n"+
			`{"active":true,"policy":"allow pub.polis.dm from following"}`+"\n"+
			`{"active":true,"policy":"deny pub.polis.dm from all"}`+"\n"+
			`{"active":true,"policy":"emit pub.polis.comment.blessing from self"}`+"\n"+
			`{"active":true,"policy":"emit pub.polis.comment.blessing from following"}`+"\n"+
			`{"active":true,"policy":"emit pub.polis.comment.blessing from thread-blessed"}`+"\n"+
			`{"active":true,"policy":"deny all from all"}`+"\n"),
		[]byte(`{"version":1,"generator":"polis-cli-go/0.61.0"}`+"\n"))

	c := checkPolicyContentConverge(&runContext{siteDir: dir})
	if c.Status != StatusFail || !strings.Contains(c.Message, "Converged") {
		t.Errorf("expected an applied convergence, got %s: %s", c.Status, c.Message)
	}
	gotPub, gotPriv := readSitePolicies(t, dir)
	if string(gotPub) != policy.DefaultPublicPolicyContent() || string(gotPriv) != policy.DefaultPrivatePolicyContent() {
		t.Errorf("v1 defaults not upgraded:\npublic:\n%s\nprivate:\n%s", gotPub, gotPriv)
	}
	if policy.HeaderVersion(gotPub) != 2 || policy.HeaderVersion(gotPriv) != 2 {
		t.Error("upgraded files should carry a v2 header")
	}
}

// A legacy site whose defaults carry no DM allow rule (0.56's public file and
// 0.61's header-only private file) is converged and gets the rule back.
func TestPolicyContentConverge_LegacyDefaultsGetTheDMAllowRule(t *testing.T) {
	dir := createCurrentSite(t)
	writeSitePolicies(t, dir,
		[]byte(`{"active":true,"policy":"allow pub.polis.comment.blessing from following"}`+"\n"),
		[]byte(`{"version":1,"generator":"polis-cli-go/0.61.0"}`+"\n"))

	c := checkPolicyContentConverge(&runContext{siteDir: dir})
	if c.Status != StatusFail || len(flagged(c)) != 0 {
		t.Errorf("expected a convergence with nothing flagged, got %s: %+v", c.Status, c)
	}
	gotPub, _ := readSitePolicies(t, dir)
	if !strings.Contains(string(gotPub), `"allow pub.polis.dm from following"`) {
		t.Errorf("DM allow rule not restored:\n%s", gotPub)
	}
}

// One past default beside one authored file: the default is upgraded, the
// authored file is left alone, and the result says both.
func TestPolicyContentConverge_MixedPastDefaultAndAuthored(t *testing.T) {
	dir := createCurrentSite(t)
	priv := []byte(policy.DefaultPrivatePolicyContent() +
		`{"active":true,"policy":"deny all from all at spammer.example"}` + "\n")
	writeSitePolicies(t, dir,
		[]byte(`{"active":true,"policy":"allow pub.polis.comment.blessing from following"}`+"\n"), priv)

	c := checkPolicyContentConverge(&runContext{siteDir: dir})
	gotPub, gotPriv := readSitePolicies(t, dir)
	if string(gotPub) != policy.DefaultPublicPolicyContent() {
		t.Errorf("public past default not converged:\n%s", gotPub)
	}
	if string(gotPriv) != string(priv) {
		t.Errorf("authored private file rewritten:\n%s", gotPriv)
	}
	if _, ok := flagged(c)[".polis/policies/rules.jsonl"]; !ok {
		t.Errorf("authored private file not reported: %+v", c)
	}
}
