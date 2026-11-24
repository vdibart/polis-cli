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
//	GET    /v1/bundles                         → list bundles
//	GET    /v1/bundles/{name}                  → get bundle
//
// Read operations (GET on content and bundles) are public.
// Write operations require Bearer token authentication.
package api

import (
	"bytes"
	"context"
	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"io"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// SecurityLogger is called when security-relevant events occur in the API layer.
type SecurityLogger func(event string, fields map[string]interface{})

// SetupRoutes registers the /v1/ API routes on the given ServeMux.
// The optional securityLogger receives security events (auth failures, etc.).
func SetupRoutes(mux *http.ServeMux, engine *ops.Engine, site storage.Site, securityLogger ...SecurityLogger) {
	SetupRoutesWithHost(mux, engine, site, nil, securityLogger...)
}

// Host is what the host serves to the API beyond the engine's content
// operations, each through the very code its own routes use. A nil field
// leaves those operations out.
type Host struct {
	Query     *Query
	Blessing  *Blessing
	Following *Following
	// Routes are the host's operations at their own /v1 paths (the site's
	// settings and facts), each through the handler the host's app uses.
	Routes []HostRoute
	// Tokens checks a bearer token that is not one of the site's API keys:
	// on polis.pub, an access token from the sign-in server agents use. It
	// returns the scopes and the grant URL the token presents. nil where the
	// host issues no such tokens.
	Tokens func(r *http.Request, token string) (TokenGrant, bool)
	// ClientAddr is the caller's address for the per-address rate limit, as
	// the host resolves it behind its proxy. nil uses the socket's peer.
	ClientAddr func(r *http.Request) string
	// OwnerSession reports whether a request carries the site owner's own
	// sign-in (their session, or loopback on their laptop) AND comes from the
	// site's own page. The owner's interface reaches /v1 this way, with no
	// token (v5 spec 1.2: the owner-only actions are performed "with the
	// owner's own sign-in"). nil where the host has no owner sign-in.
	//
	// ⛔ The host must refuse a request from another site's page here, reads
	// included: the session is a cookie and rides along on any request the
	// browser sends. /v1 has no other cross-site check, because a token is
	// never sent by the browser on its own.
	OwnerSession func(r *http.Request) bool
	// SwitchedOff reports whether the host's operator has switched a feature
	// off, right now (switches.go). nil means nothing is ever off.
	SwitchedOff func(name string) bool
}

// ownerSession reports whether the request is the owner, signed in, on the
// site's own page, presenting no token. ⭐ A request that presents a token is
// judged by its token alone, even when the owner is signed in: an agent
// running in the owner's browser is still the agent.
func (h *handlers) ownerSession(r *http.Request) bool {
	return r.Header.Get("Authorization") == "" && h.host != nil && h.host.OwnerSession != nil && h.host.OwnerSession(r)
}

type viaKey struct{}

// Via marks a request a host makes to /v1 in-process on behalf of a caller it
// has already authenticated at endpoint (the instance's /mcp). Only code in
// the process can set it; nothing a client sends can.
func Via(ctx context.Context, endpoint string) context.Context {
	return context.WithValue(ctx, viaKey{}, endpoint)
}

func via(ctx context.Context) string {
	v, _ := ctx.Value(viaKey{}).(string)
	return v
}

// TokenGrant is what a sign-in token presents.
type TokenGrant struct {
	// ID names the connection in logs (never the token).
	ID       string
	Scopes   []string
	GrantURL string
}

// lookupToken is the one place a bearer token becomes a key: one of the
// site's API keys, or else a sign-in token the host vouches for, which acts
// exactly as a personal token does (its grant is checked and marks its writes).
//
// ⛔ A key bound to one endpoint (the instance's /mcp) is accepted here only on
// a request that endpoint makes in-process (Via): presented to /v1 directly, it
// is no key at all.
func (h *handlers) lookupToken(site storage.Site, r *http.Request, token string) (ops.APIKey, bool) {
	if key, ok := ops.LookupAPIKey(site, token); ok {
		if !key.UsableAt(via(r.Context())) {
			return ops.APIKey{}, false
		}
		return key, true
	}
	if h.host != nil && h.host.Tokens != nil {
		if g, ok := h.host.Tokens(r, token); ok && g.GrantURL != "" && len(g.Scopes) > 0 {
			return ops.APIKey{ID: g.ID, Name: g.ID, Scopes: append([]string(nil), g.Scopes...), Grant: g.GrantURL}, true
		}
	}
	return ops.APIKey{}, false
}

// SetupRoutesWithHost is SetupRoutes plus the operations the host serves.
func SetupRoutesWithHost(mux *http.ServeMux, engine *ops.Engine, site storage.Site, host *Host, securityLogger ...SecurityLogger) {
	if host == nil {
		host = &Host{}
	}
	h := &handlers{engine: engine, host: host, site: site}
	if len(securityLogger) > 0 {
		h.securityLogger = securityLogger[0]
	}
	if host.Query != nil {
		mux.HandleFunc(QueryPath, h.edge(h.actsRecorded(site, func(w http.ResponseWriter, r *http.Request) { h.handleQuery(w, r, site) })))
	}
	mux.HandleFunc(SeenPath, h.edge(limit(h.actsRecorded(site, func(w http.ResponseWriter, r *http.Request) { h.handleSeen(w, r, site) }))))
	h.mountHostRoutes(mux, site)

	// The API's own description (public): generated from the engine's
	// described operations and the site's capabilities, never written by
	// hand (cli-go/pkg/apidesc).
	mux.HandleFunc(apidesc.DescriptionPath, h.edge(h.handleDescription))

	// Bundle introspection (public, read-only)
	mux.HandleFunc("/v1/bundles", h.edge(h.handleBundlesRoute))
	mux.HandleFunc("/v1/bundles/", h.edge(h.handleBundleRoute))

	// Content routes (auth depends on method)
	mux.HandleFunc("/v1/content/", h.edge(limit(h.actsRecorded(site, func(w http.ResponseWriter, r *http.Request) {
		h.routeContent(w, r, site)
	}))))
}

// cors applies CORS middleware.
func cors(next http.HandlerFunc) http.HandlerFunc {
	return corsMiddleware(next)
}

// limit applies body size limits.
func limit(next http.HandlerFunc) http.HandlerFunc {
	return limitBody(next, maxAPIBodySize)
}

// handleDescription serves GET /v1/openapi.json: this site's API, described.
func (h *handlers) handleDescription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	e := h.engine
	o := apidesc.Options{SiteURL: e.BaseURL, MCP: apidesc.InstanceMCP(e.Capabilities, e.BaseURL)}
	// ⭐ A scoped token is shown only what it can do, so a generated tool never
	// always answers 403. No token (the public document) or the owner's full
	// token sees everything. An unrecognised token sees the public document.
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") && h.site.Valid() {
		if key, ok := h.lookupToken(h.site, r, strings.TrimPrefix(auth, "Bearer ")); ok && !key.Unscoped() {
			o.Scopes = append([]string{}, key.Scopes...)
			if key.PersonalToken() {
				o.Scopes = []string{}
				if live, _ := connection.ResolveIn(r.Context(), h.site, key.Grant); live != nil {
					o.Scopes = live.Scopes()
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, apidesc.Describe(h.operations(), apidesc.Manual(), e.Capabilities, o))
}

// operations is everything this API serves, described: the host's
// operations, then the engine's.
func (h *handlers) operations() []apidesc.Operation {
	var out []apidesc.Operation
	if h.host.Query != nil {
		out = append(out, queryOperation)
	}
	if h.host.Blessing != nil {
		out = append(out, blessingOperations...)
	}
	if h.host.Following != nil {
		out = append(out, followOperations...)
	}
	out = append(out, seenOperations...)
	for _, hr := range h.host.Routes {
		out = append(out, hr.Op)
	}
	return append(out, h.engine.Operations()...)
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
	// The operation, noted before any refusal, so an error names what was asked.
	if action := contentAction(r.Method, segments); action != "" {
		ops.NoteOp(r.Context(), h.fullTypeName(contentType)+"."+action)
	}

	// Determine auth requirement based on the operation.
	// All non-GET requests require auth. Reads also require it for every type
	// that is not declared public (isPrivateContentType: dm and follow today,
	// and anything unknown). Drafts are saved and discarded by owner-only
	// actions (post.save_draft, post.discard_draft) and read through PQL.
	isPrivateType := isPrivateContentType(contentType)
	needsAuth := r.Method != http.MethodGet || isPrivateType

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

		if !h.requireScope(w, r, site, h.scopeFor(contentType, contentAction(r.Method, segments))) {
			return
		}
	}

	// An operation the host serves (blessing decisions, follow) goes to the
	// host, after the scope check above.
	if serve := h.hostAction(contentType, contentAction(r.Method, segments)); serve != nil && r.Method != http.MethodGet {
		serve(w, r)
		return
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
		} else {
			writeError(w, http.StatusNotFound, "not_found", "unknown path")
		}

	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown path")
	}
}

