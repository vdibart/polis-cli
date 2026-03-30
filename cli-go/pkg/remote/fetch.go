// Package remote provides HTTP fetching and .well-known retrieval for remote polis sites.
package remote

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxBodySize is the maximum response body size for remote content fetches (10MB).
const maxBodySize = 10 * 1024 * 1024

// Client is an HTTP client for fetching remote content.
type Client struct {
	HTTPClient *http.Client
	Cache      *LRUCache // Optional response cache; nil = no caching
}

// NewClient creates a new remote content client with a private HTTP client.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewClientWithHTTP creates a remote content client using a shared HTTP client.
// This enables connection pooling when multiple Client instances share the same
// underlying http.Client (and its transport).
func NewClientWithHTTP(hc *http.Client) *Client {
	if hc == nil {
		return NewClient()
	}
	return &Client{HTTPClient: hc}
}

// RemoteAvatarConfig represents custom avatar styling from a remote .well-known/polis.
type RemoteAvatarConfig struct {
	BG           string `json:"bg"`
	FG           string `json:"fg"`
	Border       string `json:"border,omitempty"`
	BorderW      int    `json:"border_w,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	PatternColor string `json:"pattern_color,omitempty"`
}

// WellKnown represents the .well-known/polis file structure.
type WellKnown struct {
	Version    string              `json:"version"`
	Author     string              `json:"author"`
	Domain     string              `json:"domain,omitempty"`
	Email      string              `json:"email,omitempty"`
	PublicKey  string              `json:"public_key"`
	Created    string              `json:"created"`
	SiteTitle  string              `json:"site_title,omitempty"`
	AuthorName string              `json:"author_name,omitempty"`
	Avatar     *RemoteAvatarConfig `json:"avatar,omitempty"`
	BaseURL    string              `json:"base_url,omitempty"`
	Config     Config              `json:"config,omitempty"`
}

// Config holds the configuration section from .well-known/polis.
type Config struct {
	Directories DirConfig  `json:"directories,omitempty"`
	Files       FileConfig `json:"files,omitempty"`
}

// DirConfig holds directory path configuration.
type DirConfig struct {
	Keys     string `json:"keys,omitempty"`
	Posts    string `json:"posts,omitempty"`
	Comments string `json:"comments,omitempty"`
	Snippets string `json:"snippets,omitempty"`
	Themes   string `json:"themes,omitempty"`
	Versions string `json:"versions,omitempty"`
}

// FileConfig holds file path configuration.
type FileConfig struct {
	PublicIndex     string `json:"public_index,omitempty"`
	BlessedComments string `json:"blessed_comments,omitempty"`
	FollowingIndex  string `json:"following_index,omitempty"`
}

// Manifest represents a polis site's manifest.json file.
type Manifest struct {
	Version       string `json:"version"`
	SiteTitle     string `json:"site_title,omitempty"`
	LastPublished string `json:"last_published"`
	PostCount     int    `json:"post_count"`
	CommentCount  int    `json:"comment_count"`
}

// PublicIndexEntry represents a single line in public.jsonl.
type PublicIndexEntry struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Path      string `json:"path"`
	URL       string `json:"url"`
	Published string `json:"published"`
	Hash      string `json:"current_version"`
}

// GetPath returns the entry's path, preferring the "path" field,
// falling back to "url" for backwards compatibility.
func (e PublicIndexEntry) GetPath() string {
	if e.Path != "" {
		return e.Path
	}
	return e.URL
}

// FetchContent fetches content from a URL and returns it as a string.
// If a Cache is configured, checks it first and stores successful responses.
func (c *Client) FetchContent(url string) (string, error) {
	if c.Cache != nil {
		if cached, ok := c.Cache.Get(url); ok {
			return cached, nil
		}
	}

	resp, err := c.HTTPClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fetch failed with status %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	content := string(body)
	if c.Cache != nil {
		c.Cache.Set(url, content, TTLForURL(url))
	}

	return content, nil
}

// FetchWellKnown fetches and parses the .well-known/polis file from a base URL.
func (c *Client) FetchWellKnown(baseURL string) (*WellKnown, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	url := baseURL + "/.well-known/polis"

	content, err := c.FetchContent(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch .well-known/polis: %w", err)
	}

	var wk WellKnown
	if err := json.Unmarshal([]byte(content), &wk); err != nil {
		return nil, fmt.Errorf("failed to parse .well-known/polis: %w", err)
	}

	return &wk, nil
}

// FetchPublicKey fetches the public key from a site's .well-known/polis file.
func (c *Client) FetchPublicKey(baseURL string) (string, error) {
	wk, err := c.FetchWellKnown(baseURL)
	if err != nil {
		return "", err
	}
	return wk.PublicKey, nil
}

// FetchAuthorEmail fetches the author email from a site's .well-known/polis file.
func (c *Client) FetchAuthorEmail(baseURL string) (string, error) {
	wk, err := c.FetchWellKnown(baseURL)
	if err != nil {
		return "", err
	}
	return wk.Email, nil
}

// FetchManifest fetches and parses the manifest.json from a site.
func (c *Client) FetchManifest(baseURL string) (*Manifest, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	url := baseURL + "/.well-known/polis"

	content, err := c.FetchContent(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest.json: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest.json: %w", err)
	}

	return &manifest, nil
}

// FetchPublicIndex fetches and parses the public.jsonl index from a site.
func (c *Client) FetchPublicIndex(baseURL string) ([]PublicIndexEntry, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	url := baseURL + "/content/pub.polis.core/index.jsonl"

	content, err := c.FetchContent(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch public.jsonl: %w", err)
	}

	var entries []PublicIndexEntry
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry PublicIndexEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // Skip malformed lines
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// Policy represents a single line from a remote rules.jsonl file.
// Duplicated from the policy package to avoid a circular import.
type Policy struct {
	Active bool   `json:"active"`
	Rule   string `json:"policy"`
}

// FetchPolicies fetches and parses the public policies/rules.jsonl from a site.
// Returns empty slice (not error) on 404.
func (c *Client) FetchPolicies(baseURL string) ([]Policy, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	url := baseURL + "/policies/rules.jsonl"

	resp, err := c.HTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch policies: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch policies failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("failed to read policies response: %w", err)
	}

	var policies []Policy
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p Policy
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			continue // skip malformed
		}
		policies = append(policies, p)
	}
	return policies, nil
}

// FetchFollowingList fetches the following.json from a site and returns the
// list of followed domains. Returns empty slice (not error) on 404.
func (c *Client) FetchFollowingList(baseURL string) ([]string, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	url := baseURL + "/content/pub.polis.core/follow/following.json"

	resp, err := c.HTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch following.json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch following.json failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("failed to read following.json response: %w", err)
	}

	var f struct {
		Following []struct {
			URL string `json:"url"`
		} `json:"following"`
	}
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("failed to parse following.json: %w", err)
	}

	domains := make([]string, 0, len(f.Following))
	for _, entry := range f.Following {
		domain := extractDomainFromURL(entry.URL)
		if domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains, nil
}

// extractDomainFromURL extracts the hostname from a URL string.
func extractDomainFromURL(rawURL string) string {
	s := rawURL
	if idx := strings.Index(s, "://"); idx != -1 {
		s = s[idx+3:]
	}
	if idx := strings.Index(s, "/"); idx != -1 {
		s = s[:idx]
	}
	if idx := strings.Index(s, ":"); idx != -1 {
		s = s[:idx]
	}
	return s
}

// ExtractBaseURL extracts the base URL (scheme + host) from a full URL.
func ExtractBaseURL(fullURL string) string {
	// Find the third slash (after scheme://)
	slashCount := 0
	for i, c := range fullURL {
		if c == '/' {
			slashCount++
			if slashCount == 3 {
				return fullURL[:i]
			}
		}
	}
	// No path, return as-is
	return fullURL
}

// TryAlternateExtension tries to fetch content with an alternate extension.
// If the URL ends in .html, tries .md; if it ends in .md, tries .html.
func (c *Client) TryAlternateExtension(url string) (string, string, error) {
	var altURL string

	if strings.HasSuffix(url, ".html") {
		altURL = strings.TrimSuffix(url, ".html") + ".md"
	} else if strings.HasSuffix(url, ".md") {
		altURL = strings.TrimSuffix(url, ".md") + ".html"
	} else {
		return "", "", fmt.Errorf("URL has no recognized extension")
	}

	content, err := c.FetchContent(altURL)
	if err != nil {
		return "", "", err
	}

	return content, altURL, nil
}
