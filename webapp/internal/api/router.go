// Package api provides the REST API for polis content type operations.
//
// Routes are parametric on content type:
//
//	GET    /v1/content/{type}                  → list
//	GET    /v1/content/{type}/{id}             → get
//	POST   /v1/content/{type}                  → create
//	PUT    /v1/content/{type}/{id}             → update
//	DELETE /v1/content/{type}/{id}             → delete
//	POST   /v1/content/{type}/actions/{action} → dispatch
//	GET    /v1/content/{type}/drafts           → list drafts
//	GET    /v1/content/{type}/drafts/{id}      → get draft
//	POST   /v1/content/{type}/drafts           → save draft
//	DELETE /v1/content/{type}/drafts/{id}      → delete draft
//	GET    /v1/bundles                         → list bundles
//	GET    /v1/bundles/{name}                  → get bundle
//
// Read operations (GET on content and bundles) are public.
// Write operations require Bearer token authentication.
package api

import (
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// SecurityLogger is called when security-relevant events occur in the API layer.
type SecurityLogger func(event string, fields map[string]interface{})

// SetupRoutes registers the /v1/ API routes on the given ServeMux.
// The optional securityLogger receives security events (auth failures, etc.).
func SetupRoutes(mux *http.ServeMux, engine *ops.Engine, siteDir string, securityLogger ...SecurityLogger) {
	h := &handlers{engine: engine}
	if len(securityLogger) > 0 {
		h.securityLogger = securityLogger[0]
	}

	// Bundle introspection (public, read-only)
	mux.HandleFunc("/v1/bundles", cors(h.handleBundlesRoute))
	mux.HandleFunc("/v1/bundles/", cors(h.handleBundleRoute))

	// Content routes (auth depends on method)
	mux.HandleFunc("/v1/content/", cors(limit(func(w http.ResponseWriter, r *http.Request) {
		h.routeContent(w, r, siteDir)
	})))
}

// cors applies CORS middleware.
func cors(next http.HandlerFunc) http.HandlerFunc {
	return corsMiddleware(next)
}

// limit applies body size limits.
func limit(next http.HandlerFunc) http.HandlerFunc {
	return limitBody(next, maxAPIBodySize)
}

// handleBundlesRoute handles GET /v1/bundles
func (h *handlers) handleBundlesRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	h.handleBundlesList(w, r)
}

// handleBundleRoute handles GET /v1/bundles/{name}
func (h *handlers) handleBundleRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/v1/bundles/")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "bundle name required")
		return
	}
	h.handleBundleGet(w, r, name)
}

// routeContent parses the content path and dispatches to the appropriate handler.
// Path format: /v1/content/{type}[/{id-or-subresource}[/...]]
func (h *handlers) routeContent(w http.ResponseWriter, r *http.Request, siteDir string) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/content/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "content type required")
		return
	}

	// Parse path segments
	segments := strings.SplitN(path, "/", 4) // type[/seg2[/seg3[/rest]]]
	contentType := segments[0]

	// Determine auth requirement based on the operation
	needsAuth := r.Method != http.MethodGet

	if needsAuth {
		// Check if this is a signed-request auth (for DM deliver)
		if contentType == "dm" && len(segments) >= 3 && segments[1] == "actions" && segments[2] == "deliver" {
			// Try signed-request auth first
			if r.Header.Get("X-Polis-Domain") != "" {
				senderDomain, err := verifySignedRequest(r)
				if err != nil {
					writeError(w, http.StatusForbidden, "forbidden", "Signed request verification failed: "+err.Error())
					return
				}
				// Inject verified sender domain into the action dispatch
				h.handleDMDeliver(w, r, senderDomain)
				return
			}
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
				"path": r.URL.Path, "method": r.Method, "reason": "missing_header",
				"request_id": w.Header().Get("X-Request-Id"),
			})
			writeError(w, http.StatusUnauthorized, "unauthorized", "Authorization header required")
			return
		}
		if !strings.HasPrefix(auth, "Bearer ") {
			h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
				"path": r.URL.Path, "method": r.Method, "reason": "invalid_scheme",
				"request_id": w.Header().Get("X-Request-Id"),
			})
			writeError(w, http.StatusUnauthorized, "unauthorized", "Bearer token required")
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		_, valid := ops.ValidateAPIKey(siteDir, token)
		if !valid {
			h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
				"path": r.URL.Path, "method": r.Method, "reason": "invalid_key",
				"request_id": w.Header().Get("X-Request-Id"),
			})
			writeError(w, http.StatusForbidden, "forbidden", "Invalid API key")
			return
		}
	}

	switch len(segments) {
	case 1:
		// /v1/content/{type}
		switch r.Method {
		case http.MethodGet:
			h.handleContentList(w, r, contentType)
		case http.MethodPost:
			h.handleContentCreate(w, r, contentType)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST only")
		}

	case 2:
		seg2 := segments[1]
		if seg2 == "drafts" {
			// /v1/content/{type}/drafts
			switch r.Method {
			case http.MethodGet:
				h.handleDraftsList(w, r, contentType)
			case http.MethodPost:
				h.handleDraftsCreate(w, r, contentType)
			default:
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST only")
			}
		} else {
			// /v1/content/{type}/{id}
			switch r.Method {
			case http.MethodGet:
				h.handleContentGet(w, r, contentType, seg2)
			case http.MethodPut:
				h.handleContentUpdate(w, r, contentType, seg2)
			case http.MethodDelete:
				h.handleContentDelete(w, r, contentType, seg2)
			default:
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET, PUT, or DELETE only")
			}
		}

	case 3:
		seg2 := segments[1]
		seg3 := segments[2]

		if seg2 == "actions" {
			// /v1/content/{type}/actions/{action}
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
				return
			}
			h.handleContentAction(w, r, contentType, seg3)
		} else if seg2 == "drafts" {
			// /v1/content/{type}/drafts/{id}
			switch r.Method {
			case http.MethodGet:
				h.handleDraftsGet(w, r, contentType, seg3)
			case http.MethodDelete:
				h.handleDraftsDelete(w, r, contentType, seg3)
			default:
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or DELETE only")
			}
		} else {
			writeError(w, http.StatusNotFound, "not_found", "unknown path")
		}

	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown path")
	}
}
