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

// Client is an HTTP client for fetching remote content.
type Client struct {
	HTTPClient *http.Client
}

// NewClient creates a new remote content client.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// WellKnown represents the .well-known/polis file structure.
type WellKnown struct {
	Version    string `json:"version"`
	Author     string `json:"author"`
	Domain     string `json:"domain,omitempty"`
	Email      string `json:"email,omitempty"`
	PublicKey  string `json:"public_key"`
	Created    string `json:"created"`
	SiteTitle  string `json:"site_title,omitempty"`
	BaseURL    string `json:"base_url,omitempty"`
	Config     Config `json:"config,omitempty"`
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
func (c *Client) FetchContent(url string) (string, error) {
	resp, err := c.HTTPClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fetch failed with status %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
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
	url := baseURL + "/metadata/manifest.json"

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
	url := baseURL + "/metadata/public.jsonl"

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
