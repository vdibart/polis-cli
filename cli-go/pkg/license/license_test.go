package license

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func testKeys(t *testing.T) (priv, pub []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return priv, pub
}

func TestProfileTermsUsesOnlyStandardVocabulary(t *testing.T) {
	// The checkable rule from the design: every value in a profile comes from a
	// standard's vocabulary with that standard's meaning. If this test needs a
	// new allowed value, that is the signal to stop and escalate rather than to
	// widen the test.
	aiprefValues := map[string]bool{Allow: true, Disallow: true}

	for _, profile := range []string{ProfileReserved, ProfileOpen} {
		terms, err := ProfileTerms(profile, "https://example.com", "2026-08-27T00:00:00Z")
		if err != nil {
			t.Fatalf("%s: %v", profile, err)
		}
		if err := terms.Validate(); err != nil {
			t.Errorf("%s: validate: %v", profile, err)
		}
		for name, v := range map[string]string{"train-ai": terms.TrainAI, "search": terms.Search, "ai-input": terms.AIInput} {
			if v != "" && !aiprefValues[v] {
				t.Errorf("%s: %s = %q is not a standard value", profile, name, v)
			}
		}
	}
}

func TestReservedProfileIsOpenForReachAndClosedForExtraction(t *testing.T) {
	terms, err := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if terms.TrainAI != Disallow {
		t.Errorf("train-ai = %q, want %q — the extraction axis must be closed", terms.TrainAI, Disallow)
	}
	if terms.Search != Allow {
		t.Errorf("search = %q, want %q — the reach axis is the point of the default", terms.Search, Allow)
	}
	if terms.AIInput != Disallow {
		t.Errorf("ai-input = %q, want %q — summarising into an answer is the live wound", terms.AIInput, Disallow)
	}
	if terms.Attribution != AttributionRequired {
		t.Errorf("attribution = %q, want %q", terms.Attribution, AttributionRequired)
	}
}

func TestNoPolisURLEverEntersTheSignedPayload(t *testing.T) {
	// Putting a polis.pub URL into everyone's signed frontmatter would make
	// polis.pub a permanent, unrevocable dependency of every author's terms.
	// The only URL in a payload points at the author's own domain.
	terms, err := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(terms)
	if strings.Contains(string(blob), "polis.pub") {
		t.Errorf("licence payload references polis.pub: %s", blob)
	}
	for _, u := range []string{terms.Terms, terms.Contact} {
		if u != "" && !strings.HasPrefix(u, "https://maya.example") {
			t.Errorf("payload URL %q is not on the author's own domain", u)
		}
	}
}

func TestProfileIsATagNotAURL(t *testing.T) {
	for _, p := range []string{ProfileReserved, ProfileOpen} {
		if strings.Contains(p, "://") || strings.HasPrefix(p, "http") {
			t.Errorf("profile %q looks like a URL; it must be a semantic tag", p)
		}
		if !strings.HasPrefix(p, "pub.polis.license.") {
			t.Errorf("profile %q does not follow polis namespacing", p)
		}
	}
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	priv, pub := testKeys(t)
	dir := t.TempDir()
	path := DefaultPath(dir)

	f, err := New(ProfileReserved, "https://maya.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := SignAndWrite(f, path, priv); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Verify(loaded, pub)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Error("licence signature did not verify after a write/load round trip")
	}
	if loaded.Type != TypeName {
		t.Errorf("type = %q, want %q", loaded.Type, TypeName)
	}
	if !strings.HasPrefix(loaded.Version, "sha256:") {
		t.Errorf("current_version = %q, want a sha256: hash", loaded.Version)
	}
}

func TestTamperedLicenceFailsVerification(t *testing.T) {
	priv, pub := testKeys(t)
	dir := t.TempDir()
	path := DefaultPath(dir)

	f, _ := New(ProfileReserved, "https://maya.example")
	if err := SignAndWrite(f, path, priv); err != nil {
		t.Fatal(err)
	}

	// A host rewriting the terms to something permissive is exactly the attack
	// the signature exists to make detectable.
	raw, _ := os.ReadFile(path)
	tampered := strings.Replace(string(raw), `"train-ai": "n"`, `"train-ai": "y"`, 1)
	if tampered == string(raw) {
		t.Fatal("test setup: nothing was tampered with")
	}
	os.WriteFile(path, []byte(tampered), 0644)

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := Verify(loaded, pub)
	if ok {
		t.Error("a licence with rewritten terms still verified")
	}
}

func TestBlockRoundTripsThroughFrontmatter(t *testing.T) {
	terms, _ := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T14:02:00Z")
	content := "---\ntitle: A post\npublished: 2026-08-27T14:02:00Z\n" + Block(terms) + "\nsignature: abc\n---\n\nBody text.\n"

	got := ParseBlock(content)
	if got == nil {
		t.Fatal("ParseBlock returned nil for content that carries terms")
	}
	if *got != *terms {
		t.Errorf("round trip lost data:\n got %+v\nwant %+v", *got, *terms)
	}
}

