package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

func mcpStatus(s *Server, token, addr string) int {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.RemoteAddr = addr + ":1234"
	w := httptest.NewRecorder()
	s.handleMCP(w, req)
	return w.Code
}

// ⛔ An address that keeps sending bad keys is refused before its key is
// looked at; another address, and the owner's AI with a good key, are not.
func TestMCPRefusesAnAddressThatKeepsFailing(t *testing.T) {
	s, _ := mcpSite(t)
	token, _, err := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < attemptsPerAddress; i++ {
		if code := mcpStatus(s, "polis_wrong", "203.0.113.9"); code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", i, code)
		}
	}
	if code := mcpStatus(s, "polis_wrong", "203.0.113.9"); code != http.StatusTooManyRequests {
		t.Errorf("past its share = %d, want 429", code)
	}
	if code := mcpStatus(s, token, "198.51.100.7"); code == http.StatusUnauthorized || code == http.StatusTooManyRequests {
		t.Errorf("a good key from another address = %d", code)
	}
	// Successes are never counted: many good calls from one address pass.
	for i := 0; i < 3*attemptsPerAddress; i++ {
		if code := mcpStatus(s, token, "198.51.100.8"); code == http.StatusTooManyRequests {
			t.Fatalf("good call %d was limited", i)
		}
	}
}

// The key cache never outlives a mint or a revoke made by this instance.
func TestMCPKeyCacheFollowsThisInstancesWrites(t *testing.T) {
	s, _ := mcpSite(t)
	if code := mcpStatus(s, "polis_none", "198.51.100.1"); code != http.StatusUnauthorized {
		t.Fatalf("no keys = %d", code)
	}
	token, key, _ := ops.MintAPIKey(s.Site(), "Kay", []string{ops.ScopeRender})
	if code := mcpStatus(s, token, "198.51.100.1"); code == http.StatusUnauthorized {
		t.Fatal("a key minted after the cache filled was refused")
	}
	if err := ops.RevokeAPIKey(s.Site(), key.ID); err != nil {
		t.Fatal(err)
	}
	if code := mcpStatus(s, token, "198.51.100.1"); code != http.StatusUnauthorized {
		t.Errorf("a revoked key = %d, want 401", code)
	}
}
