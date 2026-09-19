package sitecheck

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ⚠️ The Cloudflare block as it is actually served on vdibart.polis.pub, cut to
// the part that matters. It is a fixture of a REAL intermediary, not a model of
// one: it prepends a second `User-agent: *` group (which MERGES with polis's)
// and a run of named groups (which SHADOW it).
const restrictiveInjection = `# BEGIN Cloudflare Managed content

User-agent: *
Content-Signal: search=yes,ai-train=no,use=reference
Allow: /

User-agent: CCBot
Disallow: /

User-agent: ClaudeBot
Disallow: /

User-agent: GPTBot
Disallow: /

# END Cloudflare Managed Content

`

// licensedSite serves a site whose signed licence states one profile's terms,
// plus whatever robots.txt the caller wants the world to see.
//
// ⭐ The server starts BEFORE the licence is built, because the terms name their
// own origin and the signature covers them — a licence signed against a
// placeholder URL would not be the thing the check reads.
func licensedSite(t *testing.T, profile string) (*remoteSite, *httptest.Server, *license.Terms) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	site := &remoteSite{files: map[string]string{}}
	ts := site.serve(t)

	f, err := license.New(profile, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "license.json")
	if err := license.SignAndWrite(f, path, priv); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	const pointer = "content/pub.polis.core/license/license.json"
	site.mu.Lock()
	site.files["/.well-known/polis"] = string(mustJSON(map[string]interface{}{
		"version": "2.0", "public_key": string(pub), "license": pointer,
	}))
	site.files["/"+pointer] = string(data)
	site.mu.Unlock()

	rsl, err := license.RSL(f.Terms, ts.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	site.set("/rsl.xml", rsl)
	site.set("/robots.txt", polisSection(f.Terms, ts.URL))
	return site, ts, f.Terms
}

func (s *remoteSite) set(path, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = body
}

// polisSection is what the site itself generates — the same function the
// renderer and Medic use, so a fixture can never be a poorer generation than
// the real one.
func polisSection(t *license.Terms, base string) string {
	return license.Robots(license.RobotsOptions{
		SiteTerms:  t,
		RSLURL:     base + "/rsl.xml",
		SitemapURL: base + "/sitemap.xml",
	})
}

func robotsCheck(t *testing.T, ts *httptest.Server) Check {
	t.Helper()
	return checkByID(t, NewRemote().RunSite(ts.URL), "content.license_robots")
}

func findingsText(c Check) string { return strings.Join(c.Findings, "\n") }

// ⭐ THE LIVE CASE. An intermediary tightens the terms of a site that already
// reserved them. It is aligned, so it is a NOTE — recorded in full, and
// explicitly not a warning. Getting this row wrong turns the real fleet red.
func TestPublicRobots_RestrictiveInjectionAgainstReservedTermsIsANoteNotAWarning(t *testing.T) {
	site, ts, terms := licensedSite(t, license.ProfileReserved)
	site.set("/robots.txt", restrictiveInjection+polisSection(terms, ts.URL))

	c := robotsCheck(t, ts)
	if c.Outcome != OutcomePassed {
		t.Fatalf("outcome = %s, want %s — an edge that tightens a reserved site's terms is aligned with them: %s", c.Outcome, OutcomePassed, findingsText(c))
	}
	if len(c.Findings) == 0 {
		t.Fatal("a note must still SAY what was injected; a silent pass is the gap this epic exists to close")
	}
	if !strings.Contains(c.Detail, "NOT a problem to fix") {
		t.Errorf("detail must tell the reader this needs no action: %q", c.Detail)
	}
	for _, want := range []string{"CCBot", "ClaudeBot", "GPTBot", "RFC 9309", "aligned"} {
		if !strings.Contains(findingsText(c), want) {
			t.Errorf("findings do not mention %q:\n%s", want, findingsText(c))
		}
	}
}

// ⭐ THE TEST THAT PROVES THE CHECK READS THE LICENCE AND NOT THE FILE.
// Byte-for-byte the SAME served robots.txt as the test above, against a site
// whose author granted what the edge is refusing. Same file, opposite verdict.
func TestPublicRobots_TheSameInjectionAgainstOpenTermsIsAWarning(t *testing.T) {
	site, ts, terms := licensedSite(t, license.ProfileOpen)
	site.set("/robots.txt", restrictiveInjection+polisSection(terms, ts.URL))

	c := robotsCheck(t, ts)
	if c.Outcome != OutcomeWarning {
		t.Fatalf("outcome = %s, want %s — the edge is refusing what this author granted", c.Outcome, OutcomeWarning)
	}
	if !strings.Contains(findingsText(c), "CONTRADICTS THE AUTHOR'S GRANT") {
		t.Errorf("the finding must name which half of the asymmetry this is:\n%s", findingsText(c))
	}
}

