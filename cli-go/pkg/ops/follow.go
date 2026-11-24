package ops

import (
	"context"
	"errors"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ FOLLOWING AND UNFOLLOWING HAVE ONE IMPLEMENTATION.
//
// The webapp's shared follow (behind /api/following and /v1 follow.create),
// the embeddable widget, and polis follow and polis unfollow each did it their
// own way: the widget skipped the checks, polis unfollow denied the author's
// blessings and rewrote following.json by hand and emitted no stream events.
// Follow and Unfollow are the one implementation; a host supplies its clients
// and anything only it does afterwards (the webapp backfills its feed).

// ErrFollowInvalid is a follow or unfollow refused before anything is asked:
// errors.Is(err, ErrFollowInvalid), with the message the caller shows.
var ErrFollowInvalid = errors.New("invalid follow")

type followRefused string

func (f followRefused) Error() string        { return string(f) }
func (f followRefused) Is(target error) bool { return target == ErrFollowInvalid }

// FollowSite is the site that follows, and how it reaches the network.
type FollowSite struct {
	Site         storage.Site
	PrivateKey   []byte
	BaseURL      string
	DiscoveryURL string
	DS           *discovery.Client // authenticated as the site
	Remote       *remote.Client
	// Emit logs the write's event; nil logs nothing.
	Emit func(action string, fields map[string]any)
}

func (c FollowSite) config() *following.FollowConfig {
	if c.DiscoveryURL == "" || c.BaseURL == "" {
		return nil
	}
	return &following.FollowConfig{DiscoveryURL: c.DiscoveryURL, BaseURL: c.BaseURL}
}

func (c FollowSite) emit(ctx context.Context, action, op string, fields map[string]any) {
	if c.Emit != nil {
		c.Emit(action, EventFields(ctx, op, MarkerOf(GrantFrom(ctx)), fields))
	}
}

// Follow follows the site at url, blessing what the site's own rules say a
// follow blesses. ⭐ Under an agent's live grant (the request's slot) the new
// follow-list entry and every blessing it causes carry the agent's marker,
// inside their signatures; with no grant it is the owner's, unmarked. The checks are every caller's: the address is https, it is
// not this site's own, and it is compared lower-cased.
func Follow(ctx context.Context, c FollowSite, url string) (*following.FollowResult, error) {
	if c.PrivateKey == nil {
		return nil, followRefused("Not configured: no private key")
	}
	url = strings.ToLower(strings.TrimSpace(url))
	if !strings.HasPrefix(url, "https://") || len(url) <= len("https://") {
		return nil, followRefused("Author URL must use HTTPS")
	}
	own, target := discovery.ExtractDomainFromURL(c.BaseURL), discovery.ExtractDomainFromURL(url)
	if own != "" && target != "" && own == target {
		return nil, followRefused("Cannot follow your own site")
	}
	policies, _ := policy.LoadPolicies(c.Site)
	result, err := following.FollowWithBlessingMarked(c.Site, url, c.DS, c.Remote, c.PrivateKey, policies, MarkerOf(GrantFrom(ctx)), c.config())
	if err != nil {
		return nil, err
	}
	c.emit(ctx, "pub.polis.follow.add", "pub.polis.follow.create", map[string]any{
		"url": url, "comments_blessed": result.CommentsBlessed,
	})
	return result, nil
}

// Unfollow stops following the site at url and turns away its comments the
// follow had blessed, as the site's rules say.
func Unfollow(ctx context.Context, c FollowSite, url string) (*following.UnfollowResult, error) {
	if c.PrivateKey == nil {
		return nil, followRefused("Not configured: no private key")
	}
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "https://") || len(url) <= len("https://") {
		return nil, followRefused("Author URL must use HTTPS")
	}
	policies, _ := policy.LoadPolicies(c.Site)
	result, err := following.UnfollowWithDenialMarked(c.Site, url, c.DS, c.Remote, c.PrivateKey, policies, MarkerOf(GrantFrom(ctx)), c.config())
	if err != nil {
		return nil, err
	}
	c.emit(ctx, "pub.polis.follow.remove", "pub.polis.follow.delete", map[string]any{
		"url": url, "comments_denied": result.CommentsDenied,
	})
	return result, nil
}