func TestParseBlockReturnsNilWhenTermsAreUnstated(t *testing.T) {
	// Absent means unstated — not permitted, not denied.
	content := "---\ntitle: A post\nsignature: abc\n---\n\nBody.\n"
	if got := ParseBlock(content); got != nil {
		t.Errorf("ParseBlock invented terms from silence: %+v", got)
	}
}

func TestBlockIsIndentedSoItCannotCollideWithTheSigningStripList(t *testing.T) {
	// the markdown signing base drops the top-level "signature:" line (and
	// "author:" in comments) by raw prefix match, so every child of the licence
	// block must be indented or a key could be silently dropped from the
	// signing base.
	terms, _ := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T00:00:00Z")
	for i, line := range strings.Split(Block(terms), "\n") {
		if i == 0 {
			if line != "license:" {
				t.Errorf("first line = %q, want %q", line, "license:")
			}
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("line %q is not indented", line)
		}
	}
}

func TestNoBlockKeyCollidesWithAKeyJudgeMatchesByShape(t *testing.T) {
	// judge.parseFrontmatter trims a key before matching it, so unlike the
	// other parsers it does not skip indented lines. A nested child named
	// "signature" or "current-version" would be read as top-level.
	forbidden := map[string]bool{"signature": true, "current-version": true, "author": true}
	for _, k := range blockKeys {
		if forbidden[k] {
			t.Errorf("licence block key %q collides with a key matched by shape elsewhere", k)
		}
	}
}

func TestAuthoredProfileReadsTheTerseFormOnly(t *testing.T) {
	authored := "---\ntitle: A post\nlicense: reserved\n---\n\nBody.\n"
	got, ok := AuthoredProfile(authored)
	if !ok || got != "reserved" {
		t.Errorf("AuthoredProfile = (%q, %v), want (\"reserved\", true)", got, ok)
	}

	terms, _ := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T00:00:00Z")
	materialised := "---\ntitle: A post\n" + Block(terms) + "\n---\n\nBody.\n"
	if got, ok := AuthoredProfile(materialised); ok {
		t.Errorf("AuthoredProfile read a materialised block as an authored scalar: %q", got)
	}
}

func TestStripBlockRemovesTheWholeBlock(t *testing.T) {
	terms, _ := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T00:00:00Z")
	fm := "title: A post\n" + Block(terms) + "\nsignature: abc"
	got := StripBlock(fm)
	if strings.Contains(got, "train-ai") || strings.Contains(got, "license:") {
		t.Errorf("StripBlock left licence data behind:\n%s", got)
	}
	if !strings.Contains(got, "title: A post") || !strings.Contains(got, "signature: abc") {
		t.Errorf("StripBlock removed unrelated frontmatter:\n%s", got)
	}
}

func TestResolvePrefersTheWorkOverTheSite(t *testing.T) {
	site, _ := ProfileTerms(ProfileOpen, "https://maya.example", "2026-01-01T00:00:00Z")

	got, err := Resolve("reserved", site, "https://maya.example")
	if err != nil {
		t.Fatal(err)
	}
	if got.Profile != ProfileReserved {
		t.Errorf("profile = %q, want the work's %q", got.Profile, ProfileReserved)
	}
}

func TestResolveTreatsExplicitNoneAsSilence(t *testing.T) {
	site, _ := ProfileTerms(ProfileReserved, "https://maya.example", "2026-01-01T00:00:00Z")
	got, err := Resolve("none", site, "https://maya.example")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("an explicit `license: none` did not override the site default: %+v", got)
	}
}

func TestResolveStatesNothingWhenNobodyHasSaidAnything(t *testing.T) {
	got, err := Resolve("", nil, "https://maya.example")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("terms were invented from silence: %+v", got)
	}
}

func TestContentUsageEmitsOnlyTheAIPREFVocabulary(t *testing.T) {
	terms, _ := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T00:00:00Z")
	got := ContentUsage(terms)

	if got != "train-ai=n, search=y" {
		t.Errorf("Content-Usage = %q, want %q", got, "train-ai=n, search=y")
	}
	// ai-input is RSL's, not AIPREF's. Emitting it in an AIPREF surface would
	// be inventing vocabulary; conforming consumers MUST ignore unknown labels
	// (vocab-06 §6.4), so it would be noise that signals we did not read the
	// spec.
	if strings.Contains(got, "ai-input") {
		t.Errorf("Content-Usage carries an RSL-only key: %q", got)
	}
	// There is no `ai` category in AIPREF and §4.3 means there never can be.
	for _, f := range strings.Split(got, ", ") {
		key, _, _ := strings.Cut(f, "=")
		if key != "train-ai" && key != "search" {
			t.Errorf("Content-Usage carries a non-vocabulary key %q", key)
		}
	}
}

