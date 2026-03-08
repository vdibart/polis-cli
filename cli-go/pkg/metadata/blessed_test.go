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

func TestGetBlessedCommentsForPost_SourceContentPath(t *testing.T) {
	siteDir := t.TempDir()

	// Stored with mount path (plural "posts/")
	AddBlessedComment(siteDir, "posts/20260304/hello-world.md", BlessedComment{
		URL:     "https://vdibart.polis.pub/comments/20260304/david-hello-world-20260304.md",
		Version: "sha256:abc",
	})

	// Query with source content path (singular "post/") — this is what RenderFile passes
	comments, err := GetBlessedCommentsForPost(siteDir, "content/pub.polis.core/post/20260304/hello-world.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment with source content path query, got %d", len(comments))
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
		// Source content path (singular /post/) vs mount path (plural /posts/)
		{"posts/20260101/hello.md", "content/pub.polis.core/post/20260101/hello.md", true},
		{"content/pub.polis.core/post/20260101/hello.md", "posts/20260101/hello.md", true},
		{"posts/20260101/hello.md", "content/pub.polis.core/post/20260101/hello.html", true},
		// Negative cases
		{"posts/20260101/hello.md", "posts/20260101/world.md", false},
		{"posts/20260101/hello.md", "posts/20260102/hello.md", false},
	}

	for _, tt := range tests {
		got := MatchesPostPath(tt.stored, tt.query)
		if got != tt.want {
			t.Errorf("MatchesPostPath(%q, %q) = %v, want %v", tt.stored, tt.query, got, tt.want)
		}
	}
}
