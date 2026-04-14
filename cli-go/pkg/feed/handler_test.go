package feed

import (
	"encoding/json"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

func TestFeedHandler_PostEvent(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
		FollowedDomains: map[string]bool{
			"alice.polis.pub": true,
		},
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.post.published",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "alice.polis.pub",
			Payload: map[string]interface{}{
				"url":     "https://alice.polis.pub/posts/hello.md",
				"version": "abc123",
				"author":  "alice@example.com",
				"metadata": map[string]interface{}{
					"title":        "Hello World",
					"published_at": "2026-02-01T10:00:00Z",
				},
			},
		},
	}

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if item.Type != "post" {
		t.Errorf("expected type post, got %s", item.Type)
	}
	if item.Title != "Hello World" {
		t.Errorf("expected title Hello World, got %s", item.Title)
	}
	if item.URL != "https://alice.polis.pub/posts/hello.md" {
		t.Errorf("expected URL, got %s", item.URL)
	}
	if item.Hash != "abc123" {
		t.Errorf("expected hash abc123, got %s", item.Hash)
	}
	if item.AuthorDomain != "alice.polis.pub" {
		t.Errorf("expected author domain alice.polis.pub, got %s", item.AuthorDomain)
	}
	if item.AuthorURL != "https://alice.polis.pub" {
		t.Errorf("expected author URL, got %s", item.AuthorURL)
	}
	if item.Published != "2026-02-01T10:00:00Z" {
		t.Errorf("expected published date, got %s", item.Published)
	}
}

func TestFeedHandler_CommentEvent(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
		FollowedDomains: map[string]bool{
			"bob.polis.pub": true,
		},
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.comment.published",
			Timestamp: "2026-02-02T10:00:00Z",
			Actor:     "bob.polis.pub",
			Payload: map[string]interface{}{
				"comment_url": "https://bob.polis.pub/comments/reply.md",
				"in_reply_to": "https://alice.polis.pub/posts/hello.md",
				"root_post":   "https://alice.polis.pub/posts/hello.md",
				"author":      "bob@example.com",
				"version":     "def456",
				"metadata": map[string]interface{}{
					"title":        "Great post!",
					"published_at": "2026-02-02T10:00:00Z",
				},
			},
		},
	}

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if item.Type != "comment" {
		t.Errorf("expected type comment, got %s", item.Type)
	}
	if item.Title != "Great post!" {
		t.Errorf("expected title 'Great post!', got %s", item.Title)
	}
	if item.URL != "https://bob.polis.pub/comments/reply.md" {
		t.Errorf("expected comment URL, got %s", item.URL)
	}
	if item.Hash != "def456" {
		t.Errorf("expected hash def456, got %s", item.Hash)
	}
	if item.AuthorDomain != "bob.polis.pub" {
		t.Errorf("expected author domain bob.polis.pub, got %s", item.AuthorDomain)
	}
	if item.TargetURL != "https://alice.polis.pub/posts/hello.md" {
		t.Errorf("expected target URL https://alice.polis.pub/posts/hello.md, got %s", item.TargetURL)
	}
}

func TestFeedHandler_CommentEvent_NoTargetURL(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("3"),
			Type:      "pub.polis.comment.published",
			Timestamp: "2026-02-02T10:00:00Z",
			Actor:     "bob.polis.pub",
			Payload: map[string]interface{}{
				"comment_url": "https://bob.polis.pub/comments/orphan.md",
				"version":     "xyz789",
				"metadata": map[string]interface{}{
					"title": "Orphan comment",
				},
			},
		},
	}

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].TargetURL != "" {
		t.Errorf("expected empty target URL for comment without in_reply_to, got %s", items[0].TargetURL)
	}
}

