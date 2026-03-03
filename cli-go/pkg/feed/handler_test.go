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
			Type:      "pub.polis.comment.blessing.granted",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "alice.polis.pub",
			Payload:   map[string]interface{}{},
		},
		{
			ID:        json.Number("2"),
			Type:      "pub.polis.follow.announced",
			Timestamp: "2026-02-01T10:00:00Z",
			Actor:     "bob.polis.pub",
			Payload:   map[string]interface{}{},
		},
	}

	items := h.Process(events)
	if len(items) != 0 {
		t.Errorf("expected 0 items for non-feed events, got %d", len(items))
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
