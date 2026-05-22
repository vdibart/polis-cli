package render

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
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

// --- step-02/2.b.4: shape-selection wiring tests ---

func TestNewPageRenderer_DefaultsToStreamWhenRegistryEmpty(t *testing.T) {
	// Post-cutover: no active_shape set → bundle.GetActiveShapeName
	// returns "v4" default. Tenant should render v4 stream fixtures
	// without error. v4 regression sentinel — was previously v3-default.
	//
	// Note: setupTestSite pins to v3 explicitly for the v3 test scaffold.
	// This test deliberately bypasses that by installing the reference
	// payload directly without setupTestSite's v3-pinning step.
	tempDir := t.TempDir()
	if err := bundle.EnsureReferencePayload(tempDir, "pub.polis.core"); err != nil {
		t.Fatalf("install reference payload: %v", err)
	}
	// Create minimal .well-known/polis so the renderer can boot.
	wellKnownDir := filepath.Join(tempDir, ".well-known")
	os.MkdirAll(wellKnownDir, 0755)
	os.WriteFile(filepath.Join(wellKnownDir, "polis"), []byte(`{
		"base_url": "https://example.com",
		"site_title": "Test Site"
	}`), 0644)
	// Empty content index.
	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(""), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer with empty active_shape: %v", err)
	}
	// v4 Stream template must be populated (default post-cutover).
	if renderer.templates.Stream == "" {
		t.Error("default-shape tenant: Stream template must be populated (v4)")
	}
}

func TestNewPageRenderer_PicksStreamFromRegistry(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)
	// Flip to v4 via the canonical setter.
	if err := bundle.SetActiveShapeName(tempDir, "v4"); err != nil {
		t.Fatalf("SetActiveShapeName(v4): %v", err)
	}

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer(v4): %v", err)
	}
	// v4 Stream template must be populated from the embedded v4 fixture.
	if renderer.templates.Stream == "" {
		t.Error("v4 tenant: Stream template must be populated")
	}
	// v4 artifact convention — stream.html ships with <main class="focus-content" data-polis-focus="true">.
	if !strings.Contains(renderer.templates.Stream, `class="focus-content"`) ||
		!strings.Contains(renderer.templates.Stream, `data-polis-focus="true"`) {
		t.Errorf("stream.html missing focus-content / data-polis-focus demarcation")
	}
}

func TestNewPageRenderer_UnknownShapeErrors(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)
	// Write an invalid active_shape directly to registry.
	if err := bundle.SetActiveShapeName(tempDir, "v99"); err != nil {
		t.Fatalf("SetActiveShapeName(v99): %v", err)
	}

	if _, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	}); err == nil {
		t.Error("NewPageRenderer should error for active_shape=v99 (shape dir absent)")
	}
}

// --- step-05/5.b: theme-resolution chain verification (Q-S12 deferred path) ---

// TestNewPageRenderer_RegistryThemeWins covers the canonical theme-resolution
// chain: when registry.json::active_theme is set, NewPageRenderer's chain
// (theme.GetActiveTheme → theme.SelectRandomTheme fallback) picks the
// registry value. Verifies that `registry.json` stays the canonical source
// of truth for active_theme — per the Q-S12 hard constraint settled at
// manager review (2026-04-26) + reinforced by user direction post-handback
// (no projection of active_theme out of registry.json into bundle.json or
// any other file).
func TestNewPageRenderer_RegistryThemeWins(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)
	// setupTestSite writes "active_theme: testtheme" into .well-known/polis
	// (legacy fallback location). Set a different theme in the canonical
	// location (registry) — registry should win over the legacy fallback.
	if err := bundle.SetActiveThemeName(tempDir, "vice"); err != nil {
		t.Fatalf("SetActiveThemeName(vice): %v", err)
	}

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if renderer.themeName != "vice" {
		t.Errorf("renderer.themeName = %q, want %q (registry should win over legacy .well-known fallback)", renderer.themeName, "vice")
	}
}

