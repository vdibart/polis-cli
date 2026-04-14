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
		return nil // discovery not configured
	}

	checkDir := dataDir
	if dsCfg != nil && dsCfg.DataDir != "" {
		checkDir = dsCfg.DataDir
	}
	if !discovery.IsRegisteredLocally(checkDir, dsURL) {
		return nil // silently skip — site not registered
	}

	// Register each current target
	for _, target := range tf.Targets {
		url := TagTargetURL(baseURL, tf.Tag, target.URI)
		meta := TagTargetMetadata(tf.Tag, target.URI)

		var hc *http.Client
		if dsCfg != nil {
			hc = dsCfg.HTTPClient
		}
		if err := registerTagRow(dsURL, dsKey, url, tf.Version, meta, privateKey, hc); err != nil {
			return fmt.Errorf("register tag target %q: %w", target.URI, err)
		}
	}

	return nil
}

// UnregisterTarget soft-deletes a specific tag+target row from the DS.
func UnregisterTarget(tagName, targetURI string, privateKey []byte, dsCfg *DiscoveryConfig) error {
	dsURL, dsKey, baseURL := resolveDiscoveryConfig(dsCfg)
	if dsURL == "" || baseURL == "" {
		return nil
	}

	checkDir := DataDir
	if dsCfg != nil && dsCfg.DataDir != "" {
		checkDir = dsCfg.DataDir
	}
	if !discovery.IsRegisteredLocally(checkDir, dsURL) {
		return nil
	}

	url := TagTargetURL(baseURL, tagName, targetURI)
	var hc *http.Client
	if dsCfg != nil {
		hc = dsCfg.HTTPClient
	}
	return softDeleteRow(dsURL, dsKey, url, privateKey, hc)
}

// registerTagRow registers a single tag+target row with the DS.
func registerTagRow(dsURL, dsKey, contentURL, version string, meta map[string]string, privateKey []byte, hc *http.Client) error {
	payload := map[string]interface{}{
		"type":     "pub.polis.tag",
		"url":      contentURL,
		"version":  version,
		"metadata": meta,
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
