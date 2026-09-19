package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// `polis license none` must leave nothing behind.
//
// The command used to call site.WithdrawLicense and return, so robots.txt,
// rsl.xml and the terms page went on asserting terms the site no longer stated
// — published, machine-readable, and with nothing anywhere that would ever
// take them down. Vincent found it with one question: "polis license none will
// clean up all the necessary config/generated files correct?"
//
// The end state asserted here is the one that matters: after withdrawal the
// site is indistinguishable from one that never stated terms.
func TestCLIWithdrawalRemovesEveryProjection(t *testing.T) {
	dir, priv := setupUnpublishSite(t)
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("install reference payload: %v", err)
	}

	if stated, err := site.StateLicense(dir, "reserved", "https://example.com", priv); err != nil || !stated {
		t.Fatalf("StateLicense: stated=%v err=%v", stated, err)
	}
	renderer, err := render.NewPageRenderer(render.PageConfig{DataDir: dir, BaseURL: "https://example.com"})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if err := renderer.RenderLicenseSurfaces(); err != nil {
		t.Fatalf("RenderLicenseSurfaces: %v", err)
	}

	mount := site.LicenseMountDir(dir)
	surfaces := []string{
		"robots.txt",
		"rsl.xml",
		filepath.Join(mount, "index.html"),
		filepath.Join(mount, "reserved-1.html"),
	}
	for _, rel := range surfaces {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("fixture: %s was never written — %v", rel, err)
		}
	}

	// Drive the real command, not the package underneath it.
	prevDataDir, prevJSON := dataDir, jsonOutput
	t.Cleanup(func() { dataDir, jsonOutput = prevDataDir, prevJSON })
	dataDir, jsonOutput = dir, false
	handleLicense([]string{"none"})

	for _, rel := range append(surfaces, mount) {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s survived `polis license none`", rel)
		}
	}
	if terms, err := site.SiteTerms(dir); err != nil || terms != nil {
		t.Errorf("after `polis license none` SiteTerms = %v, %v; want nil, nil", terms, err)
	}
	if p := site.LicensePointer(dir); p != "" {
		t.Errorf("licence pointer survived withdrawal: %q", p)
	}
}
