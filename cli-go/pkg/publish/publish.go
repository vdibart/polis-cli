// Package publish provides post publishing logic compatible with the polis CLI.
package publish

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

var (
	frontmatterStripRe = regexp.MustCompile(`(?s)^---\n.*?\n---\n*`)
	frontmatterParseRe = regexp.MustCompile(`(?s)^---\n(.*?)\n---`)
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for frontmatter.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// PublishResult contains the result of publishing a post
type PublishResult struct {
	Success   bool   `json:"success"`
	Path      string `json:"path"`
	Title     string `json:"title"`
	Version   string `json:"version"`
	Signature string `json:"signature"`
	URL       string `json:"url,omitempty"`
}

// PostMeta contains metadata for a published post (for index)
type PostMeta struct {
	Type           string `json:"type"`
	Path           string `json:"path"`
	Title          string `json:"title"`
	Published      string `json:"published"`
	CurrentVersion string `json:"current_version"`
}

// ManifestData contains the manifest.json structure
// Field order matches bash CLI for consistency
// Note: site_title is now stored in .well-known/polis, not manifest.json
type ManifestData struct {
	Version       string `json:"version"`
	LastPublished string `json:"last_published"`
	PostCount     int    `json:"post_count"`
	CommentCount  int    `json:"comment_count"`
	ActiveTheme   string `json:"active_theme,omitempty"`
}

// ExtractTitle extracts the title from markdown content.
// Looks for the first # heading, falls back to first non-empty line.
func ExtractTitle(markdown string) string {
	lines := strings.Split(markdown, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Check for heading
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimPrefix(trimmed, "# ")
		}
		// Fall back to first non-empty line (truncated)
		if len(trimmed) > 60 {
			return trimmed[:60]
		}
		return trimmed
	}
	return "Untitled"
}

// Slugify converts a title to a URL-safe filename.
func Slugify(title string) string {
	// Convert to lowercase
	slug := strings.ToLower(title)

	// Replace spaces and special chars with hyphens
	var result []rune
	lastWasHyphen := false
	for _, r := range slug {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			result = append(result, r)
			lastWasHyphen = false
		} else if !lastWasHyphen {
			result = append(result, '-')
			lastWasHyphen = true
		}
	}

	slug = string(result)

	// Trim leading/trailing hyphens
	slug = strings.Trim(slug, "-")

	// Limit length
	if len(slug) > 50 {
		slug = slug[:50]
		// Don't end with a hyphen
		slug = strings.TrimRight(slug, "-")
	}

	if slug == "" {
		slug = "untitled-" + randomSuffix(8)
	}

	return slug
}

// randomSuffix generates a short random hex string.
func randomSuffix(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based suffix if crypto/rand fails
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// CanonicalizeContent normalizes content for consistent hashing.
// Strips leading empty lines, removes trailing whitespace from lines,
// and ensures single trailing newline.
// This matches the validator's canonicalizeContent function.
func CanonicalizeContent(content string) string {
	// Normalize line endings to LF (remove any CR characters)
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	// Strip leading empty lines (matches validator's .replace(/^\n+/, ''))
	content = strings.TrimLeft(content, "\n")

	lines := strings.Split(content, "\n")

	// Trim trailing whitespace from each line (including \r, space, tab)
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}

	// Remove trailing empty lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Join and ensure single trailing newline
	result := strings.Join(lines, "\n")
	if result != "" {
		result += "\n"
	}

	return result
}

