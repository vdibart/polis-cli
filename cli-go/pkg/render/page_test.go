package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewPageRenderer(t *testing.T) {
	// Create temp site structure
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir:       tempDir,
		BaseURL:       "https://example.com",
		RenderMarkers: false,
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	if renderer == nil {
		t.Fatal("NewPageRenderer returned nil")
	}
}

func TestRenderFile_Post(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create a test post
	postsDir := filepath.Join(tempDir, "posts")
	os.MkdirAll(postsDir, 0755)

	postContent := `---
title: Test Post
published: 2026-01-15T12:00:00Z
---
This is the **test** content.
`
	postPath := filepath.Join(postsDir, "test-post.md")
	os.WriteFile(postPath, []byte(postContent), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir:       tempDir,
		BaseURL:       "https://example.com",
		RenderMarkers: false,
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html, rendered, err := renderer.RenderFile("posts/test-post.md", "post", true)
	if err != nil {
		t.Fatalf("RenderFile failed: %v", err)
	}

	if !rendered {
		t.Error("Expected file to be rendered")
	}

	if !strings.Contains(html, "Test Post") {
		t.Errorf("Expected HTML to contain title, got: %s", html)
	}

	if !strings.Contains(html, "<strong>test</strong>") {
		t.Errorf("Expected HTML to contain bold text, got: %s", html)
	}

	// Verify HTML file was written
	htmlPath := filepath.Join(postsDir, "test-post.html")
	if _, err := os.Stat(htmlPath); os.IsNotExist(err) {
		t.Error("Expected HTML file to be created")
	}
}

func TestRenderFile_Skip(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	postsDir := filepath.Join(tempDir, "posts")
	os.MkdirAll(postsDir, 0755)

	postContent := `---
title: Skip Test
---
Content.
`
	postPath := filepath.Join(postsDir, "skip-test.md")
	os.WriteFile(postPath, []byte(postContent), 0644)

	renderer, _ := NewPageRenderer(PageConfig{
		DataDir: tempDir,
	})

	// First render with force
	_, rendered, err := renderer.RenderFile("posts/skip-test.md", "post", true)
	if err != nil {
		t.Fatalf("First render failed: %v", err)
	}
	if !rendered {
		t.Error("First render should render")
	}

	// Touch the HTML file to make it newer than MD
	htmlPath := filepath.Join(postsDir, "skip-test.html")
	futureTime := time.Now().Add(time.Second)
	os.Chtimes(htmlPath, futureTime, futureTime)

	// Second render without force should skip (HTML is newer)
	_, rendered, err = renderer.RenderFile("posts/skip-test.md", "post", false)
	if err != nil {
		t.Fatalf("Second render failed: %v", err)
	}
	if rendered {
		t.Error("Second render should skip (HTML is up to date)")
	}
}

func TestRenderIndex(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create metadata/public.jsonl with some entries
	metadataDir := filepath.Join(tempDir, "metadata")
	os.MkdirAll(metadataDir, 0755)
	publicJSONL := `{"path":"posts/hello.md","title":"Hello World","published":"2026-01-15T12:00:00Z","type":"post"}
{"path":"posts/goodbye.md","title":"Goodbye World","published":"2026-01-16T12:00:00Z","type":"post"}
`
	os.WriteFile(filepath.Join(metadataDir, "public.jsonl"), []byte(publicJSONL), 0644)

	renderer, _ := NewPageRenderer(PageConfig{
		DataDir:       tempDir,
		BaseURL:       "https://example.com",
		RenderMarkers: false,
	})

	err := renderer.RenderIndex()
	if err != nil {
		t.Fatalf("RenderIndex failed: %v", err)
	}

	// Verify index.html was created
	indexPath := filepath.Join(tempDir, "index.html")
	content, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read index.html: %v", err)
	}

	// The index template iterates over posts, check the site title is there
	if !strings.Contains(string(content), "Test Site") {
		t.Errorf("Expected index to contain site title, got: %s", content)
	}

	// Check that posts section was processed (URL should be converted to .html)
	if !strings.Contains(string(content), "posts/hello.html") {
		t.Errorf("Expected index to contain post URL, got: %s", content)
	}
}

