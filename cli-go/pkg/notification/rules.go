package notification

import (
	"crypto/sha256"
	"fmt"
	"path"
	"strings"
)

// Rule defines how a stream event type maps to a notification.
type Rule struct {
	ID        string       `json:"id"`
	EventType string       `json:"event_type"`
	Enabled   bool         `json:"enabled"`
	Filter    RuleFilter   `json:"filter"`
	Template  RuleTemplate `json:"template"`
	Batch     bool         `json:"batch"`
	// BatchWindow is a duration string like "24h". Only used when Batch is true.
	BatchWindow string `json:"batch_window,omitempty"`
}

// RuleFilter specifies how to determine relevance of an event.
type RuleFilter struct {
	// Relevance is one of: "target_domain", "source_domain", "followed_author"
	Relevance string `json:"relevance"`
}

// RuleTemplate defines the display format for a notification.
type RuleTemplate struct {
	Icon    string `json:"icon"`
	Message string `json:"message"`
	// Link is a URL template for the notification's click target.
	// Supports {{var}} substitution (same vars as Message).
	// Examples: "/_/#blessings", "/_/#followers", "{{source_url}}"
	Link string `json:"link,omitempty"`
}

// DefaultRules returns the built-in rule set seeded on first sync.
func DefaultRules() []Rule {
	return []Rule{
		{
			ID:        "new-follower",
			EventType: "pub.polis.follow.announced",
			Enabled:   true,
			Filter:    RuleFilter{Relevance: "target_domain"},
			Template:  RuleTemplate{Icon: "\U0001F464", Message: "{{actor}} started following you", Link: "/_/social/followers"},
			Batch:     true,
			BatchWindow: "24h",
		},
		{
			ID:        "lost-follower",
			EventType: "pub.polis.follow.removed",
			Enabled:   true,
			Filter:    RuleFilter{Relevance: "target_domain"},
			Template:  RuleTemplate{Icon: "\U0001F464", Message: "{{actor}} unfollowed you", Link: "/_/social/followers"},
		},
		{
			ID:        "blessing-requested",
			EventType: "pub.polis.comment.blessing.requested",
			Enabled:   true,
			Filter:    RuleFilter{Relevance: "target_domain"},
			Template:  RuleTemplate{Icon: "\U0001F514", Message: "{{actor}} requested a blessing on {{post_name}}", Link: "/_/blessings"},
		},
		{
			ID:        "blessing-granted",
			EventType: "pub.polis.comment.blessing.granted",
			Enabled:   true,
			Filter:    RuleFilter{Relevance: "source_domain"},
			Template:  RuleTemplate{Icon: "\u2713", Message: "{{actor}} blessed your comment", Link: "/_/comments?filter=blessed"},
		},
		{
			ID:        "blessing-denied",
			EventType: "pub.polis.comment.blessing.denied",
			Enabled:   true,
			Filter:    RuleFilter{Relevance: "source_domain"},
			Template:  RuleTemplate{Icon: "\u2717", Message: "{{actor}} denied your comment", Link: "/_/comments?filter=denied"},
		},
		{
			ID:        "new-comment",
			EventType: "pub.polis.comment.published",
			Enabled:   true,
			Filter:    RuleFilter{Relevance: "target_domain"},
			Template:  RuleTemplate{Icon: "\U0001F4AC", Message: "{{actor}} commented on {{post_name}}", Link: "/_/blessings"},
		},
		{
			ID:        "updated-comment",
			EventType: "pub.polis.comment.republished",
			Enabled:   false,
			Filter:    RuleFilter{Relevance: "target_domain"},
			Template:  RuleTemplate{Icon: "\U0001F4AC", Message: "{{actor}} updated their comment on {{post_name}}", Link: "/_/blessings"},
		},
		{
			ID:        "new-post",
			EventType: "pub.polis.post.published",
			Enabled:   true,
			Filter:    RuleFilter{Relevance: "followed_author"},
			Template:  RuleTemplate{Icon: "\U0001F4DD", Message: "{{actor}} published a new post", Link: "{{url}}"},
		},
		{
			ID:        "updated-post",
			EventType: "pub.polis.post.republished",
			Enabled:   false,
			Filter:    RuleFilter{Relevance: "followed_author"},
			Template:  RuleTemplate{Icon: "\U0001F4DD", Message: "{{actor}} updated a post", Link: "{{url}}"},
		},
	}
}

// ResolveTemplate performs simple {{var}} substitution on a template string.
// Available variables: actor, timestamp, source_url, target_url, target_domain, source_domain, post_name.
func ResolveTemplate(tmpl string, vars map[string]string) string {
	result := tmpl
	for k, v := range vars {
		result = strings.ReplaceAll(result, "{{"+k+"}}", v)
	}
	return result
}

// TemplateVarsFromEvent builds template variables from a stream event's fields.
func TemplateVarsFromEvent(actor, timestamp string, payload map[string]interface{}) map[string]string {
	vars := map[string]string{
		"actor":     actor,
		"timestamp": timestamp,
	}

	for _, key := range []string{"url", "source_url", "target_url", "target_domain", "source_domain", "in_reply_to", "comment_url"} {
		if v, ok := payload[key].(string); ok {
			vars[key] = v
		}
	}

	// Derive post_name from target_url, url, in_reply_to, or comment_url
	// (blessing events from the DS use in_reply_to/comment_url instead of target_url)
	postURL := ""
	if targetURL, ok := payload["target_url"].(string); ok && targetURL != "" {
		postURL = targetURL
	} else if u, ok := payload["url"].(string); ok && u != "" {
		postURL = u
	} else if v, ok := payload["in_reply_to"].(string); ok && v != "" {
		postURL = v
	} else if v, ok := payload["comment_url"].(string); ok && v != "" {
		postURL = v
	}
	if postURL != "" {
		base := path.Base(postURL)
		if strings.HasSuffix(base, ".md") {
			base = strings.TrimSuffix(base, ".md")
		}
		vars["post_name"] = base
	}

	// Extract title from metadata if available (post events include it)
	if meta, ok := payload["metadata"].(map[string]interface{}); ok {
		if title, ok := meta["title"].(string); ok && title != "" {
			vars["title"] = title
		}
	}

	return vars
}

// DedupeKey computes a deterministic dedupe key for a notification from a rule and event.
// Format: "<rule_id>:<content_identifier>"
func DedupeKey(ruleID string, payload map[string]interface{}) string {
	// Use source_url as the primary content identifier (unique per comment/action)
	if sourceURL, ok := payload["source_url"].(string); ok && sourceURL != "" {
		return ruleID + ":" + sourceURL
	}
	// Post events use "url" instead of "source_url"
	if postURL, ok := payload["url"].(string); ok && postURL != "" {
		return ruleID + ":" + postURL
	}
	// For follow events, use actor + target_domain
	if targetDomain, ok := payload["target_domain"].(string); ok && targetDomain != "" {
		return ruleID + ":" + targetDomain
	}
	// Fallback: hash the payload
	h := sha256.Sum256([]byte(fmt.Sprintf("%v", payload)))
	return ruleID + ":" + fmt.Sprintf("%x", h[:8])
}
