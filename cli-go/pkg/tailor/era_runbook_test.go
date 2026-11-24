package tailor_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/tailor"
)

// THE HAND RECIPES AGAINST TAILOR, on a site an old release built.
//
// The public upgrade guide gives a shell recipe per Tailor check so a person
// can apply changes one at a time. Until something runs them they are a draft
// read off the code. This test builds one pre-0.58 era site (TestEraFixtures'
// machinery), copies it twice, applies testdata/eras/runbook-phase1-5.sh (the
// guide's phase 1 to 5 recipes, as written) to one copy and tailor.Apply to the
// other, and then:
//
//  1. Diagnose on the hand copy must pass every phase 1 to 5 check: the guide's
//     own definition of done ("the check you just did by hand should flip to
//     pass").
//  2. The two trees must match, apart from what later phases own (tailor ran
//     all of them, the script only 1 to 5) and the documented differences in
//     runbookDocumentedDifferences.
//
// Same gate as TestEraFixtures (POLIS_TAILOR_ERAS=1). Also needs bash, jq and
// a Go toolchain (it builds the current polis for the script to call).
// POLIS_TAILOR_ERAS_RUNBOOK_VERSION picks the era (default 0.45.0).

// runbookPhaseChecks are the checks phases 1 to 5 cover, in run order.
var runbookPhaseChecks = []string{
	"wellknown-version", "author-field-migration", "wellknown-legacy-config", "wellknown-bundles", "avatar-config",
	"bundle-json",
	"layout-posts", "layout-comments", "layout-following", "layout-blessed",
	"path-separators",
	"bundle-reference-payload", "legacy-content-path", "registry-migration", "legacy-theme-locations", "bundle-active-fields",
	"notification-rules-migration", "active-shape-upgrade", "v3-legacy-archives",
	"index-rebuild", "render-site",
}

// runbookLaterPhasePaths are written only by phases 6 to 9, which the script
// does not run. A path under one of these is not compared.
var runbookLaterPhasePaths = []string{
	"policies/", ".polis/policies/", "content/pub.polis.core/tag/", ".polis/storage-salt",
	".polis/bundles/pub.polis.core/dm/", ".polis/webapp/", ".polis/tailor-backup/", ".polis/render-backup/", ".polis/bin/",
	"metadata/manifest.json",       // manifest-obsolete (8.1)
	".polis/webapp-config.json",    // webapp-config (7.1) moves it
	"content/pub.polis.core/feed/", // legacy-feed-scaffolding (8.7)
	".polis/logs/",
}

// runbookLaterPhaseExact are single entries (not prefixes) later phases own.
var runbookLaterPhaseExact = map[string]bool{
	"metadata/": true, // the directory itself: empty-metadata-dir (8.2) removes it once empty
}

// runbookLaterPhaseFields are JSON members written only by phases 6 to 9.
var runbookLaterPhaseFields = map[string][]string{
	".well-known/polis": {"public_key_messages"}, // dm-keyring (6.7)
}

// runbookNormalise strips, from BOTH sides, what a later phase changes inside
// a file phases 1 to 5 also write, and puts in a canonical order what the
// guide says only reorders.
var runbookNormalise = map[string]func(map[string]any){
	"content/pub.polis.core/bundle.json": func(m map[string]any) {
		types, _ := m["types"].(map[string]any)
		delete(types, "pub.polis.feed") // legacy-feed-scaffolding (8.7)
		for _, v := range types {       // unpublished-emits-migration (6.5.5)
			ct, _ := v.(map[string]any)
			if emits, ok := ct["emits"].([]any); ok {
				var kept []any
				for _, e := range emits {
					if s, _ := e.(string); !strings.HasSuffix(s, ".unpublished") {
						kept = append(kept, e)
					}
				}
				ct["emits"] = kept
			}
		}
	},
	// 4.7.1's recipe ends in unique_by(.id), which sorts; Tailor appends in
	// order. The rules are matched by id, so order carries nothing.
	".polis/bundles/registry.json": func(m map[string]any) {
		if rules, ok := m["notifications"].([]any); ok {
			sort.Slice(rules, func(i, j int) bool {
				a, _ := rules[i].(map[string]any)
				b, _ := rules[j].(map[string]any)
				return fmt.Sprint(a["id"]) < fmt.Sprint(b["id"])
			})
		}
	},
}

// runbookDocumentedDifferences: JSON members the guide says differ on purpose.
var runbookDocumentedDifferences = map[string][]string{
	// 1.5: "Tailor picks random colours"; the recipe chooses them.
	".well-known/polis": {"avatar"},
	// Tailor and the recipes both move the follow and blessing files byte for
	// byte, so neither is listed: any difference there is a regression.
}

