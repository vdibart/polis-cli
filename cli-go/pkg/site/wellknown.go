package site

import (
	"encoding/base64"
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
	Email       string                 `json:"email,omitempty"`
	SiteTitle   string                 `json:"site_title,omitempty"`
	AuthorName  string                 `json:"author_name"`
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

// MigrateAuthorField migrates the deprecated "author" field to "author_name"
// in .well-known/polis. If "author" exists and "author_name" is empty, the
// value is copied. The "author" key is then removed. No-op if the file is
// missing or already migrated.
func MigrateAuthorField(siteDir string) error {
	path := filepath.Join(siteDir, ".well-known", "polis")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // file missing is fine
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil // corrupt file, skip
	}

	author, hasAuthor := raw["author"]
	if !hasAuthor {
		return nil // already migrated
	}

	// Copy to author_name if it's empty/missing
	if authorStr, ok := author.(string); ok && authorStr != "" {
		if existing, _ := raw["author_name"].(string); existing == "" {
			raw["author_name"] = authorStr
		}
	}

	delete(raw, "author")

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0644)
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

// faviconPatterns maps pattern names to SVG generator functions for favicon rendering.
// These match the patterns in render/page.go, nav.js, and app.js.
var faviconPatterns = map[string]func(color string) string{
	"rings": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='14' cy='14' r='10' fill='none' stroke='%s' stroke-width='1.5'/><circle cx='14' cy='14' r='5' fill='none' stroke='%s' stroke-width='1'/></svg>`, c, c)
	},
	"cross": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='4' y1='4' x2='24' y2='24' stroke='%s' stroke-width='1.5'/><line x1='24' y1='4' x2='4' y2='24' stroke='%s' stroke-width='1.5'/></svg>`, c, c)
	},
	"grid": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='9' y1='0' x2='9' y2='28' stroke='%s' stroke-width='0.8'/><line x1='19' y1='0' x2='19' y2='28' stroke='%s' stroke-width='0.8'/><line x1='0' y1='9' x2='28' y2='9' stroke='%s' stroke-width='0.8'/><line x1='0' y1='19' x2='28' y2='19' stroke='%s' stroke-width='0.8'/></svg>`, c, c, c, c)
	},
	"dots": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='7' cy='7' r='2' fill='%s'/><circle cx='21' cy='7' r='2' fill='%s'/><circle cx='14' cy='14' r='2' fill='%s'/><circle cx='7' cy='21' r='2' fill='%s'/><circle cx='21' cy='21' r='2' fill='%s'/></svg>`, c, c, c, c, c)
	},
	"stripes": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='-2' y1='6' x2='6' y2='-2' stroke='%s' stroke-width='1.5'/><line x1='5' y1='13' x2='13' y2='5' stroke='%s' stroke-width='1.5'/><line x1='12' y1='20' x2='20' y2='12' stroke='%s' stroke-width='1.5'/><line x1='19' y1='27' x2='27' y2='19' stroke='%s' stroke-width='1.5'/><line x1='26' y1='34' x2='34' y2='26' stroke='%s' stroke-width='1.5'/></svg>`, c, c, c, c, c)
	},
	"diamond": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><polygon points='14,4 24,14 14,24 4,14' fill='none' stroke='%s' stroke-width='1.5'/></svg>`, c)
	},
	"halves": func(c string) string {
		return fmt.Sprintf(`<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><rect x='0' y='14' width='28' height='14' fill='%s' opacity='0.4'/></svg>`, c)
	},
}

// GenerateFaviconSVG produces an SVG favicon string from an avatar config and initial letter.
// If config is nil, a simple grey circle with the initial is generated.
// When a pattern is set, the initial is hidden (matching avatar rendering behavior).
func GenerateFaviconSVG(config *AvatarConfig, initial string) string {
	bg := "#888888"
	fg := "#ffffff"
	if config != nil {
		bg = config.BG
		fg = config.FG
	}

	// Determine initial display
	displayInitial := initial
	fontSize := 68
	if len([]rune(displayInitial)) > 1 {
		fontSize = 56
	}

	var patternDefs, patternFill, borderEl string

	if config != nil && config.Pattern != "" && config.Pattern != "none" && config.PatternColor != "" {
		if gen, ok := faviconPatterns[config.Pattern]; ok {
			svg := gen(config.PatternColor)
			b64 := base64.StdEncoding.EncodeToString([]byte(svg))
			patternDefs = fmt.Sprintf(`<defs><pattern id="p" patternUnits="userSpaceOnUse" width="28" height="28"><image href="data:image/svg+xml;base64,%s" width="28" height="28"/></pattern></defs>`, b64)
			patternFill = `<rect width="128" height="128" rx="22" fill="url(#p)"/>`
			displayInitial = "" // hide initial when pattern is set
		}
	}

	if config != nil && config.Border != "" && config.BorderW > 0 {
		bw := config.BorderW * 2
		borderEl = fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" rx="%d" fill="none" stroke="%s" stroke-width="%d"/>`,
			bw/2, bw/2, 128-bw, 128-bw, 22-bw/2, config.Border, bw)
	}

	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">%s<rect width="128" height="128" rx="22" fill="%s"/>%s%s<text x="64" y="80" text-anchor="middle" dominant-baseline="central" font-family="sans-serif" font-weight="600" font-size="%d" fill="%s">%s</text></svg>`,
		patternDefs, bg, patternFill, borderEl, fontSize, fg, displayInitial)
}

// WriteFavicon generates a favicon.svg from the avatar config in .well-known/polis
// and writes it to the site root directory. Returns an error if .well-known/polis
// cannot be read; missing avatar config produces a fallback initial-based favicon.
func WriteFavicon(siteDir string) error {
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return fmt.Errorf("load .well-known/polis: %w", err)
	}

	// Determine initial from author name, falling back to "?"
	initial := "?"
	if runes := []rune(wk.AuthorName); len(runes) > 0 {
		initial = string([]rune{runes[0]})
	}

	svg := GenerateFaviconSVG(wk.Avatar, initial)
	faviconPath := filepath.Join(siteDir, "favicon.svg")
	return os.WriteFile(faviconPath, []byte(svg), 0644)
}
