package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
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
	default:
		return nil, fmt.Errorf("unsupported content type: %s", req.ContentType)
	}
}

func (h *BuiltinCoreHandler) Actions(contentType string) []string {
	switch contentType {
	case "pub.polis.post":
		return []string{"list", "get", "create", "update", "delete", "render",
			"draft.list", "draft.get", "draft.save", "draft.delete"}
	case "pub.polis.comment":
		return []string{"list", "get", "create", "update", "bless", "deny", "revoke", "sync"}
	case "pub.polis.follow":
		return []string{"list", "create", "delete"}
	case "pub.polis.dm":
		return []string{"list", "get", "send", "deliver", "protection_status", "mark_read", "delete", "retry"}
	case "pub.polis.tag":
		return []string{"list", "apply", "remove", "delete"}
	case "pub.polis.theme":
		return []string{"list", "get"}
	default:
		return nil
	}
}

// ── Post operations ─────────────────────────────────────────────────

func (h *BuiltinCoreHandler) handlePost(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	case "list":
		return h.listPosts(env)
	case "create":
		return h.publishPost(req, env)
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.post", req.Action)
	}
}

// listPosts reads from content/pub.polis.core/index.jsonl and returns post entries.
func (h *BuiltinCoreHandler) listPosts(env HandlerEnv) (*ActionResult, error) {
	siteDir := env.Resolver.SiteDir()
	indexPath := filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl")

	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
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
		// Filter out comments — only include posts
		if path, ok := entry["path"].(string); ok {
			if strings.HasPrefix(path, "comments/") {
				continue
			}
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

	// Strip existing frontmatter if present
	if publish.HasFrontmatter(markdown) {
		markdown = publish.StripFrontmatter(markdown)
	}

	// Build discovery config
	var dsCfg *publish.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		dsCfg = &publish.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			DiscoveryKey: env.DiscoveryKey,
			BaseURL:      env.BaseURL,
		}
	}

	siteDir := env.Resolver.SiteDir()
	result, err := publish.PublishPost(siteDir, markdown, filename, env.PrivateKey, dsCfg)
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

// ── Comment operations ──────────────────────────────────────────────

func (h *BuiltinCoreHandler) handleComment(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	case "create":
		return h.beseechComment(req, env)
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
	if strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("markdown content required")
	}
	markdown = comment.StripFrontmatter(markdown)

	var dsCfg *comment.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		dsCfg = &comment.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			DiscoveryKey: env.DiscoveryKey,
			BaseURL:      env.BaseURL,
		}
	}

	siteDir := env.Resolver.SiteDir()
	result, err := comment.RepublishComment(siteDir, path, markdown, env.PrivateKey, dsCfg)
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

