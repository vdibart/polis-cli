// Package comment provides comment management for polis.
package comment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
	// Aliased: "version" collides with the many local `version := fm[...]` vars.
	verhist "github.com/vdibart/polis-cli/cli-go/pkg/version"
)

var (
	frontmatterStripRe = regexp.MustCompile(`(?s)^---\n.*?\n---\n*`)
	frontmatterParseRe = regexp.MustCompile(`(?s)^---\n(.*?)\n---`)
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for comment frontmatter.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

const (
	// Comment status directories
	StatusDrafts  = "drafts"
	StatusPending = "pending"
	StatusBlessed = "blessed"
	StatusDenied  = "denied"
)

// getCommentDir returns the base directory for comments of a given status.
// Private statuses (drafts, pending, denied) go to .polis/bundles/pub.polis.core/comments/
// Blessed comments go to content/pub.polis.core/comment/ (with YYYYMMDD date structure)
func getCommentDir(dataDir, status string) string {
	if status == StatusBlessed {
		return filepath.Join(dataDir, "content", "pub.polis.core", "comment")
	}
	return filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", status)
}

// CommentDraft represents a comment draft before signing.
type CommentDraft struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"` // Comment title (extracted from first heading or auto-generated)
	InReplyTo string `json:"in_reply_to"`
	RootPost  string `json:"root_post,omitempty"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// CommentMeta represents metadata for a signed comment.
// Uses CLI-compatible field names for interoperability.
type CommentMeta struct {
	ID               string   `json:"id"`
	Title            string   `json:"title,omitempty"` // Comment title
	CommentURL       string   `json:"comment_url"`     // Full URL to comment file
	CommentVersion   string   `json:"comment_version"` // current-version in frontmatter
	InReplyTo        string   `json:"in_reply_to"`     // in-reply-to.url in frontmatter
	InReplyToVersion string   `json:"in_reply_to_version,omitempty"`
	RootPost         string   `json:"root_post"` // in-reply-to.root-post in frontmatter
	Author           string   `json:"author"`    // Derived from site, not in frontmatter
	Timestamp        string   `json:"timestamp"` // published in frontmatter
	Updated          string   `json:"updated,omitempty"`
	Status           string   `json:"status"`
	VersionHistory   []string `json:"version_history,omitempty"`
	Excerpt          string   `json:"excerpt,omitempty"`
}

// SignedComment represents a fully signed comment ready for blessing request.
type SignedComment struct {
	Meta      *CommentMeta `json:"meta"`
	Content   string       `json:"content"`
	Signature string       `json:"signature"`
}

// GenerateCommentID generates a unique comment ID from the target post and timestamp.
func GenerateCommentID(inReplyTo string, timestamp time.Time) string {
	// Extract domain and slug from in_reply_to URL
	// e.g., https://alice.polis.site/posts/20260127/hello-world.md
	parts := strings.Split(inReplyTo, "/")
	domain := ""
	slug := ""

	for i, part := range parts {
		if strings.Contains(part, ".polis.") || strings.Contains(part, ".polis-") {
			domain = strings.Split(part, ".")[0] // Extract subdomain
		}
		if part == "posts" && i+2 < len(parts) {
			slug = strings.TrimSuffix(parts[len(parts)-1], ".md")
		}
	}

	if domain == "" {
		domain = "unknown"
	}
	if slug == "" {
		slug = "post"
	}

	dateStr := timestamp.Format("20060102")
	return fmt.Sprintf("%s-%s-%s", domain, slug, dateStr)
}

// SaveDraft saves a comment draft to the private drafts directory.
func SaveDraft(dataDir string, draft *CommentDraft) error {
	// Normalize URLs to .md format (defense-in-depth)
	draft.InReplyTo = polisurl.NormalizeToMD(draft.InReplyTo)
	draft.RootPost = polisurl.NormalizeToMD(draft.RootPost)

	draftsDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusDrafts)
	if err := site.MkdirAllPrivate(dataDir, draftsDir); err != nil {
		return fmt.Errorf("failed to create drafts directory: %w", err)
	}

	if draft.ID == "" {
		draft.ID = GenerateCommentID(draft.InReplyTo, time.Now().UTC())
	}
	draft.ID = ensureUniqueCommentID(dataDir, draft.ID)

	if draft.CreatedAt == "" {
		draft.CreatedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	draft.UpdatedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// If no root_post specified, use in_reply_to as root
	if draft.RootPost == "" {
		draft.RootPost = draft.InReplyTo
	}

	// Save as markdown with simple frontmatter
	content := fmt.Sprintf(`---
in_reply_to: %s
root_post: %s
created_at: %s
updated_at: %s
---

%s`, draft.InReplyTo, draft.RootPost, draft.CreatedAt, draft.UpdatedAt, draft.Content)

	draftPath := filepath.Join(draftsDir, draft.ID+".md")
	if err := os.WriteFile(draftPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to save draft: %w", err)
	}

	return nil
}

// LoadDraft loads a comment draft by ID.
func LoadDraft(dataDir, id string) (*CommentDraft, error) {
	draftPath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusDrafts, id+".md")
	data, err := os.ReadFile(draftPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read draft: %w", err)
	}

	draft := &CommentDraft{ID: id}

	// Parse frontmatter
	fm := ParseFrontmatter(string(data))
	draft.InReplyTo = fm["in_reply_to"]
	draft.RootPost = fm["root_post"]
	draft.CreatedAt = fm["created_at"]
	draft.UpdatedAt = fm["updated_at"]

	// Extract content (after frontmatter)
	draft.Content = StripFrontmatter(string(data))

	return draft, nil
}

// ListDrafts returns all comment drafts.
func ListDrafts(dataDir string) ([]*CommentDraft, error) {
	draftsDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusDrafts)
	entries, err := os.ReadDir(draftsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*CommentDraft{}, nil
		}
		return nil, fmt.Errorf("failed to read drafts directory: %w", err)
	}

	var drafts []*CommentDraft
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".md")
		draft, err := LoadDraft(dataDir, id)
		if err != nil {
			continue // Skip invalid drafts
		}
		drafts = append(drafts, draft)
	}

	return drafts, nil
}

// DeleteDraft removes a comment draft.
func DeleteDraft(dataDir, id string) error {
	draftPath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusDrafts, id+".md")
	if err := os.Remove(draftPath); err != nil {
		return fmt.Errorf("failed to delete draft: %w", err)
	}
	return nil
}

// SignComment signs a draft and moves it to pending status.
// Uses CLI-compatible frontmatter format with nested in-reply-to structure.
// authorIdentity is the domain (e.g. "alice.polis.pub") written to the author frontmatter field.
// For backward compatibility, email addresses are also accepted.
func SignComment(dataDir string, draft *CommentDraft, authorIdentity, siteURL string, privateKey []byte, generator ...string) (*SignedComment, error) {
	gen := GetGenerator()
	if len(generator) > 0 && generator[0] != "" {
		gen = generator[0]
	}
	// Normalize URLs to .md format (defense-in-depth)
	draft.InReplyTo = polisurl.NormalizeToMD(draft.InReplyTo)
	draft.RootPost = polisurl.NormalizeToMD(draft.RootPost)

	if draft.InReplyTo == "" {
		return nil, fmt.Errorf("in_reply_to is required")
	}

	timestamp := time.Now().UTC()

	// Generate comment ID and URL
	commentID := draft.ID
	if commentID == "" {
		commentID = GenerateCommentID(draft.InReplyTo, timestamp)
	}
	commentID = ensureUniqueCommentID(dataDir, commentID)
	dateDir := timestamp.Format("20060102")
	commentURL := fmt.Sprintf("%s/comments/%s/%s.md", strings.TrimSuffix(siteURL, "/"), dateDir, commentID)

	// Generate title (from first heading or auto-generate)
	title := draft.Title
	if title == "" {
		title = extractTitleFromContent(draft.Content)
	}
	if title == "" {
		// Auto-generate title from target post
		title = fmt.Sprintf("Re: %s", extractSlugFromURL(draft.InReplyTo))
	}

	// Canonicalize content
	content := CanonicalizeContent(draft.Content)

	// Compute hash of canonicalized body (validator strips leading newlines)
	hash := HashContent([]byte(content))
	timestampStr := timestamp.Format("2006-01-02T15:04:05Z")

	// Root post defaults to in_reply_to if not specified
	rootPost := draft.RootPost
	if rootPost == "" {
		rootPost = draft.InReplyTo
	}

	// Build CLI-compatible frontmatter (without signature first)
	// CLI format uses nested in-reply-to with url and root-post
	unsignedFrontmatter := fmt.Sprintf(`---
title: %s
type: comment
published: %s
generator: %s
in-reply-to:
  url: %s
  root-post: %s
current-version: sha256:%s
version-history:
  - sha256:%s (%s)
---`,
		escapeYAMLTitle(title),
		timestampStr,
		gen,
		draft.InReplyTo,
		rootPost,
		hash,
		hash,
		timestampStr,
	)

	// Canonicalize full content for signing (matches CLI behavior)
	fullUnsignedContent := unsignedFrontmatter + "\n\n" + content
	canonicalizedForSigning := CanonicalizeContent(fullUnsignedContent)

	// Sign the content
	signature, err := signing.SignContent([]byte(canonicalizedForSigning), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign comment: %w", err)
	}

	// Extract base64 signature
	sigBase64 := extractSignatureBase64(signature)

	// Build final content with signature
	finalFrontmatter := fmt.Sprintf(`---
title: %s
type: comment
published: %s
author: %s
generator: %s
in-reply-to:
  url: %s
  root-post: %s
current-version: sha256:%s
version-history:
  - sha256:%s (%s)
signature: %s
---`,
		escapeYAMLTitle(title),
		timestampStr,
		authorIdentity,
		gen,
		draft.InReplyTo,
		rootPost,
		hash,
		hash,
		timestampStr,
		sigBase64,
	)

	finalContent := finalFrontmatter + "\n\n" + content

	// Create pending directory (private)
	pendingDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusPending)
	if err := site.MkdirAllPrivate(dataDir, pendingDir); err != nil {
		return nil, fmt.Errorf("failed to create pending directory: %w", err)
	}

	// Write to pending
	pendingPath := filepath.Join(pendingDir, commentID+".md")
	if err := os.WriteFile(pendingPath, []byte(finalContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write pending comment: %w", err)
	}

	// Delete draft
	if draft.ID != "" {
		_ = DeleteDraft(dataDir, draft.ID)
	}

	meta := &CommentMeta{
		ID:             commentID,
		Title:          title,
		CommentURL:     commentURL,
		CommentVersion: "sha256:" + hash,
		InReplyTo:      draft.InReplyTo,
		RootPost:       rootPost,
		Author:         authorIdentity,
		Timestamp:      timestampStr,
		Status:         StatusPending,
		VersionHistory: []string{fmt.Sprintf("sha256:%s (%s)", hash, timestampStr)},
	}

	return &SignedComment{
		Meta:      meta,
		Content:   content,
		Signature: signature,
	}, nil
}

// MoveComment moves a comment between status directories.
// When moving to blessed status, uses date-based directory structure (content/pub.polis.core/comment/YYYYMMDD/).
// Other statuses use .polis/bundles/pub.polis.core/comments/<status>/.
// When moving to blessed, also adds the comment to index.jsonl.
func MoveComment(dataDir, commentID, fromStatus, toStatus string) error {
	// Determine source path
	var fromPath string
	if fromStatus == StatusBlessed {
		// Need to find the blessed comment in date directories
		found, foundPath := findBlessedComment(dataDir, commentID)
		if !found {
			return fmt.Errorf("comment not found: %s", commentID)
		}
		fromPath = foundPath
	} else {
		fromPath = filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", fromStatus, commentID+".md")
	}

	// Read source file
	data, err := os.ReadFile(fromPath)
	if err != nil {
		return fmt.Errorf("failed to read comment: %w", err)
	}

	content := string(data)
	fm := ParseFrontmatter(content)

	// Determine destination path
	var toPath string
	var relativePath string // For public.jsonl entry
	if toStatus == StatusBlessed {
		// Extract timestamp from frontmatter to determine date directory
		timestamp := time.Now().UTC()
		// Try CLI format first (published), then webapp format (timestamp)
		if ts := fm["published"]; ts != "" {
			if parsed, err := time.Parse("2006-01-02T15:04:05Z", ts); err == nil {
				timestamp = parsed
			}
		} else if ts := fm["timestamp"]; ts != "" {
			if parsed, err := time.Parse("2006-01-02T15:04:05Z", ts); err == nil {
				timestamp = parsed
			}
		}
		dateDir := timestamp.Format("20060102")
		toDir := filepath.Join(dataDir, "content", "pub.polis.core", "comment", dateDir)
		if err := os.MkdirAll(toDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
		toPath = filepath.Join(toDir, commentID+".md")
		relativePath = filepath.ToSlash(filepath.Join("content", "pub.polis.core", "comment", dateDir, commentID+".md"))
	} else {
		toDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", toStatus)
		if err := site.MkdirAllPrivate(dataDir, toDir); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
		toPath = filepath.Join(toDir, commentID+".md")
	}

	// Write to destination
	if err := os.WriteFile(toPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write comment: %w", err)
	}

	// Remove source
	if err := os.Remove(fromPath); err != nil {
		return fmt.Errorf("failed to remove source comment: %w", err)
	}

	// When moving to blessed, add to public.jsonl and blessed-comments.json
	if toStatus == StatusBlessed {
		// Parse nested in-reply-to structure
		inReplyToURL, _ := ParseNestedInReplyTo(content)
		// Fall back to flat field if not found
		if inReplyToURL == "" {
			inReplyToURL = fm["in_reply_to"]
		}

		// Get timestamp (CLI format or webapp format)
		published := fm["published"]
		if published == "" {
			published = fm["timestamp"]
		}

		// Get version (CLI format or webapp format)
		version := fm["current-version"]
		if version == "" {
			version = fm["comment_version"]
		}

		// Build comment URL from frontmatter or construct from path
		commentURL := fm["comment_url"]
		if commentURL == "" {
			// Fallback: URL not available, use relative path
			commentURL = relativePath
		}

		// Add to public.jsonl
		if err := metadata.AppendCommentToIndex(
			dataDir,
			relativePath,
			fm["title"],
			published,
			version,
			inReplyToURL,
		); err != nil {
			// Log but don't fail - the comment is already blessed
			_ = err
		}

		// Add to blessed-comments.json so rendered posts show the comment
		postPath := extractPostPath(inReplyToURL)
		if err := metadata.AddBlessedComment(dataDir, postPath, metadata.BlessedComment{
			URL:     commentURL,
			Version: version,
		}); err != nil {
			// Log but don't fail
			_ = err
		}

		// Update manifest comment count
		if err := publish.UpdateManifest(dataDir); err != nil {
			// Log but don't fail
			_ = err
		}
	}

	return nil
}

// PublishComment copies a pending comment to the public comments/ directory,
// renders an HTML version, appends it to public.jsonl, and updates the manifest.
// This makes the comment publicly accessible on the commenter's site regardless
// of blessing status (matching bash CLI behavior).
func PublishComment(dataDir, commentID string) error {
	// Read the pending comment
	pendingPath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusPending, commentID+".md")
	data, err := os.ReadFile(pendingPath)
	if err != nil {
		return fmt.Errorf("failed to read pending comment: %w", err)
	}

	content := string(data)
	fm := ParseFrontmatter(content)

	// Determine date directory from timestamp
	timestamp := time.Now().UTC()
	if ts := fm["published"]; ts != "" {
		if parsed, err := time.Parse("2006-01-02T15:04:05Z", ts); err == nil {
			timestamp = parsed
		}
	}
	dateDir := timestamp.Format("20060102")

	// Create target directory: content/pub.polis.core/comment/YYYYMMDD/
	targetDir := filepath.Join(dataDir, "content", "pub.polis.core", "comment", dateDir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create comments directory: %w", err)
	}

	// Copy markdown file to public location
	mdPath := filepath.Join(targetDir, commentID+".md")
	if err := os.WriteFile(mdPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write public comment: %w", err)
	}

	// Note: HTML rendering is handled by RenderSite() which is called after
	// PublishComment(). Writing raw HTML here would produce untemplated output
	// that gets overwritten anyway.

	// Append to public.jsonl
	relativePath := filepath.ToSlash(filepath.Join("content", "pub.polis.core", "comment", dateDir, commentID+".md"))

	// Seed the version history side-car (only if absent, never clobber) so a
	// later RepublishComment can diff against this base version. The base body is
	// the canonicalized content without frontmatter — identical to what republish
	// recomputes as the "old" content for its diff.
	versionsPath := verhist.GetVersionsFilePath(mdPath, ".versions")
	if _, statErr := os.Stat(versionsPath); os.IsNotExist(statErr) {
		baseHash := strings.TrimPrefix(fm["current-version"], "sha256:")
		if baseHash == "" {
			baseHash = strings.TrimPrefix(fm["comment_version"], "sha256:")
		}
		if baseHash != "" {
			basePublished := fm["published"]
			if basePublished == "" {
				basePublished = timestamp.Format("2006-01-02T15:04:05Z")
			}
			baseBody := CanonicalizeContent(StripFrontmatter(content))
			if err := verhist.InitializeHistory(versionsPath, relativePath, baseBody, baseHash, basePublished); err != nil {
				_ = err // non-fatal; version history is best-effort
			}
		}
	}

	// Parse nested in-reply-to for the index entry
	inReplyToURL, _ := ParseNestedInReplyTo(content)
	if inReplyToURL == "" {
		inReplyToURL = fm["in_reply_to"]
	}

	version := fm["current-version"]
	if version == "" {
		version = fm["comment_version"]
	}

	if err := metadata.AppendCommentToIndex(
		dataDir,
		relativePath,
		fm["title"],
		fm["published"],
		version,
		inReplyToURL,
	); err != nil {
		// Log but don't fail - the comment file is already published
		_ = err
	}

	// Update manifest comment count
	if err := publish.UpdateManifest(dataDir); err != nil {
		_ = err
	}

	return nil
}

// FindBlessedComment searches for a blessed comment in the date-based directory structure.
// Structure: content/pub.polis.core/comment/YYYYMMDD/comment-id.md
// Returns (found, absolutePath).
func FindBlessedComment(dataDir, commentID string) (bool, string) {
	return findBlessedComment(dataDir, commentID)
}

// findBlessedComment is the internal implementation.
func findBlessedComment(dataDir, commentID string) (bool, string) {
	commentsDir := filepath.Join(dataDir, "content", "pub.polis.core", "comment")

	// Walk through date directories (YYYYMMDD format)
	dateDirs, err := os.ReadDir(commentsDir)
	if err != nil {
		return false, ""
	}

	for _, dateDir := range dateDirs {
		if !dateDir.IsDir() {
			continue
		}
		commentPath := filepath.Join(commentsDir, dateDir.Name(), commentID+".md")
		if _, err := os.Stat(commentPath); err == nil {
			return true, commentPath
		}
	}

	return false, ""
}

// ListComments returns comments with a specific status.
// For blessed status, searches the date-based public comments directory.
// For other statuses, searches the private .polis/comments/<status>/ directory.
// Parses CLI-compatible frontmatter format.
func ListComments(dataDir, status string) ([]*CommentMeta, error) {
	if status == StatusBlessed {
		return listBlessedComments(dataDir)
	}

	commentsDir := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", status)
	entries, err := os.ReadDir(commentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*CommentMeta{}, nil
		}
		return nil, fmt.Errorf("failed to read comments directory: %w", err)
	}

	var comments []*CommentMeta
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		commentPath := filepath.Join(commentsDir, entry.Name())
		data, err := os.ReadFile(commentPath)
		if err != nil {
			continue
		}

		content := string(data)
		fm := ParseFrontmatter(content)

		// Parse nested in-reply-to structure (CLI format)
		inReplyToURL, rootPost := ParseNestedInReplyTo(content)

		// Fall back to flat fields if nested not found (backwards compat)
		if inReplyToURL == "" {
			inReplyToURL = fm["in_reply_to"]
		}
		if rootPost == "" {
			rootPost = fm["root_post"]
		}

		// Use CLI field names, fall back to webapp field names
		timestamp := fm["published"]
		if timestamp == "" {
			timestamp = fm["timestamp"]
		}
		version := fm["current-version"]
		if version == "" {
			version = fm["comment_version"]
		}

		// Extract excerpt from body text
		body := strings.TrimSpace(StripFrontmatter(content))
		excerpt := body
		if len(excerpt) > 140 {
			cut := strings.LastIndex(excerpt[:140], " ")
			if cut < 70 {
				cut = 140
			}
			excerpt = excerpt[:cut] + "…"
		}

		meta := &CommentMeta{
			ID:             strings.TrimSuffix(entry.Name(), ".md"),
			Title:          fm["title"],
			CommentURL:     fm["comment_url"], // May be empty for CLI-created comments
			CommentVersion: version,
			InReplyTo:      inReplyToURL,
			RootPost:       rootPost,
			Author:         fm["author"],
			Timestamp:      timestamp,
			Updated:        fm["updated"],
			Status:         status,
			Excerpt:        excerpt,
		}
		comments = append(comments, meta)
	}

	return comments, nil
}

// listBlessedComments walks the date-based directory structure to find all blessed comments.
// Structure: comments/YYYYMMDD/comment-id.md
// Parses CLI-compatible frontmatter format.
func listBlessedComments(dataDir string) ([]*CommentMeta, error) {
	var comments []*CommentMeta
	commentsDir := filepath.Join(dataDir, "content", "pub.polis.core", "comment")

	// Walk through date directories (YYYYMMDD format)
	dateDirs, err := os.ReadDir(commentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*CommentMeta{}, nil
		}
		return nil, fmt.Errorf("failed to read comments directory: %w", err)
	}

	for _, dateDir := range dateDirs {
		if !dateDir.IsDir() {
			continue
		}
		datePath := filepath.Join(commentsDir, dateDir.Name())

		// List comment files in this date directory
		files, err := os.ReadDir(datePath)
		if err != nil {
			continue
		}

		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".md") {
				continue
			}

			// Skip comments that still exist in pending (published-but-pending, not yet blessed)
			pendingPath := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", StatusPending, file.Name())
			if _, err := os.Stat(pendingPath); err == nil {
				continue
			}

			commentPath := filepath.Join(datePath, file.Name())
			data, err := os.ReadFile(commentPath)
			if err != nil {
				continue
			}

			content := string(data)
			fm := ParseFrontmatter(content)

			// Parse nested in-reply-to structure (CLI format)
			inReplyToURL, rootPost := ParseNestedInReplyTo(content)

			// Fall back to flat fields if nested not found (backwards compat)
			if inReplyToURL == "" {
				inReplyToURL = fm["in_reply_to"]
			}
			if rootPost == "" {
				rootPost = fm["root_post"]
			}

			// Use CLI field names, fall back to webapp field names
			timestamp := fm["published"]
			if timestamp == "" {
				timestamp = fm["timestamp"]
			}
			version := fm["current-version"]
			if version == "" {
				version = fm["comment_version"]
			}

			// Extract excerpt from body text
			body := strings.TrimSpace(StripFrontmatter(content))
			excerpt := body
			if len(excerpt) > 140 {
				cut := strings.LastIndex(excerpt[:140], " ")
				if cut < 70 {
					cut = 140
				}
				excerpt = excerpt[:cut] + "…"
			}

			meta := &CommentMeta{
				ID:             strings.TrimSuffix(file.Name(), ".md"),
				Title:          fm["title"],
				CommentURL:     fm["comment_url"], // May be empty for CLI-created comments
				CommentVersion: version,
				InReplyTo:      inReplyToURL,
				RootPost:       rootPost,
				Author:         fm["author"],
				Timestamp:      timestamp,
				Updated:        fm["updated"],
				Status:         StatusBlessed,
				Excerpt:        excerpt,
			}
			comments = append(comments, meta)
		}
	}

	return comments, nil
}

// GetComment retrieves a specific comment by ID and status.
// For blessed status, searches the date-based public directory.
// For other statuses, uses the private .polis/comments/<status>/ directory.
// Parses CLI-compatible frontmatter format.
func GetComment(dataDir, commentID, status string) (*SignedComment, error) {
	var commentPath string

	if status == StatusBlessed {
		found, foundPath := findBlessedComment(dataDir, commentID)
		if !found {
			return nil, fmt.Errorf("comment not found: %s", commentID)
		}
		commentPath = foundPath
	} else {
		commentPath = filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", status, commentID+".md")
	}

	data, err := os.ReadFile(commentPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read comment: %w", err)
	}

	fileContent := string(data)
	fm := ParseFrontmatter(fileContent)
	content := StripFrontmatter(fileContent)

	// Parse nested in-reply-to structure (CLI format)
	inReplyToURL, rootPost := ParseNestedInReplyTo(fileContent)

	// Fall back to flat fields if nested not found (backwards compat)
	if inReplyToURL == "" {
		inReplyToURL = fm["in_reply_to"]
	}
	if rootPost == "" {
		rootPost = fm["root_post"]
	}

	// Use CLI field names, fall back to webapp field names
	timestamp := fm["published"]
	if timestamp == "" {
		timestamp = fm["timestamp"]
	}
	version := fm["current-version"]
	if version == "" {
		version = fm["comment_version"]
	}

	meta := &CommentMeta{
		ID:             commentID,
		Title:          fm["title"],
		CommentURL:     fm["comment_url"], // May be empty for CLI-created comments
		CommentVersion: version,
		InReplyTo:      inReplyToURL,
		RootPost:       rootPost,
		Author:         fm["author"],
		Timestamp:      timestamp,
		Updated:        fm["updated"],
		Status:         status,
	}

	return &SignedComment{
		Meta:      meta,
		Content:   content,
		Signature: fm["signature"],
	}, nil
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
			continue // Skip nested items
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

// StripFrontmatter removes frontmatter from content.
func StripFrontmatter(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return content
	}

	return strings.TrimSpace(frontmatterStripRe.ReplaceAllString(content, ""))
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

// HashContent computes SHA256 hash of content.
func HashContent(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// extractSignatureBase64 extracts the base64 content from an SSH signature.
func extractSignatureBase64(sig string) string {
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

// ToJSON serializes CommentMeta to JSON.
func (m *CommentMeta) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

// extractTitleFromContent extracts title from first markdown heading.
func extractTitleFromContent(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

// extractSlugFromURL extracts the slug (filename without extension) from a URL.
func extractSlugFromURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return "post"
	}
	filename := parts[len(parts)-1]
	return strings.TrimSuffix(filename, ".md")
}

// escapeYAMLTitle escapes a title for YAML frontmatter (CLI-compatible).
// Only quotes when truly necessary (contains colons, newlines, or special chars).
func escapeYAMLTitle(s string) string {
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
		escaped := strings.ReplaceAll(s, "\"", "\\\"")
		return fmt.Sprintf("\"%s\"", escaped)
	}
	return s
}

// ensureUniqueCommentID checks for comment ID collisions across all status directories.
// Appends -2, -3, etc. if a collision is found.
func ensureUniqueCommentID(dataDir, commentID string) string {
	candidate := commentID
	suffix := 2
	for {
		collision := false
		// Check all private status dirs
		for _, status := range []string{StatusDrafts, StatusPending, StatusDenied} {
			path := filepath.Join(dataDir, ".polis", "bundles", "pub.polis.core", "comments", status, candidate+".md")
			if _, err := os.Stat(path); err == nil {
				collision = true
				break
			}
		}
		// Check blessed (public) comments date dirs
		if !collision {
			commentsDir := filepath.Join(dataDir, "content", "pub.polis.core", "comment")
			if dateDirs, err := os.ReadDir(commentsDir); err == nil {
				for _, dd := range dateDirs {
					if dd.IsDir() {
						path := filepath.Join(commentsDir, dd.Name(), candidate+".md")
						if _, err := os.Stat(path); err == nil {
							collision = true
							break
						}
					}
				}
			}
		}

		if !collision {
			break
		}
		candidate = fmt.Sprintf("%s-%d", commentID, suffix)
		suffix++
	}
	return candidate
}

// extractPostPath extracts the relative post path from a full URL.
// e.g., https://alice.polis.site/posts/20260127/hello.md -> posts/20260127/hello.md
func extractPostPath(url string) string {
	idx := strings.Index(url, "/posts/")
	if idx >= 0 {
		return url[idx+1:] // Return "posts/..." without leading slash
	}
	return url
}

// ParseNestedInReplyTo extracts url and root-post from nested in-reply-to structure.
// CLI format:
//
//	in-reply-to:
//	  url: https://...
//	  root-post: https://...
func ParseNestedInReplyTo(content string) (url, rootPost string) {
	lines := strings.Split(content, "\n")
	inReplyToSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check if we're entering in-reply-to section
		if strings.HasPrefix(line, "in-reply-to:") && !strings.HasPrefix(line, "  ") {
			inReplyToSection = true
			continue
		}

		// Check if we've left the in-reply-to section (new top-level field)
		if inReplyToSection && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			break
		}

		// Parse nested fields
		if inReplyToSection {
			if strings.HasPrefix(trimmed, "url:") {
				url = strings.TrimSpace(strings.TrimPrefix(trimmed, "url:"))
			} else if strings.HasPrefix(trimmed, "root-post:") {
				rootPost = strings.TrimSpace(strings.TrimPrefix(trimmed, "root-post:"))
			}
		}
	}

	return url, rootPost
}
