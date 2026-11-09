package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every place a secret stops being a secret.
//
// ⭐⭐ WHY A CENSUS RATHER THAN A RULE. secret.Value refuses to render itself,
// so the easy leak — a careless %v, a value dropped into an event payload — is
// not available. What the type cannot prevent is Expose() being called and the
// RESULT logged. That is legitimate at the point of use and nowhere else, so
// the defence is not a prohibition but a COUNT: each call site is listed here,
// and a new one fails the build until somebody adds it deliberately.
//
// ⛔ ADDING A LINE HERE IS A DECISION, NOT A FORMALITY. Before you do, check
// that the exposed value is consumed immediately — passed to the client or
// writer that needs it — and never stored in a struct field, put in a map, or
// returned up a call stack, because each of those puts an ordinary string
// somewhere a later change can log.
//
// This is modelled on the storage census in storage_census_test.go, which holds
// its scope as data for the same reason.
// allowedExposureSites lists every file that exposes a secret, with EXACTLY how
// many times and why. ⛔ The count is checked, not just the listing: a second
// Expose in a file already listed is a new exposure and fails the build.
var allowedExposureSites = map[string]struct {
	count   int
	reasons []string
}{
	// path relative to the repo root : why each exposure there is correct
	"webapp/cmd/hosted/main.go": {2, []string{
		"NewStore wants a DSN string and NewEmailClient wants an API key string. ⭐ Both " +
			"exposures are at the point of use: the value goes straight into the client " +
			"that needs it and is not kept. ⚠️ If either constructor is ever changed to " +
			"take a secret.Value, DELETE the matching exposure rather than leaving it — " +
			"an exposure that is no longer needed is the kind that gets logged later.",
	}},
	"webapp/cmd/hosted/main_test.go": {1, []string{
		"reads the resolved DSN back to assert the boot path does not alter it",
	}},
	"cli-go/pkg/secret/project.go": {1, []string{
		"Project turns a projected secret into the bytes it writes to disk. ⭐ This is " +
			"the ONE place that has to, and it is the right one: the value is consumed " +
			"immediately, never stored in a field and never returned, so the exposure " +
			"begins and ends inside the function that writes the key.",
	}},
	"webapp/deploy/vercel/devdeploy/main.go": {1, []string{
		"the development deploy tool reads a personal token from a file and hands it " +
			"straight to the provisioner's client, which needs a string. ⭐ At the point " +
			"of use, never printed, and the tool exits when it is done.",
	}},
	"webapp/internal/server/publish_deploy.go": {1, []string{
		"adoptDeployToken moves the handed-over deploy token from its write-only " +
			"setting into the private store, which is the point of the handover, and " +
			"uses it once to delete its own setting. ⭐ Like Project, the exposure begins " +
			"and ends in one function: the value is written, hashed and passed to one " +
			"client, never kept in a field or returned, and every message names the " +
			"setting, never the value.",
	}},
	"cli-go/pkg/provision/vercel_seed.go": {1, []string{
		"openSeedStore hands a Blob store's read-write token, read back from the project's " +
			"setting, straight to the store client, which needs a string. ⭐ At the point of " +
			"use: the token is never logged, returned or written, every message names the " +
			"setting rather than the value, and the store it opens lives only for one Seed, " +
			"after which the setting itself is deleted.",
	}},
	"cli-go/pkg/provision/vercel_platform.go": {1, []string{
		"openVercelPlatform hands the user's Vercel token, read once from their token file, " +
			"straight to the two API clients it builds (one to resolve the team, one scoped to " +
			"it), which need a string. ⭐ At the point of use: the string lives in a local for " +
			"the length of the function, is never logged, returned or written, and no message " +
			"names it.",
	}},
	"cli-go/pkg/secret/value_test.go": {3, []string{
		"the package's own tests assert that Expose returns the value",
	}},
	"cli-go/pkg/secret/declare_test.go": {1, []string{
		"the package's own tests read resolved values back",
	}},
	"cli-go/pkg/secret/value.go": {1, []string{
		"IsZero compares the value with empty inside the package; the value never leaves it",
	}},
	"cli-go/pkg/secret/value_leak_test.go": {1, []string{
		"asserts Expose still returns the value after the redaction changes",
	}},
}

// countExposures counts every selector named Expose in a Go file's syntax
// tree: a call, a method value (f := v.Expose) and a method expression
// (secret.Value.Expose(v)) are each a selector. ⛔ It reads the PARSED file,
// not its lines: a regexp over lines missed a selector split across lines
// (`v.` then `Expose()`), which gofmt allows, and comments and strings are not
// selectors, so no file needs exempting — this one included.
func countExposures(path string, src []byte) (int, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return 0, err
	}
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == "Expose" {
			n++
		}
		return true
	})
	return n, nil
}

func TestSecretExposureIsCounted(t *testing.T) {
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
				// ⚠️ Skip vendored and generated trees, which we do not own.
				if n := info.Name(); n == "vendor" || n == "node_modules" || n == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			scanned++
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			n, err := countExposures(path, b)
			if err != nil {
				// A file the compiler would reject is not ours to count, but
				// only fixtures may be one: anywhere else it hides exposures.
				if strings.Contains(filepath.ToSlash(path), "/testdata/") {
					return nil
				}
				return fmt.Errorf("parsing %s: %w", path, err)
			}
			if n > 0 {
				rel, relErr := filepath.Rel(repoRoot, path)
				if relErr != nil {
					rel = path
				}
				found[filepath.ToSlash(rel)] += n
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	// ⛔ NON-VACUITY. A census that scans nothing passes, and this exact test
	// shipped green against the wrong directory once before. The number is a
	// floor, not a count.
	if scanned < 200 {
		t.Fatalf("scanned only %d Go files; repoRoot (%q) is probably wrong, "+
			"and a census that looks at nothing passes", scanned, repoRoot)
	}

	for path, n := range found {
		entry, ok := allowedExposureSites[path]
		reasons := entry.reasons
		if ok && n != entry.count {
			t.Errorf("%s exposes a secret %d time(s); the census allows %d. A new exposure in a listed "+
				"file is still a new exposure: justify it and raise the count, or remove it.", path, n, entry.count)
		}
		if !ok {
			t.Errorf("%s exposes a secret %d time(s) and is not in the census.\n"+
				"  If the exposure is correct — the value is consumed immediately and never "+
				"stored, mapped or returned — add it to allowedExposureSites with the reason.", path, n)
			continue
		}
		if len(reasons) == 0 {
			t.Errorf("%s is in the census with no stated reason", path)
		}
	}

	// ⚠️ And the other direction: a census entry for a file that no longer
	// exposes anything is stale, and a stale allow-list is how the next real
	// one slips through unnoticed.
	for path := range allowedExposureSites {
		if _, ok := found[path]; !ok && !absentClosedSource(path) {
			t.Errorf("%s is in the census but exposes nothing any more; remove the entry", path)
		}
	}
}

// ⛔ The two evasions a line scan missed are counted.
func TestCountExposuresSeesSplitSelectorsAndNoComments(t *testing.T) {
	src := []byte("package x\n\n// v.Expose() in a comment\nvar s = \"v.Expose()\"\n\nfunc f(v interface{ Expose() string }) string {\n\tg := v.\n\t\tExpose\n\treturn v.Expose() + g()\n}\n")
	n, err := countExposures("x.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("counted %d exposures; want 2 (a selector split across lines, and a call)", n)
	}
}
