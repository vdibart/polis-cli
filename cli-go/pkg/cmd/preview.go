package cmd

import (
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
	"github.com/vdibart/polis-cli/cli-go/pkg/verify"
)

func handlePreview(args []string) {
	if len(args) < 1 {
		exitError("Usage: polis preview <url>")
	}

	contentURL := args[0]

	// Validate URL format
	if len(contentURL) < 8 || contentURL[:8] != "https://" {
		exitError("URL must use HTTPS (e.g., https://example.com/posts/hello.md)")
	}

	// The witnessed form: preview is a person asking about one artifact, which is
	// exactly where the witness axis belongs (SIGNET epic 32 D8).
	result, err := verify.VerifyContentWitnessed(contentURL, requestID)
	if err != nil {
		exitError("Failed to preview: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "preview",
			"data": map[string]interface{}{
				"url":               result.URL,
				"type":              result.Type,
				"title":             result.Title,
				"published":         result.Published,
				"current_version":   result.CurrentVersion,
				"generator":         result.Generator,
				"in_reply_to":       nilIfEmpty(result.InReplyTo),
				"author":            result.Author,
				"signature":         result.Signature,
				"hash":              result.Hash,
				"witness":           result.Witness,
				"validation_issues": result.ValidationIssues,
				"body":              result.Body,
			},
		})
	} else {
		// Human-readable output
		fmt.Println()

		// Display frontmatter (dimmed)
		fmt.Println("---")
		fmt.Printf("title: %s\n", result.Title)
		fmt.Printf("type: %s\n", result.Type)
		fmt.Printf("published: %s\n", result.Published)
		fmt.Printf("current-version: %s\n", result.CurrentVersion)
		if result.Generator != "" {
			fmt.Printf("generator: %s\n", result.Generator)
		}
		if result.InReplyTo != "" {
			fmt.Printf("in-reply-to: %s\n", result.InReplyTo)
		}
		fmt.Println("---")

		// Display body content
		fmt.Println(result.Body)

		// Divider before verification
		fmt.Println("---")

		// Signature status
		switch result.Signature.Status {
		case "valid":
			if k := result.Signature.Key; k != nil && k.Source == sitecheck.KeyRetired {
				// SIGNET epic 31 D4: a retired-key pass is a weaker claim and must
				// not print the same line as a current-key one.
				fmt.Println("[✓] Signature verified — against a RETIRED key, not the site's current key")
				fmt.Printf("    %s\n", k.Describe())
			} else {
				fmt.Println("[✓] Signature verified")
			}
		case "invalid":
			fmt.Fprintf(os.Stderr, "[x] Signature INVALID - content may have been tampered with\n")
		case "missing":
			fmt.Fprintf(os.Stderr, "[x] Signature missing\n")
		case "error":
			fmt.Fprintf(os.Stderr, "[!] Could not verify signature\n")
		}

		// Hash status
		switch result.Hash.Status {
		case "valid":
			fmt.Println("[✓] Content hash verified")
		case "mismatch":
			fmt.Fprintf(os.Stderr, "[x] Content hash MISMATCH - content may have been modified\n")
		default:
			fmt.Println("[?] Could not verify hash")
		}

		// Witness (SIGNET epic 32) — a third axis, and never a failure: an
		// unwitnessed artifact is a weaker claim, not a broken one.
		if w := result.Witness; w != nil {
			switch w.State {
			case sitecheck.WitnessStateInvalid:
				fmt.Fprintf(os.Stderr, "[x] %s\n", w.Describe())
			case sitecheck.WitnessWitnessed:
				fmt.Println("[✓] " + w.Describe())
			case sitecheck.WitnessUnverifiable:
				fmt.Println("[?] " + w.Describe())
			default:
				fmt.Println("[i] " + w.Describe())
			}
			if note := w.ContradictsClaimedTime(result.Signature.Key); note != "" {
				fmt.Fprintf(os.Stderr, "[!] The witness and the claimed signing time disagree: %s\n", note)
			}
		}

		// Validation issues
		if len(result.ValidationIssues) > 0 {
			for _, issue := range result.ValidationIssues {
				fmt.Fprintf(os.Stderr, "[!] %s\n", issue)
			}
		}

		fmt.Println()
	}
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
