package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestHandleUpdateAuthorName_PreservesMessagesKey is the regression for the
// well-known data-loss class found on the landlord (mayoinmotion): editing a
// profile field via the webapp must NOT erase public_key_messages (the published
// DM key), which the WellKnown struct doesn't model. Before the fix, the typed
// LoadWellKnown→SaveWellKnown round-trip dropped it, leaving the tenant unable to
// receive DMs and firing judge.alert.wellknown_change.
func TestHandleUpdateAuthorName_PreservesMessagesKey(t *testing.T) {
	s := newConfiguredServer(t)
	wkPath := filepath.Join(s.DataDir, ".well-known", "polis")

	// Re-seed the identity doc WITH a published messages key.
	seed := map[string]interface{}{
		"version":     "polis-cli-go/0.63.0",
		"public_key":  string(s.PublicKey),
		"author_name": "old name",
		"created":     "2026-01-01T00:00:00Z",
		"public_key_messages": map[string]interface{}{
			"current": map[string]interface{}{"epoch": 1, "key": "MSGKEYB64", "sig": "SIGB64"},
		},
	}
	data, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(wkPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	body := jsonBody(t, map[string]string{"author_name": "new name"})
	req := httptest.NewRequest(http.MethodPost, "/api/author-name", body)
	w := httptest.NewRecorder()
	s.handleUpdateAuthorName(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	raw, _ := os.ReadFile(wkPath)
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("reparse well-known: %v", err)
	}
	if got["author_name"] != "new name" {
		t.Errorf("author_name not updated: %v", got["author_name"])
	}
	if _, ok := got["public_key_messages"]; !ok {
		t.Fatal("handleUpdateAuthorName dropped public_key_messages — tenant can no longer receive DMs")
	}
}
