package remote

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractBaseURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://example.com/posts/hello.md", "https://example.com"},
		{"https://example.com/", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"https://sub.example.com/path/to/file", "https://sub.example.com"},
		{"http://example.com/posts/hello.md", "http://example.com"},
	}

	for _, tt := range tests {
		result := ExtractBaseURL(tt.input)
		if result != tt.expected {
			t.Errorf("ExtractBaseURL(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestFetchPolicies_ParsesJSONL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policies/rules.jsonl" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintln(w, `{"active":true,"policy":"allow pub.polis.dm from following"}`)
		fmt.Fprintln(w, `{"active":true,"policy":"deny pub.polis.dm from all"}`)
		fmt.Fprintln(w, `{"active":false,"policy":"allow pub.polis.post from all"}`)
	}))
	defer ts.Close()

	client := NewClient()
	client.HTTPClient = ts.Client()
	policies, err := client.FetchPolicies(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(policies) != 3 {
		t.Fatalf("expected 3 policies, got %d", len(policies))
	}
	if policies[0].Rule != "allow pub.polis.dm from following" {
		t.Errorf("unexpected rule: %s", policies[0].Rule)
	}
	if !policies[0].Active {
		t.Error("expected first policy to be active")
	}
	if policies[2].Active {
		t.Error("expected third policy to be inactive")
	}
}

func TestFetchPolicies_404ReturnsEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewClient()
	client.HTTPClient = ts.Client()
	policies, err := client.FetchPolicies(ts.URL)
	if err != nil {
		t.Fatalf("expected no error on 404, got: %v", err)
	}
	if len(policies) != 0 {
		t.Errorf("expected empty slice, got %d policies", len(policies))
	}
}

func TestFetchFollowingList_ExtractsDomains(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/content/pub.polis.core/follow/following.json" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"version": "1.0",
			"following": []map[string]string{
				{"url": "https://alice.com", "added_at": "2026-01-01T00:00:00Z"},
				{"url": "https://bob.example.org", "added_at": "2026-01-02T00:00:00Z"},
			},
		})
	}))
	defer ts.Close()

	client := NewClient()
	client.HTTPClient = ts.Client()
	domains, err := client.FetchFollowingList(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(domains))
	}
	if domains[0] != "alice.com" {
		t.Errorf("expected alice.com, got %s", domains[0])
	}
	if domains[1] != "bob.example.org" {
		t.Errorf("expected bob.example.org, got %s", domains[1])
	}
}

func TestFetchFollowingList_404ReturnsEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := NewClient()
	client.HTTPClient = ts.Client()
	domains, err := client.FetchFollowingList(ts.URL)
	if err != nil {
		t.Fatalf("expected no error on 404, got: %v", err)
	}
	if len(domains) != 0 {
		t.Errorf("expected empty slice, got %d domains", len(domains))
	}
}

func TestClientCreation(t *testing.T) {
	client := NewClient()
	if client == nil {
		t.Error("NewClient() returned nil")
	}

	if client.HTTPClient == nil {
		t.Error("HTTPClient is nil")
	}
}

func TestNewClientWithHTTP_Nil(t *testing.T) {
	client := NewClientWithHTTP(nil)
	if client == nil {
		t.Fatal("NewClientWithHTTP(nil) returned nil")
	}
	if client.HTTPClient == nil {
		t.Error("expected fallback HTTPClient, got nil")
	}
}

func TestNewClientWithHTTP_Shared(t *testing.T) {
	shared := &http.Client{}
	client := NewClientWithHTTP(shared)
	if client.HTTPClient != shared {
		t.Error("expected shared HTTPClient to be used")
	}
}

func TestFetchContent_CacheHit(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte("hello world"))
	}))
	defer server.Close()

	cache := NewLRUCache(10)
	client := &Client{
		HTTPClient: server.Client(),
		Cache:      cache,
	}

	// First fetch — should hit the server
	content, err := client.FetchContent(server.URL + "/test.md")
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if content != "hello world" {
		t.Errorf("expected 'hello world', got %q", content)
	}
	if callCount != 1 {
		t.Errorf("expected 1 server call, got %d", callCount)
	}

	// Second fetch — should hit the cache
	content2, err := client.FetchContent(server.URL + "/test.md")
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if content2 != "hello world" {
		t.Errorf("expected 'hello world' from cache, got %q", content2)
	}
	if callCount != 1 {
		t.Errorf("expected still 1 server call (cache hit), got %d", callCount)
	}
}

func TestFetchContent_NoCache(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte("data"))
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client()}

	// Two fetches with no cache — both should hit the server
	client.FetchContent(server.URL + "/a")
	client.FetchContent(server.URL + "/a")
	if callCount != 2 {
		t.Errorf("expected 2 server calls without cache, got %d", callCount)
	}
}

func TestFetchContent_CacheSkipsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cache := NewLRUCache(10)
	client := &Client{HTTPClient: server.Client(), Cache: cache}

	_, err := client.FetchContent(server.URL + "/missing")
	if err == nil {
		t.Error("expected error for 404")
	}
	if cache.Len() != 0 {
		t.Error("cache should not store error responses")
	}
}
