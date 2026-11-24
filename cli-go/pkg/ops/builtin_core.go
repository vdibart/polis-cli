package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/contentpath"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/draft"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	// ⚠️ ALIASED: `site` is the parameter name in the site-handle migration.
	sitepkg "github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// BuiltinCoreHandler handles operations for the pub.polis.core bundle.
type BuiltinCoreHandler struct{}

// NewBuiltinCoreHandler creates a handler for the builtin core content types.
func NewBuiltinCoreHandler() *BuiltinCoreHandler {
	return &BuiltinCoreHandler{}
}

func (h *BuiltinCoreHandler) Handle(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.ContentType {
	case "pub.polis.post":
		return h.handlePost(ctx, req, env)
	case "pub.polis.comment":
		return h.handleComment(ctx, req, env)
	case "pub.polis.follow":
		return h.handleFollow(ctx, req, env)
	case "pub.polis.dm":
		return h.handleDM(ctx, req, env)
	case "pub.polis.tag":
		return h.handleTag(ctx, req, env)
	case "pub.polis.theme":
		return h.handleTheme(ctx, req, env)
	case "pub.polis.license":
		return h.handleLicense(ctx, req, env)
	default:
		// A type the bundle declares but this handler has no operations for
		// (attestation, actor) is the caller asking for something unsupported,
		// not a server fault — "unsupported content type" reached the v1 API
		// as a 500.
		return nil, fmt.Errorf("unsupported action %q for %s", req.Action, req.ContentType)
	}
}

// Actions lists exactly the actions Handle accepts for a type; /v1/bundles
// publishes it as what a caller may do.
//
// ⭐ DERIVED FROM Operations (builtin_core_ops.go), the one declaration the
// API's description is also generated from. ⚠️ It once advertised post
// get/update/delete/render and the draft verbs, comment list/get/bless/deny/
// revoke/sync and follow create/delete, none of which was ever handled.
// TestActionsAreExactlyWhatTheHandlerAccepts holds both directions.
func (h *BuiltinCoreHandler) Actions(contentType string) []string {
	var out []string
	for _, op := range h.Operations() {
		if op.Type == contentType {
			out = append(out, op.Action)
		}
	}
	return out
}

// ── Licence operations ──────────────────────────────────────────────

// handleLicense serves the site's current terms — the machine endpoint for the
// site default.
//
// This answers "what are her terms NOW?" and nothing else. For "what may I do
// with THIS post?", read the work: its terms are inside its signature, and the
// index carries them too. The two questions are never asked of the same
// artifact, which is why a consumer never has to reconcile them.
func (h *BuiltinCoreHandler) handleLicense(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	case "get":
		terms, err := sitepkg.SiteTerms(env.Resolver.Site())
		if err != nil {
			return nil, fmt.Errorf("read licence: %w", err)
		}
		// A site that has stated nothing returns stated:false rather than an
		// error or an empty grant. Absent is a defined, honest state.
		return &ActionResult{
			Status: "success",
			Data: map[string]any{
				"stated":  terms != nil,
				"terms":   terms,
				"pointer": sitepkg.LicensePointer(env.Resolver.Site()),
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.license", req.Action)
	}
}

// ── Post operations ─────────────────────────────────────────────────

func (h *BuiltinCoreHandler) handlePost(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	case "list":
		return h.listPosts(env)
	case "create":
		return h.publishPost(req, env)
	case "update":
		return h.republishPost(req, env)
	case "save_draft":
		return h.saveDraft(req, env)
	case "discard_draft":
		return h.discardDraft(req, env)
	case "unpublish":
		return h.unpublishPost(req, env)
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.post", req.Action)
	}
}

// saveDraft keeps the owner's unfinished writing (owner-only; see the
// declaration).
func (h *BuiltinCoreHandler) saveDraft(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	markdown, ok := req.Payload["markdown"].(string)
	if !ok {
		return nil, fmt.Errorf("markdown is required")
	}
	id, _ := req.Payload["id"].(string)
	id, err := draft.Save(env.Resolver.Site(), id, markdown)
	if errors.Is(err, draft.ErrTooLarge) {
		return nil, fmt.Errorf("invalid draft: %w", err)
	}
	if err != nil {
		return nil, err
	}
	return &ActionResult{Status: "success", Data: map[string]any{"id": id}}, nil
}

