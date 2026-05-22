package blessing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Edge-case tests for the Grant flow. Existing grant_test.go covers
// the happy path + DS-failure + local-metadata-failure paths. These
// fill in:
//   - DS conflict responses (409 from the TOCTOU guard) propagate
//   - rate-limit (429) errors propagate with retry hint visible
//   - hook failures don't block the grant
//   - extractPostPath fallback when URL has no /posts/ prefix
//   - concurrent grants on the same post all land in the index

// ============================================================================
// DS response-status propagation
// ============================================================================

// 409 Conflict signals the TOCTOU guard fired (relationship status
// changed concurrently — e.g. someone else granted/denied between the
// caller's read and write). The error must propagate so the caller
// knows to retry with a fresh read.
func TestGrant_DSConflict_ErrorPropagates(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, _, _ := signing.GenerateKeypair()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Relationship status changed concurrently, please retry",
		})
	}))
	defer server.Close()

	client := &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := Grant(siteDir, &IncomingRequest{
		CommentURL: "https://alice.com/comments/r.md",
		InReplyTo:  "https://bob.com/posts/x.md",
	}, client, nil, privPEM)
	if err == nil {
		t.Fatal("expected error on DS 409 conflict")
	}
}

// 429 Rate Limited must propagate. The error wrapper should preserve
// the DS-side message so the caller can surface "try again in N
// seconds" to the user.
func TestGrant_DSRateLimit_ErrorPropagates(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, _, _ := signing.GenerateKeypair()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Rate limit exceeded. Try again in 60 seconds.",
		})
	}))
	defer server.Close()

	client := &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := Grant(siteDir, &IncomingRequest{
		CommentURL: "https://alice.com/comments/r.md",
		InReplyTo:  "https://bob.com/posts/x.md",
	}, client, nil, privPEM)
	if err == nil {
		t.Fatal("expected error on DS 429 rate limit")
	}
}

// ============================================================================
// extractPostPath fallback — URL has no /posts/
// ============================================================================

// When the in_reply_to URL doesn't contain "/posts/", extractPostPath
// returns the URL as-is. The Grant should still succeed (DS doesn't
// care about local path conventions); the local index just stores
// the URL string under the fallback "post path".
func TestGrant_TargetURLWithoutPostsPrefix(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, _, _ := signing.GenerateKeypair()

	commentDir := filepath.Join(siteDir, metadata.BundleContentDir, "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()

	client := &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	result, err := Grant(siteDir, &IncomingRequest{
		CommentURL: "https://alice.com/comments/r.md",
		InReplyTo:  "https://bob.com/other/path.md", // no /posts/
	}, client, nil, privPEM)
	if err != nil {
		t.Fatalf("Grant should still succeed: %v", err)
	}
	// PostPath falls back to the full URL
	if result.PostPath != "https://bob.com/other/path.md" {
		t.Errorf("PostPath = %q, want full URL fallback", result.PostPath)
	}
}

// ============================================================================
// Hook failure must not block grant
// ============================================================================

// If the configured post-comment hook errors (or doesn't exist), the
// DS grant has already succeeded — the hook is a side effect, not a
// precondition. Grant must return success.
func TestGrant_HookFailure_DoesNotBlockGrant(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, _, _ := signing.GenerateKeypair()

	if err := os.MkdirAll(filepath.Join(siteDir, metadata.BundleContentDir, "comment"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()
	client := &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}

	// Hook points at a nonexistent script — RunHook will fail.
	hookCfg := &hooks.HookConfig{
		PostComment: filepath.Join(siteDir, "no-such-hook.sh"),
	}

	result, err := Grant(siteDir, &IncomingRequest{
		CommentURL: "https://alice.com/comments/r.md",
		InReplyTo:  "https://bob.com/posts/x.md",
	}, client, hookCfg, privPEM)
	if err != nil {
		t.Fatalf("Grant should succeed despite hook failure: %v", err)
	}
	if !result.Success {
		t.Error("result.Success = false")
	}
}

// ============================================================================
// Concurrent grants — full DS + local-index correctness
// ============================================================================

// Pins down the post-R22 contract: concurrent Grants on the same
// post must (1) each succeed on DS, (2) each land in the local
// blessed.json index. The R22 fix (commit pending) added a
// package-level mutex in `cli-go/pkg/metadata` so that
// AddBlessedComment's load+modify+save cycle no longer loses entries
// when multiple goroutines race.
//
// Pre-fix history: this test was originally written to *document*
// the loss as a known API limitation — under contention it observed
// 0/12 entries surviving (the fixed `.tmp` filename in
// SaveBlessedComments amplified the race: first goroutine's rename
// consumed the only tmp file, every subsequent rename failed with
// ENOENT). The fix replaces the racy read-modify-write with a
// serialized critical section. See `plans/operational-hardening.md`
// R22 for the diagnosis + fix shape.
func TestGrant_ConcurrentGrants_AllEntriesLandInIndex(t *testing.T) {
	siteDir := t.TempDir()
	privPEM, _, _ := signing.GenerateKeypair()

	if err := os.MkdirAll(filepath.Join(siteDir, metadata.BundleContentDir, "comment"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	var dsCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dsCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()
	client := &discovery.Client{BaseURL: server.URL, HTTPClient: server.Client()}

	const grants = 12
	var wg sync.WaitGroup
	var grantErrs int32
	start := make(chan struct{})
	for i := 0; i < grants; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := &IncomingRequest{
				CommentURL:     "https://alice.com/comments/r-" + intStr(i) + ".md",
				CommentVersion: "v" + intStr(i),
				InReplyTo:      "https://bob.com/posts/20260127/hello.md",
			}
			if _, err := Grant(siteDir, req, client, nil, privPEM); err != nil {
				atomic.AddInt32(&grantErrs, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	// Contract assertion (1): every DS call succeeds. The DS is the
	// authoritative record of which blessings exist.
	if got := atomic.LoadInt32(&dsCalls); int(got) != grants {
		t.Errorf("DS should have been called %d times, got %d", grants, got)
	}

	// Contract assertion (2): Grant returns no errors.
	if got := atomic.LoadInt32(&grantErrs); got != 0 {
		t.Errorf("Grant returned %d errors under concurrent calls; expected 0", got)
	}

	// Contract assertion (3) [post-R22 fix]: every grant lands in the
	// local index. The metadata package serializes
	// AddBlessedComment's load+modify+save through a package-level
	// mutex, so concurrent callers no longer clobber each other's
	// writes. If this assertion ever fails again, the mutex was
	// bypassed — check for new call sites that use SaveBlessedComments
	// directly without going through Add/Remove.
	bc, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		t.Fatalf("LoadBlessedComments: %v", err)
	}
	var total int
	for _, post := range bc.Comments {
		if post.Post == "posts/20260127/hello.md" {
			total += len(post.Blessed)
		}
	}
	if total != grants {
		t.Errorf("local index kept %d/%d entries; expected all %d (R22 fix regressed?)", total, grants, grants)
	}
}

func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	var s []byte
	for n > 0 {
		s = append([]byte{byte('0' + n%10)}, s...)
		n /= 10
	}
	return string(s)
}
