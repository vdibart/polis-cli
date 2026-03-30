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
	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
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

	html, rendered, err := renderer.RenderFile("content/pub.polis.core/post/test-post.md", "post", true)
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

	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
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
	_, rendered, err := renderer.RenderFile("content/pub.polis.core/post/skip-test.md", "post", true)
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
	_, rendered, err = renderer.RenderFile("content/pub.polis.core/post/skip-test.md", "post", false)
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

	// Create content/pub.polis.core/index.jsonl with some entries
	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)
	publicJSONL := `{"path":"content/pub.polis.core/post/hello.md","title":"Hello World","published":"2026-01-15T12:00:00Z","type":"post"}
{"path":"content/pub.polis.core/post/goodbye.md","title":"Goodbye World","published":"2026-01-16T12:00:00Z","type":"post"}
`
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(publicJSONL), 0644)

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
	if !strings.Contains(string(content), "content/pub.polis.core/post/hello.html") {
		t.Errorf("Expected index to contain post URL, got: %s", content)
	}
}

func TestRenderAll(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create posts directory with a post
	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "post1.md"), []byte("---\ntitle: Post 1\n---\nContent 1"), 0644)

	// Create content/pub.polis.core/index.jsonl
	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(`{"path":"content/pub.polis.core/post/post1.md","title":"Post 1","type":"post"}`), 0644)

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

	// Create content/pub.polis.core/index.jsonl with 15 posts
	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)

	var entries string
	for i := 1; i <= 15; i++ {
		entries += fmt.Sprintf(`{"path":"content/pub.polis.core/post/post-%02d.md","title":"Post %d","published":"2026-01-%02dT12:00:00Z","type":"post"}`, i, i, i) + "\n"
	}
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(entries), 0644)

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

	// Create content/pub.polis.core/index.jsonl with only 5 posts (under the limit)
	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)

	var entries string
	for i := 1; i <= 5; i++ {
		entries += fmt.Sprintf(`{"path":"content/pub.polis.core/post/post-%02d.md","title":"Post %d","published":"2026-01-%02dT12:00:00Z","type":"post"}`, i, i, i) + "\n"
	}
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(entries), 0644)

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

	// Create content/pub.polis.core/index.jsonl with 15 posts
	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)

	var entries string
	for i := 1; i <= 15; i++ {
		entries += fmt.Sprintf(`{"path":"content/pub.polis.core/post/post-%02d.md","title":"Post %d","published":"2026-01-%02dT12:00:00Z","type":"post"}`, i, i, i) + "\n"
	}
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(entries), 0644)

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

	// Read the generated content/pub.polis.core/post/index.html
	content, err := os.ReadFile(filepath.Join(tempDir, "content", "pub.polis.core", "post", "index.html"))
	if err != nil {
		t.Fatalf("Failed to read content/pub.polis.core/post/index.html: %v", err)
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
		"author_name": "Test Author",
		"active_theme": "turbo"
	}`), 0644)

	themesDir := filepath.Join(tempDir, "site", "themes", "turbo")
	os.MkdirAll(themesDir, 0755)
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte("<html>{{title}}</html>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "comment.html"), []byte("<html>{{title}}</html>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "comment-inline.html"), []byte("<div>{{content}}</div>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "index.html"), []byte("<html>{{site_title}}</html>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "turbo.css"), []byte("/* test css */"), 0644)
	// NO posts.html

	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(`{"path":"content/pub.polis.core/post/test.md","title":"Test","type":"post"}`), 0644)

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

	// content/pub.polis.core/post/index.html should NOT be created
	if _, err := os.Stat(filepath.Join(tempDir, "content", "pub.polis.core", "post", "index.html")); !os.IsNotExist(err) {
		t.Error("content/pub.polis.core/post/index.html should not exist when theme lacks posts.html")
	}
}

func TestRenderFile_AuthorDomainAndPageType(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Update post template to include widget variables
	themesDir := filepath.Join(tempDir, "site", "themes", "turbo")
	postTmpl := `<!DOCTYPE html>
<html>
<head><title>{{title}}</title></head>
<body>
<div id="polis-widget" data-author="{{author_domain}}" data-page-type="{{page_type}}"></div>
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte(postTmpl), 0644)

	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
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

	html, rendered, err := renderer.RenderFile("content/pub.polis.core/post/test.md", "post", true)
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