// unpublishPost takes one of the site's own posts down (owner-only; see the
// declaration), through the one implementation, Unpublish.
func (h *BuiltinCoreHandler) unpublishPost(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	path, _ := req.Payload["path"].(string)
	if !strings.HasPrefix(path, postSourcePrefix) {
		return nil, fmt.Errorf("invalid path: it must be one of the site's posts (%s…)", postSourcePrefix)
	}
	res, err := Unpublish(env.Resolver.Site(), path, env.BaseURL, env.DiscoveryURL, env.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &ActionResult{Status: "success", Data: map[string]any{
		"path": res.Path, "url": res.ContentURL, "draft_id": res.DraftID, "discovery": res.Discovery,
	}}, nil
}

// discardDraft removes one of the owner's drafts; one that is not there is
// a 404, so a caller can tell nothing-to-discard from a failure.
func (h *BuiltinCoreHandler) discardDraft(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	id, _ := req.Payload["id"].(string)
	if draft.SanitizeID(id) == "" {
		return nil, fmt.Errorf("id is required")
	}
	err := draft.Discard(env.Resolver.Site(), id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("draft %q: %w", draft.SanitizeID(id), ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return &ActionResult{Status: "success", Data: map[string]any{"id": draft.SanitizeID(id), "discarded": true}}, nil
}

// listPosts reads from content/pub.polis.core/index.jsonl and returns post entries.
func (h *BuiltinCoreHandler) listPosts(env HandlerEnv) (*ActionResult, error) {
	site := env.Resolver.Site()

	data, err := site.Store().Get(context.Background(), "content/pub.polis.core/index.jsonl")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &ActionResult{
				Status: "success",
				Data:   map[string]any{"posts": []any{}, "count": 0},
			}, nil
		}
		return nil, fmt.Errorf("read index: %w", err)
	}

	var posts []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		// Only posts. ⚠️ Key on `type`, never on a path prefix: comment
		// entries are written as content/pub.polis.core/comment/… and never
		// matched the old "comments/" test, so they have always leaked into
		// this list — and tag + attestation entries would join them (Signet
		// epic 25).
		entryType, _ := entry["type"].(string)
		entryPath, _ := entry["path"].(string)
		if entryType != "" {
			if entryType != "post" {
				continue
			}
		} else if !strings.HasPrefix(entryPath, "content/pub.polis.core/post/") &&
			!strings.HasPrefix(entryPath, "posts/") {
			continue
		}
		posts = append(posts, entry)
	}

	// Reverse order (newest first)
	for i, j := 0, len(posts)-1; i < j; i, j = i+1, j-1 {
		posts[i], posts[j] = posts[j], posts[i]
	}

	if posts == nil {
		posts = []map[string]any{}
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"posts": posts,
			"count": len(posts),
		},
	}, nil
}

// publishPost creates and publishes a new post.
func (h *BuiltinCoreHandler) publishPost(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	markdown, _ := req.Payload["markdown"].(string)
	filename, _ := req.Payload["filename"].(string)

	if strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("markdown content required")
	}

	// ⛔ Read the author's per-work licence BEFORE stripping: it lives in the
	// frontmatter that is about to be discarded, and dropping it publishes the
	// work under terms the author did not choose (they are not retroactive).
	authored, _ := license.AuthoredProfile(markdown)
	if publish.HasFrontmatter(markdown) {
		markdown = publish.StripFrontmatter(markdown)
	}
	dsCfg := postDiscoveryConfig(env, authored)

	site := env.Resolver.Site()
	result, err := publish.PublishPostAs(site, markdown, filename, env.PrivateKey, env.Marker, dsCfg)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"path":      result.Path,
			"title":     result.Title,
			"version":   result.Version,
			"signature": result.Signature,
			"url":       result.URL,
		},
	}, nil
}

// postDiscoveryConfig is the publish configuration for a post: the discovery
// service and the site's address where the host has them, and the licence the
// author wrote on this work. With neither, it is nil, and publish falls back to
// its package defaults exactly as before. ⛔ A licence must not be lost because
// the discovery service is unset: then the configuration carries the package
// defaults beside it, so registration behaves as a nil configuration would.
func postDiscoveryConfig(env HandlerEnv, authoredLicense string) *publish.DiscoveryConfig {
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		return &publish.DiscoveryConfig{DiscoveryURL: env.DiscoveryURL, BaseURL: env.BaseURL, LicenseProfile: authoredLicense}
	}
	if authoredLicense == "" {
		return nil
	}
	baseURL := env.BaseURL
	if baseURL == "" {
		baseURL = publish.BaseURL
	}
	return &publish.DiscoveryConfig{DiscoveryURL: publish.DiscoveryURL, BaseURL: baseURL, LicenseProfile: authoredLicense}
}

// postSourcePrefix is where the site's post sources live; republish names
// one of them.
const postSourcePrefix = "content/pub.polis.core/post/"

