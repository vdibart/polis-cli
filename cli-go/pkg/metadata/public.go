// Package metadata provides management for polis public metadata files.
package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// PublicIndexFilename is the name of the bundle index file.
	PublicIndexFilename = "index.jsonl"

	// BundleContentDir is the relative path to the core bundle content directory.
	BundleContentDir = "content/pub.polis.core"
)

// IndexEntry represents a single entry in public.jsonl.
// Can be either a post or a comment.
type IndexEntry struct {
	Type           string          `json:"type"`                  // "post" or "comment"
	Path           string          `json:"path"`                  // Relative file path
	Title          string          `json:"title"`                 // Entry title
	Published      string          `json:"published"`             // ISO timestamp
	CurrentVersion string          `json:"current_version"`       // sha256:... hash
	InReplyTo      *InReplyToEntry `json:"in_reply_to,omitempty"` // Only for comments
}

// InReplyToEntry represents the in-reply-to reference in a comment index entry.
type InReplyToEntry struct {
	URL     string `json:"url"`
	Version string `json:"version,omitempty"`
}

// AppendToPublicIndex adds an entry to public.jsonl.
// If an entry with the same Path already exists, it is updated in place.
// Otherwise the entry is appended. Creates the metadata directory and file if they don't exist.
func AppendToPublicIndex(siteDir string, entry *IndexEntry) error {
	// Load existing entries to check for duplicates
	existing, err := LoadPublicIndex(siteDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Check if entry already exists by path
	found := false
	for i, e := range existing {
		if e.Path == entry.Path {
			existing[i] = *entry
			found = true
			break
		}
	}

	if found {
		return writePublicIndex(siteDir, existing)
	}

	// No duplicate - append
	metadataDir := filepath.Join(siteDir, BundleContentDir)
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		return fmt.Errorf("failed to create metadata directory: %w", err)
	}

	indexPath := filepath.Join(metadataDir, PublicIndexFilename)

	jsonLine, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open index file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(jsonLine, '\n')); err != nil {
		return fmt.Errorf("failed to write to index: %w", err)
	}

	return nil
}

// AppendCommentToIndex is a convenience function for appending a comment entry.
func AppendCommentToIndex(siteDir string, path, title, published, currentVersion, inReplyToURL string) error {
	entry := &IndexEntry{
		Type:           "comment",
		Path:           path,
		Title:          title,
		Published:      published,
		CurrentVersion: currentVersion,
		InReplyTo: &InReplyToEntry{
			URL:     inReplyToURL,
			Version: "", // Version is typically not tracked for the target
		},
	}
	return AppendToPublicIndex(siteDir, entry)
}

// AppendPostToIndex is a convenience function for appending a post entry.
func AppendPostToIndex(siteDir string, path, title, published, currentVersion string) error {
	entry := &IndexEntry{
		Type:           "post",
		Path:           path,
		Title:          title,
		Published:      published,
		CurrentVersion: currentVersion,
	}
	return AppendToPublicIndex(siteDir, entry)
}

// LoadPublicIndex reads all entries from public.jsonl.
func LoadPublicIndex(siteDir string) ([]IndexEntry, error) {
	indexPath := filepath.Join(siteDir, BundleContentDir, PublicIndexFilename)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []IndexEntry{}, nil
		}
		return nil, fmt.Errorf("failed to read public.jsonl: %w", err)
	}

	var entries []IndexEntry
	// Split by newlines and parse each line
	lines := splitLines(data)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry IndexEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Skip malformed lines
			continue
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// splitLines splits byte slice by newlines, handling both \n and \r\n.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			end := i
			if end > start && data[end-1] == '\r' {
				end--
			}
			lines = append(lines, data[start:end])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// UpdateIndexEntry updates an existing entry in public.jsonl by path.
// Rewrites the entire file with the updated entry.
func UpdateIndexEntry(siteDir, path, newTitle, newVersion string) error {
	entries, err := LoadPublicIndex(siteDir)
	if err != nil {
		return err
	}

	// Find and update the entry
	found := false
	for i, entry := range entries {
		if entry.Path == path {
			if newTitle != "" {
				entries[i].Title = newTitle
			}
			if newVersion != "" {
				entries[i].CurrentVersion = newVersion
			}
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("entry not found: %s", path)
	}

	// Rewrite the file
	return writePublicIndex(siteDir, entries)
}

// RemoveIndexEntry removes an entry from public.jsonl by path.
func RemoveIndexEntry(siteDir, path string) error {
	entries, err := LoadPublicIndex(siteDir)
	if err != nil {
		return err
	}

	// Filter out the entry
	var filtered []IndexEntry
	for _, entry := range entries {
		if entry.Path != path {
			filtered = append(filtered, entry)
		}
	}

	return writePublicIndex(siteDir, filtered)
}

// writePublicIndex writes all entries to public.jsonl.
func writePublicIndex(siteDir string, entries []IndexEntry) error {
	metadataDir := filepath.Join(siteDir, BundleContentDir)
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		return fmt.Errorf("failed to create metadata directory: %w", err)
	}

	indexPath := filepath.Join(metadataDir, PublicIndexFilename)
	tmpPath := indexPath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	for _, entry := range entries {
		jsonLine, err := json.Marshal(entry)
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("failed to marshal entry: %w", err)
		}
		if _, err := f.Write(append(jsonLine, '\n')); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("failed to write entry: %w", err)
		}
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, indexPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// GetCommentEntries returns all comment entries from public.jsonl.
func GetCommentEntries(siteDir string) ([]IndexEntry, error) {
	entries, err := LoadPublicIndex(siteDir)
	if err != nil {
		return nil, err
	}

	var comments []IndexEntry
	for _, entry := range entries {
		if entry.Type == "comment" {
			comments = append(comments, entry)
		}
	}

	return comments, nil
}

// GetPostEntries returns all post entries from public.jsonl.
func GetPostEntries(siteDir string) ([]IndexEntry, error) {
	entries, err := LoadPublicIndex(siteDir)
	if err != nil {
		return nil, err
	}

	var posts []IndexEntry
	for _, entry := range entries {
		if entry.Type == "post" {
			posts = append(posts, entry)
		}
	}

	return posts, nil
}
