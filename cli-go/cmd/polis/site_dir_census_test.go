package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every place that still thinks in paths.
//
// ⭐⭐⭐ THIS CENSUS IS THE PROGRESS BAR FOR THE SITE-HANDLE MIGRATION, not just
// a guard. `Site.Dir()` and `Site.Path()` exist so a function can convert its
// SIGNATURE without converting its INTERNALS in the same step — which is what
// lets the migration proceed one package at a time with everything compiling
// throughout.
//
// ⛔ THE RISK IS THAT THEY BECOME PERMANENT. Every call is a place that cannot
// work on a backend with no filesystem, and the migration is finished when
// this list holds only the handful that genuinely need a real path.
//
// ⚠️ SO THE NUMBER ONLY GOES DOWN. Adding a line is allowed during the
// migration — a package converting its signatures will add several — but it is
// a debt entry, not a destination, and each says what has to happen for it to
// go away.
//
// ⭐ THE MIGRATION IS FINISHED, so the list is closed. The ratcheting ceiling
// that made the number go down is gone: every survivor is named below with its
// EXACT count and is kind 1 (waiting on named work) or kind 3 (meaningless
// without a filesystem). A new escape fails, and so does a count that moved
// either way, so a converted call has to take its entry with it.
//
// This is the fourth census in this shape (storage calls, secret exposure, API
// routes, and now this). The pattern: scope as data, enforcement mechanical,
// and a non-vacuity floor because a census that scans nothing passes.

// siteEscape is one surviving file: how many escapes it holds, and why.
type siteEscape struct {
	n   int
	why string
}

// ⚠️ Dir() takes no arguments but Path() takes several, and requiring empty
// parens made this census BLIND to the common form — it passed at zero while a
// converted package held a live escape. Caught by checking the count against a
// known escape instead of trusting a green test.
var siteDirCall = regexp.MustCompile(`\.(Dir|Path)\(`)

// sitePathEscapes lists files allowed to reach for a real path, and why.
//
// ⚠️ A REASON IS NOT "it needs a path" — that is true of every entry. It is
// what has to change for the entry to disappear, or why it never will.
//
// ⭐⭐⭐ THREE KINDS, AND THE THIRD DOES NOT CLEAR:
//
//  1. WAITING ON OTHER WORK — named work elsewhere, such as the actors
//     (which move with remote support for them). These go away when that
//     work lands. ⚠️ An atomic rewrite
//     is NOT this kind any more: atomicfile.Write takes a site and a key,
//     so a temp-and-rename that still builds a path is kind 2.
//  2. STILL TO CONVERT — an exported path builder whose callers hold the
//     bytes, or a body that was left path-shaped. These go away by doing the
//     work.
//  3. ⛔ MEANINGLESS ON A NON-FILESYSTEM BACKEND, so NEVER clearing.
//     MkdirAllPrivate is the clearest case: an object store has no directories
//     and no modes, so there is nothing for it to do and no key it could
//     create. ⚠️ These are not debt — a mode that has no filesystem simply
//     never reaches them, and pretending otherwise would mean inventing a
//     no-op that silently drops a security property. The right end state is a
//     capability check at the call site, not a converted body.
var sitePathEscapes = map[string]siteEscape{
	// ⭐⭐ THE OUTPUT PIPELINE IS CONVERTED: 43 escapes down to 1, and it is not
	// a file read or write.
	//
	// Every page write, stat, delete and cache read goes through the store by
	// key, including the stream pages' output path, the licence pages, the
	// blessed-list copy, the reply-context cache and the sitemap's lastmod.
	// The stylesheet hrefs are key arithmetic (streamRelativePath), pinned
	// against the path form they replaced.
	//
	// ⚠️ The site-relative variables are normalised (ToSlash, de-rooted) rather
	// than assumed clean, because the mount dirs are user-configurable.
	//
	// What is left is one MkdirAll of the site root, gated on the store's
	// RealPath capability (kind 3). The pkg/template config and the blessed
	// cache filler take the site handle. Unpublish's
	// remove-if-empty of a date directory is filesystem-only too, and asks the
	// store for the real path behind that same capability.
	"cli-go/pkg/render/stream.go": {1, "kind 3: MkdirAll of the site root, gated on the store's RealPath capability"},

	// ⭐ comment: 31 escapes to 17. Every single-file read, write, stat and
	// delete goes through the store, and findBlessedComment returns a KEY —
	// its caller used the result to read and then delete, and both go through
	// the store now.
	//
	// ⛔ Two of those deletes had been building a SECOND store rooted at the
	// file's own directory and deleting by base name, because they had only a
	// path to work from. One store, one key.
	//
	// What is left is the MkdirAllPrivate that keeps a tenant's unpublished
	// comments at 0700 — kind 3, and deliberately not removed on the grounds
	// that the store creates .polis parents privately anyway. (The version
	// side-car is addressed by key now, through pkg/version.)
	"cli-go/pkg/comment/comment.go": {3, "kind 3: three MkdirAllPrivate (drafts, pending, a move's private destination)"},

	// ⭐ The remaining twelve packages, converted in one pass. Their
	// signatures take the handle; what is listed here is each one's
	// path-shaped remainder, with a reason read off what each escape feeds.
	"cli-go/pkg/resolve/resolve.go": {1, "kind 3: SiteDir is the hook environment's POLIS_SITE_DIR; a hook is a local script, as below"},
	"cli-go/pkg/hooks/hooks.go":     {7, "kind 3: a hook is a local script run with the site directory as its working directory; with no filesystem there is nothing to run"},

	// ⭐⭐ pkg/site's IDENTITY INTERNALS ARE CONVERTED: .well-known/polis, the
	// key-history chain, did.json and the licence are read and written by key,
	// and the write guard that refuses to drop public_key or the chain compares
	// against what the STORE holds. (Reading a local path there would find
	// nothing on a store with no filesystem, and let any write through.) The
	// bytes are unchanged: an object-backed site produces the same document,
	// chain and DID as a POSIX one, byte for byte, pinned by a test.
	//
	// ⚠️ The rules that made this a careful batch rather than a sweep still
	// hold, and the tests that pin them passed unchanged: a chain is
	// append-only and never rebuilt; valid_from is the byte-identical signed
	// string; no writer of .well-known drops a member it does not model.
	//
	// The legacy .polis/content move and its existence check are filesystem
	// only (kind 3) and ask the store for the real path behind its RealPath
	// capability, so they no longer appear here.
	// ⭐ 34 escapes to 28. The single-file writes that carried no rationale now
	// go through the store, with absentKey keeping the "a key we cannot see is
	// one we leave alone" behaviour the old PathAbsent callers had.
	//
	// ⭐ init: 27 escapes to 1. The refuse-to-overwrite checks for the key
	// pair and .well-known/polis ask the site's STORE by key, and a check that
	// fails refuses. (A local-path check finds nothing on a store with no
	// filesystem, and let init overwrite an identity there, pinned by a test.)
	// The key pair and both policy files are written by key with their stated
	// modes, and the directory skeleton is created on the real path behind the
	// RealPath capability, since an object store has no directories to create.
	"cli-go/pkg/site/init.go": {1, "kind 3: InitResult.SiteDir reports where the site lives, and the handle's directory is the only name it has"},
	"cli-go/pkg/site/dirs.go": {2, "kind 3: MkdirAllPrivate is directories and modes; an object store has neither"},

	"webapp/internal/hosted/chaplain.go":     {4, "kind 1: a hosted actor; converts with A10"},
	"webapp/internal/hosted/rosie_grants.go": {1, "kind 1: hosted grant issuance, actor-side; converts with A10"},
}

