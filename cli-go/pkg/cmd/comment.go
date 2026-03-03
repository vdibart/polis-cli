package cmd

import (
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

func handleComment(args []string) {
	if len(args) < 1 {
		printCommentUsage()
		os.Exit(1)
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "draft":
		handleCommentDraft(subArgs)
	case "sign":
		handleCommentSign(subArgs)
	case "list":
		handleCommentList(subArgs)
	case "sync":
		handleCommentSync(subArgs)
	case "help", "--help", "-h":
		printCommentUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown comment subcommand: %s\n", subcommand)
		printCommentUsage()
		os.Exit(1)
	}
}

func printCommentUsage() {
	fmt.Print(`Usage: polis comment <subcommand> [options]

Subcommands:
  draft <url>        Create a comment draft replying to <url>
  sign <id>          Sign a draft comment (moves to pending)
  list [status]      List comments (drafts, pending, blessed, denied)
  sync               Sync pending comments with discovery service

Examples:
  polis comment draft https://alice.polis.pub/posts/20260201/hello.md
  polis comment sign abc123
  polis comment list drafts
  polis comment sync
`)
}

func handleCommentDraft(args []string) {
	if len(args) < 1 {
		exitError("Usage: polis comment draft <in-reply-to-url>")
	}

	inReplyTo := polisurl.NormalizeToMD(args[0])
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Create a new draft
	draft := &comment.CommentDraft{
		InReplyTo: inReplyTo,
		RootPost:  inReplyTo, // Initially same as in_reply_to
		Content:   "",
	}

	if err := comment.SaveDraft(dir, draft); err != nil {
		exitError("Failed to create draft: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":     true,
			"id":          draft.ID,
			"in_reply_to": draft.InReplyTo,
		})
	} else {
		fmt.Printf("Created draft: %s\n", draft.ID)
		fmt.Printf("In reply to: %s\n", draft.InReplyTo)
		fmt.Printf("Edit at: .polis/content/pub.polis.core/comments/drafts/%s.md\n", draft.ID)
	}
}

func handleCommentSign(args []string) {
	if len(args) < 1 {
		exitError("Usage: polis comment sign <draft-id>")
	}

	draftID := args[0]
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load the draft
	draft, err := comment.LoadDraft(dir, draftID)
	if err != nil {
		exitError("Failed to load draft: %v", err)
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// Get author domain from .well-known/polis (domain is the public identity)
	wk, err := site.LoadWellKnown(dir)
	if err != nil {
		exitError("Failed to load .well-known/polis: %v", err)
	}

	// Get site URL from env
	siteURL := os.Getenv("POLIS_BASE_URL")
	if siteURL == "" {
		exitError("POLIS_BASE_URL not set")
	}

	// Resolve author identity from POLIS_BASE_URL, fall back to email for backward compat
	authorIdentity := extractDomain(siteURL)
	if authorIdentity == "" && wk.Email != "" {
		authorIdentity = wk.Email
	}
	if authorIdentity == "" {
		exitError("Author identity not configured — set POLIS_BASE_URL")
	}

	// Sign the comment
	signed, err := comment.SignComment(dir, draft, authorIdentity, siteURL, privKey)
	if err != nil {
		exitError("Failed to sign comment: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":   true,
			"id":        signed.Meta.ID,
			"version":   signed.Meta.CommentVersion,
			"signature": signed.Signature,
		})
	} else {
		fmt.Printf("Signed comment: %s\n", signed.Meta.ID)
		fmt.Printf("Version: %s\n", signed.Meta.CommentVersion)
		fmt.Println("Comment moved to pending. Use 'polis comment sync' to request blessing.")
	}
}

func handleCommentList(args []string) {
	status := "all"
	if len(args) > 0 {
		status = args[0]
	}

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	var results []interface{}

	switch status {
	case "drafts", "draft":
		drafts, err := comment.ListDrafts(dir)
		if err != nil {
			exitError("Failed to list drafts: %v", err)
		}
		for _, d := range drafts {
			results = append(results, d)
		}
	case "pending":
		comments, err := comment.ListComments(dir, comment.StatusPending)
		if err != nil {
			exitError("Failed to list pending comments: %v", err)
		}
		for _, c := range comments {
			results = append(results, c)
		}
	case "blessed":
		comments, err := comment.ListComments(dir, comment.StatusBlessed)
		if err != nil {
			exitError("Failed to list blessed comments: %v", err)
		}
		for _, c := range comments {
			results = append(results, c)
		}
	case "denied":
		comments, err := comment.ListComments(dir, comment.StatusDenied)
		if err != nil {
			exitError("Failed to list denied comments: %v", err)
		}
		for _, c := range comments {
			results = append(results, c)
		}
	case "all":
		// List all
		drafts, _ := comment.ListDrafts(dir)
		for _, d := range drafts {
			results = append(results, map[string]interface{}{
				"status": "draft",
				"id":     d.ID,
			})
		}
		pending, _ := comment.ListComments(dir, comment.StatusPending)
		for _, c := range pending {
			results = append(results, map[string]interface{}{
				"status": "pending",
				"id":     c.ID,
			})
		}
		blessed, _ := comment.ListComments(dir, comment.StatusBlessed)
		for _, c := range blessed {
			results = append(results, map[string]interface{}{
				"status": "blessed",
				"id":     c.ID,
			})
		}
		denied, _ := comment.ListComments(dir, comment.StatusDenied)
		for _, c := range denied {
			results = append(results, map[string]interface{}{
				"status": "denied",
				"id":     c.ID,
			})
		}
	default:
		exitError("Unknown status: %s (use: drafts, pending, blessed, denied, all)", status)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"comments": results,
		})
	} else {
		if len(results) == 0 {
			fmt.Printf("No %s comments found.\n", status)
		} else {
			fmt.Printf("%s comments:\n", status)
			for _, r := range results {
				if m, ok := r.(map[string]interface{}); ok {
					fmt.Printf("  [%s] %s\n", m["status"], m["id"])
				} else if c, ok := r.(*comment.CommentMeta); ok {
					fmt.Printf("  %s - %s\n", c.ID, c.InReplyTo)
				} else if d, ok := r.(*comment.CommentDraft); ok {
					fmt.Printf("  %s - %s\n", d.ID, d.InReplyTo)
				}
			}
		}
	}
}

func handleCommentSync(args []string) {
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Load discovery config from env
	discoveryURL := os.Getenv("DISCOVERY_SERVICE_URL")
	discoveryKey := os.Getenv("DISCOVERY_SERVICE_KEY")
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}

	baseURL := os.Getenv("POLIS_BASE_URL")
	myDomain := discovery.ExtractDomainFromURL(baseURL)
	privKey, _ := loadPrivateKey(dir)
	client := discovery.NewAuthenticatedClient(discoveryURL, discoveryKey, myDomain, privKey)

	result, err := comment.SyncPendingComments(dir, baseURL, client, nil)
	if err != nil {
		exitError("Failed to sync comments: %v", err)
	}

	if jsonOutput {
		outputJSON(result)
	} else {
		fmt.Printf("Synced: %d blessed, %d denied, %d still pending\n",
			len(result.Blessed), len(result.Denied), len(result.StillPending))
	}
}
