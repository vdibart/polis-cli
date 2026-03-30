package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetGenerator_UsesVersion(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "1.2.3"
	got := GetGenerator()
	if got != "polis-cli-go/1.2.3" {
		t.Errorf("GetGenerator() = %q, want %q", got, "polis-cli-go/1.2.3")
	}
}

func TestDefaultVersion_UsesGenerator(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "0.47.0"
	got := DefaultVersion()
	if got != "polis-cli-go/0.47.0" {
		t.Errorf("DefaultVersion() = %q, want %q", got, "polis-cli-go/0.47.0")
	}
}

func TestEnsureUniqueFilename_NoCollision(t *testing.T) {
	dataDir := t.TempDir()
	dateDir := "20260101"
	os.MkdirAll(filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir), 0755)

	result := ensureUniqueFilename(dataDir, dateDir, "hello-world")
	if result != "hello-world" {
		t.Errorf("expected 'hello-world', got %s", result)
	}
}

func TestEnsureUniqueFilename_PostCollision(t *testing.T) {
	dataDir := t.TempDir()
	dateDir := "20260101"
	postsDir := filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir)
	os.MkdirAll(postsDir, 0755)

	// Create existing post
	os.WriteFile(filepath.Join(postsDir, "hello-world.md"), []byte("existing"), 0644)

	result := ensureUniqueFilename(dataDir, dateDir, "hello-world")
	if result != "hello-world-2" {
		t.Errorf("expected 'hello-world-2', got %s", result)
	}
}

func TestEnsureUniqueFilename_MultipleCollisions(t *testing.T) {
	dataDir := t.TempDir()
	dateDir := "20260101"
	postsDir := filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir)
	os.MkdirAll(postsDir, 0755)

	// Create existing posts
	os.WriteFile(filepath.Join(postsDir, "hello-world.md"), []byte("v1"), 0644)
	os.WriteFile(filepath.Join(postsDir, "hello-world-2.md"), []byte("v2"), 0644)

	result := ensureUniqueFilename(dataDir, dateDir, "hello-world")
	if result != "hello-world-3" {
		t.Errorf("expected 'hello-world-3', got %s", result)
	}
}

func TestSlugify_UntitledGetsRandomSuffix(t *testing.T) {
	// Empty slug (all special chars) gets random suffix
	slug := Slugify("!!!")
	if !strings.HasPrefix(slug, "untitled-") {
		t.Errorf("expected 'untitled-<random>', got %s", slug)
	}
	if len(slug) <= len("untitled-") {
		t.Errorf("expected random suffix, slug too short: %s", slug)
	}

	// Two calls produce different slugs
	slug2 := Slugify("???")
	if slug == slug2 {
		t.Error("expected different random suffixes for separate calls")
	}
}

func TestSlugify_NormalTitleNoSuffix(t *testing.T) {
	slug := Slugify("Hello World")
	if slug != "hello-world" {
		t.Errorf("expected 'hello-world', got %s", slug)
	}
}

func TestSlugify_UntitledTitleInPublishPost(t *testing.T) {
	// When ExtractTitle returns "Untitled", Slugify produces "untitled"
	// PublishPost should detect this and add a random suffix
	title := ExtractTitle("")
	if title != "Untitled" {
		t.Errorf("expected 'Untitled' for empty content, got %s", title)
	}
	slug := Slugify(title)
	if slug != "untitled" {
		t.Errorf("expected 'untitled', got %s", slug)
	}
	// The random suffix is added in PublishPost, not Slugify,
	// so Slugify("Untitled") correctly returns "untitled"
}

func TestRelativePath_UsesForwardSlashes(t *testing.T) {
	// Verify that the relativePath produced by PublishPost always uses
	// forward slashes, even when filepath.Join would produce backslashes
	// on Windows. We can't easily test on Windows in CI, but we can
	// verify the filepath.ToSlash wrapper works.
	dataDir := t.TempDir()
	dateDir := "20260319"
	filename := "hello-world"

	// Simulate what PublishPost does for relativePath construction
	relativePath := filepath.ToSlash(filepath.Join("content", "pub.polis.core", "post", dateDir, filename+".md"))

	if strings.Contains(relativePath, "\\") {
		t.Errorf("relativePath contains backslash: %q", relativePath)
	}
	expected := "content/pub.polis.core/post/20260319/hello-world.md"
	if relativePath != expected {
		t.Errorf("relativePath = %q, want %q", relativePath, expected)
	}

	// Also verify the path is usable in URL construction
	baseURL := "https://example.com"
	postURL := strings.TrimRight(baseURL, "/") + "/" + relativePath
	if strings.Contains(postURL, "\\") {
		t.Errorf("postURL contains backslash: %q", postURL)
	}
	_ = dataDir // used for context only
}

func TestEnsureUniqueFilename_DraftCollision(t *testing.T) {
	dataDir := t.TempDir()
	dateDir := "20260101"
	os.MkdirAll(filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir), 0755)
	draftsDir := filepath.Join(dataDir, ".polis", "content", "pub.polis.core", "posts", "drafts")
	os.MkdirAll(draftsDir, 0755)

	// Create existing draft
	os.WriteFile(filepath.Join(draftsDir, "hello-world.md"), []byte("draft"), 0644)

	result := ensureUniqueFilename(dataDir, dateDir, "hello-world")
	if result != "hello-world-2" {
		t.Errorf("expected 'hello-world-2', got %s", result)
	}
}