// republishPost signs a new version of one of the site's own posts, keeping
// its address. ⭐ The same publish.RepublishPost the browser app's
// /api/republish calls: one implementation of republishing.
func (h *BuiltinCoreHandler) republishPost(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	path, _ := req.Payload["path"].(string)
	markdown, _ := req.Payload["markdown"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	// ⛔ The editor's rule, and a post must be a post: republish rewrites the
	// file it names into a signed post.
	if err := contentpath.Validate(path); err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	if !strings.HasPrefix(filepath.Clean(path), postSourcePrefix) {
		return nil, fmt.Errorf("invalid path: not one of the site's posts")
	}
	if strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("markdown is required")
	}
	// ⛔ As on create: the author's licence is read before the strip. A new
	// version carries the terms written on it; the old one keeps its own.
	authored, _ := license.AuthoredProfile(markdown)
	if publish.HasFrontmatter(markdown) {
		markdown = publish.StripFrontmatter(markdown)
	}
	dsCfg := postDiscoveryConfig(env, authored)
	result, err := publish.RepublishPostAs(env.Resolver.Site(), path, markdown, env.PrivateKey, env.Marker, dsCfg)
	if err != nil {
		return nil, fmt.Errorf("republish: %w", err)
	}
	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"path":      result.Path,
			"title":     result.Title,
			"version":   result.Version,
			"signature": result.Signature,
			"url":       result.URL,
		},
	}, nil
}

// ── Comment operations ──────────────────────────────────────────────

func (h *BuiltinCoreHandler) handleComment(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	case "create":
		// ⭐ Two forms: send a comment already signed here (comment_id), or
		// write one from text in one step (in_reply_to + markdown).
		if _, ok := req.Payload["comment_id"]; ok {
			// ⛔ Sending a comment signed earlier would put the owner's
			// unmarked words out as the agent's act. An agent writes its own
			// (marked) comment from text instead. Found by the security
			// review, 2026-11-15.
			if env.Marker.Grant != "" {
				return nil, fmt.Errorf("invalid request: a connected agent cannot send a comment signed earlier; write it from text (in_reply_to and markdown), so it is signed as this connection's")
			}
			return h.beseechComment(req, env)
		}
		return h.writeComment(req, env)
	case "update":
		return h.republishComment(req, env)
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.comment", req.Action)
	}
}

// republishComment produces a new signed version of an already-published comment.
// It expects the content-relative path (as returned by the comment list / index)
// in "path" and the new body in "markdown". The DS re-registration it performs
// emits pub.polis.comment.republished and preserves any granted/denied blessing.
func (h *BuiltinCoreHandler) republishComment(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	path, _ := req.Payload["path"].(string)
	markdown, _ := req.Payload["markdown"].(string)

	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	// ⛔ The editor's rule, and a comment must be a comment: this rewrites the
	// file it names into a signed comment, so an unchecked path could turn any
	// in-site file with a published: line into one.
	if err := contentpath.Validate(path); err != nil {
		return nil, err
	}
	if clean := filepath.ToSlash(filepath.Clean(path)); !strings.HasSuffix(clean, ".md") ||
		!(strings.HasPrefix(clean, "content/pub.polis.core/comment/") || strings.HasPrefix(clean, "comments/")) {
		return nil, fmt.Errorf("path is not a published comment")
	}
	if strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("markdown content required")
	}
	markdown = comment.StripFrontmatter(markdown)

	var dsCfg *comment.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		dsCfg = &comment.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			BaseURL:      env.BaseURL,
		}
	}

	site := env.Resolver.Site()
	result, err := comment.RepublishCommentAs(site, path, markdown, env.PrivateKey, env.Marker, dsCfg)
	if err != nil {
		return nil, fmt.Errorf("republish: %w", err)
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"comment_id":     result.CommentID,
			"path":           result.Path,
			"version":        result.Version,
			"signature":      result.Signature,
			"blessing_state": result.BlessingState,
			"rebeseeched":    result.Rebeseeched,
			"deferred":       result.Deferred,
		},
	}, nil
}

