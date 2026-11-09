package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every place that asks a store for the real path behind a key.
//
// ⭐ THE OTHER HALF OF THE SITE-HANDLE CENSUS. That census counts Site.Dir()
// and Site.Path(), the escapes that do not care what the store is. A call to
// a store's RealPath is the honest form of the same thing: it sits behind
// Capabilities().RealPath, so it runs only where there is a filesystem, and a
// store without one never reaches it. ⚠️ That made these sites INVISIBLE to
// the other census, which is right for its count and wrong for the record:
// filesystem-only code is exactly what a reader of the migration needs to be
// able to list. This is that list.
//
// ⛔ EVERY ENTRY IS KIND 3, meaningless on a backend with no filesystem, and
// says why. A RealPath call that is not kind 3 (one used to read or write a
// file the store could have handled by key) is a conversion that has not been
// done, and belongs in neither census: convert it.
//
// The count per file is exact, so a new call in a listed file fails too.
var realPathCalls = map[string]struct {
	n      int
	reason string
}{
	"cli-go/pkg/atomicfile/store.go":      {1, "temp-and-rename on the same directory as the target, because POSIX rename is atomic only within one filesystem"},
	"cli-go/pkg/signing/rewrite.go":       {1, "names the real path of a refused rewrite in its message, as the message always did; the key otherwise"},
	"cli-go/pkg/site/init.go":             {2, "the directory skeleton, with the 0700-under-.polis split, and the real path an identity refusal names"},
	"cli-go/pkg/site/validate.go":         {1, "polis validate asks whether the site root exists and is a directory, a question only a filesystem can answer; the root errors name the directory"},
	"cli-go/pkg/site/wellknown.go":        {4, "naming the site root when .well-known/polis is missing; an empty legacy .polis/content directory still needs migrating, which a listing cannot see; the legacy-bundles migration MOVES directories, and the interface has no rename"},
	"cli-go/pkg/index/contributors.go":    {2, "the walk counts directory entries it could not read, which a store without a filesystem has none of"},
	"cli-go/pkg/index/rebuild.go":         {1, "creates the comment type's directory up front, so a rebuild leaves the tree it always did"},
	"cli-go/pkg/agent/projection.go":      {1, "remove-if-empty: os.Remove on a directory fails while siblings remain, and that failure is the logic"},
	"cli-go/pkg/render/stream_publish.go": {1, "remove-if-empty of a date directory, the same logic"},
	"cli-go/pkg/theme/loader.go":          {1, "an EMPTY legacy theme directory still means the tenant needs migrating, which a listing cannot see"},
	"cli-go/pkg/stream/store.go":          {2, "migrates a state file to its current name with a rename, which the interface does not have"},
	"cli-go/pkg/feed/cache.go":            {1, "streams the cache through a file handle; a store with no filesystem takes the whole value in one Put"},
}

var realPathCall = regexp.MustCompile(`\.RealPath\(`)

func TestEveryRealPathCallIsNamed(t *testing.T) {
	found := map[string]int{}
	scanned := 0
	for _, root := range []string{"cli-go", "webapp"} {
		err := filepath.Walk(filepath.Join(repoRoot, root), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if n := info.Name(); n == "vendor" || n == "node_modules" || n == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(repoRoot, p)
			rel = filepath.ToSlash(rel)
			// ⛔ The package that DEFINES RealPath is not a caller of it.
			if strings.HasPrefix(rel, "cli-go/pkg/storage/") {
				return nil
			}
			scanned++
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if n := len(realPathCall.FindAll(b, -1)); n > 0 {
				found[rel] = n
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

	for file, n := range found {
		want, ok := realPathCalls[file]
		switch {
		case !ok:
			t.Errorf("%s asks a store for a real path %d time(s) and is not named here. If it is "+
				"filesystem-only behind Capabilities().RealPath, add it with why; otherwise convert it "+
				"to go through the store by key.", file, n)
		case n != want.n:
			t.Errorf("%s asks for a real path %d time(s), and the census says %d. Name the new call's "+
				"reason, or convert it.", file, n, want.n)
		}
	}
	for file := range realPathCalls {
		if _, ok := found[file]; !ok && !absentClosedSource(file) {
			t.Errorf("%s is named here but no longer asks for a real path; remove the entry", file)
		}
	}
}
