package connection_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// writeMarked puts a post on the site carrying a marker and a signed time,
// as the publish path would (the check reads the frontmatter, not the
// signature, which other checks verify).
func writeMarked(t *testing.T, site storage.Site, name, agent, grant string, at time.Time) string {
	t.Helper()
	key := "content/pub.polis.core/post/20260101/" + name + ".md"
	body := "---\ntitle: " + name + "\npublished: " + at.UTC().Format(time.RFC3339) +
		"\nagent: " + agent + "\ngrant: " + grant + "\n---\n\n# " + name + "\n"
	if err := site.Store().Put(t.Context(), key, []byte(body)); err != nil {
		t.Fatal(err)
	}
	return key
}

func logWrite(t *testing.T, site storage.Site, grant string, at time.Time) {
	t.Helper()
	if err := connection.RecordAct(site, connection.Act{
		Time: at.UTC().Format(time.RFC3339), Agent: "x", Grant: grant,
		Method: "POST", Path: "/v1/content/pub.polis.post", Status: 201,
	}); err != nil {
		t.Fatal(err)
	}
}

func kinds(res sitecheck.AgentActsResult) string {
	var k []string
	for _, f := range res.Failures {
		k = append(k, f.Kind)
	}
	return strings.Join(k, " ")
}

// ⭐ A clean site reports nothing: a post an agent really published, through
// the publish path, with its act logged.
func TestAgentActsCleanSiteReportsNothing(t *testing.T) {
	cfg := newSite(t)
	if res := sitecheck.AgentActs(cfg.Site); res.Checked != 0 || len(res.Failures) != 0 {
		t.Fatalf("a site with no agent acts: %+v", res)
	}
	g, err := connection.Issue(cfg, "Claude Desktop", "Claude Desktop", []string{"publish"})
	if err != nil {
		t.Fatal(err)
	}
	live, _ := connection.Resolve(cfg.Site, g.URL)
	if _, err := publish.PublishPostAs(cfg.Site, "# Hello\n\nFrom an agent.", "", cfg.PrivateKey, ops.MarkerOf(live)); err != nil {
		t.Fatal(err)
	}
	logWrite(t, cfg.Site, g.URL, time.Now())
	if _, err := publish.PublishPost(cfg.Site, "# Mine", "", cfg.PrivateKey); err != nil {
		t.Fatal(err)
	}
	res := sitecheck.AgentActs(cfg.Site)
	if res.Checked != 1 || len(res.Failures) != 0 || len(res.Unchecked) != 0 {
		t.Fatalf("clean site: %+v", res)
	}
}

// ⛔ 1: the marker must cite a grant this site holds, naming the same agent.
func TestAgentActsGrantMustBeRealAndMatch(t *testing.T) {
	cfg := newSite(t)
	g, _ := connection.Issue(cfg, "Claude Desktop", "Claude Desktop", []string{"publish"})
	now := time.Now()
	writeMarked(t, cfg.Site, "forged", "Claude Desktop", base+"/content/pub.polis.core/attestation/nope.json", now)
	writeMarked(t, cfg.Site, "renamed", "Someone Else", g.URL, now)
	logWrite(t, cfg.Site, g.URL, now)
	if got := kinds(sitecheck.AgentActs(cfg.Site)); got != "grant grant" {
		t.Errorf("failures = %q, want two grant failures", got)
	}
}

// ⛔ 2: signed while the grant stood: not before it was made, not after it
// was withdrawn.
func TestAgentActsMustBeSignedWhileTheGrantWasLive(t *testing.T) {
	cfg := newSite(t)
	g, _ := connection.Issue(cfg, "Claude Desktop", "Claude Desktop", []string{"publish"})
	early := time.Now().Add(-time.Hour)
	writeMarked(t, cfg.Site, "before", "Claude Desktop", g.URL, early)
	logWrite(t, cfg.Site, g.URL, early)
	if got := kinds(sitecheck.AgentActs(cfg.Site)); got != "not_live" {
		t.Fatalf("an act before its grant: %q", got)
	}

	cfg2 := newSite(t)
	g2, _ := connection.Issue(cfg2, "Claude Desktop", "Claude Desktop", []string{"publish"})
	if err := connection.Withdraw(cfg2, g2.ID); err != nil {
		t.Fatal(err)
	}
	late := time.Now().Add(time.Hour)
	writeMarked(t, cfg2.Site, "after", "Claude Desktop", g2.URL, late)
	logWrite(t, cfg2.Site, g2.URL, late)
	if got := kinds(sitecheck.AgentActs(cfg2.Site)); got != "not_live" {
		t.Errorf("an act after its grant's withdrawal: %q", got)
	}
}

// ⛔ 3: within the grant's scopes.
func TestAgentActsMustBeWithinTheGrantsScopes(t *testing.T) {
	cfg := newSite(t)
	g, _ := connection.Issue(cfg, "Reader", "Reader", []string{"read"})
	now := time.Now()
	writeMarked(t, cfg.Site, "outside", "Reader", g.URL, now)
	logWrite(t, cfg.Site, g.URL, now)
	if got := kinds(sitecheck.AgentActs(cfg.Site)); got != "scope" {
		t.Errorf("a post under a read-only grant: %q", got)
	}
}

// ⛔ 4: a marked post has its write in the act log, for that grant, near its
// signed time.
func TestAgentActsMustBeInTheActLog(t *testing.T) {
	cfg := newSite(t)
	g, _ := connection.Issue(cfg, "Claude Desktop", "Claude Desktop", []string{"publish"})
	now := time.Now()
	writeMarked(t, cfg.Site, "unlogged", "Claude Desktop", g.URL, now)
	logWrite(t, cfg.Site, g.URL, now.Add(-3*time.Hour)) // a different, older write
	if got := kinds(sitecheck.AgentActs(cfg.Site)); got != "no_record" {
		t.Errorf("a marked post with no logged write: %q", got)
	}
}

// polis validate reports the same check, as its own family.
func TestValidateReportsAgentActs(t *testing.T) {
	cfg := newSite(t)
	g, _ := connection.Issue(cfg, "Reader", "Reader", []string{"read"})
	writeMarked(t, cfg.Site, "outside", "Reader", g.URL, time.Now())
	r := sitecheck.RunLocal(cfg.Site.Dir())
	for _, c := range r.Checks {
		if c.ID == "agents.acts" {
			if c.Family != sitecheck.FamilyAgents || c.Outcome != sitecheck.OutcomeFailed || len(c.Findings) == 0 {
				t.Fatalf("agents.acts = %+v", c)
			}
			return
		}
	}
	t.Fatal("polis validate did not report agents.acts")
}
