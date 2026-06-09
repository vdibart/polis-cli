package url

import "testing"

func TestNormalizeToMD(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "already .md extension",
			input:    "https://alice.polis.pub/posts/20260127/hello-world.md",
			expected: "https://alice.polis.pub/posts/20260127/hello-world.md",
		},
		{
			name:     "convert .html to .md",
			input:    "https://alice.polis.pub/posts/20260127/hello-world.html",
			expected: "https://alice.polis.pub/posts/20260127/hello-world.md",
		},
		{
			name:     "no extension",
			input:    "https://alice.polis.pub/posts/20260127/hello-world",
			expected: "https://alice.polis.pub/posts/20260127/hello-world",
		},
		{
			name:     "with query params - .html",
			input:    "https://alice.polis.pub/posts/20260127/hello-world.html?foo=bar",
			expected: "https://alice.polis.pub/posts/20260127/hello-world.md?foo=bar",
		},
		{
			name:     "with fragment - .html",
			input:    "https://alice.polis.pub/posts/20260127/hello-world.html#section",
			expected: "https://alice.polis.pub/posts/20260127/hello-world.md#section",
		},
		{
			name:     "with query and fragment - .html",
			input:    "https://alice.polis.pub/posts/20260127/hello-world.html?foo=bar#section",
			expected: "https://alice.polis.pub/posts/20260127/hello-world.md?foo=bar#section",
		},
		{
			name:     "relative path .html",
			input:    "/posts/20260127/hello-world.html",
			expected: "/posts/20260127/hello-world.md",
		},
		{
			name:     "relative path .md",
			input:    "/posts/20260127/hello-world.md",
			expected: "/posts/20260127/hello-world.md",
		},
		{
			name:     "comment URL .html",
			input:    "https://bob.polis.pub/comments/20260128/alice-hello-world-20260128.html",
			expected: "https://bob.polis.pub/comments/20260128/alice-hello-world-20260128.md",
		},
		{
			name:     "http URL",
			input:    "http://alice.polis.pub/posts/20260127/hello-world.html",
			expected: "http://alice.polis.pub/posts/20260127/hello-world.md",
		},
		{
			name:     ".html in middle of path - not changed",
			input:    "https://alice.polis.pub/posts/html-test/hello-world.md",
			expected: "https://alice.polis.pub/posts/html-test/hello-world.md",
		},
		{
			name:     "uppercase .HTML - not changed (case sensitive)",
			input:    "https://alice.polis.pub/posts/20260127/hello-world.HTML",
			expected: "https://alice.polis.pub/posts/20260127/hello-world.HTML",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeToMD(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeToMD(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCommentContentURL(t *testing.T) {
	got := CommentContentURL("https://alice.polis.pub", "20260302", "reply-20260302")
	want := "https://alice.polis.pub/content/pub.polis.core/comment/20260302/reply-20260302.md"
	if got != want {
		t.Errorf("CommentContentURL = %q, want %q", got, want)
	}
	// Trailing slash on baseURL is trimmed.
	if got := CommentContentURL("https://alice.polis.pub/", "20260302", "x"); got != "https://alice.polis.pub/content/pub.polis.core/comment/20260302/x.md" {
		t.Errorf("trailing-slash baseURL not trimmed: %q", got)
	}
}

func TestCommentURLToContentRel(t *testing.T) {
	const want = "content/pub.polis.core/comment/20260302/reply.md"
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"mount .md", "https://a.polis.pub/comments/20260302/reply.md", want},
		{"mount .html", "https://a.polis.pub/comments/20260302/reply.html", want},
		{"canonical .md", "https://a.polis.pub/content/pub.polis.core/comment/20260302/reply.md", want},
		{"canonical .html", "https://a.polis.pub/content/pub.polis.core/comment/20260302/reply.html", want},
		{"relative mount", "/comments/20260302/reply.md", want},
		{"no extension", "https://a.polis.pub/comments/20260302/reply", want},
		{"neither form", "https://a.polis.pub/posts/20260302/slug.md", ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		if got := CommentURLToContentRel(tc.input); got != tc.want {
			t.Errorf("%s: CommentURLToContentRel(%q) = %q, want %q", tc.name, tc.input, got, tc.want)
		}
	}
}
