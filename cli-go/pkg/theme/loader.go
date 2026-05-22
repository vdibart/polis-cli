// Package theme provides theme loading and management for polis.
package theme

import (
	"bufio"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

// Templates holds the loaded theme templates.
//
// blog shape populates Post/Comment/Index/etc. stream shape (step-02/2.b) populates
// Stream/StreamPost/etc. Fields not relevant to the active shape stay empty.
// Consumers must either gate reads on shape name or check field-non-empty —
// reading a v4 field when shape is v3 is a silent logic error.
//
// A parallel ShapeTemplates type was considered and rejected in 2.b for
// diff-size reasons. If additional shapes land (v5+), a more principled
// map-keyed-by-logical-name approach should replace this flat struct.
type Templates struct {
	// blog shape fields.
	Post          string // post.html - required
	Comment       string // comment.html - required
	CommentInline string // comment-inline.html - required
	Index         string // index.html - required
	Archive       string // posts.html - optional (archive page)
	Tag           string // tag.html - optional (single tag page)
	TagIndex      string // tag-index.html - optional (tag index page)

	// stream shape fields (step-02/2.b). Loaded from shapes/v4/ when
	// LoadShape is called with shapeName=="v4".
	Stream        string // stream.html - required for v4
	StreamPost    string // stream-post.html - sibling-excerpt partial
	StreamComment string // stream-comment.html - inline comment partial
	StreamProfile string // stream-profile.html - profile item (WS-2/WS-5)
	StreamMention string // stream-mention.html - mention item (WS-2/WS-5)
}

// Manifest represents the site manifest (metadata/manifest.json).
type Manifest struct {
	Version      string `json:"version"`
	ActiveTheme  string `json:"active_theme"`
	PostCount    int    `json:"post_count"`
	CommentCount int    `json:"comment_count"`
}

// LoadShape loads templates for the given shape with optional per-theme overrides.
//
// Template resolution, per file (checked in order):
//  1. Theme dir — allows a theme to override specific templates (e.g. studio13
//     overrides post.html). Resolved via resolveDir which checks
//     <dataDir>/site/themes/<themeName>/ then <cliThemesDir>/<themeName>/.
//  2. Shape dir — the tenant's installed shape fixtures at
//     <dataDir>/.polis/bundles/pub.polis.core/shapes/<shapeName>/.
//
// If the shape dir is missing (tenant not yet installed), LoadShape returns a
// clear error pointing at the expected path so operators can diagnose.
func LoadShape(dataDir, cliThemesDir, shapeName, themeName string) (*Templates, error) {
	if shapeName == "" {
		return nil, fmt.Errorf("shape name is required")
	}
	if themeName == "" {
		return nil, fmt.Errorf("theme name is required")
	}

	shapeDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "shapes", shapeName)
	if _, err := os.Stat(shapeDir); err != nil {
		return nil, fmt.Errorf("shape %q not found at %s (run polis init or wait for Patrol/Medic resync)", shapeName, shapeDir)
	}

	// Theme dir is optional — only used when a theme overrides shape markup.
	themeDir := resolveThemeDir(dataDir, cliThemesDir, themeName)

	// Dispatch on shape name. Each shape has its own filename set under
	// shapeDir and populates distinct Templates fields. Cross-shape
	// callers (e.g. render.NewPageRenderer) get a single struct back with
	// the active shape's fields populated; gating on shape name at read
	// time keeps v3 consumers unaffected by v4 extensions.
	switch shapeName {
	case "v3":
		return loadBlogTemplates(themeDir, shapeDir)
	case "v4":
		return loadStreamTemplates(themeDir, shapeDir)
	default:
		return nil, fmt.Errorf("unsupported shape %q (known: v3, v4)", shapeName)
	}
}