// writeComment signs a new comment from text and sends it to the post's
// author for blessing, in one step. ⭐ The same two calls the browser app makes
// in two requests (/api/comments/sign, then beseech): comment.SignComment,
// then comment.BeseechComment with the id it returns.
func (h *BuiltinCoreHandler) writeComment(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	inReplyTo, _ := req.Payload["in_reply_to"].(string)
	rootPost, _ := req.Payload["root_post"].(string)
	markdown, _ := req.Payload["markdown"].(string)
	if inReplyTo == "" {
		return nil, fmt.Errorf("in_reply_to and markdown are required to write a comment, or comment_id is required to send one already signed here")
	}
	// ⭐ The one implementation of writing a comment (comment_write.go).
	signed, sent, err := WriteComment(commentSiteOf(env), &comment.CommentDraft{
		InReplyTo: inReplyTo,
		RootPost:  rootPost,
		Content:   markdown,
		Agent:     env.Marker.Agent,
		Grant:     env.Marker.Grant,
	})
	if err != nil {
		if errors.Is(err, ErrCommentInvalid) {
			return nil, fmt.Errorf("invalid request: %s", strings.TrimPrefix(err.Error(), ErrCommentInvalid.Error()+": "))
		}
		return nil, err
	}
	// ⚠️ Signed but not sent: the comment exists here and can be sent again by
	// its comment_id. Said in the answer rather than failing as if nothing
	// happened.
	return &ActionResult{Status: "success", Data: CommentSentFields(sent, map[string]any{"comment": signed.Meta})}, nil
}

// commentSiteOf is the site a handler writes a comment as.
func commentSiteOf(env HandlerEnv) CommentSite {
	return CommentSite{Site: env.Resolver.Site(), PrivateKey: env.PrivateKey, BaseURL: env.BaseURL, DiscoveryURL: env.DiscoveryURL, Marker: env.Marker}
}

// beseechComment sends a comment to the discovery service for blessing.
func (h *BuiltinCoreHandler) beseechComment(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	commentID, _ := req.Payload["comment_id"].(string)
	if commentID == "" {
		return nil, fmt.Errorf("comment_id is required")
	}
	sent := SendComment(commentSiteOf(env), commentID)
	if sent.Err != nil {
		return nil, fmt.Errorf("beseech: %w", sent.Err)
	}
	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"success":      true,
			"status":       sent.Status,
			"message":      sent.Message,
			"auto_blessed": sent.AutoBlessed,
		},
	}, nil
}

// ── Follow operations ───────────────────────────────────────────────

func (h *BuiltinCoreHandler) handleFollow(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	case "list":
		return h.listFollowing(env)
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.follow", req.Action)
	}
}

// listFollowing reads the following.json file and returns the list.
func (h *BuiltinCoreHandler) listFollowing(env HandlerEnv) (*ActionResult, error) {
	site := env.Resolver.Site()

	f, err := following.LoadSite(site)
	if err != nil {
		return nil, fmt.Errorf("load following: %w", err)
	}

	// Convert entries to map[string]any for consistent JSON
	entries := f.All()
	entryMaps := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		m := map[string]any{
			"url":      e.URL,
			"added_at": e.AddedAt,
		}
		if e.SiteTitle != "" {
			m["site_title"] = e.SiteTitle
		}
		if e.AuthorName != "" {
			m["author_name"] = e.AuthorName
		}
		entryMaps = append(entryMaps, m)
	}

	// SIGNET epic 02 — report the roster's signature state alongside the roster.
	// A caller asking "who do I follow" is exactly who should be told whether
	// that answer is signed, and this is one of the few follow-file reads whose
	// result is actually SURFACED to someone. Verification costs one small file
	// read plus one ed25519 check.
	//
	// It is a FACT, never a gate: the list is returned in full whatever the
	// status says. Whether "unsigned" or "invalid" is acceptable belongs to
	// whoever is asking (Law 2), and an unsigned follow file is the normal
	// state until the epic-11 backfill.
	sigStatus, sigErr := following.VerifySite(site)
	signature := map[string]any{"status": string(sigStatus)}
	if sigErr != nil {
		signature["message"] = sigErr.Error()
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"following": entryMaps,
			"count":     f.Count(),
			"signature": signature,
		},
	}, nil
}

// ── DM operations ───────────────────────────────────────────────────

func (h *BuiltinCoreHandler) handleDM(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	case "list":
		return h.listDMConversations(env)
	case "get":
		return h.getDMConversation(req, env)
	case "send":
		return h.sendDM(req, env)
	case "deliver":
		return h.deliverDM(req, env)
	case "protection_status":
		return h.protectionStatusDM(req, env)
	case "mark_read":
		return h.markDMRead(req, env)
	case "delete":
		return h.deleteDMConversation(req, env)
	case "retry":
		return h.retryDM(req, env)
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.dm", req.Action)
	}
}

// dmMailbox returns the tenant mailbox, the site's own domain (for convID mapping), and
// the epoch DEKs the server can open without a password (bootstrap server_dek; password
// epochs stay locked until the SPA unlock).
func (h *BuiltinCoreHandler) dmMailbox(env HandlerEnv) (*dm.Mailbox, string, map[int][32]byte, error) {
	site := env.Resolver.Site()
	deks, err := dm.LoadAvailableDEKs(site)
	if err != nil {
		return nil, "", nil, err
	}
	return dm.NewMailbox(site), dm.ExtractDomainFromURL(env.BaseURL), deks, nil
}