func TestRenderFile_WidgetVersionPopulated(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Template that uses widget_version
	themesDir := filepath.Join(tempDir, "site", "themes", "turbo")
	postTmpl := `<script src="https://polis.pub/widget-{{widget_version}}.js"></script><h1>{{title}}</h1>`
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte(postTmpl), 0644)

	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "t.md"), []byte("---\ntitle: Test\n---\nContent"), 0644)

	renderer, _ := NewPageRenderer(PageConfig{DataDir: tempDir, BaseURL: "https://example.com"})

	html, _, err := renderer.RenderFile("content/pub.polis.core/post/t.md", "post", true)
	if err != nil {
		t.Fatalf("RenderFile failed: %v", err)
	}

	expected := fmt.Sprintf("widget-%s.js", WidgetVersion)
	if !strings.Contains(html, expected) {
		t.Errorf("Expected %s in rendered HTML, got: %s", expected, html)
	}
}

func TestRenderFile_AuthorDomainFromBaseURLConfig(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Template that exposes author_domain
	themesDir := filepath.Join(tempDir, "site", "themes", "turbo")
	postTmpl := `<div data-author="{{author_domain}}">{{title}}</div>`
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte(postTmpl), 0644)

	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "t.md"), []byte("---\ntitle: Test\n---\nContent"), 0644)

	// author_domain is derived from BaseURL config
	renderer, _ := NewPageRenderer(PageConfig{DataDir: tempDir, BaseURL: "https://bob.example.com"})

	html, _, err := renderer.RenderFile("content/pub.polis.core/post/t.md", "post", true)
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
	themesDir := filepath.Join(tempDir, "site", "themes", "turbo")
	postTmpl := `<div data-author="{{author_domain}}">{{title}}</div>`
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte(postTmpl), 0644)

	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "t.md"), []byte("---\ntitle: Test\n---\nContent"), 0644)

	// No BaseURL — author_domain should be empty (graceful degradation)
	renderer, _ := NewPageRenderer(PageConfig{DataDir: tempDir})

	html, _, err := renderer.RenderFile("content/pub.polis.core/post/t.md", "post", true)
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
	themesDir := filepath.Join(tempDir, "site", "themes", "turbo")
	idxTmpl := `<!DOCTYPE html>
<html>
<head><title>{{site_title}}</title></head>
<body>
{{#recent_posts}}<div class="post-item"><a href="{{url}}">{{title}}</a><p class="excerpt">{{excerpt}}</p></div>{{/recent_posts}}
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "index.html"), []byte(idxTmpl), 0644)

	// Create a real markdown post file
	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "hello.md"), []byte("---\ntitle: Hello World\npublished: 2026-01-15T12:00:00Z\n---\nThis is the body of my post about **important things** in the world."), 0644)

	// Create content/pub.polis.core/index.jsonl
	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(`{"path":"content/pub.polis.core/post/hello.md","title":"Hello World","published":"2026-01-15T12:00:00Z","type":"post"}`+"\n"), 0644)

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

func TestRenderIndex_CommentCountWithSourceContentPath(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Use an index template that shows comment_count
	themesDir := filepath.Join(tempDir, "site", "themes", "turbo")
	idxTmpl := `<!DOCTYPE html>
<html>
<head><title>{{site_title}}</title></head>
<body>
{{#recent_posts}}<div class="post-item"><a href="{{url}}">{{title}}</a> ({{comment_count}} comments)</div>{{/recent_posts}}
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "index.html"), []byte(idxTmpl), 0644)

	// Create a post source file
	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post", "20260304")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "hello-world.md"), []byte("---\ntitle: Hello World\npublished: 2026-03-04T12:00:00Z\n---\nContent"), 0644)

	// Create index.jsonl with source content path
	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(
		`{"path":"content/pub.polis.core/post/20260304/hello-world.md","title":"Hello World","published":"2026-03-04T12:00:00Z","type":"post"}`+"\n"), 0644)

	// Create blessed.json with mount path (this is what blessing/grant writes)
	commentDir := filepath.Join(tempDir, "content", "pub.polis.core", "comment")
	os.MkdirAll(commentDir, 0755)
	blessedJSON := `{"version":"polis-cli-go/0.56.0","comments":[{"post":"posts/20260304/hello-world.md","blessed":[{"url":"https://other.polis.pub/comments/20260304/re-hello.md","version":"sha256:abc","blessed_at":"2026-03-04T14:00:00Z"}]}]}`
	os.WriteFile(filepath.Join(commentDir, "blessed.json"), []byte(blessedJSON), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir:           tempDir,
		BaseURL:           "https://david.polis.pub",
		PostsSourceDir:    "content/pub.polis.core/post",
		PostsMountDir:     "posts",
		CommentsSourceDir: "content/pub.polis.core/comment",
		CommentsMountDir:  "comments",
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

	// Should show 1 comment, not 0 — this is the bug we fixed
	if !strings.Contains(html, "(1 comments)") {
		t.Errorf("Expected '(1 comments)' in index, got: %s", html)
	}
	if strings.Contains(html, "(0 comments)") {
		t.Errorf("Index should NOT show '(0 comments)' when blessed.json has 1 comment, got: %s", html)
	}
}