func TestFeedHandler_CommentEvent_RootPostFallback(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("4"),
			Type:      "pub.polis.comment.published",
			Timestamp: "2026-02-02T10:00:00Z",
			Actor:     "bob.polis.pub",
			Payload: map[string]interface{}{
				"comment_url":   "https://bob.polis.pub/comments/reply2.md",
				"root_post":     "https://alice.polis.pub/posts/hello.md",
				"target_domain": "alice.polis.pub",
				"version":       "v2",
				"metadata": map[string]interface{}{
					"title": "Reply via root_post",
				},
			},
		},
	}

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].TargetURL != "https://alice.polis.pub/posts/hello.md" {
		t.Errorf("expected target URL from root_post, got %s", items[0].TargetURL)
	}
	if items[0].TargetDomain != "alice.polis.pub" {
		t.Errorf("expected target domain alice.polis.pub, got %s", items[0].TargetDomain)
	}
}

func TestFeedHandler_SkipsSelfEvents(t *testing.T) {
	// Legacy behavior: no policies, hardcoded self-skip
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := selfAndOtherEvents()

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item (self-event skipped), got %d", len(items))
	}
	if items[0].Title != "Alice's Post" {
		t.Errorf("expected Alice's Post, got %s", items[0].Title)
	}
}

func TestFeedHandler_IncludeSelf(t *testing.T) {
	h := &FeedHandler{
		MyDomain:    "me.polis.pub",
		IncludeSelf: true,
	}

	events := selfAndOtherEvents()
	items := h.Process(events)
	if len(items) != 2 {
		t.Fatalf("expected 2 items (self-event included), got %d", len(items))
	}
	// Self-event should be first (ID 1)
	if items[0].Title != "My Own Post" {
		t.Errorf("expected My Own Post, got %s", items[0].Title)
	}
}


// selfAndOtherEvents returns a pair of events: one self-authored and one from alice.
func selfAndOtherEvents() []discovery.StreamEvent {
	return []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.post.published",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "me.polis.pub",
			Payload: map[string]interface{}{
				"url":     "https://me.polis.pub/posts/my-post.md",
				"version": "abc",
				"metadata": map[string]interface{}{
					"title": "My Own Post",
				},
			},
		},
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.post.published",
			Timestamp: "2026-02-01T11:00:00Z",
			Actor:     "alice.polis.pub",
			Payload: map[string]interface{}{
				"url":     "https://alice.polis.pub/posts/hello.md",
				"version": "def",
				"metadata": map[string]interface{}{
					"title": "Alice's Post",
				},
			},
		},
	}
}

func TestFeedHandler_IgnoresUnknownTypes(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.tag.applied",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "bob.polis.pub",
			Payload:   map[string]interface{}{},
		},
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.some.future.type",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "charlie.polis.pub",
			Payload:   map[string]interface{}{},
		},
	}

	items := h.Process(events)
	if len(items) != 0 {
		t.Errorf("expected 0 items for non-feed events, got %d", len(items))
	}
}

func TestFeedHandler_FollowAnnounced(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.follow.announced",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "bob.polis.pub",
			Payload: map[string]interface{}{
				"target_domain": "alice.polis.pub",
			},
		},
	}

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Type != "announcement" {
		t.Errorf("expected type announcement, got %s", items[0].Type)
	}
	if items[0].EventType != "pub.polis.follow.announced" {
		t.Errorf("expected event_type pub.polis.follow.announced, got %s", items[0].EventType)
	}
	if items[0].AuthorDomain != "bob.polis.pub" {
		t.Errorf("expected author bob.polis.pub, got %s", items[0].AuthorDomain)
	}
	if items[0].TargetDomain != "alice.polis.pub" {
		t.Errorf("expected target alice.polis.pub, got %s", items[0].TargetDomain)
	}
}

func TestFeedHandler_BlessingRequested(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.comment.blessing.requested",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "charlie.polis.pub",
			Payload: map[string]interface{}{
				"source_domain": "charlie.polis.pub",
				"target_domain": "alice.polis.pub",
				"target_url":    "https://alice.polis.pub/posts/hello.html",
			},
		},
	}

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Type != "announcement" {
		t.Errorf("expected type announcement, got %s", items[0].Type)
	}
	if items[0].EventType != "pub.polis.comment.blessing.requested" {
		t.Errorf("expected event_type, got %s", items[0].EventType)
	}
}

