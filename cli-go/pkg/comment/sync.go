package comment

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// SyncResult contains the results of syncing pending comments.
type SyncResult struct {
	Blessed      []string `json:"blessed"`
	Denied       []string `json:"denied"`
	StillPending []string `json:"still_pending"`
	Errors       []string `json:"errors"`
}

// SyncPendingComments checks all pending comments with the discovery service
// and moves them to blessed/denied based on their status.
// It also runs the post-comment hook when a comment is blessed.
// Pending/denied comments are in .polis/bundles/pub.polis.core/comments/, blessed go to content/pub.polis.core/comment/YYYYMMDD/.
// baseURL is this site's base URL (e.g. "https://follower1.polis.pub"), used to
// reconstruct comment URLs from frontmatter when the flat comment_url field is absent.
func SyncPendingComments(dataDir, baseURL string, discoveryClient *discovery.Client, hookConfig *hooks.HookConfig) (*SyncResult, error) {
	result := &SyncResult{
		Blessed:      []string{},
		Denied:       []string{},
		StillPending: []string{},
		Errors:       []string{},
	}

	pendingDir := getCommentDir(dataDir, StatusPending)
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil // No pending directory, nothing to sync
		}
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		commentID := strings.TrimSuffix(entry.Name(), ".md")

		// Read comment to get version and URL
		commentPath := filepath.Join(pendingDir, entry.Name())
		data, err := os.ReadFile(commentPath)
		if err != nil {
			result.Errors = append(result.Errors, "failed to read "+commentID+": "+err.Error())
			continue
		}

		content := string(data)
		fm := ParseFrontmatter(content)

		// Reconstruct commentURL: prefer flat field, fall back to baseURL + date + ID
		commentURL := fm["comment_url"]
		if commentURL == "" && baseURL != "" {
			timestamp := time.Now().UTC()
			if ts := fm["published"]; ts != "" {
				if parsed, err := time.Parse("2006-01-02T15:04:05Z", ts); err == nil {
					timestamp = parsed
				}
			}
			dateDir := timestamp.Format("20060102")
			commentURL = polisurl.CommentContentURL(baseURL, dateDir, commentID)
		}

		// Parse in_reply_to: prefer nested format, fall back to flat field
		inReplyTo, _ := ParseNestedInReplyTo(content)
		if inReplyTo == "" {
			inReplyTo = fm["in_reply_to"]
		}

		if commentURL == "" || inReplyTo == "" {
			result.Errors = append(result.Errors, commentID+": missing comment_url or in_reply_to")
			continue
		}

		// Check blessing status via relationship-query
		resp, err := discoveryClient.QueryRelationships("pub.polis.comment.blessing", map[string]string{
			"source_url": commentURL,
			"target_url": inReplyTo,
		})
		if err != nil {
			result.Errors = append(result.Errors, "failed to check "+commentID+": "+err.Error())
			continue
		}

		// Determine status from the relationship record
		status := "pending"
		if resp != nil && len(resp.Records) > 0 {
			status = resp.Records[0].Status
		}

		switch status {
		case "granted":
			// Move to blessed directory (public comments/YYYY/MM/)
			if err := MoveComment(dataDir, commentID, StatusPending, StatusBlessed); err != nil {
				result.Errors = append(result.Errors, "failed to move "+commentID+" to blessed: "+err.Error())
				continue
			}
			result.Blessed = append(result.Blessed, commentID)

			// Run post-comment hook
			if hookConfig != nil {
				// Determine the blessed comment path based on timestamp
				timestamp := time.Now().UTC()
				if ts := fm["timestamp"]; ts != "" {
					if parsed, err := time.Parse("2006-01-02T15:04:05Z", ts); err == nil {
						timestamp = parsed
					}
				}
				dateDir := timestamp.Format("20060102")
				blessedPath := filepath.Join("comments", dateDir, commentID+".md")

				payload := &hooks.HookPayload{
					Event:         hooks.EventPostComment,
					Path:          blessedPath,
					Title:         fm["in_reply_to"],
					Version:       fm["comment_version"],
					Timestamp:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
					CommitMessage: hooks.GenerateCommitMessage(hooks.EventPostComment, fm["in_reply_to"]),
				}
				if _, err := hooks.RunHook(dataDir, hookConfig, payload); err != nil {
					// Log error but don't fail sync
					result.Errors = append(result.Errors, "hook failed for "+commentID+": "+err.Error())
				}
			}

		case "denied":
			// Move to denied directory
			if err := MoveComment(dataDir, commentID, StatusPending, StatusDenied); err != nil {
				result.Errors = append(result.Errors, "failed to move "+commentID+" to denied: "+err.Error())
				continue
			}
			result.Denied = append(result.Denied, commentID)

		case "pending":
			result.StillPending = append(result.StillPending, commentID)

		default:
			result.Errors = append(result.Errors, commentID+": unknown status "+status)
		}
	}

	return result, nil
}

