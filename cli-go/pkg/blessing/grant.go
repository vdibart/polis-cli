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
// 2. Updates the local blessed.json — SIGNED with the blesser's key
//
// ⚠️ Order is DS-then-local for failure reasons, NOT because the DS is
// authoritative. blessed.json is AUTHORED: the author knows what they blessed,
// and the DS is how the network learns it (SIGNET epic 14, D1/D4).
// 3. Optionally runs the post-comment hook
func Grant(siteDir string, request *IncomingRequest, client *discovery.Client, hookConfig *hooks.HookConfig, privateKey []byte) (*GrantResult, error) {
	return grant(siteDir, request, client, hookConfig, privateKey, discovery.AgentMarker{})
}

// grant is Grant with an optional agent marker. The zero marker is the user's
// own act and produces exactly the bytes Grant always has; only GrantAsAgent
// passes a non-zero one, after checking the live grant.
func grant(siteDir string, request *IncomingRequest, client *discovery.Client, hookConfig *hooks.HookConfig, privateKey []byte, marker discovery.AgentMarker) (*GrantResult, error) {
	// Grant via unified relationship-update endpoint
	if err := client.UpdateRelationshipMarked("pub.polis.comment.blessing", request.CommentURL, request.InReplyTo, "grant", marker, privateKey); err != nil {
		return nil, fmt.Errorf("failed to grant blessing: %w", err)
	}

	// Extract post path from in_reply_to URL
	// e.g., https://alice.polis.site/posts/20260127/hello-world.md -> posts/20260127/hello-world.md
	postPath := extractPostPath(request.InReplyTo)

	// Update local blessed-comments.json
	blessedComment := metadata.BlessedComment{
		URL:     request.CommentURL,
		Version: request.CommentVersion,
		// The marker rides on the entry INSIDE the list's signature (epic 11 D7).
		Agent: marker.Agent,
		Grant: marker.Grant,
	}

	// SIGNET epic 14 — SIGNED, because this is the author's own act. The blesser
	// decided; blessed.json records that decision and the DS learns afterward.
	// A nil key falls through to an unsigned write, which is the safe direction.
	if err := metadata.AddBlessedCommentSigned(siteDir, postPath, blessedComment, privateKey); err != nil {
		// Log warning but don't fail - the blessing was granted on discovery service
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
