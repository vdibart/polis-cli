package tailor

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setVersion sets the package Version for one test and restores it after, so
// it cannot leak into the golden tests that pin "vdev".
func setVersion(t *testing.T, v string) {
	t.Helper()
	prev := Version
	Version = v
	t.Cleanup(func() { Version = prev })
}

// snapshotFiles reads every regular file under dir, keyed by slash path.
func snapshotFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		data, _ := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out
}

// addLegacyClutter gives an old site one file for each kind of check that
// rewrites or removes something, beyond what createOldSite already carries.
func addLegacyClutter(t *testing.T, dir string) {
	t.Helper()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("site/themes/custom/custom.css", ":root { --color-bg: #123456; }\n")
	write(".polis/themes/old/old.css", "/* old */\n")
	write("metadata/public.jsonl", `{"path":"posts/20260227/test-post.md"}`+"\n")
	write(".polis/ds/ds.example/pub.polis.core/state/pub.polis.feed.me.jsonl", "{}\n")
	write(".polis/ds/ds.example/pub.polis.core/state/cursors.json",
		`{"cursors": {"pub.polis.feed.viewed_at": "2026-01-01T00:00:00Z", "pub.polis.feed.viewed": 3}}`)
	write(".polis/ds/ds.example/pub.polis.core/cache/blessed/a/orphan.md", "orphan\n")
	write(".polis/ds/ds.example/pub.polis.core/cache/blessed/a/orphan.md.meta.json", "{not json")
}

// derivedOutput is what a re-render regenerates and the run deliberately does
// not back up.
func derivedOutput(rel string) bool {
	return strings.HasSuffix(rel, ".html") || rel == "styles.css" || strings.HasSuffix(rel, ".js") || rel == "sitemap.xml"
}

// Every file an apply run rewrites or removes is in the run's backup directory
// with its original bytes — either at its own path or, if an earlier check
// moved it, at the path it was moved to.
func TestApplyBacksUpEveryFileItChangesOrRemoves(t *testing.T) {
	setVersion(t, "0.59.0")
	dir := createOldSite(t)
	addLegacyClutter(t, dir)
	before := snapshotFiles(t, dir)

	result := Apply(dir)
	if result.BackupDir == "" {
		t.Fatal("apply changed the site but reported no backup directory")
	}
	if info, err := os.Stat(result.BackupDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("backup dir: %v %v", err, info)
	}
	var m Manifest
	data, err := os.ReadFile(filepath.Join(result.BackupDir, backupManifestName))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	moves := map[string]string{} // original path (or dir prefix) → where it went
	for _, e := range m.Applied {
		for _, a := range e.Actions {
			if a.Op == "move" {
				moves[strings.TrimSuffix(a.Path, "/")] = strings.TrimSuffix(a.Dest, "/")
			}
		}
	}
	movedTo := func(rel string) string {
		for from, to := range moves {
			if rel == from {
				return to
			}
			if strings.HasPrefix(rel, from+"/") {
				return to + strings.TrimPrefix(rel, from)
			}
		}
		return ""
	}

	after := snapshotFiles(t, dir)
	backupRel, _ := filepath.Rel(dir, result.BackupDir)
	backupRel = filepath.ToSlash(backupRel)
	checked := 0
	for rel, orig := range before {
		if derivedOutput(rel) {
			continue
		}
		now, present := after[rel]
		if present && bytes.Equal(now, orig) {
			continue // untouched
		}
		path := rel
		if !present {
			if dest := movedTo(rel); dest != "" {
				if moved, ok := after[dest]; ok && bytes.Equal(moved, orig) {
					continue // moved intact
				}
				path = dest // moved, then changed: the backup holds it under its new path
			}
		}
		checked++
		saved, ok := after[backupRel+"/"+path]
		if !ok {
			t.Errorf("%s was changed or removed and is not in the backup (looked for %s)", rel, path)
			continue
		}
		if !bytes.Equal(saved, orig) {
			t.Errorf("backup of %s does not hold the original bytes:\nwant %q\ngot  %q", rel, orig, saved)
		}
	}
	if checked < 8 {
		t.Errorf("only %d changed files were checked; the fixture no longer exercises the backup", checked)
	}
	for _, e := range m.Applied {
		if e.Check == "" || len(e.Actions) == 0 {
			t.Errorf("manifest entry without a check or actions: %+v", e)
		}
	}
}

