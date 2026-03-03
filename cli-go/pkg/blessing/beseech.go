package blessing

import (
	"fmt"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// BeseechRequest contains the data needed to request a blessing.
type BeseechRequest struct {
	CommentURL       string
	CommentVersion   string
	InReplyTo        string
	InReplyToVersion string
	RootPost         string
	Author           string
}

// BeseechResult contains the result of a blessing request.
type BeseechResult struct {
	Success  bool   `json:"success"`
	Status   string `json:"status"` // "pending" or "granted" (auto-blessed)
	Message  string `json:"message,omitempty"`
}

// Beseech sends a blessing request to the discovery service.
// This is called when publishing a comment - the author requests the post owner to bless it.
// Uses the unified content-register endpoint with type=polis.comment.
func Beseech(req *BeseechRequest, client *discovery.Client, privateKey []byte) (*BeseechResult, error) {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Build metadata for polis.comment
	metadata := map[string]interface{}{
		"in_reply_to":         req.InReplyTo,
		"in_reply_to_version": req.InReplyToVersion,
		"root_post":           req.RootPost,
		"timestamp":           timestamp,
	}

	// Create canonical JSON for signing
	canonicalJSON, err := discovery.MakeContentCanonicalJSON(
		"pub.polis.comment",
		req.CommentURL,
		req.CommentVersion,
		req.Author,
		metadata,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create canonical JSON: %w", err)
	}

	// Sign the canonical JSON
	signature, err := signing.SignContent(canonicalJSON, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	// Send the content registration request
	contentReq := &discovery.ContentRegisterRequest{
		Type:      "pub.polis.comment",
		URL:       req.CommentURL,
		Version:   req.CommentVersion,
		Author:    req.Author,
		Metadata:  metadata,
		Signature: signature,
	}

	resp, err := client.RegisterContent(contentReq)
	if err != nil {
		return nil, fmt.Errorf("beseech request failed: %w", err)
	}

	// Map relationship_status back to blessing status
	status := "pending"
	if resp.RelationshipStatus == "granted" {
		status = "granted"
	}

	return &BeseechResult{
		Success: resp.Success,
		Status:  status,
		Message: resp.Message,
	}, nil
}
