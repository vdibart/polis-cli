package ops

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const apiKeysFilename = "api-keys.json"

// APIKey represents a stored API key.
type APIKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	KeyHash   string `json:"key_hash"`
	CreatedAt string `json:"created_at"`
}

// APIKeyFile is the on-disk format for api-keys.json.
type APIKeyFile struct {
	Keys []APIKey `json:"keys"`
}

// ValidateAPIKey checks if a plaintext key matches any stored key.
// Returns the matching key's name, or empty string if invalid.
func ValidateAPIKey(siteDir, plaintext string) (string, bool) {
	keysPath := apiKeysPath(siteDir)
	keyFile, err := loadAPIKeys(keysPath)
	if err != nil {
		return "", false
	}

	hash := hashKey(plaintext)
	for _, k := range keyFile.Keys {
		if subtle.ConstantTimeCompare([]byte(k.KeyHash), []byte(hash)) == 1 {
			return k.Name, true
		}
	}

	return "", false
}

func apiKeysPath(siteDir string) string {
	return filepath.Join(siteDir, ".polis", apiKeysFilename)
}

func hashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func loadAPIKeys(path string) (*APIKeyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var keyFile APIKeyFile
	if err := json.Unmarshal(data, &keyFile); err != nil {
		return nil, fmt.Errorf("parse api keys: %w", err)
	}
	return &keyFile, nil
}