func TestRenderIndex_PageTypeIsIndex(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Update index template to include page_type
	themesDir := filepath.Join(tempDir, "site", "themes", "turbo")
	idxTmpl := `<!DOCTYPE html>
<html>
<head><title>{{site_title}}</title></head>
<body>
<div data-page-type="{{page_type}}">{{site_title}}</div>
</body>
</html>`
	os.WriteFile(filepath.Join(themesDir, "index.html"), []byte(idxTmpl), 0644)

	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(""), 0644)

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

// mountConfig returns standard PageConfig fields for source/output separation tests.
func mountConfig(dir string) PageConfig {
	return PageConfig{
		DataDir:           dir,
		BaseURL:           "https://example.com",
		RenderMarkers:     false,
		PostsSourceDir:    "content/pub.polis.core/post",
		PostsMountDir:     "posts",
		CommentsSourceDir: "content/pub.polis.core/comment",
		CommentsMountDir:  "comments",
	}
}

func TestSourceToMountPath(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	cfg := mountConfig(tempDir)
	renderer, err := NewPageRenderer(cfg)
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	tests := []struct {
		name     string
		path     string
		fileType string
		want     string
	}{
		{"post maps to mount", "content/pub.polis.core/post/20260101/hello.md", "post", "posts/20260101/hello.md"},
		{"comment maps to mount", "content/pub.polis.core/comment/20260101/reply.md", "comment", "comments/20260101/reply.md"},
		{"non-matching path unchanged", "other/path.md", "post", "other/path.md"},
		{"legacy (no config)", "content/pub.polis.core/post/test.md", "", "content/pub.polis.core/post/test.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderer.sourceToMountPath(tt.path, tt.fileType)
			if got != tt.want {
				t.Errorf("sourceToMountPath(%q, %q) = %q, want %q", tt.path, tt.fileType, got, tt.want)
			}
		})
	}
}

func TestRenderFile_MountDir(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create a test post in source dir
	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "test-post.md"), []byte("---\ntitle: Mount Test\npublished: 2026-01-15T12:00:00Z\n---\nHello **world**."), 0644)

	cfg := mountConfig(tempDir)
	renderer, err := NewPageRenderer(cfg)
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html, rendered, err := renderer.RenderFile("content/pub.polis.core/post/test-post.md", "post", true)
	if err != nil {
		t.Fatalf("RenderFile failed: %v", err)
	}
	if !rendered {
		t.Error("Expected file to be rendered")
	}
	if !strings.Contains(html, "Mount Test") {
		t.Errorf("Expected HTML to contain title, got: %s", html)
	}

	// HTML should be in mount dir, NOT source dir
	mountHTML := filepath.Join(tempDir, "posts", "test-post.html")
	if _, err := os.Stat(mountHTML); os.IsNotExist(err) {
		t.Error("Expected HTML file in mount dir (posts/test-post.html)")
	}

	sourceHTML := filepath.Join(postsDir, "test-post.html")
	if _, err := os.Stat(sourceHTML); !os.IsNotExist(err) {
		t.Error("HTML should NOT be written to source dir")
	}

	// .md should NOT be copied to mount dir
	mountMD := filepath.Join(tempDir, "posts", "test-post.md")
	if _, err := os.Stat(mountMD); !os.IsNotExist(err) {
		t.Error(".md file should NOT be copied to mount dir (posts/test-post.md)")
	}
}