// HashContent computes the SHA256 hash of content.
func HashContent(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// buildFrontmatter creates the YAML frontmatter for a post with an explicit generator string.
func buildFrontmatter(title, hash, timestamp, signature, generator string) string {
	// Note: signature is base64-encoded, single line for YAML
	return fmt.Sprintf(`---
title: %s
published: %s
generator: %s
current-version: sha256:%s
version-history:
  - sha256:%s (%s)
signature: %s
---`,
		escapeYAMLString(title),
		timestamp,
		generator,
		hash,
		hash,
		timestamp,
		signature,
	)
}

// escapeYAMLString escapes a string for safe YAML inclusion.
// Matches the bash CLI behavior: only quote when necessary.
func escapeYAMLString(s string) string {
	// In YAML, single quotes (apostrophes) are fine in unquoted strings.
	// Only quote when truly necessary:
	// - Contains colon followed by space (could be key-value)
	// - Contains newline (multiline)
	// - Contains double quotes
	// - Starts/ends with whitespace
	// - Starts with special YAML chars: *, &, !, |, >, @, `, #
	needsQuoting := false

	if strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		needsQuoting = true
	} else if strings.Contains(s, ": ") || strings.HasSuffix(s, ":") {
		needsQuoting = true
	} else if strings.Contains(s, "\n") {
		needsQuoting = true
	} else if strings.Contains(s, "\"") {
		needsQuoting = true
	} else if len(s) > 0 {
		firstChar := s[0]
		if firstChar == '*' || firstChar == '&' || firstChar == '!' ||
			firstChar == '|' || firstChar == '>' || firstChar == '@' ||
			firstChar == '`' || firstChar == '#' {
			needsQuoting = true
		}
	}

	if needsQuoting {
		// Escape double quotes and wrap in double quotes
		escaped := strings.ReplaceAll(s, "\"", "\\\"")
		return fmt.Sprintf("\"%s\"", escaped)
	}
	return s
}

// PublishPost publishes a markdown post and returns the result.
// If dsCfg is non-nil, it overrides package-level discovery globals for
// multi-tenant safety. Pass nil to use globals (single-tenant / CLI mode).
func PublishPost(dataDir, markdown, filename string, privateKey []byte, dsCfg ...*DiscoveryConfig) (*PublishResult, error) {
	// Resolve generator from config (or fall back to package global)
	var cfgForGen *DiscoveryConfig
	if len(dsCfg) > 0 {
		cfgForGen = dsCfg[0]
	}
	gen := resolveGenerator(cfgForGen)

	// Extract title
	title := ExtractTitle(markdown)

	// Generate filename if not provided
	if filename == "" {
		filename = Slugify(title)
		// If the slug is generic (no meaningful title), add a random suffix
		if filename == "untitled" {
			filename = "untitled-" + randomSuffix(8)
		}
	} else {
		// Sanitize provided filename
		filename = Slugify(filename)
	}

	// Ensure .md extension is not duplicated
	filename = strings.TrimSuffix(filename, ".md")

	// Ensure unique filename (prevent collisions)
	dateDir := time.Now().UTC().Format("20060102")
	filename = ensureUniqueFilename(dataDir, dateDir, filename)

	// Canonicalize the raw markdown for consistent hashing
	canonicalBody := CanonicalizeContent(markdown)

	// Compute hash of canonicalized body (validator strips leading newlines)
	hash := HashContent([]byte(canonicalBody))

	// Get timestamp
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Build content to sign (frontmatter without signature + content)
	unsignedFrontmatter := fmt.Sprintf(`---
title: %s
published: %s
generator: %s
current-version: sha256:%s
version-history:
  - sha256:%s (%s)
---`,
		escapeYAMLString(title),
		timestamp,
		gen,
		hash,
		hash,
		timestamp,
	)

	// Build full unsigned content, then canonicalize the whole thing for signing
	// This matches the bash CLI which canonicalizes the full file before signing
	fullUnsignedContent := unsignedFrontmatter + "\n\n" + canonicalBody
	canonicalizedForSigning := CanonicalizeContent(fullUnsignedContent)

	// Sign the canonicalized content
	signature, err := signing.SignContent([]byte(canonicalizedForSigning), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign content: %w", err)
	}

	// Extract just the base64 part of the signature for frontmatter
	sigBase64 := extractSignatureBase64(signature)

	// Build final frontmatter with signature
	finalFrontmatter := buildFrontmatter(title, hash, timestamp, sigBase64, gen)

	// Build final content
	finalContent := finalFrontmatter + "\n\n" + canonicalBody

	// Create directory structure: content/pub.polis.core/post/YYYYMMDD/
	postsDir := filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir)
	if err := os.MkdirAll(postsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create posts directory: %w", err)
	}

	// Write post file
	postPath := filepath.Join(postsDir, filename+".md")
	if err := os.WriteFile(postPath, []byte(finalContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write post file: %w", err)
	}

	// Update index (path relative to site root for content access)
	// Use filepath.ToSlash to ensure forward slashes on Windows (URLs must not contain backslashes)
	relativePath := filepath.ToSlash(filepath.Join("content", "pub.polis.core", "post", dateDir, filename+".md"))

	// Initialize version history with CLI-compatible format
	// Pass content WITHOUT frontmatter (canonicalBody)
	if err := initializeVersionHistory(dataDir, dateDir, filename, relativePath, canonicalBody, hash, timestamp); err != nil {
		// Log but don't fail - version history is nice to have
		fmt.Printf("[warning] Failed to initialize version history: %v\n", err)
	}
	meta := &PostMeta{
		Type:           "post",
		Path:           relativePath,
		Title:          title,
		Published:      timestamp,
		CurrentVersion: "sha256:" + hash,
	}
	if err := AppendToIndex(dataDir, meta); err != nil {
		fmt.Printf("[warning] Failed to update index: %v\n", err)
	}

	// Update manifest
	if err := UpdateManifest(dataDir); err != nil {
		fmt.Printf("[warning] Failed to update manifest: %v\n", err)
	}

	result := &PublishResult{
		Success:   true,
		Path:      relativePath,
		Title:     title,
		Version:   "sha256:" + hash,
		Signature: signature,
	}

	// Register with discovery service (non-fatal)
	var cfg *DiscoveryConfig
	if len(dsCfg) > 0 {
		cfg = dsCfg[0]
	}
	if err := RegisterPost(dataDir, result, privateKey, cfg); err != nil {
		fmt.Printf("[!] Discovery registration skipped: %v\n", err)
		fmt.Println("[i] If your site is newly deployed, run: polis register")
	}

	return result, nil
}

