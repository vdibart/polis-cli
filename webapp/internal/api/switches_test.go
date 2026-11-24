package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// switchSetup is testSetup with a Host whose switches the test controls, and a
// scoped key holding every scope a personal token can hold.
func switchSetup(t *testing.T, off map[string]bool) (*http.ServeMux, string, string) {
	t.Helper()
	siteDir := t.TempDir()
	for _, d := range []string{".polis/keys", ".well-known", "content/pub.polis.core/post", "content/pub.polis.core/comment",
		"content/pub.polis.core/follow", ".polis/bundles/pub.polis.core/comments/pending",
		".polis/bundles/pub.polis.core/comments/drafts", "posts", "comments", "site/snippets"} {
		os.MkdirAll(filepath.Join(siteDir, d), 0o755)
	}
	privKey, pubKey, _ := signing.GenerateKeypair()
	os.WriteFile(filepath.Join(siteDir, ".polis/keys/id_ed25519"), privKey, 0o600)
	wk, _ := json.Marshal(map[string]any{"version": "1.0", "public_key": string(pubKey), "base_url": "https://test.example.com"})
	os.WriteFile(filepath.Join(siteDir, ".well-known/polis"), wk, 0o644)
	bundles := map[string]*bundle.Bundle{"pub.polis.core": bundle.DefaultCoreBundle()}
	engine, err := ops.NewEngine(ops.EngineConfig{
		Resolver: resolve.New(storage.At(siteDir), bundles), Bundles: bundles,
		PrivateKey: privKey, PublicKey: pubKey, BaseURL: "https://test.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	fullKey := seedAPIKey(t, siteDir)
	scoped, _, err := ops.MintAPIKey(storage.At(siteDir), "agent", []string{
		apidesc.ScopeRead, apidesc.ScopePublish, apidesc.ScopeComment, apidesc.ScopeBless,
		apidesc.ScopeFollow, apidesc.ScopeTag, apidesc.ScopeAppearance, apidesc.ScopeProfile,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	SetupRoutesWithHost(mux, engine, storage.At(siteDir), &Host{SwitchedOff: func(name string) bool { return off[name] }})
	return mux, fullKey, scoped
}

// tokenOps are the described operations a token reaches (public ones ignore a
// token, and site-signed ones take none).
func tokenOps() []apidesc.Operation {
	var out []apidesc.Operation
	for _, op := range ops.NewBuiltinCoreHandler().Operations() {
		if op.Auth == apidesc.AuthPublic || op.Auth == apidesc.AuthSiteSigned {
			continue
		}
		out = append(out, op)
	}
	return out
}

func call(mux *http.ServeMux, method, path, token string) *httptest.ResponseRecorder {
	var body io.Reader
	if method != http.MethodGet {
		body = strings.NewReader("{}")
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func isSwitchedOff(w *httptest.ResponseRecorder) bool {
	return w.Code == http.StatusServiceUnavailable && w.Header().Get("Retry-After") == SwitchedOffRetryAfter &&
		strings.Contains(w.Body.String(), `"switched_off"`) && strings.Contains(w.Body.String(), SwitchedOffMessage)
}

// ⛔ CENSUS: every described operation a token reaches answers 503 to a scoped
// token while api-tokens is off. A new operation is covered the day it is
// described, because the switch sits in requireScope, right after a token is
// recognised, which every token request passes.
func TestEveryTokenOperationIsSwitchedOffByAPITokens(t *testing.T) {
	mux, fullKey, scoped := switchSetup(t, map[string]bool{SwitchAPITokens: true})
	described := tokenOps()
	if len(described) < 10 {
		t.Fatalf("only %d token operations described: the census would prove nothing", len(described))
	}
	for _, op := range described {
		path := strings.ReplaceAll(op.Path, "{id}", "x")
		if w := call(mux, op.Method, path, scoped); !isSwitchedOff(w) {
			t.Errorf("%s: %s %s with a scoped token while api-tokens is off = %d %s, want 503 switched_off with Retry-After",
				op.ID(), op.Method, path, w.Code, w.Body.String())
		}
		// ⭐ The owner's own full key is the owner, not a personal token.
		if w := call(mux, op.Method, path, fullKey); w.Code == http.StatusServiceUnavailable {
			t.Errorf("%s: the owner's full key was switched off by api-tokens: %s", op.ID(), w.Body.String())
		}
	}
}

// ⛔ CENSUS: every write a token can attempt answers 503 while agent-writes is
// off, and every read does not. Write-ness is the method, as it is for the
// grant slot, so a new write is covered the day it exists. Owner-only operations
// included: a token gets the same 503 there, not a 403.
func TestEveryTokenWriteIsSwitchedOffByAgentWrites(t *testing.T) {
	mux, _, scoped := switchSetup(t, map[string]bool{SwitchAgentWrites: true})
	writes, reads := 0, 0
	for _, op := range tokenOps() {
		path := strings.ReplaceAll(op.Path, "{id}", "x")
		w := call(mux, op.Method, path, scoped)
		if op.Method == http.MethodGet {
			reads++
			if w.Code == http.StatusServiceUnavailable {
				t.Errorf("%s: a read was switched off by agent-writes: %s", op.ID(), w.Body.String())
			}
			continue
		}
		writes++
		if !isSwitchedOff(w) {
			t.Errorf("%s: %s %s, a token write while agent-writes is off = %d %s, want 503", op.ID(), op.Method, path, w.Code, w.Body.String())
		}
	}
	if writes == 0 || reads == 0 {
		t.Fatalf("census saw %d writes and %d reads", writes, reads)
	}
}

func TestNothingIsSwitchedOffWithoutASwitch(t *testing.T) {
	mux, _, scoped := switchSetup(t, nil)
	for _, op := range tokenOps() {
		path := strings.ReplaceAll(op.Path, "{id}", "x")
		if w := call(mux, op.Method, path, scoped); w.Code == http.StatusServiceUnavailable {
			t.Errorf("%s: 503 with no switch off: %s", op.ID(), w.Body.String())
		}
	}
}

// ⛔ CENSUS: a token becomes a key in exactly these places. A new one must be
// reviewed for the switches (requireScope checks them itself; the others
// grant only reads and are gated where they are) before it is added here.
func TestTokensAreAcceptedOnlyWhereTheSwitchesAreChecked(t *testing.T) {
	want := map[string]int{"query.go": 1, "router.go": 2}
	re := regexp.MustCompile(`\.lookupToken\(`)
	entries, _ := os.ReadDir(".")
	got := map[string]int{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, _ := os.ReadFile(e.Name())
		if n := len(re.FindAll(b, -1)); n > 0 {
			got[e.Name()] = n
		}
	}
	for f, n := range got {
		if want[f] != n {
			t.Errorf("%s calls lookupToken %d time(s), the census expects %d: check the new caller against the api-tokens and agent-writes switches, then update this census", f, n, want[f])
		}
	}
	for f, n := range want {
		if got[f] != n {
			t.Errorf("%s: census expects %d lookupToken call(s), found %d", f, n, got[f])
		}
	}
}