// requireScope checks the request carries a token the owner issued that may
// act in scope, and answers the refusal itself when it doesn't. ⛔ THE ONE
// CHECK for /v1: the content routes and the query operation both call it.
// The owner's own full-access token may act in every scope; ScopeFull needs
// that token.
func (h *handlers) requireScope(w http.ResponseWriter, r *http.Request, site storage.Site, scope string) bool {
	// ⭐ The owner's own sign-in is the owner: every scope, ScopeFull and the
	// owner-only operations included, no grant and no marker, as the owner's
	// own full-access token is.
	if h.ownerSession(r) {
		if r.Method != http.MethodGet {
			h.logSecurity("pub.polis.api.write", writeAuditFields(r, map[string]interface{}{
				"key_id": "owner-session", "scoped": false, "scope": scope,
				"method": r.Method, "path": r.URL.Path,
			}, w))
		}
		return true
	}
	auth := r.Header.Get("Authorization")
	if auth == "" {
		h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
			"path": r.URL.Path, "method": r.Method, "reason": "missing_header",
			"request_id": w.Header().Get("X-Request-Id"),
		})
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authorization header required")
		return false
	}
	if !strings.HasPrefix(auth, "Bearer ") {
		h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
			"path": r.URL.Path, "method": r.Method, "reason": "invalid_scheme",
			"request_id": w.Header().Get("X-Request-Id"),
		})
		writeError(w, http.StatusUnauthorized, "unauthorized", "Bearer token required")
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	key, valid := h.lookupToken(site, r, token)
	// ⛔ The operator's live switches, before anything else a token can do,
	// scope included, so every token request gets the same answer under attack.
	// A scoped key (a personal token, an agent's) is what they stop; the
	// owner's session never reaches here, and the owner's full key is the
	// owner. 503 (WriteSwitchedOff), never a 401 or 403 that would tell the
	// client its token is bad.
	if valid && !key.Unscoped() &&
		(h.switchedOff(SwitchAPITokens) || (r.Method != http.MethodGet && h.switchedOff(SwitchAgentWrites))) {
		WriteSwitchedOff(w)
		return false
	}
	if valid && !key.Allows(scope) {
		h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
			"path": r.URL.Path, "method": r.Method, "reason": "insufficient_scope",
			"key_id": key.ID, "request_id": w.Header().Get("X-Request-Id"),
		})
		need := "the " + scope + " scope"
		if scope == apidesc.ScopeFull {
			need = "the owner's own full-access token"
		}
		// ⭐ The MCP authorization spec's step-up: name every scope needed.
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+scope+`"`)
		writeError(w, http.StatusForbidden, "insufficient_scope", "This token is scoped ("+strings.Join(key.Scopes, ", ")+"); this needs "+need+".")
		return false
	}
	if !valid {
		h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
			"path": r.URL.Path, "method": r.Method, "reason": "invalid_key",
			"request_id": w.Header().Get("X-Request-Id"),
		})
		writeError(w, http.StatusForbidden, "forbidden", "Invalid API key")
		return false
	}
	if !h.checkGrant(w, r, site, key, scope) {
		return false
	}
	if !key.Unscoped() && !h.tokenBudget(w, r, site.ID(), key.ID) {
		return false
	}
	// ⭐ Every write a token makes is attributed to the token, for the
	// operator's log: which key (by id, never the token), whether it was
	// scoped, what it needed and where. The outcome is the request log's row
	// with the same request_id. Metadata only: never a body.
	if r.Method != http.MethodGet {
		// The request's own fields too (op, surface, an agent's marker), as
		// every event the write causes carries them.
		h.logSecurity("pub.polis.api.write", writeAuditFields(r, map[string]interface{}{
			"key_id": key.ID, "scoped": !key.Unscoped(), "scope": scope,
			"method": r.Method, "path": r.URL.Path,
		}, w))
	}
	return true
}

