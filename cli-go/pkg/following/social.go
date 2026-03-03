package following

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// FollowResult contains the result of a follow operation.
type FollowResult struct {
	AuthorURL       string `json:"author_url"`
	AuthorEmail     string `json:"author_email"`
	CommentsFound   int    `json:"comments_found"`
	CommentsBlessed int    `json:"comments_blessed"`
	CommentsFailed  int    `json:"comments_failed"`
	AlreadyFollowed bool   `json:"already_followed"`
}

// UnfollowResult contains the result of an unfollow operation.
type UnfollowResult struct {
	AuthorURL      string `json:"author_url"`
	CommentsDenied int    `json:"comments_denied"`
	CommentsFailed int    `json:"comments_failed"`
	CommentsFound  int    `json:"comments_found"`
	WasFollowing   bool   `json:"was_following"`
}

// FollowWithBlessing adds an author to the following list and blesses any
// pending or denied comments from that author. This matches the CLI behavior
// where following someone auto-blesses their comments.
func FollowWithBlessing(followingPath string, authorURL string, discoveryClient *discovery.Client, remoteClient *remote.Client, privKey []byte) (*FollowResult, error) {
	result := &FollowResult{
		AuthorURL: authorURL,
	}

	// Fetch author email from their site
	remoteWK, err := remoteClient.FetchWellKnown(authorURL)
	if err != nil {
		return nil, err
	}
	result.AuthorEmail = remoteWK.Email

	// Fetch unblessed relationships from this author via relationship-query
	authorDomain := discovery.ExtractDomainFromURL(authorURL)

	pendingResp, _ := discoveryClient.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"status": "pending",
	})
	deniedResp, _ := discoveryClient.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"status": "denied",
	})

	// Filter to relationships where source is from the author's domain
	var allUnblessed []discovery.RelationshipRecord
	if pendingResp != nil {
		for _, r := range pendingResp.Records {
			if discovery.ExtractDomainFromURL(r.SourceURL) == authorDomain {
				allUnblessed = append(allUnblessed, r)
			}
		}
	}
	if deniedResp != nil {
		for _, r := range deniedResp.Records {
			if discovery.ExtractDomainFromURL(r.SourceURL) == authorDomain {
				allUnblessed = append(allUnblessed, r)
			}
		}
	}
	result.CommentsFound = len(allUnblessed)

	// Bless all unblessed comments via relationship-update
	for _, rel := range allUnblessed {
		if err := discoveryClient.UpdateRelationship("pub.polis.comment.blessing", rel.SourceURL, rel.TargetURL, "grant", privKey); err != nil {
			result.CommentsFailed++
			continue
		}
		result.CommentsBlessed++
	}

	// Add to following.json
	f, err := Load(followingPath)
	if err != nil {
		return nil, err
	}

	added := f.Add(authorURL)
	if !added {
		result.AlreadyFollowed = true
	}

	// Enrich with metadata from .well-known/polis (already fetched above)
	if entry := f.Get(authorURL); entry != nil {
		entry.SiteTitle = remoteWK.SiteTitle
		entry.AuthorName = remoteWK.Author
	}

	if err := Save(followingPath, f); err != nil {
		return nil, err
	}

	// Emit follow event to discovery stream (non-fatal)
	stream.PublishEvent("pub.polis.follow.announced", map[string]interface{}{
		"target_domain": discovery.ExtractDomainFromURL(authorURL),
	}, privKey)

	return result, nil
}

// UnfollowWithDenial removes an author from the following list and denies
// any blessed comments from that author. This matches the CLI behavior.
func UnfollowWithDenial(followingPath string, authorURL string, discoveryClient *discovery.Client, remoteClient *remote.Client, privKey []byte) (*UnfollowResult, error) {
	result := &UnfollowResult{
		AuthorURL: authorURL,
	}

	// Fetch author info (non-fatal if unreachable)
	remoteWK, err := remoteClient.FetchWellKnown(authorURL)
	if err == nil && remoteWK != nil {
		authorDomain := discovery.ExtractDomainFromURL(authorURL)

		// Fetch granted blessings where source is from the author's domain
		grantedResp, _ := discoveryClient.QueryRelationships("pub.polis.comment.blessing", map[string]string{
			"status": "granted",
		})

		if grantedResp != nil {
			for _, rel := range grantedResp.Records {
				if discovery.ExtractDomainFromURL(rel.SourceURL) == authorDomain {
					result.CommentsFound++
					if err := discoveryClient.UpdateRelationship("pub.polis.comment.blessing", rel.SourceURL, rel.TargetURL, "deny", privKey); err != nil {
						result.CommentsFailed++
						continue
					}
					result.CommentsDenied++
				}
			}
		}
	}

	// Remove from following.json
	f, err := Load(followingPath)
	if err != nil {
		return nil, err
	}

	result.WasFollowing = f.Remove(authorURL)

	if err := Save(followingPath, f); err != nil {
		return nil, err
	}

	// Emit unfollow event to discovery stream (non-fatal)
	stream.PublishEvent("pub.polis.follow.removed", map[string]interface{}{
		"target_domain": discovery.ExtractDomainFromURL(authorURL),
	}, privKey)

	return result, nil
}