// ensureUniqueFilename checks for filename collisions and appends -2, -3, etc. if needed.
func ensureUniqueFilename(dataDir, dateDir, filename string) string {
	candidate := filename
	suffix := 2
	for {
		// Check posts content directory
		postPath := filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir, candidate+".md")
		if _, err := os.Stat(postPath); err == nil {
			candidate = fmt.Sprintf("%s-%d", filename, suffix)
			suffix++
			continue
		}

		// Check drafts directory
		draftPath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts", candidate+".md")
		if _, err := os.Stat(draftPath); err == nil {
			candidate = fmt.Sprintf("%s-%d", filename, suffix)
			suffix++
			continue
		}

		break
	}
	return candidate
}

// extractSignatureBase64 extracts the base64 content from an SSH signature.
func extractSignatureBase64(sig string) string {
	// Remove PEM headers and join lines
	lines := strings.Split(sig, "\n")
	var base64Lines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}
		base64Lines = append(base64Lines, line)
	}
	return strings.Join(base64Lines, "")
}

// computeUnifiedDiff computes a unified diff between old and new content.
// Returns the diff output, or empty string if contents are identical.
func computeUnifiedDiff(oldContent, newContent string) (string, error) {
	// Create a private temp directory (0700) so files aren't world-readable
	tmpDir, err := os.MkdirTemp("", "polis-diff-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	os.Chmod(tmpDir, 0700)

	// Create temp files for diff inside private directory
	oldFile, err := os.CreateTemp(tmpDir, "old-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file for old content: %w", err)
	}
	defer oldFile.Close()

	newFile, err := os.CreateTemp(tmpDir, "new-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file for new content: %w", err)
	}
	defer newFile.Close()

	// Write contents and close before diff (defer handles cleanup on error)
	if _, err := oldFile.WriteString(oldContent); err != nil {
		return "", fmt.Errorf("failed to write old content: %w", err)
	}
	if err := oldFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close old content file: %w", err)
	}

	if _, err := newFile.WriteString(newContent); err != nil {
		return "", fmt.Errorf("failed to write new content: %w", err)
	}
	if err := newFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close new content file: %w", err)
	}

	// Run diff -u
	cmd := exec.Command("diff", "-u", oldFile.Name(), newFile.Name())
	output, err := cmd.Output()

	// diff returns exit code 1 when files differ, which is expected
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Exit code 1 means files differ - this is normal
			if exitErr.ExitCode() == 1 {
				return string(output), nil
			}
		}
		return "", fmt.Errorf("diff command failed: %w", err)
	}

	// Exit code 0 means files are identical
	return string(output), nil
}