func TestEraRunbookRecipes(t *testing.T) {
	if os.Getenv("POLIS_TAILOR_ERAS") != "1" {
		t.Skip("era fixtures download old releases from GitHub; set POLIS_TAILOR_ERAS=1 to run")
	}
	for _, tool := range []string{"bash", "jq", "go"} {
		// Asked to run (POLIS_TAILOR_ERAS=1), a missing tool is a failure:
		// the deploy gate sets it, and a skip there would read as a pass.
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("needs %s on PATH", tool)
		}
	}
	version := os.Getenv("POLIS_TAILOR_ERAS_RUNBOOK_VERSION")
	if version == "" {
		version = "0.45.0"
	}
	followee := startFolloweeSite(t)
	t.Setenv("POLIS_BASE_URL", eraBaseURL)
	t.Setenv("DISCOVERY_SERVICE_URL", "https://127.0.0.1:1")

	bin, err := eraBinary(version)
	if err != nil {
		t.Fatalf("download v%s: %v", version, err)
	}
	work := t.TempDir()
	orig := filepath.Join(work, "orig")
	if _, err := buildEraSite(t, bin, orig, followee); err != nil {
		t.Fatalf("v%s could not build a site: %v", version, err)
	}
	plantCustomisations(t, orig)
	hand, auto := filepath.Join(work, "hand"), filepath.Join(work, "tailor")
	for _, dst := range []string{hand, auto} {
		if out, err := exec.Command("cp", "-a", orig, dst).CombinedOutput(); err != nil {
			t.Fatalf("cp: %v %s", err, out)
		}
	}

	// The current polis, for the recipes that call it.
	binDir := filepath.Join(work, "bin")
	build := exec.Command("go", "build", "-o", filepath.Join(binDir, "polis"), "./cmd/polis")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build polis: %v\n%s", err, out)
	}

	tailor.Apply(auto)

	script, _ := filepath.Abs(filepath.Join("testdata", "eras", "runbook-phase1-5.sh"))
	home := filepath.Join(work, "home")
	os.MkdirAll(home, 0o755)
	cmd := exec.Command("bash", script, hand)
	cmd.Env = []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + os.TempDir(),
		"POLIS_BASE_URL=" + eraBaseURL,
		"DISCOVERY_SERVICE_URL=https://127.0.0.1:1",
	}
	out, err := cmd.CombinedOutput()
	t.Logf("runbook script output:\n%s", out)
	if err != nil {
		t.Errorf("runbook script: %v", err)
	}
	if keep := os.Getenv("POLIS_TAILOR_ERAS_KEEP"); keep != "" {
		for name, dir := range map[string]string{"orig": orig, "hand": hand, "tailor": auto} {
			keepTree(t, dir, filepath.Join(keep, "runbook-v"+version, name))
		}
	}

	// (1) The guide's definition of done.
	diag := tailor.Diagnose(hand)
	status := map[string]tailor.CheckResult{}
	for _, c := range diag.Checks {
		status[c.Name] = c
	}
	for _, name := range runbookPhaseChecks {
		c, ok := status[name]
		switch {
		case !ok:
			t.Errorf("check %s is not in tailor's run any more: update runbookPhaseChecks", name)
		case c.Status == tailor.StatusFail:
			if _, known := eraKnownFindings[name]; known {
				t.Logf("KNOWN FINDING: after the hand recipes %s fails: %s", name, c.Message)
				continue
			}
			t.Errorf("after the hand recipes, %s still fails: %s %v", name, c.Message, c.Actions)
		}
	}

	// (2) Same tree as tailor, apart from later phases and documented differences.
	//
	// Two choices are random on Tailor's side and made by the person on the
	// other: the avatar colours (1.5, documented) and the theme, which the
	// registry gets when 4.5.1's `polis render` creates it (also random). So
	// before comparing, the hand copy takes Tailor's picks, as a person who
	// chose the same would, and is rendered again. Every rendered page then
	// has to match.
	adoptChoices(t, binDir, cmd.Env, auto, hand)
	for _, d := range diffTrees(hand, auto) {
		if why, ok := runbookKnownDivergences[strings.SplitN(d, ":", 2)[0]]; ok {
			t.Logf("KNOWN FINDING: %s (%s)", d, why)
			continue
		}
		t.Errorf("hand recipes and tailor differ: %s", d)
	}
}

// runbookKnownDivergences: places where the recipe and Tailor really do reach
// different trees, each a finding to rule on rather than a documented choice.
var runbookKnownDivergences = map[string]string{}

