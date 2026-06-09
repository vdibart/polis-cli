package comment

import (
	"fmt"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
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
// commentRegistrationMetadata builds the signed metadata for a comment content
// registration. When beseech is false it adds metadata.beseech=false, telling
// the DS to register the comment as content WITHOUT requesting a blessing
// (publish-only). When beseech is true the flag is OMITTED, so the payload is
// byte-identical to the historical default (publish + beseech together) and
// existing clients/signatures are unaffected. Defect 4,
// plans/comment-registration-severe-bug.md.
func commentRegistrationMetadata(meta *CommentMeta, beseech bool) map[string]interface{} {
	m := map[string]interface{}{
		"in_reply_to": meta.InReplyTo,
		"root_post":   meta.RootPost,
		"timestamp":   meta.Timestamp,
	}
	if meta.InReplyToVersion != "" {
		m["in_reply_to_version"] = meta.InReplyToVersion
	}
	if !beseech {
		m["beseech"] = false
	}
	return m
}

func registerCommentContent(meta *CommentMeta, commentID string, privateKey []byte, dsURL, dsKey, baseURL string, beseech bool) (*discovery.ContentRegisterResponse, error) {
	ts, err := time.Parse("2006-01-02T15:04:05Z", meta.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}
	dateDir := ts.Format("20060102")
	// Register the CANONICAL source-path URL (content/pub.polis.core/comment/...),
	// matching where the signed .md lives and how posts register — NOT the
	// /comments/ mount path. Defect 1, plans/comment-registration-severe-bug.md.
	commentURL := polisurl.CommentContentURL(baseURL, dateDir, commentID)

	commentMetadata := commentRegistrationMetadata(meta, beseech)

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

// BeseechComment publishes a pending comment, registers it with the discovery
// service, and REQUESTS a blessing — the default "publish + beseech together"
// flow. It is a thin wrapper over publishAndRegisterComment with beseech=true,
// preserving the historical behavior (and byte-identical registration payload).
//
// If dsCfg is non-nil, it overrides package-level discovery globals for
// multi-tenant safety. Pass nil to use globals (single-tenant / CLI mode).
//
// Returns an error if discovery is not configured or the request fails.
func BeseechComment(dataDir, commentID string, privateKey []byte, dsCfg ...*DiscoveryConfig) (*BeseechResult, error) {
	return publishAndRegisterComment(dataDir, commentID, privateKey, true, dsCfg...)
}

// publishAndRegisterComment publishes a pending comment to the public content
// tree and registers it with the discovery service. The beseech parameter
// DECOUPLES publication from the blessing request (Defect 4): when true the DS
// also evaluates/requests a blessing (the default); when false the comment is
// registered as content only (publish-only, via signed metadata.beseech=false).
// Either way PublishComment runs FIRST and unconditionally — so a comment is
// always published, indexed, rendered, and discoverable regardless of whether a
// blessing is ever requested (it is never stuck unpublished for lack of a
// beseech). plans/comment-registration-severe-bug.md.
func publishAndRegisterComment(dataDir, commentID string, privateKey []byte, beseech bool, dsCfg ...*DiscoveryConfig) (*BeseechResult, error) {
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

	// Publish the comment to the public content tree BEFORE DS registration —
	// ALWAYS, regardless of beseech. Makes the comment fetchable via HTTPS (so a
	// post owner reviewing a blessing request can fetch it) and, for the
	// publish-only path, ensures it is published even when no blessing is asked.
	// Runs regardless of DS-registration status — when the site isn't registered
	// we still want the comment on the local public mount.
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

	// Register the comment content with the discovery service (beseech controls
	// whether the DS also requests a blessing).
	resp, err := registerCommentContent(signed.Meta, commentID, privateKey, dsURL, dsKey, baseURL, beseech)
	if err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}

	result := &BeseechResult{
		Success: resp.Success,
		Status:  resp.Status,
		Message: resp.Message,
		Comment: signed.Meta,
	}

	// If auto-blessed, move to blessed directory. (A no-op when beseech=false:
	// the DS skips blessing evaluation, so RelationshipStatus is never granted.)
	if resp.RelationshipStatus == "granted" {
		if err := MoveComment(dataDir, commentID, StatusPending, StatusBlessed); err != nil {
			return result, fmt.Errorf("move auto-blessed comment: %w", err)
		}
		result.AutoBlessed = true
	}

	return result, nil
}
