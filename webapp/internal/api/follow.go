package api

import (
	"encoding/json"
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
)

// Following is how the host serves follow and unfollow to the API: the same
// functions its own /api/following route calls.
type Following struct {
	Follow   func(r *http.Request, url string) (interface{}, int, error)
	Unfollow func(r *http.Request, url string) (interface{}, int, error)
}

var siteURLField = apidesc.String("The site's address, e.g. https://alice.polis.pub.")

// followOperations are follow and unfollow, declared beside their routes.
var followOperations = []apidesc.Operation{
	{
		Type: "pub.polis.follow", Action: "create", Title: "Follow a site", Method: "POST", Path: "/v1/content/follow",
		Summary: "Follow a site as the owner",
		Description: "Adds a site to the owner's signed follow list and tells the discovery service. Its posts " +
			"start appearing in gather, and comments it already left on the owner's posts are blessed where the " +
			"owner's rules say so. Following is public: anyone can see whom the owner follows. Follow only " +
			"when the owner asked.",
		Input: apidesc.Object(map[string]any{"url": siteURLField}, "url"),
		Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeFollow,
		Write: true, Signs: true,
		// Served by the host, not the engine's create: 200, not 201.
		Status: 200,
		Output: apidesc.Out(map[string]any{
			"author_url": apidesc.String("The site followed."), "author_email": apidesc.String("Its author's email, where it publishes one."),
			"already_followed": apidesc.Bool("It was already followed."),
			"comments_found":   apidesc.Num("Its comments on the owner's posts found waiting."),
			"comments_blessed": apidesc.Num("Of those, blessed by following it."), "comments_failed": apidesc.Num("Of those, not blessed."),
			"blessed_comments": apidesc.Arr("The comments blessed.", apidesc.String("A comment's address.")),
		}),
	},
	{
		Type: "pub.polis.follow", Action: "delete", Title: "Unfollow a site", Method: "POST", Path: "/v1/content/follow/actions/delete",
		Summary: "Unfollow a site as the owner",
		Description: "Removes a site from the owner's signed follow list and tells the discovery service. " +
			"Comments blessed only because the owner followed it are turned away where the owner's rules say so.",
		Input: apidesc.Object(map[string]any{"url": siteURLField}, "url"),
		Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeFollow,
		Write: true, Signs: true,
		Output: apidesc.Out(map[string]any{
			"author_url": apidesc.String("The site unfollowed."), "was_following": apidesc.Bool("It was followed."),
			"comments_found":  apidesc.Num("Its blessed comments on the owner's posts."),
			"comments_denied": apidesc.Num("Of those, turned away by unfollowing."), "comments_failed": apidesc.Num("Of those, not turned away."),
		}),
	},
}

func (h *handlers) handleFollow(w http.ResponseWriter, r *http.Request, action string) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "body must be JSON: {\"url\"}")
		return
	}
	f := h.host.Following
	var (
		result interface{}
		status int
		err    error
	)
	if action == "create" {
		result, status, err = f.Follow(r, req.URL)
	} else {
		result, status, err = f.Unfollow(r, req.URL)
	}
	if err != nil {
		writeHostError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// writeHostError answers a host operation's failure with its status.
func writeHostError(w http.ResponseWriter, status int, err error) {
	code := "invalid_request"
	if status >= 500 {
		code = "internal_error"
	} else if status == http.StatusPreconditionFailed {
		code = "not_registered"
	} else if status == http.StatusNotFound {
		code = "not_found"
	}
	writeError(w, status, code, err.Error())
}

// hostAction returns the host's handler for a /v1/content request, or nil
// when the engine serves it.
func (h *handlers) hostAction(contentType, action string) func(http.ResponseWriter, *http.Request) {
	is := func(short string) bool { return contentType == short || contentType == "pub.polis."+short }
	switch {
	case h.host.Blessing != nil && is("comment") && (action == "bless" || action == "deny" || action == "unbless"):
		return func(w http.ResponseWriter, r *http.Request) { h.handleBlessing(w, r, action) }
	case h.host.Following != nil && is("follow") && (action == "create" || action == "delete"):
		return func(w http.ResponseWriter, r *http.Request) { h.handleFollow(w, r, action) }
	}
	return nil
}
