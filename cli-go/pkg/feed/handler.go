package feed

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
)

// FeedHandler transforms discovery stream events into FeedItems.
// It uses policy evaluation to filter events (e.g., suppressing self-authored
// content via "omit pub.polis.feed from self") and maps event payloads
// to the common FeedItem structure.
type FeedHandler struct {
	// MyDomain is the local site's domain.
	MyDomain string
	// FollowedDomains is the set of domains we follow (for validation).
	FollowedDomains map[string]bool
	// Policies is the active policy set loaded from rules.jsonl files.
	// When non-empty, events are evaluated against policies — deny/omit
	// decisions cause the event to be skipped.
	Policies []policy.Policy
	// FollowerDomains is a set of domains that follow us. Used for policy
	// evaluation when rules reference the "followers" source.
	FollowerDomains map[string]bool
}

// Process converts stream events into FeedItems.
// It evaluates events against policies (skipping on deny/omit) and filters
// unknown event types.
func (h *FeedHandler) Process(events []discovery.StreamEvent) []FeedItem {
	var items []FeedItem

	for _, evt := range events {
		// Policy check: deny/omit events that match a deny or omit policy.
		// The default private policy "omit pub.polis.feed from self" replaces
		// the former hardcoded self-event skip.
		//
		// The event type is prefixed with "pub.polis.feed." so that
		// feed-specific policies match via dot-boundary prefix matching
		// without interfering with policies targeting the raw event types
		// in other contexts (mirrors NotificationHandler pattern).
		if len(h.Policies) > 0 {
			pEvt := policy.Event{
				Type:        "pub.polis.feed." + evt.Type,
				ActorDomain: evt.Actor,
			}
			if td, ok := evt.Payload["target_domain"].(string); ok {
				pEvt.TargetDomain = td
			}
			ctx := policy.EvalContext{
				MyDomain:         h.MyDomain,
				FollowingDomains: h.FollowedDomains,
				FollowerDomains:  h.FollowerDomains,
			}
			result := policy.EvaluateWithLog(h.Policies, pEvt, ctx)
			if result.Decision == policy.Deny || result.Decision == policy.Omit {
				continue
			}
		} else {
			// Fallback: no policies loaded, use legacy hardcoded self-skip
			if evt.Actor == h.MyDomain {
				continue
			}
		}

		item, ok := h.eventToItem(evt)
		if !ok {
			continue
		}
		items = append(items, item)
	}

	return items
}

// eventToItem maps a single stream event to a FeedItem.
// Returns false if the event type is not a feed-relevant type.
func (h *FeedHandler) eventToItem(evt discovery.StreamEvent) (FeedItem, bool) {
	switch evt.Type {
	case "pub.polis.post.published", "pub.polis.post.republished":
		return h.postEventToItem(evt), true
	case "pub.polis.comment.published", "pub.polis.comment.republished":
		return h.commentEventToItem(evt), true
	case "pub.polis.comment.blessing.granted":
		return h.blessingGrantedToItem(evt), true
	default:
		return FeedItem{}, false
	}
}

// postEventToItem extracts FeedItem fields from a post event.
func (h *FeedHandler) postEventToItem(evt discovery.StreamEvent) FeedItem {
	url, _ := evt.Payload["url"].(string)
	version, _ := evt.Payload["version"].(string)

	// Title may be top-level (DS emits flat) or nested under metadata (legacy)
	title, _ := evt.Payload["title"].(string)
	published, _ := evt.Payload["published_at"].(string)
	if title == "" || published == "" {
		if md, ok := evt.Payload["metadata"].(map[string]interface{}); ok {
			if title == "" {
				title, _ = md["title"].(string)
			}
			if published == "" {
				published, _ = md["published_at"].(string)
			}
		}
	}

	if published == "" {
		published = evt.Timestamp
	}

	return FeedItem{
		Type:         "post",
		Title:        title,
		URL:          url,
		Published:    published,
		Hash:         version,
		AuthorURL:    "https://" + evt.Actor,
		AuthorDomain: evt.Actor,
	}
}

// commentEventToItem extracts FeedItem fields from a comment event.
func (h *FeedHandler) commentEventToItem(evt discovery.StreamEvent) FeedItem {
	// Comment URL may be "url" (DS emits flat) or "comment_url" (legacy)
	commentURL, _ := evt.Payload["url"].(string)
	if commentURL == "" {
		commentURL, _ = evt.Payload["comment_url"].(string)
	}
	version, _ := evt.Payload["version"].(string)

	// Title may be top-level (DS emits flat) or nested under metadata (legacy)
	title, _ := evt.Payload["title"].(string)
	published, _ := evt.Payload["published_at"].(string)
	if title == "" || published == "" {
		if md, ok := evt.Payload["metadata"].(map[string]interface{}); ok {
			if title == "" {
				title, _ = md["title"].(string)
			}
			if published == "" {
				published, _ = md["published_at"].(string)
			}
		}
	}

	if published == "" {
		published = evt.Timestamp
	}

	// Extract target post URL (what this comment is replying to)
	targetURL, _ := evt.Payload["in_reply_to"].(string)
	if targetURL == "" {
		targetURL, _ = evt.Payload["root_post"].(string)
	}
	targetDomain, _ := evt.Payload["target_domain"].(string)

	return FeedItem{
		Type:         "comment",
		Title:        title,
		URL:          commentURL,
		Published:    published,
		Hash:         version,
		AuthorURL:    "https://" + evt.Actor,
		AuthorDomain: evt.Actor,
		TargetURL:    targetURL,
		TargetDomain: targetDomain,
	}
}

// blessingGrantedToItem extracts a FeedItem from a blessing.granted event.
// The actor on this event is the post author (who blessed), not the comment author.
// The comment author is in source_domain.
func (h *FeedHandler) blessingGrantedToItem(evt discovery.StreamEvent) FeedItem {
	// Comment URL: "comment_url" (auto-blessed via content register) or "source_url" (manual grant)
	commentURL, _ := evt.Payload["comment_url"].(string)
	if commentURL == "" {
		commentURL, _ = evt.Payload["source_url"].(string)
	}

	// Target post URL
	targetURL, _ := evt.Payload["in_reply_to"].(string)
	if targetURL == "" {
		targetURL, _ = evt.Payload["root_post"].(string)
	}
	if targetURL == "" {
		targetURL, _ = evt.Payload["target_url"].(string)
	}

	targetDomain, _ := evt.Payload["target_domain"].(string)
	sourceDomain, _ := evt.Payload["source_domain"].(string)

	title, _ := evt.Payload["title"].(string)

	return FeedItem{
		Type:         "comment",
		URL:          commentURL,
		Title:        title,
		Published:    evt.Timestamp,
		AuthorURL:    "https://" + sourceDomain,
		AuthorDomain: sourceDomain,
		TargetURL:    targetURL,
		TargetDomain: targetDomain,
	}
}