// adoptChoices copies Tailor's random picks (avatar, active theme) into the
// hand copy and re-renders it.
func adoptChoices(t *testing.T, binDir string, env []string, from, to string) {
	t.Helper()
	var wk struct {
		Avatar map[string]any `json:"avatar"`
	}
	data, _ := os.ReadFile(filepath.Join(from, ".well-known", "polis"))
	json.Unmarshal(data, &wk)
	polis := filepath.Join(binDir, "polis")
	run := func(args ...string) {
		c := exec.Command(polis, args...)
		c.Dir = to
		c.Env = env
		if out, err := c.CombinedOutput(); err != nil {
			t.Errorf("polis %v: %v %s", args, err, out)
		}
	}
	var mineWK struct {
		Avatar map[string]any `json:"avatar"`
	}
	data, _ = os.ReadFile(filepath.Join(to, ".well-known", "polis"))
	json.Unmarshal(data, &mineWK)
	if wk.Avatar != nil && !reflect.DeepEqual(wk.Avatar, mineWK.Avatar) {
		args := []string{"site", "set", "avatar"}
		for _, k := range []string{"bg", "fg", "pattern"} {
			if v, ok := wk.Avatar[k].(string); ok && v != "" {
				args = append(args, "--"+k, v)
			}
		}
		run(args...)
	}
	var reg map[string]any
	regPath := filepath.Join(".polis", "bundles", "registry.json")
	data, _ = os.ReadFile(filepath.Join(from, regPath))
	json.Unmarshal(data, &reg)
	if theme, ok := reg["active_theme"].(string); ok {
		var mine map[string]any
		data, _ = os.ReadFile(filepath.Join(to, regPath))
		if json.Unmarshal(data, &mine) == nil && mine["active_theme"] != theme {
			mine["active_theme"] = theme
			b, _ := json.MarshalIndent(mine, "", "  ")
			os.WriteFile(filepath.Join(to, regPath), append(b, '\n'), 0o600)
		}
	}
	run("render", "--force")
}

// diffTrees lists every difference between the two site trees that is not
// owned by a later phase or documented.
func diffTrees(a, b string) []string {
	fa, fb := treeFiles(a), treeFiles(b)
	var out []string
	keys := map[string]bool{}
	for k := range fa {
		keys[k] = true
	}
	for k := range fb {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, rel := range sorted {
		if laterPhase(rel) {
			continue
		}
		da, inA := fa[rel]
		db, inB := fb[rel]
		switch {
		case !inA:
			out = append(out, rel+": only tailor has it")
		case !inB:
			out = append(out, rel+": only the hand recipes left it")
		case bytes.Equal(da, db):
		default:
			if d := jsonDiff(rel, da, db); d != "" {
				out = append(out, rel+": "+d)
			}
		}
	}
	return out
}

func laterPhase(rel string) bool {
	if runbookLaterPhaseExact[rel] {
		return true
	}
	for _, p := range runbookLaterPhasePaths {
		if rel == p || strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// treeFiles maps every regular file and every directory (as "dir/") to its
// bytes; a directory maps to nil, so an empty directory left by one side shows.
func treeFiles(root string) map[string][]byte {
	out := map[string][]byte{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			out[rel+"/"] = nil
			return nil
		}
		if data, err := os.ReadFile(p); err == nil {
			out[rel] = data
		}
		return nil
	})
	return out
}

// jsonDiff compares two JSON documents member by member, ignoring the members
// later phases own and the documented differences. "" means equal. For
// anything that is not a JSON object it reports that the bytes differ.
func jsonDiff(rel string, a, b []byte) string {
	var ma, mb map[string]any
	if json.Unmarshal(a, &ma) != nil || json.Unmarshal(b, &mb) != nil {
		return fmt.Sprintf("bytes differ (%d vs %d bytes)", len(a), len(b))
	}
	for _, k := range append(runbookLaterPhaseFields[rel], runbookDocumentedDifferences[rel]...) {
		delete(ma, k)
		delete(mb, k)
	}
	if norm := runbookNormalise[rel]; norm != nil {
		norm(ma)
		norm(mb)
	}
	var diffs []string
	for k := range ma {
		if _, ok := mb[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("member %q only in the hand result", k))
		}
	}
	for k := range mb {
		va, ok := ma[k]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("member %q only in tailor's result", k))
			continue
		}
		if !reflect.DeepEqual(va, mb[k]) {
			ja, _ := json.Marshal(va)
			jb, _ := json.Marshal(mb[k])
			diffs = append(diffs, fmt.Sprintf("member %q: hand %s, tailor %s", k, clip(ja), clip(jb)))
		}
	}
	if len(diffs) == 0 && !bytes.Equal(a, b) {
		// Same members, different bytes: formatting or member order only.
		return ""
	}
	sort.Strings(diffs)
	return strings.Join(diffs, "; ")
}

func clip(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}

// plantCustomisations gives the old site what a customised one has, so both
// the recipes and Tailor must handle it, and the tree comparison checks they
// agree: a theme of the owner's in site/themes/ (kept), one in .polis/themes/
// (moved to site/themes/), and a stale copy of a shipped theme (removed).
// Neither of the owner's themes may become the active theme by chance.
func plantCustomisations(t *testing.T, dir string) {
	t.Helper()
	for path, body := range map[string]string{
		"site/themes/mine/mine.css":             ":root { --color-bg: #fff; }\n",
		"site/themes/mine/snippets/about.html":  "<p>mine</p>\n",
		".polis/themes/also-mine/also-mine.css": ":root { --color-bg: #000; }\n",
		"site/themes/vice/vice.css":             "/* a stale copy of a shipped theme */\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
