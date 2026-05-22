package comment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// TestSyncParsesNestedInReplyTo verifies that the sync logic can parse
// CLI-format frontmatter where in-reply-to is a nested YAML block and
// comment_url is absent (reconstructed from baseURL + date + ID).
func TestSyncParsesNestedInReplyTo(t *testing.T) {
	dataDir := t.TempDir()

	// Create pending directory
	pendingDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending")
	if err := os.MkdirAll(pendingDir, 0755); err != nil {
		t.Fatal(err)
	}

	// CLI-format comment with nested in-reply-to (no flat comment_url or in_reply_to)
	cliContent := `---
title: Re: hello-world
type: comment
published: 2026-02-22T10:30:00Z
author: follower1.polis.pub
generator: polis-cli-go/0.50.0
in-reply-to:
  url: https://testpilot.polis.pub/posts/20260222/hello-world.md
  root-post: https://testpilot.polis.pub/posts/20260222/hello-world.md
current-version: sha256:abc123
version-history:
  - sha256:abc123 (2026-02-22T10:30:00Z)
signature: AAAA
---

Great post!
`
	commentID := "testpilot-hello-world-20260222"
	if err := os.WriteFile(filepath.Join(pendingDir, commentID+".md"), []byte(cliContent), 0644); err != nil {
		t.Fatal(err)
	}

	baseURL := "https://follower1.polis.pub"

	// Read the comment as sync.go would
	data, err := os.ReadFile(filepath.Join(pendingDir, commentID+".md"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	fm := ParseFrontmatter(content)

	// Verify flat comment_url is empty (not in CLI format)
	if fm["comment_url"] != "" {
		t.Errorf("expected empty comment_url from flat parsing, got %q", fm["comment_url"])
	}

	// Verify flat in_reply_to is empty (nested block, not flat)
	if fm["in_reply_to"] != "" {
		t.Errorf("expected empty in_reply_to from flat parsing, got %q", fm["in_reply_to"])
	}

	// Verify nested in-reply-to parsing works
	inReplyTo, rootPost := ParseNestedInReplyTo(content)
	if inReplyTo != "https://testpilot.polis.pub/posts/20260222/hello-world.md" {
		t.Errorf("expected in_reply_to URL, got %q", inReplyTo)
	}
	if rootPost != "https://testpilot.polis.pub/posts/20260222/hello-world.md" {
		t.Errorf("expected root_post URL, got %q", rootPost)
	}

	// Verify comment URL reconstruction from baseURL + published date + ID
	commentURL := fm["comment_url"]
	if commentURL == "" && baseURL != "" {
		timestamp := time.Now().UTC()
		if ts := fm["published"]; ts != "" {
			if parsed, err := time.Parse("2006-01-02T15:04:05Z", ts); err == nil {
				timestamp = parsed
			}
		}
		dateDir := timestamp.Format("20060102")
		commentURL = strings.TrimSuffix(baseURL, "/") + "/comments/" + dateDir + "/" + commentID + ".md"
	}

	expectedURL := "https://follower1.polis.pub/comments/20260222/" + commentID + ".md"
	if commentURL != expectedURL {
		t.Errorf("expected reconstructed URL %q, got %q", expectedURL, commentURL)
	}
}

func TestSyncFromEvents_BlessingGranted(t *testing.T) {
	dataDir := t.TempDir()

	// Create pending directory and a comment file
	pendingDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending")
	if err := os.MkdirAll(pendingDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Also create the blessed date directory for MoveComment
	blessedDir := filepath.Join(dataDir, "content", "pub.polis.core", "comment")
	if err := os.MkdirAll(blessedDir, 0755); err != nil {
		t.Fatal(err)
	}

	commentContent := `---
comment_url: https://alice.com/comments/20260222/testpost-20260222.md
in_reply_to: https://bob.com/posts/20260222/hello.md
published: 2026-02-22T10:30:00Z
comment_version: sha256:abc123
---

Great post!
`
	commentID := "testpost-20260222"
	if err := os.WriteFile(filepath.Join(pendingDir, commentID+".md"), []byte(commentContent), 0644); err != nil {
		t.Fatal(err)
	}

	events := []discovery.StreamEvent{
		{
			ID:   json.Number("100"),
			Type: "pub.polis.comment.blessing.granted",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/20260222/testpost-20260222.md",
				"source_domain": "alice.com",
				"target_url":    "https://bob.com/posts/20260222/hello.md",
				"target_domain": "bob.com",
			},
		},
	}

	result, err := SyncFromEvents(dataDir, "https://alice.com", events, nil)
	if err != nil {
		t.Fatalf("SyncFromEvents error: %v", err)
	}

	if len(result.Blessed) != 1 {
		t.Fatalf("expected 1 blessed, got %d", len(result.Blessed))
	}
	if result.Blessed[0] != commentID {
		t.Errorf("blessed[0] = %q, want %q", result.Blessed[0], commentID)
	}

	// Verify the file was moved from pending
	if _, err := os.Stat(filepath.Join(pendingDir, commentID+".md")); !os.IsNotExist(err) {
		t.Error("pending comment file still exists after blessing")
	}
}

func TestSyncFromEvents_BlessingDenied(t *testing.T) {
	dataDir := t.TempDir()

	pendingDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending")
	deniedDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "denied")
	if err := os.MkdirAll(pendingDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deniedDir, 0755); err != nil {
		t.Fatal(err)
	}

	commentContent := `---
comment_url: https://alice.com/comments/20260222/test.md
in_reply_to: https://bob.com/posts/20260222/hello.md
published: 2026-02-22T10:30:00Z
---

Hi!
`
	commentID := "test"
	if err := os.WriteFile(filepath.Join(pendingDir, commentID+".md"), []byte(commentContent), 0644); err != nil {
		t.Fatal(err)
	}

	events := []discovery.StreamEvent{
		{
			ID:   json.Number("100"),
			Type: "pub.polis.comment.blessing.denied",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/20260222/test.md",
				"source_domain": "alice.com",
				"target_url":    "https://bob.com/posts/20260222/hello.md",
				"target_domain": "bob.com",
			},
		},
	}

	result, err := SyncFromEvents(dataDir, "https://alice.com", events, nil)
	if err != nil {
		t.Fatalf("SyncFromEvents error: %v", err)
	}

	if len(result.Denied) != 1 {
		t.Fatalf("expected 1 denied, got %d", len(result.Denied))
	}
	if result.Denied[0] != commentID {
		t.Errorf("denied[0] = %q, want %q", result.Denied[0], commentID)
	}

	// Verify moved to denied
	if _, err := os.Stat(filepath.Join(deniedDir, commentID+".md")); os.IsNotExist(err) {
		t.Error("denied comment file not found")
	}
}