func (h *BuiltinCoreHandler) listDMConversations(env HandlerEnv) (*ActionResult, error) {
	mb, selfDomain, _, err := h.dmMailbox(env)
	if err != nil {
		return nil, err
	}
	entries, err := mb.RebuildInbox()
	if err != nil {
		return nil, fmt.Errorf("load conversations: %w", err)
	}

	convMaps := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		convMaps = append(convMaps, map[string]any{
			"id":              dm.ComputeConversationID(selfDomain, e.Peer),
			"peer_domain":     e.Peer,
			"peer_url":        "https://" + e.Peer,
			"last_message_at": e.LastMessageAt,
			"unread_count":    e.Unread,
			"last_preview":    "", // no server-readable preview under end-to-end encryption
		})
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"conversations": convMaps,
			"count":         len(convMaps),
		},
	}, nil
}

func (h *BuiltinCoreHandler) getDMConversation(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	convID, _ := req.Payload["id"].(string)
	if convID == "" {
		return nil, fmt.Errorf("conversation id required")
	}

	mb, selfDomain, deks, err := h.dmMailbox(env)
	if err != nil {
		return nil, err
	}
	peer, ok, err := mb.PeerForConversationID(selfDomain, convID)
	if err != nil {
		return nil, fmt.Errorf("resolve conversation: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("conversation not found")
	}

	msgs, err := mb.ReadConversation(peer, deks)
	if err != nil {
		return nil, fmt.Errorf("read conversation: %w", err)
	}

	// content is empty for messages whose epoch DEK is locked (password epoch not
	// unlocked); the "locked" flag + key_epoch tell the UI to prompt for unlock.
	msgMaps := make([]map[string]any, 0, len(msgs))
	for _, msg := range msgs {
		m := map[string]any{
			"id":        msg.ID,
			"from":      msg.From,
			"to":        msg.To,
			"content":   msg.Plaintext,
			"timestamp": msg.At,
			"status":    msg.Status,
			"key_epoch": msg.KeyEpoch,
			"locked":    msg.Locked,
		}
		if msg.ReplyTo != "" {
			m["reply_to_id"] = msg.ReplyTo
		}
		// ⭐ A message the instance cannot open (its epoch is under the
		// owner's password) carries its box, so the owner's browser, which
		// holds the key once unlocked, can open it (v5 spec 2.3, gap M1). The
		// box is ciphertext: no key travels, and dm.get is owner-only.
		if msg.Locked {
			m["ciphertext"] = msg.Ciphertext
			m["nonce"] = msg.Nonce
			m["box_pub"] = msg.BoxPub
		}
		msgMaps = append(msgMaps, m)
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"conversation_id": convID,
			"peer_domain":     peer,
			"peer_url":        "https://" + peer,
			"messages":        msgMaps,
			"count":           len(msgMaps),
		},
	}, nil
}

func (h *BuiltinCoreHandler) sendDM(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	recipientURL, _ := req.Payload["recipient_url"].(string)
	content, _ := req.Payload["content"].(string)
	replyToID, _ := req.Payload["reply_to_id"].(string)
	// ⭐ The one implementation of sending a server-sealed message (dm_send.go).
	sent, err := SendDM(DMSender{
		Site: env.Resolver.Site(), PrivateKey: env.PrivateKey, PublicKey: env.PublicKey, BaseURL: env.BaseURL,
		HTTPClient: env.DMHTTPClient, AllowPrivateRecipients: env.AllowPrivateDMRecipients, Emit: env.EmitEvent,
	}, recipientURL, content, replyToID)
	if err != nil {
		return nil, err
	}
	res := &ActionResult{Status: "success", Data: sent.Fields()}
	if !sent.Delivered {
		res.Code = http.StatusAccepted // saved as unsent, as the sealed send answers
	}
	return res, nil
}

