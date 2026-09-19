package clone

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fullSite is a mock polis site that serves everything a modern site publishes:
// a .well-known/polis carrying pointers, a bundle.json declaring where content
// lives, an index with BOTH posts and comments, and the optional signed
// artifacts later epics added.
type fullSite struct {
	wellKnown string
	bundle    string
	index     []string
	files     map[string]string // request path -> body
	fallback  string            // when set, served for every unknown path (a hostile site answers anything)
	requested map[string]int
}

func (s *fullSite) serve(t *testing.T) *httptest.Server {
	t.Helper()
	s.requested = map[string]int{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requested[r.URL.Path]++
		switch r.URL.Path {
		case "/.well-known/polis":
			fmt.Fprint(w, s.wellKnown)
			return
		case "/content/pub.polis.core/index.jsonl":
			for _, line := range s.index {
				fmt.Fprintln(w, line)
			}
			return
		}
		if body, ok := s.files[r.URL.Path]; ok {
			fmt.Fprint(w, body)
			return
		}
		if s.fallback != "" {
			fmt.Fprint(w, s.fallback)
			return
		}
		http.NotFound(w, r)
	}))
}

// bundleJSON builds a bundle.json declaring NON-DEFAULT directories, so any
// hardcoded "content/pub.polis.core/comment" in the clone path misses.
func bundleJSON() string {
	return `{
  "name": "pub.polis.core",
  "version": "1.0.0",
  "handler": {"type": "builtin"},
  "types": {
    "pub.polis.post": {"dir": "writing", "mount": "/posts"},
    "pub.polis.comment": {"dir": "replies", "mount": "/comments"},
    "pub.polis.follow": {"dir": "roster"},
    "pub.polis.license": {"dir": "terms", "mount": "/license"}
  }
}`
}

// A site's identity document carries fields no Go struct in this repo models —
// `bundles`, `license` and `public_key_messages` among them. Clone used to
// round-trip it through remote.WellKnown, which silently dropped every one of
// them: the cloned site had no bundle pointer and no licence pointer.
func TestClone_PreservesWellKnownFieldsNoStructModels(t *testing.T) {
	wk := `{
  "version": "2.0",
  "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest",
  "author_name": "Alice",
  "created": "2025-01-01T00:00:00Z",
  "license": "/content/pub.polis.core/terms/license.json",
  "public_key_messages": {"epoch": 1},
  "bundles": {"pub.polis.core": {"path": "content/pub.polis.core/bundle.json"}}
}`
	site := &fullSite{
		wellKnown: wk,
		files:     map[string]string{"/content/pub.polis.core/bundle.json": bundleJSON()},
	}
	ts := site.serve(t)
	defer ts.Close()

	target := t.TempDir()
	if _, err := Clone(ts.URL, target, CloneOptions{FullClone: true}); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(target, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != wk {
		t.Errorf("cloned .well-known/polis was rewritten rather than copied:\ngot:  %s\nwant: %s", got, wk)
	}
	for _, key := range []string{"bundles", "license", "public_key_messages"} {
		if !strings.Contains(string(got), key) {
			t.Errorf("cloned identity document lost the %q field", key)
		}
	}
}

// The site declares where its content lives. A clone that assumes the default
// layout fetches nothing on a site that moved a directory — and reports success.
func TestClone_ResolvesContentDirsFromTheBundle(t *testing.T) {
	site := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k","license":"/content/pub.polis.core/terms/license.json","bundles":{"pub.polis.core":{"path":"content/pub.polis.core/bundle.json"}}}`,
		files: map[string]string{
			"/content/pub.polis.core/bundle.json":               bundleJSON(),
			"/content/pub.polis.core/replies/blessed.json":      `{"comments":[{"blessed":[{"url":"u"}]}]}`,
			"/content/pub.polis.core/roster/following.json":     `{"following":[]}`,
			"/content/pub.polis.core/terms/license.json":        `{"v":"pub.polis.license.v1"}`,
			"/content/pub.polis.core/writing/20260101/hello.md": "---\ntitle: Hello\n---\n\nbody\n",
		},
		index: []string{`{"type":"post","path":"content/pub.polis.core/writing/20260101/hello.md","title":"Hello","published":"2026-01-01T00:00:00Z"}`},
	}
	ts := site.serve(t)
	defer ts.Close()

	target := t.TempDir()
	result, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	for _, rel := range []string{
		filepath.Join("content", "pub.polis.core", "bundle.json"),
		filepath.Join("content", "pub.polis.core", "replies", "blessed.json"),
		filepath.Join("content", "pub.polis.core", "roster", "following.json"),
		filepath.Join("content", "pub.polis.core", "terms", "license.json"),
		filepath.Join("content", "pub.polis.core", "writing", "20260101", "hello.md"),
	} {
		if _, err := os.Stat(filepath.Join(target, rel)); err != nil {
			t.Errorf("missing from clone: %s", rel)
		}
	}
	if result.BlessedCommentsSynced != 1 {
		t.Errorf("BlessedCommentsSynced = %d, want 1 — the declared comment dir was not read", result.BlessedCommentsSynced)
	}
	// The default paths must never have been tried: they are not where this
	// site said its content lives.
	if n := site.requested["/content/pub.polis.core/comment/blessed.json"]; n != 0 {
		t.Errorf("clone fetched the DEFAULT blessed.json path %d times; it should follow the bundle", n)
	}
}

