// Package index provides index rebuilding functionality.
package index

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
)

// Version is set at init time by cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// PostEntry represents a post entry in public.jsonl.
type PostEntry struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Published string `json:"published"`
	Hash      string `json:"hash"`
}

// RebuildOptions configures what to rebuild.
type RebuildOptions struct {
	Posts         bool
	Comments      bool
	Notifications bool
	All           bool
	// Discovery service params for blessed comments rebuild
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string // Site base URL (e.g., https://alice.polis.pub)
}

// RebuildResult contains the results of a rebuild operation.
type RebuildResult struct {
	PostsRebuilt         int `json:"posts_rebuilt"`
	CommentsRebuilt      int `json:"comments_rebuilt"`
	NotificationsCleared int `json:"notifications_cleared"`
}

// RebuildIndex rebuilds the public.jsonl index from posts directory.
func RebuildIndex(dataDir, baseURL string, opts RebuildOptions) (*RebuildResult, error) {
	result := &RebuildResult{}

	if opts.All || opts.Posts {
		count, err := rebuildPostsIndex(dataDir, baseURL)
		if err != nil {
			return nil, fmt.Errorf("failed to rebuild posts index: %w", err)
		}
		result.PostsRebuilt = count
	}

	if opts.All || opts.Comments {
		count, err := rebuildCommentsIndex(dataDir, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to rebuild comments index: %w", err)
		}
		result.CommentsRebuilt = count
	}

	if opts.All || opts.Notifications {
		count, err := clearNotifications(dataDir)
		if err != nil {
			return nil, fmt.Errorf("failed to clear notifications: %w", err)
		}
		result.NotificationsCleared = count
	}

	// Regenerate manifest
	if err := regenerateManifest(dataDir); err != nil {
		return nil, fmt.Errorf("failed to regenerate manifest: %w", err)
	}

	return result, nil
}

// rebuildPostsIndex rebuilds the public.jsonl from posts.
func rebuildPostsIndex(dataDir, baseURL string) (int, error) {
	postsDir := filepath.Join(dataDir, "content", "pub.polis.core", "post")
	indexPath := filepath.Join(dataDir, "content", "pub.polis.core", "index.jsonl")

	// Ensure metadata directory exists
	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		return 0, err
	}

	// Find all markdown files
	var entries []PostEntry
	err := filepath.Walk(postsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			// Skip .versions directories
			if info.Name() == ".versions" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		entry, err := buildPostEntry(path, dataDir, baseURL)
		if err != nil {
			return nil // Skip files that can't be parsed
		}
		entries = append(entries, entry)
		return nil
	})

	if err != nil {
		return 0, err
	}

	// Sort by published date (newest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Published > entries[j].Published
	})

	// Write index file
	file, err := os.Create(indexPath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		file.WriteString(string(data) + "\n")
	}

	return len(entries), nil
}

// buildPostEntry creates a PostEntry from a markdown file.
func buildPostEntry(path, dataDir, baseURL string) (PostEntry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return PostEntry{}, err
	}

	fm, body := parseFrontmatter(string(content))

	// Calculate relative path for URL
	relPath, _ := filepath.Rel(dataDir, path)
	url := baseURL + "/" + relPath

	// Calculate hash
	hash := sha256.Sum256([]byte(canonicalizeContent(body)))

	return PostEntry{
		Type:      "post",
		Title:     fm["title"],
		URL:       url,
		Published: fm["published"],
		Hash:      fmt.Sprintf("sha256:%x", hash),
	}, nil
}