func (h *BuiltinCoreHandler) deliverDM(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	// The sender domain should be set in the payload by the API router
	// after verifying the signed request headers
	senderDomain, _ := req.Payload["sender_domain"].(string)
	envelopeJSON, _ := req.Payload["envelope"].(string)

	if senderDomain == "" {
		return nil, fmt.Errorf("sender_domain required (set by signed-request auth)")
	}
	if envelopeJSON == "" {
		return nil, fmt.Errorf("envelope required")
	}

	// Load following domains for policy check
	site := env.Resolver.Site()
	followingDomains := loadFollowingDomains(site)

	// One limiter per recipient site, for the life of the process — see
	// dm_ratelimit.go. Built per call, it limited nothing.
	receiver := dm.NewReceiver(env.PrivateKey, env.PublicKey, env.BaseURL, site, dmRateLimiterFor(site))
	if maxSize := envInt("POLIS_DM_MAX_SIZE", 0); maxSize > 0 {
		receiver.MaxMessageSize = maxSize
	}
	receiver.Domain = dm.ExtractDomainFromURL(env.BaseURL)
	receiver.Logger = dm.EventFunc(env.EmitEvent)

	msg, err := receiver.ReceiveMessage(senderDomain, []byte(envelopeJSON), followingDomains)
	if err != nil {
		return nil, fmt.Errorf("deliver: %w", err)
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"message_id":      msg.ID,
			"conversation_id": dm.ComputeConversationID(receiver.Domain, senderDomain),
			"status":          "received",
		},
	}, nil
}

// loadFollowingDomains returns the set of domains this site follows, keyed lowercase.
// Shared by the deliver policy gate and the protection_status follow-file front-check.
func loadFollowingDomains(site storage.Site) map[string]bool {
	followingDomains := make(map[string]bool)
	if f, err := following.LoadSite(site); err == nil {
		for _, e := range f.All() {
			if domain := dm.ExtractDomainFromURL(e.URL); domain != "" {
				followingDomains[strings.ToLower(domain)] = true
			}
		}
	}
	return followingDomains
}

// protectionStatusDM answers a signed protection_status query (gate #3): is this site's
// messages secured? It returns a signed {protected} bit only to a caller in this site's
// follow relationship; non-followers get a forbidden error (mapped to 403). The verified
// caller domain is injected by the API router after checking the signed-request headers.
func (h *BuiltinCoreHandler) protectionStatusDM(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	callerDomain, _ := req.Payload["sender_domain"].(string)
	if callerDomain == "" {
		return nil, fmt.Errorf("sender_domain required (set by signed-request auth)")
	}

	site := env.Resolver.Site()
	responderDomain := dm.ExtractDomainFromURL(env.BaseURL)
	followingDomains := loadFollowingDomains(site)

	ps, err := dm.BuildProtectionStatus(site, responderDomain, env.PrivateKey, callerDomain, followingDomains)
	if err != nil {
		if errors.Is(err, dm.ErrProtectionStatusForbidden) {
			return nil, fmt.Errorf("forbidden: caller not in follow relationship")
		}
		return nil, fmt.Errorf("protection_status: %w", err)
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"domain":    ps.Domain,
			"protected": ps.Protected,
			"at":        ps.At,
			"sig":       ps.Sig,
		},
	}, nil
}

func (h *BuiltinCoreHandler) markDMRead(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	convID, _ := req.Payload["conversation_id"].(string)
	if convID == "" {
		return nil, fmt.Errorf("conversation_id required")
	}

	mb, selfDomain, _, err := h.dmMailbox(env)
	if err != nil {
		return nil, err
	}
	peer, ok, err := mb.PeerForConversationID(selfDomain, convID)
	if err != nil {
		return nil, fmt.Errorf("resolve conversation: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("conversation not found")
	}
	if err := mb.MarkRead(peer); err != nil {
		return nil, fmt.Errorf("mark read: %w", err)
	}

	return &ActionResult{
		Status: "success",
		Data:   map[string]any{"conversation_id": convID},
	}, nil
}

func (h *BuiltinCoreHandler) deleteDMConversation(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	convID, _ := req.Payload["id"].(string)
	if convID == "" {
		return nil, fmt.Errorf("conversation id required")
	}

	mb, selfDomain, _, err := h.dmMailbox(env)
	if err != nil {
		return nil, err
	}
	peer, ok, err := mb.PeerForConversationID(selfDomain, convID)
	if err != nil {
		return nil, fmt.Errorf("resolve conversation: %w", err)
	}
	if ok {
		if err := mb.DeleteConversation(peer); err != nil {
			return nil, fmt.Errorf("delete conversation: %w", err)
		}
	}

	return &ActionResult{
		Status: "success",
		Data:   map[string]any{"conversation_id": convID, "deleted": true},
	}, nil
}

func (h *BuiltinCoreHandler) retryDM(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	mb, selfDomain, _, err := h.dmMailbox(env)
	if err != nil {
		return nil, err
	}

	unsent, err := mb.UnsentMessages()
	if err != nil {
		return nil, fmt.Errorf("get unsent: %w", err)
	}

	if len(unsent) == 0 {
		return &ActionResult{
			Status: "success",
			Data:   map[string]any{"retried": 0, "message": "no unsent messages"},
		}, nil
	}

	// Report unsent messages — actual re-delivery (re-fetch key, re-box, re-POST) is a
	// follow-up; surfacing them lets the UI show "failed to send".
	unsentMaps := make([]map[string]any, 0, len(unsent))
	for _, u := range unsent {
		unsentMaps = append(unsentMaps, map[string]any{
			"conversation_id": dm.ComputeConversationID(selfDomain, u.Peer),
			"message_id":      u.Message.ID,
			"to":              u.Message.To,
			"timestamp":       u.Message.At,
		})
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"unsent_count": len(unsent),
			"unsent":       unsentMaps,
		},
	}, nil
}

