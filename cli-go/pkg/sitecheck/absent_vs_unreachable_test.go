package sitecheck

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// blockWithAFile puts a regular FILE where a directory is expected, so every
// stat beneath it fails with "not a directory" — a failure that is NOT an
// absence, and one a test can stage without chmod or root.
func blockWithAFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ⛔ "Not installed" means the bundle directory is GENUINELY absent. One that
// cannot be reached must go on to the comparison and report drift: reading every
// failure as absent made Patrol call such a tenant healthy, made Tailor pass it,
// and stopped Medic attempting the reinstall.
func TestReferencePayloadDriftTreatsOnlyAGenuineAbsenceAsNotInstalled(t *testing.T) {
	absent := t.TempDir()
	if _, installed, err := ReferencePayloadDrift(absent); installed || err != nil {
		t.Errorf("no bundle directory: installed=%v err=%v, want false and nil", installed, err)
	}

	blocked := t.TempDir()
	blockWithAFile(t, filepath.Join(blocked, ".polis", "bundles"))
	mismatches, installed, _ := ReferencePayloadDrift(blocked)
	if !installed {
		t.Fatal("an unreachable bundle directory was reported as not installed")
	}
	if len(mismatches) == 0 {
		t.Error("an unreachable bundle directory compared clean against the reference payload")
	}
}

// The DS directory checks keep "there is none" apart from "I could not look".
func TestStaleFeedChecksReportAnUnreachableDSDirectoryAsItself(t *testing.T) {
	absent := t.TempDir()
	if _, err := StaleScopedFeedFiles(absent); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no DS directory: err = %v, want not-exist", err)
	}
	if _, err := StaleFeedViewedAtFiles(absent); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no DS directory: err = %v, want not-exist", err)
	}

	blocked := t.TempDir()
	blockWithAFile(t, filepath.Join(blocked, ".polis"))
	if _, err := StaleScopedFeedFiles(blocked); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unreachable DS directory: err = %v, want a failure that is not not-exist", err)
	}
	if _, err := StaleFeedViewedAtFiles(blocked); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unreachable DS directory: err = %v, want a failure that is not not-exist", err)
	}
}

// ⛔ Medic matches the words "missing on disk" to decide whether to REWRITE the
// core bundle path in .well-known/polis. So only a bundle path that is genuinely
// absent may say them: a path that leads through a regular file, or one that
// points outside the site, is a different problem and must not trigger a heal.
func TestOnlyAGenuinelyMissingBundlePathSaysMissingOnDisk(t *testing.T) {
	siteDir := t.TempDir()
	blockWithAFile(t, filepath.Join(siteDir, "content", "afile"))
	outside := filepath.Join(filepath.Dir(siteDir), "outside-bundle.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	problem := func(path string) string {
		wk := &site.WellKnown{Bundles: map[string]site.BundleEntry{"pub.polis.core": {Path: path}}}
		ps := BundlePathProblems(siteDir, wk)
		if len(ps) == 0 {
			return ""
		}
		return ps[0].String()
	}

	if got := problem("content/pub.polis.core/bundle.json"); !strings.Contains(got, "missing on disk") {
		t.Errorf("a missing bundle: %q, want it to say missing on disk", got)
	}
	if got := problem("content/afile/bundle.json"); got == "" || strings.Contains(got, "missing on disk") {
		t.Errorf("a path through a regular file: %q, want a problem that does NOT say missing on disk", got)
	}
	if got := problem("../outside-bundle.json"); strings.Contains(got, "missing on disk") {
		t.Errorf("a path outside the site: %q, must not say missing on disk", got)
	}
}
