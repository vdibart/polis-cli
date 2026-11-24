package ops

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const apiKeysFilename = "api-keys.json" //nolint:gosec // G101: a setting's or path's name, not a credential

// ScopeRender is the scope for a site's RENDERED output: its appearance and
// anything else a render projects, never an authored or signed source. ⭐ It is
// the projections-never-sources line written as a permission, and the only
// scope there is so far.
const ScopeRender = "render"

// knownScope reports whether a key may be minted with a scope: the API's
// vocabulary (apidesc), whose `render` is appearance's earlier name.
func knownScope(name string) bool { return name != apidesc.ScopeFull && apidesc.KnownScope(name) }

// APIKey represents a stored API key.
type APIKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	KeyHash   string `json:"key_hash"`
	CreatedAt string `json:"created_at"`
	// Scopes limits what the key may do. ⭐ ABSENT MEANS FULL ACCESS: every key
	// written before scopes existed has none, and keeps working as it did.
	// ⚠️ So a build older than scopes reads a scoped key as a full one; scoped
	// keys are only safe on an instance running a build that knows them.
	Scopes []string `json:"scopes,omitempty"`
	// Bound is the one path the key works at ("/mcp"), or ""
	// for anywhere its scopes allow. ⭐ One key per MCP endpoint means a
	// connection limited to one part of the site cannot be replayed at
	// another. ⚠️ Like Scopes, a build older than this field ignores it.
	Bound string `json:"bound,omitempty"`
	// Grant is the URL of the signed grant this key presents (a personal
	// token), and GrantID its record id. ⭐ A scoped key with a grant is an
	// agent's: every write it makes checks the grant is live and is marked with
	// it. ⛔ A scoped key WITHOUT one (made before grants) may only read until
	// the owner renews it. ⚠️ A build older than this field ignores it and
	// treats the key as plainly scoped, unmarked.
	Grant   string `json:"grant,omitempty"`
	GrantID string `json:"grant_id,omitempty"`
}

// PersonalToken reports whether the key presents an agent's grant.
func (k APIKey) PersonalToken() bool { return k.Grant != "" }

// UsableAt reports whether the key may be used at path.
func (k APIKey) UsableAt(path string) bool { return k.Bound == "" || k.Bound == path }

// Unscoped reports whether the key has full access.
func (k APIKey) Unscoped() bool { return len(k.Scopes) == 0 }

// Allows reports whether the key may act in the given scope. An unscoped key
// may act in every scope.
func (k APIKey) Allows(scope string) bool {
	if k.Unscoped() {
		return true
	}
	if scope == apidesc.ScopeFull {
		return false
	}
	for _, s := range k.Scopes {
		if apidesc.SameScope(s, scope) {
			return true
		}
	}
	return false
}

// APIKeyFile is the on-disk format for api-keys.json.
type APIKeyFile struct {
	Keys []APIKey `json:"keys"`
}

// ValidateAPIKey checks if a plaintext key matches a stored key with FULL
// access, and returns its name. ⛔ A scoped key is refused here: every caller of
// this function grants everything, so letting a scoped key through would
// ignore its scopes. A caller that honours scopes uses LookupAPIKey.
func ValidateAPIKey(site storage.Site, plaintext string) (string, bool) {
	k, ok := LookupAPIKey(site, plaintext)
	if !ok || !k.Unscoped() {
		return "", false
	}
	return k.Name, true
}

// LookupAPIKey returns the stored key matching a plaintext key, scopes
// included, so the caller can refuse an operation the key is not scoped for.
func LookupAPIKey(site storage.Site, plaintext string) (APIKey, bool) {
	// ⚠️ Any read failure, absence included, is "no valid key": a request
	// is refused rather than let through, as it was when this read by path.
	keyFile, err := loadAPIKeys(site)
	if err != nil {
		return APIKey{}, false
	}

	hash := hashKey(plaintext)
	for _, k := range keyFile.Keys {
		if subtle.ConstantTimeCompare([]byte(k.KeyHash), []byte(hash)) == 1 {
			return k, true
		}
	}

	return APIKey{}, false
}

// ErrAPIKeyNotFound is returned by RevokeAPIKey for an id no key has.
var ErrAPIKeyNotFound = errors.New("no API key has that id")

