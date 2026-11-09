package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔⛔ NO VALIDATION ERROR MAY CARRY AN ABSOLUTE PATH, AND THIS IS A SECURITY
// CONTROL, NOT TIDINESS.
//
// R18-12 (2026-05-18): /api/status returned errors containing hosted-disk paths
// like /data/tenants/<handle>/.well-known/polis, handing the hosted filesystem
// layout to any owner session — and to any future XSS against the SPA.
//
// ⭐ The checks now address the site by KEY, so the relative form is what they
// produce rather than what a later pass strips off. ⚠️ scrubAbsolutePaths
// REMAINS as defence in depth: it is idempotent, and it is what catches an
// absolute path a future edit introduces. ⛔ Do not delete it on the grounds
// that paths are keys now.
//
// ⭐⭐ This test also proves the conversion was output-identical: the Paths it
// prints are exactly the relative forms a caller saw before.
func TestValidateReportsRelativePathsOnly(t *testing.T) {
	dir := t.TempDir()
	// a site with nothing in it: several checks fire at once
	if err := os.MkdirAll(filepath.Join(dir, ".polis"), 0o700); err != nil {
		t.Fatal(err)
	}
	result := Validate(storage.At(dir))
	if result == nil || len(result.Errors) == 0 {
		t.Fatal("an empty site produced no validation errors")
	}
	for _, e := range result.Errors {
		if e.Path == "" {
			continue
		}
		if filepath.IsAbs(e.Path) {
			t.Errorf("%s reported an ABSOLUTE path %q — R18-12 exists to stop exactly this "+
				"leaking the hosted filesystem layout to an owner session", e.Code, e.Path)
		}
		if strings.Contains(e.Path, dir) {
			t.Errorf("%s reported a path containing the site root: %q", e.Code, e.Path)
		}
	}
	t.Logf("%d errors, every Path relative", len(result.Errors))
	for _, e := range result.Errors {
		t.Logf("  %-28s %s", e.Code, e.Path)
	}
}
