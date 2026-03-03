package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// SyncResult aggregates results from all handlers in a unified sync run.
type SyncResult struct {
	NewNotifications int
	NewFeedItems     int
	FollowersChanged bool
	CommentsChanged  bool
	FilesChanged     bool // any handler reported file changes -> triggers RenderSite
}

// runUnifiedSync performs a single unified sync cycle: queries the DS stream
// with a single cursor, fans out events to all registered handlers, then
// advances the cursor and renders if needed.
func (s *Server) runUnifiedSync() SyncResult {
	result := SyncResult{}

	if s.DiscoveryURL == "" {
		return result
	}
	baseURL := s.GetBaseURL()
	if baseURL == "" || s.PrivateKey == nil {
		return result
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return result
	}

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	// Get unified cursor, migrating from old per-handler cursors on first run
	cursor := s.getUnifiedCursor(store)

	// Collect events from targeted queries (2-3 DS calls with a shared cursor)
	allEvents, newCursor := s.queryStreamEvents(myDomain, cursor)
	if len(allEvents) == 0 {
		// Still update cursor timestamp even if no new events
		if newCursor != "" && cursorGreater(newCursor, cursor) {
			_ = store.SetCursor("pub.polis.sync", newCursor)
		}
		return result
	}

	s.LogDebug("unified sync: processing %d events from cursor %s", len(allEvents), cursor)

	// Fan out to registered handlers
	for _, h := range s.syncHandlers {
		filtered := stream.FilterEvents(allEvents, h.EventTypes())
		if len(filtered) == 0 {
			continue
		}

		hr := h.Process(filtered)
		if hr.Error != nil {
			s.LogWarn("unified sync: %s handler error: %v", h.Name(), hr.Error)
		}
		if hr.FilesChanged {
			result.FilesChanged = true
		}

		switch h.Name() {
		case "notifications":
			result.NewNotifications = hr.NewItems
		case "feed":
			result.NewFeedItems = hr.NewItems
		case "followers":
			result.FollowersChanged = hr.FilesChanged
		case "comment-status":
			result.CommentsChanged = hr.FilesChanged
		}
	}

	// Advance unified cursor
	if newCursor != "" {
		_ = store.SetCursor("pub.polis.sync", newCursor)
	}

	// Single RenderSite if any files changed
	if result.FilesChanged {
		if err := s.RenderSite(); err != nil {
			log.Printf("[warning] unified sync render failed: %v", err)
		}
	}

	// Broadcast counts via SSE
	s.broadcastCounts(result)

	return result
}

// getUnifiedCursor returns the polis.sync cursor, migrating from old
// per-handler cursors on first use (takes the minimum to not miss events).
func (s *Server) getUnifiedCursor(store *stream.Store) string {
	cursor, _ := store.GetCursor("pub.polis.sync")
	if cursor != "" && cursor != "0" {
		return cursor
	}

	// First unified sync — use minimum of old cursors to avoid missing events
	// Check both new (pub.polis.*) and old (polis.*) key names for migration
	oldKeys := []string{"pub.polis.notification", "pub.polis.follow", "pub.polis.feed", "polis.notification", "polis.follow", "polis.feed", "polis.sync"}
	minCursor := ""
	for _, key := range oldKeys {
		c, _ := store.GetCursor(key)
		if c != "" && c != "0" {
			if minCursor == "" || cursorLess(c, minCursor) {
				minCursor = c
			}
		}
	}
	if minCursor != "" {
		return minCursor
	}
	return "0"
}

// cursorLess returns true if cursor a is numerically less than cursor b.
func cursorLess(a, b string) bool {
	ai, errA := strconv.Atoi(a)
	bi, errB := strconv.Atoi(b)
	if errA != nil || errB != nil {
		return a < b
	}
	return ai < bi
}

