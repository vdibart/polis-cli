package blessing

import (
	"errors"
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
)

// ErrNoPendingRequest reports that no pending blessing request on this
// domain's posts matches the reference given.
var ErrNoPendingRequest = errors.New("no pending blessing request found")

// ResolvePendingRequest finds the pending request a user names by the
// comment's version or its URL.
//
// ⚠️ A version alone is not enough to act on: the DS identifies a
// relationship by its source_url (the comment) and target_url (the post it
// replies to), and a grant or deny sent without them is rejected. Both live on
// the pending request, so every by-version path resolves it here first.
func ResolvePendingRequest(client *discovery.Client, domain, ref string) (*IncomingRequest, error) {
	requests, err := FetchPendingRequests(client, domain)
	if err != nil {
		return nil, fmt.Errorf("fetch pending requests: %w", err)
	}
	for i := range requests {
		if requests[i].CommentVersion == ref || requests[i].CommentURL == ref {
			return &requests[i], nil
		}
	}
	return nil, fmt.Errorf("%w for: %s", ErrNoPendingRequest, ref)
}

// GrantPending grants the pending request named by ref (a comment version or
// URL), resolving the comment URL and in-reply-to from it first.
func GrantPending(siteDir, domain, ref string, client *discovery.Client, hookConfig *hooks.HookConfig, privateKey []byte) (*GrantResult, error) {
	request, err := ResolvePendingRequest(client, domain, ref)
	if err != nil {
		return nil, err
	}
	return Grant(siteDir, request, client, hookConfig, privateKey)
}
