package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchCommentCountsCtx_EmptyInputShortCircuits(t *testing.T) {
	// Empty input must not produce a DS request. Spin up a server that
	// fails the test if reached.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("DS should not be called for empty input; got %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	counts, err := c.FetchCommentCountsCtx(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("expected empty map, got %v", counts)
	}
}

func TestFetchCommentCountsCtx_NormalizesHtmlToMd(t *testing.T) {
	// The caller passes .html URLs; the request must send the .md form
	// (matching what DS stored at register time). The response is keyed
	// by .md but the returned map must be keyed by the caller's original
	// .html form.
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/content/comments/counts" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var body struct {
			URLs []string `json:"urls"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		receivedBody, _ = json.Marshal(body)

		// Verify the wire form is .md
		for _, u := range body.URLs {
			if u == "https://discover.polis.pub/posts/a.html" {
				t.Errorf("wire URL should be .md, got .html: %s", u)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"counts": map[string]int{
				"https://discover.polis.pub/posts/a.md": 3,
			},
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	htmlURL := "https://discover.polis.pub/posts/a.html"
	counts, err := c.FetchCommentCountsCtx(context.Background(), []string{htmlURL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := counts[htmlURL]; got != 3 {
		t.Errorf("expected counts[%q]=3, got %d (body sent: %s)", htmlURL, got, string(receivedBody))
	}
}

func TestFetchCommentCountsCtx_NormalizesBackslashes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URLs []string `json:"urls"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, u := range body.URLs {
			if u != "https://s13.nyc/content/post/a.md" {
				t.Errorf("expected backslash-normalized URL, got %q", u)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"counts": map[string]int{
				"https://s13.nyc/content/post/a.md": 1,
			},
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	original := "https://s13.nyc/content\\post\\a.html"
	counts, err := c.FetchCommentCountsCtx(context.Background(), []string{original})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := counts[original]; got != 1 {
		t.Errorf("expected counts[%q]=1, got %d", original, got)
	}
}

func TestFetchCommentCountsCtx_HandlesContextCancellation(t *testing.T) {
	// Slow DS — should be cancelled by the context timeout.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"counts": map[string]int{}})
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.FetchCommentCountsCtx(ctx, []string{"https://x.example/a.md"})
	if err == nil {
		t.Fatal("expected context-deadline error, got nil")
	}
}

func TestFetchCommentCountsCtx_OmitsMissingURLsAsZero(t *testing.T) {
	// DS response only includes URLs with non-zero counts. The client
	// must NOT fail when a requested URL is missing from the response;
	// the caller defaults missing keys to zero.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"counts": map[string]int{
				"https://x.example/a.md": 5,
			},
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	urls := []string{"https://x.example/a.md", "https://x.example/b.md"}
	counts, err := c.FetchCommentCountsCtx(context.Background(), urls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if counts["https://x.example/a.md"] != 5 {
		t.Errorf("expected 5 for a, got %d", counts["https://x.example/a.md"])
	}
	if _, has := counts["https://x.example/b.md"]; has {
		t.Errorf("expected b to be absent from result map, got %d", counts["https://x.example/b.md"])
	}
}

func TestFetchCommentCountsCtx_PropagatesDSError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"INVALID_PAYLOAD","message":"urls length exceeds cap"}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	_, err := c.FetchCommentCountsCtx(context.Background(), []string{"https://x.example/a.md"})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestNormalizeURLForDS(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://example.com/posts/a.html", "https://example.com/posts/a.md"},
		{"https://example.com/posts/a.md", "https://example.com/posts/a.md"},
		{"https://s13.nyc/content\\post\\a.html", "https://s13.nyc/content/post/a.md"},
		{"https://s13.nyc/content\\post\\a.md", "https://s13.nyc/content/post/a.md"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeURLForDS(c.in); got != c.want {
			t.Errorf("normalizeURLForDS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
