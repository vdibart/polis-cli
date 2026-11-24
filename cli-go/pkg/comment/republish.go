package comment

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	verhist "github.com/vdibart/polis-cli/cli-go/pkg/version"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// RepublishResult describes the outcome of a comment republish.
type RepublishResult struct {
	CommentID     string `json:"comment_id"`
	Path          string `json:"path"`    // content-relative path
	Version       string `json:"version"` // sha256:<hash> of the new version
	Signature     string `json:"signature"`
	BlessingState string `json:"blessing_state"` // pending | blessed | denied
	Rebeseeched   bool   `json:"rebeseeched"`    // pending comment was re-sent for blessing
	Deferred      bool   `json:"deferred"`       // DS contact skipped (site not registered)
	RegisterError string `json:"register_error,omitempty"`
}

// RepublishComment produces a new signed version of an already-published comment,
// mirroring publish.RepublishPost. It:
//
//   - preserves the original `published` timestamp (so the comment URL and the DS
//     update-detection stay stable) and records the edit time in `updated`;
//   - appends the new version to the .versions/ side-car (lazy-initializing a base
//     from the prior content for comments published before side-cars existed);
//   - advances only `current_version` in index.jsonl (path/in_reply_to untouched);
//   - re-registers the content with the DS, which emits pub.polis.comment.republished
//     and preserves granted/denied blessings (blessed carries forward, denied stays
//     denied), re-evaluating only still-pending comments.
//
// blessed.json is never touched here: the blessed version stays pinned to the hash
// that was blessed, so divergence from index.jsonl's current_version is the
// "edited since blessing" signal (see IsEditedSinceBlessing).
//
// commentPath is the content-relative path to the comment markdown file
// (content/pub.polis.core/comment/YYYYMMDD/<id>.md). markdown is the new body with
// frontmatter already stripped. DS registration failures are non-fatal — the local
// republish still succeeds — matching RepublishPost.
func RepublishComment(site storage.Site, commentPath, markdown string, privateKey []byte, dsCfg ...*DiscoveryConfig) (*RepublishResult, error) {
	return RepublishCommentAs(site, commentPath, markdown, privateKey, discovery.AgentMarker{}, dsCfg...)
}