// TestNewPageRenderer_RandomFallbackWhenEmpty covers the fallback path:
// when neither registry.json::active_theme nor .well-known/polis::active_theme
// is set, NewPageRenderer falls back to theme.SelectRandomTheme. This is
// the path that EXERCISES on harness clones today — random-theme on clones
// is intentional v5 behavior (Q-S12 deferred at manager review 2026-04-26).
// SelectRandomTheme persists its choice to registry, so the next render
// is deterministic.
func TestNewPageRenderer_RandomFallbackWhenEmpty(t *testing.T) {
	tempDir := t.TempDir()
	// Install bundle reference payload — gives us themes for SelectRandomTheme
	// to pick from, and the blog shape templates needed by NewPageRenderer.
	if err := bundle.EnsureReferencePayload(tempDir, "pub.polis.core"); err != nil {
		t.Fatalf("install reference payload: %v", err)
	}
	// Write a minimal .well-known/polis WITHOUT active_theme — no legacy
	// fallback to find. Registry (created by EnsureReferencePayload) also
	// has no active_theme set yet.
	wkDir := filepath.Join(tempDir, ".well-known")
	os.MkdirAll(wkDir, 0755)
	os.WriteFile(filepath.Join(wkDir, "polis"), []byte(`{"base_url":"https://example.com","site_title":"Test","author_name":"Test"}`), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewPageRenderer (empty registry, empty well-known): %v", err)
	}
	// Random fallback should have picked SOME theme.
	if renderer.themeName == "" {
		t.Fatal("themeName empty after random fallback; expected SelectRandomTheme to pick a bundled theme")
	}
	// SelectRandomTheme persists its choice — verify it's now in the registry
	// so subsequent renders are deterministic.
	saved, err := bundle.GetActiveThemeName(tempDir)
	if err != nil {
		t.Fatalf("GetActiveThemeName after random fallback: %v", err)
	}
	if saved != renderer.themeName {
		t.Errorf("registry not updated after random fallback: GetActiveThemeName=%q, renderer.themeName=%q", saved, renderer.themeName)
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

// TestRenderArchive_FromShape verifies that when a theme omits posts.html,
// RenderArchive falls through to the shape's template. Post-SHAPE refactor the
// shape always provides posts.html, so the old "no template anywhere" scenario
// no longer arises.
func TestRenderArchive_FromShape(t *testing.T) {
	tempDir := t.TempDir()

	if err := bundle.EnsureReferencePayload(tempDir, "pub.polis.core"); err != nil {
		t.Fatalf("install reference payload: %v", err)
	}

	wellKnownDir := filepath.Join(tempDir, ".well-known")
	os.MkdirAll(wellKnownDir, 0755)
	os.WriteFile(filepath.Join(wellKnownDir, "polis"), []byte(`{
		"base_url": "https://example.com",
		"site_title": "Test Site",
		"author_name": "Test Author",
		"active_theme": "testtheme"
	}`), 0644)

	themesDir := filepath.Join(tempDir, "site", "themes", "testtheme")
	os.MkdirAll(themesDir, 0755)
	// Turbo theme intentionally omits posts.html — should fall through to shape.
	os.WriteFile(filepath.Join(themesDir, "post.html"), []byte("<html>{{title}}</html>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "comment.html"), []byte("<html>{{title}}</html>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "comment-inline.html"), []byte("<div>{{content}}</div>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "index.html"), []byte("<html>{{site_title}}</html>"), 0644)
	os.WriteFile(filepath.Join(themesDir, "testtheme.css"), []byte("/* test css */"), 0644)

	contentDir := filepath.Join(tempDir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(`{"path":"content/pub.polis.core/post/test.md","title":"Test","type":"post"}`), 0644)

	renderer, err := NewPageRenderer(PageConfig{
		DataDir: tempDir,
	})
	if err != nil {
		t.Fatalf("NewPageRenderer failed: %v", err)
	}

	// RenderArchive should succeed using the shape's posts.html as fallback.
	if err := renderer.RenderArchive(); err != nil {
		t.Fatalf("RenderArchive failed: %v", err)
	}
}

func TestRenderFile_AuthorDomainAndPageType(t *testing.T) {
	tempDir := t.TempDir()
	setupTestSite(t, tempDir)

	// Update post template to include widget variables
	themesDir := filepath.Join(tempDir, "site", "themes", "testtheme")
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
	themesDir := filepath.Join(tempDir, "site", "themes", "testtheme")
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
	themesDir := filepath.Join(tempDir, "site", "themes", "testtheme")
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
	themesDir := filepath.Join(tempDir, "site", "themes", "testtheme")
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
	themesDir := filepath.Join(tempDir, "site", "themes", "testtheme")
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
	themesDir := filepath.Join(tempDir, "site", "themes", "testtheme")
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
	themesDir := filepath.Join(tempDir, "site", "themes", "testtheme")
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
	themesDir := filepath.Join(tempDir, "site", "themes", "testtheme")
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

	// Install the reference bundle payload — gives us a working blog shape at
	// .polis/bundles/pub.polis.core/shapes/v3/, which render now requires.
	if err := bundle.EnsureReferencePayload(dir, "pub.polis.core"); err != nil {
		t.Fatalf("install reference payload: %v", err)
	}

	// Pin to v3 — this scaffold writes v3-style per-post templates into
	// site/themes/testtheme/, so we need active_shape=v3 to dispatch the
	// v3 render path. Post-cutover the default is v4; tests that exercise
	// v3 behavior must opt in explicitly.
	if err := bundle.SetActiveShapeName(dir, "v3"); err != nil {
		t.Fatalf("SetActiveShapeName(v3): %v", err)
	}

	// Create .well-known/polis
	wellKnownDir := filepath.Join(dir, ".well-known")
	os.MkdirAll(wellKnownDir, 0755)
	os.WriteFile(filepath.Join(wellKnownDir, "polis"), []byte(`{
		"base_url": "https://example.com",
		"site_title": "Test Site",
		"author_name": "Test Author",
		"active_theme": "testtheme"
	}`), 0644)

	// Create site/themes/testtheme with minimal templates
	themesDir := filepath.Join(dir, "site", "themes", "testtheme")
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
	os.WriteFile(filepath.Join(themesDir, "testtheme.css"), []byte("/* test css */"), 0644)

	// Create empty content/pub.polis.core/index.jsonl
	contentDir := filepath.Join(dir, "content", "pub.polis.core")
	os.MkdirAll(contentDir, 0755)
	os.WriteFile(filepath.Join(contentDir, "index.jsonl"), []byte(""), 0644)
}

// ── R16-12 SSRF on in_reply_to URL ──────────────────────────────────

func TestReplyURLAllowed_RejectsInternalAndUnsafe(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool // true = should be allowed
	}{
		// Allowed
		{"https public", "https://alice.example.com/posts/foo.html", true},
		{"http public", "http://alice.example.com/posts/foo.html", true},
		{"https with port", "https://alice.example.com:8443/posts/foo.html", true},

		// Rejected — internal/loopback/sentinel
		{"localhost", "http://localhost/", false},
		{"127.0.0.1", "http://127.0.0.1/", false},
		{"ipv6 loopback", "http://[::1]/", false},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", false},
		{"single label host", "http://redis/", false},
		{"dotinternal", "http://polis-ds.internal/health", false},
		{"dotlocal mDNS", "http://printer.local/", false},

		// Rejected — bad scheme / malformed
		{"file scheme", "file:///etc/passwd", false},
		{"javascript scheme", "javascript:alert(1)", false},
		{"empty", "", false},
		{"garbage", "::not a url::", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := replyURLAllowed(c.url)
			if got != c.want {
				t.Errorf("replyURLAllowed(%q) = %v, want %v", c.url, got, c.want)
			}
		})
	}
}

func TestFetchReplyContextFull_BlocksSSRFTargets(t *testing.T) {
	// All of these would otherwise reach internal/sensitive endpoints.
	for _, url := range []string{
		"http://localhost:6379/",
		"http://127.0.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://redis/",
		"http://polis-ds.internal/health",
	} {
		entry, ok := FetchReplyContextFull(url)
		if ok {
			t.Errorf("FetchReplyContextFull(%q) should have been blocked, got entry=%+v", url, entry)
		}
	}
}

// ── R19-1, R19-2 reply-context cache hardening ──────────────────────

// errAllRoundTripper makes any HTTP call fail. Use it to assert that
// a cache-hit path does NOT touch the network.
type errAllRoundTripper struct{}

func (errAllRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("test stub: HTTP fetch should not have happened on a cache-hit path")
}

// TestLoadOrFetchReplyContext_PartialEntryHits — R19-2 regression
// guard. A cached entry with Title + Excerpt but empty BodyMD must
// count as a cache hit; pre-fix the BodyMD!="" predicate forced a
// re-fetch on every call, defeating the cache's purpose for the
// remaining caller (buildCommentThreadEnrichments, which only
// consumes Title + Excerpt).
func TestLoadOrFetchReplyContext_PartialEntryHits(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, replyContextCachePath)
	os.MkdirAll(filepath.Dir(cachePath), 0755)

	// Seed a partial entry (Title + Excerpt + Domain, no BodyMD).
	partial := replyContextCache{
		"https://alice.example.com/posts/foo.html": ReplyContextEntry{
			Title:   "Foo",
			Excerpt: "An excerpt about foo.",
			Domain:  "alice.example.com",
		},
	}
	data, _ := json.MarshalIndent(partial, "", "  ")
	os.WriteFile(cachePath, data, 0644)

	// Swap in an error-everywhere HTTP client so any fetch attempt
	// fails. If the predicate forces a re-fetch we'd get ok=false.
	origClient := DefaultHTTPClient
	DefaultHTTPClient = &http.Client{Transport: errAllRoundTripper{}}
	defer func() { DefaultHTTPClient = origClient }()

	entry, ok := LoadOrFetchReplyContext(dir, "https://alice.example.com/posts/foo.html", 0)
	if !ok {
		t.Fatal("expected cache hit on partial entry; got ok=false (predicate forced re-fetch)")
	}
	if entry.Title != "Foo" || entry.Excerpt != "An excerpt about foo." {
		t.Errorf("returned entry doesn't match seed: %+v", entry)
	}
}

// stubRoundTripper returns a deterministic .md body for any GET so
// FetchReplyContextFull can succeed in the R19-1 race test without
// real network I/O.
type stubRoundTripper struct{}

func (stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Synthesize a minimal markdown body with frontmatter that
	// FetchReplyContextFull's parser will accept. Path-derived title
	// lets the test verify entries are keyed correctly.
	path := req.URL.Path
	body := "---\ntitle: " + path + "\n---\n\nBody for " + path + ".\n"
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

// countingRoundTripper wraps stubRoundTripper to count requests per
// URL — used by the singleflight test to assert exactly one
// underlying fetch fires for N concurrent same-URL callers.
type countingRoundTripper struct {
	mu     sync.Mutex
	counts map[string]int
	gate   chan struct{} // optional: block fetches until close()
}

func (c *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.counts[req.URL.String()]++
	c.mu.Unlock()
	if c.gate != nil {
		<-c.gate // wait for test to release
	}
	return stubRoundTripper{}.RoundTrip(req)
}

// TestLoadOrFetchReplyContext_SingleflightDeduplicates — R19-3
// regression guard. N concurrent callers for the SAME URL must
// trigger exactly one underlying HTTP fetch via the singleflight
// group. Pre-R19-3, each caller would fetch independently, wasting
// origin bandwidth.
func TestLoadOrFetchReplyContext_SingleflightDeduplicates(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments"), 0755)

	// Reset the package-level negative cache + singleflight inflight
	// map so prior tests don't pollute state.
	replyNegativeCacheMu.Lock()
	replyNegativeCache = make(map[string]time.Time)
	replyNegativeCacheMu.Unlock()

	// gate keeps fetches in flight until we open it — guarantees all
	// N callers reach the singleflight Do before the first fetch
	// completes, so they all share the in-flight call.
	gate := make(chan struct{})
	counter := &countingRoundTripper{counts: make(map[string]int), gate: gate}

	origClient := DefaultHTTPClient
	DefaultHTTPClient = &http.Client{Transport: counter}
	defer func() { DefaultHTTPClient = origClient }()

	const url = "https://alice.example.com/posts/shared.html"
	const n = 10

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			LoadOrFetchReplyContext(dir, url, 0)
		}()
	}

	// Give goroutines time to all enter singleflight, then release.
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()

	counter.mu.Lock()
	got := counter.counts[strings.TrimSuffix(url, ".html")+".md"]
	counter.mu.Unlock()
	if got != 1 {
		t.Errorf("expected exactly 1 underlying fetch for %d concurrent same-URL callers; got %d", n, got)
	}
}

