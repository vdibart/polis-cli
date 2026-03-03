package metadata

import (
	"testing"
)

func TestAppendToPublicIndex_NoDuplicates(t *testing.T) {
	siteDir := t.TempDir()

	entry := &IndexEntry{
		Type:           "post",
		Path:           "posts/20260101/hello.md",
		Title:          "Hello",
		Published:      "2026-01-01T00:00:00Z",
		CurrentVersion: "sha256:abc",
	}

	if err := AppendToPublicIndex(siteDir, entry); err != nil {
		t.Fatalf("first append failed: %v", err)
	}

	entries, _ := LoadPublicIndex(siteDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestAppendToPublicIndex_DuplicateUpdatesInPlace(t *testing.T) {
	siteDir := t.TempDir()

	entry1 := &IndexEntry{
		Type:           "post",
		Path:           "posts/20260101/hello.md",
		Title:          "Hello v1",
		Published:      "2026-01-01T00:00:00Z",
		CurrentVersion: "sha256:abc",
	}
	entry2 := &IndexEntry{
		Type:           "post",
		Path:           "posts/20260101/hello.md",
		Title:          "Hello v2",
		Published:      "2026-01-01T00:00:00Z",
		CurrentVersion: "sha256:def",
	}

	AppendToPublicIndex(siteDir, entry1)
	AppendToPublicIndex(siteDir, entry2)

	entries, _ := LoadPublicIndex(siteDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after dedup, got %d", len(entries))
	}
	if entries[0].Title != "Hello v2" {
		t.Errorf("expected updated title 'Hello v2', got %s", entries[0].Title)
	}
	if entries[0].CurrentVersion != "sha256:def" {
		t.Errorf("expected updated version, got %s", entries[0].CurrentVersion)
	}
}

func TestAppendToPublicIndex_DifferentPathsAppended(t *testing.T) {
	siteDir := t.TempDir()

	entry1 := &IndexEntry{
		Type: "post",
		Path: "posts/20260101/hello.md",
	}
	entry2 := &IndexEntry{
		Type: "post",
		Path: "posts/20260101/world.md",
	}

	AppendToPublicIndex(siteDir, entry1)
	AppendToPublicIndex(siteDir, entry2)

	entries, _ := LoadPublicIndex(siteDir)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestAppendCommentToIndex_Dedup(t *testing.T) {
	siteDir := t.TempDir()

	path := "comments/20260101/comment-id.md"

	AppendCommentToIndex(siteDir, path, "Comment v1", "2026-01-01T00:00:00Z", "sha256:aaa", "https://example.com/posts/hello.md")
	AppendCommentToIndex(siteDir, path, "Comment v2", "2026-01-01T00:00:00Z", "sha256:bbb", "https://example.com/posts/hello.md")

	entries, _ := LoadPublicIndex(siteDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after comment dedup, got %d", len(entries))
	}
	if entries[0].Title != "Comment v2" {
		t.Errorf("expected updated title, got %s", entries[0].Title)
	}
}
