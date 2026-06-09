package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchLatestCommentsCtx_EmptyInputShortCircuits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("DS should not be called for empty input; got %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	latest, err := c.FetchLatestCommentsCtx(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(latest) != 0 {
		t.Errorf("expected empty map, got %v", latest)
	}
}

func TestFetchLatestCommentsCtx_NormalizesHtmlToMdAndRekeys(t *testing.T) {
	// Caller passes a .html post URL; the request must send the .md form,
	// and the returned map must be keyed by the caller's original .html form.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/content/comments/latest" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var body struct {
			URLs []string `json:"urls"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, u := range body.URLs {
			if u == "https://discover.polis.pub/posts/a.html" {
				t.Errorf("wire URL should be .md, got .html: %s", u)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"latest": map[string]interface{}{
				"https://discover.polis.pub/posts/a.md": map[string]interface{}{
					"url":           "https://bob.example/comments/20260525/x.md",
					"author_domain": "bob.example",
					"published":     "2026-05-25T00:00:00Z",
					"version":       "sha256:v1",
				},
			},
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	htmlURL := "https://discover.polis.pub/posts/a.html"
	latest, err := c.FetchLatestCommentsCtx(context.Background(), []string{htmlURL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lc, ok := latest[htmlURL]
	if !ok {
		t.Fatalf("expected result keyed by caller's .html URL %q, got %v", htmlURL, latest)
	}
	if lc.URL != "https://bob.example/comments/20260525/x.md" {
		t.Errorf("unexpected comment url: %q", lc.URL)
	}
	if lc.AuthorDomain != "bob.example" {
		t.Errorf("unexpected author_domain: %q", lc.AuthorDomain)
	}
	if lc.Published != "2026-05-25T00:00:00Z" {
		t.Errorf("unexpected published: %q", lc.Published)
	}
	if lc.Version != "sha256:v1" {
		t.Errorf("unexpected version: %q", lc.Version)
	}
}

func TestFetchLatestCommentsCtx_OmitsPostsWithoutComments(t *testing.T) {
	// DS omits posts with no comments. The client must not fail; the caller
	// treats a missing key as "no comment".
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"latest": map[string]interface{}{
				"https://x.example/a.md": map[string]interface{}{
					"url":           "https://y.example/comments/1.md",
					"author_domain": "y.example",
					"published":     "",
					"version":       "sha256:v2",
				},
			},
		})
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	urls := []string{"https://x.example/a.md", "https://x.example/b.md"}
	latest, err := c.FetchLatestCommentsCtx(context.Background(), urls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := latest["https://x.example/a.md"]; !ok {
		t.Errorf("expected a to be present")
	}
	if _, ok := latest["https://x.example/b.md"]; ok {
		t.Errorf("expected b to be absent from result map")
	}
}

func TestFetchLatestCommentsCtx_HandlesContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"latest": map[string]interface{}{}})
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.FetchLatestCommentsCtx(ctx, []string{"https://x.example/a.md"})
	if err == nil {
		t.Fatal("expected context-deadline error, got nil")
	}
}

func TestFetchLatestCommentsCtx_PropagatesDSError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"INVALID_PAYLOAD","message":"urls length exceeds cap"}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "")
	_, err := c.FetchLatestCommentsCtx(context.Background(), []string{"https://x.example/a.md"})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}
