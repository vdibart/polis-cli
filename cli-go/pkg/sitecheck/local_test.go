package sitecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// buildSite writes a minimal but real polis site: an identity document, a
// bundle declaration, an index and one post.
type siteBuilder struct {
	dir  string
	priv []byte
	pub  []byte
}

func newSite(t *testing.T) *siteBuilder {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	b := &siteBuilder{dir: t.TempDir(), priv: priv, pub: pub}
	b.write(filepath.Join(".well-known", "polis"), mustJSON(map[string]interface{}{
		"version":     "2.0",
		"public_key":  string(pub),
		"author_name": "Alice",
		"created":     "2026-01-01T00:00:00Z",
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]string{"path": "content/pub.polis.core/bundle.json"},
		},
	}))
	b.write(filepath.Join("content", "pub.polis.core", "bundle.json"), mustJSON(map[string]interface{}{
		"name":    "pub.polis.core",
		"version": "1.0.0",
		"handler": map[string]string{"type": "builtin"},
		"types":   map[string]interface{}{"pub.polis.post": map[string]string{"dir": "post"}},
	}))
	return b
}

// withPrivateState gives the site a .polis/ tree with correctly-permissioned
// keys — i.e. makes it look like the owner's working copy rather than a clone.
func (b *siteBuilder) withPrivateState(t *testing.T) *siteBuilder {
	t.Helper()
	b.writeMode(filepath.Join(".polis", "keys", "id_ed25519"), b.priv, 0600)
	b.writeMode(filepath.Join(".polis", "keys", "id_ed25519.pub"), b.pub, 0644)
	return b
}

func (b *siteBuilder) write(rel string, data []byte) { b.writeMode(rel, data, 0644) }

func (b *siteBuilder) writeMode(rel string, data []byte, mode os.FileMode) {
	path := filepath.Join(b.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		panic(err)
	}
}

// addPost writes a post and its index entry. Unsigned when sign is false.
func (b *siteBuilder) addPost(t *testing.T, slug, body string, sign bool) {
	t.Helper()
	rel := filepath.Join("content", "pub.polis.core", "post", "20260101", slug+".md")
	content := signedPost(t, b.priv, body, false)
	if !sign {
		canonical := signing.CanonicalizeContent(body)
		content = "---\ntitle: Test\npublished: 2026-01-15T10:00:00Z\ncurrent-version: sha256:" +
			SHA256Hex([]byte(canonical)) + "\n---\n\n" + canonical
	}
	b.write(rel, []byte(content))

	fm, _, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatal(err)
	}
	entry := mustJSONLine(map[string]string{
		"type": "post", "path": filepath.ToSlash(rel),
		"title": slug, "published": "2026-01-15T10:00:00Z",
		"current_version": fm.CurrentVersion,
	})
	indexPath := filepath.Join(b.dir, "content", "pub.polis.core", "index.jsonl")
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write(append(entry, '\n'))
}