func TestFeedHandler_SiteRegistered(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.site.registered",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "newuser.polis.pub",
			Payload:   map[string]interface{}{},
		},
	}

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Type != "announcement" {
		t.Errorf("expected type announcement, got %s", items[0].Type)
	}
	if items[0].EventType != "pub.polis.site.registered" {
		t.Errorf("expected event_type, got %s", items[0].EventType)
	}
	if items[0].AuthorDomain != "newuser.polis.pub" {
		t.Errorf("expected author newuser.polis.pub, got %s", items[0].AuthorDomain)
	}
}

func TestFeedHandler_BlessingGrantedAutoBlessed(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.comment.blessing.granted",
			Timestamp: "2026-02-04T12:00:00Z",
			Actor:     "david.polis.pub", // Post author who blessed
			Payload: map[string]interface{}{
				"comment_url":   "https://me.polis.pub/comments/20260304/reply.md",
				"in_reply_to":   "https://david.polis.pub/posts/20260304/hello-world.html",
				"root_post":     "https://david.polis.pub/posts/20260304/hello-world.html",
				"auto_blessed":  true,
				"source_domain": "me.polis.pub",
				"target_domain": "david.polis.pub",
			},
		},
	}

	items := h.Process(events)
	if len(items) != 2 {
		t.Fatalf("expected 2 items from blessing.granted (announcement + comment), got %d", len(items))
	}

	// First item: announcement (post author granted blessing)
	ann := items[0]
	if ann.Type != "announcement" {
		t.Errorf("expected type announcement, got %s", ann.Type)
	}
	if ann.AuthorDomain != "david.polis.pub" {
		t.Errorf("expected author domain david.polis.pub (post author who granted), got %s", ann.AuthorDomain)
	}
	if ann.TargetDomain != "me.polis.pub" {
		t.Errorf("expected target domain me.polis.pub (comment author who received), got %s", ann.TargetDomain)
	}

	// Second item: comment (so it groups under the post)
	cmt := items[1]
	if cmt.Type != "comment" {
		t.Errorf("expected type comment, got %s", cmt.Type)
	}
	if cmt.AuthorDomain != "me.polis.pub" {
		t.Errorf("expected comment author me.polis.pub, got %s", cmt.AuthorDomain)
	}
	if cmt.TargetURL != "https://david.polis.pub/posts/20260304/hello-world.html" {
		t.Errorf("expected target URL, got %s", cmt.TargetURL)
	}
}

func TestFeedHandler_BlessingGrantedManual(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.comment.blessing.granted",
			Timestamp: "2026-02-05T15:00:00Z",
			Actor:     "alice.polis.pub", // Post author who manually blessed
			Payload: map[string]interface{}{
				"source_url":    "https://bob.polis.pub/comments/20260305/reply.md",
				"target_url":    "https://alice.polis.pub/posts/20260301/post.html",
				"action":        "grant",
				"source_domain": "bob.polis.pub",
				"target_domain": "alice.polis.pub",
			},
		},
	}

	items := h.Process(events)
	if len(items) != 2 {
		t.Fatalf("expected 2 items from manual blessing.granted, got %d", len(items))
	}

	ann := items[0]
	if ann.Type != "announcement" {
		t.Errorf("expected type announcement, got %s", ann.Type)
	}
	if ann.URL != "https://bob.polis.pub/comments/20260305/reply.md" {
		t.Errorf("expected comment URL from source_url, got %s", ann.URL)
	}
	if ann.AuthorDomain != "alice.polis.pub" {
		t.Errorf("expected author domain alice.polis.pub (post author who granted), got %s", ann.AuthorDomain)
	}
	if ann.TargetDomain != "bob.polis.pub" {
		t.Errorf("expected target domain bob.polis.pub (comment author), got %s", ann.TargetDomain)
	}

	cmt := items[1]
	if cmt.Type != "comment" {
		t.Errorf("expected type comment, got %s", cmt.Type)
	}
	if cmt.AuthorDomain != "bob.polis.pub" {
		t.Errorf("expected comment author bob.polis.pub, got %s", cmt.AuthorDomain)
	}
	if cmt.TargetURL != "https://alice.polis.pub/posts/20260301/post.html" {
		t.Errorf("expected target URL from target_url, got %s", cmt.TargetURL)
	}
}

