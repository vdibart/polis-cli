package ops

import (
	"encoding/json"
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(storage.At(siteDir).Path(apiKeysKey), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKey(t *testing.T) {
	siteDir := t.TempDir()

	plaintext := "polis_test_token_abc123"
	seedAPIKeyFile(t, siteDir, []APIKey{
		{ID: "test", Name: "test-key", KeyHash: hashKey(plaintext)},
	})

	name, ok := ValidateAPIKey(storage.At(siteDir), plaintext)
	if !ok {
		t.Error("expected key to be valid")
	}
	if name != "test-key" {
		t.Errorf("expected name 'test-key', got %q", name)
	}

	_, ok = ValidateAPIKey(storage.At(siteDir), "polis_invalid_key")
	if ok {
		t.Error("expected invalid key to fail validation")
	}
}

func TestValidateAPIKey_NoKeyFile(t *testing.T) {
	siteDir := t.TempDir()
	// No api-keys.json written.
	_, ok := ValidateAPIKey(storage.At(siteDir), "polis_some_key")
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

	name1, ok1 := ValidateAPIKey(storage.At(siteDir), pt1)
	if !ok1 || name1 != "key-1" {
		t.Errorf("key-1 validation failed: ok=%v, name=%q", ok1, name1)
	}
	name2, ok2 := ValidateAPIKey(storage.At(siteDir), pt2)
	if !ok2 || name2 != "key-2" {
		t.Errorf("key-2 validation failed: ok=%v, name=%q", ok2, name2)
	}
}

func TestMintAPIKey_StoresOnlyTheHashAndCarriesScopes(t *testing.T) {
	siteDir := t.TempDir()
	site := storage.At(siteDir)

	plaintext, key, err := MintAPIKey(site, "Kay", []string{ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(site.Path(apiKeysKey))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), plaintext) {
		t.Fatal("the keys file holds the plaintext key")
	}
	got, ok := LookupAPIKey(site, plaintext)
	if !ok || got.ID != key.ID || !got.Allows(ScopeRender) || got.Unscoped() {
		t.Fatalf("lookup = %+v, %v; want the render-scoped key %s", got, ok, key.ID)
	}
	if got.Allows("source") {
		t.Error("a render-scoped key allows a scope it was not given")
	}
}

// ⛔ ValidateAPIKey's callers grant everything, so a scoped key must not pass it.
func TestValidateAPIKey_RefusesAScopedKey(t *testing.T) {
	site := storage.At(t.TempDir())
	plaintext, _, err := MintAPIKey(site, "Kay", []string{ScopeRender})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ValidateAPIKey(site, plaintext); ok {
		t.Fatal("a render-scoped key passed the full-access check")
	}
	full, _, err := MintAPIKey(site, "script", nil)
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := ValidateAPIKey(site, full); !ok || name != "script" {
		t.Fatalf("an unscoped key failed the full-access check: %q, %v", name, ok)
	}
}

func TestMintAPIKey_RefusesUnknownScopeAndEmptyName(t *testing.T) {
	site := storage.At(t.TempDir())
	if _, _, err := MintAPIKey(site, "x", []string{"everything"}); err == nil {
		t.Error("an unknown scope was accepted")
	}
	if _, _, err := MintAPIKey(site, "  ", nil); err == nil {
		t.Error("an empty name was accepted")
	}
}

func TestRevokeAPIKey(t *testing.T) {
	site := storage.At(t.TempDir())
	p1, k1, _ := MintAPIKey(site, "one", nil)
	p2, _, _ := MintAPIKey(site, "two", []string{ScopeRender})
	if err := RevokeAPIKey(site, k1.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupAPIKey(site, p1); ok {
		t.Error("a revoked key still validates")
	}
	if _, ok := LookupAPIKey(site, p2); !ok {
		t.Error("revoking one key removed another")
	}
	if err := RevokeAPIKey(site, k1.ID); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("revoking twice = %v, want ErrAPIKeyNotFound", err)
	}
	keys, err := ListAPIKeys(site)
	if err != nil || len(keys) != 1 || keys[0].Name != "two" {
		t.Errorf("list after revoke = %+v, %v", keys, err)
	}
}

func TestListAPIKeys_NoFileIsNoKeys(t *testing.T) {
	keys, err := ListAPIKeys(storage.At(t.TempDir()))
	if err != nil || len(keys) != 0 {
		t.Errorf("list with no file = %+v, %v", keys, err)
	}
}

// ⭐ The keys file is unsigned, so a member this build does not model survives
// a write, at the top level and inside an entry it did not touch.
func TestMintAPIKey_PreservesWhatItDoesNotModel(t *testing.T) {
	siteDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(siteDir, ".polis"), 0700); err != nil {
		t.Fatal(err)
	}
	seed := `{"keys":[{"id":"old","name":"old","key_hash":"abc","created_at":"","expires":"2027-01-01"}],"note":"kept"}`
	if err := os.WriteFile(filepath.Join(siteDir, ".polis", apiKeysFilename), []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}
	site := storage.At(siteDir)
	if _, _, err := MintAPIKey(site, "new", nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(site.Path(apiKeysKey))
	for _, want := range []string{`"expires": "2027-01-01"`, `"note": "kept"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the rewrite dropped %s:\n%s", want, raw)
		}
	}
}

// ⭐ A scoped key allows exactly its scopes; render and appearance are one
// scope under two names; no list of scopes ever adds up to full.
func TestScopedKeysAllowExactlyTheirScopes(t *testing.T) {
	appearance := APIKey{Scopes: []string{"appearance"}}
	render := APIKey{Scopes: []string{"render"}}
	if !appearance.Allows("render") || !render.Allows("appearance") {
		t.Error("render and appearance must be the same scope")
	}
	if appearance.Allows("publish") {
		t.Error("appearance must not allow publish")
	}
	many := APIKey{Scopes: []string{"read", "publish", "comment", "bless", "follow", "tag", "appearance", "profile", "policy:read", "policy:write", "notify", "status"}}
	if many.Allows("full") {
		t.Error("no set of scopes may stand for the owner's full token")
	}
	if !(APIKey{}).Allows("full") {
		t.Error("an unscoped key is the owner's full token")
	}
	site := storage.At(t.TempDir())
	if _, _, err := MintAPIKey(site, "agent", []string{"publish", "notify"}); err != nil {
		t.Errorf("minting with vocabulary scopes: %v", err)
	}
	for _, bad := range []string{"full", "messages", "everything"} {
		if _, _, err := MintAPIKey(site, "agent", []string{bad}); err == nil {
			t.Errorf("minting with scope %q succeeded", bad)
		}
	}
}
