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
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.post.published",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "me.polis.pub", // Self-authored
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
			Actor:     "alice.polis.pub", // Not self
			Payload: map[string]interface{}{
				"url":     "https://alice.polis.pub/posts/hello.md",
				"version": "def",
				"metadata": map[string]interface{}{
					"title": "Alice's Post",
				},
			},
		},
	}

	items := h.Process(events)
	if len(items) != 1 {
		t.Fatalf("expected 1 item (self-event skipped), got %d", len(items))
	}
	if items[0].Title != "Alice's Post" {
		t.Errorf("expected Alice's Post, got %s", items[0].Title)
	}
}

func TestFeedHandler_IgnoresUnknownTypes(t *testing.T) {
	h := &FeedHandler{
		MyDomain: "me.polis.pub",
	}

	events := []discovery.StreamEvent{
		{
			ID:        json.Number("1"),
			Type:      "pub.polis.follow.announced",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "bob.polis.pub",
			Payload:   map[string]interface{}{},
		},
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.comment.blessing.requested",
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
	if len(items) != 1 {
		t.Fatalf("expected 1 item from blessing.granted, got %d", len(items))
	}

	item := items[0]
	if item.Type != "comment" {
		t.Errorf("expected type comment, got %s", item.Type)
	}
	if item.URL != "https://me.polis.pub/comments/20260304/reply.md" {
		t.Errorf("expected comment URL, got %s", item.URL)
	}
	if item.AuthorDomain != "me.polis.pub" {
		t.Errorf("expected author domain me.polis.pub (comment author), got %s", item.AuthorDomain)
	}
	if item.AuthorURL != "https://me.polis.pub" {
		t.Errorf("expected author URL https://me.polis.pub, got %s", item.AuthorURL)
	}
	if item.TargetURL != "https://david.polis.pub/posts/20260304/hello-world.html" {
		t.Errorf("expected target URL, got %s", item.TargetURL)
	}
	if item.TargetDomain != "david.polis.pub" {
		t.Errorf("expected target domain david.polis.pub, got %s", item.TargetDomain)
	}
	if item.Published != "2026-02-04T12:00:00Z" {
		t.Errorf("expected published from event timestamp, got %s", item.Published)
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
	if len(items) != 1 {
		t.Fatalf("expected 1 item from manual blessing.granted, got %d", len(items))
	}

	item := items[0]
	if item.Type != "comment" {
		t.Errorf("expected type comment, got %s", item.Type)
	}
	if item.URL != "https://bob.polis.pub/comments/20260305/reply.md" {
		t.Errorf("expected comment URL from source_url, got %s", item.URL)
	}
	if item.AuthorDomain != "bob.polis.pub" {
		t.Errorf("expected author domain bob.polis.pub (comment author), got %s", item.AuthorDomain)
	}
	if item.TargetURL != "https://alice.polis.pub/posts/20260301/post.html" {
		t.Errorf("expected target URL from target_url, got %s", item.TargetURL)
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
	// Should get 2 items: the post + the blessing.granted comment (comment.published was self-skipped)
	if len(items) != 2 {
		t.Fatalf("expected 2 items (post + blessed comment), got %d", len(items))
	}

	// Verify the post
	if items[0].Type != "post" {
		t.Errorf("expected first item to be post, got %s", items[0].Type)
	}
	if items[0].Title != "Hello World!" {
		t.Errorf("expected post title 'Hello World!', got %s", items[0].Title)
	}

	// Verify the comment came through via blessing.granted
	if items[1].Type != "comment" {
		t.Errorf("expected second item to be comment, got %s", items[1].Type)
	}
	if items[1].URL != "https://me.polis.pub/comments/20260304/reply.md" {
		t.Errorf("expected my comment URL, got %s", items[1].URL)
	}
	if items[1].AuthorDomain != "me.polis.pub" {
		t.Errorf("expected author to be me (comment author), got %s", items[1].AuthorDomain)
	}
	if items[1].TargetURL != "https://david.polis.pub/posts/20260304/hello-world.html" {
		t.Errorf("expected target URL, got %s", items[1].TargetURL)
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
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
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
	if len(items) != 1 {
		t.Fatalf("expected 1 item even with empty payload, got %d", len(items))
	}
	if items[0].Type != "comment" {
		t.Errorf("expected type comment, got %s", items[0].Type)
	}
	if items[0].Published != "2026-02-05T15:00:00Z" {
		t.Errorf("expected timestamp fallback, got %s", items[0].Published)
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