func TestFeedHandler_BlessingGranted_SkippedWhenActorIsSelf(t *testing.T) {
	// When I bless someone else's comment on my post, actor = me.
	// The blessing event should be skipped (self-skip applies to actor).
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.comment.blessing.granted",
			Timestamp: "2026-02-05T15:00:00Z",
			Actor:     "me.polis.pub", // I blessed it
			Payload: map[string]interface{}{
				"comment_url":   "https://bob.polis.pub/comments/20260305/reply.md",
				"in_reply_to":   "https://me.polis.pub/posts/20260301/my-post.html",
				"source_domain": "bob.polis.pub",
				"target_domain": "me.polis.pub",
			},
		},
	}

	items := h.Process(events)
	if len(items) != 0 {
		t.Errorf("expected 0 items (self-actor blessing skipped), got %d", len(items))
	}
}

func TestFeedHandler_BlessingGranted_OwnCommentVisibleViaBlessingEvent(t *testing.T) {
	// Core bug scenario: my comment on someone's post is skipped via
	// comment.published (actor=me) but appears via blessing.granted (actor=post author).
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		// My comment published — actor is me, so it gets skipped
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.comment.published",
			Timestamp: "2026-02-04T10:00:00Z",
			Actor:     "me.polis.pub",
			Payload: map[string]interface{}{
				"comment_url":   "https://me.polis.pub/comments/20260304/reply.md",
				"in_reply_to":   "https://david.polis.pub/posts/20260304/hello-world.html",
				"source_domain": "me.polis.pub",
				"target_domain": "david.polis.pub",
				"version":       "abc",
				"metadata": map[string]interface{}{
					"title": "My Reply",
				},
			},
		},
		// David's post — actor is david, not me
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.post.published",
			Timestamp: "2026-02-04T08:00:00Z",
			Actor:     "david.polis.pub",
			Payload: map[string]interface{}{
				"url":     "https://david.polis.pub/posts/20260304/hello-world.html",
				"version": "def",
				"metadata": map[string]interface{}{
					"title":        "Hello World!",
					"published_at": "2026-02-04T08:00:00Z",
				},
			},
		},
		// David blesses my comment — actor is david, not me
		{
			ID:        json.Number("3"),
			Type:      "pub.polis.comment.blessing.granted",
			Timestamp: "2026-02-04T12:00:00Z",
			Actor:     "david.polis.pub",
			Payload: map[string]interface{}{
				"comment_url":   "https://me.polis.pub/comments/20260304/reply.md",
				"in_reply_to":   "https://david.polis.pub/posts/20260304/hello-world.html",
				"root_post":     "https://david.polis.pub/posts/20260304/hello-world.html",
				"auto_blessed":  true,
				"source_domain": "me.polis.pub",
				"target_domain": "david.polis.pub",
			},
		},
	}

	items := h.Process(events)
	// Should get 3 items: the post + blessing announcement + comment (comment.published was self-skipped)
	if len(items) != 3 {
		t.Fatalf("expected 3 items (post + announcement + comment), got %d", len(items))
	}

	// Verify the post
	if items[0].Type != "post" {
		t.Errorf("expected first item to be post, got %s", items[0].Type)
	}
	if items[0].Title != "Hello World!" {
		t.Errorf("expected post title 'Hello World!', got %s", items[0].Title)
	}

	// Verify the announcement
	if items[1].Type != "announcement" {
		t.Errorf("expected second item to be announcement, got %s", items[1].Type)
	}
	if items[1].AuthorDomain != "david.polis.pub" {
		t.Errorf("expected announcement author david.polis.pub (granter), got %s", items[1].AuthorDomain)
	}

	// Verify the comment (groups under the post)
	if items[2].Type != "comment" {
		t.Errorf("expected third item to be comment, got %s", items[2].Type)
	}
	if items[2].URL != "https://me.polis.pub/comments/20260304/reply.md" {
		t.Errorf("expected my comment URL, got %s", items[2].URL)
	}
	if items[2].AuthorDomain != "me.polis.pub" {
		t.Errorf("expected comment author me.polis.pub, got %s", items[2].AuthorDomain)
	}
}

