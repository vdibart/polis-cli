package blessing

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
)

// SyncResult contains the result of syncing blessed comments.
type SyncResult struct {
	Synced   int `json:"synced"`
	Existing int `json:"existing"`
	Total    int `json:"total"`
}

// SyncBlessedComments syncs blessed comments from the discovery service to local storage.
// Uses the unified relationship-query endpoint to fetch granted blessings.
func SyncBlessedComments(siteDir, domain string, client *discovery.Client) (*SyncResult, error) {
	result := &SyncResult{}

	// Fetch all granted blessings for this domain from discovery service
	resp, err := client.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"actor":  domain,
		"status": "granted",
	})
	if err != nil {
		return nil, err
	}

	result.Total = len(resp.Records)

	// Load current local blessed comments
	blessedFile, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		return nil, err
	}

	// Track which comment URLs we already have locally
	existingURLs := make(map[string]bool)
	for _, post := range blessedFile.Comments {
		for _, bc := range post.Blessed {
			existingURLs[bc.URL] = true
		}
	}

	// Add any missing blessed comments
	for _, rel := range resp.Records {
		if existingURLs[rel.SourceURL] {
			result.Existing++
			continue
		}

		// Add to local index
		bc := metadata.BlessedComment{
			URL:       rel.SourceURL,
			BlessedAt: rel.UpdatedAt,
		}

		postPath := extractPostPath(rel.TargetURL)
		if err := metadata.AddBlessedComment(siteDir, postPath, bc); err != nil {
			// Log but continue
			continue
		}

		result.Synced++
	}

	return result, nil
}

