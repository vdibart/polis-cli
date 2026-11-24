package api

import (
	"encoding/json"
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
)

// Blessing is how the host serves blessing decisions to the API: the host's
// own blessing functions (bless, deny, unbless), never a second implementation.
// Each returns its result, or the HTTP status to answer with its error.
type Blessing struct {
	Bless   func(r *http.Request, commentURL, commentVersion, inReplyTo string) (interface{}, int, error)
	Deny    func(r *http.Request, commentURL, inReplyTo string) (interface{}, int, error)
	Unbless func(commentURL string) (interface{}, int, error)
}

var commentURLField = apidesc.String("The comment's address, as the waiting request or the post's comments give it.")
var inReplyToField = apidesc.String("The address of the owner's post the comment replies to.")

// blessingOperations are the blessing decisions, declared here beside their
// routes because the host serves them.
var blessingOperations = []apidesc.Operation{
	{
		Type: "pub.polis.comment", Action: "bless", Title: "Bless a comment", Method: "POST", Path: "/v1/content/comment/actions/bless",
		Summary: "Bless a comment on one of the owner's posts, so it shows there",
		Description: "Accepts a comment waiting on one of the owner's posts: the blessing is signed with the " +
			"site's key and recorded with the discovery service, the comment is checked and kept, and the " +
			"site is rebuilt so readers see it under the post. The comment stays its author's, signed by " +
			"them. Bless only what the owner asked for.",
		Input: apidesc.Object(map[string]any{
			"comment_url":     commentURLField,
			"in_reply_to":     inReplyToField,
			"comment_version": apidesc.String("Optional: the comment version being blessed; looked up when absent."),
		}, "comment_url"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeBless,
		Write: true, Signs: true,
		Output: apidesc.Out(map[string]any{
			"success": apidesc.Bool("Always true."), "comment_url": apidesc.String("The comment blessed."),
			"comment_version": apidesc.String("Its version."), "post_path": apidesc.String("The post it now shows on."),
		}),
	},
	{
		Type: "pub.polis.comment", Action: "deny", Title: "Turn a comment away", Method: "POST", Path: "/v1/content/comment/actions/deny",
		Summary: "Turn a waiting comment away from one of the owner's posts (\"not here\")",
		Description: "Declines a comment waiting on one of the owner's posts, by a request signed with the " +
			"site's key to the discovery service. The comment stays on its author's own site; it just won't " +
			"show under the owner's post.",
		Input: apidesc.Object(map[string]any{
			"comment_url": commentURLField,
			"in_reply_to": inReplyToField,
		}, "comment_url", "in_reply_to"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeBless,
		Write: true, Signs: true,
		Output: apidesc.Out(map[string]any{"success": apidesc.Bool("Always true."), "comment_version": apidesc.String("The comment's version.")}),
	},
	{
		Type: "pub.polis.comment", Action: "unbless", Title: "Unbless a comment", Method: "POST", Path: "/v1/content/comment/actions/unbless",
		Summary: "Take a blessed comment off one of the owner's posts",
		Description: "Withdraws the owner's blessing of a comment: the signed list of blessed comments drops it " +
			"and the site is rebuilt. Other sites stop showing it at their next sync. The comment stays on " +
			"its author's own site.",
		Input: apidesc.Object(map[string]any{
			"comment_url": commentURLField,
		}, "comment_url"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeBless,
		Write: true, Signs: true,
		Output: apidesc.Out(map[string]any{"success": apidesc.Bool("Always true."), "comment_url": apidesc.String("The comment, its blessing withdrawn.")}),
	},
}

// BlessingOperations is the blessing decisions' descriptions.
func BlessingOperations() []apidesc.Operation { return blessingOperations }

// FollowOperations is what the API's follow and unfollow operations declare.
func FollowOperations() []apidesc.Operation { return followOperations }

// handleBlessing serves comment.bless, comment.deny and comment.unbless. The
// caller has checked the token's scope.
func (h *handlers) handleBlessing(w http.ResponseWriter, r *http.Request, action string) {
	var req struct {
		CommentURL     string `json:"comment_url"`
		CommentVersion string `json:"comment_version"`
		InReplyTo      string `json:"in_reply_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "body must be JSON")
		return
	}
	b := h.host.Blessing
	var (
		result interface{}
		status int
		err    error
	)
	switch action {
	case "bless":
		result, status, err = b.Bless(r, req.CommentURL, req.CommentVersion, req.InReplyTo)
	case "deny":
		result, status, err = b.Deny(r, req.CommentURL, req.InReplyTo)
	case "unbless":
		result, status, err = b.Unbless(req.CommentURL)
	}
	if err != nil {
		writeHostError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
