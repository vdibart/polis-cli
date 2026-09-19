package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// remotePostFixture serves a remote site over TLS and points the test server's
// shared HTTP client at it, so handleRemotePost fetches from it for real.
func remotePostFixture(t *testing.T, routes map[string]string) (*Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range routes {
		body := body
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		})
	}
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)

	s := newTestServer(t)
	s.SharedHTTPClient = ts.Client()
	return s, ts.URL
}

func getRemotePost(t *testing.T, s *Server, postURL string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/remote/post?url="+url.QueryEscape(postURL), nil)
	w := httptest.NewRecorder()
	s.handleRemotePost(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// A remote host that only serves HTML (both .md and .html answer with a page)
// must never have that page's markup handed to the SPA, which innerHTMLs it.
func TestHandleRemotePost_HTMLOnlyPageNeverReturnsRemoteMarkup(t *testing.T) {
	page := `<!DOCTYPE html><html><body><main><h1>Hi</h1>` +
		`<script>alert(1)</script><img src=x onerror="alert(2)">` +
		`<a href="javascript:alert(3)">x</a></main></body></html>`
	s, base := remotePostFixture(t, map[string]string{
		"/posts/evil.md":   page,
		"/posts/evil.html": page,
	})

	resp := getRemotePost(t, s, base+"/posts/evil.md")
	for _, field := range []string{"content", "raw"} {
		got, _ := resp[field].(string)
		for _, bad := range []string{"<script", "onerror", "javascript:", "<img"} {
			if strings.Contains(strings.ToLower(got), bad) {
				t.Errorf("%s contains %q: %s", field, bad, got)
			}
		}
	}
	content, _ := resp["content"].(string)
	if !strings.Contains(content, base+"/posts/evil.md") {
		t.Errorf("expected a link to the original page, got %q", content)
	}
}

// Markdown sources keep rendering, sanitised by the shared render policy.
func TestHandleRemotePost_MarkdownRendersAndIsSanitised(t *testing.T) {
	s, base := remotePostFixture(t, map[string]string{
		"/posts/hello.md": "---\ntitle: Hello\n---\n# Hello World\n\nA *normal* post.\n\n<script>alert(1)</script>\n",
	})

	resp := getRemotePost(t, s, base+"/posts/hello.md")
	content, _ := resp["content"].(string)
	if !strings.Contains(content, "Hello World</h1>") || !strings.Contains(content, "<em>normal</em>") {
		t.Errorf("expected rendered markdown, got %q", content)
	}
	if strings.Contains(content, "<script") {
		t.Errorf("script survived rendering: %q", content)
	}
}
