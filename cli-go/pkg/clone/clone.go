// Package clone provides functionality to clone remote polis sites.
package clone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
)

// CloneState tracks the state of a cloned site.
type CloneState struct {
	SourceURL    string `json:"source_url"`
	ClonedAt     string `json:"cloned_at"`
	LastUpdated  string `json:"last_updated"`
	PostsCount   int    `json:"posts_count"`
	LastManifest string `json:"last_manifest_hash,omitempty"`
}

// CloneResult contains the results of a clone operation.
type CloneResult struct {
	TargetDir            string `json:"target_dir"`
	PostsDownloaded      int    `json:"posts_downloaded"`
	CommentsDownloaded   int    `json:"comments_downloaded"`
	BlessedCommentsSynced int   `json:"blessed_comments_synced"`
	Errors               int    `json:"errors"`
}

// CloneOptions configures the clone operation.
type CloneOptions struct {
	FullClone bool // If true, re-download everything; if false, only new content
}

// Clone clones a remote polis site to a local directory.
func Clone(serverURL, targetDir string, opts CloneOptions) (*CloneResult, error) {
	client := remote.NewClient()
	result := &CloneResult{TargetDir: targetDir}

	// Normalize server URL
	serverURL = normalizeURL(serverURL)

	// Create target directory
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create target directory: %w", err)
	}

	// Check for existing clone state
	statePath := filepath.Join(targetDir, ".polis-clone-state.json")
	var state *CloneState
	if !opts.FullClone {
		state, _ = loadState(statePath)
	}

	// Fetch .well-known/polis
	wk, err := client.FetchWellKnown(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch .well-known/polis: %w", err)
	}

	// Create .well-known directory and save polis file
	wkDir := filepath.Join(targetDir, ".well-known")
	if err := os.MkdirAll(wkDir, 0755); err != nil {
		return nil, err
	}

	wkData, _ := json.MarshalIndent(wk, "", "  ")
	if err := os.WriteFile(filepath.Join(wkDir, "polis"), append(wkData, '\n'), 0644); err != nil {
		return nil, err
	}

	// Fetch manifest
	manifest, err := client.FetchManifest(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest: %w", err)
	}

	// Create content directory structure
	bundleDir := filepath.Join(targetDir, "content", "pub.polis.core")
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return nil, err
	}

	// Manifest data is now absorbed into .well-known/polis (already written above)
	_ = manifest

	// Fetch public index
	entries, err := client.FetchPublicIndex(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch index.jsonl: %w", err)
	}

	// Save index.jsonl
	indexFile, err := os.Create(filepath.Join(bundleDir, "index.jsonl"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			indexFile.Close()
			return nil, fmt.Errorf("marshal index entry: %w", err)
		}
		if _, err := indexFile.WriteString(string(data) + "\n"); err != nil {
			indexFile.Close()
			return nil, fmt.Errorf("write index entry: %w", err)
		}
	}
	if err := indexFile.Close(); err != nil {
		return nil, fmt.Errorf("close index file: %w", err)
	}

	// Download posts. Modern public indexes (post-bundle-refactor) emit
	// path-based entries with the URL field left empty — the canonical fetch
	// URL is derived as serverURL + "/" + entry.Path. Older indexes that
	// populated URL directly are still supported via the GetPath fallback.
	for _, entry := range entries {
		if entry.Type != "post" {
			continue
		}

		// Check if we should skip (incremental mode)
		if state != nil && !opts.FullClone {
			if entry.Published <= state.LastUpdated {
				continue // Skip already-downloaded posts
			}
		}

		// Resolve content URL + on-disk relative path. Two cases:
		//   - Modern indexes (post-bundle-refactor, e.g. discover.polis.pub):
		//     entry.Path = "content/pub.polis.core/post/.../slug.md",
		//     entry.URL = "". Derive URL from path.
		//   - Legacy indexes: entry.Path = "", entry.URL = absolute URL.
		//     Derive path from URL by stripping serverURL prefix.
		// Either way we land on a relPath rooted at the content tree so the
		// local on-disk layout mirrors the remote's source-content layout.
		relPath := entry.Path
		if relPath == "" {
			relPath = strings.TrimPrefix(entry.URL, serverURL+"/")
		}
		contentURL := entry.URL
		if contentURL == "" {
			contentURL = serverURL + "/" + strings.TrimPrefix(relPath, "/")
		}

		// Download the post
		content, err := client.FetchContent(contentURL)
		if err != nil {
			result.Errors++
			continue
		}

		// Local path mirrors the remote's source-content layout under
		// targetDir so subsequent `polis render` finds the markdown where it
		// expects.
		localPath := filepath.Join(targetDir, relPath)
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			result.Errors++
			continue
		}

		if err := os.WriteFile(localPath, []byte(content), 0644); err != nil {
			result.Errors++
			continue
		}

		result.PostsDownloaded++
	}

	// Try to fetch blessed.json (comment/blessed.json)
	commentDir := filepath.Join(bundleDir, "comment")
	os.MkdirAll(commentDir, 0755)
	blessedURL := serverURL + "/content/pub.polis.core/comment/blessed.json"
	if content, err := client.FetchContent(blessedURL); err == nil {
		if err := os.WriteFile(filepath.Join(commentDir, "blessed.json"), []byte(content), 0644); err == nil {
			// Count blessed comments
			var bc struct {
				Comments []struct {
					Blessed []interface{} `json:"blessed"`
				} `json:"comments"`
			}
			if json.Unmarshal([]byte(content), &bc) == nil {
				for _, post := range bc.Comments {
					result.BlessedCommentsSynced += len(post.Blessed)
				}
			}
		}
	}

	// Try to fetch following.json
	followDir := filepath.Join(bundleDir, "follow")
	os.MkdirAll(followDir, 0755)
	followingURL := serverURL + "/content/pub.polis.core/follow/following.json"
	if content, err := client.FetchContent(followingURL); err == nil {
		os.WriteFile(filepath.Join(followDir, "following.json"), []byte(content), 0644)
	}

	// Save clone state
	newState := &CloneState{
		SourceURL:   serverURL,
		ClonedAt:    time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		LastUpdated: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		PostsCount:  result.PostsDownloaded,
	}
	if state != nil {
		newState.ClonedAt = state.ClonedAt
		newState.PostsCount = state.PostsCount + result.PostsDownloaded
	}
	saveState(statePath, newState)

	return result, nil
}

// normalizeURL ensures URL has https:// prefix and no trailing slash.
func normalizeURL(url string) string {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	url = strings.TrimSuffix(url, "/")
	return url
}

// loadState loads the clone state from disk.
func loadState(path string) (*CloneState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state CloneState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// saveState saves the clone state to disk.
func saveState(path string, state *CloneState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// ExtractDomainForDir extracts a directory name from a URL.
func ExtractDomainForDir(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimSuffix(url, "/")
	if idx := strings.Index(url, "/"); idx > 0 {
		url = url[:idx]
	}
	return url
}
