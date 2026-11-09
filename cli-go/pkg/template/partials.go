package template

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
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
		src    partialSource
	}

	var lookupOrder []lookupEntry

	switch prefix {
	case "theme":
		// Theme-first, then base, then global
		lookupOrder = []lookupEntry{
			{"theme", e.themeSnippets()},
			{"base", e.baseSnippets()},
			{"base", e.shapeRoot()},
			{"global", e.globalSnippets()},
		}
	default: // "global" or no prefix
		// Global-first, then theme, then base
		lookupOrder = []lookupEntry{
			{"global", e.globalSnippets()},
			{"theme", e.themeSnippets()},
			{"base", e.baseSnippets()},
			{"base", e.shapeRoot()},
		}
	}

	// Try each source in order
	for _, lookup := range lookupOrder {
		if lookup.src.store == nil {
			continue
		}

		content, resolved, err := e.loadFrom(lookup.src, cleanPath)
		if err == nil {
			return content, resolved, lookup.source, nil
		}
	}

	return "", "", "", fmt.Errorf("partial not found: %s", path)
}

// partialSource is one place a partial may live: a key prefix in a store. The
// zero value is "no such place".
//
// ⭐ A source names its own store, so the chain can mix roots: the site's
// candidates are in the site's store, and the CLI themes directory, which is
// not a site, is a POSIX store rooted there and stays a path.
type partialSource struct {
	store  storage.Store
	prefix string
}

// siteSource returns the site candidate at key when it is present.
func (e *Engine) siteSource(key string) partialSource {
	if !e.config.Site.Valid() {
		return partialSource{}
	}
	st := e.config.Site.Store()
	if present, _ := prefixPresent(st, key); present {
		return partialSource{store: st, prefix: key}
	}
	return partialSource{}
}

// globalSnippets is the site's global snippets.
func (e *Engine) globalSnippets() partialSource {
	return e.siteSource("site/snippets")
}

// themeSnippets returns the theme snippets source.
// Tries local theme first, then CLI themes directory.
func (e *Engine) themeSnippets() partialSource {
	if e.config.ActiveTheme == "" {
		return partialSource{}
	}

	// Try local theme first
	if src := e.siteSource("site/themes/" + e.config.ActiveTheme + "/snippets"); src.store != nil {
		return src
	}

	// Fall back to CLI themes.
	//
	// ⭐ A DIFFERENT ROOT, not an exclusion: CLIThemesDir is outside the site,
	// but a store rooted there addresses its contents exactly as the site store
	// addresses the site's. Each candidate names its own store, and the reader
	// downstream reads from whichever one won.
	if e.config.CLIThemesDir != "" {
		cliDir := filepath.Join(e.config.CLIThemesDir, e.config.ActiveTheme, "snippets")
		if present, _ := storage.PathExists(cliDir); present {
			return partialSource{store: storage.NewPOSIX(cliDir), prefix: ""}
		}
	}

	return partialSource{}
}

// activeShape is the shape whose partials are the fallback. Defaults to v3
// when ActiveShape is unset (pre-step-02 callers that don't thread the shape
// name through).
func (e *Engine) activeShape() string {
	if e.config.ActiveShape == "" {
		return "v3"
	}
	return e.config.ActiveShape
}

// baseSnippets is the last-resort partial source — post-SHAPE refactor this is
// the installed shape's snippets/ dir, which carries what used to live under
// themes/_base/snippets/.
func (e *Engine) baseSnippets() partialSource {
	return e.siteSource(".polis/bundles/pub.polis.core/shapes/" + e.activeShape() + "/snippets")
}

// shapeRoot is the active shape's root, used as an additional fallback for
// partial lookup. stream.html references {{> stream-post}} — stream-post.html
// lives at the shape root (it's an Entry template), not under snippets/.
// Without this fallback the partial resolver can't find it.
func (e *Engine) shapeRoot() partialSource {
	return e.siteSource(".polis/bundles/pub.polis.core/shapes/" + e.activeShape())
}

// loadFrom loads a partial from a source.
// Returns content, resolved path, and error.
func (e *Engine) loadFrom(src partialSource, p string) (string, string, error) {
	ctx := context.Background()
	// Check if path has explicit extension
	hasExplicitExt := strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".html")

	if hasExplicitExt {
		// Try exact path only
		key, ok := keyUnder(src.prefix, p)
		if !ok {
			return "", "", fmt.Errorf("partial path escapes lookup dir: %s", p)
		}
		content, err := src.store.Get(ctx, key)
		if err != nil {
			return "", "", err
		}
		return e.processPartialContent(string(content), p), p, nil
	}

	// Resolution order: .md -> .html -> exact
	extensions := []string{".md", ".html", ""}
	for _, ext := range extensions {
		key, ok := keyUnder(src.prefix, p+ext)
		if !ok {
			return "", "", fmt.Errorf("partial path escapes lookup dir: %s", p)
		}
		content, err := src.store.Get(ctx, key)
		if err == nil {
			resolvedPath := p + ext
			return e.processPartialContent(string(content), resolvedPath), resolvedPath, nil
		}
	}

	return "", "", fmt.Errorf("file not found: %s", p)
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

// keyUnder joins a partial's path onto a source's key prefix and reports
// whether the result stays under it. Used as a final defense-in-depth check
// after validatePartialPath: a key that cleans to somewhere outside the
// prefix is refused, as a path outside the lookup dir always was.
func keyUnder(prefix, p string) (string, bool) {
	key, err := storage.CleanKey(path.Join(prefix, filepath.ToSlash(p)))
	if err != nil || key == "" {
		return "", false
	}
	if prefix == "" {
		return key, true
	}
	if key == prefix || strings.HasPrefix(key, prefix+"/") {
		return key, true
	}
	return "", false
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

// prefixPresent reports whether anything is stored at or under key.
//
// ⚠️ On a filesystem this is whether the DIRECTORY exists, as it always was,
// so an empty one still counts and stops the chain from falling through. A
// store with no filesystem has no directories: a prefix is present exactly
// when something is stored under it.
func prefixPresent(st storage.Store, key string) (bool, error) {
	if st.Capabilities().RealPath {
		return st.Exists(context.Background(), key)
	}
	entries, err := st.List(context.Background(), key)
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}
