// Package discovery provides a client for the polis discovery service.
package discovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// maxDSResponseSize is the maximum response body size for DS API responses (5MB).
const maxDSResponseSize = 5 * 1024 * 1024

// Client is an HTTP client for the discovery service.
type Client struct {
	BaseURL       string
	APIKey        string
	Domain        string // optional: for signed GET requests
	PrivateKeyPEM []byte // optional: for signed GET requests
	HTTPClient    *http.Client
	DSKeyCache    *DSKeyCache // optional: for DS response verification
	RequestID     string      // optional: propagated as X-Request-Id for cross-boundary tracing
	Logger        func(method, path string, durationMs int64, requestID string) // optional: DS roundtrip timing
}

// NewClient creates a new discovery service client (unauthenticated GET requests).
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		DSKeyCache: NewDSKeyCache(baseURL, 0),
	}
}

// NewAuthenticatedClient creates a discovery client that signs GET requests
// with domain ownership proof via X-Polis-Domain/Signature/Timestamp headers.
func NewAuthenticatedClient(baseURL, apiKey, domain string, privateKeyPEM []byte) *Client {
	return &Client{
		BaseURL:       baseURL,
		APIKey:        apiKey,
		Domain:        domain,
		PrivateKeyPEM: privateKeyPEM,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		DSKeyCache: NewDSKeyCache(baseURL, 0),
	}
}

// queryAuthPayload is the canonical payload for signed GET request authentication.
// Field order is critical — must match the TS side's buildQueryAuthCanonicalJSON.
type queryAuthPayload struct {
	Action    string `json:"action"`
	Domain    string `json:"domain"`
	Timestamp string `json:"timestamp"`
}

// MakeQueryAuthCanonicalJSON creates the canonical JSON for signed GET auth.
// Must produce identical output to the TS side's buildQueryAuthCanonicalJSON.
func MakeQueryAuthCanonicalJSON(domain, timestamp string) ([]byte, error) {
	return json.Marshal(queryAuthPayload{
		Action:    "query",
		Domain:    domain,
		Timestamp: timestamp,
	})
}

// setRequestID sets X-Request-Id on the request if the client has one configured.
func (c *Client) setRequestID(req *http.Request) {
	if c.RequestID != "" {
		req.Header.Set("X-Request-Id", c.RequestID)
	}
}

// doWithTiming executes an HTTP request, calling the Logger callback if set.
func (c *Client) doWithTiming(req *http.Request) (*http.Response, error) {
	c.setRequestID(req)
	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if c.Logger != nil {
		durationMs := time.Since(start).Milliseconds()
		c.Logger(req.Method, req.URL.Path, durationMs, c.RequestID)
	}
	return resp, err
}

// addAuthHeaders adds X-Polis-Domain, X-Polis-Signature, X-Polis-Timestamp
// headers to a request if the client has auth credentials configured.
// No-op if Domain/PrivateKeyPEM are empty (backward compatible).
func (c *Client) addAuthHeaders(req *http.Request) error {
	if c.Domain == "" || len(c.PrivateKeyPEM) == 0 {
		return nil
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	canonicalJSON, err := MakeQueryAuthCanonicalJSON(c.Domain, timestamp)
	if err != nil {
		return fmt.Errorf("failed to build auth canonical payload: %w", err)
	}

	signature, err := signing.SignContent(canonicalJSON, c.PrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to sign auth payload: %w", err)
	}

	// SSH signatures contain newlines (PEM format), which are invalid in
	// HTTP headers. Strip them — the TS parser already strips whitespace
	// via .replace(/\s/g, '') before extracting the base64 payload.
	compactSig := strings.ReplaceAll(signature, "\n", "")

	req.Header.Set("X-Polis-Domain", c.Domain)
	req.Header.Set("X-Polis-Signature", compactSig)
	req.Header.Set("X-Polis-Timestamp", timestamp)

	return nil
}

// ============================================================================
// Unified Content Types
// ============================================================================

// ContentRegisterRequest is the unified content registration request.
type ContentRegisterRequest struct {
	Type      string                 `json:"type"`
	URL       string                 `json:"url"`
	Version   string                 `json:"version"`
	Author    string                 `json:"author"`
	Metadata  map[string]interface{} `json:"metadata"`
	Signature string                 `json:"signature"`
}

// ContentRegisterResponse is the response from content-register.
type ContentRegisterResponse struct {
	Success            bool   `json:"success"`
	Message            string `json:"message,omitempty"`
	Type               string `json:"type"`
	URL                string `json:"url"`
	Status             string `json:"status"` // "created" or "updated"
	RelationshipStatus string `json:"relationship_status,omitempty"`
	Error              string `json:"error,omitempty"`
}

// ContentUnregisterRequest is the request to unregister content.
type ContentUnregisterRequest struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Signature string `json:"signature"`
}