// SyncFromEvents processes blessing grant/deny events from the stream to move
// pending comment files without making per-comment HTTP calls. This is the
// primary event-driven path used by the unified sync loop; SyncPendingComments
// remains as a catch-up fallback for startup.
func SyncFromEvents(dataDir, baseURL string, events []discovery.StreamEvent, hookConfig *hooks.HookConfig) (*SyncResult, error) {
	result := &SyncResult{
		Blessed:      []string{},
		Denied:       []string{},
		StillPending: []string{},
		Errors:       []string{},
	}

	myDomain := discovery.ExtractDomainFromURL(baseURL)
	if myDomain == "" {
		return result, nil
	}

	// Build map of blessing events: source_url -> status
	blessingEvents := make(map[string]string)
	for _, evt := range events {
		switch evt.Type {
		case "pub.polis.comment.blessing.granted", "pub.polis.comment.blessing.denied":
			sourceURL, _ := evt.Payload["source_url"].(string)
			sourceDomain, _ := evt.Payload["source_domain"].(string)
			if sourceURL == "" || sourceDomain != myDomain {
				continue
			}
			if evt.Type == "pub.polis.comment.blessing.granted" {
				blessingEvents[sourceURL] = "granted"
			} else {
				blessingEvents[sourceURL] = "denied"
			}
		}
	}

	if len(blessingEvents) == 0 {
		return result, nil
	}

	// Scan pending comments and match against events
	pendingDir := getCommentDir(dataDir, StatusPending)
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		commentID := strings.TrimSuffix(entry.Name(), ".md")
		commentPath := filepath.Join(pendingDir, entry.Name())
		data, err := os.ReadFile(commentPath)
		if err != nil {
			result.Errors = append(result.Errors, "failed to read "+commentID+": "+err.Error())
			continue
		}

		content := string(data)
		fm := ParseFrontmatter(content)

		// Reconstruct commentURL: prefer flat field, fall back to baseURL + date + ID
		commentURL := fm["comment_url"]
		if commentURL == "" && baseURL != "" {
			timestamp := time.Now().UTC()
			if ts := fm["published"]; ts != "" {
				if parsed, err := time.Parse("2006-01-02T15:04:05Z", ts); err == nil {
					timestamp = parsed
				}
			}
			dateDir := timestamp.Format("20060102")
			commentURL = polisurl.CommentContentURL(baseURL, dateDir, commentID)
		}

		if commentURL == "" {
			continue
		}

		status, found := blessingEvents[commentURL]
		if !found {
			continue
		}

		switch status {
		case "granted":
			if err := MoveComment(dataDir, commentID, StatusPending, StatusBlessed); err != nil {
				result.Errors = append(result.Errors, "failed to move "+commentID+" to blessed: "+err.Error())
				continue
			}
			result.Blessed = append(result.Blessed, commentID)

			// Run post-comment hook
			if hookConfig != nil {
				timestamp := time.Now().UTC()
				if ts := fm["timestamp"]; ts != "" {
					if parsed, err := time.Parse("2006-01-02T15:04:05Z", ts); err == nil {
						timestamp = parsed
					}
				}
				dateDir := timestamp.Format("20060102")
				blessedPath := filepath.Join("comments", dateDir, commentID+".md")

				payload := &hooks.HookPayload{
					Event:         hooks.EventPostComment,
					Path:          blessedPath,
					Title:         fm["in_reply_to"],
					Version:       fm["comment_version"],
					Timestamp:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
					CommitMessage: hooks.GenerateCommitMessage(hooks.EventPostComment, fm["in_reply_to"]),
				}
				if _, err := hooks.RunHook(dataDir, hookConfig, payload); err != nil {
					result.Errors = append(result.Errors, "hook failed for "+commentID+": "+err.Error())
				}
			}

		case "denied":
			if err := MoveComment(dataDir, commentID, StatusPending, StatusDenied); err != nil {
				result.Errors = append(result.Errors, "failed to move "+commentID+" to denied: "+err.Error())
				continue
			}
			result.Denied = append(result.Denied, commentID)
		}
	}

	return result, nil
}

