package template

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVariableSubstitution(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.Title = "My Post"
	ctx.SiteTitle = "My Site"
	ctx.Year = "2026"

	template := `<title>{{title}} - {{site_title}}</title>
<footer>&copy; {{year}}</footer>`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, "My Post - My Site") {
		t.Errorf("Expected title substitution, got: %s", result)
	}

	if !strings.Contains(result, "&copy; 2026") {
		t.Errorf("Expected year substitution, got: %s", result)
	}
}

func TestUnknownVariablePassthrough(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()

	template := `Hello {{unknown_var}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if result != "Hello {{unknown_var}}" {
		t.Errorf("Expected unknown variable to pass through, got: %s", result)
	}
}

func TestPostsSection(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.Posts = []PostData{
		{URL: "/posts/1.html", Title: "First Post", PublishedHuman: "January 1, 2026"},
		{URL: "/posts/2.html", Title: "Second Post", PublishedHuman: "January 2, 2026"},
	}

	template := `{{#posts}}<a href="{{url}}">{{title}}</a>{{/posts}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, `<a href="/posts/1.html">First Post</a>`) {
		t.Errorf("Expected first post, got: %s", result)
	}

	if !strings.Contains(result, `<a href="/posts/2.html">Second Post</a>`) {
		t.Errorf("Expected second post, got: %s", result)
	}
}

func TestBlessedCommentsSection(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.BlessedComments = []BlessedCommentData{
		{URL: "/comment1", AuthorName: "Alice", Content: "<p>Great post!</p>"},
		{URL: "/comment2", AuthorName: "Bob", Content: "<p>Thanks for sharing</p>"},
	}

	template := `{{#blessed_comments}}<div class="comment">{{author_name}}: {{content}}</div>{{/blessed_comments}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, "Alice: <p>Great post!</p>") {
		t.Errorf("Expected Alice's comment, got: %s", result)
	}

	if !strings.Contains(result, "Bob: <p>Thanks for sharing</p>") {
		t.Errorf("Expected Bob's comment, got: %s", result)
	}
}

func TestCommentsSection(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.Comments = []CommentData{
		{URL: "/comments/1.html", TargetAuthor: "alice.com", PublishedHuman: "January 1, 2026", Preview: "Great post!"},
	}

	template := `{{#comments}}<span>{{target_author}}</span> {{preview}}{{/comments}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, "<span>alice.com</span> Great post!") {
		t.Errorf("Expected comment variables substituted, got: %s", result)
	}
}

func TestCommentsSectionViaPartial(t *testing.T) {
	// Regression test: comment loop variables must resolve through partials.
	// The index template uses {{> theme:comment-item}} inside {{#comments}},
	// so target_author and preview must be available after partial expansion.
	tempDir := t.TempDir()
	themeDir := filepath.Join(tempDir, "site", "themes", "turbo", "snippets")
	os.MkdirAll(themeDir, 0755)
	os.WriteFile(filepath.Join(themeDir, "comment-item.html"), []byte(
		`<span class="author">{{target_author}}</span> <span class="preview">{{preview}}</span>`), 0644)

	engine := New(Config{
		DataDir:     tempDir,
		ActiveTheme: "turbo",
	})
	ctx := NewRenderContext()
	ctx.Comments = []CommentData{
		{URL: "/c/1.html", TargetAuthor: "bob.example.com", PublishedHuman: "Feb 6, 2026", Preview: "Nice work"},
	}

	template := `{{#comments}}{{> theme:comment-item}}{{/comments}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if strings.Contains(result, "{{target_author}}") {
		t.Errorf("target_author was not substituted: %s", result)
	}
	if strings.Contains(result, "{{preview}}") {
		t.Errorf("preview was not substituted: %s", result)
	}
	if !strings.Contains(result, "bob.example.com") {
		t.Errorf("Expected target_author value, got: %s", result)
	}
	if !strings.Contains(result, "Nice work") {
		t.Errorf("Expected preview value, got: %s", result)
	}
}

func TestPartialLoading(t *testing.T) {
	// Create temp directory with snippets
	tempDir := t.TempDir()
	snippetsDir := filepath.Join(tempDir, "site", "snippets")
	os.MkdirAll(snippetsDir, 0755)

	// Create a test snippet
	os.WriteFile(filepath.Join(snippetsDir, "about.html"), []byte("<p>About content</p>"), 0644)

	engine := New(Config{
		DataDir: tempDir,
	})
	ctx := NewRenderContext()

	template := `<div>{{> about}}</div>`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, "<p>About content</p>") {
		t.Errorf("Expected snippet content, got: %s", result)
	}
}

// mockMarkdownRenderer is a simple markdown renderer for testing.
func mockMarkdownRenderer(md string) (string, error) {
	// Very simple markdown simulation for testing
	result := md
	result = strings.ReplaceAll(result, "# ", "<h1 id=\"test\">")
	result = strings.ReplaceAll(result, "\n\n", "</h1>\n<p>")
	result = strings.ReplaceAll(result, "*", "<em>")
	if strings.Contains(result, "<p>") {
		result += "</em></p>"
	}
	return result, nil
}

func TestPartialWithMarkdown(t *testing.T) {
	// Create temp directory with snippets
	tempDir := t.TempDir()
	snippetsDir := filepath.Join(tempDir, "site", "snippets")
	os.MkdirAll(snippetsDir, 0755)

	// Create a markdown snippet
	os.WriteFile(filepath.Join(snippetsDir, "intro.md"), []byte("# Hello\n\nThis is *intro*"), 0644)

	engine := New(Config{
		DataDir:          tempDir,
		MarkdownRenderer: mockMarkdownRenderer,
	})
	ctx := NewRenderContext()

	template := `{{> intro}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Markdown should be rendered to HTML
	if !strings.Contains(result, "<h1") || !strings.Contains(result, ">Hello") {
		t.Errorf("Expected h1 heading, got: %s", result)
	}

	if !strings.Contains(result, "<em>") {
		t.Errorf("Expected em text, got: %s", result)
	}
}

func TestPartialResolutionOrder(t *testing.T) {
	// Create temp directory with both global and theme snippets
	tempDir := t.TempDir()

	// Global snippets
	globalDir := filepath.Join(tempDir, "site", "snippets")
	os.MkdirAll(globalDir, 0755)
	os.WriteFile(filepath.Join(globalDir, "about.html"), []byte("GLOBAL ABOUT"), 0644)

	// Theme snippets
	themeDir := filepath.Join(tempDir, "site", "themes", "turbo", "snippets")
	os.MkdirAll(themeDir, 0755)
	os.WriteFile(filepath.Join(themeDir, "about.html"), []byte("THEME ABOUT"), 0644)

	engine := New(Config{
		DataDir:     tempDir,
		ActiveTheme: "turbo",
	})
	ctx := NewRenderContext()

	// Global-first (default)
	result, _ := engine.Render(`{{> about}}`, ctx)
	if !strings.Contains(result, "GLOBAL ABOUT") {
		t.Errorf("Expected global snippet (default), got: %s", result)
	}

	// Explicit global
	result, _ = engine.Render(`{{> global:about}}`, ctx)
	if !strings.Contains(result, "GLOBAL ABOUT") {
		t.Errorf("Expected global snippet (explicit), got: %s", result)
	}

	// Theme-first
	result, _ = engine.Render(`{{> theme:about}}`, ctx)
	if !strings.Contains(result, "THEME ABOUT") {
		t.Errorf("Expected theme snippet, got: %s", result)
	}
}

// installShapeSnippets writes partial files at the installed-shape snippets
// location used by the engine's base-snippet lookup post-SHAPE refactor.
func installShapeSnippets(t *testing.T, dataDir string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "shapes", "v3", "snippets")
	os.MkdirAll(dir, 0755)
	for name, content := range files {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	}
}

func TestBaseSnippetFallback(t *testing.T) {
	tempDir := t.TempDir()

	// Installed shape snippets act as the last-resort fallback.
	installShapeSnippets(t, tempDir, map[string]string{
		"post-item.html": "BASE POST ITEM",
		"about.html":     "BASE ABOUT",
	})

	// Theme with NO snippets (CSS-only).
	themeDir := filepath.Join(tempDir, "site", "themes", "minimal")
	os.MkdirAll(themeDir, 0755)

	engine := New(Config{
		DataDir:     tempDir,
		ActiveTheme: "minimal",
	})
	ctx := NewRenderContext()

	// theme: prefix with no theme snippets should fall back to the shape
	result, _ := engine.Render(`{{> theme:post-item}}`, ctx)
	if !strings.Contains(result, "BASE POST ITEM") {
		t.Errorf("Expected shape snippet fallback for theme: prefix, got: %s", result)
	}

	// default prefix (global-first) should also fall back to shape
	result, _ = engine.Render(`{{> about}}`, ctx)
	if !strings.Contains(result, "BASE ABOUT") {
		t.Errorf("Expected shape snippet fallback for default prefix, got: %s", result)
	}
}

// TestShapeRootResolutionForEntryPartials verifies the shape-root fallback
// in the partial-resolver lookup chain. v4's `{{> stream-post}}` resolves
// stream-post.html from the shape root (it's an Entry template, not a
// snippet). If a future contributor adds shapes/<v>/snippets/stream-post.html
// it would silently shadow the root version — this test acts as a tripwire:
// when only the shape-root file exists, resolution must succeed.
//
// Pair this with TestShapeRootShadowedByShapeSnippets below to catch a
// flipped lookup order at review time (snippets/ should win when both
// exist; shape-root should win when only the root file exists).
func TestShapeRootResolutionForEntryPartials(t *testing.T) {
	tempDir := t.TempDir()

	// Active shape's root carries the Entry template. snippets/ is empty —
	// no stream-post.html collision. We use ActiveShape "v4" + the same
	// path layout the bundle install lays down.
	shapeRoot := filepath.Join(tempDir, ".polis", "bundles", "pub.polis.core", "shapes", "v4")
	os.MkdirAll(shapeRoot, 0755)
	os.WriteFile(filepath.Join(shapeRoot, "stream-post.html"), []byte("ROOT STREAM POST"), 0644)

	// Empty snippets/ dir — confirms shape-root wins when snippets/ has no
	// shadowing file.
	os.MkdirAll(filepath.Join(shapeRoot, "snippets"), 0755)

	engine := New(Config{DataDir: tempDir, ActiveShape: "v4"})
	result, err := engine.Render(`{{> stream-post}}`, NewRenderContext())
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if !strings.Contains(result, "ROOT STREAM POST") {
		t.Errorf("Expected shape-root resolution, got: %s", result)
	}
}

// TestShapeRootShadowedByShapeSnippets is the tripwire's other half: when
// snippets/<file> AND <file> at the shape root both exist, snippets/ wins.
// If a future refactor flips the order, this test catches it before the
// silent shadow lands in production.
func TestShapeRootShadowedByShapeSnippets(t *testing.T) {
	tempDir := t.TempDir()

	shapeRoot := filepath.Join(tempDir, ".polis", "bundles", "pub.polis.core", "shapes", "v4")
	os.MkdirAll(shapeRoot, 0755)
	os.WriteFile(filepath.Join(shapeRoot, "stream-post.html"), []byte("ROOT STREAM POST"), 0644)

	snippetsDir := filepath.Join(shapeRoot, "snippets")
	os.MkdirAll(snippetsDir, 0755)
	os.WriteFile(filepath.Join(snippetsDir, "stream-post.html"), []byte("SNIPPETS STREAM POST"), 0644)

	engine := New(Config{DataDir: tempDir, ActiveShape: "v4"})
	result, err := engine.Render(`{{> stream-post}}`, NewRenderContext())
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if !strings.Contains(result, "SNIPPETS STREAM POST") {
		t.Errorf("Expected snippets/ to shadow shape-root, got: %s", result)
	}
}

func TestBaseSnippetOverriddenByTheme(t *testing.T) {
	tempDir := t.TempDir()

	installShapeSnippets(t, tempDir, map[string]string{
		"post-item.html": "BASE POST ITEM",
	})

	// Theme override snippet.
	themeDir := filepath.Join(tempDir, "site", "themes", "studio13", "snippets")
	os.MkdirAll(themeDir, 0755)
	os.WriteFile(filepath.Join(themeDir, "post-item.html"), []byte("STUDIO13 POST ITEM"), 0644)

	engine := New(Config{
		DataDir:     tempDir,
		ActiveTheme: "studio13",
	})
	ctx := NewRenderContext()

	// theme: prefix should use theme override, not the shape fallback.
	result, _ := engine.Render(`{{> theme:post-item}}`, ctx)
	if !strings.Contains(result, "STUDIO13 POST ITEM") {
		t.Errorf("Expected theme override over shape, got: %s", result)
	}
}

func TestMarkers(t *testing.T) {
	content := WrapWithMarkers("<p>Hello</p>", "about.html", "global")

	if !strings.Contains(content, "POLIS-SNIPPET-START: global:about.html") {
		t.Errorf("Expected start marker, got: %s", content)
	}

	if !strings.Contains(content, `data-source="global"`) {
		t.Errorf("Expected data-source attribute, got: %s", content)
	}

	if !strings.Contains(content, "POLIS-SNIPPET-END: global:about.html") {
		t.Errorf("Expected end marker, got: %s", content)
	}
}

func TestMaxRecursionDepth(t *testing.T) {
	tempDir := t.TempDir()
	snippetsDir := filepath.Join(tempDir, "site", "snippets")
	os.MkdirAll(snippetsDir, 0755)

	// Create recursive snippet
	os.WriteFile(filepath.Join(snippetsDir, "recursive.html"), []byte("{{> recursive}}"), 0644)

	engine := New(Config{
		DataDir: tempDir,
	})
	ctx := NewRenderContext()

	_, err := engine.Render(`{{> recursive}}`, ctx)
	if err == nil {
		t.Error("Expected error for infinite recursion")
	}

	if !strings.Contains(err.Error(), "maximum") {
		t.Errorf("Expected max depth error, got: %v", err)
	}
}

func TestPostTitleWithPartialSyntax(t *testing.T) {
	// Regression test: a post title containing {{> partial}} must not be
	// interpreted as a partial include. This reproduces the crash on
	// discover.polis.pub where a post title was:
	// "The template engine is Mustache-like: {{variable}}, {{> partial}}, {{#section}}"
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.Posts = []PostData{
		{
			URL:            "/posts/template-post.html",
			Title:          `Mustache-like: {{variable}}, {{> partial}}, {{#section}}`,
			Published:      "2026-02-01T10:00:00Z",
			PublishedHuman: "February 1, 2026",
		},
	}

	template := `{{#posts}}<a href="{{url}}">{{title}}</a>{{/posts}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render should not fail on title containing partial syntax, got: %v", err)
	}

	// The title should appear literally in the output
	if !strings.Contains(result, `{{> partial}}`) {
		t.Errorf("Expected literal {{> partial}} in output, got: %s", result)
	}
}

func TestPostTitleWithPartialSyntaxViaSnippet(t *testing.T) {
	// Regression test: reproduces the real-world discover.polis.pub crash where
	// the index template uses {{> theme:post-item}} inside {{#posts}}, and
	// the snippet contains {{title}}. The recursive renderWithDepth call
	// inside processPartials substitutes {{title}} with the user value,
	// which must not be re-interpreted as template syntax.
	tempDir := t.TempDir()
	themeDir := filepath.Join(tempDir, "site", "themes", "sols", "snippets")
	os.MkdirAll(themeDir, 0755)
	os.WriteFile(filepath.Join(themeDir, "post-item.html"), []byte(
		`<a href="{{url}}"><span>{{title}}</span></a>`), 0644)

	engine := New(Config{
		DataDir:     tempDir,
		ActiveTheme: "sols",
	})
	ctx := NewRenderContext()
	ctx.Posts = []PostData{
		{
			URL:            "/posts/template-post.html",
			Title:          `The syntax: {{variable}}, {{> partial}}, {{#section}}`,
			Published:      "2026-02-01T10:00:00Z",
			PublishedHuman: "February 1, 2026",
		},
	}

	tmpl := `{{#posts}}{{> theme:post-item}}{{/posts}}`

	result, err := engine.Render(tmpl, ctx)
	if err != nil {
		t.Fatalf("Render should not fail on title with partial syntax via snippet, got: %v", err)
	}

	if !strings.Contains(result, `{{> partial}}`) {
		t.Errorf("Expected literal {{> partial}} in output, got: %s", result)
	}
	if !strings.Contains(result, "/posts/template-post.html") {
		t.Errorf("Expected URL in output, got: %s", result)
	}
}

func TestRecentPostsSection(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()

	// Create 15 posts
	for i := 1; i <= 15; i++ {
		ctx.Posts = append(ctx.Posts, PostData{
			URL:            fmt.Sprintf("/posts/%d.html", i),
			Title:          fmt.Sprintf("Post %d", i),
			PublishedHuman: "January 1, 2026",
		})
	}

	template := `{{#recent_posts}}<a href="{{url}}">{{title}}</a>
{{/recent_posts}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Should contain exactly 10 posts (the limit)
	count := strings.Count(result, "<a href=")
	if count != 10 {
		t.Errorf("Expected 10 posts rendered, got %d", count)
	}

	// Should contain post 10 but not post 11
	if !strings.Contains(result, "Post 10") {
		t.Errorf("Expected Post 10 in output")
	}
	if strings.Contains(result, "Post 11") {
		t.Errorf("Post 11 should not be in output")
	}
}

func TestRecentPostsSectionFewPosts(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()

	// Create only 5 posts (under limit)
	for i := 1; i <= 5; i++ {
		ctx.Posts = append(ctx.Posts, PostData{
			URL:            fmt.Sprintf("/posts/%d.html", i),
			Title:          fmt.Sprintf("Post %d", i),
			PublishedHuman: "January 1, 2026",
		})
	}

	template := `{{#recent_posts}}<a href="{{url}}">{{title}}</a>
{{/recent_posts}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Should contain all 5 posts
	count := strings.Count(result, "<a href=")
	if count != 5 {
		t.Errorf("Expected 5 posts rendered, got %d", count)
	}
}

func TestRecentCommentsSection(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()

	// Create 15 comments
	for i := 1; i <= 15; i++ {
		ctx.Comments = append(ctx.Comments, CommentData{
			URL:            fmt.Sprintf("/comments/%d.html", i),
			TargetAuthor:   fmt.Sprintf("author%d.com", i),
			PublishedHuman: "January 1, 2026",
			Preview:        fmt.Sprintf("Comment %d", i),
		})
	}

	template := `{{#recent_comments}}<span>{{target_author}}</span> {{preview}}
{{/recent_comments}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Should contain exactly 10 comments (the limit)
	count := strings.Count(result, "<span>")
	if count != 10 {
		t.Errorf("Expected 10 comments rendered, got %d", count)
	}

	// Should contain comment 10 but not comment 11
	if !strings.Contains(result, "author10.com") {
		t.Errorf("Expected author10.com in output")
	}
	if strings.Contains(result, "author11.com") {
		t.Errorf("author11.com should not be in output")
	}
}

func TestRecentCommentsSectionFewComments(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()

	// Create only 3 comments (under limit)
	for i := 1; i <= 3; i++ {
		ctx.Comments = append(ctx.Comments, CommentData{
			URL:            fmt.Sprintf("/comments/%d.html", i),
			TargetAuthor:   fmt.Sprintf("author%d.com", i),
			PublishedHuman: "January 1, 2026",
			Preview:        fmt.Sprintf("Comment %d", i),
		})
	}

	template := `{{#recent_comments}}<span>{{target_author}}</span>
{{/recent_comments}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Should contain all 3 comments
	count := strings.Count(result, "<span>")
	if count != 3 {
		t.Errorf("Expected 3 comments rendered, got %d", count)
	}
}

func TestAuthorDomainAndPageTypeSubstitution(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.AuthorDomain = "alice.polis.pub"
	ctx.PageType = "post"

	tmpl := `<div data-author="{{author_domain}}" data-page-type="{{page_type}}"></div>`

	result, err := engine.Render(tmpl, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, `data-author="alice.polis.pub"`) {
		t.Errorf("Expected author_domain substitution, got: %s", result)
	}
	if !strings.Contains(result, `data-page-type="post"`) {
		t.Errorf("Expected page_type substitution, got: %s", result)
	}
}

func TestWidgetVersionSubstitution(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.WidgetVersion = "1.4.1"

	tmpl := `<script src="https://polis.pub/widget-{{widget_version}}.js"></script>`

	result, err := engine.Render(tmpl, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, `widget-1.4.1.js`) {
		t.Errorf("Expected widget_version substitution, got: %s", result)
	}
}

func TestWidgetVersionEmptyWhenUnset(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()

	tmpl := `<script src="https://polis.pub/widget-{{widget_version}}.js"></script>`

	result, err := engine.Render(tmpl, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, `widget-.js`) {
		t.Errorf("Expected empty widget_version, got: %s", result)
	}
}

func TestAuthorDomainEmptyWhenUnset(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	// AuthorDomain and PageType not set — should substitute to empty strings

	tmpl := `<div data-author="{{author_domain}}" data-page-type="{{page_type}}"></div>`

	result, err := engine.Render(tmpl, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, `data-author=""`) {
		t.Errorf("Expected empty author_domain, got: %s", result)
	}
	if !strings.Contains(result, `data-page-type=""`) {
		t.Errorf("Expected empty page_type, got: %s", result)
	}
}

func TestFormatHumanDate(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2026-01-08T12:00:00Z", "January 8, 2026"},
		{"2026-12-25", "December 25, 2026"},
		{"invalid", "invalid"},
	}

	for _, tc := range tests {
		result := FormatHumanDate(tc.input)
		if result != tc.expected {
			t.Errorf("FormatHumanDate(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

// TestFormatHumanDateTime locks in the stream meta-line format
// ("April 23, 2026 · 4:12pm") used by both SSR (page.go / v4.go) and
// the structured-query API (handlers_stream.go). SC-3 close — drift
// regression guard.
func TestFormatHumanDateTime(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2026-04-23T16:12:00Z", "April 23, 2026 · 4:12pm"},
		{"2026-01-08T08:30:00Z", "January 8, 2026 · 8:30am"},
		{"2026-04-23T00:00:00Z", "April 23, 2026 · 12:00am"},
		{"2026-04-23T12:00:00Z", "April 23, 2026 · 12:00pm"},
		// RFC3339Nano tolerated.
		{"2026-04-23T16:12:00.123456789Z", "April 23, 2026 · 4:12pm"},
		// Date-only fallback — keeps the date even when there's no time
		// component to render, so the meta-line doesn't go blank.
		{"2026-12-25", "December 25, 2026 · 12:00am"},
		// Unparseable — return as-is so callers can flag bad data without
		// us silently fabricating a date.
		{"not-a-date", "not-a-date"},
		{"", ""},
	}
	for _, tc := range tests {
		result := FormatHumanDateTime(tc.input)
		if result != tc.expected {
			t.Errorf("FormatHumanDateTime(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestStripYear(t *testing.T) {
	tests := []struct{ in, want string }{
		{"April 23, 2026 · 4:12pm", "April 23 · 4:12pm"},
		{"January 8, 2026 · 8:30am", "January 8 · 8:30am"},
		{"December 25, 2026 · 12:00am", "December 25 · 12:00am"},
		// Relative strings (no year) pass through unchanged.
		{"just now", "just now"},
		{"3 hours ago", "3 hours ago"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := StripYear(tc.in); got != tc.want {
			t.Errorf("StripYear(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatYear(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2026-04-23T16:12:00Z", "2026"},
		{"2025-12-31T23:59:59Z", "2025"},
		{"2026-12-25", "2026"},
		{"not-a-date", ""},
		{"", ""},
	}
	for _, tc := range tests {
		result := FormatYear(tc.input)
		if result != tc.expected {
			t.Errorf("FormatYear(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestTruncateSignature(t *testing.T) {
	sig := "AAAAC3NzaC1lZDI1NTE5AAAAIKs8y..."
	result := TruncateSignature(sig, 16)

	if len(result) != 16+3 { // 16 chars + "..."
		t.Errorf("Expected truncated signature of length 19, got: %d", len(result))
	}

	if !strings.HasSuffix(result, "...") {
		t.Errorf("Expected truncated signature to end with ..., got: %s", result)
	}
}

func TestFollowingSection(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.Following = []FollowingData{
		{URL: "https://alice.polis.pub", Domain: "alice.polis.pub", AuthorName: "Alice", SiteTitle: "Alice's Blog"},
		{URL: "https://bob.example.com", Domain: "bob.example.com", AuthorName: "", SiteTitle: ""},
	}

	tmpl := `{{#following}}<a href="{{url}}">{{author_name}}</a> {{/following}}`

	result, err := engine.Render(tmpl, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, `<a href="https://alice.polis.pub">Alice</a>`) {
		t.Errorf("Expected Alice link, got: %s", result)
	}
	// Bob has no author_name, should use domain as fallback
	if !strings.Contains(result, `<a href="https://bob.example.com">bob.example.com</a>`) {
		t.Errorf("Expected bob domain fallback, got: %s", result)
	}
}

func TestExcerptInPostsSection(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.Posts = []PostData{
		{URL: "/posts/1.html", Title: "Post One", Excerpt: "This is the excerpt of the first post."},
		{URL: "/posts/2.html", Title: "Post Two", Excerpt: ""},
	}

	template := `{{#posts}}<div class="post-blurb">{{excerpt}}</div>{{/posts}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, "This is the excerpt of the first post.") {
		t.Errorf("Expected excerpt in output, got: %s", result)
	}

	// Second post has empty excerpt - should render empty
	count := strings.Count(result, `<div class="post-blurb">`)
	if count != 2 {
		t.Errorf("Expected 2 post blurbs, got %d", count)
	}
}

func TestExcerptInRecentPostsSection(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.RecentPosts = []PostData{
		{URL: "/posts/1.html", Title: "Recent", Excerpt: "Recent post excerpt here."},
	}

	template := `{{#recent_posts}}<p>{{excerpt}}</p>{{/recent_posts}}`

	result, err := engine.Render(template, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, "Recent post excerpt here.") {
		t.Errorf("Expected excerpt in recent_posts output, got: %s", result)
	}
}

func TestFollowingSectionEmpty(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	// No following entries

	tmpl := `<div>{{#following}}<a href="{{url}}">{{author_name}}</a>{{/following}}</div>`

	result, err := engine.Render(tmpl, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Should produce empty content for the section
	if result != "<div></div>" {
		t.Errorf("Expected empty following section, got: %s", result)
	}
}

func TestCommentCountDisplayVariable(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.Posts = []PostData{
		{URL: "/posts/1.html", Title: "No Comments", CommentCount: 0},
		{URL: "/posts/2.html", Title: "Has Comments", CommentCount: 3},
	}

	tmpl := `{{#posts}}<div class="sp-comments">{{comment_count_display}}</div>{{/posts}}`

	result, err := engine.Render(tmpl, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Post with 0 comments should produce empty div (for CSS :empty to hide)
	if !strings.Contains(result, `<div class="sp-comments"></div>`) {
		t.Errorf("Expected empty sp-comments div for 0 comments, got: %s", result)
	}

	// Post with 3 comments should show count
	if !strings.Contains(result, `<div class="sp-comments">3</div>`) {
		t.Errorf("Expected sp-comments div with '3', got: %s", result)
	}
}

func TestCommentCountDisplayInRecentPosts(t *testing.T) {
	engine := New(Config{})
	ctx := NewRenderContext()
	ctx.RecentPosts = []PostData{
		{URL: "/posts/1.html", Title: "Recent", CommentCount: 5},
	}

	tmpl := `{{#recent_posts}}<span>{{comment_count_display}}</span>{{/recent_posts}}`

	result, err := engine.Render(tmpl, ctx)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if !strings.Contains(result, "<span>5</span>") {
		t.Errorf("Expected comment_count_display=5 in recent_posts, got: %s", result)
	}
}

// ── R16-10 partial path traversal regression ────────────────────────

func TestValidatePartialPath_RejectsTraversal(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool // true = should error
	}{
		{"empty", "", true},
		{"single dotdot", "..", true},
		{"trailing dotdot", "../etc/passwd", true},
		{"middle dotdot", "shared/../../etc/passwd", true},
		{"absolute unix", "/etc/passwd", true},
		{"absolute backslash", `\etc\passwd`, true},
		{"null byte", "abc\x00def", true},
		{"plain name", "about", false},
		{"with extension", "about.md", false},
		{"subdir", "shared/header.html", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePartialPath(c.path)
			gotErr := err != nil
			if gotErr != c.want {
				t.Errorf("validatePartialPath(%q): got err=%v, want err=%v", c.path, err, c.want)
			}
		})
	}
}

func TestProcessPartials_BlocksTraversalToOutsideFile(t *testing.T) {
	// Sibling dirs under <DataDir>/site/: snippets (lookup root) and secrets.
	// A traversal partial like "{{> ../secrets/key}}" would otherwise resolve
	// to the secrets dir via filepath.Join's `..` handling.
	tmpDir := t.TempDir()
	siteDir := filepath.Join(tmpDir, "site")
	snippetsDir := filepath.Join(siteDir, "snippets")
	secretsDir := filepath.Join(siteDir, "secrets")
	if err := os.MkdirAll(snippetsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secretsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretsDir, "key.html"), []byte("LEAKED"), 0600); err != nil {
		t.Fatal(err)
	}

	engine := New(Config{DataDir: tmpDir})
	ctx := NewRenderContext()

	tmpl := `{{> ../secrets/key}}`
	result, err := engine.Render(tmpl, ctx)
	if err == nil {
		t.Logf("Render returned no error; result=%q", result)
	}
	if strings.Contains(result, "LEAKED") {
		t.Errorf("partial traversal succeeded — result contains secret content: %q", result)
	}
}
