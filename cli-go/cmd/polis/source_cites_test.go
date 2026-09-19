package main

// The source-tree citation check.
//
// ⛔ ONE RULE: NO SOURCE FILE CITES A PATH UNDER THE PLANS DIRECTORY. Plans are
// private and they move — to done/, or away — so a comment that points at one
// breaks for the contributor who has the private repo and means nothing to one
// who does not. A comment states the fact the plan stood for instead. This is
// not a linter: it reads no prose, and "Signet epic NN" references are allowed.

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// sourceTrees are the four trees that ship as code.
var sourceTrees = []string{"cli-go", "webapp", "discovery-service", "cli-bash"}

// sourceExts are the file types that ship. An extensionless file counts when it
// is a script (starts with #!), which is how cli-bash/bin/polis is found.
// Markdown ships (READMEs, CHANGELOGs, protocol notes) and counts, except the
// CLAUDE.md files, which are the release procedure's decision.
var sourceExts = map[string]bool{
	".go": true, ".ts": true, ".js": true, ".mjs": true, ".sh": true, ".md": true,
	".html": true, ".css": true, ".json": true, ".jsonl": true,
	".sql": true, ".toml": true, ".txt": true,
}

// planPathRE matches a path into the plans directory: the directory name, a
// slash, then a path character. A bare mention of the directory ("links into
// plans/ fail") is not a citation.
var planPathRE = regexp.MustCompile(`\bplans/[A-Za-z0-9_-]`)

// citeFixtures are lines that name a plans path as TEST DATA, because the rule
// they test is about that directory. Each is a file plus a substring of the
// line; an entry that no longer matches anything fails, so none can go stale.
//
// It is empty, and should stay so: these test files are published, and the
// release's content scan stops on a plans path in anything it publishes. A
// fixture that needs a private path names one outside plans/.
var citeFixtures = []struct{ file, line string }{}

func isSourceFile(path string, info os.FileInfo) bool {
	if filepath.Base(path) == "CLAUDE.md" {
		return false
	}
	if sourceExts[filepath.Ext(path)] {
		return true
	}
	if filepath.Ext(path) != "" || info.Size() < 2 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 2)
	_, err = f.Read(head)
	return err == nil && string(head) == "#!"
}

// planCitations walks trees under root and returns every citing line not
// covered by fixtures, keyed "file:line", plus the files scanned per tree and
// which fixtures matched.
func planCitations(t *testing.T, root string, trees []string, fixtures []struct{ file, line string }) (hits []string, scanned map[string]int, used map[int]bool) {
	t.Helper()
	scanned = map[string]int{}
	used = map[int]bool{}
	for _, tree := range trees {
		err := filepath.Walk(filepath.Join(root, tree), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if n := info.Name(); n == "node_modules" || n == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !isSourceFile(path, info) {
				return nil
			}
			scanned[tree]++
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
			for n := 1; sc.Scan(); n++ {
				line := sc.Text()
				if !planPathRE.MatchString(line) {
					continue
				}
				fixture := false
				for i, fx := range fixtures {
					if fx.file == rel && strings.Contains(line, fx.line) {
						used[i], fixture = true, true
					}
				}
				if !fixture {
					hits = append(hits, rel+":"+strconv.Itoa(n)+": "+strings.TrimSpace(line))
				}
			}
			return sc.Err()
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}
	return hits, scanned, used
}

// unscanned names each tree the walk read nothing from.
func unscanned(trees []string, scanned map[string]int) []string {
	var out []string
	for _, tree := range trees {
		if scanned[tree] == 0 {
			out = append(out, tree)
		}
	}
	return out
}

func TestNoSourceFileCitesAPlan(t *testing.T) {
	trees := sourceTrees
	if _, err := os.Stat(filepath.Join(repoRoot, "plans")); os.IsNotExist(err) {
		// The public repository: closed source (discovery-service/) is simply absent there.
		trees = nil
		for _, tree := range sourceTrees {
			if _, err := os.Stat(filepath.Join(repoRoot, tree)); err == nil {
				trees = append(trees, tree)
			}
		}
	}
	hits, scanned, used := planCitations(t, repoRoot, trees, citeFixtures)
	for _, tree := range unscanned(trees, scanned) {
		t.Errorf("%s: scanned no source files — the check would pass on an empty tree", tree)
	}
	for _, h := range hits {
		t.Errorf("cites a plan; state the fact it stood for instead:\n  %s", h)
	}
	for i, fx := range citeFixtures {
		if !used[i] {
			t.Errorf("stale fixture exemption: nothing in %s contains %q", fx.file, fx.line)
		}
	}
}

// The check must catch a planted citation in each kind of file it claims to
// read, and must not pass by reading nothing. The planted text spells the
// directory PLANS so that this file does not cite a plan itself.
func TestPlanCitationCheckCatchesAPlantedCitation(t *testing.T) {
	root := t.TempDir()
	plant := map[string]string{
		"a/pkg/x.go":        "// See PLANS/some-design.md.\n",
		"a/www/app.css":     "/* PLANS/done/theme.md */\n",
		"a/bin/tool":        "#!/bin/bash\n# PLANS/signet/epics/01-x.md\n",
		"a/ok.go":           "// links into PLANS/ fail\n// Signet epic 08, D1.\n",
		"a/data/notes.md":   "PLANS/shipped-doc.md\n",
		"a/CLAUDE.md":       "PLANS/release-decides.md\n",
		"a/bin/no-shebang":  "PLANS/not-a-script.md\n",
		"a/fixture_test.go": `{"../PLANS/fixture.md"}` + "\n",
	}
	for p, body := range plant {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(strings.ReplaceAll(body, "PLANS/", "pla"+"ns/")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixtures := []struct{ file, line string }{{"a/fixture_test.go", "fixture.md"}}
	hits, scanned, used := planCitations(t, root, []string{"a"}, fixtures)

	want := map[string]bool{"a/pkg/x.go:1": false, "a/www/app.css:1": false, "a/bin/tool:2": false, "a/data/notes.md:1": false}
	for _, h := range hits {
		key := h[:strings.Index(h, ": ")]
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected hit %s", h)
		}
		want[key] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("planted citation at %s not caught", k)
		}
	}
	if scanned["a"] != 6 {
		t.Errorf("scanned %d files, want 6 (CLAUDE.md and a non-script extensionless file are out)", scanned["a"])
	}
	if !used[0] {
		t.Error("fixture exemption did not match")
	}

	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, scanned, _ = planCitations(t, empty, []string{"b"}, nil)
	if got := unscanned([]string{"b"}, scanned); len(got) != 1 {
		t.Errorf("an empty tree must be reported as unscanned, got %v", got)
	}
}
