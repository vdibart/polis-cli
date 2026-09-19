package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

const testBase = "https://alice.polis.pub"

func newSite(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: testBase, SiteTitle: "alice"}); err != nil {
		t.Fatalf("site.Init: %v", err)
	}
	key, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	return Config{SiteDir: dir, BaseURL: testBase, PrivateKey: key}
}

func TestDefaultIssuesOnceAndAPassLaterFindsItStanding(t *testing.T) {
	cfg := newSite(t)
	outcome, w, err := IssueDefault(cfg)
	if err != nil || outcome != DefaultIssued {
		t.Fatalf("first pass = %s, %v", outcome, err)
	}
	g, _ := LiveRosieGrant(cfg.SiteDir)
	if !g.Valid() || g.Basis() != attestation.CustodyBasisHostingTerms || g.URL() != w.URL {
		t.Fatalf("live grant after default = %+v", g)
	}
	for i := 0; i < 2; i++ {
		if outcome, _, _ := IssueDefault(cfg); outcome != DefaultStanding {
			t.Fatalf("pass %d = %s, want %s", i+2, outcome, DefaultStanding)
		}
	}
	if gs, _ := attestation.Grants(cfg.SiteDir, Rosie); len(gs) != 1 {
		t.Fatalf("grants after three passes = %d, want 1", len(gs))
	}
}

func TestSwitchingRosieOffSurvivesTheDefaultPass(t *testing.T) {
	cfg := newSite(t)
	if _, _, err := IssueDefault(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := Disable(cfg); err != nil {
		t.Fatal(err)
	}
	// Every later boot runs the pass again. She stays off.
	for i := 0; i < 3; i++ {
		outcome, _, err := IssueDefault(cfg)
		if err != nil || outcome != DefaultWithdrawn {
			t.Fatalf("pass after switch-off = %s, %v; want %s", outcome, err, DefaultWithdrawn)
		}
	}
	if g, _ := LiveRosieGrant(cfg.SiteDir); g != nil {
		t.Fatal("Rosie is live after the user switched her off and the pass ran")
	}
}

func TestAUserWhoSwitchedBackOnReceivesNoFurtherDefault(t *testing.T) {
	cfg := newSite(t)
	_, _, _ = IssueDefault(cfg)
	_, _ = Disable(cfg)
	w, already, err := Enable(cfg)
	if err != nil || already || w.Basis != attestation.CustodyBasisUserSigned {
		t.Fatalf("Enable = %+v, %v, %v", w, already, err)
	}
	if outcome, _, _ := IssueDefault(cfg); outcome != DefaultWithdrawn {
		t.Fatalf("default after a withdrawal ever = %s, want %s", outcome, DefaultWithdrawn)
	}
	if g, _ := LiveRosieGrant(cfg.SiteDir); !g.Valid() || g.Basis() != attestation.CustodyBasisUserSigned {
		t.Fatal("the user's own switch-on is not the live grant")
	}
}

func TestEnableIsIdempotent(t *testing.T) {
	cfg := newSite(t)
	if _, already, err := Enable(cfg); err != nil || already {
		t.Fatal(err)
	}
	if _, already, err := Enable(cfg); err != nil || !already {
		t.Fatalf("second Enable: already=%v err=%v", already, err)
	}
}

func TestDisableWithdrawsEveryStandingRosieGrant(t *testing.T) {
	cfg := newSite(t)
	_, _, _ = IssueDefault(cfg)
	rec := &attestation.Record{
		Issuer: testBase, Predicate: attestation.PredicateGrant,
		Subject:  attestation.Subject{Type: attestation.SubjectIdentity, ID: testBase},
		Asserted: "2030-01-01T00:00:00Z",
		Payload: map[string]string{
			attestation.GrantKeyAgent: Rosie, attestation.GrantKeyProvider: RosieProvider,
			attestation.GrantKeyBehaviours: "rosie/2", attestation.CustodyKeyBasis: attestation.CustodyBasisHostingTerms,
		},
	}
	if _, err := attestation.Issue(cfg.SiteDir, rec, cfg.PrivateKey); err != nil {
		t.Fatal(err)
	}
	written, err := Disable(cfg)
	if err != nil || len(written) != 2 {
		t.Fatalf("Disable withdrew %d, err %v; want 2", len(written), err)
	}
	gs, _ := attestation.Grants(cfg.SiteDir, Rosie)
	for _, g := range gs {
		if !g.Withdrawn {
			t.Errorf("%s/%d still standing", g.Set, g.Version)
		}
	}
}

func readProjection(t *testing.T, dir string) Projection {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, MountDir(dir), AgentsFilename))
	if err != nil {
		t.Fatalf("agents.json: %v", err)
	}
	var p Projection
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProjectionReflectsEveryGrantAndWithdrawal(t *testing.T) {
	cfg := newSite(t)
	_, w, _ := IssueDefault(cfg)

	if got, want := site.AgentsPointer(cfg.SiteDir), "/attestations/agents.json"; got != want {
		t.Fatalf("agents pointer = %q, want %q", got, want)
	}
	p := readProjection(t, cfg.SiteDir)
	if len(p.Agents) != 1 || p.Agents[0].State != "live" || p.Agents[0].Grant != w.URL {
		t.Fatalf("projection after default = %+v", p.Agents)
	}
	if _, err := os.Stat(filepath.Join(cfg.SiteDir, "attestations", AgentsPageDir, "index.html")); err != nil {
		t.Fatalf("page: %v", err)
	}

	_, _ = Disable(cfg)
	p = readProjection(t, cfg.SiteDir)
	if p.Agents[0].State != "withdrawn" || p.Agents[0].WithdrawnAt == "" {
		t.Fatalf("projection after switch-off = %+v", p.Agents[0])
	}

	on, _, _ := Enable(cfg)
	p = readProjection(t, cfg.SiteDir)
	// Two records asserted in the same second sort by content hash, so find
	// each by its URL rather than by position.
	states := map[string]string{}
	for _, it := range p.Agents {
		states[it.Grant] = it.State
	}
	if len(p.Agents) != 2 || states[w.URL] != "withdrawn" || states[on.URL] != "live" {
		t.Fatalf("projection after switch-on = %+v", p.Agents)
	}
}

