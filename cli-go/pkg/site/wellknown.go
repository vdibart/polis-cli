package site

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WellKnownDirectories contains directory path configuration.
type WellKnownDirectories struct {
	Keys     string `json:"keys,omitempty"`
	Posts    string `json:"posts,omitempty"`
	Comments string `json:"comments,omitempty"`
	Snippets string `json:"snippets,omitempty"`
	Themes   string `json:"themes,omitempty"`
	Versions string `json:"versions,omitempty"`
}

// WellKnownFiles contains file path configuration.
type WellKnownFiles struct {
	PublicIndex     string `json:"public_index,omitempty"`
	BlessedComments string `json:"blessed_comments,omitempty"`
	FollowingIndex  string `json:"following_index,omitempty"`
}

// WellKnownConfig contains the config section with directories and files.
type WellKnownConfig struct {
	Directories WellKnownDirectories `json:"directories,omitempty"`
	Files       WellKnownFiles       `json:"files,omitempty"`
}

// WellKnown represents the .well-known/polis file structure.
// This struct supports both canonical fields (bash CLI) and webapp-specific fields.
type WellKnown struct {
	// Canonical fields (bash CLI)
	Version   string           `json:"version,omitempty"`
	Author    string           `json:"author,omitempty"`
	Email     string           `json:"email,omitempty"` // Private by default; only serialized if user opts in
	PublicKey string           `json:"public_key"`
	SiteTitle string           `json:"site_title,omitempty"`
	Created   string           `json:"created,omitempty"`
	Config    *WellKnownConfig `json:"config,omitempty"`

	// Deprecated fields (kept for backward compat read/upgrade, not written by new code)
	Domain        string `json:"domain,omitempty"`         // Use POLIS_BASE_URL env var instead
	Subdomain     string `json:"subdomain,omitempty"`      // Derived from POLIS_BASE_URL at runtime
	BaseURL       string `json:"base_url,omitempty"`       // Use POLIS_BASE_URL env var instead
	PublicKeyPath string `json:"public_key_path,omitempty"`
	Generator     string `json:"generator,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"` // Use Created instead
}

// LoadWellKnown reads and parses the .well-known/polis file from a site directory.
func LoadWellKnown(siteDir string) (*WellKnown, error) {
	path := filepath.Join(siteDir, ".well-known", "polis")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var wk WellKnown
	if err := json.Unmarshal(data, &wk); err != nil {
		return nil, err
	}

	return &wk, nil
}

// SaveWellKnown writes the .well-known/polis file to a site directory.
func SaveWellKnown(siteDir string, wk *WellKnown) error {
	// Ensure .well-known directory exists
	dir := filepath.Join(siteDir, ".well-known")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(wk, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(dir, "polis")
	return os.WriteFile(path, data, 0644)
}

// GetSiteTitle returns the site title from .well-known/polis.
// Note: Does NOT fall back to base_url - that's runtime config from POLIS_BASE_URL env var.
func GetSiteTitle(siteDir string) string {
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return ""
	}
	return wk.SiteTitle
}

// GetPublicKey returns the public key from .well-known/polis.
func GetPublicKey(siteDir string) string {
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return ""
	}
	return wk.PublicKey
}

