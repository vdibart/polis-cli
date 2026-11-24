package tailor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const ownCSS = ":root { --color-bg: #102030; --color-text: #f0e0d0; }\n"

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A user's own theme, installed where the themes guide says and set active,
// comes out of a full apply intact and still active.
func TestApplyKeepsTheUsersOwnActiveTheme(t *testing.T) {
	setVersion(t, "0.59.0")
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(storage.At(dir), "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	css := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "themes", "my-theme", "my-theme.css")
	writeFile(t, css, ownCSS)
	if err := bundle.SetActiveThemeName(storage.At(dir), "my-theme"); err != nil {
		t.Fatal(err)
	}

	diag := Diagnose(dir)
	for _, cr := range diag.Checks {
		for _, a := range cr.Actions {
			if a.Op != "flag" && strings.Contains(a.Path+a.Detail, "my-theme") {
				t.Errorf("dry run plans to change the user's theme: %s %+v", cr.Name, a)
			}
		}
	}

	Apply(dir)

	got, err := os.ReadFile(css)
	if err != nil || string(got) != ownCSS {
		t.Fatalf("own theme lost or changed: %v %q", err, got)
	}
	at, err := bundle.GetActiveThemeName(storage.At(dir))
	if err != nil || at != "my-theme" {
		t.Errorf("active theme = %q (%v), want my-theme", at, err)
	}
}

// A user's own theme in site/themes/ stays there (render reads it, and it is
// the only place a theme's snippets are read from); one in .polis/themes/,
// which nothing reads, moves to site/themes/. A shipped theme's old copy is
// removed and kept in the backup.
func TestApplyKeepsALegacyOwnThemeAndBacksUpShippedOnes(t *testing.T) {
	setVersion(t, "0.59.0")
	dir := createOldSite(t)
	inSite := filepath.Join(dir, "site", "themes", "my-theme", "my-theme.css")
	writeFile(t, inSite, ownCSS)
	writeFile(t, filepath.Join(dir, "site", "themes", "my-theme", "snippets", "about.md"), "mine\n")
	writeFile(t, filepath.Join(dir, ".polis", "themes", "other", "other.css"), ownCSS)
	writeFile(t, filepath.Join(dir, "site", "themes", "vice", "vice.css"), "/* hand-edited vice */\n")

	diag := Diagnose(dir)
	var legacy CheckResult
	for _, cr := range diag.Checks {
		if cr.Name == "legacy-theme-locations" {
			legacy = cr
		}
	}
	for _, want := range []string{"site/themes/my-theme", ".polis/themes/other"} {
		if !strings.Contains(legacy.Message, want) {
			t.Errorf("dry run does not name %s: %q", want, legacy.Message)
		}
	}

	result := Apply(dir)

	if got, err := os.ReadFile(inSite); err != nil || string(got) != ownCSS {
		t.Errorf("own theme in site/themes not kept: %v %q", err, got)
	}
	if _, err := os.Stat(filepath.Join(dir, "site", "themes", "my-theme", "snippets", "about.md")); err != nil {
		t.Errorf("own theme's snippets lost: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "site", "themes", "other", "other.css")); err != nil || string(got) != ownCSS {
		t.Errorf("own theme from .polis/themes not moved to site/themes: %v %q", err, got)
	}
	if _, err := os.Stat(filepath.Join(dir, "site", "themes", "vice")); err == nil {
		t.Error("old copy of shipped vice still in site/themes")
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis", "themes")); err == nil {
		t.Error(".polis/themes still present")
	}
	backedUp := filepath.Join(result.BackupDir, "site", "themes", "vice", "vice.css")
	if got, err := os.ReadFile(backedUp); err != nil || string(got) != "/* hand-edited vice */\n" {
		t.Errorf("edited shipped legacy theme not in the backup: %v %q", err, got)
	}
	// A second dry run is clean for this check: a kept own theme is not drift.
	for _, cr := range Diagnose(dir).Checks {
		if cr.Name == "legacy-theme-locations" && cr.Status != StatusPass {
			t.Errorf("after apply: %s %q", cr.Status, cr.Message)
		}
	}
}

// An edited shipped file is reinstalled, its edited bytes are in the backup,
// and the result says where.
func TestApplyKeepsAnEditedShippedFileInTheBackup(t *testing.T) {
	setVersion(t, "0.59.0")
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(storage.At(dir), "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(".polis", "bundles", "pub.polis.core", "themes", "vice", "vice.css")
	shipped, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	edited := append(append([]byte{}, shipped...), []byte("\n/* my tweak */\n")...)
	if err := os.WriteFile(filepath.Join(dir, rel), edited, 0o644); err != nil {
		t.Fatal(err)
	}

	result := Apply(dir)

	var cr CheckResult
	for _, c := range result.Checks {
		if c.Name == "reference-payload-integrity" {
			cr = c
		}
	}
	if rel, _ := filepath.Rel(dir, result.BackupDir); !strings.Contains(cr.Message, filepath.ToSlash(rel)) {
		t.Errorf("result does not name the backup: %q", cr.Message)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, rel)); !bytes.Equal(got, shipped) {
		t.Error("shipped file not reinstalled")
	}
	if got, err := os.ReadFile(filepath.Join(result.BackupDir, rel)); err != nil || !bytes.Equal(got, edited) {
		t.Errorf("edited copy not in the backup: %v", err)
	}
}

// An unlisted theme directory polis never shipped is the user's, and is kept.
func TestOrphanedThemeDirsKeepsTheUsersOwnTheme(t *testing.T) {
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(storage.At(dir), "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	own := plantOrphanThemeDir(t, dir, "my-theme")

	result := checkOrphanedThemeDirs(&runContext{siteDir: dir, dryRun: false})
	if result.Status != StatusPass || !strings.Contains(result.Message, "my-theme") {
		t.Errorf("got %s %q, want a pass naming the kept theme", result.Status, result.Message)
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("own theme removed: %v", err)
	}
}

// Pre-0.58 private drafts and pending comments move to where current code
// reads them; one that would land on an existing file stays and is reported.
func TestLayoutPrivateDraftsMovesWithoutOverwriting(t *testing.T) {
	dir := createOldSite(t)
	writeFile(t, filepath.Join(dir, ".polis", "comments", "pending", "reply.md"), "pending\n")
	writeFile(t, filepath.Join(dir, ".polis", "posts", "drafts", "draft.md"), "draft\n")
	writeFile(t, filepath.Join(dir, ".polis", "comments", "drafts", "clash.md"), "old\n")
	writeFile(t, filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments", "drafts", "clash.md"), "new\n")

	cr := applyWithBackup(&runContext{siteDir: dir, backupDir: filepath.Join(dir, ".polis", "tailor-backup", "t")}, checkLayoutPrivateDrafts)

	if cr.Status != StatusFail {
		t.Fatalf("got %s %q", cr.Status, cr.Message)
	}
	for rel, want := range map[string]string{
		".polis/bundles/pub.polis.core/comments/pending/reply.md": "pending\n",
		".polis/bundles/pub.polis.core/posts/drafts/draft.md":     "draft\n",
		".polis/bundles/pub.polis.core/comments/drafts/clash.md":  "new\n",
		".polis/comments/drafts/clash.md":                         "old\n",
	} {
		if got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want %q", rel, got, err, want)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("created drafts dir: %v %v", err, info)
	}
}

// The payload checks rewrite only shapes/ and themes/, so that is all they back
// up. A backup never holds key material: not the DM keyring, its
// conversations, the identity keys or the storage salt.
func TestBackupNeverCopiesKeyMaterial(t *testing.T) {
	setVersion(t, "0.59.0")
	dir := createCurrentSite(t)
	if err := bundle.EnsureReferencePayload(storage.At(dir), "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "dm", "conversations", "c1.json"), "{}")
	// Force both payload checks to apply: an edited shipped file.
	css := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "themes", "vice", "vice.css")
	writeFile(t, css, "/* edited */\n")

	result := Apply(dir)
	if result.BackupDir == "" {
		t.Fatal("expected a backup")
	}
	if _, err := os.Stat(filepath.Join(result.BackupDir, ".polis", "bundles", "pub.polis.core", "themes", "vice", "vice.css")); err != nil {
		t.Errorf("edited shipped file not backed up: %v", err)
	}
	filepath.WalkDir(result.BackupDir, func(p string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(result.BackupDir, p)
		if secretPath(filepath.ToSlash(rel)) && rel != ".polis" && rel != filepath.Join(".polis", "bundles") && rel != filepath.Join(".polis", "bundles", "pub.polis.core") {
			t.Errorf("backup holds key material: %s", rel)
		}
		return nil
	})
}

// An own theme moved from .polis/themes into an EMPTY site/themes survives the
// removal of the empty legacy tree; one whose name clashes is left in place.
func TestLegacyThemeMoveIntoEmptySiteThemesSurvives(t *testing.T) {
	dir := createOldSite(t)
	os.MkdirAll(filepath.Join(dir, "site", "themes"), 0o755)
	writeFile(t, filepath.Join(dir, ".polis", "themes", "mine", "mine.css"), ownCSS)

	cr := applyWithBackup(&runContext{siteDir: dir, backupDir: filepath.Join(dir, ".polis", "tailor-backup", "t")}, checkLegacyThemeLocations)
	if got, err := os.ReadFile(filepath.Join(dir, "site", "themes", "mine", "mine.css")); err != nil || string(got) != ownCSS {
		t.Fatalf("moved own theme lost: %v (%s %q)", err, cr.Status, cr.Message)
	}

	// A clash: the copy in .polis/themes stays, and the check says so.
	writeFile(t, filepath.Join(dir, ".polis", "themes", "mine", "mine.css"), "/* other copy */\n")
	cr = applyWithBackup(&runContext{siteDir: dir, backupDir: filepath.Join(dir, ".polis", "tailor-backup", "t2")}, checkLegacyThemeLocations)
	if _, err := os.Stat(filepath.Join(dir, ".polis", "themes", "mine", "mine.css")); err != nil {
		t.Errorf("clashing own theme removed: %v", err)
	}
	if cr.Status != StatusFail || !strings.Contains(cr.Message, "reconcile") {
		t.Errorf("clash not reported: %s %q", cr.Status, cr.Message)
	}
}

// A site with both pre-0.58 drafts and a 0.58–0.62 .polis/content tree: a full
// run moves both, because legacy-content-path runs before the drafts move.
func TestLegacyContentPathAndPrivateDraftsBothMove(t *testing.T) {
	setVersion(t, "0.59.0")
	dir := createOldSite(t)
	writeFile(t, filepath.Join(dir, ".polis", "posts", "drafts", "a.md"), "a\n")
	writeFile(t, filepath.Join(dir, ".polis", "content", "pub.polis.core", "posts", "drafts", "b.md"), "b\n")

	Apply(dir)
	for _, rel := range []string{"a.md", "b.md"} {
		if _, err := os.Stat(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", rel)); err != nil {
			t.Errorf("draft %s not at its current path: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".polis", "content")); err == nil {
		t.Error(".polis/content still present")
	}
}
