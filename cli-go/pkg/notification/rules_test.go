package notification

import (
	"testing"
)

func TestDefaultRules(t *testing.T) {
	rules := DefaultRules()
	if len(rules) != 9 {
		t.Errorf("DefaultRules() returned %d rules, want 9", len(rules))
	}

	// Check all event types are covered
	eventTypes := map[string]bool{
		"pub.polis.follow.announced":            false,
		"pub.polis.follow.removed":              false,
		"pub.polis.comment.blessing.requested":  false,
		"pub.polis.comment.blessing.granted":    false,
		"pub.polis.comment.blessing.denied":     false,
		"pub.polis.comment.published":           false,
		"pub.polis.comment.republished":         false,
		"pub.polis.post.published":              false,
		"pub.polis.post.republished":            false,
	}
	for _, r := range rules {
		if _, ok := eventTypes[r.EventType]; !ok {
			t.Errorf("unexpected event type %q", r.EventType)
		}
		eventTypes[r.EventType] = true
	}
	for et, covered := range eventTypes {
		if !covered {
			t.Errorf("event type %q not covered by any rule", et)
		}
	}

	// Check that blessing.granted/denied use source_domain filter
	for _, r := range rules {
		switch r.ID {
		case "blessing-granted", "blessing-denied":
			if r.Filter.Relevance != "source_domain" {
				t.Errorf("rule %q has filter %q, want source_domain", r.ID, r.Filter.Relevance)
			}
		case "new-post", "updated-post":
			if r.Filter.Relevance != "followed_author" {
				t.Errorf("rule %q has filter %q, want followed_author", r.ID, r.Filter.Relevance)
			}
		}
	}

	// Check disabled rules
	for _, r := range rules {
		switch r.ID {
		case "updated-comment", "updated-post":
			if r.Enabled {
				t.Errorf("rule %q should be disabled by default", r.ID)
			}
		default:
			if !r.Enabled {
				t.Errorf("rule %q should be enabled by default", r.ID)
			}
		}
	}
}

func TestResolveTemplate(t *testing.T) {
	tests := []struct {
		name   string
		tmpl   string
		vars   map[string]string
		expect string
	}{
		{
			name:   "simple actor",
			tmpl:   "{{actor}} started following you",
			vars:   map[string]string{"actor": "alice.com"},
			expect: "alice.com started following you",
		},
		{
			name:   "multiple vars",
			tmpl:   "{{actor}} commented on {{post_name}}",
			vars:   map[string]string{"actor": "bob.com", "post_name": "welcome"},
			expect: "bob.com commented on welcome",
		},
		{
			name:   "no vars to replace",
			tmpl:   "static message",
			vars:   map[string]string{},
			expect: "static message",
		},
		{
			name:   "missing var left as-is",
			tmpl:   "{{actor}} on {{post_name}}",
			vars:   map[string]string{"actor": "alice.com"},
			expect: "alice.com on {{post_name}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveTemplate(tt.tmpl, tt.vars)
			if got != tt.expect {
				t.Errorf("ResolveTemplate() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestTemplateVarsFromEvent(t *testing.T) {
	vars := TemplateVarsFromEvent("alice.com", "2025-01-15T10:30:00Z", map[string]interface{}{
		"source_url":    "https://bob.com/comments/reply.md",
		"target_url":    "https://alice.com/posts/welcome.md",
		"target_domain": "alice.com",
		"source_domain": "bob.com",
	})

	if vars["actor"] != "alice.com" {
		t.Errorf("actor = %q, want alice.com", vars["actor"])
	}
	if vars["post_name"] != "welcome" {
		t.Errorf("post_name = %q, want welcome", vars["post_name"])
	}
	if vars["source_domain"] != "bob.com" {
		t.Errorf("source_domain = %q, want bob.com", vars["source_domain"])
	}
}

func TestDedupeKey(t *testing.T) {
	// With source_url
	key := DedupeKey("blessing-requested", map[string]interface{}{
		"source_url": "https://bob.com/comments/reply.md",
	})
	if key != "blessing-requested:https://bob.com/comments/reply.md" {
		t.Errorf("DedupeKey() = %q", key)
	}

	// Without source_url, falls back to target_domain
	key = DedupeKey("new-follower", map[string]interface{}{
		"target_domain": "alice.com",
	})
	if key != "new-follower:alice.com" {
		t.Errorf("DedupeKey() = %q", key)
	}

	// With url (post events use "url" instead of "source_url")
	key = DedupeKey("new-post", map[string]interface{}{
		"url":           "https://discover.polis.pub/posts/20260212/hello-world.md",
		"target_domain": "discover.polis.pub",
	})
	if key != "new-post:https://discover.polis.pub/posts/20260212/hello-world.md" {
		t.Errorf("DedupeKey() with url = %q, want post URL-based key", key)
	}

	// source_url takes priority over url
	key = DedupeKey("new-comment", map[string]interface{}{
		"source_url": "https://bob.com/comments/reply.md",
		"url":        "https://bob.com/other.md",
	})
	if key != "new-comment:https://bob.com/comments/reply.md" {
		t.Errorf("DedupeKey() source_url should take priority, got %q", key)
	}

	// Empty payload uses hash fallback
	key = DedupeKey("test", map[string]interface{}{})
	if key == "" {
		t.Error("DedupeKey() should not be empty")
	}
}

func TestTemplateVarsFromEvent_PostEvent(t *testing.T) {
	// Post events have "url" instead of "target_url", and metadata.title
	vars := TemplateVarsFromEvent("discover.polis.pub", "2026-02-12T04:00:00Z", map[string]interface{}{
		"url":           "https://discover.polis.pub/posts/20260212/hello-world.md",
		"target_domain": "discover.polis.pub",
		"metadata": map[string]interface{}{
			"title": "Hello World",
		},
	})

	if vars["post_name"] != "hello-world" {
		t.Errorf("post_name = %q, want hello-world (derived from url)", vars["post_name"])
	}
	if vars["title"] != "Hello World" {
		t.Errorf("title = %q, want 'Hello World'", vars["title"])
	}
}
