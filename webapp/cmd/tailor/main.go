// Command tailor diagnoses and auto-fixes polis sites to bring them up to current spec.
//
// Usage:
//
//	tailor [--apply] [--json] [--quiet] [--only id,...|--skip id,...] [site-dir]
//	tailor --list [--json]
//
// By default runs in dry-run mode (diagnosis only). Use --apply to actually fix issues.
// --only / --skip take a chosen subset of checks; --list names them.
// Prints a detailed human-readable report to stdout by default.
// Use --json for machine-readable output. Use --quiet for summary only.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/tailor"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	args := os.Args[1:]
	siteDir := "."
	apply := false
	jsonOutput := false
	quiet := false
	list := false
	var opts tailor.Options
	usage := "Usage: tailor [--apply] [--json] [--quiet] [--only id,...|--skip id,...] [site-dir]\n       tailor --list [--json]\n"
	ids := func(i int, flag string) []string {
		if i+1 >= len(args) || args[i+1] == "" || args[i+1][0] == '-' {
			fmt.Fprintf(os.Stderr, "error: %s needs a comma-separated list of check ids (tailor --list shows them)\n%s", flag, usage)
			os.Exit(2)
		}
		var out []string
		for _, id := range strings.Split(args[i+1], ",") {
			if id = strings.TrimSpace(id); id != "" {
				out = append(out, id)
			}
		}
		return out
	}

	// Parse args
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--apply":
			apply = true
		case "--json":
			jsonOutput = true
		case "--quiet":
			quiet = true
		case "--list":
			list = true
		case "--only":
			opts.Only = append(opts.Only, ids(i, "--only")...)
			i++
		case "--skip":
			opts.Skip = append(opts.Skip, ids(i, "--skip")...)
			i++
		case "--help", "-h":
			fmt.Fprint(os.Stderr, usage)
			fmt.Fprintf(os.Stderr, "\nDiagnose and auto-fix polis sites to bring them up to current spec.\n")
			fmt.Fprintf(os.Stderr, "\nOptions:\n")
			fmt.Fprintf(os.Stderr, "  --apply   Apply fixes (default: dry-run diagnosis only)\n")
			fmt.Fprintf(os.Stderr, "  --json    Machine-readable JSON output\n")
			fmt.Fprintf(os.Stderr, "  --quiet   Summary only (suppress per-check detail)\n")
			fmt.Fprintf(os.Stderr, "  --list    List every check: id, phase, kind, what it does, what it needs first\n")
			fmt.Fprintf(os.Stderr, "  --only    Run only these checks (comma-separated ids)\n")
			fmt.Fprintf(os.Stderr, "  --skip    Run every check except these\n")
			fmt.Fprintf(os.Stderr, "\n--apply with --only/--skip refuses, changing nothing, if a chosen check needs\n")
			fmt.Fprintf(os.Stderr, "one you left out that has not been applied yet.\n")
			fmt.Fprintf(os.Stderr, "\nTo choose changes yourself, one at a time or in phases:\n")
			fmt.Fprintf(os.Stderr, "  %s\n", tailor.GuideURL(Version))
			fmt.Fprintf(os.Stderr, "is the upgrade guide: every check (tailor --list) as a recipe you can run by hand (what it\n")
			fmt.Fprintf(os.Stderr, "changes, how to detect it, the shell commands to apply it), in run order. It is\n")
			fmt.Fprintf(os.Stderr, "written so an AI assistant can work through it with you: give it the guide and\n")
			fmt.Fprintf(os.Stderr, "this site's dry-run report (tailor --json), decide together which changes to make,\n")
			fmt.Fprintf(os.Stderr, "then apply them with --only, or by hand, and re-run the dry run to check.\n")
			fmt.Fprintf(os.Stderr, "\nExit codes:\n")
			fmt.Fprintf(os.Stderr, "  0  Site is healthy (all checks pass)\n")
			fmt.Fprintf(os.Stderr, "  1  Issues found or fix failed\n")
			fmt.Fprintf(os.Stderr, "  2  Usage error\n")
			os.Exit(0)
		case "--version", "-v":
			fmt.Printf("tailor v%s\n", Version)
			os.Exit(0)
		case "":
			// An unset shell variable expands to ""; it names no directory.
			fmt.Fprintf(os.Stderr, "error: empty argument at position %d\n%s", i+1, usage)
			os.Exit(2)
		default:
			if args[i][0] == '-' {
				fmt.Fprintf(os.Stderr, "error: unknown flag: %s\n", args[i])
				os.Exit(2)
			}
			siteDir = args[i]
		}
	}

	if list {
		printList(jsonOutput)
		return
	}

	// Verify directory exists
	if _, err := os.Stat(siteDir); errors.Is(err, fs.ErrNotExist) { //nolint:gosec // G703: an operator's own command-line flag, on the operator's machine
		fmt.Fprintf(os.Stderr, "error: directory does not exist: %s\n", siteDir)
		os.Exit(2)
	}

	// Set version for the tailor package
	tailor.Version = Version

	// Run
	var result *tailor.Result
	var err error
	if apply {
		result, err = tailor.ApplyWith(siteDir, opts)
	} else {
		result, err = tailor.DiagnoseWith(siteDir, opts)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	// Output
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(result) // stdout gone: nobody left to tell
	} else {
		printHuman(result, quiet)
	}

	// Exit code
	if result.Summary.Failed > 0 {
		os.Exit(1)
	}
}