// ContentCheckResponse is the response from content-check.
type ContentCheckResponse struct {
	Exists  bool   `json:"exists"`
	Type    string `json:"type"`
	URL     string `json:"url"`
	Version string `json:"version,omitempty"`
	Actor   string `json:"actor,omitempty"`
}

// ContentRecord represents a content item from the discovery service.
type ContentRecord struct {
	ID        json.Number            `json:"id"`
	Type      string                 `json:"type"`
	URL       string                 `json:"url"`
	Version   string                 `json:"version"`
	Actor     string                 `json:"actor"`
	Author    string                 `json:"author"`
	Metadata  map[string]interface{} `json:"metadata"`
	CreatedAt string                 `json:"created_at"`
	UpdatedAt string                 `json:"updated_at"`
}

// UnmarshalJSON handles DS responses where metadata may arrive as a
// JSON string (double-encoded from Postgres JSONB) instead of an object.
func (r *ContentRecord) UnmarshalJSON(data []byte) error {
	type Alias ContentRecord
	var raw struct {
		Alias
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ContentRecord(raw.Alias)
	r.Metadata = unmarshalJSONBField(raw.Metadata)
	return nil
}

// ContentQueryResponse is the response from content-query.
type ContentQueryResponse struct {
	Count       int             `json:"count"`
	Records     []ContentRecord `json:"records"`
	DSSignature string          `json:"ds_signature,omitempty"`
	DSKeyID     string          `json:"ds_key_id,omitempty"`
}

// ============================================================================
// Unified Relationship Types
// ============================================================================

// RelationshipUpdateRequest updates a relationship status.
type RelationshipUpdateRequest struct {
	Type      string `json:"type"`
	SourceURL string `json:"source_url"`
	TargetURL string `json:"target_url"`
	Action    string `json:"action"` // "grant" or "deny"
	Signature string `json:"signature"`
}

// RelationshipRecord represents a relationship from the discovery service.
type RelationshipRecord struct {
	ID        json.Number            `json:"id"`
	Type      string                 `json:"type"`
	SourceURL string                 `json:"source_url"`
	TargetURL string                 `json:"target_url"`
	Actor     string                 `json:"actor"`
	Status    string                 `json:"status"`
	Metadata  map[string]interface{} `json:"metadata"`
	CreatedAt string                 `json:"created_at"`
	UpdatedAt string                 `json:"updated_at"`
}

// UnmarshalJSON handles DS responses where metadata may arrive as a
// JSON string (double-encoded from Postgres JSONB) instead of an object.
func (r *RelationshipRecord) UnmarshalJSON(data []byte) error {
	type Alias RelationshipRecord
	var raw struct {
		Alias
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = RelationshipRecord(raw.Alias)
	r.Metadata = unmarshalJSONBField(raw.Metadata)
	return nil
}

// RelationshipQueryResponse is the response from relationship-query.
type RelationshipQueryResponse struct {
	Count       int                  `json:"count"`
	Records     []RelationshipRecord `json:"records"`
	DSSignature string               `json:"ds_signature,omitempty"`
	DSKeyID     string               `json:"ds_key_id,omitempty"`
}

// ============================================================================
// Content Methods
// ============================================================================

// RegisterContent registers or updates content with the discovery service.
func (c *Client) RegisterContent(req *ContentRegisterRequest) (*ContentRegisterResponse, error) {
	endpoint := c.BaseURL + "/v1/content"

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result ContentRegisterResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if resp.StatusCode >= 400 {
		if result.Error != "" {
			return &result, fmt.Errorf("content registration failed: %s", result.Error)
		}
		return &result, fmt.Errorf("content registration failed with status %d", resp.StatusCode)
	}

	return &result, nil
}

// UnregisterContent removes content from the discovery service.
func (c *Client) UnregisterContent(contentType, contentURL, signature string) error {
	endpoint := c.BaseURL + "/v1/content/unregister"

	req := ContentUnregisterRequest{
		Type:      contentType,
		URL:       contentURL,
		Signature: signature,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
		return fmt.Errorf("content unregistration failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// CheckContent checks if content exists in the discovery service.
func (c *Client) CheckContent(contentType, contentURL string) (*ContentCheckResponse, error) {
	params := url.Values{}
	params.Set("type", contentType)
	params.Set("url", contentURL)
	endpoint := c.BaseURL + "/v1/content/check?" + params.Encode()

	httpReq, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("content check failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result ContentCheckResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// QueryContent queries content by type and optional filters.
func (c *Client) QueryContent(contentType string, filters map[string]string) (*ContentQueryResponse, error) {
	params := url.Values{}
	params.Set("type", contentType)
	for k, v := range filters {
		params.Set(k, v)
	}
	endpoint := c.BaseURL + "/v1/content?" + params.Encode()

	httpReq, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if err := c.addAuthHeaders(httpReq); err != nil {
		return nil, err
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("content query failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result ContentQueryResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if err := c.verifyResponse(respBody, result.DSSignature, result.DSKeyID); err != nil {
		return nil, fmt.Errorf("content query: %w", err)
	}

	return &result, nil
}

// ============================================================================
// Relationship Methods
// ============================================================================

// UpdateRelationship updates a relationship status (grant/deny blessing).
// The privateKey is used to sign the request payload.
func (c *Client) UpdateRelationship(relType, sourceURL, targetURL, action string, privateKey []byte) error {
	endpoint := c.BaseURL + "/v1/relationships"

	// Create canonical payload for signing (must match DS buildRelationshipCanonicalJSON)
	canonicalPayload := relationshipCanonicalPayload{
		Type:      relType,
		SourceURL: sourceURL,
		TargetURL: targetURL,
		Action:    action,
	}
	canonicalJSON, err := json.Marshal(canonicalPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal canonical payload: %w", err)
	}

	// Sign the canonical payload
	signature, err := signing.SignContent(canonicalJSON, privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign payload: %w", err)
	}

	req := RelationshipUpdateRequest{
		Type:      relType,
		SourceURL: sourceURL,
		TargetURL: targetURL,
		Action:    action,
		Signature: signature,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
		return fmt.Errorf("relationship update failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// QueryRelationships queries relationships by type and optional filters.
func (c *Client) QueryRelationships(relType string, filters map[string]string) (*RelationshipQueryResponse, error) {
	params := url.Values{}
	params.Set("type", relType)
	for k, v := range filters {
		params.Set(k, v)
	}
	endpoint := c.BaseURL + "/v1/relationships?" + params.Encode()

	httpReq, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if err := c.addAuthHeaders(httpReq); err != nil {
		return nil, err
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("relationship query failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result RelationshipQueryResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if err := c.verifyResponse(respBody, result.DSSignature, result.DSKeyID); err != nil {
		return nil, fmt.Errorf("relationship query: %w", err)
	}

	return &result, nil
}

// ============================================================================
// Canonical Payload Types (for signing)
// ============================================================================

// contentCanonicalPayload is the unified canonical payload for content registration signing.
// CRITICAL: Field order determines signature output.
type contentCanonicalPayload struct {
	Type     string                 `json:"type"`
	URL      string                 `json:"url"`
	Version  string                 `json:"version"`
	Author   string                 `json:"author"`
	Metadata map[string]interface{} `json:"metadata"`
}

// relationshipCanonicalPayload is the canonical payload for relationship update signing.
// Must match DS buildRelationshipCanonicalJSON: {type, source_url, target_url, action}
type relationshipCanonicalPayload struct {
	Type      string `json:"type"`
	SourceURL string `json:"source_url"`
	TargetURL string `json:"target_url"`
	Action    string `json:"action"`
}

// MakeContentCanonicalJSON creates canonical JSON for content registration signing.
func MakeContentCanonicalJSON(contentType, contentURL, version, author string, metadata map[string]interface{}) ([]byte, error) {
	return json.Marshal(contentCanonicalPayload{
		Type:     contentType,
		URL:      contentURL,
		Version:  version,
		Author:   author,
		Metadata: metadata,
	})
}

// MakeContentUnregisterCanonicalJSON creates the deterministic canonical JSON for
// content unregistration signing. Must match DS buildContentUnregisterCanonicalJSON: {type, url}
func MakeContentUnregisterCanonicalJSON(contentType, contentURL string) string {
	b, _ := json.Marshal(struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}{
		Type: contentType,
		URL:  contentURL,
	})
	return string(b)
}

// MakeRelationshipCanonicalJSON creates canonical JSON for relationship update signing.
// Must match DS buildRelationshipCanonicalJSON: {type, source_url, target_url, action}
func MakeRelationshipCanonicalJSON(relType, sourceURL, targetURL, action string) ([]byte, error) {
	return json.Marshal(relationshipCanonicalPayload{
		Type:      relType,
		SourceURL: sourceURL,
		TargetURL: targetURL,
		Action:    action,
	})
}

// ============================================================================
// Site Registration (/v1/sites/* endpoints, table: ds_registered_sites)
// ============================================================================

// SiteCheckResponse is the response from the sites-check endpoint.
type SiteCheckResponse struct {
	IsRegistered        bool   `json:"is_registered"`
	Domain              string `json:"domain,omitempty"`
	CreatedAt           string `json:"created_at,omitempty"`
	RegistryURL         string `json:"registry_url,omitempty"`
	RegistrationVersion int    `json:"registration_version,omitempty"`
	ServiceAttestation  string `json:"service_attestation,omitempty"`
	AttestationKeyID    string `json:"attestation_key_id,omitempty"`
	DSSignature         string `json:"ds_signature,omitempty"`
	DSKeyID             string `json:"ds_key_id,omitempty"`
}

// SiteRegisterResponse is the response from the sites-register endpoint.
type SiteRegisterResponse struct {
	Success            bool   `json:"success"`
	Domain             string `json:"domain,omitempty"`
	RegistryURL        string `json:"registry_url,omitempty"`
	CreatedAt          string `json:"created_at,omitempty"`
	ServiceAttestation string `json:"service_attestation,omitempty"`
	Error              string `json:"error,omitempty"`
	Code               string `json:"code,omitempty"`
}

// SiteUnregisterResponse is the response from the sites-unregister endpoint.
type SiteUnregisterResponse struct {
	Success bool   `json:"success"`
	Domain  string `json:"domain,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

// siteRegistrationPayload is the canonical payload structure for site registration.
type siteRegistrationPayload struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	Domain  string `json:"domain"`
}

// siteRegisterRequest is the full request payload for the sites-register endpoint.
type siteRegisterRequest struct {
	Version    int    `json:"version"`
	Action     string `json:"action"`
	Domain     string `json:"domain"`
	Signature  string `json:"signature"`
	Email      string `json:"email,omitempty"`
	AuthorName string `json:"author_name,omitempty"`
}

// siteUnregisterRequest is the full request payload for the sites-unregister endpoint.
type siteUnregisterRequest struct {
	Version   int    `json:"version"`
	Action    string `json:"action"`
	Domain    string `json:"domain"`
	Signature string `json:"signature"`
}

// CheckSiteRegistration checks if a domain is registered with the discovery service.
func (c *Client) CheckSiteRegistration(domain string) (*SiteCheckResponse, error) {
	endpoint := fmt.Sprintf("%s/v1/sites/check?domain=%s", c.BaseURL, domain)

	httpReq, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result SiteCheckResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if err := c.verifyResponse(respBody, result.DSSignature, result.DSKeyID); err != nil {
		return nil, fmt.Errorf("site check: %w", err)
	}

	return &result, nil
}

// RegisterSite registers a domain with the discovery service.
func (c *Client) RegisterSite(domain string, privateKey []byte, email, authorName string) (*SiteRegisterResponse, error) {
	endpoint := c.BaseURL + "/v1/sites"

	canonicalPayload := siteRegistrationPayload{
		Version: 1,
		Action:  "register",
		Domain:  domain,
	}
	canonicalJSON, err := json.Marshal(canonicalPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal canonical payload: %w", err)
	}

	signature, err := signing.SignContent(canonicalJSON, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign payload: %w", err)
	}

	req := siteRegisterRequest{
		Version:    1,
		Action:     "register",
		Domain:     domain,
		Signature:  signature,
		Email:      email,
		AuthorName: authorName,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result SiteRegisterResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if resp.StatusCode >= 400 {
		if result.Error != "" {
			return &result, fmt.Errorf("registration failed: %s", result.Error)
		}
		return &result, fmt.Errorf("registration failed with status %d", resp.StatusCode)
	}

	result.Success = true
	return &result, nil
}

// UnregisterSite unregisters a domain from the discovery service.
func (c *Client) UnregisterSite(domain string, privateKey []byte) (*SiteUnregisterResponse, error) {
	endpoint := c.BaseURL + "/v1/sites/unregister"

	canonicalPayload := siteRegistrationPayload{
		Version: 1,
		Action:  "unregister",
		Domain:  domain,
	}
	canonicalJSON, err := json.Marshal(canonicalPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal canonical payload: %w", err)
	}

	signature, err := signing.SignContent(canonicalJSON, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign payload: %w", err)
	}

	req := siteUnregisterRequest{
		Version:   1,
		Action:    "unregister",
		Domain:    domain,
		Signature: signature,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result SiteUnregisterResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if resp.StatusCode >= 400 {
		if result.Error != "" {
			return &result, fmt.Errorf("unregistration failed: %s", result.Error)
		}
		return &result, fmt.Errorf("unregistration failed with status %d", resp.StatusCode)
	}

	result.Success = true
	return &result, nil
}

// MakeSiteRegistrationCanonicalJSON creates canonical JSON for site registration signing.
func MakeSiteRegistrationCanonicalJSON(action, domain string) ([]byte, error) {
	return json.Marshal(siteRegistrationPayload{
		Version: 1,
		Action:  action,
		Domain:  domain,
	})
}

// ============================================================================
// Stream (/v1/stream endpoints, table: ds_events)
// ============================================================================

// unmarshalJSONBField handles Postgres JSONB fields that may arrive as either
// a JSON object or a double-encoded JSON string (e.g. "{\"key\":\"val\"}").
func unmarshalJSONBField(raw json.RawMessage) map[string]interface{} {
	if len(raw) == 0 {
		return map[string]interface{}{}
	}
	var result map[string]interface{}
	if raw[0] == '"' {
		// Double-encoded string — unquote then parse
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			json.Unmarshal([]byte(s), &result)
		}
	} else {
		json.Unmarshal(raw, &result)
	}
	if result == nil {
		return map[string]interface{}{}
	}
	return result
}


// verifyResponse verifies the DS envelope signature on a query response.
// It extracts the data fields (everything except ds_signature and ds_key_id),
// builds canonical JSON, and verifies the signature.
func (c *Client) verifyResponse(rawBody []byte, dsSignature, dsKeyID string) error {
	if c.DSKeyCache == nil {
		return nil
	}

	// Parse the full response into a generic map
	var full map[string]interface{}
	if err := json.Unmarshal(rawBody, &full); err != nil {
		return fmt.Errorf("failed to parse response for verification: %w", err)
	}

	// Extract data fields (everything except ds_signature, ds_key_id)
	data := make(map[string]interface{}, len(full))
	for k, v := range full {
		if k != "ds_signature" && k != "ds_key_id" {
			data[k] = v
		}
	}

	return VerifyDSResponse(c.DSKeyCache, data, SignedResponse{
		DSSignature: dsSignature,
		DSKeyID:     dsKeyID,
	})
}

// StreamEvent represents a single event in the discovery stream.
type StreamEvent struct {
	ID        json.Number            `json:"id"`
	Type      string                 `json:"type"`
	Timestamp string                 `json:"created_at"`
	Actor     string                 `json:"actor"`
	Signature string                 `json:"signature"`
	Payload   map[string]interface{} `json:"payload"`
}

// UnmarshalJSON handles DS responses where payload may arrive as a
// JSON string (double-encoded from Postgres JSONB) instead of an object.
func (e *StreamEvent) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID        json.Number     `json:"id"`
		Type      string          `json:"type"`
		Timestamp string          `json:"created_at"`
		Actor     string          `json:"actor"`
		Signature string          `json:"signature"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	e.ID = raw.ID
	e.Type = raw.Type
	e.Timestamp = raw.Timestamp
	e.Actor = raw.Actor
	e.Signature = raw.Signature

	e.Payload = unmarshalJSONBField(raw.Payload)

	return nil
}

// StreamQueryResponse is the response from GET /v1/stream.
type StreamQueryResponse struct {
	Events      []StreamEvent `json:"events"`
	Cursor      string        `json:"cursor"`
	HasMore     bool          `json:"has_more"`
	DSSignature string        `json:"ds_signature,omitempty"`
	DSKeyID     string        `json:"ds_key_id,omitempty"`
}

// StreamHealthResponse is the response from GET /v1/stream/health.
type StreamHealthResponse struct {
	Status       string `json:"status"`
	LatestCursor string `json:"latest_cursor"`
	OldestCursor string `json:"oldest_cursor"`
	EventCount   int    `json:"event_count"`
}

// StreamQuery queries the discovery stream for events.
func (c *Client) StreamQuery(since string, limit int, typeFilter string, actorFilter string, targetFilter string, sourceFilter ...string) (*StreamQueryResponse, error) {
	params := url.Values{}
	if since != "" {
		params.Set("since", since)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if typeFilter != "" {
		params.Set("type", typeFilter)
	}
	if actorFilter != "" {
		params.Set("actor", actorFilter)
	}
	if targetFilter != "" {
		params.Set("target", targetFilter)
	}
	if len(sourceFilter) > 0 && sourceFilter[0] != "" {
		params.Set("source", sourceFilter[0])
	}

	endpoint := c.BaseURL + "/v1/stream"
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	httpReq, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if err := c.addAuthHeaders(httpReq); err != nil {
		return nil, err
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("stream query failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result StreamQueryResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if err := c.verifyResponse(respBody, result.DSSignature, result.DSKeyID); err != nil {
		return nil, fmt.Errorf("stream query: %w", err)
	}

	return &result, nil
}

// StreamPublish publishes an event to the discovery stream.
func (c *Client) StreamPublish(eventType, actor string, payload map[string]interface{}, signature string) error {
	endpoint := c.BaseURL + "/v1/stream"

	reqBody := struct {
		Type      string                 `json:"type"`
		Actor     string                 `json:"actor"`
		Payload   map[string]interface{} `json:"payload"`
		Signature string                 `json:"signature"`
	}{
		Type:      eventType,
		Actor:     actor,
		Payload:   payload,
		Signature: signature,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
		return fmt.Errorf("stream publish failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// StreamHealth returns the health status of the discovery stream.
func (c *Client) StreamHealth() (*StreamHealthResponse, error) {
	endpoint := c.BaseURL + "/v1/stream/health"

	httpReq, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("stream health failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result StreamHealthResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// MakeStreamCanonicalJSON creates the canonical JSON bytes for stream event signing.
func MakeStreamCanonicalJSON(eventType string, payload map[string]interface{}) ([]byte, error) {
	canonical := struct {
		Type    string                 `json:"type"`
		Payload map[string]interface{} `json:"payload"`
	}{
		Type:    eventType,
		Payload: payload,
	}
	return json.Marshal(canonical)
}

// ============================================================================
// Key Rotation
// ============================================================================

// KeyRotationRequest is the request body for /v1/sites/keys/rotate.
type KeyRotationRequest struct {
	Domain        string `json:"domain"`
	OldKey        string `json:"old_key"`
	NewKey        string `json:"new_key"`
	TransitionSig string `json:"transition_sig"`
	Timestamp     string `json:"timestamp"`
}

// keyRotationCanonical is the canonical structure for key rotation signing.
// Keys sorted alphabetically: action, domain, new_key, old_key, timestamp.
type keyRotationCanonical struct {
	Action    string `json:"action"`
	Domain    string `json:"domain"`
	NewKey    string `json:"new_key"`
	OldKey    string `json:"old_key"`
	Timestamp string `json:"timestamp"`
}

// MakeKeyRotationCanonicalJSON creates the deterministic canonical JSON for
// key rotation signature verification. Keys are sorted alphabetically.
func MakeKeyRotationCanonicalJSON(domain, oldKey, newKey, timestamp string) ([]byte, error) {
	return json.Marshal(keyRotationCanonical{
		Action:    "key-rotation",
		Domain:    domain,
		NewKey:    newKey,
		OldKey:    oldKey,
		Timestamp: timestamp,
	})
}

// RotateKey sends a key rotation request to the discovery service.
func (c *Client) RotateKey(req KeyRotationRequest) error {
	endpoint := c.BaseURL + "/v1/sites/keys/rotate"

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
		return fmt.Errorf("key rotation failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// ============================================================================
// Utility Functions
// ============================================================================

// ExtractDomainFromURL extracts the hostname from a URL string.
func ExtractDomainFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// JoinDomains joins domain strings with commas for the stream actor filter.
func JoinDomains(domains []string) string {
	return strings.Join(domains, ",")
}