// rebuildCommentsIndex rebuilds blessed-comments.json from the discovery service.
// Falls back to an empty file if discovery is not configured.
func rebuildCommentsIndex(dataDir string, opts RebuildOptions) (int, error) {
	commentDir := filepath.Join(dataDir, "content", "pub.polis.core", "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		return 0, err
	}

	// If discovery is configured, fetch blessed comments via relationship-query
	if opts.DiscoveryURL != "" && opts.DiscoveryKey != "" && opts.BaseURL != "" {
		client := discovery.NewClient(opts.DiscoveryURL, opts.DiscoveryKey)

		// Extract domain from base URL
		domain := opts.BaseURL
		domain = strings.TrimPrefix(domain, "https://")
		domain = strings.TrimPrefix(domain, "http://")
		domain = strings.TrimSuffix(domain, "/")

		resp, err := client.QueryRelationships("pub.polis.comment.blessing", map[string]string{
			"actor":  domain,
			"status": "granted",
		})
		if err == nil && len(resp.Records) > 0 {
			// Build fresh blessed-comments.json
			bc := &metadata.BlessedComments{
				Version:  GetGenerator(),
				Comments: []metadata.PostComments{},
			}

			// Group by post (target_url)
			postMap := make(map[string][]metadata.BlessedComment)
			for _, rel := range resp.Records {
				postPath := rel.TargetURL
				// Extract relative path
				if idx := strings.Index(postPath, "/posts/"); idx >= 0 {
					postPath = postPath[idx+1:]
				}
				postMap[postPath] = append(postMap[postPath], metadata.BlessedComment{
					URL:       rel.SourceURL,
					BlessedAt: rel.UpdatedAt,
				})
			}

			for post, blessed := range postMap {
				bc.Comments = append(bc.Comments, metadata.PostComments{
					Post:    post,
					Blessed: blessed,
				})
			}

			if err := metadata.SaveBlessedComments(dataDir, bc); err != nil {
				return 0, err
			}

			return len(resp.Records), nil
		}
		// If fetch fails, fall through to empty file
	}

	// No discovery or fetch failed - ensure file exists with empty comments
	blessedPath := filepath.Join(commentDir, "blessed.json")
	if _, err := os.Stat(blessedPath); os.IsNotExist(err) {
		bc := &metadata.BlessedComments{
			Version:  GetGenerator(),
			Comments: []metadata.PostComments{},
		}
		if err := metadata.SaveBlessedComments(dataDir, bc); err != nil {
			return 0, err
		}
	}

	return 0, nil
}

// clearNotifications clears notification state files.
// Current path: .polis/ds/<domain>/pub.polis.core/state/pub.polis.notification.jsonl
func clearNotifications(dataDir string) (int, error) {
	count := 0

	// Clear state files under .polis/ds/*/pub.polis.core/
	dsDir := filepath.Join(dataDir, ".polis", "ds")
	entries, err := os.ReadDir(dsDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			statePath := filepath.Join(dsDir, entry.Name(), "pub.polis.core", "state", "pub.polis.notification.jsonl")
			if data, err := os.ReadFile(statePath); err == nil {
				for _, line := range strings.Split(string(data), "\n") {
					if strings.TrimSpace(line) != "" {
						count++
					}
				}
				os.WriteFile(statePath, []byte{}, 0644)
			}
		}
	}

	return count, nil
}

// regenerateManifest is a no-op — manifest.json has been absorbed into .well-known/polis.
func regenerateManifest(dataDir string) error {
	return nil
}

// parseFrontmatter extracts frontmatter fields from content.
func parseFrontmatter(content string) (map[string]string, string) {
	fm := make(map[string]string)
	lines := strings.Split(content, "\n")

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm, content
	}

	var bodyStart int
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			bodyStart = i + 1
			break
		}
		if idx := strings.Index(lines[i], ":"); idx > 0 {
			key := strings.TrimSpace(lines[i][:idx])
			value := strings.TrimSpace(lines[i][idx+1:])
			fm[key] = value
		}
	}

	body := ""
	if bodyStart < len(lines) {
		body = strings.Join(lines[bodyStart:], "\n")
	}

	return fm, body
}

// canonicalizeContent normalizes content for hashing.
func canonicalizeContent(content string) string {
	content = strings.TrimLeft(content, "\n")
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n") + "\n"
}
