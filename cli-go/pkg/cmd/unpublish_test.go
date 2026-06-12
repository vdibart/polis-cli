package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// setupUnpublishSite creates a minimal polis site fixture in a temp directory.
// It creates .well-known/polis, .polis/keys/id_ed25519, and content dirs.
// Returns the site directory and the private key PEM bytes.
func setupUnpublishSite(t *testing.T) (string, []byte) {
	t.Helper()
	dir := t.TempDir()

	// Create .well-known/polis
	wellKnownDir := filepath.Join(dir, ".well-known")
	if err := os.MkdirAll(wellKnownDir, 0755); err != nil {
		t.Fatal(err)
	}
	wk := map[string]interface{}{
		"base_url":   "https://example.com",
		"public_key": "ssh-ed25519 AAAA_PLACEHOLDER test@example.com",
		"site_title": "Test Site",
	}
	wkData, _ := json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(filepath.Join(wellKnownDir, "polis"), wkData, 0644); err != nil {
		t.Fatal(err)
	}

	// Generate a real keypair for signing
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	// Write private key
	keysDir := filepath.Join(dir, ".polis", "keys")
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, "id_ed25519"), privPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, "id_ed25519.pub"), pubSSH, 0644); err != nil {
		t.Fatal(err)
	}

	// Create content directories
	if err := os.MkdirAll(filepath.Join(dir, "content", "pub.polis.core", "post"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "content", "pub.polis.core", "comment"), 0755); err != nil {
		t.Fatal(err)
	}

	return dir, privPEM
}