// resolveThemeDir finds a theme directory across the locations valid during
// the SHAPE refactor transition window:
//
//  1. Installed bundle theme (post-refactor canonical):
//     <dataDir>/.polis/bundles/pub.polis.core/themes/<name>/
//  2. Per-tenant override:
//     <dataDir>/site/themes/<name>/   (legacy; consolidated by Patrol/Medic in 1.g)
//  3. CLI-shipped repo theme (legacy, only for unmigrated themes —
//     especial, sols, studio13, turbo, zane in step 1):
//     <cliThemesDir>/<name>/
//
// Returns "" if the theme isn't found in any location. Callers must handle this
// case (e.g., LoadShape allows empty themeDir to mean "no theme overrides").
func resolveThemeDir(dataDir, cliThemesDir, name string) string {
	candidates := []string{
		filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "themes", name),
		filepath.Join(dataDir, "site", "themes", name),
	}
	if cliThemesDir != "" {
		candidates = append(candidates, filepath.Join(cliThemesDir, name))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// readWithFallback reads a template file from themeDir first, falling back to baseDir.
// An empty themeDir is treated as "no theme overrides" — the shape/base dir is the sole source.
func readWithFallback(themeDir, baseDir, filename string) (string, error) {
	// Try theme directory first (if provided)
	if themeDir != "" {
		if content, err := os.ReadFile(filepath.Join(themeDir, filename)); err == nil {
			return string(content), nil
		}
	}
	// Fall back to shape/base directory
	if baseDir != "" {
		if content, err := os.ReadFile(filepath.Join(baseDir, filename)); err == nil {
			return string(content), nil
		}
	}
	return "", fmt.Errorf("template %q not found in theme or shape", filename)
}

// loadBlogTemplates loads the blog shape template set from themeDir (if present)
// falling back to baseDir (the shape fixture dir). Original pre-step-02
// behavior preserved verbatim.
func loadBlogTemplates(themeDir, baseDir string) (*Templates, error) {
	templates := &Templates{}

	// Load required templates (theme dir first, then shape dir)
	required := map[string]*string{
		"post.html":           &templates.Post,
		"comment.html":        &templates.Comment,
		"comment-inline.html": &templates.CommentInline,
		"index.html":          &templates.Index,
	}

	for filename, dest := range required {
		content, err := readWithFallback(themeDir, baseDir, filename)
		if err != nil {
			return nil, fmt.Errorf("required template %q: %w", filename, err)
		}
		*dest = content
	}

	// Load optional templates (same fallback pattern)
	if content, err := readWithFallback(themeDir, baseDir, "posts.html"); err == nil {
		templates.Archive = content
	}
	if content, err := readWithFallback(themeDir, baseDir, "tag.html"); err == nil {
		templates.Tag = content
	}
	if content, err := readWithFallback(themeDir, baseDir, "tag-index.html"); err == nil {
		templates.TagIndex = content
	}

	return templates, nil
}

// loadStreamTemplates loads the stream shape template set (step-02/2.b). Requires
// stream.html (the per-post page shell); per-type partials (post/comment/
// profile/mention) are loaded if present. Themes may override via themeDir
// but most v4 themes are CSS-only and use the shape-shipped markup.
func loadStreamTemplates(themeDir, baseDir string) (*Templates, error) {
	templates := &Templates{}

	// stream.html is the only required template.
	content, err := readWithFallback(themeDir, baseDir, "stream.html")
	if err != nil {
		return nil, fmt.Errorf("required template %q: %w", "stream.html", err)
	}
	templates.Stream = content

	// Per-type partials — all optional. The render pipeline gates their
	// use on item-type at compose time.
	optional := map[string]*string{
		"stream-post.html":    &templates.StreamPost,
		"stream-comment.html": &templates.StreamComment,
		"stream-profile.html": &templates.StreamProfile,
		"stream-mention.html": &templates.StreamMention,
	}
	for filename, dest := range optional {
		if content, err := readWithFallback(themeDir, baseDir, filename); err == nil {
			*dest = content
		}
	}

	return templates, nil
}

// GetActiveTheme returns the active theme name. Reads from
// .polis/bundles/registry.json (post-1e canonical) with a legacy fallback to
// active_theme in .well-known/polis for pre-migration sites.
func GetActiveTheme(dataDir string) (string, error) {
	return bundle.GetActiveThemeName(dataDir)
}

// SelectRandomTheme picks a random theme from available themes and persists the choice.
// This matches the bash CLI's select_theme() behavior.
func SelectRandomTheme(dataDir, cliThemesDir string) (string, error) {
	themes, err := ListThemes(dataDir, cliThemesDir)
	if err != nil || len(themes) == 0 {
		return "", fmt.Errorf("no themes found")
	}
	selected := themes[rand.IntN(len(themes))]
	// Persist the choice so future renders use the same theme
	if err := SetActiveTheme(dataDir, selected); err != nil {
		// Non-fatal: theme works even if not saved
	}
	return selected, nil
}

// SetActiveTheme updates the active theme in .polis/bundles/registry.json.
func SetActiveTheme(dataDir, themeName string) error {
	return bundle.SetActiveThemeName(dataDir, themeName)
}

// RemoveLegacyThemeLocations blindly deletes theme directories from the legacy
// per-tenant locations:
//   - <dataDir>/site/themes/
//   - <dataDir>/.polis/themes/
//
// Per the step-01 forced-upgrade policy, any user customizations in those
// locations are lost — the canonical replacement is installed at
// .polis/bundles/pub.polis.core/themes/ via bundle.EnsureReferencePayload.
//
// One-time migration; idempotent (no-op when neither dir exists).
func RemoveLegacyThemeLocations(dataDir string) error {
	for _, root := range []string{
		filepath.Join(dataDir, "site", "themes"),
		filepath.Join(dataDir, ".polis", "themes"),
	} {
		if err := os.RemoveAll(root); err != nil {
			return fmt.Errorf("remove %s: %w", root, err)
		}
	}
	return nil
}

// LegacyThemeLocationsExist reports whether either legacy theme directory
// is present on the tenant. Used by Patrol to flag tenants needing migration.
func LegacyThemeLocationsExist(dataDir string) bool {
	for _, root := range []string{
		filepath.Join(dataDir, "site", "themes"),
		filepath.Join(dataDir, ".polis", "themes"),
	} {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// CopyBaseCSS copies the shared base CSS file to base.css at the site root.
// Shared CSS lives in the installed bundle's _shared theme — pre-refactor it
// was at themes/_base/base.css.
func CopyBaseCSS(dataDir, cliThemesDir string) error {
	destPath := filepath.Join(dataDir, "base.css")

	// Installed bundle (post-refactor canonical).
	bundlePath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "themes", "_shared", "base.css")
	if _, err := os.Stat(bundlePath); err == nil {
		return copyFile(bundlePath, destPath)
	}

	// Legacy local _base override (pre-refactor sites that haven't been
	// resynced yet — safe to keep during the transition window).
	localPath := filepath.Join(dataDir, "site", "themes", "_base", "base.css")
	if _, err := os.Stat(localPath); err == nil {
		return copyFile(localPath, destPath)
	}

	// No base.css found — not an error (older themes may not have it).
	return nil
}

// CopyCSS copies the active theme's CSS file to styles.css at the site root.
// The CSS filename matches the theme name ({themename}.css). Looked up in the
// installed bundle first, then legacy locations during the transition.
func CopyCSS(dataDir, cliThemesDir, themeName string) error {
	cssFilename := themeName + ".css"
	destPath := filepath.Join(dataDir, "styles.css")

	// Installed bundle (post-refactor canonical).
	bundlePath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "themes", themeName, cssFilename)
	if _, err := os.Stat(bundlePath); err == nil {
		return copyFile(bundlePath, destPath)
	}

	// Legacy per-tenant override.
	localCSSPath := filepath.Join(dataDir, "site", "themes", themeName, cssFilename)
	if _, err := os.Stat(localCSSPath); err == nil {
		return copyFile(localCSSPath, destPath)
	}

	// Legacy CLI-shipped theme (unmigrated themes in step 1).
	if cliThemesDir != "" {
		cliCSSPath := filepath.Join(cliThemesDir, themeName, cssFilename)
		if _, err := os.Stat(cliCSSPath); err == nil {
			return copyFile(cliCSSPath, destPath)
		}
	}

	return fmt.Errorf("CSS file not found: %s", cssFilename)
}

// CopyStreamCSS writes styles.css for the stream shape by concatenating the shape's
// structural stylesheet (shapes/v4/stream.css) with the active theme's CSS.
// Order matters: shape first, theme last, so theme custom-property overrides win.
//
// The tenant-installed bundle tree is the source of truth. Legacy CLI-shipped
// fallbacks are not consulted here because v4 support only exists in post-
// refactor sites (Patrol/Medic guarantees the shape payload is installed).
func CopyStreamCSS(dataDir, themeName string) error {
	shapeCSSPath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "shapes", "v4", "stream.css")
	shapeCSS, err := os.ReadFile(shapeCSSPath)
	if err != nil {
		return fmt.Errorf("read stream shape CSS: %w", err)
	}

	themeCSSPath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "themes", themeName, themeName+".css")
	themeCSS, err := os.ReadFile(themeCSSPath)
	if err != nil {
		return fmt.Errorf("read v4 theme CSS %q: %w", themeName, err)
	}

	destPath := filepath.Join(dataDir, "styles.css")
	out := make([]byte, 0, len(shapeCSS)+len(themeCSS)+64)
	out = append(out, shapeCSS...)
	out = append(out, '\n')
	out = append(out, themeCSS...)
	return os.WriteFile(destPath, out, 0644)
}

