package stream

import (
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Discovery service configuration for stream event publishing.
// Set by the calling application (CLI or webapp) during initialization.
//
// For multi-tenant use (e.g., hosted service), pass a *DiscoveryConfig
// to PublishEvent instead of using these globals.
var (
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string // POLIS_BASE_URL — actor domain is extracted from this
	DataDir      string // Site data directory — used for registration check
)

// DiscoveryConfig holds per-tenant discovery service configuration.
// When passed to PublishEvent, it overrides the package-level globals,
// enabling safe multi-tenant operation.
type DiscoveryConfig struct {
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string
	DataDir      string
}

// PublishEvent publishes an event to the discovery stream.
// Returns nil if discovery is not configured (with diagnostic log).
// If dsCfg is non-nil, it overrides package-level discovery globals for
// multi-tenant safety. Pass nil to use globals (single-tenant / CLI mode).
func PublishEvent(eventType string, payload map[string]interface{}, privateKey []byte, dsCfg ...*DiscoveryConfig) error {
	var dsURL, dsKey, baseURL string
	if len(dsCfg) > 0 && dsCfg[0] != nil {
		dsURL = dsCfg[0].DiscoveryURL
		dsKey = dsCfg[0].DiscoveryKey
		baseURL = dsCfg[0].BaseURL
	} else {
		dsURL = DiscoveryURL
		dsKey = DiscoveryKey
		baseURL = BaseURL
	}

	if dsURL == "" || baseURL == "" {
		var missing []string
		if dsURL == "" {
			missing = append(missing, "DISCOVERY_SERVICE_URL")
		}
		if baseURL == "" {
			missing = append(missing, "POLIS_BASE_URL")
		}
		fmt.Printf("[i] Stream event skipped: %s not set\n", strings.Join(missing, ", "))
		return nil
	}

	dataDir := DataDir
	if len(dsCfg) > 0 && dsCfg[0] != nil && dsCfg[0].DataDir != "" {
		dataDir = dsCfg[0].DataDir
	}
	if !discovery.IsRegisteredLocally(dataDir, dsURL) {
		return nil // silently skip — stream events are fire-and-forget
	}
	if privateKey == nil {
		fmt.Println("[i] Stream event skipped: no private key")
		return nil
	}

	actor := discovery.ExtractDomainFromURL(baseURL)
	if actor == "" {
		return fmt.Errorf("could not extract domain from base URL")
	}

	canonical, err := discovery.MakeStreamCanonicalJSON(eventType, payload)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}

	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	client := discovery.NewClient(dsURL, dsKey)
	if err := client.StreamPublish(eventType, actor, payload, sig); err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	return nil
}