// queryStreamEvents makes 2-3 targeted DS queries with a shared cursor and
// returns deduplicated events. The queries cover:
// 1. Events targeting our domain (follows, blessing requests, comments on our posts)
// 2. Events where we're the source (blessing grants/denials of our comments)
// 3. Events from followed authors (new posts/comments for feed + notifications)
func (s *Server) queryStreamEvents(myDomain, cursor string) ([]discovery.StreamEvent, string) {
	client := discovery.NewAuthenticatedClient(s.DiscoveryURL, s.DiscoveryKey, myDomain, s.PrivateKey)
	newCursor := cursor
	seen := make(map[string]bool) // event ID -> already collected
	var allEvents []discovery.StreamEvent

	addEvents := func(events []discovery.StreamEvent, resultCursor string) {
		for _, evt := range events {
			id := fmt.Sprintf("%v", evt.ID)
			if !seen[id] {
				seen[id] = true
				allEvents = append(allEvents, evt)
			}
		}
		if cursorGreater(resultCursor, newCursor) {
			newCursor = resultCursor
		}
	}

	// Query 1: Events targeting our domain
	result, err := client.StreamQuery(cursor, 1000, "", "", myDomain)
	if err != nil {
		s.LogDebug("unified sync: target_domain query failed: %v", err)
	} else {
		addEvents(result.Events, result.Cursor)
	}

	// Query 2: Events where we're the source (for blessing grant/deny)
	result, err = client.StreamQuery(cursor, 1000, "", "", "", myDomain)
	if err != nil {
		s.LogDebug("unified sync: source_domain query failed: %v", err)
	} else {
		addEvents(result.Events, result.Cursor)
	}

	// Query 3: Events from followed authors (feed + notifications)
	followingPath := following.DefaultPath(s.DataDir)
	f, err := following.Load(followingPath)
	if err == nil && f.Count() > 0 {
		var domains []string
		for _, entry := range f.All() {
			d := discovery.ExtractDomainFromURL(entry.URL)
			if d != "" {
				domains = append(domains, d)
			}
		}
		if len(domains) > 0 {
			actorFilter := discovery.JoinDomains(domains)
			result, err = client.StreamQuery(cursor, 1000, "", actorFilter, "")
			if err != nil {
				s.LogDebug("unified sync: followed_author query failed: %v", err)
			} else {
				addEvents(result.Events, result.Cursor)
			}
		}
	}

	return allEvents, newCursor
}

// --- Notification Sync Handler ---

type notificationSyncHandler struct {
	server *Server
}

func (h *notificationSyncHandler) Name() string { return "notifications" }

func (h *notificationSyncHandler) EventTypes() []string {
	return (&stream.NotificationHandler{}).EventTypes()
}

