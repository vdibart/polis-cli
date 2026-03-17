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

func TestStripMarkers(t *testing.T) {
	marked := `<!-- POLIS-SNIPPET-START: global:about.html path=about.html -->
<span class="polis-snippet-boundary" data-snippet="global:about.html" data-path="about.html" data-source="global" hidden></span>
<p>Hello</p>
<!-- POLIS-SNIPPET-END: global:about.html -->`

	stripped := StripMarkers(marked)

	if strings.Contains(stripped, "POLIS-SNIPPET") {
		t.Errorf("Expected markers to be stripped, got: %s", stripped)
	}

	if !strings.Contains(stripped, "<p>Hello</p>") {
		t.Errorf("Expected content to remain, got: %s", stripped)
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
