package publish

import (
	"fmt"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// Discovery service configuration. Set by the calling application
// (CLI or webapp) during initialization. If any are empty, registration
// is skipped with a diagnostic message.
//
// For multi-tenant use (e.g., hosted service), pass a *DiscoveryConfig
// to RegisterPost/PublishPost/RepublishPost instead of using these globals.
var (
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string
)

// DiscoveryConfig holds per-tenant discovery service configuration.
// When passed to RegisterPost, PublishPost, or RepublishPost, it overrides
// the package-level globals, enabling safe multi-tenant operation.
type DiscoveryConfig struct {
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string
}

// resolveDiscoveryConfig returns the effective config: explicit if provided,
// otherwise falls back to package-level globals.
func resolveDiscoveryConfig(cfg *DiscoveryConfig) (dsURL, dsKey, baseURL string) {
	if cfg != nil {
		return cfg.DiscoveryURL, cfg.DiscoveryKey, cfg.BaseURL
	}
	return DiscoveryURL, DiscoveryKey, BaseURL
}

// RegisterPost registers a published post with the discovery service.
// Called automatically by PublishPost/RepublishPost when discovery is configured.
// Returns nil on success. Returns an error if registration fails.
// If both dsURL and baseURL are empty (unconfigured), returns nil with a
// diagnostic log. If cfg is nil, falls back to package-level globals.
func RegisterPost(dataDir string, result *PublishResult, privateKey []byte, cfg *DiscoveryConfig) error {
	dsURL, dsKey, baseURL := resolveDiscoveryConfig(cfg)
	if dsURL == "" || baseURL == "" {
		var missing []string
		if dsURL == "" {
			missing = append(missing, "DISCOVERY_SERVICE_URL")
		}
		if baseURL == "" {
			missing = append(missing, "POLIS_BASE_URL")
		}
		fmt.Printf("[i] Discovery registration skipped: %s not set\n", strings.Join(missing, ", "))
		return nil
	}

	if !discovery.IsRegisteredLocally(dataDir, dsURL) {
		fmt.Println("[i] DS registration skipped: site not registered with discovery service")
		return nil
	}

	// Determine author identity: email from .well-known/polis, or domain from baseURL
	wk, err := site.LoadWellKnown(dataDir)
	if err != nil {
		return fmt.Errorf("load .well-known/polis: %w", err)
	}
	author := wk.Email
	if author == "" {
		author = polisurl.ExtractDomain(baseURL)
	}
	if author == "" {
		return fmt.Errorf("no author identity (need email in .well-known/polis or POLIS_BASE_URL)")
	}

	// Build post URL from base URL + path
	postURL := strings.TrimRight(baseURL, "/") + "/" + result.Path

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	metadata := map[string]interface{}{
		"title":           result.Title,
		"published_at":    now,
		"last_updated_at": now,
	}

	// Build canonical JSON for signing
	canonical, err := discovery.MakeContentCanonicalJSON(
		"pub.polis.post", postURL, result.Version, author, metadata,
	)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}

	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	client := discovery.NewClient(dsURL, dsKey)
	req := &discovery.ContentRegisterRequest{
		Type:      "pub.polis.post",
		URL:       postURL,
		Version:   result.Version,
		Author:    author,
		Metadata:  metadata,
		Signature: sig,
	}

	// Retry once on transient failures (e.g., DS timeout fetching public key).
	var registerErr error
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := client.RegisterContent(req); err != nil {
			registerErr = err
			if attempt == 0 {
				fmt.Printf("[!] DS registration attempt failed, retrying: %v\n", err)
				time.Sleep(3 * time.Second)
				continue
			}
		} else {
			registerErr = nil
			break
		}
	}
	if registerErr != nil {
		return fmt.Errorf("register: %w", registerErr)
	}

	fmt.Printf("[✓] Registered with discovery service: %s\n", postURL)
	return nil
}
