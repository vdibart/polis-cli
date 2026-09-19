package cmd

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// TestRotateKeyUpdatesWellKnown verifies the JSON update logic used by rotate-key
// to update .well-known/polis with the new public key.
func TestRotateKeyUpdatesWellKnown(t *testing.T) {
	dir := t.TempDir()

	// Set up a minimal .well-known/polis
	wellKnownDir := filepath.Join(dir, ".well-known")
	if err := os.MkdirAll(wellKnownDir, 0755); err != nil {
		t.Fatal(err)
	}

	originalWK := map[string]interface{}{
		"base_url":    "https://example.com",
		"public_key":  "ssh-ed25519 AAAA_OLD_KEY test@example.com",
		"site_title":  "My Site",
		"author_name": "Test Author",
		"email":       "test@example.com",
	}
	data, _ := json.MarshalIndent(originalWK, "", "  ")
	wellKnownPath := filepath.Join(wellKnownDir, "polis")
	if err := os.WriteFile(wellKnownPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Generate a new keypair
	_, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the rotate-key update logic
	wkData, err := os.ReadFile(wellKnownPath)
	if err != nil {
		t.Fatal(err)
	}

	var wkJSON map[string]interface{}
	if err := json.Unmarshal(wkData, &wkJSON); err != nil {
		t.Fatal(err)
	}
	wkJSON["public_key"] = strings.TrimSpace(string(pubSSH))
	updatedWK, err := json.MarshalIndent(wkJSON, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wellKnownPath, append(updatedWK, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	// Read back and verify
	readBack, err := os.ReadFile(wellKnownPath)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(readBack, &result); err != nil {
		t.Fatalf("failed to parse updated .well-known/polis: %v", err)
	}

	// Verify the public key was updated
	newKey, ok := result["public_key"].(string)
	if !ok {
		t.Fatal("public_key not found in updated .well-known/polis")
	}
	if newKey == "ssh-ed25519 AAAA_OLD_KEY test@example.com" {
		t.Error("public_key was not updated - still has old value")
	}
	if !strings.HasPrefix(newKey, "ssh-ed25519 ") {
		t.Errorf("expected public_key to start with 'ssh-ed25519 ', got: %s", newKey)
	}

	// Verify other fields were preserved
	if result["base_url"] != "https://example.com" {
		t.Errorf("base_url was modified: %v", result["base_url"])
	}
	if result["site_title"] != "My Site" {
		t.Errorf("site_title was modified: %v", result["site_title"])
	}
	if result["author_name"] != "Test Author" {
		t.Errorf("author_name was modified: %v", result["author_name"])
	}
	if result["email"] != "test@example.com" {
		t.Errorf("email was modified: %v", result["email"])
	}
}

// TestRotateKeyRepublishesTheDIDDocument covers the rotation half of the
// did:web contract: did:web carries no key history, so the document must state
// the key that is current or a resolver will verify against a retired one.
//
// It mirrors TestRotateKeyUpdatesWellKnown's shape — the handler itself talks
// to the discovery service and exits the process — but the publication step is
// the real site.PublishDIDDocument, so the assertion is about production code
// rather than a restatement of it.
func TestRotateKeyRepublishesTheDIDDocument(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wellKnownPath := filepath.Join(dir, ".well-known", "polis")

	oldPriv, oldPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_ = oldPriv
	wk := map[string]interface{}{
		"version":     "2",
		"public_key":  strings.TrimSpace(string(oldPub)),
		"author_name": "Test Author",
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(wellKnownPath, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	if err := site.PublishDIDDocument(dir, "example.com"); err != nil {
		t.Fatalf("publish before rotation: %v", err)
	}
	before := readDIDKey(t, dir)

	// Rotate: new keypair into .well-known/polis, exactly as the handler does.
	_, newPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	wk["public_key"] = strings.TrimSpace(string(newPub))
	data, _ = json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(wellKnownPath, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if err := site.PublishDIDDocument(dir, "example.com"); err != nil {
		t.Fatalf("publish after rotation: %v", err)
	}
	after := readDIDKey(t, dir)

	if after == before {
		t.Error("did.json still states the old key after rotation")
	}
	wantPub, err := signing.ParsePublicKey(newPub)
	if err != nil {
		t.Fatal(err)
	}
	if after != base64.RawURLEncoding.EncodeToString(wantPub) {
		t.Error("did.json does not state the site's new key")
	}
}

// readDIDKey returns verificationMethod[0].publicKeyJwk.x from the published
// document.
func readDIDKey(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".well-known", "did.json"))
	if err != nil {
		t.Fatalf("read did.json: %v", err)
	}
	var doc struct {
		VerificationMethod []struct {
			PublicKeyJwk struct {
				X string `json:"x"`
			} `json:"publicKeyJwk"`
		} `json:"verificationMethod"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse did.json: %v", err)
	}
	if len(doc.VerificationMethod) != 1 {
		t.Fatalf("verificationMethod has %d entries, want 1", len(doc.VerificationMethod))
	}
	return doc.VerificationMethod[0].PublicKeyJwk.X
}