// ⛔ CONTAINMENT PROVES NOTHING. polis's section is in the served file, byte for
// byte, in both tests above — so a substring test would have called both clean.
func TestPublicRobots_ContainmentIsNotTheTest(t *testing.T) {
	site, ts, terms := licensedSite(t, license.ProfileOpen)
	served := restrictiveInjection + polisSection(terms, ts.URL)
	site.set("/robots.txt", served)

	if !strings.Contains(served, polisSection(terms, ts.URL)) {
		t.Fatal("fixture is wrong: polis's section must be present verbatim for this test to mean anything")
	}
	if got := robotsCheck(t, ts).Outcome; got != OutcomeWarning {
		t.Fatalf("outcome = %s: a file that CONTAINS our section intact still governs named agents by someone else's group", got)
	}
}

// The attack: an intermediary granting, for one agent, what the author refused.
func TestPublicRobots_ThirdPartyGrantingWhatTheTermsRefuseIsAWarning(t *testing.T) {
	site, ts, terms := licensedSite(t, license.ProfileReserved)
	site.set("/robots.txt", "# BEGIN Acme Turbo Crawl Boost\n\nUser-agent: GPTBot\nAllow: /\nContent-Usage: train-ai=y\n\n"+polisSection(terms, ts.URL))

	c := robotsCheck(t, ts)
	if c.Outcome != OutcomeWarning {
		t.Fatalf("outcome = %s, want %s", c.Outcome, OutcomeWarning)
	}
	f := findingsText(c)
	if !strings.Contains(f, "GRANTS WHAT THE AUTHOR REFUSED") || !strings.Contains(f, "train-ai=y") {
		t.Errorf("findings must name the grant and its direction:\n%s", f)
	}
	if !strings.Contains(f, "Acme Turbo Crawl Boost") {
		t.Errorf("an injector that labels its block is attributable; the label was not reported:\n%s", f)
	}
}

// ⚠️ SILENCE IS ALSO A GRANT. A group that governs an agent, allows it to crawl
// and carries no content-usage rule has erased the author's refusal for that
// agent — under AIPREF an absent preference is "unknown", which is latitude.
func TestPublicRobots_ShadowingGroupThatAllowsAndSaysNothingErasesTheRefusal(t *testing.T) {
	site, ts, terms := licensedSite(t, license.ProfileReserved)
	site.set("/robots.txt", "User-agent: GPTBot\nAllow: /\n\n"+polisSection(terms, ts.URL))

	c := robotsCheck(t, ts)
	if c.Outcome != OutcomeWarning {
		t.Fatalf("outcome = %s, want %s — the refusal never reaches GPTBot", c.Outcome, OutcomeWarning)
	}
	if !strings.Contains(findingsText(c), "no train-ai rule") {
		t.Errorf("the finding must say the refusal did not reach the agent:\n%s", findingsText(c))
	}
	if !strings.Contains(findingsText(c), "origin undeterminable") {
		t.Errorf("an unlabelled block must be reported as unattributable, not silently attributed:\n%s", findingsText(c))
	}
}

// ⭐ ACCESS DOMINATES — the rule that keeps the live case from being a warning.
// The same agent, same missing content-usage rule, but forbidden to crawl at
// all: it cannot extract what it cannot fetch, so nothing was granted.
func TestPublicRobots_ADeniedAgentIsNeverAGrantEvenWithNoPreference(t *testing.T) {
	site, ts, terms := licensedSite(t, license.ProfileReserved)
	site.set("/robots.txt", "User-agent: GPTBot\nDisallow: /\n\n"+polisSection(terms, ts.URL))

	if got := robotsCheck(t, ts).Outcome; got != OutcomePassed {
		t.Fatalf("outcome = %s, want %s — a blocked agent cannot be granted anything", got, OutcomePassed)
	}
}

func TestPublicRobots_MissingSectionIsAWarning(t *testing.T) {
	site, ts, _ := licensedSite(t, license.ProfileReserved)
	site.set("/robots.txt", "User-agent: *\nAllow: /\n")

	c := robotsCheck(t, ts)
	if c.Outcome != OutcomeWarning {
		t.Fatalf("outcome = %s, want %s — the signed terms reach nobody", c.Outcome, OutcomeWarning)
	}
	if !strings.Contains(findingsText(c), "NOT in the served file") {
		t.Errorf("findings:\n%s", findingsText(c))
	}
}

func TestPublicRobots_AlteredSectionIsAWarning(t *testing.T) {
	site, ts, terms := licensedSite(t, license.ProfileReserved)
	altered := strings.Replace(polisSection(terms, ts.URL), "train-ai=n", "train-ai=y", 1)
	site.set("/robots.txt", altered)

	c := robotsCheck(t, ts)
	if c.Outcome != OutcomeWarning {
		t.Fatalf("outcome = %s, want %s", c.Outcome, OutcomeWarning)
	}
	if !strings.Contains(findingsText(c), "the signed licence says") {
		t.Errorf("the finding must quote what the licence actually says:\n%s", findingsText(c))
	}
}