func TestRenderFile_MountDir_Skip(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "skip-test.md"), []byte("---\ntitle: Skip Test\n---\nContent."), 0644)

	cfg := mountConfig(tempDir)
	renderer, _ := NewPageRenderer(cfg)

	// First render
	_, rendered, err := renderer.RenderFile("content/pub.polis.core/post/skip-test.md", "post", true)
	if err != nil {
		t.Fatalf("First render failed: %v", err)
	}
	if !rendered {
		t.Error("First render should render")
	}

	// Touch the mount HTML to be newer
	mountHTML := filepath.Join(tempDir, "posts", "skip-test.html")
	futureTime := time.Now().Add(time.Second)
	os.Chtimes(mountHTML, futureTime, futureTime)

	// Second render should skip
	_, rendered, err = renderer.RenderFile("content/pub.polis.core/post/skip-test.md", "post", false)
	if err != nil {
		t.Fatalf("Second render failed: %v", err)
	}
	if rendered {
		t.Error("Second render should skip (HTML in mount dir is up to date)")
	}
}

func TestRenderFile_MountDir_CSSHomePaths(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Template that exposes css_path and home_path
	themesDir := filepath.Join(tempDir, "site", "themes", "turbo")
	postTmpl := `<link rel="stylesheet" href="{{css_path}}"><a href="{{home_path}}">Home</a><h1>{{title}}</h1>`
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte(postTmpl), 0644)

	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post", "20260101")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "test.md"), []byte("---\ntitle: Path Test\n---\nContent"), 0644)

	cfg := mountConfig(tempDir)
	renderer, _ := NewPageRenderer(cfg)

	html, _, err := renderer.RenderFile("content/pub.polis.core/post/20260101/test.md", "post", true)
	if err != nil {
		t.Fatalf("RenderFile failed: %v", err)
	}

	// Mount path is posts/20260101/test.html (depth 2), so CSS should be ../../styles.css
	if !strings.Contains(html, `../../styles.css`) {
		t.Errorf("Expected ../../styles.css for mount path depth 2, got: %s", html)
	}
	if !strings.Contains(html, `../../index.html`) {
		t.Errorf("Expected ../../index.html for mount path depth 2, got: %s", html)
	}
}

func TestRenderAll_MountDir(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create a post and a comment in source dirs
	postsDir := filepath.Join(tempDir, "content", "pub.polis.core", "post")
	os.MkdirAll(postsDir, 0755)
	os.WriteFile(filepath.Join(postsDir, "post1.md"), []byte("---\ntitle: Post 1\n---\nContent 1"), 0644)

	commentsDir := filepath.Join(tempDir, "content", "pub.polis.core", "comment")
	os.MkdirAll(commentsDir, 0755)
	os.WriteFile(filepath.Join(commentsDir, "comment1.md"), []byte("---\ntitle: Comment 1\n---\nReply"), 0644)

	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(
		`{"path":"content/pub.polis.core/post/post1.md","title":"Post 1","type":"post"}`+"\n"+
			`{"path":"content/pub.polis.core/comment/comment1.md","title":"Comment 1","type":"comment"}`+"\n"), 0644)

	cfg := mountConfig(tempDir)
	renderer, _ := NewPageRenderer(cfg)

	stats, err := renderer.RenderAll(true)
	if err != nil {
		t.Fatalf("RenderAll failed: %v", err)
	}

	if stats.PostsRendered != 1 {
		t.Errorf("Expected 1 post rendered, got %d", stats.PostsRendered)
	}
	if stats.CommentsRendered != 1 {
		t.Errorf("Expected 1 comment rendered, got %d", stats.CommentsRendered)
	}

	// Verify output is in mount dirs
	if _, err := os.Stat(filepath.Join(tempDir, "posts", "post1.html")); os.IsNotExist(err) {
		t.Error("Expected posts/post1.html in mount dir")
	}
	if _, err := os.Stat(filepath.Join(tempDir, "posts", "post1.md")); !os.IsNotExist(err) {
		t.Error(".md file should NOT be copied to mount dir (posts/post1.md)")
	}
	if _, err := os.Stat(filepath.Join(tempDir, "comments", "comment1.html")); os.IsNotExist(err) {
		t.Error("Expected comments/comment1.html in mount dir")
	}
}

func TestRenderAll_CopiesArtifacts(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Create blessed.json in comment source dir
	commentDir := filepath.Join(tempDir, "content", "pub.polis.core", "comment")
	os.MkdirAll(commentDir, 0755)
	os.WriteFile(filepath.Join(commentDir, "blessed.json"), []byte(`{"comments":[]}`), 0644)

	cfg := mountConfig(tempDir)
	renderer, _ := NewPageRenderer(cfg)

	_, err := renderer.RenderAll(true)
	if err != nil {
		t.Fatalf("RenderAll failed: %v", err)
	}

	// Verify blessed.json was copied to comments mount dir
	dstBlessed := filepath.Join(tempDir, "comments", "blessed.json")
	if _, err := os.Stat(dstBlessed); os.IsNotExist(err) {
		t.Error("Expected blessed.json copied to comments mount dir")
	}
}

