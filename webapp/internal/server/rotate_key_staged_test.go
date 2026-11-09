package server

import (
	"bytes"
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
)

// rotationServer is a configured server pointed at a DS that counts calls and
// answers with the given status.
func rotationServer(t *testing.T, dsStatus int) (*Server, *atomic.Int32) {
	t.Helper()
	s := newConfiguredServer(t)
	var calls atomic.Int32
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(dsStatus)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ds.Close)
	s.DiscoveryURL = ds.URL
	// A real site has a genesis key history, dated from .well-known's created.
	wkPath := filepath.Join(s.DataDir, ".well-known", "polis")
	var wk map[string]interface{}
	raw, _ := os.ReadFile(wkPath)
	if err := json.Unmarshal(raw, &wk); err != nil {
		t.Fatal(err)
	}
	wk["created"] = "2026-01-01T00:00:00Z"
	// As `polis init` writes it: trimmed. The chain's genesis copies it
	// byte-for-byte, and a handover signs the trimmed key.
	wk["public_key"] = strings.TrimSpace(wk["public_key"].(string))
	raw, _ = json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(wkPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := site.WriteGenesisKeyHistory(s.Site()); err != nil {
		t.Fatal(err)
	}
	return s, &calls
}

func keyBytes(t *testing.T, s *Server) []byte {
	b, err := os.ReadFile(filepath.Join(s.DataDir, site.PrivateKeyKey))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ⛔⛔ A FAILURE WRITING THE NEW KEY LEAVES THE OLD KEY INTACT AND NOTHING
// ANNOUNCED. Before this, the DS was told first and the key was then written
// in place, so a crash or full disk at the write left an empty key while the
// network was already on the new one: an unrecoverable identity.
func TestAFailedKeyWriteAnnouncesNothingAndKeepsTheOldKey(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s, calls := rotationServer(t, http.StatusOK)
	before := keyBytes(t, s)
	keys := filepath.Join(s.DataDir, ".polis", "keys")
	if err := os.Chmod(keys, 0o500); err != nil { // the staging write cannot land
		t.Fatal(err)
	}
	defer os.Chmod(keys, 0o700)
	w := httptest.NewRecorder()
	s.handleRotateKey(w, httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil))
	if w.Code == http.StatusOK {
		t.Fatal("rotation reported success although the new key could not be written")
	}
	if calls.Load() != 0 {
		t.Errorf("⛔ the DS was told about a key that was never written (%d calls)", calls.Load())
	}
	if !bytes.Equal(keyBytes(t, s), before) {
		t.Error("⛔ the current key changed although the rotation failed")
	}
}

// ⭐ A staged key that does not parse is refused before anything is announced,
// and left exactly as it was: it is evidence of an interrupted rotation.
func TestAnUnreadableStagedKeyIsRefusedUntouched(t *testing.T) {
	s, calls := rotationServer(t, http.StatusOK)
	if err := os.WriteFile(filepath.Join(s.DataDir, site.StagedKeyKey), []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleRotateKey(w, httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil))
	if w.Code == http.StatusOK || calls.Load() != 0 {
		t.Errorf("status %d, DS calls %d; want a refusal and none", w.Code, calls.Load())
	}
	if b, _ := os.ReadFile(filepath.Join(s.DataDir, site.StagedKeyKey)); string(b) != "staged" {
		t.Error("⛔ the staged key from the interrupted rotation was overwritten")
	}
}

// ⛔⛔ A 5xx IS NOT A REFUSAL. The DS may have taken the new key before its
// answer failed, so the staged key is kept and the current key untouched.
func TestALostDSAnswerKeepsTheStagedKey(t *testing.T) {
	s, _ := rotationServer(t, http.StatusBadGateway)
	before := keyBytes(t, s)
	w := httptest.NewRecorder()
	s.handleRotateKey(w, httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d; want 503", w.Code)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, site.StagedKeyKey)); err != nil {
		t.Error("⛔ the staged key was deleted after a 5xx — it may be the only copy of the key the DS holds")
	}
	if !bytes.Equal(keyBytes(t, s), before) {
		t.Error("the current key changed although the rotation did not finish")
	}
}

// ⭐ Resuming: the DS already holds the staged key (an earlier answer was
// lost), so nothing is re-sent and the staged key is installed and published.
func TestARotationResumesWhenTheDSAlreadyHoldsTheStagedKey(t *testing.T) {
	s, _ := rotationServer(t, http.StatusOK)
	staged, stagedPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.DataDir, site.StagedKeyKey), staged, 0o600); err != nil {
		t.Fatal(err)
	}
	// ⭐ The answer that decides "already accepted" is a SIGNED one: the
	// server's DS client verifies it, as it does every DS read.
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
				map[string]any{"public_key": strings.TrimSpace(string(s.PublicKey)), "valid_until": "2026-10-09T00:00:00Z"},
				map[string]any{"public_key": strings.TrimSpace(string(stagedPub)), "valid_until": nil},
			}}
			// Round-trip so the canonical form is built from what the client parses.
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
	defer ds.Close()
	s.DiscoveryURL = ds.URL
	w := httptest.NewRecorder()
	s.handleRotateKey(w, httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("resume = %d: %s", w.Code, w.Body.String())
	}
	if posts.Load() != 0 {
		t.Errorf("the rotation was re-sent %d times although the DS already held the key", posts.Load())
	}
	if !bytes.Equal(keyBytes(t, s), staged) {
		t.Error("the staged key was not installed")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, site.StagedKeyKey)); !os.IsNotExist(err) {
		t.Error("the staged key was left after the resumed rotation")
	}
}

// ⭐ A refusal from the DS drops the staged key and leaves the current one.
func TestARefusedRotationDropsTheStagedKey(t *testing.T) {
	s, _ := rotationServer(t, http.StatusConflict)
	before := keyBytes(t, s)
	w := httptest.NewRecorder()
	s.handleRotateKey(w, httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil))
	if w.Code == http.StatusOK {
		t.Fatal("a refused rotation reported success")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, site.StagedKeyKey)); !os.IsNotExist(err) {
		t.Error("the staged key was left behind after a refusal")
	}
	if !bytes.Equal(keyBytes(t, s), before) {
		t.Error("⛔ the current key changed although the DS refused the rotation")
	}
}

// ⭐ A completed rotation leaves a 0600 key — even over a 0644 file — and no
// staged copy.
func TestARotatedKeyIsAlways0600AndNothingIsLeftStaged(t *testing.T) {
	s, _ := rotationServer(t, http.StatusOK)
	kp := filepath.Join(s.DataDir, site.PrivateKeyKey)
	if err := os.Chmod(kp, 0o644); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleRotateKey(w, httptest.NewRequest(http.MethodPost, "/api/rotate-key", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("rotate-key = %d: %s", w.Code, w.Body.String())
	}
	if fi, _ := os.Stat(kp); fi.Mode().Perm() != 0o600 {
		t.Errorf("the rotated key is %o; want 600", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, site.StagedKeyKey)); !os.IsNotExist(err) {
		t.Error("a staged key was left after a completed rotation")
	}
}
