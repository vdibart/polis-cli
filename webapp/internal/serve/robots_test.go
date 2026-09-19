package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
)

// The licence's file-based surfaces are the ones a static self-hoster can
// actually publish — that deployment sets no response headers — so robots.txt
// reaching the wire is not a nicety. These tests drive the SHIPPED handler over
// HTTP, because the defect they exist to catch is invisible on disk: the file
// was written correctly and refused at the handler, and every write-side test
// stayed green throughout.

// siteWithRobots renders robots.txt from real terms into a temp site, the way
// the render pass does, and returns the bytes written.
func siteWithRobots(t *testing.T, root string) string {
	t.Helper()
	terms := &license.Terms{
		V:       "pub.polis.license.v1",
		Profile: "pub.polis.license.reserved/1",
		TrainAI: "n",
		Search:  "y",
		Terms:   "https://alice.polis.pub/license",
	}
	robots := license.Robots(license.RobotsOptions{
		SiteTerms: terms,
		RSLURL:    "https://alice.polis.pub/rsl.xml",
	})
	if robots == "" {
		t.Fatal("license.Robots produced nothing for stated terms")
	}
	if err := os.WriteFile(filepath.Join(root, "robots.txt"), []byte(robots), 0644); err != nil {
		t.Fatal(err)
	}
	return robots
}

func getRobots(t *testing.T, root string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	ServeTenantPublic(w, r, dirStorage{root}, "alice", nil)
	return w
}

// TestACrawlerCanReadTheSitesTerms is the whole delivery requirement: the file
// reaches the wire, as text, carrying BOTH standards' payloads — the AIPREF
// preference signal and the pointer to the RSL terms AIPREF cannot express.
func TestACrawlerCanReadTheSitesTerms(t *testing.T) {
	root := t.TempDir()
	written := siteWithRobots(t, root)

	w := getRobots(t, root)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — robots.txt is generated and must be served", w.Code)
	}
	if got, want := w.Header().Get("Content-Type"), "text/plain; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}

	body := w.Body.String()
	if body != written {
		t.Errorf("served bytes differ from the rendered file:\n got %q\nwant %q", body, written)
	}
	// AIPREF: the preference signal.
	if !strings.Contains(body, "Content-Usage: train-ai=n, search=y") {
		t.Errorf("no Content-Usage rule in the served body:\n%s", body)
	}
	// RSL: without this directive rsl.xml is live, correct, and undiscoverable
	// by the mechanism RSL itself defines.
	if !strings.Contains(body, "License: https://alice.polis.pub/rsl.xml") {
		t.Errorf("no License: directive in the served body:\n%s", body)
	}
}

// TestASiteThatStatedNoTermsServesNoRobots pins the other half of the rule. A
// site with no licence gets no robots.txt written (render/license_pages.go
// returns early on nil terms) — the host must not speak for the author — so
// the handler must 404 rather than invent a default.
func TestASiteThatStatedNoTermsServesNoRobots(t *testing.T) {
	root := t.TempDir()

	w := getRobots(t, root)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — a site that stated nothing must say nothing", w.Code)
	}
}

// TestTheAllowanceIsANameNotAnExtension guards the shape of the fix. The
// rejected one-token version (adding ".txt" to the extension switch) would
// publish every .txt in a tenant's public tree; this fails if anyone applies it.
func TestTheAllowanceIsANameNotAnExtension(t *testing.T) {
	root := t.TempDir()
	siteWithRobots(t, root)
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "posts"), 0755); err != nil {
		t.Fatal(err)
	}
	// Same name, wrong path — the spec fixes robots.txt at exactly one place.
	if err := os.WriteFile(filepath.Join(root, "posts", "robots.txt"), []byte("nested"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/notes.txt", "/posts/robots.txt"} {
		w := httptest.NewRecorder()
		ServeTenantPublic(w, httptest.NewRequest(http.MethodGet, path, nil), dirStorage{root}, "alice", nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 — the allowance is an exact path, not a file type", path, w.Code)
		}
	}
}

// TestRobotsCarriesNoLicenceHeaders — robots.txt is a projection OF the terms,
// not a work covered BY them. A Content-Usage header on it would be a claim
// about the wrong resource.
func TestRobotsCarriesNoLicenceHeaders(t *testing.T) {
	root := t.TempDir()
	siteWithRobots(t, root)

	w := getRobots(t, root)

	if got := w.Header().Get("Content-Usage"); got != "" {
		t.Errorf("Content-Usage = %q, want empty", got)
	}
	if got := w.Header().Get("Link"); got != "" {
		t.Errorf("Link = %q, want empty", got)
	}
}