// ── Tag operations ──────────────────────────────────────────────────

func (h *BuiltinCoreHandler) handleTag(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	case "list":
		return h.listTags(req, env)
	case "apply":
		return h.applyTag(req, env)
	case "remove":
		return h.removeTag(req, env)
	case "delete":
		return h.deleteTag(req, env)
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.tag", req.Action)
	}
}

func (h *BuiltinCoreHandler) listTags(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	site := env.Resolver.Site()

	// If a specific tag name is requested, load just that one
	if tagName, ok := req.Payload["tag"].(string); ok && tagName != "" {
		// ⭐ Normalised as the webapp does, so "Go" finds the go tag; a name
		// that does not normalise ("../x") is refused here, and LoadTag refuses
		// anything unnormalised on its own as well.
		tagName, err := tag.NormalizeTagName(tagName)
		if err != nil {
			return nil, fmt.Errorf("invalid tag name: %w", err)
		}
		tf, err := tag.LoadTag(site, tagName)
		if err != nil {
			return nil, fmt.Errorf("load tag: %w", err)
		}
		targets := make([]map[string]any, 0, len(tf.Targets))
		for _, t := range tf.Targets {
			targets = append(targets, map[string]any{
				"uri":   t.URI,
				"added": t.Added,
			})
		}
		return &ActionResult{
			Status: "success",
			Data: map[string]any{
				"tag":     tf.Tag,
				"targets": targets,
				"count":   len(targets),
			},
		}, nil
	}

	tags, err := tag.ListTags(site)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}

	tagMaps := make([]map[string]any, 0, len(tags))
	for _, tf := range tags {
		tagMaps = append(tagMaps, map[string]any{
			"tag":     tf.Tag,
			"count":   len(tf.Targets),
			"updated": tf.Updated,
		})
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"tags":  tagMaps,
			"count": len(tagMaps),
		},
	}, nil
}

func (h *BuiltinCoreHandler) applyTag(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	tagName, _ := req.Payload["tag"].(string)
	targetURI, _ := req.Payload["target_uri"].(string)

	if tagName == "" {
		return nil, fmt.Errorf("tag name required")
	}
	if targetURI == "" {
		return nil, fmt.Errorf("target_uri required")
	}

	site := env.Resolver.Site()
	tf, err := tag.ApplyTag(site, tagName, targetURI, env.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("apply tag: %w", err)
	}

	// DS sync (non-fatal)
	var dsCfg *tag.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		// ⛔ THE SITE, EXPLICITLY. Without it the check falls back to the
		// process-wide tag.DefaultSite, which on hosted is whichever tenant
		// initialised last — so one tenant read another's registration marker.
		dsCfg = &tag.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			BaseURL:      env.BaseURL,
			Site:         site,
		}
	}
	if err := tag.SyncTag(site, tf, env.PrivateKey, dsCfg); err != nil {
		fmt.Printf("[!] Tag DS sync skipped: %v\n", err)
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"tag":        tf.Tag,
			"target_uri": targetURI,
			"count":      len(tf.Targets),
		},
	}, nil
}

func (h *BuiltinCoreHandler) removeTag(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	tagName, _ := req.Payload["tag"].(string)
	targetURI, _ := req.Payload["target_uri"].(string)

	if tagName == "" {
		return nil, fmt.Errorf("tag name required")
	}
	if targetURI == "" {
		return nil, fmt.Errorf("target_uri required")
	}

	site := env.Resolver.Site()
	tf, err := tag.RemoveTarget(site, tagName, targetURI, env.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("remove tag target: %w", err)
	}

	// DS unregister (non-fatal)
	var dsCfg *tag.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		// ⛔ THE SITE, EXPLICITLY. Without it the check falls back to the
		// process-wide tag.DefaultSite, which on hosted is whichever tenant
		// initialised last — so one tenant read another's registration marker.
		dsCfg = &tag.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			BaseURL:      env.BaseURL,
			Site:         site,
		}
	}
	if err := tag.UnregisterTarget(tagName, targetURI, env.PrivateKey, dsCfg); err != nil {
		fmt.Printf("[!] Tag DS unregister skipped: %v\n", err)
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"tag":        tf.Tag,
			"target_uri": targetURI,
			"count":      len(tf.Targets),
		},
	}, nil
}

