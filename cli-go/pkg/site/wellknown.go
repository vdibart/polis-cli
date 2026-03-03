package site

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// BundleEntry represents a bundle registration in .well-known/polis.
type BundleEntry struct {
	Active bool   `json:"active"`
	Path   string `json:"path"`
}

// WellKnown represents the .well-known/polis v2 file structure.
// This is the identity document and bundle registry for a polis site.
type WellKnown struct {
	Version     string                 `json:"version"`
	PublicKey   string                 `json:"public_key"`
	Author      string                 `json:"author"`
	Email       string                 `json:"email,omitempty"`
	SiteTitle   string                 `json:"site_title,omitempty"`
	Created     string                 `json:"created"`
	ActiveTheme string                 `json:"active_theme,omitempty"`
	Bundles     map[string]BundleEntry `json:"bundles,omitempty"`
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
	dir := filepath.Join(siteDir, ".well-known")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(wk, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	path := filepath.Join(dir, "polis")
	return os.WriteFile(path, data, 0644)
}

// GetSiteTitle returns the site title from .well-known/polis.
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

// GetActiveTheme returns the active theme from .well-known/polis.
func GetActiveTheme(siteDir string) string {
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return ""
	}
	return wk.ActiveTheme
}

// SetActiveTheme updates the active theme in .well-known/polis.
func SetActiveTheme(siteDir, theme string) error {
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return err
	}
	wk.ActiveTheme = theme
	return SaveWellKnown(siteDir, wk)
}
