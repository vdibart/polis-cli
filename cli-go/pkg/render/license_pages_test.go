package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// The renderer is one of TWO writers of robots.txt and rsl.xml; Medic is the
// other, and it restores them from the same signed licence. If the renderer
// adds anything of its own, Medic's content comparison sees a mismatch it can
// never resolve and the two rewrite the file at each other forever.
//
// So the contract asserted here is not "the renderer produces good content" —
// it is "the renderer produces EXACTLY site.LicenseProjections' content, and
// nothing else." That function is where the content is decided, for both.
func TestRenderedProjectionsAreExactlyTheSharedGeneration(t *testing.T) {
	dir := t.TempDir()
	setupTestSite(t, dir)

	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	if stated, err := site.StateLicense(dir, "reserved", "https://example.com", priv); err != nil || !stated {
		t.Fatalf("StateLicense: stated=%v err=%v", stated, err)
	}

	// A work that diverges from the site default — the input that produces a
	// per-path rule, and the one Medic used to be blind to.
	postDir := filepath.Join(dir, "content", "pub.polis.core", "post", "2026")
	os.MkdirAll(postDir, 0755)
	os.WriteFile(filepath.Join(postDir, "20260828-open.md"), []byte(
		"---\ntitle: Open\npublished: 2026-08-28T12:00:00Z\nlicense:\n"+
			"  v: "+license.SchemaVersion+"\n"+
			"  profile: "+license.ProfileOpen+"\n"+
			"  train-ai: y\n  search: y\n  ai-input: y\n"+
			"  terms: https://example.com/license\n"+
			"  asserted: 2026-08-28T12:00:00Z\n---\n\nBody.\n"), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir:        dir,
		BaseURL:        "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post",
		PostsMountDir:  "posts",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if err := renderer.RenderLicenseSurfaces(); err != nil {
		t.Fatalf("RenderLicenseSurfaces: %v", err)
	}

	wantRobots, wantRSL, err := site.LicenseProjections(dir, "https://example.com")
	if err != nil {
		t.Fatalf("LicenseProjections: %v", err)
	}

	for _, c := range []struct {
		rel  string
		want []byte
	}{
		{"robots.txt", wantRobots},
		{"rsl.xml", wantRSL},
	} {
		got, err := os.ReadFile(filepath.Join(dir, c.rel))
		if err != nil {
			t.Fatalf("read %s: %v", c.rel, err)
		}
		if string(got) != string(c.want) {
			t.Errorf("%s: the renderer wrote something other than the shared generation\n--- rendered ---\n%s\n--- shared ---\n%s", c.rel, got, c.want)
		}
	}

	// The two inputs whose absence from Medic's side was the write war.
	robots := string(wantRobots)
	if !strings.Contains(robots, "Sitemap: https://example.com/sitemap.xml") {
		t.Errorf("robots.txt lost its Sitemap line:\n%s", robots)
	}
	if !strings.Contains(robots, "Content-Usage: /posts/2026/20260828-open.html train-ai=y") {
		t.Errorf("robots.txt lost the per-work override rule:\n%s", robots)
	}
}

// A site that has stated nothing gets nothing — not an empty robots.txt, not a
// terms page saying "no terms". The host must not speak for the user.
func TestUnstatedSiteRendersNoLicenceSurfaces(t *testing.T) {
	dir := t.TempDir()
	setupTestSite(t, dir)

	renderer, err := NewPageRenderer(PageConfig{DataDir: dir, BaseURL: "https://example.com"})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if err := renderer.RenderLicenseSurfaces(); err != nil {
		t.Fatalf("RenderLicenseSurfaces: %v", err)
	}

	for _, rel := range []string{"robots.txt", "rsl.xml"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s was written for a site that stated no terms", rel)
		}
	}
}

