package server

import (
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/styleguide"
	"github.com/vdibart/polis-cli/webapp/internal/serve"
)

// SetupRoutes registers every route the instance serves outside /v1 (which
// instance.go and server.go mount separately).
//
// ⭐ WHAT IS LEFT UNDER /api/, AND WHY. The site's API is /v1; /api/ is not a
// second one. Every /api/ route below is in one of four groups, each saying
// why it is not a /v1 operation. ⛔ Nothing new goes under /api/: a site
// action is a /v1 operation. A route that fits none of these groups is a bug.
//
//  1. Getting signed in (a named exception of the one API): a caller cannot
//     need a credential to get one.
//  2. The capability descriptor (a named exception): read before a caller
//     knows what the API offers.
//  3. The embeddable widget (a named exception): polis.pub's own service.
//  4. NOT an exception, kept for a live caller: the public pages of the v3
//     and v4 shapes. Both are unsupported for new sites but still render
//     self-hosted sites exactly as before, and the v4 shape's stream.js
//     (cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.js) calls
//     these from every visitor's browser. They go when the v4 shape does.
//
// ⚠️ The hosted service answers some /api/ paths itself before a tenant's
// server sees them (polis.pub's account and export routes, the visitor nav's
// /api/nav/state, the widget token and signups): see hosted.go.
func SetupRoutes(mux *http.ServeMux, s *Server) {
	// ── 2. The capability descriptor ──
	// ⭐ Fetched, never injected, and PUBLIC. See capabilities.go for why a
	// privileged global was the thing to remove.
	mux.HandleFunc(CapabilitiesPath, s.handleCapabilities)

	// ── 1. Getting signed in ──
	// ⛔ Recovery: PUBLIC by necessity. You cannot authenticate to reach the
	// thing that lets you authenticate, and a locked-out owner is refused by
	// everything else. Safe because they accept only a signature over a
	// challenge this instance just issued. Rate-limited in the handlers.
	mux.HandleFunc(RecoveryChallengePath, s.handleRecoveryChallenge)
	mux.HandleFunc(RecoveryVerifyPath, s.handleRecoveryVerify)

	// ⭐ Enrolment, and the list of enrolled providers: authorised INSIDE the
	// handler, by an enrolment session or by ownership. Not on the public
	// allow-list — see enrol.go.
	mux.HandleFunc(EnrolPathPrefix, s.handleEnrol)
	mux.HandleFunc(ProvidersPath, s.handleProviders)

	// ⛔ Sign-in ceremony: PUBLIC, because somebody not signed in is exactly
	// who needs it. Safe because it accepts only an assertion over a challenge
	// this instance just issued. Sign-out is NOT public — it needs a session.
	// (The v5 app calls /api/signout on an instance; on polis.pub it uses the
	// hosted service's /auth/logout instead.)
	mux.HandleFunc(SignInBeginPath, s.handleSignInBegin)
	mux.HandleFunc(SignInCompletePath, s.handleSignInComplete)
	mux.HandleFunc(SignOutPath, s.handleSignOut)

	// ── 3. The embeddable widget (cross-origin, widget token auth) ──
	// ⭐ polis.pub's own service: only the hosted service issues a widget
	// token and sends these routes' cross-origin headers, and its signups make
	// polis.pub accounts. Elsewhere /api/widget/connect answers 501. Its
	// writes run the action layer's code (ops.WriteComment, ops.Follow), so
	// this is a second transport, not a second implementation.
	mux.HandleFunc("/api/widget/comment", limitBody(s.handleWidgetComment, MaxCommentBodySize+4096))
	mux.HandleFunc("/api/widget/follow", limitBody(s.handleWidgetFollow, MaxDefaultBodySize))
	mux.HandleFunc("/api/widget/connect", limitBody(s.handleWidgetConnect, MaxDefaultBodySize))

	// ── 4. Kept for the v4 shape's public pages (NOT an exception) ──
	// Kept: the v4 shape's public stream.js calls it for the read-focus card's
	// latest comment; v4 is unsupported but still renders. Public comment
	// content, already served to anonymous visitors.
	mux.HandleFunc("/api/v1/stream/focus-comment", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamFocusComment))
	// Kept: the v4 shape's public stream.js calls it to show a cross-site
	// post's whole body in place (its CSP blocks fetching the other origin);
	// v4 is unsupported but still renders. The v5 app reads the same thing
	// through /v1 (post.body).
	mux.HandleFunc("/api/v1/stream/body", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamBody))

	// ── Outside /api/ ──
	// The owner's AI acts here with an API key; not under /api/, so the key is
	// its whole authorisation (mcp.go).
	mux.HandleFunc("/mcp", limitBody(s.handleMCP, MaxDefaultBodySize))
	// PQL-native data endpoint. GET /pql/<sentence> content-negotiates:
	// Accept: application/json → versioned JSON envelope; else → the HTML
	// infinity-stream shell. Public wrapper + per-IP limiter; owner-private
	// scopes are refused by the owner gate (PQLRequiresOwner), never by this
	// handler.
	mux.HandleFunc("/pql/", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamPQL))
	// The v5 style guide, for an agent changing how a site looks.
	mux.HandleFunc(styleguide.Path, s.handleStyleGuide)
	// Serve generated favicon from data dir (avatar-based, overrides embedded fallback)
	mux.HandleFunc("/favicon.svg", s.handleFavicon)
	// Content source path redirect (content/ .html → mount path)
	mux.HandleFunc("/content/", s.handleContentRedirect)
}

