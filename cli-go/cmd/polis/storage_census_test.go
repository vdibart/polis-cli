package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ⛔⛔ THE CENSUS. A direct filesystem call in a detached package must carry a
// reason at the site, and a NEW one must fail the build.
//
// This exists because "in scope" was never written down. The detach's parent
// ticket claimed 164 sites left direct with 22 undocumented; an independent census
// measured 255 and 112, and neither number could be reproduced from the other
// because each used a different, unstated package list. Worse, pkg/version was
// named in a subtask's own description and silently skipped — and that subtask is
// now closed and uneditable.
//
// ⭐ So scope is DATA here, not prose. detachedPackages is the definition, and
// adding a package to it is how a future cluster declares itself done.
//
// ⚠️ A site stays legal by SAYING WHY, in one of the recognised phrases below,
// within its enclosing function. That is deliberately cheap: the point is that a
// reader meets the reason where the decision was made, not in a ticket that may
// already be locked.
var detachedPackages = []string{
	// cli-go — the eight subtask clusters, plus pkg/version which .03 named.
	"pkg/cmd", "pkg/sitecheck", "pkg/clone",
	// ⚠️ pkg/sitecheck/fleettest is EXCLUDED below: it is a test harness that
	// happens not to live in a _test.go file, and its own doc says nothing outside
	// tests imports it. Annotating fixture code no shipped binary runs would be
	// noise, so the census skips it by name instead of pretending it is detached.
	"pkg/render", "pkg/theme", "pkg/snippet", "pkg/template",
	"pkg/publish", "pkg/comment", "pkg/metadata", "pkg/dm", "pkg/tag", "pkg/index", "pkg/stream",
	"pkg/patrol", "pkg/medic", "pkg/judge", "pkg/clerk", "pkg/chaplain", "pkg/tailor",
	"pkg/cache/upkeep", "pkg/actor", "pkg/agent", "pkg/attestation",
	"pkg/site", "pkg/license", "pkg/bundle", "pkg/following", "pkg/policy",
	"pkg/hooks", "pkg/notification", "pkg/feed", "pkg/ops", "pkg/version",
	// ⭐ Added 2026-10-02 by Vincent's scope ruling. These four were found by a
	// review to have direct calls and sit OUTSIDE this list, so a bare call in
	// them failed nothing — verified by injecting os.ReadFile into pkg/cache and
	// watching the census pass.
	"pkg/cache", "pkg/discovery", "pkg/sitemap", "pkg/signing",
	// Written storage-first from the start.
	"pkg/seen", "pkg/apidesc",
}

// detachedWebappPackages are the webapp side of the same list.
var detachedWebappPackages = []string{"internal/server", "internal/hosted"}

// reasonPhrases are the forms that count as a stated reason. Keep the list short:
// a reason that needs a new phrase probably needs a sentence instead.
var reasonPhrases = []string{
	"NOT the storage interface",
	"Stays:",
	"PERMISSION RULE",
	"Depends on ModTimes",
}

// ⛔ THE CALL LIST IS THE GUARD'S REACH, and the first version missed a third of
// it. An independent AST census found 245 direct calls where this matched 173:
// os.Chmod, CreateTemp, MkdirTemp, Mkdir, Symlink, Readlink, Truncate, Link and
// the rest were simply absent, so a new one in any of those forms failed nothing.
//
// ⚠️ What it still cannot see, stated rather than implied: an ALIASED os import,
// a call made through a function value, and a call split across lines. Those want
// an AST walk rather than a regex, which is a bigger change than this ticket.
var fsCallRe = regexp.MustCompile(
	`\bos\.(ReadFile|WriteFile|Stat|Lstat|MkdirAll|Mkdir|Remove|RemoveAll|ReadDir|Open|OpenFile|` +
		`Create|CreateTemp|MkdirTemp|Rename|Chmod|Chown|Symlink|Readlink|Link|Truncate|OpenRoot|DirFS)\(` +
		`|\bfilepath\.(Walk|WalkDir|Glob|EvalSymlinks)\(` +
		`|\bioutil\.\w+\(`)

func TestEveryDirectFilesystemCallInADetachedPackageSaysWhy(t *testing.T) {
	type site struct{ file, line, call string }
	var bare []site
	scanned, total := 0, 0

	check := func(root string, pkgs []string) {
		for _, pkg := range pkgs {
			dir := filepath.Join(root, pkg)
			if _, err := os.Stat(dir); err != nil {
				if rel, rerr := filepath.Rel(repoRoot, dir); rerr == nil && absentClosedSource(filepath.ToSlash(rel)) {
					continue // closed source, not in the public tree
				}
				t.Errorf("detachedPackages names %s, which does not exist — the list is stale", pkg)
				continue
			}
			_ = filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".go") ||
					strings.HasSuffix(path, "_test.go") ||
					strings.Contains(filepath.ToSlash(path), "/fleettest/") {
					return nil
				}
				b, rErr := os.ReadFile(path)
				if rErr != nil {
					return nil
				}
				scanned++
				lines := strings.Split(string(b), "\n")
				// The reason may sit anywhere in the enclosing function, because a
				// block of related calls is explained once at its head.
				// ⭐ fnStart walks BACK over the doc-comment block above the func,
				// because that is where Go convention puts the reason — requiring it
				// inside the body would force unidiomatic code to satisfy a test.
				fnStart := 0
				for i, line := range lines {
					if strings.HasPrefix(line, "func ") {
						fnStart = i
						for j := i - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "//"); j-- {
							fnStart = j
						}
					}
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "//") || !fsCallRe.MatchString(line) {
						continue
					}
					total++
					window := strings.Join(lines[fnStart:i+1], "\n")
					stated := false
					for _, p := range reasonPhrases {
						if strings.Contains(window, p) {
							stated = true
							break
						}
					}
					if !stated {
						rel, _ := filepath.Rel(root, path)
						bare = append(bare, site{rel, strconv.Itoa(i + 1), strings.TrimSpace(line)})
					}
				}
				return nil
			})
		}
	}

	// ⛔ repoRoot, NOT "..": this test lives in cmd/polis, so ".." is cmd/. The
	// first version used ".." and scanned ZERO files — the same mistake made in the
	// error-vocabulary guard and written up as a lesson, repeated here in the very
	// test added to stop guards being lax. Only the non-vacuity check below caught
	// it, which is the argument for that check being mandatory.
	check(filepath.Join(repoRoot, "cli-go"), detachedPackages)
	check(filepath.Join(repoRoot, "webapp"), detachedWebappPackages)

	// ⛔ A census that scans nothing passes forever.
	if scanned < 50 || total < 20 {
		t.Fatalf("scanned %d files and %d calls; the census is not looking at the corpus", scanned, total)
	}
	t.Logf("census: %d direct calls across %d files in %d declared packages",
		total, scanned, len(detachedPackages)+len(detachedWebappPackages))

	if len(bare) > 0 {
		var b strings.Builder
		for _, s := range bare {
			b.WriteString("\n  " + s.file + ":" + s.line + "  " + s.call)
		}
		t.Errorf("%d direct filesystem call(s) in a DETACHED package carry no stated reason. "+
			"Either route them through pkg/storage, or say why they stay — one of %q — in the "+
			"enclosing function:%s", len(bare), reasonPhrases, b.String())
	}
}