// checkGrant is the grant half of the one check: what a scoped key may do
// beyond its scopes. The owner's own full key and session are the owner and
// carry no grant.
//
// ⛔ A PERSONAL TOKEN acts only while the grant it presents is live
// (delegation §3.2: on the site, verifying, naming agent/1, never withdrawn),
// and only within that grant's scopes; anything that cannot be established
// refuses (fail closed), reads included. A write then puts the grant in the
// request's slot, which every signing path reads for the marker; a write that
// finds no slot is refused rather than made unmarked.
//
// ⛔ A SCOPED KEY WITHOUT A GRANT (minted before grants) may read but not
// write, until the owner renews it by opening the keys list.
func (h *handlers) checkGrant(w http.ResponseWriter, r *http.Request, site storage.Site, key ops.APIKey, scope string) bool {
	if key.Unscoped() {
		return true
	}
	refuse := func(status int, code, reason, msg string) bool {
		h.logSecurity("pub.polis.security.auth_failed", map[string]interface{}{
			"path": r.URL.Path, "method": r.Method, "reason": reason,
			"key_id": key.ID, "request_id": w.Header().Get("X-Request-Id"),
		})
		writeError(w, status, code, msg)
		return false
	}
	if !key.PersonalToken() {
		if r.Method == http.MethodGet {
			return true
		}
		return refuse(http.StatusForbidden, "grant_required", "no_grant",
			"This key was made before keys carried a grant, so it can read but not write. The owner renews it by opening the keys list in their settings.")
	}
	live, err := connection.ResolveIn(r.Context(), site, key.Grant)
	if err != nil || live == nil {
		return refuse(http.StatusForbidden, "grant_withdrawn", "grant_not_live",
			"The grant this token presents is withdrawn or cannot be confirmed, so it can do nothing. The owner can connect it again.")
	}
	inGrant := false
	for _, s := range live.Scopes() {
		if apidesc.SameScope(s, scope) {
			inGrant = true
		}
	}
	if !inGrant {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+scope+`"`)
		return refuse(http.StatusForbidden, "insufficient_scope", "outside_grant",
			"The grant this token presents does not include the "+scope+" scope.")
	}
	if r.Method != http.MethodGet && !ops.SetGrant(r.Context(), live) {
		return refuse(http.StatusInternalServerError, "internal_error", "no_grant_slot",
			"This write could not carry its agent's marker, so it was not made.")
	}
	return true
}

