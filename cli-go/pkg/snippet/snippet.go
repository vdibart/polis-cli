// Package snippet provides management for polis snippet files.
// Snippets are reusable HTML/Markdown templates that can be included in posts.
// There are two tiers:
// - Global snippets: site-specific customizations in data/snippets/
// - Theme snippets: shared theme templates in cli/themes/{active_theme}/snippets/
package snippet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SnippetInfo represents metadata about a single snippet file or directory.
type SnippetInfo struct {
	Path        string `json:"path"`         // Relative path e.g. "about.html" or "widgets/header.html"
	Name        string `json:"name"`         // Display name (filename without path)
	Type        string `json:"type"`         // "global" or "theme"
	IsDir       bool   `json:"is_dir"`       // True if this is a directory
	Extension   string `json:"extension"`    // ".html" or ".md"
	HasOverride bool   `json:"has_override"` // True if global overrides theme
	Size        int64  `json:"size"`         // File size in bytes
	ModTime     string `json:"mod_time"`     // Modification time ISO format
}

// SnippetTree represents the listing of snippets at a given path.
type SnippetTree struct {
	Path        string        `json:"path"`         // Current relative path
	Parent      string        `json:"parent"`       // Parent path for navigation
	ActiveTheme string        `json:"active_theme"` // Current active theme name
	Entries     []SnippetInfo `json:"entries"`      // Files and directories at this path
}

// SnippetContent represents a snippet's content for editing.
type SnippetContent struct {
	Path    string `json:"path"`
	Source  string `json:"source"` // "global" or "theme"
	Content string `json:"content"`
	ModTime string `json:"mod_time"`
}

// validatePath ensures the path is safe and doesn't contain traversal sequences.
func validatePath(path string) error {
	// Reject absolute paths
	if strings.HasPrefix(path, "/") {
		return fmt.Errorf("absolute paths not allowed")
	}
	// Reject path traversal
	if strings.Contains(path, "..") {
		return fmt.Errorf("path traversal not allowed")
	}
	// Reject null bytes
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("null bytes not allowed")
	}
	return nil
}

