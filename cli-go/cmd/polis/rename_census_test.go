package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ⛔⛔ A HAND-ROLLED TEMP-AND-RENAME IS NOT AN ATOMIC WRITE THE SITE CAN CARRY.
//
// atomicfile.Write takes the site and a key: temp-and-rename on a filesystem,
// one all-or-nothing Put on a store without one. ⭐ An os.Rename anywhere else
// is either a write that cannot run on a backend with no filesystem, or a
// MOVE (a one-time layout migration, an actor's quarantine), which is a
// different operation with its own reason.
//
// ⚠️ THREE WRITES HID HERE. The public index, the blessing list and
// sitemap.xml each did their own temp-and-rename on a path, so neither the
// atomicfile census (which looks for atomicfile.WriteFile) nor the site-dir
// census (which looks for Dir()/Path() on a handle) saw what they were. This
// test is the thing that would have.
//
// ⛔ THE EXEMPTIONS ARE NAMED, NOT IMPLIED, and each reason says what it is:
// a move, an actor, or a write still to convert. A new os.Rename is not
// added here to make the build pass: if it is a write, it goes through
// atomicfile.Write.
var renameAllowed = map[string]string{
	// ⭐ THE ONE IMPLEMENTATION OF TEMP-AND-RENAME. POSIX Put and atomicfile both
	// write through it, so it is the rename every write is SUPPOSED to use.
	"cli-go/pkg/storage/atomicwrite.go": "the atomic write itself: a unique temp file renamed over the target",

	// One-time layout MIGRATIONS: each moves a directory or file that an
	// older layout put somewhere else. An object store never had the old
	// layout, so it never reaches them.
	"cli-go/pkg/site/wellknown.go":     "one-time migration: moves .polis/content/<bundle>/* into .polis/bundles",
	"webapp/internal/server/server.go": "one-time migration: moves .polis/drafts into the bundle tree",
	"cli-go/pkg/stream/store.go":       "one-time migration: renames a stream state file to its new name",

	// A MOVE of a whole new site directory on the owner's machine: a branch
	// is staged beside its output and lands by rename, so a failure leaves
	// nothing at the output. Neither directory is a site the command owns yet.
	"cli-go/pkg/branch/branch.go": "move: a fully staged branched site into its output directory",

	// ACTORS: they do not run on a remote instance until A10, and A10 will
	// change these call paths anyway.
	"cli-go/pkg/medic/medic.go":        "actor: renames legacy registration files and moves files to quarantine; converts with A10",
	"cli-go/pkg/tailor/tailor.go":      "actor: tailor's own atomic moves, each with a Stays rationale; converts with A10",
	"webapp/internal/hosted/reaper.go": "actor, hosted only: writes an account archive under /data/archives, which is not a site",
}

func TestNoUnexplainedRename(t *testing.T) {
	found := map[string]bool{}
	scanned := 0
	for _, root := range []string{
		filepath.Join(repoRoot, "cli-go", "pkg"),
		filepath.Join(repoRoot, "cli-go", "cmd"),
		filepath.Join(repoRoot, "webapp", "internal"),
		filepath.Join(repoRoot, "webapp", "cmd"),
	} {
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(repoRoot, p)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "cli-go/pkg/atomicfile/") {
				return nil // the definition of the atomic write, not a caller
			}
			scanned++
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				if strings.Contains(line, "os.Rename(") {
					found[rel] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	// ⛔ NON-VACUITY: a census that scans nothing passes.
	if scanned < 200 {
		t.Fatalf("scanned only %d Go files; repoRoot (%q) is probably wrong", scanned, repoRoot)
	}

	var unexplained, stale []string
	for f := range found {
		if _, ok := renameAllowed[f]; !ok {
			unexplained = append(unexplained, f)
		}
	}
	for f := range renameAllowed {
		if !found[f] && !absentClosedSource(f) {
			stale = append(stale, f)
		}
	}
	sort.Strings(unexplained)
	sort.Strings(stale)
	for _, f := range unexplained {
		t.Errorf("%s calls os.Rename and is not named in renameAllowed. If it is a write, use "+
			"atomicfile.Write(ctx, site, key, …); if it is a move, name it with why.", f)
	}
	for _, f := range stale {
		t.Errorf("%s is named in renameAllowed but no longer calls os.Rename; remove the entry", f)
	}
}