// A check whose backup cannot be written changes nothing.
func TestApplyDoesNotChangeAFileItCouldNotBackUp(t *testing.T) {
	dir := createOldSite(t)
	wk := filepath.Join(dir, ".well-known", "polis")
	before, _ := os.ReadFile(wk)

	// A FILE where the backup directory must go makes every backup write fail.
	blocker := filepath.Join(dir, ".polis", "tailor-backup")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := &runContext{siteDir: dir, backupDir: filepath.Join(blocker, "run"), generator: "polis-cli-go/test"}
	cr := applyWithBackup(ctx, checkWellKnownLegacyConfig)

	if cr.Status != StatusFail || !strings.Contains(cr.Message, "not applied") {
		t.Fatalf("result = %s %q, want a not-applied failure", cr.Status, cr.Message)
	}
	after, _ := os.ReadFile(wk)
	if !bytes.Equal(before, after) {
		t.Error(".well-known/polis changed although its backup failed")
	}
}

// A run that changes nothing but regenerated output leaves no backup directory.
func TestApplyOnAnUpToDateSiteLeavesNoBackupDirectory(t *testing.T) {
	setVersion(t, "0.59.0")
	dir := createOldSite(t)
	Apply(dir)
	entries, _ := os.ReadDir(filepath.Join(dir, ".polis", "tailor-backup"))
	first := len(entries)

	result := Apply(dir)
	if result.BackupDir != "" {
		t.Errorf("second apply reported backup dir %s", result.BackupDir)
	}
	entries, _ = os.ReadDir(filepath.Join(dir, ".polis", "tailor-backup"))
	if len(entries) != first {
		t.Errorf("second apply added a backup directory (%d → %d)", first, len(entries))
	}
}

// An apply that did not take is reported as such, decided by re-checking:
// a blocked backup is "failed", a real fix is "fixed", and a check that only
// reports is "reported" — never "fixed".
func TestApplyOutcomesAreDecidedByRechecking(t *testing.T) {
	setVersion(t, "0.59.0")
	dir := createOldSite(t)
	blocker := filepath.Join(dir, ".polis", "tailor-backup")
	os.WriteFile(blocker, []byte("x"), 0o600)
	ctx := &runContext{siteDir: dir, backupDir: filepath.Join(blocker, "run"), generator: "polis-cli-go/test"}
	if cr := applyWithBackup(ctx, checkWellKnownLegacyConfig); cr.Outcome != OutcomeFailed {
		t.Errorf("blocked backup: outcome %q, want failed", cr.Outcome)
	}
	os.Remove(blocker)

	res := Apply(dir)
	got := map[string]string{}
	for _, c := range res.Checks {
		got[c.Name] = c.Outcome
	}
	if got["wellknown-legacy-config"] != OutcomeFixed || got["layout-posts"] != OutcomeFixed {
		t.Errorf("fixes: %q %q", got["wellknown-legacy-config"], got["layout-posts"])
	}
	// The fixture's key file and published key are both a placeholder, but the
	// published key was never a real key; key-consistency only ever reports.
	if o := got["key-consistency"]; o != "" && o != OutcomeReported {
		t.Errorf("key-consistency outcome %q, want reported or none", o)
	}
	if res.Summary.Fixed == 0 || res.Summary.Fixed+res.Summary.NotApplied+res.Summary.Reported != res.Summary.Failed {
		t.Errorf("summary does not add up: %+v", res.Summary)
	}
}
