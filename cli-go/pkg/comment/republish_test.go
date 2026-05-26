package comment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	verhist "github.com/vdibart/polis-cli/cli-go/pkg/version"
)

// setupPublishedComment signs and publishes a comment, returning the commentID,
// the content-relative path, and the original version string.
func setupPublishedComment(t *testing.T, dataDir, body string) (commentID, commentPath, origVersion string) {
	t.Helper()
	for _, d := range []string{
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending"),
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", "denied"),
		filepath.Join(dataDir, "content", "pub.polis.core", "comment"),
	} {
		os.MkdirAll(d, 0755)
	}

	privKey := generateTestKey(t)
	draft := &CommentDraft{
		InReplyTo: "https://alice.polis.pub/posts/20260101/hello.md",
		Content:   body,
	}
	signed, err := SignComment(dataDir, draft, "bob.polis.pub", "https://bob.polis.pub", privKey)
	if err != nil {
		t.Fatalf("SignComment: %v", err)
	}
	if err := PublishComment(dataDir, signed.Meta.ID); err != nil {
		t.Fatalf("PublishComment: %v", err)
	}
	ts, _ := time.Parse("2006-01-02T15:04:05Z", signed.Meta.Timestamp)
	dateDir := ts.Format("20060102")
	rel := filepath.ToSlash(filepath.Join("content", "pub.polis.core", "comment", dateDir, signed.Meta.ID+".md"))
	return signed.Meta.ID, rel, signed.Meta.CommentVersion
}

func TestRepublishComment_PreservesPublishedAndVersionsHistory(t *testing.T) {
	dataDir := t.TempDir()
	commentID, commentPath, origVersion := setupPublishedComment(t, dataDir, "Great post!")
	privKey := generateTestKey(t)

	origContent, _ := os.ReadFile(filepath.Join(dataDir, commentPath))
	origPublished := ParseFrontmatter(string(origContent))["published"]

	result, err := RepublishComment(dataDir, commentPath, "Edited body, much improved.", privKey, &DiscoveryConfig{})
	if err != nil {
		t.Fatalf("RepublishComment: %v", err)
	}

	if result.Version == origVersion {
		t.Errorf("version did not change: %s", result.Version)
	}
	if result.CommentID != commentID {
		t.Errorf("CommentID = %q, want %q", result.CommentID, commentID)
	}

	newContent, _ := os.ReadFile(filepath.Join(dataDir, commentPath))
	fm := ParseFrontmatter(string(newContent))
	if fm["published"] != origPublished {
		t.Errorf("published changed: %q -> %q", origPublished, fm["published"])
	}
	if fm["updated"] == "" {
		t.Error("updated timestamp not set")
	}
	if fm["current-version"] != result.Version {
		t.Errorf("current-version = %q, want %q", fm["current-version"], result.Version)
	}

	// version-history should now have two entries (base + republish).
	vh := publish.ExtractVersionHistory(string(newContent))
	if len(vh) != 2 {
		t.Fatalf("expected 2 version-history entries, got %d: %v", len(vh), vh)
	}

	// The prior version must be reconstructable from the side-car.
	fullPath := filepath.Join(dataDir, commentPath)
	reconstructed, err := verhist.ReconstructVersion(fullPath, origVersion, ".versions")
	if err != nil {
		t.Fatalf("ReconstructVersion(%s): %v", origVersion, err)
	}
	if !strings.Contains(reconstructed, "Great post!") {
		t.Errorf("reconstructed prior version = %q, want it to contain original body", reconstructed)
	}
}

func TestRepublishComment_AdvancesIndexPreservingShape(t *testing.T) {
	dataDir := t.TempDir()
	_, commentPath, _ := setupPublishedComment(t, dataDir, "Great post!")
	privKey := generateTestKey(t)

	result, err := RepublishComment(dataDir, commentPath, "Edited.", privKey, &DiscoveryConfig{})
	if err != nil {
		t.Fatalf("RepublishComment: %v", err)
	}

	entries, err := metadata.LoadPublicIndex(dataDir)
	if err != nil {
		t.Fatalf("LoadPublicIndex: %v", err)
	}
	var found *metadata.IndexEntry
	for i := range entries {
		if entries[i].Path == commentPath {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("comment not found in index at %s", commentPath)
	}
	if found.CurrentVersion != result.Version {
		t.Errorf("index current_version = %q, want %q", found.CurrentVersion, result.Version)
	}
	if found.Type != "comment" {
		t.Errorf("index type = %q, want comment", found.Type)
	}
	if found.InReplyTo == nil || found.InReplyTo.URL == "" {
		t.Error("index entry lost in_reply_to on republish")
	}
}

func TestRepublishComment_BlessedCarriesForward(t *testing.T) {
	dataDir := t.TempDir()
	commentID, commentPath, origVersion := setupPublishedComment(t, dataDir, "Great post!")
	privKey := generateTestKey(t)

	// Simulate a blessed comment: remove the pending copy and record the blessing
	// at the original version.
	postPath := "posts/20260101/hello.md"
	commentURL := "https://bob.polis.pub/comments/" + filepath.Base(filepath.Dir(commentPath)) + "/" + commentID + ".md"
	if err := metadata.AddBlessedComment(dataDir, postPath, metadata.BlessedComment{URL: commentURL, Version: origVersion}); err != nil {
		t.Fatalf("AddBlessedComment: %v", err)
	}
	os.Remove(filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusPending, commentID+".md"))

	result, err := RepublishComment(dataDir, commentPath, "Edited blessed comment.", privKey, &DiscoveryConfig{})
	if err != nil {
		t.Fatalf("RepublishComment: %v", err)
	}
	if result.BlessingState != StatusBlessed {
		t.Errorf("BlessingState = %q, want blessed", result.BlessingState)
	}
	if result.Rebeseeched {
		t.Error("blessed comment should not be re-beseeched")
	}

	// blessed.json must still pin the ORIGINAL version (carry forward / divergence).
	blessed, err := metadata.GetBlessedCommentsForPost(dataDir, postPath)
	if err != nil {
		t.Fatalf("GetBlessedCommentsForPost: %v", err)
	}
	if len(blessed) != 1 {
		t.Fatalf("expected 1 blessed comment, got %d", len(blessed))
	}
	if blessed[0].Version != origVersion {
		t.Errorf("blessed.json version = %q, want original %q (must not advance)", blessed[0].Version, origVersion)
	}
	if blessed[0].Version == result.Version {
		t.Error("blessed.json advanced to the new version — should carry forward the old one")
	}
}

