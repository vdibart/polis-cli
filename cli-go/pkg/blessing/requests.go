// Package blessing provides blessing workflow management for polis.
// This handles incoming blessing requests (others' comments on my posts).
package blessing

import (
	"net/url"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// IncomingRequest represents a blessing request from another user
// who wants their comment on our post to be blessed.
type IncomingRequest struct {
	ID             string `json:"id"`
	CommentURL     string `json:"comment_url"`
	CommentVersion string `json:"comment_version"`
	InReplyTo      string `json:"in_reply_to"`
	RootPost       string `json:"root_post"`
	Author         string `json:"author"`
	Timestamp      string `json:"timestamp"`
	CreatedAt      string `json:"created_at"`
}

// FetchPendingRequests retrieves pending blessing requests for the authenticated domain.
// The DS post-filter scopes results to records where the caller is the actor (commenter),
// source_url domain (commenter), or target_url domain (post owner).
// We filter client-side to only return records where target_url domain matches ours,
// so the post owner sees incoming requests but the commenter doesn't.
func FetchPendingRequests(client *discovery.Client, domain string) ([]IncomingRequest, error) {
	resp, err := client.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"status": "pending",
	})
	if err != nil {
		return nil, err
	}

	// Filter to records where target_url domain matches our domain.
	// This ensures only the post owner sees incoming blessing requests,
	// not the commenter (whose source_url domain would match instead).
	var result []IncomingRequest
	for _, r := range resp.Records {
		if !domainMatches(r.TargetURL, domain) {
			continue
		}

		commentVersion, _ := r.Metadata["comment_version"].(string)

		result = append(result, IncomingRequest{
			ID:             r.ID.String(),
			CommentURL:     r.SourceURL,
			CommentVersion: commentVersion,
			InReplyTo:      r.TargetURL,
			Author:         r.Actor,
			CreatedAt:      r.CreatedAt,
		})
	}

	if result == nil {
		result = []IncomingRequest{}
	}

	return result, nil
}

// domainMatches checks if a URL's host matches the given domain.
func domainMatches(rawURL, domain string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return strings.EqualFold(host, domain)
}
