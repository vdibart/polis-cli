package blessing

import (
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
)

// GrantResult contains the result of granting a blessing.
type GrantResult struct {
	Success        bool   `json:"success"`
	CommentURL     string `json:"comment_url"`
	CommentVersion string `json:"comment_version"`
	PostPath       string `json:"post_path"`
}

// Grant approves a blessing request.
// This:
// 1. Calls the discovery service to grant the blessing via relationship-update
// 2. Updates the local metadata/blessed-comments.json index
// 3. Optionally runs the post-comment hook
func Grant(siteDir string, request *IncomingRequest, client *discovery.Client, hookConfig *hooks.HookConfig, privateKey []byte) (*GrantResult, error) {
	// Grant via unified relationship-update endpoint
	if err := client.UpdateRelationship("pub.polis.comment.blessing", request.CommentURL, request.InReplyTo, "grant", privateKey); err != nil {
		return nil, fmt.Errorf("failed to grant blessing: %w", err)
	}

	// Extract post path from in_reply_to URL
	// e.g., https://alice.polis.site/posts/20260127/hello-world.md -> posts/20260127/hello-world.md
	postPath := extractPostPath(request.InReplyTo)

	// Update local blessed-comments.json
	blessedComment := metadata.BlessedComment{
		URL:     request.CommentURL,
		Version: request.CommentVersion,
	}

	if err := metadata.AddBlessedComment(siteDir, postPath, blessedComment); err != nil {
		// Log warning but don't fail - the blessing was granted on discovery service
		// The local index is a convenience, not the source of truth
		fmt.Printf("[warning] Failed to update blessed-comments.json: %v\n", err)
	}

	// Run post-comment hook if configured
	if hookConfig != nil && hookConfig.PostComment != "" {
		payload := &hooks.HookPayload{
			Event:         hooks.EventPostComment,
			Path:          postPath,
			Title:         request.InReplyTo,
			Version:       request.CommentVersion,
			CommitMessage: hooks.GenerateCommitMessage(hooks.EventPostComment, request.InReplyTo),
		}
		if _, err := hooks.RunHook(siteDir, hookConfig, payload); err != nil {
			// Log warning but don't fail
			fmt.Printf("[warning] post-comment hook failed: %v\n", err)
		}
	}

	return &GrantResult{
		Success:        true,
		CommentURL:     request.CommentURL,
		CommentVersion: request.CommentVersion,
		PostPath:       postPath,
	}, nil
}

// GrantByVersion grants a blessing using just the comment version.
// This is a convenience wrapper when we only have the version string.
func GrantByVersion(siteDir string, commentVersion string, commentURL string, inReplyTo string, client *discovery.Client, hookConfig *hooks.HookConfig, privateKey []byte) (*GrantResult, error) {
	request := &IncomingRequest{
		CommentVersion: commentVersion,
		CommentURL:     commentURL,
		InReplyTo:      inReplyTo,
	}
	return Grant(siteDir, request, client, hookConfig, privateKey)
}

// extractPostPath extracts the relative post path from a full URL.
// e.g., https://alice.polis.site/posts/20260127/hello.md -> posts/20260127/hello.md
func extractPostPath(url string) string {
	// Look for /posts/ in the URL
	idx := strings.Index(url, "/posts/")
	if idx >= 0 {
		return url[idx+1:] // Return "posts/..." without leading slash
	}

	// Fallback: use the URL as-is if we can't parse it
	return url
}