// updateCurrentHashInVersionsFile updates the CURRENT_HASH header in a versions file.
func updateCurrentHashInVersionsFile(versionsPath, newHash string) error {
	content, err := os.ReadFile(versionsPath)
	if err != nil {
		return fmt.Errorf("failed to read versions file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# CURRENT_HASH=") {
			lines[i] = "# CURRENT_HASH=sha256:" + newHash
			break
		}
	}

	return os.WriteFile(versionsPath, []byte(strings.Join(lines, "\n")), 0644)
}

// initializeVersionHistory creates the initial version history file.
// dateDir should be in format "YYYYMMDD" (e.g., "20260127")
// canonicalPath is the relative path like "posts/20260128/filename.md"
// contentWithoutFrontmatter is the body content without YAML frontmatter
func initializeVersionHistory(dataDir, dateDir, filename, canonicalPath, contentWithoutFrontmatter, hash, timestamp string) error {
	versionsDir := filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir, ".versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		return err
	}

	// CLI-compatible format with header and VERSION block
	versionContent := fmt.Sprintf(`# VERSION_FILE_FORMAT=1.0
# CANONICAL_FILE=%s
# CURRENT_HASH=sha256:%s

[VERSION sha256:%s]
TIMESTAMP=%s
PARENT=none
FULL_CONTENT_START
%sFULL_CONTENT_END

`, canonicalPath, hash, hash, timestamp, contentWithoutFrontmatter)

	versionPath := filepath.Join(versionsDir, filename+".md")
	return os.WriteFile(versionPath, []byte(versionContent), 0644)
}

// AppendToIndex appends a post entry to public.jsonl.
// Delegates to metadata.AppendPostToIndex for deduplication support.
func AppendToIndex(dataDir string, meta *PostMeta) error {
	return metadata.AppendPostToIndex(dataDir, meta.Path, meta.Title, meta.Published, meta.CurrentVersion)
}

// UpdateManifest is a no-op — manifest.json has been absorbed into .well-known/polis.
// Post/comment counts are computed on-the-fly by the webapp from the content directories.
func UpdateManifest(dataDir string) error {
	return nil
}

// HasFrontmatter checks if content already has YAML frontmatter.
func HasFrontmatter(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "---")
}

// StripFrontmatter removes existing frontmatter from content.
func StripFrontmatter(content string) string {
	// Only trim leading whitespace, preserve trailing content
	// (TrimSpace was removing trailing newlines, causing diff inconsistencies)
	content = strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(content, "---") {
		return content
	}

	// Find the closing ---
	result := frontmatterStripRe.ReplaceAllString(content, "")
	// Canonicalize the result to ensure consistent format for diffs
	return CanonicalizeContent(result)
}

// ParseFrontmatter extracts frontmatter fields from content.
func ParseFrontmatter(content string) map[string]string {
	result := make(map[string]string)
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return result
	}

	// Find frontmatter block
	matches := frontmatterParseRe.FindStringSubmatch(content)
	if len(matches) < 2 {
		return result
	}

	// Parse simple key: value pairs
	lines := strings.Split(matches[1], "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
			continue // Skip nested items like version-history entries
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			result[key] = value
		}
	}

	return result
}

