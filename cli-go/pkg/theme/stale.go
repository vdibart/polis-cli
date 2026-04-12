package theme

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// baseTemplateFiles are the template files that _base provides.
var baseTemplateFiles = []string{
	"index.html", "post.html", "posts.html",
	"comment.html", "comment-inline.html",
	"tag.html", "tag-index.html",
}

// baseSnippetFiles are the snippet files that _base provides.
var baseSnippetFiles = []string{
	"about.html", "also-reading.html", "blessed-comment.html",
	"comment-item.html", "polis-widget.html", "post-item.html",
}

// oldCanonicalHashes maps filenames to SHA-256 hashes of known old system
// templates (with the theme-name HTML comment stripped). These are the templates
// that shipped with themes before the _base consolidation. Files matching these
// hashes are safe to remove — they're system files, not user customizations.
var oldCanonicalHashes = map[string]string{
	// Templates (hashed after stripping leading <!-- ... --> comment block)
	"index.html":          "5bf8b5c285685cca977f5435a9c3d4c253995d01c9350c505c70f847ee4afddd",
	"post.html":           "255c510add9de95ae69314d2d27f721447cf51e8fe4b974446f3604fb50c605f",
	"posts.html":          "9258ca3af95ba623b0755dbbbf86043ba15481dfee2857c02646d7f7302f7560",
	"comment.html":        "c8c0a6d52f1e2d0ea27e35aedb0f2313e469af193ba203de6f3253fffc6ada87",
	"comment-inline.html": "e3d7e9fc4dbd868b6c560f876bb40e978972cc7156c77c37545410482ff3c86b",
	"tag.html":            "be8e267db3bbadeafea035b1a720ace517a03fd7953c3ca2ee85456cbc76ad3d",
	"tag-index.html":      "1f49619cd6884ed45dbf0a8c09cfcb641b007dbd466f4f03addd30f102178368",
	// Snippets (hashed as-is, no comment to strip)
	"snippets/about.html":           "c4ce7e30ab58b1c4771089996677cf555c46706271763be793440d62d968ba28",
	"snippets/also-reading.html":    "fd16d02be6e72f8d0b82c8f93fb05db69c1175a09dfdebe325963dc4ad823ee7",
	"snippets/blessed-comment.html": "235d0f3ae0ff54425a323ca9ccf8bdeef8437255fd036135a54b8ea8ef1eabad",
	"snippets/comment-item.html":    "9f0641c8dc67fa527e537cd8b3bdb6e2809f511f92973df4e1c29947a9f4754b",
	"snippets/polis-widget.html":    "002a69a2c4445fb68e6a2e6f0c03d699099339cdbca63d5d67411d135f07b52f",
	"snippets/post-item.html":       "22962e9390beabf63a6eed8a8ec224ac42aa5a2e63eb4742b863098f7ccb2388",
}

// htmlCommentRe matches an HTML comment block at the start of a file.
var htmlCommentRe = regexp.MustCompile(`(?s)\A\s*<!--.*?-->\s*\n`)

// normalizeForHash strips the leading HTML comment block (theme-name comment)
// from template content before hashing, so all per-theme variants hash the same.
func normalizeForHash(content string) string {
	return htmlCommentRe.ReplaceAllString(content, "")
}

// isStaleFile returns true if the file at themePath is a known old system template
// (matches a canonical hash) OR matches the current _base file exactly.
func isStaleFile(themePath, basePath, relPath string) bool {
	themeData, err := os.ReadFile(themePath)
	if err != nil {
		return false
	}

	// Check 1: exact match against _base (handles future _base updates)
	if baseData, err := os.ReadFile(basePath); err == nil {
		if string(themeData) == string(baseData) {
			return true
		}
	}

	// Check 2: canonical hash match (handles old pre-consolidation templates)
	expectedHash, ok := oldCanonicalHashes[relPath]
	if !ok {
		return false
	}

	// For template files (not snippets), strip the comment before hashing
	content := string(themeData)
	if !strings.HasPrefix(relPath, "snippets/") {
		content = normalizeForHash(content)
	}

	h := sha256.Sum256([]byte(content))
	actualHash := fmt.Sprintf("%x", h)
	return actualHash == expectedHash
}

// StaleThemeFiles returns the list of stale system files in a theme directory.
// A file is stale if it matches _base exactly or matches a known old canonical hash.
func StaleThemeFiles(themeDir, baseDir string) []string {
	var stale []string

	for _, file := range baseTemplateFiles {
		if isStaleFile(filepath.Join(themeDir, file), filepath.Join(baseDir, file), file) {
			stale = append(stale, file)
		}
	}

	for _, file := range baseSnippetFiles {
		relPath := filepath.Join("snippets", file)
		if isStaleFile(filepath.Join(themeDir, relPath), filepath.Join(baseDir, relPath), relPath) {
			stale = append(stale, relPath)
		}
	}

	return stale
}
