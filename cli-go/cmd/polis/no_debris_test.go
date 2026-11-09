package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ NO SITE-SHAPED TREE LIVES INSIDE A SOURCE PACKAGE.
//
// A test that writes relative to its working directory (a key handed to a path
// helper, a site rooted at "") leaves a `.polis/`, `content/pub.polis.core/` or
// `.well-known/` tree inside the package it ran in — and `git add` of the
// package then commits it. Thirty-nine such files were committed during the
// site-handle conversion and found later by review. This walks the source
// trees (committed or not) so a leftover fails here, the next time anyone
// runs the suite.
func TestNoSiteShapedTreeInsideASourcePackage(t *testing.T) {
	// ⭐ Every directory a site has, not just its private ones: posts/,
	// comments/ and snippets/ are what a render leaves, and content/<any>/ is
	// what a bundle type writes. No source package has a directory by these
	// names outside testdata/ and fixtures/.
	shapes := []string{"/.polis/", "/content/", "/.well-known/", "/posts/", "/comments/", "/snippets/"}
	scanned := 0
	for _, root := range []string{
		filepath.Join(repoRoot, "cli-go", "pkg"),
		filepath.Join(repoRoot, "cli-go", "cmd"),
		filepath.Join(repoRoot, "webapp", "internal"),
		filepath.Join(repoRoot, "webapp", "cmd"),
	} {
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repoRoot, p)
			rel = filepath.ToSlash(rel)
			if info.IsDir() {
				// ⭐ Deliberate fixtures live under these names.
				if b := info.Name(); b == "testdata" || b == "fixtures" {
					return filepath.SkipDir
				}
				return nil
			}
			scanned++
			for _, s := range shapes {
				if strings.Contains("/"+rel, s) {
					t.Errorf("%s: a site-shaped tree inside a source package — debris from a test "+
						"that wrote relative to its working directory. Delete it and find the write.", rel)
					return nil
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if scanned < 200 {
		t.Fatalf("scanned only %d files; the walk is wrong", scanned)
	}
}

// ⛔ NOTHING A TEST RUN LEAVES BEHIND GOES UNNOTICED, whatever its shape. Any
// untracked file git does not ignore, under the Go trees or as a site at the
// repository root, is debris until someone tracks or ignores it on purpose —
// Go source excepted.
// The shape list above cannot know every name a stray write picks; this does
// not need to.
func TestNoUntrackedDebrisInTheSourceTrees(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Skip("not a git checkout")
	}
	out, err := exec.Command("git", "-C", repoRoot, "ls-files", "--others", "--exclude-standard",
		"--", "cli-go", "webapp", "site", ".polis", "content", ".well-known").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// ⚠️ Go source is someone's work in progress in a shared checkout,
		// never debris: a test writes data, not code.
		if f == "" || strings.HasSuffix(f, ".go") ||
			strings.Contains("/"+f, "/testdata/") || strings.Contains("/"+f, "/fixtures/") {
			continue
		}
		t.Errorf("%s: untracked and not ignored — debris from a test or a tool that wrote relative to its "+
			"working directory. Delete it and find the write, or track or ignore it on purpose.", f)
	}
}
