// Package theme provides theme loading and management for polis.
package theme

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Version is set at init time by cmd package.
var Version = "dev"

// Templates holds the loaded theme templates.
type Templates struct {
	Post          string // post.html - required
	Comment       string // comment.html - required
	CommentInline string // comment-inline.html - required
	Index         string // index.html - required
	Archive       string // posts.html - optional (archive page)
	Tag           string // tag.html - optional (single tag page)
	TagIndex      string // tag-index.html - optional (tag index page)
}

// Manifest represents the site manifest (metadata/manifest.json).
type Manifest struct {
	Version      string `json:"version"`
	ActiveTheme  string `json:"active_theme"`
	PostCount    int    `json:"post_count"`
	CommentCount int    `json:"comment_count"`
}

// Load loads templates from the active theme.
// It tries the local theme first (site/themes/{name}/), then falls back to CLI themes.
// For each individual template, it checks the theme directory first, then falls back
// to _base/ (shared base templates). This allows CSS-only themes that inherit all
// HTML from _base, while still permitting per-theme template overrides.
func Load(dataDir, cliThemesDir, themeName string) (*Templates, error) {
	if themeName == "" {
		return nil, fmt.Errorf("theme name is required")
	}

	// Resolve theme directory (local first, then CLI)
	themeDir := resolveDir(dataDir, cliThemesDir, themeName)
	if themeDir == "" {
		return nil, fmt.Errorf("theme %q not found", themeName)
	}

	// Resolve _base directory for fallback
	baseDir := resolveDir(dataDir, cliThemesDir, "_base")

	return loadWithFallback(themeDir, baseDir)
}

// resolveDir finds a theme directory, checking local site first then CLI themes.
func resolveDir(dataDir, cliThemesDir, name string) string {
	localDir := filepath.Join(dataDir, "site", "themes", name)
	if _, err := os.Stat(localDir); err == nil {
		return localDir
	}
	if cliThemesDir != "" {
		cliDir := filepath.Join(cliThemesDir, name)
		if _, err := os.Stat(cliDir); err == nil {
			return cliDir
		}
	}
	return ""
}

// readWithFallback reads a template file from themeDir first, falling back to baseDir.
func readWithFallback(themeDir, baseDir, filename string) (string, error) {
	// Try theme directory first
	if content, err := os.ReadFile(filepath.Join(themeDir, filename)); err == nil {
		return string(content), nil
	}
	// Fall back to _base directory
	if baseDir != "" {
		if content, err := os.ReadFile(filepath.Join(baseDir, filename)); err == nil {
			return string(content), nil
		}
	}
	return "", fmt.Errorf("template %q not found in theme or _base", filename)
}

