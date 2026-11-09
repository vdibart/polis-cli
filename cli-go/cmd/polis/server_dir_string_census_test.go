package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every place the self-hosted server still reaches its site through a
// DIRECTORY STRING rather than the site handle.
//
// ⭐ WHY A SECOND CENSUS. The site-handle census counts .Dir() and .Path() on a
// handle, so a function that never took the handle is invisible to it: the
// server held dozens of filepath.Join(s.DataDir, …) and its own
// storage.At / storage.NewSite of the data directory while that census sat at
// its floor. On a remote instance each of those is a read or write against a
// local disk that does not last. This one counts them from the caller's side.
//
// ⭐ What it counts, in the production files of webapp/internal/server,
// webapp/internal/webui and webapp/cmd/server: every use of s.DataDir, and
// every storage.At / storage.NewSite / storage.NewPOSIX built from a string.
// Comment lines are skipped. The server's one construction point, Site(), is
// one of the counted lines, and stays.
//
// ⛔ THE COUNTS ARE EXACT, PER FILE. A new directory string fails the build,
// and so does one that went away without its count being lowered, so the
// list stays a record of what is left rather than what once was.
var serverDirStringSurvivors = map[string]struct {
	count  int
	reason string
}{
	"webapp/internal/server/server.go": {12, "kind 3: the one construction point, Site(), and its " +
		"backend form, UseBackend, which roots a non-filesystem store at the same directory (both name " +
		"the site's root, neither reads a disk); the disk " +
		"logger (the logs directory and its pruning, a local file by design: an instance without a " +
		"disk logs JSON to stdout); the one-time move of the legacy drafts directory, which renames a " +
		"directory and exists only on disk; and log lines that name the directory"},
	"webapp/internal/server/ownergate.go": {1, "kind 3: a NAME, not an access — the owner instance's SiteID, " +
		"which keys sessions so two sites sharing a base URL cannot open each other's; nothing reads a disk " +
		"through it"},
	"webapp/internal/server/handlers.go": {2, "kind 3: a log line naming the directory. " +
		"kind 1: the site export (WriteZipArchive) walks and streams the directory, and waits on a " +
		"streaming read in the storage interface, which is whole-file; hosted shares the function"},
}

var serverDirString = regexp.MustCompile(`\bs\.DataDir\b|\bstorage\.(At|NewSite|NewPOSIX)\(`)

func TestTheServerReachesItsSiteThroughTheHandle(t *testing.T) {
	found := map[string]int{}
	scanned := 0
	for _, root := range []string{
		filepath.Join(repoRoot, "webapp", "internal", "server"),
		filepath.Join(repoRoot, "webapp", "internal", "webui"),
		filepath.Join(repoRoot, "webapp", "cmd", "server"),
	} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scanned++
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			n := 0
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				n += len(serverDirString.FindAllString(line, -1))
			}
			if n > 0 {
				rel, _ := filepath.Rel(repoRoot, path)
				found[filepath.ToSlash(rel)] = n
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	// ⛔ NON-VACUITY: a census pointed at the wrong directory passes.
	if scanned < 20 {
		t.Fatalf("scanned only %d Go files; repoRoot (%q) is probably wrong", scanned, repoRoot)
	}
	if len(found) == 0 {
		t.Fatal("found no directory strings at all; Site() alone should be one — the pattern is probably broken")
	}

	for file, n := range found {
		want, ok := serverDirStringSurvivors[file]
		switch {
		case !ok:
			t.Errorf("%s reaches its site through a directory string %d time(s) and is not in the census. "+
				"Use s.Site() and a key, or add it with what must change for it to go away.", file, n)
		case n > want.count:
			t.Errorf("%s has %d directory strings, the census allows %d. Use s.Site() and a key for the "+
				"new one, or raise the count deliberately and say why in its reason.", file, n, want.count)
		case n < want.count:
			t.Errorf("%s has %d directory strings, the census says %d. Lower its count so the census "+
				"keeps ratcheting.", file, n, want.count)
		}
	}
	for file := range serverDirStringSurvivors {
		if _, ok := found[file]; !ok && !absentClosedSource(file) {
			t.Errorf("%s is in the census but holds no directory string; remove its entry", file)
		}
	}
}