// actsRecorded installs the request's grant slot and its metadata (request
// id, surface), and once the request is answered:
//   - an error is logged once, with its envelope code and the operation;
//   - a write is recorded in the site's private act log: a connection's under
//     its grant (and on the security log, metadata only, never a body), the
//     owner's own unmarked, so the log answers "what changed my site?".
func (h *handlers) actsRecorded(site storage.Site, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rid := w.Header().Get("X-Request-Id")
		meta := &ops.RequestMeta{RequestID: rid, Surface: h.surfaceOf(r)}
		r = r.WithContext(ops.WithRequestMeta(ops.WithGrantSlot(r.Context()), meta))
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next(sw, r)

		live := ops.GrantFrom(r.Context())
		surface := meta.Surface
		if live != nil {
			surface = ops.SurfaceAgent
		}
		if sw.status >= 400 {
			code := sw.code
			if code == "" {
				code = codeForStatus(sw.status) // what the envelope will carry
			}
			fields := map[string]interface{}{
				"status": sw.status, "code": code, "op": meta.Op, "method": r.Method,
				"path": r.URL.Path, "surface": surface, "request_id": rid,
			}
			if live != nil {
				fields["agent"], fields["grant"] = live.Agent(), live.URL()
			}
			h.logSecurity(APIErrorEvent, fields)
		}
		if r.Method == http.MethodGet {
			return
		}
		act := connection.Act{
			Method: r.Method, Path: r.URL.Path, Status: sw.status, RequestID: rid,
			Op: meta.Op, Surface: surface,
		}
		if live == nil {
			// ⭐ The owner's own write, through their app or their own token.
			// Not a refused one (it was nobody's act), and not another site's
			// signed delivery (that is not the owner writing).
			if (surface == ops.SurfaceOwnerApp || surface == ops.SurfaceToken) && sw.status < 400 {
				_ = connection.RecordAct(site, act)
			}
			return
		}
		act.Agent, act.Grant = live.Agent(), live.URL()
		_ = connection.RecordAct(site, act)
		h.logSecurity("pub.polis.api.agent_act", map[string]interface{}{
			"agent": live.Agent(), "grant": live.URL(), "method": r.Method, "path": r.URL.Path,
			"status": sw.status, "request_id": rid, "op": meta.Op,
		})
	}
}

// APIErrorEvent is logged once for every /v1 request answered with an error.
const APIErrorEvent = "pub.polis.api.error"

// surfaceOf is who a request is from, before its credential is checked: the
// owner signed in, a bearer token, another site's signed request, or nobody.
// A token that turns out to carry a grant is recorded as an agent's.
func (h *handlers) surfaceOf(r *http.Request) string {
	switch {
	case h.ownerSession(r):
		return ops.SurfaceOwnerApp
	case r.Header.Get("Authorization") != "":
		return ops.SurfaceToken
	case r.Header.Get("X-Polis-Signature") != "":
		return ops.SurfaceSite
	}
	return ops.SurfacePublic
}

// statusWriter remembers the status a handler answered with, and the error
// code writeError answered with.
type statusWriter struct {
	http.ResponseWriter
	status int
	code   string
}