func TestRenderIndex_MountDirURLs(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(
		`{"path":"content/pub.polis.core/post/hello.md","title":"Hello","published":"2026-01-15T12:00:00Z","type":"post"}`+"\n"), 0644)

	cfg := mountConfig(tempDir)
	renderer, _ := NewPageRenderer(cfg)

	err := renderer.RenderIndex()
	if err != nil {
		t.Fatalf("RenderIndex failed: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(tempDir, "index.html"))
	html := string(content)

	// URL should use absolute mount path, not source path
	if !strings.Contains(html, "/posts/hello.html") {
		t.Errorf("Expected absolute mount path URL (/posts/hello.html), got: %s", html)
	}
	if strings.Contains(html, "content/pub.polis.core/post/hello.html") {
		t.Errorf("URL should NOT use source path, got: %s", html)
	}
}

func TestRenderArchive_MountDir(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)

	var entries string
	for i := 1; i <= 5; i++ {
		entries += fmt.Sprintf(`{"path":"content/pub.polis.core/post/post-%02d.md","title":"Post %d","published":"2026-01-%02dT12:00:00Z","type":"post"}`, i, i, i) + "\n"
	}
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(entries), 0644)

	cfg := mountConfig(tempDir)
	renderer, err := NewPageRenderer(cfg)
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	err = renderer.RenderArchive()
	if err != nil {
		t.Fatalf("RenderArchive failed: %v", err)
	}

	// Archive should be in mount dir (posts/index.html)
	archivePath := filepath.Join(tempDir, "posts", "index.html")
	content, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("Failed to read posts/index.html: %v", err)
	}

	html := string(content)
	postCount := strings.Count(html, `class="post-item"`)
	if postCount != 5 {
		t.Errorf("Expected 5 post items on archive page, got %d", postCount)
	}

	// Should NOT be in legacy location
	legacyPath := filepath.Join(tempDir, "content", "pub.polis.core", "post", "index.html")
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Error("Archive should NOT be in legacy source dir when mount dir is configured")
	}
}

func TestBuildAvatarHTML_Default(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html := renderer.buildAvatarHTML()

	// Default test site has author_name "Test Author", so initial should be "T"
	if !strings.Contains(html, `class="avatar-initial"`) {
		t.Error("expected avatar-initial class")
	}
	if !strings.Contains(html, ">T</span>") {
		t.Errorf("expected initial 'T', got: %s", html)
	}
	// No inline style for default (no avatar config in .well-known/polis)
	if strings.Contains(html, "style=") {
		t.Errorf("default avatar should not have inline style, got: %s", html)
	}
}

