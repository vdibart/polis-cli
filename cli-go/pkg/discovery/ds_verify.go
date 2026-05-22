package discovery

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// DSKeyCache fetches and caches the discovery service's public key.
type DSKeyCache struct {
	mu        sync.Mutex
	dsURL     string
	publicKey string // ssh-ed25519 format
	keyID     string
	fetchedAt time.Time
	ttl       time.Duration
	client    *http.Client
}

// NewDSKeyCache creates a key cache for the given DS URL.
func NewDSKeyCache(dsURL string, ttl time.Duration) *DSKeyCache {
	if ttl == 0 {
		ttl = time.Hour
	}
	return &DSKeyCache{
		dsURL: dsURL,
		ttl:   ttl,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// NewDSKeyCacheWithHTTP creates a key cache using a shared HTTP client.
func NewDSKeyCacheWithHTTP(dsURL string, ttl time.Duration, hc *http.Client) *DSKeyCache {
	if ttl == 0 {
		ttl = time.Hour
	}
	if hc == nil {
		return NewDSKeyCache(dsURL, ttl)
	}
	return &DSKeyCache{
		dsURL:  dsURL,
		ttl:    ttl,
		client: hc,
	}
}

// GetKey returns the cached DS public key, fetching it if expired or missing.
func (c *DSKeyCache) GetKey() (publicKey, keyID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.publicKey != "" && time.Since(c.fetchedAt) < c.ttl {
		return c.publicKey, c.keyID, nil
	}

	if err := c.fetch(); err != nil {
		return "", "", err
	}
	return c.publicKey, c.keyID, nil
}

// Refresh forces a re-fetch of the DS key (e.g., after verification failure).
func (c *DSKeyCache) Refresh() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetch()
}

func (c *DSKeyCache) fetch() error {
	// Try /.well-known/polis first
	key, keyID, err := c.fetchWellKnown()
	if err == nil {
		// Normalize to SSH format (DS may publish raw base64)
		sshKey, convErr := ensureSSHFormat(key)
		if convErr != nil {
			return fmt.Errorf("DS public key format error: %w", convErr)
		}
		c.publicKey = sshKey
		c.keyID = keyID
		c.fetchedAt = time.Now()
		return nil
	}

	// Fallback to /v1/sites/public-key
	key, keyID, err = c.fetchPublicKeyEndpoint()
	if err != nil {
		return fmt.Errorf("failed to fetch DS public key: %w", err)
	}
	sshKey, convErr := ensureSSHFormat(key)
	if convErr != nil {
		return fmt.Errorf("DS public key format error: %w", convErr)
	}
	c.publicKey = sshKey
	c.keyID = keyID
	c.fetchedAt = time.Now()
	return nil
}

func (c *DSKeyCache) fetchWellKnown() (string, string, error) {
	resp, err := c.client.Get(c.dsURL + "/.well-known/polis")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", "", err
	}

	var result struct {
		PublicKey string `json:"public_key"`
		KeyID     string `json:"key_id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", err
	}
	if result.PublicKey == "" {
		return "", "", fmt.Errorf("empty public_key")
	}
	return result.PublicKey, result.KeyID, nil
}

func (c *DSKeyCache) fetchPublicKeyEndpoint() (string, string, error) {
	resp, err := c.client.Get(c.dsURL + "/v1/sites/public-key")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", "", err
	}

	var result struct {
		PublicKey string `json:"public_key"`
		KeyID     string `json:"key_id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", err
	}
	if result.PublicKey == "" {
		return "", "", fmt.Errorf("empty public_key")
	}
	return result.PublicKey, result.KeyID, nil
}

// ensureSSHFormat converts a raw base64 Ed25519 public key to ssh-ed25519 format
// if needed. If the key already starts with "ssh-ed25519", it's returned as-is.
func ensureSSHFormat(key string) (string, error) {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "ssh-ed25519 ") {
		return key, nil
	}

	// Try decoding as raw base64 (32-byte Ed25519 public key)
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		// Try URL-safe base64
		raw, err = base64.RawStdEncoding.DecodeString(key)
		if err != nil {
			return "", fmt.Errorf("public key is neither ssh-ed25519 format nor valid base64: %s", key)
		}
	}

	if len(raw) != 32 {
		return "", fmt.Errorf("decoded key is %d bytes, expected 32 for Ed25519", len(raw))
	}

	// Build SSH wire format: [4-byte len]["ssh-ed25519"][4-byte len][32-byte key]
	keyType := []byte("ssh-ed25519")
	var blob []byte
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(keyType)))
	blob = append(blob, lenBuf...)
	blob = append(blob, keyType...)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(raw)))
	blob = append(blob, lenBuf...)
	blob = append(blob, raw...)

	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob), nil
}

// SignedResponse holds the DS signature fields present in signed query responses.
type SignedResponse struct {
	DSSignature string `json:"ds_signature"`
	DSKeyID     string `json:"ds_key_id"`
}