func TestSyncFromEvents_IgnoresOtherDomains(t *testing.T) {
	dataDir := t.TempDir()

	pendingDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending")
	if err := os.MkdirAll(pendingDir, 0755); err != nil {
		t.Fatal(err)
	}

	commentContent := `---
comment_url: https://alice.com/comments/20260222/test.md
in_reply_to: https://bob.com/posts/20260222/hello.md
---

Hi!
`
	if err := os.WriteFile(filepath.Join(pendingDir, "test.md"), []byte(commentContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Event for a different source_domain — should be ignored
	events := []discovery.StreamEvent{
		{
			ID:   json.Number("100"),
			Type: "pub.polis.comment.blessing.granted",
			Payload: map[string]interface{}{
				"source_url":    "https://charlie.com/comments/20260222/other.md",
				"source_domain": "charlie.com", // Not alice.com
				"target_url":    "https://bob.com/posts/20260222/hello.md",
				"target_domain": "bob.com",
			},
		},
	}

	result, err := SyncFromEvents(dataDir, "https://alice.com", events, nil)
	if err != nil {
		t.Fatalf("SyncFromEvents error: %v", err)
	}

	if len(result.Blessed) != 0 || len(result.Denied) != 0 {
		t.Errorf("expected no changes for other domain, got %d blessed %d denied", len(result.Blessed), len(result.Denied))
	}
}

func TestSyncFromEvents_EmptyEvents(t *testing.T) {
	dataDir := t.TempDir()

	result, err := SyncFromEvents(dataDir, "https://alice.com", nil, nil)
	if err != nil {
		t.Fatalf("SyncFromEvents error: %v", err)
	}
	if len(result.Blessed) != 0 || len(result.Denied) != 0 {
		t.Error("expected empty result for empty events")
	}
}

func TestSyncFromEvents_EmptyBaseURL(t *testing.T) {
	dataDir := t.TempDir()

	events := []discovery.StreamEvent{
		{ID: json.Number("1"), Type: "pub.polis.comment.blessing.granted", Payload: map[string]interface{}{
			"source_url": "https://x.com/c/1.md", "source_domain": "",
		}},
	}

	result, err := SyncFromEvents(dataDir, "", events, nil)
	if err != nil {
		t.Fatalf("SyncFromEvents error: %v", err)
	}
	if len(result.Blessed) != 0 || len(result.Denied) != 0 {
		t.Error("expected empty result for empty baseURL")
	}
}

// TestSyncParsesLegacyFlatFrontmatter verifies backward compat with flat frontmatter.
func TestSyncParsesLegacyFlatFrontmatter(t *testing.T) {
	content := `---
comment_url: https://follower1.polis.pub/comments/20260222/test.md
in_reply_to: https://testpilot.polis.pub/posts/20260222/hello.md
timestamp: 2026-02-22T10:30:00Z
comment_version: sha256:abc123
---

Hello!
`
	fm := ParseFrontmatter(content)

	if fm["comment_url"] != "https://follower1.polis.pub/comments/20260222/test.md" {
		t.Errorf("expected comment_url, got %q", fm["comment_url"])
	}
	if fm["in_reply_to"] != "https://testpilot.polis.pub/posts/20260222/hello.md" {
		t.Errorf("expected in_reply_to, got %q", fm["in_reply_to"])
	}

	// Nested parse should return empty for flat format
	inReplyTo, _ := ParseNestedInReplyTo(content)
	if inReplyTo != "" {
		t.Errorf("expected empty nested in_reply_to for flat format, got %q", inReplyTo)
	}
}