func (h *notificationSyncHandler) Process(events []discovery.StreamEvent) stream.HandlerResult {
	s := h.server

	baseURL := s.GetBaseURL()
	if baseURL == "" || s.PrivateKey == nil {
		return stream.HandlerResult{}
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return stream.HandlerResult{}
	}

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	// Load notification config (rules + muted domains)
	var config stream.NotificationConfig
	_ = store.LoadConfig("notifications", &config)

	rules := config.Rules
	if len(rules) == 0 {
		rules = notification.DefaultRules()
		config.Rules = rules
		_ = store.SaveConfig("notifications", &config)
	} else {
		// Merge new default rules not present in saved config
		defaults := notification.DefaultRules()
		existingIDs := make(map[string]bool, len(rules))
		for _, r := range rules {
			existingIDs[r.ID] = true
		}
		added := false
		for _, d := range defaults {
			if !existingIDs[d.ID] {
				rules = append(rules, d)
				added = true
			}
		}
		if added {
			config.Rules = rules
			_ = store.SaveConfig("notifications", &config)
		}
	}

	// Build muted domains set
	mutedDomains := make(map[string]bool, len(config.MutedDomains))
	for _, d := range config.MutedDomains {
		mutedDomains[d] = true
	}

	// Build followed domains set for client-side filtering
	followedDomains := make(map[string]bool)
	followingPath := following.DefaultPath(s.DataDir)
	f, err := following.Load(followingPath)
	if err == nil {
		for _, entry := range f.All() {
			d := discovery.ExtractDomainFromURL(entry.URL)
			if d != "" {
				followedDomains[d] = true
			}
		}
	}

	// Load policies for event filtering
	privPath, pubPath := policy.DefaultPaths(s.DataDir)
	policies, _ := policy.LoadPolicies(privPath, pubPath)

	// Load follower state for policy evaluation
	var followerDomains map[string]bool
	if len(policies) > 0 {
		var followerState stream.FollowerState
		_ = store.LoadState("pub.polis.follow", &followerState)
		followerDomains = make(map[string]bool, len(followerState.Followers))
		for _, f := range followerState.Followers {
			followerDomains[f] = true
		}
	}

	handler := &stream.NotificationHandler{
		MyDomain:        myDomain,
		Rules:           rules,
		MutedDomains:    mutedDomains,
		FollowedDomains: followedDomains,
		Policies:        policies,
		FollowerDomains: followerDomains,
	}

	entries := handler.Process(events)
	if len(entries) == 0 {
		return stream.HandlerResult{}
	}

	mgr := notification.NewManager(s.DataDir, discoveryDomain)
	added, err := mgr.Append(entries)
	if err != nil {
		return stream.HandlerResult{Error: err}
	}

	// Prune old notifications
	pruneCfg := notification.DefaultPruneConfig()
	if config.MaxItems > 0 {
		pruneCfg.MaxItems = config.MaxItems
	}
	if config.MaxAgeDays > 0 {
		pruneCfg.MaxAgeDays = config.MaxAgeDays
	}
	if _, err := mgr.Prune(pruneCfg); err != nil {
		s.LogWarn("notification prune failed: %v", err)
	}

	return stream.HandlerResult{NewItems: added}
}

// --- Feed Sync Handler ---

type feedSyncHandler struct {
	server *Server
}

func (h *feedSyncHandler) Name() string { return "feed" }

func (h *feedSyncHandler) EventTypes() []string {
	return []string{
		"pub.polis.post.published",
		"pub.polis.post.republished",
		"pub.polis.comment.published",
		"pub.polis.comment.republished",
	}
}

func (h *feedSyncHandler) Process(events []discovery.StreamEvent) stream.HandlerResult {
	s := h.server
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		return stream.HandlerResult{}
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return stream.HandlerResult{}
	}

	discoveryDomain := s.GetDiscoveryDomain()

	// Load followed domains for feed filtering
	followingPath := following.DefaultPath(s.DataDir)
	f, err := following.Load(followingPath)
	if err != nil || f.Count() == 0 {
		return stream.HandlerResult{}
	}

	followedDomains := make(map[string]bool)
	for _, entry := range f.All() {
		d := discovery.ExtractDomainFromURL(entry.URL)
		if d != "" {
			followedDomains[d] = true
		}
	}

	handler := &feed.FeedHandler{
		MyDomain:        myDomain,
		FollowedDomains: followedDomains,
	}

	items := handler.Process(events)
	if len(items) == 0 {
		return stream.HandlerResult{}
	}

	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)
	newCount, err := cm.MergeItems(items)
	if err != nil {
		return stream.HandlerResult{Error: err}
	}

	return stream.HandlerResult{NewItems: newCount}
}

// --- Follow Sync Handler ---

type followSyncHandler struct {
	server *Server
}

func (h *followSyncHandler) Name() string { return "followers" }

func (h *followSyncHandler) EventTypes() []string {
	return []string{"pub.polis.follow.announced", "pub.polis.follow.removed"}
}

func (h *followSyncHandler) Process(events []discovery.StreamEvent) stream.HandlerResult {
	s := h.server
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		return stream.HandlerResult{}
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return stream.HandlerResult{}
	}

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	handler := &stream.FollowHandler{MyDomain: myDomain}

	state := handler.NewState()
	_ = store.LoadState(handler.TypePrefix(), state)

	newState, err := handler.Process(events, state)
	if err != nil {
		return stream.HandlerResult{Error: err}
	}

	_ = store.SaveState(handler.TypePrefix(), newState)

	// Check if follower count changed
	oldFs := state.(*stream.FollowerState)
	newFs := newState.(*stream.FollowerState)
	changed := oldFs.Count != newFs.Count

	return stream.HandlerResult{FilesChanged: changed}
}

