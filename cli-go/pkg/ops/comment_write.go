package ops

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// ⭐⭐ WRITING A COMMENT HAS ONE IMPLEMENTATION.
//
// Signing a comment and sending it to the discovery service for its post's
// author to bless were done in four places (the API's comment.create, the
// embeddable widget, the old browser app's two routes, the CLI), each with its
// own author, generator and answer when the send failed. The steps are here,
// once: SignComment signs, SendComment sends a signed comment, WriteComment
// does both. ⛔ The signing base is untouched: these call the comment
// package's own functions, with the same arguments every caller used.

// maxCommentMarkdown is the largest comment accepted, as the browser app has it.
const maxCommentMarkdown = 64 * 1024

// ErrCommentInvalid is a comment that cannot be written as asked.
var ErrCommentInvalid = errors.New("invalid comment")

// CommentSite is what writing a comment needs of the site writing it.
type CommentSite struct {
	Site         storage.Site
	PrivateKey   []byte
	BaseURL      string // the site's address; its domain is the comment's author
	DiscoveryURL string // "" falls back to the comment package's configured service
	// Marker is the agent's, when sending is its act (the request's grant):
	// the registration and blessing request are signed with it.
	Marker discovery.AgentMarker
}

// CommentSent is what sending a signed comment came to.
type CommentSent struct {
	Sent        bool
	Status      string // the discovery service's answer: pending, blessed, …
	Message     string
	AutoBlessed bool  // the post's author had already blessed it (a re-registration)
	Err         error // why it was not sent; the comment stays signed here, resendable by id
}

// SignComment signs a draft as the site's comment. The author is the site's
// domain and the generator this build's, whichever caller signs.
func SignComment(cs CommentSite, draft *comment.CommentDraft) (*comment.SignedComment, error) {
	if draft == nil || strings.TrimSpace(draft.InReplyTo) == "" {
		return nil, fmt.Errorf("%w: in_reply_to is required", ErrCommentInvalid)
	}
	if strings.TrimSpace(draft.Content) == "" {
		return nil, fmt.Errorf("%w: markdown is required", ErrCommentInvalid)
	}
	if len(draft.Content) > maxCommentMarkdown {
		return nil, fmt.Errorf("%w: comment too large (max 64KB)", ErrCommentInvalid)
	}
	// The post addresses are the .md sources, whichever form a caller had.
	draft.InReplyTo = polisurl.NormalizeToMD(draft.InReplyTo)
	if draft.RootPost != "" {
		draft.RootPost = polisurl.NormalizeToMD(draft.RootPost)
	}
	author := polisurl.ExtractDomain(cs.BaseURL)
	if author == "" {
		return nil, fmt.Errorf("site base URL not configured")
	}
	signed, err := comment.SignComment(cs.Site, draft, author, cs.BaseURL, cs.PrivateKey, comment.GetGenerator())
	if err != nil {
		return nil, fmt.Errorf("sign comment: %w", err)
	}
	return signed, nil
}

// SendComment sends a comment signed here to the discovery service, so its
// post's author can bless it. A failure is the answer's Err, never an error:
// the comment is signed and kept, and sending it again by id may succeed.
func SendComment(cs CommentSite, commentID string) CommentSent {
	var cfg *comment.DiscoveryConfig
	if cs.DiscoveryURL != "" {
		cfg = &comment.DiscoveryConfig{DiscoveryURL: cs.DiscoveryURL, BaseURL: cs.BaseURL, Marker: cs.Marker}
	} else if cs.Marker.Agent != "" || cs.Marker.Grant != "" {
		// The package's configured service, with the marker riding along.
		cfg = &comment.DiscoveryConfig{DiscoveryURL: comment.DiscoveryURL, BaseURL: comment.BaseURL, Marker: cs.Marker}
		if cs.BaseURL != "" {
			cfg.BaseURL = cs.BaseURL
		}
	}
	res, err := comment.BeseechComment(cs.Site, commentID, cs.PrivateKey, cfg)
	if err != nil {
		return CommentSent{Err: err}
	}
	return CommentSent{Sent: true, Status: res.Status, Message: res.Message, AutoBlessed: res.AutoBlessed}
}

// WriteComment signs a comment and sends it. The error is the signing's; a
// signed comment that could not be sent comes back with CommentSent.Err.
func WriteComment(cs CommentSite, draft *comment.CommentDraft) (*comment.SignedComment, CommentSent, error) {
	signed, err := SignComment(cs, draft)
	if err != nil {
		return nil, CommentSent{}, err
	}
	return signed, SendComment(cs, signed.Meta.ID), nil
}

// CommentSentFields is a send's outcome as every API answer states it: sent,
// and either the service's status or the reason it was not sent.
func CommentSentFields(s CommentSent, into map[string]any) map[string]any {
	if into == nil {
		into = map[string]any{}
	}
	into["sent"] = s.Sent
	if s.Err != nil {
		into["send_error"] = s.Err.Error()
		return into
	}
	into["status"] = s.Status
	into["message"] = s.Message
	into["auto_blessed"] = s.AutoBlessed
	return into
}
