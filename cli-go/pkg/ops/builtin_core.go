package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
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
	case "pub.polis.feed":
		return h.handleFeed(ctx, req, env)
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
		return []string{"list", "get", "create", "bless", "deny", "revoke", "sync"}
	case "pub.polis.follow":
		return []string{"list", "create", "delete"}
	case "pub.polis.feed":
		return []string{"list", "refresh"}
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
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.comment", req.Action)
	}
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

// ── Feed operations ─────────────────────────────────────────────────

func (h *BuiltinCoreHandler) handleFeed(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	switch req.Action {
	default:
		return nil, fmt.Errorf("unsupported action %q for pub.polis.feed", req.Action)
	}
}
