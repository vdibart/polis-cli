package ops

import (
	"strings"
	"testing"
)

func TestGenerateAndValidateAPIKey(t *testing.T) {
	siteDir := t.TempDir()

	plaintext, key, err := GenerateAPIKey(siteDir, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(plaintext, "polis_") {
		t.Errorf("expected key to start with 'polis_', got %q", plaintext[:10])
	}
	if key.Name != "test-key" {
		t.Errorf("expected name 'test-key', got %q", key.Name)
	}
	if key.ID == "" {
		t.Error("expected non-empty ID")
	}
	if key.CreatedAt == "" {
		t.Error("expected non-empty CreatedAt")
	}

	// Validate the key
	name, ok := ValidateAPIKey(siteDir, plaintext)
	if !ok {
		t.Error("expected key to be valid")
	}
	if name != "test-key" {
		t.Errorf("expected name 'test-key', got %q", name)
	}

	// Invalid key should fail
	_, ok = ValidateAPIKey(siteDir, "polis_invalid_key")
	if ok {
		t.Error("expected invalid key to fail validation")
	}
}

func TestListAPIKeys(t *testing.T) {
	siteDir := t.TempDir()

	// No keys initially
	keys, err := ListAPIKeys(siteDir)
	if err != nil {
		t.Fatal(err)
	}
	if keys != nil {
		t.Errorf("expected nil for no keys, got %v", keys)
	}

	// Generate two keys
	GenerateAPIKey(siteDir, "key-1")
	GenerateAPIKey(siteDir, "key-2")

	keys, err = ListAPIKeys(siteDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	// Hashes should be stripped
	for _, k := range keys {
		if k.KeyHash != "" {
			t.Error("key hash should be stripped from listing")
		}
	}
}

func TestRevokeAPIKey(t *testing.T) {
	siteDir := t.TempDir()

	plaintext, key, err := GenerateAPIKey(siteDir, "to-revoke")
	if err != nil {
		t.Fatal(err)
	}

	// Key should be valid
	_, ok := ValidateAPIKey(siteDir, plaintext)
	if !ok {
		t.Fatal("key should be valid before revocation")
	}

	// Revoke it
	if err := RevokeAPIKey(siteDir, key.ID); err != nil {
		t.Fatal(err)
	}

	// Key should no longer be valid
	_, ok = ValidateAPIKey(siteDir, plaintext)
	if ok {
		t.Error("key should be invalid after revocation")
	}

	// Revoking non-existent key should error
	err = RevokeAPIKey(siteDir, "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent key")
	}
}

func TestMultipleKeysValidation(t *testing.T) {
	siteDir := t.TempDir()

	pt1, _, _ := GenerateAPIKey(siteDir, "key-1")
	pt2, _, _ := GenerateAPIKey(siteDir, "key-2")

	// Both should be valid
	name1, ok1 := ValidateAPIKey(siteDir, pt1)
	name2, ok2 := ValidateAPIKey(siteDir, pt2)

	if !ok1 || name1 != "key-1" {
		t.Errorf("key-1 validation failed: ok=%v, name=%q", ok1, name1)
	}
	if !ok2 || name2 != "key-2" {
		t.Errorf("key-2 validation failed: ok=%v, name=%q", ok2, name2)
	}
}
