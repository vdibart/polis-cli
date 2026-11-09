package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ⛔⛔ THE PATH FORM OF AN ATOMIC WRITE IS NOT ALLOWED ON THE SERVING PATH.
//
// atomicfile.WriteFile takes a filesystem path; atomicfile.Write takes the site
// and a key. ⭐ Only the second works on a backend with no filesystem — on
// Vercel the filesystem is read-only outside /tmp, and a path write there
// either fails or lands somewhere the site never reads back. The first render
// of a new blob-backed instance failed on exactly that before the conversion.
//
// ⚠️ THE EXEMPTIONS ARE NAMED, NOT IMPLIED. Each is out of scope for a stated
// reason, and the reason says what has to happen for it to go away. ⛔ A new
// file is not added here to make the build pass: if it is on the serving path,
// it converts.
//
// Before this test, the rule lived only in a commit message, and the first time
// someone reached for the familiar path form it would have regressed silently.
var atomicPathFormAllowed = map[string]string{
	// ⭐ ACTORS: they do not run on a remote instance until A10, and A10 will
	// change these call paths anyway. Converting them now is work done twice
	// — only packages the serving path calls are in scope.
	"cli-go/pkg/medic/medic.go":              "actor; converts with A10",
	"cli-go/pkg/tailor/checks.go":            "actor; converts with A10",
	"cli-go/pkg/judge/judge.go":              "actor; converts with A10",
	"webapp/internal/hosted/judgepublish.go": "Judge's publishing, hosted only; converts with A10",
	"cli-go/pkg/actor/registry.go":           "the operator's actor registry; actors convert with A10",
	"cli-go/pkg/actor/projection.go":         "actor key projection at hosted boot; actors convert with A10",
}

func TestTheServingPathMakesNoPathFormAtomicWrites(t *testing.T) {
	found := map[string]bool{}
	scanned := 0
	for _, root := range []string{
		filepath.Join(repoRoot, "cli-go", "pkg"),
		filepath.Join(repoRoot, "webapp", "internal"),
	} {
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(repoRoot, p)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "cli-go/pkg/atomicfile/") {
				return nil // the definition, not a caller
			}
			scanned++
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			for _, line := range strings.Split(string(b), "\n") {
				trim := strings.TrimSpace(line)
				if strings.HasPrefix(trim, "//") {
					continue
				}
				if strings.Contains(line, "atomicfile.WriteFile(") {
					found[rel] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	// ⛔ Non-vacuity: a scan that reads nothing passes.
	if scanned < 200 {
		t.Fatalf("scanned only %d files; repoRoot is probably wrong", scanned)
	}

	var files []string
	for f := range found {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if _, ok := atomicPathFormAllowed[f]; !ok {
			t.Errorf("%s uses atomicfile.WriteFile — the PATH form, which cannot work on a "+
				"backend with no filesystem. Use atomicfile.Write(ctx, site, key, …) instead. "+
				"⛔ Do not add the file to the exemption list to make this pass unless it is "+
				"genuinely off the serving path, and say why.", f)
		}
	}
	for f := range atomicPathFormAllowed {
		if !found[f] {
			if absentClosedSource(f) {
				continue
			}
			t.Errorf("%s is exempt but no longer uses the path form — remove its entry, so "+
				"the list stays a record of what is left rather than what once was", f)
		}
	}
}
