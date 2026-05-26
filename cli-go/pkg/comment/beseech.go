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
	Generator    string // e.g. "polis-cli-go/0.59.0" — used in comment metadata
}

// BeseechResult contains the result of a comment beseech request.
type BeseechResult struct {
	Success     bool         `json:"success"`
	Status      string       `json:"status"` // "created" or "updated"
	Message     string       `json:"message"`
	AutoBlessed bool         `json:"auto_blessed"` // true if auto-blessed by discovery
	Comment     *CommentMeta `json:"comment"`      // comment metadata (for callers that need it for hooks etc.)
}

// registerCommentContent registers (or re-registers) a comment's content with
// the discovery service. Shared by BeseechComment (first publish) and
// RepublishComment (subsequent versions).
//
// Re-registering an already-known (type, url) pair makes the DS emit
// pub.polis.comment.republished and preserve any existing granted/denied blessing
// relationship — see handleCommentBlessing in
// discovery-service/core/handlers/content.ts. The comment URL is derived from
// baseURL + the comment's published date + ID, so callers MUST keep the published
// timestamp stable across republishes to keep the URL — and thus the DS update
// detection — stable.
func registerCommentContent(meta *CommentMeta, commentID string, privateKey []byte, dsURL, dsKey, baseURL string) (*discovery.ContentRegisterResponse, error) {
	ts, err := time.Parse("2006-01-02T15:04:05Z", meta.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}
	dateDir := ts.Format("20060102")
	commentURL := fmt.Sprintf("%s/comments/%s/%s.md", baseURL, dateDir, commentID)

	commentMetadata := map[string]interface{}{
		"in_reply_to": meta.InReplyTo,
		"root_post":   meta.RootPost,
		"timestamp":   meta.Timestamp,
	}
	if meta.InReplyToVersion != "" {
		commentMetadata["in_reply_to_version"] = meta.InReplyToVersion
	}

	canonical, err := discovery.MakeContentCanonicalJSON(
		"pub.polis.comment", commentURL, meta.CommentVersion, meta.Author, commentMetadata,
	)
	if err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}

	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	client := discovery.NewClient(dsURL, dsKey)
	return client.RegisterContent(&discovery.ContentRegisterRequest{
		Type:      "pub.polis.comment",
		URL:       commentURL,
		Version:   meta.CommentVersion,
		Author:    meta.Author,
		Metadata:  commentMetadata,
		Signature: sig,
	})
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

	// Get the pending comment
	signed, err := GetComment(dataDir, commentID, StatusPending)
	if err != nil {
		return nil, fmt.Errorf("comment not found in pending: %w", err)
	}

	// Publish the comment to the public comments/ directory before DS registration.
	// This makes the comment accessible via HTTPS so the post owner can fetch it
	// when reviewing the blessing request (matches bash CLI behavior). Runs
	// regardless of DS-registration status — when the site isn't registered we
	// still want the comment to land on the local public mount so the user can
	// share its URL / register later and resync.
	if err := PublishComment(dataDir, commentID); err != nil {
		return nil, fmt.Errorf("publish comment: %w", err)
	}

	// Registration check is now AFTER the local publish step. When the site
	// hasn't registered with the DS, we don't have a relationship to invoke
	// for blessing — short-circuit with a "deferred" result instead of an
	// error. The comment is already signed (sign step) and on the public
	// mount (PublishComment above); only the DS registration is deferred
	// until the user runs 'polis register' and re-beseeches. Returning an
	// error here would force callers to treat a known + expected state
	// (unregistered site = no DS contact) as a runtime failure, which
	// produced confusing HTTP 500s on the v4 SPA's first comment.
	if !discovery.IsRegisteredLocally(dataDir, dsURL) {
		return &BeseechResult{
			Success: true,
			Status:  "deferred",
			Message: "Site not registered with discovery — comment saved locally. Register and re-beseech to send for blessing.",
			Comment: signed.Meta,
		}, nil
	}

	// Register the comment content with the discovery service.
	resp, err := registerCommentContent(signed.Meta, commentID, privateKey, dsURL, dsKey, baseURL)
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
