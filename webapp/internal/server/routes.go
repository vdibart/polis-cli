package server

import (
	"net/http"

	"github.com/vdibart/polis-cli/webapp/internal/serve"
)

// SetupRoutes registers all API routes on the given ServeMux.
func SetupRoutes(mux *http.ServeMux, s *Server) {
	// API routes — GET-only handlers need no body limit
	// ⭐ The capability descriptor: fetched, never injected, and PUBLIC. See
	// capabilities.go for why a privileged global was the thing to remove.
	mux.HandleFunc(CapabilitiesPath, s.handleCapabilities)

	// ⛔ Recovery: PUBLIC by necessity. You cannot authenticate to reach the
	// thing that lets you authenticate, and a locked-out owner is refused by
	// everything else. Safe because they accept only a signature over a
	// challenge this instance just issued. Rate-limited in the handlers.
	mux.HandleFunc(RecoveryChallengePath, s.handleRecoveryChallenge)
	mux.HandleFunc(RecoveryVerifyPath, s.handleRecoveryVerify)

	// ⭐ Enrolment: authorised INSIDE the handler, by an enrolment session or
	// by ownership. Not on the public allow-list — see enrol.go.
	mux.HandleFunc(EnrolPathPrefix, s.handleEnrol)
	mux.HandleFunc(ProvidersPath, s.handleProviders)

	// ⛔ Sign-in ceremony: PUBLIC, because somebody not signed in is exactly
	// who needs it. Safe because it accepts only an assertion over a challenge
	// this instance just issued. Sign-out is NOT public — it needs a session.
	mux.HandleFunc(SignInBeginPath, s.handleSignInBegin)
	mux.HandleFunc(SignInCompletePath, s.handleSignInComplete)
	mux.HandleFunc(SignOutPath, s.handleSignOut)

	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/deploy-status", s.handleDeployStatus)
	mux.HandleFunc("/api/deploy/apply", s.handleDeployApply)
	mux.HandleFunc("/api/validate", s.handleValidate)
	mux.HandleFunc("/api/posts", s.handlePosts)
	mux.HandleFunc("/api/posts/", s.handlePost)

	// Content-heavy POST routes — post markdown (256KB + JSON overhead)
	mux.HandleFunc("/api/render", limitBody(s.handleRender, MaxPostBodySize+4096))
	mux.HandleFunc("/api/publish", limitBody(s.handlePublish, MaxPostBodySize+4096))
	mux.HandleFunc("/api/drafts", limitBody(s.handleDrafts, MaxPostBodySize+4096))
	mux.HandleFunc("/api/republish", limitBody(s.handleRepublish, MaxPostBodySize+4096))
	mux.HandleFunc("/api/unpublish", limitBody(s.handleUnpublish, MaxDefaultBodySize))

	// Content-heavy POST routes — comment text (64KB + JSON overhead)
	mux.HandleFunc("/api/comments/drafts", limitBody(s.handleCommentDrafts, MaxCommentBodySize+4096))
	mux.HandleFunc("/api/comments/sign", limitBody(s.handleCommentSign, MaxCommentBodySize+4096))

	// Content-heavy POST routes — snippet/about content (64KB + JSON overhead)
	mux.HandleFunc("/api/snippets", limitBody(s.handleSnippets, MaxSnippetBodySize+4096))
	mux.HandleFunc("/api/snippets/", limitBody(s.handleSnippet, MaxSnippetBodySize+4096))
	mux.HandleFunc("/api/about", limitBody(s.handleAbout, MaxSnippetBodySize+4096))

	// Default body limit (1MB) — small-payload POST handlers
	mux.HandleFunc("/api/init", limitBody(s.handleInit, MaxDefaultBodySize))
	mux.HandleFunc("/api/drafts/", limitBody(s.handleDraft, MaxDefaultBodySize))
	mux.HandleFunc("/api/comments/drafts/", limitBody(s.handleCommentDraft, MaxDefaultBodySize))
	mux.HandleFunc("/api/comments/beseech", limitBody(s.handleCommentBeseech, MaxDefaultBodySize))
	mux.HandleFunc("/api/comments/pending", s.handleCommentsPending)
	mux.HandleFunc("/api/comments/pending/", s.handleCommentByStatus)
	mux.HandleFunc("/api/comments/blessed", s.handleCommentsBlessed)
	mux.HandleFunc("/api/comments/blessed/", s.handleCommentByStatus)
	mux.HandleFunc("/api/comments/denied", s.handleCommentsDenied)
	mux.HandleFunc("/api/comments/denied/", s.handleCommentByStatus)
	mux.HandleFunc("/api/comments/sync", limitBody(s.handleCommentsSync, MaxDefaultBodySize))

	// Blessing API routes (ON MY POSTS - incoming blessing requests)
	mux.HandleFunc("/api/blessing/requests", s.handleBlessingRequests)
	mux.HandleFunc("/api/blessing/grant", limitBody(s.handleBlessingGrant, MaxDefaultBodySize))
	mux.HandleFunc("/api/blessing/deny", limitBody(s.handleBlessingDeny, MaxDefaultBodySize))
	mux.HandleFunc("/api/blessing/revoke", limitBody(s.handleBlessingRevoke, MaxDefaultBodySize))
	mux.HandleFunc("/api/blessed-comments", s.handleBlessedComments)

	// Settings and automation API routes
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/settings/show-frontmatter", limitBody(s.handleShowFrontmatter, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/hide-read", limitBody(s.handleHideRead, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/webapp-theme", limitBody(s.handleWebappTheme, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/editor-panel-mode", limitBody(s.handleEditorPanelMode, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/avatar", limitBody(s.handleUpdateAvatar, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/author-name", limitBody(s.handleUpdateAuthorName, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/theme", limitBody(s.handleThemeSwitch, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/custom-css", limitBody(s.handleCustomCSS, MaxDefaultBodySize))
	mux.HandleFunc("/api/site/palette", s.handleSitePalette)
	mux.HandleFunc("/api/stream-marks", limitBody(s.handleStreamMarks, MaxDefaultBodySize))
	mux.HandleFunc("/api/nav-shortcuts", limitBody(s.handleNavShortcuts, MaxDefaultBodySize))
	mux.HandleFunc("/api/rotate-key", limitBody(s.handleRotateKey, MaxDefaultBodySize))
	mux.HandleFunc("/api/download-site", s.handleDownloadSite)
	mux.HandleFunc("/api/content/", s.handleContent)
	mux.HandleFunc("/api/templates", s.handleTemplates)

	// Hook/automation routes — localhost only. Hooks are executable scripts run
	// server-side and never run on the hosted platform (EnableHooks=false), so
	// the surface isn't even registered there. The handlers also 403 when
	// !EnableHooks (defense in depth); this removes the reachable route entirely.
	// EnableHooks is set before SetupRoutes runs in both Handler() and the
	// localhost Run path.
	if s.EnableHooks {
		mux.HandleFunc("/api/automations", limitBody(s.handleAutomations, MaxHookBodySize+4096))
		mux.HandleFunc("/api/automations/quick", limitBody(s.handleAutomationsQuick, MaxDefaultBodySize))
		mux.HandleFunc("/api/automations/", limitBody(s.handleAutomation, MaxDefaultBodySize))
		mux.HandleFunc("/api/hooks/generate", limitBody(s.handleHooksGenerate, MaxDefaultBodySize))
	}

	// Site registration API routes
	mux.HandleFunc("/api/site/registration-status", s.handleSiteRegistrationStatus)
	mux.HandleFunc("/api/site/register", limitBody(s.handleSiteRegister, MaxDefaultBodySize))
	mux.HandleFunc("/api/site/unregister", limitBody(s.handleSiteUnregister, MaxDefaultBodySize))
	mux.HandleFunc("/api/site/deploy-check", limitBody(s.handleDeployCheck, MaxDefaultBodySize))
	mux.HandleFunc("/api/site/setup-wizard-dismiss", limitBody(s.handleSetupWizardDismiss, MaxDefaultBodySize))
	mux.HandleFunc("/api/site/license", limitBody(s.handleSiteLicense, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/rosie", limitBody(s.handleRosieSettings, MaxDefaultBodySize))
	mux.HandleFunc("/api/keys", limitBody(s.handleAPIKeys, MaxDefaultBodySize))
	mux.HandleFunc("/api/keys/", limitBody(s.handleAPIKey, MaxDefaultBodySize))
	mux.HandleFunc("/api/mcp/calls", s.handleMCPCalls)
	mux.HandleFunc("/api/kay", s.kayOnly(s.handleKay))
	mux.HandleFunc("/api/kay/connect", s.kayOnly(limitBody(s.handleKayConnect, MaxDefaultBodySize)))
	mux.HandleFunc("/api/kay/ask", s.kayOnly(limitBody(s.handleKayAsk, MaxDefaultBodySize)))
	mux.HandleFunc("/api/kay/chats/", s.kayOnly(s.handleKayChat))
	mux.HandleFunc("/api/kay/components", s.kayOnly(s.handleKayComponents))
	// The owner's AI acts here with an API key; not under /api/, so the key is
	// its whole authorisation (mcp.go).
	mux.HandleFunc("/mcp", limitBody(s.handleMCP, MaxDefaultBodySize))
	mux.HandleFunc("/mcp/c/", limitBody(s.handleMCPComponent, MaxDefaultBodySize))

	// Social API routes (following, feed, remote content)
	mux.HandleFunc("/api/following", limitBody(s.handleFollowing, MaxDefaultBodySize))
	// 06-profiles Phase 2: enriched directory of profiles in the user's
	// network (display name, relationship, recent post, last activity).
	// /api/following stays as the mutation endpoint (POST/DELETE) — this
	// new endpoint is GET-only and pairs with the new /profiles SPA route.
	mux.HandleFunc("/api/profiles", limitBody(s.handleProfiles, MaxDefaultBodySize))
	mux.HandleFunc("/api/feed", s.handleFeed)
	mux.HandleFunc("/api/feed/refresh", limitBody(s.handleFeedRefresh, MaxDefaultBodySize))
	mux.HandleFunc("/api/feed/read", limitBody(s.handleFeedRead, MaxDefaultBodySize))
	mux.HandleFunc("/api/feed/viewed", limitBody(s.handleFeedViewed, MaxDefaultBodySize))
	mux.HandleFunc("/api/comment/blessing/viewed", limitBody(s.handleBlessingInboxViewed, MaxDefaultBodySize))
	mux.HandleFunc("/api/dm/viewed", limitBody(s.handleDMSurfaceViewed, MaxDefaultBodySize))
	mux.HandleFunc("/api/feed/counts", s.handleFeedCounts)
	mux.HandleFunc("/api/feed/grouped", s.handleFeedGrouped)

	// NOTE: /api/v1/stream/items was retired in the PQL hard cutover — the
	// stream's data path is now GET /pql/<sentence> (registered below). The
	// handleStreamItems engine still backs it (handleStreamPQL delegates to it).
	// Read-focus single-latest-comment lookup (read-focus mode shows exactly
	// one comment per post, blessed or not — sourced from DS). GET-only, same
	// public wrapper as /stream/items.
	mux.HandleFunc("/api/v1/stream/focus-comment", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamFocusComment))
	// Read-focus full-body proxy: returns one post's rendered body so the SPA
	// can show the whole post (not the excerpt) for cross-tenant entries that
	// the browser can't fetch directly under its connect-src CSP. GET-only,
	// same public wrapper as /stream/items.
	mux.HandleFunc("/api/v1/stream/body", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamBody))
	// PQL-native data endpoint (pull-PQL-through Phase 3). GET /pql/<sentence>
	// content-negotiates: Accept: application/json → versioned JSON envelope;
	// else → HTML infinity-stream shell (Phase 5). Same public wrapper +
	// per-IP limiter as /stream/items; owner-private scopes are refused by the
	// owner gate (PQLRequiresOwner), never by this handler.
	mux.HandleFunc("/pql/", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamPQL))
	// step-06/6.e: client-emitted structured event endpoint. Owner SPA
	// posts {event, fields} on icon-preset clicks (pub.polis.stream.
	// preset_loaded) for usage telemetry. Allowlist-restricted.
	mux.HandleFunc("/api/v1/event", limitBody(s.handleClientEvent, MaxDefaultBodySize))
	// step-06/6.f.2: notification rule declarations from registry.json,
	// surfaced to the SPA so owner-extras.js's activity-mode renderer
	// has rule.template + rule.icon to fill per-entry metadata signal.
	// Read-only; cached one-shot at SPA init.
	mux.HandleFunc("/api/v1/bundle/notification-rules", s.handleNotificationRules)
	mux.HandleFunc("/api/remote/avatar", s.handleRemoteAvatar)
	mux.HandleFunc("/api/remote/post", s.handleRemotePost)

	// Notification API routes
	mux.HandleFunc("/api/notifications", s.handleNotifications)
	mux.HandleFunc("/api/notifications/count", s.handleNotificationCount)
	mux.HandleFunc("/api/notifications/read", limitBody(s.handleNotificationRead, MaxDefaultBodySize))

	// DM API routes
	mux.HandleFunc("/api/dm/keyring", s.handleDMKeyring)
	mux.HandleFunc("/api/dm/password", limitBody(s.handleDMSetPassword, MaxDefaultBodySize))
	// Phase 4.8 O(1) re-wrap: change password / regenerate recovery phrase. The
	// browser re-wraps the (unchanged) current-epoch DEK; the server stores the new
	// blob + KDF. No pubkey change → no republish.
	mux.HandleFunc("/api/dm/password/rewrap", limitBody(s.handleDMRewrapPassword, MaxDefaultBodySize))
	mux.HandleFunc("/api/dm/conversations", s.handleDMConversations)
	mux.HandleFunc("/api/dm/conversations/", s.handleDMConversation)
	mux.HandleFunc("/api/dm/send", limitBody(s.handleDMSend, MaxDefaultBodySize))
	// Phase 4.6 browser-seal send: resolve the recipient's verified message key,
	// then relay a browser-sealed envelope (the sender's password-epoch DEK never
	// reaches the server).
	mux.HandleFunc("/api/dm/recipient-key", s.handleDMRecipientKey)
	mux.HandleFunc("/api/dm/send-sealed", limitBody(s.handleDMSendSealed, MaxDefaultBodySize))
	// Phase 4.10: expose a recipient's signed protection_status to the SPA (server
	// makes the signed, mutuals-gated query; failures → status:"unknown").
	mux.HandleFunc("/api/dm/protection-status", s.handleDMProtectionStatus)
	mux.HandleFunc("/api/dm/mark-read", limitBody(s.handleDMMarkRead, MaxDefaultBodySize))
	mux.HandleFunc("/api/dm/retry", limitBody(s.handleDMRetry, MaxDefaultBodySize))
	mux.HandleFunc("/api/dm/recipients", s.handleDMRecipients)

	// Tag API routes
	mux.HandleFunc("/api/tags", limitBody(s.handleTags, MaxDefaultBodySize))

	// Social plugin routes
	mux.HandleFunc("/api/pulse", s.handlePulse)
	mux.HandleFunc("/api/conversations", s.handleConversations)
	mux.HandleFunc("/api/followers/count", s.handleFollowerCount)

	// Render API routes (for snippet editing workflow)
	mux.HandleFunc("/api/render-page", limitBody(s.handleRenderPage, MaxDefaultBodySize))

	// SSE and consolidated counts routes
	mux.HandleFunc("/api/sse", s.handleSSE)
	mux.HandleFunc("/api/counts", s.handleCounts)
	mux.HandleFunc("/api/nav/state", s.handleNavState)

	// Serve generated favicon from data dir (avatar-based, overrides embedded fallback)
	mux.HandleFunc("/favicon.svg", s.handleFavicon)

	// Content source path redirect (content/ .html → mount path)
	mux.HandleFunc("/content/", s.handleContentRedirect)

	// Widget API routes (cross-origin, widget token auth)
	mux.HandleFunc("/api/widget/publish", limitBody(s.handleWidgetPublish, MaxCommentBodySize+4096))
	mux.HandleFunc("/api/widget/comment", limitBody(s.handleWidgetComment, MaxCommentBodySize+4096))
	mux.HandleFunc("/api/widget/follow", limitBody(s.handleWidgetFollow, MaxDefaultBodySize))
	mux.HandleFunc("/api/widget/connect", limitBody(s.handleWidgetConnect, MaxDefaultBodySize))
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
// 404 under --reader: admin SPA at /, /_/... deep-link routes,
// /api/feed, /api/blessings, /api/comments, /api/dm, /api/init,
// auth-related routes, /widget-X.Y.Z.js (hosted-only). All editor-
// surface routes registered by SetupRoutes are simply not registered
// in --reader mode — the mux 404s any request that doesn't match a
// route in this set.
func SetupReaderRoutes(mux *http.ServeMux, s *Server) {
	// /api/v1/stream/items retired in the PQL hard cutover — see SetupRoutes.
	// The data path is GET /pql/<sentence> (registered below).
	// Read-focus single-latest-comment lookup — same wrapper as in SetupRoutes.
	mux.HandleFunc("/api/v1/stream/focus-comment", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamFocusComment))
	// Read-focus full-body proxy — same wrapper as in SetupRoutes.
	mux.HandleFunc("/api/v1/stream/body", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamBody))
	// PQL-native data endpoint — same wrapper as in SetupRoutes.
	mux.HandleFunc("/pql/", publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamPQL))

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
