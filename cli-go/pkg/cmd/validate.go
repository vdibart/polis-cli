package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
)

// handleValidate runs the site validation suite.
//
// TWO FORMS, and cloning is not one of them:
//
//	polis validate [path]   a local directory — your own site, OR a clone
//	polis validate <url>    fetch over the network, storing nothing
//
// ⛔ It never clones. Cloning is `polis clone`'s job and a user composes the
// two. Neither form writes anything, which is why the shape of the argument can
// select the form safely and no flag is needed.
func handleValidate(args []string) {
	target := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			target = a
			break
		}
	}

	// Witness checks may fetch a discovery service's key; that fetch carries this
	// run's request id (SIGNET epic 32 E8).
	dsKeys := sitecheck.NewDSKeyLookup(nil, requestID)

	var report *sitecheck.Report
	switch {
	case target == "":
		report = sitecheck.RunLocalWith(getDataDir(), dsKeys)
	case isLocalPath(target):
		report = sitecheck.RunLocalWith(target, dsKeys)
	default:
		url := target
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			url = "https://" + url
		}
		r := sitecheck.NewRemote()
		r.DSKeys = dsKeys
		if isSiteRoot(url) {
			report = r.RunSite(url)
		} else {
			report = r.RunArtifact(url)
		}
	}

	if jsonOutput {
		outputJSON(report)
	} else {
		printReport(report)
	}

	// A validator that always exits 0 cannot be used in CI, which is one of the
	// places this is meant to run. Not-applicable checks do NOT affect the exit
	// code — they are an honest gap, not a defect.
	if !report.OK() {
		os.Exit(1)
	}
}

// isLocalPath reports whether target names something on this filesystem.
// An existing path always wins, so a directory named like a hostname still
// validates as a directory.
func isLocalPath(target string) bool {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return false
	}
	if _, err := os.Stat(target); err == nil {
		return true
	}
	// Not on disk. A bare hostname is a URL the user left the scheme off;
	// anything else is a path they got wrong, and saying so is better than
	// silently trying to fetch it.
	host := target
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	return !strings.Contains(host, ".")
}

// isSiteRoot reports whether a URL addresses a whole site rather than one
// artifact within it.
func isSiteRoot(url string) bool {
	rest := url
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	i := strings.Index(rest, "/")
	return i < 0 || strings.Trim(rest[i:], "/") == ""
}

var familyOrder = []string{
	sitecheck.FamilyContent,
	sitecheck.FamilyIndex,
	sitecheck.FamilyPolicy,
	sitecheck.FamilyIdentity,
	sitecheck.FamilyBundle,
}

func printReport(r *sitecheck.Report) {
	scope := "whole site"
	if r.Scope == sitecheck.ScopeArtifact {
		scope = "one artifact"
	}
	fmt.Printf("Checking %s (%s, %s)\n", r.Target, r.Form, scope)
	if r.Note != "" {
		fmt.Printf("%s\n", wrapNote(r.Note))
	}

	if r.FatalError != "" {
		fmt.Printf("\n[x] %s\n", r.FatalError)
		return
	}

	byFamily := map[string][]sitecheck.Check{}
	for _, c := range r.Checks {
		byFamily[c.Family] = append(byFamily[c.Family], c)
	}
	families := append([]string{}, familyOrder...)
	for f := range byFamily {
		if !contains(families, f) {
			families = append(families, f)
		}
	}

	for _, family := range families {
		checks := byFamily[family]
		if len(checks) == 0 {
			continue
		}
		sort.SliceStable(checks, func(i, j int) bool { return checks[i].ID < checks[j].ID })
		fmt.Printf("\n%s\n", family)
		for _, c := range checks {
			mark := "?"
			text := c.Detail
			switch c.Outcome {
			case sitecheck.OutcomePassed:
				mark = "✓"
			case sitecheck.OutcomeFailed:
				mark = "✗"
			case sitecheck.OutcomeWarning:
				mark = "!"
			case sitecheck.OutcomeNotApplicable:
				mark = "–"
				text = "NOT CHECKED: " + c.Reason
			}
			fmt.Printf("  %s %-24s %s\n", mark, c.ID, text)
			for _, f := range c.Findings {
				if f == "" {
					continue
				}
				fmt.Printf("      · %s\n", f)
			}
		}
	}

	fmt.Printf("\n%d checks: %d passed, %d failed, %d warning, %d not applicable\n",
		len(r.Checks), r.Totals.Passed, r.Totals.Failed, r.Totals.Warning, r.Totals.NotApplicable)

	// ⛔ The line this command exists for. A clean result must mean "I checked
	// and it was fine", never "I did not check" — so when anything went
	// unchecked, say so instead of letting the absence of errors imply
	// coverage.
	if r.Totals.NotApplicable > 0 {
		fmt.Printf("[!] %d check(s) did not run. This result covers only what was checked; see NOT CHECKED above.\n", r.Totals.NotApplicable)
	}
	// ⚠️ A warning is about the site's SURROUNDINGS, not the site — an
	// intermediary that rewrote a public file the author cannot edit. It does
	// not affect the exit code, and saying so here is what keeps it from being
	// read as a defect the reader is expected to go and fix.
	if r.Totals.Warning > 0 {
		fmt.Printf("[!] %d warning(s). These describe what a third party did to this site's public surface, not a fault in the site; the exit code is unaffected.\n", r.Totals.Warning)
	}
	if r.Totals.Failed == 0 {
		fmt.Println("[✓] Nothing checked was found wrong.")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// wrapNote soft-wraps the scope caveat so it reads as prose rather than one
// long line.
func wrapNote(note string) string {
	const width = 76
	var lines []string
	line := ""
	for _, word := range strings.Fields(note) {
		if line == "" {
			line = word
			continue
		}
		if len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		line += " " + word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