// --- Blessing Sync Handler ---

type blessingSyncHandler struct {
	server *Server
}

func (h *blessingSyncHandler) Name() string { return "blessings" }

func (h *blessingSyncHandler) EventTypes() []string {
	return (&stream.BlessingHandler{}).EventTypes()
}

func (h *blessingSyncHandler) Process(events []discovery.StreamEvent) stream.HandlerResult {
	s := h.server
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		return stream.HandlerResult{}
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return stream.HandlerResult{}
	}

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	handler := &stream.BlessingHandler{MyDomain: myDomain}

	state := handler.NewState()
	_ = store.LoadState(handler.TypePrefix(), state)

	newState, err := handler.Process(events, state)
	if err != nil {
		return stream.HandlerResult{Error: err}
	}

	_ = store.SaveState(handler.TypePrefix(), newState)

	// Policy-driven auto-decisions for blessing requests
	privPath, pubPath := policy.DefaultPaths(s.DataDir)
	policies, _ := policy.LoadPolicies(privPath, pubPath)

	if len(policies) > 0 && s.PrivateKey != nil {
		// Build eval context
		followedDomains := make(map[string]bool)
		followingPath := following.DefaultPath(s.DataDir)
		if fl, err := following.Load(followingPath); err == nil {
			for _, entry := range fl.All() {
				if d := discovery.ExtractDomainFromURL(entry.URL); d != "" {
					followedDomains[d] = true
				}
			}
		}
		var followerState stream.FollowerState
		_ = store.LoadState("pub.polis.follow", &followerState)
		followerDomains := make(map[string]bool, len(followerState.Followers))
		for _, f := range followerState.Followers {
			followerDomains[f] = true
		}
		ctx := policy.EvalContext{
			FollowingDomains: followedDomains,
			FollowerDomains:  followerDomains,
		}

		client := discovery.NewAuthenticatedClient(s.DiscoveryURL, s.DiscoveryKey, myDomain, s.PrivateKey)
		var hc *hooks.HookConfig
		if s.Config != nil {
			hc = s.Config.Hooks
		}

		for _, evt := range events {
			if evt.Type != "pub.polis.comment.blessing.requested" {
				continue
			}
			targetDomain, _ := evt.Payload["target_domain"].(string)
			if targetDomain != myDomain {
				continue
			}

			commentURL := firstNonEmptyString(evt.Payload, "comment_url", "source_url")
			inReplyTo := firstNonEmptyString(evt.Payload, "in_reply_to", "target_url")
			if commentURL == "" || inReplyTo == "" {
				continue
			}

			pEvt := policy.Event{
				Type:         evt.Type,
				ActorDomain:  evt.Actor,
				TargetDomain: targetDomain,
				TargetPath:   inReplyTo,
			}

			decision, explicit := policy.EvaluateExplicit(policies, pEvt, ctx)
			if !explicit {
				continue // no match -> manual review
			}

			if decision == policy.Allow {
				// Auto-grant
				commentVersion, _ := evt.Payload["comment_version"].(string)
				_, err := blessing.GrantByVersion(s.DataDir, commentVersion, commentURL, inReplyTo, client, hc, s.PrivateKey)
				if err != nil {
					s.LogWarn("policy auto-grant failed for %s: %v", commentURL, err)
				} else {
					s.LogEvent("pub.polis.comment.blessing.auto_grant", map[string]interface{}{
					"comment_url": commentURL,
					"actor":       evt.Actor,
				})
				}
			} else {
				// Auto-deny
				_, err := blessing.Deny(commentURL, inReplyTo, client, s.PrivateKey)
				if err != nil {
					s.LogWarn("policy auto-deny failed for %s: %v", commentURL, err)
				} else {
					s.LogEvent("pub.polis.comment.blessing.auto_deny", map[string]interface{}{
					"comment_url": commentURL,
					"actor":       evt.Actor,
				})
				}
			}
		}
	}

	// For granted blessings targeting our domain, fetch and store the comment
	// so it appears on our post pages (same as handleBlessingGrant does manually).
	filesChanged := false
	for _, evt := range events {
		if evt.Type != "pub.polis.comment.blessing.granted" {
			continue
		}
		targetDomain, _ := evt.Payload["target_domain"].(string)
		if targetDomain != myDomain {
			continue
		}

		// comment_url (current DS) with source_url fallback (legacy DS)
		commentURL := firstNonEmptyString(evt.Payload, "comment_url", "source_url")
		inReplyTo := firstNonEmptyString(evt.Payload, "in_reply_to", "target_url")
		if commentURL == "" || inReplyTo == "" {
			continue
		}

		// Extract relative path, e.g. "comments/20260222/id.md"
		commentRelPath := extractCommentRelPath(commentURL)
		if commentRelPath == "" {
			continue
		}

		// Skip if already stored locally
		localPath := filepath.Join(s.DataDir, commentRelPath)
		if _, err := os.Stat(localPath); err == nil {
			continue
		}

		// Fetch comment markdown from the commenter's site
		rc := remote.NewClient()
		content, err := rc.FetchContent(polisurl.NormalizeToMD(commentURL))
		if err != nil {
			s.LogWarn("blessing sync: failed to fetch comment %s: %v", commentURL, err)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			s.LogWarn("blessing sync: failed to create dir for %s: %v", localPath, err)
			continue
		}
		if err := os.WriteFile(localPath, []byte(content), 0644); err != nil {
			s.LogWarn("blessing sync: failed to write %s: %v", localPath, err)
			continue
		}

		// Update blessed-comments.json index
		postPath := extractPostPathFromURL(inReplyTo)
		if err := metadata.AddBlessedComment(s.DataDir, postPath, metadata.BlessedComment{URL: commentURL}); err != nil {
			s.LogWarn("blessing sync: failed to update blessed-comments for %s: %v", commentURL, err)
		}

		filesChanged = true
		s.LogEvent("pub.polis.comment.blessing.store", map[string]interface{}{
			"comment_url": commentURL,
		})
	}

	return stream.HandlerResult{FilesChanged: filesChanged}
}

