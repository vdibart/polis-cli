package template

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// partialPattern matches {{> path}} or {{> prefix:path}} syntax.
var partialPattern = regexp.MustCompile(`\{\{>\s*([^}]+)\}\}`)

// processPartials expands all {{> path}} includes in the template.
// Supports:
// - {{> about}} - global-first lookup (default)
// - {{> global:about}} - explicit global-first
// - {{> theme:about}} - theme-first lookup
// - {{> about.html}} or {{> about.md}} - explicit extension
func (e *Engine) processPartials(template string, ctx *RenderContext, depth int) (string, error) {
	if depth > e.maxDepth {
		return "", fmt.Errorf("maximum partial recursion depth (%d) exceeded", e.maxDepth)
	}

	var lastErr error

	result := partialPattern.ReplaceAllStringFunc(template, func(match string) string {
		// Extract path from {{> path}}
		submatch := partialPattern.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}

		path := strings.TrimSpace(submatch[1])
		content, snippetPath, source, err := e.loadPartial(path)
		if err != nil {
			lastErr = fmt.Errorf("failed to load partial %q: %w", path, err)
			return match // Return original on error
		}

		// Recursively process the loaded partial
		processed, err := e.renderWithDepth(content, ctx, depth+1)
		if err != nil {
			lastErr = fmt.Errorf("failed to render partial %q: %w", path, err)
			return content // Return unprocessed content on error
		}

		// Wrap with markers if enabled
		if e.config.RenderMarkers {
			processed = WrapWithMarkers(processed, snippetPath, source)
		}

		return processed
	})

	return result, lastErr
}

// loadPartial loads a partial file from the appropriate location.
// Returns content, resolved path, source ("global", "theme", or "base"), and error.
func (e *Engine) loadPartial(path string) (string, string, string, error) {
	// Parse prefix (global: or theme:)
	prefix, cleanPath := parsePartialPrefix(path)

	// Determine lookup order based on prefix.
	// _base snippets are always the last fallback before giving up.
	type lookupEntry struct {
		source string
		dir    string
	}

	var lookupOrder []lookupEntry

	switch prefix {
	case "theme":
		// Theme-first, then base, then global
		lookupOrder = []lookupEntry{
			{"theme", e.getThemeSnippetsDir()},
			{"base", e.getBaseSnippetsDir()},
			{"global", e.GetGlobalSnippetsDir()},
		}
	default: // "global" or no prefix
		// Global-first, then theme, then base
		lookupOrder = []lookupEntry{
			{"global", e.GetGlobalSnippetsDir()},
			{"theme", e.getThemeSnippetsDir()},
			{"base", e.getBaseSnippetsDir()},
		}
	}

	// Try each source in order
	for _, lookup := range lookupOrder {
		if lookup.dir == "" {
			continue
		}

		content, resolved, err := e.loadFromDir(lookup.dir, cleanPath)
		if err == nil {
			return content, resolved, lookup.source, nil
		}
	}

	return "", "", "", fmt.Errorf("partial not found: %s", path)
}

// getThemeSnippetsDir returns the theme snippets directory.
// Tries local theme first, then CLI themes directory.
func (e *Engine) getThemeSnippetsDir() string {
	if e.config.ActiveTheme == "" {
		return ""
	}

	// Try local theme first
	localDir := filepath.Join(e.config.DataDir, "site", "themes", e.config.ActiveTheme, "snippets")
	if _, err := os.Stat(localDir); err == nil {
		return localDir
	}

	// Fall back to CLI themes
	if e.config.CLIThemesDir != "" {
		cliDir := filepath.Join(e.config.CLIThemesDir, e.config.ActiveTheme, "snippets")
		if _, err := os.Stat(cliDir); err == nil {
			return cliDir
		}
	}

	return ""
}

// getBaseSnippetsDir returns the _base theme snippets directory.
// Tries local _base first, then CLI themes _base directory.
func (e *Engine) getBaseSnippetsDir() string {
	// Try local _base first
	localDir := filepath.Join(e.config.DataDir, "site", "themes", "_base", "snippets")
	if _, err := os.Stat(localDir); err == nil {
		return localDir
	}

	// Fall back to CLI themes _base
	if e.config.CLIThemesDir != "" {
		cliDir := filepath.Join(e.config.CLIThemesDir, "_base", "snippets")
		if _, err := os.Stat(cliDir); err == nil {
			return cliDir
		}
	}

	return ""
}

// loadFromDir loads a partial from a specific directory.
// Returns content, resolved path, and error.
func (e *Engine) loadFromDir(dir, path string) (string, string, error) {
	// Check if path has explicit extension
	hasExplicitExt := strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".html")

	if hasExplicitExt {
		// Try exact path only
		fullPath := filepath.Join(dir, path)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return "", "", err
		}
		return e.processPartialContent(string(content), path), path, nil
	}

	// Resolution order: .md -> .html -> exact
	extensions := []string{".md", ".html", ""}
	for _, ext := range extensions {
		fullPath := filepath.Join(dir, path+ext)
		content, err := os.ReadFile(fullPath)
		if err == nil {
			resolvedPath := path + ext
			return e.processPartialContent(string(content), resolvedPath), resolvedPath, nil
		}
	}

	return "", "", fmt.Errorf("file not found: %s", path)
}

// processPartialContent processes the content of a loaded partial.
// If it's a .md file, it renders markdown to HTML.
func (e *Engine) processPartialContent(content, path string) string {
	// If it's a markdown file and we have a markdown renderer, render to HTML
	if strings.HasSuffix(path, ".md") && e.markdownRenderer != nil {
		html, err := e.markdownRenderer(content)
		if err != nil {
			// Return as-is on error
			return content
		}
		return html
	}
	return content
}

// parsePartialPrefix extracts the prefix (global:, theme:) from a partial path.
// Returns prefix and clean path.
func parsePartialPrefix(path string) (string, string) {
	if strings.HasPrefix(path, "global:") {
		return "global", strings.TrimPrefix(path, "global:")
	}
	if strings.HasPrefix(path, "theme:") {
		return "theme", strings.TrimPrefix(path, "theme:")
	}
	return "", path
}
