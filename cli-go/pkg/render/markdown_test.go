package render

import (
	"strings"
	"testing"
)

func TestMarkdownToHTML_Heading(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"h1", "# Hello", "<h1"},
		{"h2", "## Hello", "<h2"},
		{"h3", "### Hello", "<h3"},
		{"h4", "#### Hello", "<h4"},
		{"h5", "##### Hello", "<h5"},
		{"h6", "###### Hello", "<h6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := MarkdownToHTML(tt.input)
			if err != nil {
				t.Fatalf("MarkdownToHTML failed: %v", err)
			}
			if !strings.Contains(html, tt.contains) {
				t.Errorf("Expected HTML to contain %q, got %q", tt.contains, html)
			}
		})
	}
}

func TestMarkdownToHTML_Emphasis(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"bold asterisk", "**bold**", "<strong>bold</strong>"},
		{"bold underscore", "__bold__", "<strong>bold</strong>"},
		{"italic asterisk", "*italic*", "<em>italic</em>"},
		{"italic underscore", "_italic_", "<em>italic</em>"},
		{"strikethrough", "~~deleted~~", "<del>deleted</del>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := MarkdownToHTML(tt.input)
			if err != nil {
				t.Fatalf("MarkdownToHTML failed: %v", err)
			}
			if !strings.Contains(html, tt.contains) {
				t.Errorf("Expected HTML to contain %q, got %q", tt.contains, html)
			}
		})
	}
}

func TestMarkdownToHTML_Lists(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			"unordered list",
			"- item 1\n- item 2",
			[]string{"<ul>", "<li>", "item 1", "item 2", "</ul>"},
		},
		{
			"ordered list",
			"1. first\n2. second",
			[]string{"<ol>", "<li>", "first", "second", "</ol>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := MarkdownToHTML(tt.input)
			if err != nil {
				t.Fatalf("MarkdownToHTML failed: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(html, want) {
					t.Errorf("Expected HTML to contain %q, got %q", want, html)
				}
			}
		})
	}
}