func TestSiteHandlePathEscapesAreCounted(t *testing.T) {
	roots := []string{
		filepath.Join(repoRoot, "cli-go"),
		filepath.Join(repoRoot, "webapp"),
	}

	found := map[string]int{}
	scanned := 0

	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if n := info.Name(); n == "vendor" || n == "node_modules" || n == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			// ⚠️ TESTS ARE OUT OF SCOPE, deliberately. A test asserting a
			// root is legitimate and permanent; what this measures is
			// PRODUCTION path-dependence — the code that cannot run on a
			// backend without a filesystem. Counting tests would make the
			// progress bar never reach zero.
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			// ⛔ And the file that DEFINES Dir/Path is not a caller of them.
			if strings.HasSuffix(path, "pkg/storage/site.go") {
				return nil
			}
			scanned++
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			// ⚠️ Only calls on a storage.Site count. `.Dir()` and `.Path()`
			// are common method names, so the line must also mention a site
			// handle — crude, and deliberately so: a false positive costs a
			// census line, a false negative hides a path escape.
			for _, line := range strings.Split(string(b), "\n") {
				if !siteDirCall.MatchString(line) {
					continue
				}
				if !strings.Contains(line, "site.") && !strings.Contains(line, "Site.") &&
					!strings.Contains(line, "s.site") {
					continue
				}
				rel, relErr := filepath.Rel(repoRoot, path)
				if relErr != nil {
					rel = path
				}
				found[filepath.ToSlash(rel)]++
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	// ⛔ NON-VACUITY. A census that scans nothing passes, and this repo has
	// shipped exactly that — a guard pointed at the wrong directory stayed
	// green. The floor is a floor, not a count.
	if scanned < 200 {
		t.Fatalf("scanned only %d Go files; repoRoot (%q) is probably wrong", scanned, repoRoot)
	}

	// ⭐ An exact count per file, both ways. More is a new escape; fewer is a
	// converted call whose entry has to shrink with it, or the list drifts
	// into describing code that is gone.
	for path, n := range found {
		e, ok := sitePathEscapes[path]
		if !ok {
			t.Errorf("%s reaches for a real path and is not in the census. Convert it to use "+
				"the Store, or, if it is kind 1 or kind 3, add it with its count and reason.", path)
			continue
		}
		if n != e.n {
			t.Errorf("%s holds %d path escapes, the census says %d. Update its count, and its "+
				"reason if what it does changed.", path, n, e.n)
		}
	}
	// ⛔ EVERY SURVIVOR IS KIND 1 OR KIND 3. Kind 2 is work still to do, and the
	// migration is finished: an entry that is still to convert gets converted,
	// not listed. A kind-1 entry names the work it waits on.
	for path, e := range sitePathEscapes {
		reason := e.why
		if !strings.HasPrefix(reason, "kind 1: ") && !strings.HasPrefix(reason, "kind 3: ") {
			t.Errorf("%s: the reason must begin \"kind 1: \" (naming the work it waits on) or "+
				"\"kind 3: \" (why it is meaningless without a filesystem); got %q", path, reason)
		}
		if strings.Contains(reason, "kind 2") {
			t.Errorf("%s is listed as kind 2, still to convert: convert it", path)
		}
	}

	for path := range sitePathEscapes {
		if _, ok := found[path]; !ok && !absentClosedSource(path) {
			t.Errorf("%s is in the census but no longer reaches for a path; remove the entry", path)
		}
	}
}