// CopyStreamController copies the stream shape's stream.js placeholder (WS-2 stub) to
// the tenant root so v4 pages' `<script src="/stream.js" defer>` reference
// resolves to a 200 instead of a 404. The real controller lands in step 4 and
// will trigger a shape version bump for cache-bust + tenant resync.
//
// Source: <dataDir>/.polis/bundles/pub.polis.core/shapes/v4/stream.js
//         (installed by Patrol/Medic from the embedded reference payload).
// Destination: <dataDir>/stream.js
func CopyStreamController(dataDir string) error {
	srcPath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "shapes", "v4", "stream.js")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read v4 controller stub: %w", err)
	}
	destPath := filepath.Join(dataDir, "stream.js")
	return os.WriteFile(destPath, src, 0644)
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	dest, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dest.Close()

	_, err = io.Copy(dest, source)
	return err
}

// ListThemes returns the names of all available themes.
// It combines themes from the installed bundle, the per-tenant override
// directory, and the CLI themes directory (legacy fallback for unmigrated themes).
func ListThemes(dataDir, cliThemesDir string) ([]string, error) {
	themeSet := make(map[string]bool)

	// Installed bundle themes (post-refactor canonical).
	bundleThemesDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "themes")
	if entries, err := os.ReadDir(bundleThemesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && isValidTheme(filepath.Join(bundleThemesDir, entry.Name())) {
				themeSet[entry.Name()] = true
			}
		}
	}

	// Per-tenant overrides (legacy).
	localThemesDir := filepath.Join(dataDir, "site", "themes")
	if entries, err := os.ReadDir(localThemesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && isValidTheme(filepath.Join(localThemesDir, entry.Name())) {
				themeSet[entry.Name()] = true
			}
		}
	}

	// CLI-shipped themes (legacy — only the unmigrated ones in step 1).
	if cliThemesDir != "" {
		if entries, err := os.ReadDir(cliThemesDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && isValidTheme(filepath.Join(cliThemesDir, entry.Name())) {
					themeSet[entry.Name()] = true
				}
			}
		}
	}

	var themes []string
	for name := range themeSet {
		themes = append(themes, name)
	}

	return themes, nil
}