// ⭐ THE ROOT CAUSE, asserted where it lived: the v4 empty-corpus branch.
//
// A brand-new tenant has zero posts. Hosted signup applies the `reserved`
// licence and renders immediately, so every signup took the branch that
// returned before RenderLicenseSurfaces — publishing an rsl.xml whose <terms>
// URL 404s, forever, because nothing else ever writes that page.
//
// Terms are a statement a site makes about itself, not about its corpus. A
// site with no posts still owes the page its own machine surfaces point at.
func TestZeroPostStreamSiteRendersItsTermsPage(t *testing.T) {
	dir := t.TempDir()
	setupStreamTestSite(t, dir)

	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	if stated, err := site.StateLicense(dir, "reserved", "https://example.com", priv); err != nil || !stated {
		t.Fatalf("StateLicense: stated=%v err=%v", stated, err)
	}

	renderer, err := NewPageRenderer(PageConfig{DataDir: dir, BaseURL: "https://example.com"})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	stats, err := renderer.RenderAll(true)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if stats.PostsRendered != 0 {
		t.Fatalf("fixture is not zero-post: %d posts rendered", stats.PostsRendered)
	}

	mount := site.LicenseMountDir(dir)
	for _, rel := range []string{
		filepath.Join(mount, "index.html"),
		filepath.Join(mount, "reserved-1.html"),
		"robots.txt",
		"rsl.xml",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("%s: a zero-post site that stated terms must still publish this — %v", rel, err)
		}
	}

	// The specific defect: rsl.xml advertises a terms URL, and that URL must
	// resolve to something on disk.
	rsl := mustReadFile(t, filepath.Join(dir, "rsl.xml"))
	if !strings.Contains(rsl, "<terms>https://example.com/"+mount+"</terms>") {
		t.Errorf("rsl.xml does not advertise the terms mount %q:\n%s", mount, rsl)
	}
}

// The mirror: withdrawal removes every projection it created, so the site is
// indistinguishable from one that never stated terms.
//
// ⛔ Remove, do not blank. An empty robots.txt is a statement; an absent one is
// the pre-terms state, and that is what withdrawal returns a site to.
func TestWithdrawalLeavesNoProjectionBehind(t *testing.T) {
	dir := t.TempDir()
	setupStreamTestSite(t, dir)

	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	if stated, err := site.StateLicense(dir, "reserved", "https://example.com", priv); err != nil || !stated {
		t.Fatalf("StateLicense: stated=%v err=%v", stated, err)
	}
	renderer, err := NewPageRenderer(PageConfig{DataDir: dir, BaseURL: "https://example.com"})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if err := renderer.RenderLicenseSurfaces(); err != nil {
		t.Fatalf("RenderLicenseSurfaces: %v", err)
	}

	mount := site.LicenseMountDir(dir)
	// Sanity: the surfaces we are about to withdraw actually exist.
	for _, rel := range []string{filepath.Join(mount, "index.html"), "robots.txt", "rsl.xml"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("fixture: %s was never written — %v", rel, err)
		}
	}

	if err := site.WithdrawLicense(dir); err != nil {
		t.Fatalf("WithdrawLicense: %v", err)
	}

	for _, rel := range []string{
		"robots.txt",
		"rsl.xml",
		filepath.Join(mount, "index.html"),
		filepath.Join(mount, "reserved-1.html"),
		mount,
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s survived withdrawal — the site still asserts terms it no longer states", rel)
		}
	}
	terms, err := site.SiteTerms(dir)
	if err != nil || terms != nil {
		t.Errorf("after withdrawal SiteTerms = %v, %v; want nil, nil", terms, err)
	}
}

// A mount the author has put their own files in is not ours to delete. The
// generated pages go; anything else keeps the directory alive.
func TestWithdrawalSparesAuthorFilesInTheMount(t *testing.T) {
	dir := t.TempDir()
	setupStreamTestSite(t, dir)

	priv, _, _ := signing.GenerateKeypair()
	if stated, err := site.StateLicense(dir, "reserved", "https://example.com", priv); err != nil || !stated {
		t.Fatalf("StateLicense: stated=%v err=%v", stated, err)
	}
	renderer, _ := NewPageRenderer(PageConfig{DataDir: dir, BaseURL: "https://example.com"})
	if err := renderer.RenderLicenseSurfaces(); err != nil {
		t.Fatalf("RenderLicenseSurfaces: %v", err)
	}

	mount := site.LicenseMountDir(dir)
	keep := filepath.Join(dir, mount, "NOTES.txt")
	if err := os.WriteFile(keep, []byte("mine\n"), 0644); err != nil {
		t.Fatalf("write author file: %v", err)
	}

	if err := site.WithdrawLicense(dir); err != nil {
		t.Fatalf("WithdrawLicense: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("an author's own file in the licence mount was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, mount, "index.html")); !os.IsNotExist(err) {
		t.Error("the generated terms page survived withdrawal")
	}
}

// Withdrawing from a site that never stated terms is a no-op, not an error —
// and it must not delete a robots.txt that was never ours.
func TestWithdrawalOnAnUnstatedSiteIsHarmless(t *testing.T) {
	dir := t.TempDir()
	setupStreamTestSite(t, dir)

	if err := site.WithdrawLicense(dir); err != nil {
		t.Fatalf("WithdrawLicense on an unstated site: %v", err)
	}
}
