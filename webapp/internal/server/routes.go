package server

import "net/http"

// SetupRoutes registers all API routes on the given ServeMux.
func SetupRoutes(mux *http.ServeMux, s *Server) {
	// API routes — GET-only handlers need no body limit
	mux.HandleFunc("/api/status", s.handleStatus)
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

	// Content-heavy POST routes — hook scripts (32KB + JSON overhead)
	mux.HandleFunc("/api/automations", limitBody(s.handleAutomations, MaxHookBodySize+4096))

	// Default body limit (1MB) — small-payload POST handlers
	mux.HandleFunc("/api/init", limitBody(s.handleInit, MaxDefaultBodySize))
	mux.HandleFunc("/api/link", limitBody(s.handleLink, MaxDefaultBodySize))
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
	mux.HandleFunc("/api/settings/view-mode", limitBody(s.handleViewMode, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/show-frontmatter", limitBody(s.handleShowFrontmatter, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/hide-read", limitBody(s.handleHideRead, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/webapp-theme", limitBody(s.handleWebappTheme, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/editor-panel-mode", limitBody(s.handleEditorPanelMode, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/site-title", limitBody(s.handleUpdateSiteTitle, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/avatar", limitBody(s.handleUpdateAvatar, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/author-name", limitBody(s.handleUpdateAuthorName, MaxDefaultBodySize))
	mux.HandleFunc("/api/settings/theme", limitBody(s.handleThemeSwitch, MaxDefaultBodySize))
	mux.HandleFunc("/api/rotate-key", limitBody(s.handleRotateKey, MaxDefaultBodySize))
	mux.HandleFunc("/api/download-site", s.handleDownloadSite)
	mux.HandleFunc("/api/content/", s.handleContent)
	mux.HandleFunc("/api/automations/quick", limitBody(s.handleAutomationsQuick, MaxDefaultBodySize))
	mux.HandleFunc("/api/automations/", limitBody(s.handleAutomation, MaxDefaultBodySize))
	mux.HandleFunc("/api/templates", s.handleTemplates)
	mux.HandleFunc("/api/hooks/generate", limitBody(s.handleHooksGenerate, MaxDefaultBodySize))

	// Site registration API routes
	mux.HandleFunc("/api/site/registration-status", s.handleSiteRegistrationStatus)
	mux.HandleFunc("/api/site/register", limitBody(s.handleSiteRegister, MaxDefaultBodySize))
	mux.HandleFunc("/api/site/unregister", limitBody(s.handleSiteUnregister, MaxDefaultBodySize))
	mux.HandleFunc("/api/site/deploy-check", limitBody(s.handleDeployCheck, MaxDefaultBodySize))
	mux.HandleFunc("/api/site/setup-wizard-dismiss", limitBody(s.handleSetupWizardDismiss, MaxDefaultBodySize))

	// Social API routes (following, feed, remote content)
	mux.HandleFunc("/api/following", limitBody(s.handleFollowing, MaxDefaultBodySize))
	mux.HandleFunc("/api/feed", s.handleFeed)
	mux.HandleFunc("/api/feed/refresh", limitBody(s.handleFeedRefresh, MaxDefaultBodySize))
	mux.HandleFunc("/api/feed/read", limitBody(s.handleFeedRead, MaxDefaultBodySize))
	mux.HandleFunc("/api/feed/counts", s.handleFeedCounts)
	mux.HandleFunc("/api/feed/grouped", s.handleFeedGrouped)
	mux.HandleFunc("/api/remote/avatar", s.handleRemoteAvatar)
	mux.HandleFunc("/api/remote/post", s.handleRemotePost)

	// Notification API routes
	mux.HandleFunc("/api/notifications", s.handleNotifications)
	mux.HandleFunc("/api/notifications/count", s.handleNotificationCount)
	mux.HandleFunc("/api/notifications/read", limitBody(s.handleNotificationRead, MaxDefaultBodySize))

	// DM API routes
	mux.HandleFunc("/api/dm/conversations", s.handleDMConversations)
	mux.HandleFunc("/api/dm/conversations/", s.handleDMConversation)
	mux.HandleFunc("/api/dm/send", limitBody(s.handleDMSend, MaxDefaultBodySize))
	mux.HandleFunc("/api/dm/mark-read", limitBody(s.handleDMMarkRead, MaxDefaultBodySize))
	mux.HandleFunc("/api/dm/retry", limitBody(s.handleDMRetry, MaxDefaultBodySize))
	mux.HandleFunc("/api/dm/recipients", s.handleDMRecipients)

	// Social plugin routes
	mux.HandleFunc("/api/pulse", s.handlePulse)
	mux.HandleFunc("/api/activity", s.handleActivityStream)
	mux.HandleFunc("/api/conversations", s.handleConversations)
	mux.HandleFunc("/api/followers/count", s.handleFollowerCount)

	// Render API routes (for snippet editing workflow)
	mux.HandleFunc("/api/render-page", limitBody(s.handleRenderPage, MaxDefaultBodySize))

	// SSE and consolidated counts routes
	mux.HandleFunc("/api/sse", s.handleSSE)
	mux.HandleFunc("/api/counts", s.handleCounts)

	// Content source path redirect (content/ .html → mount path)
	mux.HandleFunc("/content/", s.handleContentRedirect)

	// Widget API routes (cross-origin, widget token auth)
	mux.HandleFunc("/api/widget/publish", limitBody(s.handleWidgetPublish, MaxCommentBodySize+4096))
	mux.HandleFunc("/api/widget/comment", limitBody(s.handleWidgetComment, MaxCommentBodySize+4096))
	mux.HandleFunc("/api/widget/follow", limitBody(s.handleWidgetFollow, MaxDefaultBodySize))
	mux.HandleFunc("/api/widget/connect", limitBody(s.handleWidgetConnect, MaxDefaultBodySize))
}