// createTestPost creates a post .md file with frontmatter and its .html sibling,
// adds an entry to index.jsonl, and creates a .versions file.
// Returns the relative post path.
func createTestPost(t *testing.T, dir, dateDir, slug, title string) string {
	t.Helper()

	postDir := filepath.Join(dir, "content", "pub.polis.core", "post", dateDir)
	if err := os.MkdirAll(postDir, 0755); err != nil {
		t.Fatal(err)
	}

	relPath := filepath.Join("content", "pub.polis.core", "post", dateDir, slug+".md")

	// Write .md file with full frontmatter
	mdContent := "---\ntitle: " + title + "\npublished: 2026-02-01T00:00:00Z\ngenerator: polis-cli-go/0.59.0\ncurrent-version: sha256:abc123\nversion-history:\n  - sha256:abc123 (2026-02-01T00:00:00Z)\nsignature: AAAA_PLACEHOLDER\n---\n\nHello world.\n"
	if err := os.WriteFile(filepath.Join(dir, relPath), []byte(mdContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Write .html file (rendered output)
	htmlPath := filepath.Join(postDir, slug+".html")
	if err := os.WriteFile(htmlPath, []byte("<h1>"+title+"</h1>"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create .versions file
	versionsDir := filepath.Join(postDir, ".versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		t.Fatal(err)
	}
	versionsFile := filepath.Join(versionsDir, slug+".md")
	if err := os.WriteFile(versionsFile, []byte("# VERSION HISTORY\nsha256:abc123"), 0644); err != nil {
		t.Fatal(err)
	}

	// Add entry to public.jsonl
	if err := metadata.AppendPostToIndex(dir, relPath, title, "2026-02-01T00:00:00Z", "sha256:abc123"); err != nil {
		t.Fatal(err)
	}

	return relPath
}

// createTestComment creates a blessed comment .md file with frontmatter.
func createTestComment(t *testing.T, dir, dateDir, commentID, title string) string {
	t.Helper()

	commentDir := filepath.Join(dir, "content", "pub.polis.core", "comment", dateDir)
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatal(err)
	}

	relPath := filepath.Join("content", "pub.polis.core", "comment", dateDir, commentID+".md")

	mdContent := "---\ntitle: " + title + "\ntype: comment\npublished: 2026-02-01T00:00:00Z\nauthor: test.example.com\ngenerator: polis-cli-go/0.59.0\nin-reply-to:\n  url: https://target.com/posts/post.md\n  root-post: https://target.com/posts/post.md\ncurrent-version: sha256:def456\nversion-history:\n  - sha256:def456 (2026-02-01T00:00:00Z)\nsignature: BBBB_PLACEHOLDER\n---\n\nThis is a comment.\n"
	if err := os.WriteFile(filepath.Join(dir, relPath), []byte(mdContent), 0644); err != nil {
		t.Fatal(err)
	}

	return relPath
}

func TestRunUnpublish_PostSuccess(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	postPath := createTestPost(t, dir, "20260201", "my-post", "My Post")

	// Run unpublish with skipConfirm=true
	err := RunUnpublish(dir, postPath, true)
	if err != nil {
		t.Fatalf("RunUnpublish returned error: %v", err)
	}

	// Assert original .md file is deleted
	if _, err := os.Stat(filepath.Join(dir, postPath)); !os.IsNotExist(err) {
		t.Error("expected .md file to be deleted after unpublish")
	}

	// Assert .html file is deleted
	htmlPath := strings.TrimSuffix(filepath.Join(dir, postPath), ".md") + ".html"
	if _, err := os.Stat(htmlPath); !os.IsNotExist(err) {
		t.Error("expected .html file to be deleted after unpublish")
	}

	// Assert .versions file is deleted
	versionsFile := filepath.Join(dir, "content", "pub.polis.core", "post", "20260201", ".versions", "my-post.md")
	if _, err := os.Stat(versionsFile); !os.IsNotExist(err) {
		t.Error("expected .versions file to be deleted after unpublish")
	}

	// Assert draft was created with stripped frontmatter
	draftPath := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "my-post.md")
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("expected draft to be created: %v", err)
	}
	draftStr := string(draftContent)

	// Draft should start with # Title
	if !strings.HasPrefix(draftStr, "# My Post") {
		t.Errorf("draft should start with '# My Post', got: %s", draftStr[:min(50, len(draftStr))])
	}

	// Draft should NOT contain frontmatter markers
	if strings.Contains(draftStr, "---") {
		t.Error("draft should not contain frontmatter markers '---'")
	}

	// Draft should NOT contain signature or version fields
	if strings.Contains(draftStr, "signature:") {
		t.Error("draft should not contain 'signature:' field")
	}
	if strings.Contains(draftStr, "current-version:") {
		t.Error("draft should not contain 'current-version:' field")
	}
	if strings.Contains(draftStr, "version-history:") {
		t.Error("draft should not contain 'version-history:' field")
	}

	// Draft should contain body content
	if !strings.Contains(draftStr, "Hello world") {
		t.Error("draft should contain body content 'Hello world'")
	}

	// Assert public.jsonl entry is removed
	entries, err := metadata.LoadPublicIndex(dir)
	if err != nil {
		t.Fatalf("failed to load public index: %v", err)
	}
	for _, e := range entries {
		if e.Path == postPath {
			t.Error("expected post entry to be removed from public.jsonl")
		}
	}
}

func TestRunUnpublish_CommentSuccess(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	commentPath := createTestComment(t, dir, "20260201", "comment-a", "Re: Some Post")

	// Run unpublish
	err := RunUnpublish(dir, commentPath, true)
	if err != nil {
		t.Fatalf("RunUnpublish returned error: %v", err)
	}

	// Assert original file is deleted
	if _, err := os.Stat(filepath.Join(dir, commentPath)); !os.IsNotExist(err) {
		t.Error("expected comment .md file to be deleted after unpublish")
	}

	// Assert draft was created
	draftPath := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments", "drafts", "comment-a.md")
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("expected comment draft to be created: %v", err)
	}
	draftStr := string(draftContent)

	// Draft should contain title
	if !strings.Contains(draftStr, "Re: Some Post") {
		t.Error("draft should contain comment title")
	}

	// Draft should NOT contain signature
	if strings.Contains(draftStr, "signature:") {
		t.Error("draft should not contain 'signature:' field")
	}

	// Draft should contain body
	if !strings.Contains(draftStr, "This is a comment") {
		t.Error("draft should contain body content")
	}
}