func printHuman(result *tailor.Result, quiet bool) {
	fmt.Printf("polis-tailor v%s\n", Version)
	fmt.Println(strings.Repeat("=", 19+len(Version)))
	fmt.Println()
	fmt.Printf("Site: %s\n", result.SiteDir)

	if result.BackupDir != "" {
		fmt.Printf("Backup: %s\n", result.BackupDir)
	}
	fmt.Println()

	if !quiet {
		for _, cr := range result.Checks {
			printCheck(cr, result.Mode)
		}
	}

	// Summary
	fmt.Println()
	if result.Summary.Failed == 0 {
		fmt.Printf("All %d checks passed.", result.Summary.Passed)
		if result.Summary.Skipped > 0 {
			fmt.Printf(" (%d skipped)", result.Summary.Skipped)
		}
		fmt.Println(" Site is up to date.")
	} else {
		if result.Mode == "diagnose" {
			fmt.Printf("Summary: %d issue(s) found, %d OK",
				result.Summary.Failed, result.Summary.Passed)
			if result.Summary.Skipped > 0 {
				fmt.Printf(", %d skipped", result.Summary.Skipped)
			}
			fmt.Println()
			fmt.Println("Run with --apply to fix all issues (backup will be created first).")
			fmt.Printf("To choose which to fix, with --only/--skip or by hand (an AI assistant can help):\n  %s\n", tailor.GuideURL(Version))
		} else {
			sum := result.Summary
			fmt.Printf("%d fixed", sum.Fixed)
			if sum.NotApplied > 0 {
				fmt.Printf(", %d NOT APPLIED", sum.NotApplied)
			}
			if sum.Reported > 0 {
				fmt.Printf(", %d reported for you to decide", sum.Reported)
			}
			if sum.Passed > 0 {
				fmt.Printf(", %d already OK", sum.Passed)
			}
			fmt.Println(".")
			if sum.NotApplied > 0 {
				fmt.Println("Some changes did not apply; see [✗] above. The backup directory holds whatever was changed.")
			} else if sum.Reported == 0 {
				fmt.Printf("Site is ready for polis v%s.\n", Version)
			}
		}
	}
}

func printCheck(cr tailor.CheckResult, mode string) {
	switch cr.Status {
	case tailor.StatusPass:
		fmt.Printf("[✓] %s\n", cr.Name)
		fmt.Printf("    %s\n", cr.Message)
	case tailor.StatusFail:
		switch cr.Outcome {
		case tailor.OutcomeFailed:
			// ⛔ The apply did not take: say so, and describe its actions as
			// planned, never as done.
			fmt.Printf("[✗] %s — NOT APPLIED\n", cr.Name)
			mode = "diagnose"
		case tailor.OutcomeReported:
			fmt.Printf("[i] %s — reported, nothing to apply\n", cr.Name)
		default:
			fmt.Printf("[!] %s\n", cr.Name)
		}
		fmt.Printf("    %s\n", cr.Message)
		if cr.Reason != "" {
			fmt.Printf("    Why: %s\n", cr.Reason)
		}
		for _, a := range cr.Actions {
			switch a.Op {
			case "move":
				verb := "Would move:"
				if mode == "apply" {
					verb = "Moved:"
				}
				fmt.Printf("    %s %s → %s\n", verb, a.Path, a.Dest)
			case "create":
				verb := "Would create:"
				if mode == "apply" {
					verb = "Created:"
				}
				if a.Detail != "" {
					fmt.Printf("    %s %s (%s)\n", verb, a.Path, a.Detail)
				} else {
					fmt.Printf("    %s %s\n", verb, a.Path)
				}
			case "update":
				verb := "Would update:"
				if mode == "apply" {
					verb = "Updated:"
				}
				if a.Detail != "" {
					fmt.Printf("    %s %s (%s)\n", verb, a.Path, a.Detail)
				} else {
					fmt.Printf("    %s %s\n", verb, a.Path)
				}
			case "remove":
				verb := "Would remove:"
				if mode == "apply" {
					verb = "Removed:"
				}
				fmt.Printf("    %s %s\n", verb, a.Path)
			}
		}
	case tailor.StatusSkip:
		fmt.Printf("[-] %s\n", cr.Name)
		fmt.Printf("    %s\n", cr.Message)
	}
	fmt.Println()
}

// printList prints every declared check in run order.
func printList(asJSON bool) {
	checks := tailor.Checks()
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(checks) // stdout gone: nobody left to tell
		return
	}
	for _, c := range checks {
		fmt.Printf("%-31s %-4s %-10s %s\n", c.ID, c.Phase, c.Kind, c.Summary)
		if len(c.DependsOn) > 0 {
			fmt.Printf("%-47s needs: %s\n", "", strings.Join(c.DependsOn, ", "))
		}
	}
	fmt.Printf("\nEach check's recipe, to apply by hand or with an AI assistant: %s\n", tailor.GuideURL(Version))
}