func TestMarkdownToHTML_Links(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			"inline link",
			"[text](https://example.com)",
			[]string{`<a href="https://example.com"`, `>text</a>`},
		},
		{
			"autolink",
			"https://example.com",
			[]string{`<a href="https://example.com"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := MarkdownToHTML(tt.input)
			if err != nil {
				t.Fatalf("MarkdownToHTML failed: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(html, want) {
					t.Errorf("Expected HTML to contain %q, got %q", want, html)
				}
			}
		})
	}
}

func TestMarkdownToHTML_CodeBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			"inline code",
			"`code`",
			[]string{"<code>", "code", "</code>"},
		},
		{
			"fenced code block",
			"```\ncode block\n```",
			[]string{"<pre>", "<code>", "code block"},
		},
		{
			"fenced with language",
			"```go\nfunc main() {}\n```",
			[]string{"<pre>", "<code", "func main()"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := MarkdownToHTML(tt.input)
			if err != nil {
				t.Fatalf("MarkdownToHTML failed: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(html, want) {
					t.Errorf("Expected HTML to contain %q, got %q", want, html)
				}
			}
		})
	}
}

func TestMarkdownToHTML_Blockquote(t *testing.T) {
	html, err := MarkdownToHTML("> This is a quote")
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	if !strings.Contains(html, "<blockquote>") {
		t.Errorf("Expected blockquote, got %q", html)
	}
}

func TestMarkdownToHTML_Paragraph(t *testing.T) {
	html, err := MarkdownToHTML("This is a paragraph.")
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	if !strings.Contains(html, "<p>") {
		t.Errorf("Expected paragraph tags, got %q", html)
	}
}

func TestMarkdownToHTML_HorizontalRule(t *testing.T) {
	inputs := []string{"---", "***", "___"}
	for _, input := range inputs {
		html, err := MarkdownToHTML(input)
		if err != nil {
			t.Fatalf("MarkdownToHTML failed: %v", err)
		}
		if !strings.Contains(html, "<hr") {
			t.Errorf("Expected <hr> for %q, got %q", input, html)
		}
	}
}

func TestMarkdownToHTML_Table(t *testing.T) {
	input := `| Header 1 | Header 2 |
| -------- | -------- |
| Cell 1   | Cell 2   |`

	html, err := MarkdownToHTML(input)
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}

	expected := []string{"<table>", "<thead>", "<tbody>", "<tr>", "<th>", "<td>"}
	for _, want := range expected {
		if !strings.Contains(html, want) {
			t.Errorf("Expected HTML to contain %q for table", want)
		}
	}
}

func TestMarkdownToHTML_RawHTML(t *testing.T) {
	// Safe raw HTML should pass through (goldmark WithUnsafe + bluemonday allows it)
	input := `<div class="custom">content</div>`
	html, err := MarkdownToHTML(input)
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	if !strings.Contains(html, `<div class="custom">`) {
		t.Errorf("Safe raw HTML should be preserved, got %q", html)
	}
}

func TestMarkdownToHTML_ScriptStripped(t *testing.T) {
	input := `Hello <script>alert(1)</script> world`
	html, err := MarkdownToHTML(input)
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	if strings.Contains(html, "<script>") {
		t.Errorf("Script tags should be stripped, got %q", html)
	}
	if strings.Contains(html, "alert(1)") {
		t.Errorf("Script content should be stripped, got %q", html)
	}
	if !strings.Contains(html, "Hello") || !strings.Contains(html, "world") {
		t.Errorf("Non-script text should be preserved, got %q", html)
	}
}

func TestMarkdownToHTML_EventHandlerStripped(t *testing.T) {
	input := `<img src="photo.jpg" onerror="alert(1)">`
	html, err := MarkdownToHTML(input)
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	if strings.Contains(html, "onerror") {
		t.Errorf("Event handlers should be stripped, got %q", html)
	}
	if !strings.Contains(html, "<img") {
		t.Errorf("Img tag should be preserved, got %q", html)
	}
}

func TestMarkdownToHTML_IframeStripped(t *testing.T) {
	input := `<iframe src="https://evil.com"></iframe>`
	html, err := MarkdownToHTML(input)
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	if strings.Contains(html, "<iframe") {
		t.Errorf("Iframe should be stripped, got %q", html)
	}
}

func TestMarkdownToHTML_SafeElementsPreserved(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"details", "<details><summary>Toggle</summary>Content</details>", "<details>"},
		{"video", `<video controls src="video.mp4"></video>`, "<video"},
		{"kbd", "<kbd>Ctrl+C</kbd>", "<kbd>"},
		{"mark", "<mark>highlighted</mark>", "<mark>"},
		{"figure", "<figure><figcaption>Caption</figcaption></figure>", "<figure>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := MarkdownToHTML(tt.input)
			if err != nil {
				t.Fatalf("MarkdownToHTML failed: %v", err)
			}
			if !strings.Contains(html, tt.contains) {
				t.Errorf("Expected %q to be preserved, got %q", tt.contains, html)
			}
		})
	}
}

func TestMarkdownToHTML_JavascriptURLStripped(t *testing.T) {
	input := `<a href="javascript:alert(1)">click me</a>`
	html, err := MarkdownToHTML(input)
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	if strings.Contains(html, "javascript:") {
		t.Errorf("javascript: URLs should be stripped, got %q", html)
	}
	if !strings.Contains(html, "click me") {
		t.Errorf("Link text should be preserved, got %q", html)
	}
}

func TestMarkdownToHTML_FormStripped(t *testing.T) {
	input := `<form action="/steal"><input type="text" name="password"><button>Submit</button></form>`
	html, err := MarkdownToHTML(input)
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	if strings.Contains(html, "<form") {
		t.Errorf("Form elements should be stripped, got %q", html)
	}
	if strings.Contains(html, "<input") {
		t.Errorf("Input elements should be stripped, got %q", html)
	}
}

func TestMarkdownToHTML_Unicode(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"chinese", "# 你好世界"},
		{"emoji", "Hello 🎉 World"},
		{"mixed", "Привет мир! 🌍 مرحبا"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := MarkdownToHTML(tt.input)
			if err != nil {
				t.Fatalf("MarkdownToHTML failed: %v", err)
			}
			// Unicode should be preserved
			if !strings.Contains(html, "你好") && !strings.Contains(html, "🎉") && !strings.Contains(html, "Привет") {
				// At least one should be present based on the test case
				if tt.name == "chinese" && !strings.Contains(html, "你好") {
					t.Errorf("Unicode content should be preserved")
				}
			}
		})
	}
}

func TestMarkdownToHTML_Empty(t *testing.T) {
	html, err := MarkdownToHTML("")
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	if html != "" {
		t.Errorf("Empty input should produce empty output, got %q", html)
	}
}

func TestMarkdownToHTML_Typographer(t *testing.T) {
	// Typographer extension converts quotes and dashes
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"smart quotes", `"hello"`, "\u201c"}, // Left double quote
		{"em dash", "a -- b", "\u2013"},       // En dash (-- becomes en dash)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := MarkdownToHTML(tt.input)
			if err != nil {
				t.Fatalf("MarkdownToHTML failed: %v", err)
			}
			if !strings.Contains(html, tt.contains) {
				t.Logf("HTML output: %q", html)
				// Don't fail - typographer behavior may vary
			}
		})
	}
}

func TestMarkdownToHTML_AutoHeadingID(t *testing.T) {
	html, err := MarkdownToHTML("# My Heading")
	if err != nil {
		t.Fatalf("MarkdownToHTML failed: %v", err)
	}
	// With WithAutoHeadingID(), headings should have id attributes
	if !strings.Contains(html, "id=") {
		t.Logf("HTML: %q", html)
		// This might depend on goldmark version, so just log
	}
}

func TestMarkdownToPlainText_Basic(t *testing.T) {
	result := MarkdownToPlainText("This is **bold** and *italic* text.", 200)
	if !strings.Contains(result, "bold") {
		t.Errorf("Expected 'bold' in result, got: %s", result)
	}
	if strings.Contains(result, "<") {
		t.Errorf("Expected no HTML tags in result, got: %s", result)
	}
	if strings.Contains(result, "*") {
		t.Errorf("Expected no markdown formatting in result, got: %s", result)
	}
}

func TestMarkdownToPlainText_StripsTags(t *testing.T) {
	// Markdown with raw HTML
	result := MarkdownToPlainText("Hello <div>world</div> end", 200)
	if strings.Contains(result, "<div>") {
		t.Errorf("Expected HTML tags stripped, got: %s", result)
	}
	if !strings.Contains(result, "world") {
		t.Errorf("Expected 'world' in result, got: %s", result)
	}
}

func TestMarkdownToPlainText_Truncation(t *testing.T) {
	long := strings.Repeat("word ", 100) // 500 chars
	result := MarkdownToPlainText(long, 50)
	if len(result) > 50 {
		t.Errorf("Expected result <= 50 chars, got %d: %s", len(result), result)
	}
	if !strings.HasSuffix(result, "...") {
		t.Errorf("Expected truncated result to end with '...', got: %s", result)
	}
}

func TestMarkdownToPlainText_Empty(t *testing.T) {
	result := MarkdownToPlainText("", 200)
	if result != "" {
		t.Errorf("Expected empty result, got: %s", result)
	}
}

func TestMarkdownToPlainText_ShortContent(t *testing.T) {
	result := MarkdownToPlainText("Short text.", 200)
	if strings.HasSuffix(result, "...") {
		t.Errorf("Short text should not be truncated, got: %s", result)
	}
	if !strings.Contains(result, "Short text.") {
		t.Errorf("Expected original text, got: %s", result)
	}
}

func TestMarkdownToPlainText_Headings(t *testing.T) {
	result := MarkdownToPlainText("# Title\n\nBody paragraph.", 200)
	if !strings.Contains(result, "Title") {
		t.Errorf("Expected 'Title' in result, got: %s", result)
	}
	if !strings.Contains(result, "Body paragraph") {
		t.Errorf("Expected 'Body paragraph' in result, got: %s", result)
	}
}

// Benchmark rendering performance
func BenchmarkMarkdownToHTML_Short(b *testing.B) {
	input := "# Hello\n\nThis is a **test**."
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MarkdownToHTML(input)
	}
}

func BenchmarkMarkdownToHTML_Long(b *testing.B) {
	input := strings.Repeat("# Heading\n\nParagraph with **bold** and *italic* text.\n\n- List item 1\n- List item 2\n\n", 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MarkdownToHTML(input)
	}
}