// beseechComment sends a comment to the discovery service for blessing.
func (h *BuiltinCoreHandler) beseechComment(req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	commentID, _ := req.Payload["comment_id"].(string)
	if commentID == "" {
		return nil, fmt.Errorf("comment_id is required")
	}

	var dsCfg *comment.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		dsCfg = &comment.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			DiscoveryKey: env.DiscoveryKey,
			BaseURL:      env.BaseURL,
		}
	}

	siteDir := env.Resolver.SiteDir()
	result, err := comment.BeseechComment(siteDir, commentID, env.PrivateKey, dsCfg)
	if err != nil {
		return nil, fmt.Errorf("beseech: %w", err)
	}

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"success":      result.Success,
			"status":       result.Status,
			"message":      result.Message,
			"auto_blessed": result.AutoBlessed,
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
	siteDir := env.Resolver.SiteDir()
	followingPath := following.DefaultPath(siteDir)

	f, err := following.Load(followingPath)
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

	return &ActionResult{
		Status: "success",
		Data: map[string]any{
			"following": entryMaps,
			"count":     f.Count(),
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
	siteDir := env.Resolver.SiteDir()
	deks, err := dm.LoadAvailableDEKs(siteDir)
	if err != nil {
		return nil, "", nil, err
	}
	return dm.NewMailbox(dm.DMDir(siteDir)), dm.ExtractDomainFromURL(env.BaseURL), deks, nil
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

	if recipientURL == "" {
		return nil, fmt.Errorf("recipient_url required")
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("content required")
	}
	// DM-4: outbound SSRF guard on the v1 API send path (https + safe host).
	if err := dm.ValidateRecipientURL(recipientURL); err != nil {
		return nil, fmt.Errorf("invalid recipient_url: %w", err)
	}

	sender := dm.NewSender(env.PrivateKey, env.PublicKey, env.BaseURL, env.Resolver.SiteDir())
	// Extract domain from BaseURL
	sender.Domain = dm.ExtractDomainFromURL(env.BaseURL)

	msg, err := sender.SendMessage(recipientURL, content, replyToID)
	if err != nil && msg == nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	result := map[string]any{
		"message_id":      msg.ID,
		"conversation_id": dm.ComputeConversationID(sender.Domain, dm.ExtractDomainFromURL(recipientURL)),
		"status":          msg.Status,
	}
	if err != nil {
		result["error"] = err.Error()
	}

	return &ActionResult{
		Status: "success",
		Data:   result,
	}, nil
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
	siteDir := env.Resolver.SiteDir()
	followingDomains := loadFollowingDomains(siteDir)

	rl := dm.NewRateLimiter(envInt("POLIS_DM_RATE_PER_SENDER", 0), envInt("POLIS_DM_RATE_GLOBAL", 0))
	receiver := dm.NewReceiver(env.PrivateKey, env.PublicKey, env.BaseURL, siteDir, rl)
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
func loadFollowingDomains(siteDir string) map[string]bool {
	followingDomains := make(map[string]bool)
	if f, err := following.Load(following.DefaultPath(siteDir)); err == nil {
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

	siteDir := env.Resolver.SiteDir()
	responderDomain := dm.ExtractDomainFromURL(env.BaseURL)
	followingDomains := loadFollowingDomains(siteDir)

	ps, err := dm.BuildProtectionStatus(siteDir, responderDomain, env.PrivateKey, callerDomain, followingDomains)
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
	siteDir := env.Resolver.SiteDir()

	// If a specific tag name is requested, load just that one
	if tagName, ok := req.Payload["tag"].(string); ok && tagName != "" {
		path := tag.TagPath(siteDir, tagName)
		tf, err := tag.Load(path)
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

	tags, err := tag.ListTags(siteDir)
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

	siteDir := env.Resolver.SiteDir()
	tf, err := tag.ApplyTag(siteDir, tagName, targetURI, env.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("apply tag: %w", err)
	}

	// DS sync (non-fatal)
	var dsCfg *tag.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		dsCfg = &tag.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			DiscoveryKey: env.DiscoveryKey,
			BaseURL:      env.BaseURL,
		}
	}
	if err := tag.SyncTag(siteDir, tf, env.PrivateKey, dsCfg); err != nil {
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

	siteDir := env.Resolver.SiteDir()
	tf, err := tag.RemoveTarget(siteDir, tagName, targetURI, env.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("remove tag target: %w", err)
	}

	// DS unregister (non-fatal)
	var dsCfg *tag.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		dsCfg = &tag.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			DiscoveryKey: env.DiscoveryKey,
			BaseURL:      env.BaseURL,
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
	tagName, _ := req.Payload["tag"].(string)
	if tagName == "" {
		return nil, fmt.Errorf("tag name required")
	}

	siteDir := env.Resolver.SiteDir()

	// Load tag to get all targets for DS cleanup
	path := tag.TagPath(siteDir, tagName)
	tf, err := tag.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load tag for deletion: %w", err)
	}

	// Delete local file
	if err := tag.DeleteTag(siteDir, tagName); err != nil {
		return nil, fmt.Errorf("delete tag: %w", err)
	}

	// DS unregister all targets (non-fatal)
	var dsCfg *tag.DiscoveryConfig
	if env.DiscoveryURL != "" && env.BaseURL != "" {
		dsCfg = &tag.DiscoveryConfig{
			DiscoveryURL: env.DiscoveryURL,
			DiscoveryKey: env.DiscoveryKey,
			BaseURL:      env.BaseURL,
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
	names := make([]string, 0, len(env.Bundle.Themes))
	for name := range env.Bundle.Themes {
		names = append(names, name)
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
	name, _ := req.Payload["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("theme name required")
	}
	if env.Bundle == nil {
		return nil, fmt.Errorf("bundle not loaded")
	}
	th, err := env.Bundle.GetTheme(name)
	if err != nil {
		return nil, err
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