// ExtractVersionHistory extracts the version history entries from frontmatter.
func ExtractVersionHistory(content string) []string {
	var history []string
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return history
	}

	// Find frontmatter block
	matches := frontmatterParseRe.FindStringSubmatch(content)
	if len(matches) < 2 {
		return history
	}

	// Find version-history entries
	lines := strings.Split(matches[1], "\n")
	inVersionHistory := false
	for _, line := range lines {
		if strings.HasPrefix(line, "version-history:") {
			inVersionHistory = true
			continue
		}
		if inVersionHistory {
			if strings.HasPrefix(line, "  - ") {
				entry := strings.TrimPrefix(line, "  - ")
				history = append(history, entry)
			} else if !strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "\t") && line != "" {
				break // End of version-history
			}
		}
	}

	return history
}

// RepublishPost updates an existing published post.
func RepublishPost(dataDir, postPath, markdown string, privateKey []byte, dsCfg ...*DiscoveryConfig) (*PublishResult, error) {
	// Resolve generator from config (or fall back to package global)
	var cfgForGen *DiscoveryConfig
	if len(dsCfg) > 0 {
		cfgForGen = dsCfg[0]
	}
	gen := resolveGenerator(cfgForGen)

	// Read existing post to get original metadata
	fullPath := filepath.Join(dataDir, postPath)
	existingContent, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read existing post: %w", err)
	}

	// Parse existing frontmatter
	existingFM := ParseFrontmatter(string(existingContent))
	originalPublished := existingFM["published"]
	if originalPublished == "" {
		originalPublished = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}

	// Get the old hash (without sha256: prefix) for version history
	oldCurrentVersion := existingFM["current-version"]
	oldHash := strings.TrimPrefix(oldCurrentVersion, "sha256:")

	// Get old content without frontmatter for diff computation
	oldContentWithoutFrontmatter := StripFrontmatter(string(existingContent))

	// Get existing version history
	versionHistory := ExtractVersionHistory(string(existingContent))

	// Extract title from new content
	title := ExtractTitle(markdown)

	// Canonicalize the raw markdown for consistent hashing
	canonicalBody := CanonicalizeContent(markdown)

	// Compute hash of canonicalized body (validator strips leading newlines)
	hash := HashContent([]byte(canonicalBody))

	// Get update timestamp
	updateTimestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Build version history string
	versionHistory = append(versionHistory, fmt.Sprintf("sha256:%s (%s)", hash, updateTimestamp))
	var versionHistoryYAML string
	for _, v := range versionHistory {
		versionHistoryYAML += fmt.Sprintf("\n  - %s", v)
	}

	// Build content to sign (frontmatter without signature + content)
	unsignedFrontmatter := fmt.Sprintf(`---
title: %s
published: %s
updated: %s
generator: %s
current-version: sha256:%s
version-history:%s
---`,
		escapeYAMLString(title),
		originalPublished,
		updateTimestamp,
		gen,
		hash,
		versionHistoryYAML,
	)

	// Build full unsigned content, then canonicalize the whole thing for signing
	// This matches the bash CLI which canonicalizes the full file before signing
	fullUnsignedContent := unsignedFrontmatter + "\n\n" + canonicalBody
	canonicalizedForSigning := CanonicalizeContent(fullUnsignedContent)

	// Sign the canonicalized content
	signature, err := signing.SignContent([]byte(canonicalizedForSigning), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign content: %w", err)
	}

	// Extract just the base64 part of the signature for frontmatter
	sigBase64 := extractSignatureBase64(signature)

	// Build final frontmatter with signature
	finalFrontmatter := fmt.Sprintf(`---
title: %s
published: %s
updated: %s
generator: %s
current-version: sha256:%s
version-history:%s
signature: %s
---`,
		escapeYAMLString(title),
		originalPublished,
		updateTimestamp,
		gen,
		hash,
		versionHistoryYAML,
		sigBase64,
	)

	// Build final content
	finalContent := finalFrontmatter + "\n\n" + canonicalBody

	// Write updated post file
	if err := os.WriteFile(fullPath, []byte(finalContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write post file: %w", err)
	}

	// Update version history file with CLI-compatible format
	// Path format: posts/YYYYMMDD/filename.md
	pathParts := strings.Split(postPath, string(filepath.Separator))
	if len(pathParts) >= 3 {
		dateDir := pathParts[1]
		filename := strings.TrimSuffix(pathParts[2], ".md")
		// Pass content WITHOUT frontmatter for diff computation
		if err := appendVersionHistory(dataDir, dateDir, filename, postPath, oldHash, hash, updateTimestamp, oldContentWithoutFrontmatter, canonicalBody); err != nil {
			fmt.Printf("[warning] Failed to update version history: %v\n", err)
		}
	}

	// Update index entry
	if err := UpdateIndexEntry(dataDir, postPath, title, "sha256:"+hash); err != nil {
		fmt.Printf("[warning] Failed to update index: %v\n", err)
	}

	// Update manifest
	if err := UpdateManifest(dataDir); err != nil {
		fmt.Printf("[warning] Failed to update manifest: %v\n", err)
	}

	result := &PublishResult{
		Success:   true,
		Path:      postPath,
		Title:     title,
		Version:   "sha256:" + hash,
		Signature: signature,
	}

	// Register with discovery service (non-fatal)
	var cfg *DiscoveryConfig
	if len(dsCfg) > 0 {
		cfg = dsCfg[0]
	}
	if err := RegisterPost(dataDir, result, privateKey, cfg); err != nil {
		fmt.Printf("[!] Discovery registration skipped: %v\n", err)
		fmt.Println("[i] If your site is newly deployed, run: polis register")
	}

	return result, nil
}