// VerifyDSResponse verifies a DS response envelope signature.
// data is the response body minus the ds_signature and ds_key_id fields.
// On first failure, refreshes the key cache and retries once.
func VerifyDSResponse(cache *DSKeyCache, data map[string]interface{}, sig SignedResponse) error {
	if sig.DSSignature == "" {
		return fmt.Errorf("missing ds_signature in DS response")
	}

	canonical := BuildCanonicalJSON(data)

	// First attempt
	pubKey, _, err := cache.GetKey()
	if err != nil {
		return fmt.Errorf("failed to get DS public key: %w", err)
	}

	valid, err := signing.VerifySignature([]byte(canonical), []byte(pubKey), sig.DSSignature)
	if err == nil && valid {
		return nil
	}

	// Retry with refreshed key (handles key rotation)
	if refreshErr := cache.Refresh(); refreshErr != nil {
		return fmt.Errorf("DS signature verification failed and key refresh failed: %w", refreshErr)
	}

	pubKey, _, err = cache.GetKey()
	if err != nil {
		return fmt.Errorf("failed to get DS public key after refresh: %w", err)
	}

	valid, err = signing.VerifySignature([]byte(canonical), []byte(pubKey), sig.DSSignature)
	if err != nil {
		return fmt.Errorf("DS signature verification failed after key refresh: %w", err)
	}
	if !valid {
		return fmt.Errorf("DS signature verification failed after key refresh — discarding response")
	}
	return nil
}

// BuildCanonicalJSON produces deterministic JSON with all keys sorted alphabetically.
// This must match the DS-side buildCanonicalJSON in response_signing.ts.
// Uses marshalCanonical to avoid Go's default HTML escaping (<, >, &) which
// JS JSON.stringify does not apply — the DS signs responses without HTML
// escaping, so we must verify against the same byte sequence.
func BuildCanonicalJSON(data interface{}) string {
	sorted := sortValue(data)
	result, _ := marshalCanonical(sorted)
	return string(result)
}

// sortValue recursively sorts map keys for canonical JSON output.
func sortValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		sorted := make(sortedMap, 0, len(val))
		for k, v := range val {
			sorted = append(sorted, sortedEntry{k, sortValue(v)})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
		return sorted
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = sortValue(item)
		}
		return result
	default:
		return v
	}
}

// sortedEntry is a key-value pair for ordered JSON output.
type sortedEntry struct {
	key   string
	value interface{}
}

// sortedMap is an ordered slice of key-value pairs that marshals with sorted keys.
type sortedMap []sortedEntry

func (s sortedMap) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, entry := range s {
		if i > 0 {
			buf = append(buf, ',')
		}
		// Must use marshalCanonical (no HTML escape) to match DS's JS
		// JSON.stringify output byte-for-byte.
		keyJSON, _ := marshalCanonical(entry.key)
		valJSON, _ := marshalCanonical(entry.value)
		buf = append(buf, keyJSON...)
		buf = append(buf, ':')
		buf = append(buf, valJSON...)
	}
	buf = append(buf, '}')
	return buf, nil
}

// VerifyAutoblessAttestation verifies a DS attestation signature on
// an autoblessed comment. The attestation proves which DS instance
// made the autobless decision and which policy rule matched.
// Returns nil if valid, error if verification fails (or if the
// attestation is missing).
//
// R20-C-F4 (2026-05-18): previously this returned nil on empty
// attestation as a legacy-record concession. Combined with the
// caller in `cli-go/pkg/stream/blessing.go` (which logged
// verification errors but merged the entry into blessingMap
// anyway), autoblessed events were effectively unverified — an
// attacker could emit a fake autobless event with empty
// `ds_attestation` and the local cache would accept it. New
// posture: require attestation when verification is requested; the
// caller is responsible for not calling this for legacy/non-
// autoblessed records.
func VerifyAutoblessAttestation(
	cache *DSKeyCache,
	commentURL, targetURL, policyRule, policySource, dsKeyID, attestation string,
) error {
	if attestation == "" {
		return fmt.Errorf("autobless attestation is required but missing")
	}

	canonical := BuildCanonicalJSON(map[string]interface{}{
		"type":          "pub.polis.comment.blessing",
		"action":        "autobless",
		"comment_url":   commentURL,
		"target_url":    targetURL,
		"policy_rule":   policyRule,
		"policy_source": policySource,
		"ds_key_id":     dsKeyID,
	})

	pubKey, _, err := cache.GetKey()
	if err != nil {
		return fmt.Errorf("failed to get DS public key for autobless verification: %w", err)
	}

	valid, err := signing.VerifySignature([]byte(canonical), []byte(pubKey), attestation)
	if err == nil && valid {
		return nil
	}

	// Retry with refreshed key (handles key rotation)
	if refreshErr := cache.Refresh(); refreshErr != nil {
		return fmt.Errorf("autobless attestation verification failed and key refresh failed: %w", refreshErr)
	}

	pubKey, _, err = cache.GetKey()
	if err != nil {
		return fmt.Errorf("failed to get DS public key after refresh: %w", err)
	}

	valid, err = signing.VerifySignature([]byte(canonical), []byte(pubKey), attestation)
	if err != nil {
		return fmt.Errorf("autobless attestation verification failed after key refresh: %w", err)
	}
	if !valid {
		return fmt.Errorf("autobless attestation verification failed after key refresh — discarding")
	}
	return nil
}
