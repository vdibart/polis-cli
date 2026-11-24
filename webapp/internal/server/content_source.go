package server

import (
	"errors"
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// The owner's own words, as written: a published post's or comment's
// markdown, which the owner's interface puts in the editor to edit (v5 spec
// 2.1, `label:edit`). Before these, the interface fetched the public source
// file, which only a site that serves its pages has: a laptop, or a remote
// instance whose pages live elsewhere, does not.
//
// ⛔ Only a published source is read: content/pub.polis.core/<type>/<date>/<name>.md,
// one level, by the exact pattern, so no draft, pending or private file can be
// named, and the store's key check refuses anything that escapes the site.

var sourcePattern = map[string]*regexp.Regexp{
	"post":    regexp.MustCompile(`^content/pub\.polis\.core/post/[0-9]{8}/[A-Za-z0-9][A-Za-z0-9._-]*\.md$`),
	"comment": regexp.MustCompile(`^content/pub\.polis\.core/comment/[0-9]{8}/[A-Za-z0-9][A-Za-z0-9._-]*\.md$`),
}

// sourceRoutes are post.get and comment.get.
func (s *Server) sourceRoutes() []api.HostRoute {
	route := func(kind, what string) api.HostRoute {
		return api.HostRoute{Op: apidesc.Operation{
			Type: "pub.polis." + kind, Action: "get", Title: "Read one of your " + kind + "s", Method: "GET", Path: "/v1/content/" + kind + "/source",
			Summary: "Read one of the site's " + what + " as it was written",
			Description: "The markdown of one of the site's published " + what + ", without its frontmatter (the " +
				"signature and the fields made again on republishing), given its file path from all " + kind +
				"s from me (source_path). For editing: change it and send it back with " + kind + ".update.",
			QueryParams:   map[string]string{"path": "The " + kind + "'s file path, e.g. content/pub.polis.core/" + kind + "/20261113/hello.md."},
			RequiredQuery: []string{"path"},
			Auth:          apidesc.AuthOwner, Scope: apidesc.ScopeRead,
		}, Serve: func(w http.ResponseWriter, r *http.Request) { s.handleSource(w, r, kind) }}
	}
	return []api.HostRoute{route("post", "posts"), route("comment", "comments"), s.postBodyRoute()}
}

// postBodyPath is where a post from any site is read whole.
const postBodyPath = "/v1/content/post/body"

// postBodyRoute is post.body: the full body of a post the owner is reading,
// usually another site's, which the gather list knows only by its short
// excerpt. ⛔ Owner-only (read scope): the site fetches the address itself,
// so an open route would make it anyone's fetching proxy. The fetch is gated
// against private and loopback addresses and cached for a day.
func (s *Server) postBodyRoute() api.HostRoute {
	return api.HostRoute{Op: apidesc.Operation{
		Type: "pub.polis.post", Action: "body", Title: "Read a post in full", Method: "GET", Path: postBodyPath,
		Summary: "Read any site's post in full, rendered to HTML",
		Description: "The whole body of one post, given its address, rendered to HTML (the title heading left out). " +
			"For a post from another site, which a query returns only as a short excerpt. The site fetches it " +
			"and keeps it for a day.",
		QueryParams:   map[string]string{"url": "The post's address, e.g. https://alice.polis.pub/posts/20261120/hello.html."},
		RequiredQuery: []string{"url"},
		Auth:          apidesc.AuthOwner, Scope: apidesc.ScopeRead,
	}, Serve: s.handlePostBody}
}

func (s *Server) handlePostBody(w http.ResponseWriter, r *http.Request) {
	postURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if !strings.HasPrefix(postURL, "https://") && !strings.HasPrefix(postURL, "http://") {
		writeJSONError(w, http.StatusBadRequest, "url must be a post's full address, e.g. https://alice.polis.pub/posts/20261120/hello.html")
		return
	}
	html, ok := s.remotePostBody(postURL)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "that post could not be read just now")
		return
	}
	writeJSON(w, map[string]string{"url": postURL, "body_html": html})
}

func (s *Server) handleSource(w http.ResponseWriter, r *http.Request, kind string) {
	path := strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("path")), "/")
	if !sourcePattern[kind].MatchString(path) {
		writeJSONError(w, http.StatusBadRequest, "path must be one of this site's published "+kind+"s, e.g. content/pub.polis.core/"+kind+"/20261113/hello.md")
		return
	}
	key, err := storage.CleanKey(path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "not a path on this site")
		return
	}
	data, err := s.Site().Store().Get(r.Context(), key)
	if errors.Is(err, fs.ErrNotExist) {
		writeJSONError(w, http.StatusNotFound, "no "+kind+" at that path")
		return
	}
	if err != nil {
		s.LogError("%s source: %v", kind, err)
		writeJSONError(w, http.StatusInternalServerError, "could not read it just now")
		return
	}
	writeJSON(w, map[string]string{"path": path, "markdown": strings.TrimLeft(publish.StripFrontmatter(string(data)), "\n")})
}