// GetActiveTheme reads the active_theme from .well-known/polis.
func GetActiveTheme(dataDir string) (string, error) {
	wkPath := filepath.Join(dataDir, ".well-known", "polis")
	data, err := os.ReadFile(wkPath)
	if err != nil {
		// Default to "zane" if well-known doesn't exist or can't be read
		return "zane", nil
	}

	var wk struct {
		ActiveTheme string `json:"active_theme"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return "zane", nil
	}

	if wk.ActiveTheme == "" {
		return "zane", nil
	}
	return wk.ActiveTheme, nil
}

// ListSnippets returns a merged list of snippets from global and theme directories.
// Global snippets take precedence over theme snippets with the same name.
// Theme snippets are located at:
//  1. data/site/themes/{active_theme}/snippets/ (local copy)
//  2. cli/themes/{active_theme}/snippets/ (fallback to CLI themes)
//
// The filter parameter controls which snippets are returned:
//   - "all" or "": return both global and theme snippets (merged)
//   - "global": return only global snippets
//   - "theme": return only theme snippets
func ListSnippets(dataDir, cliThemesDir, activeTheme, relativePath, filter string) (*SnippetTree, error) {
	if err := validatePath(relativePath); err != nil {
		return nil, err
	}

	// If activeTheme is empty, try to get it from manifest
	if activeTheme == "" {
		var err error
		activeTheme, err = GetActiveTheme(dataDir)
		if err != nil {
			activeTheme = "zane"
		}
	}

	globalBase := filepath.Join(dataDir, "site", "snippets")

	// Theme snippets: prefer local site/themes/, fall back to CLI themes
	themeBase := filepath.Join(dataDir, "site", "themes", activeTheme, "snippets")
	if _, err := os.Stat(themeBase); os.IsNotExist(err) && cliThemesDir != "" {
		// Fall back to CLI themes directory
		themeBase = filepath.Join(cliThemesDir, activeTheme, "snippets")
	}

	globalPath := filepath.Join(globalBase, relativePath)
	themePath := filepath.Join(themeBase, relativePath)

	// Maps to track entries: key is the entry name
	entries := make(map[string]SnippetInfo)
	themeEntries := make(map[string]bool) // Track which entries exist in theme

	// Determine what to scan based on filter
	scanTheme := filter == "" || filter == "all" || filter == "theme"
	scanGlobal := filter == "" || filter == "all" || filter == "global"

	// First, read theme snippets (will be overridden by global if scanning both)
	if scanTheme {
		if themeDir, err := os.ReadDir(themePath); err == nil {
			for _, entry := range themeDir {
				name := entry.Name()
				// Skip hidden files
				if strings.HasPrefix(name, ".") {
					continue
				}

				info, err := entry.Info()
				if err != nil {
					continue
				}

				ext := filepath.Ext(name)
				// Only include .html and .md files, or directories
				if !entry.IsDir() && ext != ".html" && ext != ".md" {
					continue
				}

				entryPath := relativePath
				if entryPath != "" {
					entryPath = filepath.Join(entryPath, name)
				} else {
					entryPath = name
				}

				entries[name] = SnippetInfo{
					Path:        entryPath,
					Name:        name,
					Type:        "theme",
					IsDir:       entry.IsDir(),
					Extension:   ext,
					HasOverride: false,
					Size:        info.Size(),
					ModTime:     info.ModTime().Format("2006-01-02T15:04:05Z"),
				}
				themeEntries[name] = true
			}
		}
	}

	// Then, read global snippets (override theme entries if scanning both)
	if scanGlobal {
		if globalDir, err := os.ReadDir(globalPath); err == nil {
			for _, entry := range globalDir {
				name := entry.Name()
				// Skip hidden files
				if strings.HasPrefix(name, ".") {
					continue
				}

				info, err := entry.Info()
				if err != nil {
					continue
				}

				ext := filepath.Ext(name)
				// Only include .html and .md files, or directories
				if !entry.IsDir() && ext != ".html" && ext != ".md" {
					continue
				}

				entryPath := relativePath
				if entryPath != "" {
					entryPath = filepath.Join(entryPath, name)
				} else {
					entryPath = name
				}

				hasOverride := themeEntries[name]

				entries[name] = SnippetInfo{
					Path:        entryPath,
					Name:        name,
					Type:        "global",
					IsDir:       entry.IsDir(),
					Extension:   ext,
					HasOverride: hasOverride,
					Size:        info.Size(),
					ModTime:     info.ModTime().Format("2006-01-02T15:04:05Z"),
				}
			}
		}
	}

	// Convert map to sorted slice
	sortedEntries := make([]SnippetInfo, 0, len(entries))
	for _, entry := range entries {
		sortedEntries = append(sortedEntries, entry)
	}

	// Sort: directories first, then by name
	sort.Slice(sortedEntries, func(i, j int) bool {
		if sortedEntries[i].IsDir != sortedEntries[j].IsDir {
			return sortedEntries[i].IsDir // directories first
		}
		return sortedEntries[i].Name < sortedEntries[j].Name
	})

	// Calculate parent path for navigation
	parent := ""
	if relativePath != "" {
		parent = filepath.Dir(relativePath)
		if parent == "." {
			parent = ""
		}
	}

	return &SnippetTree{
		Path:        relativePath,
		Parent:      parent,
		ActiveTheme: activeTheme,
		Entries:     sortedEntries,
	}, nil
}

// resolveSnippetFile tries to find the snippet file, handling extension fallback.
// Per TEMPLATING.md, resolution order is: .md -> .html -> exact
func resolveSnippetFile(baseDir, snippetPath string) string {
	// If path already has extension, try exact match only
	ext := filepath.Ext(snippetPath)
	if ext == ".html" || ext == ".md" {
		fullPath := filepath.Join(baseDir, snippetPath)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath
		}
		return ""
	}

	// Per TEMPLATING.md: .md -> .html -> exact
	// Try with .md extension first
	fullPath := filepath.Join(baseDir, snippetPath+".md")
	if _, err := os.Stat(fullPath); err == nil {
		return fullPath
	}

	// Try with .html extension
	fullPath = filepath.Join(baseDir, snippetPath+".html")
	if _, err := os.Stat(fullPath); err == nil {
		return fullPath
	}

	// Try exact path
	fullPath = filepath.Join(baseDir, snippetPath)
	if _, err := os.Stat(fullPath); err == nil {
		return fullPath
	}

	return ""
}

// ReadSnippet reads the content of a snippet from the specified source.
// source must be "global" or "theme".
// Theme snippets are located at:
//  1. data/site/themes/{active_theme}/snippets/ (local copy)
//  2. cli/themes/{active_theme}/snippets/ (fallback to CLI themes)
func ReadSnippet(dataDir, cliThemesDir, activeTheme, snippetPath, source string) (*SnippetContent, error) {
	if err := validatePath(snippetPath); err != nil {
		return nil, err
	}

	// If activeTheme is empty, try to get it from manifest
	if activeTheme == "" {
		var err error
		activeTheme, err = GetActiveTheme(dataDir)
		if err != nil {
			activeTheme = "zane"
		}
	}

	var fullPath string
	switch source {
	case "global":
		fullPath = resolveSnippetFile(filepath.Join(dataDir, "site", "snippets"), snippetPath)
	case "theme":
		// Theme snippets: prefer local site/themes/, fall back to CLI themes
		fullPath = resolveSnippetFile(filepath.Join(dataDir, "site", "themes", activeTheme, "snippets"), snippetPath)
		if fullPath == "" && cliThemesDir != "" {
			// Fall back to CLI themes directory
			fullPath = resolveSnippetFile(filepath.Join(cliThemesDir, activeTheme, "snippets"), snippetPath)
		}
	default:
		return nil, fmt.Errorf("invalid source: must be 'global' or 'theme'")
	}

	if fullPath == "" {
		return nil, fmt.Errorf("snippet not found: %s", snippetPath)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read snippet: %w", err)
	}

	info, err := os.Stat(fullPath)
	modTime := ""
	if err == nil {
		modTime = info.ModTime().Format("2006-01-02T15:04:05Z")
	}

	return &SnippetContent{
		Path:    snippetPath,
		Source:  source,
		Content: string(data),
		ModTime: modTime,
	}, nil
}

// WriteSnippet saves content to the specified source (global or theme).
// Theme snippets are written to:
//  1. data/site/themes/{active_theme}/snippets/ (local copy) if it exists
//  2. cli/themes/{active_theme}/snippets/ (fallback to CLI themes)
//
// When writing, if the snippetPath doesn't have an extension and no existing
// file is found, defaults to .html extension.
func WriteSnippet(dataDir, cliThemesDir, activeTheme, snippetPath, content, source string) error {
	if err := validatePath(snippetPath); err != nil {
		return err
	}

	// If activeTheme is empty, try to get it from manifest
	if activeTheme == "" {
		var err error
		activeTheme, err = GetActiveTheme(dataDir)
		if err != nil {
			activeTheme = "zane"
		}
	}

	var fullPath string
	switch source {
	case "global":
		// Try to resolve existing file first
		baseDir := filepath.Join(dataDir, "site", "snippets")
		resolved := resolveSnippetFile(baseDir, snippetPath)
		if resolved != "" {
			fullPath = resolved
		} else {
			// New file: default to .html if no extension
			ext := filepath.Ext(snippetPath)
			if ext != ".html" && ext != ".md" {
				snippetPath = snippetPath + ".html"
			}
			fullPath = filepath.Join(baseDir, snippetPath)
		}
	case "theme":
		// Theme snippets: prefer local site/themes/, fall back to CLI themes
		localThemeBase := filepath.Join(dataDir, "site", "themes", activeTheme, "snippets")
		cliThemeBase := filepath.Join(cliThemesDir, activeTheme, "snippets")

		// Try to resolve existing file
		resolved := resolveSnippetFile(localThemeBase, snippetPath)
		if resolved != "" {
			fullPath = resolved
		} else if cliThemesDir != "" {
			resolved = resolveSnippetFile(cliThemeBase, snippetPath)
			if resolved != "" {
				fullPath = resolved
			}
		}

		// If no existing file found, default to local theme path with .html
		if fullPath == "" {
			ext := filepath.Ext(snippetPath)
			if ext != ".html" && ext != ".md" {
				snippetPath = snippetPath + ".html"
			}
			fullPath = filepath.Join(localThemeBase, snippetPath)
		}
	default:
		return fmt.Errorf("invalid source: must be 'global' or 'theme'")
	}

	// Ensure parent directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write atomically via temp file
	tmpPath := fullPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, fullPath); err != nil {
		os.Remove(tmpPath) // Clean up temp file on failure
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// CreateSnippet creates a new snippet in the global snippets directory only.
// Theme snippet creation is not supported - users should edit themes directly.
func CreateSnippet(dataDir, snippetPath, content string) error {
	if err := validatePath(snippetPath); err != nil {
		return err
	}

	// Validate extension
	ext := filepath.Ext(snippetPath)
	if ext != ".html" && ext != ".md" {
		return fmt.Errorf("snippet must have .html or .md extension")
	}

	fullPath := filepath.Join(dataDir, "site", "snippets", snippetPath)

	// Check if snippet already exists
	if _, err := os.Stat(fullPath); err == nil {
		return fmt.Errorf("snippet already exists: %s", snippetPath)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write the file
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to create snippet: %w", err)
	}

	return nil
}

// DeleteSnippet removes a snippet from the global snippets directory only.
// Theme snippet deletion is not supported.
func DeleteSnippet(dataDir, snippetPath string) error {
	if err := validatePath(snippetPath); err != nil {
		return err
	}

	fullPath := filepath.Join(dataDir, "site", "snippets", snippetPath)

	// Check if it exists
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("snippet not found: %s", snippetPath)
		}
		return fmt.Errorf("failed to stat snippet: %w", err)
	}

	// Don't allow deleting directories via this function
	if info.IsDir() {
		return fmt.Errorf("cannot delete directory: %s", snippetPath)
	}

	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("failed to delete snippet: %w", err)
	}

	return nil
}
