package cmd

import (
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
)

func handleUnfollow(args []string) {
	if len(args) < 1 {
		exitError("Usage: polis unfollow <author-url>")
	}

	authorURL := args[0]
	dir := getDataDir()

	// Validate URL format
	if len(authorURL) < 8 || authorURL[:8] != "https://" {
		exitError("Author URL must use HTTPS (e.g., https://example.com)")
	}

	if !isPolisSite(dir) {
		exitError("Polis not initialized. Run 'polis init' first.")
	}

	// Load discovery client
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}
	baseURL := os.Getenv("POLIS_BASE_URL")

	// ⭐ The one implementation of unfollowing (ops.Unfollow): the site's rules,
	// the signed follow list through its own write point, the stream events.
	// This command used to deny the author's blessings and rewrite
	// following.json by hand, so the DS and other sites never heard of it.
	result, err := ops.Unfollow(cliContext(), ops.FollowSite{
		Site: getSite(), PrivateKey: privKey, BaseURL: baseURL, DiscoveryURL: discoveryURL,
		DS:     discovery.NewAuthenticatedClient(discoveryURL, discovery.ExtractDomainFromURL(baseURL), privKey),
		Remote: remote.NewClient(),
		Emit:   func(action string, fields map[string]any) { logCLIAction(action, fields) },
	}, authorURL)
	if err != nil {
		exitError("Failed to unfollow %s: %v", authorURL, err)
	}
	commentCount, deniedCount, failedCount, removed := result.CommentsFound, result.CommentsDenied, result.CommentsFailed, result.WasFollowing

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "unfollow",
			"data": map[string]interface{}{
				"author_url":             authorURL,
				"comments_found":         commentCount,
				"comments_denied":        deniedCount,
				"removed_from_following": removed,
			},
		})
	} else {
		fmt.Println()
		fmt.Printf("[✓] Successfully unfollowed %s\n", authorURL)
		fmt.Printf("  - Removed from following.json\n")
		if commentCount > 0 {
			if failedCount == 0 {
				fmt.Printf("  - Denied %d blessed comment(s)\n", deniedCount)
			} else {
				fmt.Printf("  - Denied %d/%d comment(s) (%d failed)\n", deniedCount, commentCount, failedCount)
			}
		}
	}
}
