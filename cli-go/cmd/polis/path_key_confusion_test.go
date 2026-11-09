package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ⛔⛔ A KEY MUST NEVER REACH A PATH-ADDRESSED HELPER, and nothing else can
// catch it.
//
// ⚠️ THIS WAS A LIVE, SILENT BUG. Converting pkg/render, a directory variable
// became a key while a surviving `ReadAtPath(filepath.Join(tagsDir, name))`
// stayed behind. It then read "content/pub.polis.core/tag/x.json" relative to
// the process working directory, and the caller's `continue` swallowed the
// error — so every tag vanished from the site index with no test failing and
// the differential green.
//
// ⭐⭐⭐ WHY IT NEEDS THIS TEST RATHER THAN CARE:
//   - A PATH AND A KEY ARE BOTH `string`. The compiler cannot distinguish them,
//     so the type system that makes the rest of this migration safe is blind
//     here.
//   - ⛔ A runtime check cannot either. I tried making the helpers refuse a
//     relative path, and it is WRONG: a relative --data-dir is supported and
//     tested, and the differential itself has `rel` and `cwd` modes.
//
// ⚠️ WHAT THESE GUARDS DO NOT CATCH — they are lexical, and narrower than a
// reader might assume. A hand audit of the remaining call sites (2026-10-08)
// found no live bug, but these shapes pass unexamined:
//   - a key held in a STRUCT FIELD and passed later;
//   - a key built from ANOTHER PACKAGE'S constant (e.g. tag.TagKey(...));
//   - a key reaching an os.* call directly (os.Open, os.ReadFile, ...);
//   - a path handed to a store held in a LOCAL VARIABLE rather than site.Store().
// Review those by hand; do not read a green run as covering them.
//
// ⭐ So it is checked lexically, in the packages that have converted: an
// argument to a path-addressed helper must mention a root. `site.Path(...)`,
// `.Dir()`, an explicitly non-site root, or an absolute literal all qualify.

// pathKeyConverted lists the packages that address their site by key, and so
// must not hand a key to a path helper. ⭐ Add a package here when it converts.
var pathKeyConverted = []string{
	"pkg/metadata", "pkg/tag", "pkg/following", "pkg/theme",
	"pkg/cache", "pkg/render", "pkg/site", "pkg/comment",
	// ⭐ Added as each one's atomic writes converted. ⚠️ notification's
	// conversion left m.stateKey — now a key — reaching os.Open. That is the
	// same CONFUSION, but these tests would NOT have caught it: they check the
	// storage path helpers, not os.* (see WHAT THESE GUARDS DO NOT CATCH).
	"pkg/stream", "pkg/notification", "pkg/feed", "pkg/dm",
	"pkg/agent", "pkg/actor", "pkg/bundle", "pkg/atomicfile",
}

var pathHelperCall = regexp.MustCompile(
	`storage\.(ReadAtPath|WriteAtPath|StatAtPath|DeleteAtPath|AppendAtPath|AppendLineAtPath|PathExists|PathAbsent|ListDir|ListAtPath|WithPathLock)\(([^\n]*)`)

// keyAssignment matches a variable assigned a SITE-RELATIVE KEY: a literal
// with no leading slash, or one concatenated onto. ⭐ That is the information
// the call site does not carry, and tracing it is what found the live bug — a
// lexical check on the argument alone cannot work, because a variable's NAME
// does not say whether it holds a path or a key.
var keyAssignment = regexp.MustCompile(
	`\b(\w+)\s*:?=\s*"(?:content/|\.polis/|site/|posts/|comments/|tags/)[^"]*"`)

// keyDerivation matches a variable assigned an expression BUILT FROM another
// variable: `x := y + "/" + …` or `x := filepath.Join(y, …)` (or path.Join,
// pathpkg.Join). If y holds a key, so does x.
//
// ⛔ Without this the guard only knew the variable a literal landed in, and a
// key one assignment away walked straight past it: listBlessedComments built
// datePath from commentsDir and handed it to storage.ListDir and ReadAtPath,
// which resolved it against the process working directory, and nothing fired.
var keyDerivation = regexp.MustCompile(
	`\b(\w+)\s*:?=\s*(?:(?:filepath|path|pathpkg)\.Join\(\s*)?(\w+)\s*[+,]`)

// siteKeyVars returns every variable in src that holds a site-relative key:
// those assigned a key literal, then, to a fixed point, those derived from one.
func siteKeyVars(src string) map[string]bool {
	vars := map[string]bool{}
	for _, m := range keyAssignment.FindAllStringSubmatch(src, -1) {
		vars[m[1]] = true
	}
	derivations := keyDerivation.FindAllStringSubmatch(src, -1)
	for changed := true; changed; {
		changed = false
		for _, m := range derivations {
			if vars[m[2]] && !vars[m[1]] {
				vars[m[1]] = true
				changed = true
			}
		}
	}
	return vars
}

