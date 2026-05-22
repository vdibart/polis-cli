// Package metadata provides management for polis public metadata files.
package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

const (
	// BlessedCommentsFilename is the name of the blessed comments index file.
	BlessedCommentsFilename = "blessed.json"
)

// blessedMu serializes load + modify + save of blessed.json across all
// entry points in this package. Without it, AddBlessedComment,
// RemoveBlessedComment, and SaveBlessedComments callers race on the
// shared on-disk file: each reads the same snapshot, modifies its
// copy, and writes back — last writer wins, dropping the other's
// entries. Same shape as R19-1's reply-context cache race; see
// `plans/operational-hardening.md` R22 for the full diagnosis.
//
// All public mutators (Add, Remove, Save) take this lock. Internal
// helpers that already hold it use the saveLocked variant to avoid
// re-entrancy.
var blessedMu sync.Mutex

// BlessedComments represents the blessed-comments.json file structure.
// This file is the public index of comments that the site owner has blessed,
// grouped by the post they're replying to.
type BlessedComments struct {
	Version  string         `json:"version"`
	Comments []PostComments `json:"comments"`
}

// PostComments groups blessed comments for a single post.
type PostComments struct {
	Post    string           `json:"post"`
	Blessed []BlessedComment `json:"blessed"`
}

// BlessedComment represents a single blessed comment entry.
type BlessedComment struct {
	URL       string `json:"url"`
	Version   string `json:"version"`
	BlessedAt string `json:"blessed_at"`
}

// LoadBlessedComments reads the blessed-comments.json file from the metadata directory.
// Returns an error if the file doesn't exist.
//
// Reads are not synchronized against concurrent writers — the file is
// updated atomically via rename in saveBlessedCommentsLocked, so a
// reader observes either the pre-write or post-write contents but
// never a partial write. For load+modify+save callers, use
// AddBlessedComment / RemoveBlessedComment to get the right
// serialization; direct LoadBlessedComments is for read-only paths.
func LoadBlessedComments(siteDir string) (*BlessedComments, error) {
	return loadBlessedCommentsRaw(siteDir)
}

// loadBlessedCommentsRaw is the unsynchronized read path; reused from
// inside the locked Add/Remove flows.
func loadBlessedCommentsRaw(siteDir string) (*BlessedComments, error) {
	filePath := filepath.Join(siteDir, BundleContentDir, "comment", BlessedCommentsFilename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read blessed-comments.json: %w", err)
	}

	var bc BlessedComments
	if err := json.Unmarshal(data, &bc); err != nil {
		return nil, fmt.Errorf("failed to parse blessed-comments.json: %w", err)
	}

	return &bc, nil
}

// SaveBlessedComments writes the blessed-comments.json file atomically.
// It writes to a temporary file first, then renames to ensure atomic update.
//
// Takes blessedMu so callers doing their own load+modify+save sequence
// (e.g. index rebuild) are serialized against AddBlessedComment /
// RemoveBlessedComment. Internal callers that already hold the lock
// use saveBlessedCommentsLocked.
func SaveBlessedComments(siteDir string, bc *BlessedComments) error {
	blessedMu.Lock()
	defer blessedMu.Unlock()
	return saveBlessedCommentsLocked(siteDir, bc)
}

// saveBlessedCommentsLocked is the unsynchronized write path. Caller
// must hold blessedMu.
func saveBlessedCommentsLocked(siteDir string, bc *BlessedComments) error {
	metadataDir := filepath.Join(siteDir, BundleContentDir, "comment")
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		return fmt.Errorf("failed to create metadata directory: %w", err)
	}

	data, err := json.MarshalIndent(bc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal blessed comments: %w", err)
	}

	// Write atomically via temp file. The shared `.tmp` filename is
	// safe under blessedMu — only one goroutine holds it at a time.
	filePath := filepath.Join(metadataDir, BlessedCommentsFilename)
	tmpPath := filePath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath) // Clean up temp file on failure
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// AddBlessedComment adds a comment to the blessed comments index.
// Creates the post entry if it doesn't exist.
//
// Holds blessedMu for the full load+modify+save cycle to prevent
// concurrent callers from losing each other's entries.
func AddBlessedComment(siteDir string, postPath string, comment BlessedComment, generator ...string) error {
	blessedMu.Lock()
	defer blessedMu.Unlock()

	gen := GetGenerator()
	if len(generator) > 0 && generator[0] != "" {
		gen = generator[0]
	}
	// Load current state. loadBlessedCommentsRaw bypasses the mutex
	// since we already hold it.
	bc, err := loadBlessedCommentsRaw(siteDir)
	if err != nil {
		// If file doesn't exist, create new structure
		if errors.Is(err, os.ErrNotExist) {
			bc = &BlessedComments{
				Version:  gen,
				Comments: []PostComments{},
			}
		} else {
			return err
		}
	}

	// Set blessed_at if not provided
	if comment.BlessedAt == "" {
		comment.BlessedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}

	// Find or create post entry
	found := false
	for i, pc := range bc.Comments {
		if pc.Post == postPath {
			// Check if comment already exists (by URL or version)
			for _, existing := range pc.Blessed {
				if existing.URL == comment.URL || (comment.Version != "" && existing.Version == comment.Version) {
					// Already blessed, nothing to do
					return nil
				}
			}
			// Add to existing post entry
			bc.Comments[i].Blessed = append(bc.Comments[i].Blessed, comment)
			found = true
			break
		}
	}

	if !found {
		// Create new post entry
		bc.Comments = append(bc.Comments, PostComments{
			Post:    postPath,
			Blessed: []BlessedComment{comment},
		})
	}

	return saveBlessedCommentsLocked(siteDir, bc)
}

