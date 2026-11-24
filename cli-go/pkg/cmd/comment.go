package cmd

import (
	"context"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
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
	case "send":
		handleCommentSend(subArgs)
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
  sign <id>          Sign a draft comment, publish it and send it to the
                     post's author for blessing
  send <id>          Send a pending comment again (after a failed send, or
                     once the site is registered)
  list [status]      List comments (drafts, pending, blessed, denied)
  sync               Check pending comments' blessing status and file them
                     as blessed or denied

Examples:
  polis comment draft https://alice.polis.pub/posts/20260201/hello.md
  polis comment sign abc123
  polis comment send abc123
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

	if err := comment.SaveDraft(storage.At(dir), draft); err != nil {
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
		fmt.Printf("Edit at: .polis/bundles/pub.polis.core/comments/drafts/%s.md\n", draft.ID)
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
	draft, err := comment.LoadDraft(storage.At(dir), draftID)
	if err != nil {
		exitError("Failed to load draft: %v", err)
	}

	// Load private key
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	// Get author domain from .well-known/polis (domain is the public identity)
	wk, err := site.LoadWellKnown(getSite())
	if err != nil {
		exitError("Failed to load .well-known/polis: %v", err)
	}

	// Get site URL from env
	siteURL := os.Getenv("POLIS_BASE_URL")
	if siteURL == "" {
		exitError("POLIS_BASE_URL not set")
	}

	_ = wk // the site must have one; the author is the site's domain

	// ⭐ The one implementation of signing a comment (ops.SignComment): the
	// author is the site's domain and the generator this build's, as the API
	// and the widget sign.
	signed, err := ops.SignComment(ops.CommentSite{Site: storage.At(dir), PrivateKey: privKey, BaseURL: siteURL}, draft)
	if err != nil {
		exitError("Failed to sign comment: %v", err)
	}
	if !jsonOutput {
		fmt.Printf("Signed comment: %s\n", signed.Meta.ID)
		fmt.Printf("Version: %s\n", signed.Meta.CommentVersion)
	}
	// Then send it, as the API's comment.create does after signing.
	reportCommentSent(signed.Meta.ID, map[string]interface{}{
		"id":        signed.Meta.ID,
		"version":   signed.Meta.CommentVersion,
		"signature": signed.Signature,
	}, sendComment(dir, privKey, signed.Meta.ID))
}

func handleCommentSend(args []string) {
	if len(args) < 1 {
		exitError("Usage: polis comment send <comment-id>")
	}
	dir := getDataDir()
	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}
	reportCommentSent(args[0], map[string]interface{}{"id": args[0]}, sendComment(dir, privKey, args[0]))
}

// commentSendResult is what sending a pending comment came to.
type commentSendResult struct {
	Status, Message string
	AutoBlessed     bool
	Err             error
}

// sendComment publishes a pending comment signed here and sends it to the
// discovery service with its blessing request. ⭐ It runs the API's own
// operation in-process (comment.create with a comment_id, which is
// ops.SendComment), so what the send causes (the render, the event) is the
// shared after-write module's, with the CLI's parts (no hook).
func sendComment(dir string, privKey []byte, commentID string) commentSendResult {
	pubKey, _ := storage.NewPOSIX(dir).Get(context.Background(), ".polis/keys/id_ed25519.pub")
	engine, err := cliEngine(dir, privKey, pubKey)
	if err != nil {
		return commentSendResult{Err: err}
	}
	res, err := engine.Dispatch(cliContext(), ops.ActionRequest{Action: "create", ContentType: "pub.polis.comment",
		Payload: map[string]any{"comment_id": commentID}})
	if err != nil {
		return commentSendResult{Err: err}
	}
	str := func(k string) string { v, _ := res.Data[k].(string); return v }
	auto, _ := res.Data["auto_blessed"].(bool)
	return commentSendResult{Status: str("status"), Message: str("message"), AutoBlessed: auto}
}

// reportCommentSent prints what a send came to, and exits non-zero when the
// comment did not reach the discovery service: the reply is signed and kept
// here, but its post's author has not been sent it.
func reportCommentSent(commentID string, out map[string]interface{}, sent commentSendResult) {
	retry := fmt.Sprintf("polis comment send %s", commentID)
	deferred := sent.Err == nil && sent.Status == "deferred"
	reached := sent.Err == nil && !deferred
	out["success"] = reached
	out["sent"] = reached
	if sent.Err != nil {
		out["send_error"] = sent.Err.Error()
	} else {
		out["status"] = sent.Status
		out["message"] = sent.Message
		out["auto_blessed"] = sent.AutoBlessed
	}
	if jsonOutput {
		outputJSON(out)
	} else {
		switch {
		case sent.Err != nil:
			fmt.Fprintf(os.Stderr, "[x] Not sent: %v\n", sent.Err)
			fmt.Fprintf(os.Stderr, "The comment is signed and kept in pending. Send it again with: %s\n", retry)
		case deferred:
			fmt.Println("Published on this site, but not sent: the site is not registered with the discovery service.")
			fmt.Printf("Run 'polis register', then: %s\n", retry)
		case sent.AutoBlessed:
			fmt.Println("Published and sent. The post's author had already blessed it, so it moved to blessed.")
		default:
			fmt.Println("Published and sent to the post's author for blessing. 'polis comment sync' checks for their answer.")
		}
	}
	if !reached {
		os.Exit(1)
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
		drafts, err := comment.ListDrafts(storage.At(dir))
		if err != nil {
			exitError("Failed to list drafts: %v", err)
		}
		for _, d := range drafts {
			results = append(results, d)
		}
	case "pending":
		comments, err := comment.ListComments(storage.At(dir), comment.StatusPending)
		if err != nil {
			exitError("Failed to list pending comments: %v", err)
		}
		for _, c := range comments {
			results = append(results, c)
		}
	case "blessed":
		comments, err := comment.ListComments(storage.At(dir), comment.StatusBlessed)
		if err != nil {
			exitError("Failed to list blessed comments: %v", err)
		}
		for _, c := range comments {
			results = append(results, c)
		}
	case "denied":
		comments, err := comment.ListComments(storage.At(dir), comment.StatusDenied)
		if err != nil {
			exitError("Failed to list denied comments: %v", err)
		}
		for _, c := range comments {
			results = append(results, c)
		}
	case "all":
		// List all
		drafts, _ := comment.ListDrafts(storage.At(dir))
		for _, d := range drafts {
			results = append(results, map[string]interface{}{
				"status": "draft",
				"id":     d.ID,
			})
		}
		pending, _ := comment.ListComments(storage.At(dir), comment.StatusPending)
		for _, c := range pending {
			results = append(results, map[string]interface{}{
				"status": "pending",
				"id":     c.ID,
			})
		}
		blessed, _ := comment.ListComments(storage.At(dir), comment.StatusBlessed)
		for _, c := range blessed {
			results = append(results, map[string]interface{}{
				"status": "blessed",
				"id":     c.ID,
			})
		}
		denied, _ := comment.ListComments(storage.At(dir), comment.StatusDenied)
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
	if discoveryURL == "" {
		discoveryURL = "https://ds.polis.pub"
	}

	baseURL := os.Getenv("POLIS_BASE_URL")
	myDomain := discovery.ExtractDomainFromURL(baseURL)
	privKey, _ := loadPrivateKey(dir)
	client := discovery.NewAuthenticatedClient(discoveryURL, myDomain, privKey)

	result, err := comment.SyncPendingComments(storage.At(dir), baseURL, client, nil)
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
