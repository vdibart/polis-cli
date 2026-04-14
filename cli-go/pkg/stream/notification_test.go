package stream

import (
	"encoding/json"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
)

func TestNotificationHandler_EventTypes(t *testing.T) {
	h := &NotificationHandler{MyDomain: "bob.com", Rules: notification.DefaultRules()}
	types := h.EventTypes()

	expected := map[string]bool{
		"pub.polis.follow.announced":    true,
		"pub.polis.follow.removed":      true,
		"pub.polis.comment.blessing.requested":  true,
		"pub.polis.comment.blessing.granted":    true,
		"pub.polis.comment.blessing.denied":     true,
		"pub.polis.post.published":      true,
		"pub.polis.post.republished":    true,
		"pub.polis.comment.published":   true,
		"pub.polis.comment.republished": true,
	}

	if len(types) != len(expected) {
		t.Fatalf("EventTypes() len = %d, want %d", len(types), len(expected))
	}
	for _, typ := range types {
		if !expected[typ] {
			t.Errorf("unexpected event type: %q", typ)
		}
	}
}

func TestNotificationHandler_BlessingRequested(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/welcome.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(entries))
	}
	if entries[0].RuleID != "blessing-requested" {
		t.Errorf("rule_id = %q, want %q", entries[0].RuleID, "blessing-requested")
	}
	if entries[0].Actor != "alice.com" {
		t.Errorf("actor = %q, want %q", entries[0].Actor, "alice.com")
	}
	if entries[0].Message != "alice.com requested a blessing on welcome" {
		t.Errorf("message = %q", entries[0].Message)
	}
}

func TestNotificationHandler_BlessingGranted_SourceDomain(t *testing.T) {
	// alice.com is the commenter — should get notified via source_domain
	h := &NotificationHandler{
		MyDomain: "alice.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",    // post owner
				"source_domain": "alice.com",  // commenter
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(entries))
	}
	if entries[0].RuleID != "blessing-granted" {
		t.Errorf("rule_id = %q, want %q", entries[0].RuleID, "blessing-granted")
	}
	if entries[0].Actor != "bob.com" {
		t.Errorf("actor = %q, want %q", entries[0].Actor, "bob.com")
	}
}

func TestNotificationHandler_BlessingDenied_SourceDomain(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "alice.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.denied",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
				"source_domain": "alice.com",
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(entries))
	}
	if entries[0].RuleID != "blessing-denied" {
		t.Errorf("rule_id = %q, want %q", entries[0].RuleID, "blessing-denied")
	}
}

func TestNotificationHandler_CommentPublished(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.published",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/welcome.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(entries))
	}
	if entries[0].RuleID != "new-comment" {
		t.Errorf("rule_id = %q, want %q", entries[0].RuleID, "new-comment")
	}
	if entries[0].Message != "alice.com commented on welcome" {
		t.Errorf("message = %q", entries[0].Message)
	}
}

func TestNotificationHandler_SkipSelfEvents(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.published",
			Actor: "bob.com", // self-event
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
				"source_url":    "https://bob.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 0 {
		t.Errorf("expected 0 notifications for self-event, got %d", len(entries))
	}
}

func TestNotificationHandler_SkipMutedDomains(t *testing.T) {
	h := &NotificationHandler{
		MyDomain:     "bob.com",
		Rules:        notification.DefaultRules(),
		MutedDomains: map[string]bool{"spam.com": true},
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.follow.announced",
			Actor: "spam.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 0 {
		t.Errorf("expected 0 notifications for muted domain, got %d", len(entries))
	}
}

