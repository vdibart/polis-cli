// Package comment provides comment management for polis.
package comment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	pathpkg "path"
	"regexp"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	// ⚠️ ALIASED: `site` is the parameter name throughout the site-handle
	// migration, and an unaliased import of this package shadows it.
	sitepkg "github.com/vdibart/polis-cli/cli-go/pkg/site"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
	// Aliased: "version" collides with the many local `version := fm[...]` vars.
	verhist "github.com/vdibart/polis-cli/cli-go/pkg/version"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
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

// commentDirKey is the key of the directory holding comments of a status.
// Private statuses (drafts, pending, denied) live under
// .polis/bundles/pub.polis.core/comments/<status>; blessed comments under
// content/pub.polis.core/comment/ (with YYYYMMDD date directories).
func commentDirKey(status string) string {
	if status == StatusBlessed {
		return "content/pub.polis.core/comment"
	}
	return ".polis/bundles/pub.polis.core/comments/" + status
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
func SaveDraft(site storage.Site, draft *CommentDraft) error {
	// Normalize URLs to .md format (defense-in-depth)
	draft.InReplyTo = polisurl.NormalizeToMD(draft.InReplyTo)
	draft.RootPost = polisurl.NormalizeToMD(draft.RootPost)

	draftsDir := ".polis/bundles/pub.polis.core/comments/" + StatusDrafts
	// ⛔ MkdirAllPrivate wants a PATH — it is directories and modes, which the
	// store has no verb for — so the key is joined onto the root here. ⚠️ It
	// stays for a reason learned the hard way: an explicit 0700 removed because "the store
	// handles it" shipped a world-readable .polis once already.
	if err := sitepkg.MkdirAllPrivate(site, site.Path(draftsDir)); err != nil {
		return fmt.Errorf("failed to create drafts directory: %w", err)
	}

	if draft.ID == "" {
		draft.ID = GenerateCommentID(draft.InReplyTo, time.Now().UTC())
	} else if err := lookupCommentID(draft.ID); err != nil {
		// Refused BEFORE the uniqueness probe below, which stats the id as a path.
		return err
	}
	draft.ID = ensureUniqueCommentID(site, draft.ID)

	if draft.CreatedAt == "" {
		draft.CreatedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	draft.UpdatedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// If no root_post specified, use in_reply_to as root
	if draft.RootPost == "" {
		draft.RootPost = draft.InReplyTo
	}

	// Save as markdown with simple frontmatter
	content := DraftContent(draft)

	// ⛔ The ID becomes a path on the next line; see id_safety.go.
	if err := validCommentID(draft.ID); err != nil {
		return err
	}
	draftKey := draftsDir + "/" + draft.ID + ".md"
	if err := site.Store().Put(context.Background(), draftKey, []byte(content)); err != nil {
		return fmt.Errorf("failed to save draft: %w", err)
	}

	return nil
}

// LoadDraft loads a comment draft by ID.
func LoadDraft(site storage.Site, id string) (*CommentDraft, error) {
	if err := lookupCommentID(id); err != nil {
		return nil, err
	}
	draftKey := ".polis/bundles/pub.polis.core/comments/" + StatusDrafts + "/" + id + ".md"
	data, err := site.Store().Get(context.Background(), draftKey)
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
func ListDrafts(site storage.Site) ([]*CommentDraft, error) {
	draftsDir := ".polis/bundles/pub.polis.core/comments/" + StatusDrafts
	entries, err := site.Store().List(context.Background(), draftsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []*CommentDraft{}, nil
		}
		return nil, fmt.Errorf("failed to read drafts directory: %w", err)
	}

	var drafts []*CommentDraft
	for _, entry := range entries {
		if entry.IsDir || !strings.HasSuffix(pathpkg.Base(entry.Key), ".md") {
			continue
		}
		id := strings.TrimSuffix(pathpkg.Base(entry.Key), ".md")
		draft, err := LoadDraft(site, id)
		if err != nil {
			continue // Skip invalid drafts
		}
		drafts = append(drafts, draft)
	}

	return drafts, nil
}

// DeleteDraft removes a comment draft.
func DeleteDraft(site storage.Site, id string) error {
	if err := lookupCommentID(id); err != nil {
		return err
	}
	// ⭐ One store, addressed by key. This used to build a SECOND store rooted
	// at the file's own directory and delete by base name, because it had only
	// a path to work from.
	draftKey := ".polis/bundles/pub.polis.core/comments/" + StatusDrafts + "/" + id + ".md"
	if err := site.Store().Delete(context.Background(), draftKey); err != nil {
		return fmt.Errorf("failed to delete draft: %w", err)
	}
	return nil
}

// SignComment signs a draft and moves it to pending status.
// Uses CLI-compatible frontmatter format with nested in-reply-to structure.
// authorIdentity is the domain (e.g. "alice.polis.pub") written to the author frontmatter field.
// For backward compatibility, email addresses are also accepted.
func SignComment(site storage.Site, draft *CommentDraft, authorIdentity, siteURL string, privateKey []byte, generator ...string) (*SignedComment, error) {
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
	} else if err := lookupCommentID(commentID); err != nil {
		// Refused BEFORE the uniqueness probe below, which stats the id as a path.
		return nil, err
	}
	commentID = ensureUniqueCommentID(site, commentID)
	dateDir := timestamp.Format("20060102")
	// Canonical source-path URL (content/pub.polis.core/comment/...), matching
	// where PublishComment writes the signed .md — so the comment self-declares
	// the same URL it is registered + served at. Comments historically used the
	// /comments/ mount path, which reaches the .md only through a permanent 301;
	// the registered URL is signature-bound and the DS's upsert key.
	commentURL := polisurl.CommentContentURL(siteURL, dateDir, commentID)

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

	// A comment carries the COMMENTER's terms, because a comment is the
	// commenter's content on the commenter's domain. That falls out of the
	// architecture; there is no special case here beyond reading this site's
	// own licence rather than the post author's.
	licenseYAML, err := commentLicenseFrontmatter(site, siteURL)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve licence terms: %w", err)
	}

	// Build CLI-compatible frontmatter (without signature first)
	// CLI format uses nested in-reply-to with url and root-post
	//
	// ⚠️ licenseYAML is spliced into BOTH this and the final frontmatter below.
	// The two differ only by the `author:` and `signature:` lines, which are
	// exactly what signing.MarkdownSigningBase deletes to recover the signed bytes. Build the
	// block once or signatures break silently.
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
  - sha256:%s (%s)%s
---`,
		escapeYAMLTitle(title),
		timestampStr,
		gen,
		draft.InReplyTo,
		rootPost,
		hash,
		hash,
		timestampStr,
		licenseYAML,
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
  - sha256:%s (%s)%s
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
		licenseYAML,
		sigBase64,
	)

	finalContent := finalFrontmatter + "\n\n" + content

	// Create pending directory (private). ⛔ MkdirAllPrivate wants a path: it
	// is directories and modes, which the store has no verb for, and the
	// explicit 0700 stays for the reason given in SaveDraft.
	pendingDir := commentDirKey(StatusPending)
	if err := sitepkg.MkdirAllPrivate(site, site.Path(pendingDir)); err != nil {
		return nil, fmt.Errorf("failed to create pending directory: %w", err)
	}

	// Write to pending
	if err := validCommentID(commentID); err != nil {
		return nil, err
	}
	if err := site.Store().Put(context.Background(), pendingDir+"/"+commentID+".md", []byte(finalContent)); err != nil {
		return nil, fmt.Errorf("failed to write pending comment: %w", err)
	}

	// Delete draft
	if draft.ID != "" {
		_ = DeleteDraft(site, draft.ID)
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
func MoveComment(site storage.Site, commentID, fromStatus, toStatus string) error {
	if err := lookupCommentID(commentID); err != nil {
		return err
	}
	if err := knownStatus(fromStatus); err != nil {
		return err
	}
	if err := knownStatus(toStatus); err != nil {
		return err
	}
	// Determine source path
	var fromKey string
	if fromStatus == StatusBlessed {
		// Need to find the blessed comment in date directories
		found, foundKey := findBlessedComment(site, commentID)
		if !found {
			return fmt.Errorf("comment not found: %s", commentID)
		}
		fromKey = foundKey
	} else {
		fromKey = ".polis/bundles/pub.polis.core/comments/" + fromStatus + "/" + commentID + ".md"
	}

	// Read source file
	data, err := site.Store().Get(context.Background(), fromKey)
	if err != nil {
		return fmt.Errorf("failed to read comment: %w", err)
	}

	content := string(data)
	fm := ParseFrontmatter(content)

	// Determine destination path
	var toKey string
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
		toKey = "content/pub.polis.core/comment/" + dateDir + "/" + commentID + ".md"
		relativePath = toKey
	} else {
		// ⛔ MkdirAllPrivate STAYS, and this is deliberate. The store's Put
		// does create .polis parents privately — that is what At() guarantees
		// and two tests assert it — but this is the directory that holds a
		// tenant's unpublished comments, and the lesson of the append conversion was that an
		// explicit 0700 removed "because the store handles it" shipped a
		// world-readable .polis with nothing at any layer catching it. The
		// call is idempotent and cheap; the property is not recoverable.
		toDir := site.Path(".polis", "bundles", "pub.polis.core", "comments", toStatus)
		if err := sitepkg.MkdirAllPrivate(site, toDir); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
		toKey = ".polis/bundles/pub.polis.core/comments/" + toStatus + "/" + commentID + ".md"
	}

	// Write to destination
	if err := site.Store().Put(context.Background(), toKey, data); err != nil {
		return fmt.Errorf("failed to write comment: %w", err)
	}

	// Remove source. ⭐ One store, addressed by key — this used to build a
	// second store rooted at the source file's own DIRECTORY and delete by
	// base name, because it had only a path to work from.
	if err := site.Store().Delete(context.Background(), fromKey); err != nil {
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
			site,
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
		if err := metadata.AddBlessedComment(site, postPath, metadata.BlessedComment{
			URL:     commentURL,
			Version: version,
		}); err != nil {
			// Log but don't fail
			_ = err
		}

		// Update manifest comment count
		if err := publish.UpdateManifest(site); err != nil {
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
func PublishComment(site storage.Site, commentID string) error {
	if err := lookupCommentID(commentID); err != nil {
		return err
	}
	// Read the pending comment
	pendingKey := ".polis/bundles/pub.polis.core/comments/" + StatusPending + "/" + commentID + ".md"
	data, err := site.Store().Get(context.Background(), pendingKey)
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

	// The public key: content/pub.polis.core/comment/YYYYMMDD/<id>.md. It is
	// also the index path.
	relativePath := commentDirKey(StatusBlessed) + "/" + dateDir + "/" + commentID + ".md"

	// Copy markdown file to public location
	if err := site.Store().Put(context.Background(), relativePath, data); err != nil {
		return fmt.Errorf("failed to write public comment: %w", err)
	}

	// Note: HTML rendering is handled by RenderSite() which is called after
	// PublishComment(). Writing raw HTML here would produce untemplated output
	// that gets overwritten anyway.

	// Seed the version history side-car (only if absent, never clobber) so a
	// later RepublishComment can diff against this base version. The base body is
	// the canonicalized content without frontmatter — identical to what republish
	// recomputes as the "old" content for its diff.
	versionsKey := verhist.VersionsKey(relativePath, ".versions")
	if _, statErr := site.Store().Stat(context.Background(), versionsKey); errors.Is(statErr, fs.ErrNotExist) {
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
			if err := verhist.InitializeHistory(site, versionsKey, relativePath, baseBody, baseHash, basePublished); err != nil {
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
		site,
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
	if err := publish.UpdateManifest(site); err != nil {
		_ = err
	}

	return nil
}

// FindBlessedComment searches for a blessed comment in the date-based directory structure.
// Structure: content/pub.polis.core/comment/YYYYMMDD/comment-id.md
// Returns (found, key): the site-relative key, never an absolute path.
func FindBlessedComment(site storage.Site, commentID string) (bool, string) {
	return findBlessedComment(site, commentID)
}

// findBlessedComment is the internal implementation.
// findBlessedComment reports whether a blessed comment exists, and its KEY.
//
// ⭐ A KEY, NOT A PATH: its one caller used the result to read and then delete
// the file, and both go through the store now. ⚠️ The traversal guard stays
// first — a traversing id is simply not a comment of this site, and that must
// be decided before anything is joined.
func findBlessedComment(site storage.Site, commentID string) (bool, string) {
	if lookupCommentID(commentID) != nil {
		return false, ""
	}
	const commentsKey = "content/pub.polis.core/comment"

	// Walk through date directories (YYYYMMDD format)
	ctx := context.Background()
	dateDirs, err := site.Store().List(ctx, commentsKey)
	if err != nil {
		return false, ""
	}

	for _, dateDir := range dateDirs {
		if !dateDir.IsDir {
			continue
		}
		key := commentsKey + "/" + pathpkg.Base(dateDir.Key) + "/" + commentID + ".md"
		if _, err := site.Store().Stat(ctx, key); err == nil {
			return true, key
		}
	}

	return false, ""
}

// ListComments returns comments with a specific status.
// For blessed status, searches the date-based public comments directory.
// For other statuses, searches the private .polis/comments/<status>/ directory.
// Parses CLI-compatible frontmatter format.
func ListComments(site storage.Site, status string) ([]*CommentMeta, error) {
	if err := knownStatus(status); err != nil {
		return nil, err
	}
	if status == StatusBlessed {
		return listBlessedComments(site)
	}

	commentsDir := ".polis/bundles/pub.polis.core/comments/" + status
	entries, err := site.Store().List(context.Background(), commentsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []*CommentMeta{}, nil
		}
		return nil, fmt.Errorf("failed to read comments directory: %w", err)
	}

	var comments []*CommentMeta
	for _, entry := range entries {
		if entry.IsDir || !strings.HasSuffix(pathpkg.Base(entry.Key), ".md") {
			continue
		}

		commentKey := commentsDir + "/" + pathpkg.Base(entry.Key)
		data, err := site.Store().Get(context.Background(), commentKey)
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
			ID:             strings.TrimSuffix(pathpkg.Base(entry.Key), ".md"),
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
func listBlessedComments(site storage.Site) ([]*CommentMeta, error) {
	var comments []*CommentMeta
	commentsDir := "content/pub.polis.core/comment"

	// Walk through date directories (YYYYMMDD format)
	dateDirs, err := site.Store().List(context.Background(), commentsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []*CommentMeta{}, nil
		}
		return nil, fmt.Errorf("failed to read comments directory: %w", err)
	}

	for _, dateDir := range dateDirs {
		if !dateDir.IsDir {
			continue
		}
		datePath := commentsDir + "/" + pathpkg.Base(dateDir.Key)

		// List comment files in this date directory
		files, err := site.Store().List(context.Background(), datePath)
		if err != nil {
			continue
		}

		for _, file := range files {
			if file.IsDir || !strings.HasSuffix(pathpkg.Base(file.Key), ".md") {
				continue
			}

			// Skip comments that still exist in pending (published-but-pending, not yet blessed)
			pendingKey := commentDirKey(StatusPending) + "/" + pathpkg.Base(file.Key)
			if _, err := site.Store().Stat(context.Background(), pendingKey); err == nil {
				continue
			}

			data, err := site.Store().Get(context.Background(), file.Key)
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
				ID:             strings.TrimSuffix(pathpkg.Base(file.Key), ".md"),
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
func GetComment(site storage.Site, commentID, status string) (*SignedComment, error) {
	if err := lookupCommentID(commentID); err != nil {
		return nil, err
	}
	if err := knownStatus(status); err != nil {
		return nil, err
	}
	var commentKey string

	if status == StatusBlessed {
		found, foundKey := findBlessedComment(site, commentID)
		if !found {
			return nil, fmt.Errorf("comment not found: %s", commentID)
		}
		commentKey = foundKey
	} else {
		commentKey = ".polis/bundles/pub.polis.core/comments/" + status + "/" + commentID + ".md"
	}

	data, err := site.Store().Get(context.Background(), commentKey)
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

// escapeYAMLTitle escapes a title for YAML frontmatter: only quotes when truly
// necessary (colons, newlines, special chars).
//
// ⛔ "CLI-compatible" used to stand here and was wrong: bash writes titles raw.
// What this MUST stay compatible with is publish.escapeYAMLString — the rules
// are identical and unquoteYAMLTitle relies on that. TestTheTwoEscapersAgree
// holds it; do not change one without the other.
func escapeYAMLTitle(s string) string {
	needsQuoting := false
	// ANY leading or trailing whitespace, not only a space: a tab-led title
	// is reachable (`# <tab>Title` survives ExtractTitle), and the frontmatter
	// parser trims an unquoted value, so an unquoted tab was lost.
	if s != strings.TrimSpace(s) {
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

// ReplyFields returns the target and thread root a comment file declares,
// reading the signed nested `in-reply-to:` block and falling back to the flat
// draft fields. Both are read from the frontmatter block only.
func ReplyFields(content string) (inReplyTo, rootPost string) {
	inReplyTo, rootPost = ParseNestedInReplyTo(content)
	if inReplyTo == "" || rootPost == "" {
		fm := ParseFrontmatter(content)
		if inReplyTo == "" {
			inReplyTo = fm["in_reply_to"]
		}
		if rootPost == "" {
			rootPost = fm["root_post"]
		}
	}
	if rootPost == "" {
		rootPost = inReplyTo
	}
	return inReplyTo, rootPost
}

// UnpublishedDraftContent builds the draft an unpublished comment returns to.
//
// ⛔ It must be the format SaveDraft writes and LoadDraft reads, because the
// only thing a returned draft is FOR is being signed again. Until close-out E2
// the reply fields were written as an HTML comment that nothing read (and, for
// a signed comment, were never even found: the nested block is invisible to a
// flat frontmatter parse), so `polis comment sign` refused every unpublished
// comment with "in_reply_to is required" while the dialog promised a republish.
//
// Everything published is dropped: signature, version and history. A republish
// is a fresh publication, not a restoration.
//
// published is the comment file's full text, title its raw frontmatter title,
// and body the frontmatter-stripped body — with whatever heading it carries.
//
// ⛔ The unquote and the heading strip belong HERE, in the one builder, and in
// this order. A comment's frontmatter title is YAML-quoted and its body heading
// is not (`escapeYAMLTitle` quotes anything containing ": ", and every
// auto-generated title is `Re: <slug>`), so a caller that stripped with the raw
// title matched nothing and this function then prepended a second identical
// heading — one more per publish → unpublish cycle (close-out R2-1).
func UnpublishedDraftContent(published, title, body string) string {
	inReplyTo, rootPost := ReplyFields(published)
	fm := ParseFrontmatter(published)

	title = unquoteYAMLTitle(title)
	body = render.StripLeadingTitleHeading(body, title)

	createdAt := fm["published"]
	if createdAt == "" {
		createdAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	draft := &CommentDraft{
		InReplyTo: inReplyTo,
		RootPost:  rootPost,
		CreatedAt: createdAt,
		UpdatedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Content:   "# " + title + "\n\n" + body,
	}
	return DraftContent(draft)
}

// DraftContent renders a draft file's text. SaveDraft writes exactly this.
func DraftContent(draft *CommentDraft) string {
	return fmt.Sprintf(`---
in_reply_to: %s
root_post: %s
created_at: %s
updated_at: %s
---

%s`, draft.InReplyTo, draft.RootPost, draft.CreatedAt, draft.UpdatedAt, draft.Content)
}

// unquoteYAMLTitle reverses escapeYAMLTitle, so a title that needed quoting in
// frontmatter does not come back with the quotes as part of the title.
//
// ⭐ One implementation, shared with the post branches of both unpublish paths:
// escapeYAMLTitle and publish.escapeYAMLString apply the same rule, so their
// inverse is the same function (close-out R3-1). ⛔ That equivalence is the
// whole basis for sharing an inverse, so TestTheTwoEscapersAgree pins it — a
// comment would let one escaper drift while this stayed green.
func unquoteYAMLTitle(s string) string {
	return publish.UnquoteYAMLString(strings.TrimSpace(s))
}

// ensureUniqueCommentID checks for comment ID collisions across all status directories.
// Appends -2, -3, etc. if a collision is found.
func ensureUniqueCommentID(site storage.Site, commentID string) string {
	candidate := commentID
	suffix := 2
	for {
		collision := false
		// Check all private status dirs
		for _, status := range []string{StatusDrafts, StatusPending, StatusDenied} {
			key := commentDirKey(status) + "/" + candidate + ".md"
			if _, err := site.Store().Stat(context.Background(), key); err == nil {
				collision = true
				break
			}
		}
		// Check blessed (public) comments date dirs
		if !collision {
			commentsDir := "content/pub.polis.core/comment"
			if dateDirs, err := site.Store().List(context.Background(), commentsDir); err == nil {
				for _, dd := range dateDirs {
					if dd.IsDir {
						key := commentsDir + "/" + pathpkg.Base(dd.Key) + "/" + candidate + ".md"
						if _, err := site.Store().Stat(context.Background(), key); err == nil {
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
	// ⚠️ Anchored to the FRONTMATTER BLOCK, and the key matches at column 0 —
	// the same rule the signing base follows (docs/signet/spec/signing-base.md,
	// §4.2). A document-wide scan
	// read a BODY line beginning `in-reply-to:` as the comment's target, so a
	// comment about replying could name someone else's post.
	trimmedContent := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmedContent, "---") {
		return "", ""
	}
	matches := frontmatterParseRe.FindStringSubmatch(trimmedContent)
	if len(matches) < 2 {
		return "", ""
	}

	lines := strings.Split(matches[1], "\n")
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
