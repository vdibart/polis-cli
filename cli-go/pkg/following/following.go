// Package following manages the following.json file for tracking followed authors.
package following

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// normalizeFollowURL ensures consistent URL comparison by lowercasing and trimming trailing slashes.
func normalizeFollowURL(u string) string {
	return strings.TrimRight(strings.ToLower(u), "/")
}

// Version is set at init time by cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// FollowingFile represents the following.json structure.
type FollowingFile struct {
	Version   string           `json:"version"`
	Following []FollowingEntry `json:"following"`
}

// FollowingEntry represents a single followed author.
type FollowingEntry struct {
	URL        string `json:"url"`
	AddedAt    string `json:"added_at"`
	SiteTitle  string `json:"site_title,omitempty"`
	AuthorName string `json:"author_name,omitempty"`
}

// DefaultPath returns the default path to following.json.
func DefaultPath(dataDir string) string {
	return filepath.Join(dataDir, "content", "pub.polis.core", "follow", "following.json")
}

// Load loads the following.json file from the given path.
func Load(path string) (*FollowingFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &FollowingFile{
				Following: []FollowingEntry{},
			}, nil
		}
		return nil, fmt.Errorf("failed to read following.json: %w", err)
	}

	var f FollowingFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse following.json: %w", err)
	}

	return &f, nil
}

// Save saves the following.json file to the given path.
func Save(path string, f *FollowingFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal following.json: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("failed to write following.json: %w", err)
	}

	return nil
}

// Add adds an author to the following list.
// URL comparison ignores trailing slashes.
func (f *FollowingFile) Add(authorURL string) bool {
	norm := normalizeFollowURL(authorURL)
	for _, entry := range f.Following {
		if normalizeFollowURL(entry.URL) == norm {
			return false // Already following
		}
	}

	f.Following = append(f.Following, FollowingEntry{
		URL:     authorURL,
		AddedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	})

	return true
}

// Remove removes an author from the following list.
// Removes ALL matching entries (handles legacy duplicates).
// URL comparison ignores trailing slashes.
func (f *FollowingFile) Remove(authorURL string) bool {
	norm := normalizeFollowURL(authorURL)
	found := false
	filtered := f.Following[:0]
	for _, entry := range f.Following {
		if normalizeFollowURL(entry.URL) == norm {
			found = true
		} else {
			filtered = append(filtered, entry)
		}
	}
	f.Following = filtered
	return found
}

// IsFollowing checks if an author is in the following list.
// URL comparison ignores trailing slashes.
func (f *FollowingFile) IsFollowing(authorURL string) bool {
	norm := normalizeFollowURL(authorURL)
	for _, entry := range f.Following {
		if normalizeFollowURL(entry.URL) == norm {
			return true
		}
	}
	return false
}

// Get retrieves a following entry by URL.
// URL comparison ignores trailing slashes.
func (f *FollowingFile) Get(authorURL string) *FollowingEntry {
	norm := normalizeFollowURL(authorURL)
	for i := range f.Following {
		if normalizeFollowURL(f.Following[i].URL) == norm {
			return &f.Following[i]
		}
	}
	return nil
}

// UpdateMetadata sets the site title and author name for a matching entry.
// Returns true if the entry was found and updated.
func (f *FollowingFile) UpdateMetadata(url, siteTitle, authorName string) bool {
	entry := f.Get(url)
	if entry == nil {
		return false
	}
	entry.SiteTitle = siteTitle
	entry.AuthorName = authorName
	return true
}

// EntriesMissingMetadata returns entries that have neither site_title nor author_name.
func (f *FollowingFile) EntriesMissingMetadata() []FollowingEntry {
	var missing []FollowingEntry
	for _, e := range f.Following {
		if e.SiteTitle == "" && e.AuthorName == "" {
			missing = append(missing, e)
		}
	}
	return missing
}

// Count returns the number of followed authors.
func (f *FollowingFile) Count() int {
	return len(f.Following)
}

// All returns all following entries.
func (f *FollowingFile) All() []FollowingEntry {
	return f.Following
}