func TestBuildAvatarHTML_BGAndFG(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Overwrite .well-known/polis with avatar bg/fg config
	wkPath := filepath.Join(tempDir, ".well-known", "polis")
	os.WriteFile(wkPath, []byte(`{
		"author_name": "Alice",
		"avatar": {"bg": "#ff0000", "fg": "#ffffff"}
	}`), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html := renderer.buildAvatarHTML()

	if !strings.Contains(html, "background-color:#ff0000") {
		t.Errorf("expected bg color, got: %s", html)
	}
	if !strings.Contains(html, "color:#ffffff") {
		t.Errorf("expected fg color, got: %s", html)
	}
	if !strings.Contains(html, ">A</span>") {
		t.Errorf("expected initial 'A', got: %s", html)
	}
}

func TestBuildAvatarHTML_Border(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	wkPath := filepath.Join(tempDir, ".well-known", "polis")
	os.WriteFile(wkPath, []byte(`{
		"author_name": "Bob",
		"avatar": {"bg": "#000", "fg": "#fff", "border": "#gold", "border_w": 2}
	}`), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html := renderer.buildAvatarHTML()

	if !strings.Contains(html, "border:2px solid #gold") {
		t.Errorf("expected border style, got: %s", html)
	}
	// Initial should still show (no pattern)
	if !strings.Contains(html, ">B</span>") {
		t.Errorf("expected initial 'B', got: %s", html)
	}
}

func TestBuildAvatarHTML_PatternHidesInitial(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	wkPath := filepath.Join(tempDir, ".well-known", "polis")
	os.WriteFile(wkPath, []byte(`{
		"author_name": "Carol",
		"avatar": {"bg": "#1a2c50", "fg": "#fff", "pattern": "dots", "pattern_color": "#d06888"}
	}`), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html := renderer.buildAvatarHTML()

	// Pattern should produce base64 SVG background-image
	if !strings.Contains(html, "background-image:url(data:image/svg+xml;base64,") {
		t.Errorf("expected SVG pattern background-image, got: %s", html)
	}
	// Initial should be hidden when pattern is set
	if strings.Contains(html, ">C</span>") {
		t.Errorf("initial should be hidden when pattern is set, got: %s", html)
	}
	if !strings.Contains(html, "></span>") {
		t.Errorf("expected empty span content, got: %s", html)
	}
}

func TestBuildAvatarHTML_PatternNoneShowsInitial(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	wkPath := filepath.Join(tempDir, ".well-known", "polis")
	os.WriteFile(wkPath, []byte(`{
		"author_name": "Dave",
		"avatar": {"bg": "#123", "fg": "#fff", "pattern": "none", "pattern_color": "#abc"}
	}`), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html := renderer.buildAvatarHTML()

	// "none" pattern should NOT produce background-image
	if strings.Contains(html, "background-image") {
		t.Errorf("pattern 'none' should not produce background-image, got: %s", html)
	}
	// Initial should show
	if !strings.Contains(html, ">D</span>") {
		t.Errorf("expected initial 'D' with pattern=none, got: %s", html)
	}
}

func TestBuildAvatarHTML_AllPatterns(t *testing.T) {
	patterns := []string{"rings", "cross", "grid", "dots", "stripes", "diamond", "halves"}

	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			tempDir := t.TempDir()
			setupTestSite(t, tempDir)

			wkPath := filepath.Join(tempDir, ".well-known", "polis")
			os.WriteFile(wkPath, []byte(fmt.Sprintf(`{
				"author_name": "Test",
				"avatar": {"bg": "#000", "fg": "#fff", "pattern": "%s", "pattern_color": "#ff0"}
			}`, pattern)), 0644)

			renderer, err := NewPageRenderer(PageConfig{
				DataDir: tempDir,
				BaseURL: "https://example.com",
			})
			if err != nil {
				t.Fatalf("NewPageRenderer failed: %v", err)
			}

			html := renderer.buildAvatarHTML()

			if !strings.Contains(html, "background-image:url(data:image/svg+xml;base64,") {
				t.Errorf("pattern %q should produce SVG background-image, got: %s", pattern, html)
			}
		})
	}
}

func TestBuildAvatarHTML_UnknownPatternShowsInitial(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	wkPath := filepath.Join(tempDir, ".well-known", "polis")
	os.WriteFile(wkPath, []byte(`{
		"author_name": "Eve",
		"avatar": {"bg": "#000", "fg": "#fff", "pattern": "unknown_pattern", "pattern_color": "#abc"}
	}`), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html := renderer.buildAvatarHTML()

	// Unknown pattern should be ignored, no background-image
	if strings.Contains(html, "background-image") {
		t.Errorf("unknown pattern should not produce background-image, got: %s", html)
	}
	// Initial should show since pattern was not recognized
	if !strings.Contains(html, ">E</span>") {
		t.Errorf("expected initial 'E' with unknown pattern, got: %s", html)
	}
}

func TestBuildAvatarHTML_FallbackToDomain(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Overwrite .well-known/polis WITHOUT author_name
	wkPath := filepath.Join(tempDir, ".well-known", "polis")
	os.WriteFile(wkPath, []byte(`{
		"base_url": "https://alice.polis.pub"
	}`), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://alice.polis.pub",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	html := renderer.buildAvatarHTML()

	// Should fall back to domain initial "a"
	if !strings.Contains(html, ">a</span>") {
		t.Errorf("expected domain initial 'a', got: %s", html)
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
		"author_name": "Test Author",
		"active_theme": "turbo"
	}`), 0644)

	// Create site/themes/turbo with minimal templates
	themesDir := filepath.Join(dir, "site", "themes", "turbo")
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

	// Create empty content/pub.polis.core/index.jsonl
	contentDir := filepath.Join(dir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(""), 0644)
}
