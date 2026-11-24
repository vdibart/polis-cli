package tailor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// upgradeGuide is the public page that gives every Tailor check a manual
// recipe, so a self-hoster can take the upgrade one change at a time.
const upgradeGuide = GuidePath

var guideCheckHeading = regexp.MustCompile("(?m)^#### `([a-z0-9-]+)`\\s*$")

// The guide has exactly one entry per declared check, in run order. A new
// check without a recipe, or a recipe for a check that is gone, fails here.
func TestUpgradeGuideCoversEveryCheck(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(upgradeGuide)))
	if err != nil {
		t.Fatalf("read %s: %v", upgradeGuide, err)
	}
	var inGuide []string
	for _, m := range guideCheckHeading.FindAllStringSubmatch(string(data), -1) {
		inGuide = append(inGuide, m[1])
	}
	declared := map[string]bool{}
	for _, d := range Checks() {
		declared[d.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range inGuide {
		if !declared[id] {
			t.Errorf("%s has an entry for %q, which is not a Tailor check", upgradeGuide, id)
		}
		if seen[id] {
			t.Errorf("%s has two entries for %q", upgradeGuide, id)
		}
		seen[id] = true
	}
	for i, d := range Checks() {
		if !seen[d.ID] {
			t.Errorf("%s has no entry for check %q: add its Detect / Apply recipe", upgradeGuide, d.ID)
		} else if i < len(inGuide) && inGuide[i] != d.ID {
			t.Errorf("%s is out of run order at entry %d: has %q, Tailor runs %q", upgradeGuide, i+1, inGuide[i], d.ID)
			break
		}
	}
}

// runbookScript runs the guide's recipes in the era suite, which compares
// their result with Tailor's on sites built by old releases.
const runbookScript = "testdata/eras/runbook-phase1-5.sh"

// runbookStepsWithoutQuote are the script's steps that quote nothing, each
// for a reason the guide gives.
var runbookStepsWithoutQuote = map[string]string{
	"wellknown-version":      "the guide's X.Y.Z is a placeholder the script fills in",
	"legacy-theme-locations": "the guide gives this recipe in prose",
	"active-shape-upgrade":   "report only on a self-hosted site",
}

var (
	runbookStep = regexp.MustCompile(`^step ([a-z0-9-]+)\s*$`)
	blankRun    = regexp.MustCompile(`\s+`)
)

func collapse(s string) string { return blankRun.ReplaceAllString(s, " ") }

// The era suite tests the recipes the guide publishes, not a copy that has
// drifted from them. Every line the script marks as the guide's must appear in
// that check's entry (or, before the first step, in the guide's setup), and
// every step quotes the guide unless it says why it cannot.
func TestRunbookScriptQuotesTheGuide(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(upgradeGuide)))
	if err != nil {
		t.Fatalf("read %s: %v", upgradeGuide, err)
	}
	guide := string(data)
	sections := map[string]string{}
	heads := guideCheckHeading.FindAllStringSubmatchIndex(guide, -1)
	if len(heads) == 0 {
		t.Fatal("no check entries in the guide")
	}
	setup := collapse(guide[:heads[0][0]])
	for i, h := range heads {
		end := len(guide)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		sections[guide[h[2]:h[3]]] = collapse(guide[h[0]:end])
	}

	script, err := os.ReadFile(filepath.FromSlash(runbookScript))
	if err != nil {
		t.Fatal(err)
	}
	step, quoting := "", false
	quoted := map[string]int{}
	var steps []string
	for n, line := range strings.Split(string(script), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case trim == "# guide>":
			quoting = true
			continue
		case trim == "# <guide":
			quoting = false
			continue
		}
		if m := runbookStep.FindStringSubmatch(trim); m != nil && !quoting {
			step = m[1]
			steps = append(steps, step)
			if _, ok := sections[step]; !ok {
				t.Errorf("%s:%d: step %q has no entry in the guide", runbookScript, n+1, step)
			}
			continue
		}
		if !quoting || trim == "" {
			continue
		}
		where, region := "the guide's setup", setup
		if step != "" {
			where, region = "the "+step+" entry", sections[step]
		}
		if !strings.Contains(region, collapse(trim)) {
			t.Errorf("%s:%d quotes a line that is not in %s of %s any more:\n  %s\nCopy the guide's current recipe into the script.",
				runbookScript, n+1, where, upgradeGuide, trim)
		}
		quoted[step]++
	}
	if quoting {
		t.Errorf("%s: a # guide> block is never closed", runbookScript)
	}
	if quoted[""] == 0 {
		t.Errorf("%s does not quote the guide's setup (jqi and the path variables)", runbookScript)
	}
	for _, s := range steps {
		_, exempt := runbookStepsWithoutQuote[s]
		switch {
		case quoted[s] == 0 && !exempt:
			t.Errorf("step %s runs no recipe quoted from the guide: quote it, or list why in runbookStepsWithoutQuote", s)
		case quoted[s] > 0 && exempt:
			t.Errorf("step %s quotes the guide but is listed in runbookStepsWithoutQuote: remove it from the list", s)
		}
	}
}

func TestGuideURLPinsTheReleaseTag(t *testing.T) {
	for v, want := range map[string]string{
		"0.71.0":          "https://github.com/vdibart/polis-cli/blob/v0.71.0/" + GuidePath,
		"0.71.0+3b6705f6": "https://github.com/vdibart/polis-cli/blob/v0.71.0/" + GuidePath,
		"dev":             "https://github.com/vdibart/polis-cli/blob/main/" + GuidePath,
	} {
		if got := GuideURL(v); got != want {
			t.Errorf("GuideURL(%q) = %s, want %s", v, got, want)
		}
	}
}