// The licence is found by following the `license` pointer. A site that states
// no terms has no pointer, and clone must not guess a path and copy a stray
// file — that would clone a statement the author never made.
func TestClone_LicenceIsFoundByPointerAndAbsenceIsOK(t *testing.T) {
	withPointer := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k","license":"/content/pub.polis.core/terms/license.json","bundles":{"pub.polis.core":{"path":"content/pub.polis.core/bundle.json"}}}`,
		files: map[string]string{
			"/content/pub.polis.core/bundle.json":        bundleJSON(),
			"/content/pub.polis.core/terms/license.json": `{"v":"pub.polis.license.v1"}`,
		},
	}
	ts := withPointer.serve(t)
	defer ts.Close()
	target := t.TempDir()
	res, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.LicenseCloned {
		t.Error("LicenseCloned = false, want true")
	}

	// A site that states no terms: pointer absent, and a stray file at the
	// conventional path that must NOT be picked up.
	noPointer := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k","bundles":{"pub.polis.core":{"path":"content/pub.polis.core/bundle.json"}}}`,
		files: map[string]string{
			"/content/pub.polis.core/bundle.json":          bundleJSON(),
			"/content/pub.polis.core/license/license.json": `{"v":"pub.polis.license.v1"}`,
			"/content/pub.polis.core/terms/license.json":   `{"v":"pub.polis.license.v1"}`,
		},
	}
	ts2 := noPointer.serve(t)
	defer ts2.Close()
	target2 := t.TempDir()
	res2, err := Clone(ts2.URL, target2, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatal(err)
	}
	if res2.LicenseCloned {
		t.Error("a site with no licence pointer states no terms; clone must not find one by convention")
	}
}

func TestClone_FetchesDIDDocumentWhenPublished(t *testing.T) {
	site := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k"}`,
		files:     map[string]string{"/.well-known/did.json": `{"id":"did:web:alice.example"}`},
	}
	ts := site.serve(t)
	defer ts.Close()

	target := t.TempDir()
	res, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DIDDocumentCloned {
		t.Fatal("DIDDocumentCloned = false, want true")
	}
	data, err := os.ReadFile(filepath.Join(target, ".well-known", "did.json"))
	if err != nil || !strings.Contains(string(data), "did:web:alice.example") {
		t.Errorf("did.json not cloned: %v %s", err, data)
	}

	// Absent is an ordinary state — most sites have never published one.
	bare := &fullSite{wellKnown: `{"version":"2.0","public_key":"k"}`}
	ts2 := bare.serve(t)
	defer ts2.Close()
	res2, err := Clone(ts2.URL, t.TempDir(), CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("a site with no DID document must still clone: %v", err)
	}
	if res2.DIDDocumentCloned {
		t.Error("DIDDocumentCloned = true for a site that publishes none")
	}
}

