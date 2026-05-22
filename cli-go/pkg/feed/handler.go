// =============================================================================
// HANDBOOK TRAIL MARKER — DS-to-stream thread (event-to-FeedItem transformer)
// =============================================================================
// FeedHandler is the transformer in the middle of the continuous sync path —
// it consumes typed events the DS emitted, filters self-authored ones (unless
// IncludeSelf), and produces FeedItems that get persisted to the local cache
// (pub.polis.feed.jsonl) by stream.Store.
//
// Trail across files (DS-to-stream, continuous sync):
//   producer       — webapp/internal/server/sync.go: runUnifiedSync()
//   DS endpoint    — discovery-service/core/handlers/stream.ts
//   transformer    — this file (FeedHandler.Process)
//   cursor + cache — cli-go/pkg/stream/store.go (Store)
//
// Pull the thread:
//   github.com/vdibart/polis-cli/blob/main/docs/handbook/ds-to-stream.md  (tour)
//   github.com/vdibart/polis-cli/blob/main/docs/general/content-types.md
//   github.com/vdibart/polis-cli/blob/main/AGENTS.md                      (map)
// =============================================================================

package feed

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// FeedHandler transforms discovery stream events into FeedItems.
// It filters self-authored events (unless IncludeSelf is set) and maps
// event payloads to the common FeedItem structure.
//
// Policy evaluation is NOT done here — policies govern network engagement
// (DM acceptance, blessing auto-decision), while feed display is a separate
// concern handled by this handler's own filtering logic.
type FeedHandler struct {
	// MyDomain is the local site's domain.
	MyDomain string
	// FollowedDomains is the set of domains we follow (for validation).
	FollowedDomains map[string]bool
	// IncludeSelf disables the self-event filter. When true, events where
	// Actor == MyDomain are processed instead of skipped. Used for "me" scope.
	IncludeSelf bool
}

// Process converts stream events into FeedItems.
// It skips self-authored events (unless IncludeSelf) and filters
// unknown event types.
func (h *FeedHandler) Process(events []discovery.StreamEvent) []FeedItem {
	var items []FeedItem

	for _, evt := range events {
		// IncludeSelf: bypass all filtering for self-events in "me" mode
		if evt.Actor == h.MyDomain && h.IncludeSelf {
			results := h.eventToItems(evt)
			items = append(items, results...)
			continue
		}

		// Skip self-authored events — your own activity is noise in the feed.
		if evt.Actor == h.MyDomain {
			continue
		}

		results := h.eventToItems(evt)
		items = append(items, results...)
	}

	return items
}