func TestNotificationHandler_IgnoresOtherDomains(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		// Follow for someone else
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.follow.announced",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "charlie.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
		// Blessing granted for someone else (source_domain != bob.com)
		{
			ID:    json.Number("2"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "charlie.com",
			Payload: map[string]interface{}{
				"target_domain": "charlie.com",
				"source_domain": "alice.com",
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://charlie.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:01:00Z",
		},
		// Follow for us (should be the ONLY notification)
		{
			ID:    json.Number("3"),
			Type:  "pub.polis.follow.announced",
			Actor: "dave.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:02:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(entries))
	}
	if entries[0].RuleID != "new-follower" {
		t.Errorf("rule_id = %q, want %q", entries[0].RuleID, "new-follower")
	}
	if entries[0].Actor != "dave.com" {
		t.Errorf("actor = %q, want %q", entries[0].Actor, "dave.com")
	}
}

func TestNotificationHandler_DisabledRulesSkipped(t *testing.T) {
	// comment.republished is disabled by default
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.republished",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 0 {
		t.Errorf("expected 0 notifications for disabled rule, got %d", len(entries))
	}
}

func TestNotificationHandler_EnabledEventTypes(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	types := h.EnabledEventTypes()
	// Default: 7 enabled rules covering 7 unique event types
	// (updated-comment and updated-post are disabled)
	if len(types) != 7 {
		t.Errorf("EnabledEventTypes() len = %d, want 7", len(types))
	}
}

func TestNotificationHandler_RulesByRelevance(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	groups := h.RulesByRelevance()
	if len(groups["target_domain"]) != 4 {
		t.Errorf("target_domain rules = %d, want 4", len(groups["target_domain"]))
	}
	if len(groups["source_domain"]) != 2 {
		t.Errorf("source_domain rules = %d, want 2", len(groups["source_domain"]))
	}
	// followed_author has 1 (new-post is enabled, updated-post is disabled)
	if len(groups["followed_author"]) != 1 {
		t.Errorf("followed_author rules = %d, want 1", len(groups["followed_author"]))
	}
}

func TestNotificationHandler_FollowedDomains_FiltersUnfollowed(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
		FollowedDomains: map[string]bool{
			"alice.com": true,
		},
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.post.published",
			Actor: "alice.com", // followed — should produce notification
			Payload: map[string]interface{}{
				"url": "https://alice.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
		{
			ID:    json.Number("2"),
			Type:  "pub.polis.post.published",
			Actor: "stranger.com", // NOT followed — should be filtered out
			Payload: map[string]interface{}{
				"url": "https://stranger.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:01:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 1 {
		t.Fatalf("expected 1 notification (only followed), got %d", len(entries))
	}
	if entries[0].Actor != "alice.com" {
		t.Errorf("actor = %q, want %q", entries[0].Actor, "alice.com")
	}
}

func TestNotificationHandler_FollowedDomains_NilAcceptsAll(t *testing.T) {
	// Legacy mode: FollowedDomains is nil, should accept any non-self actor
	h := &NotificationHandler{
		MyDomain:        "bob.com",
		Rules:           notification.DefaultRules(),
		FollowedDomains: nil, // legacy: no client-side filtering
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.post.published",
			Actor: "stranger.com",
			Payload: map[string]interface{}{
				"url": "https://stranger.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 1 {
		t.Fatalf("expected 1 notification with nil FollowedDomains, got %d", len(entries))
	}
}

func TestNotificationHandler_LinkResolution(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		// Follow event — link should be "/_/#followers"
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.follow.announced",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
		// Blessing granted — link should be "/_/#my-comments-blessed"
		{
			ID:    json.Number("2"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "charlie.com",
			Payload: map[string]interface{}{
				"target_domain": "charlie.com",
				"source_domain": "bob.com",
				"source_url":    "https://bob.com/comments/1.md",
				"target_url":    "https://charlie.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:01:00Z",
		},
		// Comment on our post — link should be "/_/#blessings"
		{
			ID:    json.Number("3"),
			Type:  "pub.polis.comment.published",
			Actor: "dave.com",
			Payload: map[string]interface{}{
				"target_domain": "bob.com",
				"source_url":    "https://dave.com/comments/1.md",
				"target_url":    "https://bob.com/posts/welcome.md",
			},
			Timestamp: "2026-02-10T10:02:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 3 {
		t.Fatalf("expected 3 notifications, got %d", len(entries))
	}

	// Check link fields
	linkTests := []struct {
		ruleID string
		link   string
	}{
		{"new-follower", "/_/social/followers"},
		{"blessing-granted", "/_/comments?filter=blessed"},
		{"new-comment", "/_/blessings"},
	}

	for i, lt := range linkTests {
		if entries[i].RuleID != lt.ruleID {
			t.Errorf("entries[%d].RuleID = %q, want %q", i, entries[i].RuleID, lt.ruleID)
		}
		if entries[i].Link != lt.link {
			t.Errorf("entries[%d].Link = %q, want %q", i, entries[i].Link, lt.link)
		}
	}
}

func TestNotificationHandler_BlessingRequested_RealDSPayload(t *testing.T) {
	// Real DS payload uses comment_url/in_reply_to instead of source_url/target_url.
	// Verify that post_name is derived from in_reply_to correctly.
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"comment_url":   "https://alice.com/comments/20260220/hello-bob.md",
				"in_reply_to":   "https://bob.com/posts/welcome.md",
				"root_post":     "https://bob.com/posts/welcome.md",
				"target_domain": "bob.com",
				"source_domain": "alice.com",
			},
			Timestamp: "2026-02-20T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(entries))
	}
	if entries[0].RuleID != "blessing-requested" {
		t.Errorf("rule_id = %q, want %q", entries[0].RuleID, "blessing-requested")
	}
	// post_name should be derived from in_reply_to (last path segment, no extension)
	want := "alice.com requested a blessing on welcome"
	if entries[0].Message != want {
		t.Errorf("message = %q, want %q", entries[0].Message, want)
	}
}

func TestNotificationHandler_AllDefaultRulesHaveLinks(t *testing.T) {
	rules := notification.DefaultRules()
	for _, r := range rules {
		if r.Template.Link == "" {
			t.Errorf("rule %q has empty Link template", r.ID)
		}
	}
}

func TestNotificationHandler_SelfEventsSkipped(t *testing.T) {
	h := &NotificationHandler{
		MyDomain: "bob.com",
		Rules:    notification.DefaultRules(),
	}

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.follow.announced",
			Actor: "bob.com", // self-event
			Payload: map[string]interface{}{
				"target_domain": "alice.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	entries := h.Process(events)
	if len(entries) != 0 {
		t.Errorf("expected 0 notifications (self-event skipped), got %d", len(entries))
	}
}