// loadWithFallback loads templates from themeDir, falling back to baseDir for missing files.
func loadWithFallback(themeDir, baseDir string) (*Templates, error) {
	templates := &Templates{}

	// Load required templates (theme dir first, then _base)
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

// GetActiveTheme returns the active theme name from the manifest.
// Returns empty string if no theme is set.
func GetActiveTheme(dataDir string) (string, error) {
	manifest, err := LoadManifest(dataDir)
	if err != nil {
		return "", err
	}
	return manifest.ActiveTheme, nil
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

// LoadManifest loads site metadata from .well-known/polis.
// Returns a Manifest struct for compatibility with existing code.
func LoadManifest(dataDir string) (*Manifest, error) {
	wkPath := filepath.Join(dataDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read .well-known/polis: %w", err)
	}

	var wk struct {
		ActiveTheme string `json:"active_theme"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return nil, fmt.Errorf("failed to parse .well-known/polis: %w", err)
	}

	return &Manifest{
		ActiveTheme: wk.ActiveTheme,
	}, nil
}

// SetActiveTheme updates the active theme in .well-known/polis.
func SetActiveTheme(dataDir, themeName string) error {
	wkPath := filepath.Join(dataDir, ".well-known", "polis")

	// Read existing well-known
	var wk map[string]interface{}
	data, err := os.ReadFile(wkPath)
	if err != nil {
		wk = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(data, &wk); err != nil {
			wk = make(map[string]interface{})
		}
	}

	wk["active_theme"] = themeName

	out, err := json.MarshalIndent(wk, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal .well-known/polis: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(wkPath), 0755); err != nil {
		return fmt.Errorf("failed to create .well-known directory: %w", err)
	}

	return os.WriteFile(wkPath, append(out, '\n'), 0644)
}

// CopyBaseCSS copies the _base/base.css file to base.css at the site root.
// This provides shared structural CSS that all themes inherit.
func CopyBaseCSS(dataDir, cliThemesDir string) error {
	destPath := filepath.Join(dataDir, "base.css")

	// Try local _base first
	localPath := filepath.Join(dataDir, "site", "themes", "_base", "base.css")
	if _, err := os.Stat(localPath); err == nil {
		return copyFile(localPath, destPath)
	}

	// Fall back to CLI themes _base
	if cliThemesDir != "" {
		cliPath := filepath.Join(cliThemesDir, "_base", "base.css")
		if _, err := os.Stat(cliPath); err == nil {
			return copyFile(cliPath, destPath)
		}
	}

	// No base.css found — not an error (older themes may not have it)
	return nil
}

// CopyCSS copies the theme's CSS file to styles.css at the site root.
// The CSS filename should match the theme name ({themename}.css).
func CopyCSS(dataDir, cliThemesDir, themeName string) error {
	cssFilename := themeName + ".css"

	// Try local theme first
	localCSSPath := filepath.Join(dataDir, "site", "themes", themeName, cssFilename)
	destPath := filepath.Join(dataDir, "styles.css")

	if _, err := os.Stat(localCSSPath); err == nil {
		return copyFile(localCSSPath, destPath)
	}

	// Fall back to CLI themes
	if cliThemesDir != "" {
		cliCSSPath := filepath.Join(cliThemesDir, themeName, cssFilename)
		if _, err := os.Stat(cliCSSPath); err == nil {
			return copyFile(cliCSSPath, destPath)
		}
	}

	return fmt.Errorf("CSS file not found: %s", cssFilename)
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
// It combines themes from both local and CLI directories.
func ListThemes(dataDir, cliThemesDir string) ([]string, error) {
	themeSet := make(map[string]bool)

	// List local themes
	localThemesDir := filepath.Join(dataDir, "site", "themes")
	if entries, err := os.ReadDir(localThemesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && isValidTheme(filepath.Join(localThemesDir, entry.Name())) {
				themeSet[entry.Name()] = true
			}
		}
	}

	// List CLI themes
	if cliThemesDir != "" {
		if entries, err := os.ReadDir(cliThemesDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && isValidTheme(filepath.Join(cliThemesDir, entry.Name())) {
					themeSet[entry.Name()] = true
				}
			}
		}
	}

	// Convert to slice
	var themes []string
	for name := range themeSet {
		themes = append(themes, name)
	}

	return themes, nil
}

// isValidTheme checks if a directory contains a valid theme.
// A theme is valid if it has either all required HTML templates (legacy full theme)
// or at least a CSS file (CSS-only theme that inherits templates from _base).
// The _base directory is excluded — it's a system directory, not a selectable theme.
func isValidTheme(themeDir string) bool {
	name := filepath.Base(themeDir)
	if name == "_base" {
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
// Returns the local theme path if it exists, otherwise the CLI theme path.
func GetThemeDir(dataDir, cliThemesDir, themeName string) string {
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
type ThemePalette struct {
	Name   string   `json:"name"`
	Colors []string `json:"colors"` // 5 hex colors: bg, text, accent1, accent2, cyan
	Active bool     `json:"active"`
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
// The active theme is marked with Active=true.
func ListThemesWithPalettes(dataDir, cliThemesDir string) ([]ThemePalette, error) {
	themes, err := ListThemes(dataDir, cliThemesDir)
	if err != nil {
		return nil, err
	}

	activeTheme, _ := GetActiveTheme(dataDir)

	var palettes []ThemePalette
	for _, name := range themes {
		themeDir := GetThemeDir(dataDir, cliThemesDir, name)
		p := ExtractPalette(themeDir, name)
		p.Active = (name == activeTheme)
		palettes = append(palettes, p)
	}

	sort.Slice(palettes, func(i, j int) bool {
		return palettes[i].Name < palettes[j].Name
	})

	return palettes, nil
}