func TestContentUsageIsEmptyWhenNothingIsStated(t *testing.T) {
	if got := ContentUsage(nil); got != "" {
		t.Errorf("ContentUsage(nil) = %q, want empty — an empty assertion is worse than silence", got)
	}
}

func TestRobotsRuleCarriesAPathForPerResourceTerms(t *testing.T) {
	// attach-04 §3's optional path pattern is what lets a static self-hoster,
	// who cannot set response headers, still express per-resource terms.
	open, _ := ProfileTerms(ProfileOpen, "https://maya.example", "2026-08-27T00:00:00Z")
	got := RobotsRule("/posts/20260827/open-post.html", open)
	want := "Content-Usage: /posts/20260827/open-post.html train-ai=y, search=y"
	if got != want {
		t.Errorf("RobotsRule = %q, want %q", got, want)
	}
}

func TestRobotsIsEmptyForASiteThatHasStatedNothing(t *testing.T) {
	if got := Robots(RobotsOptions{}); got != "" {
		t.Errorf("Robots for an unstated site = %q, want empty — the host must not speak for the user", got)
	}
}

func TestRobotsCarriesBothStandards(t *testing.T) {
	terms, _ := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T00:00:00Z")
	got := Robots(RobotsOptions{SiteTerms: terms, RSLURL: "https://maya.example/rsl.xml"})

	if !strings.Contains(got, "Content-Usage: train-ai=n, search=y") {
		t.Errorf("robots.txt missing the AIPREF rule:\n%s", got)
	}
	if !strings.Contains(got, "License: https://maya.example/rsl.xml") {
		t.Errorf("robots.txt missing the RSL directive:\n%s", got)
	}
	if !strings.Contains(got, "User-Agent: *") {
		t.Errorf("content-usage rules must sit inside a group:\n%s", got)
	}
}

func TestRSLCarriesWhatAIPREFCannotSay(t *testing.T) {
	terms, _ := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T00:00:00Z")
	got, err := RSL(terms, "https://maya.example/")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`xmlns="https://rslstandard.org/rsl"`,
		`<prohibits type="usage">ai-train</prohibits>`,
		`<permits type="usage">search</permits>`,
		`<prohibits type="usage">ai-input</prohibits>`,
		`<payment type="attribution">`,
		`<terms>https://maya.example/license</terms>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rsl.xml missing %s:\n%s", want, got)
		}
	}
}

func TestRSLAssertsNothingTheTermsDoNotState(t *testing.T) {
	// A term the author never stated must not appear as a rule — silence in,
	// silence out.
	terms := &Terms{V: SchemaVersion, Profile: ProfileReserved, TrainAI: Disallow}
	got, err := RSL(terms, "https://maya.example/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "search") {
		t.Errorf("rsl.xml asserts a rule for an unstated category:\n%s", got)
	}
	if strings.Contains(got, "ai-input") {
		t.Errorf("rsl.xml asserts a rule for an unstated category:\n%s", got)
	}
	if strings.Contains(got, "payment") {
		t.Errorf("rsl.xml asserts a payment term that was never stated:\n%s", got)
	}
}

func TestHeadHTMLAndLinkHeaderAgreeWithTheTerms(t *testing.T) {
	terms, _ := ProfileTerms(ProfileReserved, "https://maya.example", "2026-08-27T00:00:00Z")

	head := HeadHTML(terms)
	if !strings.Contains(head, `rel="license" href="https://maya.example/license"`) {
		t.Errorf("head missing the licence link:\n%s", head)
	}
	if !strings.Contains(head, `content="train-ai=n, search=y"`) {
		t.Errorf("head missing the preference meta:\n%s", head)
	}

	if got, want := LinkHeader(terms), `<https://maya.example/license>; rel="license"`; got != want {
		t.Errorf("Link header = %q, want %q", got, want)
	}
	if got := LinkHeader(nil); got != "" {
		t.Errorf("LinkHeader(nil) = %q, want empty", got)
	}
}

func TestLicenceFileIsPublicReadable(t *testing.T) {
	// R11-17: self-hosters serve content/ through another user's web server.
	priv, _ := testKeys(t)
	dir := t.TempDir()
	path := DefaultPath(dir)
	f, _ := New(ProfileReserved, "https://maya.example")
	if err := SignAndWrite(f, path, priv); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("licence file mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestDefaultPathIsUnderTheContentTree(t *testing.T) {
	got := DefaultPath("/site")
	want := filepath.Join("/site", "content", "pub.polis.core", "license", "license.json")
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
	// It must not sit beside policies/ — inbound policy and outbound licence
	// are opposite directions, and adjacency invites exactly that conflation.
	if strings.Contains(got, "policies") {
		t.Errorf("licence file lives beside inbound policy: %q", got)
	}
}