// firstNonEmptyString returns the first non-empty string value from payload
// for the given keys, or empty string if none found.
func firstNonEmptyString(payload map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := payload[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// extractPostPathFromURL extracts the relative post path from a full URL.
// e.g., "https://alice.polis.site/posts/20260127/hello.md" -> "posts/20260127/hello.md"
func extractPostPathFromURL(u string) string {
	idx := strings.Index(u, "/posts/")
	if idx >= 0 {
		return u[idx+1:] // "posts/..."
	}
	return u
}

// --- Comment Status Sync Handler ---

type commentStatusSyncHandler struct {
	server *Server
}

func (h *commentStatusSyncHandler) Name() string { return "comment-status" }

func (h *commentStatusSyncHandler) EventTypes() []string {
	return []string{"pub.polis.comment.blessing.granted", "pub.polis.comment.blessing.denied"}
}

func (h *commentStatusSyncHandler) Process(events []discovery.StreamEvent) stream.HandlerResult {
	s := h.server
	baseURL := s.GetBaseURL()
	if baseURL == "" || s.PrivateKey == nil {
		return stream.HandlerResult{}
	}

	var hc *hooks.HookConfig
	if s.Config != nil {
		hc = s.Config.Hooks
	}

	result, err := comment.SyncFromEvents(s.DataDir, baseURL, events, hc)
	if err != nil {
		return stream.HandlerResult{Error: err}
	}

	filesChanged := len(result.Blessed) > 0 || len(result.Denied) > 0
	if filesChanged {
		s.LogEvent("pub.polis.comment.sync_auto", map[string]interface{}{
			"blessed": len(result.Blessed),
			"denied":  len(result.Denied),
		})
	}

	return stream.HandlerResult{
		FilesChanged: filesChanged,
		NewItems:     len(result.Blessed) + len(result.Denied),
	}
}