// pathVarAssignment matches a variable assigned an ABSOLUTE PATH built from the
// site handle.
var pathVarAssignment = regexp.MustCompile(`\b(\w+)\s*:?=\s*(?:r\.config\.)?(?:site|Site)\.Path\(`)

var storeCall = regexp.MustCompile(
	`Store\(\)\.(Get|Put|Stat|Exists|List|Delete)\((?:ctx|context\.Background\(\)), *([^,)]+)`)

// ⛔⛔ AND THE INVERSE: A PATH MUST NEVER REACH A KEY-ADDRESSED STORE METHOD.
//
// ⚠️ ALSO A LIVE BUG, found immediately after the first one and in the same
// conversion. Three calls in pkg/comment held site.Path(...) — an absolute
// path — and handed it to Store().List, which roots its argument under the site
// again. The pending-comments endpoint returned 500.
//
// ⭐⭐⭐ THE PAIR IS THE POINT. The same `string` type flows both ways, so the
// confusion has two directions and a guard for one does not see the other:
//   - a KEY reaching a path helper reads the process working directory;
//   - a PATH reaching the store gets rooted twice.
//
// Neither is visible to the compiler, and the first cost a silent data loss
// while the second cost a 500.
func TestNoPathReachesTheStore(t *testing.T) {
	scanned, flagged := 0, 0
	for _, pkg := range pathKeyConverted {
		root := filepath.Join(repoRoot, "cli-go", pkg)
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
				return err
			}
			if strings.HasSuffix(p, "_test.go") {
				return nil
			}
			scanned++
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			src := string(b)
			pathVars := map[string]bool{}
			for _, m := range pathVarAssignment.FindAllStringSubmatch(src, -1) {
				pathVars[m[1]] = true
			}
			if len(pathVars) == 0 {
				return nil
			}
			for i, line := range strings.Split(src, "\n") {
				m := storeCall.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				for v := range pathVars {
					if !regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\b`).MatchString(m[2]) {
						continue
					}
					flagged++
					rel, _ := filepath.Rel(repoRoot, p)
					t.Errorf("%s:%d: Store().%s is KEY-addressed, but %q holds an absolute "+
						"site.Path(...):\n    %s\n"+
						"  ⛔ The store roots its argument under the site, so this resolves "+
						"twice and finds nothing. Pass the key.",
						rel, i+1, m[1], v, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", pkg, err)
		}
	}
	if scanned < 20 {
		t.Fatalf("scanned only %d files; the package list or repoRoot is wrong", scanned)
	}
	t.Logf("%d files scanned, %d flagged", scanned, flagged)
}

func TestNoKeyReachesAPathHelper(t *testing.T) {
	scanned, flagged := 0, 0
	for _, pkg := range pathKeyConverted {
		root := filepath.Join(repoRoot, "cli-go", pkg)
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
				return err
			}
			if strings.HasSuffix(p, "_test.go") {
				return nil
			}
			scanned++
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			src := string(b)
			// variables in this FILE that hold a site-relative key
			keyVars := siteKeyVars(src)
			if len(keyVars) == 0 {
				return nil
			}
			for i, line := range strings.Split(src, "\n") {
				m := pathHelperCall.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				// ⭐ A key wrapped in site.Path(...) is being ROOTED, which is
				// exactly right — that is the sanctioned way to hand a key to a
				// path-addressed helper.
				if strings.Contains(m[2], ".Path(") {
					continue
				}
				for v := range keyVars {
					if !regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\b`).MatchString(m[2]) {
						continue
					}
					flagged++
					rel, _ := filepath.Rel(repoRoot, p)
					t.Errorf("%s:%d: storage.%s is PATH-addressed, but %q holds a site-relative KEY:\n"+
						"    %s\n"+
						"  ⛔ This reads or writes relative to the process working directory, not the "+
						"site. Use site.Store() with the key, or join the key onto the root.",
						rel, i+1, m[1], v, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", pkg, err)
		}
	}
	// ⛔ Non-vacuity: a scan that reads nothing passes.
	if scanned < 20 {
		t.Fatalf("scanned only %d files across %d packages; the package list or repoRoot is wrong",
			scanned, len(pathKeyConverted))
	}
	t.Logf("%d files scanned across %d converted packages, %d flagged", scanned, len(pathKeyConverted), flagged)
}
