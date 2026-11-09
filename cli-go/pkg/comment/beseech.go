package comment

import (
	"context"
	"fmt"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	// ⚠️ ALIASED: `site` is the parameter name throughout the site-handle
	// migration, and an unaliased import of this package shadows it.
	sitepkg "github.com/vdibart/polis-cli/cli-go/pkg/site"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Discovery service configuration. Set by the calling application
// (CLI or webapp) during initialization. Required for BeseechComment.
//
// For multi-tenant use (e.g., hosted service), pass a *DiscoveryConfig
// to BeseechComment instead of using these globals.
var (
	DiscoveryURL string
	BaseURL      string
)

// DiscoveryConfig holds per-tenant discovery service configuration.
// When passed to BeseechComment, it overrides the package-level globals,
// enabling safe multi-tenant operation.
type DiscoveryConfig struct {
	DiscoveryURL string
	BaseURL      string
	Generator    string // e.g. "polis-cli-go/0.59.0" — used in comment metadata
}

// BeseechResult contains the result of a comment beseech request.
type BeseechResult struct {
	Success     bool         `json:"success"`
	Status      string       `json:"status"` // "created" or "updated"
	Message     string       `json:"message"`
	AutoBlessed bool         `json:"auto_blessed"` // true only if the post author had already blessed it (the DS decides nothing)
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
// existing clients/signatures are unaffected.
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

func registerCommentContent(site storage.Site, meta *CommentMeta, commentID string, privateKey []byte, dsURL, baseURL string, beseech bool) (*discovery.ContentRegisterResponse, error) {
	ts, err := time.Parse("2006-01-02T15:04:05Z", meta.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}
	dateDir := ts.Format("20060102")
	// Register the CANONICAL source-path URL (content/pub.polis.core/comment/...),
	// matching where the signed .md lives and how posts register — NOT the
	// /comments/ mount path. The registered URL is signature-bound and the DS's
	// (type,url) upsert key, so it must be the canonical artifact's; the mount
	// path only reaches the .md through a permanent 301.
	commentURL := polisurl.CommentContentURL(baseURL, dateDir, commentID)

	commentMetadata := commentRegistrationMetadata(meta, beseech)
	// SIGNET epic 32: bind the DS's witness to the whole published comment, not
	// only the body `version` hashes. The file is the one PublishComment (or
	// RepublishComment) just wrote; if it cannot be read the registration goes
	// ahead and the witness binds `version` alone.
	publishedKey := "content/pub.polis.core/comment/" + dateDir + "/" + commentID + ".md"
	if data, rerr := site.Store().Get(context.Background(), publishedKey); rerr == nil {
		commentMetadata[discovery.MetadataArtifactHash] = discovery.ArtifactHash(
			signing.MarkdownSigningBase(string(data), signing.TypeComment))
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

	client := discovery.NewClient(dsURL)
	resp, err := client.RegisterContent(&discovery.ContentRegisterRequest{
		Type:      "pub.polis.comment",
		URL:       commentURL,
		Version:   meta.CommentVersion,
		Author:    meta.Author,
		Metadata:  commentMetadata,
		Signature: sig,
	})
	if err == nil && resp != nil && resp.Witness != nil {
		// Never fails the registration; see publish.RegisterPost.
		if _, werr := sitepkg.RecordWitness(site, commentURL, *resp.Witness); werr != nil {
			fmt.Printf("[!] Could not publish the discovery service's witness for %s: %v\n", commentURL, werr)
		}
	}
	return resp, err
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
func BeseechComment(site storage.Site, commentID string, privateKey []byte, dsCfg ...*DiscoveryConfig) (*BeseechResult, error) {
	return publishAndRegisterComment(site, commentID, privateKey, true, dsCfg...)
}

// publishAndRegisterComment publishes a pending comment to the public content
// tree and registers it with the discovery service. The beseech parameter
// DECOUPLES publication from the blessing request: when true the DS
// also records a (pending) blessing request (the default); when false the comment is
// registered as content only (publish-only, via signed metadata.beseech=false).
// Either way PublishComment runs FIRST and unconditionally — so a comment is
// always published, indexed, rendered, and discoverable regardless of whether a
// blessing is ever requested (it is never stuck unpublished for lack of a
// beseech).
func publishAndRegisterComment(site storage.Site, commentID string, privateKey []byte, beseech bool, dsCfg ...*DiscoveryConfig) (*BeseechResult, error) {
	// ⛔ Before anything else: the id is a path below, and it arrives from a
	// request body. Refusing here keeps a traversing id from reaching the
	// discovery service as well as the disk.
	if err := lookupCommentID(commentID); err != nil {
		return nil, err
	}
	var dsURL, baseURL string
	if len(dsCfg) > 0 && dsCfg[0] != nil {
		dsURL = dsCfg[0].DiscoveryURL
		baseURL = dsCfg[0].BaseURL
	} else {
		dsURL = DiscoveryURL
		baseURL = BaseURL
	}

	if dsURL == "" {
		return nil, fmt.Errorf("discovery service not configured")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("POLIS_BASE_URL not configured")
	}

	// Get the pending comment
	signed, err := GetComment(site, commentID, StatusPending)
	if err != nil {
		return nil, fmt.Errorf("comment not found in pending: %w", err)
	}

	// Publish the comment to the public content tree BEFORE DS registration —
	// ALWAYS, regardless of beseech. Makes the comment fetchable via HTTPS (so a
	// post owner reviewing a blessing request can fetch it) and, for the
	// publish-only path, ensures it is published even when no blessing is asked.
	// Runs regardless of DS-registration status — when the site isn't registered
	// we still want the comment on the local public mount.
	if err := PublishComment(site, commentID); err != nil {
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
	if !discovery.IsRegisteredLocally(site, dsURL) {
		return &BeseechResult{
			Success: true,
			Status:  "deferred",
			Message: "Site not registered with discovery — comment saved locally. Register and re-beseech to send for blessing.",
			Comment: signed.Meta,
		}, nil
	}

	// Register the comment content with the discovery service (beseech controls
	// whether the DS also requests a blessing).
	resp, err := registerCommentContent(site, signed.Meta, commentID, privateKey, dsURL, baseURL, beseech)
	if err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}

	result := &BeseechResult{
		Success: resp.Success,
		Status:  resp.Status,
		Message: resp.Message,
		Comment: signed.Meta,
	}

	// ⚠️ Signet epic 45: the discovery service decides nothing for the post
	// author, so a FIRST registration always answers "pending" and the post
	// author's own site decides later — the commenter learns asynchronously, and
	// no caller may promise a blessing here. "granted" now means only that the
	// post author blessed this comment earlier (a re-registration), which is a
	// fact worth reflecting locally. (Never granted when beseech=false.)
	if resp.RelationshipStatus == "granted" {
		if err := MoveComment(site, commentID, StatusPending, StatusBlessed); err != nil {
			return result, fmt.Errorf("move already-blessed comment: %w", err)
		}
		result.AutoBlessed = true
	}

	return result, nil
}