// mustJSONLine emits COMPACT JSON. index.jsonl is one object per LINE, and a
// pretty-printed entry parses as nothing — LoadPublicIndex skips malformed
// lines, so the index check would quietly report "empty index" and the test
// would assert nothing.
func mustJSONLine(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func mustJSON(v interface{}) []byte {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func checkByID(t *testing.T, r *Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("report has no check %q; it has %v", id, ids(r))
	return Check{}
}

func ids(r *Report) []string {
	out := make([]string, 0, len(r.Checks))
	for _, c := range r.Checks {
		out = append(out, c.ID)
	}
	return out
}

// ⛔ THE CENTRAL TEST. A clone is a real directory that is genuinely missing
// things. Every owner-only check must come back NOT APPLICABLE with a reason —
// never passed, and never silently absent. Otherwise validating a clone looks
// identical to validating your own site, and a clean result stops meaning
// anything.
func TestRunLocal_AClonesBlindSpotsAreReportedNotPassed(t *testing.T) {
	b := newSite(t) // deliberately NO .polis/ — this is what a clone looks like
	b.addPost(t, "hello", "Hello.\n", true)

	r := RunLocal(b.dir)

	ownerOnly := []string{"identity.key_files", "identity.key_perms", "identity.key_match", "bundle.registry", "policy.syntax"}
	for _, id := range ownerOnly {
		c := checkByID(t, r, id)
		if c.Outcome != OutcomeNotApplicable {
			t.Errorf("%s: outcome = %s, want not_applicable — a clone cannot answer this", id, c.Outcome)
		}
		if c.Reason == "" {
			t.Errorf("%s: not_applicable with no reason — the reader learns nothing about what was skipped", id)
		}
	}

	if r.Totals.NotApplicable < len(ownerOnly) {
		t.Errorf("not_applicable total = %d, want at least %d", r.Totals.NotApplicable, len(ownerOnly))
	}
	if r.Totals.Failed != 0 {
		t.Errorf("a valid clone must not FAIL; failed = %d", r.Totals.Failed)
	}
	if !r.OK() {
		t.Error("a valid clone must report OK")
	}

	// And the same run must still have actually verified the public artifacts.
	posts := checkByID(t, r, "content.posts")
	if posts.Outcome != OutcomePassed || posts.Examined != 1 {
		t.Errorf("content.posts = %+v, want a passed check over 1 post", posts)
	}
}

// The owner's copy answers the checks a clone cannot. Same command, same
// checks, different visibility — driven by what is there, not by a flag.
func TestRunLocal_OwnersCopyAnswersWhatACloneCannot(t *testing.T) {
	b := newSite(t).withPrivateState(t)
	b.addPost(t, "hello", "Hello.\n", true)

	r := RunLocal(b.dir)
	for _, id := range []string{"identity.key_files", "identity.key_perms", "identity.key_match"} {
		c := checkByID(t, r, id)
		if c.Outcome != OutcomePassed {
			t.Errorf("%s: outcome = %s (%s / %s), want passed", id, c.Outcome, c.Detail, c.Reason)
		}
	}
}

// D5. Most artifacts on most sites are unsigned. A validator that reads
// absent-as-failure tells nearly every self-hoster their site is broken.
func TestRunLocal_AnEntirelyUnsignedSiteIsOK(t *testing.T) {
	b := newSite(t)
	b.addPost(t, "one", "First.\n", false)
	b.addPost(t, "two", "Second.\n", false)

	r := RunLocal(b.dir)
	if !r.OK() {
		t.Fatalf("an unsigned site must report OK; failed = %d", r.Totals.Failed)
	}
	posts := checkByID(t, r, "content.posts")
	if posts.Outcome != OutcomePassed {
		t.Errorf("content.posts outcome = %s, want passed", posts.Outcome)
	}
	if posts.Examined != 2 {
		t.Errorf("examined = %d, want 2", posts.Examined)
	}
	// ⚠️ And it must SAY they were unsigned, or "passed" over-promises.
	if !contains(posts.Detail, "unsigned") {
		t.Errorf("detail = %q, want it to state how many were unsigned", posts.Detail)
	}
}

// Only present-and-failing is a finding.
func TestRunLocal_TamperedPostIsAFinding(t *testing.T) {
	b := newSite(t)
	b.addPost(t, "hello", "Hello.\n", true)

	path := filepath.Join(b.dir, "content", "pub.polis.core", "post", "20260101", "hello.md")
	data, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(string(data)+"\nappended after signing\n"), 0644)

	r := RunLocal(b.dir)
	posts := checkByID(t, r, "content.posts")
	if posts.Outcome != OutcomeFailed {
		t.Fatalf("content.posts outcome = %s, want failed", posts.Outcome)
	}
	if len(posts.Findings) == 0 {
		t.Error("a failed check must enumerate what failed")
	}
	if r.OK() {
		t.Error("a report with a tampered post must not be OK")
	}
}

// A site missing its identity key cannot have ANY signature checked. Reporting
// those as passed would be the exact defect this command was rebuilt to fix.
func TestRunLocal_NoPublishedKeyMakesEveryContentCheckNotApplicable(t *testing.T) {
	b := newSite(t)
	b.addPost(t, "hello", "Hello.\n", true)
	b.write(filepath.Join(".well-known", "polis"), mustJSON(map[string]string{"version": "2.0"}))

	r := RunLocal(b.dir)
	for _, id := range []string{"content.posts", "content.comments", "content.following", "content.blessed", "content.license", "content.attestations", "content.tags"} {
		c := checkByID(t, r, id)
		if c.Outcome != OutcomeNotApplicable {
			t.Errorf("%s: outcome = %s, want not_applicable — there is no key to verify against", id, c.Outcome)
		}
	}
	if checkByID(t, r, "identity.well_known").Outcome != OutcomeFailed {
		t.Error("an identity document with no public_key is a real failure")
	}
}

// Every family the command reference promises must appear in every site run,
// present as an answer or present as an honest gap. A family that simply
// vanishes is how "five families" became "two implemented".
func TestRunLocal_AllFiveFamiliesAreAlwaysReported(t *testing.T) {
	r := RunLocal(newSite(t).dir)
	seen := map[string]bool{}
	for _, c := range r.Checks {
		seen[c.Family] = true
	}
	for _, family := range []string{FamilyContent, FamilyIndex, FamilyPolicy, FamilyIdentity, FamilyBundle} {
		if !seen[family] {
			t.Errorf("family %q is absent from the report entirely", family)
		}
	}
}

// not_applicable must never be countable as passed, in the totals or the JSON.
func TestReport_NotApplicableIsNeverFoldedIntoPassed(t *testing.T) {
	r := RunLocal(newSite(t).dir)
	if r.Totals.Passed+r.Totals.Failed+r.Totals.NotApplicable != len(r.Checks) {
		t.Fatalf("totals %+v do not account for %d checks", r.Totals, len(r.Checks))
	}
	if r.Totals.NotApplicable == 0 {
		t.Fatal("expected an empty site to have unanswerable checks")
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var round Report
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatal(err)
	}
	if round.Totals != r.Totals {
		t.Errorf("totals did not survive --json: %+v vs %+v", round.Totals, r.Totals)
	}
	for _, c := range round.Checks {
		if c.Outcome == OutcomeNotApplicable && c.Reason == "" {
			t.Errorf("%s: not_applicable reached --json with no reason", c.ID)
		}
	}
}

func TestRunLocal_MissingDirectoryIsFatalNotClean(t *testing.T) {
	r := RunLocal(filepath.Join(t.TempDir(), "nope"))
	if r.FatalError == "" {
		t.Fatal("want a fatal error for a directory that is not there")
	}
	if r.OK() {
		t.Error("a run that could not start must never report OK")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// The index check must actually run — it is one of the three families that did
// not exist before this epic, and a malformed index.jsonl silently reporting
// "empty index" is exactly how a check can look present and do nothing.
func TestRunLocal_IndexConsistencyCatchesOrphansAndPhantoms(t *testing.T) {
	b := newSite(t)
	b.addPost(t, "indexed", "Indexed.\n", true)

	// A phantom: on disk, not in the index.
	b.write(filepath.Join("content", "pub.polis.core", "post", "20260101", "phantom.md"),
		[]byte("---\ntitle: Phantom\n---\n\nbody\n"))
	// An orphan: in the index, not on disk.
	indexPath := filepath.Join(b.dir, "content", "pub.polis.core", "index.jsonl")
	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(append(mustJSONLine(map[string]string{
		"type": "post", "path": "content/pub.polis.core/post/20260101/gone.md",
	}), '\n'))
	f.Close()

	c := checkByID(t, RunLocal(b.dir), "index.consistency")
	if c.Outcome != OutcomeFailed {
		t.Fatalf("index.consistency = %s (%q), want failed", c.Outcome, c.Detail)
	}
	for _, want := range []string{"phantom", "orphan"} {
		if !contains(c.Detail, want) {
			t.Errorf("detail %q does not mention %q", c.Detail, want)
		}
	}
}

// Policy parseability is another family that did not exist. A broken rule must
// be a finding, and a site with no rules must be not-applicable rather than
// clean-by-accident.
func TestRunLocal_PolicySyntaxIsCheckedAndAbsenceIsNotAPass(t *testing.T) {
	b := newSite(t).withPrivateState(t)
	if c := checkByID(t, RunLocal(b.dir), "policy.syntax"); c.Outcome != OutcomeNotApplicable {
		t.Errorf("no policy files: outcome = %s, want not_applicable", c.Outcome)
	}

	b.write(filepath.Join("policies", "rules.jsonl"),
		[]byte(`{"policy":"allow comment from anyone"}`+"\n"+`{"policy":"this is not a rule"}`+"\n"))
	c := checkByID(t, RunLocal(b.dir), "policy.syntax")
	if c.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %s (%q), want failed", c.Outcome, c.Detail)
	}
	if len(c.Findings) == 0 {
		t.Error("a policy failure must name the rule that would not parse")
	}
}

// ⛔ `polis validate` NEVER clones. Cloning is `polis clone`'s job and a user
// composes the two. Asserted structurally: nothing in this package may reach
// for the clone package.
func TestSitecheck_NeverClones(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "polis-cli/cli-go/pkg/"+"clone") {
			t.Errorf("%s references the clone package — validation must never clone", e.Name())
		}
	}
}