func TestRenderAll(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create posts directory with a post
	postsDir := filepath.Join(tempDir, "posts")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "post1.md"), []byte("---\ntitle: Post 1\n---\nContent 1"), 0644)

	// Create metadata/public.jsonl
	metadataDir := filepath.Join(tempDir, "metadata")
	os.MkdirAll(metadataDir, 0755)
	os.WriteFile(filepath.Join(metadataDir, "public.jsonl"), []byte(`{"path":"posts/post1.md","title":"Post 1","type":"post"}`), 0644)

	renderer, _ := NewPageRenderer(PageConfig{
		DataDir: tempDir,
	})

	stats, err := renderer.RenderAll(true)
	if err != nil {
		t.Fatalf("RenderAll failed: %v", err)
	}

	if stats.PostsRendered != 1 {
		t.Errorf("Expected 1 post rendered, got %d", stats.PostsRendered)
	}

	if !stats.IndexGenerated {
		t.Error("Expected index to be generated")
	}
}

func TestRenderIndex_LimitsRecentPosts(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create metadata/public.jsonl with 15 posts
	metadataDir := filepath.Join(tempDir, "metadata")
	os.MkdirAll(metadataDir, 0755)

	var entries string
	for i := 1; i <= 15; i++ {
		entries += fmt.Sprintf(`{"path":"posts/post-%02d.md","title":"Post %d","published":"2026-01-%02dT12:00:00Z","type":"post"}`, i, i, i) + "\n"
	}
	os.WriteFile(filepath.Join(metadataDir, "public.jsonl"), []byte(entries), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir:       tempDir,
		BaseURL:       "https://example.com",
		RenderMarkers: false,
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	err = renderer.RenderIndex()
	if err != nil {
		t.Fatalf("RenderIndex failed: %v", err)
	}

	// Read the generated index.html
	content, err := os.ReadFile(filepath.Join(tempDir, "index.html"))
	if err != nil {
		t.Fatalf("Failed to read index.html: %v", err)
	}

	html := string(content)

	// Should contain only 10 post items (the limit)
	postCount := strings.Count(html, `class="post-item"`)
	if postCount != 10 {
		t.Errorf("Expected 10 post items on index page, got %d", postCount)
	}

	// Should contain "View all 15 posts" link
	if !strings.Contains(html, "View all 15 posts") {
		t.Errorf("Expected 'View all 15 posts' link, got: %s", html)
	}
}

func TestRenderIndex_NoViewAllWhenFewPosts(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create metadata/public.jsonl with only 5 posts (under the limit)
	metadataDir := filepath.Join(tempDir, "metadata")
	os.MkdirAll(metadataDir, 0755)

	var entries string
	for i := 1; i <= 5; i++ {
		entries += fmt.Sprintf(`{"path":"posts/post-%02d.md","title":"Post %d","published":"2026-01-%02dT12:00:00Z","type":"post"}`, i, i, i) + "\n"
	}
	os.WriteFile(filepath.Join(metadataDir, "public.jsonl"), []byte(entries), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir:       tempDir,
		BaseURL:       "https://example.com",
		RenderMarkers: false,
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	err = renderer.RenderIndex()
	if err != nil {
		t.Fatalf("RenderIndex failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tempDir, "index.html"))
	if err != nil {
		t.Fatalf("Failed to read index.html: %v", err)
	}

	html := string(content)

	// Should contain all 5 posts
	postCount := strings.Count(html, `class="post-item"`)
	if postCount != 5 {
		t.Errorf("Expected 5 post items on index page, got %d", postCount)
	}

	// Should NOT contain "View all" link when posts <= 10
	if strings.Contains(html, "View all") {
		t.Errorf("Expected no 'View all' link when posts <= 10, got: %s", html)
	}
}

func TestRenderArchive(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create metadata/public.jsonl with 15 posts
	metadataDir := filepath.Join(tempDir, "metadata")
	os.MkdirAll(metadataDir, 0755)

	var entries string
	for i := 1; i <= 15; i++ {
		entries += fmt.Sprintf(`{"path":"posts/post-%02d.md","title":"Post %d","published":"2026-01-%02dT12:00:00Z","type":"post"}`, i, i, i) + "\n"
	}
	os.WriteFile(filepath.Join(metadataDir, "public.jsonl"), []byte(entries), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir:       tempDir,
		BaseURL:       "https://example.com",
		RenderMarkers: false,
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	err = renderer.RenderArchive()
	if err != nil {
		t.Fatalf("RenderArchive failed: %v", err)
	}

	// Read the generated posts/index.html
	content, err := os.ReadFile(filepath.Join(tempDir, "posts", "index.html"))
	if err != nil {
		t.Fatalf("Failed to read posts/index.html: %v", err)
	}

	html := string(content)

	// Should contain ALL 15 posts (no limit on archive)
	postCount := strings.Count(html, `class="post-item"`)
	if postCount != 15 {
		t.Errorf("Expected 15 post items on archive page, got %d", postCount)
	}

	// Should link back to home
	if !strings.Contains(html, `../index.html`) {
		t.Errorf("Expected back link to ../index.html")
	}

	// Should reference ../styles.css
	if !strings.Contains(html, `All Posts`) {
		t.Errorf("Expected 'All Posts' title")
	}
}

func TestRenderArchive_NoTemplate(t *testing.T) {
	tempDir := t.TempDir()

	// Create a minimal test site WITHOUT posts.html
	wellKnownDir := filepath.Join(tempDir, ".well-known")
	os.MkdirAll(wellKnownDir, 0755)
	os.WriteFile(filepath.Join(wellKnownDir, "polis"), []byte(`{
		"base_url": "https://example.com",
		"site_title": "Test Site",
		"author_name": "Test Author"
	}`), 0644)

	themesDir := filepath.Join(tempDir, ".polis", "themes", "turbo")
	os.MkdirAll(themesDir, 0755)
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte("<html>{{title}}</html>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "comment.html"), []byte("<html>{{title}}</html>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "comment-inline.html"), []byte("<div>{{content}}</div>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "index.html"), []byte("<html>{{site_title}}</html>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "turbo.css"), []byte("/* test css */"), 0644)
	// NO posts.html

	metadataDir := filepath.Join(tempDir, "metadata")
	os.MkdirAll(metadataDir, 0755)
	os.WriteFile(filepath.Join(metadataDir, "manifest.json"), []byte(`{"active_theme":"turbo"}`), 0644)
	os.WriteFile(filepath.Join(metadataDir, "public.jsonl"), []byte(`{"path":"posts/test.md","title":"Test","type":"post"}`), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	// RenderArchive should not error when posts.html doesn't exist
	err = renderer.RenderArchive()
	if err != nil {
		t.Fatalf("RenderArchive should not error when template missing: %v", err)
	}

	// posts/index.html should NOT be created
	if _, err := os.Stat(filepath.Join(tempDir, "posts", "index.html")); !os.IsNotExist(err) {
		t.Error("posts/index.html should not exist when theme lacks posts.html")
	}
}

func TestRenderFile_AuthorDomainAndPageType(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Update post template to include widget variables
	themesDir := filepath.Join(tempDir, ".polis", "themes", "turbo")
	postTmpl := `<!DOCTYPE html>
<html>
<head><title>{{title}}</title></head>
<body>
<div id="polis-widget" data-author="{{author_domain}}" data-page-type="{{page_type}}"></div>
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte(postTmpl), 0644)

	postsDir := filepath.Join(tempDir, "posts")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "test.md"), []byte("---\ntitle: Widget Test\npublished: 2026-01-15T12:00:00Z\n---\nHello"), 0644)

	// author_domain is derived from BaseURL config, not from .well-known/polis
	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://alice.polis.pub",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html, rendered, err := renderer.RenderFile("posts/test.md", "post", true)
	if err != nil {
		t.Fatalf("RenderFile failed: %v", err)
	}
	if !rendered {
		t.Fatal("Expected file to be rendered")
	}

	if !strings.Contains(html, `data-author="alice.polis.pub"`) {
		t.Errorf("Expected author_domain in rendered HTML, got: %s", html)
	}
	if !strings.Contains(html, `data-page-type="post"`) {
		t.Errorf("Expected page_type=post in rendered HTML, got: %s", html)
	}
}

func TestRenderFile_AuthorDomainFromBaseURLConfig(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Template that exposes author_domain
	themesDir := filepath.Join(tempDir, ".polis", "themes", "turbo")
	postTmpl := `<div data-author="{{author_domain}}">{{title}}</div>`
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte(postTmpl), 0644)

	postsDir := filepath.Join(tempDir, "posts")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "t.md"), []byte("---\ntitle: Test\n---\nContent"), 0644)

	// author_domain is derived from BaseURL config
	renderer, _ := NewPageRenderer(PageConfig{DataDir: tempDir, BaseURL: "https://bob.example.com"})

	html, _, err := renderer.RenderFile("posts/t.md", "post", true)
	if err != nil {
		t.Fatalf("RenderFile failed: %v", err)
	}

	if !strings.Contains(html, `data-author="bob.example.com"`) {
		t.Errorf("Expected author_domain from BaseURL config, got: %s", html)
	}
}

func TestRenderFile_AuthorDomainEmptyWithoutBaseURL(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Template that exposes author_domain
	themesDir := filepath.Join(tempDir, ".polis", "themes", "turbo")
	postTmpl := `<div data-author="{{author_domain}}">{{title}}</div>`
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte(postTmpl), 0644)

	postsDir := filepath.Join(tempDir, "posts")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "t.md"), []byte("---\ntitle: Test\n---\nContent"), 0644)

	// No BaseURL — author_domain should be empty (graceful degradation)
	renderer, _ := NewPageRenderer(PageConfig{DataDir: tempDir})

	html, _, err := renderer.RenderFile("posts/t.md", "post", true)
	if err != nil {
		t.Fatalf("RenderFile failed: %v", err)
	}

	if !strings.Contains(html, `data-author=""`) {
		t.Errorf("Expected empty author_domain without BaseURL, got: %s", html)
	}
}

func TestRenderIndex_ExcerptPopulated(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Update index template to show excerpt
	themesDir := filepath.Join(tempDir, ".polis", "themes", "turbo")
	idxTmpl := `<!DOCTYPE html>
<html>
<head><title>{{site_title}}</title></head>
<body>
{{#recent_posts}}<div class="post-item"><a href="{{url}}">{{title}}</a><p class="excerpt">{{excerpt}}</p></div>{{/recent_posts}}
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "index.html"), []byte(idxTmpl), 0644)

	// Create a real markdown post file
	postsDir := filepath.Join(tempDir, "posts")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "hello.md"), []byte("---\ntitle: Hello World\npublished: 2026-01-15T12:00:00Z\n---\nThis is the body of my post about **important things** in the world."), 0644)

	// Create metadata/public.jsonl
	metadataDir := filepath.Join(tempDir, "metadata")
	os.WriteFile(filepath.Join(metadataDir, "public.jsonl"), []byte(`{"path":"posts/hello.md","title":"Hello World","published":"2026-01-15T12:00:00Z","type":"post"}`+"\n"), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	err = renderer.RenderIndex()
	if err != nil {
		t.Fatalf("RenderIndex failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tempDir, "index.html"))
	if err != nil {
		t.Fatalf("Failed to read index.html: %v", err)
	}

	html := string(content)

	// Excerpt should contain plain text from post body
	if !strings.Contains(html, "important things") {
		t.Errorf("Expected excerpt to contain 'important things', got: %s", html)
	}

	// Excerpt should NOT contain markdown formatting
	if strings.Contains(html, "**important") {
		t.Errorf("Excerpt should not contain markdown formatting, got: %s", html)
	}
}

func TestRenderIndex_PageTypeIsIndex(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Update index template to include page_type
	themesDir := filepath.Join(tempDir, ".polis", "themes", "turbo")
	idxTmpl := `<!DOCTYPE html>
<html>
<head><title>{{site_title}}</title></head>
<body>
<div data-page-type="{{page_type}}">{{site_title}}</div>
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "index.html"), []byte(idxTmpl), 0644)

	metadataDir := filepath.Join(tempDir, "metadata")
	os.MkdirAll(metadataDir, 0755)
	os.WriteFile(filepath.Join(metadataDir, "public.jsonl"), []byte(""), 0644)

	renderer, _ := NewPageRenderer(PageConfig{DataDir: tempDir, BaseURL: "https://example.com"})
	err := renderer.RenderIndex()
	if err != nil {
		t.Fatalf("RenderIndex failed: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(tempDir, "index.html"))
	if !strings.Contains(string(content), `data-page-type="index"`) {
		t.Errorf("Expected page_type=index in rendered index, got: %s", content)
	}
}

// setupTestSite creates a minimal polis site structure for testing.
func setupTestSite(t *testing.T, dir string) {
	t.Helper()

	// Create .well-known/polis
	wellKnownDir := filepath.Join(dir, ".well-known")
	os.MkdirAll(wellKnownDir, 0755)
	os.WriteFile(filepath.Join(wellKnownDir, "polis"), []byte(`{
		"base_url": "https://example.com",
		"site_title": "Test Site",
		"author_name": "Test Author"
	}`), 0644)

	// Create .polis/themes/turbo with minimal templates
	themesDir := filepath.Join(dir, ".polis", "themes", "turbo")
	os.MkdirAll(themesDir, 0755)

	postTemplate := `<!DOCTYPE html>
<html>
<head><title>{{title}} - {{site_title}}</title></head>
<body>
<h1>{{title}}</h1>
<div class="content">{{content}}</div>
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte(postTemplate), 0644)
	os.WriteFile(filepath.Join(themesDir, "comment.html"), []byte(postTemplate), 0644)
	os.WriteFile(filepath.Join(themesDir, "comment-inline.html"), []byte(`<div>{{content}}</div>`), 0644)

	indexTemplate := `<!DOCTYPE html>
<html>
<head><title>{{site_title}}</title></head>
<body>
<h1>{{site_title}}</h1>
{{#recent_posts}}<div class="post-item"><a href="{{url}}">{{title}}</a></div>{{/recent_posts}}
{{view_all_posts}}
{{#recent_comments}}<div class="comment-item">{{target_author}}: {{preview}}</div>{{/recent_comments}}
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "index.html"), []byte(indexTemplate), 0644)

	archiveTemplate := `<!DOCTYPE html>
<html>
<head><title>All Posts - {{site_title}}</title></head>
<body>
<a href="../index.html">Back</a>
{{#posts}}<div class="post-item"><a href="{{url}}">{{title}}</a></div>{{/posts}}
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "posts.html"), []byte(archiveTemplate), 0644)

	// Create CSS file (required by RenderAll)
	os.WriteFile(filepath.Join(themesDir, "turbo.css"), []byte("/* test css */"), 0644)

	// Create empty metadata/public.jsonl
	metadataDir := filepath.Join(dir, "metadata")
	os.MkdirAll(metadataDir, 0755)
	os.WriteFile(filepath.Join(metadataDir, "public.jsonl"), []byte(""), 0644)
}
