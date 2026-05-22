// Tests for Client.RegisterContent error surfaces.
//
// These exist to give the retry layer in publish.RegisterPost (and any
// future retry wrappers) a known taxonomy of failure modes to drive
// retry decisions off of: network errors, 5xx, 4xx, timeouts, and
// malformed responses.

package discovery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testRegisterRequest() *ContentRegisterRequest {
	return &ContentRegisterRequest{
		Type:    "pub.polis.post",
		URL:     "https://alice.example/posts/hello.md",
		Version: "sha256:" + strings.Repeat("a", 64),
		Author:  "alice.example",
		Metadata: map[string]interface{}{
			"title":           "Hello",
			"published_at":    "2026-05-21T00:00:00Z",
			"last_updated_at": "2026-05-21T00:00:00Z",
		},
		Signature: "fake-sig",
	}
}

func TestRegisterContent_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/content" {
			t.Errorf("expected /v1/content, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("expected Authorization header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Content registered",
			"type":    "pub.polis.post",
			"url":     "https://alice.example/posts/hello.md",
			"status":  "created",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	resp, err := client.RegisterContent(testRegisterRequest())
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Errorf("expected Success=true, got %+v", resp)
	}
}

// 5xx server errors should propagate as errors. These are exactly the
// cases the retry wrapper should retry — e.g. DS timing out fetching
// the author's well-known/polis.
func TestRegisterContent_ServerError5xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "DS timeout fetching public key",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	_, err := client.RegisterContent(testRegisterRequest())
	if err == nil {
		t.Fatal("expected error for 5xx response, got nil")
	}
	if !strings.Contains(err.Error(), "timeout fetching public key") {
		t.Errorf("expected error to surface DS error message, got: %v", err)
	}
}

// 4xx client errors (sig invalid, validation failure) must NOT be
// retried — the retry layer should be able to distinguish these from
// 5xx. Here we just verify the error contains the DS-side message so
// callers can route on it.
func TestRegisterContent_ClientError4xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Signature verification failed",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	_, err := client.RegisterContent(testRegisterRequest())
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "Signature verification failed") {
		t.Errorf("expected sig-failure surfaced, got: %v", err)
	}
}

// Network failures (DS unreachable) must produce errors. This is the
// other class the retry wrapper exists for.
func TestRegisterContent_NetworkError(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "test-key") // unreachable
	_, err := client.RegisterContent(testRegisterRequest())
	if err == nil {
		t.Error("expected network error, got nil")
	}
}

// Request timeouts should surface as errors. Forces the request to take
// longer than the HTTP client's timeout.
func TestRegisterContent_RequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the client timeout
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	client.HTTPClient = &http.Client{Timeout: 50 * time.Millisecond}

	_, err := client.RegisterContent(testRegisterRequest())
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// 502/503/504 are the gateway-tier failures most relevant to retry
// decisions (Fly edge timeout, upstream proxy errors). Verify the
// client surfaces each.
func TestRegisterContent_GatewayErrors(t *testing.T) {
	cases := []int{
		http.StatusBadGateway,         // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout,     // 504
	}
	for _, code := range cases {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				// Intentionally no JSON body — gateways often return plain text.
				w.Write([]byte("upstream broken"))
			}))
			defer server.Close()

			client := NewClient(server.URL, "test-key")
			_, err := client.RegisterContent(testRegisterRequest())
			if err == nil {
				t.Errorf("status %d: expected error, got nil", code)
			}
		})
	}
}

// Malformed (non-JSON) response body should surface as an error rather
// than panic or silently succeed.
func TestRegisterContent_MalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html>not json</html>"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	_, err := client.RegisterContent(testRegisterRequest())
	if err == nil {
		t.Error("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("expected parse-response error, got: %v", err)
	}
}

// TestRegisterContent_TransientThenSuccess models the actual retry
// scenario the wrapper in publish.RegisterPost is built for: first
// call fails (e.g. DS times out fetching well-known), second call
// succeeds (cache warmed, or upstream recovered).
//
// At the bare client level there's no retry — this test just confirms
// each call independently observes the changing server state, which is
// the contract the retry wrapper relies on.
func TestRegisterContent_TransientThenSuccess(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "transient"})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"status":  "created",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")

	// First attempt fails.
	if _, err := client.RegisterContent(testRegisterRequest()); err == nil {
		t.Fatal("first attempt: expected error, got nil")
	}
	// Second attempt succeeds.
	resp, err := client.RegisterContent(testRegisterRequest())
	if err != nil {
		t.Fatalf("second attempt: expected success, got %v", err)
	}
	if !resp.Success {
		t.Errorf("second attempt: expected Success=true, got %+v", resp)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected exactly 2 calls, got %d", got)
	}
}