// TestLoadOrFetchReplyContext_NegativeCacheBlocksRefetch — R19-4
// regression guard. After a failed fetch, the next call for the
// same URL must short-circuit (not re-fetch) until the negative
// entry expires.
func TestLoadOrFetchReplyContext_NegativeCacheBlocksRefetch(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments"), 0755)

	// Reset state.
	replyNegativeCacheMu.Lock()
	replyNegativeCache = make(map[string]time.Time)
	replyNegativeCacheMu.Unlock()

	counter := &countingRoundTripper{counts: make(map[string]int)}
	origClient := DefaultHTTPClient
	DefaultHTTPClient = &http.Client{Transport: errAllRoundTripper{}}
	defer func() { DefaultHTTPClient = origClient }()

	const url = "https://alice.example.com/posts/dead.html"

	// First call: fetch attempt fails → negative cache populated.
	_, ok := LoadOrFetchReplyContext(dir, url, 0)
	if ok {
		t.Fatal("first call: expected ok=false on fetch failure")
	}

	// Swap to the counting transport that WOULD succeed if hit.
	DefaultHTTPClient = &http.Client{Transport: counter}

	// Second call: must short-circuit via negative cache; no fetch.
	_, ok = LoadOrFetchReplyContext(dir, url, 0)
	if ok {
		t.Error("second call: negative cache hit should keep ok=false")
	}

	counter.mu.Lock()
	got := counter.counts[strings.TrimSuffix(url, ".html")+".md"]
	counter.mu.Unlock()
	if got != 0 {
		t.Errorf("second call should have hit negative cache (0 fetches); got %d", got)
	}
}