// appendVersionHistory appends a new version to the version history file.
// dateDir should be in format "YYYYMMDD" (e.g., "20260127")
// canonicalPath is the relative path like "posts/20260128/filename.md"
// previousHash is the hash of the previous version (without sha256: prefix)
// oldContentWithoutFrontmatter is the previous version's content for diff computation
// newContentWithoutFrontmatter is the new content for diff computation
func appendVersionHistory(dataDir, dateDir, filename, canonicalPath, previousHash, newHash, timestamp, oldContentWithoutFrontmatter, newContentWithoutFrontmatter string) error {
	versionsDir := filepath.Join(dataDir, "content", "pub.polis.core", "post", dateDir, ".versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		return err
	}

	versionPath := filepath.Join(versionsDir, filename+".md")

	// Update CURRENT_HASH in the header
	if err := updateCurrentHashInVersionsFile(versionPath, newHash); err != nil {
		// If file doesn't exist, create it with header
		if os.IsNotExist(err) {
			header := fmt.Sprintf(`# VERSION_FILE_FORMAT=1.0
# CANONICAL_FILE=%s
# CURRENT_HASH=sha256:%s

`, canonicalPath, newHash)
			if err := os.WriteFile(versionPath, []byte(header), 0644); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// Compute unified diff
	diffContent, err := computeUnifiedDiff(oldContentWithoutFrontmatter, newContentWithoutFrontmatter)
	if err != nil {
		return fmt.Errorf("failed to compute diff: %w", err)
	}

	// Append new version entry with diff
	versionEntry := fmt.Sprintf(`[VERSION sha256:%s]
TIMESTAMP=%s
PARENT=sha256:%s
DIFF_START
%sDIFF_END

`, newHash, timestamp, previousHash, diffContent)

	f, err := os.OpenFile(versionPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(versionEntry)
	return err
}

// UpdateIndexEntry updates an existing entry in the index.
func UpdateIndexEntry(dataDir, postPath, newTitle, newVersion string) error {
	indexPath := filepath.Join(dataDir, "content", "pub.polis.core", "index.jsonl")

	data, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}

	var newLines []string
	lines := strings.Split(string(data), "\n")
	found := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry PostMeta
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			newLines = append(newLines, line)
			continue
		}

		if entry.Path == postPath {
			// Update this entry
			entry.Title = newTitle
			entry.CurrentVersion = newVersion
			updated, _ := json.Marshal(entry)
			newLines = append(newLines, string(updated))
			found = true
		} else {
			newLines = append(newLines, line)
		}
	}

	if !found {
		return fmt.Errorf("post not found in index: %s", postPath)
	}

	return os.WriteFile(indexPath, []byte(strings.Join(newLines, "\n")+"\n"), 0644)
}
