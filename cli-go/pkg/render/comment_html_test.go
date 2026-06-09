package render

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// commentBodyRoundTripper returns a comment .md (frontmatter + body) for any
// GET, so LoadOrFetchCommentHTML can succeed without real network I/O.
type commentBodyRoundTripper struct{ body string }

func (c commentBodyRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	md := "---\ntimestamp: 2026-05-25T00:00:00Z\nin_reply_to: https://discover.polis.pub/posts/a.md\n---\n\n" + c.body + "\n"
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(md)),
		Header:     make(http.Header),
	}, nil
}

func TestLoadOrFetchCommentHTML_RendersBody(t *testing.T) {
	dir := t.TempDir()
	// Public-looking domain passes the replyURLAllowed SSRF gate.
	commentURL := "https://bob.example.com/comments/20260525/render-test.md"

	orig := DefaultHTTPClient
	DefaultHTTPClient = &http.Client{Transport: commentBodyRoundTripper{body: "Hello from the latest comment."}}
	defer func() { DefaultHTTPClient = orig }()

	html, ok := LoadOrFetchCommentHTML(dir, commentURL, 0)
	if !ok {
		t.Fatal("expected ok=true for a fetchable comment with a body")
	}
	if !strings.Contains(html, "Hello from the latest comment.") {
		t.Errorf("rendered HTML missing body text: %q", html)
	}
	// MarkdownToHTML wraps paragraphs — confirm it actually rendered, not raw md.
	if !strings.Contains(html, "<p>") {
		t.Errorf("expected rendered HTML (paragraph tag), got: %q", html)
	}
}

func TestLoadOrFetchCommentHTML_FetchFailureDegrades(t *testing.T) {
	dir := t.TempDir()
	commentURL := "https://bob.example.com/comments/20260525/dead-origin.md"

	orig := DefaultHTTPClient
	DefaultHTTPClient = &http.Client{Transport: errAllRoundTripper{}}
	defer func() { DefaultHTTPClient = orig }()

	html, ok := LoadOrFetchCommentHTML(dir, commentURL, 0)
	if ok {
		t.Errorf("expected ok=false on fetch failure, got html=%q", html)
	}
	if html != "" {
		t.Errorf("expected empty html on failure, got %q", html)
	}
}

func TestLoadOrFetchCommentHTML_BlocksSSRFTarget(t *testing.T) {
	dir := t.TempDir()
	// A loopback target must be rejected by replyURLAllowed before any fetch.
	orig := DefaultHTTPClient
	DefaultHTTPClient = &http.Client{Transport: commentBodyRoundTripper{body: "should never be reached"}}
	defer func() { DefaultHTTPClient = orig }()

	_, ok := LoadOrFetchCommentHTML(dir, "http://127.0.0.1/comments/20260525/x.md", 0)
	if ok {
		t.Error("expected SSRF target to be blocked (ok=false)")
	}
}