func TestFeedHandler_BlessingGranted_FallbackFieldPrecedence(t *testing.T) {
	// When both comment_url and source_url exist, comment_url wins.
	// When both in_reply_to and target_url exist, in_reply_to wins.
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.comment.blessing.granted",
			Timestamp: "2026-02-05T15:00:00Z",
			Actor:     "alice.polis.pub",
			Payload: map[string]interface{}{
				"comment_url":   "https://bob.polis.pub/comments/preferred.md",
				"source_url":    "https://bob.polis.pub/comments/fallback.md",
				"in_reply_to":   "https://alice.polis.pub/posts/preferred.html",
				"root_post":     "https://alice.polis.pub/posts/root.html",
				"target_url":    "https://alice.polis.pub/posts/fallback.html",
				"source_domain": "bob.polis.pub",
				"target_domain": "alice.polis.pub",
			},
		},
	}

	items := h.Process(events)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].URL != "https://bob.polis.pub/comments/preferred.md" {
		t.Errorf("expected comment_url to take precedence, got %s", items[0].URL)
	}
	if items[0].TargetURL != "https://alice.polis.pub/posts/preferred.html" {
		t.Errorf("expected in_reply_to to take precedence, got %s", items[0].TargetURL)
	}
}

func TestFeedHandler_BlessingGranted_EmptyPayload(t *testing.T) {
	// Blessing event with minimal/empty payload should still produce an item
	// (with empty fields) rather than panic.
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.comment.blessing.granted",
			Timestamp: "2026-02-05T15:00:00Z",
			Actor:     "alice.polis.pub",
			Payload:   map[string]interface{}{},
		},
	}

	items := h.Process(events)
	if len(items) != 2 {
		t.Fatalf("expected 2 items even with empty payload, got %d", len(items))
	}
	if items[0].Type != "announcement" {
		t.Errorf("expected type announcement, got %s", items[0].Type)
	}
	if items[0].Published != "2026-02-05T15:00:00Z" {
		t.Errorf("expected timestamp fallback, got %s", items[0].Published)
	}
	if items[1].Type != "comment" {
		t.Errorf("expected type comment, got %s", items[1].Type)
	}
}

func TestFeedHandler_RepublishedEvents(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.post.republished",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "alice.polis.pub",
			Payload: map[string]interface{}{
				"url":     "https://alice.polis.pub/posts/updated.md",
				"version": "v2",
				"metadata": map[string]interface{}{
					"title":        "Updated Post",
					"published_at": "2026-02-01T10:00:00Z",
				},
			},
		},
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.comment.republished",
			Timestamp: "2026-02-02T10:00:00Z",
			Actor:     "bob.polis.pub",
			Payload: map[string]interface{}{
				"comment_url": "https://bob.polis.pub/comments/reply.md",
				"version":     "v2",
				"metadata": map[string]interface{}{
					"title": "Updated Comment",
				},
			},
		},
	}

	items := h.Process(events)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Type != "post" {
		t.Errorf("expected post, got %s", items[0].Type)
	}
	if items[1].Type != "comment" {
		t.Errorf("expected comment, got %s", items[1].Type)
	}
}

func TestFeedHandler_FallbackTimestamp(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.post.published",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "alice.polis.pub",
			Payload: map[string]interface{}{
				"url":     "https://alice.polis.pub/posts/no-date.md",
				"version": "abc",
				"metadata": map[string]interface{}{
					"title": "No Published Date",
					// No published_at — should fall back to event timestamp
				},
			},
		},
	}

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Published != "2026-02-01T10:00:00Z" {
		t.Errorf("expected fallback to event timestamp, got %s", items[0].Published)
	}
}

func TestFeedHandler_EmptyEvents(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	items := h.Process(nil)
	if len(items) != 0 {
		t.Errorf("expected 0 items for nil events, got %d", len(items))
	}

	items = h.Process([]discovery.StreamEvent{})
	if len(items) != 0 {
		t.Errorf("expected 0 items for empty events, got %d", len(items))
	}
}