func TestRunUnpublish_NoFile(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	err := RunUnpublish(dir, "content/pub.polis.core/post/20260201/nonexistent.md", true)
	if err == nil {
		t.Fatal("expected error for nonexistent post")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error to contain 'not found', got: %v", err)
	}
}

func TestRunUnpublish_InvalidPath(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")

	err := RunUnpublish(dir, "content/pub.polis.core/post/../../../etc/passwd", true)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "..") {
		t.Errorf("expected error to mention '..', got: %v", err)
	}
}

func TestRunUnpublish_NotUnderContentDir(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")

	err := RunUnpublish(dir, "random/foo.md", true)
	if err == nil {
		t.Fatal("expected error for path not under content/pub.polis.core/post/ or comment/")
	}
	if !strings.Contains(err.Error(), "content/pub.polis.core/post/") {
		t.Errorf("expected error to mention valid paths, got: %v", err)
	}
}

func TestRunUnpublish_PreservesOtherPosts(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	// Create two posts
	postA := createTestPost(t, dir, "20260201", "post-a", "Post A")
	postB := createTestPost(t, dir, "20260201", "post-b", "Post B")

	entries, err := metadata.LoadPublicIndex(dir)
	if err != nil {
		t.Fatalf("failed to load public index: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in public.jsonl, got %d", len(entries))
	}

	// Unpublish only post A
	if err := RunUnpublish(dir, postA, true); err != nil {
		t.Fatalf("RunUnpublish returned error: %v", err)
	}

	// Post A files should be deleted, draft should exist
	if _, err := os.Stat(filepath.Join(dir, postA)); !os.IsNotExist(err) {
		t.Error("expected post-a .md file to be deleted")
	}
	draftA := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", "post-a.md")
	if _, err := os.Stat(draftA); err != nil {
		t.Errorf("expected post-a draft to exist: %v", err)
	}

	// Post B files should still exist
	if _, err := os.Stat(filepath.Join(dir, postB)); err != nil {
		t.Errorf("post-b .md file should still exist: %v", err)
	}
	htmlB := strings.TrimSuffix(filepath.Join(dir, postB), ".md") + ".html"
	if _, err := os.Stat(htmlB); err != nil {
		t.Errorf("post-b .html file should still exist: %v", err)
	}

	// Only post B should remain in public.jsonl
	entries, err = metadata.LoadPublicIndex(dir)
	if err != nil {
		t.Fatalf("failed to load public index after unpublish: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry in public.jsonl after unpublish, got %d", len(entries))
	}
	if entries[0].Path != postB {
		t.Errorf("expected remaining entry to be %s, got %s", postB, entries[0].Path)
	}
}

// TestInferContentTypeFromURL locks the URL→type mapping used by the
// discovery-only `polis unpublish --url` path (removing stale/duplicate rows).
func TestInferContentTypeFromURL(t *testing.T) {
	cases := map[string]string{
		"https://x.polis.pub/comments/20260304/david-hello-world-20260304.md":                      "pub.polis.comment",
		"https://x.polis.pub/content/pub.polis.core/comment/20260304/david-hello-world-20260304.md": "pub.polis.comment",
		"https://x.polis.pub/posts/20260317/the-simplest-architecture.md":                           "pub.polis.post",
		"https://x.polis.pub/content/pub.polis.core/post/20260317/the-simplest-architecture.md":      "pub.polis.post",
		"https://x.polis.pub/tags/follow-up/abc":                                                     "", // not post/comment
	}
	for url, want := range cases {
		if got := inferContentTypeFromURL(url); got != want {
			t.Errorf("inferContentTypeFromURL(%q) = %q, want %q", url, got, want)
		}
	}
}