// eventToItems maps a single stream event to one or more FeedItems.
// Returns an empty slice if the event type is not feed-relevant.
func (h *FeedHandler) eventToItems(evt discovery.StreamEvent) []FeedItem {
	switch evt.Type {
	case "pub.polis.post.published", "pub.polis.post.republished":
		return []FeedItem{h.postEventToItem(evt)}
	case "pub.polis.comment.published", "pub.polis.comment.republished":
		return []FeedItem{h.commentEventToItem(evt)}
	case "pub.polis.comment.blessing.granted":
		return h.blessingGrantedToItems(evt)
	case "pub.polis.comment.blessing.requested":
		return []FeedItem{h.blessingRequestedToItem(evt)}
	case "pub.polis.follow.announced":
		return []FeedItem{h.followEventToItem(evt)}
	case "pub.polis.follow.removed":
		return []FeedItem{h.followRemoveEventToItem(evt)}
	case "pub.polis.site.registered":
		return []FeedItem{h.siteRegisteredToItem(evt)}
	default:
		return nil
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
		EventType:    evt.Type,
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
		EventType:    evt.Type,
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

// blessingGrantedToItems extracts FeedItems from a blessing.granted event.
// Returns both an announcement (for the activity feed) and a comment item
// (so the comment appears grouped under the post it replies to).
//
// The actor on this event is the post author (who blessed), not the comment author.
// The comment author is in source_domain.
func (h *FeedHandler) blessingGrantedToItems(evt discovery.StreamEvent) []FeedItem {
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

	// Announcement: the post author (target_domain) granted the blessing
	// to the comment author (source_domain). AuthorDomain = who acted (granter).
	announcement := FeedItem{
		Type:         "announcement",
		EventType:    evt.Type,
		URL:          commentURL,
		Title:        title,
		Published:    evt.Timestamp,
		AuthorURL:    "https://" + targetDomain,
		AuthorDomain: targetDomain,
		TargetURL:    targetURL,
		TargetDomain: sourceDomain,
	}

	// Comment: so the blessed comment appears grouped under the post in the feed.
	// The comment author is source_domain.
	comment := FeedItem{
		Type:         "comment",
		EventType:    "pub.polis.comment.blessed",
		URL:          commentURL,
		Title:        title,
		Published:    evt.Timestamp,
		AuthorURL:    "https://" + sourceDomain,
		AuthorDomain: sourceDomain,
		TargetURL:    targetURL,
		TargetDomain: targetDomain,
	}

	return []FeedItem{announcement, comment}
}

// blessingRequestedToItem extracts a FeedItem from a blessing.requested event.
func (h *FeedHandler) blessingRequestedToItem(evt discovery.StreamEvent) FeedItem {
	sourceDomain, _ := evt.Payload["source_domain"].(string)
	targetDomain, _ := evt.Payload["target_domain"].(string)
	targetURL, _ := evt.Payload["target_url"].(string)

	return FeedItem{
		Type:         "announcement",
		EventType:    evt.Type,
		URL:          "blessing-request:" + evt.Actor + ":" + sourceDomain,
		Published:    evt.Timestamp,
		AuthorURL:    "https://" + sourceDomain,
		AuthorDomain: sourceDomain,
		TargetURL:    targetURL,
		TargetDomain: targetDomain,
	}
}

// followEventToItem extracts a FeedItem from a follow.announced event.
func (h *FeedHandler) followEventToItem(evt discovery.StreamEvent) FeedItem {
	targetDomain, _ := evt.Payload["target_domain"].(string)

	return FeedItem{
		Type:         "announcement",
		EventType:    evt.Type,
		URL:          "follow:" + evt.Actor + ":" + targetDomain,
		Published:    evt.Timestamp,
		AuthorURL:    "https://" + evt.Actor,
		AuthorDomain: evt.Actor,
		TargetURL:    "https://" + targetDomain,
		TargetDomain: targetDomain,
	}
}

// followRemoveEventToItem extracts a FeedItem from a follow.removed event.
// Mirrors followEventToItem but uses an "unfollow:" URL prefix so dedup
// keys can distinguish announce-then-remove for the same (actor, target)
// pair. The activity-stream renderer (owner-extras.js appendFollowSignal)
// reads EventType to choose between "followed" and "unfollowed" verbs.
func (h *FeedHandler) followRemoveEventToItem(evt discovery.StreamEvent) FeedItem {
	targetDomain, _ := evt.Payload["target_domain"].(string)

	return FeedItem{
		Type:         "announcement",
		EventType:    evt.Type,
		URL:          "unfollow:" + evt.Actor + ":" + targetDomain,
		Published:    evt.Timestamp,
		AuthorURL:    "https://" + evt.Actor,
		AuthorDomain: evt.Actor,
		TargetURL:    "https://" + targetDomain,
		TargetDomain: targetDomain,
	}
}

// siteRegisteredToItem extracts a FeedItem from a site.registered event.
func (h *FeedHandler) siteRegisteredToItem(evt discovery.StreamEvent) FeedItem {
	return FeedItem{
		Type:         "announcement",
		EventType:    evt.Type,
		URL:          "site-registered:" + evt.Actor,
		Published:    evt.Timestamp,
		AuthorURL:    "https://" + evt.Actor,
		AuthorDomain: evt.Actor,
	}
}
