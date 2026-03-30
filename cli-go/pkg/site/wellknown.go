package site

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
)

// BundleEntry represents a bundle registration in .well-known/polis.
type BundleEntry struct {
	Active bool   `json:"active"`
	Path   string `json:"path"`
}

// AvatarConfig represents custom avatar styling for a polis site.
type AvatarConfig struct {
	BG           string `json:"bg"`
	FG           string `json:"fg"`
	Border       string `json:"border,omitempty"`
	BorderW      int    `json:"border_w,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	PatternColor string `json:"pattern_color,omitempty"`
}

// WellKnown represents the .well-known/polis v2 file structure.
// This is the identity document and bundle registry for a polis site.
type WellKnown struct {
	Version     string                 `json:"version"`
	PublicKey   string                 `json:"public_key"`
	Author      string                 `json:"author"`
	Email       string                 `json:"email,omitempty"`
	SiteTitle   string                 `json:"site_title,omitempty"`
	AuthorName  string                 `json:"author_name,omitempty"`
	Avatar      *AvatarConfig          `json:"avatar,omitempty"`
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

// GenerateDefaultAvatar creates a random avatar config with a contrast-safe
// background color and white foreground. The avatar has no border or pattern —
// those are optional customizations users can add later via the webapp.
func GenerateDefaultAvatar() *AvatarConfig {
	const fg = "#ffffff"
	for i := 0; i < 10; i++ {
		h := rand.Float64() * 360
		s := 25 + rand.Float64()*35 // [25, 60]
		l := 25 + rand.Float64()*30 // [25, 55]
		bg := hslToHex(h, s, l)
		if ContrastRatio(bg, fg) >= 4.5 {
			return &AvatarConfig{BG: bg, FG: fg}
		}
	}
	// Fallback: known contrast-safe color
	return &AvatarConfig{BG: "#2a5a6a", FG: fg}
}

// hslToHex converts HSL values (h: 0-360, s: 0-100, l: 0-100) to a #rrggbb hex string.
func hslToHex(h, s, l float64) string {
	s /= 100
	l /= 100
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2

	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}

	ri := int(math.Round((r + m) * 255))
	gi := int(math.Round((g + m) * 255))
	bi := int(math.Round((b + m) * 255))
	return fmt.Sprintf("#%02x%02x%02x", ri, gi, bi)
}

// hexToRGB parses a #rrggbb hex string into r, g, b components (0-255).
func hexToRGB(hex string) (int, int, int) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0
	}
	var r, g, b int
	fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b)
	return r, g, b
}

// relativeLuminance computes the WCAG 2.0 relative luminance for an sRGB color.
func relativeLuminance(r, g, b int) float64 {
	linearize := func(v int) float64 {
		s := float64(v) / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linearize(r) + 0.7152*linearize(g) + 0.0722*linearize(b)
}

// ContrastRatio computes the WCAG 2.0 contrast ratio between two #rrggbb hex colors.
func ContrastRatio(hex1, hex2 string) float64 {
	r1, g1, b1 := hexToRGB(hex1)
	r2, g2, b2 := hexToRGB(hex2)
	l1 := relativeLuminance(r1, g1, b1)
	l2 := relativeLuminance(r2, g2, b2)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}
