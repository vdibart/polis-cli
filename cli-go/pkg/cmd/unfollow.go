package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
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
	apiKey := os.Getenv("DISCOVERY_SERVICE_KEY")
	client := discovery.NewClient(discoveryURL, apiKey)

	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// Fetch author email
	remoteClient := remote.NewClient()
	remoteWK, err := remoteClient.FetchWellKnown(authorURL)
	if err != nil {
		// Continue even if we can't fetch - just remove from following
		remoteWK = nil
	}

	// Deny any blessed comments from this author
	deniedCount := 0
	failedCount := 0
	commentCount := 0

	if remoteWK != nil {
		authorDomain := discovery.ExtractDomainFromURL(authorURL)
		// Fetch granted blessings where source is from the author's domain
		grantedResp, _ := client.QueryRelationships("pub.polis.comment.blessing", map[string]string{
			"status": "granted",
		})

		if grantedResp != nil {
			for _, rel := range grantedResp.Records {
				if discovery.ExtractDomainFromURL(rel.SourceURL) == authorDomain {
					commentCount++
					if err := client.UpdateRelationship("pub.polis.comment.blessing", rel.SourceURL, rel.TargetURL, "deny", privKey); err != nil {
						failedCount++
						continue
					}
					deniedCount++
				}
			}
		}
	}

	// Remove from following.json
	followingPath := filepath.Join(dir, "content", "pub.polis.core", "follow", "following.json")
	f, err := following.Load(followingPath)
	if err != nil {
		exitError("Failed to load following.json: %v", err)
	}

	removed := f.Remove(authorURL)

	if err := following.Save(followingPath, f); err != nil {
		exitError("Failed to save following.json: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "unfollow",
			"data": map[string]interface{}{
				"author_url":            authorURL,
				"comments_found":        commentCount,
				"comments_denied":       deniedCount,
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
