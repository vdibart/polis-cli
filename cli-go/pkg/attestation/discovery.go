package attestation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// Discovery service configuration, set by the calling application during
// initialization. Multi-tenant callers pass a *DiscoveryConfig instead.
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
	HTTPClient   *http.Client // Optional shared client for connection pooling
}

func resolveDiscoveryConfig(cfg *DiscoveryConfig) (dsURL, dsKey, baseURL string) {
	if cfg != nil {
		return cfg.DiscoveryURL, cfg.DiscoveryKey, cfg.BaseURL
	}
	return DiscoveryURL, DiscoveryKey, BaseURL
}

// Register announces one attestation to the discovery service.
//
// ⚠️ THIS IS THE CONSUMER THAT EARNS `current_version` ITS PLACE. The follow
// file dropped the equivalent field because nothing read it; here the DS reads
// it to tell when a registered content row changed. Same rule — does this field
// have a consumer? — applied to different facts, giving the opposite answer.
//
// The registered URL is the record's CONTENT path, which actually resolves and
// serves the record as JSON. pub.polis.tag registers a synthetic /tags/<tag>/
// <hash> URL because a tag+target pair has no file of its own; an attestation
// does, so pointing at the real one costs nothing and makes the row's URL
// fetchable. That matters here specifically: a subject may be another
// attestation, and a reference to a URL that 404s is not a reference.
//
// One record, one row. A DS row per claim is the same cardinality as a file per
// claim, so nothing has to be reconciled between the two.
func Register(r *Record, id string, privateKey []byte, cfg *DiscoveryConfig) error {
	dsURL, dsKey, baseURL := resolveDiscoveryConfig(cfg)
	if dsURL == "" || baseURL == "" {
		stream.LogSuppressedEmit("pub.polis.attestation.issued", "config_missing",
			discovery.ExtractDomainFromURL(baseURL),
			map[string]interface{}{"predicate": r.Predicate})
		return nil
	}

	checkDir := DataDir
	if cfg != nil && cfg.DataDir != "" {
		checkDir = cfg.DataDir
	}
	if !discovery.IsRegisteredLocally(checkDir, dsURL) {
		stream.LogSuppressedEmit("pub.polis.attestation.issued", "not_registered_locally",
			discovery.ExtractDomainFromURL(baseURL),
			map[string]interface{}{"predicate": r.Predicate, "ds_url": dsURL})
		return nil
	}

	// The author is the tenant's domain, matching the DS's extractActor, which
	// derives it from the record URL.
	author := polisurl.ExtractDomain(baseURL)
	contentURL := RecordURL(baseURL, id)

	meta := map[string]interface{}{
		"predicate":    r.Predicate,
		"subject":      r.Subject.ID,
		"subject_type": r.Subject.Type,
	}
	// Only present when the claim is pinned. An empty string would be a
	// different byte sequence from an absent key on both sides of the
	// signature, so it must be omitted rather than blanked.
	if r.Subject.Version != "" {
		meta["subject_version"] = r.Subject.Version
	}

	// Canonical JSON matching the DS's buildContentCanonicalJSON:
	// {type, url, version, author, metadata}. Same byte sequence on both sides.
	canonical, err := discovery.MakeContentCanonicalJSON(
		TypeName, contentURL, r.Version, author, meta,
	)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}

	sig, err := signing.SignContent(canonical, privateKey)
	if err != nil {
		return fmt.Errorf("sign registration: %w", err)
	}

	payload := map[string]interface{}{
		"type":      TypeName,
		"url":       contentURL,
		"version":   r.Version,
		"author":    author,
		"metadata":  meta,
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

	var hc *http.Client
	if cfg != nil {
		hc = cfg.HTTPClient
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

	// SIGNET epic 32: publish the DS's countersignature. An attestation's
	// `version` already hashes the whole signed record, so the witness binds all
	// of it with no artifact_hash needed. Never fails the registration.
	publishWitness(resp.Body, checkDir, contentURL)
	return nil
}

// publishWitness reads a registration response and records the witness it
// carries, if any, in the site's published witness set.
func publishWitness(body io.Reader, siteDir, artifactURL string) {
	if siteDir == "" {
		return
	}
	var result struct {
		Witness *discovery.Witness `json:"witness"`
	}
	data, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil || json.Unmarshal(data, &result) != nil || result.Witness == nil {
		return
	}
	if _, werr := site.RecordWitness(siteDir, artifactURL, *result.Witness); werr != nil {
		fmt.Printf("[!] Could not publish the discovery service's witness for %s: %v\n", artifactURL, werr)
	}
}

// RegisterIssued announces a record this site ALREADY issued — one written by
// a path that deliberately does not touch the discovery service (`polis actor
// register` writes its agent-disclosure records that way), or whose
// registration at issue time was skipped or failed.
//
// Register is quiet by design when it cannot announce: at issue time an
// unannounced record is a normal state and the command must not fail. Asked to
// register and nothing else, silence would read as success, so this refuses
// LOUDLY instead:
//   - no discovery service or base URL configured;
//   - the site is not registered with that service (no local marker);
//   - the record's issuer is not this site — only the issuer may announce;
//   - the record's signature does not verify against this site's published
//     key, with its history (announcing a record nobody can verify spreads it).
//
// Re-registering an already-announced record is safe: the DS keys a content
// row by URL and version.
func RegisterIssued(dataDir, id string, privateKey []byte, cfg *DiscoveryConfig) (*Record, error) {
	r, err := Load(Path(dataDir, id))
	if err != nil {
		return nil, err
	}
	dsURL, _, baseURL := resolveDiscoveryConfig(cfg)
	if dsURL == "" || baseURL == "" {
		return r, fmt.Errorf("no discovery service configured (DISCOVERY_SERVICE_URL and POLIS_BASE_URL must both be set)")
	}
	if !discovery.IsRegisteredLocally(dataDir, dsURL) {
		return r, fmt.Errorf("this site is not registered with %s — run `polis register` first", dsURL)
	}
	if !strings.EqualFold(strings.TrimRight(r.Issuer, "/"), strings.TrimRight(baseURL, "/")) {
		return r, fmt.Errorf("record %s was issued by %s, not this site (%s); only its issuer may announce it", id, r.Issuer, baseURL)
	}
	switch status, verr := VerifyRecord(dataDir, r); status {
	case StatusValid:
	default:
		detail := ""
		if verr != nil {
			detail = ": " + verr.Error()
		}
		return r, fmt.Errorf("record %s is %s against this site's published key%s — not announced", id, status, detail)
	}
	if cfg == nil {
		cfg = &DiscoveryConfig{}
	}
	withDir := *cfg
	withDir.DataDir = dataDir
	return r, Register(r, id, privateKey, &withDir)
}