// ⛔ A signature cites the SOURCE: the grant record under content/, never the
// projection under the mount.
func TestTheMarkerNeverCitesTheProjection(t *testing.T) {
	cfg := newSite(t)
	_, _, _ = IssueDefault(cfg)
	g, _ := LiveRosieGrant(cfg.SiteDir)
	if !strings.Contains(g.URL(), "/content/pub.polis.core/attestation/") || strings.Contains(g.URL(), AgentsFilename) {
		t.Fatalf("live grant URL %q is not the record's source URL", g.URL())
	}
}

// The README's design rule: every branch refreshes the projection, the failure
// branch included. A stale file still saying "live" after a failed write is
// worse than none.
func TestProjectionIsRefreshedAfterAFailedWrite(t *testing.T) {
	cfg := newSite(t)
	_, _, _ = IssueDefault(cfg)
	_, _ = Disable(cfg)

	// A stale projection claiming she is live.
	stale := Projection{Type: ProjectionType, Agents: []ProjectionItem{{Agent: Rosie, State: "live"}}}
	data, _ := json.Marshal(stale)
	path := filepath.Join(cfg.SiteDir, MountDir(cfg.SiteDir), AgentsFilename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	broken := cfg
	broken.BaseURL = ""
	if _, _, err := Enable(broken); err == nil {
		t.Fatal("Enable with no site address succeeded")
	}
	p := readProjection(t, cfg.SiteDir)
	if len(p.Agents) != 1 || p.Agents[0].State != "withdrawn" {
		t.Fatalf("projection after a failed switch-on = %+v, want the truth (withdrawn)", p.Agents)
	}
}

func TestASiteWithNoGrantPublishesNothing(t *testing.T) {
	cfg := newSite(t)
	if err := RefreshProjections(cfg.SiteDir); err != nil {
		t.Fatal(err)
	}
	if site.AgentsPointer(cfg.SiteDir) != "" {
		t.Error("a site with no grant has an agents pointer")
	}
	if _, err := os.Stat(filepath.Join(cfg.SiteDir, MountDir(cfg.SiteDir), AgentsFilename)); !os.IsNotExist(err) {
		t.Error("a site with no grant has an agents.json")
	}
}

func TestThePointerIsNotRewrittenWhenUnchanged(t *testing.T) {
	cfg := newSite(t)
	_, _, _ = IssueDefault(cfg)
	wk := filepath.Join(cfg.SiteDir, ".well-known", "polis")
	before, _ := os.Stat(wk)
	beforeBytes, _ := os.ReadFile(wk)
	if err := RefreshProjections(cfg.SiteDir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(wk)
	afterBytes, _ := os.ReadFile(wk)
	if !after.ModTime().Equal(before.ModTime()) || string(afterBytes) != string(beforeBytes) {
		t.Fatal(".well-known/polis was rewritten by a refresh that changed nothing")
	}
}

// Review F1: a key rotation must not silently stop Rosie, and must not stop the
// user switching her off.
func TestAKeyRotationKeepsRosieLiveAndSwitchOffStillWorks(t *testing.T) {
	cfg := newSite(t)
	if _, _, err := IssueDefault(cfg); err != nil {
		t.Fatal(err)
	}

	oldPriv := cfg.PrivateKey
	oldPub, _ := os.ReadFile(filepath.Join(cfg.SiteDir, ".polis", "keys", "id_ed25519.pub"))
	newPriv, newPubRaw, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	newPub := strings.TrimSpace(string(newPubRaw))
	validFrom := time.Now().UTC().Add(2 * time.Minute).Format("2006-01-02T15:04:05Z")
	canonical, err := discovery.MakeKeyRotationCanonicalJSON("alice.polis.pub", strings.TrimSpace(string(oldPub)), newPub, validFrom)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, oldPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := site.RecordKeyRotation(cfg.SiteDir, newPub, sig, validFrom); err != nil {
		t.Fatal(err)
	}
	cfg.PrivateKey = newPriv

	if g, _ := LiveRosieGrant(cfg.SiteDir); !g.Valid() {
		t.Fatal("Rosie stopped after a key rotation: her grant no longer resolves")
	}
	if outcome, _, _ := IssueDefault(cfg); outcome != DefaultStanding {
		t.Fatalf("default pass after rotation = %s, want %s", outcome, DefaultStanding)
	}
	written, err := Disable(cfg)
	if err != nil || len(written) != 1 {
		t.Fatalf("switching Rosie off after a rotation: withdrew %d, err %v", len(written), err)
	}
	if g, _ := LiveRosieGrant(cfg.SiteDir); g != nil {
		t.Fatal("Rosie still live after switch-off")
	}
	if outcome, _, _ := IssueDefault(cfg); outcome != DefaultWithdrawn {
		t.Fatalf("default pass after rotation + switch-off = %s", outcome)
	}
}

func TestRosieTextAvoidsTheInstrumentWords(t *testing.T) {
	all := strings.ToLower(strings.Join([]string{RosieText.Title, RosieText.Does, RosieText.IfOff,
		RosieText.Anytime, RosieText.CatchUp, RosieText.Traced, RosieText.NotStarted}, " "))
	for _, word := range []string{"grant", "disclosure", "attribution"} {
		if strings.Contains(all, word) {
			t.Errorf("the friendly text uses %q", word)
		}
	}
	plain := RosieText.Plain()
	for _, part := range []string{RosieText.Does, RosieText.IfOff, RosieText.Anytime} {
		if !strings.Contains(plain, part) {
			t.Errorf("Plain() omits %q", part)
		}
	}
}

func TestCatchUpRunsOncePerGrant(t *testing.T) {
	cfg := newSite(t)
	if NeedsCatchUp(cfg.SiteDir) {
		t.Fatal("a site with no grant needs a catch-up")
	}
	_, w, _ := IssueDefault(cfg)
	if !NeedsCatchUp(cfg.SiteDir) {
		t.Fatal("a newly live grant needs no catch-up")
	}
	if err := RecordCatchUp(cfg.SiteDir, w.URL); err != nil {
		t.Fatal(err)
	}
	if NeedsCatchUp(cfg.SiteDir) {
		t.Fatal("catch-up recorded, still needed")
	}
	_, _ = Disable(cfg)
	_, _, _ = Enable(cfg)
	if !NeedsCatchUp(cfg.SiteDir) {
		t.Fatal("switching her back on needs a fresh catch-up")
	}
}
