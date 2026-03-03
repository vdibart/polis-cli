package metadata

import (
	"testing"
)

func TestGetBlessedCommentsForPost_ExactMatch(t *testing.T) {
	siteDir := t.TempDir()

	AddBlessedComment(siteDir, "posts/20260101/hello.md", BlessedComment{
		URL:     "https://bob.polis.pub/comments/20260102/re-hello.md",
		Version: "sha256:abc",
	})

	comments, err := GetBlessedCommentsForPost(siteDir, "posts/20260101/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
}

func TestGetBlessedCommentsForPost_ExtensionSwap(t *testing.T) {
	siteDir := t.TempDir()

	// Stored with .md extension
	AddBlessedComment(siteDir, "posts/20260101/hello.md", BlessedComment{
		URL: "https://bob.polis.pub/comments/20260102/re-hello.md",
	})

	// Query with .html extension
	comments, err := GetBlessedCommentsForPost(siteDir, "posts/20260101/hello.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment with .html query, got %d", len(comments))
	}
}

func TestGetBlessedCommentsForPost_FullURLMatch(t *testing.T) {
	siteDir := t.TempDir()

	// Stored as full URL
	AddBlessedComment(siteDir, "https://alice.polis.pub/posts/20260101/hello.md", BlessedComment{
		URL: "https://bob.polis.pub/comments/20260102/re-hello.md",
	})

	// Query with relative path
	comments, err := GetBlessedCommentsForPost(siteDir, "posts/20260101/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment with URL-to-path match, got %d", len(comments))
	}
}

func TestMatchesPostPath(t *testing.T) {
	tests := []struct {
		stored, query string
		want          bool
	}{
		{"posts/20260101/hello.md", "posts/20260101/hello.md", true},
		{"posts/20260101/hello.md", "posts/20260101/hello.html", true},
		{"posts/20260101/hello.html", "posts/20260101/hello.md", true},
		{"https://alice.polis.pub/posts/20260101/hello.md", "posts/20260101/hello.md", true},
		{"posts/20260101/hello.md", "https://alice.polis.pub/posts/20260101/hello.md", true},
		{"posts/20260101/hello.md", "posts/20260101/world.md", false},
		{"posts/20260101/hello.md", "posts/20260102/hello.md", false},
	}

	for _, tt := range tests {
		got := matchesPostPath(tt.stored, tt.query)
		if got != tt.want {
			t.Errorf("matchesPostPath(%q, %q) = %v, want %v", tt.stored, tt.query, got, tt.want)
		}
	}
}
