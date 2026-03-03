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
// It creates .well-known/polis, .polis/keys/id_ed25519, and content/pub.polis.core/post/ dirs.
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

	// Create content directory (for index.jsonl and posts)
	if err := os.MkdirAll(filepath.Join(dir, "content", "pub.polis.core", "post"), 0755); err != nil {
		t.Fatal(err)
	}

	return dir, privPEM
}

// createTestPost creates a post .md file and its .html sibling, and adds an
// entry to content/pub.polis.core/index.jsonl. Returns the relative post path (e.g.
// "content/pub.polis.core/post/20260201/my-post.md").
func createTestPost(t *testing.T, dir, dateDir, slug, title string) string {
	t.Helper()

	postDir := filepath.Join(dir, "content", "pub.polis.core", "post", dateDir)
	if err := os.MkdirAll(postDir, 0755); err != nil {
		t.Fatal(err)
	}

	relPath := filepath.Join("content", "pub.polis.core", "post", dateDir, slug+".md")

	// Write .md file
	mdContent := "---\ntitle: " + title + "\npublished: 2026-02-01T00:00:00Z\n---\n\n# " + title + "\n\nHello world.\n"
	if err := os.WriteFile(filepath.Join(dir, relPath), []byte(mdContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Write .html file (rendered output)
	htmlPath := filepath.Join(postDir, slug+".html")
	if err := os.WriteFile(htmlPath, []byte("<h1>"+title+"</h1>"), 0644); err != nil {
		t.Fatal(err)
	}

	// Add entry to public.jsonl
	if err := metadata.AppendPostToIndex(dir, relPath, title, "2026-02-01T00:00:00Z", "sha256:abc123"); err != nil {
		t.Fatal(err)
	}

	return relPath
}

func TestRunUnpublish_Success(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	// Save and restore globals
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	// Set env vars
	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	postPath := createTestPost(t, dir, "20260201", "my-post", "My Post")

	// Verify files exist before unpublish
	if _, err := os.Stat(filepath.Join(dir, postPath)); err != nil {
		t.Fatalf("post .md should exist before unpublish: %v", err)
	}
	htmlPath := strings.TrimSuffix(filepath.Join(dir, postPath), ".md") + ".html"
	if _, err := os.Stat(htmlPath); err != nil {
		t.Fatalf("post .html should exist before unpublish: %v", err)
	}

	// Run unpublish
	err := RunUnpublish(dir, postPath)
	if err != nil {
		t.Fatalf("RunUnpublish returned error: %v", err)
	}

	// Assert .md file is deleted
	if _, err := os.Stat(filepath.Join(dir, postPath)); !os.IsNotExist(err) {
		t.Error("expected .md file to be deleted after unpublish")
	}

	// Assert .html file is deleted
	if _, err := os.Stat(htmlPath); !os.IsNotExist(err) {
		t.Error("expected .html file to be deleted after unpublish")
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

func TestRunUnpublish_NoFile(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")
	t.Setenv("DISCOVERY_SERVICE_URL", "http://localhost:1/fake-ds")

	err := RunUnpublish(dir, "content/pub.polis.core/post/20260201/nonexistent.md")
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

	err := RunUnpublish(dir, "content/pub.polis.core/post/../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "..") {
		t.Errorf("expected error to mention '..', got: %v", err)
	}
}

func TestRunUnpublish_NotUnderPosts(t *testing.T) {
	dir, _ := setupUnpublishSite(t)

	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = false

	t.Setenv("POLIS_BASE_URL", "https://example.com")

	err := RunUnpublish(dir, "comments/foo.md")
	if err == nil {
		t.Fatal("expected error for path not under content/pub.polis.core/post/")
	}
	if !strings.Contains(err.Error(), "content/pub.polis.core/post/") {
		t.Errorf("expected error to mention 'content/pub.polis.core/post/', got: %v", err)
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

	// Verify both entries exist before unpublish
	entries, err := metadata.LoadPublicIndex(dir)
	if err != nil {
		t.Fatalf("failed to load public index: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in public.jsonl, got %d", len(entries))
	}

	// Unpublish only post A
	if err := RunUnpublish(dir, postA); err != nil {
		t.Fatalf("RunUnpublish returned error: %v", err)
	}

	// Post A files should be deleted
	if _, err := os.Stat(filepath.Join(dir, postA)); !os.IsNotExist(err) {
		t.Error("expected post-a .md file to be deleted")
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
	if entries[0].Title != "Post B" {
		t.Errorf("expected remaining entry title to be 'Post B', got %s", entries[0].Title)
	}
}