func (s *statusWriter) noteErrorCode(code string) { s.code = code }

// errorCoder is a response writer that records the code of the error written
// to it.
type errorCoder interface{ noteErrorCode(code string) }

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// contentAction is the engine action a /v1/content request dispatches, as the
// switch below routes it; "" for a path that is not an operation.
func contentAction(method string, segments []string) string {
	switch {
	case len(segments) == 1 && method == http.MethodGet:
		return "list"
	case len(segments) == 1 && method == http.MethodPost:
		return "create"
	case len(segments) == 2:
		switch method {
		case http.MethodGet:
			return "get"
		case http.MethodPut:
			return "update"
		case http.MethodDelete:
			return "delete"
		}
	case len(segments) == 3 && segments[1] == "actions":
		return segments[2]
	}
	return ""
}

// fullTypeName is a content type's full name ("post" → "pub.polis.post").
func (h *handlers) fullTypeName(contentType string) string {
	for fqn, ct := range coreTypes.Types {
		if contentType == fqn || contentType == ct.Dir {
			return fqn
		}
	}
	return contentType
}

// scopeFor is the scope an operation's declaration names. ⛔ FAIL CLOSED: an
// operation that isn't declared needs the owner's own full-access token.
func (h *handlers) scopeFor(contentType, action string) string {
	full := h.fullTypeName(contentType)
	for _, op := range h.operations() {
		if op.Type == full && op.Action == action && op.Auth == apidesc.AuthOwner {
			return op.Scope
		}
	}
	return apidesc.ScopeFull
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

// How each write an agent can make is marked. ⛔ A test fails the build when a
// write the API offers an agent has no entry, so a new operation must say
// where its marker goes before an agent can use it.
const (
	// MarkedInSignature: the act's signed bytes carry {agent, grant}.
	MarkedInSignature = "signed"
	// RecordedInActLog: nothing is signed, so the act is recorded in the
	// site's private act log with its grant. (A request to the discovery
	// service is signed and marked: delegation.md §5.3.)
	RecordedInActLog = "recorded"
)

// AgentWrites declares the marker path of every write an agent's grant can
// reach.
var AgentWrites = map[string]string{
	// Signed content and decisions: the marker is inside the signature.
	// A post's or comment's registration with the discovery service (a
	// comment's is also its blessing request) carries the same marker inside
	// the request's signature (delegation.md §5.3).
	"post.create":    MarkedInSignature, // agent:/grant: frontmatter, and the registration request
	"post.update":    MarkedInSignature, // the new version's frontmatter, and its registration
	"comment.create": MarkedInSignature, // agent:/grant: frontmatter, and the registration/blessing request
	"comment.update": MarkedInSignature, // the new version's frontmatter, and its registration
	"comment.bless":  MarkedInSignature, // the relationship request and the blessed.json entry
	"comment.deny":   MarkedInSignature, // the relationship request
	// ⚠️ Removing an entry from the signed blessing list leaves no entry to
	// carry a marker, so it is recorded instead.
	"comment.unbless": RecordedInActLog,
	// The policy rule carries the grant URL in added_by, but the private rules
	// file is not signed, so the act log is the record.
	"site.policy_tighten": RecordedInActLog,
	"site.policy_release": RecordedInActLog,
	// Nothing signed.
	"site.set_name":     RecordedInActLog,
	"site.set_avatar":   RecordedInActLog,
	"site.set_about":    RecordedInActLog,
	"site.set_theme":    RecordedInActLog,
	"site.set_tokens":   RecordedInActLog,
	"site.reset_tokens": RecordedInActLog,
	"site.rebuild":      RecordedInActLog,
	"seen.mark":         RecordedInActLog,
	// A follow adds an entry to the signed follow list, and the entry carries
	// the marker inside the list's signature; each blessing it causes is a
	// marked relationship request.
	"follow.create": MarkedInSignature,
	// ⚠️ Removing an entry leaves no entry to carry a marker (as unbless), so
	// the unfollow is recorded; each denial it causes is a marked request.
	"follow.delete": RecordedInActLog,
}

// writeAuditFields is pub.polis.api.write's fields with the request's own
// (request id, op, surface, an agent's marker), as every event a write causes
// carries them; the response's request id when the request installed none.
func writeAuditFields(r *http.Request, fields map[string]interface{}, w http.ResponseWriter) map[string]interface{} {
	fields = ops.RequestFields(r.Context(), fields)
	if rid := w.Header().Get("X-Request-Id"); rid != "" && (fields["request_id"] == nil || fields["request_id"] == "") {
		fields["request_id"] = rid
	}
	return fields
}