// apiKeysMu serialises the read-modify-write of the keys file within this
// process, and apiKeysLockKey across every instance sharing the store.
// ⛔ The process lock alone lost writes on a multi-instance remote: two
// instances revoking and minting at once each wrote back what they read, so
// a revoke could answer 204 and leave the key valid.
var apiKeysMu sync.Mutex

const apiKeysLockKey = ".polis/" + apiKeysFilename + ".lock"

// apiKeysGeneration counts this process's writes to the keys file, so a
// cache of it (the MCP key lookup) can tell it is stale.
var apiKeysGeneration atomic.Uint64

// APIKeysGeneration is apiKeysGeneration's current value.
func APIKeysGeneration() uint64 { return apiKeysGeneration.Load() }

// MatchAPIKey returns the key among keys that plaintext is, if any. It is
// LookupAPIKey for a caller holding the list already.
func MatchAPIKey(keys []APIKey, plaintext string) (APIKey, bool) {
	hash := hashKey(plaintext)
	for _, k := range keys {
		if subtle.ConstantTimeCompare([]byte(k.KeyHash), []byte(hash)) == 1 {
			return k, true
		}
	}
	return APIKey{}, false
}

// ListAPIKeys returns the stored keys. A site with no keys file has none.
func ListAPIKeys(site storage.Site) ([]APIKey, error) {
	keyFile, err := loadAPIKeys(site)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return keyFile.Keys, nil
}

// MintAPIKey creates a key and returns its plaintext, which exists only in the
// return value: the file stores the hash. Every scope must be known.
func MintAPIKey(site storage.Site, name string, scopes []string) (string, APIKey, error) {
	return MintBoundAPIKey(site, name, scopes, "")
}

