package ops

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
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

// GenerateAPIKey creates a new API key and stores its hash.
// Returns the plaintext key (only shown once) and the stored key metadata.
func GenerateAPIKey(siteDir, name string) (string, *APIKey, error) {
	// Generate 32 random bytes → 64-char hex string
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate random bytes: %w", err)
	}
	plaintext := "polis_" + hex.EncodeToString(raw)

	// Hash for storage
	hash := hashKey(plaintext)

	// Generate short ID
	idBytes := make([]byte, 4)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, fmt.Errorf("generate key id: %w", err)
	}

	key := &APIKey{
		ID:        hex.EncodeToString(idBytes),
		Name:      name,
		KeyHash:   hash,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// Load existing keys
	keysPath := apiKeysPath(siteDir)
	keyFile, err := loadAPIKeys(keysPath)
	if err != nil {
		keyFile = &APIKeyFile{}
	}

	keyFile.Keys = append(keyFile.Keys, *key)

	if err := saveAPIKeys(keysPath, keyFile); err != nil {
		return "", nil, fmt.Errorf("save api keys: %w", err)
	}

	return plaintext, key, nil
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

// ListAPIKeys returns all stored key metadata (without hashes).
func ListAPIKeys(siteDir string) ([]APIKey, error) {
	keysPath := apiKeysPath(siteDir)
	keyFile, err := loadAPIKeys(keysPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Strip hashes from response
	result := make([]APIKey, len(keyFile.Keys))
	for i, k := range keyFile.Keys {
		result[i] = APIKey{
			ID:        k.ID,
			Name:      k.Name,
			CreatedAt: k.CreatedAt,
		}
	}
	return result, nil
}

// RevokeAPIKey removes an API key by its ID.
func RevokeAPIKey(siteDir, keyID string) error {
	keysPath := apiKeysPath(siteDir)
	keyFile, err := loadAPIKeys(keysPath)
	if err != nil {
		return fmt.Errorf("load api keys: %w", err)
	}

	found := false
	remaining := make([]APIKey, 0, len(keyFile.Keys))
	for _, k := range keyFile.Keys {
		if k.ID == keyID {
			found = true
			continue
		}
		remaining = append(remaining, k)
	}

	if !found {
		return fmt.Errorf("api key %q not found", keyID)
	}

	keyFile.Keys = remaining
	return saveAPIKeys(keysPath, keyFile)
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

func saveAPIKeys(path string, keyFile *APIKeyFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(keyFile, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