func TestRepublishComment_PendingStaysPendingAndSyncs(t *testing.T) {
	dataDir := t.TempDir()
	commentID, commentPath, _ := setupPublishedComment(t, dataDir, "Great post!")
	privKey := generateTestKey(t)

	// Pending copy still present (PublishComment leaves it). DS not configured, so
	// no auto-bless transition can occur.
	result, err := RepublishComment(dataDir, commentPath, "Edited pending comment.", privKey, &DiscoveryConfig{})
	if err != nil {
		t.Fatalf("RepublishComment: %v", err)
	}
	if result.BlessingState != StatusPending {
		t.Errorf("BlessingState = %q, want pending", result.BlessingState)
	}

	// The private pending copy must be re-synced with the new content.
	pendingPath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusPending, commentID+".md")
	data, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatalf("read pending: %v", err)
	}
	if !strings.Contains(string(data), "Edited pending comment.") {
		t.Error("pending copy not synced with republished content")
	}
	if ParseFrontmatter(string(data))["current-version"] != result.Version {
		t.Error("pending copy current-version not synced")
	}
}

func TestIsEditedSinceBlessing(t *testing.T) {
	dataDir := t.TempDir()
	commentID, commentPath, origVersion := setupPublishedComment(t, dataDir, "Great post!")
	privKey := generateTestKey(t)

	dateDir := filepath.Base(filepath.Dir(commentPath))
	postPath := "posts/20260101/hello.md"
	commentURL := "https://bob.polis.pub/comments/" + dateDir + "/" + commentID + ".md"
	if err := metadata.AddBlessedComment(dataDir, postPath, metadata.BlessedComment{URL: commentURL, Version: origVersion}); err != nil {
		t.Fatalf("AddBlessedComment: %v", err)
	}
	os.Remove(filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusPending, commentID+".md"))

	// Freshly blessed, not yet edited.
	edited, err := IsEditedSinceBlessing(dataDir, commentPath)
	if err != nil {
		t.Fatalf("IsEditedSinceBlessing: %v", err)
	}
	if edited {
		t.Error("expected not edited before republish")
	}

	// Republish: blessed.json stays pinned to origVersion, the file advances.
	if _, err := RepublishComment(dataDir, commentPath, "Edited after blessing.", privKey, &DiscoveryConfig{}); err != nil {
		t.Fatalf("RepublishComment: %v", err)
	}

	edited, err = IsEditedSinceBlessing(dataDir, commentPath)
	if err != nil {
		t.Fatalf("IsEditedSinceBlessing: %v", err)
	}
	if !edited {
		t.Error("expected edited-since-blessing after republish")
	}
}

func TestIsEditedSinceBlessing_NotBlessed(t *testing.T) {
	dataDir := t.TempDir()
	_, commentPath, _ := setupPublishedComment(t, dataDir, "Great post!")

	edited, err := IsEditedSinceBlessing(dataDir, commentPath)
	if err != nil {
		t.Fatalf("IsEditedSinceBlessing: %v", err)
	}
	if edited {
		t.Error("unblessed comment should report not edited")
	}
}

func TestLocalBlessingState(t *testing.T) {
	dataDir := t.TempDir()
	base := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments")
	os.MkdirAll(filepath.Join(base, StatusPending), 0755)
	os.MkdirAll(filepath.Join(base, StatusDenied), 0755)

	if got := localBlessingState(dataDir, "nope"); got != StatusBlessed {
		t.Errorf("absent comment = %q, want blessed", got)
	}
	os.WriteFile(filepath.Join(base, StatusPending, "p.md"), []byte("x"), 0644)
	if got := localBlessingState(dataDir, "p"); got != StatusPending {
		t.Errorf("pending = %q, want pending", got)
	}
	os.WriteFile(filepath.Join(base, StatusDenied, "d.md"), []byte("x"), 0644)
	if got := localBlessingState(dataDir, "d"); got != StatusDenied {
		t.Errorf("denied = %q, want denied", got)
	}
}
