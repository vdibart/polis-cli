package tag

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// Discovery service configuration. Set by the calling application
// (CLI or webapp) during initialization.
//
// For multi-tenant use (e.g., hosted service), pass a *DiscoveryConfig
// to SyncTag instead of using these globals.
var (
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string
	DataDir      string
)

// DiscoveryConfig holds per-tenant discovery service configuration.
type DiscoveryConfig struct {
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string
	DataDir      string
	HTTPClient   *http.Client // Optional shared HTTP client for connection pooling
	Generator    string       // e.g. "polis-cli-go/0.59.0" — used in tag metadata
}

// resolveDiscoveryConfig returns the effective config: explicit if provided,
// otherwise falls back to package-level globals.
func resolveDiscoveryConfig(cfg *DiscoveryConfig) (dsURL, dsKey, baseURL string) {
	if cfg != nil {
		return cfg.DiscoveryURL, cfg.DiscoveryKey, cfg.BaseURL
	}
	return DiscoveryURL, DiscoveryKey, BaseURL
}

// TagTargetURL generates a deterministic URL for a tag+target pair.
// Format: https://<base>/tags/<tagName>/<sha256(targetURI)[:16]>
func TagTargetURL(baseURL, tagName, targetURI string) string {
	h := sha256.Sum256([]byte(targetURI))
	hash := hex.EncodeToString(h[:])[:16]
	return fmt.Sprintf("%s/tags/%s/%s", baseURL, tagName, hash)
}

// TagTargetMetadata returns the DS metadata payload for a tag+target pair.
func TagTargetMetadata(tagName, targetURI string) map[string]string {
	return map[string]string{
		"tag":    tagName,
		"target": targetURI,
	}
}

// SyncTag performs full reconciliation of a tag file with the discovery service.
// Each target in the tag file becomes a separate DS row. Targets no longer present
// are soft-deleted.
func SyncTag(dataDir string, tf *TagFile, privateKey []byte, dsCfg *DiscoveryConfig) error {
	dsURL, dsKey, baseURL := resolveDiscoveryConfig(dsCfg)
	if dsURL == "" || baseURL == "" {
		stream.LogSuppressedEmit("pub.polis.tag.applied", "config_missing",
			discovery.ExtractDomainFromURL(baseURL),
			map[string]interface{}{"tag": tf.Tag})
		return nil
	}

	checkDir := dataDir
	if dsCfg != nil && dsCfg.DataDir != "" {
		checkDir = dsCfg.DataDir
	}
	if !discovery.IsRegisteredLocally(checkDir, dsURL) {
		stream.LogSuppressedEmit("pub.polis.tag.applied", "not_registered_locally",
			discovery.ExtractDomainFromURL(baseURL),
			map[string]interface{}{"tag": tf.Tag, "ds_url": dsURL})
		return nil
	}

	// Author is the tenant's domain — matches DS extractActor() which extracts
	// from the tag URL. Could be overridden by email in future.
	author := polisurl.ExtractDomain(baseURL)

	// Register each current target. The DS normalizes metadata URLs
	// (backslash→slash, .html→.md) BEFORE building canonical JSON for
	// signature verification. We must normalize here too so the bytes
	// Chaplain signs match the bytes the DS verifies against.
	for _, target := range tf.Targets {
		normalizedTargetURI := polisurl.NormalizeToMD(target.URI)
		url := TagTargetURL(baseURL, tf.Tag, normalizedTargetURI)
		meta := TagTargetMetadata(tf.Tag, normalizedTargetURI)

		var hc *http.Client
		if dsCfg != nil {
			hc = dsCfg.HTTPClient
		}
		if err := registerTagRow(dsURL, dsKey, url, tf.Version, author, meta, privateKey, hc); err != nil {
			return fmt.Errorf("register tag target %q: %w", target.URI, err)
		}
	}

	return nil
}

// UnregisterTarget soft-deletes a specific tag+target row from the DS.
func UnregisterTarget(tagName, targetURI string, privateKey []byte, dsCfg *DiscoveryConfig) error {
	dsURL, dsKey, baseURL := resolveDiscoveryConfig(dsCfg)
	if dsURL == "" || baseURL == "" {
		stream.LogSuppressedEmit("pub.polis.tag.removed", "config_missing",
			discovery.ExtractDomainFromURL(baseURL),
			map[string]interface{}{"tag": tagName, "target": targetURI})
		return nil
	}

	checkDir := DataDir
	if dsCfg != nil && dsCfg.DataDir != "" {
		checkDir = dsCfg.DataDir
	}
	if !discovery.IsRegisteredLocally(checkDir, dsURL) {
		stream.LogSuppressedEmit("pub.polis.tag.removed", "not_registered_locally",
			discovery.ExtractDomainFromURL(baseURL),
			map[string]interface{}{"tag": tagName, "target": targetURI, "ds_url": dsURL})
		return nil
	}

	// Normalize the target URI the same way SyncTag does so the hashed URL
	// matches what was registered (DS also normalizes on ingestion).
	normalizedTargetURI := polisurl.NormalizeToMD(targetURI)
	url := TagTargetURL(baseURL, tagName, normalizedTargetURI)
	var hc *http.Client
	if dsCfg != nil {
		hc = dsCfg.HTTPClient
	}
	return softDeleteRow(dsURL, dsKey, url, privateKey, hc)
}

// registerTagRow registers a single tag+target row with the DS. The payload
// is signed with the tenant's private key and the signature is included so
// the DS can verify against the tenant's well-known public key.
func registerTagRow(dsURL, dsKey, contentURL, version, author string, meta map[string]string, privateKey []byte, hc *http.Client) error {
	// Metadata must be map[string]interface{} so it matches the canonical
	// payload structure the DS builds (DS uses generic object shape).
	metaIface := make(map[string]interface{}, len(meta))
	for k, v := range meta {
		metaIface[k] = v
	}

	// Build canonical JSON matching DS buildContentCanonicalJSON: {type, url,
	// version, author, metadata}. Same byte-sequence on both sides.
	canonical, err := discovery.MakeContentCanonicalJSON(
		"pub.polis.tag", contentURL, version, author, metaIface,
	)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}

	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	payload := map[string]interface{}{
		"type":      "pub.polis.tag",
		"url":       contentURL,
		"version":   version,
		"author":    author,
		"metadata":  metaIface,
		"signature": sig,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, dsURL+"/v1/content", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if dsKey != "" {
		req.Header.Set("Authorization", "Bearer "+dsKey)
	}

	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("DS request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("DS returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// softDeleteRow marks a content row as removed in the DS.
func softDeleteRow(dsURL, dsKey, contentURL string, privateKey []byte, hc *http.Client) error {
	payload := map[string]interface{}{
		"type":   "pub.polis.tag",
		"url":    contentURL,
		"status": "removed",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, dsURL+"/v1/content", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if dsKey != "" {
		req.Header.Set("Authorization", "Bearer "+dsKey)
	}

	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("DS request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("DS returned HTTP %d", resp.StatusCode)
	}

	return nil
}
