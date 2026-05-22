// Command tailor diagnoses and auto-fixes polis sites to bring them up to current spec.
//
// Usage:
//
//	tailor [--apply] [--json] [--quiet] [site-dir]
//
// By default runs in dry-run mode (diagnosis only). Use --apply to actually fix issues.
// Prints a detailed human-readable report to stdout by default.
// Use --json for machine-readable output. Use --quiet for summary only.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/tailor"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	siteDir := "."
	apply := false
	jsonOutput := false
	quiet := false

	// Parse args
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--apply":
			apply = true
		case "--json":
			jsonOutput = true
		case "--quiet":
			quiet = true
		case "--help", "-h":
			fmt.Fprintf(os.Stderr, "Usage: tailor [--apply] [--json] [--quiet] [site-dir]\n")
			fmt.Fprintf(os.Stderr, "\nDiagnose and auto-fix polis sites to bring them up to current spec.\n")
			fmt.Fprintf(os.Stderr, "\nOptions:\n")
			fmt.Fprintf(os.Stderr, "  --apply   Apply fixes (default: dry-run diagnosis only)\n")
			fmt.Fprintf(os.Stderr, "  --json    Machine-readable JSON output\n")
			fmt.Fprintf(os.Stderr, "  --quiet   Summary only (suppress per-check detail)\n")
			fmt.Fprintf(os.Stderr, "\nExit codes:\n")
			fmt.Fprintf(os.Stderr, "  0  Site is healthy (all checks pass)\n")
			fmt.Fprintf(os.Stderr, "  1  Issues found or fix failed\n")
			fmt.Fprintf(os.Stderr, "  2  Usage error\n")
			os.Exit(0)
		case "--version", "-v":
			fmt.Printf("tailor v%s\n", Version)
			os.Exit(0)
		default:
			if args[i][0] == '-' {
				fmt.Fprintf(os.Stderr, "error: unknown flag: %s\n", args[i])
				os.Exit(2)
			}
			siteDir = args[i]
		}
	}

	// Verify directory exists
	if _, err := os.Stat(siteDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: directory does not exist: %s\n", siteDir)
		os.Exit(2)
	}

	// Set version for the tailor package
	tailor.Version = Version

	// Run
	var result *tailor.Result
	if apply {
		result = tailor.Apply(siteDir)
	} else {
		result = tailor.Diagnose(siteDir)
	}

	// Output
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
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
		} else {
			fmt.Printf("All %d issue(s) fixed.", result.Summary.Failed)
			if result.Summary.Passed > 0 {
				fmt.Printf(" %d already OK.", result.Summary.Passed)
			}
			fmt.Printf(" Site is ready for polis v%s.\n", Version)
		}
	}
}

func printCheck(cr tailor.CheckResult, mode string) {
	switch cr.Status {
	case tailor.StatusPass:
		fmt.Printf("[✓] %s\n", cr.Name)
		fmt.Printf("    %s\n", cr.Message)
	case tailor.StatusFail:
		fmt.Printf("[!] %s\n", cr.Name)
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
