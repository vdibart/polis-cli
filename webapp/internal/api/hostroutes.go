package api

import (
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// HostRoute is an operation the host serves at its own /v1 path, through the
// handler its own app already uses. The API checks the operation's scope and
// then hands the request over unchanged: one implementation of each action.
type HostRoute struct {
	Op    apidesc.Operation
	Serve http.HandlerFunc
}

// mountHostRoutes registers every host route, grouping operations that share
// a path by method.
func (h *handlers) mountHostRoutes(mux *http.ServeMux, site storage.Site) {
	byPath := map[string][]HostRoute{}
	var order []string
	for _, hr := range h.host.Routes {
		if _, seen := byPath[hr.Op.Path]; !seen {
			order = append(order, hr.Op.Path)
		}
		byPath[hr.Op.Path] = append(byPath[hr.Op.Path], hr)
	}
	for _, path := range order {
		routes := byPath[path]
		mux.HandleFunc(path, h.edge(limit(h.actsRecorded(site, func(w http.ResponseWriter, r *http.Request) {
			for _, hr := range routes {
				if hr.Op.Method != r.Method {
					continue
				}
				ops.NoteOp(r.Context(), hr.Op.Type+"."+hr.Op.Action)
				// ⛔ A sign-in-only operation refuses every token, the owner's
				// full-access one included, before any scope is considered.
				if hr.Op.SignInOnly && !h.ownerSession(r) {
					h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
						"path": r.URL.Path, "method": r.Method, "reason": "sign_in_only",
						"request_id": w.Header().Get("X-Request-Id"),
					})
					writeError(w, http.StatusForbidden, "sign_in_only", "Only the site's owner, signed in, can do this. No token can, not even the owner's own full-access one.")
					return
				}
				if hr.Op.Auth == apidesc.AuthOwner && !h.requireScope(w, r, site, hr.Op.Scope) {
					return
				}
				hr.Serve(w, r)
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "not a method this path serves")
		}))))
	}
}
