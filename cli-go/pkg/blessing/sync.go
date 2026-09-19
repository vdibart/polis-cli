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

// streamPageLimit is the per-page size for stream pagination, matching the
// mainline pattern (clerk.pageStreamQuery, following/reconcile, chaplain).
const streamPageLimit = 1000

// SyncBlessedComments rebuilds the local blessed.json from the discovery
// service — the set of others' comments on THIS tenant's posts that the tenant
// has blessed.
//
// It reads the tenant's own pub.polis.comment.blessing.granted stream events
// (server-side filtered by actor = this domain, since both auto-blessed and
// manual grants are emitted with actor = the post author). This is the same
// source the live blessingSyncHandler / stream.BlessingHandler consume, and it
// avoids: reconstructing exact post URLs, enumerating the tenant's posts, and
// the relationship-query's 10k-record cliff.
//
// Trust posture matches the prior behavior — the CLI trusts the DS and does not
// re-verify autobless DS attestations (that gate lives in stream.BlessingHandler,
// which needs a DSKeyCache the CLI does not wire up).
//
// NOTE: the earlier implementation queried granted relationships by
// {actor: thisDomain}, which returned the tenant's OWN comments blessed
// elsewhere (relationships store actor = the commenter), not the comments the
// tenant blessed on its posts — syncing the wrong set.
func SyncBlessedComments(siteDir, domain string, client *discovery.Client) (*SyncResult, error) {
	result := &SyncResult{}

	// Load current local blessed comments so we can skip ones we already hold.
	blessedFile, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		return nil, err
	}
	existingURLs := make(map[string]bool)
	for _, post := range blessedFile.Comments {
		for _, bc := range post.Blessed {
			existingURLs[bc.URL] = true
		}
	}

	// Page the stream for this tenant's granted-blessing events. actorFilter =
	// domain keeps each page to grants where WE are the post owner.
	cursor := ""
	for {
		resp, err := client.StreamQuery(cursor, streamPageLimit,
			"pub.polis.comment.blessing.granted", domain, "")
		if err != nil {
			return nil, err
		}
		if resp == nil {
			break
		}

		for _, evt := range resp.Events {
			// Defensive: the actor filter should already scope to us, but the
			// payload carries target_domain (the post owner) — drop anything
			// that is not ours.
			if td, _ := evt.Payload["target_domain"].(string); td != "" && td != domain {
				continue
			}

			// Auto-blessed events carry comment_url/in_reply_to; manual grants
			// carry source_url/target_url.
			commentURL := firstNonEmptyPayload(evt.Payload, "comment_url", "source_url")
			inReplyTo := firstNonEmptyPayload(evt.Payload, "in_reply_to", "target_url")
			if commentURL == "" || inReplyTo == "" {
				continue
			}

			result.Total++

			if existingURLs[commentURL] {
				result.Existing++
				continue
			}

			bc := metadata.BlessedComment{
				URL:       commentURL,
				BlessedAt: evt.Timestamp,
			}
			if err := metadata.AddBlessedComment(siteDir, extractPostPath(inReplyTo), bc); err != nil {
				// Best-effort: log-and-continue, as before.
				continue
			}
			// Guard against the same comment appearing again in a later page.
			existingURLs[commentURL] = true
			result.Synced++
		}

		if !resp.HasMore || resp.Cursor == "" {
			break
		}
		cursor = resp.Cursor
	}

	return result, nil
}

// firstNonEmptyPayload returns the first non-empty string value among the given
// payload keys, or "" if none are present.
func firstNonEmptyPayload(payload map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := payload[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
