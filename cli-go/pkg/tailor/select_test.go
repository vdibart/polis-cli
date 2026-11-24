package tailor

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every declared id is the name its check reports, so --list, --only and the
// run agree, and no id is declared twice.
func TestCheckTableIDsMatchTheChecks(t *testing.T) {
	dir := createOldSite(t)
	ctx := &runContext{siteDir: dir, dryRun: true, baseURL: extractBaseURL(dir), generator: GetGenerator()}
	seen := map[string]bool{}
	defs := Checks()
	for _, d := range defs {
		if seen[d.ID] {
			t.Errorf("duplicate id %s", d.ID)
		}
		seen[d.ID] = true
		if got := d.fn(ctx).Name; got != d.ID {
			t.Errorf("table says %q, check reports %q", d.ID, got)
		}
		if d.Summary == "" || d.Phase == "" || d.Kind == "" {
			t.Errorf("%s: incomplete declaration %+v", d.ID, d)
		}
		for _, dep := range d.DependsOn {
			if !seen[dep] {
				t.Errorf("%s depends on %s, which does not run before it", d.ID, dep)
			}
		}
	}
	if len(defs) != len(Diagnose(dir).Checks) {
		t.Errorf("table has %d checks, a run has %d", len(defs), len(Diagnose(dir).Checks))
	}
}

func TestOnlyAndSkipRunExactlyTheSelection(t *testing.T) {
	dir := createOldSite(t)
	res, err := DiagnoseWith(dir, Options{Only: []string{"webapp-view-mode", "tag-directory"}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range res.Checks {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "tag-directory,webapp-view-mode" {
		t.Errorf("only ran %s, want tag-directory,webapp-view-mode in run order", got)
	}

	res, err = DiagnoseWith(dir, Options{Skip: []string{"cli-update"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Checks) != len(Checks())-1 {
		t.Errorf("skip ran %d checks, want %d", len(res.Checks), len(Checks())-1)
	}
	for _, c := range res.Checks {
		if c.Name == "cli-update" {
			t.Error("skipped check ran")
		}
	}

	// Apply honours the selection too: only the tag directory appears.
	if _, err := ApplyWith(dir, Options{Only: []string{"tag-directory"}}); err != nil {
		t.Fatal(err)
	}
	if !dirExists(filepath.Join(dir, "content", "pub.polis.core", "tag")) {
		t.Error("selected check did not apply")
	}
	if _, err := os.Stat(filepath.Join(dir, "metadata", "manifest.json")); err != nil {
		t.Error("an unselected cleanup check applied")
	}
}

func TestApplyRefusesASelectionMissingAnUnmetPrerequisite(t *testing.T) {
	dir := createOldSite(t) // posts still in posts/: layout-posts has not happened
	before := snapshotFiles(t, dir)

	_, err := ApplyWith(dir, Options{Only: []string{"index-rebuild"}})
	var se *SelectionError
	if !errors.As(err, &se) || !strings.Contains(se.Msg, "index-rebuild needs layout-posts") {
		t.Fatalf("err = %v, want a refusal naming layout-posts", err)
	}
	after := snapshotFiles(t, dir)
	if len(after) != len(before) {
		t.Errorf("refused apply changed the site (%d → %d files)", len(before), len(after))
	}

	// Once the prerequisite has been applied, the same selection is accepted.
	if _, err := ApplyWith(dir, Options{Skip: []string{"cli-update"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyWith(dir, Options{Only: []string{"index-rebuild"}}); err != nil {
		t.Errorf("selection refused after its prerequisites passed: %v", err)
	}
}

func TestSelectionUsageErrors(t *testing.T) {
	dir := createOldSite(t)
	for _, o := range []Options{
		{Only: []string{"no-such-check"}},
		{Skip: []string{"nope"}},
		{Only: []string{"tag-directory"}, Skip: []string{"cli-update"}},
	} {
		var se *SelectionError
		if _, err := DiagnoseWith(dir, o); !errors.As(err, &se) {
			t.Errorf("%+v: err = %v, want a SelectionError", o, err)
		}
	}
}

// cli-update only reports: a newer release is a finding naming where to get
// it, and nothing is written anywhere.
func TestCLIUpdateReportsANewerReleaseAndDownloadsNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	defer srv.Close()
	prevURL, prevVer := latestReleaseURL, Version
	latestReleaseURL, Version = srv.URL, "0.70.0"
	defer func() { latestReleaseURL, Version = prevURL, prevVer }()

	dir := createCurrentSite(t)
	before := snapshotFiles(t, dir)
	res, err := ApplyWith(dir, Options{Only: []string{"cli-update"}})
	if err != nil {
		t.Fatal(err)
	}
	cr := res.Checks[0]
	if cr.Status != StatusFail || !strings.Contains(cr.Message, "v9.9.9 is available") {
		t.Errorf("got %s %q", cr.Status, cr.Message)
	}
	if after := snapshotFiles(t, dir); len(after) != len(before) {
		t.Errorf("cli-update wrote files (%d → %d)", len(before), len(after))
	}

	Version = "9.9.9"
	if cr := checkCLIUpdate(&runContext{siteDir: dir}); cr.Status != StatusPass {
		t.Errorf("current release: %s %q", cr.Status, cr.Message)
	}
}
