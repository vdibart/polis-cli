// Package snippet provides management for polis snippet files.
// Snippets are reusable HTML/Markdown templates that can be included in posts.
// There are two tiers:
// - Global snippets: site-specific customizations in data/snippets/
// - Theme snippets: shared theme templates in cli/themes/{active_theme}/snippets/
package snippet

import (
	"context"
	"errors"
	"fmt"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
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

// activeThemeOrDefault returns the active theme name for snippet lookup,
// falling back to "zane" when no theme is configured. This is the snippet
// package's compatibility shim — it differs from theme.GetActiveTheme /
// bundle.GetActiveThemeName in that it always produces a non-empty string,
// which the snippet rendering path requires (it needs *some* theme dir to
// resolve theme: snippets against, even on a freshly-initialized site).
//
// Keep this distinct from the canonical accessors so a reader scanning
// snippet code sees the "always returns a usable name" semantic explicitly.
func activeThemeOrDefault(site storage.Site) (string, error) {
	name, err := bundle.GetActiveThemeName(site)
	if err != nil || name == "" {
		return "zane", nil
	}
	return name, nil
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
func ListSnippets(site storage.Site, cliThemesDir, activeTheme, relativePath, filter string) (*SnippetTree, error) {
	if err := validatePath(relativePath); err != nil {
		return nil, err
	}
	if !site.Valid() {
		return nil, fmt.Errorf("list snippets: site handle is not usable")
	}

	// If activeTheme is empty, try to get it from manifest
	if activeTheme == "" {
		var err error
		activeTheme, err = activeThemeOrDefault(site)
		if err != nil {
			activeTheme = "zane"
		}
	}

	globalBase := siteRoot(site, globalSnippetsKey)

	// Theme snippets: prefer local site/themes/, fall back to CLI themes
	themeBase := siteRoot(site, localThemeKey(activeTheme))
	// Only a GENUINE absence falls back: a theme directory that is there but
	// cannot be read is not replaced by the CLI's copy.
	if present, existsErr := themeBase.present(); existsErr == nil && !present && cliThemesDir != "" {
		// Fall back to CLI themes directory
		themeBase = cliRoot(cliThemesDir, activeTheme)
	}

	// Maps to track entries: key is the entry name
	entries := make(map[string]SnippetInfo)
	themeEntries := make(map[string]bool) // Track which entries exist in theme

	// Determine what to scan based on filter
	scanTheme := filter == "" || filter == "all" || filter == "theme"
	scanGlobal := filter == "" || filter == "all" || filter == "global"

	// First, read theme snippets (will be overridden by global if scanning both)
	if scanTheme {
		if themeDir := themeBase.list(relativePath); len(themeDir) > 0 {
			for _, entry := range themeDir {
				name := pathpkg.Base(entry.Key)
				// Skip hidden files
				if strings.HasPrefix(name, ".") {
					continue
				}

				// ⚠️ Depends on ModTimes: a snippet's size and mtime are both
				// reported to the caller, so a backend without mtimes lists
				// snippets with an empty date rather than failing.
				info, err := themeBase.store.Stat(context.Background(), entry.Key)
				if err != nil {
					continue
				}

				ext := filepath.Ext(name)
				// Only include .html and .md files, or directories
				if !entry.IsDir && ext != ".html" && ext != ".md" {
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
					IsDir:       entry.IsDir,
					Extension:   ext,
					HasOverride: false,
					Size:        info.Size,
					ModTime:     info.ModTime.Format("2006-01-02T15:04:05Z"),
				}
				themeEntries[name] = true
			}
		}
	}

	// Then, read global snippets (override theme entries if scanning both)
	if scanGlobal {
		if globalDir := globalBase.list(relativePath); len(globalDir) > 0 {
			for _, entry := range globalDir {
				name := pathpkg.Base(entry.Key)
				// Skip hidden files
				if strings.HasPrefix(name, ".") {
					continue
				}

				// ⚠️ Depends on ModTimes: a snippet's size and mtime are both
				// reported to the caller, so a backend without mtimes lists
				// snippets with an empty date rather than failing.
				info, err := globalBase.store.Stat(context.Background(), entry.Key)
				if err != nil {
					continue
				}

				ext := filepath.Ext(name)
				// Only include .html and .md files, or directories
				if !entry.IsDir && ext != ".html" && ext != ".md" {
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
					IsDir:       entry.IsDir,
					Extension:   ext,
					HasOverride: hasOverride,
					Size:        info.Size,
					ModTime:     info.ModTime.Format("2006-01-02T15:04:05Z"),
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

// resolveSnippetFile tries to find the snippet file, handling extension
// fallback, and returns its key in r, or "" when there is none.
// Per TEMPLATING.md, resolution order is: .md -> .html -> exact
func resolveSnippetFile(r snippetRoot, snippetPath string) string {
	present := func(rel string) bool {
		ok, _ := r.store.Exists(context.Background(), r.key(rel))
		return ok
	}
	// If path already has extension, try exact match only
	ext := filepath.Ext(snippetPath)
	if ext == ".html" || ext == ".md" {
		if present(snippetPath) {
			return r.key(snippetPath)
		}
		return ""
	}

	// Per TEMPLATING.md: .md -> .html -> exact
	for _, candidate := range []string{snippetPath + ".md", snippetPath + ".html", snippetPath} {
		if present(candidate) {
			return r.key(candidate)
		}
	}

	return ""
}

// ReadSnippet reads the content of a snippet from the specified source.
// source must be "global" or "theme".
// Theme snippets are located at:
//  1. data/site/themes/{active_theme}/snippets/ (local copy)
//  2. cli/themes/{active_theme}/snippets/ (fallback to CLI themes)
func ReadSnippet(site storage.Site, cliThemesDir, activeTheme, snippetPath, source string) (*SnippetContent, error) {
	if err := validatePath(snippetPath); err != nil {
		return nil, err
	}
	if !site.Valid() {
		return nil, fmt.Errorf("read snippet: site handle is not usable")
	}

	// If activeTheme is empty, try to get it from manifest
	if activeTheme == "" {
		var err error
		activeTheme, err = activeThemeOrDefault(site)
		if err != nil {
			activeTheme = "zane"
		}
	}

	var from snippetRoot
	var key string
	switch source {
	case "global":
		from = siteRoot(site, globalSnippetsKey)
		key = resolveSnippetFile(from, snippetPath)
	case "theme":
		// Theme snippets: prefer local site/themes/, fall back to CLI themes
		from = siteRoot(site, localThemeKey(activeTheme))
		key = resolveSnippetFile(from, snippetPath)
		if key == "" && cliThemesDir != "" {
			// Fall back to CLI themes directory
			from = cliRoot(cliThemesDir, activeTheme)
			key = resolveSnippetFile(from, snippetPath)
		}
	default:
		return nil, fmt.Errorf("invalid source: must be 'global' or 'theme'")
	}

	if key == "" {
		return nil, fmt.Errorf("snippet not found: %s", snippetPath)
	}

	ctx := context.Background()
	data, err := from.store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to read snippet: %w", err)
	}

	// ⚠️ Depends on ModTimes: this is the displayed modification date, and it
	// is already tolerant of a missing one — it stays empty.
	info, err := from.store.Stat(ctx, key)
	modTime := ""
	if err == nil {
		modTime = info.ModTime.Format("2006-01-02T15:04:05Z")
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
func WriteSnippet(site storage.Site, cliThemesDir, activeTheme, snippetPath, content, source string) error {
	if err := validatePath(snippetPath); err != nil {
		return err
	}
	if !site.Valid() {
		return fmt.Errorf("write snippet: site handle is not usable")
	}

	// If activeTheme is empty, try to get it from manifest
	if activeTheme == "" {
		var err error
		activeTheme, err = activeThemeOrDefault(site)
		if err != nil {
			activeTheme = "zane"
		}
	}

	var to snippetRoot
	var key string
	switch source {
	case "global":
		// Try to resolve existing file first
		to = siteRoot(site, globalSnippetsKey)
		if key = resolveSnippetFile(to, snippetPath); key == "" {
			// New file: default to .html if no extension
			ext := filepath.Ext(snippetPath)
			if ext != ".html" && ext != ".md" {
				snippetPath = snippetPath + ".html"
			}
			key = to.key(snippetPath)
		}
	case "theme":
		// Theme snippets: prefer local site/themes/, fall back to CLI themes
		to = siteRoot(site, localThemeKey(activeTheme))

		// Try to resolve existing file
		key = resolveSnippetFile(to, snippetPath)
		if key == "" && cliThemesDir != "" {
			cli := cliRoot(cliThemesDir, activeTheme)
			if key = resolveSnippetFile(cli, snippetPath); key != "" {
				to = cli
			}
		}

		// If no existing file found, default to local theme path with .html
		if key == "" {
			ext := filepath.Ext(snippetPath)
			if ext != ".html" && ext != ".md" {
				snippetPath = snippetPath + ".html"
			}
			key = to.key(snippetPath)
		}
	default:
		return fmt.Errorf("invalid source: must be 'global' or 'theme'")
	}

	// ⭐ An atomic write, through whichever root the snippet resolved to:
	// temp-and-rename on a filesystem, a single Put where there is none. A
	// crash mid-write leaves the old snippet intact, as it always did, and
	// atomicfile creates the parent at the store's 0755.
	if err := atomicfile.Write(context.Background(), to.site, key, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write snippet: %w", err)
	}

	return nil
}

// CreateSnippet creates a new snippet in the global snippets directory only.
// Theme snippet creation is not supported - users should edit themes directly.
func CreateSnippet(site storage.Site, snippetPath, content string) error {
	if err := validatePath(snippetPath); err != nil {
		return err
	}
	if !site.Valid() {
		return fmt.Errorf("create snippet: site handle is not usable")
	}

	// Validate extension
	ext := filepath.Ext(snippetPath)
	if ext != ".html" && ext != ".md" {
		return fmt.Errorf("snippet must have .html or .md extension")
	}

	ctx := context.Background()
	key := siteRoot(site, globalSnippetsKey).key(snippetPath)

	// Check if snippet already exists
	if present, _ := site.Store().Exists(ctx, key); present {
		return fmt.Errorf("snippet already exists: %s", snippetPath)
	}

	// Parent prefixes are implicit in the store.
	if err := site.Store().Put(ctx, key, []byte(content)); err != nil {
		return fmt.Errorf("failed to create snippet: %w", err)
	}

	return nil
}

// DeleteSnippet removes a snippet from the global snippets directory only.
// Theme snippet deletion is not supported.
func DeleteSnippet(site storage.Site, snippetPath string) error {
	if err := validatePath(snippetPath); err != nil {
		return err
	}
	if !site.Valid() {
		return fmt.Errorf("delete snippet: site handle is not usable")
	}

	ctx := context.Background()
	key := siteRoot(site, globalSnippetsKey).key(snippetPath)

	// Check if it exists
	info, err := site.Store().Stat(ctx, key)
	if err != nil {
		// ⚠️ storage.ErrNotFound, NOT os.IsNotExist: the store wraps the
		// filesystem error, and os.IsNotExist does not unwrap. Matching the old
		// way compiles and silently reports "failed to stat" for a snippet that
		// is merely absent, which a test caught.
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("snippet not found: %s", snippetPath)
		}
		return fmt.Errorf("failed to stat snippet: %w", err)
	}

	// Don't allow deleting directories via this function
	if info.IsDir {
		return fmt.Errorf("cannot delete directory: %s", snippetPath)
	}

	if err := site.Store().Delete(ctx, key); err != nil {
		return fmt.Errorf("failed to delete snippet: %w", err)
	}

	return nil
}

// The site's snippet locations, as keys.
const globalSnippetsKey = "site/snippets"

func localThemeKey(theme string) string { return "site/themes/" + theme + "/snippets" }

// snippetRoot is one place snippets live: a key prefix in a store.
//
// ⭐ This is what makes the two-root resolution above work without a special
// case. The site's own snippets and its local theme are prefixes in the
// site's store; the CLI themes directory is not a site, so it is a POSIX store
// rooted there, with no prefix, and stays a path. Whichever root resolution
// chose is the one every read, stat and write goes through.
type snippetRoot struct {
	site   storage.Site // the handle atomicfile writes through
	store  storage.Store
	prefix string
}

func siteRoot(site storage.Site, prefix string) snippetRoot {
	return snippetRoot{site: site, store: site.Store(), prefix: prefix}
}

func cliRoot(cliThemesDir, theme string) snippetRoot {
	dir := filepath.Join(cliThemesDir, theme, "snippets")
	st := storage.NewPOSIX(dir)
	return snippetRoot{site: storage.On(dir, st), store: st}
}

// key joins a validated snippet path onto the root's prefix.
func (r snippetRoot) key(rel string) string {
	rel = filepath.ToSlash(rel)
	if r.prefix == "" {
		return pathpkg.Clean(rel)
	}
	return pathpkg.Join(r.prefix, rel)
}

// present reports whether the root exists. On a filesystem that is whether
// its directory exists, as it always was; a store with no filesystem has no
// directories, so there a root is present when something is stored under it.
func (r snippetRoot) present() (bool, error) {
	ctx := context.Background()
	if r.store.Capabilities().RealPath {
		return r.store.Exists(ctx, r.prefix)
	}
	entries, err := r.store.List(ctx, r.prefix)
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

// list returns what is directly under rel in the root, or nothing when it
// could not be read.
func (r snippetRoot) list(rel string) []storage.Entry {
	prefix := r.prefix
	if rel != "" {
		prefix = r.key(rel)
	}
	entries, err := r.store.List(context.Background(), prefix)
	if err != nil {
		return nil
	}
	return entries
}
