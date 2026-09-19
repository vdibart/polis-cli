package cmd

import (
	"flag"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/clone"
)

// presence renders an optional artifact's outcome. "not published" rather than
// "missing": absent is an ordinary state for most of these, not a defect.
func presence(ok bool) string {
	if ok {
		return "cloned"
	}
	return "not published"
}

// cloneUsage states what a clone is FOR. Vincent, 2026-09-17: a clone copies
// someone else's live site so it can be read and analysed offline — never so
// their content or settings can be served as your own.
const cloneUsage = `Usage: polis clone [--full|--diff] <url> [target-dir]

Copy someone else's live polis site to a local folder, to read and analyse it
offline (for example with 'polis validate <dir>'). A clone is not a way to
serve their content or settings as your own: it carries no keys and no
policies, and it is not your site.

Flags go before the URL.

`

func handleClone(args []string) {
	fs := flag.NewFlagSet("clone", flag.ExitOnError)
	fullClone := fs.Bool("full", false, "Re-download all content (ignore cached state)")
	diffClone := fs.Bool("diff", false, "Only download new/changed content (default if previously cloned)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), cloneUsage)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) < 1 {
		exitError("Usage: polis clone [--full|--diff] <url> [target-dir]")
	}

	serverURL := remaining[0]
	targetDir := ""
	if len(remaining) > 1 {
		targetDir = remaining[1]
	}

	// Derive target directory from URL if not specified
	if targetDir == "" {
		targetDir = clone.ExtractDomainForDir(serverURL)
	}

	// Determine mode
	opts := clone.CloneOptions{
		FullClone: *fullClone,
	}

	// If neither specified, let the clone package decide based on state file
	if !*fullClone && !*diffClone {
		// Mode will be determined by clone package
	} else if *fullClone {
		opts.FullClone = true
	}

	if !jsonOutput {
		mode := "auto"
		if *fullClone {
			mode = "full"
		} else if *diffClone {
			mode = "diff"
		}
		fmt.Printf("[i] Cloning %s to %s (mode: %s)...\n", serverURL, targetDir, mode)
	}

	result, err := clone.Clone(serverURL, targetDir, opts)
	if err != nil {
		exitError("Failed to clone: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "clone",
			"data": map[string]interface{}{
				"target_dir":              result.TargetDir,
				"posts_downloaded":        result.PostsDownloaded,
				"comments_downloaded":     result.CommentsDownloaded,
				"blessed_comments_synced": result.BlessedCommentsSynced,
				"tags_downloaded":         result.TagsDownloaded,
				"attestations_downloaded": result.AttestationsDownloaded,
				"license_cloned":          result.LicenseCloned,
				"did_document_cloned":     result.DIDDocumentCloned,
				"not_enumerable":          result.NotEnumerable,
				"rejected_paths":          result.RejectedPaths,
				"errors":                  result.Errors,
			},
		})
	} else {
		fmt.Println()
		fmt.Println("[✓] Clone complete!")
		fmt.Printf("  Posts downloaded: %d\n", result.PostsDownloaded)
		fmt.Printf("  Comments downloaded: %d\n", result.CommentsDownloaded)
		fmt.Printf("  Blessed comments synced: %d\n", result.BlessedCommentsSynced)
		fmt.Printf("  Tags downloaded: %d\n", result.TagsDownloaded)
		fmt.Printf("  Attestations downloaded: %d\n", result.AttestationsDownloaded)
		fmt.Printf("  Licence document: %s\n", presence(result.LicenseCloned))
		fmt.Printf("  DID document: %s\n", presence(result.DIDDocumentCloned))
		// State what could NOT be collected. A clone that omits a type in
		// silence makes a later `polis validate` look like it found nothing
		// wrong with artifacts nobody fetched.
		if len(result.NotEnumerable) > 0 {
			fmt.Printf("  Not collected (the site publishes no index entries for these): %s\n", strings.Join(result.NotEnumerable, ", "))
		}
		// Paths the source site supplied that would have landed outside the
		// clone folder. Skipped, and said so.
		if len(result.RejectedPaths) > 0 {
			fmt.Printf("  Refused (would write outside the clone folder): %s\n", strings.Join(result.RejectedPaths, ", "))
		}
		if result.Errors > 0 {
			fmt.Printf("  Errors: %d\n", result.Errors)
		}
		fmt.Printf("  Target directory: %s\n", result.TargetDir)
	}
}
