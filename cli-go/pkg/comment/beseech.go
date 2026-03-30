package comment

import (
	"fmt"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Discovery service configuration. Set by the calling application
// (CLI or webapp) during initialization. Required for BeseechComment.
//
// For multi-tenant use (e.g., hosted service), pass a *DiscoveryConfig
// to BeseechComment instead of using these globals.
var (
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string
)

// DiscoveryConfig holds per-tenant discovery service configuration.
// When passed to BeseechComment, it overrides the package-level globals,
// enabling safe multi-tenant operation.
type DiscoveryConfig struct {
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string
}

// BeseechResult contains the result of a comment beseech request.
type BeseechResult struct {
	Success     bool         `json:"success"`
	Status      string       `json:"status"`  // "created" or "updated"
	Message     string       `json:"message"`
	AutoBlessed bool         `json:"auto_blessed"` // true if auto-blessed by discovery
	Comment     *CommentMeta `json:"comment"`      // comment metadata (for callers that need it for hooks etc.)
}

// BeseechComment registers a pending comment with the discovery service
// and handles auto-blessing. If the discovery service grants the blessing
// automatically (e.g., self-comment, followed author), the comment is
// moved to blessed status.
//
// If dsCfg is non-nil, it overrides package-level discovery globals for
// multi-tenant safety. Pass nil to use globals (single-tenant / CLI mode).
//
// Returns an error if discovery is not configured or the request fails.
func BeseechComment(dataDir, commentID string, privateKey []byte, dsCfg ...*DiscoveryConfig) (*BeseechResult, error) {
	var dsURL, dsKey, baseURL string
	if len(dsCfg) > 0 && dsCfg[0] != nil {
		dsURL = dsCfg[0].DiscoveryURL
		dsKey = dsCfg[0].DiscoveryKey
		baseURL = dsCfg[0].BaseURL
	} else {
		dsURL = DiscoveryURL
		dsKey = DiscoveryKey
		baseURL = BaseURL
	}

	if dsURL == "" {
		return nil, fmt.Errorf("discovery service not configured")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("POLIS_BASE_URL not configured")
	}

	if !discovery.IsRegisteredLocally(dataDir, dsURL) {
		return nil, fmt.Errorf("site not registered with discovery service (run 'polis register' first)")
	}

	// Get the pending comment
	signed, err := GetComment(dataDir, commentID, StatusPending)
	if err != nil {
		return nil, fmt.Errorf("comment not found in pending: %w", err)
	}

	// Publish the comment to the public comments/ directory before DS registration.
	// This makes the comment accessible via HTTPS so the post owner can fetch it
	// when reviewing the blessing request (matches bash CLI behavior).
	if err := PublishComment(dataDir, commentID); err != nil {
		return nil, fmt.Errorf("publish comment: %w", err)
	}

	// Compute comment URL from base URL + date directory + comment ID
	ts, err := time.Parse("2006-01-02T15:04:05Z", signed.Meta.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}
	dateDir := ts.Format("20060102")
	commentURL := fmt.Sprintf("%s/comments/%s/%s.md", baseURL, dateDir, commentID)

	// Build metadata for polis.comment content registration
	commentMetadata := map[string]interface{}{
		"in_reply_to": signed.Meta.InReplyTo,
		"root_post":   signed.Meta.RootPost,
		"timestamp":   signed.Meta.Timestamp,
	}
	if signed.Meta.InReplyToVersion != "" {
		commentMetadata["in_reply_to_version"] = signed.Meta.InReplyToVersion
	}

	// Build canonical JSON for signing
	canonical, err := discovery.MakeContentCanonicalJSON(
		"pub.polis.comment", commentURL, signed.Meta.CommentVersion, signed.Meta.Author, commentMetadata,
	)
	if err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}

	// Sign the canonical payload
	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	// Register with discovery service
	client := discovery.NewClient(dsURL, dsKey)
	contentReq := &discovery.ContentRegisterRequest{
		Type:      "pub.polis.comment",
		URL:       commentURL,
		Version:   signed.Meta.CommentVersion,
		Author:    signed.Meta.Author,
		Metadata:  commentMetadata,
		Signature: sig,
	}

	resp, err := client.RegisterContent(contentReq)
	if err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}

	result := &BeseechResult{
		Success: resp.Success,
		Status:  resp.Status,
		Message: resp.Message,
		Comment: signed.Meta,
	}

	// If auto-blessed, move to blessed directory
	if resp.RelationshipStatus == "granted" {
		if err := MoveComment(dataDir, commentID, StatusPending, StatusBlessed); err != nil {
			return result, fmt.Errorf("move auto-blessed comment: %w", err)
		}
		result.AutoBlessed = true
	}

	return result, nil
}