// TestLoadOrFetchReplyContext_ConcurrentWritesAllPersist — R19-1
// regression guard. N concurrent callers each fetch a distinct URL;
// every entry must survive the cache write. Pre-fix the load+save
// sequence dropped 4 out of every 5 entries because each caller's
// snapshot overwrote the others.
func TestLoadOrFetchReplyContext_ConcurrentWritesAllPersist(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments"), 0755)

	origClient := DefaultHTTPClient
	DefaultHTTPClient = &http.Client{Transport: stubRoundTripper{}}
	defer func() { DefaultHTTPClient = origClient }()

	const n = 20
	urls := make([]string, n)
	for i := 0; i < n; i++ {
		urls[i] = fmt.Sprintf("https://alice.example.com/posts/p%02d.html", i)
	}

	var wg sync.WaitGroup
	for _, u := range urls {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			_, ok := LoadOrFetchReplyContext(dir, u, 0)
			if !ok {
				t.Errorf("fetch failed for %s", u)
			}
		}(u)
	}
	wg.Wait()

	// Every URL we wrote must be present in the persisted cache.
	final := loadReplyContextCache(dir)
	if len(final) != n {
		t.Errorf("expected %d entries in persisted cache; got %d", n, len(final))
	}
	for _, u := range urls {
		if _, present := final[u]; !present {
			t.Errorf("entry for %s missing from persisted cache", u)
		}
	}
}
