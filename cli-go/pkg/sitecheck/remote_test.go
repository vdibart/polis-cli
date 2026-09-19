package sitecheck

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// remoteSite serves a polis site over HTTP and counts every request, so a test
// can assert what was fetched and how often.
type remoteSite struct {
	mu    sync.Mutex
	files map[string]string
	hits  map[string]int
}

func (s *remoteSite) serve(t *testing.T) *httptest.Server {
	t.Helper()
	s.hits = map[string]int{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		body, ok := s.files[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (s *remoteSite) hitCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// signedRemoteSite builds a site serving one signed post, its index and its
// identity document.
func signedRemoteSite(t *testing.T, posts int) (*remoteSite, []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	site := &remoteSite{files: map[string]string{
		"/.well-known/polis": string(mustJSON(map[string]interface{}{
			"version": "2.0", "public_key": string(pub),
		})),
	}}
	var index strings.Builder
	for i := 0; i < posts; i++ {
		rel := fmt.Sprintf("content/pub.polis.core/post/20260101/p%d.md", i)
		content := signedPost(t, priv, fmt.Sprintf("Post %d.\n", i), false)
		site.files["/"+rel] = content
		fm, _, _ := ParseFrontmatter(content)
		index.Write(mustJSONLine(map[string]string{
			"type": "post", "path": rel, "published": "2026-01-01T00:00:00Z",
			"current_version": fm.CurrentVersion,
		}))
		index.WriteString("\n")
	}
	site.files["/content/pub.polis.core/index.jsonl"] = index.String()
	return site, priv
}

// ⛔ A Done-when, asserted rather than assumed: the remote form writes NOTHING.
// Cloning is `polis clone`'s job, and a validator that quietly left a directory
// behind would be doing it badly.
func TestRunSite_StoresNothing(t *testing.T) {
	site, _ := signedRemoteSite(t, 2)
	ts := site.serve(t)

	work := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })

	before := treeSnapshot(t, work)
	r := NewRemote().RunSite(ts.URL)
	if !r.OK() {
		t.Fatalf("expected a clean site; %+v", r.Totals)
	}
	after := treeSnapshot(t, work)

	if len(after) != len(before) {
		t.Errorf("remote validation wrote to disk: %v", after)
	}
}

func treeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && path != root {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// ⭐ Two fetches for one artifact, and the key is cached PER DOMAIN — so a site
// with 500 posts costs one key fetch, not 500.
func TestRunArtifact_CostsTwoFetchesAndCachesTheKeyPerDomain(t *testing.T) {
	site, _ := signedRemoteSite(t, 3)
	ts := site.serve(t)

	rr := NewRemote()
	for i := 0; i < 3; i++ {
		url := fmt.Sprintf("%s/content/pub.polis.core/post/20260101/p%d.md", ts.URL, i)
		r := rr.RunArtifact(url)
		if !r.OK() {
			t.Fatalf("artifact %d did not verify: %+v", i, r.Checks)
		}
	}

	if got := site.hitCount("/.well-known/polis"); got != 1 {
		t.Errorf("fetched the identity document %d times for 3 artifacts on one domain, want 1", got)
	}
	if rr.KeyFetches != 1 {
		t.Errorf("KeyFetches = %d, want 1", rr.KeyFetches)
	}
	for i := 0; i < 3; i++ {
		p := fmt.Sprintf("/content/pub.polis.core/post/20260101/p%d.md", i)
		if got := site.hitCount(p); got != 1 {
			t.Errorf("%s fetched %d times, want 1", p, got)
		}
	}
}

// ⚠️ A per-URL result verifies ONE artifact. It must say so, in --json too, or
// it can be quoted as a claim about the whole site.
func TestRunArtifact_CarriesItsScope(t *testing.T) {
	site, _ := signedRemoteSite(t, 1)
	ts := site.serve(t)

	r := NewRemote().RunArtifact(ts.URL + "/content/pub.polis.core/post/20260101/p0.md")
	if r.Scope != ScopeArtifact {
		t.Errorf("scope = %q, want %q", r.Scope, ScopeArtifact)
	}
	if r.Note == "" {
		t.Fatal("a per-URL result must carry its caveat in prose")
	}

	data, _ := json.Marshal(r)
	var round Report
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatal(err)
	}
	if round.Scope != ScopeArtifact || round.Note == "" {
		t.Errorf("scope and note did not survive --json: %+v", round)
	}
	for _, c := range round.Checks {
		if c.Family == FamilyIndex || c.Family == FamilyPolicy || c.Family == FamilyBundle {
			t.Errorf("a per-URL run reported %s (%s); it examined no such thing", c.ID, c.Family)
		}
	}
}

func TestRunArtifact_TamperedArtifactFails(t *testing.T) {
	site, _ := signedRemoteSite(t, 1)
	site.files["/content/pub.polis.core/post/20260101/p0.md"] += "\nappended after signing\n"
	ts := site.serve(t)

	r := NewRemote().RunArtifact(ts.URL + "/content/pub.polis.core/post/20260101/p0.md")
	if r.OK() {
		t.Fatal("a tampered artifact must not report OK")
	}
}

// Remote form must be as loud about its blind spots as local form is about a
// clone's. Owner-only state is not served over HTTP and must never look checked.
func TestRunSite_ReportsWhatHTTPCannotSee(t *testing.T) {
	site, _ := signedRemoteSite(t, 1)
	ts := site.serve(t)

	r := NewRemote().RunSite(ts.URL)
	for _, id := range []string{"identity.key_files", "identity.key_perms", "identity.key_match", "bundle.registry", "content.attestations", "content.tags"} {
		c := checkByID(t, r, id)
		if c.Outcome != OutcomeNotApplicable {
			t.Errorf("%s: outcome = %s, want not_applicable over HTTP", id, c.Outcome)
		}
		if c.Reason == "" {
			t.Errorf("%s: no reason given", id)
		}
	}
	// The index check must state that phantom detection is impossible remotely,
	// so the same words do not silently mean less than they do locally.
	idx := checkByID(t, r, "index.consistency")
	if !contains(idx.Detail, "phantom") {
		t.Errorf("index.consistency detail = %q, want it to name what it could not do", idx.Detail)
	}
}

func TestRunSite_UnreachableHostIsFatalNotClean(t *testing.T) {
	site := &remoteSite{files: map[string]string{}}
	ts := site.serve(t)

	r := NewRemote().RunSite(ts.URL)
	if r.FatalError == "" {
		t.Fatal("a site serving no identity document cannot be validated; want a fatal error")
	}
	if r.OK() {
		t.Error("a run that could not start must never report OK")
	}
}

// ⚠️ REGRESSION. Both rotation paths sign polisurl.ExtractDomain(POLIS_BASE_URL),
// which STRIPS THE PORT. A remote validator that kept the port rebuilt a
// different canonical rotation message and reported a perfectly good chain as
// forged. Found by running `polis validate` against a real rotated site on
// localhost:8931 — every unit test passed, because none of them used a port.
func TestRemoteDerivesTheDomainTheSameWayARotationSignsIt(t *testing.T) {
	k0, k1 := genKey(t), genKey(t)

	// A site whose rotation was signed for "localhost" (no port), the way
	// polis rotate-key does it.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk := map[string]interface{}{"public_key": k0.pub, "created": "2026-03-03T05:35:09Z", "author_name": "A"}
	data, _ := json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := site.WriteGenesisKeyHistory(dir); err != nil {
		t.Fatal(err)
	}
	khRotate(t, dir, "localhost", k0, k1, "2026-09-15T10:22:03Z")
	wkBody, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}

	// Served on a port, as any local or staging deployment is.
	r := &Report{Target: "http://localhost:8931", Form: FormRemote, Scope: ScopeSite}
	rr := NewRemote()
	rr.checkRemoteKeyHistory(r, "http://localhost:8931", wkBody, k1.pub)

	for _, c := range r.Checks {
		if c.ID == "identity.key_history" {
			if c.Outcome != OutcomePassed {
				t.Fatalf("a chain signed for the port-stripped host must verify when served on a port: %+v", c)
			}
			return
		}
	}
	t.Fatal("no identity.key_history check was produced")
}

// The remote index read (pkg/remote) skipped a line it
// could not parse, so a half-garbage served index read as clean. It fails,
// naming the line.
func TestRunSite_MalformedIndexLineIsNamed(t *testing.T) {
	site, _ := signedRemoteSite(t, 1)
	site.files["/content/pub.polis.core/index.jsonl"] += "{broken\n"
	ts := site.serve(t)

	idx := checkByID(t, NewRemote().RunSite(ts.URL), "index.consistency")
	if idx.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %s, want failed; %+v", idx.Outcome, idx)
	}
	found := false
	for _, f := range idx.Findings {
		if strings.Contains(f, "1 line(s) of index.jsonl do not parse") && strings.Contains(f, "lines 2") {
			found = true
		}
	}
	if !found {
		t.Errorf("findings = %v, want the malformed line named", idx.Findings)
	}
}
