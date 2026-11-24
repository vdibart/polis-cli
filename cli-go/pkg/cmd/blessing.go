package cmd

import (
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"

	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

func handleBlessing(args []string) {
	if len(args) < 1 {
		printBlessingUsage()
		os.Exit(1)
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "requests", "list":
		handleBlessingRequests(subArgs)
	case "grant":
		handleBlessingGrant(subArgs)
	case "deny":
		handleBlessingDeny(subArgs)
	case "beseech":
		handleBlessingBeseech(subArgs)
	case "sync":
		handleBlessingSync(subArgs)
	case "help", "--help", "-h":
		printBlessingUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown blessing subcommand: %s\n", subcommand)
		printBlessingUsage()
		os.Exit(1)
	}
}

func printBlessingUsage() {
	fmt.Print(`Usage: polis blessing <subcommand> [options]

Subcommands:
  requests              List pending blessing requests on your posts
  grant <version>       Grant a blessing to a comment
  deny <version>        Deny a blessing request
  beseech <version>     Send a pending comment again, by its version
  sync                  Sync blessed comments from discovery service

Examples:
  polis blessing requests
  polis blessing grant sha256:abc123...
  polis blessing deny sha256:abc123...
  polis blessing beseech sha256:abc123...
  polis blessing sync
`)
}

// blessingRequestsPayload builds the JSON-mode payload for `blessing requests`.
//
// The {status, command, data} envelope is the CLI's documented contract and is
// what every consumer parses (docs/cli/user/json-mode.md, the polis skill, and
// the bash e2e suite). This command previously emitted a bare {"requests": ...}
// while its siblings in this file used the envelope, so every one of those
// consumers was broken against the Go binary.
func blessingRequestsPayload(requests []blessing.IncomingRequest) map[string]interface{} {
	if requests == nil {
		requests = []blessing.IncomingRequest{}
	}
	return map[string]interface{}{
		"status":  "success",
		"command": "blessing-requests",
		"data": map[string]interface{}{
			"count":    len(requests),
			"requests": requests,
		},
	}
}

func handleBlessingRequests(args []string) {
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load discovery config from env
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}

	// Get domain from POLIS_BASE_URL
	baseURL := os.Getenv("POLIS_BASE_URL")
	if baseURL == "" {
		exitError("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		exitError("Could not extract domain from POLIS_BASE_URL")
	}

	privKey, _ := loadPrivateKey(dir)
	client := discovery.NewAuthenticatedClient(discoveryURL, domain, privKey)

	requests, err := blessing.FetchPendingRequests(client, domain)
	if err != nil {
		exitError("Failed to fetch requests: %v", err)
	}

	if jsonOutput {
		outputJSON(blessingRequestsPayload(requests))
	} else {
		if len(requests) == 0 {
			fmt.Println("No pending blessing requests.")
		} else {
			fmt.Printf("Pending blessing requests (%d):\n", len(requests))
			for _, req := range requests {
				fmt.Printf("\n  Version: %s\n", req.CommentVersion)
				fmt.Printf("  Author: %s\n", req.Author)
				fmt.Printf("  Reply to: %s\n", req.InReplyTo)
				fmt.Printf("  Comment URL: %s\n", req.CommentURL)
			}
		}
	}
}

func handleBlessingGrant(args []string) {
	if len(args) < 1 {
		exitError("Usage: polis blessing grant <comment-version>")
	}

	commentVersion := args[0]
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// Load discovery config from env
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}

	baseURL := os.Getenv("POLIS_BASE_URL")
	if baseURL == "" {
		exitError("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		exitError("Could not extract domain from POLIS_BASE_URL")
	}

	client := discovery.NewAuthenticatedClient(discoveryURL, domain, privKey)

	// Grant the blessing. The version alone cannot be granted — the DS needs
	// the comment URL and the post it replies to — so the pending request is
	// resolved from it first.
	result, err := blessing.GrantPending(dir, domain, commentVersion, client, nil, privKey)
	if err != nil {
		exitError("Failed to grant blessing: %v", err)
	}

	if jsonOutput {
		outputJSON(result)
	} else {
		fmt.Printf("Blessed comment: %s\n", commentVersion)
		if result.CommentURL != "" {
			fmt.Printf("Comment URL: %s\n", result.CommentURL)
		}
	}
}

func handleBlessingDeny(args []string) {
	if len(args) < 1 {
		exitError("Usage: polis blessing deny <comment-version>")
	}

	commentVersion := args[0]
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// Load discovery config from env
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}

	// To deny, we need to look up the pending relationship first
	baseURL := os.Getenv("POLIS_BASE_URL")
	if baseURL == "" {
		exitError("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(baseURL)

	client := discovery.NewAuthenticatedClient(discoveryURL, domain, privKey)

	// Find the pending request matching this comment version
	targetRequest, err := blessing.ResolvePendingRequest(client, domain, commentVersion)
	if err != nil {
		exitError("%v", err)
	}

	// Deny the blessing
	result, err := blessing.DenyRequest(targetRequest, client, privKey)
	if err != nil {
		exitError("Failed to deny blessing: %v", err)
	}

	if jsonOutput {
		outputJSON(result)
	} else {
		fmt.Printf("Denied blessing for: %s\n", commentVersion)
	}
}

// beseechNotFound is what `polis blessing beseech` says for a version that is
// not one of this site's pending comments. Only a pending comment signed here
// can be sent again; the discovery service keys a blessing on the comment URL
// and its reply target, and a re-registration returns an earlier denied or
// granted answer unchanged, so a denied comment cannot be re-requested.
const beseechNotFound = "no pending comment signed on this site has that version. " +
	"Only a PENDING comment can be re-requested (polis comment send <id> does the same by id). " +
	"A denied comment cannot be re-requested: the discovery service keeps the denial. " +
	"polis republish <comment path> signs a new version and asks for the blessing again"

// handleBlessingBeseech sends a pending comment again by its version, through
// the same send as polis comment send (the API's comment.create by id).
func handleBlessingBeseech(args []string) {
	if len(args) < 1 {
		exitError("Usage: polis blessing beseech <comment-version>")
	}
	version := args[0]
	dir := getDataDir()
	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}
	// Find it here before touching the discovery service.
	var commentID string
	pending, _ := comment.ListComments(storage.At(dir), comment.StatusPending)
	for _, c := range pending {
		if c.CommentVersion == version {
			commentID = c.ID
			break
		}
	}
	if commentID == "" {
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"status":  "error",
				"command": "blessing-beseech",
				"error": map[string]interface{}{
					"code":    "NOT_FOUND",
					"message": beseechNotFound,
				},
			})
		} else {
			fmt.Fprintf(os.Stderr, "[x] Not found: %s\n", beseechNotFound)
		}
		os.Exit(1)
	}
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}
	reportCommentSent(commentID, map[string]interface{}{"id": commentID, "version": version}, sendComment(dir, privKey, commentID))
}

func handleBlessingSync(args []string) {
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load discovery config from env
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}

	baseURL := os.Getenv("POLIS_BASE_URL")
	if baseURL == "" {
		exitError("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		exitError("Could not extract domain from POLIS_BASE_URL")
	}

	client := discovery.NewClient(discoveryURL)

	// Sync blessed comments
	result, err := blessing.SyncBlessedComments(dir, domain, client)
	if err != nil {
		exitError("Failed to sync blessed comments: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "blessing-sync",
			"data": map[string]interface{}{
				"synced":   result.Synced,
				"existing": result.Existing,
				"total":    result.Total,
			},
		})
	} else {
		if result.Synced > 0 {
			fmt.Printf("[✓] Synced %d comment(s) to blessed-comments.json\n", result.Synced)
		} else {
			fmt.Println("[i] Already in sync - no new comments to add")
		}
	}
}
