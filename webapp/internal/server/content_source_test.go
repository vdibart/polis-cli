package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/render"
)

// ⭐ The owner's interface reads a post's or comment's words to edit them
// through /v1 (post.get, comment.get), on any kind of instance, not by
// fetching a public file only some instances serve.
func TestTheSourceOfAPostOrCommentIsReadForEditing(t *testing.T) {
	s, h := queryRoutes(t)
	ctx := t.Context()
	put := func(key, body string) {
		if err := s.Site().Store().Put(ctx, key, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	put("content/pub.polis.core/post/20261113/hello.md", "---\ntitle: Hello\nsignature: x\n---\n\n# Hello\n\nThe words.\n")
	put("content/pub.polis.core/comment/20261113/reply.md", "---\nin-reply-to: y\n---\nA reply.\n")
	read := mint(t, s, "read")

	for kind, path := range map[string]string{
		"post":    "content/pub.polis.core/post/20261113/hello.md",
		"comment": "content/pub.polis.core/comment/20261113/reply.md",
	} {
		w := get(t, h, "/v1/content/"+kind+"/source?path="+url.QueryEscape(path), read)
		if w.Code != http.StatusOK {
			t.Fatalf("%s source = %d %s", kind, w.Code, w.Body.String())
		}
		var got map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if got["path"] != path {
			t.Errorf("%s: path = %q", kind, got["path"])
		}
		want := map[string]string{"post": "# Hello\n\nThe words.\n", "comment": "A reply.\n"}[kind]
		if got["markdown"] != want {
			t.Errorf("%s: markdown = %q, want %q (no frontmatter)", kind, got["markdown"], want)
		}
	}

	// ⛔ Nothing but a published source: no draft, no private file, no escape.
	for _, bad := range []string{
		"content/pub.polis.core/post/drafts/x.md",
		".polis/keys/id_ed25519",
		"content/pub.polis.core/post/20261113/../../../.polis/keys/id_ed25519.md",
		"content/pub.polis.core/comment/20261113/reply.md", // a comment, asked as a post
		"/etc/passwd",
	} {
		if w := get(t, h, "/v1/content/post/source?path="+url.QueryEscape(bad), read); w.Code != http.StatusBadRequest {
			t.Errorf("post source of %q = %d, want 400", bad, w.Code)
		}
	}
	if w := get(t, h, "/v1/content/post/source?path="+url.QueryEscape("content/pub.polis.core/post/20261113/gone.md"), read); w.Code != http.StatusNotFound {
		t.Errorf("a post that isn't there = %d, want 404", w.Code)
	}
	if w := get(t, h, "/v1/content/post/source?path="+url.QueryEscape("content/pub.polis.core/post/20261113/hello.md"), ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no credential = %d, want 401", w.Code)
	}
}

// ⭐ A post from another site is read whole through /v1 (post.body), so the
// owner's interface can open a card past the short excerpt a query carries,
// without calling anything outside the API.
func TestAnotherSitesPostIsReadWholeThroughTheAPI(t *testing.T) {
	s, h := queryRoutes(t)
	read := mint(t, s, "read")

	orig := render.DefaultHTTPClient
	render.DefaultHTTPClient = &http.Client{Transport: focusBodyRoundTripper{body: "First paragraph.\n\nSecond, with [a link](https://example.com/x)."}}
	defer func() { render.DefaultHTTPClient = orig }()

	w := get(t, h, "/v1/content/post/body?url="+url.QueryEscape("https://other.example/posts/20261120/a.html"), read)
	if w.Code != http.StatusOK {
		t.Fatalf("post body = %d %s", w.Code, w.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	for _, want := range []string{"<p>First paragraph.</p>", `href="https://example.com/x"`} {
		if !strings.Contains(got["body_html"], want) {
			t.Errorf("body_html = %q, want it to hold %q", got["body_html"], want)
		}
	}

	// A post that cannot be read is said plainly; the interface keeps its excerpt.
	render.DefaultHTTPClient = &http.Client{Transport: failingRoundTripper{}}
	if w := get(t, h, "/v1/content/post/body?url="+url.QueryEscape("https://gone.example/posts/20261120/b.html"), read); w.Code != http.StatusNotFound {
		t.Errorf("unreadable post = %d, want 404", w.Code)
	}
	for _, bad := range []string{"", "posts/20261120/a.html", "file:///etc/passwd"} {
		if w := get(t, h, "/v1/content/post/body?url="+url.QueryEscape(bad), read); w.Code != http.StatusBadRequest {
			t.Errorf("url %q = %d, want 400", bad, w.Code)
		}
	}
	// ⛔ Owner-only: the site fetches the address, so no credential, no fetch.
	if w := get(t, h, "/v1/content/post/body?url="+url.QueryEscape("https://other.example/posts/20261120/a.html"), ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no credential = %d, want 401", w.Code)
	}
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unreachable")
}
