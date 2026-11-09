package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// signedHistoryDS is a DS whose signed key history says current is the
// site's active key, and which counts rotation posts.
func signedHistoryDS(t *testing.T, retired, current string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	dsPriv, dsPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/polis":
			_ = json.NewEncoder(w).Encode(map[string]any{"public_key": string(dsPub), "key_id": "test-ds"})
			return
		case "/v1/sites/keys/history":
			body := map[string]any{"domain": "x", "keys": []any{
				map[string]any{"public_key": retired, "valid_until": "2026-10-09T00:00:00Z"},
				map[string]any{"public_key": current, "valid_until": nil},
			}}
			raw, _ := json.Marshal(body)
			var parsed map[string]any
			_ = json.Unmarshal(raw, &parsed)
			sig, err := signing.SignContent([]byte(discovery.BuildCanonicalJSON(parsed)), dsPriv)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			parsed["ds_signature"] = sig
			parsed["ds_key_id"] = "test-ds"
			_ = json.NewEncoder(w).Encode(parsed)
			return
		}
		posts.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ds.Close)
	return ds, &posts
}

func chainOf(t *testing.T, s *Server) *site.KeyHistoryBlock {
	t.Helper()
	block, err := site.LoadKeyHistory(s.Site())
	if err != nil || block == nil {
		t.Fatalf("load key history: %v", err)
	}
	return block
}

// ⛔⛔ A crash after the new key is installed and before the key history is
// written. The handler stages and installs through the same seam this test
// calls, then dies. The next rotation request must FINISH that rotation —
// signing the handover with the retired key kept on disk — and the published
// chain must verify.
func TestARotationCrashedBeforeTheHistoryWriteIsFinishedAndTheChainVerifies(t *testing.T) {
	s, _ := rotationServer(t, http.StatusOK)
	ctx := context.Background()
	oldPub := strings.TrimSpace(string(s.PublicKey))
	newPriv, newPubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := site.StageRotationKey(ctx, s.Site(), newPriv); err != nil {
		t.Fatal(err)
	}
	if err := site.InstallRotationKey(ctx, s.Site(), newPriv, newPubSSH); err != nil {
		t.Fatal(err)
	}
	// ✂ crash; the server restarts and loads the installed key.
	s.LoadKeys()

	ds, posts := signedHistoryDS(t, oldPub, strings.TrimSpace(string(newPubSSH)))
	s.DiscoveryURL = ds.URL
	w := httptest.NewRecorder()
	s.handleRotateKey(w, httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("resume = %d: %s", w.Code, w.Body.String())
	}
	if posts.Load() != 0 {
		t.Errorf("the rotation was re-sent %d times although the DS already held the key", posts.Load())
	}
	block := chainOf(t, s)
	if err := site.VerifyChain(block, polisurl.ExtractDomain(s.GetBaseURL())); err != nil {
		t.Fatalf("⛔ the published key history does not verify: %v", err)
	}
	if !site.SameSSHKey(block.Current.Key, string(newPubSSH)) {
		t.Errorf("the chain's head is %s; want the installed key", block.Current.Key)
	}
	for _, k := range []string{site.StagedKeyKey, site.PreviousKeyKey} {
		if _, err := os.Stat(filepath.Join(s.DataDir, k)); !os.IsNotExist(err) {
			t.Errorf("%s was left after the rotation finished", k)
		}
	}
}

// ⛔ The state an older build left after that crash — installed, staged copy
// gone, nothing recorded — is refused before the DS is told anything, and the
// chain is left exactly as it was.
func TestARotationFromAKeyThatIsNotPublishedIsRefused(t *testing.T) {
	s, calls := rotationServer(t, http.StatusOK)
	before := chainOf(t, s)
	newPriv, newPubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.DataDir, site.PrivateKeyKey), newPriv, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.DataDir, site.PublicKeyKey), newPubSSH, 0o644); err != nil {
		t.Fatal(err)
	}
	s.LoadKeys()

	w := httptest.NewRecorder()
	s.handleRotateKey(w, httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d; want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "does not match the public_key .well-known/polis publishes") {
		t.Errorf("the refusal should name the state: %s", w.Body.String())
	}
	if calls.Load() != 0 {
		t.Errorf("the DS was contacted %d times", calls.Load())
	}
	if after := chainOf(t, s); len(after.History) != len(before.History) || after.Current.Key != before.Current.Key {
		t.Error("⛔ a refused rotation changed the key history")
	}
}