// RepublishCommentAs is RepublishComment for an act made by a user agent: the
// marker goes inside the new version's signature. The zero marker is the
// owner's own edit.
func RepublishCommentAs(site storage.Site, commentPath, markdown string, privateKey []byte, marker discovery.AgentMarker, dsCfg ...*DiscoveryConfig) (*RepublishResult, error) {
	var cfg *DiscoveryConfig
	if len(dsCfg) > 0 {
		cfg = dsCfg[0]
	}
	gen := GetGenerator()
	if cfg != nil && cfg.Generator != "" {
		gen = cfg.Generator
	}

	existing, err := site.Store().Get(context.Background(), commentPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read comment: %w", err)
	}
	existingStr := string(existing)
	fm := ParseFrontmatter(existingStr)

	// Preserve published (required for stable URL); record edit time separately.
	published := fm["published"]
	if published == "" {
		published = fm["timestamp"]
	}
	if published == "" {
		return nil, fmt.Errorf("comment is missing a published timestamp; cannot republish")
	}
	author := fm["author"]

	inReplyToURL, rootPost := ParseNestedInReplyTo(existingStr)
	if inReplyToURL == "" {
		inReplyToURL = fm["in_reply_to"]
	}
	if rootPost == "" {
		rootPost = fm["root_post"]
	}
	if rootPost == "" {
		rootPost = inReplyToURL
	}

	oldCurrentVersion := fm["current-version"]
	if oldCurrentVersion == "" {
		oldCurrentVersion = fm["comment_version"]
	}
	oldHash := strings.TrimPrefix(oldCurrentVersion, "sha256:")
	oldBody := CanonicalizeContent(StripFrontmatter(existingStr))

	// New version.
	newBody := CanonicalizeContent(markdown)
	newHash := HashContent([]byte(newBody))
	updateTimestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Re-extract the title from the new content (mirrors SignComment), falling
	// back to the "Re: <slug>" form. Re-extracting (rather than reusing fm["title"])
	// avoids double-escaping the already-quoted frontmatter title.
	title := extractTitleFromContent(markdown)
	if title == "" {
		title = fmt.Sprintf("Re: %s", extractSlugFromURL(inReplyToURL))
	}

	// Append the new version to the history list.
	versionHistory := publish.ExtractVersionHistory(existingStr)
	versionHistory = append(versionHistory, fmt.Sprintf("sha256:%s (%s)", newHash, updateTimestamp))
	var vhYAML string
	for _, v := range versionHistory {
		vhYAML += fmt.Sprintf("\n  - %s", v)
	}
	// The new version keeps the terms the comment was published under, copied
	// byte for byte from the version being replaced: terms are not retroactive,
	// so an edit neither drops them nor re-resolves them to the site's current
	// terms. A comment published with none gets none. It sits where SignComment
	// puts it, after the version history.
	licenseYAML := ""
	if block := license.ExtractBlock(existingStr); block != "" {
		licenseYAML = "\n" + block
	}
	// ⛔ The licence block and the marker follow the version history in BOTH
	// frontmatters below, so they are inside the signature. Built once.
	markerYAML, err := marker.FrontmatterYAML()
	if err != nil {
		return nil, err
	}
	vhYAML += licenseYAML + markerYAML

	commentID := strings.TrimSuffix(filepath.Base(commentPath), ".md")

	unsignedFrontmatter := fmt.Sprintf(`---
title: %s
type: comment
published: %s
updated: %s
generator: %s
in-reply-to:
  url: %s
  root-post: %s
current-version: sha256:%s
version-history:%s
---`,
		escapeYAMLTitle(title), published, updateTimestamp, gen,
		inReplyToURL, rootPost, newHash, vhYAML,
	)

	canonicalizedForSigning := CanonicalizeContent(unsignedFrontmatter + "\n\n" + newBody)
	signature, err := signing.SignContent([]byte(canonicalizedForSigning), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign comment: %w", err)
	}
	sigBase64 := extractSignatureBase64(signature)

	finalFrontmatter := fmt.Sprintf(`---
title: %s
type: comment
published: %s
updated: %s
author: %s
generator: %s
in-reply-to:
  url: %s
  root-post: %s
current-version: sha256:%s
version-history:%s
signature: %s
---`,
		escapeYAMLTitle(title), published, updateTimestamp, author, gen,
		inReplyToURL, rootPost, newHash, vhYAML, sigBase64,
	)
	finalContent := finalFrontmatter + "\n\n" + newBody

	// Overwrite the public canonical file.
	if err := site.Store().Put(context.Background(), commentPath, []byte(finalContent)); err != nil {
		return nil, fmt.Errorf("failed to write comment: %w", err)
	}

	// Update the version history side-car. Lazy-init a base from the prior content
	// for comments published before side-cars existed, so the blessed/prior version
	// remains reconstructable.
	versionsKey := verhist.VersionsKey(commentPath, ".versions")
	if _, statErr := site.Store().Stat(context.Background(), versionsKey); errors.Is(statErr, fs.ErrNotExist) && oldHash != "" {
		if err := verhist.InitializeHistory(site, versionsKey, commentPath, oldBody, oldHash, published); err != nil {
			_ = err // non-fatal
		}
	}
	if err := verhist.AppendHistory(site, versionsKey, commentPath, oldHash, newHash, updateTimestamp, oldBody, newBody); err != nil {
		_ = err // non-fatal; version history is best-effort
	}

	// Keep the private pending/denied copy in sync so a later MoveComment doesn't
	// resurrect stale content.
	syncPrivateCommentCopy(site, commentID, finalContent)

	// Advance current_version in the index (upsert by path preserves type +
	// in_reply_to). blessed.json is intentionally left untouched.
	if err := metadata.AppendCommentToIndex(site, commentPath, title, published, "sha256:"+newHash, inReplyToURL); err != nil {
		_ = err // non-fatal
	}

	state := localBlessingState(site, commentID)

	result := &RepublishResult{
		CommentID:     commentID,
		Path:          commentPath,
		Version:       "sha256:" + newHash,
		Signature:     signature,
		BlessingState: state,
	}

	// Re-register with the DS (non-fatal). This emits pub.polis.comment.republished
	// and lets the DS preserve granted/denied state.
	dsURL, base := resolveDiscovery(cfg)
	if dsURL == "" || base == "" {
		return result, nil // DS not configured — local-only republish
	}
	if !discovery.IsRegisteredLocally(site, dsURL) {
		result.Deferred = true
		return result, nil
	}

	meta := &CommentMeta{
		ID:             commentID,
		CommentVersion: "sha256:" + newHash,
		InReplyTo:      inReplyToURL,
		RootPost:       rootPost,
		Author:         author,
		Timestamp:      published,
	}
	// Republish always re-registers + re-evaluates blessing (beseech=true),
	// preserving the historical behavior.
	resp, err := registerCommentContent(site, meta, commentID, privateKey, dsURL, base, true, marker)
	if err != nil {
		result.RegisterError = err.Error()
		return result, nil // non-fatal, mirroring RepublishPost
	}

	// A still-pending comment that the DS now grants transitions to blessed (the
	// new version is what gets blessed). Blessed/denied comments are left as-is.
	if state == StatusPending {
		result.Rebeseeched = true
		if resp.RelationshipStatus == "granted" {
			if err := MoveComment(site, commentID, StatusPending, StatusBlessed); err == nil {
				result.BlessingState = StatusBlessed
			}
		}
	}

	return result, nil
}