// MintBoundAPIKey is MintAPIKey for a key that works only at one path. ⛔ A
// bound key must be scoped: an unscoped key is accepted by callers that grant
// everything and never look at where it is used.
func MintBoundAPIKey(site storage.Site, name string, scopes []string, bound string) (string, APIKey, error) {
	if bound != "" && len(scopes) == 0 {
		return "", APIKey{}, fmt.Errorf("a key bound to one path must be scoped")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return "", APIKey{}, fmt.Errorf("a key needs a name of 1 to 100 characters")
	}
	for _, s := range scopes {
		if !knownScope(s) {
			return "", APIKey{}, fmt.Errorf("unknown scope %q", s)
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", APIKey{}, fmt.Errorf("generate key: %w", err)
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", APIKey{}, fmt.Errorf("generate key id: %w", err)
	}
	plaintext := "polis_" + hex.EncodeToString(secret)
	key := APIKey{
		ID:        "key_" + hex.EncodeToString(id),
		Name:      name,
		KeyHash:   hashKey(plaintext),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Scopes:    append([]string(nil), scopes...),
		Bound:     bound,
	}
	entry, err := json.Marshal(key)
	if err != nil {
		return "", APIKey{}, err
	}
	err = rewriteAPIKeys(site, func(entries []json.RawMessage) ([]json.RawMessage, error) {
		return append(entries, entry), nil
	})
	if err != nil {
		return "", APIKey{}, err
	}
	return plaintext, key, nil
}

// MintPersonalToken mints a scoped key that presents the grant at grantURL
// (record id grantID). The grant must already be written; the key only
// points at it.
func MintPersonalToken(site storage.Site, name string, scopes []string, grantURL, grantID string) (string, APIKey, error) {
	return MintBoundPersonalToken(site, name, scopes, grantURL, grantID, "")
}

// MintBoundPersonalToken is MintPersonalToken for a token that works only at
// one path (an instance's /mcp, which calls /v1 for it in-process).
func MintBoundPersonalToken(site storage.Site, name string, scopes []string, grantURL, grantID, bound string) (string, APIKey, error) {
	if grantURL == "" || grantID == "" || len(scopes) == 0 {
		return "", APIKey{}, fmt.Errorf("a personal token needs its grant and its scopes")
	}
	plaintext, key, err := MintBoundAPIKey(site, name, scopes, bound)
	if err != nil {
		return "", APIKey{}, err
	}
	if err := SetAPIKeyGrant(site, key.ID, scopes, grantURL, grantID); err != nil {
		_ = RevokeAPIKey(site, key.ID)
		return "", APIKey{}, err
	}
	key.Scopes, key.Grant, key.GrantID = append([]string(nil), scopes...), grantURL, grantID
	return plaintext, key, nil
}

// SetAPIKeyGrant records, on the key with id, the grant it presents and the
// scopes that grant allows (renewing a key made before grants). ⭐ Every other
// member of the entry is kept as it was.
func SetAPIKeyGrant(site storage.Site, id string, scopes []string, grantURL, grantID string) error {
	found := false
	err := rewriteAPIKeys(site, func(entries []json.RawMessage) ([]json.RawMessage, error) {
		for i, e := range entries {
			var m map[string]json.RawMessage
			if json.Unmarshal(e, &m) != nil {
				continue
			}
			var gotID string
			if json.Unmarshal(m["id"], &gotID) != nil || gotID != id {
				continue
			}
			for k, v := range map[string]any{"scopes": scopes, "grant": grantURL, "grant_id": grantID} {
				b, err := json.Marshal(v)
				if err != nil {
					return nil, err
				}
				m[k] = b
			}
			b, err := json.Marshal(m)
			if err != nil {
				return nil, err
			}
			entries[i] = b
			found = true
		}
		return entries, nil
	})
	if err == nil && !found {
		return fmt.Errorf("no key %q", id)
	}
	return err
}

// RevokeAPIKey deletes the key with the given id.
func RevokeAPIKey(site storage.Site, id string) error {
	return rewriteAPIKeys(site, func(entries []json.RawMessage) ([]json.RawMessage, error) {
		out := entries[:0:0]
		found := false
		for _, e := range entries {
			var k struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(e, &k) == nil && k.ID == id {
				found = true
				continue
			}
			out = append(out, e)
		}
		if !found {
			return nil, ErrAPIKeyNotFound
		}
		return out, nil
	})
}

// rewriteAPIKeys applies change to the file's key entries and writes it back.
// ⭐ THE FILE IS UNSIGNED, SO WHAT THIS BUILD DOES NOT MODEL IS PRESERVED: each
// entry and every other top-level member is carried as raw JSON, never rebuilt
// from the struct, so a field a newer build added survives a mint or a revoke.
func rewriteAPIKeys(site storage.Site, change func([]json.RawMessage) ([]json.RawMessage, error)) error {
	apiKeysMu.Lock()
	defer apiKeysMu.Unlock()
	defer apiKeysGeneration.Add(1)
	ctx := context.Background()
	return WithStoreLock(ctx, site, apiKeysLockKey, 30*time.Second, 10*time.Second, func() error {
		return rewriteAPIKeysLocked(ctx, site, change)
	})
}

func rewriteAPIKeysLocked(ctx context.Context, site storage.Site, change func([]json.RawMessage) ([]json.RawMessage, error)) error {
	doc := map[string]json.RawMessage{}
	data, err := site.Store().Get(ctx, apiKeysKey)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read api keys: %w", err)
	default:
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse api keys: %w", err)
		}
	}
	var entries []json.RawMessage
	if raw, ok := doc["keys"]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("parse api keys: %w", err)
		}
	}
	entries, err = change(entries)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []json.RawMessage{}
	}
	keys, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	doc["keys"] = keys
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	// ⛔ 0600: it is the site's list of credentials, and nothing but the
	// site has a reason to read it.
	return site.Store().Put(ctx, apiKeysKey, append(out, '\n'), storage.WithMode(0o600))
}

// apiKeysKey is the API keys file relative to the site root. ⭐ A KEY, NOT A
// PATH: it replaced a path builder whose caller read the file by path.
const apiKeysKey = ".polis/" + apiKeysFilename

func hashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func loadAPIKeys(site storage.Site) (*APIKeyFile, error) {
	data, err := site.Store().Get(context.Background(), apiKeysKey)
	if err != nil {
		return nil, err
	}
	var keyFile APIKeyFile
	if err := json.Unmarshal(data, &keyFile); err != nil {
		return nil, fmt.Errorf("parse api keys: %w", err)
	}
	return &keyFile, nil
}
