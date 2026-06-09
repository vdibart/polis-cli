// Package discovery provides a client for the polis discovery service.
package discovery

import (
	"bytes"
	"context"
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

// marshalCanonical produces JSON bytes whose output matches JavaScript's
// JSON.stringify — specifically, WITHOUT Go's default HTML escaping of
// <, >, & to \u003c, \u003e, \u0026. The DS side builds canonical JSON
// with JSON.stringify, so any payload we sign must match byte-for-byte.
//
// Use this for EVERY canonical JSON that's signed or verified against a
// signature — content registrations, relationships, stream events,
// key rotations, site registrations. Do NOT use for ordinary HTTP
// request bodies where encoding differences don't matter.
func marshalCanonical(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder appends a trailing newline; strip it.
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// maxDSResponseRecords is the maximum number of records/events in a single DS response.
// Prevents memory exhaustion from responses with millions of tiny objects that fit the
// byte limit but overwhelm struct allocation.
const maxDSResponseRecords = 10_000

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
	CreatedAfter  string      // optional: ISO 8601 timestamp floor for stream queries
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

// NewClientWithHTTP creates a discovery client using a shared HTTP client.
// This enables connection pooling when multiple Client instances share the same
// underlying http.Client (and its transport).
func NewClientWithHTTP(baseURL, apiKey string, hc *http.Client) *Client {
	if hc == nil {
		return NewClient(baseURL, apiKey)
	}
	return &Client{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: hc,
		DSKeyCache: NewDSKeyCacheWithHTTP(baseURL, 0, hc),
	}
}

// NewAuthenticatedClientWithHTTP creates an authenticated discovery client
// using a shared HTTP client for connection pooling.
func NewAuthenticatedClientWithHTTP(baseURL, apiKey, domain string, privateKeyPEM []byte, hc *http.Client) *Client {
	if hc == nil {
		return NewAuthenticatedClient(baseURL, apiKey, domain, privateKeyPEM)
	}
	return &Client{
		BaseURL:       baseURL,
		APIKey:        apiKey,
		Domain:        domain,
		PrivateKeyPEM: privateKeyPEM,
		HTTPClient:    hc,
		DSKeyCache:    NewDSKeyCacheWithHTTP(baseURL, 0, hc),
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
	return marshalCanonical(queryAuthPayload{
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
//
// R20-C-F1 (2026-05-18): Timestamp added — DS requires it for replay
// defense; signed canonical includes it; freshness ±5min.
type ContentUnregisterRequest struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Timestamp string `json:"timestamp"`
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
//
// R20-C-F2 (2026-05-18): Timestamp added — DS requires it for replay
// defense; signed canonical includes it; freshness ±5min.
type RelationshipUpdateRequest struct {
	Type      string `json:"type"`
	SourceURL string `json:"source_url"`
	TargetURL string `json:"target_url"`
	Action    string `json:"action"` // "grant" or "deny"
	Timestamp string `json:"timestamp"`
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
//
// R20-C-F1 (2026-05-18): callers must pass the same `timestamp` they
// used when building the canonical for `signature`. DS rejects
// timestamps further than ±5min from server clock.
func (c *Client) UnregisterContent(contentType, contentURL, timestamp, signature string) error {
	endpoint := c.BaseURL + "/v1/content/unregister"

	req := ContentUnregisterRequest{
		Type:      contentType,
		URL:       contentURL,
		Timestamp: timestamp,
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

// UnpublishContent unpublishes content from the discovery service (clean break retraction).
// For posts, this cascades blessing changes (blessed→orphaned, pending→denied).
// For comments, this resets the comment's blessing to 'pending'.
//
// R20-C-F1 (2026-05-18): callers must pass the same `timestamp` they
// used when building the canonical for `signature`.
func (c *Client) UnpublishContent(contentType, contentURL, timestamp, signature string) error {
	endpoint := c.BaseURL + "/v1/content/unpublish"

	req := ContentUnregisterRequest{
		Type:      contentType,
		URL:       contentURL,
		Timestamp: timestamp,
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
		return fmt.Errorf("content unpublish failed with status %d: %s", resp.StatusCode, string(respBody))
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
	if len(result.Records) > maxDSResponseRecords {
		return nil, fmt.Errorf("DS response exceeds record limit: %d > %d", len(result.Records), maxDSResponseRecords)
	}

	if err := c.verifyResponse(respBody, result.DSSignature, result.DSKeyID); err != nil {
		return nil, fmt.Errorf("content query: %w", err)
	}

	return &result, nil
}

// CommentCountsRequest is the payload for POST /v1/content/comments/counts.
type CommentCountsRequest struct {
	URLs []string `json:"urls"`
}

// CommentCountsResponse is the response from the comment-counts endpoint.
// Counts is keyed by the normalized post URL (DS canonical form: .md, forward-slash).
// URLs with zero comments are omitted from the response.
type CommentCountsResponse struct {
	Counts map[string]int `json:"counts"`
}

// normalizeURLForDS mirrors discovery-service/core/validation.ts normalizeUrl:
// replaces backslashes with forward slashes (Windows path leak guard) and
// converts .html extensions to .md (DS stores comment in_reply_to URLs in
// canonical .md form at register time). Sending .html URLs without this
// normalization would silently miss every match.
func normalizeURLForDS(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	if strings.HasSuffix(s, ".html") {
		s = s[:len(s)-5] + ".md"
	}
	return s
}

// FetchCommentCountsCtx queries the DS for total comment counts on a batch
// of post URLs. Returns a map keyed by the URL form the CALLER provided
// (not the normalized form) so callers don't need to re-map the result.
//
// Empty input short-circuits with an empty map — no DS roundtrip.
//
// The provided context governs the request lifetime. Stream-handler callers
// pass a context with a strict timeout (e.g. 500ms for the visible-viewport
// fetch) so the response never blocks beyond their budget; background
// callers pass a longer-lived context.Background()-derived context.
func (c *Client) FetchCommentCountsCtx(ctx context.Context, urls []string) (map[string]int, error) {
	if len(urls) == 0 {
		return map[string]int{}, nil
	}

	// Normalize for the wire, but remember the caller's form so the
	// returned map keys round-trip exactly. Multiple caller URLs may
	// normalize to the same DS form (e.g. both .html and .md → .md),
	// so use a multimap from normalized → list of original.
	normalized := make([]string, 0, len(urls))
	callerByNormalized := make(map[string][]string, len(urls))
	for _, u := range urls {
		n := normalizeURLForDS(u)
		if _, seen := callerByNormalized[n]; !seen {
			normalized = append(normalized, n)
		}
		callerByNormalized[n] = append(callerByNormalized[n], u)
	}

	body, err := json.Marshal(CommentCountsRequest{URLs: normalized})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := c.BaseURL + "/v1/content/comments/counts"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("comment counts request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("comment counts failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed CommentCountsResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// Re-key the result by the caller's URL form. Each normalized URL
	// in the response may map to one or more caller URLs.
	out := make(map[string]int, len(urls))
	for normURL, total := range parsed.Counts {
		for _, originalURL := range callerByNormalized[normURL] {
			out[originalURL] = total
		}
	}
	return out, nil
}

// LatestCommentsRequest is the payload for POST /v1/content/comments/latest.
type LatestCommentsRequest struct {
	URLs []string `json:"urls"`
}

// LatestComment describes the single most-recent comment on a post, regardless
// of blessing status. URL is the DS canonical (.md) comment URL; AuthorDomain
// is the commenter's domain; Published is the comment's frontmatter timestamp
// (may be empty for legacy comments registered without one).
type LatestComment struct {
	URL          string `json:"url"`
	AuthorDomain string `json:"author_domain"`
	Published    string `json:"published"`
	Version      string `json:"version"`
}

// LatestCommentsResponse is the response from the latest-comment endpoint.
// Latest is keyed by the normalized post URL (DS canonical form). Posts with
// no comments are omitted from the response.
type LatestCommentsResponse struct {
	Latest map[string]LatestComment `json:"latest"`
}

// FetchLatestCommentsCtx queries the DS for the single most-recent comment on
// a batch of post URLs (blessed or not). Returns a map keyed by the URL form
// the CALLER provided (not the normalized form) so callers don't re-map the
// result. Posts with no comments are absent from the map.
//
// Empty input short-circuits with an empty map — no DS roundtrip.
//
// The provided context governs the request lifetime; stream-handler callers
// pass a strict timeout so read-focus never blocks beyond their budget.
func (c *Client) FetchLatestCommentsCtx(ctx context.Context, urls []string) (map[string]LatestComment, error) {
	if len(urls) == 0 {
		return map[string]LatestComment{}, nil
	}

	// Normalize for the wire, but remember the caller's form so the returned
	// map keys round-trip exactly (mirrors FetchCommentCountsCtx).
	normalized := make([]string, 0, len(urls))
	callerByNormalized := make(map[string][]string, len(urls))
	for _, u := range urls {
		n := normalizeURLForDS(u)
		if _, seen := callerByNormalized[n]; !seen {
			normalized = append(normalized, n)
		}
		callerByNormalized[n] = append(callerByNormalized[n], u)
	}

	body, err := json.Marshal(LatestCommentsRequest{URLs: normalized})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := c.BaseURL + "/v1/content/comments/latest"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.doWithTiming(httpReq)
	if err != nil {
		return nil, fmt.Errorf("latest comments request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDSResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("latest comments failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed LatestCommentsResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// Re-key by the caller's URL form. Each normalized URL in the response
	// may map to one or more caller URLs.
	out := make(map[string]LatestComment, len(urls))
	for normURL, lc := range parsed.Latest {
		for _, originalURL := range callerByNormalized[normURL] {
			out[originalURL] = lc
		}
	}
	return out, nil
}

// ============================================================================
// Relationship Methods
// ============================================================================

// UpdateRelationship updates a relationship status (grant/deny blessing).
// The privateKey is used to sign the request payload.
//
// R20-C-F2 (2026-05-18): a fresh ISO 8601 timestamp is added to the
// canonical payload at signing time. DS validates within ±5min of
// server clock to defeat replay; captured grant signatures can no
// longer be re-submitted later to revert a deny back to granted.
func (c *Client) UpdateRelationship(relType, sourceURL, targetURL, action string, privateKey []byte) error {
	endpoint := c.BaseURL + "/v1/relationships"

	timestamp := time.Now().UTC().Format(time.RFC3339)

	// Create canonical payload for signing (must match DS buildRelationshipCanonicalJSON).
	// Uses marshalCanonical to avoid Go's default HTML escaping (<, >, &).
	canonicalPayload := relationshipCanonicalPayload{
		Type:      relType,
		SourceURL: sourceURL,
		TargetURL: targetURL,
		Action:    action,
		Timestamp: timestamp,
	}
	canonicalJSON, err := marshalCanonical(canonicalPayload)
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
		Timestamp: timestamp,
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
	if len(result.Records) > maxDSResponseRecords {
		return nil, fmt.Errorf("DS response exceeds record limit: %d > %d", len(result.Records), maxDSResponseRecords)
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
// Must match DS buildRelationshipCanonicalJSON:
// {type, source_url, target_url, action, timestamp}
//
// R20-C-F2 (2026-05-18): timestamp added for replay defense. DS
// rejects timestamps further than ±5min from server clock.
type relationshipCanonicalPayload struct {
	Type      string `json:"type"`
	SourceURL string `json:"source_url"`
	TargetURL string `json:"target_url"`
	Action    string `json:"action"`
	Timestamp string `json:"timestamp"`
}

// MakeContentCanonicalJSON creates canonical JSON for content registration signing.
func MakeContentCanonicalJSON(contentType, contentURL, version, author string, metadata map[string]interface{}) ([]byte, error) {
	return marshalCanonical(contentCanonicalPayload{
		Type:     contentType,
		URL:      contentURL,
		Version:  version,
		Author:   author,
		Metadata: metadata,
	})
}

// MakeContentUnregisterCanonicalJSON creates the deterministic canonical JSON for
// content unregistration signing. Must match DS
// buildContentUnregisterCanonicalJSON: {type, url, timestamp}.
//
// R20-C-F1 (2026-05-18): timestamp added for replay defense.
func MakeContentUnregisterCanonicalJSON(contentType, contentURL, timestamp string) string {
	b, _ := marshalCanonical(struct {
		Type      string `json:"type"`
		URL       string `json:"url"`
		Timestamp string `json:"timestamp"`
	}{
		Type:      contentType,
		URL:       contentURL,
		Timestamp: timestamp,
	})
	return string(b)
}

// MakeContentUnpublishCanonicalJSON creates the deterministic canonical JSON for
// content unpublish signing. Same shape as unregister: {type, url, timestamp}.
func MakeContentUnpublishCanonicalJSON(contentType, contentURL, timestamp string) string {
	return MakeContentUnregisterCanonicalJSON(contentType, contentURL, timestamp)
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

// 06-profiles Phase 3: site directory listing.

// SiteListOptions configures the ListSites client call. Cursor is
// opaque (round-trip from a prior response); sort is "name" or
// "activity"; search is a case-insensitive substring match against
// domain OR author_name; limit clamps to the server's max (100).
type SiteListOptions struct {
	Sort   string // "name" (default) | "activity"
	Limit  int    // 1–100; server clamps. 0 → server default (50).
	Cursor string // opaque from prior response; "" for first page
	Search string // optional case-insensitive substring
}

// SiteListEntry is one row in the directory response. Mirrors the DS
// ds_sites_with_activity view; the email column is NOT included by
// design (server omits at the source).
//
// Note: the DS also ships `id` (BIGINT row id) and `post_count` (BIGINT
// from a subquery) on each row, but we don't decode them. postgres.js
// serializes BIGINT as a JSON string to preserve precision past 2^53,
// which doesn't round-trip into Go int64 via the default Unmarshal.
// Since we don't actually use either field, we let Go ignore them
// rather than introduce a json.Number / string-tolerant decoder.
type SiteListEntry struct {
	Domain                string `json:"domain"`
	RegistryURL           string `json:"registry_url"`
	AuthorName            string `json:"author_name,omitempty"`
	Description           string `json:"description,omitempty"`
	CreatedAt             string `json:"created_at,omitempty"`
	UpdatedAt             string `json:"updated_at,omitempty"`
	LastActiveAt          string `json:"last_active_at,omitempty"`
	// Actor's most-recent active post; all three are empty when the
	// actor has no posts yet.
	RecentPostURL         string `json:"recent_post_url,omitempty"`
	RecentPostTitle       string `json:"recent_post_title,omitempty"`
	RecentPostPublishedAt string `json:"recent_post_published_at,omitempty"`
}

// SiteListResponse is the response shape for GET /v1/sites/list.
// NextCursor is empty when there are no more pages.
type SiteListResponse struct {
	Rows       []SiteListEntry `json:"rows"`
	NextCursor string          `json:"next_cursor,omitempty"`
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

// siteRegistrationPayload is the canonical payload structure for site
// registration / unregistration signing.
//
// R20-C-F3 (2026-05-18): `Timestamp` is omitempty so the register
// flow continues to produce the legacy `{version, action, domain}`
// canonical (register is naturally non-replayable because DS upserts
// by domain). For `unregister` the caller populates Timestamp; DS
// validates ±5min freshness and rejects replays.
type siteRegistrationPayload struct {
	Version   int    `json:"version"`
	Action    string `json:"action"`
	Domain    string `json:"domain"`
	Timestamp string `json:"timestamp,omitempty"`
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
//
// R20-C-F3 (2026-05-18): Timestamp added — DS requires it for replay
// defense; signed canonical includes it; freshness ±5min.
type siteUnregisterRequest struct {
	Version   int    `json:"version"`
	Action    string `json:"action"`
	Domain    string `json:"domain"`
	Timestamp string `json:"timestamp"`
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

// ListSites queries the discovery service's paginated site directory.
// Returns one page of registered sites + a cursor for the next page
// (empty when the page is the last one). The underlying endpoint is
// unauthenticated and unsigned — the response is informational
// directory data, not security-critical attestation; callers that
// need a verified site record should follow up with CheckSiteRegistration.
//
// Added for 06-profiles Phase 3: the webapp's /api/profiles?scope=all-polis
// proxies through this method.
func (c *Client) ListSites(opts SiteListOptions) (*SiteListResponse, error) {
	q := url.Values{}
	if opts.Sort != "" {
		q.Set("sort", opts.Sort)
	}
	if opts.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Search != "" {
		q.Set("search", opts.Search)
	}

	endpoint := c.BaseURL + "/v1/sites/list"
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

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

	if resp.StatusCode == 404 {
		// Old DS that hasn't deployed Phase 3 yet. Bubble up a typed
		// sentinel so callers can render "feature requires DS upgrade"
		// rather than a generic 500.
		return nil, ErrListSitesNotDeployed
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("list sites failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result SiteListResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &result, nil
}

// ErrListSitesNotDeployed signals that the DS the client is pointing
// at hasn't shipped /v1/sites/list yet (06-profiles Phase 3). The
// webapp converts this into a 503 with a human-readable message so
// the SPA can degrade gracefully under "all of polis" qualifier
// during DS upgrade windows.
var ErrListSitesNotDeployed = fmt.Errorf("DS /v1/sites/list endpoint not available")

// RegisterSite registers a domain with the discovery service.
func (c *Client) RegisterSite(domain string, privateKey []byte, email, authorName string) (*SiteRegisterResponse, error) {
	endpoint := c.BaseURL + "/v1/sites"

	canonicalPayload := siteRegistrationPayload{
		Version: 1,
		Action:  "register",
		Domain:  domain,
	}
	canonicalJSON, err := marshalCanonical(canonicalPayload)
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
//
// R20-C-F3 (2026-05-18): a fresh ISO 8601 timestamp is added to the
// canonical payload at signing time. DS validates within ±5min of
// server clock to defeat replay; a captured unregister signature can
// no longer be re-submitted later to delete a re-registered site.
func (c *Client) UnregisterSite(domain string, privateKey []byte) (*SiteUnregisterResponse, error) {
	endpoint := c.BaseURL + "/v1/sites/unregister"

	timestamp := time.Now().UTC().Format(time.RFC3339)

	canonicalPayload := siteRegistrationPayload{
		Version:   1,
		Action:    "unregister",
		Domain:    domain,
		Timestamp: timestamp,
	}
	canonicalJSON, err := marshalCanonical(canonicalPayload)
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
		Timestamp: timestamp,
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
	if c.CreatedAfter != "" {
		params.Set("created_after", c.CreatedAfter)
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
	if len(result.Events) > maxDSResponseRecords {
		return nil, fmt.Errorf("DS response exceeds record limit: %d > %d", len(result.Events), maxDSResponseRecords)
	}

	if err := c.verifyResponse(respBody, result.DSSignature, result.DSKeyID); err != nil {
		return nil, fmt.Errorf("stream query: %w", err)
	}

	return &result, nil
}

// StreamQueryInvolved queries events where the domain is involved as either
// target or source (OR). Replaces separate target + source queries.
func (c *Client) StreamQueryInvolved(since string, limit int, domain string) (*StreamQueryResponse, error) {
	params := url.Values{}
	if since != "" {
		params.Set("since", since)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	params.Set("involved", domain)

	endpoint := c.BaseURL + "/v1/stream?" + params.Encode()

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
		return nil, fmt.Errorf("stream query (involved) failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result StreamQueryResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(result.Events) > maxDSResponseRecords {
		return nil, fmt.Errorf("DS response exceeds record limit: %d > %d", len(result.Events), maxDSResponseRecords)
	}

	if err := c.verifyResponse(respBody, result.DSSignature, result.DSKeyID); err != nil {
		return nil, fmt.Errorf("stream query (involved): %w", err)
	}

	return &result, nil
}

// StreamBatchFilter represents a single filter set in a batch request.
type StreamBatchFilter struct {
	Since    string   `json:"since"`
	Limit    int      `json:"limit"`
	Types    []string `json:"type,omitempty"`
	Actors   []string `json:"actor,omitempty"`
	Target   string   `json:"targetDomain,omitempty"`
	Source   string   `json:"sourceDomain,omitempty"`
	Involved string   `json:"involvedDomain,omitempty"`
}

// StreamBatchResponse is the response from POST /v1/stream/batch.
type StreamBatchResponse struct {
	Results     []StreamQueryResponse `json:"results"`
	DSSignature string                `json:"ds_signature,omitempty"`
	DSKeyID     string                `json:"ds_key_id,omitempty"`
}

// StreamQueryBatch sends multiple filter sets in a single HTTP request.
func (c *Client) StreamQueryBatch(queries []StreamBatchFilter) (*StreamBatchResponse, error) {
	endpoint := c.BaseURL + "/v1/stream/batch"

	reqBody := struct {
		Queries []StreamBatchFilter `json:"queries"`
	}{Queries: queries}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal batch request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
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
		return nil, fmt.Errorf("stream batch query failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result StreamBatchResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	totalEvents := 0
	for _, r := range result.Results {
		totalEvents += len(r.Events)
	}
	if totalEvents > maxDSResponseRecords {
		return nil, fmt.Errorf("DS response exceeds record limit: %d > %d", totalEvents, maxDSResponseRecords)
	}

	if err := c.verifyResponse(respBody, result.DSSignature, result.DSKeyID); err != nil {
		return nil, fmt.Errorf("stream batch query: %w", err)
	}

	return &result, nil
}

// StreamQueryUnified queries the stream with OR-combined filters in a single
// DB query. Combines involved domain + followed actors for maximum efficiency.
func (c *Client) StreamQueryUnified(since string, limit int, involvedDomain string, actors []string) (*StreamQueryResponse, error) {
	params := url.Values{}
	if since != "" {
		params.Set("since", since)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if involvedDomain != "" {
		params.Set("involved", involvedDomain)
	}
	if len(actors) > 0 {
		params.Set("actor", JoinDomains(actors))
	}

	endpoint := c.BaseURL + "/v1/stream/unified?" + params.Encode()

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
		return nil, fmt.Errorf("stream unified query failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result StreamQueryResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(result.Events) > maxDSResponseRecords {
		return nil, fmt.Errorf("DS response exceeds record limit: %d > %d", len(result.Events), maxDSResponseRecords)
	}

	if err := c.verifyResponse(respBody, result.DSSignature, result.DSKeyID); err != nil {
		return nil, fmt.Errorf("stream unified query: %w", err)
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
	return marshalCanonical(canonical)
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
	return marshalCanonical(keyRotationCanonical{
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