// isValidTheme checks if a directory contains a valid theme.
// A theme is valid if it has either all required HTML templates (legacy full theme)
// or at least a CSS file (CSS-only theme that inherits templates from the shape).
// System dirs (_base, _shared) are excluded — they're not selectable themes.
func isValidTheme(themeDir string) bool {
	name := filepath.Base(themeDir)
	if name == "_base" || name == "_shared" {
		return false
	}

	// CSS-only theme: has a {name}.css file (templates come from _base)
	cssFile := filepath.Join(themeDir, name+".css")
	if _, err := os.Stat(cssFile); err == nil {
		return true
	}

	// Legacy full theme: has all 4 required templates
	required := []string{"post.html", "comment.html", "comment-inline.html", "index.html"}
	for _, file := range required {
		if _, err := os.Stat(filepath.Join(themeDir, file)); err != nil {
			return false
		}
	}
	return true
}

// GetThemeDir returns the path to a theme's directory.
//
// Checks in post-step-01 canonical order:
//   1. Installed bundle theme at .polis/bundles/pub.polis.core/themes/<name>/
//   2. Legacy per-tenant override at site/themes/<name>/
//   3. CLI-shipped theme (legacy, transition-only)
//
// Returns "" if the theme isn't found in any location. Mirrors resolveThemeDir
// but kept as a separate exported function for palette-extraction callers
// (resolveThemeDir is internal and has different fallback semantics for
// template loading).
func GetThemeDir(dataDir, cliThemesDir, themeName string) string {
	bundleDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "themes", themeName)
	if _, err := os.Stat(bundleDir); err == nil {
		return bundleDir
	}

	localDir := filepath.Join(dataDir, "site", "themes", themeName)
	if _, err := os.Stat(localDir); err == nil {
		return localDir
	}

	if cliThemesDir != "" {
		cliDir := filepath.Join(cliThemesDir, themeName)
		if _, err := os.Stat(cliDir); err == nil {
			return cliDir
		}
	}

	return ""
}