// Comments are in the index and were being skipped, so `comments_downloaded`
// was printed on every clone and incremented on none — and a clone had no
// comment files whose signatures could be checked.
func TestClone_DownloadsIndexedComments(t *testing.T) {
	site := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k"}`,
		index: []string{
			`{"type":"post","path":"content/pub.polis.core/post/20260101/hello.md","published":"2026-01-01T00:00:00Z"}`,
			`{"type":"comment","path":"content/pub.polis.core/comment/20260102/abc.md","published":"2026-01-02T00:00:00Z"}`,
		},
		files: map[string]string{
			"/content/pub.polis.core/post/20260101/hello.md":  "---\ntitle: Hello\n---\n\nbody\n",
			"/content/pub.polis.core/comment/20260102/abc.md": "---\ntype: comment\n---\n\nreply\n",
		},
	}
	ts := site.serve(t)
	defer ts.Close()

	target := t.TempDir()
	res, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.PostsDownloaded != 1 {
		t.Errorf("PostsDownloaded = %d, want 1", res.PostsDownloaded)
	}
	if res.CommentsDownloaded != 1 {
		t.Errorf("CommentsDownloaded = %d, want 1", res.CommentsDownloaded)
	}
	if _, err := os.Stat(filepath.Join(target, "content", "pub.polis.core", "comment", "20260102", "abc.md")); err != nil {
		t.Errorf("comment file not written: %v", err)
	}
}

// What a clone could NOT collect must be stated, not implied by absence.
// Otherwise `polis validate ./clone` reports "no problems" about artifacts
// nobody looked for.
func TestClone_ReportsTypesItCannotEnumerate(t *testing.T) {
	site := &fullSite{wellKnown: `{"version":"2.0","public_key":"k"}`}
	ts := site.serve(t)
	defer ts.Close()

	res, err := Clone(ts.URL, t.TempDir(), CloneOptions{FullClone: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"pub.polis.tag": true, "pub.polis.attestation": true}
	if len(res.NotEnumerable) != len(want) {
		t.Fatalf("NotEnumerable = %v, want %v", res.NotEnumerable, want)
	}
	for _, got := range res.NotEnumerable {
		if !want[got] {
			t.Errorf("unexpected entry %q", got)
		}
	}

	// And it survives the JSON contract.
	data, _ := json.Marshal(res)
	if !strings.Contains(string(data), "not_enumerable") {
		t.Error("not_enumerable missing from --json output")
	}
}

// R24-6's remaining half. Three signed content types were published with no way
// for a third party to discover they exist — index.jsonl carried posts and
// comments, and nothing enumerated tag or attestation. Signet epic 25 D4 made
// them contributors to the same file, so a clone collects them like any other
// indexed line.
func TestClone_CollectsTagsAndAttestationsWhenTheIndexEnumeratesThem(t *testing.T) {
	site := &fullSite{
		wellKnown: `{"version":"2.0","public_key":"k","bundles":{"pub.polis.core":{"path":"content/pub.polis.core/bundle.json"}}}`,
		files: map[string]string{
			"/content/pub.polis.core/bundle.json":                           bundleJSON(),
			"/content/pub.polis.core/tag/reading.json":                      `{"tag":"reading","targets":[],"current_version":"sha256:t"}`,
			"/content/pub.polis.core/attestation/20260828T140200Z-abc.json": `{"type":"pub.polis.attestation","issuer":"https://alice.example"}`,
		},
		index: []string{
			`{"type":"tag","path":"content/pub.polis.core/tag/reading.json","title":"reading","published":"2026-01-01T00:00:00Z","current_version":"sha256:t"}`,
			`{"type":"attestation","path":"content/pub.polis.core/attestation/20260828T140200Z-abc.json","title":"pub.polis.attestation.same-as","published":"2026-08-28T14:02:00Z","current_version":"sha256:a"}`,
		},
	}
	ts := site.serve(t)
	defer ts.Close()

	target := t.TempDir()
	res, err := Clone(ts.URL, target, CloneOptions{FullClone: true})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if res.TagsDownloaded != 1 {
		t.Errorf("TagsDownloaded = %d, want 1", res.TagsDownloaded)
	}
	if res.AttestationsDownloaded != 1 {
		t.Errorf("AttestationsDownloaded = %d, want 1", res.AttestationsDownloaded)
	}
	for _, rel := range []string{
		filepath.Join("content", "pub.polis.core", "tag", "reading.json"),
		filepath.Join("content", "pub.polis.core", "attestation", "20260828T140200Z-abc.json"),
	} {
		if _, err := os.Stat(filepath.Join(target, rel)); err != nil {
			t.Errorf("%s not written: %v", rel, err)
		}
	}

	// Found, therefore not reported as unenumerable.
	if len(res.NotEnumerable) != 0 {
		t.Errorf("NotEnumerable = %v, want empty — both types were in the index", res.NotEnumerable)
	}
}
