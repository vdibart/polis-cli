package stream

import (
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"strings"
	"time"

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
	BaseURL      string // POLIS_BASE_URL — actor domain is extracted from this
	DataDir      string // Site data directory — used for registration check

	// DefaultSite is the process-wide site handle, for a caller that holds one.
	// ⭐ It wins over DataDir: a handle carries the store the site lives on, so
	// an instance whose site is not on local disk checks the right marker.
	DefaultSite storage.Site
)

// DiscoveryConfig holds per-tenant discovery service configuration.
// When passed to PublishEvent, it overrides the package-level globals,
// enabling safe multi-tenant operation.
type DiscoveryConfig struct {
	DiscoveryURL string
	BaseURL      string
	DataDir      string
	// Site is the per-call site handle. It wins over DataDir, for the same
	// reason DefaultSite wins over the package's DataDir.
	Site      storage.Site
	Generator string // e.g. "polis-cli-go/0.59.0"
}

// PublishEvent publishes an event to the discovery stream.
// Returns nil if discovery is not configured (with diagnostic log).
// If dsCfg is non-nil, it overrides package-level discovery globals for
// multi-tenant safety. Pass nil to use globals (single-tenant / CLI mode).
func PublishEvent(eventType string, payload map[string]interface{}, privateKey []byte, dsCfg ...*DiscoveryConfig) error {
	var dsURL, baseURL string
	if len(dsCfg) > 0 && dsCfg[0] != nil {
		dsURL = dsCfg[0].DiscoveryURL
		baseURL = dsCfg[0].BaseURL
	} else {
		dsURL = DiscoveryURL
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

	// ⚠️ The per-call config may name a different site than the package
	// default, so the handle is built from whichever wins — one construction
	// point, after the choice rather than before it.
	//
	// ⭐ Per-call beats process-wide, and a handle beats a directory at each level.
	var site storage.Site
	switch {
	case len(dsCfg) > 0 && dsCfg[0] != nil && dsCfg[0].Site.Valid():
		site = dsCfg[0].Site
	case len(dsCfg) > 0 && dsCfg[0] != nil && dsCfg[0].DataDir != "":
		site = storage.At(dsCfg[0].DataDir)
	case DefaultSite.Valid():
		site = DefaultSite
	default:
		site = storage.At(DataDir)
	}
	if !discovery.IsRegisteredLocally(site, dsURL) {
		LogSuppressedEmit(eventType, "not_registered_locally",
			discovery.ExtractDomainFromURL(baseURL),
			map[string]interface{}{"ds_url": dsURL})
		return nil
	}
	if privateKey == nil {
		fmt.Println("[i] Stream event skipped: no private key")
		return nil
	}

	actor := discovery.ExtractDomainFromURL(baseURL)
	if actor == "" {
		return fmt.Errorf("could not extract domain from base URL")
	}

	// R23-2: a freshness field, so a captured POST /v1/stream cannot be
	// replayed for ever once the discovery service stops accepting events
	// without one.
	//
	// ⭐ IT GOES INSIDE THE PAYLOAD, not beside it. A sibling field would be
	// verified once at POST time and then discarded — a stored event carries
	// the service's `created_at`, not the string the client signed — so
	// VerifyAuthorSignature could never rebuild the canonical, and every event
	// published this way would be silently dropped by every consumer. The
	// payload is stored and returned verbatim, so a timestamp in it survives.
	//
	// ⛔ The caller's map is NOT mutated. These maps are built by callers that
	// reuse and re-read them; stamping a timestamp into one would leak a stale
	// value into the next publish.
	payload = withEventTimestamp(payload, time.Now().UTC().Format(time.RFC3339))

	canonical, err := discovery.MakeStreamCanonicalJSON(eventType, payload)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}

	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	client := discovery.NewClient(dsURL)
	if err := client.StreamPublish(eventType, actor, payload, sig); err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	return nil
}

// withEventTimestamp returns a copy of payload carrying `timestamp`, leaving the
// caller's map untouched.
//
// ⚠️ A caller that already set `timestamp` keeps its value. Nothing in this repo
// does, but an event type whose own schema has a timestamp would otherwise have
// it silently overwritten with the wall clock — which is both a lie and a
// signature that verifies, the worst combination.
func withEventTimestamp(payload map[string]interface{}, ts string) map[string]interface{} {
	out := make(map[string]interface{}, len(payload)+1)
	for k, v := range payload {
		out[k] = v
	}
	if _, already := out["timestamp"]; !already {
		out["timestamp"] = ts
	}
	return out
}