// SetupReaderRoutes registers the read-only public surface used by
// polis-server's --reader mode (step 5.a). The route boundary:
//
// EXISTS under --reader:
//   - / and any tenant-static path (/posts/, /comments/, /styles.css,
//     /sitemap.xml, /stream.js, /.well-known/polis, etc.) → served by
//     tenantStaticHandler via the shared webapp/internal/serve package
//   - /pql/<sentence> — PQL-native data + public stream endpoint
//   - /content/ — source-path 301 redirect to mount paths
//   - /favicon.svg — generated favicon (avatar-derived)
//
// 404 under --reader: the owner app at /, /_/... deep-link routes, /v1/,
// auth-related routes, /widget-X.Y.Z.js (hosted-only). All editor-
// surface routes registered by SetupRoutes are simply not registered
// in --reader mode — the mux 404s any request that doesn't match a
// route in this set.
func SetupReaderRoutes(mux *http.ServeMux, s *Server) {
	// Kept for the v4 shape's public stream.js, as in SetupRoutes (group 4).
	mux.HandleFunc("/api/v1/stream/focus-comment", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamFocusComment))
	mux.HandleFunc("/api/v1/stream/body", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamBody))
	// PQL-native data endpoint — same wrapper as in SetupRoutes.
	mux.HandleFunc("/pql/", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamPQL))
	// The v5 style guide, as in SetupRoutes.
	mux.HandleFunc(styleguide.Path, s.handleStyleGuide)

	// Content source path redirect (content/ .html → mount path)
	mux.HandleFunc("/content/", s.handleContentRedirect)

	// Generated favicon (avatar-based; fallback handled by tenant-static)
	mux.HandleFunc("/favicon.svg", s.handleFavicon)
}

// tenantStaticHandler returns an http.HandlerFunc that serves rendered
// tenant content from the configured data directory using the shared
// webapp/internal/serve package. Used as the catch-all "/" handler
// under --reader mode. No post-process hook (the hosted nav-widget
// injection is hosted-specific; non-hosted polis-server doesn't have
// a "visitor" concept).
func tenantStaticHandler(s *Server) http.HandlerFunc {
	storage := NewDataDirStorage(s.Site())
	return func(w http.ResponseWriter, r *http.Request) {
		serve.ServeTenantPublic(w, r, storage, "", nil)
	}
}
