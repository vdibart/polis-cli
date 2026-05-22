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

	// Reject paths that try to escape the lookup directories. Templates can
	// come from cloned remote sites or hosted-tenant theme uploads, so a
	// malicious "{{> ../../../etc/passwd}}" must never resolve to a file
	// outside the lookup dirs. Defense-in-depth: loadFromDir also verifies
	// the joined path stays under the lookup dir.
	if err := validatePartialPath(cleanPath); err != nil {
		return "", "", "", err
	}

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
			{"base", e.getShapeRootDir()},
			{"global", e.GetGlobalSnippetsDir()},
		}
	default: // "global" or no prefix
		// Global-first, then theme, then base
		lookupOrder = []lookupEntry{
			{"global", e.GetGlobalSnippetsDir()},
			{"theme", e.getThemeSnippetsDir()},
			{"base", e.getBaseSnippetsDir()},
			{"base", e.getShapeRootDir()},
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

// getBaseSnippetsDir returns the snippets directory that serves as the
// last-resort partial source — post-SHAPE refactor this is the installed shape's
// snippets/ dir, which carries what used to live under themes/_base/snippets/.
//
// Defaults to v3 when ActiveShape is unset (pre-step-02 callers that don't
// thread the shape name through).
func (e *Engine) getBaseSnippetsDir() string {
	shape := e.config.ActiveShape
	if shape == "" {
		shape = "v3"
	}
	shapeDir := filepath.Join(e.config.DataDir, ".polis", "bundles", "pub.polis.core", "shapes", shape, "snippets")
	if _, err := os.Stat(shapeDir); err == nil {
		return shapeDir
	}
	return ""
}

// getShapeRootDir returns the active shape's root directory, used as an
// additional fallback for partial lookup. stream.html references
// {{> stream-post}} — stream-post.html lives at the shape root (it's an
// Entry template), not under snippets/. Without this fallback the partial
// resolver can't find it.
func (e *Engine) getShapeRootDir() string {
	shape := e.config.ActiveShape
	if shape == "" {
		shape = "v3"
	}
	shapeDir := filepath.Join(e.config.DataDir, ".polis", "bundles", "pub.polis.core", "shapes", shape)
	if _, err := os.Stat(shapeDir); err == nil {
		return shapeDir
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
		if !pathUnder(fullPath, dir) {
			return "", "", fmt.Errorf("partial path escapes lookup dir: %s", path)
		}
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
		if !pathUnder(fullPath, dir) {
			return "", "", fmt.Errorf("partial path escapes lookup dir: %s", path)
		}
		content, err := os.ReadFile(fullPath)
		if err == nil {
			resolvedPath := path + ext
			return e.processPartialContent(string(content), resolvedPath), resolvedPath, nil
		}
	}

	return "", "", fmt.Errorf("file not found: %s", path)
}

// validatePartialPath rejects partial paths that could escape the lookup
// dir: empty, absolute, containing `..`, or containing null bytes. Cleaned
// containment is also verified per-lookup-dir in loadFromDir; this is the
// fast pre-check before any I/O.
func validatePartialPath(path string) error {
	if path == "" {
		return fmt.Errorf("empty partial path")
	}
	if strings.ContainsRune(path, 0) {
		return fmt.Errorf("partial path contains null byte")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return fmt.Errorf("partial path must be relative: %s", path)
	}
	// Walk segments: any ".." is rejected outright. Done by string scan rather
	// than filepath.Clean comparison so we surface the explicit traversal
	// attempt rather than silently normalizing it.
	for _, seg := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if seg == ".." {
			return fmt.Errorf("partial path contains '..': %s", path)
		}
	}
	return nil
}

// pathUnder reports whether the cleaned absolute form of fullPath is the
// same as or a descendant of dir's cleaned absolute form. Used as a final
// defense-in-depth check after filepath.Join.
func pathUnder(fullPath, dir string) bool {
	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absFull)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
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
