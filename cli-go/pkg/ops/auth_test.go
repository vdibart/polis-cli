package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// seedAPIKeyFile writes an APIKeyFile fixture directly to .polis/api-keys.json
// with the given hash entries. Used by ValidateAPIKey tests after the
// GenerateAPIKey/ListAPIKeys/RevokeAPIKey helpers were retired.
func seedAPIKeyFile(t *testing.T, siteDir string, entries []APIKey) {
	t.Helper()
	keyFile := &APIKeyFile{Keys: entries}
	data, err := json.Marshal(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apiKeysPath(siteDir), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKey(t *testing.T) {
	siteDir := t.TempDir()

	plaintext := "polis_test_token_abc123"
	seedAPIKeyFile(t, siteDir, []APIKey{
		{ID: "test", Name: "test-key", KeyHash: hashKey(plaintext)},
	})

	name, ok := ValidateAPIKey(siteDir, plaintext)
	if !ok {
		t.Error("expected key to be valid")
	}
	if name != "test-key" {
		t.Errorf("expected name 'test-key', got %q", name)
	}

	_, ok = ValidateAPIKey(siteDir, "polis_invalid_key")
	if ok {
		t.Error("expected invalid key to fail validation")
	}
}

func TestValidateAPIKey_NoKeyFile(t *testing.T) {
	siteDir := t.TempDir()
	// No api-keys.json written.
	_, ok := ValidateAPIKey(siteDir, "polis_some_key")
	if ok {
		t.Error("expected validation to fail when key file is missing")
	}
}

func TestValidateAPIKey_MultipleKeys(t *testing.T) {
	siteDir := t.TempDir()

	pt1 := "polis_token_one"
	pt2 := "polis_token_two"
	seedAPIKeyFile(t, siteDir, []APIKey{
		{ID: "k1", Name: "key-1", KeyHash: hashKey(pt1)},
		{ID: "k2", Name: "key-2", KeyHash: hashKey(pt2)},
	})

	name1, ok1 := ValidateAPIKey(siteDir, pt1)
	if !ok1 || name1 != "key-1" {
		t.Errorf("key-1 validation failed: ok=%v, name=%q", ok1, name1)
	}
	name2, ok2 := ValidateAPIKey(siteDir, pt2)
	if !ok2 || name2 != "key-2" {
		t.Errorf("key-2 validation failed: ok=%v, name=%q", ok2, name2)
	}
}
