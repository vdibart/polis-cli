package tailor

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck/fleettest"
)

// ⛔ THE TAILOR GOLDEN. The predicates Tailor shares with the hosted service's
// checks live in pkg/sitecheck, and moving them must not change what Tailor
// does: same pass/fail/skip, same messages, same actions, on the same sites.
// This pins all three over the fixture fleet (pkg/sitecheck/fleettest):
//
//   - diagnose: the public Diagnose over every fixture, exactly as a user runs it
//   - dry_run / apply: each check whose predicate moves, run with the site's own
//     base URL (Diagnose has none on these sites, which would hide the
//     foreign-content extractor), in allChecks order, in both modes
//
// If this fails, do NOT regenerate — a change moved what Tailor does.
var update = flag.Bool("update", false, "rewrite the Tailor golden files")

const goldenDir = "testdata/golden"

// movedChecks are the Tailor checks whose predicates are shared with the
// hosted service's, in allChecks order.
var movedChecks = []struct {
	name string
	fn   checkFunc
}{
	{"author-field-migration", checkAuthorFieldMigration},
	{"policy-content-converge", checkPolicyContentConverge},
	{"dm-directories", checkDMDirectories},
	{"webapp-view-mode", checkWebappViewMode},
	{"reference-payload-integrity", checkReferencePayloadIntegrity},
	{"registry-integrity", checkRegistryIntegrity},
	{"key-consistency", checkKeyConsistency},
	{"bundle-path-integrity", checkBundlePathIntegrity},
	{"bundle-declarations", checkBundleDeclarations},
	{"index-entries", checkIndexEntries},
	{"blessed-following-structure", checkBlessedFollowingStructure},
	{"foreign-content-in-public-path", checkForeignContentInPublicPath},
	{"stale-scoped-feed", checkStaleScopedFeed},
	{"stale-feed-viewed-at", checkStaleFeedViewedAt},
	{"orphaned-theme-dirs", checkOrphanedThemeDirs},
}

type tailorGolden struct {
	Diagnose []CheckResult `json:"diagnose"`
	DryRun   []CheckResult `json:"dry_run"`
	Apply    []CheckResult `json:"apply"`
}

func TestGolden_TailorFleet(t *testing.T) {
	t.Setenv("POLIS_BASE_URL", "")
	diagRoot, applyRoot := t.TempDir(), t.TempDir()
	fleettest.BuildFleet(t, diagRoot)
	fleettest.BuildFleet(t, applyRoot)
	dirs := map[string]string{diagRoot: "$SITES", applyRoot: "$SITES"}

	got := map[string]*tailorGolden{}
	for _, tn := range fleettest.Tenants() {
		g := &tailorGolden{}
		diagDir := filepath.Join(diagRoot, tn.Name)
		g.Diagnose = Diagnose(diagDir, "polis-cli-go/golden").Checks

		baseURL := "https://" + tn.Name + "." + fleettest.BaseDomain
		for _, mode := range []struct {
			dir    string
			dryRun bool
			out    *[]CheckResult
		}{
			{diagDir, true, &g.DryRun},
			{filepath.Join(applyRoot, tn.Name), false, &g.Apply},
		} {
			ctx := &runContext{siteDir: mode.dir, dryRun: mode.dryRun, baseURL: baseURL, generator: "polis-cli-go/golden"}
			if !mode.dryRun {
				ctx.backupDir = filepath.Join(t.TempDir(), "backup")
			}
			for _, c := range movedChecks {
				*mode.out = append(*mode.out, c.fn(ctx))
			}
		}
		got[tn.Name] = g
	}

	names := make([]string, 0, len(got))
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	if *update {
		if err := os.RemoveAll(goldenDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(goldenDir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	platformFile := runtime.GOOS + "-" + runtime.GOARCH
	for _, name := range names {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(got[name]); err != nil {
			t.Fatal(err)
		}
		out := fleettest.Normalize(buf.String(), dirs)
		out = strings.ReplaceAll(out, platformFile, "$GOOS-$GOARCH")
		out = strings.ReplaceAll(out, platform, "$GOOS/$GOARCH")
		path := filepath.Join(goldenDir, name+".json")
		if *update {
			if err := os.WriteFile(path, []byte(out), 0644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: no golden (%v)", name, err)
			continue
		}
		if string(want) != out {
			t.Errorf("%s: Tailor's findings differ from golden %s\n%s", name, path, lineDiff(string(want), out))
		}
	}
	if !*update {
		files, _ := filepath.Glob(filepath.Join(goldenDir, "*.json"))
		if len(files) != len(names) {
			t.Errorf("%d golden files, %d produced", len(files), len(names))
		}
	}
}

func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			n, _ := json.Marshal(i + 1)
			return "  line " + string(n) + "\n  want: " + wl + "\n   got: " + gl
		}
	}
	return "  (identical lines, different bytes)"
}
