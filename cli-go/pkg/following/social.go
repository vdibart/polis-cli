package following

import (
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
)

// DataDir is the site data directory, used for registration checks.
// Set by the calling application during initialization.
var DataDir string

// DiscoveryURL is the DS URL, used for registration checks.
var DiscoveryURL string

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

// FollowWithBlessing adds an author to the following list and re-evaluates
// pending/denied comments against the site's policies. With default policies
// containing "emit pub.polis.comment.blessing from following", this auto-blesses
// comments from the newly followed author — matching the previous hardcoded behavior.
// FollowConfig holds optional configuration for follow/unfollow operations.
// If nil or fields are empty, falls back to package-level globals (deprecated).
type FollowConfig struct {
	DataDir      string
	DiscoveryURL string
}

func FollowWithBlessing(followingPath string, authorURL string, discoveryClient *discovery.Client, remoteClient *remote.Client, privKey []byte, policies []policy.Policy, cfg ...*FollowConfig) (*FollowResult, error) {
	result := &FollowResult{
		AuthorURL: authorURL,
	}

	// Fetch author email from their site
	remoteWK, err := remoteClient.FetchWellKnown(authorURL)
	if err != nil {
		return nil, err
	}
	result.AuthorEmail = remoteWK.Email

	// Add to following.json first (so policy eval includes this author)
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

	// Build following set for policy evaluation (includes newly added author)
	followingDomains := make(map[string]bool)
	for _, entry := range f.Following {
		d := discovery.ExtractDomainFromURL(entry.URL)
		if d != "" {
			followingDomains[d] = true
		}
	}

	// DS operations: reads (queries) are always allowed, writes (grants, stream
	// events) require registration. This lets unregistered sites follow authors
	// locally and pull their feed, without announcing to the network.
	dd, du := DataDir, DiscoveryURL
	if len(cfg) > 0 && cfg[0] != nil {
		if cfg[0].DataDir != "" {
			dd = cfg[0].DataDir
		}
		if cfg[0].DiscoveryURL != "" {
			du = cfg[0].DiscoveryURL
		}
	}
	registered := discovery.IsRegisteredLocally(dd, du)

	// Fetch pending/denied blessings and re-evaluate against policies (DS read — always allowed)
	pendingResp, err := discoveryClient.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"status": "pending",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Could not check pending blessings: %v\n", err)
	}
	deniedResp, err := discoveryClient.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"status": "denied",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Could not check denied blessings: %v\n", err)
	}

	var allUnblessed []discovery.RelationshipRecord
	if pendingResp != nil {
		allUnblessed = append(allUnblessed, pendingResp.Records...)
	}
	if deniedResp != nil {
		allUnblessed = append(allUnblessed, deniedResp.Records...)
	}
	result.CommentsFound = len(allUnblessed)

	// Re-evaluate each unblessed comment against policies (DS write — requires registration)
	if registered {
		ctx := policy.EvalContext{
			FollowingDomains: followingDomains,
		}
		for _, rel := range allUnblessed {
			sourceDomain := discovery.ExtractDomainFromURL(rel.SourceURL)
			evt := policy.Event{
				Type:        "pub.polis.comment.blessing",
				ActorDomain: sourceDomain,
			}
			decision, matched := policy.EvaluateExplicit(policies, evt, ctx)
			if matched && (decision == policy.Allow || decision == policy.Emit) {
				if err := discoveryClient.UpdateRelationship("pub.polis.comment.blessing", rel.SourceURL, rel.TargetURL, "grant", privKey); err != nil {
					result.CommentsFailed++
					continue
				}
				result.CommentsBlessed++
			}
		}

		// Emit follow event to discovery stream (DS write — non-fatal)
		stream.PublishEvent("pub.polis.follow.announced", map[string]interface{}{
			"target_domain": discovery.ExtractDomainFromURL(authorURL),
		}, privKey)
	} else {
		fmt.Println("[i] DS blessing/announcement skipped: site not registered")
	}

	return result, nil
}

// UnfollowWithDenial removes an author from the following list and re-evaluates
// granted blessings against policies. Blessings that no longer match an emit/allow
// rule (because the author is no longer followed) get denied.
func UnfollowWithDenial(followingPath string, authorURL string, discoveryClient *discovery.Client, remoteClient *remote.Client, privKey []byte, policies []policy.Policy, cfg ...*FollowConfig) (*UnfollowResult, error) {
	result := &UnfollowResult{
		AuthorURL: authorURL,
	}

	// Remove from following.json first (so policy eval excludes this author)
	f, err := Load(followingPath)
	if err != nil {
		return nil, err
	}

	result.WasFollowing = f.Remove(authorURL)

	if err := Save(followingPath, f); err != nil {
		return nil, err
	}

	// Build following set for policy evaluation (excludes removed author)
	followingDomains := make(map[string]bool)
	for _, entry := range f.Following {
		d := discovery.ExtractDomainFromURL(entry.URL)
		if d != "" {
			followingDomains[d] = true
		}
	}

	// DS operations: reads always allowed, writes require registration
	dd, du := DataDir, DiscoveryURL
	if len(cfg) > 0 && cfg[0] != nil {
		if cfg[0].DataDir != "" {
			dd = cfg[0].DataDir
		}
		if cfg[0].DiscoveryURL != "" {
			du = cfg[0].DiscoveryURL
		}
	}
	registered := discovery.IsRegisteredLocally(dd, du)

	// Fetch granted blessings and re-evaluate against policies (DS read — always allowed)
	grantedResp, err := discoveryClient.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"status": "granted",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Could not check granted blessings: %v\n", err)
	}

	if registered && grantedResp != nil {
		ctx := policy.EvalContext{
			FollowingDomains: followingDomains,
		}
		for _, rel := range grantedResp.Records {
			sourceDomain := discovery.ExtractDomainFromURL(rel.SourceURL)
			evt := policy.Event{
				Type:        "pub.polis.comment.blessing",
				ActorDomain: sourceDomain,
			}
			decision, matched := policy.EvaluateExplicit(policies, evt, ctx)
			// If no emit/allow rule matches, deny the blessing
			if !matched || decision == policy.Deny || decision == policy.Omit {
				result.CommentsFound++
				if err := discoveryClient.UpdateRelationship("pub.polis.comment.blessing", rel.SourceURL, rel.TargetURL, "deny", privKey); err != nil {
					result.CommentsFailed++
					continue
				}
				result.CommentsDenied++
			}
		}

		// Emit unfollow event to discovery stream (DS write — non-fatal)
		stream.PublishEvent("pub.polis.follow.removed", map[string]interface{}{
			"target_domain": discovery.ExtractDomainFromURL(authorURL),
		}, privKey)
	}

	return result, nil
}
