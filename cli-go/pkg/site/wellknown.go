package site

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

// BundleEntry represents a bundle registration in .well-known/polis.
//
// Listing-is-activation: presence in bundles[] means the bundle is active.
// There used to be an `Active bool` field here; it was always true and only
// read in one place (a no-op skip in site.Validate). Step-01 cleanup C4
// removed it. A shipped Patrol/Medic migration strips the legacy field
// from existing tenants' .well-known/polis — see stripBundleActiveFields.
type BundleEntry struct {
	Path string `json:"path"`
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
//
// active_theme used to live here but moved to .polis/bundles/registry.json in
// step-01/1e — read it via bundle.GetActiveThemeName(siteDir).
type WellKnown struct {
	Version    string                 `json:"version"`
	PublicKey  string                 `json:"public_key"`
	Email      string                 `json:"email,omitempty"`
	SiteTitle  string                 `json:"site_title,omitempty"`
	AuthorName string                 `json:"author_name"`
	Avatar     *AvatarConfig          `json:"avatar,omitempty"`
	Created    string                 `json:"created"`
	Bundles    map[string]BundleEntry `json:"bundles,omitempty"`
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

// LoadWellKnownRaw reads .well-known/polis as a raw map without struct-shape
// constraints. Distinguishes three cases via return signature:
//
//	(nil, nil) — file does not exist (not an error)
//	(nil, err) — file present but read failure or malformed JSON
//	(raw, nil) — file present and parsed OK
//
// Downstream integrity checks (e.g. patrol F2/F3/F4) consume the error to
// report malformed-but-present distinctly from absent. Pre-existing helpers
// that want "assume-absent-on-any-error" semantics discard the error.
func LoadWellKnownRaw(siteDir string) (map[string]interface{}, error) {
	path := filepath.Join(siteDir, ".well-known", "polis")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// SaveWellKnown writes the .well-known/polis file to a site directory.
// Atomic: a crash mid-write cannot leave the trust anchor truncated.
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

	return atomicfile.WriteFile(filepath.Join(dir, "polis"), data, 0644)
}

// SaveWellKnownRaw writes a raw map back to .well-known/polis with
// pretty-printing. Use when callers mutate a raw JSON map (to preserve fields
// not modeled by the WellKnown struct) and need to write it back atomically.
// Mirrors SaveWellKnown but takes a map; the on-disk format is identical.
func SaveWellKnownRaw(siteDir string, raw map[string]interface{}) error {
	dir := filepath.Join(siteDir, ".well-known")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return atomicfile.WriteFile(filepath.Join(dir, "polis"), data, 0644)
}

// LegacyPrivateContentExists reports whether the tenant has any private state
// at the pre-1f location (.polis/content/). Used by Patrol to flag tenants
// needing the rename remediation in 1g.
func LegacyPrivateContentExists(siteDir string) bool {
	root := filepath.Join(siteDir, ".polis", "content")
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

// LegacyActiveThemeInWellKnown reports whether .well-known/polis still carries
// an active_theme field that the registry migration would relocate.
//
// Assume-absent-on-error: missing file, read failures, and malformed JSON all
// return false (the legacy check can't safely assert presence if the file
// isn't readable). Routed through LoadWellKnownRaw so a single parse path
// serves both this check and the parse-error-aware patrol checks.
func LegacyActiveThemeInWellKnown(siteDir string) bool {
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil || raw == nil {
		return false
	}
	v, _ := raw["active_theme"].(string)
	return v != ""
}

// LegacyBundleActiveFieldsExist reports whether either .well-known/polis's
// bundles.<name>.active or .polis/bundles/registry.json's
// installed_bundles[].active still carries a field. The "active" flags were
// removed in step-01 cleanup C1+C4 — listing-is-activation now. This is
// idempotent: returns false once both files have been stripped.
func LegacyBundleActiveFieldsExist(siteDir string) bool {
	if wellKnownHasBundleActive(siteDir) {
		return true
	}
	return registryHasInstalledBundleActive(siteDir)
}

func wellKnownHasBundleActive(siteDir string) bool {
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil || raw == nil {
		return false
	}
	bundles, _ := raw["bundles"].(map[string]interface{})
	for _, entry := range bundles {
		em, _ := entry.(map[string]interface{})
		if _, ok := em["active"]; ok {
			return true
		}
	}
	return false
}

func registryHasInstalledBundleActive(siteDir string) bool {
	raw, err := bundle.LoadRegistryRaw(siteDir)
	if err != nil || raw == nil {
		return false
	}
	bundles, _ := raw["installed_bundles"].([]interface{})
	for _, entry := range bundles {
		em, _ := entry.(map[string]interface{})
		if _, ok := em["active"]; ok {
			return true
		}
	}
	return false
}

// StripBundleActiveFields removes the legacy "active" flag from both
// .well-known/polis (under bundles.<name>.active) and
// .polis/bundles/registry.json (under installed_bundles[].active). Idempotent:
// no-op if neither field is present. Uses raw-map mutation to preserve any
// unknown-to-the-struct fields (per the step-01 avatar-block lesson).
//
// One-time migration; pairs with LegacyBundleActiveFieldsExist + the Patrol
// check checkBundleActiveFields. Will be retired once all tenants are on
// the cleaned-up format.
func StripBundleActiveFields(siteDir string) error {
	if err := stripWellKnownBundleActive(siteDir); err != nil {
		return fmt.Errorf("strip well-known bundle.active: %w", err)
	}
	if err := stripRegistryInstalledBundleActive(siteDir); err != nil {
		return fmt.Errorf("strip registry installed_bundles.active: %w", err)
	}
	return nil
}

func stripWellKnownBundleActive(siteDir string) error {
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil || raw == nil {
		return nil // unparseable → don't risk corrupting; missing → nothing to strip
	}
	bundles, _ := raw["bundles"].(map[string]interface{})
	if bundles == nil {
		return nil
	}
	changed := false
	for _, entry := range bundles {
		em, _ := entry.(map[string]interface{})
		if em == nil {
			continue
		}
		if _, ok := em["active"]; ok {
			delete(em, "active")
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return SaveWellKnownRaw(siteDir, raw)
}

func stripRegistryInstalledBundleActive(siteDir string) error {
	regPath := filepath.Join(siteDir, ".polis", "bundles", "registry.json")
	data, err := os.ReadFile(regPath)
	if err != nil {
		return nil // missing → nothing to strip
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	bundles, _ := raw["installed_bundles"].([]interface{})
	if bundles == nil {
		return nil
	}
	changed := false
	for _, entry := range bundles {
		em, _ := entry.(map[string]interface{})
		if em == nil {
			continue
		}
		if _, ok := em["active"]; ok {
			delete(em, "active")
			changed = true
		}
	}
	if !changed {
		return nil
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(regPath, append(out, '\n'), 0600)
}

// MigratePrivateBundlesPath moves a tenant's per-bundle private state from the
// legacy .polis/content/ tree into .polis/bundles/. Per-bundle subtrees
// (posts/, comments/, dm/, etc.) move as units; the destination
// .polis/bundles/<bundle>/ may already exist (created by EnsureReferencePayload
// with shapes/ and themes/), and the moved subtrees slot in alongside.
//
// Idempotent: returns nil if .polis/content/ doesn't exist or is empty.
// Returns an error if a destination subdir already exists with conflicting
// content — won't auto-merge to avoid silent data loss.
//
// One-time migration introduced in step-01/1g; will be retired once all
// tenants are on the new layout.
func MigratePrivateBundlesPath(siteDir string) error {
	legacyRoot := filepath.Join(siteDir, ".polis", "content")
	if _, err := os.Stat(legacyRoot); os.IsNotExist(err) {
		return nil // already migrated, or never had legacy state
	}

	bundlesRoot := filepath.Join(siteDir, ".polis", "bundles")
	if err := os.MkdirAll(bundlesRoot, 0700); err != nil {
		return fmt.Errorf("ensure .polis/bundles: %w", err)
	}

	// For each bundle dir under .polis/content/<bundle>/, walk its subtrees
	// and move each into .polis/bundles/<bundle>/<subtree>.
	bundleEntries, err := os.ReadDir(legacyRoot)
	if err != nil {
		return fmt.Errorf("read legacy root: %w", err)
	}
	for _, bundleEntry := range bundleEntries {
		if !bundleEntry.IsDir() {
			continue
		}
		bundleName := bundleEntry.Name()
		legacyBundleDir := filepath.Join(legacyRoot, bundleName)
		newBundleDir := filepath.Join(bundlesRoot, bundleName)
		if err := os.MkdirAll(newBundleDir, 0700); err != nil {
			return fmt.Errorf("ensure %s: %w", newBundleDir, err)
		}
		subtrees, err := os.ReadDir(legacyBundleDir)
		if err != nil {
			return fmt.Errorf("read %s: %w", legacyBundleDir, err)
		}
		for _, sub := range subtrees {
			src := filepath.Join(legacyBundleDir, sub.Name())
			dst := filepath.Join(newBundleDir, sub.Name())
			if _, err := os.Stat(dst); err == nil {
				// Destination already exists — refuse to merge.
				return fmt.Errorf("migration ambiguous: %s already exists, won't merge from %s", dst, src)
			}
			if err := os.Rename(src, dst); err != nil {
				return fmt.Errorf("rename %s -> %s: %w", src, dst, err)
			}
		}
		// Empty bundle dir — remove.
		_ = os.Remove(legacyBundleDir)
	}

	// Remove the legacy root if empty.
	_ = os.Remove(legacyRoot)
	return nil
}

// MigrateAuthorField migrates the deprecated "author" field to "author_name"
// in .well-known/polis. If "author" exists and "author_name" is empty, the
// value is copied. The "author" key is then removed. No-op if the file is
// missing or already migrated.
func MigrateAuthorField(siteDir string) error {
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil || raw == nil {
		return nil // missing or corrupt: skip
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
	return SaveWellKnownRaw(siteDir, raw)
}

// GetActiveTheme returns the active theme name. Reads from
// .polis/bundles/registry.json (post-1e canonical) with a legacy fallback to
// active_theme in .well-known/polis for pre-migration sites.
func GetActiveTheme(siteDir string) string {
	name, _ := bundle.GetActiveThemeName(siteDir)
	return name
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
//
// Shape matches the on-page avatar (border-radius:50% in the shared theme
// CSS) — modern browsers honor the transparent SVG viewBox area, so the
// favicon renders as a true circle rather than the rounded square the
// previous rect-based markup produced. The pattern fill is applied to a
// circle so the pattern clips to the avatar shape; the border is a
// stroked circle whose stroke straddles the viewBox edge so the full
// border width is visible.
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
			patternFill = `<circle cx="64" cy="64" r="64" fill="url(#p)"/>`
			displayInitial = "" // hide initial when pattern is set (matches buildAvatarHTML)
		}
	}

	if config != nil && config.Border != "" && config.BorderW > 0 {
		// borderW*2 keeps parity with the previous rect-based scaling
		// (avatar 48px → favicon 128px). The stroke is centered at
		// r=64-bw/2, so its outer edge lands exactly on the viewBox
		// edge (r=64) and inner edge at r=64-bw — full border width
		// visible against the background fill.
		bw := config.BorderW * 2
		borderEl = fmt.Sprintf(`<circle cx="64" cy="64" r="%d" fill="none" stroke="%s" stroke-width="%d"/>`,
			64-bw/2, config.Border, bw)
	}

	// font-weight=500 matches .site-avatar in themes/_shared/base.css so
	// the initial in the favicon has the same visual weight as the
	// initial in the on-page avatar. Text centered at y=64 with
	// dominant-baseline=central.
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 128">%s<circle cx="64" cy="64" r="64" fill="%s"/>%s%s<text x="64" y="64" text-anchor="middle" dominant-baseline="central" font-family="sans-serif" font-weight="500" font-size="%d" fill="%s">%s</text></svg>`,
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
