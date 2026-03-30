// Package feed provides feed management for followed authors.
package feed

// FeedItem represents a single item in the aggregated feed.
type FeedItem struct {
	Type         string `json:"type"`                    // "post", "comment", or "announcement"
	EventType    string `json:"event_type,omitempty"`    // Original DS event type (e.g. "pub.polis.post.published")
	Title        string `json:"title"`
	URL          string `json:"url"`
	Published    string `json:"published"`
	Hash         string `json:"hash,omitempty"`
	AuthorURL    string `json:"author_url"`
	AuthorDomain string `json:"author_domain"`
	TargetURL    string `json:"target_url,omitempty"`
	TargetDomain string `json:"target_domain,omitempty"`
}