func (h *BuiltinCoreHandler) deleteTag(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	// DELETE /v1/content/tag/{name} passes the path segment as "id", like
	// every other delete; "tag" is the actions route's body field.
	tagName, _ := req.Payload["tag"].(string)
	if tagName == "" {
		tagName, _ = req.Payload["id"].(string)
	}
	if tagName == "" {
		return nil, fmt.Errorf("tag name required")
	}
	tagName, err := tag.NormalizeTagName(tagName)
	if err != nil {
		return nil, fmt.Errorf("invalid tag name: %w", err)
	}

	site := env.Resolver.Site()

	// Load tag to get all targets for DS cleanup
	tf, err := tag.LoadTag(site, tagName)
	if err != nil {
		return nil, fmt.Errorf("load tag for deletion: %w", err)
	}

	// Delete local file
	if err := tag.DeleteTag(site, tagName); err != nil {
		return nil, fmt.Errorf("delete tag: %w", err)
	}

	// DS unregister all targets (non-fatal)
	var dsCfg *tag.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		// ⛔ THE SITE, EXPLICITLY. Without it the check falls back to the
		// process-wide tag.DefaultSite, which on hosted is whichever tenant
		// initialised last — so one tenant read another's registration marker.
		dsCfg = &tag.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			BaseURL:      env.BaseURL,
			Site:         site,
		}
	}
	for _, target := range tf.Targets {
		if err := tag.UnregisterTarget(tf.Tag, target.URI, env.PrivateKey, dsCfg); err != nil {
			fmt.Printf("[!] Tag DS unregister skipped for %s: %v\n", target.URI, err)
		}
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"tag":     tf.Tag,
			"deleted": true,
		},
	}, nil
}

// ── Theme operations ────────────────────────────────────────────────

func (h *BuiltinCoreHandler) handleTheme(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	case "list":
		return h.listThemes(env)
	case "get":
		return h.getTheme(req, env)
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.theme", req.Action)
	}
}

// listThemes returns theme declarations from the bundle manifest.
func (h *BuiltinCoreHandler) listThemes(env HandlerEnv) (*ActionResult, error) {
	if env.Bundle == nil {
		return &ActionResult{
			Status: "success",
			Data:   map[string]any{"themes": []any{}, "count": 0},
		}, nil
	}

	// Sort for stable output
	// ⛔ Only the themes an owner may choose (theme.Selectable): this list is
	// what an agent picks from before set_theme, which refuses the reserved
	// themes and has no _shared to switch to.
	names := make([]string, 0, len(env.Bundle.Themes))
	for name := range env.Bundle.Themes {
		if theme.Selectable(name) {
			names = append(names, name)
		}
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}

	themes := make([]map[string]any, 0, len(names))
	for _, name := range names {
		themes = append(themes, themeToMap(name, env.Bundle.Themes[name]))
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"themes": themes,
			"count":  len(themes),
		},
	}, nil
}

// getTheme returns a single theme's declared metadata.
func (h *BuiltinCoreHandler) getTheme(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	// The v1 route passes the path segment as "id", like every other get;
	// "name" is kept for direct dispatch callers.
	name, _ := req.Payload["id"].(string)
	if name == "" {
		name, _ = req.Payload["name"].(string)
	}
	if name == "" {
		return nil, fmt.Errorf("theme name required")
	}
	if env.Bundle == nil {
		return nil, fmt.Errorf("bundle not loaded")
	}
	th, err := env.Bundle.GetTheme(name)
	if err != nil {
		return nil, fmt.Errorf("theme %q: %w", name, ErrNotFound)
	}
	return &ActionResult{
		Status: "success",
		Data:   themeToMap(name, th),
	}, nil
}

// themeToMap converts a Theme declaration to a JSON-friendly map.
// Name comes from the caller so callers can use the map key (avoids reliance on
// the embedded Name field in case of skew).
func themeToMap(name string, th *bundle.Theme) map[string]any {
	m := map[string]any{
		"name":    name,
		"version": th.Version,
		"css":     th.CSS,
	}
	if len(th.CompatibleShapes) > 0 {
		m["compatible_shapes"] = th.CompatibleShapes
	}
	return m
}

// envInt reads an integer from an environment variable, returning defaultVal if unset or invalid.
func envInt(key string, defaultVal int) int {
	s := os.Getenv(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}
