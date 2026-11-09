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
	"bytes"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// SecurityLogger is called when security-relevant events occur in the API layer.
type SecurityLogger func(event string, fields map[string]interface{})

// SetupRoutes registers the /v1/ API routes on the given ServeMux.
// The optional securityLogger receives security events (auth failures, etc.).
func SetupRoutes(mux *http.ServeMux, engine *ops.Engine, site storage.Site, securityLogger ...SecurityLogger) {
	h := &handlers{engine: engine}
	if len(securityLogger) > 0 {
		h.securityLogger = securityLogger[0]
	}

	// Bundle introspection (public, read-only)
	mux.HandleFunc("/v1/bundles", cors(h.handleBundlesRoute))
	mux.HandleFunc("/v1/bundles/", cors(h.handleBundleRoute))

	// Content routes (auth depends on method)
	mux.HandleFunc("/v1/content/", cors(limit(func(w http.ResponseWriter, r *http.Request) {
		h.routeContent(w, r, site)
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
func (h *handlers) routeContent(w http.ResponseWriter, r *http.Request, site storage.Site) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/content/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "content type required")
		return
	}

	// Parse path segments
	segments := strings.SplitN(path, "/", 4) // type[/seg2[/seg3[/rest]]]
	contentType := segments[0]

	// Determine auth requirement based on the operation.
	// All non-GET requests require auth. Reads also require it for drafts
	// (unpublished content) and for every type that is not declared public
	// (isPrivateContentType: dm and follow today, and anything unknown).
	isDraftPath := len(segments) >= 2 && segments[1] == "drafts"
	isPrivateType := isPrivateContentType(contentType)
	needsAuth := r.Method != http.MethodGet || isDraftPath || isPrivateType

	if needsAuth {
		// Check if this is a signed-request auth (for DM deliver). Accept both
		// the short form ("dm") and the fully-qualified form ("pub.polis.dm")
		// so this gate stays aligned with Engine.resolveTypeName, which treats
		// the two as the same type. Pre-R18-13 the FQN form fell through to
		// Bearer auth (which always 401'd for remote senders); same outcome,
		// but the gate-vs-resolver mismatch was a smell waiting for a sibling
		// content type to grow into the same shape.
		isDMType := contentType == "dm" || contentType == "pub.polis.dm"
		isDMSignedAction := isDMType && len(segments) >= 3 && segments[1] == "actions" &&
			(segments[2] == "deliver" || segments[2] == "protection_status")
		if isDMSignedAction {
			// Site-to-site actions (`deliver`, `protection_status`) authenticate ONLY with
			// signed-request headers — never a bearer API key. The signed-request branch
			// cryptographically establishes the caller domain; the generic Bearer path
			// (handleContentAction) would take `sender_domain` from the request body
			// unverified, letting a key holder forge inbound DMs with spoofed attribution
			// (DM-7). So a request without X-Polis-Domain is refused here rather than
			// falling through to Bearer auth.
			if r.Header.Get("X-Polis-Domain") == "" {
				writeError(w, http.StatusForbidden, "forbidden", "This action requires a signed site-to-site request (X-Polis-Domain/Signature/Timestamp).")
				return
			}
			// DM-2: the signature binds the recipient domain + body digest. Read the raw
			// body once for the digest and reset it so the downstream handler can re-read.
			// recipientDomain is OUR configured domain (engine.BaseURL), never the request
			// Host. Body is already capped by the limitBody MaxBytesReader.
			body, rerr := io.ReadAll(r.Body)
			if rerr != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "could not read request body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			recipientDomain := dm.ExtractDomainFromURL(h.engine.BaseURL)
			callerDomain, err := dm.VerifySignedRequestForAction(r, segments[2], recipientDomain, body, fetchRemotePublicKey, nil)
			if err != nil {
				writeError(w, http.StatusForbidden, "forbidden", "Signed request verification failed: "+err.Error())
				return
			}
			// Inject the verified caller domain into the action dispatch.
			if segments[2] == "deliver" {
				h.handleDMDeliver(w, r, callerDomain)
			} else {
				h.handleDMProtectionStatus(w, r, callerDomain)
			}
			return
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
		key, valid := ops.LookupAPIKey(site, token)
		if valid && !key.Unscoped() {
			// ⛔ Content is an authored source, and no scope covers sources yet,
			// so only a full-access key may write it. A scoped key (render) is
			// told which kind of key it is, not that it is invalid.
			h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
				"path": r.URL.Path, "method": r.Method, "reason": "insufficient_scope",
				"key_id": key.ID, "request_id": w.Header().Get("X-Request-Id"),
			})
			writeError(w, http.StatusForbidden, "insufficient_scope", "This API key is scoped ("+strings.Join(key.Scopes, ", ")+") and cannot act on content.")
			return
		}
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

// isPrivateContentType reports whether reads of a content type require auth.
//
// ⛔ FAIL CLOSED, from the bundle's own declaration. A type is public only if
// the core bundle declares it and does not mark it Private; anything else —
// a private type, a type this build does not know, a misspelling — requires
// auth. This used to be a hand-kept deny-list of private types (dm, follow),
// so a new private type was public until someone remembered to add it.
func isPrivateContentType(shortOrFull string) bool {
	for fqn, ct := range coreTypes.Types {
		if shortOrFull == fqn || shortOrFull == ct.Dir {
			return ct.Private
		}
	}
	return true
}

// coreTypes is the core bundle's declaration, read once.
var coreTypes = bundle.DefaultCoreBundle()