// A group appended INSIDE polis's own section would otherwise be read as ours,
// which is the one way an injector could hide.
func TestPublicRobots_AGroupAppendedInsideOurSectionIsAWarning(t *testing.T) {
	site, ts, terms := licensedSite(t, license.ProfileReserved)
	site.set("/robots.txt", polisSection(terms, ts.URL)+"\nUser-agent: GPTBot\nAllow: /\nContent-Usage: train-ai=y\n")

	c := robotsCheck(t, ts)
	if c.Outcome != OutcomeWarning {
		t.Fatalf("outcome = %s, want %s", c.Outcome, OutcomeWarning)
	}
	if !strings.Contains(findingsText(c), "INSIDE polis's own section") {
		t.Errorf("findings:\n%s", findingsText(c))
	}
}

// ⛔ D8. A fetch that failed is NOT CHECKED — never fine. This is the only check
// in the suite that reads the public wire; a silent skip would leave the hole it
// exists to close looking closed.
func TestPublicRobots_AFetchFailureIsNotCheckedNotOK(t *testing.T) {
	site, ts, _ := licensedSite(t, license.ProfileReserved)
	site.mu.Lock()
	delete(site.files, "/robots.txt")
	site.mu.Unlock()

	r := NewRemote().RunSite(ts.URL)
	c := checkByID(t, r, "content.license_robots")
	if c.Outcome != OutcomeNotApplicable {
		t.Fatalf("outcome = %s, want %s", c.Outcome, OutcomeNotApplicable)
	}
	if !strings.Contains(c.Reason, "NOT checked") {
		t.Errorf("reason must say what this result does not cover: %q", c.Reason)
	}
	if !r.OK() {
		t.Error("an unreachable robots.txt is a gap in coverage, not a defect in the site")
	}
}

// Terms that did not verify are nothing to compare against, and comparing the
// file to itself would be theatre.
func TestPublicTerms_WithoutAVerifiedLicenceNothingIsClaimed(t *testing.T) {
	site, _ := signedRemoteSite(t, 1) // publishes no licence pointer
	ts := site.serve(t)

	r := NewRemote().RunSite(ts.URL)
	for _, id := range []string{"content.license_robots", "content.license_rsl"} {
		c := checkByID(t, r, id)
		if c.Outcome != OutcomeNotApplicable {
			t.Errorf("%s: outcome = %s, want %s", id, c.Outcome, OutcomeNotApplicable)
		}
	}
}

func TestPublicRSL_UntouchedPassesAndAlteredWarns(t *testing.T) {
	site, ts, _ := licensedSite(t, license.ProfileReserved)
	if got := checkByID(t, NewRemote().RunSite(ts.URL), "content.license_rsl"); got.Outcome != OutcomePassed {
		t.Fatalf("outcome = %s, want %s: %s", got.Outcome, OutcomePassed, got.Detail)
	}

	site.mu.Lock()
	served := site.files["/rsl.xml"]
	site.mu.Unlock()
	site.set("/rsl.xml", strings.Replace(served, `<payment type="attribution"></payment>`, "", 1))

	c := checkByID(t, NewRemote().RunSite(ts.URL), "content.license_rsl")
	if c.Outcome != OutcomeWarning {
		t.Fatalf("outcome = %s, want %s — attribution is a term AIPREF cannot express, so rsl.xml is its only machine-readable home", c.Outcome, OutcomeWarning)
	}
}

// A warning must never move the exit code. The tenant usually cannot edit a
// managed robots.txt, and an alert its subject cannot action is noise.
func TestPublicRobots_AWarningDoesNotFailTheRun(t *testing.T) {
	site, ts, terms := licensedSite(t, license.ProfileOpen)
	site.set("/robots.txt", restrictiveInjection+polisSection(terms, ts.URL))

	r := NewRemote().RunSite(ts.URL)
	if r.Totals.Warning == 0 {
		t.Fatal("fixture produced no warning")
	}
	if !r.OK() {
		t.Error("a warning about an intermediary must not fail the run")
	}
	if r.Totals.Failed != 0 {
		t.Errorf("Failed = %d; a warning must not be counted as a failure", r.Totals.Failed)
	}
}

// RFC 9309 §2.2.1 in isolation: same-value groups merge, a named group wins
// over `*`, and an Allow ties out against an equally specific Disallow
// (§2.2.2).
func TestRobotsPrecedence_MergeAndSpecificity(t *testing.T) {
	merged := mergeRobots(parseRobots(`
User-agent: *
Allow: /
Content-Usage: train-ai=n

User-agent: *
Disallow: /

User-agent: GPTBot
User-agent: CCBot
Disallow: /
`))
	star, ok := merged["*"]
	if !ok {
		t.Fatal("no wildcard group")
	}
	if !star.hasUsage || star.usage != "train-ai=n" {
		t.Errorf("the two `*` groups must merge, keeping the content-usage rule: %+v", star)
	}
	if star.deniesRoot() {
		t.Error("Allow: / and Disallow: / are equally specific; the least restrictive wins (§2.2.2)")
	}
	for _, agent := range []string{"gptbot", "ccbot"} {
		g, ok := merged[agent]
		if !ok {
			t.Fatalf("%s got no group of its own", agent)
		}
		if !g.deniesRoot() {
			t.Errorf("%s: consecutive User-agent lines name ONE group, so both inherit the Disallow", agent)
		}
	}
}