// CalculateCSSPath returns the relative path to styles.css from a given file path.
// For example:
// - posts/2026/01/post.html -> ../../../styles.css
// - comments/2026/01/comment.html -> ../../../styles.css
// - index.html -> styles.css
func CalculateCSSPath(filePath string) string {
	// Clean and normalize the path
	filePath = filepath.Clean(filePath)
	filePath = filepath.ToSlash(filePath) // Use forward slashes

	// Count directory depth
	parts := strings.Split(filePath, "/")
	depth := len(parts) - 1 // Subtract 1 for the filename

	if depth <= 0 {
		return "styles.css"
	}

	// Build relative path
	var prefix string
	for i := 0; i < depth; i++ {
		prefix += "../"
	}

	return prefix + "styles.css"
}

// CalculateHomePath returns the relative path to index.html from a given file path.
func CalculateHomePath(filePath string) string {
	// Clean and normalize the path
	filePath = filepath.Clean(filePath)
	filePath = filepath.ToSlash(filePath) // Use forward slashes

	// Count directory depth
	parts := strings.Split(filePath, "/")
	depth := len(parts) - 1 // Subtract 1 for the filename

	if depth <= 0 {
		return "index.html"
	}

	// Build relative path
	var prefix string
	for i := 0; i < depth; i++ {
		prefix += "../"
	}

	return prefix + "index.html"
}

// ThemePalette holds a theme's name and representative colors for UI display.
//
// DisplayName is the human-facing label (from bundle.Theme.DisplayName). When
// empty, the picker UI should fall back to Name. Useful when tenant-data
// identifiers diverge from branding — e.g. studio13-nk stored internally,
// labeled "studio13" in the picker.
type ThemePalette struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Colors      []string `json:"colors"` // 5 hex colors: bg, text, accent1, accent2, cyan
	Active      bool     `json:"active"`
}

// cssColorVar matches CSS custom property declarations like --color-bg: #1a1525;
var cssColorVar = regexp.MustCompile(`^\s*--color-([a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{3,8})\s*;`)