// IsEditedSinceBlessing reports whether a blessed comment's live version has
// diverged from the version that was blessed — i.e. it was republished after
// being blessed. It compares the comment's live current-version (frontmatter)
// against the version pinned in blessed.json (which RepublishComment deliberately
// leaves untouched). Returns false for comments that are not blessed, since there
// is no blessed version to diverge from.
//
// This is the single source of truth for the "edited since blessing" UI signal;
// callers (webapp, themes, stream renderer) should use it rather than
// reimplementing the comparison. commentPath is the content-relative path.
func IsEditedSinceBlessing(site storage.Site, commentPath string) (bool, error) {
	data, err := site.Store().Get(context.Background(), commentPath)
	if err != nil {
		return false, fmt.Errorf("read comment: %w", err)
	}
	fm := ParseFrontmatter(string(data))
	current := fm["current-version"]
	if current == "" {
		current = fm["comment_version"]
	}

	blessed, err := metadata.LoadBlessedComments(site)
	if err != nil {
		// No (or unreadable) blessed.json => treat as not blessed.
		return false, nil
	}

	want := commentKey(commentPath)
	for _, pc := range blessed.Comments {
		for _, bc := range pc.Blessed {
			if commentKey(bc.URL) != want {
				continue
			}
			if bc.Version == "" {
				return false, nil // blessed but version not tracked — can't tell
			}
			return bc.Version != current, nil
		}
	}
	return false, nil // not blessed
}

// commentKey reduces a comment URL or content path to the shared "YYYYMMDD/id"
// identity so a blessed.json URL and an index/content path can be matched. The
// extension is dropped so .md/.html variants compare equal.
func commentKey(urlOrPath string) string {
	s := urlOrPath
	if i := strings.Index(s, "/comments/"); i >= 0 {
		s = s[i+len("/comments/"):]
	} else if i := strings.Index(s, "/comment/"); i >= 0 {
		s = s[i+len("/comment/"):]
	}
	s = strings.TrimSuffix(s, ".md")
	s = strings.TrimSuffix(s, ".html")
	return s
}

// localBlessingState infers a comment's blessing state from its on-disk location:
// a private pending/ or denied/ copy wins; otherwise the comment is treated as
// blessed (or published-but-unregistered, which behaves the same for republish).
func localBlessingState(site storage.Site, commentID string) string {
	if lookupCommentID(commentID) != nil {
		return StatusBlessed
	}
	pendingKey := ".polis/bundles/pub.polis.core/comments/" + StatusPending + "/" + commentID + ".md"
	if _, err := site.Store().Stat(context.Background(), pendingKey); err == nil {
		return StatusPending
	}
	deniedKey := ".polis/bundles/pub.polis.core/comments/" + StatusDenied + "/" + commentID + ".md"
	if _, err := site.Store().Stat(context.Background(), deniedKey); err == nil {
		return StatusDenied
	}
	return StatusBlessed
}

// syncPrivateCommentCopy rewrites the private pending/ or denied/ copy (whichever
// exists) with the republished content, keeping it consistent with the public file.
func syncPrivateCommentCopy(site storage.Site, commentID, content string) {
	if lookupCommentID(commentID) != nil {
		return
	}
	for _, status := range []string{StatusPending, StatusDenied} {
		pKey := ".polis/bundles/pub.polis.core/comments/" + status + "/" + commentID + ".md"
		if _, err := site.Store().Stat(context.Background(), pKey); err == nil {
			_ = site.Store().Put(context.Background(), pKey, []byte(content))
		}
	}
}

// resolveDiscovery resolves DS settings from the per-call config or the package
// globals, mirroring BeseechComment.
func resolveDiscovery(cfg *DiscoveryConfig) (dsURL, baseURL string) {
	if cfg != nil {
		return cfg.DiscoveryURL, cfg.BaseURL
	}
	return DiscoveryURL, BaseURL
}
