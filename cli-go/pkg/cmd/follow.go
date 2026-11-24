package cmd

import (
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
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
	wk, err := site.LoadWellKnown(getSite())
	if err != nil {
		exitError("Failed to load .well-known/polis: %v", err)
	}
	blessedBy := wk.Email

	// Load private key for signing
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// The site's own rules decide which waiting comments a follow blesses
	// (ops.Follow loads them). This command used to grant every pending AND
	// denied comment from the author unconditionally, overturning a deny the
	// user had made, with no rule saying so.
	if _, err := policy.LoadPolicies(getSite()); err != nil {
		exitError("Failed to load policies: %v", err)
	}

	// Authenticated discovery client (pending/denied queries need it)
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}
	baseURL := os.Getenv("POLIS_BASE_URL")
	myDomain := discovery.ExtractDomainFromURL(baseURL)
	client := discovery.NewAuthenticatedClient(discoveryURL, myDomain, privKey)

	// ⭐ The one implementation of following (ops.Follow): the same checks,
	// rules, follow and event as /v1 follow.create and the web app.
	result, err := ops.Follow(cliContext(), ops.FollowSite{
		Site: getSite(), PrivateKey: privKey, BaseURL: baseURL, DiscoveryURL: discoveryURL,
		DS: client, Remote: remote.NewClient(),
		Emit: func(action string, fields map[string]any) { logCLIAction(action, fields) },
	}, authorURL)
	if err != nil {
		exitError("Failed to follow %s: %v", authorURL, err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "follow",
			"data": map[string]interface{}{
				"author_url":         authorURL,
				"author_email":       result.AuthorEmail,
				"blessed_by":         blessedBy,
				"comments_found":     result.CommentsFound,
				"comments_blessed":   result.CommentsBlessed,
				"comments_failed":    result.CommentsFailed,
				"blessed_comments":   result.BlessedComments,
				"already_followed":   result.AlreadyFollowed,
				"added_to_following": true,
			},
		})
		return
	}

	fmt.Println()
	if result.AlreadyFollowed {
		fmt.Printf("[i] Already following %s\n", authorURL)
	} else {
		fmt.Printf("[✓] Successfully followed %s\n", authorURL)
		fmt.Printf("  - Added to following.json\n")
	}
	switch {
	case result.CommentsFailed > 0:
		fmt.Printf("  - Blessed %d comment(s) your policies allow (%d failed)\n", result.CommentsBlessed, result.CommentsFailed)
	case result.CommentsBlessed > 0:
		fmt.Printf("  - Blessed %d comment(s) your policies allow\n", result.CommentsBlessed)
	}
	for _, u := range result.BlessedComments {
		fmt.Printf("      %s\n", u)
	}
}