// ExtractPalette reads a theme's CSS file and extracts 5 representative colors.
// Returns bg, text, two accent colors, and cyan.
func ExtractPalette(themeDir, themeName string) ThemePalette {
	palette := ThemePalette{Name: themeName}

	cssPath := filepath.Join(themeDir, themeName+".css")
	f, err := os.Open(cssPath)
	if err != nil {
		return palette
	}
	defer f.Close()

	// Parse all --color-* variables from the :root block
	vars := make(map[string]string)
	inRoot := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ":root") {
			inRoot = true
			continue
		}
		if inRoot && trimmed == "}" {
			break
		}
		if !inRoot {
			continue
		}
		if m := cssColorVar.FindStringSubmatch(line); m != nil {
			vars[m[1]] = m[2]
		}
	}

	// Pick 5 representative colors: bg, text, accent1, accent2, cyan
	bg := vars["bg"]
	text := vars["text"]
	cyan := vars["cyan"]

	// Find two accent colors: prefer theme-specific named accents
	// (skip bg*, text*, border*, and *-soft/*-dim/*-glow variants)
	var accents []string
	for name, val := range vars {
		if name == "bg" || name == "text" || name == "cyan" {
			continue
		}
		if strings.HasPrefix(name, "bg") || strings.HasPrefix(name, "text") || strings.HasPrefix(name, "border") {
			continue
		}
		if strings.HasSuffix(name, "-soft") || strings.HasSuffix(name, "-dim") || strings.HasSuffix(name, "-glow") {
			continue
		}
		accents = append(accents, val)
	}
	// Sort for deterministic output
	sort.Strings(accents)

	accent1 := ""
	accent2 := ""
	if len(accents) >= 1 {
		accent1 = accents[0]
	}
	if len(accents) >= 2 {
		accent2 = accents[1]
	}

	palette.Colors = []string{bg, text, accent1, accent2, cyan}
	return palette
}

// ListThemesWithPalettes returns all available themes with their color palettes.
// The active theme is marked with Active=true. DisplayName is populated from
// the bundle declaration (bundle.Theme.DisplayName) when available, so picker
// UIs can show a friendlier label than the internal theme name.
func ListThemesWithPalettes(dataDir, cliThemesDir string) ([]ThemePalette, error) {
	themes, err := ListThemes(dataDir, cliThemesDir)
	if err != nil {
		return nil, err
	}

	activeTheme, _ := GetActiveTheme(dataDir)
	defaults := bundle.DefaultCoreBundle()

	var palettes []ThemePalette
	for _, name := range themes {
		themeDir := GetThemeDir(dataDir, cliThemesDir, name)
		p := ExtractPalette(themeDir, name)
		p.Active = (name == activeTheme)
		if th, ok := defaults.Themes[name]; ok && th.DisplayName != "" {
			p.DisplayName = th.DisplayName
		}
		palettes = append(palettes, p)
	}

	sort.Slice(palettes, func(i, j int) bool {
		return palettes[i].Name < palettes[j].Name
	})

	return palettes, nil
}

// ListUserSelectableThemes returns themes the picker UI should offer, filtered
// by compatibility with the tenant's active shape. Themes whose
// CompatibleShapes doesn't include the active shape are excluded. This keeps
// blog-only themes (e.g. studio13-nk with its HTML overrides) off v4 pickers,
// and stream-only themes (e.g. the future v4-era studio13) off v3 pickers.
//
// When activeShape is empty, defaults to v3 (matches bundle.GetActiveShapeName).
// Themes not declared in DefaultCoreBundle (orphans on disk) are excluded —
// installed-but-undeclared is a disk-drift condition covered by Patrol's
// orphan-dir check.
func ListUserSelectableThemes(dataDir, cliThemesDir, activeShape string) ([]ThemePalette, error) {
	if activeShape == "" {
		activeShape = "v3"
	}
	wantFQN := bundle.QualifyShape(activeShape)
	all, err := ListThemesWithPalettes(dataDir, cliThemesDir)
	if err != nil {
		return nil, err
	}
	defaults := bundle.DefaultCoreBundle()
	filtered := make([]ThemePalette, 0, len(all))
	for _, p := range all {
		th, ok := defaults.Themes[p.Name]
		if !ok {
			continue // not declared — don't surface
		}
		if !compatibleWithShape(th, wantFQN) {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered, nil
}

// compatibleWithShape reports whether a theme declares compatibility with the
// given fully-qualified shape FQN. An empty CompatibleShapes list means
// "compatible with none" — themes must opt in explicitly.
func compatibleWithShape(th *bundle.Theme, shapeFQN string) bool {
	for _, s := range th.CompatibleShapes {
		if s == shapeFQN {
			return true
		}
	}
	return false
}
