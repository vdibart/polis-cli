// Tests for the retry layer wrapped around DS RegisterContent in RegisterPost.
//
// The retry exists to mask transient DS failures — most notably the
// well-known fetch timeout when DS and the author are on the same Fly
// machine (project_known_issues.md). Without retry, the post lands on
// the author's site but never reaches followers' feeds.
//
// These tests are slow (~3-6s) because the retry delay is hardcoded
// at 3 seconds. They are guarded by testing.Short.

package publish

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// setupDataDir builds the minimum filesystem state RegisterPost needs:
// .well-known/polis with email + a DS registration marker so
// IsRegisteredLocally returns true.
func setupDataDir(t *testing.T, dsURL string) (dataDir string, privKey []byte) {
	t.Helper()
	dataDir = t.TempDir()

	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755); err != nil {
		t.Fatalf("mkdir .well-known: %v", err)
	}
	wk := map[string]interface{}{
		"public_key": "ssh-ed25519 AAAA...",
		"email":      "alice@example.com",
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), data, 0644); err != nil {
		t.Fatalf("write .well-known/polis: %v", err)
	}

	if err := discovery.WriteRegistrationMarker(dataDir, dsURL, "alice.example", ""); err != nil {
		t.Fatalf("WriteRegistrationMarker: %v", err)
	}

	return dataDir, priv
}

// TestRegisterPost_RetriesOnTransientFailure documents the retry contract:
// when DS returns a 5xx on the first attempt and 200 on the second,
// RegisterPost must succeed (no error) and DS must have been called
// exactly twice.
//
// The retry layer's purpose is to mask transient failures — without it,
// posts silently fail to register (project_known_issues.md).
func TestRegisterPost_RetriesOnTransientFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow test: 3s retry delay")
	}

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Failed to fetch public key for signature verification",
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"status":  "created",
		})
	}))
	defer server.Close()

	dataDir, privKey := setupDataDir(t, server.URL)

	cfg := &DiscoveryConfig{
		DiscoveryURL: server.URL,
		DiscoveryKey: "test-key",
		BaseURL:      "https://alice.example",
	}

	result := &PublishResult{
		Success: true,
		Path:    "content/pub.polis.core/post/hello.md",
		Title:   "Hello",
		Version: "sha256:" + "a" + "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcde",
	}

	if err := RegisterPost(dataDir, result, privKey, cfg); err != nil {
		t.Fatalf("expected retry to mask transient failure, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected exactly 2 DS calls (1 fail + 1 retry), got %d", got)
	}
}

// TestRegisterPost_GivesUpAfterRetry verifies that the retry layer has
// a bounded attempt count — when both attempts fail, the error bubbles
// up to the caller. Without an upper bound the publish flow could hang
// indefinitely.
func TestRegisterPost_GivesUpAfterRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("slow test: 3s retry delay")
	}

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error": "upstream timeout"}`))
	}))
	defer server.Close()

	dataDir, privKey := setupDataDir(t, server.URL)

	cfg := &DiscoveryConfig{
		DiscoveryURL: server.URL,
		DiscoveryKey: "test-key",
		BaseURL:      "https://alice.example",
	}

	result := &PublishResult{
		Success: true,
		Path:    "content/pub.polis.core/post/hello.md",
		Title:   "Hello",
		Version: "sha256:" + "11111111111111111111111111111111" + "11111111111111111111111111111111",
	}

	err := RegisterPost(dataDir, result, privKey, cfg)
	if err == nil {
		t.Fatal("expected error when DS fails both attempts, got nil")
	}
	got := atomic.LoadInt32(&calls)
	if got != 2 {
		t.Errorf("expected exactly 2 attempts before giving up, got %d", got)
	}
}

// TestRegisterPost_SucceedsOnFirstAttempt — no retry path should fire
// when DS returns 200 immediately. Used to detect over-retry (where a
// future refactor accidentally adds attempts that weren't needed).
func TestRegisterPost_SucceedsOnFirstAttempt(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"status":  "created",
		})
	}))
	defer server.Close()

	dataDir, privKey := setupDataDir(t, server.URL)

	cfg := &DiscoveryConfig{
		DiscoveryURL: server.URL,
		DiscoveryKey: "test-key",
		BaseURL:      "https://alice.example",
	}

	result := &PublishResult{
		Success: true,
		Path:    "content/pub.polis.core/post/hello.md",
		Title:   "Hello",
		Version: "sha256:" + "22222222222222222222222222222222" + "22222222222222222222222222222222",
	}

	if err := RegisterPost(dataDir, result, privKey, cfg); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected exactly 1 DS call on success path, got %d", got)
	}
}