// RemoveBlessedComment removes a comment from the blessed comments index.
// Matches by URL.
//
// Holds blessedMu for the full load+modify+save cycle.
func RemoveBlessedComment(siteDir string, commentURL string) error {
	blessedMu.Lock()
	defer blessedMu.Unlock()

	bc, err := loadBlessedCommentsRaw(siteDir)
	if err != nil {
		return err
	}

	// Find and remove the comment
	for i, pc := range bc.Comments {
		for j, c := range pc.Blessed {
			if c.URL == commentURL {
				// Remove this comment
				bc.Comments[i].Blessed = append(pc.Blessed[:j], pc.Blessed[j+1:]...)

				// If post has no more blessed comments, remove the post entry
				if len(bc.Comments[i].Blessed) == 0 {
					bc.Comments = append(bc.Comments[:i], bc.Comments[i+1:]...)
				}

				return saveBlessedCommentsLocked(siteDir, bc)
			}
		}
	}

	// Comment not found, nothing to do
	return nil
}

// GetBlessedCommentsForPost returns all blessed comments for a specific post.
// Uses flexible path matching: tries exact match, .md/.html swap, and URL-to-path extraction.
func GetBlessedCommentsForPost(siteDir string, postPath string) ([]BlessedComment, error) {
	bc, err := LoadBlessedComments(siteDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []BlessedComment{}, nil
		}
		return nil, err
	}

	for _, pc := range bc.Comments {
		if MatchesPostPath(pc.Post, postPath) {
			return pc.Blessed, nil
		}
	}

	return []BlessedComment{}, nil
}

// MatchesPostPath checks if two post paths refer to the same post.
// Handles exact match, .md/.html extension swaps, full URL vs relative path,
// and source content path (singular /post/) vs mount path (plural /posts/).
func MatchesPostPath(stored, query string) bool {
	if stored == query {
		return true
	}

	// Try .md <-> .html swap
	storedBase := strings.TrimSuffix(strings.TrimSuffix(stored, ".md"), ".html")
	queryBase := strings.TrimSuffix(strings.TrimSuffix(query, ".md"), ".html")
	if storedBase == queryBase {
		return true
	}

	// Try extracting relative path from full URL or source content path
	// e.g., "https://alice.polis.pub/posts/20260101/hello.md" matches "posts/20260101/hello.md"
	// e.g., "content/pub.polis.core/post/20260101/hello.md" matches "posts/20260101/hello.md"
	extractPath := func(s string) string {
		if idx := strings.Index(s, "/posts/"); idx >= 0 {
			return s[idx+1:] // "posts/20260101/hello.md"
		}
		// Also match source content path: .../post/YYYYMMDD/slug.md → posts/YYYYMMDD/slug.md
		if idx := strings.Index(s, "/post/"); idx >= 0 {
			return "posts/" + s[idx+len("/post/"):]
		}
		return s
	}
	storedRel := extractPath(stored)
	queryRel := extractPath(query)
	if storedRel == queryRel {
		return true
	}

	// Compare without extensions after extraction
	storedRelBase := strings.TrimSuffix(strings.TrimSuffix(storedRel, ".md"), ".html")
	queryRelBase := strings.TrimSuffix(strings.TrimSuffix(queryRel, ".md"), ".html")
	return storedRelBase == queryRelBase
}

