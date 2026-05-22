package comment

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBeseechComment_NotConfigured(t *testing.T) {
	oldURL, oldKey, oldBase := DiscoveryURL, DiscoveryKey, BaseURL
	defer func() { DiscoveryURL, DiscoveryKey, BaseURL = oldURL, oldKey, oldBase }()

	DiscoveryURL = ""
	DiscoveryKey = ""
	BaseURL = ""

	_, err := BeseechComment(t.TempDir(), "test-id", nil)
	if err == nil {
		t.Error("expected error when not configured")
	}
	if err.Error() != "discovery service not configured" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBeseechComment_NoBaseURL(t *testing.T) {
	oldURL, oldKey, oldBase := DiscoveryURL, DiscoveryKey, BaseURL
	defer func() { DiscoveryURL, DiscoveryKey, BaseURL = oldURL, oldKey, oldBase }()

	DiscoveryURL = "https://discovery.example.com"
	DiscoveryKey = "test-key"
	BaseURL = ""

	_, err := BeseechComment(t.TempDir(), "test-id", nil)
	if err == nil {
		t.Error("expected error when base URL not configured")
	}
	if err.Error() != "POLIS_BASE_URL not configured" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBeseechComment_CommentNotFound(t *testing.T) {
	oldURL, oldKey, oldBase := DiscoveryURL, DiscoveryKey, BaseURL
	defer func() { DiscoveryURL, DiscoveryKey, BaseURL = oldURL, oldKey, oldBase }()

	DiscoveryURL = "https://discovery.example.com"
	DiscoveryKey = "test-key"
	BaseURL = "https://test.polis.pub"

	dataDir := t.TempDir()

	_, err := BeseechComment(dataDir, "nonexistent-id", nil)
	if err == nil {
		t.Error("expected error when comment not found")
	}
}

// TestBeseechComment_NotRegisteredLocally exercises the "deferred" path
// introduced when the site has DS config + base URL + a pending comment
// but no local registration marker. The expected outcome is:
//   - no error (was previously: error "site not registered with...")
//   - result.Status == "deferred"
//   - result.Success == true
//   - the comment IS published to content/pub.polis.core/comment/YYYYMMDD/{id}.md
//     (PublishComment runs BEFORE the registration check now — local
//     state captured even when DS contact is skipped)
//
// Regression guard: re-ordering or removing the deferred branch (or moving
// the PublishComment step back below the registration check) would fail
// this test.
func TestBeseechComment_NotRegisteredLocally(t *testing.T) {
	oldURL, oldKey, oldBase := DiscoveryURL, DiscoveryKey, BaseURL
	defer func() { DiscoveryURL, DiscoveryKey, BaseURL = oldURL, oldKey, oldBase }()

	DiscoveryURL = "https://discovery.example.com"
	DiscoveryKey = "test-key"
	BaseURL = "https://test.polis.pub"

	dataDir := t.TempDir()

	// Set up the comment directory tree SignComment + PublishComment expect.
	dirs := []string{
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "denied"),
		filepath.Join(dataDir, "content", "pub.polis.core", "comment"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Sign a comment so beseech has something in pending/ to act on.
	privKey := generateTestKey(t)
	draft := &CommentDraft{
		InReplyTo: "https://alice.polis.pub/posts/20260101/hello.md",
		Content:   "Local-only comment — DS deferred until registration.",
	}
	signed, err := SignComment(dataDir, draft, "bob.polis.pub", "https://bob.polis.pub", privKey)
	if err != nil {
		t.Fatalf("SignComment failed: %v", err)
	}
	if signed.Meta.ID == "" {
		t.Fatal("SignComment returned empty comment ID")
	}

	// Intentionally do NOT write a registration marker; this is the
	// scenario under test.

	result, err := BeseechComment(dataDir, signed.Meta.ID, privKey)
	if err != nil {
		t.Fatalf("BeseechComment returned error when not registered (should defer): %v", err)
	}
	if result == nil {
		t.Fatal("BeseechComment returned nil result")
	}
	if !result.Success {
		t.Errorf("result.Success = false, want true")
	}
	if result.Status != "deferred" {
		t.Errorf("result.Status = %q, want %q", result.Status, "deferred")
	}
	if result.Message == "" {
		t.Error("result.Message should explain why DS contact was deferred")
	}
	if result.AutoBlessed {
		t.Error("result.AutoBlessed should be false in deferred path")
	}

	// PublishComment runs BEFORE the registration check, so the .md file
	// should now exist under content/pub.polis.core/comment/YYYYMMDD/.
	ts, err := time.Parse("2006-01-02T15:04:05Z", signed.Meta.Timestamp)
	if err != nil {
		t.Fatalf("parse signed timestamp: %v", err)
	}
	publishedPath := filepath.Join(
		dataDir, "content", "pub.polis.core", "comment",
		ts.Format("20060102"), signed.Meta.ID+".md",
	)
	if _, err := os.Stat(publishedPath); err != nil {
		t.Errorf("expected published comment at %s: %v", publishedPath, err)
	}

	// And the pending file should still be there (deferred path doesn't
	// move it to blessed/ since no auto-bless response from DS).
	pendingPath := filepath.Join(
		dataDir, ".polis", "bundles", "pub.polis.core", "comments",
		"pending", signed.Meta.ID+".md",
	)
	if _, err := os.Stat(pendingPath); err != nil {
		t.Errorf("expected pending comment to remain at %s: %v", pendingPath, err)
	}
}
