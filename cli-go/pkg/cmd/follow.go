package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

func handleFollow(args []string) {
	if len(args) < 1 {
		exitError("Usage: polis follow <author-url>")
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

	// Get our author email for blessing operations
	wk, err := site.LoadWellKnown(dir)
	if err != nil {
		exitError("Failed to load .well-known/polis: %v", err)
	}
	blessedBy := wk.Email

	// Fetch author info from their site
	remoteClient := remote.NewClient()
	remoteWK, err := remoteClient.FetchWellKnown(authorURL)
	if err != nil {
		exitError("Failed to fetch author information: %v", err)
	}
	authorEmail := remoteWK.Email

	// Load private key for signing
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// Load discovery client (authenticated for pending/denied queries)
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}
	apiKey := os.Getenv("DISCOVERY_SERVICE_KEY")
	baseURL := os.Getenv("POLIS_BASE_URL")
	myDomain := discovery.ExtractDomainFromURL(baseURL)
	client := discovery.NewAuthenticatedClient(discoveryURL, apiKey, myDomain, privKey)

	// Fetch unblessed comments from this author on our posts via relationship-query
	authorDomain := discovery.ExtractDomainFromURL(authorURL)

	pendingResp, err := client.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"status": "pending",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Could not check pending blessings: %v\n", err)
	}
	deniedResp, err := client.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"status": "denied",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Could not check denied blessings: %v\n", err)
	}

	// Filter to relationships where source is from the author's domain
	var allUnblessed []discovery.RelationshipRecord
	if pendingResp != nil {
		for _, r := range pendingResp.Records {
			if discovery.ExtractDomainFromURL(r.SourceURL) == authorDomain {
				allUnblessed = append(allUnblessed, r)
			}
		}
	}
	if deniedResp != nil {
		for _, r := range deniedResp.Records {
			if discovery.ExtractDomainFromURL(r.SourceURL) == authorDomain {
				allUnblessed = append(allUnblessed, r)
			}
		}
	}

	// Bless all unblessed comments
	blessedCount := 0
	failedCount := 0

	for _, rel := range allUnblessed {
		// Grant blessing via relationship-update
		if err := client.UpdateRelationship("pub.polis.comment.blessing", rel.SourceURL, rel.TargetURL, "grant", privKey); err != nil {
			failedCount++
			continue
		}
		blessedCount++
	}

	// Add to following.json
	followingPath := filepath.Join(dir, "content", "pub.polis.core", "follow", "following.json")
	f, err := following.Load(followingPath)
	if err != nil {
		exitError("Failed to load following.json: %v", err)
	}

	f.Add(authorURL)

	if err := following.Save(followingPath, f); err != nil {
		exitError("Failed to save following.json: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "follow",
			"data": map[string]interface{}{
				"author_url":         authorURL,
				"author_email":       authorEmail,
				"blessed_by":         blessedBy,
				"comments_found":     len(allUnblessed),
				"comments_blessed":   blessedCount,
				"added_to_following": true,
			},
		})
	} else {
		fmt.Println()
		fmt.Printf("[✓] Successfully followed %s\n", authorURL)
		fmt.Printf("  - Added to following.json\n")
		if len(allUnblessed) > 0 {
			if failedCount == 0 {
				fmt.Printf("  - Blessed %d comment(s)\n", blessedCount)
			} else {
				fmt.Printf("  - Blessed %d/%d comment(s) (%d failed)\n", blessedCount, len(allUnblessed), failedCount)
			}
		}
	}

	// Suppress unused variable warning
	_ = blessedBy
}
